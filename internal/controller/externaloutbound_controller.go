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
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/metrics"
)

// validShadowsocksMethods is the whitelist of shadowsocks ciphers accepted in
// the credentials secret's method key.
var validShadowsocksMethods = []string{
	"aes-128-gcm",
	"aes-256-gcm",
	"chacha20-ietf-poly1305",
	"xchacha20-ietf-poly1305",
	"2022-blake3-aes-128-gcm",
	"2022-blake3-aes-256-gcm",
	"2022-blake3-chacha20-poly1305",
	"none",
}

// ExternalOutboundReconciler reconciles a ExternalOutbound object.
// It is a pure validation controller: it manages no workloads and only reports
// spec coherence and credential health via the Accepted status condition.
type ExternalOutboundReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=externaloutbounds,verbs=get;list;watch
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=externaloutbounds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=externaloutbounds/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile validates the ExternalOutbound spec and its credentials secret and
// records the verdict in the Accepted status condition. It never creates or
// deletes any workload.
func (r *ExternalOutboundReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	start := time.Now()

	var reconcileErr error
	defer func() {
		result := "success"
		if reconcileErr != nil {
			result = "error"
			metrics.ReconcileErrorsTotal.WithLabelValues("externaloutbound", "reconcile_error").Inc()
		}
		metrics.ReconcileDurationSeconds.WithLabelValues("externaloutbound", result).Observe(time.Since(start).Seconds())
	}()

	eob := &proxyv1alpha1.ExternalOutbound{}
	if err := r.Get(ctx, req.NamespacedName, eob); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		reconcileErr = err
		return ctrl.Result{}, err
	}

	violation := specViolation(eob)
	if violation == nil {
		var err error
		if violation, err = r.nameConflict(ctx, eob); err != nil {
			reconcileErr = err
			return ctrl.Result{}, err
		}
	}
	if violation == nil {
		var err error
		if violation, err = r.credentialViolation(ctx, eob); err != nil {
			reconcileErr = err
			return ctrl.Result{}, err
		}
	}

	if violation != nil {
		logger.Info("ExternalOutbound rejected", "name", eob.Name, "reason", violation.reason, "message", violation.message)
		result, err := r.setAccepted(ctx, eob, metav1.ConditionFalse, violation.reason, violation.message)
		reconcileErr = err
		return result, err
	}

	logger.Info("ExternalOutbound accepted", "name", eob.Name)
	result, err := r.setAccepted(ctx, eob, metav1.ConditionTrue, "Valid", "ExternalOutbound spec is valid")
	reconcileErr = err
	return result, err
}

// externalOutboundViolation is a failed validation check: reason is a terse
// CamelCase word for the Accepted condition, message a human-readable detail.
type externalOutboundViolation struct {
	reason  string
	message string
}

// specViolation runs the stateless spec coherence checks shared with the
// validating webhook: TLS rules per protocol, hysteria2 options placement and
// credentialsSecretRef presence.
func specViolation(eob *proxyv1alpha1.ExternalOutbound) *externalOutboundViolation {
	protocol := string(eob.Spec.Protocol)

	switch protocol {
	case proxyv1alpha1.ExternalProtocolTrojan,
		proxyv1alpha1.ExternalProtocolHysteria2,
		proxyv1alpha1.ExternalProtocolTUIC,
		proxyv1alpha1.ExternalProtocolAnyTLS:
		if eob.Spec.TLS == nil {
			return &externalOutboundViolation{"InvalidSpec", fmt.Sprintf("spec.tls is required for protocol %q", protocol)}
		}
		if eob.Spec.TLS.ServerName == "" && !eob.Spec.TLS.Insecure {
			return &externalOutboundViolation{"InvalidSpec", "spec.tls.serverName is required unless spec.tls.insecure is true"}
		}
	case proxyv1alpha1.ExternalProtocolSocks5,
		proxyv1alpha1.ExternalProtocolShadowsocks:
		if eob.Spec.TLS != nil {
			return &externalOutboundViolation{"InvalidSpec", fmt.Sprintf("spec.tls is not allowed for protocol %q", protocol)}
		}
	}

	if eob.Spec.Hysteria2 != nil && protocol != proxyv1alpha1.ExternalProtocolHysteria2 {
		return &externalOutboundViolation{"InvalidSpec", "spec.hysteria2 is only allowed when spec.protocol is hysteria2"}
	}

	if eob.Spec.CredentialsSecretRef == nil &&
		protocol != proxyv1alpha1.ExternalProtocolSocks5 &&
		protocol != proxyv1alpha1.ExternalProtocolHTTP {
		return &externalOutboundViolation{"InvalidSpec", fmt.Sprintf("spec.credentialsSecretRef is required for protocol %q", protocol)}
	}

	return nil
}

// nameConflict rejects the ExternalOutbound when a SingBoxNode with the same
// name exists in the namespace; the SingBoxNode wins the outbound name.
func (r *ExternalOutboundReconciler) nameConflict(ctx context.Context, eob *proxyv1alpha1.ExternalOutbound) (*externalOutboundViolation, error) {
	node := &proxyv1alpha1.SingBoxNode{}
	if err := r.Get(ctx, types.NamespacedName{Name: eob.Name, Namespace: eob.Namespace}, node); err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &externalOutboundViolation{"NameConflict",
		fmt.Sprintf("a SingBoxNode named %q exists in namespace %q and wins the outbound name", eob.Name, eob.Namespace)}, nil
}

// credentialViolation verifies the referenced credentials secret exists and
// holds every key the protocol requires. Secret values are never logged or
// retained beyond this check.
func (r *ExternalOutboundReconciler) credentialViolation(ctx context.Context, eob *proxyv1alpha1.ExternalOutbound) (*externalOutboundViolation, error) {
	ref := eob.Spec.CredentialsSecretRef
	if ref == nil {
		return nil, nil
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: eob.Namespace}, secret); err != nil {
		if errors.IsNotFound(err) {
			return &externalOutboundViolation{"SecretNotFound",
				fmt.Sprintf("credentials secret %q not found in namespace %q", ref.Name, eob.Namespace)}, nil
		}
		return nil, err
	}

	if eob.Spec.Protocol == proxyv1alpha1.ExternalProtocolSocks5 || eob.Spec.Protocol == proxyv1alpha1.ExternalProtocolHTTP {
		hasUsername := len(secret.Data[proxyv1alpha1.CredKeyUsername]) > 0
		hasPassword := len(secret.Data[proxyv1alpha1.CredKeyPassword]) > 0
		if hasUsername != hasPassword {
			return &externalOutboundViolation{"SecretKeyMissing",
				fmt.Sprintf("credentials secret %q must set both %q and %q or neither", ref.Name, proxyv1alpha1.CredKeyUsername, proxyv1alpha1.CredKeyPassword)}, nil
		}
		return nil, nil
	}

	for _, key := range requiredCredentialKeys(eob) {
		if len(secret.Data[key]) == 0 {
			return &externalOutboundViolation{"SecretKeyMissing",
				fmt.Sprintf("credentials secret %q is missing required key %q for protocol %q", ref.Name, key, eob.Spec.Protocol)}, nil
		}
	}

	if eob.Spec.Protocol == proxyv1alpha1.ExternalProtocolShadowsocks {
		method := string(secret.Data[proxyv1alpha1.CredKeyMethod])
		if !slices.Contains(validShadowsocksMethods, method) {
			return &externalOutboundViolation{"InvalidMethod",
				fmt.Sprintf("shadowsocks method %q in credentials secret %q is not supported", method, ref.Name)}, nil
		}
	}

	return nil, nil
}

// requiredCredentialKeys lists the secret keys that must be present and
// non-empty for the given spec, per protocol.
func requiredCredentialKeys(eob *proxyv1alpha1.ExternalOutbound) []string {
	switch eob.Spec.Protocol {
	case proxyv1alpha1.ExternalProtocolShadowsocks:
		return []string{proxyv1alpha1.CredKeyMethod, proxyv1alpha1.CredKeyPassword}
	case proxyv1alpha1.ExternalProtocolTrojan,
		proxyv1alpha1.ExternalProtocolAnyTLS:
		return []string{proxyv1alpha1.CredKeyPassword}
	case proxyv1alpha1.ExternalProtocolHysteria2:
		keys := []string{proxyv1alpha1.CredKeyPassword}
		if eob.Spec.Hysteria2 != nil && eob.Spec.Hysteria2.Obfs {
			keys = append(keys, proxyv1alpha1.CredKeyObfsPassword)
		}
		return keys
	case proxyv1alpha1.ExternalProtocolTUIC:
		return []string{proxyv1alpha1.CredKeyUUID, proxyv1alpha1.CredKeyPassword}
	default:
		return nil
	}
}

// setAccepted writes the single Accepted condition and bumps
// ObservedGeneration. Optimistic-concurrency conflicts are tolerated by
// requeueing.
func (r *ExternalOutboundReconciler) setAccepted(ctx context.Context, eob *proxyv1alpha1.ExternalOutbound, status metav1.ConditionStatus, reason, message string) (ctrl.Result, error) {
	latest := &proxyv1alpha1.ExternalOutbound{}
	if err := r.Get(ctx, types.NamespacedName{Name: eob.Name, Namespace: eob.Namespace}, latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.ObservedGeneration = latest.Generation
	apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
		Type:               proxyv1alpha1.ExternalOutboundAcceptedConditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: latest.Generation,
	})
	if err := r.Status().Update(ctx, latest); err != nil {
		if errors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// credentialsSecretMapper maps a Secret to every ExternalOutbound in its
// namespace referencing it, keeping the Accepted condition fresh when
// credentials rotate.
func (r *ExternalOutboundReconciler) credentialsSecretMapper(ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}
	eobList := &proxyv1alpha1.ExternalOutboundList{}
	if err := r.List(ctx, eobList, client.InNamespace(secret.Namespace)); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, eob := range eobList.Items {
		if eob.Spec.CredentialsSecretRef != nil && eob.Spec.CredentialsSecretRef.Name == secret.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: eob.Name, Namespace: eob.Namespace},
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *ExternalOutboundReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.ExternalOutbound{}).
		Named("externaloutbound").
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.credentialsSecretMapper)).
		Complete(r)
}
