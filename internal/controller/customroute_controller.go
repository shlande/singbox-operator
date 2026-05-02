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
	"github.com/shlande/singbox-operator/internal/metrics"
)

// CustomRouteReconciler reconciles a CustomRoute object
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=customroutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=customroutes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=customroutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=singboxnodes,verbs=get;list;watch;update;patch
type CustomRouteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *CustomRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	start := time.Now()

	var reconcileErr error
	defer func() {
		result := "success"
		if reconcileErr != nil {
			result = "error"
			metrics.ReconcileErrorsTotal.WithLabelValues("customroute", "reconcile_error").Inc()
		}
		metrics.ReconcileDurationSeconds.WithLabelValues("customroute", result).Observe(time.Since(start).Seconds())
	}()

	route := &proxyv1alpha1.CustomRoute{}
	if err := r.Get(ctx, req.NamespacedName, route); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		reconcileErr = err
		return ctrl.Result{}, err
	}

	inboundNode := &proxyv1alpha1.SingBoxNode{}
	if err := r.Get(ctx, types.NamespacedName{Name: route.Spec.InboundNode, Namespace: route.Namespace}, inboundNode); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("InboundNode not found, setting Degraded", "inboundNode", route.Spec.InboundNode)
			reconcileErr = fmt.Errorf("inbound node not found")
			return r.setDegradedRoute(ctx, route, "InboundNodeNotFound",
				fmt.Sprintf("inboundNode %q not found", route.Spec.InboundNode))
		}
		reconcileErr = err
		return ctrl.Result{}, err
	}

	outboundNode := &proxyv1alpha1.SingBoxNode{}
	if err := r.Get(ctx, types.NamespacedName{Name: route.Spec.OutboundNode, Namespace: route.Namespace}, outboundNode); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("OutboundNode not found, setting Degraded", "outboundNode", route.Spec.OutboundNode)
			reconcileErr = fmt.Errorf("outbound node not found")
			return r.setDegradedRoute(ctx, route, "OutboundNodeNotFound",
				fmt.Sprintf("outboundNode %q not found", route.Spec.OutboundNode))
		}
		reconcileErr = err
		return ctrl.Result{}, err
	}

	if err := r.triggerNodeReconcile(ctx, inboundNode); err != nil {
		logger.Error(err, "Failed to trigger inbound node reconcile")
		reconcileErr = err
		return ctrl.Result{}, err
	}

	return r.updateRouteStatus(ctx, route, inboundNode.Name, outboundNode.Name)
}

func (r *CustomRouteReconciler) triggerNodeReconcile(ctx context.Context, node *proxyv1alpha1.SingBoxNode) error {
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

func (r *CustomRouteReconciler) setDegradedRoute(ctx context.Context, route *proxyv1alpha1.CustomRoute, reason, message string) (ctrl.Result, error) {
	latest := &proxyv1alpha1.CustomRoute{}
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

func (r *CustomRouteReconciler) updateRouteStatus(ctx context.Context, route *proxyv1alpha1.CustomRoute, resolvedInbound, resolvedOutbound string) (ctrl.Result, error) {
	latest := &proxyv1alpha1.CustomRoute{}
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

func (r *CustomRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.CustomRoute{}).
		Named("customroute").
		Complete(r)
}
