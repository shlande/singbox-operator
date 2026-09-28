package apiserver

import (
	"fmt"
	"slices"
	"sort"

	"github.com/shlande/singbox-operator/api/v1alpha1"
)

// buildClientPolicies returns policy-only connection paths and native sing-box rules.
// All route policies share the single ai selector; unavailable paths reject traffic.
func buildClientPolicies(input ClientConfigInput) ([]any, []any, map[string]bool, error) {
	policies := slices.Clone(input.EgressPolicies)
	sort.Slice(policies, func(i, j int) bool {
		if policies[i].Spec.Priority != policies[j].Spec.Priority {
			return policies[i].Spec.Priority < policies[j].Spec.Priority
		}
		return policies[i].Name < policies[j].Name
	})
	var rules []any
	targets := make(map[string]bool)
	for _, policy := range policies {
		if policy == nil {
			return nil, nil, nil, fmt.Errorf("nil egress policy")
		}
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
		name, count := "", 0
		for _, n := range input.OutboundsByName {
			if spec.EgressSelector.Matches(n) {
				name, count = n.Name, count+1
			}
		}
		for _, e := range input.ExternalOutboundsByName {
			if spec.EgressSelector.Matches(e) {
				name, count = e.Name, count+1
			}
		}
		if count != 1 {
			return nil, nil, nil, fmt.Errorf("policy %s: egressSelector matched %d targets, expected exactly one", policy.Name, count)
		}
		targets[name] = true
		if targetHasClientGroup(input, name, "ai") {
			rule["outbound"] = "ai"
		} else {
			rule["action"] = "reject"
		}
		rules = append(rules, rule)
	}
	return nil, rules, targets, nil
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
