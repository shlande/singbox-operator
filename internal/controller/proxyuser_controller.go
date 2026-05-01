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

// ProxyUserReconciler reconciles a ProxyUser object
// +kubebuilder:rbac:groups=proxy.proxy.io,resources=proxyusers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxy.proxy.io,resources=proxyusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=proxy.proxy.io,resources=proxyusers/finalizers,verbs=update
// +kubebuilder:rbac:groups=proxy.proxy.io,resources=proxynodes,verbs=get;list;watch;update;patch
type ProxyUserReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ProxyUserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	user := &proxyv1alpha1.ProxyUser{}
	if err := r.Get(ctx, req.NamespacedName, user); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	matchingNodes, err := r.findMatchingInboundNodes(ctx, user)
	if err != nil {
		logger.Error(err, "Failed to find matching inbound nodes")
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

func (r *ProxyUserReconciler) findMatchingInboundNodes(ctx context.Context, user *proxyv1alpha1.ProxyUser) ([]proxyv1alpha1.ProxyNode, error) {
	allNodes := &proxyv1alpha1.ProxyNodeList{}
	if err := r.List(ctx, allNodes, client.InNamespace(user.Namespace)); err != nil {
		return nil, fmt.Errorf("listing ProxyNodes: %w", err)
	}

	var matching []proxyv1alpha1.ProxyNode
	for _, node := range allNodes.Items {
		if hasRole(&node, proxyv1alpha1.ProxyRoleInbound) && nodeSupportsProtocol(&node, user.Spec.Protocol) {
			matching = append(matching, node)
		}
	}
	return matching, nil
}

func (r *ProxyUserReconciler) triggerNodeReconcile(ctx context.Context, node *proxyv1alpha1.ProxyNode) error {
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

func (r *ProxyUserReconciler) updateStatus(ctx context.Context, user *proxyv1alpha1.ProxyUser, nodes []proxyv1alpha1.ProxyNode) (ctrl.Result, error) {
	latest := &proxyv1alpha1.ProxyUser{}
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
		Message:            fmt.Sprintf("ProxyUser active on %d nodes", len(nodes)),
		ObservedGeneration: latest.Generation,
	})

	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProxyUserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.ProxyUser{}).
		Named("proxyuser").
		Complete(r)
}
