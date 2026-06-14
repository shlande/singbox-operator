package usagecollector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// ElasticsearchSink — UsageSink implementation backed by an ES data stream.
// ---------------------------------------------------------------------------

// ElasticsearchSink writes usage records to an Elasticsearch data stream
// using the _bulk API with create actions for idempotent writes.
type ElasticsearchSink struct {
	client  *http.Client
	bulkURL string // fully-qualified POST URL (base + "/{stream}/_bulk")
	apiKey  string
	closed  bool
	mu      sync.Mutex
}

// NewElasticsearchSink creates an ElasticsearchSink from the provided
// CollectorConfig. At minimum, ESEndpoint and ESDataStream must be
// non-empty. If ESAPIKey is set, every request carries an
// Authorization: ApiKey header.
func NewElasticsearchSink(cfg CollectorConfig) (*ElasticsearchSink, error) {
	if cfg.ESEndpoint == "" {
		return nil, fmt.Errorf("ESEndpoint is required")
	}
	if cfg.ESDataStream == "" {
		return nil, fmt.Errorf("ESDataStream is required")
	}

	base := strings.TrimSuffix(cfg.ESEndpoint, "/")
	bulkURL, err := url.JoinPath(base, cfg.ESDataStream, "_bulk")
	if err != nil {
		return nil, fmt.Errorf("failed to build ES bulk URL: %w", err)
	}

	return &ElasticsearchSink{
		client:  &http.Client{Timeout: cfg.ShutdownTimeout},
		bulkURL: bulkURL,
		apiKey:  cfg.ESAPIKey,
	}, nil
}

// Write sends a batch of usage records to the Elasticsearch data stream
// using the _bulk API. An empty or nil batch returns nil immediately
// without sending an HTTP request.
//
// Every record is written with a create action and the document _id set
// to DocumentKey(record), ensuring idempotent writes. ES status 201
// (created) and 409 (conflict/already exists) are both treated as success.
//
// When the bulk response reports partial failures (some items failed),
// Write returns a *BulkPartialFailureError containing the count of
// succeeded and failed items plus the _id values of every failed item.
func (s *ElasticsearchSink) Write(ctx context.Context, batch UsageBatch) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("elasticsearch sink is closed")
	}
	s.mu.Unlock()

	if len(batch) == 0 {
		return nil
	}

	// Build NDJSON body: action line + document line per record.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	for _, r := range batch {
		action := bulkAction{
			Create: bulkActionCreate{
				Index: "", // set via data stream URL, not per-document
				ID:    DocumentKey(r),
			},
		}
		if err := enc.Encode(action); err != nil {
			return fmt.Errorf("failed to encode bulk action: %w", err)
		}
		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("failed to encode usage record: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.bulkURL, &buf)
	if err != nil {
		return fmt.Errorf("failed to create ES request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "ApiKey "+s.apiKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("ES bulk request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read ES response: %w", err)
	}

	if resp.StatusCode >= 300 {
		return fmt.Errorf("ES returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var bulkResp bulkResponse
	if err := json.Unmarshal(body, &bulkResp); err != nil {
		return fmt.Errorf("failed to parse ES bulk response: %w", err)
	}

	return s.evaluateBulkResponse(bulkResp)
}

// evaluateBulkResponse inspects every item in the bulk response.
// Items with status 201 (created) or 409 (conflict) are treated as
// success. Any other status is a failure.
//
// When all items succeeded, nil is returned. Otherwise a
// *BulkPartialFailureError is returned with counts and failed _id values.
func (s *ElasticsearchSink) evaluateBulkResponse(resp bulkResponse) error {
	var failedIDs []string
	succeeded := 0
	failed := 0

	for _, item := range resp.Items {
		status := item.Create.Status
		id := item.Create.ID
		if status == 201 || status == 409 {
			succeeded++
		} else {
			failed++
			if id != "" {
				failedIDs = append(failedIDs, id)
			}
		}
	}

	if failed == 0 {
		return nil
	}

	return &BulkPartialFailureError{
		Succeeded: succeeded,
		Failed:    failed,
		FailedIDs: failedIDs,
	}
}

// Close marks the sink as closed. After Close returns, subsequent Write
// calls will return an error. There is no internal buffer to flush —
// every Write call sends the request immediately.
func (s *ElasticsearchSink) Close(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.client.CloseIdleConnections()
	return nil
}

// ---------------------------------------------------------------------------
// ES bulk JSON types
// ---------------------------------------------------------------------------

type bulkActionCreate struct {
	Index string `json:"_index,omitempty"`
	ID    string `json:"_id"`
}

type bulkAction struct {
	Create bulkActionCreate `json:"create"`
}

type bulkItemCreate struct {
	ID     string `json:"_id"`
	Status int    `json:"status"`
	Error  *struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	} `json:"error,omitempty"`
}

type bulkItem struct {
	Create bulkItemCreate `json:"create"`
}

type bulkResponse struct {
	Errors bool       `json:"errors"`
	Items  []bulkItem `json:"items"`
}

// ---------------------------------------------------------------------------
// BulkPartialFailureError
// ---------------------------------------------------------------------------

// BulkPartialFailureError is returned by Write when the Elasticsearch
// _bulk response reports that some documents failed to be created.
// The caller can inspect Succeeded / Failed counts and FailedIDs to
// decide whether to retry the failed subset.
type BulkPartialFailureError struct {
	Succeeded int
	Failed    int
	FailedIDs []string
}

func (e *BulkPartialFailureError) Error() string {
	return fmt.Sprintf(
		"bulk write partial failure: %d succeeded, %d failed (failed ids: %s)",
		e.Succeeded, e.Failed, strings.Join(e.FailedIDs, ", "),
	)
}
