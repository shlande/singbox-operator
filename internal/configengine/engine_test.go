package configengine_test

import (
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
)

// helper: build a minimal ProxyNode
func makeNode(name, address, region string, roles []v1alpha1.ProxyRole, protocols []v1alpha1.ProtocolConfig, relayNodePort int32) *v1alpha1.ProxyNode {
	return &v1alpha1.ProxyNode{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ProxyNodeSpec{
			Address:            address,
			Region:             region,
			Roles:              roles,
			SupportedProtocols: protocols,
			RelayNodePort:      relayNodePort,
		},
	}
}

func makeUser(name, protocol string) *v1alpha1.ProxyUser {
	return &v1alpha1.ProxyUser{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.ProxyUserSpec{Protocol: protocol},
	}
}

func makeRoute(name, inboundNode, outboundNode string) *v1alpha1.ProxyRoute {
	return &v1alpha1.ProxyRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ProxyRouteSpec{
			InboundNode:  inboundNode,
			OutboundNode: outboundNode,
		},
	}
}

// parseConfig unmarshals the Output.Config into a generic map for inspection
func parseConfig(t *testing.T, out configengine.Output) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.Unmarshal(out.Config, &result); err != nil {
		t.Fatalf("failed to parse config JSON: %v", err)
	}
	return result
}

func inboundsOf(t *testing.T, cfg map[string]interface{}) []interface{} {
	t.Helper()
	v, ok := cfg["inbounds"]
	if !ok {
		return nil
	}
	arr, _ := v.([]interface{})
	return arr
}

func outboundsOf(t *testing.T, cfg map[string]interface{}) []interface{} {
	t.Helper()
	v, ok := cfg["outbounds"]
	if !ok {
		return nil
	}
	arr, _ := v.([]interface{})
	return arr
}

func routeFinal(cfg map[string]interface{}) string {
	r, ok := cfg["route"].(map[string]interface{})
	if !ok {
		return ""
	}
	f, _ := r["final"].(string)
	return f
}

func inboundTags(t *testing.T, cfg map[string]interface{}) []string {
	t.Helper()
	var tags []string
	for _, ib := range inboundsOf(t, cfg) {
		m, _ := ib.(map[string]interface{})
		tags = append(tags, m["tag"].(string))
	}
	return tags
}

func outboundTags(t *testing.T, cfg map[string]interface{}) []string {
	t.Helper()
	var tags []string
	for _, ob := range outboundsOf(t, cfg) {
		m, _ := ob.(map[string]interface{})
		tags = append(tags, m["tag"].(string))
	}
	return tags
}

func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

func routeRulesOf(t *testing.T, cfg map[string]interface{}) []interface{} {
	t.Helper()
	r, ok := cfg["route"].(map[string]interface{})
	if !ok {
		return nil
	}
	rules, _ := r["rules"].([]interface{})
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
	outNode := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 31962,
	)
	user1 := makeUser("user-alice", "vless")
	user2 := makeUser("user-bob", "vless")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.ProxyUser{user1, user2},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
			"user-bob":   {UUID: "bbbb-2222"},
		},
		OutboundNodes: []*v1alpha1.ProxyNode{outNode},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "relay-user", Password: "relay-pass"},
		},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{"node-b": outNode},
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
		m := ib.(map[string]interface{})
		if m["tag"] == "inbound-vless" {
			users := m["users"].([]interface{})
			if len(users) != 2 {
				t.Errorf("expected 2 virtual users in inbound, got %d", len(users))
			}
			names := make(map[string]bool)
			for _, u := range users {
				um := u.(map[string]interface{})
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
	rule := rules[0].(map[string]interface{})
	if rule["outbound"] != "outbound-node-b" {
		t.Errorf("expected rule outbound=outbound-node-b, got %v", rule["outbound"])
	}
	authUsers, _ := rule["auth_user"].([]interface{})
	if len(authUsers) != 2 {
		t.Errorf("expected 2 auth_users in rule, got %d", len(authUsers))
	}

	for _, ob := range outboundsOf(t, cfg) {
		m := ob.(map[string]interface{})
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
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{},
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
		m := ib.(map[string]interface{})
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
	user := makeUser("user-carol", "trojan")
	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.ProxyUser{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-carol": {Password: "s3cr3t"},
		},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-c": {Username: "relay-u", Password: "relay-p"},
		},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{},
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
// Test 4: Manual route — single inbound per protocol with all users,
// auth_user routing rule binds users to outbound.
// ---------------------------------------------------------------------------
func TestConfigEngine_ManualRoute(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeB := makeNode("node-b", "5.6.7.8", "us-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 31962,
	)
	user := makeUser("user-dave", "vless")
	route := makeRoute("route-a-to-b", "node-a", "node-b")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.ProxyUser{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-dave": {UUID: "dddd-4444"},
		},
		Routes: []*v1alpha1.ProxyRoute{route},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "r-user", Password: "r-pass"},
		},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{"node-b": nodeB},
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
		m := ib.(map[string]interface{})
		if m["tag"] == expectedInboundTag {
			users := m["users"].([]interface{})
			if len(users) != 1 {
				t.Errorf("expected 1 user in inbound, got %d", len(users))
			}
			u := users[0].(map[string]interface{})
			if u["name"] != "user-dave#node-b" {
				t.Errorf("expected user name=user-dave#node-b, got %v", u["name"])
			}
		}
	}

	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("missing outbound-node-b, got %v", obs)
	}

	for _, ob := range outboundsOf(t, cfg) {
		m := ob.(map[string]interface{})
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
		m := rule.(map[string]interface{})
		if m["outbound"] != "outbound-node-b" {
			continue
		}
		authUsers, _ := m["auth_user"].([]interface{})
		for _, u := range authUsers {
			if u.(string) == "user-dave#node-b" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no routing rule with auth_user=user-dave#node-b → outbound-node-b")
	}
}

// ---------------------------------------------------------------------------
// Test 5: Inbound node with no matching users — inbounds must be empty slice
// ---------------------------------------------------------------------------
func TestConfigEngine_NoUsersOnEntry(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	input := configengine.Input{
		Node:                node,
		Users:               []*v1alpha1.ProxyUser{},
		UserCreds:           map[string]configengine.UserCredential{},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{},
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
	outNode1 := makeNode("node-b1", "5.5.5.5", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	outNode2 := makeNode("node-b2", "6.6.6.6", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)

	input := configengine.Input{
		Node:          node,
		Users:         []*v1alpha1.ProxyUser{},
		UserCreds:     map[string]configengine.UserCredential{},
		OutboundNodes: []*v1alpha1.ProxyNode{outNode1, outNode2},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b1": {Username: "u1", Password: "p1"},
			"node-b2": {Username: "u2", Password: "p2"},
		},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{
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
// Test 7: Hash consistency — same input → same hash; different input → different hash
// ---------------------------------------------------------------------------
func TestConfigEngine_HashConsistency(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	user := makeUser("user-alice", "vless")
	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.ProxyUser{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{},
	}

	out1, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}
	out2, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}

	// same input → same hash
	if out1.Hash != out2.Hash {
		t.Errorf("hash not stable: %q vs %q", out1.Hash, out2.Hash)
	}
	if len(out1.Hash) != 16 {
		t.Errorf("expected 16-char hash, got %d: %q", len(out1.Hash), out1.Hash)
	}

	// different input → different hash
	user2 := makeUser("user-bob", "vless")
	input2 := input
	input2.Users = []*v1alpha1.ProxyUser{user2}
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
// Test 8: ExtractNodePorts — inbound node with multiple protocols
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
			{Protocol: "http", Port: 10080},
		},
		10900,
	)
	userS := makeUser("user-socks", "socks5")
	userH := makeUser("user-http", "http")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.ProxyUser{userS, userH},
		UserCreds: map[string]configengine.UserCredential{
			"user-socks": {Username: "su", Password: "sp"},
			"user-http":  {UUID: "hu", Password: "hp"},
		},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{},
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
	if !containsTag(ibs, "inbound-http") {
		t.Errorf("missing http inbound, got %v", ibs)
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]interface{})
		if m["tag"] == "inbound-socks5" && m["type"] != "socks" {
			t.Errorf("expected type=socks, got %v", m["type"])
		}
		if m["tag"] == "inbound-http" && m["type"] != "http" {
			t.Errorf("expected type=http, got %v", m["type"])
		}
	}
}

// ---------------------------------------------------------------------------
// Test 10: Dedup — region-auto outbound node that is also in an explicit Route
// must appear exactly once in outbounds.
// ---------------------------------------------------------------------------
func TestConfigEngine_DedupRegionAutoAndExplicitRoute(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 31962,
	)
	user := makeUser("user-eve", "vless")
	route := makeRoute("route-a-to-b", "node-a", "node-b")

	input := configengine.Input{
		Node:                nodeA,
		Users:               []*v1alpha1.ProxyUser{user},
		UserCreds:           map[string]configengine.UserCredential{"user-eve": {UUID: "eeee-5555"}},
		OutboundNodes:       []*v1alpha1.ProxyNode{nodeB},
		Routes:              []*v1alpha1.ProxyRoute{route},
		NodeCreds:           map[string]configengine.NodeCredential{"node-b": {Username: "u", Password: "p"}},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{"node-b": nodeB},
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
// Test 11: Multi-route — 1 user, 2 routes → 2 inbounds on distinct ports,
// each containing all users, with auth_user routing rules per outbound.
// ---------------------------------------------------------------------------
func TestConfigEngine_MultiRouteInbounds(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeB := makeNode("node-b", "5.5.5.5", "us-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeC := makeNode("node-c", "6.6.6.6", "us-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	user := makeUser("user-frank", "vless")
	routeToB := makeRoute("route-a-to-b", "node-a", "node-b")
	routeToC := makeRoute("route-a-to-c", "node-a", "node-c")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.ProxyUser{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-frank": {UUID: "ffff-6666"},
		},
		Routes: []*v1alpha1.ProxyRoute{routeToB, routeToC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{
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
		m := ib.(map[string]interface{})
		if m["tag"] != "inbound-vless" {
			continue
		}
		users := m["users"].([]interface{})
		if len(users) != 2 {
			t.Fatalf("expected 2 virtual users in inbound-vless, got %d", len(users))
		}
		for _, u := range users {
			um := u.(map[string]interface{})
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
		m := rule.(map[string]interface{})
		ruleOutbounds[m["outbound"].(string)] = true
		authUsers, _ := m["auth_user"].([]interface{})
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
	nodeB := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 31962,
	)
	nodeC := makeNode("node-c", "9.9.9.9", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10809,
	)
	user := makeUser("user-alice", "vless")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.ProxyUser{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {UUID: "aaaa-1111"},
		},
		OutboundNodes: []*v1alpha1.ProxyNode{nodeB, nodeC},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "ub", Password: "pb"},
			"node-c": {Username: "uc", Password: "pc"},
		},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{
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
		m := ib.(map[string]interface{})
		if m["tag"] != "inbound-vless" {
			continue
		}
		users := m["users"].([]interface{})
		if len(users) != 2 {
			t.Fatalf("expected 2 virtual users, got %d", len(users))
		}
		names := make(map[string]bool)
		for _, u := range users {
			um := u.(map[string]interface{})
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
		rm := rule.(map[string]interface{})
		ruleTargets[rm["outbound"].(string)] = true
		authUsers, _ := rm["auth_user"].([]interface{})
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
// Test 14: hysteria2 inbound — users have password, inbound has tls block
// ---------------------------------------------------------------------------
func TestConfigEngine_Hysteria2Inbound(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 30443}},
		0,
	)
	user := makeUser("user-alice", "hysteria2")

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.ProxyUser{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {Password: "s3cr3t"},
		},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{},
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
		m := ib.(map[string]interface{})
		if m["tag"] != "inbound-hysteria2" {
			continue
		}
		if m["type"] != "hysteria2" {
			t.Errorf("expected type=hysteria2, got %v", m["type"])
		}
		tls, ok := m["tls"].(map[string]interface{})
		if !ok {
			t.Error("expected tls block in hysteria2 inbound")
		} else if tls["enabled"] != true {
			t.Errorf("expected tls.enabled=true, got %v", tls["enabled"])
		}
		users, _ := m["users"].([]interface{})
		if len(users) != 1 {
			t.Fatalf("expected 1 user, got %d", len(users))
		}
		u := users[0].(map[string]interface{})
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
// Test 15: hysteria2 inbound with outbound nodes — virtual users use DerivePassword
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
	user := makeUser("user-alice", "hysteria2")

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.ProxyUser{user},
		UserCreds: map[string]configengine.UserCredential{
			"user-alice": {Password: "s3cr3t"},
		},
		OutboundNodes: []*v1alpha1.ProxyNode{nodeB},
		NodeCreds: map[string]configengine.NodeCredential{
			"node-b": {Username: "relay-user", Password: "relay-pass"},
		},
		OutboundNodesByName: map[string]*v1alpha1.ProxyNode{"node-b": nodeB},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]interface{})
		if m["tag"] != "inbound-hysteria2" {
			continue
		}
		users, _ := m["users"].([]interface{})
		if len(users) != 1 {
			t.Fatalf("expected 1 virtual user, got %d", len(users))
		}
		u := users[0].(map[string]interface{})
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
// Test 12: DeriveUUID — determinism and uniqueness
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
