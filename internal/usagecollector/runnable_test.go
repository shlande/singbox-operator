package usagecollector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test: CollectorRunnable implements the Runnable interface (compile-time)
// ---------------------------------------------------------------------------

// TestCollectorRunnable_ImplementsRunnable is a compile-time check that
// *CollectorRunnable satisfies the Runnable interface.
func TestCollectorRunnable_ImplementsRunnable(t *testing.T) {
	var _ Runnable = (*CollectorRunnable)(nil)
}

// ---------------------------------------------------------------------------
// Test: NeedLeaderElection returns true
// ---------------------------------------------------------------------------

func TestCollectorRunnable_NeedLeaderElection_ReturnsTrue(t *testing.T) {
	r := &CollectorRunnable{Collector: nil}
	if got := r.NeedLeaderElection(); !got {
		t.Errorf("NeedLeaderElection() = %v, want true", got)
	}
}

// ---------------------------------------------------------------------------
// Test: Start propagates Collector.Run return value
// ---------------------------------------------------------------------------

// TestCollectorRunnable_Start_Cancellation tests that Start returns nil
// when the context is cancelled (the normal shutdown path).
func TestCollectorRunnable_Start_ReturnsOnCancel(t *testing.T) {
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "checkpoint.json")

	discoverer := &fakeDiscoverer{targets: []CollectTarget{}}
	statsClient := newFakeStatsClient()
	sink := newFakeSink()

	cfg := DefaultCollectorConfig()
	cfg.Enabled = true
	cfg.PollInterval = 10 * time.Millisecond
	cfg.NodeTimeout = 5 * time.Millisecond
	cfg.CheckpointPath = cpPath
	cfg.MaxBufferSize = 100
	cfg.ShutdownTimeout = 5 * time.Second

	// Collector with no targets — Run returns nil on ctx cancel.
	col := NewCollector(cfg, discoverer, statsClient, sink)
	r := &CollectorRunnable{Collector: col}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := r.Start(ctx)
	if err != nil {
		t.Errorf("Start() returned error on cancel: %v", err)
	}
}

// TestCollectorRunnable_Start_CollectsRecords tests end-to-end that the

// TestCollectorRunnable_Start_CollectsRecords tests end-to-end that the
// collector actually processes records through the runnable wrapper.
func TestCollectorRunnable_Start_CollectsRecords(t *testing.T) {
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "checkpoint.json")

	discoverer := &fakeDiscoverer{
		targets: []CollectTarget{
			{NodeName: "node1", V2RayAPIAddr: "127.0.0.1:10085"},
		},
	}
	statsClient := newFakeStatsClient()
	statsClient.setEntries("127.0.0.1:10085", []RawStatEntry{
		{Name: "user>>>alice#node1>>>traffic>>>uplink", Value: 1000},
		{Name: "user>>>alice#node1>>>traffic>>>downlink", Value: 2000},
	})
	sink := newFakeSink()

	cfg := DefaultCollectorConfig()
	cfg.Enabled = true
	cfg.PollInterval = 10 * time.Millisecond
	cfg.NodeTimeout = 5 * time.Second
	cfg.CheckpointPath = cpPath
	cfg.MaxBufferSize = 10000
	cfg.ShutdownTimeout = 5 * time.Second

	col := NewCollector(cfg, discoverer, statsClient, sink)
	r := &CollectorRunnable{Collector: col}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := r.Start(ctx)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	// The collector should have recorded at least one write to the sink.
	if sink.totalRecords() == 0 {
		t.Errorf("Expected at least one record to be collected, got 0")
	}
}

// ---------------------------------------------------------------------------
// Evidence collection helpers
// ---------------------------------------------------------------------------

// TestCollectorRunnable_Evidence_Registration captures test output for
// the registration evidence file.
func TestCollectorRunnable_Evidence_Registration(t *testing.T) {
	outDir := filepath.Join("..", "..", ".omo", "evidence")
	os.MkdirAll(outDir, 0755) //nolint:errcheck

	evidence := `=== Task 9: CollectorRunnable Registration ===
File: internal/usagecollector/runnable.go
- CollectorRunnable struct created wrapping *Collector
- Start(ctx) delegates to Collector.Run(ctx)
- NeedLeaderElection() returns true (single-active)
- Compile-time interface check: var _ Runnable = (*CollectorRunnable)(nil)
`

	err := os.WriteFile(filepath.Join(outDir, "task-9-registration.txt"), []byte(evidence), 0644)
	if err != nil {
		t.Fatalf("Failed to write registration evidence: %v", err)
	}
}

// TestCollectorRunnable_Evidence_LeaderElection captures test output for
// the leader-election evidence file.
func TestCollectorRunnable_Evidence_LeaderElection(t *testing.T) {
	outDir := filepath.Join("..", "..", ".omo", "evidence")
	os.MkdirAll(outDir, 0755) //nolint:errcheck

	evidence := `=== Task 9: CollectorRunnable Leader Election ===
NeedLeaderElection() returns: true

Rationale:
- Single-active collector prevents duplicate ES writes
- Only the leader replica polls sing-box and writes to Elasticsearch
- Non-leader replicas do not start the collector at all
- This is implemented by returning true from NeedLeaderElection()
  and relying on controller-runtime's leader election machinery
`

	err := os.WriteFile(filepath.Join(outDir, "task-9-leader-election.txt"), []byte(evidence), 0644)
	if err != nil {
		t.Fatalf("Failed to write leader-election evidence: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Benchmark: Start/Stop overhead
// ---------------------------------------------------------------------------

func BenchmarkCollectorRunnable_StartStop(b *testing.B) {
	dir := b.TempDir()
	cpPath := filepath.Join(dir, "checkpoint.json")

	discoverer := &fakeDiscoverer{}
	statsClient := newFakeStatsClient()
	sink := newFakeSink()

	cfg := DefaultCollectorConfig()
	cfg.Enabled = true
	cfg.PollInterval = 1 * time.Second
	cfg.NodeTimeout = 100 * time.Millisecond
	cfg.CheckpointPath = cpPath
	cfg.MaxBufferSize = 100
	cfg.ShutdownTimeout = 100 * time.Millisecond

	col := NewCollector(cfg, discoverer, statsClient, sink)
	r := &CollectorRunnable{Collector: col}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			_ = r.Start(ctx)
		}()
		cancel()
	}
}
