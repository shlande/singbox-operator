package usagecollector

import (
	"fmt"
	"time"
)

// CollectorConfig holds runtime configuration for the usage collector and
// its Elasticsearch sink. When Enabled is false, no collection occurs and
// Validate() always succeeds.
type CollectorConfig struct {
	// Enabled controls whether the usage collector runs. When false,
	// all other fields are ignored and Validate() returns nil.
	Enabled bool

	// PollInterval is how often the collector queries each sing-box node
	// for traffic stats. Must be > 0 when Enabled is true.
	PollInterval time.Duration

	// NodeTimeout is the per-node gRPC query timeout. Must be > 0 and
	// should typically be less than PollInterval.
	NodeTimeout time.Duration

	// ESEndpoint is the Elasticsearch sink URL (e.g. "http://elasticsearch:9200").
	// Required when Enabled is true.
	ESEndpoint string

	// ESAPIKey is an optional API key for Elasticsearch authentication.
	// This value must never be logged.
	ESAPIKey string

	// ESDataStream is the Elasticsearch data stream name (e.g. "usage-traffic").
	// Required when Enabled is true.
	ESDataStream string

	MaxBufferSize int

	// ShutdownTimeout is the maximum duration the collector waits for
	// in-flight flush operations to complete during graceful shutdown.
	ShutdownTimeout time.Duration
}

// DefaultCollectorConfig returns a CollectorConfig with all default values
// populated. The collector is disabled by default.
func DefaultCollectorConfig() CollectorConfig {
	return CollectorConfig{
		Enabled:         false,
		PollInterval:    30 * time.Second,
		NodeTimeout:     10 * time.Second,
		MaxBufferSize:   10000,
		ShutdownTimeout: 30 * time.Second,
	}
}

// Validate checks that the configuration is internally consistent.
// A disabled collector is always valid. When enabled, required fields
// must be set and durations must be positive.
func (c CollectorConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.PollInterval <= 0 {
		return fmt.Errorf("PollInterval must be positive, got %v", c.PollInterval)
	}
	if c.NodeTimeout <= 0 {
		return fmt.Errorf("NodeTimeout must be positive, got %v", c.NodeTimeout)
	}
	if c.MaxBufferSize <= 0 {
		return fmt.Errorf("MaxBufferSize must be positive, got %d", c.MaxBufferSize)
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("ShutdownTimeout must be positive, got %v", c.ShutdownTimeout)
	}
	if c.ESEndpoint == "" {
		return fmt.Errorf("ESEndpoint is required when collector is enabled")
	}
	if c.ESDataStream == "" {
		return fmt.Errorf("ESDataStream is required when collector is enabled")
	}

	return nil
}
