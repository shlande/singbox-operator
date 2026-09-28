package apiserver

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
)

// buildClientPolicies returns policy-only connection paths and native sing-box rules.
// All route policies share the single ai selector; unavailable paths reject traffic.
func buildClientPolicies(input ClientConfigInput) ([]any, []any, map[string]bool, error) {
	policies := slices.Clone(input.EgressPolicies)
	for _, p := range policies {
		if p == nil {
			return nil, nil, nil, fmt.Errorf("nil egress policy")
		}
	}
	sort.Slice(policies, func(i, j int) bool {
		if policies[i].Spec.Priority != policies[j].Spec.Priority {
			return policies[i].Spec.Priority < policies[j].Spec.Priority
		}
		return policies[i].Name < policies[j].Name
	})
	var outbounds, rules []any
	var aiMembers []string
	targets := make(map[string]bool)
	for _, policy := range policies {
		spec := policy.Spec
		if err := spec.IngressSelector.Valid(); err != nil {
			return nil, nil, nil, fmt.Errorf("policy %s ingressSelector: %w", policy.Name, err)
		}
		if err := spec.EgressSelector.Valid(); err != nil {
			return nil, nil, nil, fmt.Errorf("policy %s egressSelector: %w", policy.Name, err)
		}
		if err := spec.Match.Valid(spec.FallbackAction != ""); err != nil {
			return nil, nil, nil, fmt.Errorf("policy %s match: %w", policy.Name, err)
		}
		if spec.FallbackAction != "" && spec.FallbackAction != v1alpha1.EgressPolicyActionReject {
			return nil, nil, nil, fmt.Errorf("policy %s: invalid fallbackAction %q", policy.Name, spec.FallbackAction)
		}
		if spec.Action != v1alpha1.EgressPolicyActionRoute && spec.Action != v1alpha1.EgressPolicyActionReject {
			return nil, nil, nil, fmt.Errorf("policy %s: invalid action %q", policy.Name, spec.Action)
		}
		rule := policyMatchRule(spec.Match)
		if spec.Action == v1alpha1.EgressPolicyActionReject {
			rule["action"] = "reject"
			rules = append(rules, rule)
			continue
		}

		name, kind, count := "", "", 0
		for _, n := range input.OutboundsByName {
			if spec.EgressSelector.Matches(n) {
				name, kind, count = n.Name, "node", count+1
			}
		}
		for _, e := range input.ExternalOutboundsByName {
			if spec.EgressSelector.Matches(e) {
				name, kind, count = e.Name, "external", count+1
			}
		}
		if count != 1 {
			return nil, nil, nil, fmt.Errorf("policy %s: egressSelector matched %d targets, expected exactly one", policy.Name, count)
		}
		targets[name] = true
		var members []string
		if strings.TrimSpace(input.UserCred.UUID) != "" && configengine.IsNodeAllowed(name, input.AllowedNodeNames, input.DeniedNodeNames) {
			for _, inbound := range input.InboundNodes {
				if !spec.IngressSelector.Matches(inbound) || input.OfflineNodeNames[inbound.Name] {
					continue
				}
				proto := configengine.EffectiveInboundProtocol(inbound)
				if !supportsProtocol(inbound, proto) {
					continue
				}
				address, port, ok := findEntryEndpoint(inbound.Status.EntryEndpoints, proto)
				if !ok {
					continue
				}
				if len(inbound.Spec.AllowedOutbounds) > 0 && !slices.Contains(inbound.Spec.AllowedOutbounds, name) {
					continue
				}
				if kind == "node" {
					n := input.OutboundsByName[name]
					if input.OfflineNodeNames[name] || (n.Spec.RelayPort == 0 && n.Name != inbound.Name) || (len(n.Spec.AllowedInbounds) > 0 && !slices.Contains(n.Spec.AllowedInbounds, inbound.Name)) {
						continue
					}
				} else if e := input.ExternalOutboundsByName[name]; len(e.Spec.AllowedInbounds) > 0 && !slices.Contains(e.Spec.AllowedInbounds, inbound.Name) {
					continue
				}
				pathTag := "ai#" + inbound.Name + "#" + name
				members = append(members, pathTag)
				outbounds = append(outbounds, buildProxyOutbound(pathTag, address, port, proto, input.User.Name, name, inbound.Status.TLSServerName, input.UserCred))
			}
		}
		if len(members) == 0 {
			rule["action"] = "reject"
		} else {
			aiMembers = append(aiMembers, members...)
			rule["outbound"] = "ai"
		}
		rules = append(rules, rule)
	}
	if len(aiMembers) > 0 {
		sort.Strings(aiMembers)
		aiMembers = slices.Compact(aiMembers)
		outbounds = append(outbounds, map[string]any{"type": "selector", "tag": "ai", "outbounds": aiMembers})
	}
	return outbounds, rules, targets, nil
}

func policyMatchRule(match v1alpha1.EgressPolicyMatch) map[string]any {
	rule := make(map[string]any)
	if len(match.Domain) > 0 {
		rule["domain"] = match.Domain
	}
	if len(match.DomainSuffix) > 0 {
		rule["domain_suffix"] = match.DomainSuffix
	}
	if len(match.DomainRegex) > 0 {
		rule["domain_regex"] = match.DomainRegex
	}
	if len(match.IPCIDR) > 0 {
		rule["ip_cidr"] = match.IPCIDR
	}
	if len(match.RuleSet) > 0 {
		rule["rule_set"] = match.RuleSet
	}
	return rule
}
