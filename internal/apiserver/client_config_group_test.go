package apiserver

import (
	"encoding/json"
	"testing"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/credmanager"
)

// Region-based grouping: client config groups are keyed by the inbound
// node's spec.region (unknown regions are omitted), never by spec.tag.

func TestBuildClientConfig_MultiGroup(t *testing.T) {
	// Given: 2 inbound nodes in different regions, each with its own
	// same-region outbound. Both nodes carry Spec.Tag, which must be
	// ignored — groups follow the region.
	inA := makeInboundNode("in-a", "jp", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inA.Spec.Tag = "cdn"
	inA.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	inB := makeInboundNode("in-b", "hk", "5.6.7.8", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inB.Spec.Tag = "ignored"
	inB.Status.EntryEndpoints = []string{"vless:5.6.7.8:10443"}

	outJP := makeOutboundNode("out-jp", "jp")
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
	// + 2 group selectors (hk, jp) + 1 direct = 6
	if len(result) != 5 {
		t.Fatalf("expected 5 items, got %d", len(result))
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

	// Verify group selector for "hk" (index 3, dict-sorted: hk < jp)
	selHK, ok4 := result[2].(map[string]any)
	if !ok4 {
		t.Fatal("result[2] is not a map")
	}
	if selHK["type"] != "selector" {
		t.Errorf("result[2] type expected selector, got %v", selHK["type"])
	}
	if selHK["tag"] != "hk" {
		t.Errorf("result[2] tag expected hk, got %v", selHK["tag"])
	}
	outboundsHK, ok5 := selHK["outbounds"].([]string)
	if !ok5 {
		t.Fatal("result[2] outbounds is not []string")
	}
	if len(outboundsHK) != 1 || outboundsHK[0] != "out-hk#in-b" {
		t.Errorf("hk outbounds expected [out-hk#in-b], got %v", outboundsHK)
	}

	// Verify group selector for "jp" (index 4)
	selJP, ok6 := result[3].(map[string]any)
	if !ok6 {
		t.Fatal("result[3] is not a map")
	}
	if selJP["type"] != "selector" {
		t.Errorf("result[3] type expected selector, got %v", selJP["type"])
	}
	if selJP["tag"] != "jp" {
		t.Errorf("result[3] tag expected jp, got %v", selJP["tag"])
	}
	outboundsJP, ok7 := selJP["outbounds"].([]string)
	if !ok7 {
		t.Fatal("result[3] outbounds is not []string")
	}
	if len(outboundsJP) != 1 || outboundsJP[0] != "out-jp#in-a" {
		t.Errorf("jp outbounds expected [out-jp#in-a], got %v", outboundsJP)
	}

	// Verify direct (index 5)
	direct, ok8 := result[4].(map[string]any)
	if !ok8 {
		t.Fatal("result[4] is not a map")
	}
	if direct["type"] != "direct" {
		t.Errorf("result[4] type expected direct, got %v", direct["type"])
	}
	if direct["tag"] != "direct" {
		t.Errorf("result[4] tag expected direct, got %v", direct["tag"])
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
	if len(result) != 1 {
		t.Fatalf("expected 2 items, got %d", len(result))
	}

	// No empty selector is emitted; only direct remains.
	// Verify direct (index 0)
	direct, ok := result[0].(map[string]any)
	if !ok {
		t.Fatal("result[0] is not a map")
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
	if len(result) != 4 {
		t.Fatalf("expected 4 items, got %d", len(result))
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

	// Verify group selector for "us" (index 3, after proxy selector)
	selUs, ok4 := result[2].(map[string]any)
	if !ok4 {
		t.Fatal("result[2] is not a map")
	}
	if selUs["tag"] != "us" {
		t.Errorf("result[2] tag expected us, got %v", selUs["tag"])
	}
	outboundsUs, ok5 := selUs["outbounds"].([]string)
	if !ok5 {
		t.Fatal("result[2] outbounds is not []string")
	}
	if len(outboundsUs) != 2 {
		t.Fatalf("expected 2 outbounds in us group, got %v", outboundsUs)
	}
	// Both relay tags should appear (sorted)
	if outboundsUs[0] != "out-1#in-a" || outboundsUs[1] != "out-1#in-b" {
		t.Errorf("us outbounds expected [out-1#in-a out-1#in-b], got %v", outboundsUs)
	}

	// Verify direct (index 4)
	direct, ok6 := result[3].(map[string]any)
	if !ok6 {
		t.Fatal("result[3] is not a map")
	}
	if direct["type"] != "direct" {
		t.Errorf("result[3] type expected direct, got %v", direct["type"])
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
	if len(result) != 3 {
		t.Fatalf("expected 3 items, got %d", len(result))
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
	selUs, ok2 := result[1].(map[string]any)
	if !ok2 {
		t.Fatal("result[1] is not a map")
	}
	if selUs["type"] != "selector" {
		t.Errorf("result[1] type expected selector, got %v", selUs["type"])
	}
	if selUs["tag"] != "us" {
		t.Errorf("expected group selector tag 'us', got %v", selUs["tag"])
	}
}

// TestBuildClientConfig_GroupOrderDeterministic: group selectors are ordered by dictionary sort,
// and calling BuildClientConfig twice with the same input produces byte-identical JSON.
func TestBuildClientConfig_GroupOrderDeterministic(t *testing.T) {
	inA := makeInboundNode("in-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inA.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	inB := makeInboundNode("in-b", "hk", "5.6.7.8", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inB.Status.EntryEndpoints = []string{"vless:5.6.7.8:10443"}

	outZ := makeOutboundNode("out-z", "us")
	outA := makeOutboundNode("out-a", "hk")

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

	// Selectors are emitted in stable tag order.
	var selectorTags []string
	for _, ob := range result1 {
		if m, ok := ob.(map[string]any); ok && m["type"] == "selector" {
			selectorTags = append(selectorTags, m["tag"].(string))
		}
	}
	if len(selectorTags) != 2 || selectorTags[0] != "hk" || selectorTags[1] != "us" {
		t.Errorf("selector order = %v, want [hk us]", selectorTags)
	}

}
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
	if len(result) != 6 {
		t.Fatalf("expected 6 items, got %d", len(result))
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
// region is omitted from normalized groups.

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

	if len(result) != 1 {
		t.Fatalf("unknown region should produce direct only, got %d items", len(result))
	}

}
func TestBuildClientConfig_GroupByTargetRegion_CrossRegionRoute(t *testing.T) {
	inbound := makeInboundNode("in-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outJP := makeOutboundNode("out-jp", "jp")
	route := makeCustomRoute("r1", "default", "in-a", "out-jp")

	user := makeUser("user-alice", "secret-alice")
	input := ClientConfigInput{
		User:         user,
		UserCred:     credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes: []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{
			"in-a": {route},
		},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{
			"out-jp": outJP,
		},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("BuildClientConfig returned error: %v", err)
	}

	group := selectorGroupOutbounds(result, "jp")
	if len(group) != 0 {
		t.Errorf("cross-region route must not enter jp selector, got %v", group)
	}
	if g := selectorGroupOutbounds(result, "us"); len(g) != 0 {
		t.Errorf("expected no 'us' group, got %v", g)
	}
}

func TestBuildClientConfig_ClientGroupsAndPhysicalRegion(t *testing.T) {
	in := makeInboundNode("in", "jp", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{{Protocol: "vless", Port: 443}})
	in.Status.EntryEndpoints = []string{"vless:1.2.3.4:443"}
	out := makeOutboundNode("kddi", "jp")
	out.Spec.ClientGroups = []string{"hk", "jp", "us", "ai"}
	result, err := BuildClientConfig(ClientConfigInput{User: makeUser("u", "s"), UserCred: credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}, InboundNodes: []*proxyv1alpha1.SingBoxNode{in}, OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{"kddi": out}})
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"hk", "jp", "us", "ai"} {
		members := selectorGroupOutbounds(result, group)
		if len(members) != 1 || members[0] != "kddi#in" {
			t.Errorf("%s = %v", group, members)
		}
	}
	other := makeInboundNode("hk-in", "hk", "1.2.3.5", []proxyv1alpha1.ProtocolConfig{{Protocol: "vless", Port: 443}})
	other.Status.EntryEndpoints = []string{"vless:1.2.3.5:443"}
	result, err = BuildClientConfig(ClientConfigInput{User: makeUser("u", "s"), UserCred: credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}, InboundNodes: []*proxyv1alpha1.SingBoxNode{other}, OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{"kddi": out}})
	if err != nil {
		t.Fatal(err)
	}
	if got := selectorGroupOutbounds(result, "jp"); len(got) != 0 {
		t.Errorf("jp = %v", got)
	}
}
