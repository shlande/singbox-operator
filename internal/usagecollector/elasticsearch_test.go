package usagecollector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newTestES creates an httptest server that records received requests
// and returns configurable responses. The handler receives the request
// and the test controls its behaviour.
func newTestES(handler http.HandlerFunc) (*httptest.Server, *ElasticsearchSink) {
	ts := httptest.NewServer(http.HandlerFunc(handler))
	sink, _ := NewElasticsearchSink(CollectorConfig{
		ESEndpoint:   ts.URL,
		ESDataStream: "usage-traffic",
	})
	return ts, sink
}

// testRecord returns a UsageRecord with fixed fields.
func testRecord(user, node string, t time.Time) UsageRecord {
	return UsageRecord{
		Timestamp:     t,
		User:          user,
		Node:          node,
		UplinkBytes:   100,
		DownlinkBytes: 200,
		CollectedAt:   t,
	}
}

// parseBulkRequest parses an NDJSON bulk request body into action lines
// and document lines. Returns slices of parsed map objects.
func parseBulkRequest(body string) (actions []map[string]any, docs []map[string]any) {
	scanner := bufio.NewScanner(strings.NewReader(body))
	lines := []string{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	for i := 0; i < len(lines); i += 2 {
		var act map[string]any
		json.Unmarshal([]byte(lines[i]), &act)
		actions = append(actions, act)
		if i+1 < len(lines) {
			var doc map[string]any
			json.Unmarshal([]byte(lines[i+1]), &doc)
			docs = append(docs, doc)
		}
	}
	return
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_BatchOfTwoRecords — verifies that a batch
// of 2 records produces a correct NDJSON bulk request with _id set.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_BatchOfTwoRecords(t *testing.T) {
	var receivedBody string
	var receivedPath string
	handler := func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		receivedBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// All docs created successfully
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}},{"create":{"status":201}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	batch := UsageBatch{
		testRecord("alice", "node-a", now),
		testRecord("bob", "node-b", now),
	}

	ctx := context.Background()
	if err := sink.Write(ctx, batch); err != nil {
		t.Fatalf("unexpected Write error: %v", err)
	}

	// Verify URL path
	if receivedPath != "/usage-traffic/_bulk" {
		t.Fatalf("expected path /usage-traffic/_bulk, got %q", receivedPath)
	}

	actions, docs := parseBulkRequest(receivedBody)
	if len(actions) != 2 {
		t.Fatalf("expected 2 action lines, got %d", len(actions))
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 document lines, got %d", len(docs))
	}

	// Check action lines contain "create" with _id. _index is optional
	// because the data stream name is already in the URL path.
	for i, act := range actions {
		create, ok := act["create"].(map[string]any)
		if !ok {
			t.Fatalf("action[%d]: missing 'create' key, got %v", i, act)
		}
		if id, ok := create["_id"].(string); !ok || id == "" {
			t.Errorf("action[%d]: _id is missing or empty", i)
		}
	}

	// Check document lines contain record fields
	for i, doc := range docs {
		if doc["user"] == nil {
			t.Errorf("doc[%d]: missing user field", i)
		}
		if doc["uplink_bytes"] == nil {
			t.Errorf("doc[%d]: missing uplink_bytes field", i)
		}
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_IdempotentSameID — verifies that writing
// the same record twice produces the same _id in both requests.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_IdempotentSameID(t *testing.T) {
	requestCount := 0
	var ids [][]string
	handler := func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		body := string(buf)

		actions, _ := parseBulkRequest(body)
		var reqIDs []string
		for _, act := range actions {
			create := act["create"].(map[string]any)
			reqIDs = append(reqIDs, create["_id"].(string))
		}
		ids = append(ids, reqIDs)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// On second write, return conflict (409) for idempotency proof
		if requestCount == 1 {
			w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}}]}`))
		} else {
			w.Write([]byte(`{"errors":false,"items":[{"create":{"status":409}}]}`))
		}
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	rec := testRecord("alice", "node-a", now)

	ctx := context.Background()
	// First write
	if err := sink.Write(ctx, UsageBatch{rec}); err != nil {
		t.Fatalf("first Write failed: %v", err)
	}
	// Second write (same record)
	if err := sink.Write(ctx, UsageBatch{rec}); err != nil {
		t.Fatalf("second Write failed: %v", err)
	}

	if requestCount != 2 {
		t.Fatalf("expected 2 HTTP requests, got %d", requestCount)
	}

	// Both requests must have the same _id
	id1 := ids[0][0]
	id2 := ids[1][0]
	if id1 != id2 {
		t.Fatalf("idempotency broken: first _id=%q, second _id=%q", id1, id2)
	}

	// The _id should match DocumentKey
	expectedID := DocumentKey(rec)
	if id1 != expectedID {
		t.Fatalf("_id mismatch: got %q, want %q (DocumentKey)", id1, expectedID)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_PartialFailure — verifies that when ES
// reports partial bulk failure (one doc accepted, one rejected),
// Write returns a retryable BulkPartialFailureError.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_PartialFailure(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Item 0: 201 (success), Item 1: 429 (too many requests)
		w.Write([]byte(`{"errors":true,"items":[{"create":{"_id":"abc","status":201}},{"create":{"_id":"def","status":429,"error":{"type":"es_rejected_execution_exception","reason":"rejected"}}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	batch := UsageBatch{
		testRecord("alice", "node-a", now),
		testRecord("bob", "node-b", now),
	}

	ctx := context.Background()
	err := sink.Write(ctx, batch)
	if err == nil {
		t.Fatal("expected error for partial failure, got nil")
	}

	var pfErr *BulkPartialFailureError
	if !errors.As(err, &pfErr) {
		t.Fatalf("expected BulkPartialFailureError, got %T: %v", err, err)
	}

	if pfErr.Succeeded != 1 {
		t.Errorf("Succeeded = %d, want 1", pfErr.Succeeded)
	}
	if pfErr.Failed != 1 {
		t.Errorf("Failed = %d, want 1", pfErr.Failed)
	}
	if len(pfErr.FailedIDs) != 1 {
		t.Errorf("FailedIDs length = %d, want 1", len(pfErr.FailedIDs))
	}
	if pfErr.FailedIDs[0] != "def" {
		t.Errorf("FailedIDs[0] = %q, want %q", pfErr.FailedIDs[0], "def")
	}
	if pfErr.Error() == "" {
		t.Error("BulkPartialFailureError.Error() returned empty string")
	}

	// Verify the error message contains useful info
	errMsg := pfErr.Error()
	if !strings.Contains(errMsg, "1 succeeded") {
		t.Errorf("error message missing succeeded count: %s", errMsg)
	}
	if !strings.Contains(errMsg, "1 failed") {
		t.Errorf("error message missing failed count: %s", errMsg)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_ApiKeyAuth — verifies the Authorization
// header is set when ESAPIKey is configured.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_ApiKeyAuth(t *testing.T) {
	var receivedAuth string
	handler := func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}}]}`))
	}

	ts := httptest.NewServer(http.HandlerFunc(handler))
	defer ts.Close()

	sink, err := NewElasticsearchSink(CollectorConfig{
		ESEndpoint:   ts.URL,
		ESDataStream: "usage-traffic",
		ESAPIKey:     "secret-key-123",
	})
	if err != nil {
		t.Fatalf("NewElasticsearchSink failed: %v", err)
	}
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := sink.Write(ctx, UsageBatch{testRecord("alice", "node-a", now)}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if receivedAuth != "ApiKey secret-key-123" {
		t.Errorf("Authorization = %q, want %q", receivedAuth, "ApiKey secret-key-123")
	}

	// Verify no Authorization header when ESAPIKey is empty
	var noAuthReceived string
	handler2 := func(w http.ResponseWriter, r *http.Request) {
		noAuthReceived = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}}]}`))
	}

	ts2 := httptest.NewServer(http.HandlerFunc(handler2))
	defer ts2.Close()

	sink2, _ := NewElasticsearchSink(CollectorConfig{
		ESEndpoint:   ts2.URL,
		ESDataStream: "usage-traffic",
		ESAPIKey:     "", // no key
	})
	defer sink2.Close(context.Background())

	if err := sink2.Write(ctx, UsageBatch{testRecord("alice", "node-a", now)}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if noAuthReceived != "" {
		t.Errorf("unexpected Authorization header: %q", noAuthReceived)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_EmptyBatch — verifies that writing an empty
// batch returns nil without sending an HTTP request.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_EmptyBatch(t *testing.T) {
	requestSent := false
	handler := func(w http.ResponseWriter, r *http.Request) {
		requestSent = true
		w.WriteHeader(http.StatusOK)
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	ctx := context.Background()
	if err := sink.Write(ctx, UsageBatch{}); err != nil {
		t.Fatalf("unexpected error for empty batch: %v", err)
	}

	if requestSent {
		t.Fatal("expected no HTTP request for empty batch, but one was sent")
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_NilBatch — verifies nil batch is handled
// like empty.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_NilBatch(t *testing.T) {
	requestSent := false
	handler := func(w http.ResponseWriter, r *http.Request) {
		requestSent = true
		w.WriteHeader(http.StatusOK)
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	ctx := context.Background()
	if err := sink.Write(ctx, nil); err != nil {
		t.Fatalf("unexpected error for nil batch: %v", err)
	}

	if requestSent {
		t.Fatal("expected no HTTP request for nil batch, but one was sent")
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Close — verifies Close flushes and prevents
// further writes.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Close(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()

	ctx := context.Background()

	// Write before close should succeed
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	if err := sink.Write(ctx, UsageBatch{testRecord("alice", "node-a", now)}); err != nil {
		t.Fatalf("Write before Close failed: %v", err)
	}

	// Close should succeed
	if err := sink.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Write after close should fail
	now2 := time.Date(2026, 6, 14, 12, 0, 1, 0, time.UTC)
	if err := sink.Write(ctx, UsageBatch{testRecord("bob", "node-b", now2)}); err == nil {
		t.Fatal("expected error after Close, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_NewElasticsearchSink_Validation — verifies
// constructor input validation.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_NewElasticsearchSink_Validation(t *testing.T) {
	tests := []struct {
		name string
		cfg  CollectorConfig
	}{
		{
			name: "empty endpoint",
			cfg: CollectorConfig{
				ESEndpoint:   "",
				ESDataStream: "usage-traffic",
			},
		},
		{
			name: "empty data stream",
			cfg: CollectorConfig{
				ESEndpoint:   "http://es:9200",
				ESDataStream: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewElasticsearchSink(tt.cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_ContentType — verifies Content-Type header.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_ContentType(t *testing.T) {
	var receivedContentType string
	handler := func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := sink.Write(ctx, UsageBatch{testRecord("alice", "node-a", now)}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if receivedContentType != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want application/x-ndjson", receivedContentType)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_HTTPError — verifies non-200 HTTP responses
// return an error.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_HTTPError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"authentication failed"}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	err := sink.Write(ctx, UsageBatch{testRecord("alice", "node-a", now)})
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should contain status code 401: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_ColumnTypes — verifies all UsageRecord
// fields are included in the document body.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_ColumnTypes(t *testing.T) {
	var receivedBody string
	handler := func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		receivedBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	rec := UsageRecord{
		User:          "charlie",
		Node:          "node-c",
		UplinkBytes:   1024,
		DownlinkBytes: 2048,
		CollectedAt:   now,
	}
	ctx := context.Background()
	if err := sink.Write(ctx, UsageBatch{rec}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	_, docs := parseBulkRequest(receivedBody)
	if len(docs) != 1 {
		t.Fatalf("expected 1 document line, got %d", len(docs))
	}
	doc := docs[0]

	// All columns present
	if doc["user"] != "charlie" {
		t.Errorf("user = %v, want charlie", doc["user"])
	}
	if doc["node"] != "node-c" {
		t.Errorf("node = %v, want node-c", doc["node"])
	}
	// JSON numbers can be float64
	uplink, _ := doc["uplink_bytes"].(float64)
	if int64(uplink) != 1024 {
		t.Errorf("uplink_bytes = %v, want 1024", doc["uplink_bytes"])
	}
	downlink, _ := doc["downlink_bytes"].(float64)
	if int64(downlink) != 2048 {
		t.Errorf("downlink_bytes = %v, want 2048", doc["downlink_bytes"])
	}
	if doc["collected_at"] == nil {
		t.Error("collected_at is missing")
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_URLConstruction — verifies the vanity URL
// is not included in the bulk path (only path, not full URL).
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_URLConstruction(t *testing.T) {
	var receivedURL string
	handler := func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := sink.Write(ctx, UsageBatch{testRecord("alice", "node-a", now)}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if receivedURL != "/usage-traffic/_bulk" {
		t.Errorf("URL = %q, want /usage-traffic/_bulk", receivedURL)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_409IsSuccess — 409 (conflict) is not treated
// as error — it means the document already exists (idempotent).
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_409IsSuccess(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// All docs return 409 (already exist) — this is success for idempotent writes
		w.Write([]byte(`{"errors":false,"items":[{"create":{"_id":"abc","status":409}},{"create":{"_id":"def","status":409}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	batch := UsageBatch{
		testRecord("alice", "node-a", now),
		testRecord("bob", "node-b", now),
	}

	ctx := context.Background()
	if err := sink.Write(ctx, batch); err != nil {
		t.Fatalf("expected nil error for all-409 (idempotency), got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_ContextCancellation — respects context.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_ContextCancellation(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// hang forever (test will cancel context)
		select {}
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := sink.Write(ctx, UsageBatch{testRecord("alice", "node-a", now)})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_AllFailed — when all items fail, error is returned.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_AllFailed(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":true,"items":[{"create":{"_id":"a","status":500,"error":{"type":"internal","reason":"fail"}}},{"create":{"_id":"b","status":500,"error":{"type":"internal","reason":"fail"}}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	err := sink.Write(ctx, UsageBatch{
		testRecord("alice", "node-a", now),
		testRecord("bob", "node-b", now),
	})

	var pfErr *BulkPartialFailureError
	if !errors.As(err, &pfErr) {
		t.Fatalf("expected BulkPartialFailureError, got %T", err)
	}
	if pfErr.Succeeded != 0 {
		t.Errorf("Succeeded = %d, want 0", pfErr.Succeeded)
	}
	if pfErr.Failed != 2 {
		t.Errorf("Failed = %d, want 2", pfErr.Failed)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_ImplementsUsageSink — compile-time interface check.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_ImplementsUsageSink(t *testing.T) {
	var _ UsageSink = (*ElasticsearchSink)(nil)
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_Structure — verify the exact NDJSON structure
// including newline delimiters.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_Structure(t *testing.T) {
	var receivedBody string
	handler := func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		receivedBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := sink.Write(ctx, UsageBatch{testRecord("alice", "node-a", now)}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Verify NDJSON format: action line + "\n" + doc line + "\n"
	lines := strings.Split(strings.TrimSuffix(receivedBody, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %q", len(lines), receivedBody)
	}

	// Line 0 must be the action metadata
	if !strings.HasPrefix(lines[0], `{"create"`) {
		t.Errorf("line 0 should be action metadata, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], `{"@timestamp"`) {
		t.Errorf("line 1 should be document body, got %q", lines[1])
	}

	// Body must end with \n
	if !strings.HasSuffix(receivedBody, "\n") {
		t.Error("NDJSON body must end with trailing newline")
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_ESResponseHasErrorTrue — verifies that when
// the ES response has "errors": true but all items have status 201/409,
// it's still treated as success (items field wins).
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_ErrorsFlagButAllSuccess(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// errors:true but all items are 201 — shouldn't happen in practice,
		// but we trust item-level status
		w.Write([]byte(`{"errors":true,"items":[{"create":{"_id":"a","status":201}},{"create":{"_id":"b","status":201}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	err := sink.Write(ctx, UsageBatch{
		testRecord("alice", "node-a", now),
		testRecord("bob", "node-b", now),
	})
	if err != nil {
		t.Fatalf("expected nil error (all items 201), got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_RecordTimestamp — verifies CollectedAt is
// serialized correctly.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_RecordTimestamp(t *testing.T) {
	var receivedBody string
	handler := func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		receivedBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	rec := testRecord("alice", "node-a", now)
	ctx := context.Background()
	if err := sink.Write(ctx, UsageBatch{rec}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	_, docs := parseBulkRequest(receivedBody)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	collectedAt, ok := docs[0]["collected_at"].(string)
	if !ok {
		t.Fatalf("collected_at is not a string: %T", docs[0]["collected_at"])
	}
	expectedTS := now.Format(time.RFC3339Nano)
	if collectedAt != expectedTS {
		t.Errorf("collected_at = %q, want %q", collectedAt, expectedTS)
	}
}

// ---------------------------------------------------------------------------
// TestElasticsearchSink_Write_JSONFieldOrder — the order of fields in JSON
// doesn't matter for correctness, but we verify the field names are present.
// ---------------------------------------------------------------------------

func TestElasticsearchSink_Write_JSONFieldPresence(t *testing.T) {
	expectedFields := []string{"user", "node", "uplink_bytes", "downlink_bytes", "collected_at"}
	var receivedBody string
	handler := func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		receivedBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errors":false,"items":[{"create":{"status":201}}]}`))
	}

	ts, sink := newTestES(handler)
	defer ts.Close()
	defer sink.Close(context.Background())

	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := sink.Write(ctx, UsageBatch{testRecord("alice", "node-a", now)}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	for _, field := range expectedFields {
		if !strings.Contains(receivedBody, fmt.Sprintf("%q", field)) {
			t.Errorf("document missing field %q", field)
		}
	}
}
