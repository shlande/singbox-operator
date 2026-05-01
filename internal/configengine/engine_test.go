package configengine_test

import (
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
)

// helper: build a minimal ProxyNode
func makeNode(name, address, region string, roles []v1alpha1.ProxyRole, protocols []v1alpha1.ProtocolConfig, relayPort int32) *v1alpha1.ProxyNode {
	return &v1alpha1.ProxyNode{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ProxyNodeSpec{
			Address:            address,
			Region:             region,
			Roles:              roles,
			SupportedProtocols: protocols,
			RelayPort:          relayPort,
			RelayProtocol:      "socks5",
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

// ---------------------------------------------------------------------------
// Test 1: Inbound node with 2 vless users + 1 outbound node
// ---------------------------------------------------------------------------
func TestConfigEngine_InboundNode(t *testing.T) {
	node := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	outNode := makeNode("node-b", "5.6.7.8", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 10808,
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

	// 2 vless inbounds
	if !containsTag(ibs, "inbound-vless-user-alice") {
		t.Errorf("missing inbound tag for user-alice, got %v", ibs)
	}
	if !containsTag(ibs, "inbound-vless-user-bob") {
		t.Errorf("missing inbound tag for user-bob, got %v", ibs)
	}

	// 1 socks5 outbound to node-b + 1 direct
	if !containsTag(obs, "outbound-node-b") {
		t.Errorf("missing socks5 outbound to node-b, got %v", obs)
	}
	if !containsTag(obs, "direct") {
		t.Errorf("missing direct outbound, got %v", obs)
	}

	// route.final points to first outbound (node-b)
	final := routeFinal(cfg)
	if final != "outbound-node-b" {
		t.Errorf("expected route.final=outbound-node-b, got %q", final)
	}

	// verify outbound socks server/port
	for _, ob := range outboundsOf(t, cfg) {
		m := ob.(map[string]interface{})
		if m["tag"] == "outbound-node-b" {
			if m["server"] != "5.6.7.8" {
				t.Errorf("expected server=5.6.7.8, got %v", m["server"])
			}
			if m["server_port"].(float64) != 10808 {
				t.Errorf("expected server_port=10808, got %v", m["server_port"])
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
		nil, 10808,
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

	// must have both user inbound AND relay inbound
	if !containsTag(ibs, "inbound-trojan-user-carol") {
		t.Errorf("missing trojan inbound, got %v", ibs)
	}
	if !containsTag(ibs, "relay-socks5") {
		t.Errorf("missing relay-socks5 inbound, got %v", ibs)
	}

	// must have direct outbound
	if !containsTag(obs, "direct") {
		t.Errorf("missing direct outbound, got %v", obs)
	}
}

// ---------------------------------------------------------------------------
// Test 4: Manual route (inbound node + ProxyRoute pointing to outbound node B)
// ---------------------------------------------------------------------------
func TestConfigEngine_ManualRoute(t *testing.T) {
	nodeA := makeNode("node-a", "1.2.3.4", "us-west",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
		[]v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}},
		10808,
	)
	nodeB := makeNode("node-b", "5.6.7.8", "us-east",
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
		nil, 10808,
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
	obs := outboundTags(t, cfg)

	if !containsTag(obs, "route-route-a-to-b") {
		t.Errorf("missing route outbound tag, got %v", obs)
	}

	// verify it points to node-b's address
	for _, ob := range outboundsOf(t, cfg) {
		m := ob.(map[string]interface{})
		if m["tag"] == "route-route-a-to-b" {
			if m["server"] != "5.6.7.8" {
				t.Errorf("expected server=5.6.7.8, got %v", m["server"])
			}
		}
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
		[]v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound}, nil, 10808,
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
		nil, 10808,
	)
	ports := configengine.ExtractNodePorts(node)
	if len(ports) != 1 || ports[0] != 10808 {
		t.Errorf("expected [10808], got %v", ports)
	}
}

// ---------------------------------------------------------------------------
// Test 9: socks5 and http user inbounds
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

	if !containsTag(ibs, "inbound-socks5-user-socks") {
		t.Errorf("missing socks5 inbound, got %v", ibs)
	}
	if !containsTag(ibs, "inbound-http-user-http") {
		t.Errorf("missing http inbound, got %v", ibs)
	}

	for _, ib := range inboundsOf(t, cfg) {
		m := ib.(map[string]interface{})
		if m["tag"] == "inbound-socks5-user-socks" {
			if m["type"] != "socks" {
				t.Errorf("expected type=socks, got %v", m["type"])
			}
		}
		if m["tag"] == "inbound-http-user-http" {
			if m["type"] != "http" {
				t.Errorf("expected type=http, got %v", m["type"])
			}
		}
	}
}
