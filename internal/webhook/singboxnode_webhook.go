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

// SingBoxNodeWebhook handles defaulting and validation for SingBoxNode.
type SingBoxNodeWebhook struct {
	NodePortRangeMin int32
	NodePortRangeMax int32
}

func (w *SingBoxNodeWebhook) Default(ctx context.Context, node *v1alpha1.SingBoxNode) error {
	return nil
}

func (w *SingBoxNodeWebhook) ValidateCreate(ctx context.Context, node *v1alpha1.SingBoxNode) (admission.Warnings, error) {
	return nil, w.validateSingBoxNode(node)
}

func (w *SingBoxNodeWebhook) ValidateUpdate(ctx context.Context, oldNode, newNode *v1alpha1.SingBoxNode) (admission.Warnings, error) {
	return nil, w.validateSingBoxNode(newNode)
}

func (w *SingBoxNodeWebhook) ValidateDelete(ctx context.Context, node *v1alpha1.SingBoxNode) (admission.Warnings, error) {
	return nil, nil
}

func (w *SingBoxNodeWebhook) validateSingBoxNode(node *v1alpha1.SingBoxNode) error {
	var allErrs field.ErrorList

	if node.Spec.Address == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "address"), "address must not be empty"))
	} else if net.ParseIP(node.Spec.Address) == nil {
		if !isValidHostname(node.Spec.Address) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "address"), node.Spec.Address, "address must be a valid IP address or hostname"))
		}
	}

	seenProtocols := make(map[string]bool)
	for i, proto := range node.Spec.SupportedProtocols {
		if seenProtocols[proto.Protocol] {
			allErrs = append(allErrs, field.Duplicate(field.NewPath("spec", "supportedProtocols").Index(i).Child("protocol"), proto.Protocol))
		}
		seenProtocols[proto.Protocol] = true
	}

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
			if w.inNodePortRange(proto.Port) {
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("spec", "supportedProtocols").Index(i).Child("port"),
					proto.Port,
					fmt.Sprintf("port %d falls within the Kubernetes NodePort range [%d, %d]; hostPort in this range is rejected by Cilium — use a port outside this range", proto.Port, w.NodePortRangeMin, w.NodePortRangeMax),
				))
			}
		}
	}

	if node.Spec.RelayPort != 0 && w.inNodePortRange(node.Spec.RelayPort) {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "relayPort"),
			node.Spec.RelayPort,
			fmt.Sprintf("relayPort %d falls within the Kubernetes NodePort range [%d, %d]; hostPort in this range is rejected by Cilium — use a port outside this range", node.Spec.RelayPort, w.NodePortRangeMin, w.NodePortRangeMax),
		))
	}

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

	seenAllowedInbounds := make(map[string]bool)
	for i, entry := range node.Spec.AllowedInbounds {
		if entry == "" {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "allowedInbounds").Index(i),
				entry,
				"allowedInbounds entry must not be empty",
			))
		}
		if seenAllowedInbounds[entry] {
			allErrs = append(allErrs, field.Duplicate(
				field.NewPath("spec", "allowedInbounds").Index(i),
				entry,
			))
		}
		seenAllowedInbounds[entry] = true
	}

	seenAllowedOutbounds := make(map[string]bool)
	for i, entry := range node.Spec.AllowedOutbounds {
		if entry == "" {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "allowedOutbounds").Index(i),
				entry,
				"allowedOutbounds entry must not be empty",
			))
		}
		if seenAllowedOutbounds[entry] {
			allErrs = append(allErrs, field.Duplicate(
				field.NewPath("spec", "allowedOutbounds").Index(i),
				entry,
			))
		}
		seenAllowedOutbounds[entry] = true
	}

	// Validate inbound protocol consistency for inbound-role nodes.
	// The default "hysteria2" string is kept in sync with
	// internal/configengine/engine.go EffectiveInboundProtocol logic.
	isInbound := false
	for _, role := range node.Spec.Roles {
		if role == v1alpha1.ProxyRoleInbound {
			isInbound = true
			break
		}
	}
	if isInbound {
		if len(node.Spec.SupportedProtocols) == 0 {
			allErrs = append(allErrs, field.Required(
				field.NewPath("spec", "supportedProtocols"),
				"inbound nodes must declare at least one supported protocol with a port",
			))
		} else {
			effectiveProto := node.Spec.InboundProtocol
			if effectiveProto == "" {
				effectiveProto = "hysteria2"
			}
			found := false
			for _, p := range node.Spec.SupportedProtocols {
				if p.Protocol == effectiveProto {
					found = true
					break
				}
			}
			if !found {
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("spec", "inboundProtocol"),
					node.Spec.InboundProtocol,
					fmt.Sprintf("effective inbound protocol %q must be present in spec.supportedProtocols (add an entry with protocol=%q and the desired port)", effectiveProto, effectiveProto),
				))
			}
		}
	}

	if len(allErrs) > 0 {
		return allErrs.ToAggregate()
	}
	return nil
}

func (w *SingBoxNodeWebhook) inNodePortRange(port int32) bool {
	if w.NodePortRangeMin == 0 && w.NodePortRangeMax == 0 {
		return false
	}
	return port >= w.NodePortRangeMin && port <= w.NodePortRangeMax
}

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

func SetupSingBoxNodeWebhookWithManager(mgr ctrl.Manager, nodePortRangeMin, nodePortRangeMax int32) error {
	wh := &SingBoxNodeWebhook{
		NodePortRangeMin: nodePortRangeMin,
		NodePortRangeMax: nodePortRangeMax,
	}
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.SingBoxNode{}).
		WithDefaulter(wh).
		WithValidator(wh).
		Complete()
}
