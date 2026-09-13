package configengine_test

import (
	"encoding/json"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
)

// helper: build a minimal ProxyNode
func makeNode(name, address, region string, roles []v1alpha1.ProxyRole, protocols []v1alpha1.ProtocolConfig, relayNodePort int32) *v1alpha1.SingBoxNode {
	return &v1alpha1.SingBoxNode{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.SingBoxNodeSpec{
			Address:            address,
			Region:             region,
			Roles:              roles,
			SupportedProtocols: protocols,
			RelayPort:          relayNodePort,
		},
	}
}

func makeUser(name string) *v1alpha1.User {
	return &v1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.UserSpec{},
	}
}

func makeRoute(name, inboundNode, outboundNode string) *v1alpha1.CustomRoute {
	return &v1alpha1.CustomRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.CustomRouteSpec{
			InboundNode:  inboundNode,
			OutboundNode: outboundNode,
		},
	}
}

// parseConfig unmarshals the Output.Config into a generic map for inspection
func parseConfig(t *testing.T, out configengine.Output) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(out.Config, &result); err != nil {
		t.Fatalf("failed to parse config JSON: %v", err)
	}
	return result
}

func inboundsOf(t *testing.T, cfg map[string]any) []any {
	t.Helper()
	v, ok := cfg["inbounds"]
	if !ok {
		return nil
	}
	arr, _ := v.([]any)
	return arr
}

func outboundsOf(t *testing.T, cfg map[string]any) []any {
	t.Helper()
	v, ok := cfg["outbounds"]
	if !ok {
		return nil
	}
	arr, _ := v.([]any)
	return arr
}

func routeFinal(cfg map[string]any) string {
	r, ok := cfg["route"].(map[string]any)
	if !ok {
		return ""
	}
	f, _ := r["final"].(string)
	return f
}

func inboundTags(t *testing.T, cfg map[string]any) []string {
	t.Helper()
	var tags []string
	for _, ib := range inboundsOf(t, cfg) {
		m, _ := ib.(map[string]any)
		tags = append(tags, m["tag"].(string))
	}
	return tags
}

func outboundTags(t *testing.T, cfg map[string]any) []string {
	t.Helper()
	var tags []string
	for _, ob := range outboundsOf(t, cfg) {
		m, _ := ob.(map[string]any)
		tags = append(tags, m["tag"].(string))
	}
	return tags
}

func containsTag(tags []string, tag string) bool {
	return slices.Contains(tags, tag)
}

func routeRulesOf(t *testing.T, cfg map[string]any) []any {
	t.Helper()
	r, ok := cfg["route"].(map[string]any)
	if !ok {
		return nil
	}
	rules, _ := r["rules"].([]any)
	return rules
}

// ---------------------------------------------------------------------------
// Test 1: Inbound node with 2 vless users + 1 outbound node (no Routes)
// Fallback path: single inbound per protocol containing all users.
// ---------------------------------------------------------------------------
func TestConfigEngine_InboundNode(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	node.Spec.InboundProtocol = "vless"
	outNode := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 31962,
	)
	user1 := makeUser("user-alice")
	user2 := makeUser("user-bob")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{user1, user2},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
			"user-bob":   {UUID: "bbbb-2222"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{outNode},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "relay-user", Password: "relay-pass"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"node-b": outNode},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)
	obs := outboundTags(t, cfg)

	if !containsTag(ibs, "inbound-vless") {
		t.Errorf("missing inbound-vless tag, got %v", ibs)
	}
	if len(ibs) != 1 {
		t.Errorf("expected exactly 1 inbound, got %d: %v", len(ibs), ibs)
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] == "inbound-vless" {
			users := m["users"].([]any)
			if len(users) != 2 {
				t.Errorf("expected 2 virtual users in inbound, got %d", len(users))
			}
			names := make(map[string]bool)
			for _, u := range users {
				um := u.(map[string]any)
				names[um["name"].(string)] = true
			}
			if !names["user-alice#node-b"] {
				t.Errorf("missing virtual user user-alice#node-b, got %v", names)
			}
			if !names["user-bob#node-b"] {
				t.Errorf("missing virtual user user-bob#node-b, got %v", names)
			}
		}
	}

	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("missing socks5 outbound to node-b, got %v", obs)
	}
	if !containsTag(obs, "direct") {
		t.Errorf("missing direct outbound, got %v", obs)
	}

	if routeFinal(cfg) != "direct" {
		t.Errorf("expected route.final=direct, got %q", routeFinal(cfg))
	}

	rules := routeRulesOf(t, cfg)
	if len(rules) != 1 {
		t.Fatalf("expected 1 routing rule, got %d", len(rules))
	}
	rule := rules[0].(map[string]any)
	if rule["outbound"] != "outbound-node-b" {
		t.Errorf("expected rule outbound=outbound-node-b, got %v", rule["outbound"])
	}
	authUsers, _ := rule["auth_user"].([]any)
	if len(authUsers) != 2 {
		t.Errorf("expected 2 auth_users in rule, got %d", len(authUsers))
	}

	for _, ob := range outboundsOf(t, cfg) {
		m := ob.(map[string]any)
		if m["tag"] == "outbound-node-b" {
			if m["server"] != "5.6.7.8" {
				t.Errorf("expected server=5.6.7.8, got %v", m["server"])
			}
			if m["server_port"].(float64) != 31962 {
				t.Errorf("expected server_port=31962, got %v", m["server_port"])
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Test 2: Outbound-only node
// ---------------------------------------------------------------------------
func TestConfigEngine_OutboundNode(t *testing.T) {
	node := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 0,
	)
	input := configengine.Input{
		Node: node,
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "relay-user", Password: "relay-pass"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)
	obs := outboundTags(t, cfg)

	// 1 relay socks5 inbound
	if len(ibs) != 1 || ibs[0] != "relay-socks5" {
		t.Errorf("expected [relay-socks5] inbound, got %v", ibs)
	}

	// outbounds = [direct]
	if len(obs) != 1 || obs[0] != "direct" {
		t.Errorf("expected [direct] outbound, got %v", obs)
	}

	if routeFinal(cfg) != "direct" {
		t.Errorf("expected route.final=direct, got %q", routeFinal(cfg))
	}

	// verify relay inbound port
	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] == "relay-socks5" {
			if m["listen_port"].(float64) != 10808 {
				t.Errorf("expected listen_port=10808, got %v", m["listen_port"])
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Test 3: Multi-role node (inbound + outbound)
// ---------------------------------------------------------------------------
func TestConfigEngine_MultiRoleNode(t *testing.T) {
	node := makeNode("node-c", "9.9.9.9", "eu-central",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound, v1alpha1.ProxyRoleOutbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "trojan", Port: 10444}},
		10808,
	)
	node.Spec.InboundProtocol = "trojan"
	user := makeUser("user-carol")
	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-carol": {UUID: "s3cr3t-uuid"},
		},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-c": {Username: "relay-u", Password: "relay-p"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)
	obs := outboundTags(t, cfg)

	if !containsTag(ibs, "inbound-trojan") {
		t.Errorf("missing trojan inbound, got %v", ibs)
	}
	if !containsTag(ibs, "relay-socks5") {
		t.Errorf("missing relay-socks5 inbound, got %v", ibs)
	}
	if !containsTag(obs, "direct") {
		t.Errorf("missing direct outbound, got %v", obs)
	}
}

// ---------------------------------------------------------------------------
// Test 4: Manual route �?single inbound per protocol with all users,
// auth_user routing rule binds users to outbound.
// ---------------------------------------------------------------------------
func TestConfigEngine_ManualRoute(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	nodeB := makeNode("node-b", "5.6.7.8", "us-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 31962,
	)
	user := makeUser("user-dave")
	route := makeRoute("route-a-to-b", "node-a", "node-b")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-dave": {UUID: "dddd-4444"},
		},
		Routes: []*v1alpha1.CustomRoute{route},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "r-user", Password: "r-pass"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"node-b": nodeB},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)
	obs := outboundTags(t, cfg)

	expectedInboundTag := "inbound-vless"
	if !containsTag(ibs, expectedInboundTag) {
		t.Errorf("missing inbound tag %q, got %v", expectedInboundTag, ibs)
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] == expectedInboundTag {
			users := m["users"].([]any)
			if len(users) != 1 {
				t.Errorf("expected 1 user in inbound, got %d", len(users))
			}
			u := users[0].(map[string]any)
			if u["name"] != "user-dave#node-b" {
				t.Errorf("expected user name=user-dave#node-b, got %v", u["name"])
			}
		}
	}

	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("missing outbound-node-b, got %v", obs)
	}

	for _, ob := range outboundsOf(t, cfg) {
		m := ob.(map[string]any)
		if m["tag"] == "outbound-node-b" && m["server"] != "5.6.7.8" {
			t.Errorf("expected server=5.6.7.8, got %v", m["server"])
		}
	}

	rules := routeRulesOf(t, cfg)
	if len(rules) == 0 {
		t.Fatal("expected at least one routing rule, got none")
	}
	found := false
	for _, rule := range rules {
		m := rule.(map[string]any)
		if m["outbound"] != "outbound-node-b" {
			continue
		}
		authUsers, _ := m["auth_user"].([]any)
		for _, u := range authUsers {
			if u.(string) == "user-dave#node-b" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no routing rule with auth_user=user-dave#node-b �?outbound-node-b")
	}
}

// ---------------------------------------------------------------------------
// Test 5: Inbound node with no matching users �?inbounds must be empty slice
// ---------------------------------------------------------------------------
func TestConfigEngine_NoUsersOnEntry(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	node.Spec.InboundProtocol = "vless"
	input := configengine.Input{
		Node:                node,
		Users:               []*v1alpha1.User{},
		UserCreds:           map[string]configengine.UserCredential{},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundsOf(t, cfg)

	// inbounds must exist as a field (not nil), but be empty
	if cfg["inbounds"] == nil {
		t.Error("inbounds field must not be nil")
	}
	if len(ibs) != 0 {
		t.Errorf("expected 0 inbounds, got %d: %v", len(ibs), ibs)
	}
}

// ---------------------------------------------------------------------------
// Test 6: Inbound node + 2 outbound nodes in same region
// ---------------------------------------------------------------------------
func TestConfigEngine_MultipleOutboundNodes(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	node.Spec.InboundProtocol = "vless"
	outNode1 := makeNode("node-b1", "5.5.5.5", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	outNode2 := makeNode("node-b2", "6.6.6.6", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)

	input := configengine.Input{
		Node:          node,
		Users:         []*v1alpha1.User{},
		UserCreds:     map[string]configengine.UserCredential{},
		OutboundNodes: []*v1alpha1.SingBoxNode{outNode1, outNode2},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b1": {Username: "u1", Password: "p1"},
			"node-b2": {Username: "u2", Password: "p2"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b1": outNode1,
			"node-b2": outNode2,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	// 2 socks5 outbounds + 1 direct
	if !containsTag(obs, "outbound-node-b1") {
		t.Errorf("missing outbound-node-b1, got %v", obs)
	}
	if !containsTag(obs, "outbound-node-b2") {
		t.Errorf("missing outbound-node-b2, got %v", obs)
	}
	if !containsTag(obs, "direct") {
		t.Errorf("missing direct outbound, got %v", obs)
	}
	if len(obs) != 3 {
		t.Errorf("expected exactly 3 outbounds, got %d: %v", len(obs), obs)
	}
}

// ---------------------------------------------------------------------------
// Test 7: Hash consistency �?same input �?same hash; different input �?different hash
// ---------------------------------------------------------------------------
func TestConfigEngine_HashConsistency(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	node.Spec.InboundProtocol = "vless"
	user := makeUser("user-alice")
	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
	}

	out1, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}
	out2, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}

	// same input �?same hash
	if out1.Hash != out2.Hash {
		t.Errorf("hash not stable: %q vs %q", out1.Hash, out2.Hash)
	}
	if len(out1.Hash) != 16 {
		t.Errorf("expected 16-char hash, got %d: %q", len(out1.Hash), out1.Hash)
	}

	// different input �?different hash
	user2 := makeUser("user-bob")
	input2 := input
	input2.Users = []*v1alpha1.User{user2}
	input2.UserCreds = map[string]configengine.UserCredential{
		"user-bob": {UUID: "bbbb-2222"},
	}
	out3, err := configengine.Compute(input2)
	if err != nil {
		t.Fatalf("unexpected error on third call: %v", err)
	}
	if out1.Hash == out3.Hash {
		t.Errorf("different inputs produced the same hash: %q", out1.Hash)
	}
}

// ---------------------------------------------------------------------------
// Test 8: ExtractNodePorts �?inbound node with multiple protocols
// ---------------------------------------------------------------------------
func TestExtractNodePorts_InboundNode(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{
			{Protocol: "vless", Port: 10443},
			{Protocol: "trojan", Port: 10444},
		},
		10808,
	)
	ports := configengine.ExtractNodePorts(node)
	if len(ports) != 2 {
		t.Errorf("expected 2 ports, got %d: %v", len(ports), ports)
	}
}

func TestExtractNodePorts_OutboundNode(t *testing.T) {
	node := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 0,
	)
	ports := configengine.ExtractNodePorts(node)
	if len(ports) != 1 || ports[0] != 10808 {
		t.Errorf("expected [10808], got %v", ports)
	}
}

// ---------------------------------------------------------------------------
// Test 9: socks5 and http user inbounds (fallback, no Routes)
// ---------------------------------------------------------------------------
func TestConfigEngine_Socks5AndHTTPUsers(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{
			{Protocol: "socks5", Port: 10808},
		},
		10900,
	)
	node.Spec.InboundProtocol = "socks5"
	userS := makeUser("user-socks")
	userH := makeUser("user-http")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{userS, userH},
		UserCreds: map[string]configengine.UserCredential{
			"user-socks": {UUID: "socks-uuid"},
			"user-http":  {UUID: "http-uuid"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)

	if !containsTag(ibs, "inbound-socks5") {
		t.Errorf("missing socks5 inbound, got %v", ibs)
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] == "inbound-socks5" && m["type"] != "socks" {
			t.Errorf("expected type=socks, got %v", m["type"])
		}
	}
}

// ---------------------------------------------------------------------------
// Test 10: Dedup �?region-auto outbound node that is also in an explicit Route
// must appear exactly once in outbounds.
// ---------------------------------------------------------------------------
func TestConfigEngine_DedupRegionAutoAndExplicitRoute(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 31962,
	)
	user := makeUser("user-eve")
	route := makeRoute("route-a-to-b", "node-a", "node-b")

	input := configengine.Input{
		Node:                nodeA,
		Users:               []*v1alpha1.User{user},
		UserCreds:           map[string]configengine.UserCredential{"user-eve": {UUID: "eeee-5555"}},
		OutboundNodes:       []*v1alpha1.SingBoxNode{nodeB},
		Routes:              []*v1alpha1.CustomRoute{route},
		NodeCreds:           map[string]configengine.NodeCredential{"node-b": {Username: "u", Password: "p"}},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"node-b": nodeB},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	count := 0
	for _, tag := range obs {
		if tag == "outbound-node-b" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected outbound-node-b exactly once, got %d times in %v", count, obs)
	}
}

// ---------------------------------------------------------------------------
// Test 11: Multi-route �?1 user, 2 routes �?2 inbounds on distinct ports,
// each containing all users, with auth_user routing rules per outbound.
// ---------------------------------------------------------------------------
func TestConfigEngine_MultiRouteInbounds(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	nodeB := makeNode("node-b", "5.5.5.5", "us-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeC := makeNode("node-c", "6.6.6.6", "us-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	user := makeUser("user-frank")
	routeToB := makeRoute("route-a-to-b", "node-a", "node-b")
	routeToC := makeRoute("route-a-to-c", "node-a", "node-c")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-frank": {UUID: "ffff-6666"},
		},
		Routes: []*v1alpha1.CustomRoute{routeToB, routeToC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB,
			"node-c": nodeC,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)
	obs := outboundTags(t, cfg)

	if len(ibs) != 1 || ibs[0] != "inbound-vless" {
		t.Errorf("expected exactly 1 inbound [inbound-vless], got %v", ibs)
	}

	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("missing outbound-node-b, got %v", obs)
	}
	if !containsTag(obs, "outbound-node-c") {
		t.Errorf("missing outbound-node-c, got %v", obs)
	}

	var uuidB, uuidC string
	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-vless" {
			continue
		}
		users := m["users"].([]any)
		if len(users) != 2 {
			t.Fatalf("expected 2 virtual users in inbound-vless, got %d", len(users))
		}
		for _, u := range users {
			um := u.(map[string]any)
			switch um["name"].(string) {
			case "user-frank#node-b":
				uuidB = um["uuid"].(string)
			case "user-frank#node-c":
				uuidC = um["uuid"].(string)
			default:
				t.Errorf("unexpected virtual user name: %v", um["name"])
			}
		}
	}
	if uuidB == "" {
		t.Error("missing virtual user user-frank#node-b")
	}
	if uuidC == "" {
		t.Error("missing virtual user user-frank#node-c")
	}
	if uuidB != "" && uuidC != "" && uuidB == uuidC {
		t.Errorf("expected distinct UUIDs for different routes, both got %q", uuidB)
	}

	rules := routeRulesOf(t, cfg)
	if len(rules) != 2 {
		t.Fatalf("expected 2 routing rules, got %d", len(rules))
	}

	ruleOutbounds := make(map[string]bool)
	for _, rule := range rules {
		m := rule.(map[string]any)
		ruleOutbounds[m["outbound"].(string)] = true
		authUsers, _ := m["auth_user"].([]any)
		if len(authUsers) == 0 {
			t.Errorf("rule for %v has no auth_user", m["outbound"])
		}
		if _, hasInbound := m["inbound"]; hasInbound {
			t.Errorf("routing rule for %v must not have inbound field", m["outbound"])
		}
	}
	if !ruleOutbounds["outbound-node-b"] {
		t.Errorf("missing routing rule for outbound-node-b")
	}
	if !ruleOutbounds["outbound-node-c"] {
		t.Errorf("missing routing rule for outbound-node-c")
	}
}

// ---------------------------------------------------------------------------
// Test 13: Region-auto outbound nodes trigger virtual user mode without ProxyRoute
// ---------------------------------------------------------------------------
func TestConfigEngine_RegionAutoVirtualUsers(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeC := makeNode("node-c", "9.9.9.9", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)
	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB,
			"node-c": nodeC,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)

	if len(ibs) != 1 || ibs[0] != "inbound-vless" {
		t.Errorf("expected exactly [inbound-vless], got %v", ibs)
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-vless" {
			continue
		}
		users := m["users"].([]any)
		if len(users) != 2 {
			t.Fatalf("expected 2 virtual users, got %d", len(users))
		}
		names := make(map[string]bool)
		for _, u := range users {
			um := u.(map[string]any)
			names[um["name"].(string)] = true
		}
		if !names["user-alice#node-b"] {
			t.Errorf("missing virtual user user-alice#node-b, got %v", names)
		}
		if !names["user-alice#node-c"] {
			t.Errorf("missing virtual user user-alice#node-c, got %v", names)
		}
	}

	rules := routeRulesOf(t, cfg)
	if len(rules) != 2 {
		t.Fatalf("expected 2 routing rules, got %d", len(rules))
	}
	ruleTargets := make(map[string]bool)
	for _, rule := range rules {
		rm := rule.(map[string]any)
		ruleTargets[rm["outbound"].(string)] = true
		authUsers, _ := rm["auth_user"].([]any)
		if len(authUsers) == 0 {
			t.Errorf("rule for %v has no auth_user", rm["outbound"])
		}
	}
	if !ruleTargets["outbound-node-b"] {
		t.Errorf("missing routing rule for outbound-node-b")
	}
	if !ruleTargets["outbound-node-c"] {
		t.Errorf("missing routing rule for outbound-node-c")
	}
}

// ---------------------------------------------------------------------------
// Test 14: hysteria2 inbound �?users have password, inbound has tls block
// ---------------------------------------------------------------------------
func TestConfigEngine_Hysteria2Inbound(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 30443}},
		0,
	)
	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "s3cr3t-uuid"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)

	if !containsTag(ibs, "inbound-hysteria2") {
		t.Errorf("missing inbound-hysteria2 tag, got %v", ibs)
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-hysteria2" {
			continue
		}
		if m["type"] != "hysteria2" {
			t.Errorf("expected type=hysteria2, got %v", m["type"])
		}
		tls, ok := m["tls"].(map[string]any)
		if !ok {
			t.Error("expected tls block in hysteria2 inbound")
		} else if tls["enabled"] != true {
			t.Errorf("expected tls.enabled=true, got %v", tls["enabled"])
		}
		users, _ := m["users"].([]any)
		if len(users) != 1 {
			t.Fatalf("expected 1 user, got %d", len(users))
		}
		u := users[0].(map[string]any)
		if u["name"] != "user-alice" {
			t.Errorf("expected name=user-alice, got %v", u["name"])
		}
		if _, hasPassword := u["password"]; !hasPassword {
			t.Error("expected password field in hysteria2 user")
		}
		if _, hasUUID := u["uuid"]; hasUUID {
			t.Error("hysteria2 user must not have uuid field")
		}
	}
}

// ---------------------------------------------------------------------------
// Test 15: hysteria2 inbound with outbound nodes �?virtual users use DerivePassword
// ---------------------------------------------------------------------------
func TestConfigEngine_Hysteria2VirtualUsers(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 30443}},
		0,
	)
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "s3cr3t-uuid"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "relay-user", Password: "relay-pass"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"node-b": nodeB},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-hysteria2" {
			continue
		}
		users, _ := m["users"].([]any)
		if len(users) != 1 {
			t.Fatalf("expected 1 virtual user, got %d", len(users))
		}
		u := users[0].(map[string]any)
		if u["name"] != "user-alice#node-b" {
			t.Errorf("expected virtual user name=user-alice#node-b, got %v", u["name"])
		}
		if _, hasPassword := u["password"]; !hasPassword {
			t.Error("expected password field in hysteria2 virtual user")
		}
	}

	obs := outboundTags(t, cfg)
	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("missing outbound-node-b, got %v", obs)
	}
}

// ---------------------------------------------------------------------------
// Test 16: Dual-role node (inbound + outbound) generates self-direct outbound
// and virtual users with routing rules pointing to outbound-<nodeName> (direct).
// ---------------------------------------------------------------------------
func TestConfigEngine_DualRoleNode_SelfDirect(t *testing.T) {
	node := makeNode("node-x", "1.2.3.4", "ap-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound, v1alpha1.ProxyRoleOutbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	node.Spec.InboundProtocol = "vless"
	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-x": {Username: "relay-u", Password: "relay-p"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)
	obs := outboundTags(t, cfg)

	if !containsTag(ibs, "inbound-vless") {
		t.Errorf("missing inbound-vless, got %v", ibs)
	}
	if !containsTag(ibs, "relay-socks5") {
		t.Errorf("missing relay-socks5, got %v", ibs)
	}

	if !containsTag(obs, "outbound-node-x") {
		t.Errorf("missing outbound-node-x, got %v", obs)
	}
	if !containsTag(obs, "direct") {
		t.Errorf("missing direct outbound, got %v", obs)
	}

	for _, ob := range outboundsOf(t, cfg) {
		m := ob.(map[string]any)
		if m["tag"] == "outbound-node-x" {
			if m["type"] != "direct" {
				t.Errorf("expected outbound-node-x to be type=direct, got %v", m["type"])
			}
		}
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-vless" {
			continue
		}
		users := m["users"].([]any)
		if len(users) != 1 {
			t.Fatalf("expected 1 virtual user in inbound-vless, got %d", len(users))
		}
		u := users[0].(map[string]any)
		if u["name"] != "user-alice#node-x" {
			t.Errorf("expected virtual user user-alice#node-x, got %v", u["name"])
		}
	}

	rules := routeRulesOf(t, cfg)
	if len(rules) != 1 {
		t.Fatalf("expected 1 routing rule, got %d", len(rules))
	}
	rule := rules[0].(map[string]any)
	if rule["outbound"] != "outbound-node-x" {
		t.Errorf("expected rule outbound=outbound-node-x, got %v", rule["outbound"])
	}
	authUsers, _ := rule["auth_user"].([]any)
	if len(authUsers) != 1 || authUsers[0].(string) != "user-alice#node-x" {
		t.Errorf("expected auth_user=[user-alice#node-x], got %v", authUsers)
	}
}

// ---------------------------------------------------------------------------
// Test 17: Dual-role node with additional outbound peer �?self and peer both
// produce routing rules; self outbound is direct, peer outbound is SOCKS5.
// ---------------------------------------------------------------------------
func TestConfigEngine_DualRoleNode_SelfAndPeer(t *testing.T) {
	node := makeNode("node-x", "1.2.3.4", "ap-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound, v1alpha1.ProxyRoleOutbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	node.Spec.InboundProtocol = "vless"
	peer := makeNode("node-y", "5.6.7.8", "ap-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 31962,
	)
	user := makeUser("user-bob")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-bob": {UUID: "bbbb-2222"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{peer},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-x": {Username: "rx-u", Password: "rx-p"},
			"node-y": {Username: "ry-u", Password: "ry-p"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"node-y": peer},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	if !containsTag(obs, "outbound-node-x") {
		t.Errorf("missing outbound-node-x, got %v", obs)
	}
	if !containsTag(obs, "outbound-node-y") {
		t.Errorf("missing outbound-node-y, got %v", obs)
	}

	for _, ob := range outboundsOf(t, cfg) {
		m := ob.(map[string]any)
		switch m["tag"] {
		case "outbound-node-x":
			if m["type"] != "direct" {
				t.Errorf("expected outbound-node-x type=direct, got %v", m["type"])
			}
		case "outbound-node-y":
			if m["type"] != "socks" {
				t.Errorf("expected outbound-node-y type=socks, got %v", m["type"])
			}
		}
	}

	rules := routeRulesOf(t, cfg)
	if len(rules) != 2 {
		t.Fatalf("expected 2 routing rules, got %d", len(rules))
	}
	ruleTargets := make(map[string]bool)
	for _, rule := range rules {
		rm := rule.(map[string]any)
		ruleTargets[rm["outbound"].(string)] = true
	}
	if !ruleTargets["outbound-node-x"] {
		t.Errorf("missing routing rule for outbound-node-x")
	}
	if !ruleTargets["outbound-node-y"] {
		t.Errorf("missing routing rule for outbound-node-y")
	}
}

// ---------------------------------------------------------------------------
// Test V2ray: UsageCollectionEnabled=true �?config must contain experimental.v2ray_api
// ---------------------------------------------------------------------------
func TestCompute_UsageCollectionEnabled(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeC := makeNode("node-c", "9.9.9.9", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)
	alice := makeUser("user-alice")
	bob := makeUser("user-bob")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{alice, bob},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
			"user-bob":   {UUID: "bbbb-2222"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB,
			"node-c": nodeC,
		},
		UsageCollectionEnabled: true,
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)

	// Verify experimental block exists
	exp, ok := cfg["experimental"].(map[string]any)
	if !ok {
		t.Fatal("expected experimental key in config when UsageCollectionEnabled=true")
	}

	v2ray, ok := exp["v2ray_api"].(map[string]any)
	if !ok {
		t.Fatal("expected experimental.v2ray_api key")
	}

	// Verify default listen address
	listen, _ := v2ray["listen"].(string)
	if listen != "0.0.0.0:10085" {
		t.Errorf("expected listen=0.0.0.0:10085, got %q", listen)
	}

	// Verify stats.enabled
	stats, ok := v2ray["stats"].(map[string]any)
	if !ok {
		t.Fatal("expected experimental.v2ray_api.stats key")
	}
	enabled, _ := stats["enabled"].(bool)
	if !enabled {
		t.Error("expected stats.enabled=true")
	}

	// Verify stats.users contains all virtual user names
	rawUsers, _ := stats["users"].([]any)
	if len(rawUsers) != 4 {
		t.Fatalf("expected 4 stats users, got %d: %v", len(rawUsers), rawUsers)
	}
	userSet := make(map[string]bool)
	for _, u := range rawUsers {
		userSet[u.(string)] = true
	}
	expectedUsers := []string{
		"user-alice#node-b",
		"user-alice#node-c",
		"user-bob#node-b",
		"user-bob#node-c",
	}
	for _, expected := range expectedUsers {
		if !userSet[expected] {
			t.Errorf("missing stats user %q, got %v", expected, userSet)
		}
	}

	// Custom listen address should override the default
	input.V2RayAPIListenAddr = "127.0.0.1:9999"
	out2, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error on custom listen: %v", err)
	}
	cfg2 := parseConfig(t, out2)
	exp2 := cfg2["experimental"].(map[string]any)
	v2ray2 := exp2["v2ray_api"].(map[string]any)
	listen2, _ := v2ray2["listen"].(string)
	if listen2 != "127.0.0.1:9999" {
		t.Errorf("expected custom listen=127.0.0.1:9999, got %q", listen2)
	}
}

// ---------------------------------------------------------------------------
// Test V2ray: UsageCollectionEnabled=false �?config must NOT contain experimental key
// ---------------------------------------------------------------------------
func TestCompute_UsageCollectionDisabled(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	alice := makeUser("user-alice")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{alice},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB,
		},
		UsageCollectionEnabled: false,
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)

	if _, ok := cfg["experimental"]; ok {
		t.Error("config must NOT contain experimental key when UsageCollectionEnabled=false")
	}

	// Backward compatibility: log/inbounds/outbounds/route must still exist
	for _, key := range []string{"log", "inbounds", "outbounds", "route"} {
		if _, ok := cfg[key]; !ok {
			t.Errorf("config must contain %q key (backward compatibility)", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 12: DeriveUUID �?determinism and uniqueness
// ---------------------------------------------------------------------------
func TestDeriveUUID(t *testing.T) {
	uuid1 := configengine.DeriveUUID("f0a5a0d6-951a-4936-a7e7-93a8f86f2fb8", "acck-jp")
	uuid2 := configengine.DeriveUUID("f0a5a0d6-951a-4936-a7e7-93a8f86f2fb8", "acck-jp")
	if uuid1 != uuid2 {
		t.Errorf("DeriveUUID not deterministic: %q vs %q", uuid1, uuid2)
	}

	uuid3 := configengine.DeriveUUID("f0a5a0d6-951a-4936-a7e7-93a8f86f2fb8", "xtom-jp")
	if uuid1 == uuid3 {
		t.Errorf("DeriveUUID not unique for different suffixes")
	}

	if len(uuid1) != 36 {
		t.Errorf("expected 36-char UUID, got %d: %q", len(uuid1), uuid1)
	}
}

// ---------------------------------------------------------------------------
// Test: naive inbound — type, tag, TLS block, username+password user fields
// ---------------------------------------------------------------------------
func TestConfigEngine_NaiveInbound(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "naive", Port: 10443}},
		0,
	)
	node.Spec.InboundProtocol = "naive"
	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "s3cr3t-uuid"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)

	if !containsTag(ibs, "inbound-naive") {
		t.Errorf("missing inbound-naive tag, got %v", ibs)
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-naive" {
			continue
		}
		if m["type"] != "naive" {
			t.Errorf("expected type=naive, got %v", m["type"])
		}
		tls, ok := m["tls"].(map[string]any)
		if !ok {
			t.Error("expected tls block in naive inbound")
		} else if tls["enabled"] != true {
			t.Errorf("expected tls.enabled=true, got %v", tls["enabled"])
		}
		users, _ := m["users"].([]any)
		if len(users) != 1 {
			t.Fatalf("expected 1 user, got %d", len(users))
		}
		u := users[0].(map[string]any)
		if _, hasUsername := u["username"]; !hasUsername {
			t.Error("expected username field in naive user")
		}
		if _, hasPassword := u["password"]; !hasPassword {
			t.Error("expected password field in naive user")
		}
		if _, hasUUID := u["uuid"]; hasUUID {
			t.Error("naive user must not have uuid field")
		}
	}
}

// ---------------------------------------------------------------------------
// Test: naive inbound with outbound node — virtual users have username+password
// ---------------------------------------------------------------------------
func TestConfigEngine_NaiveVirtualUsers(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "naive", Port: 10443}},
		0,
	)
	nodeA.Spec.InboundProtocol = "naive"
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "s3cr3t-uuid"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "relay-user", Password: "relay-pass"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"node-b": nodeB},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-naive" {
			continue
		}
		tls, ok := m["tls"].(map[string]any)
		if !ok {
			t.Error("expected tls block in naive virtual-user inbound")
		} else if tls["enabled"] != true {
			t.Errorf("expected tls.enabled=true, got %v", tls["enabled"])
		}
		users, _ := m["users"].([]any)
		if len(users) != 1 {
			t.Fatalf("expected 1 virtual user, got %d", len(users))
		}
		u := users[0].(map[string]any)
		// naive users[] must NOT have a "name" field (sing-box strict JSON decode rejects it)
		if _, hasName := u["name"]; hasName {
			t.Errorf("naive virtual user must not have 'name' field, got %v", u["name"])
		}
		// username carries the virtual user identity for auth_user routing and stats matching
		if u["username"] != "user-alice#node-b" {
			t.Errorf("expected username=user-alice#node-b, got %v", u["username"])
		}
		if _, hasPassword := u["password"]; !hasPassword {
			t.Error("expected password field in naive virtual user")
		}
	}
}

// ---------------------------------------------------------------------------
// Test: anytls inbound — type, tag, TLS block, name+password only (no username/uuid)
// ---------------------------------------------------------------------------
func TestConfigEngine_AnyTLSInbound(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "anytls", Port: 10443}},
		0,
	)
	node.Spec.InboundProtocol = "anytls"
	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "s3cr3t-uuid"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)

	if !containsTag(ibs, "inbound-anytls") {
		t.Errorf("missing inbound-anytls tag, got %v", ibs)
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-anytls" {
			continue
		}
		if m["type"] != "anytls" {
			t.Errorf("expected type=anytls, got %v", m["type"])
		}
		tls, ok := m["tls"].(map[string]any)
		if !ok {
			t.Error("expected tls block in anytls inbound")
		} else if tls["enabled"] != true {
			t.Errorf("expected tls.enabled=true, got %v", tls["enabled"])
		}
		if _, hasPaddingScheme := m["padding_scheme"]; hasPaddingScheme {
			t.Error("anytls inbound must not have padding_scheme field at the top level")
		}
		users, _ := m["users"].([]any)
		if len(users) != 1 {
			t.Fatalf("expected 1 user, got %d", len(users))
		}
		u := users[0].(map[string]any)
		if u["name"] != "user-alice" {
			t.Errorf("expected name=user-alice, got %v", u["name"])
		}
		if _, hasPassword := u["password"]; !hasPassword {
			t.Error("expected password field in anytls user")
		}
		if _, hasUsername := u["username"]; hasUsername {
			t.Error("anytls user must not have username field")
		}
		if _, hasUUID := u["uuid"]; hasUUID {
			t.Error("anytls user must not have uuid field")
		}
	}
}

// ---------------------------------------------------------------------------
// Test: anytls inbound with outbound node — virtual users have password only
// ---------------------------------------------------------------------------
func TestConfigEngine_AnyTLSVirtualUsers(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "anytls", Port: 10443}},
		0,
	)
	nodeA.Spec.InboundProtocol = "anytls"
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "s3cr3t-uuid"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "relay-user", Password: "relay-pass"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"node-b": nodeB},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-anytls" {
			continue
		}
		tls, ok := m["tls"].(map[string]any)
		if !ok {
			t.Error("expected tls block in anytls virtual-user inbound")
		} else if tls["enabled"] != true {
			t.Errorf("expected tls.enabled=true, got %v", tls["enabled"])
		}
		users, _ := m["users"].([]any)
		if len(users) != 1 {
			t.Fatalf("expected 1 virtual user, got %d", len(users))
		}
		u := users[0].(map[string]any)
		if u["name"] != "user-alice#node-b" {
			t.Errorf("expected virtual user name=user-alice#node-b, got %v", u["name"])
		}
		if _, hasPassword := u["password"]; !hasPassword {
			t.Error("expected password field in anytls virtual user")
		}
		if _, hasUsername := u["username"]; hasUsername {
			t.Error("anytls virtual user must not have username field")
		}
		if _, hasUUID := u["uuid"]; hasUUID {
			t.Error("anytls virtual user must not have uuid field")
		}
	}
}

// ---------------------------------------------------------------------------
// Test: tuic inbound — type, tag, TLS block, name+uuid+password user fields
// ---------------------------------------------------------------------------
func TestConfigEngine_TUICInbound(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "tuic", Port: 10443}},
		0,
	)
	node.Spec.InboundProtocol = "tuic"
	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "s3cr3t-uuid"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)

	if !containsTag(ibs, "inbound-tuic") {
		t.Errorf("missing inbound-tuic tag, got %v", ibs)
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-tuic" {
			continue
		}
		if m["type"] != "tuic" {
			t.Errorf("expected type=tuic, got %v", m["type"])
		}
		tls, ok := m["tls"].(map[string]any)
		if !ok {
			t.Error("expected tls block in tuic inbound")
		} else if tls["enabled"] != true {
			t.Errorf("expected tls.enabled=true, got %v", tls["enabled"])
		}
		users, _ := m["users"].([]any)
		if len(users) != 1 {
			t.Fatalf("expected 1 user, got %d", len(users))
		}
		u := users[0].(map[string]any)
		if u["name"] != "user-alice" {
			t.Errorf("expected name=user-alice, got %v", u["name"])
		}
		if _, hasUUID := u["uuid"]; !hasUUID {
			t.Error("expected uuid field in tuic user")
		}
		if _, hasPassword := u["password"]; !hasPassword {
			t.Error("expected password field in tuic user")
		}
	}
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Test: OutboundNodeOutbounds — AllowedInbounds defensive filter
// ---------------------------------------------------------------------------
func TestConfigEngine_OutboundNodeOutbounds_AllowedInboundsFilter(t *testing.T) {
	// Inbound node A with outbound nodes B (AllowedInbounds=[A]) and C (AllowedInbounds=[OtherNode])
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"

	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeB.Spec.AllowedInbounds = []string{"node-a"} // allows node-a

	nodeC := makeNode("node-c", "9.9.9.9", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)
	nodeC.Spec.AllowedInbounds = []string{"OtherNode"} // does NOT allow node-a

	user := makeUser("user-alice")

	input := configengine.Input{
		Node:          nodeA,
		Users:         []*v1alpha1.User{user},
		UserCreds:     map[string]configengine.UserCredential{"user-alice": {UUID: "aaaa-1111"}},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB,
			"node-c": nodeC,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	// node-b is allowed (AllowedInbounds includes node-a) → must appear
	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("expected outbound-node-b (AllowedInbounds matches), got %v", obs)
	}
	// node-c is NOT allowed (AllowedInbounds=[OtherNode]) → must NOT appear
	if containsTag(obs, "outbound-node-c") {
		t.Errorf("outbound-node-c must NOT appear when AllowedInbounds=[OtherNode] and inbound is node-a, got %v", obs)
	}
	// direct outbound always present
	if !containsTag(obs, "direct") {
		t.Errorf("missing direct outbound, got %v", obs)
	}
}

// ---------------------------------------------------------------------------
// Test: buildRouteOutbounds — AllowedInbounds defensive filter
// ---------------------------------------------------------------------------
func TestConfigEngine_RouteOutbounds_AllowedInboundsFilter(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"

	nodeB := makeNode("node-b", "5.6.7.8", "us-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeB.Spec.AllowedInbounds = []string{"node-a"} // allows node-a

	nodeC := makeNode("node-c", "9.9.9.9", "us-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)
	nodeC.Spec.AllowedInbounds = []string{"OtherNode"} // does NOT allow node-a

	user := makeUser("user-alice")
	routeToB := makeRoute("route-a-to-b", "node-a", "node-b")
	routeToC := makeRoute("route-a-to-c", "node-a", "node-c")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		Routes: []*v1alpha1.CustomRoute{routeToB, routeToC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB,
			"node-c": nodeC,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	// route to node-b is allowed → outbound-node-b must appear
	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("expected outbound-node-b (AllowedInbounds matches), got %v", obs)
	}
	// route to node-c is NOT allowed → outbound-node-c must NOT appear
	if containsTag(obs, "outbound-node-c") {
		t.Errorf("outbound-node-c must NOT appear when AllowedInbounds=[OtherNode] and inbound is node-a, got %v", obs)
	}
	if !containsTag(obs, "direct") {
		t.Errorf("missing direct outbound, got %v", obs)
	}
}

// ---------------------------------------------------------------------------
// Test: AllowedInbounds empty — backward compat, all outbounds included
// ---------------------------------------------------------------------------
func TestConfigEngine_AllowedInboundsEmpty_BackwardCompat(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"

	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	// AllowedInbounds not set (nil/empty) = allow all

	nodeC := makeNode("node-c", "9.9.9.9", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)
	// AllowedInbounds not set (nil/empty) = allow all

	user := makeUser("user-alice")

	input := configengine.Input{
		Node:          nodeA,
		Users:         []*v1alpha1.User{user},
		UserCreds:     map[string]configengine.UserCredential{"user-alice": {UUID: "aaaa-1111"}},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB,
			"node-c": nodeC,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	// Both outbound nodes must appear (backward compat)
	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("expected outbound-node-b (AllowedInbounds empty=allow all), got %v", obs)
	}
	if !containsTag(obs, "outbound-node-c") {
		t.Errorf("expected outbound-node-c (AllowedInbounds empty=allow all), got %v", obs)
	}
}

// ---------------------------------------------------------------------------
// Test: buildExperimentalConfig and buildRouteInbounds — regression: pre-filtered
// OutboundNodes should not crash and produce correct structure
// ---------------------------------------------------------------------------
func TestConfigEngine_DefensiveFilterRegression(t *testing.T) {
	// Node A (inbound) with node B (AllowedInbounds matches), node C (mismatch)
	// buildExperimentalConfig and buildRouteInbounds receive pre-filtered OutboundNodes
	// (simulating controller pre-filter having already removed node C).
	// They should produce correct output without crashing.
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"

	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeB.Spec.AllowedInbounds = []string{"node-a"}

	nodeC := makeNode("node-c", "9.9.9.9", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)
	nodeC.Spec.AllowedInbounds = []string{"node-a"}

	alice := makeUser("user-alice")
	bob := makeUser("user-bob")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{alice, bob},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
			"user-bob":   {UUID: "bbbb-2222"},
		},
		// Both outbound nodes are already allowed (simulating pre-filter)
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB,
			"node-c": nodeC,
		},
		UsageCollectionEnabled: true,
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)
	obs := outboundTags(t, cfg)

	// Inbound must exist and be correct
	if !containsTag(ibs, "inbound-vless") {
		t.Errorf("missing inbound-vless, got %v", ibs)
	}

	// Outbounds: both node-b and node-c must appear
	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("expected outbound-node-b, got %v", obs)
	}
	if !containsTag(obs, "outbound-node-c") {
		t.Errorf("expected outbound-node-c, got %v", obs)
	}

	// Experimental block must exist with correct stats users
	exp, ok := cfg["experimental"].(map[string]any)
	if !ok {
		t.Fatal("expected experimental key in config")
	}
	v2ray, ok := exp["v2ray_api"].(map[string]any)
	if !ok {
		t.Fatal("expected experimental.v2ray_api key")
	}
	stats, ok := v2ray["stats"].(map[string]any)
	if !ok {
		t.Fatal("expected experimental.v2ray_api.stats key")
	}
	rawUsers, _ := stats["users"].([]any)
	// 2 users × 2 outbound nodes = 4 virtual users
	if len(rawUsers) != 4 {
		t.Errorf("expected 4 stats users, got %d: %v", len(rawUsers), rawUsers)
	}
	userSet := make(map[string]bool)
	for _, u := range rawUsers {
		userSet[u.(string)] = true
	}
	for _, expected := range []string{
		"user-alice#node-b", "user-alice#node-c",
		"user-bob#node-b", "user-bob#node-c",
	} {
		if !userSet[expected] {
			t.Errorf("missing stats user %q, got %v", expected, userSet)
		}
	}

	// Routing rules: 2 rules (one per outbound node)
	rules := routeRulesOf(t, cfg)
	if len(rules) != 2 {
		t.Fatalf("expected 2 routing rules, got %d", len(rules))
	}
}

// ---------------------------------------------------------------------------
// Test: Self-outbound node S with AllowedInbounds=[S] — another inbound X
// must NOT see outbound-S in its config.
// ---------------------------------------------------------------------------
func TestConfigEngine_SelfOutbound_AllowedInboundsRestrictsPeers(t *testing.T) {
	// Node S: dual-role (inbound+outbound), AllowedInbounds=[S]
	nodeS := makeNode("node-s", "10.0.10.1", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound, v1alpha1.ProxyRoleOutbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeS.Spec.InboundProtocol = "vless"
	nodeS.Spec.AllowedInbounds = []string{"node-s"} // only allows itself

	// Node X: inbound-only, same region, not in S's AllowedInbounds
	nodeX := makeNode("node-x", "10.0.10.2", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10444}},
		10809,
	)
	nodeX.Spec.InboundProtocol = "vless"

	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  nodeX,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeS},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-s": {Username: "relay-u", Password: "relay-p"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"node-s": nodeS},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	// node-s should NOT appear because X is not in S's AllowedInbounds
	if containsTag(obs, "outbound-node-s") {
		t.Errorf("outbound-node-s must NOT appear when AllowedInbounds=[S] and inbound is X, got %v", obs)
	}
	// direct outbound should still be present
	if !containsTag(obs, "direct") {
		t.Errorf("missing direct outbound, got %v", obs)
	}
}

// ---------------------------------------------------------------------------
// Test: Self-outbound node S with AllowedInbounds=[S] in a mixed scenario —
// another inbound X should see outbound-Y but NOT outbound-S.
// ---------------------------------------------------------------------------
func TestConfigEngine_SelfOutbound_AllowedInboundsWithPeer(t *testing.T) {
	// Node S: dual-role (inbound+outbound), AllowedInbounds=[S]
	nodeS := makeNode("node-s", "10.0.11.1", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound, v1alpha1.ProxyRoleOutbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeS.Spec.InboundProtocol = "vless"
	nodeS.Spec.AllowedInbounds = []string{"node-s"}

	// Node Y: outbound-only, no AllowedInbounds restriction (backward compat)
	nodeY := makeNode("node-y", "10.0.11.2", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 31962,
	)

	// Node X: inbound-only, same region
	nodeX := makeNode("node-x", "10.0.11.3", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10444}},
		10809,
	)
	nodeX.Spec.InboundProtocol = "vless"

	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  nodeX,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeS, nodeY},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-s": {Username: "rs-u", Password: "rs-p"},
			"node-y": {Username: "ry-u", Password: "ry-p"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-s": nodeS,
			"node-y": nodeY,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	// node-s should NOT appear (X not in AllowedInbounds)
	if containsTag(obs, "outbound-node-s") {
		t.Errorf("outbound-node-s must NOT appear when AllowedInbounds=[S], got %v", obs)
	}
	// node-y should appear (no AllowedInbounds restriction → backward compat)
	if !containsTag(obs, "outbound-node-y") {
		t.Errorf("expected outbound-node-y (no AllowedInbounds restriction), got %v", obs)
	}
	// direct should be present
	if !containsTag(obs, "direct") {
		t.Errorf("missing direct outbound, got %v", obs)
	}
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Test: AllowedOutbounds filtering across 5 scenarios
// ---------------------------------------------------------------------------

// Scenario 1: Regression — empty AllowedOutbounds generates all same-region outbounds
func TestConfigEngine_AllowedOutbounds_Empty_Regression(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	// AllowedOutbounds left empty → should generate all same-region outbounds

	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeC := makeNode("node-c", "9.9.9.9", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)

	user := makeUser("user-alice")
	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB, "node-c": nodeC,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("expected outbound-node-b (empty AllowedOutbounds=allow all), got %v", obs)
	}
	if !containsTag(obs, "outbound-node-c") {
		t.Errorf("expected outbound-node-c (empty AllowedOutbounds=allow all), got %v", obs)
	}
}

// Scenario 2: Non-empty AllowedOutbounds whitelist — only whitelisted outbounds generated
func TestConfigEngine_AllowedOutbounds_WhitelistFilter(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	nodeA.Spec.AllowedOutbounds = []string{"node-b"} // only allow B

	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeC := makeNode("node-c", "9.9.9.9", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)

	user := makeUser("user-alice")
	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB, "node-c": nodeC,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("expected outbound-node-b (whitelisted), got %v", obs)
	}
	if containsTag(obs, "outbound-node-c") {
		t.Errorf("outbound-node-c must NOT appear when AllowedOutbounds=[node-b], got %v", obs)
	}
}

// Scenario 3: Non-empty AllowedOutbounds + CustomRoute target not in whitelist → route outbound not generated
func TestConfigEngine_AllowedOutbounds_CustomRouteGated(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	nodeA.Spec.AllowedOutbounds = []string{"node-b"} // only allow B

	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeC := makeNode("node-c", "9.9.9.9", "eu-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)

	// CustomRoute explicitly binds A → C, but C is not in AllowedOutbounds
	routeToC := makeRoute("route-a-to-c", "node-a", "node-c")

	user := makeUser("user-alice")
	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeC},
		Routes:        []*v1alpha1.CustomRoute{routeToC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB, "node-c": nodeC,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("expected outbound-node-b (whitelisted), got %v", obs)
	}
	if containsTag(obs, "outbound-node-c") {
		t.Errorf("outbound-node-c must NOT appear — CustomRoute target node-c is not in AllowedOutbounds=[node-b], got %v", obs)
	}
}

// Scenario 4: AND-gate — dual-non-empty AllowedOutbounds + AllowedInbounds
func TestConfigEngine_AllowedOutbounds_AndGate(t *testing.T) {
	// Sub-case 1: A.AllowedOutbounds=[B] && B.AllowedInbounds=[A] → connected
	t.Run("both-allow-connected", func(t *testing.T) {
		nodeA := makeNode("node-a", "1.2.3.4", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
			[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
			10808,
		)
		nodeA.Spec.InboundProtocol = "vless"
		nodeA.Spec.AllowedOutbounds = []string{"node-b"}

		nodeB := makeNode("node-b", "5.6.7.8", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
		)
		nodeB.Spec.AllowedInbounds = []string{"node-a"} // allows node-a

		user := makeUser("user-alice")
		input := configengine.Input{
			Node:  nodeA,
			Users: []*v1alpha1.User{user},
			UserCreds: map[string]configengine.UserCredential{
				"user-alice": {UUID: "aaaa-1111"},
			},
			OutboundNodes: []*v1alpha1.SingBoxNode{nodeB},
			NodeCreds: map[string]configengine.NodeCredential{
				"node-b": {Username: "ub", Password: "pb"},
			},
			OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
				"node-b": nodeB,
			},
		}

		out, err := configengine.Compute(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg := parseConfig(t, out)
		obs := outboundTags(t, cfg)

		if !containsTag(obs, "outbound-node-b") {
			t.Errorf("expected outbound-node-b when both sides allow, got %v", obs)
		}
	})

	// Sub-case 2: A.AllowedOutbounds=[B] && B.AllowedInbounds=[C] → disconnected (B rejects A)
	t.Run("outbound-rejects-disconnected", func(t *testing.T) {
		nodeA := makeNode("node-a", "1.2.3.4", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
			[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
			10808,
		)
		nodeA.Spec.InboundProtocol = "vless"
		nodeA.Spec.AllowedOutbounds = []string{"node-b"}

		nodeB := makeNode("node-b", "5.6.7.8", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
		)
		nodeB.Spec.AllowedInbounds = []string{"node-c"} // does NOT allow node-a

		nodeC := makeNode("node-c", "9.9.9.9", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
		)

		user := makeUser("user-alice")
		input := configengine.Input{
			Node:  nodeA,
			Users: []*v1alpha1.User{user},
			UserCreds: map[string]configengine.UserCredential{
				"user-alice": {UUID: "aaaa-1111"},
			},
			OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeC},
			NodeCreds: map[string]configengine.NodeCredential{
				"node-b": {Username: "ub", Password: "pb"},
				"node-c": {Username: "uc", Password: "pc"},
			},
			OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
				"node-b": nodeB, "node-c": nodeC,
			},
		}

		out, err := configengine.Compute(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg := parseConfig(t, out)
		obs := outboundTags(t, cfg)

		if containsTag(obs, "outbound-node-b") {
			t.Errorf("outbound-node-b must NOT appear when B.AllowedInbounds=[C] rejects A, got %v", obs)
		}
		// node-c should also not appear — not in AllowedOutbounds
		if containsTag(obs, "outbound-node-c") {
			t.Errorf("outbound-node-c must NOT appear — not in AllowedOutbounds=[node-b], got %v", obs)
		}
	})
}

// Scenario 5: Self-as-outbound — dual-role node with AllowedOutbounds=[self]
func TestConfigEngine_AllowedOutbounds_SelfAsOutbound(t *testing.T) {
	nodeS := makeNode("node-s", "10.0.10.1", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound, v1alpha1.ProxyRoleOutbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeS.Spec.InboundProtocol = "vless"
	nodeS.Spec.AllowedOutbounds = []string{"node-s"} // only allow self

	nodeY := makeNode("node-y", "10.0.10.2", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)

	user := makeUser("user-alice")
	input := configengine.Input{
		Node:  nodeS,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeS, nodeY},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-s": {Username: "rs-u", Password: "rs-p"},
			"node-y": {Username: "ry-u", Password: "ry-p"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-s": nodeS, "node-y": nodeY,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	if !containsTag(obs, "outbound-node-s") {
		t.Errorf("expected outbound-node-s (self allowed), got %v", obs)
	}
	if containsTag(obs, "outbound-node-y") {
		t.Errorf("outbound-node-y must NOT appear when AllowedOutbounds=[node-s], got %v", obs)
	}
}

// Scenario 6: Self-as-outbound DENIED — dual-role node with AllowedOutbounds=[other]
// Self direct outbound MUST NOT appear when AllowedOutbounds does not include self.
func TestConfigEngine_AllowedOutbounds_SelfAsOutboundDenied(t *testing.T) {
	nodeS := makeNode("node-s", "10.0.10.1", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound, v1alpha1.ProxyRoleOutbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeS.Spec.InboundProtocol = "vless"
	nodeS.Spec.AllowedOutbounds = []string{"other-node"} // self NOT allowed

	nodeY := makeNode("other-node", "10.0.10.2", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)

	user := makeUser("user-alice")
	input := configengine.Input{
		Node:  nodeS,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeS, nodeY},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-s": {Username: "rs-u", Password: "rs-p"},
			"node-y": {Username: "ry-u", Password: "ry-p"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-s": nodeS, "other-node": nodeY,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	if containsTag(obs, "outbound-node-s") {
		t.Errorf("outbound-node-s must NOT appear when AllowedOutbounds=[other-node], got %v", obs)
	}
	// other-node should appear as a valid same-region outbound
	if !containsTag(obs, "outbound-other-node") {
		t.Errorf("expected outbound-other-node to appear for same-region outbound, got %v", obs)
	}
}

// ---------------------------------------------------------------------------
// Test: tuic inbound with 2 outbound nodes — 2 virtual users, distinct UUIDs
// ---------------------------------------------------------------------------
func TestConfigEngine_TUICVirtualUsers(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "tuic", Port: 10443}},
		0,
	)
	nodeA.Spec.InboundProtocol = "tuic"
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeC := makeNode("node-c", "9.10.11.12", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	user := makeUser("user-alice")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "s3cr3t-uuid"},
		},
		OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "relay-user-b", Password: "relay-pass-b"},
			"node-c": {Username: "relay-user-c", Password: "relay-pass-c"},
		},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
			"node-b": nodeB,
			"node-c": nodeC,
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-tuic" {
			continue
		}
		tls, ok := m["tls"].(map[string]any)
		if !ok {
			t.Error("expected tls block in tuic virtual-user inbound")
		} else if tls["enabled"] != true {
			t.Errorf("expected tls.enabled=true, got %v", tls["enabled"])
		}
		users, _ := m["users"].([]any)
		if len(users) != 2 {
			t.Fatalf("expected 2 virtual users, got %d", len(users))
		}
		uuids := make(map[string]bool)
		for _, vu := range users {
			u := vu.(map[string]any)
			name, _ := u["name"].(string)
			if name != "user-alice#node-b" && name != "user-alice#node-c" {
				t.Errorf("unexpected virtual user name: %v", name)
			}
			if _, hasUUID := u["uuid"]; !hasUUID {
				t.Errorf("tuic virtual user %q must have uuid field", name)
			}
			if _, hasPassword := u["password"]; !hasPassword {
				t.Errorf("tuic virtual user %q must have password field", name)
			}
			uuids[u["uuid"].(string)] = true
		}
		if len(uuids) != 2 {
			t.Error("expected two distinct UUIDs for the two TUIC virtual users")
		}
	}
}

// ---------------------------------------------------------------------------
// Test: ExternalOutbound per-protocol rendered shape (auto-discovery path)
// ---------------------------------------------------------------------------

func makeExternalOutbound(name string, protocol v1alpha1.ExternalOutboundProtocol, server string, port int32) *v1alpha1.ExternalOutbound {
	return &v1alpha1.ExternalOutbound{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ExternalOutboundSpec{
			Protocol: protocol,
			Server:   server,
			Port:     port,
		},
	}
}

func outboundByTag(t *testing.T, cfg map[string]any, tag string) map[string]any {
	t.Helper()
	for _, ob := range outboundsOf(t, cfg) {
		m, _ := ob.(map[string]any)
		if m["tag"] == tag {
			return m
		}
	}
	return nil
}

func TestConfigEngine_ExternalOutboundProtocols(t *testing.T) {
	cases := []struct {
		name   string
		eob    *v1alpha1.ExternalOutbound
		creds  configengine.ExternalCredential
		verify func(t *testing.T, ob map[string]any)
	}{
		{
			name:  "socks5-no-auth",
			eob:   makeExternalOutbound("ext-socks", v1alpha1.ExternalProtocolSocks5, "10.1.1.1", 1080),
			creds: configengine.ExternalCredential{},
			verify: func(t *testing.T, ob map[string]any) {
				if ob["type"] != "socks" {
					t.Errorf("expected type=socks, got %v", ob["type"])
				}
				if ob["version"] != "5" {
					t.Errorf("expected version=5, got %v", ob["version"])
				}
				if _, ok := ob["username"]; ok {
					t.Errorf("socks5 outbound without creds must not have username, got %v", ob["username"])
				}
				if _, ok := ob["password"]; ok {
					t.Errorf("socks5 outbound without creds must not have password, got %v", ob["password"])
				}
			},
		},
		{
			name:  "socks5-with-auth",
			eob:   makeExternalOutbound("ext-socks", v1alpha1.ExternalProtocolSocks5, "10.1.1.1", 1080),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyUsername: "u1", v1alpha1.CredKeyPassword: "p1"},
			verify: func(t *testing.T, ob map[string]any) {
				if ob["type"] != "socks" {
					t.Errorf("expected type=socks, got %v", ob["type"])
				}
				if ob["username"] != "u1" {
					t.Errorf("expected username=u1, got %v", ob["username"])
				}
				if ob["password"] != "p1" {
					t.Errorf("expected password=p1, got %v", ob["password"])
				}
			},
		},
		{
			name:  "http-no-tls",
			eob:   makeExternalOutbound("ext-http", v1alpha1.ExternalProtocolHTTP, "10.1.1.2", 8080),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyUsername: "u2", v1alpha1.CredKeyPassword: "p2"},
			verify: func(t *testing.T, ob map[string]any) {
				if ob["type"] != "http" {
					t.Errorf("expected type=http, got %v", ob["type"])
				}
				if ob["username"] != "u2" || ob["password"] != "p2" {
					t.Errorf("expected username=u2 password=p2, got %v/%v", ob["username"], ob["password"])
				}
				if _, ok := ob["tls"]; ok {
					t.Error("http outbound without spec.tls must not have a tls block")
				}
			},
		},
		{
			name: "http-with-tls",
			eob: func() *v1alpha1.ExternalOutbound {
				e := makeExternalOutbound("ext-http", v1alpha1.ExternalProtocolHTTP, "10.1.1.2", 8443)
				e.Spec.TLS = &v1alpha1.ExternalOutboundTLS{ServerName: "example.com", Insecure: true}
				return e
			}(),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyUsername: "u2", v1alpha1.CredKeyPassword: "p2"},
			verify: func(t *testing.T, ob map[string]any) {
				tls, ok := ob["tls"].(map[string]any)
				if !ok {
					t.Fatal("expected tls block in http outbound")
				}
				if tls["enabled"] != true {
					t.Errorf("expected tls.enabled=true, got %v", tls["enabled"])
				}
				if tls["server_name"] != "example.com" {
					t.Errorf("expected tls.server_name=example.com, got %v", tls["server_name"])
				}
				if tls["insecure"] != true {
					t.Errorf("expected tls.insecure=true, got %v", tls["insecure"])
				}
			},
		},
		{
			name:  "shadowsocks",
			eob:   makeExternalOutbound("ext-ss", v1alpha1.ExternalProtocolShadowsocks, "10.1.1.3", 8388),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyMethod: "2022-blake3-aes-128-gcm", v1alpha1.CredKeyPassword: "ss-pass"},
			verify: func(t *testing.T, ob map[string]any) {
				if ob["type"] != "shadowsocks" {
					t.Errorf("expected type=shadowsocks, got %v", ob["type"])
				}
				if ob["method"] != "2022-blake3-aes-128-gcm" {
					t.Errorf("expected method=2022-blake3-aes-128-gcm, got %v", ob["method"])
				}
				if ob["password"] != "ss-pass" {
					t.Errorf("expected password=ss-pass, got %v", ob["password"])
				}
			},
		},
		{
			name: "trojan",
			eob: func() *v1alpha1.ExternalOutbound {
				e := makeExternalOutbound("ext-trojan", v1alpha1.ExternalProtocolTrojan, "10.1.1.4", 443)
				e.Spec.TLS = &v1alpha1.ExternalOutboundTLS{ServerName: "trojan.example.com"}
				return e
			}(),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyPassword: "trojan-pass"},
			verify: func(t *testing.T, ob map[string]any) {
				if ob["type"] != "trojan" {
					t.Errorf("expected type=trojan, got %v", ob["type"])
				}
				if ob["password"] != "trojan-pass" {
					t.Errorf("expected password=trojan-pass, got %v", ob["password"])
				}
				tls, ok := ob["tls"].(map[string]any)
				if !ok {
					t.Fatal("expected tls block in trojan outbound")
				}
				if tls["enabled"] != true || tls["server_name"] != "trojan.example.com" {
					t.Errorf("expected tls enabled with server_name=trojan.example.com, got %v", tls)
				}
				if _, ok := tls["insecure"]; ok {
					t.Errorf("tls.insecure must be omitted when false, got %v", tls["insecure"])
				}
			},
		},
		{
			name: "hysteria2",
			eob: func() *v1alpha1.ExternalOutbound {
				e := makeExternalOutbound("ext-hy2", v1alpha1.ExternalProtocolHysteria2, "10.1.1.5", 30443)
				e.Spec.TLS = &v1alpha1.ExternalOutboundTLS{ServerName: "hy2.example.com"}
				e.Spec.Hysteria2 = &v1alpha1.Hysteria2Options{UpMbps: 100, DownMbps: 200, Obfs: true}
				return e
			}(),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyPassword: "hy2-pass", v1alpha1.CredKeyObfsPassword: "obfs-pass"},
			verify: func(t *testing.T, ob map[string]any) {
				if ob["type"] != "hysteria2" {
					t.Errorf("expected type=hysteria2, got %v", ob["type"])
				}
				if ob["password"] != "hy2-pass" {
					t.Errorf("expected password=hy2-pass, got %v", ob["password"])
				}
				if ob["up_mbps"].(float64) != 100 {
					t.Errorf("expected up_mbps=100, got %v", ob["up_mbps"])
				}
				if ob["down_mbps"].(float64) != 200 {
					t.Errorf("expected down_mbps=200, got %v", ob["down_mbps"])
				}
				obfs, ok := ob["obfs"].(map[string]any)
				if !ok {
					t.Fatal("expected obfs block in hysteria2 outbound")
				}
				if obfs["type"] != "salamander" || obfs["password"] != "obfs-pass" {
					t.Errorf("expected obfs type=salamander password=obfs-pass, got %v", obfs)
				}
				tls, ok := ob["tls"].(map[string]any)
				if !ok || tls["server_name"] != "hy2.example.com" {
					t.Errorf("expected tls block with server_name=hy2.example.com, got %v", ob["tls"])
				}
			},
		},
		{
			name: "hysteria2-no-bandwidth-no-obfs",
			eob: func() *v1alpha1.ExternalOutbound {
				e := makeExternalOutbound("ext-hy2", v1alpha1.ExternalProtocolHysteria2, "10.1.1.5", 30443)
				e.Spec.TLS = &v1alpha1.ExternalOutboundTLS{ServerName: "hy2.example.com"}
				e.Spec.Hysteria2 = &v1alpha1.Hysteria2Options{}
				return e
			}(),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyPassword: "hy2-pass"},
			verify: func(t *testing.T, ob map[string]any) {
				if _, ok := ob["up_mbps"]; ok {
					t.Errorf("up_mbps must be omitted when 0, got %v", ob["up_mbps"])
				}
				if _, ok := ob["down_mbps"]; ok {
					t.Errorf("down_mbps must be omitted when 0, got %v", ob["down_mbps"])
				}
				if _, ok := ob["obfs"]; ok {
					t.Error("obfs must be omitted when disabled")
				}
			},
		},
		{
			name: "tuic",
			eob: func() *v1alpha1.ExternalOutbound {
				e := makeExternalOutbound("ext-tuic", v1alpha1.ExternalProtocolTUIC, "10.1.1.6", 10443)
				e.Spec.TLS = &v1alpha1.ExternalOutboundTLS{ServerName: "tuic.example.com"}
				return e
			}(),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyUUID: "tuic-uuid", v1alpha1.CredKeyPassword: "tuic-pass"},
			verify: func(t *testing.T, ob map[string]any) {
				if ob["type"] != "tuic" {
					t.Errorf("expected type=tuic, got %v", ob["type"])
				}
				if ob["uuid"] != "tuic-uuid" {
					t.Errorf("expected uuid=tuic-uuid, got %v", ob["uuid"])
				}
				if ob["password"] != "tuic-pass" {
					t.Errorf("expected password=tuic-pass, got %v", ob["password"])
				}
				tls, ok := ob["tls"].(map[string]any)
				if !ok || tls["server_name"] != "tuic.example.com" {
					t.Errorf("expected tls block with server_name=tuic.example.com, got %v", ob["tls"])
				}
			},
		},
		{
			name: "anytls",
			eob: func() *v1alpha1.ExternalOutbound {
				e := makeExternalOutbound("ext-anytls", v1alpha1.ExternalProtocolAnyTLS, "10.1.1.7", 11443)
				e.Spec.TLS = &v1alpha1.ExternalOutboundTLS{ServerName: "anytls.example.com"}
				return e
			}(),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyPassword: "anytls-pass"},
			verify: func(t *testing.T, ob map[string]any) {
				if ob["type"] != "anytls" {
					t.Errorf("expected type=anytls, got %v", ob["type"])
				}
				if ob["password"] != "anytls-pass" {
					t.Errorf("expected password=anytls-pass, got %v", ob["password"])
				}
				tls, ok := ob["tls"].(map[string]any)
				if !ok || tls["server_name"] != "anytls.example.com" {
					t.Errorf("expected tls block with server_name=anytls.example.com, got %v", ob["tls"])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodeA := makeNode("node-a", "1.2.3.4", "us-west",
				[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
				10808,
			)
			nodeA.Spec.InboundProtocol = "vless"
			user := makeUser("user-alice")

			input := configengine.Input{
				Node:                    nodeA,
				Users:                   []*v1alpha1.User{user},
				UserCreds:               map[string]configengine.UserCredential{"user-alice": {UUID: "aaaa-1111"}},
				OutboundNodesByName:     map[string]*v1alpha1.SingBoxNode{},
				ExternalOutbounds:       []*v1alpha1.ExternalOutbound{tc.eob},
				ExternalOutboundsByName: map[string]*v1alpha1.ExternalOutbound{tc.eob.Name: tc.eob},
				ExternalCreds:           map[string]configengine.ExternalCredential{tc.eob.Name: tc.creds},
			}

			out, err := configengine.Compute(input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			cfg := parseConfig(t, out)

			tag := "outbound-" + tc.eob.Name
			ob := outboundByTag(t, cfg, tag)
			if ob == nil {
				t.Fatalf("missing outbound %q, got %v", tag, outboundTags(t, cfg))
			}
			if ob["server"] != tc.eob.Spec.Server {
				t.Errorf("expected server=%s, got %v", tc.eob.Spec.Server, ob["server"])
			}
			if ob["server_port"].(float64) != float64(tc.eob.Spec.Port) {
				t.Errorf("expected server_port=%d, got %v", tc.eob.Spec.Port, ob["server_port"])
			}
			tc.verify(t, ob)
		})
	}
}

// ---------------------------------------------------------------------------
// Test: ExternalOutbound with missing required credentials is skipped entirely
// (no outbound entry, no virtual users, no route rules)
// ---------------------------------------------------------------------------
func TestConfigEngine_ExternalOutboundMissingCreds(t *testing.T) {
	cases := []struct {
		name  string
		eob   *v1alpha1.ExternalOutbound
		creds configengine.ExternalCredential
	}{
		{
			name:  "shadowsocks-missing-method",
			eob:   makeExternalOutbound("ext-bad", v1alpha1.ExternalProtocolShadowsocks, "10.2.1.1", 8388),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyPassword: "ss-pass"},
		},
		{
			name:  "shadowsocks-missing-password",
			eob:   makeExternalOutbound("ext-bad", v1alpha1.ExternalProtocolShadowsocks, "10.2.1.1", 8388),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyMethod: "aes-128-gcm"},
		},
		{
			name:  "trojan-missing-password",
			eob:   makeExternalOutbound("ext-bad", v1alpha1.ExternalProtocolTrojan, "10.2.1.2", 443),
			creds: configengine.ExternalCredential{},
		},
		{
			name:  "hysteria2-missing-password",
			eob:   makeExternalOutbound("ext-bad", v1alpha1.ExternalProtocolHysteria2, "10.2.1.3", 30443),
			creds: configengine.ExternalCredential{},
		},
		{
			name: "hysteria2-obfs-missing-obfs-password",
			eob: func() *v1alpha1.ExternalOutbound {
				e := makeExternalOutbound("ext-bad", v1alpha1.ExternalProtocolHysteria2, "10.2.1.3", 30443)
				e.Spec.Hysteria2 = &v1alpha1.Hysteria2Options{Obfs: true}
				return e
			}(),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyPassword: "hy2-pass"},
		},
		{
			name:  "tuic-missing-uuid",
			eob:   makeExternalOutbound("ext-bad", v1alpha1.ExternalProtocolTUIC, "10.2.1.4", 10443),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyPassword: "tuic-pass"},
		},
		{
			name:  "tuic-missing-password",
			eob:   makeExternalOutbound("ext-bad", v1alpha1.ExternalProtocolTUIC, "10.2.1.4", 10443),
			creds: configengine.ExternalCredential{v1alpha1.CredKeyUUID: "tuic-uuid"},
		},
		{
			name:  "anytls-missing-password",
			eob:   makeExternalOutbound("ext-bad", v1alpha1.ExternalProtocolAnyTLS, "10.2.1.5", 11443),
			creds: configengine.ExternalCredential{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodeA := makeNode("node-a", "1.2.3.4", "us-west",
				[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
				[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
				10808,
			)
			nodeA.Spec.InboundProtocol = "vless"
			user := makeUser("user-alice")

			input := configengine.Input{
				Node:                nodeA,
				Users:               []*v1alpha1.User{user},
				UserCreds:           map[string]configengine.UserCredential{"user-alice": {UUID: "aaaa-1111"}},
				OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
				ExternalOutbounds:   []*v1alpha1.ExternalOutbound{tc.eob},
				ExternalOutboundsByName: map[string]*v1alpha1.ExternalOutbound{
					tc.eob.Name: tc.eob,
				},
				ExternalCreds:          map[string]configengine.ExternalCredential{tc.eob.Name: tc.creds},
				UsageCollectionEnabled: true,
			}

			out, err := configengine.Compute(input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			cfg := parseConfig(t, out)

			// No outbound entry for the broken external.
			obs := outboundTags(t, cfg)
			if containsTag(obs, "outbound-ext-bad") {
				t.Errorf("outbound-ext-bad must NOT appear when required creds are missing, got %v", obs)
			}

			// No route rules may point at the nonexistent outbound.
			rules := routeRulesOf(t, cfg)
			for _, rule := range rules {
				m := rule.(map[string]any)
				if m["outbound"] == "outbound-ext-bad" {
					t.Errorf("route rule must not point at skipped outbound-ext-bad: %v", m)
				}
			}

			// With the only outbound peer skipped the node falls back to plain
			// user inbounds (no virtual users).
			for _, ib := range inboundsOf(t, cfg) {
				m := ib.(map[string]any)
				users, _ := m["users"].([]any)
				for _, vu := range users {
					u := vu.(map[string]any)
					if u["name"] == "user-alice#ext-bad" {
						t.Errorf("virtual user user-alice#ext-bad must NOT appear for skipped outbound")
					}
				}
			}

			// No v2ray stats user for the skipped outbound either.
			exp, ok := cfg["experimental"].(map[string]any)
			if !ok {
				t.Fatal("expected experimental key in config")
			}
			stats := exp["v2ray_api"].(map[string]any)["stats"].(map[string]any)
			rawUsers, _ := stats["users"].([]any)
			for _, u := range rawUsers {
				if u.(string) == "user-alice#ext-bad" {
					t.Errorf("stats user user-alice#ext-bad must NOT appear for skipped outbound")
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test: ExternalOutbound name collision — the SingBoxNode always wins
// ---------------------------------------------------------------------------
func TestConfigEngine_ExternalOutboundNameCollision(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	user := makeUser("user-alice")

	// Collides with outbound peer node-b and with the node itself (node-a).
	extPeer := makeExternalOutbound("node-b", v1alpha1.ExternalProtocolTrojan, "9.9.9.9", 443)
	extSelf := makeExternalOutbound("node-a", v1alpha1.ExternalProtocolTrojan, "8.8.8.8", 443)

	input := configengine.Input{
		Node:                nodeA,
		Users:               []*v1alpha1.User{user},
		UserCreds:           map[string]configengine.UserCredential{"user-alice": {UUID: "aaaa-1111"}},
		OutboundNodes:       []*v1alpha1.SingBoxNode{nodeB},
		NodeCreds:           map[string]configengine.NodeCredential{"node-b": {Username: "ru", Password: "rp"}},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"node-b": nodeB},
		ExternalOutbounds:   []*v1alpha1.ExternalOutbound{extPeer, extSelf},
		ExternalOutboundsByName: map[string]*v1alpha1.ExternalOutbound{
			"node-b": extPeer,
			"node-a": extSelf,
		},
		ExternalCreds: map[string]configengine.ExternalCredential{
			"node-b": {v1alpha1.CredKeyPassword: "ext-pass"},
			"node-a": {v1alpha1.CredKeyPassword: "ext-pass"},
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := parseConfig(t, out)

	// Exactly one outbound-node-b, and it must be the SingBoxNode relay entry.
	ob := outboundByTag(t, cfg, "outbound-node-b")
	if ob == nil {
		t.Fatal("missing outbound-node-b (SingBoxNode relay)")
	}
	if ob["type"] != "socks" {
		t.Errorf("collision: outbound-node-b must be the socks relay entry, got type=%v", ob["type"])
	}
	if ob["server"] != "5.6.7.8" {
		t.Errorf("collision: outbound-node-b server must be the SingBoxNode address 5.6.7.8, got %v", ob["server"])
	}
	if ob["username"] != "ru" || ob["password"] != "rp" {
		t.Errorf("collision: outbound-node-b must carry the relay credentials, got %v/%v", ob["username"], ob["password"])
	}

	// The external node-a entry must be suppressed entirely.
	if ob := outboundByTag(t, cfg, "outbound-node-a"); ob != nil {
		t.Errorf("collision: external outbound-node-a must be skipped, got %v", ob)
	}

	// No route rule or virtual user may reference the external trojan server.
	count := 0
	for _, tag := range outboundTags(t, cfg) {
		if tag == "outbound-node-b" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected outbound-node-b exactly once, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Test: ExternalOutbound bound via kind=ExternalOutbound CustomRoute end-to-end
// ---------------------------------------------------------------------------
func TestConfigEngine_ExternalOutboundRouteBinding(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	user := makeUser("user-alice")

	eob := makeExternalOutbound("ext-1", v1alpha1.ExternalProtocolTrojan, "10.3.1.1", 443)
	eob.Spec.TLS = &v1alpha1.ExternalOutboundTLS{ServerName: "ext.example.com"}

	route := makeRoute("route-a-to-ext", "node-a", "ext-1")
	route.Spec.OutboundKind = v1alpha1.OutboundKindExternalOutbound

	input := configengine.Input{
		Node:                nodeA,
		Users:               []*v1alpha1.User{user},
		UserCreds:           map[string]configengine.UserCredential{"user-alice": {UUID: "aaaa-1111"}},
		Routes:              []*v1alpha1.CustomRoute{route},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
		// Route-only external: not present in the auto-discovery list.
		ExternalOutboundsByName: map[string]*v1alpha1.ExternalOutbound{"ext-1": eob},
		ExternalCreds:           map[string]configengine.ExternalCredential{"ext-1": {v1alpha1.CredKeyPassword: "ext-pass"}},
		UsageCollectionEnabled:  true,
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := parseConfig(t, out)
	ibs := inboundTags(t, cfg)
	obs := outboundTags(t, cfg)

	if !containsTag(ibs, "inbound-vless") {
		t.Errorf("missing inbound-vless, got %v", ibs)
	}
	if !containsTag(obs, "outbound-ext-1") {
		t.Fatalf("missing outbound-ext-1, got %v", obs)
	}

	ob := outboundByTag(t, cfg, "outbound-ext-1")
	if ob["type"] != "trojan" {
		t.Errorf("expected type=trojan, got %v", ob["type"])
	}
	if ob["server"] != "10.3.1.1" {
		t.Errorf("expected server=10.3.1.1, got %v", ob["server"])
	}
	if ob["password"] != "ext-pass" {
		t.Errorf("expected password=ext-pass, got %v", ob["password"])
	}
	if _, ok := ob["tls"].(map[string]any); !ok {
		t.Error("expected tls block in trojan outbound")
	}

	// Virtual user for the external outbound.
	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-vless" {
			continue
		}
		users := m["users"].([]any)
		if len(users) != 1 {
			t.Fatalf("expected 1 virtual user, got %d", len(users))
		}
		u := users[0].(map[string]any)
		if u["name"] != "user-alice#ext-1" {
			t.Errorf("expected virtual user user-alice#ext-1, got %v", u["name"])
		}
	}

	// Route rule auth_user -> outbound-ext-1.
	rules := routeRulesOf(t, cfg)
	if len(rules) != 1 {
		t.Fatalf("expected 1 routing rule, got %d", len(rules))
	}
	rule := rules[0].(map[string]any)
	if rule["outbound"] != "outbound-ext-1" {
		t.Errorf("expected rule outbound=outbound-ext-1, got %v", rule["outbound"])
	}
	authUsers, _ := rule["auth_user"].([]any)
	if len(authUsers) != 1 || authUsers[0].(string) != "user-alice#ext-1" {
		t.Errorf("expected auth_user=[user-alice#ext-1], got %v", authUsers)
	}

	// v2ray stats users include the external virtual user.
	exp := cfg["experimental"].(map[string]any)
	stats := exp["v2ray_api"].(map[string]any)["stats"].(map[string]any)
	rawUsers, _ := stats["users"].([]any)
	found := false
	for _, u := range rawUsers {
		if u.(string) == "user-alice#ext-1" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing stats user user-alice#ext-1, got %v", rawUsers)
	}
}

// ---------------------------------------------------------------------------
// Test: ExternalOutbound auto-discovery — the engine trusts the controller's
// region/allowedInbounds pre-filter: whatever lands in input.ExternalOutbounds
// is rendered, regardless of spec.region.
// ---------------------------------------------------------------------------
func TestConfigEngine_ExternalOutboundAutoDiscovery(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	user := makeUser("user-alice")

	// Same region as node-a — normal auto-discovery case.
	extSame := makeExternalOutbound("ext-auto", v1alpha1.ExternalProtocolSocks5, "10.4.1.1", 1080)
	extSame.Spec.Region = "us-west"
	// Different region — must STILL be rendered: the controller pre-filters,
	// the engine does not re-check spec.region.
	extOther := makeExternalOutbound("ext-other-region", v1alpha1.ExternalProtocolSocks5, "10.4.1.2", 1080)
	extOther.Spec.Region = "eu-central"

	input := configengine.Input{
		Node:                nodeA,
		Users:               []*v1alpha1.User{user},
		UserCreds:           map[string]configengine.UserCredential{"user-alice": {UUID: "aaaa-1111"}},
		OutboundNodes:       []*v1alpha1.SingBoxNode{nodeB},
		NodeCreds:           map[string]configengine.NodeCredential{"node-b": {Username: "ru", Password: "rp"}},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"node-b": nodeB},
		ExternalOutbounds:   []*v1alpha1.ExternalOutbound{extSame, extOther},
		ExternalOutboundsByName: map[string]*v1alpha1.ExternalOutbound{
			"ext-auto":         extSame,
			"ext-other-region": extOther,
		},
		ExternalCreds:          map[string]configengine.ExternalCredential{},
		UsageCollectionEnabled: true,
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	if !containsTag(obs, "outbound-ext-auto") {
		t.Errorf("missing outbound-ext-auto, got %v", obs)
	}
	if !containsTag(obs, "outbound-ext-other-region") {
		t.Errorf("engine must trust the controller pre-filter and render ext-other-region, got %v", obs)
	}

	// Virtual users: SingBoxNode names first, external names after, in order.
	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		if m["tag"] != "inbound-vless" {
			continue
		}
		users := m["users"].([]any)
		if len(users) != 3 {
			t.Fatalf("expected 3 virtual users, got %d", len(users))
		}
		wantOrder := []string{"user-alice#node-b", "user-alice#ext-auto", "user-alice#ext-other-region"}
		for i, want := range wantOrder {
			got := users[i].(map[string]any)["name"]
			if got != want {
				t.Errorf("virtual user %d: expected %q, got %v", i, want, got)
			}
		}
	}

	// One route rule per outbound.
	rules := routeRulesOf(t, cfg)
	if len(rules) != 3 {
		t.Fatalf("expected 3 routing rules, got %d", len(rules))
	}
	ruleTargets := make(map[string]bool)
	for _, rule := range rules {
		m := rule.(map[string]any)
		ruleTargets[m["outbound"].(string)] = true
	}
	for _, want := range []string{"outbound-node-b", "outbound-ext-auto", "outbound-ext-other-region"} {
		if !ruleTargets[want] {
			t.Errorf("missing routing rule for %s", want)
		}
	}

	// Stats users cover the external outbounds too.
	exp := cfg["experimental"].(map[string]any)
	stats := exp["v2ray_api"].(map[string]any)["stats"].(map[string]any)
	rawUsers, _ := stats["users"].([]any)
	if len(rawUsers) != 3 {
		t.Fatalf("expected 3 stats users, got %d: %v", len(rawUsers), rawUsers)
	}
	userSet := make(map[string]bool)
	for _, u := range rawUsers {
		userSet[u.(string)] = true
	}
	for _, want := range []string{"user-alice#node-b", "user-alice#ext-auto", "user-alice#ext-other-region"} {
		if !userSet[want] {
			t.Errorf("missing stats user %q, got %v", want, userSet)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: kind=ExternalOutbound route referencing an unresolved ExternalOutbound
// is skipped — no outbound entry, no virtual user, no route rule.
// ---------------------------------------------------------------------------
func TestConfigEngine_ExternalOutboundUnresolvedRoute(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	user := makeUser("user-alice")

	route := makeRoute("route-a-to-missing", "node-a", "ext-missing")
	route.Spec.OutboundKind = v1alpha1.OutboundKindExternalOutbound

	input := configengine.Input{
		Node:                    nodeA,
		Users:                   []*v1alpha1.User{user},
		UserCreds:               map[string]configengine.UserCredential{"user-alice": {UUID: "aaaa-1111"}},
		Routes:                  []*v1alpha1.CustomRoute{route},
		OutboundNodesByName:     map[string]*v1alpha1.SingBoxNode{},
		ExternalOutboundsByName: map[string]*v1alpha1.ExternalOutbound{},
		ExternalCreds:           map[string]configengine.ExternalCredential{},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := parseConfig(t, out)

	obs := outboundTags(t, cfg)
	if containsTag(obs, "outbound-ext-missing") {
		t.Errorf("outbound-ext-missing must NOT appear for an unresolved route, got %v", obs)
	}

	rules := routeRulesOf(t, cfg)
	for _, rule := range rules {
		m := rule.(map[string]any)
		if m["outbound"] == "outbound-ext-missing" {
			t.Errorf("route rule must not point at unresolved outbound-ext-missing: %v", m)
		}
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]any)
		users, _ := m["users"].([]any)
		for _, vu := range users {
			u := vu.(map[string]any)
			if u["name"] == "user-alice#ext-missing" {
				t.Errorf("virtual user user-alice#ext-missing must NOT appear for unresolved route")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Test: mixed valid and broken auto-discovered ExternalOutbounds — only the
// usable one is rendered; its dedup against a SingBoxNode-kind route works.
// ---------------------------------------------------------------------------
func TestConfigEngine_ExternalOutboundMixedValidBroken(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeA.Spec.InboundProtocol = "vless"
	user := makeUser("user-alice")

	extGood := makeExternalOutbound("ext-good", v1alpha1.ExternalProtocolHysteria2, "10.5.1.1", 30443)
	extGood.Spec.TLS = &v1alpha1.ExternalOutboundTLS{ServerName: "good.example.com"}
	extBad := makeExternalOutbound("ext-bad", v1alpha1.ExternalProtocolShadowsocks, "10.5.1.2", 8388)

	// Also bind ext-good by an explicit route: it must appear exactly once.
	route := makeRoute("route-a-to-good", "node-a", "ext-good")
	route.Spec.OutboundKind = v1alpha1.OutboundKindExternalOutbound

	input := configengine.Input{
		Node:                nodeA,
		Users:               []*v1alpha1.User{user},
		UserCreds:           map[string]configengine.UserCredential{"user-alice": {UUID: "aaaa-1111"}},
		Routes:              []*v1alpha1.CustomRoute{route},
		OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
		ExternalOutbounds:   []*v1alpha1.ExternalOutbound{extGood, extBad},
		ExternalOutboundsByName: map[string]*v1alpha1.ExternalOutbound{
			"ext-good": extGood,
			"ext-bad":  extBad,
		},
		ExternalCreds: map[string]configengine.ExternalCredential{
			"ext-good": {v1alpha1.CredKeyPassword: "good-pass"},
			// ext-bad: only a password, method missing → skipped.
			"ext-bad": {v1alpha1.CredKeyPassword: "bad-pass"},
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := parseConfig(t, out)
	obs := outboundTags(t, cfg)

	count := 0
	for _, tag := range obs {
		if tag == "outbound-ext-good" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected outbound-ext-good exactly once (auto+route dedup), got %d in %v", count, obs)
	}
	if containsTag(obs, "outbound-ext-bad") {
		t.Errorf("outbound-ext-bad must NOT appear (missing method cred), got %v", obs)
	}

	rules := routeRulesOf(t, cfg)
	if len(rules) != 1 {
		t.Fatalf("expected exactly 1 routing rule, got %d", len(rules))
	}
	if rules[0].(map[string]any)["outbound"] != "outbound-ext-good" {
		t.Errorf("expected rule for outbound-ext-good, got %v", rules[0])
	}
}

// ---------------------------------------------------------------------------
// RegressionUnload: peers without relayPort must not produce dangling
// virtual users / route rules / stats entries — no outbound entry is built
// for them. Same-node (direct) outbounds are exempt.
// ---------------------------------------------------------------------------
func TestConfigEngine_RelayPortMissing(t *testing.T) {
	newInbound := func() *v1alpha1.SingBoxNode {
		n := makeNode("in-a", "1.2.3.4", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
			[]v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 443}}, 0)
		return n
	}

	t.Run("region peer without relayPort is fully excluded", func(t *testing.T) {
		nodeA := makeNode("in-a", "1.2.3.4", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
			[]v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 443}}, 0)
		nodeB := makeNode("node-b", "5.6.7.8", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962)
		nodeNoRelay := makeNode("no-relay", "9.9.9.9", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 0)
		user := makeUser("user-alice")

		input := configengine.Input{
			Node:  nodeA,
			Users: []*v1alpha1.User{user},
			UserCreds: map[string]configengine.UserCredential{
				"user-alice": {UUID: "aaaa-1111"},
			},
			OutboundNodes: []*v1alpha1.SingBoxNode{nodeB, nodeNoRelay},
			NodeCreds: map[string]configengine.NodeCredential{
				"node-b": {Username: "ub", Password: "pb"},
			},
			OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{
				"node-b":   nodeB,
				"no-relay": nodeNoRelay,
			},
			UsageCollectionEnabled: true,
		}

		out, err := configengine.Compute(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg := parseConfig(t, out)

		// Virtual users: only node-b.
		for _, ib := range inboundsOf(t, cfg) {
			for _, u := range ib.(map[string]any)["users"].([]any) {
				name := u.(map[string]any)["name"].(string)
				if name != "user-alice#node-b" {
					t.Errorf("unexpected virtual user %q (no-relay must be excluded)", name)
				}
			}
		}

		// Route rules + outbounds: only outbound-node-b.
		rules := routeRulesOf(t, cfg)
		if len(rules) != 1 || rules[0].(map[string]any)["outbound"] != "outbound-node-b" {
			t.Errorf("expected single rule to outbound-node-b, got %v", rules)
		}
		tags := outboundTags(t, cfg)
		for _, tag := range tags {
			if tag == "outbound-no-relay" {
				t.Errorf("unexpected outbound entry outbound-no-relay")
			}
		}

		// Stats users: only #node-b.
		exp := cfg["experimental"].(map[string]any)
		statsUsers := exp["v2ray_api"].(map[string]any)["stats"].(map[string]any)["users"].([]any)
		if len(statsUsers) != 1 || statsUsers[0].(string) != "user-alice#node-b" {
			t.Errorf("expected stats users [user-alice#node-b], got %v", statsUsers)
		}
	})

	t.Run("route target without relayPort produces no rule", func(t *testing.T) {
		nodeA := newInbound()
		nodeNoRelay := makeNode("no-relay", "9.9.9.9", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 0)
		user := makeUser("user-alice")
		route := &v1alpha1.CustomRoute{
			Spec: v1alpha1.CustomRouteSpec{InboundNode: "in-a", OutboundNode: "no-relay"},
		}

		input := configengine.Input{
			Node:  nodeA,
			Users: []*v1alpha1.User{user},
			UserCreds: map[string]configengine.UserCredential{
				"user-alice": {UUID: "aaaa-1111"},
			},
			Routes:              []*v1alpha1.CustomRoute{route},
			OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{"no-relay": nodeNoRelay},
		}

		out, err := configengine.Compute(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg := parseConfig(t, out)
		if rules := routeRulesOf(t, cfg); len(rules) != 0 {
			t.Errorf("expected no route rules, got %v", rules)
		}
		for _, tag := range outboundTags(t, cfg) {
			if tag == "outbound-no-relay" {
				t.Errorf("unexpected outbound entry outbound-no-relay")
			}
		}
	})

	t.Run("self dual-role without relayPort keeps direct outbound", func(t *testing.T) {
		self := makeNode("self-a", "1.2.3.4", "us-west",
			[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound, v1alpha1.ProxyRoleOutbound},
			[]v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 443}}, 0)
		user := makeUser("user-alice")

		input := configengine.Input{
			Node:  self,
			Users: []*v1alpha1.User{user},
			UserCreds: map[string]configengine.UserCredential{
				"user-alice": {UUID: "aaaa-1111"},
			},
			OutboundNodesByName: map[string]*v1alpha1.SingBoxNode{},
		}

		out, err := configengine.Compute(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg := parseConfig(t, out)

		found := false
		for _, tag := range outboundTags(t, cfg) {
			if tag == "outbound-self-a" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected direct outbound-self-a for self dual-role node")
		}
	})
}
