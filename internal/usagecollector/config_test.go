package usagecollector

import (
	"testing"
	"time"
)

// Defaults test: DefaultCollectorConfig() returns expected defaults.
func TestCollectorConfig_Defaults(t *testing.T) {
	c := DefaultCollectorConfig()

	if c.Enabled {
		t.Errorf("DefaultCollectorConfig().Enabled = true, want false")
	}
	if c.PollInterval != 30*time.Second {
		t.Errorf("DefaultCollectorConfig().PollInterval = %v, want 30s", c.PollInterval)
	}
	if c.NodeTimeout != 10*time.Second {
		t.Errorf("DefaultCollectorConfig().NodeTimeout = %v, want 10s", c.NodeTimeout)
	}
	if c.ESEndpoint != "" {
		t.Errorf("DefaultCollectorConfig().ESEndpoint = %q, want empty", c.ESEndpoint)
	}
	if c.ESAPIKey != "" {
		t.Errorf("DefaultCollectorConfig().ESAPIKey = %q, want empty", c.ESAPIKey)
	}
	if c.ESDataStream != "" {
		t.Errorf("DefaultCollectorConfig().ESDataStream = %q, want empty", c.ESDataStream)
	}
	if c.CheckpointPath != "/tmp/usage-collector-checkpoint.json" {
		t.Errorf("DefaultCollectorConfig().CheckpointPath = %q, want /tmp/usage-collector-checkpoint.json", c.CheckpointPath)
	}
	if c.MaxBufferSize != 10000 {
		t.Errorf("DefaultCollectorConfig().MaxBufferSize = %d, want 10000", c.MaxBufferSize)
	}
	if c.ShutdownTimeout != 30*time.Second {
		t.Errorf("DefaultCollectorConfig().ShutdownTimeout = %v, want 30s", c.ShutdownTimeout)
	}
}

// Disabled collector always passes validation.
func TestCollectorConfig_Validate_Disabled(t *testing.T) {
	c := CollectorConfig{Enabled: false}
	if err := c.Validate(); err != nil {
		t.Errorf("disabled config Validate() = %v, want nil", err)
	}
}

// Disabled even with missing ES fields passes validation.
func TestCollectorConfig_Validate_DisabledWithMissingES(t *testing.T) {
	c := CollectorConfig{
		Enabled:      false,
		PollInterval: 30 * time.Second,
		NodeTimeout:  10 * time.Second,
	}
	if err := c.Validate(); err != nil {
		t.Errorf("disabled config with missing ES fields Validate() = %v, want nil", err)
	}
}

// Enabled with valid fields passes validation.
func TestCollectorConfig_Validate_EnabledValid(t *testing.T) {
	c := CollectorConfig{
		Enabled:         true,
		PollInterval:    30 * time.Second,
		NodeTimeout:     10 * time.Second,
		ESEndpoint:      "http://elasticsearch:9200",
		ESDataStream:    "usage-traffic",
		CheckpointPath:  "/tmp/checkpoint.json",
		MaxBufferSize:   5000,
		ShutdownTimeout: 30 * time.Second,
	}
	if err := c.Validate(); err != nil {
		t.Errorf("valid enabled config Validate() = %v, want nil", err)
	}
}

// Enabled with empty ESEndpoint must fail.
func TestCollectorConfig_Validate_EnabledNoEndpoint(t *testing.T) {
	c := CollectorConfig{
		Enabled:      true,
		PollInterval: 30 * time.Second,
		NodeTimeout:  10 * time.Second,
		ESDataStream: "usage-traffic",
	}
	if err := c.Validate(); err == nil {
		t.Error("enabled config without ESEndpoint Validate() = nil, want error")
	}
}

// Enabled with empty ESDataStream must fail.
func TestCollectorConfig_Validate_EnabledNoDataStream(t *testing.T) {
	c := CollectorConfig{
		Enabled:      true,
		PollInterval: 30 * time.Second,
		NodeTimeout:  10 * time.Second,
		ESEndpoint:   "http://elasticsearch:9200",
	}
	if err := c.Validate(); err == nil {
		t.Error("enabled config without ESDataStream Validate() = nil, want error")
	}
}

// PollInterval <= 0 must fail.
func TestCollectorConfig_Validate_InvalidPollInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"zero", 0},
		{"negative", -5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := CollectorConfig{
				Enabled:         true,
				PollInterval:    tt.interval,
				NodeTimeout:     10 * time.Second,
				ESEndpoint:      "http://es:9200",
				ESDataStream:    "usage-traffic",
				CheckpointPath:  "/tmp/cp.json",
				MaxBufferSize:   1000,
				ShutdownTimeout: 30 * time.Second,
			}
			if err := c.Validate(); err == nil {
				t.Errorf("PollInterval=%v Validate() = nil, want error", tt.interval)
			}
		})
	}
}

// NodeTimeout <= 0 must fail.
func TestCollectorConfig_Validate_InvalidNodeTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{"zero", 0},
		{"negative", -1 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := CollectorConfig{
				Enabled:         true,
				PollInterval:    30 * time.Second,
				NodeTimeout:     tt.timeout,
				ESEndpoint:      "http://es:9200",
				ESDataStream:    "usage-traffic",
				CheckpointPath:  "/tmp/cp.json",
				MaxBufferSize:   1000,
				ShutdownTimeout: 30 * time.Second,
			}
			if err := c.Validate(); err == nil {
				t.Errorf("NodeTimeout=%v Validate() = nil, want error", tt.timeout)
			}
		})
	}
}

// MaxBufferSize <= 0 must fail.
func TestCollectorConfig_Validate_InvalidMaxBufferSize(t *testing.T) {
	c := CollectorConfig{
		Enabled:         true,
		PollInterval:    30 * time.Second,
		NodeTimeout:     10 * time.Second,
		ESEndpoint:      "http://es:9200",
		ESDataStream:    "usage-traffic",
		CheckpointPath:  "/tmp/cp.json",
		MaxBufferSize:   0,
		ShutdownTimeout: 30 * time.Second,
	}
	if err := c.Validate(); err == nil {
		t.Error("MaxBufferSize=0 Validate() = nil, want error")
	}
}

// ShutdownTimeout <= 0 must fail.
func TestCollectorConfig_Validate_InvalidShutdownTimeout(t *testing.T) {
	c := CollectorConfig{
		Enabled:         true,
		PollInterval:    30 * time.Second,
		NodeTimeout:     10 * time.Second,
		ESEndpoint:      "http://es:9200",
		ESDataStream:    "usage-traffic",
		CheckpointPath:  "/tmp/cp.json",
		MaxBufferSize:   1000,
		ShutdownTimeout: 0,
	}
	if err := c.Validate(); err == nil {
		t.Error("ShutdownTimeout=0 Validate() = nil, want error")
	}
}
