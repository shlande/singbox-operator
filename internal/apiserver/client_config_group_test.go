package apiserver

import (
	"encoding/json"
	"testing"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/credmanager"
)

func TestBuildClientConfig_MultiGroup(t *testing.T) {
	// Given: 2 inbound nodes in the same region with different tags
	inA := makeInboundNode("in-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inA.Spec.Tag = "cdn"
	inA.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	inB := makeInboundNode("in-b", "us", "5.6.7.8", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inB.Spec.Tag = "us"
	inB.Status.EntryEndpoints = []string{"vless:5.6.7.8:10443"}

	// Given: 1 shared outbound node
	out1 := makeOutboundNode("out-1", "us")

	user := makeUser("user-alice", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.SingBoxNode{inA, inB},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"out-1": out1,
		},
	}

	// When
	result, err := BuildClientConfig(input)

	// Then
	if err != nil {
		t.Fatalf("BuildClientConfig returned error: %v", err)
	}

	// 2 proxy outbounds (out-1#in-a, out-1#in-b) + 1 proxy selector + 2 group selectors (cdn, us) + 1 direct = 6
	if len(result) != 6 {
		t.Fatalf("expected 6 items, got %d", len(result))
	}

	// Verify proxy outbound entries (first 2 items)
	ob0, ok0 := result[0].(map[string]any)
	if !ok0 {
		t.Fatal("result[0] is not a map")
	}
	if ob0["tag"] != "out-1#in-a" {
		t.Errorf("expected tag out-1#in-a, got %v", ob0["tag"])
	}

	ob1, ok1 := result[1].(map[string]any)
	if !ok1 {
		t.Fatal("result[1] is not a map")
	}
	if ob1["tag"] != "out-1#in-b" {
		t.Errorf("expected tag out-1#in-b, got %v", ob1["tag"])
	}

	// Verify top-level proxy selector (index 2, appears before group selectors)
	selProxy, ok2 := result[2].(map[string]any)
	if !ok2 {
		t.Fatal("result[2] is not a map")
	}
	if selProxy["type"] != "selector" {
		t.Errorf("result[2] type expected selector, got %v", selProxy["type"])
	}
	if selProxy["tag"] != "proxy" {
		t.Errorf("result[2] tag expected proxy, got %v", selProxy["tag"])
	}
	outboundsProxy, ok3 := selProxy["outbounds"].([]string)
	if !ok3 {
		t.Fatal("result[2] outbounds is not []string")
	}
	if len(outboundsProxy) != 2 || outboundsProxy[0] != "cdn" || outboundsProxy[1] != "us" {
		t.Errorf("proxy outbounds expected [cdn us], got %v", outboundsProxy)
	}

	// Verify group selector for "cdn" (index 3, dict-sorted: cdn < us)
	selCdn, ok4 := result[3].(map[string]any)
	if !ok4 {
		t.Fatal("result[3] is not a map")
	}
	if selCdn["type"] != "selector" {
		t.Errorf("result[3] type expected selector, got %v", selCdn["type"])
	}
	if selCdn["tag"] != "cdn" {
		t.Errorf("result[3] tag expected cdn, got %v", selCdn["tag"])
	}
	outboundsCdn, ok5 := selCdn["outbounds"].([]string)
	if !ok5 {
		t.Fatal("result[3] outbounds is not []string")
	}
	if len(outboundsCdn) != 1 || outboundsCdn[0] != "out-1#in-a" {
		t.Errorf("cdn outbounds expected [out-1#in-a], got %v", outboundsCdn)
	}

	// Verify group selector for "us" (index 4)
	selUs, ok6 := result[4].(map[string]any)
	if !ok6 {
		t.Fatal("result[4] is not a map")
	}
	if selUs["type"] != "selector" {
		t.Errorf("result[4] type expected selector, got %v", selUs["type"])
	}
	if selUs["tag"] != "us" {
		t.Errorf("result[4] tag expected us, got %v", selUs["tag"])
	}
	outboundsUs, ok7 := selUs["outbounds"].([]string)
	if !ok7 {
		t.Fatal("result[4] outbounds is not []string")
	}
	if len(outboundsUs) != 1 || outboundsUs[0] != "out-1#in-b" {
		t.Errorf("us outbounds expected [out-1#in-b], got %v", outboundsUs)
	}

	// Verify direct (index 5)
	direct, ok8 := result[5].(map[string]any)
	if !ok8 {
		t.Fatal("result[5] is not a map")
	}
	if direct["type"] != "direct" {
		t.Errorf("result[5] type expected direct, got %v", direct["type"])
	}
	if direct["tag"] != "direct" {
		t.Errorf("result[5] tag expected direct, got %v", direct["tag"])
	}
}

func TestBuildClientConfig_EmptyGroupNotEmitted(t *testing.T) {
	// Given: 1 inbound node with no tag (→ "others" group), 1 outbound that is offline
	inbound := makeInboundNode("in-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	// No Spec.Tag set → empty string → "others"
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("out-1", "us")

	user := makeUser("user-alice", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"out-1": outbound,
		},
		// The outbound is offline, so resolveOutboundNodes skips it
		OfflineNodeNames: map[string]bool{"out-1": true},
	}

	// When
	result, err := BuildClientConfig(input)

	// Then
	if err != nil {
		t.Fatalf("BuildClientConfig returned error: %v", err)
	}

	// 0 proxy outbounds (offline outbound) + 0 group selectors (empty group) + 1 proxy selector + 1 direct = 2
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result))
	}

	// Verify top-level proxy selector (index 0) with empty outbounds
	selProxy, ok := result[0].(map[string]any)
	if !ok {
		t.Fatal("result[0] is not a map")
	}
	if selProxy["type"] != "selector" {
		t.Errorf("result[0] type expected selector, got %v", selProxy["type"])
	}
	if selProxy["tag"] != "proxy" {
		t.Errorf("result[0] tag expected proxy, got %v", selProxy["tag"])
	}
	outboundsProxy, ok := selProxy["outbounds"].([]string)
	if !ok {
		t.Fatal("result[0] outbounds is not []string")
	}
	if len(outboundsProxy) != 0 {
		t.Errorf("proxy outbounds expected empty slice, got %v", outboundsProxy)
	}

	// Verify direct (index 1)
	direct, ok := result[1].(map[string]any)
	if !ok {
		t.Fatal("result[1] is not a map")
	}
	if direct["type"] != "direct" {
		t.Errorf("result[1] type expected direct, got %v", direct["type"])
	}
	if direct["tag"] != "direct" {
		t.Errorf("result[1] tag expected direct, got %v", direct["tag"])
	}
}

// TestBuildClientConfig_DefaultGroupMerge: 2 inbound nodes both with no tag (both go to "others" group),
// each with its own outbound, different regions so no cross-discovery. Assert merged group selector.
func TestBuildClientConfig_DefaultGroupMerge(t *testing.T) {
	inA := makeInboundNode("in-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inA.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	inB := makeInboundNode("in-b", "eu", "5.6.7.8", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inB.Status.EntryEndpoints = []string{"vless:5.6.7.8:10443"}

	// Each region has its own outbound, so no cross-discovery
	out1 := makeOutboundNode("out-1", "us")
	out2 := makeOutboundNode("out-2", "eu")

	user := makeUser("user-alice", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.SingBoxNode{inA, inB},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"out-1": out1,
			"out-2": out2,
		},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("BuildClientConfig returned error: %v", err)
	}

	// 2 proxy outbounds + proxy selector + 1 group selector (others) + direct = 5
	if len(result) != 5 {
		t.Fatalf("expected 5 items, got %d", len(result))
	}

	// Verify proxy outbound entries (first 2 items)
	ob0, ok0 := result[0].(map[string]any)
	if !ok0 {
		t.Fatal("result[0] is not a map")
	}
	if ob0["tag"] != "out-1#in-a" {
		t.Errorf("expected tag out-1#in-a, got %v", ob0["tag"])
	}
	ob1, ok1 := result[1].(map[string]any)
	if !ok1 {
		t.Fatal("result[1] is not a map")
	}
	if ob1["tag"] != "out-2#in-b" {
		t.Errorf("expected tag out-2#in-b, got %v", ob1["tag"])
	}

	// Verify proxy selector (index 2, appears before group selectors)
	selProxy, ok2 := result[2].(map[string]any)
	if !ok2 {
		t.Fatal("result[2] is not a map")
	}
	if selProxy["type"] != "selector" {
		t.Errorf("result[2] type expected selector, got %v", selProxy["type"])
	}
	if selProxy["tag"] != "proxy" {
		t.Errorf("result[2] tag expected proxy, got %v", selProxy["tag"])
	}
	outboundsProxy, ok3 := selProxy["outbounds"].([]string)
	if !ok3 {
		t.Fatal("result[2] outbounds is not []string")
	}
	if len(outboundsProxy) != 1 || outboundsProxy[0] != "others" {
		t.Errorf("proxy outbounds expected [others], got %v", outboundsProxy)
	}

	// Verify group selector for "others" (index 3, after proxy selector)
	selOthers, ok4 := result[3].(map[string]any)
	if !ok4 {
		t.Fatal("result[3] is not a map")
	}
	if selOthers["type"] != "selector" {
		t.Errorf("result[3] type expected selector, got %v", selOthers["type"])
	}
	if selOthers["tag"] != "others" {
		t.Errorf("result[3] tag expected others, got %v", selOthers["tag"])
	}
	outboundsOthers, ok5 := selOthers["outbounds"].([]string)
	if !ok5 {
		t.Fatal("result[3] outbounds is not []string")
	}
	if len(outboundsOthers) != 2 {
		t.Fatalf("expected 2 outbounds in others group, got %v", outboundsOthers)
	}
	// Both outbound tags should appear (sorted)
	if outboundsOthers[0] != "out-1#in-a" || outboundsOthers[1] != "out-2#in-b" {
		t.Errorf("others outbounds expected [out-1#in-a out-2#in-b], got %v", outboundsOthers)
	}

	// Verify direct (index 4)
	direct, ok6 := result[4].(map[string]any)
	if !ok6 {
		t.Fatal("result[4] is not a map")
	}
	if direct["type"] != "direct" {
		t.Errorf("result[4] type expected direct, got %v", direct["type"])
	}
	if direct["tag"] != "direct" {
		t.Errorf("result[4] tag expected direct, got %v", direct["tag"])
	}
}

// TestBuildClientConfig_TagOnOutboundIgnored: outbound node's own Spec.Tag is irrelevant.
// Only the inbound's tag matters for grouping.
func TestBuildClientConfig_TagOnOutboundIgnored(t *testing.T) {
	inbound := makeInboundNode("in-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("out-1", "us")
	outbound.Spec.Tag = "cdn" // outbound tag should be irrelevant for grouping

	user := makeUser("user-alice", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"out-1": outbound,
		},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("BuildClientConfig returned error: %v", err)
	}

	// 1 proxy outbound + proxy selector + 1 group selector (others) + direct = 4
	if len(result) != 4 {
		t.Fatalf("expected 4 items, got %d", len(result))
	}

	// Verify proxy outbound tag is correct (outbound's own tag does not affect outbound tag format)
	ob0, ok0 := result[0].(map[string]any)
	if !ok0 {
		t.Fatal("result[0] is not a map")
	}
	if ob0["tag"] != "out-1#in-a" {
		t.Errorf("expected tag out-1#in-a, got %v", ob0["tag"])
	}

	// Verify proxy selector (index 1, before group selectors)
	selProxy, ok1 := result[1].(map[string]any)
	if !ok1 {
		t.Fatal("result[1] is not a map")
	}
	if selProxy["type"] != "selector" {
		t.Errorf("result[1] type expected selector, got %v", selProxy["type"])
	}
	if selProxy["tag"] != "proxy" {
		t.Errorf("result[1] tag expected proxy, got %v", selProxy["tag"])
	}

	// Verify group selector is "others" (index 2)
	selOthers, ok2 := result[2].(map[string]any)
	if !ok2 {
		t.Fatal("result[2] is not a map")
	}
	if selOthers["type"] != "selector" {
		t.Errorf("result[2] type expected selector, got %v", selOthers["type"])
	}
	if selOthers["tag"] != "others" {
		t.Errorf("expected group selector tag 'others', got %v", selOthers["tag"])
	}
}

// TestBuildClientConfig_GroupOrderDeterministic: group selectors are ordered by dictionary sort,
// and calling BuildClientConfig twice with the same input produces byte-identical JSON.
func TestBuildClientConfig_GroupOrderDeterministic(t *testing.T) {
	inA := makeInboundNode("in-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inA.Spec.Tag = "zebra"
	inA.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	inB := makeInboundNode("in-b", "us", "5.6.7.8", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inB.Spec.Tag = "apple"
	inB.Status.EntryEndpoints = []string{"vless:5.6.7.8:10443"}

	out := makeOutboundNode("out-1", "us")

	user := makeUser("user-alice", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.SingBoxNode{inA, inB},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"out-1": out,
		},
	}

	result1, err1 := BuildClientConfig(input)
	if err1 != nil {
		t.Fatalf("first call returned error: %v", err1)
	}
	result2, err2 := BuildClientConfig(input)
	if err2 != nil {
		t.Fatalf("second call returned error: %v", err2)
	}

	// Marshal both to JSON
	json1, err1 := json.Marshal(result1)
	if err1 != nil {
		t.Fatalf("marshal result1: %v", err1)
	}
	json2, err2 := json.Marshal(result2)
	if err2 != nil {
		t.Fatalf("marshal result2: %v", err2)
	}

	// Bytes must be equal (deterministic)
	if len(json1) != len(json2) {
		t.Errorf("result lengths differ: %d vs %d", len(json1), len(json2))
	}
	for i := range json1 {
		if json1[i] != json2[i] {
			t.Errorf("results differ at byte %d: %d vs %d", i, json1[i], json2[i])
			break
		}
	}

	// Verify proxy selector order is dictionary order [apple, zebra]
	var proxyOutbounds []string
	for _, ob := range result1 {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "selector" && m["tag"] == "proxy" {
			proxyOutbounds, _ = m["outbounds"].([]string)
		}
	}
	if len(proxyOutbounds) != 2 || proxyOutbounds[0] != "apple" || proxyOutbounds[1] != "zebra" {
		t.Errorf("proxy outbounds expected [apple zebra] (dict order), got %v", proxyOutbounds)
	}
}

// TestBuildClientConfig_GroupDedup: group outbounds never contain duplicates.
func TestBuildClientConfig_GroupDedup(t *testing.T) {
	// Use DIFFERENT regions so dual-role nodes only discover themselves (no cross-discovery)
	// Each produces exactly 1 self-outbound, both in the "cdn" group
	nodeA := makeDualRoleNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	nodeA.Spec.Tag = "cdn"
	nodeA.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	nodeB := makeDualRoleNode("node-b", "eu", "5.6.7.8", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	nodeB.Spec.Tag = "cdn"
	nodeB.Status.EntryEndpoints = []string{"vless:5.6.7.8:10443"}

	user := makeUser("user-alice", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.SingBoxNode{nodeA, nodeB},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"node-a": nodeA,
			"node-b": nodeB,
		},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("BuildClientConfig returned error: %v", err)
	}

	// 2 proxy outbounds (node-a, node-b self-outbounds) + 1 group selector (cdn) + proxy selector + direct = 5
	if len(result) != 5 {
		t.Fatalf("expected 5 items, got %d", len(result))
	}

	// Find the "cdn" group selector and verify no duplicates
	var cdnOutbounds []string
	for _, ob := range result {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "selector" && m["tag"] == "cdn" {
			cdnOutbounds, _ = m["outbounds"].([]string)
		}
	}

	if len(cdnOutbounds) != 2 {
		t.Fatalf("expected 2 outbounds in cdn group, got %v", cdnOutbounds)
	}

	// Verify no duplicates: the two self-outbounds should be node-a and node-b
	seen := make(map[string]bool)
	for _, ob := range cdnOutbounds {
		if seen[ob] {
			t.Errorf("duplicate outbound tag %q in cdn group", ob)
		}
		seen[ob] = true
	}
}
