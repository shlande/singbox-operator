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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
	"github.com/shlande/singbox-operator/internal/metrics"
)

// UserReconciler reconciles a User object
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=users,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=users/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=users/finalizers,verbs=update
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=singboxnodes,verbs=get;list;watch;update;patch
type UserReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *UserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	start := time.Now()

	var reconcileErr error
	defer func() {
		result := "success"
		if reconcileErr != nil {
			result = "error"
			metrics.ReconcileErrorsTotal.WithLabelValues("user", "reconcile_error").Inc()
		}
		metrics.ReconcileDurationSeconds.WithLabelValues("user", result).Observe(time.Since(start).Seconds())
	}()

	user := &proxyv1alpha1.User{}
	if err := r.Get(ctx, req.NamespacedName, user); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		reconcileErr = err
		return ctrl.Result{}, err
	}

	matchingNodes, err := r.findMatchingInboundNodes(ctx, user)
	if err != nil {
		logger.Error(err, "Failed to find matching inbound nodes")
		reconcileErr = err
		return ctrl.Result{}, err
	}

	for i := range matchingNodes {
		node := &matchingNodes[i]
		if err := r.triggerNodeReconcile(ctx, node); err != nil {
			logger.Error(err, "Failed to trigger node reconcile", "node", node.Name)
		}
	}

	return r.updateStatus(ctx, user, matchingNodes)
}

func (r *UserReconciler) findMatchingInboundNodes(ctx context.Context, user *proxyv1alpha1.User) ([]proxyv1alpha1.SingBoxNode, error) {
	allNodes := &proxyv1alpha1.SingBoxNodeList{}
	if err := r.List(ctx, allNodes, client.InNamespace(user.Namespace)); err != nil {
		return nil, fmt.Errorf("listing SingBoxNodes: %w", err)
	}

	var matching []proxyv1alpha1.SingBoxNode
	for _, node := range allNodes.Items {
		if hasRole(&node, proxyv1alpha1.ProxyRoleInbound) && nodeSupportsProtocol(&node, user.Spec.Protocol) {
			matching = append(matching, node)
		}
	}

	// Apply UserGroup restrictions if specified — fail-open on missing UserGroup
	if user.Spec.UserGroupRef == "" {
		return matching, nil
	}

	var ug proxyv1alpha1.UserGroup
	if err := r.Get(ctx, types.NamespacedName{Namespace: user.Namespace, Name: user.Spec.UserGroupRef}, &ug); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		// UserGroup not found — fail-open, no restrictions applied
		return matching, nil
	}

	allowedSet := make(map[string]bool, len(ug.Spec.AllowedNodes))
	for _, n := range ug.Spec.AllowedNodes {
		allowedSet[n] = true
	}
	deniedSet := make(map[string]bool, len(ug.Spec.DeniedNodes))
	for _, n := range ug.Spec.DeniedNodes {
		deniedSet[n] = true
	}

	var filtered []proxyv1alpha1.SingBoxNode
	for _, node := range matching {
		if configengine.IsNodeAllowed(node.Name, allowedSet, deniedSet) {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func (r *UserReconciler) triggerNodeReconcile(ctx context.Context, node *proxyv1alpha1.SingBoxNode) error {
	latest := &proxyv1alpha1.SingBoxNode{}
	if err := r.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: node.Namespace}, latest); err != nil {
		return err
	}
	if latest.Annotations == nil {
		latest.Annotations = make(map[string]string)
	}
	latest.Annotations["singboxoperator.shlande.top/reconcile-trigger"] = metav1.Now().UTC().Format("20060102T150405Z")
	return r.Update(ctx, latest)
}

func (r *UserReconciler) updateStatus(ctx context.Context, user *proxyv1alpha1.User, nodes []proxyv1alpha1.SingBoxNode) (ctrl.Result, error) {
	latest := &proxyv1alpha1.User{}
	if err := r.Get(ctx, types.NamespacedName{Name: user.Name, Namespace: user.Namespace}, latest); err != nil {
		return ctrl.Result{}, err
	}

	nodeNames := make([]string, 0, len(nodes))
	for _, n := range nodes {
		nodeNames = append(nodeNames, n.Name)
	}

	latest.Status.ActiveNodeCount = int32(len(nodes))
	latest.Status.ActiveNodes = nodeNames
	latest.Status.ObservedGeneration = latest.Generation

	apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            fmt.Sprintf("User active on %d nodes", len(nodes)),
		ObservedGeneration: latest.Generation,
	})

	if latest.Spec.UserGroupRef != "" {
		var ug proxyv1alpha1.UserGroup
		ugErr := r.Get(ctx, types.NamespacedName{Namespace: latest.Namespace, Name: latest.Spec.UserGroupRef}, &ug)
		if ugErr != nil {
			if client.IgnoreNotFound(ugErr) != nil {
				return ctrl.Result{}, ugErr
			}
			apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
				Type:               "UserGroupReady",
				Status:             metav1.ConditionFalse,
				Reason:             "UserGroupNotFound",
				Message:            fmt.Sprintf("UserGroup %q not found in namespace %q", latest.Spec.UserGroupRef, latest.Namespace),
				ObservedGeneration: latest.Generation,
			})
		} else {
			apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
				Type:               "UserGroupReady",
				Status:             metav1.ConditionTrue,
				Reason:             "UserGroupFound",
				Message:            "",
				ObservedGeneration: latest.Generation,
			})
		}
	}

	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}

	metrics.ProxyUsersTotal.WithLabelValues(latest.Spec.Protocol).Set(1)

	return ctrl.Result{}, nil
}

func (r *UserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.User{}).
		Named("user").
		Complete(r)
}
