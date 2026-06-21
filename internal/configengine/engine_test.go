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
		if u["name"] != "user-alice#node-b" {
			t.Errorf("expected virtual user name=user-alice#node-b, got %v", u["name"])
		}
		if _, hasUsername := u["username"]; !hasUsername {
			t.Error("expected username field in naive virtual user")
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
