/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/metrics"
)

// EgressPolicyReconciler validates EgressPolicy selectors and propagates policy
// changes to every affected ingress SingBoxNode.
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=egresspolicies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=egresspolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=singboxnodes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=externaloutbounds,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
type EgressPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *EgressPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()
	var reconcileErr error
	defer func() {
		result := "success"
		if reconcileErr != nil {
			result = "error"
			metrics.ReconcileErrorsTotal.WithLabelValues("egresspolicy", "reconcile_error").Inc()
		}
		metrics.ReconcileDurationSeconds.WithLabelValues("egresspolicy", result).Observe(time.Since(start).Seconds())
	}()

	policy := &proxyv1alpha1.EgressPolicy{}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		reconcileErr = err
		return ctrl.Result{}, err
	}

	nodes := &proxyv1alpha1.SingBoxNodeList{}
	if err := r.List(ctx, nodes, client.InNamespace(policy.Namespace)); err != nil {
		reconcileErr = err
		return ctrl.Result{}, err
	}
	externalOutbounds := &proxyv1alpha1.ExternalOutboundList{}
	if err := r.List(ctx, externalOutbounds, client.InNamespace(policy.Namespace)); err != nil {
		reconcileErr = err
		return ctrl.Result{}, err
	}

	var ingressNames []string
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if hasRole(node, proxyv1alpha1.ProxyRoleInbound) && policy.Spec.IngressSelector.Matches(node) {
			ingressNames = append(ingressNames, node.Name)
		}
	}
	sort.Strings(ingressNames)

	var egressNames []string
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if hasRole(node, proxyv1alpha1.ProxyRoleOutbound) && policy.Spec.EgressSelector.Matches(node) {
			egressNames = append(egressNames, node.Name)
		}
	}
	for i := range externalOutbounds.Items {
		egress := &externalOutbounds.Items[i]
		if policy.Spec.EgressSelector.Matches(egress) {
			egressNames = append(egressNames, egress.Name)
		}
	}
	sort.Strings(egressNames)

	degradedReason, degradedMessage := validatePolicyResolution(policy, ingressNames, egressNames)
	if err := r.updateStatus(ctx, policy, ingressNames, egressNames, degradedReason, degradedMessage); err != nil {
		reconcileErr = err
		return ctrl.Result{}, err
	}

	for _, ingressName := range ingressNames {
		if err := r.triggerNodeReconcile(ctx, policy.Namespace, ingressName); err != nil {
			reconcileErr = err
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func validatePolicyResolution(policy *proxyv1alpha1.EgressPolicy, ingressNames, egressNames []string) (string, string) {
	if err := policy.Spec.IngressSelector.Valid(); err != nil {
		return "InvalidIngressSelector", err.Error()
	}
	if err := policy.Spec.EgressSelector.Valid(); err != nil {
		return "InvalidEgressSelector", err.Error()
	}
	if err := policy.Spec.Match.Valid(policy.Spec.FallbackAction != ""); err != nil {
		return "InvalidMatch", err.Error()
	}
	if policy.Spec.Action != proxyv1alpha1.EgressPolicyActionRoute && policy.Spec.Action != proxyv1alpha1.EgressPolicyActionReject {
		return "InvalidAction", fmt.Sprintf("unsupported action %q", policy.Spec.Action)
	}
	if len(ingressNames) == 0 {
		return "IngressNotFound", "ingressSelector matched no inbound SingBoxNode"
	}
	if policy.Spec.FallbackAction != "" && policy.Spec.FallbackAction != proxyv1alpha1.EgressPolicyActionReject {
		return "InvalidFallbackAction", fmt.Sprintf("unsupported fallbackAction %q", policy.Spec.FallbackAction)
	}
	if policy.Spec.Action == proxyv1alpha1.EgressPolicyActionRoute && len(egressNames) != 1 {
		return "EgressNotUnique", fmt.Sprintf("egressSelector matched %d egress targets, expected exactly one", len(egressNames))
	}
	return "", ""
}

func (r *EgressPolicyReconciler) updateStatus(ctx context.Context, policy *proxyv1alpha1.EgressPolicy, ingressNames, egressNames []string, reason, message string) error {
	latest := &proxyv1alpha1.EgressPolicy{}
	if err := r.Get(ctx, types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}, latest); err != nil {
		return err
	}
	before := latest.Status.DeepCopy()
	latest.Status.ResolvedIngresses = ingressNames
	latest.Status.ResolvedEgress = ""
	if len(egressNames) == 1 {
		latest.Status.ResolvedEgress = egressNames[0]
	}
	latest.Status.ObservedGeneration = latest.Generation
	if reason != "" {
		apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               proxyv1alpha1.EgressPolicyDegradedConditionType,
			Status:             metav1.ConditionTrue,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: latest.Generation,
		})
		apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               proxyv1alpha1.EgressPolicyReadyConditionType,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: latest.Generation,
		})
	} else {
		apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               proxyv1alpha1.EgressPolicyDegradedConditionType,
			Status:             metav1.ConditionFalse,
			Reason:             "Resolved",
			Message:            "EgressPolicy selectors resolved",
			ObservedGeneration: latest.Generation,
		})
		apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               proxyv1alpha1.EgressPolicyReadyConditionType,
			Status:             metav1.ConditionTrue,
			Reason:             "Resolved",
			Message:            "EgressPolicy resolved successfully",
			ObservedGeneration: latest.Generation,
		})
	}
	if apiequality.Semantic.DeepEqual(before, &latest.Status) {
		return nil
	}
	return r.Status().Update(ctx, latest)
}

func (r *EgressPolicyReconciler) triggerNodeReconcile(ctx context.Context, namespace, name string) error {
	node := &proxyv1alpha1.SingBoxNode{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, node); err != nil {
		return err
	}
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations["singboxoperator.shlande.top/reconcile-trigger"] = metav1.Now().UTC().Format("20060102T150405Z")
	return r.Update(ctx, node)
}

func (r *EgressPolicyReconciler) policyMapper(ctx context.Context, obj client.Object) []reconcile.Request {
	var policies proxyv1alpha1.EgressPolicyList
	if err := r.List(ctx, &policies, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(policies.Items))
	for i := range policies.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: policies.Items[i].Name, Namespace: policies.Items[i].Namespace}})
	}
	return requests
}

func (r *EgressPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.EgressPolicy{}).
		Named("egresspolicy").
		Watches(&proxyv1alpha1.SingBoxNode{}, handler.EnqueueRequestsFromMapFunc(r.policyMapper), builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.LabelChangedPredicate{}))).
		Watches(&proxyv1alpha1.ExternalOutbound{}, handler.EnqueueRequestsFromMapFunc(r.policyMapper), builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.LabelChangedPredicate{}))).
		Complete(r)
}
