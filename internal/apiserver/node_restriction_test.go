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

	user := makeUser("user-alice", "secret-alice")
	userCred := credmanager.UserCredential{UUID: baseUUID}

	t.Run("DeniedNodeNames excludes denied outbound node-b, keeps node-c", func(t *testing.T) {
		input := ClientConfigInput{
			User:            user,
			UserCred:        userCred,
			InboundNodes:    []*proxyv1alpha1.SingBoxNode{inbound},
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
			User:            user,
			UserCred:        userCred,
			InboundNodes:    []*proxyv1alpha1.SingBoxNode{inbound},
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
			User:            user,
			UserCred:        userCred,
			InboundNodes:    []*proxyv1alpha1.SingBoxNode{inbound},
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

// TestBuildClientConfig_WithAllowedOutbounds verifies the AllowedOutbounds
// filtering in resolveOutboundNodes, which restricts which outbound nodes
// an inbound node may use as upstream.
//
// Three scenarios:
//  1. Whitelist: inbound node-a with AllowedOutbounds=["node-b"] → only
//     node-b appears, node-c is excluded.
//  2. Regression: empty/nil AllowedOutbounds → all same-region outbounds
//     appear (backward compatible).
//  3. Self-as-outbound: dual-role node-a with AllowedOutbounds=["node-a"]
//     → only self outbound (tag "node-a") appears, other same-region
//     outbound node-b excluded.
func TestBuildClientConfig_WithAllowedOutbounds(t *testing.T) {
	const baseUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	user := makeUser("user-alice", "secret-alice")
	userCred := credmanager.UserCredential{UUID: baseUUID}

	t.Run("whitelist-allowed-outbounds", func(t *testing.T) {
		inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
			{Protocol: "vless", Port: 10443},
		})
		inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
		inbound.Spec.AllowedOutbounds = []string{"node-b"}

		outboundB := makeOutboundNode("node-b", "us")
		outboundC := makeOutboundNode("node-c", "us")

		input := ClientConfigInput{
			User:            user,
			UserCred:        userCred,
			InboundNodes:    []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
				"node-b": outboundB,
				"node-c": outboundC,
			},
		}

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		tags := collectTags(result)

		if !tags["node-b#node-a"] {
			t.Errorf("node-b#node-a should be present (node-b is in AllowedOutbounds), got tags: %v", tags)
		}
		if tags["node-c#node-a"] {
			t.Errorf("node-c#node-a should be absent (node-c not in AllowedOutbounds), got tags: %v", tags)
		}
		if n := countProxyOutbounds(result); n != 1 {
			t.Errorf("expected 1 proxy outbound (node-b only), got %d", n)
		}
	})

	t.Run("empty-allowed-outbounds-allows-all-regression", func(t *testing.T) {
		inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
			{Protocol: "vless", Port: 10443},
		})
		inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

		outboundB := makeOutboundNode("node-b", "us")
		outboundC := makeOutboundNode("node-c", "us")

		input := ClientConfigInput{
			User:            user,
			UserCred:        userCred,
			InboundNodes:    []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
				"node-b": outboundB,
				"node-c": outboundC,
			},
		}

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		tags := collectTags(result)

		if !tags["node-b#node-a"] {
			t.Errorf("node-b#node-a should be present with empty AllowedOutbounds, got tags: %v", tags)
		}
		if !tags["node-c#node-a"] {
			t.Errorf("node-c#node-a should be present with empty AllowedOutbounds, got tags: %v", tags)
		}
		if n := countProxyOutbounds(result); n != 2 {
			t.Errorf("expected 2 proxy outbounds with no restrictions, got %d", n)
		}
	})

	t.Run("self-allowed-outbounds-dual-role-includes-only-self", func(t *testing.T) {
		node := makeDualRoleNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
			{Protocol: "vless", Port: 10443},
		})
		node.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
		node.Spec.AllowedOutbounds = []string{"node-a"}

		outboundB := makeOutboundNode("node-b", "us")

		input := ClientConfigInput{
			User:            user,
			UserCred:        userCred,
			InboundNodes:    []*proxyv1alpha1.SingBoxNode{node},
			RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
				"node-b": outboundB,
			},
		}

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		tags := collectTags(result)

		if !tags["node-a"] {
			t.Errorf("node-a (self tag) should be present, got tags: %v", tags)
		}
		if tags["node-b#node-a"] {
			t.Errorf("node-b#node-a should be absent (node-b not in AllowedOutbounds), got tags: %v", tags)
		}
		if n := countProxyOutbounds(result); n != 1 {
			t.Errorf("expected 1 proxy outbound (self only), got %d", n)
		}
	})
}

// TestBuildClientConfig_RelayPortMissing verifies that outbound peers without
// a relay port never appear in client configs (no server-side outbound entry
// exists for them), while the same-node direct outbound stays visible.
func TestBuildClientConfig_RelayPortMissing(t *testing.T) {
	const baseUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	withRelay := makeOutboundNode("node-relay", "us")
	withoutRelay := makeOutboundNode("node-norelay", "us")
	withoutRelay.Spec.RelayPort = 0

	user := makeUser("user-alice", "secret-alice")
	userCred := credmanager.UserCredential{UUID: baseUUID}

	t.Run("region peer without relayPort is excluded", func(t *testing.T) {
		input := ClientConfigInput{
			User:            user,
			UserCred:        userCred,
			InboundNodes:    []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
				"node-relay":   withRelay,
				"node-norelay": withoutRelay,
			},
		}

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tags := collectTags(result)
		if !tags["node-relay#node-a"] {
			t.Errorf("node-relay#node-a should be present, got tags: %v", tags)
		}
		if tags["node-norelay#node-a"] {
			t.Errorf("node-norelay#node-a should be absent (no relayPort), got tags: %v", tags)
		}
	})

	t.Run("route target without relayPort is excluded", func(t *testing.T) {
		route := makeCustomRoute("r1", "default", "node-a", "node-norelay")
		input := ClientConfigInput{
			User:            user,
			UserCred:        userCred,
			InboundNodes:    []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{"node-a": {route}},
			OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
				"node-norelay": withoutRelay,
			},
		}

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tags := collectTags(result); tags["node-norelay#node-a"] {
			t.Errorf("node-norelay#node-a should be absent (route target without relayPort), got tags: %v", tags)
		}
	})

	t.Run("self dual-role without relayPort stays visible", func(t *testing.T) {
		self := makeDualRoleNode("node-self", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
			{Protocol: "vless", Port: 10443},
		})
		self.Spec.RelayPort = 0
		self.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

		input := ClientConfigInput{
			User:            user,
			UserCred:        userCred,
			InboundNodes:    []*proxyv1alpha1.SingBoxNode{self},
			RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{"node-self": self},
		}

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tags := collectTags(result); !tags["node-self"] {
			t.Errorf("self entry node-self should be present even without relayPort, got tags: %v", tags)
		}
	})
}
