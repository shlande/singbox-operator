package apiserver

import (
	"encoding/json"
	"testing"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/credmanager"
)

// Region-based grouping: client config groups are keyed by the inbound
// node's spec.region (falling back to "others"), never by spec.tag.

func TestBuildClientConfig_MultiGroup(t *testing.T) {
	// Given: 2 inbound nodes in different regions, each with its own
	// same-region outbound. Both nodes carry Spec.Tag, which must be
	// ignored — groups follow the region.
	inA := makeInboundNode("in-a", "jp-tokyo", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inA.Spec.Tag = "cdn"
	inA.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	inB := makeInboundNode("in-b", "hk", "5.6.7.8", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inB.Spec.Tag = "ignored"
	inB.Status.EntryEndpoints = []string{"vless:5.6.7.8:10443"}

	outJP := makeOutboundNode("out-jp", "jp-tokyo")
	outHK := makeOutboundNode("out-hk", "hk")

	user := makeUser("user-alice", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.SingBoxNode{inA, inB},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"out-jp": outJP,
			"out-hk": outHK,
		},
	}

	// When
	result, err := BuildClientConfig(input)

	// Then
	if err != nil {
		t.Fatalf("BuildClientConfig returned error: %v", err)
	}

	// 2 proxy outbounds (out-jp#in-a, out-hk#in-b) + 1 proxy selector
	// + 2 group selectors (hk, jp-tokyo) + 1 direct = 6
	if len(result) != 6 {
		t.Fatalf("expected 6 items, got %d", len(result))
	}

	// Verify proxy outbound entries (first 2 items)
	ob0, ok0 := result[0].(map[string]any)
	if !ok0 {
		t.Fatal("result[0] is not a map")
	}
	if ob0["tag"] != "out-jp#in-a" {
		t.Errorf("expected tag out-jp#in-a, got %v", ob0["tag"])
	}

	ob1, ok1 := result[1].(map[string]any)
	if !ok1 {
		t.Fatal("result[1] is not a map")
	}
	if ob1["tag"] != "out-hk#in-b" {
		t.Errorf("expected tag out-hk#in-b, got %v", ob1["tag"])
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
	if len(outboundsProxy) != 2 || outboundsProxy[0] != "hk" || outboundsProxy[1] != "jp-tokyo" {
		t.Errorf("proxy outbounds expected [hk jp-tokyo], got %v", outboundsProxy)
	}

	// Verify group selector for "hk" (index 3, dict-sorted: hk < jp-tokyo)
	selHK, ok4 := result[3].(map[string]any)
	if !ok4 {
		t.Fatal("result[3] is not a map")
	}
	if selHK["type"] != "selector" {
		t.Errorf("result[3] type expected selector, got %v", selHK["type"])
	}
	if selHK["tag"] != "hk" {
		t.Errorf("result[3] tag expected hk, got %v", selHK["tag"])
	}
	outboundsHK, ok5 := selHK["outbounds"].([]string)
	if !ok5 {
		t.Fatal("result[3] outbounds is not []string")
	}
	if len(outboundsHK) != 1 || outboundsHK[0] != "out-hk#in-b" {
		t.Errorf("hk outbounds expected [out-hk#in-b], got %v", outboundsHK)
	}

	// Verify group selector for "jp-tokyo" (index 4)
	selJP, ok6 := result[4].(map[string]any)
	if !ok6 {
		t.Fatal("result[4] is not a map")
	}
	if selJP["type"] != "selector" {
		t.Errorf("result[4] type expected selector, got %v", selJP["type"])
	}
	if selJP["tag"] != "jp-tokyo" {
		t.Errorf("result[4] tag expected jp-tokyo, got %v", selJP["tag"])
	}
	outboundsJP, ok7 := selJP["outbounds"].([]string)
	if !ok7 {
		t.Fatal("result[4] outbounds is not []string")
	}
	if len(outboundsJP) != 1 || outboundsJP[0] != "out-jp#in-a" {
		t.Errorf("jp-tokyo outbounds expected [out-jp#in-a], got %v", outboundsJP)
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
	// Given: 1 inbound node (region "us") whose only outbound candidate is
	// offline, so it produces no proxy outbound and no group.
	inbound := makeInboundNode("in-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
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

// TestBuildClientConfig_RegionGroupMerge: 2 inbound nodes in the same region
// share one region group; their relay entries are merged into it.
func TestBuildClientConfig_RegionGroupMerge(t *testing.T) {
	inA := makeInboundNode("in-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inA.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	inB := makeInboundNode("in-b", "us", "5.6.7.8", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inB.Status.EntryEndpoints = []string{"vless:5.6.7.8:10443"}

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

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("BuildClientConfig returned error: %v", err)
	}

	// 2 proxy outbounds + proxy selector + 1 group selector (us) + direct = 5
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
	if ob1["tag"] != "out-1#in-b" {
		t.Errorf("expected tag out-1#in-b, got %v", ob1["tag"])
	}

	// Verify proxy selector (index 2, appears before group selectors)
	selProxy, ok2 := result[2].(map[string]any)
	if !ok2 {
		t.Fatal("result[2] is not a map")
	}
	outboundsProxy, ok3 := selProxy["outbounds"].([]string)
	if !ok3 {
		t.Fatal("result[2] outbounds is not []string")
	}
	if len(outboundsProxy) != 1 || outboundsProxy[0] != "us" {
		t.Errorf("proxy outbounds expected [us], got %v", outboundsProxy)
	}

	// Verify group selector for "us" (index 3, after proxy selector)
	selUs, ok4 := result[3].(map[string]any)
	if !ok4 {
		t.Fatal("result[3] is not a map")
	}
	if selUs["tag"] != "us" {
		t.Errorf("result[3] tag expected us, got %v", selUs["tag"])
	}
	outboundsUs, ok5 := selUs["outbounds"].([]string)
	if !ok5 {
		t.Fatal("result[3] outbounds is not []string")
	}
	if len(outboundsUs) != 2 {
		t.Fatalf("expected 2 outbounds in us group, got %v", outboundsUs)
	}
	// Both relay tags should appear (sorted)
	if outboundsUs[0] != "out-1#in-a" || outboundsUs[1] != "out-1#in-b" {
		t.Errorf("us outbounds expected [out-1#in-a out-1#in-b], got %v", outboundsUs)
	}

	// Verify direct (index 4)
	direct, ok6 := result[4].(map[string]any)
	if !ok6 {
		t.Fatal("result[4] is not a map")
	}
	if direct["type"] != "direct" {
		t.Errorf("result[4] type expected direct, got %v", direct["type"])
	}
}

// TestBuildClientConfig_TagIgnoredRegionGrouping: Spec.Tag on both inbound
// and outbound nodes is irrelevant for grouping — only region counts.
func TestBuildClientConfig_TagIgnoredRegionGrouping(t *testing.T) {
	inbound := makeInboundNode("in-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Spec.Tag = "cdn" // must be ignored
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("out-1", "us")
	outbound.Spec.Tag = "cdn" // must be ignored as well

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

	// 1 proxy outbound + proxy selector + 1 group selector (us) + direct = 4
	if len(result) != 4 {
		t.Fatalf("expected 4 items, got %d", len(result))
	}

	// Verify proxy outbound tag is correct
	ob0, ok0 := result[0].(map[string]any)
	if !ok0 {
		t.Fatal("result[0] is not a map")
	}
	if ob0["tag"] != "out-1#in-a" {
		t.Errorf("expected tag out-1#in-a, got %v", ob0["tag"])
	}

	// Verify group selector is keyed by region "us" (index 2), not by tag "cdn"
	selUs, ok2 := result[2].(map[string]any)
	if !ok2 {
		t.Fatal("result[2] is not a map")
	}
	if selUs["type"] != "selector" {
		t.Errorf("result[2] type expected selector, got %v", selUs["type"])
	}
	if selUs["tag"] != "us" {
		t.Errorf("expected group selector tag 'us', got %v", selUs["tag"])
	}
}

// TestBuildClientConfig_GroupOrderDeterministic: group selectors are ordered by dictionary sort,
// and calling BuildClientConfig twice with the same input produces byte-identical JSON.
func TestBuildClientConfig_GroupOrderDeterministic(t *testing.T) {
	inA := makeInboundNode("in-a", "zebra", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inA.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	inB := makeInboundNode("in-b", "apple", "5.6.7.8", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inB.Status.EntryEndpoints = []string{"vless:5.6.7.8:10443"}

	outZ := makeOutboundNode("out-z", "zebra")
	outA := makeOutboundNode("out-a", "apple")

	user := makeUser("user-alice", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.SingBoxNode{inA, inB},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"out-z": outZ,
			"out-a": outA,
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
	// Two dual-role nodes in the same region, both carrying a Spec.Tag that
	// must be ignored. Each discovers the other as a relay target, so the
	// single "us" group must contain 4 unique entries.
	nodeA := makeDualRoleNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	nodeA.Spec.Tag = "cdn"
	nodeA.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	nodeB := makeDualRoleNode("node-b", "us", "5.6.7.8", []proxyv1alpha1.ProtocolConfig{
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

	// 4 proxy outbounds (node-a, node-b#node-a, node-b, node-a#node-b)
	// + proxy selector + 1 group selector (us) + direct = 7
	if len(result) != 7 {
		t.Fatalf("expected 7 items, got %d", len(result))
	}

	// Find the "us" group selector and verify no duplicates
	var usOutbounds []string
	for _, ob := range result {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "selector" && m["tag"] == "us" {
			usOutbounds, _ = m["outbounds"].([]string)
		}
	}

	if len(usOutbounds) != 4 {
		t.Fatalf("expected 4 outbounds in us group, got %v", usOutbounds)
	}

	// Verify no duplicates
	seen := make(map[string]bool)
	for _, ob := range usOutbounds {
		if seen[ob] {
			t.Errorf("duplicate outbound tag %q in us group", ob)
		}
		seen[ob] = true
	}
}

// TestBuildClientConfig_EmptyRegionFallsBackToOthers: an inbound node with no
// region still lands in the "others" group.
func TestBuildClientConfig_EmptyRegionFallsBackToOthers(t *testing.T) {
	inbound := makeInboundNode("in-a", "", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	// The relay candidate must share the inbound's region ("" here) to be
	// discovered; use a CustomRoute pin instead to keep it deterministic.
	outbound := makeOutboundNode("out-1", "")

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

	found := false
	for _, ob := range result {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "selector" && m["tag"] == "others" {
			found = true
			outbounds, _ := m["outbounds"].([]string)
			if len(outbounds) != 1 || outbounds[0] != "out-1#in-a" {
				t.Errorf("others outbounds expected [out-1#in-a], got %v", outbounds)
			}
		}
	}
	if !found {
		t.Error("expected an 'others' group selector for region-less inbound")
	}
}
