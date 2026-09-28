package apiserver

import (
	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/credmanager"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestBuildClientConfigPolicyIsolationAndNativeMatch(t *testing.T) {
	in := makeInboundNode("in", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{{Protocol: "vless", Port: 443}})
	in.Status.EntryEndpoints = []string{"vless:1.2.3.4:443"}
	out := makeOutboundNode("ai", "us")
	out.Spec.ClientGroups = []string{"ai"}
	p := &proxyv1alpha1.EgressPolicy{ObjectMeta: metav1.ObjectMeta{Name: "ai", Namespace: "ns"}, Spec: proxyv1alpha1.EgressPolicySpec{IngressSelector: proxyv1alpha1.EgressPolicySelector{MatchNames: []string{"in"}}, EgressSelector: proxyv1alpha1.EgressPolicySelector{MatchNames: []string{"ai"}}, Match: proxyv1alpha1.EgressPolicyMatch{DomainSuffix: []string{"example.com"}}, Action: proxyv1alpha1.EgressPolicyActionRoute}}
	r, err := BuildClientConfig(ClientConfigInput{User: makeUser("u", "s"), UserCred: credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}, InboundNodes: []*proxyv1alpha1.SingBoxNode{in}, OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{"ai": out}, EgressPolicies: []*proxyv1alpha1.EgressPolicy{p}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range r {
		if m, ok := x.(map[string]any); ok {
			if m["tag"] == "ai" {
				found = true
				members := m["outbounds"].([]string)
				if len(members) != 1 || members[0] != "ai#in" {
					t.Fatalf("unexpected AI selector members: %v", members)
				}
			}
			if m["tag"] == "proxy" {
				t.Fatal("proxy selector must not be generated")
			}
		}
	}
	if !found {
		t.Fatal("missing AI selector")
	}
}
