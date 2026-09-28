package apiserver

import (
	"encoding/json"
	"testing"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/credmanager"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAIPolicyIsIndependentOfClashModes(t *testing.T) {
	in := makeInboundNode("in", "hk", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{{Protocol: "vless", Port: 443}})
	in.Status.EntryEndpoints = []string{"vless:1.2.3.4:443"}
	out := makeOutboundNode("ai", "eu")
	hk := makeOutboundNode("hk-node", "hk")
	policy := &proxyv1alpha1.EgressPolicy{ObjectMeta: metav1.ObjectMeta{Name: "ai", Namespace: "ns"}, Spec: proxyv1alpha1.EgressPolicySpec{
		IngressSelector: proxyv1alpha1.EgressPolicySelector{MatchNames: []string{"in"}}, EgressSelector: proxyv1alpha1.EgressPolicySelector{MatchNames: []string{"ai"}},
		Match: proxyv1alpha1.EgressPolicyMatch{DomainSuffix: []string{"example.com"}}, Action: proxyv1alpha1.EgressPolicyActionRoute,
	}}
	input := ClientConfigInput{User: makeUser("u", "s"), UserCred: credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}, InboundNodes: []*proxyv1alpha1.SingBoxNode{in}, OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{"ai": out, "hk-node": hk}, EgressPolicies: []*proxyv1alpha1.EgressPolicy{policy}}
	generated, err := BuildClientConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := MergeClientConfig(DefaultTemplate, generated, input)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(merged, &config); err != nil {
		t.Fatal(err)
	}
	var aiIndex, firstModeIndex = -1, -1
	for i, raw := range config["route"].(map[string]any)["rules"].([]any) {
		rule := raw.(map[string]any)
		if rule["outbound"] == "ai" {
			aiIndex = i
			if _, ok := rule["clash_mode"]; ok {
				t.Fatal("AI policy rule must not have clash_mode")
			}
		}
		if _, ok := rule["clash_mode"]; ok && firstModeIndex < 0 {
			firstModeIndex = i
		}
	}
	if aiIndex < 0 {
		t.Fatal("missing AI policy route")
	}
	if firstModeIndex < 0 || aiIndex >= firstModeIndex {
		t.Fatalf("AI rule index %d must precede mode index %d", aiIndex, firstModeIndex)
	}
}
