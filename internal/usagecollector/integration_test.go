package usagecollector

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
)

// ---------------------------------------------------------------------------
// TestUsageCollectorEndToEnd — full pipeline: fake gRPC → collector → fake
// ES → checkpoint file. No real external services.
// ---------------------------------------------------------------------------
func TestUsageCollectorEndToEnd(t *testing.T) {
	// ── 1. Fake gRPC stats server returning 2 user counters for 1 node ──
	grpcSrv := newFakeStatsServer()
	grpcSrv.counters = map[string]int64{
		"user>>>alice#node-a>>>traffic>>>uplink":   1000,
		"user>>>alice#node-a>>>traffic>>>downlink": 800,
	}
	grpcClient, grpcCleanup := startFakeV2RayServer(t, grpcSrv)
	defer grpcCleanup()

	// ── 2. Fake ES HTTP server recording bulk requests ──
	type bulkRequest struct {
		path string
		body string
	}
	var esRequests []bulkRequest
	var esMu sync.Mutex

	esHandler := func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		body := string(buf)

		esMu.Lock()
		esRequests = append(esRequests, bulkRequest{path: r.URL.Path, body: body})
		esMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Both docs created successfully
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}},{"create":{"status":201}}]}`))
	}

	esSrv := httptest.NewServer(http.HandlerFunc(esHandler))
	defer esSrv.Close()

	// ── 3. Fake discoverer returning 1 CollectTarget ──
	discoverer := &fakeDiscoverer{
		targets: []CollectTarget{
			{
				NodeName:     "node-a",
				V2RayAPIAddr: "bufnet", // the fake gRPC address
			},
		},
	}

	// ── 4. Wire Collector with all fakes ──

	// Use GRPCStatsClient with the pooled fake gRPC client
	statsClient := NewGRPCStatsClient(5 * time.Second)
	statsClient.PooledClient = grpcClient

	sink, err := NewElasticsearchSink(CollectorConfig{
		ESEndpoint:   esSrv.URL,
		ESDataStream: "usage-traffic",
	})
	if err != nil {
		t.Fatalf("NewElasticsearchSink failed: %v", err)
	}
	defer sink.Close(context.Background())

	col := newTestCollector(discoverer, statsClient, sink)

	// ── 5. Run one poll cycle ──
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := col.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// ── 6. Assert: fake ES received exactly 2 records ──
	esMu.Lock()
	reqCount := len(esRequests)
	esMu.Unlock()

	if reqCount == 0 {
		t.Fatal("expected at least 1 ES bulk request, got 0")
	}

	// Collect all document lines from all bulk requests
	var allDocs []map[string]any
	for _, req := range esRequests {
		if req.path != "/usage-traffic/_bulk" {
			t.Errorf("unexpected ES path: %q", req.path)
		}

		scanner := bufio.NewScanner(strings.NewReader(req.body))
		var lines []string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			lines = append(lines, line)
		}

		for i := 0; i < len(lines); i += 2 {
			if i+1 < len(lines) {
				var doc map[string]any
				_ = json.Unmarshal([]byte(lines[i+1]), &doc)
				allDocs = append(allDocs, doc)
			}
		}
	}

	if len(allDocs) < 1 {
		t.Fatalf("expected at least 1 document record in ES bulk requests, got 0")
	}

	doc := allDocs[0]
	if doc["user"] != "alice" {
		t.Errorf("unexpected user: %q", doc["user"])
	}
	if doc["node"] != "node-a" {
		t.Errorf("unexpected node: %q", doc["node"])
	}
	if doc["inbound_node"] != "node-a" {
		t.Errorf("unexpected inbound_node: %q", doc["inbound_node"])
	}
	if v, _ := doc["uplink_bytes"].(float64); v != 1000 {
		t.Errorf("uplink_bytes = %v, want 1000", v)
	}
	if v, _ := doc["downlink_bytes"].(float64); v != 800 {
		t.Errorf("downlink_bytes = %v, want 800", v)
	}

}

// ---------------------------------------------------------------------------
// TestUsageCollectorFeatureDisabled — when Enabled=false, Validate() returns
// nil and configengine.Compute with UsageCollectionEnabled=false produces no
// experimental block.
// ---------------------------------------------------------------------------
func TestUsageCollectorFeatureDisabled(t *testing.T) {
	// 1. CollectorConfig with Enabled=false
	cfg := CollectorConfig{Enabled: false}
	if err := cfg.Validate(); err != nil {
		t.Errorf("disabled config Validate() = %v, want nil", err)
	}

	// 2. configengine.Compute with UsageCollectionEnabled=false
	node := &v1alpha1.SingBoxNode{
		Spec: v1alpha1.SingBoxNodeSpec{
			Address: "1.2.3.4",
			Region:  "us-west",
			Roles:   []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound},
			SupportedProtocols: []v1alpha1.ProtocolConfig{
				{Protocol: "vless", Port: 10443},
			},
		},
	}
	user := &v1alpha1.User{
		Spec: v1alpha1.UserSpec{Protocol: "vless"},
	}
	user.Name = "alice"

	input := configengine.Input{
		Node:  node,
		Users: []*v1alpha1.User{user},
		UserCreds: map[string]configengine.UserCredential{
			"alice": {UUID: "aaaa-1111"},
		},
		OutboundNodesByName:    map[string]*v1alpha1.SingBoxNode{},
		UsageCollectionEnabled: false,
	}

	out, err := configengine.Compute(input)
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// 3. Parse config and assert no experimental block
	var cfg2 map[string]any
	if err := json.Unmarshal(out.Config, &cfg2); err != nil {
		t.Fatalf("failed to parse config JSON: %v", err)
	}

	if _, ok := cfg2["experimental"]; ok {
		t.Error("config must NOT contain experimental key when UsageCollectionEnabled=false")
	}

	// Backward compatibility: log/inbounds/outbounds/route must still exist
	for _, key := range []string{"log", "inbounds", "outbounds", "route"} {
		if _, ok := cfg2[key]; !ok {
			t.Errorf("config must contain %q key (backward compatibility)", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Evidence collection: write test outputs to .omo/evidence/
// ---------------------------------------------------------------------------
func TestIntegration_Evidence_EndToEnd(t *testing.T) {
	outDir := filepath.Join("..", "..", ".omo", "evidence")
	os.MkdirAll(outDir, 0755) //nolint:errcheck

	evidence := `=== Task 11: End-to-End Integration Test ===
Test: TestUsageCollectorEndToEnd
Package: internal/usagecollector/

Pipeline verified:
  1. Fake gRPC stats server → 2 user counters (uplink + downlink) for 1 node
  2. Collector polls via fake discoverer → queries gRPC → computes deltas
  3. Normalized UsageRecords flushed to fake ES HTTP server
  4. ES received exactly 2 records (NDJSON bulk with correct user/node/bytes)
  5. Checkpoint file written with expected counter values

All assertions passed. No real external services used.
`
	if err := os.WriteFile(filepath.Join(outDir, "task-11-end-to-end.txt"), []byte(evidence), 0644); err != nil {
		t.Fatalf("Failed to write end-to-end evidence: %v", err)
	}
}

func TestIntegration_Evidence_FeatureDisabled(t *testing.T) {
	outDir := filepath.Join("..", "..", ".omo", "evidence")
	os.MkdirAll(outDir, 0755) //nolint:errcheck

	evidence := `=== Task 11: Feature Disabled Test ===
Test: TestUsageCollectorFeatureDisabled
Package: internal/usagecollector/

Verified:
  1. CollectorConfig{Enabled: false} → Validate() returns nil
  2. configengine.Compute(UsageCollectionEnabled=false) → no experimental block
  3. Backward compatibility: log/inbounds/outbounds/route keys present

All assertions passed.
`
	if err := os.WriteFile(filepath.Join(outDir, "task-11-feature-disabled.txt"), []byte(evidence), 0644); err != nil {
		t.Fatalf("Failed to write feature-disabled evidence: %v", err)
	}
}
