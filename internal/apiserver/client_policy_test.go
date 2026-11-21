package apiserver

import (
	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/credmanager"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestBuildClientConfig_EgressPolicyCrossRegionExternalUsesInboundEndpoint(t *testing.T) {
	inbound := makeInboundNode("gcp-hk", "hk", "192.0.2.10", []proxyv1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}})
	inbound.Status.EntryEndpoints = []string{"vless:192.0.2.10:10443"}
	egress := makeExternalOutbound("kddi", "jp")
	egress.Spec.ClientGroups = []string{"ai"}
	policy := &proxyv1alpha1.EgressPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "route-kddi", Namespace: "default"},
		Spec: proxyv1alpha1.EgressPolicySpec{
			IngressSelector: proxyv1alpha1.EgressPolicySelector{MatchNames: []string{"gcp-hk"}},
			EgressSelector:  proxyv1alpha1.EgressPolicySelector{MatchNames: []string{"kddi"}},
			Match:           proxyv1alpha1.EgressPolicyMatch{DomainSuffix: []string{"example.com"}},
			Action:          proxyv1alpha1.EgressPolicyActionRoute,
		},
	}

	result, err := BuildClientConfig(ClientConfigInput{
		User:                    makeUser("u", "s"),
		UserCred:                credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:            []*proxyv1alpha1.SingBoxNode{inbound},
		ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{"kddi": egress},
		EgressPolicies:          []*proxyv1alpha1.EgressPolicy{policy},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !collectTags(result)["kddi#gcp-hk"] {
		t.Fatalf("missing cross-region policy path, tags: %v", collectTags(result))
	}
	if got := selectorGroupOutbounds(result, "ai"); len(got) != 1 || got[0] != "kddi#gcp-hk" {
		t.Fatalf("ai selector = %v, want [kddi#gcp-hk]", got)
	}
	for _, raw := range result {
		ob, ok := raw.(map[string]any)
		if !ok || ob["tag"] != "kddi#gcp-hk" {
			continue
		}
		if ob["server"] != "192.0.2.10" || ob["server_port"] != 10443 {
			t.Fatalf("policy path must use inbound endpoint, got server=%v port=%v", ob["server"], ob["server_port"])
		}
		if ob["server"] == egress.Spec.Server || ob["server_port"] == egress.Spec.Port {
			t.Fatalf("external server endpoint leaked into client path: %v", ob)
		}
	}
}

func TestBuildClientConfig_EgressPolicyIngressSelectorScopesPath(t *testing.T) {
	matching := makeInboundNode("gcp-hk", "hk", "192.0.2.10", []proxyv1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}})
	matching.Status.EntryEndpoints = []string{"vless:192.0.2.10:10443"}
	nonMatching := makeInboundNode("gcp-us", "us", "192.0.2.11", []proxyv1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}})
	nonMatching.Status.EntryEndpoints = []string{"vless:192.0.2.11:10443"}
	egress := makeExternalOutbound("kddi", "jp")
	egress.Spec.ClientGroups = []string{"ai"}
	policy := &proxyv1alpha1.EgressPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "route-kddi", Namespace: "default"},
		Spec: proxyv1alpha1.EgressPolicySpec{
			IngressSelector: proxyv1alpha1.EgressPolicySelector{MatchNames: []string{"gcp-hk"}},
			EgressSelector:  proxyv1alpha1.EgressPolicySelector{MatchNames: []string{"kddi"}},
			Match:           proxyv1alpha1.EgressPolicyMatch{DomainSuffix: []string{"example.com"}},
			Action:          proxyv1alpha1.EgressPolicyActionRoute,
		},
	}

	result, err := BuildClientConfig(ClientConfigInput{
		User:                    makeUser("u", "s"),
		UserCred:                credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:            []*proxyv1alpha1.SingBoxNode{matching, nonMatching},
		ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{"kddi": egress},
		EgressPolicies:          []*proxyv1alpha1.EgressPolicy{policy},
	})
	if err != nil {
		t.Fatal(err)
	}
	tags := collectTags(result)
	if !tags["kddi#gcp-hk"] {
		t.Fatal("matching ingress must receive the policy path")
	}
	if tags["kddi#gcp-us"] {
		t.Fatal("non-matching ingress must not receive the policy path")
	}
	if got := selectorGroupOutbounds(result, "ai"); len(got) != 1 || got[0] != "kddi#gcp-hk" {
		t.Fatalf("ai selector = %v, want only matching ingress path", got)
	}
}

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
