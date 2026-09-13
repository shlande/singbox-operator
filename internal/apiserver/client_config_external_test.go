package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
	"github.com/shlande/singbox-operator/internal/credmanager"
)

func makeExternalOutbound(name, region string) *proxyv1alpha1.ExternalOutbound {
	return &proxyv1alpha1.ExternalOutbound{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: proxyv1alpha1.ExternalOutboundSpec{
			Protocol: proxyv1alpha1.ExternalProtocolTrojan,
			Server:   "198.51.100.77",
			Port:     54321,
			Region:   region,
		},
	}
}

func makeExternalRoute(name, namespace, inbound, outbound string) *proxyv1alpha1.CustomRoute {
	return &proxyv1alpha1.CustomRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: proxyv1alpha1.CustomRouteSpec{
			InboundNode:  inbound,
			OutboundNode: outbound,
			OutboundKind: proxyv1alpha1.OutboundKindExternalOutbound,
		},
	}
}

// selectorGroupOutbounds returns the outbound tags of the group selector with
// the given tag, or nil when no such selector exists.
func selectorGroupOutbounds(result []any, group string) []string {
	for _, ob := range result {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "selector" && m["tag"] == group {
			arr, _ := m["outbounds"].([]string)
			return arr
		}
	}
	return nil
}

// Test 1: Same-region ExternalOutbound is auto-discovered and derives
// credentials from its name like an outbound SingBoxNode.
func TestBuildClientConfig_ExternalOutbound_RegionMatch(t *testing.T) {
	const baseUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	ext := makeExternalOutbound("ext-1", "us")

	input := ClientConfigInput{
		User:                    makeUser("user-alice", "secret-alice"),
		UserCred:                credmanager.UserCredential{UUID: baseUUID},
		InboundNodes:            []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound:         map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName:         map[string]*proxyv1alpha1.SingBoxNode{},
		ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{"ext-1": ext},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1 proxy + others group selector + proxy selector + direct = 4
	if len(result) != 4 {
		t.Fatalf("expected 4 items, got %d", len(result))
	}

	tags := collectTags(result)
	if !tags["ext-1#node-a"] {
		t.Errorf("tag 'ext-1#node-a' not found in result, got tags: %v", tags)
	}

	if group := selectorGroupOutbounds(result, "us"); len(group) != 1 || group[0] != "ext-1#node-a" {
		t.Errorf("selector(others).outbounds should be [\"ext-1#node-a\"], got %v", group)
	}

	expectedUUID := configengine.DeriveUUID(baseUUID, "ext-1")
	var foundUUID string
	for _, ob := range result {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == protoVless {
			foundUUID, _ = m["uuid"].(string)
		}
	}
	if foundUUID != expectedUUID {
		t.Errorf("expected derived UUID %q, got %q", expectedUUID, foundUUID)
	}
}

// Test 2: Region auto-discovery does not apply when the ExternalOutbound
// region is empty or differs from the inbound region.
func TestBuildClientConfig_ExternalOutbound_RegionMismatchOrEmpty(t *testing.T) {
	const baseUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	newInput := func(inboundRegion string, ext *proxyv1alpha1.ExternalOutbound) ClientConfigInput {
		inbound := makeInboundNode("node-a", inboundRegion, "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
			{Protocol: "vless", Port: 10443},
		})
		inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
		return ClientConfigInput{
			User:                    makeUser("user-alice", "secret-alice"),
			UserCred:                credmanager.UserCredential{UUID: baseUUID},
			InboundNodes:            []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound:         map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName:         map[string]*proxyv1alpha1.SingBoxNode{},
			ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{ext.Name: ext},
		}
	}

	t.Run("region mismatch excludes external outbound", func(t *testing.T) {
		input := newInput("us", makeExternalOutbound("ext-1", "eu"))

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if collectTags(result)["ext-1#node-a"] {
			t.Errorf("tag 'ext-1#node-a' should be absent when regions mismatch")
		}
		if n := countProxyOutbounds(result); n != 0 {
			t.Errorf("expected 0 proxy outbounds, got %d", n)
		}
	})

	t.Run("empty external region never auto-discovers", func(t *testing.T) {
		input := newInput("us", makeExternalOutbound("ext-1", ""))

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if collectTags(result)["ext-1#node-a"] {
			t.Errorf("tag 'ext-1#node-a' should be absent when external region is empty")
		}
		if n := countProxyOutbounds(result); n != 0 {
			t.Errorf("expected 0 proxy outbounds, got %d", n)
		}
	})

	t.Run("both regions empty still does not auto-discover", func(t *testing.T) {
		input := newInput("", makeExternalOutbound("ext-1", ""))

		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if collectTags(result)["ext-1#node-a"] {
			t.Errorf("tag 'ext-1#node-a' should be absent when both regions are empty")
		}
		if n := countProxyOutbounds(result); n != 0 {
			t.Errorf("expected 0 proxy outbounds, got %d", n)
		}
	})
}

// Test 3: A CustomRoute with outboundKind=ExternalOutbound binds the external
// outbound even when its region does not match.
func TestBuildClientConfig_ExternalOutbound_RouteBound(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	// Region-only-empty external outbound: reachable only via explicit route.
	ext := makeExternalOutbound("ext-1", "")
	route := makeExternalRoute("route-a-ext", "default", "node-a", "ext-1")

	input := ClientConfigInput{
		User:         makeUser("user-alice", "secret-alice"),
		UserCred:     credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes: []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{
			"node-a": {route},
		},
		OutboundsByName:         map[string]*proxyv1alpha1.SingBoxNode{},
		ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{"ext-1": ext},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1 proxy + others group selector + proxy selector + direct = 4
	if len(result) != 4 {
		t.Fatalf("expected 4 items, got %d", len(result))
	}

	tags := collectTags(result)
	if !tags["ext-1#node-a"] {
		t.Errorf("tag 'ext-1#node-a' not found in result, got tags: %v", tags)
	}
	// Empty-region targets group under "others" (target-region scheme).
	if group := selectorGroupOutbounds(result, "others"); len(group) != 1 || group[0] != "ext-1#node-a" {
		t.Errorf("selector(others).outbounds should be [\"ext-1#node-a\"], got %v", group)
	}
}

// Test 4: A CustomRoute with the default (SingBoxNode) outbound kind must not
// resolve names in the ExternalOutbound map.
func TestBuildClientConfig_ExternalOutbound_RouteKindSingBoxNodeIgnoresExternal(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	// makeCustomRoute leaves OutboundKind unset → defaults to SingBoxNode.
	route := makeCustomRoute("route-a-ext", "default", "node-a", "ext-1")

	input := ClientConfigInput{
		User:         makeUser("user-alice", "secret-alice"),
		UserCred:     credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes: []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{
			"node-a": {route},
		},
		OutboundsByName:         map[string]*proxyv1alpha1.SingBoxNode{},
		ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{"ext-1": makeExternalOutbound("ext-1", "")},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if collectTags(result)["ext-1#node-a"] {
		t.Errorf("tag 'ext-1#node-a' should be absent when route kind is SingBoxNode")
	}
	if len(result) != 2 {
		t.Errorf("expected 2 items (selector + direct), got %d", len(result))
	}
}

// Test 5: Gating parity — AllowedInbounds, inbound AllowedOutbounds and
// UserGroup allow/deny lists apply to ExternalOutbound names exactly like
// outbound SingBoxNode names.
func TestBuildClientConfig_ExternalOutbound_Gating(t *testing.T) {
	const baseUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	newInput := func(mutate func(inbound *proxyv1alpha1.SingBoxNode, ext *proxyv1alpha1.ExternalOutbound, input *ClientConfigInput)) ClientConfigInput {
		inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
			{Protocol: "vless", Port: 10443},
		})
		inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
		ext := makeExternalOutbound("ext-1", "us")
		input := ClientConfigInput{
			User:                    makeUser("user-alice", "secret-alice"),
			UserCred:                credmanager.UserCredential{UUID: baseUUID},
			InboundNodes:            []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound:         map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName:         map[string]*proxyv1alpha1.SingBoxNode{},
			ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{"ext-1": ext},
		}
		if mutate != nil {
			mutate(inbound, ext, &input)
		}
		return input
	}

	expectTags := func(t *testing.T, input ClientConfigInput, want bool) {
		t.Helper()
		result, err := BuildClientConfig(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := collectTags(result)["ext-1#node-a"]
		if got != want {
			t.Errorf("tag 'ext-1#node-a' presence = %v, want %v", got, want)
		}
	}

	t.Run("allowedInbounds mismatch excludes external outbound", func(t *testing.T) {
		expectTags(t, newInput(func(_ *proxyv1alpha1.SingBoxNode, ext *proxyv1alpha1.ExternalOutbound, _ *ClientConfigInput) {
			ext.Spec.AllowedInbounds = []string{"node-z"}
		}), false)
	})

	t.Run("allowedInbounds match includes external outbound", func(t *testing.T) {
		expectTags(t, newInput(func(_ *proxyv1alpha1.SingBoxNode, ext *proxyv1alpha1.ExternalOutbound, _ *ClientConfigInput) {
			ext.Spec.AllowedInbounds = []string{"node-a"}
		}), true)
	})

	t.Run("inbound allowedOutbounds mismatch excludes external outbound", func(t *testing.T) {
		expectTags(t, newInput(func(inbound *proxyv1alpha1.SingBoxNode, _ *proxyv1alpha1.ExternalOutbound, _ *ClientConfigInput) {
			inbound.Spec.AllowedOutbounds = []string{"node-z"}
		}), false)
	})

	t.Run("inbound allowedOutbounds match includes external outbound", func(t *testing.T) {
		expectTags(t, newInput(func(inbound *proxyv1alpha1.SingBoxNode, _ *proxyv1alpha1.ExternalOutbound, _ *ClientConfigInput) {
			inbound.Spec.AllowedOutbounds = []string{"ext-1"}
		}), true)
	})

	t.Run("denied node name excludes external outbound", func(t *testing.T) {
		expectTags(t, newInput(func(_ *proxyv1alpha1.SingBoxNode, _ *proxyv1alpha1.ExternalOutbound, input *ClientConfigInput) {
			input.DeniedNodeNames = map[string]bool{"ext-1": true}
		}), false)
	})

	t.Run("allowed node names without external excludes it", func(t *testing.T) {
		expectTags(t, newInput(func(_ *proxyv1alpha1.SingBoxNode, _ *proxyv1alpha1.ExternalOutbound, input *ClientConfigInput) {
			input.AllowedNodeNames = map[string]bool{"node-a": true}
		}), false)
	})

	t.Run("allowed node names with external includes it", func(t *testing.T) {
		expectTags(t, newInput(func(_ *proxyv1alpha1.SingBoxNode, _ *proxyv1alpha1.ExternalOutbound, input *ClientConfigInput) {
			input.AllowedNodeNames = map[string]bool{"node-a": true, "ext-1": true}
		}), true)
	})

	t.Run("gating applies to route-bound externals as well", func(t *testing.T) {
		inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
			{Protocol: "vless", Port: 10443},
		})
		inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}
		ext := makeExternalOutbound("ext-1", "")
		ext.Spec.AllowedInbounds = []string{"node-z"}
		input := ClientConfigInput{
			User:         makeUser("user-alice", "secret-alice"),
			UserCred:     credmanager.UserCredential{UUID: baseUUID},
			InboundNodes: []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{
				"node-a": {makeExternalRoute("route-a-ext", "default", "node-a", "ext-1")},
			},
			OutboundsByName:         map[string]*proxyv1alpha1.SingBoxNode{},
			ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{"ext-1": ext},
		}
		expectTags(t, input, false)
	})
}

// Test 6: Name collision — an outbound SingBoxNode always wins over an
// ExternalOutbound of the same name.
func TestBuildClientConfig_ExternalOutbound_SingBoxNodeWins(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("node-b", "us")
	ext := makeExternalOutbound("node-b", "us")

	newInput := func(offline map[string]bool) ClientConfigInput {
		return ClientConfigInput{
			User:                    makeUser("user-alice", "secret-alice"),
			UserCred:                credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
			InboundNodes:            []*proxyv1alpha1.SingBoxNode{inbound},
			RoutesByInbound:         map[string][]*proxyv1alpha1.CustomRoute{},
			OutboundsByName:         map[string]*proxyv1alpha1.SingBoxNode{"node-b": outbound},
			ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{"node-b": ext},
			OfflineNodeNames:        offline,
		}
	}

	t.Run("colliding name yields exactly one outbound", func(t *testing.T) {
		result, err := BuildClientConfig(newInput(nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tags := collectTags(result)
		if !tags["node-b#node-a"] {
			t.Errorf("tag 'node-b#node-a' not found in result, got tags: %v", tags)
		}
		if n := countProxyOutbounds(result); n != 1 {
			t.Errorf("expected exactly 1 proxy outbound for the colliding name, got %d", n)
		}
	})

	t.Run("offline SingBoxNode name is not stolen by the external", func(t *testing.T) {
		result, err := BuildClientConfig(newInput(map[string]bool{"node-b": true}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if collectTags(result)["node-b#node-a"] {
			t.Errorf("tag 'node-b#node-a' should be absent; external must not take over an offline SingBoxNode name")
		}
		if n := countProxyOutbounds(result); n != 0 {
			t.Errorf("expected 0 proxy outbounds, got %d", n)
		}
	})
}

// Test 7: OfflineNodeNames does not apply to ExternalOutbounds — they are
// never "offline".
func TestBuildClientConfig_ExternalOutbound_OfflineNotApplicable(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	ext := makeExternalOutbound("ext-1", "us")

	input := ClientConfigInput{
		User:                    makeUser("user-alice", "secret-alice"),
		UserCred:                credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:            []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound:         map[string][]*proxyv1alpha1.CustomRoute{},
		OutboundsByName:         map[string]*proxyv1alpha1.SingBoxNode{},
		ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{"ext-1": ext},
		OfflineNodeNames:        map[string]bool{"ext-1": true},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !collectTags(result)["ext-1#node-a"] {
		t.Errorf("tag 'ext-1#node-a' should be present; offline filtering must not apply to ExternalOutbounds")
	}
}

// Test 8: Mixed sources (region SingBoxNode, region external, route external)
// are deduplicated and sorted by name.
func TestBuildClientConfig_ExternalOutbound_MixedSourcesSorted(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("node-c", "us")
	extA := makeExternalOutbound("ext-a", "us")
	extB := makeExternalOutbound("ext-b", "")
	route := makeExternalRoute("route-a-extb", "default", "node-a", "ext-b")

	input := ClientConfigInput{
		User:         makeUser("user-alice", "secret-alice"),
		UserCred:     credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes: []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{
			"node-a": {route},
		},
		OutboundsByName: map[string]*proxyv1alpha1.SingBoxNode{"node-c": outbound},
		ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{
			"ext-a": extA,
			"ext-b": extB,
		},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3 proxies + 2 group selectors (us, others for region-less ext-b)
	// + proxy selector + direct = 7
	if len(result) != 7 {
		t.Fatalf("expected 7 items, got %d", len(result))
	}

	wantOrder := []string{"ext-a#node-a", "ext-b#node-a", "node-c#node-a"}
	for i, want := range wantOrder {
		m, ok := result[i].(map[string]any)
		if !ok {
			t.Fatalf("result[%d] is not a map", i)
		}
		if m["tag"] != want {
			t.Errorf("result[%d] tag expected %q, got %v", i, want, m["tag"])
		}
	}

	// ext-b has an empty region, so it groups under "others" while the
	// region-ful ext-a and node-c stay in "us".
	if g := selectorGroupOutbounds(result, "others"); len(g) != 1 || g[0] != "ext-b#node-a" {
		t.Errorf("selector(others).outbounds should be [\"ext-b#node-a\"], got %v", g)
	}
	if g := selectorGroupOutbounds(result, "us"); len(g) != 2 {
		t.Errorf("selector(us).outbounds should contain 2 tags, got %v", g)
	}
}

// Test 9: External server address/port never appear in client output.
func TestBuildClientConfig_ExternalOutbound_NoServerLeak(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	ext := makeExternalOutbound("ext-1", "us")
	route := makeExternalRoute("route-a-ext", "default", "node-a", "ext-1")

	input := ClientConfigInput{
		User:         makeUser("user-alice", "secret-alice"),
		UserCred:     credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes: []*proxyv1alpha1.SingBoxNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.CustomRoute{
			"node-a": {route},
		},
		OutboundsByName:         map[string]*proxyv1alpha1.SingBoxNode{},
		ExternalOutboundsByName: map[string]*proxyv1alpha1.ExternalOutbound{"ext-1": ext},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !collectTags(result)["ext-1#node-a"] {
		t.Fatal("tag 'ext-1#node-a' not found in result")
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(raw), ext.Spec.Server) {
		t.Errorf("client output must not contain external server address %q", ext.Spec.Server)
	}
	if strings.Contains(string(raw), "54321") {
		t.Errorf("client output must not contain external server port %d", ext.Spec.Port)
	}
}

// collectResponseTags counts outbound tags in a rendered client config body.
func collectResponseTags(t *testing.T, body []byte) map[string]int {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	tags := make(map[string]int)
	obs, _ := parsed["outbounds"].([]any)
	for _, ob := range obs {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if tag, _ := m["tag"].(string); tag != "" {
			tags[tag]++
		}
	}
	return tags
}

// Test 10: Handler lists ExternalOutbounds and includes them in client configs.
func TestHandler_ExternalOutbound(t *testing.T) {
	const testUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	const namespace = "default"

	secret := makeUserSecret(namespace, "test-secret", testUUID, "pw")

	user := makeUser("user-alice", "test-secret")
	user.Namespace = namespace

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Namespace = namespace
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	ext := makeExternalOutbound("ext-1", "us")
	ext.Namespace = namespace
	route := makeExternalRoute("route-a-ext2", namespace, "node-a", "ext-2")
	ext2 := makeExternalOutbound("ext-2", "")
	ext2.Namespace = namespace

	fakeClient := newFakeClient(secret, user, inbound, ext, ext2, route)

	srv := &Server{
		BindAddress: ":0",
		TemplateRef: "",
		Client:      fakeClient,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/"+namespace+"/"+testUUID, nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d; body: %s", w.Code, w.Body.String())
	}

	tags := collectResponseTags(t, w.Body.Bytes())
	if tags["ext-1#node-a"] != 1 {
		t.Errorf("expected exactly 1 outbound tagged 'ext-1#node-a', got %d", tags["ext-1#node-a"])
	}
	if tags["ext-2#node-a"] != 1 {
		t.Errorf("expected exactly 1 outbound tagged 'ext-2#node-a' (route-bound), got %d", tags["ext-2#node-a"])
	}
	if strings.Contains(w.Body.String(), ext.Spec.Server) || strings.Contains(w.Body.String(), ext2.Spec.Server) {
		t.Errorf("response must not contain external server addresses, body: %s", w.Body.String())
	}
}

// Test 11: Handler-level name collision — the ExternalOutbound is skipped
// when an outbound SingBoxNode with the same name exists.
func TestHandler_ExternalOutbound_SingBoxNodeWins(t *testing.T) {
	const testUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	const namespace = "default"

	secret := makeUserSecret(namespace, "test-secret", testUUID, "pw")

	user := makeUser("user-alice", "test-secret")
	user.Namespace = namespace

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Namespace = namespace
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("node-b", "us")
	outbound.Namespace = namespace

	ext := makeExternalOutbound("node-b", "us")
	ext.Namespace = namespace

	fakeClient := newFakeClient(secret, user, inbound, outbound, ext)

	srv := &Server{
		BindAddress: ":0",
		TemplateRef: "",
		Client:      fakeClient,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/"+namespace+"/"+testUUID, nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d; body: %s", w.Code, w.Body.String())
	}

	tags := collectResponseTags(t, w.Body.Bytes())
	if tags["node-b#node-a"] != 1 {
		t.Errorf("expected exactly 1 outbound tagged 'node-b#node-a', got %d", tags["node-b#node-a"])
	}
	if strings.Contains(w.Body.String(), ext.Spec.Server) {
		t.Errorf("response must not contain external server address %q", ext.Spec.Server)
	}
}
