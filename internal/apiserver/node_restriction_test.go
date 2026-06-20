package apiserver

import (
	"testing"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/credmanager"
)

// TestBuildClientConfig_WithNodeRestrictions verifies AllowedNodeNames and
// DeniedNodeNames filtering in BuildClientConfig.
//
// Setup: inbound node-a (us, vless), outbound node-b (us) and node-c (us).
func TestBuildClientConfig_WithNodeRestrictions(t *testing.T) {
	const baseUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outboundB := makeOutboundNode("node-b", "us")
	outboundC := makeOutboundNode("node-c", "us")

	user := makeUser("user-alice", "vless", "secret-alice")
	userCred := credmanager.UserCredential{UUID: baseUUID}

	t.Run("DeniedNodeNames excludes denied outbound node-b, keeps node-c", func(t *testing.T) {
		input := ClientConfigInput{
			User:         user,
			UserCred:     userCred,
			InboundNodes: []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
				"node-b": outboundB,
				"node-c": outboundC,
			},
			DeniedNodeNames: map[string]bool{"node-b": true},
		}

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		tags := collectTags(result)

		if tags["node-b#node-a"] {
			t.Errorf("node-b#node-a should be absent (node-b is denied), got tags: %v", tags)
		}
		if !tags["node-c#node-a"] {
			t.Errorf("node-c#node-a should be present (node-c is not denied), got tags: %v", tags)
		}
		if n := countProxyOutbounds(result); n != 1 {
			t.Errorf("expected 1 proxy outbound (node-c), got %d", n)
		}
	})

	t.Run("nil AllowedNodeNames and DeniedNodeNames allow all outbounds (regression)", func(t *testing.T) {
		input := ClientConfigInput{
			User:         user,
			UserCred:     userCred,
			InboundNodes: []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
				"node-b": outboundB,
				"node-c": outboundC,
			},
			AllowedNodeNames: nil,
			DeniedNodeNames:  nil,
		}

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		tags := collectTags(result)

		if !tags["node-b#node-a"] {
			t.Errorf("node-b#node-a should be present with nil restrictions, got tags: %v", tags)
		}
		if !tags["node-c#node-a"] {
			t.Errorf("node-c#node-a should be present with nil restrictions, got tags: %v", tags)
		}
		if n := countProxyOutbounds(result); n != 2 {
			t.Errorf("expected 2 proxy outbounds with no restrictions, got %d", n)
		}
	})

	t.Run("AllowedNodeNames keeps only node-b outbound, excludes node-c", func(t *testing.T) {
		// AllowedNodeNames must include the inbound node-a as well,
		// because BuildClientConfig also checks inbound nodes against the allowlist.
		input := ClientConfigInput{
			User:         user,
			UserCred:     userCred,
			InboundNodes: []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
				"node-b": outboundB,
				"node-c": outboundC,
			},
			AllowedNodeNames: map[string]bool{
				"node-a": true, // inbound node must be allowed for it to be processed
				"node-b": true, // only this outbound is allowlisted
			},
		}

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		tags := collectTags(result)

		if !tags["node-b#node-a"] {
			t.Errorf("node-b#node-a should be present (node-b is in allowlist), got tags: %v", tags)
		}
		if tags["node-c#node-a"] {
			t.Errorf("node-c#node-a should be absent (node-c is not in allowlist), got tags: %v", tags)
		}
		if n := countProxyOutbounds(result); n != 1 {
			t.Errorf("expected 1 proxy outbound (node-b only), got %d", n)
		}
	})
}
