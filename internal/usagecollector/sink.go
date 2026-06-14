package usagecollector

import "context"

// UsageSink is the abstract interface for writing usage records to a storage
// backend. Implementations must be safe for concurrent use.
//
// Implementations must not accept or expose any storage-backend-specific
// types (e.g. Elasticsearch request/response objects).
type UsageSink interface {
	// Write persists a batch of usage records. The implementation must ensure
	// at-least-once semantics: if Write returns nil, all records in the batch
	// are durably stored (or duplicated in a way that is safe given the
	// idempotent DocumentKey).
	Write(ctx context.Context, batch UsageBatch) error

	// Close releases any resources held by the sink. After Close returns,
	// the sink must not be used for further Write calls.
	Close(ctx context.Context) error
}
