package controller

import (
	"testing"

	v1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResolveEgressPolicyRejectsAmbiguousSelector(t *testing.T) {
	policy := &v1alpha1.EgressPolicy{Spec: v1alpha1.EgressPolicySpec{
		Action:         v1alpha1.EgressPolicyActionRoute,
		Match:          v1alpha1.EgressPolicyMatch{Domain: []string{"example.com"}},
		EgressSelector: v1alpha1.EgressPolicySelector{MatchLabels: map[string]string{"tier": "egress"}},
	}}
	nodes := []v1alpha1.SingBoxNode{
		{ObjectMeta: metav1.ObjectMeta{Name: "out-a", Labels: map[string]string{"tier": "egress"}}, Spec: v1alpha1.SingBoxNodeSpec{Roles: []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "out-b", Labels: map[string]string{"tier": "egress"}}, Spec: v1alpha1.SingBoxNodeSpec{Roles: []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}}},
	}
	matches, resolved, message := resolveEgressPolicy(policy, nodes, nil)
	if len(matches) != 2 || resolved != "" || message == "" {
		t.Fatalf("expected ambiguous policy, got matches=%v resolved=%q message=%q", matches, resolved, message)
	}
}

func TestEgressConnectedToInboundRequiresRegionAndBindings(t *testing.T) {
	in := &v1alpha1.SingBoxNode{ObjectMeta: metav1.ObjectMeta{Name: "in"}, Spec: v1alpha1.SingBoxNodeSpec{Region: "hk", Roles: []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound}}}
	nodes := []v1alpha1.SingBoxNode{
		{ObjectMeta: metav1.ObjectMeta{Name: "same"}, Spec: v1alpha1.SingBoxNodeSpec{Region: "hk", Roles: []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "other"}, Spec: v1alpha1.SingBoxNodeSpec{Region: "us-east", Roles: []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}}},
	}
	if !egressConnectedToInbound(in, "same", nodes, nil) {
		t.Fatal("same-region outbound should be connected")
	}
	if egressConnectedToInbound(in, "other", nodes, nil) {
		t.Fatal("cross-region outbound should not be connected")
	}

	external := []v1alpha1.ExternalOutbound{
		{ObjectMeta: metav1.ObjectMeta{Name: "ext-same"}, Spec: v1alpha1.ExternalOutboundSpec{Region: "hk"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "ext-other"}, Spec: v1alpha1.ExternalOutboundSpec{Region: "us-east"}},
	}
	if !egressConnectedToInbound(in, "ext-same", nil, external) {
		t.Fatal("same-region external outbound should be connected")
	}
	if egressConnectedToInbound(in, "ext-other", nil, external) {
		t.Fatal("cross-region external outbound should not be connected")
	}
}

func TestResolveEgressPolicyUsesLabelAndNameSelectors(t *testing.T) {
	policy := &v1alpha1.EgressPolicy{Spec: v1alpha1.EgressPolicySpec{
		Action: v1alpha1.EgressPolicyActionRoute,
		Match:  v1alpha1.EgressPolicyMatch{DomainSuffix: []string{"example.com"}},
		EgressSelector: v1alpha1.EgressPolicySelector{
			MatchLabels: map[string]string{"tier": "egress"},
			MatchNames:  []string{"out-b"},
		},
	}}
	nodes := []v1alpha1.SingBoxNode{
		{ObjectMeta: metav1.ObjectMeta{Name: "out-a", Labels: map[string]string{"tier": "egress"}}, Spec: v1alpha1.SingBoxNodeSpec{Roles: []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "out-b", Labels: map[string]string{"tier": "egress"}}, Spec: v1alpha1.SingBoxNodeSpec{Roles: []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}}},
	}
	matches, resolved, message := resolveEgressPolicy(policy, nodes, nil)
	if message != "" || resolved != "out-b" || len(matches) != 1 || matches[0] != "out-b" {
		t.Fatalf("expected out-b, got matches=%v resolved=%q message=%q", matches, resolved, message)
	}
}
