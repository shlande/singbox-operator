package usagecollector

import (
	"context"
	"testing"
	"time"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// fakeDiscovererScheme returns a scheme with SingBoxNode, User, and CustomRoute
// registered so the fake client works with these types.
func fakeDiscovererScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = proxyv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

// ---------------------------------------------------------------------------
// Test 1: Empty cluster — Discover returns empty slice, nil error (no panic)
// ---------------------------------------------------------------------------

func TestDiscoverEmptyCluster(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(fakeDiscovererScheme()).
		Build()

	d := NewK8sDiscoverer(fakeClient, fakeClient, "default")
	targets, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover on empty cluster returned error: %v", err)
	}
	if targets == nil {
		t.Fatal("Discover returned nil slice, want empty non-nil slice")
	}
	if len(targets) != 0 {
		t.Fatalf("Discover returned %d targets, want 0", len(targets))
	}
}

// ---------------------------------------------------------------------------
// Test 2: One inbound node, two users via routes → Discover returns one
// CollectTarget with correct VirtualUsers
// ---------------------------------------------------------------------------

func TestDiscoverOneInboundNodeWithUsers(t *testing.T) {
	// Inbound node
	inbound := &proxyv1alpha1.SingBoxNode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-a",
			Namespace: "default",
		},
		Spec: proxyv1alpha1.SingBoxNodeSpec{
			Address: "10.0.0.1",
			Region:  "us-west",
			Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
			SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
				{Protocol: "hysteria2", Port: 8443},
			},
		},
	}

	// Outbound node (same region, so it's a valid outbound target)
	outbound := &proxyv1alpha1.SingBoxNode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-b",
			Namespace: "default",
		},
		Spec: proxyv1alpha1.SingBoxNodeSpec{
			Address: "10.0.0.2",
			Region:  "us-west",
			Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
		},
	}

	// Two users
	alice := &proxyv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "alice",
			Namespace: "default",
			// User controller resolves ActiveNodes; simplified for test
		},
		Spec: proxyv1alpha1.UserSpec{
			Protocol: "hysteria2",
			AuthSecret: corev1.SecretReference{
				Name: "alice-secret",
			},
		},
	}

	bob := &proxyv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bob",
			Namespace: "default",
		},
		Spec: proxyv1alpha1.UserSpec{
			Protocol: "hysteria2",
			AuthSecret: corev1.SecretReference{
				Name: "bob-secret",
			},
		},
	}

	// CustomRoute routes inbound to outbound
	route := &proxyv1alpha1.CustomRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-a-to-b",
			Namespace: "default",
		},
		Spec: proxyv1alpha1.CustomRouteSpec{
			InboundNode:  "node-a",
			OutboundNode: "node-b",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(fakeDiscovererScheme()).
		WithObjects(inbound, outbound, alice, bob, route).
		WithStatusSubresource(inbound, outbound, alice, bob).
		Build()

	// Set status on inbound (not critical for discovery, but realistic)
	_ = fakeClient.Status().Update(context.Background(), inbound)

	d := NewK8sDiscoverer(fakeClient, fakeClient, "default")
	targets, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 CollectTarget, got %d", len(targets))
	}

	// Verify CollectTarget fields
	ct := targets[0]
	if ct.NodeName != "node-a" {
		t.Fatalf("NodeName = %q, want node-a", ct.NodeName)
	}
	if ct.V2RayAPIAddr != "10.0.0.1:10085" {
		t.Fatalf("V2RayAPIAddr = %q, want 10.0.0.1:10085", ct.V2RayAPIAddr)
	}

	// Virtual users: alice#node-b, bob#node-b
	expectedVirtualUsers := []string{"alice#node-b", "bob#node-b"}
	if len(ct.VirtualUsers) != len(expectedVirtualUsers) {
		t.Fatalf("VirtualUsers length = %d, want %d; got %v",
			len(ct.VirtualUsers), len(expectedVirtualUsers), ct.VirtualUsers)
	}
	for _, expected := range expectedVirtualUsers {
		found := false
		for _, got := range ct.VirtualUsers {
			if got == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("VirtualUsers missing expected entry %q; got %v", expected, ct.VirtualUsers)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 3: Node deleted between discovery cycles → next Discover cycle excludes it
// ---------------------------------------------------------------------------

func TestDiscoverExcludesDeletedNode(t *testing.T) {
	makeNode := func(name, address string, roles []proxyv1alpha1.ProxyRole) *proxyv1alpha1.SingBoxNode {
		return &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				Address: address,
				Region:  "us-west",
				Roles:   roles,
			},
		}
	}

	inbound1 := makeNode("node-a", "10.0.0.1", []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound})
	inbound2 := makeNode("node-b", "10.0.0.2", []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound})

	fakeClient := fake.NewClientBuilder().
		WithScheme(fakeDiscovererScheme()).
		WithObjects(inbound1, inbound2).
		Build()

	d := NewK8sDiscoverer(fakeClient, fakeClient, "default")

	// First discovery: 2 nodes
	targets1, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("First Discover failed: %v", err)
	}
	if len(targets1) != 2 {
		t.Fatalf("First Discover: expected 2 targets, got %d", len(targets1))
	}

	// Delete node-a
	if err := fakeClient.Delete(context.Background(), inbound1); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Second discovery: 1 node (node-b only)
	targets2, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Second Discover failed: %v", err)
	}
	if len(targets2) != 1 {
		t.Fatalf("After deletion: expected 1 target, got %d", len(targets2))
	}
	if targets2[0].NodeName != "node-b" {
		t.Fatalf("After deletion: expected NodeName node-b, got %q", targets2[0].NodeName)
	}
}

// ---------------------------------------------------------------------------
// Test 4: NormalizeCounterToRecord with valid counter name → correct UsageRecord
// ---------------------------------------------------------------------------

func TestNormalizeCounterToRecordValid(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	// Valid counter: user>>>alice#node-b>>>traffic>>>uplink
	record, ok := NormalizeCounterToRecord("user>>>alice#node-b>>>traffic>>>uplink", 1000, now)
	if !ok {
		t.Fatal("NormalizeCounterToRecord returned ok=false for valid uplink counter")
	}
	if record.User != "alice" {
		t.Fatalf("User = %q, want alice", record.User)
	}
	if record.Node != "node-b" {
		t.Fatalf("Node = %q, want node-b", record.Node)
	}
	if record.UplinkBytes != 1000 {
		t.Fatalf("UplinkBytes = %d, want 1000", record.UplinkBytes)
	}
	if record.DownlinkBytes != 0 {
		t.Fatalf("DownlinkBytes = %d, want 0", record.DownlinkBytes)
	}
	if !record.CollectedAt.Equal(now) {
		t.Fatalf("CollectedAt = %v, want %v", record.CollectedAt, now)
	}

	// Valid counter: user>>>bob#node-a>>>traffic>>>downlink
	record2, ok2 := NormalizeCounterToRecord("user>>>bob#node-a>>>traffic>>>downlink", 500, now)
	if !ok2 {
		t.Fatal("NormalizeCounterToRecord returned ok=false for valid downlink counter")
	}
	if record2.User != "bob" {
		t.Fatalf("User = %q, want bob", record2.User)
	}
	if record2.Node != "node-a" {
		t.Fatalf("Node = %q, want node-a", record2.Node)
	}
	if record2.UplinkBytes != 0 {
		t.Fatalf("UplinkBytes = %d, want 0", record2.UplinkBytes)
	}
	if record2.DownlinkBytes != 500 {
		t.Fatalf("DownlinkBytes = %d, want 500", record2.DownlinkBytes)
	}
}

// ---------------------------------------------------------------------------
// Test 5: NormalizeCounterToRecord with non-user counter → returns ok=false
// ---------------------------------------------------------------------------

func TestNormalizeCounterToRecordInvalid(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	invalidCounters := []struct {
		name    string
		counter string
	}{
		{"empty string", ""},
		{"wrong prefix", "something>>>else>>>traffic>>>uplink"},
		{"no hash separator", "user>>>alice>>>traffic>>>uplink"},
		{"missing direction", "user>>>alice#node-b>>>traffic"},
		{"bad direction", "user>>>alice#node-b>>>traffic>>>sideways"},
		{"extra segments", "user>>>alice#node-b>>>traffic>>>uplink>>>extra"},
	}

	for _, tc := range invalidCounters {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := NormalizeCounterToRecord(tc.counter, 100, now)
			if ok {
				t.Fatalf("NormalizeCounterToRecord(%q) returned ok=true, want false", tc.counter)
			}
		})
	}
}
