package apiserver

import proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"

func policyOnlyEgresses(policies *proxyv1alpha1.EgressPolicyList, nodes []proxyv1alpha1.SingBoxNode, outbounds []proxyv1alpha1.ExternalOutbound) map[string]bool {
	names := make(map[string]bool)
	for i := range policies.Items {
		policy := &policies.Items[i]
		if policy.Spec.Action != proxyv1alpha1.EgressPolicyActionRoute {
			continue
		}
		var matches []string
		for j := range nodes {
			node := &nodes[j]
			if hasRole(node, proxyv1alpha1.ProxyRoleOutbound) && policy.Spec.EgressSelector.Matches(node) {
				matches = append(matches, node.Name)
			}
		}
		for j := range outbounds {
			if policy.Spec.EgressSelector.Matches(&outbounds[j]) {
				matches = append(matches, outbounds[j].Name)
			}
		}
		if len(matches) == 1 {
			names[matches[0]] = true
		}
	}
	return names
}
