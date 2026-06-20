package apiserver

import (
	"testing"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/credmanager"
)

// collectTags extracts all tag strings from the result outbounds.
func collectTags(result []any) map[string]bool {
	tags := make(map[string]bool)
	for _, ob := range result {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if tag, _ := m["tag"].(string); tag != "" {
			tags[tag] = true
		}
	}
	return tags
}

// countProxyOutbounds returns the number of proxy-type (non-selector, non-direct) outbounds.
func countProxyOutbounds(result []any) int {
	n := 0
	for _, ob := range result {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ != "selector" && typ != "direct" {
			n++
		}
	}
	return n
}

func setupBasicInput(inbound *proxyv1alpha1.SingBoxNode, outbound *proxyv1alpha1.SingBoxNode, offlineNames map[string]bool) ClientConfigInput {
	return ClientConfigInput{
		User:             makeUser("user-alice", "secret-alice"),
		UserCred:         credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:     []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound:  map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName:  map[string]*proxyv1alpha1.SingBoxNode{outbound.Name: outbound},
		OfflineNodeNames: offlineNames,
	}
}

// Test 1: Healthy node includes outbound in result.
func TestNodeReadiness_HealthyNode_IncludesOutbound(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
	outbound := makeOutboundNode("node-b", "us")

	input := setupBasicInput(inbound, outbound, map[string]bool{})

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tags := collectTags(result)
	if !tags["node-b#node-a"] {
		t.Errorf("tag 'node-b#node-a' not found in result, got tags: %v", tags)
	}
}

// Test 2: Node marked offline excludes its outbound.
func TestNodeReadiness_NodeNotReady_ExcludesOutbound(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
	outbound := makeOutboundNode("node-b", "us")

	input := setupBasicInput(inbound, outbound, map[string]bool{"node-b": true})

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tags := collectTags(result)
	if tags["node-b#node-a"] {
		t.Errorf("tag 'node-b#node-a' should not be in result when node-b is offline")
	}
	if len(result) != 2 {
		t.Errorf("expected exactly 2 items (selector + direct), got %d", len(result))
	}
}

// Test 3: Node recovery re-includes outbound.
func TestNodeReadiness_NodeRecovery_ReincludesOutbound(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
	outbound := makeOutboundNode("node-b", "us")

	inputOffline := setupBasicInput(inbound, outbound, map[string]bool{"node-b": true})
	resultOffline, err := BuildClientConfig(inputOffline)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tagsOffline := collectTags(resultOffline)
	if tagsOffline["node-b#node-a"] {
		t.Errorf("offline result should not contain node-b#node-a")
	}

	inputRecovered := setupBasicInput(inbound, outbound, map[string]bool{})
	resultRecovered, err := BuildClientConfig(inputRecovered)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tagsRecovered := collectTags(resultRecovered)
	if !tagsRecovered["node-b#node-a"] {
		t.Errorf("recovered result should contain node-b#node-a")
	}

	if len(resultOffline) == len(resultRecovered) {
		t.Errorf("results should differ after node recovery")
	}
}

// Test 4: Annotation marks node offline even though it is structurally "Ready".
func TestNodeReadiness_AnnotationOffline_ExcludesDespiteReady(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
	outbound := makeOutboundNode("node-b", "us")

	input := setupBasicInput(inbound, outbound, map[string]bool{"node-b": true})

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tags := collectTags(result)
	if tags["node-b#node-a"] {
		t.Errorf("tag 'node-b#node-a' should not be in result when annotation marks node-b offline")
	}
}

// Test 5: Annotation removal re-includes the node.
func TestNodeReadiness_AnnotationRemoval_Reincludes(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
	outbound := makeOutboundNode("node-b", "us")

	inputOffline := setupBasicInput(inbound, outbound, map[string]bool{"node-b": true})
	resultOffline, err := BuildClientConfig(inputOffline)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if collectTags(resultOffline)["node-b#node-a"] {
		t.Errorf("offline result should not contain node-b#node-a")
	}

	inputOnline := setupBasicInput(inbound, outbound, map[string]bool{})
	resultOnline, err := BuildClientConfig(inputOnline)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !collectTags(resultOnline)["node-b#node-a"] {
		t.Errorf("re-included result should contain node-b#node-a")
	}

	if len(resultOffline) == len(resultOnline) {
		t.Errorf("results should differ after annotation removal")
	}
}

// Test 6: Deleted node (not ready) is excluded from outbounds.
func TestNodeReadiness_NodeDeleted_Excludes(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
	outbound := makeOutboundNode("node-b", "us")

	input := setupBasicInput(inbound, outbound, map[string]bool{"node-b": true})

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tags := collectTags(result)
	if tags["node-b#node-a"] {
		t.Errorf("tag 'node-b#node-a' should not be in result when node-b is deleted")
	}
}

// Test 7: Multiple unhealthy nodes; only healthy ones included.
func TestNodeReadiness_MultipleUnhealthyNodes_AllExcluded(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound1 := makeOutboundNode("node-b1", "us")
	outbound2 := makeOutboundNode("node-b2", "us")
	outbound3 := makeOutboundNode("node-b3", "us")

	input := ClientConfigInput{
		User:            makeUser("user-alice", "secret-alice"),
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"node-b1": outbound1,
			"node-b2": outbound2,
			"node-b3": outbound3,
		},
		OfflineNodeNames: map[string]bool{
			"node-b1": true,
			"node-b2": true,
		},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tags := collectTags(result)

	if tags["node-b1#node-a"] {
		t.Errorf("tag 'node-b1#node-a' should be excluded (node-b1 is offline)")
	}
	if tags["node-b2#node-a"] {
		t.Errorf("tag 'node-b2#node-a' should be excluded (node-b2 is offline)")
	}
	if !tags["node-b3#node-a"] {
		t.Errorf("tag 'node-b3#node-a' should be included (node-b3 is healthy)")
	}
	if len(result) != 3 {
		t.Errorf("expected exactly 3 items (1 proxy + selector + direct), got %d", len(result))
	}
	if n := countProxyOutbounds(result); n != 1 {
		t.Errorf("expected 1 proxy outbound, got %d", n)
	}
}

// Test 8: CustomRoute to an unhealthy outbound node excludes it.
func TestNodeReadiness_CustomRouteUnhealthy_Excluded(t *testing.T) {
	inbound := makeInboundNode("node-a", "eu", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
	outbound := makeOutboundNode("node-b", "us")

	customRoute := makeCustomRoute("route-a-b", "default", "node-a", "node-b")

	input := ClientConfigInput{
		User:         makeUser("user-alice", "secret-alice"),
		UserCred:     credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes: []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{
			"node-a": {customRoute},
		},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"node-b": outbound,
		},
		OfflineNodeNames: map[string]bool{"node-b": true},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tags := collectTags(result)
	if tags["node-b#node-a"] {
		t.Errorf("tag 'node-b#node-a' should not be in result when CustomRoute outbound node-b is offline")
	}
}

// Test 9: Dual-role node unhealthy excludes itself from outbounds.
func TestNodeReadiness_DualRoleNodeUnhealthy_ExcludesSelf(t *testing.T) {
	nodeX := makeDualRoleNode("node-x", "ap", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	nodeX.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	input := ClientConfigInput{
		User:            makeUser("user-alice", "secret-alice"),
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.SingBoxNode{nodeX},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"node-x": nodeX,
		},
		OfflineNodeNames: map[string]bool{"node-x": true},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tags := collectTags(result)
	if tags["node-x"] {
		t.Errorf("tag 'node-x' should not be in result when dual-role node-x is offline")
	}
	if tags["node-x#node-x"] {
		t.Errorf("tag 'node-x#node-x' should not be in result")
	}

	if len(result) != 2 {
		t.Errorf("expected exactly 2 items (selector + direct), got %d", len(result))
	}
	if n := countProxyOutbounds(result); n != 0 {
		t.Errorf("expected 0 proxy outbounds, got %d", n)
	}
}
