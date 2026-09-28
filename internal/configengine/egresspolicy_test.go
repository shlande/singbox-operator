package configengine

import (
	"encoding/json"
	"testing"

	v1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func policyTestNode(name, region string, role v1alpha1.ProxyRole) *v1alpha1.SingBoxNode {
	return &v1alpha1.SingBoxNode{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: v1alpha1.SingBoxNodeSpec{
		Address: name + ".example", Region: region, Roles: []v1alpha1.ProxyRole{role},
	}}
}

func policy(name string, priority int32, action string, match v1alpha1.EgressPolicyMatch) *v1alpha1.EgressPolicy {
	return &v1alpha1.EgressPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.EgressPolicySpec{
			Match:           match,
			Action:          action,
			Priority:        priority,
			IngressSelector: v1alpha1.EgressPolicySelector{MatchNames: []string{"in"}},
			EgressSelector:  v1alpha1.EgressPolicySelector{MatchNames: []string{"out"}},
		},
		Status: v1alpha1.EgressPolicyStatus{ResolvedEgress: "out"},
	}
}

func TestComputeEgressPoliciesSortAndRender(t *testing.T) {
	in := policyTestNode("in", "us-west", v1alpha1.ProxyRoleInbound)
	out := policyTestNode("out", "us-east", v1alpha1.ProxyRoleOutbound)
	out.Spec.RelayPort = 31000
	policies := []*v1alpha1.EgressPolicy{
		policy("z-reject", 20, v1alpha1.EgressPolicyActionReject, v1alpha1.EgressPolicyMatch{IPCIDR: []string{"10.0.0.0/8"}}),
		policy("a-route", 10, v1alpha1.EgressPolicyActionRoute, v1alpha1.EgressPolicyMatch{DomainSuffix: []string{"example.com"}}),
	}
	result, err := Compute(Input{
		Node:                    in,
		EgressPolicies:          policies,
		OutboundNodesByName:     map[string]*v1alpha1.SingBoxNode{"out": out},
		NodeCreds:               map[string]NodeCredential{"out": {Username: "u", Password: "p"}},
		ExternalOutboundsByName: map[string]*v1alpha1.ExternalOutbound{},
		ExternalCreds:           map[string]ExternalCredential{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Route struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(result.Config, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Route.Rules) != 2 {
		t.Fatalf("got %d route rules, want 2", len(config.Route.Rules))
	}
	if config.Route.Rules[0]["domain_suffix"] == nil || config.Route.Rules[0]["outbound"] != "outbound-out" {
		t.Fatalf("priority 10 route was not first: %#v", config.Route.Rules[0])
	}
	if config.Route.Rules[1]["ip_cidr"] == nil || config.Route.Rules[1]["action"] != "reject" {
		t.Fatalf("priority 20 reject was not second: %#v", config.Route.Rules[1])
	}
}

func TestComputeEgressPoliciesTieBreaksByName(t *testing.T) {
	in := policyTestNode("in", "us-west", v1alpha1.ProxyRoleInbound)
	policies := []*v1alpha1.EgressPolicy{
		policy("b", 1, v1alpha1.EgressPolicyActionReject, v1alpha1.EgressPolicyMatch{Domain: []string{"b.example"}}),
		policy("a", 1, v1alpha1.EgressPolicyActionReject, v1alpha1.EgressPolicyMatch{Domain: []string{"a.example"}}),
	}
	result, err := Compute(Input{Node: in, EgressPolicies: policies})
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Route struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(result.Config, &config); err != nil {
		t.Fatal(err)
	}
	got := config.Route.Rules[0]["domain"].([]any)[0]
	if got != "a.example" {
		t.Fatalf("tie was not name-stable, got %v", got)
	}
}
