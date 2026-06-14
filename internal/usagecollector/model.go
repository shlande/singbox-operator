package usagecollector

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// UsageRecord represents a single traffic usage data point collected from
// sing-box. Each record captures the uplink/downlink bytes for a specific
// user-node pair at a specific collection time.
type UsageRecord struct {
	User          string    `json:"user"`
	Node          string    `json:"node"`
	UplinkBytes   int64     `json:"uplink_bytes"`
	DownlinkBytes int64     `json:"downlink_bytes"`
	CollectedAt   time.Time `json:"collected_at"`
}

// UsageBatch is a collection of UsageRecords written to a sink in one call.
type UsageBatch []UsageRecord

// ValidateRecord checks that required fields are populated. Returns an error
// if User or Node is empty.
func ValidateRecord(r UsageRecord) error {
	if r.User == "" {
		return fmt.Errorf("usage record: user field is required")
	}
	if r.Node == "" {
		return fmt.Errorf("usage record: node field is required")
	}
	return nil
}

// DocumentKey returns a stable, deterministic key for deduplication.
// The key is a SHA-256 hex digest of (user + node + collected_at unix nano).
// Same inputs always produce the same output.
func DocumentKey(r UsageRecord) string {
	h := sha256.Sum256([]byte(r.User + "\x00" + r.Node + "\x00" + fmt.Sprint(r.CollectedAt.UnixNano())))
	return fmt.Sprintf("%x", h)
}
