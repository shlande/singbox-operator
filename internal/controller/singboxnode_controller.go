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
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
	"github.com/shlande/singbox-operator/internal/credmanager"
	"github.com/shlande/singbox-operator/internal/metrics"
)

const (
	singboxNodeFinalizer = "singboxoperator.shlande.top/singboxnode-finalizer"
	configMapSuffix      = "-config"
	deploymentSuffix     = "-deploy"
	configHashAnnotation = "singboxoperator.shlande.top/config-hash"
	singboxImage         = "ghcr.io/sagernet/sing-box:latest"
	relayContainerPort   = int32(10808)
)

// SingBoxNodeReconciler reconciles a SingBoxNode object
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=singboxnodes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=singboxnodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=singboxnodes/finalizers,verbs=update
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=customroutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
type SingBoxNodeReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	DefaultTLSSecret string
}

func (r *SingBoxNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	start := time.Now()

	var reconcileErr error
	defer func() {
		result := "success"
		if reconcileErr != nil {
			result = "error"
			metrics.ReconcileErrorsTotal.WithLabelValues("singboxnode", "reconcile_error").Inc()
		}
		metrics.ReconcileDurationSeconds.WithLabelValues("singboxnode", result).Observe(time.Since(start).Seconds())
	}()

	node := &proxyv1alpha1.SingBoxNode{}
	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		reconcileErr = err
		return ctrl.Result{}, err
	}

	if !node.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, node)
	}

	if !controllerutil.ContainsFinalizer(node, singboxNodeFinalizer) {
		controllerutil.AddFinalizer(node, singboxNodeFinalizer)
		if err := r.Update(ctx, node); err != nil {
			reconcileErr = err
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if err := r.ensureCredential(ctx, node); err != nil {
		logger.Error(err, "Failed to ensure node credential")
		reconcileErr = err
		return ctrl.Result{}, err
	}

	input, err := r.collectInput(ctx, node)
	if err != nil {
		logger.Error(err, "Failed to collect input")
		reconcileErr = err
		return ctrl.Result{}, err
	}

	output, err := configengine.Compute(input)
	if err != nil {
		logger.Error(err, "Failed to compute config")
		reconcileErr = err
		return r.setDegraded(ctx, node, "ConfigComputeFailed", err.Error())
	}

	if err := r.reconcileConfigMap(ctx, node, output); err != nil {
		logger.Error(err, "Failed to reconcile ConfigMap")
		reconcileErr = err
		return ctrl.Result{}, err
	}

	if err := r.reconcileDeployment(ctx, node, output.Hash); err != nil {
		logger.Error(err, "Failed to reconcile Deployment")
		reconcileErr = err
		return ctrl.Result{}, err
	}

	if requeue, err := r.reconcileServices(ctx, node); err != nil {
		logger.Error(err, "Failed to reconcile Services")
		reconcileErr = err
		return ctrl.Result{}, err
	} else if requeue {
		return ctrl.Result{Requeue: true}, nil
	}

	return r.updateStatus(ctx, node, output.Hash)
}

func (r *SingBoxNodeReconciler) handleDeletion(ctx context.Context, node *proxyv1alpha1.SingBoxNode) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(node, singboxNodeFinalizer) {
		controllerutil.RemoveFinalizer(node, singboxNodeFinalizer)
		if err := r.Update(ctx, node); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *SingBoxNodeReconciler) ensureCredential(ctx context.Context, node *proxyv1alpha1.SingBoxNode) error {
	if hasRole(node, proxyv1alpha1.ProxyRoleOutbound) {
		_, err := credmanager.EnsureNodeCredential(ctx, r.Client, node)
		return err
	}
	return nil
}

func (r *SingBoxNodeReconciler) collectInput(ctx context.Context, node *proxyv1alpha1.SingBoxNode) (configengine.Input, error) {
	input := configengine.Input{
		Node:                node,
		UserCreds:           make(map[string]configengine.UserCredential),
		NodeCreds:           make(map[string]configengine.NodeCredential),
		OutboundNodesByName: make(map[string]*proxyv1alpha1.SingBoxNode),
	}

	allNodes := &proxyv1alpha1.SingBoxNodeList{}
	if err := r.List(ctx, allNodes, client.InNamespace(node.Namespace)); err != nil {
		return input, fmt.Errorf("listing SingBoxNodes: %w", err)
	}
	sort.Slice(allNodes.Items, func(i, j int) bool {
		return allNodes.Items[i].Name < allNodes.Items[j].Name
	})
	for i := range allNodes.Items {
		other := &allNodes.Items[i]
		if other.Name == node.Name {
			continue
		}
		if other.Spec.Region == node.Spec.Region && hasRole(other, proxyv1alpha1.ProxyRoleOutbound) {
			input.OutboundNodes = append(input.OutboundNodes, other)
			input.OutboundNodesByName[other.Name] = other
			cred, err := credmanager.GetNodeCredential(ctx, r.Client, other.Name, node.Namespace)
			if err == nil {
				input.NodeCreds[other.Name] = configengine.NodeCredential{
					Username: cred.Username,
					Password: cred.Password,
				}
			}
		}
	}

	if hasRole(node, proxyv1alpha1.ProxyRoleInbound) {
		allUsers := &proxyv1alpha1.UserList{}
		if err := r.List(ctx, allUsers, client.InNamespace(node.Namespace)); err != nil {
			return input, fmt.Errorf("listing Users: %w", err)
		}
		sort.Slice(allUsers.Items, func(i, j int) bool {
			return allUsers.Items[i].Name < allUsers.Items[j].Name
		})
		for i := range allUsers.Items {
			user := &allUsers.Items[i]
			if nodeSupportsProtocol(node, user.Spec.Protocol) {
				input.Users = append(input.Users, user)
				cred, err := credmanager.GetUserCredential(ctx, r.Client, user)
				if err == nil {
					input.UserCreds[user.Name] = configengine.UserCredential{
						UUID: cred.UUID,
					}
				}
			}
		}
	}

	allRoutes := &proxyv1alpha1.CustomRouteList{}
	if err := r.List(ctx, allRoutes, client.InNamespace(node.Namespace)); err != nil {
		return input, fmt.Errorf("listing CustomRoutes: %w", err)
	}
	for i := range allRoutes.Items {
		route := &allRoutes.Items[i]
		if route.Spec.InboundNode == node.Name {
			input.Routes = append(input.Routes, route)
			outboundNode := &proxyv1alpha1.SingBoxNode{}
			if err := r.Get(ctx, types.NamespacedName{Name: route.Spec.OutboundNode, Namespace: node.Namespace}, outboundNode); err == nil {
				input.OutboundNodesByName[outboundNode.Name] = outboundNode
				cred, err := credmanager.GetNodeCredential(ctx, r.Client, outboundNode.Name, node.Namespace)
				if err == nil {
					input.NodeCreds[outboundNode.Name] = configengine.NodeCredential{
						Username: cred.Username,
						Password: cred.Password,
					}
				}
			}
		}
	}

	if hasRole(node, proxyv1alpha1.ProxyRoleOutbound) {
		cred, err := credmanager.GetNodeCredential(ctx, r.Client, node.Name, node.Namespace)
		if err == nil {
			input.NodeCreds[node.Name] = configengine.NodeCredential{
				Username: cred.Username,
				Password: cred.Password,
			}
		}
	}

	return input, nil
}

func (r *SingBoxNodeReconciler) reconcileConfigMap(ctx context.Context, node *proxyv1alpha1.SingBoxNode, output configengine.Output) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.Name + configMapSuffix,
			Namespace: node.Namespace,
		},
	}
	var op controllerutil.OperationResult
	var err error
	if node.Annotations != nil && node.Annotations[configHashAnnotation] != output.Hash {
		metrics.ConfigUpdatesTotal.WithLabelValues(node.Spec.Region, "config_change").Inc()
	}
	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if cm.Annotations == nil {
			cm.Annotations = make(map[string]string)
		}
		cm.Annotations[configHashAnnotation] = output.Hash
		cm.Data = map[string]string{
			"config.json": string(output.Config),
		}
		return controllerutil.SetControllerReference(node, cm, r.Scheme)
	})
	if err != nil {
		return err
	}
	if op == controllerutil.OperationResultUpdated {
		metrics.ConfigUpdatesTotal.WithLabelValues(node.Spec.Region, "config_update").Inc()
	}
	return nil
}

func (r *SingBoxNodeReconciler) reconcileDeployment(ctx context.Context, node *proxyv1alpha1.SingBoxNode, configHash string) error {
	cmName := node.Name + configMapSuffix

	volumeMounts := []corev1.VolumeMount{
		{Name: "config", MountPath: "/etc/sing-box"},
	}
	volumes := []corev1.Volume{
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				},
			},
		},
	}
	if needsTLS(node) {
		tlsSecret := node.Spec.TLSSecretName
		if tlsSecret == "" {
			tlsSecret = r.DefaultTLSSecret
		}
		if tlsSecret != "" {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "tls",
				MountPath: "/etc/sing-box/tls",
				ReadOnly:  true,
			})
			volumes = append(volumes, corev1.Volume{
				Name: "tls",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: tlsSecret,
						Items: []corev1.KeyToPath{
							{Key: "tls.crt", Path: "tls.crt"},
							{Key: "tls.key", Path: "tls.key"},
						},
					},
				},
			})
		}
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.Name + deploymentSuffix,
			Namespace: node.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		replicas := int32(1)
		deploy.Spec = appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":         "singbox",
					"singboxnode": node.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":         "singbox",
						"singboxnode": node.Name,
					},
					Annotations: map[string]string{
						configHashAnnotation: configHash,
					},
				},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{
						"kubernetes.io/hostname": node.Spec.NodeRef,
					},
					Containers: []corev1.Container{
						{
							Name:         "singbox",
							Image:        singboxImage,
							Args:         []string{"run", "-c", "/etc/sing-box/config.json"},
							VolumeMounts: volumeMounts,
							Ports:        buildHostPorts(node),
						},
					},
					Volumes: volumes,
				},
			},
		}
		return controllerutil.SetControllerReference(node, deploy, r.Scheme)
	})
	return err
}

func (r *SingBoxNodeReconciler) reconcileServices(ctx context.Context, node *proxyv1alpha1.SingBoxNode) (requeue bool, err error) {
	if err = r.deleteOrphanServices(ctx, node); err != nil {
		return false, fmt.Errorf("deleting orphan services: %w", err)
	}
	return false, nil
}

func (r *SingBoxNodeReconciler) deleteOrphanServices(ctx context.Context, node *proxyv1alpha1.SingBoxNode) error {
	svcList := &corev1.ServiceList{}
	if err := r.List(ctx, svcList, client.InNamespace(node.Namespace)); err != nil {
		return err
	}
	for i := range svcList.Items {
		svc := &svcList.Items[i]
		for _, ref := range svc.OwnerReferences {
			if ref.Kind == "SingBoxNode" && ref.Name == node.Name {
				if err := r.Delete(ctx, svc); err != nil && !errors.IsNotFound(err) {
					return fmt.Errorf("deleting orphan service %s: %w", svc.Name, err)
				}
				break
			}
		}
	}
	return nil
}

func (r *SingBoxNodeReconciler) updateStatus(ctx context.Context, node *proxyv1alpha1.SingBoxNode, configHash string) (ctrl.Result, error) {
	latest := &proxyv1alpha1.SingBoxNode{}
	if err := r.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: node.Namespace}, latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.Phase = "Running"
	latest.Status.ConfigHash = configHash
	latest.Status.ObservedGeneration = latest.Generation

	var endpoints []string
	for _, proto := range latest.Spec.SupportedProtocols {
		endpoints = append(endpoints, fmt.Sprintf("%s:%s:%d", proto.Protocol, latest.Spec.Address, proto.Port))
	}
	latest.Status.EntryEndpoints = endpoints
	latest.Status.TLSServerName = r.resolveTLSServerName(ctx, latest)

	apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            "SingBoxNode reconciled successfully",
		ObservedGeneration: latest.Generation,
	})

	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}

	for _, role := range latest.Spec.Roles {
		metrics.ProxyNodesTotal.WithLabelValues(latest.Spec.Region, string(role), "Running").Set(1)
	}

	return ctrl.Result{}, nil
}

func (r *SingBoxNodeReconciler) setDegraded(ctx context.Context, node *proxyv1alpha1.SingBoxNode, reason, message string) (ctrl.Result, error) {
	latest := &proxyv1alpha1.SingBoxNode{}
	if err := r.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: node.Namespace}, latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.Phase = "Failed"
	apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
		Type:               "Degraded",
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: latest.Generation,
	})
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, fmt.Errorf("%s: %s", reason, message)
}

func (r *SingBoxNodeReconciler) sameRegionNodeMapper(ctx context.Context, obj client.Object) []reconcile.Request {
	changedNode, ok := obj.(*proxyv1alpha1.SingBoxNode)
	if !ok {
		return nil
	}
	allNodes := &proxyv1alpha1.SingBoxNodeList{}
	if err := r.List(ctx, allNodes, client.InNamespace(changedNode.Namespace)); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, n := range allNodes.Items {
		if n.Name != changedNode.Name && n.Spec.Region == changedNode.Spec.Region {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: n.Name, Namespace: n.Namespace},
			})
		}
	}
	return requests
}

func (r *SingBoxNodeReconciler) matchingProtocolNodeMapper(ctx context.Context, obj client.Object) []reconcile.Request {
	user, ok := obj.(*proxyv1alpha1.User)
	if !ok {
		return nil
	}
	allNodes := &proxyv1alpha1.SingBoxNodeList{}
	if err := r.List(ctx, allNodes, client.InNamespace(user.Namespace)); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, n := range allNodes.Items {
		if hasRole(&n, proxyv1alpha1.ProxyRoleInbound) && nodeSupportsProtocol(&n, user.Spec.Protocol) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: n.Name, Namespace: n.Namespace},
			})
		}
	}
	return requests
}

func (r *SingBoxNodeReconciler) affectedByRouteMapper(ctx context.Context, obj client.Object) []reconcile.Request {
	route, ok := obj.(*proxyv1alpha1.CustomRoute)
	if !ok {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: route.Spec.InboundNode, Namespace: route.Namespace}},
	}
}

func (r *SingBoxNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.SingBoxNode{}).
		Named("singboxnode").
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Watches(&proxyv1alpha1.SingBoxNode{},
			handler.EnqueueRequestsFromMapFunc(r.sameRegionNodeMapper),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&proxyv1alpha1.User{},
			handler.EnqueueRequestsFromMapFunc(r.matchingProtocolNodeMapper)).
		Watches(&proxyv1alpha1.CustomRoute{},
			handler.EnqueueRequestsFromMapFunc(r.affectedByRouteMapper)).
		Complete(r)
}

func buildHostPorts(node *proxyv1alpha1.SingBoxNode) []corev1.ContainerPort {
	var ports []corev1.ContainerPort
	for _, proto := range node.Spec.SupportedProtocols {
		for _, netProto := range hostPortProtocols(proto.Protocol) {
			ports = append(ports, corev1.ContainerPort{
				Name:          proto.Protocol + "-" + strings.ToLower(string(netProto)),
				ContainerPort: proto.Port,
				HostPort:      proto.Port,
				Protocol:      netProto,
			})
		}
	}
	if node.Spec.RelayPort > 0 {
		ports = append(ports,
			corev1.ContainerPort{Name: "relay-tcp", ContainerPort: relayContainerPort, HostPort: node.Spec.RelayPort, Protocol: corev1.ProtocolTCP},
			corev1.ContainerPort{Name: "relay-udp", ContainerPort: relayContainerPort, HostPort: node.Spec.RelayPort, Protocol: corev1.ProtocolUDP},
		)
	}
	return ports
}

func hostPortProtocols(protocol string) []corev1.Protocol {
	switch protocol {
	case "hysteria2":
		return []corev1.Protocol{corev1.ProtocolUDP}
	case "socks5":
		return []corev1.Protocol{corev1.ProtocolTCP, corev1.ProtocolUDP}
	default:
		return []corev1.Protocol{corev1.ProtocolTCP}
	}
}

func hasRole(node *proxyv1alpha1.SingBoxNode, role proxyv1alpha1.ProxyRole) bool {
	for _, r := range node.Spec.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func nodeSupportsProtocol(node *proxyv1alpha1.SingBoxNode, protocol string) bool {
	for _, p := range node.Spec.SupportedProtocols {
		if p.Protocol == protocol {
			return true
		}
	}
	return false
}

func needsTLS(node *proxyv1alpha1.SingBoxNode) bool {
	for _, p := range node.Spec.SupportedProtocols {
		if p.Protocol == "hysteria2" {
			return true
		}
	}
	return false
}

func (r *SingBoxNodeReconciler) resolveTLSServerName(ctx context.Context, node *proxyv1alpha1.SingBoxNode) string {
	if !needsTLS(node) {
		return ""
	}
	secretName := node.Spec.TLSSecretName
	if secretName == "" {
		secretName = r.DefaultTLSSecret
	}
	if secretName == "" {
		return ""
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: node.Namespace}, secret); err != nil {
		return ""
	}
	certPEM := secret.Data["tls.crt"]
	if len(certPEM) == 0 {
		return ""
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	for _, san := range cert.DNSNames {
		if san != "" {
			return san
		}
	}
	return ""
}
