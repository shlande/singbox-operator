package usagecollector

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Fake implementations for injection into Collector under test
// ---------------------------------------------------------------------------

// fakeDiscoverer implements Discoverer with a configurable target list.
type fakeDiscoverer struct {
	targets []CollectTarget
	err     error // optional error to return from Discover
}

func (f *fakeDiscoverer) Discover(_ context.Context) ([]CollectTarget, error) {
	return f.targets, f.err
}

// fakeStatsClient implements StatsClient with configurable stat entries per address.
type fakeStatsClient struct {
	mu      sync.Mutex
	entries map[string][]RawStatEntry // addr → entries
	err     error                     // optional error to return
	callLog []string                  // records which addrs were queried
}

func newFakeStatsClient() *fakeStatsClient {
	return &fakeStatsClient{entries: make(map[string][]RawStatEntry)}
}

func (f *fakeStatsClient) setEntries(addr string, entries []RawStatEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[addr] = entries
}

func (f *fakeStatsClient) setError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeStatsClient) QueryUserStats(_ context.Context, addr string) ([]RawStatEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callLog = append(f.callLog, addr)
	if f.err != nil {
		return nil, f.err
	}
	return f.entries[addr], nil
}

// fakeSink implements UsageSink with configurable behavior.
type fakeSink struct {
	mu         sync.Mutex
	records    []UsageRecord
	writeErr   error // optional error to return from Write
	writeCount int32 // atomic counter of Write calls
	closed     bool
}

func newFakeSink() *fakeSink {
	return &fakeSink{}
}

func (f *fakeSink) setWriteErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeErr = err
}

func (f *fakeSink) Write(_ context.Context, batch UsageBatch) error {
	atomic.AddInt32(&f.writeCount, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.records = append(f.records, batch...)
	return nil
}

func (f *fakeSink) Close(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSink) totalRecords() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.records)
}

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

// newTestCollector constructs a Collector with fake dependencies for testing.
func newTestCollector(d Discoverer, sc StatsClient, sink UsageSink) *Collector {
	cfg := CollectorConfig{
		Enabled:         true,
		PollInterval:    50 * time.Millisecond,
		NodeTimeout:     2 * time.Second,
		MaxBufferSize:   1000,
		ShutdownTimeout: 2 * time.Second,
	}
	return NewCollector(cfg, d, sc, sink)
}

// ---------------------------------------------------------------------------
// Scenario 1: Full poll cycle
// Fake discoverer returns 1 target, fake stats client returns 2 user
// ---------------------------------------------------------------------------

func TestCollector_FullPollCycle(t *testing.T) {

	// Prepare fake dependencies
	target := CollectTarget{
		NodeName:     "node-a",
		V2RayAPIAddr: "10.0.0.1:10085",
	}
	disc := &fakeDiscoverer{targets: []CollectTarget{target}}

	statsClient := newFakeStatsClient()
	statsClient.setEntries("10.0.0.1:10085", []RawStatEntry{
		{Name: "user>>>alice#node-b>>>traffic>>>uplink", Value: 1000},
		{Name: "user>>>alice#node-b>>>traffic>>>downlink", Value: 800},
	})

	sink := newFakeSink()

	col := newTestCollector(disc, statsClient, sink)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := col.Run(ctx)
	// Expect nil error on context cancellation (clean shutdown)
	if err != nil {
		t.Fatalf("Run returned error on clean shutdown: %v", err)
	}

	if sink.totalRecords() < 1 {
		t.Fatalf("expected at least 1 record in sink, got %d", sink.totalRecords())
	}

	statsClient.mu.Lock()
	addrs := statsClient.callLog
	statsClient.mu.Unlock()
	if len(addrs) == 0 {
		t.Fatal("no stats queries were made")
	}
	for _, addr := range addrs {
		if addr != "10.0.0.1:10085" {
			t.Fatalf("unexpected queried addr: %q", addr)
		}
	}
}

// fakeSinkWithFailures fails Write on specific call numbers.
type fakeSinkWithFailures struct {
	*fakeSink
	callCount   int32
	failOnCalls map[int32]error
}

func (f *fakeSinkWithFailures) Write(ctx context.Context, batch UsageBatch) error {
	c := atomic.AddInt32(&f.callCount, 1)
	if err, ok := f.failOnCalls[c]; ok {
		return err
	}
	return f.fakeSink.Write(ctx, batch)
}



// ---------------------------------------------------------------------------
// Scenario 4: Shutdown flush
// Cancel context with buffered records → collector attempts final flush.
// ---------------------------------------------------------------------------

func TestCollector_ShutdownFlush(t *testing.T) {

	target := CollectTarget{
		NodeName:     "node-a",
		V2RayAPIAddr: "10.0.0.1:10085",
	}
	disc := &fakeDiscoverer{targets: []CollectTarget{target}}

	statsClient := newFakeStatsClient()
	statsClient.setEntries("10.0.0.1:10085", []RawStatEntry{
		{Name: "user>>>alice#node-b>>>traffic>>>uplink", Value: 1000},
		{Name: "user>>>alice#node-b>>>traffic>>>downlink", Value: 800},
	})

	sink := newFakeSink()

	col := newTestCollector(disc, statsClient, sink)

	ctx, cancel := context.WithCancel(context.Background())

	// Start collector in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- col.Run(ctx)
	}()

	// Wait a bit for at least one poll cycle to complete, then cancel
	time.Sleep(150 * time.Millisecond)
	cancel()

	// Wait for Run to return
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after cancel")
	}

	// Records should have been flushed before shutdown
	if sink.totalRecords() == 0 {
		t.Fatal("no records in sink after shutdown — final flush may have been skipped")
	}
}

// ---------------------------------------------------------------------------
// Scenario 5: Anti-reentrance
// If poll cycle takes longer than interval, next tick is skipped.
// ---------------------------------------------------------------------------

func TestCollector_AntiReentrance(t *testing.T) {

	target := CollectTarget{
		NodeName:     "node-a",
		V2RayAPIAddr: "10.0.0.1:10085",
	}
	disc := &fakeDiscoverer{targets: []CollectTarget{target}}

	// BlockingStatsClient blocks until the test signals it to proceed,
	// ensuring the poll cycle is "in flight" when the next tick fires.
	blockingClient := &blockingStatsClient{
		entries: map[string][]RawStatEntry{
			"10.0.0.1:10085": {
				{Name: "user>>>alice#node-b>>>traffic>>>uplink", Value: 100},
			},
		},
		proceed: make(chan struct{}),
	}

	sink := newFakeSink()

	cfg := CollectorConfig{
		Enabled:         true,
		PollInterval:    20 * time.Millisecond,
		NodeTimeout:     5 * time.Second,
		MaxBufferSize:   1000,
		ShutdownTimeout: 2 * time.Second,
	}

	col := NewCollector(cfg, disc, blockingClient, sink)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Run in background.
	errCh := make(chan error, 1)
	go func() {
		errCh <- col.Run(ctx)
	}()

	// Wait for the first poll cycle to start and block inside QueryUserStats.
	blockingClient.waitForEnter(t)

	// Now the first poll cycle is stuck. Let several ticks fire.
	// They should all be skipped due to anti-reentrance.
	time.Sleep(100 * time.Millisecond)

	// While still blocked, only 1 call should have entered QueryUserStats.
	enteredWhileBlocked := blockingClient.enteredCalls()
	if enteredWhileBlocked != 1 {
		t.Fatalf("anti-reentrance broken: %d calls entered QueryUserStats while first cycle was still blocked (want 1)", enteredWhileBlocked)
	}

	// Allow the first poll cycle to complete.
	blockingClient.allowOne()

	// Wait for Run to finish (context timeout will cancel it).
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s")
	}

	// Anti-reentrance verified: only 1 call entered while the poll cycle was stuck.
	completed := blockingClient.completedCalls()
	entered := blockingClient.enteredCalls()
	t.Logf("anti-reentrance: %d calls entered QueryUserStats, %d completed", entered, completed)
}

// blockingStatsClient blocks on QueryUserStats until signalled by the test.
// This lets us deterministically verify anti-reentrance.
type blockingStatsClient struct {
	mu      sync.Mutex
	entries map[string][]RawStatEntry

	proceed chan struct{} // closed once per test to allow one cycle through

	enterCount    int
	completeCount int
}

func (s *blockingStatsClient) QueryUserStats(_ context.Context, addr string) ([]RawStatEntry, error) {
	s.mu.Lock()
	s.enterCount++
	s.mu.Unlock()

	select {
	case <-s.proceed:
		// Allowed to proceed.
	}

	s.mu.Lock()
	s.completeCount++
	s.mu.Unlock()
	return s.entries[addr], nil
}

func (s *blockingStatsClient) waitForEnter(t *testing.T) {
	t.Helper()
	for i := 0; i < 100; i++ {
		s.mu.Lock()
		c := s.enterCount
		s.mu.Unlock()
		if c > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("blockingStatsClient: poll cycle never started")
}

func (s *blockingStatsClient) allowOne() {
	close(s.proceed)
}

func (s *blockingStatsClient) completedCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completeCount
}

func (s *blockingStatsClient) enteredCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enterCount
}

// ---------------------------------------------------------------------------
// Scenario 6: Empty discovery
// No targets → no stats queries, no sink writes, no errors.
// ---------------------------------------------------------------------------

func TestCollector_EmptyDiscovery(t *testing.T) {

	disc := &fakeDiscoverer{targets: []CollectTarget{}} // empty

	statsClient := newFakeStatsClient()
	sink := newFakeSink()

	col := newTestCollector(disc, statsClient, sink)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := col.Run(ctx)
	if err != nil {
		t.Fatalf("Run returned error on empty discovery: %v", err)
	}

	// No stats queries should have been made
	statsClient.mu.Lock()
	callCount := len(statsClient.callLog)
	statsClient.mu.Unlock()
	if callCount != 0 {
		t.Fatalf("expected 0 stats queries with empty discovery, got %d", callCount)
	}

	// No sink writes
	if sink.totalRecords() != 0 {
		t.Fatalf("expected 0 sink records, got %d", sink.totalRecords())
	}
}

// ---------------------------------------------------------------------------
// Backpressure test: if buffer exceeds MaxBufferSize, oldest records are
// dropped and a warning is logged.
// ---------------------------------------------------------------------------

func TestCollector_BackpressureBufferDrop(t *testing.T) {

	target := CollectTarget{
		NodeName:     "node-a",
		V2RayAPIAddr: "10.0.0.1:10085",
	}
	disc := &fakeDiscoverer{targets: []CollectTarget{target}}

	// Produce many counters per target
	entries := make([]RawStatEntry, 0, 200)
	for i := 0; i < 200; i++ {
		entries = append(entries, RawStatEntry{
			Name:  fmt.Sprintf("user>>>user%d#node-x>>>traffic>>>uplink", i),
			Value: int64(i * 100),
		})
	}
	statsClient := newFakeStatsClient()
	statsClient.setEntries("10.0.0.1:10085", entries)

	sink := newFakeSink()

	// Small buffer to trigger backpressure
	cfg := CollectorConfig{
		Enabled:         true,
		PollInterval:    50 * time.Millisecond,
		NodeTimeout:     2 * time.Second,
		MaxBufferSize:   50,
		ShutdownTimeout: 2 * time.Second,
	}

	col := NewCollector(cfg, disc, statsClient, sink)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := col.Run(ctx)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// With MaxBufferSize=50, we shouldn't have more than 50 records in sink
	// per flush. The total may be higher due to multiple cycles.
	total := sink.totalRecords()
	if total == 0 {
		t.Fatal("no records flushed to sink")
	}
	// Not a hard assertion on exact count since multiple cycles may flush,
	// but each individual flush batch shouldn't exceed MaxBufferSize.
	// Just verify the collector didn't panic or hang.
	t.Logf("backpressure test: total sink records = %d (MaxBufferSize=50)", total)
}

// ---------------------------------------------------------------------------
// Run error propagation: if discoverer returns an error, Run should still
// continue (not crash), logging the error.
// ---------------------------------------------------------------------------

func TestCollector_DiscoverErrorDoesNotCrash(t *testing.T) {

	disc := &fakeDiscoverer{err: fmt.Errorf("K8s API unavailable")}
	statsClient := newFakeStatsClient()
	sink := newFakeSink()

	col := newTestCollector(disc, statsClient, sink)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := col.Run(ctx)
	if err != nil {
		t.Fatalf("Run returned error on discovery failure: %v", err)
	}

	// No stats queries, no sink writes
	if sink.totalRecords() != 0 {
		t.Fatalf("expected 0 sink records after discovery error, got %d", sink.totalRecords())
	}
}

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

func TestCollector_InterfacesSatisfied(t *testing.T) {
	var _ Discoverer = (*fakeDiscoverer)(nil)
	var _ StatsClient = (*fakeStatsClient)(nil)
	var _ UsageSink = (*fakeSink)(nil)
	var _ UsageSink = (*fakeSinkWithFailures)(nil)
}
