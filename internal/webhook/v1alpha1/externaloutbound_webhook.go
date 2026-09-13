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

package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	singboxoperatorv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var externaloutboundlog = logf.Log.WithName("externaloutbound-resource")

// SetupExternalOutboundWebhookWithManager registers the webhook for ExternalOutbound in the manager.
func SetupExternalOutboundWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &singboxoperatorv1alpha1.ExternalOutbound{}).
		WithValidator(&ExternalOutboundValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-singboxoperator-shlande-top-v1alpha1-externaloutbound,mutating=false,failurePolicy=fail,sideEffects=None,groups=singboxoperator.shlande.top,resources=externaloutbounds,verbs=create;update,versions=v1alpha1,name=vexternaloutbound-v1alpha1.kb.io,admissionReviewVersions=v1

// ExternalOutboundValidator struct is responsible for validating the ExternalOutbound resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type ExternalOutboundValidator struct{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type ExternalOutbound.
func (v *ExternalOutboundValidator) ValidateCreate(_ context.Context, obj *singboxoperatorv1alpha1.ExternalOutbound) (admission.Warnings, error) {
	externaloutboundlog.Info("Validation for ExternalOutbound upon creation", "name", obj.GetName())

	return nil, validateExternalOutbound(obj)
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type ExternalOutbound.
func (v *ExternalOutboundValidator) ValidateUpdate(_ context.Context, oldObj, newObj *singboxoperatorv1alpha1.ExternalOutbound) (admission.Warnings, error) {
	externaloutboundlog.Info("Validation for ExternalOutbound upon update", "name", newObj.GetName())

	return nil, validateExternalOutbound(newObj)
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type ExternalOutbound.
func (v *ExternalOutboundValidator) ValidateDelete(_ context.Context, obj *singboxoperatorv1alpha1.ExternalOutbound) (admission.Warnings, error) {
	externaloutboundlog.Info("Validation for ExternalOutbound upon deletion", "name", obj.GetName())

	return nil, nil
}

// validateExternalOutbound runs the stateless spec coherence checks: TLS rules
// per protocol, hysteria2 options placement and credentialsSecretRef presence.
// Cluster-state checks (secret existence, name conflicts) are left to the
// ExternalOutbound controller.
func validateExternalOutbound(eob *singboxoperatorv1alpha1.ExternalOutbound) error {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")
	protocol := string(eob.Spec.Protocol)

	switch protocol {
	case singboxoperatorv1alpha1.ExternalProtocolTrojan,
		singboxoperatorv1alpha1.ExternalProtocolHysteria2,
		singboxoperatorv1alpha1.ExternalProtocolTUIC,
		singboxoperatorv1alpha1.ExternalProtocolAnyTLS:
		if eob.Spec.TLS == nil {
			allErrs = append(allErrs, field.Required(
				specPath.Child("tls"),
				fmt.Sprintf("tls is required for protocol %q", protocol)))
		} else if eob.Spec.TLS.ServerName == "" && !eob.Spec.TLS.Insecure {
			allErrs = append(allErrs, field.Required(
				specPath.Child("tls", "serverName"),
				"serverName is required unless insecure is true"))
		}
	case singboxoperatorv1alpha1.ExternalProtocolSocks5,
		singboxoperatorv1alpha1.ExternalProtocolShadowsocks:
		if eob.Spec.TLS != nil {
			allErrs = append(allErrs, field.Forbidden(
				specPath.Child("tls"),
				fmt.Sprintf("tls is not allowed for protocol %q", protocol)))
		}
	}

	if eob.Spec.Hysteria2 != nil && protocol != singboxoperatorv1alpha1.ExternalProtocolHysteria2 {
		allErrs = append(allErrs, field.Forbidden(
			specPath.Child("hysteria2"),
			"hysteria2 options are only allowed when protocol is hysteria2"))
	}

	if eob.Spec.CredentialsSecretRef == nil &&
		protocol != singboxoperatorv1alpha1.ExternalProtocolSocks5 &&
		protocol != singboxoperatorv1alpha1.ExternalProtocolHTTP {
		allErrs = append(allErrs, field.Required(
			specPath.Child("credentialsSecretRef"),
			fmt.Sprintf("credentialsSecretRef is required for protocol %q", protocol)))
	}

	if len(allErrs) > 0 {
		return allErrs.ToAggregate()
	}
	return nil
}
