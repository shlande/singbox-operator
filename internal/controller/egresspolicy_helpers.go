package controller

import (
	"fmt"
	"slices"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

// resolveEgressPolicy returns the matching egress candidates, the unique
// target name (when one exists), and a validation message. It deliberately
// considers SingBoxNodes and ExternalOutbounds together: an ambiguous selector
// must never result in an arbitrary generated route.
func resolveEgressPolicy(policy *proxyv1alpha1.EgressPolicy, nodes []proxyv1alpha1.SingBoxNode, outbounds []proxyv1alpha1.ExternalOutbound) ([]string, string, string) {
	if err := policy.Spec.Match.Valid(policy.Spec.FallbackAction != ""); err != nil {
		return nil, "", err.Error()
	}
	if policy.Spec.Action != proxyv1alpha1.EgressPolicyActionRoute && policy.Spec.Action != proxyv1alpha1.EgressPolicyActionReject {
		return nil, "", fmt.Sprintf("unsupported action %q", policy.Spec.Action)
	}
	if policy.Spec.Action == proxyv1alpha1.EgressPolicyActionReject {
		return nil, "", ""
	}

	var names []string
	for i := range nodes {
		node := &nodes[i]
		if hasRole(node, proxyv1alpha1.ProxyRoleOutbound) && policy.Spec.EgressSelector.Matches(node) {
			names = append(names, node.Name)
		}
	}
	for i := range outbounds {
		if policy.Spec.EgressSelector.Matches(&outbounds[i]) {
			names = append(names, outbounds[i].Name)
		}
	}
	if len(names) != 1 {
		return names, "", fmt.Sprintf("egressSelector matched %d egress targets, expected exactly one", len(names))
	}
	return names, names[0], ""
}

func findOutboundNode(nodes []proxyv1alpha1.SingBoxNode, name string) *proxyv1alpha1.SingBoxNode {
	for i := range nodes {
		if nodes[i].Name == name && hasRole(&nodes[i], proxyv1alpha1.ProxyRoleOutbound) {
			return &nodes[i]
		}
	}
	return nil
}

func findExternalOutbound(outbounds []proxyv1alpha1.ExternalOutbound, name string) *proxyv1alpha1.ExternalOutbound {
	for i := range outbounds {
		if outbounds[i].Name == name {
			return &outbounds[i]
		}
	}
	return nil
}

// egressConnectedToInbound reports whether an egress target can actually be
// reached from the current inbound. EgressPolicy selectors may match targets
// in any region, but a policy must not add a cross-region outbound to a node's
// config when the normal region/allowlist connection rules exclude it.
func egressConnectedToInbound(inbound *proxyv1alpha1.SingBoxNode, name string, nodes []proxyv1alpha1.SingBoxNode, outbounds []proxyv1alpha1.ExternalOutbound) bool {
	if target := findOutboundNode(nodes, name); target != nil {
		if target.Name == inbound.Name {
			return hasRole(inbound, proxyv1alpha1.ProxyRoleOutbound)
		}
		if target.Spec.Region != inbound.Spec.Region {
			return false
		}
		if len(target.Spec.AllowedInbounds) > 0 && !slices.Contains(target.Spec.AllowedInbounds, inbound.Name) {
			return false
		}
		if len(inbound.Spec.AllowedOutbounds) > 0 && !slices.Contains(inbound.Spec.AllowedOutbounds, target.Name) {
			return false
		}
		return true
	}
	if target := findExternalOutbound(outbounds, name); target != nil {
		if target.Spec.Region == "" || target.Spec.Region != inbound.Spec.Region {
			return false
		}
		if len(target.Spec.AllowedInbounds) > 0 && !slices.Contains(target.Spec.AllowedInbounds, inbound.Name) {
			return false
		}
		if len(inbound.Spec.AllowedOutbounds) > 0 && !slices.Contains(inbound.Spec.AllowedOutbounds, target.Name) {
			return false
		}
		return true
	}
	return false
}
