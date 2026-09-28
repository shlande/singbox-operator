package controller

import (
	"fmt"

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
