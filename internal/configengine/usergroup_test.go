package configengine_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
)

// ---------------------------------------------------------------------------
// TestIsNodeAllowed — 5 cases covering deny-wins semantics
// ---------------------------------------------------------------------------
func TestIsNodeAllowed(t *testing.T) {
	tests := []struct {
		name         string
		nodeName     string
		allowedNodes map[string]bool
		deniedNodes  map[string]bool
		want         bool
	}{
		{
			name:         "deny-list blocks node",
			nodeName:     "node-b",
			allowedNodes: nil,
			deniedNodes:  map[string]bool{"node-b": true},
			want:         false,
		},
		{
			name:         "no restrictions allows all nodes",
			nodeName:     "node-b",
			allowedNodes: nil,
			deniedNodes:  nil,
			want:         true,
		},
		{
			name:         "node in allowlist is allowed",
			nodeName:     "node-b",
			allowedNodes: map[string]bool{"node-b": true},
			deniedNodes:  nil,
			want:         true,
		},
		{
			name:         "node not in allowlist is denied",
			nodeName:     "node-c",
			allowedNodes: map[string]bool{"node-b": true},
			deniedNodes:  nil,
			want:         false,
		},
		{
			name:         "deny-wins: node in both allow and deny lists",
			nodeName:     "node-b",
			allowedNodes: map[string]bool{"node-b": true},
			deniedNodes:  map[string]bool{"node-b": true},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configengine.IsNodeAllowed(tt.nodeName, tt.allowedNodes, tt.deniedNodes)
			if got != tt.want {
				t.Errorf("IsNodeAllowed(%q, %v, %v) = %v, want %v",
					tt.nodeName, tt.allowedNodes, tt.deniedNodes, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestComputeWithUserNodeRestrictions — alice denied from node-b, bob unrestricted.
// Asserts virtual users in inbound reflect the per-user deny-set.
// ---------------------------------------------------------------------------
func TestComputeWithUserNodeRestrictions(t *testing.T) {
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

	nodeA.Spec.InboundProtocol = "vless"

	alice := &v1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "alice"},
		Spec:       v1alpha1.UserSpec{},
	}
	bob := &v1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "bob"},
		Spec:       v1alpha1.UserSpec{},
	}

	input := configengine.Input{
		Node:  nodeA,
		Users: []*v1alpha1.User{alice, bob},
		UserCreds: map[string]configengine.UserCredential{
			"alice": {UUID: "aaaa-1111"},
			"bob":   {UUID: "bbbb-2222"},
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
		// alice is denied from node-b; bob has no restrictions
		UserNodeRestrictions: map[string]map[string]bool{
			"alice": {"node-b": true},
		},
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := parseConfig(t, out)

	// Collect all virtual user names across all inbounds
	ibUsers := make(map[string]bool)
	for _, ib := range inboundsOf(t, cfg) {
		m, _ := ib.(map[string]any)
		users, _ := m["users"].([]any)
		for _, u := range users {
			um, _ := u.(map[string]any)
			if name, ok := um["name"].(string); ok {
				ibUsers[name] = true
			}
		}
	}

	// alice#node-b must be absent (alice is denied from node-b)
	if ibUsers["alice#node-b"] {
		t.Errorf("alice#node-b should be absent (alice denied from node-b), got inbound users: %v", ibUsers)
	}

	// alice#node-c must be present (alice is not denied from node-c)
	if !ibUsers["alice#node-c"] {
		t.Errorf("alice#node-c should be present (alice not restricted from node-c), got inbound users: %v", ibUsers)
	}

	// bob#node-b must be present (bob has no restrictions)
	if !ibUsers["bob#node-b"] {
		t.Errorf("bob#node-b should be present (bob has no restrictions), got inbound users: %v", ibUsers)
	}

	// bob#node-c must be present (bob has no restrictions)
	if !ibUsers["bob#node-c"] {
		t.Errorf("bob#node-c should be present (bob has no restrictions), got inbound users: %v", ibUsers)
	}
}
