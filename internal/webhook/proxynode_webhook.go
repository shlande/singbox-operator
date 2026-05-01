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

package webhook

import (
	"context"
	"fmt"
	"net"

	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/shlande/singbox-operator/api/v1alpha1"
)

// ProxyNodeWebhook handles defaulting and validation for ProxyNode.
type ProxyNodeWebhook struct{}

// Default implements admission.Defaulter[*v1alpha1.ProxyNode].
func (w *ProxyNodeWebhook) Default(ctx context.Context, node *v1alpha1.ProxyNode) error {
	return nil
}

// ValidateCreate implements admission.Validator[*v1alpha1.ProxyNode].
func (w *ProxyNodeWebhook) ValidateCreate(ctx context.Context, node *v1alpha1.ProxyNode) (admission.Warnings, error) {
	return nil, validateProxyNode(node)
}

// ValidateUpdate implements admission.Validator[*v1alpha1.ProxyNode].
func (w *ProxyNodeWebhook) ValidateUpdate(ctx context.Context, oldNode, newNode *v1alpha1.ProxyNode) (admission.Warnings, error) {
	return nil, validateProxyNode(newNode)
}

// ValidateDelete implements admission.Validator[*v1alpha1.ProxyNode].
func (w *ProxyNodeWebhook) ValidateDelete(ctx context.Context, node *v1alpha1.ProxyNode) (admission.Warnings, error) {
	return nil, nil
}

func validateProxyNode(node *v1alpha1.ProxyNode) error {
	var allErrs field.ErrorList

	// Validate address is non-empty and valid IP or hostname
	if node.Spec.Address == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "address"), "address must not be empty"))
	} else if net.ParseIP(node.Spec.Address) == nil {
		if !isValidHostname(node.Spec.Address) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "address"), node.Spec.Address, "address must be a valid IP address or hostname"))
		}
	}

	// Validate relayNodePort range (0 means not set / NodePort will be randomly assigned)
	if node.Spec.RelayNodePort != 0 && (node.Spec.RelayNodePort < 30000 || node.Spec.RelayNodePort > 32767) {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "relayNodePort"), node.Spec.RelayNodePort, "relayNodePort must be in the Kubernetes NodePort range (30000-32767)"))
	}

	// Validate supportedProtocols ports are in Kubernetes NodePort range (30000-32767)
	for i, proto := range node.Spec.SupportedProtocols {
		if proto.Port != 0 && (proto.Port < 30000 || proto.Port > 32767) {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "supportedProtocols").Index(i).Child("port"),
				proto.Port,
				"port must be in the Kubernetes NodePort range (30000-32767)",
			))
		}
	}

	// Validate no duplicate protocols in supportedProtocols
	seenProtocols := make(map[string]bool)
	for i, proto := range node.Spec.SupportedProtocols {
		if seenProtocols[proto.Protocol] {
			allErrs = append(allErrs, field.Duplicate(field.NewPath("spec", "supportedProtocols").Index(i).Child("protocol"), proto.Protocol))
		}
		seenProtocols[proto.Protocol] = true
	}

	// Validate no port conflicts within supportedProtocols
	usedPorts := make(map[int32]string)
	for i, proto := range node.Spec.SupportedProtocols {
		if proto.Port != 0 {
			if existing, conflict := usedPorts[proto.Port]; conflict {
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("spec", "supportedProtocols").Index(i).Child("port"),
					proto.Port,
					fmt.Sprintf("port conflict with %s", existing),
				))
			}
			usedPorts[proto.Port] = fmt.Sprintf("supportedProtocols[%d].port", i)
		}
	}

	// Validate roles: at least one, only inbound/outbound
	if len(node.Spec.Roles) == 0 {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "roles"), "at least one role is required"))
	}
	for i, role := range node.Spec.Roles {
		if role != v1alpha1.ProxyRoleInbound && role != v1alpha1.ProxyRoleOutbound {
			allErrs = append(allErrs, field.NotSupported(
				field.NewPath("spec", "roles").Index(i),
				role,
				[]string{string(v1alpha1.ProxyRoleInbound), string(v1alpha1.ProxyRoleOutbound)},
			))
		}
	}

	if len(allErrs) > 0 {
		return allErrs.ToAggregate()
	}
	return nil
}

// isValidHostname performs a basic hostname validation.
func isValidHostname(hostname string) bool {
	if len(hostname) == 0 || len(hostname) > 253 {
		return false
	}
	for _, c := range hostname {
		if c == ' ' {
			return false
		}
	}
	return true
}

// SetupProxyNodeWebhookWithManager registers the webhook with the manager.
func SetupProxyNodeWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.ProxyNode{}).
		WithDefaulter(&ProxyNodeWebhook{}).
		WithValidator(&ProxyNodeWebhook{}).
		Complete()
}
