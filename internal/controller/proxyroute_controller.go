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

	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxyv1alpha1 "github.com/your-org/singbox-operator/api/v1alpha1"
)

// ProxyRouteReconciler reconciles a ProxyRoute object
// +kubebuilder:rbac:groups=proxy.proxy.io,resources=proxyroutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxy.proxy.io,resources=proxyroutes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=proxy.proxy.io,resources=proxyroutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=proxy.proxy.io,resources=proxynodes,verbs=get;list;watch;update;patch
type ProxyRouteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ProxyRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	route := &proxyv1alpha1.ProxyRoute{}
	if err := r.Get(ctx, req.NamespacedName, route); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Validate that inboundNode exists
	inboundNode := &proxyv1alpha1.ProxyNode{}
	if err := r.Get(ctx, types.NamespacedName{Name: route.Spec.InboundNode, Namespace: route.Namespace}, inboundNode); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("InboundNode not found, setting Degraded", "inboundNode", route.Spec.InboundNode)
			return r.setDegradedRoute(ctx, route, "InboundNodeNotFound",
				fmt.Sprintf("inboundNode %q not found", route.Spec.InboundNode))
		}
		return ctrl.Result{}, err
	}

	// Validate that outboundNode exists
	outboundNode := &proxyv1alpha1.ProxyNode{}
	if err := r.Get(ctx, types.NamespacedName{Name: route.Spec.OutboundNode, Namespace: route.Namespace}, outboundNode); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("OutboundNode not found, setting Degraded", "outboundNode", route.Spec.OutboundNode)
			return r.setDegradedRoute(ctx, route, "OutboundNodeNotFound",
				fmt.Sprintf("outboundNode %q not found", route.Spec.OutboundNode))
		}
		return ctrl.Result{}, err
	}

	// Trigger inbound node reconcile (it will pick up this route in collectInput)
	if err := r.triggerNodeReconcile(ctx, inboundNode); err != nil {
		logger.Error(err, "Failed to trigger inbound node reconcile")
		return ctrl.Result{}, err
	}

	return r.updateRouteStatus(ctx, route, inboundNode.Name, outboundNode.Name)
}

// triggerNodeReconcile triggers a ProxyNode reconcile by updating a trigger annotation
func (r *ProxyRouteReconciler) triggerNodeReconcile(ctx context.Context, node *proxyv1alpha1.ProxyNode) error {
	latest := &proxyv1alpha1.ProxyNode{}
	if err := r.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: node.Namespace}, latest); err != nil {
		return err
	}
	if latest.Annotations == nil {
		latest.Annotations = make(map[string]string)
	}
	latest.Annotations["proxy.io/reconcile-trigger"] = metav1.Now().UTC().Format("20060102T150405Z")
	return r.Update(ctx, latest)
}

// setDegradedRoute sets the ProxyRoute status to Degraded
func (r *ProxyRouteReconciler) setDegradedRoute(ctx context.Context, route *proxyv1alpha1.ProxyRoute, reason, message string) (ctrl.Result, error) {
	latest := &proxyv1alpha1.ProxyRoute{}
	if err := r.Get(ctx, types.NamespacedName{Name: route.Name, Namespace: route.Namespace}, latest); err != nil {
		return ctrl.Result{}, err
	}
	apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
		Type:               "Degraded",
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: latest.Generation,
	})
	apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: latest.Generation,
	})
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// updateRouteStatus updates the ProxyRoute status with resolved node names
func (r *ProxyRouteReconciler) updateRouteStatus(ctx context.Context, route *proxyv1alpha1.ProxyRoute, resolvedInbound, resolvedOutbound string) (ctrl.Result, error) {
	latest := &proxyv1alpha1.ProxyRoute{}
	if err := r.Get(ctx, types.NamespacedName{Name: route.Name, Namespace: route.Namespace}, latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.ResolvedInboundNode = resolvedInbound
	latest.Status.ResolvedOutboundNode = resolvedOutbound
	latest.Status.ObservedGeneration = latest.Generation
	apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            fmt.Sprintf("Route from %s to %s resolved", resolvedInbound, resolvedOutbound),
		ObservedGeneration: latest.Generation,
	})
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProxyRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.ProxyRoute{}).
		Named("proxyroute").
		Complete(r)
}
