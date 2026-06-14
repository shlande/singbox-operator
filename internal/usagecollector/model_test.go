package usagecollector

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// test helpers — in-memory UsageSink implementation for contract testing
// ---------------------------------------------------------------------------

type memorySink struct {
	records []UsageRecord
	closed  bool
}

func (s *memorySink) Write(_ context.Context, batch UsageBatch) error {
	if s.closed {
		return errSinkClosed
	}
	s.records = append(s.records, batch...)
	return nil
}

func (s *memorySink) Close(_ context.Context) error {
	s.closed = true
	return nil
}

var errSinkClosed = &sinkClosedError{}

type sinkClosedError struct{}

func (e *sinkClosedError) Error() string { return "sink is closed" }

// ---------------------------------------------------------------------------
// TestUsageRecord — table-driven tests for UsageRecord validation & structure
// ---------------------------------------------------------------------------

func TestUsageRecord(t *testing.T) {
	t.Run("ValidateRecord/missing-user", func(t *testing.T) {
		r := UsageRecord{
			User:          "",
			Node:          "node-a",
			UplinkBytes:   100,
			DownlinkBytes: 200,
			CollectedAt:   time.Now(),
		}
		if err := ValidateRecord(r); err == nil {
			t.Fatal("expected error for missing user, got nil")
		}
	})

	t.Run("ValidateRecord/missing-node", func(t *testing.T) {
		r := UsageRecord{
			User:          "alice",
			Node:          "",
			UplinkBytes:   100,
			DownlinkBytes: 200,
			CollectedAt:   time.Now(),
		}
		if err := ValidateRecord(r); err == nil {
			t.Fatal("expected error for missing node, got nil")
		}
	})

	t.Run("ValidateRecord/valid", func(t *testing.T) {
		r := UsageRecord{
			User:          "alice",
			Node:          "node-a",
			UplinkBytes:   100,
			DownlinkBytes: 200,
			CollectedAt:   time.Now(),
		}
		if err := ValidateRecord(r); err != nil {
			t.Fatalf("unexpected error for valid record: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// TestDocumentKey — table-driven tests for DocumentKey idempotency
// ---------------------------------------------------------------------------

func TestDocumentKey(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 123456789, time.UTC)

	t.Run("same-inputs-same-key", func(t *testing.T) {
		r1 := UsageRecord{
			User:          "alice",
			Node:          "node-a",
			UplinkBytes:   100,
			DownlinkBytes: 200,
			CollectedAt:   now,
		}
		r2 := UsageRecord{
			User:          "alice",
			Node:          "node-a",
			UplinkBytes:   100,
			DownlinkBytes: 200,
			CollectedAt:   now,
		}
		k1 := DocumentKey(r1)
		k2 := DocumentKey(r2)
		if k1 != k2 {
			t.Fatalf("same inputs produced different keys:\n  key1: %s\n  key2: %s", k1, k2)
		}
	})

	t.Run("different-user-different-key", func(t *testing.T) {
		r1 := UsageRecord{User: "alice", Node: "node-a", CollectedAt: now}
		r2 := UsageRecord{User: "bob", Node: "node-a", CollectedAt: now}
		if DocumentKey(r1) == DocumentKey(r2) {
			t.Fatal("different users produced the same key")
		}
	})

	t.Run("different-node-different-key", func(t *testing.T) {
		r1 := UsageRecord{User: "alice", Node: "node-a", CollectedAt: now}
		r2 := UsageRecord{User: "alice", Node: "node-b", CollectedAt: now}
		if DocumentKey(r1) == DocumentKey(r2) {
			t.Fatal("different nodes produced the same key")
		}
	})

	t.Run("different-time-different-key", func(t *testing.T) {
		r1 := UsageRecord{User: "alice", Node: "node-a", CollectedAt: now}
		r2 := UsageRecord{User: "alice", Node: "node-a", CollectedAt: now.Add(time.Second)}
		if DocumentKey(r1) == DocumentKey(r2) {
			t.Fatal("different collection times produced the same key")
		}
	})

	t.Run("key-is-hex-string", func(t *testing.T) {
		r := UsageRecord{User: "alice", Node: "node-a", CollectedAt: now}
		key := DocumentKey(r)
		if len(key) != 64 {
			t.Fatalf("expected 64-char hex key, got %d chars: %q", len(key), key)
		}
	})
}

// ---------------------------------------------------------------------------
// TestUsageSinkContract — tests for the UsageSink interface contract
// ---------------------------------------------------------------------------

func TestUsageSinkContract(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	t.Run("write-and-read-back", func(t *testing.T) {
		sink := &memorySink{}
		batch := UsageBatch{
			{User: "alice", Node: "node-a", UplinkBytes: 100, DownlinkBytes: 200, CollectedAt: now},
			{User: "bob", Node: "node-b", UplinkBytes: 300, DownlinkBytes: 400, CollectedAt: now},
		}
		if err := sink.Write(ctx, batch); err != nil {
			t.Fatalf("unexpected write error: %v", err)
		}
		if len(sink.records) != 2 {
			t.Fatalf("expected 2 records, got %d", len(sink.records))
		}
	})

	t.Run("close-prevents-write", func(t *testing.T) {
		sink := &memorySink{}
		if err := sink.Close(ctx); err != nil {
			t.Fatalf("unexpected close error: %v", err)
		}
		if err := sink.Write(ctx, UsageBatch{}); err == nil {
			t.Fatal("expected error after close, got nil")
		}
	})

	t.Run("multiple-writes-accumulate", func(t *testing.T) {
		sink := &memorySink{}
		rec := UsageRecord{User: "alice", Node: "node-a", CollectedAt: now}

		if err := sink.Write(ctx, UsageBatch{rec}); err != nil {
			t.Fatalf("first write failed: %v", err)
		}
		if err := sink.Write(ctx, UsageBatch{rec}); err != nil {
			t.Fatalf("second write failed: %v", err)
		}
		if len(sink.records) != 2 {
			t.Fatalf("expected 2 records after two writes, got %d", len(sink.records))
		}
	})
}
