package usagecollector

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
)

// Collector orchestrates the usage data collection pipeline:
//
//	discovery → v2ray API polling → delta computation → normalization → checkpoint → sink write.
//
// It runs an infinite poll loop driven by a ticker and stops on context
// cancellation.  The loop is anti-reentrant: only one poll cycle runs at
// a time; if a new tick arrives while the previous cycle is still in-flight
// the tick is skipped.
type Collector struct {
	cfg         CollectorConfig
	discoverer  Discoverer
	statsClient StatsClient
	sink        UsageSink

	// pollMu ensures only one poll cycle runs at a time.
	pollMu sync.Mutex

	// inPoll is set to 1 while a poll cycle is in progress; read atomically
	// for fast anti-reentrance checks without acquiring the mutex.
	inPoll atomic.Bool

	// checkpoint holds the in-memory counter-state. Updated by
	// ComputeDelta during each poll cycle, and persisted only after a
	// successful sink write.
	checkpoint   Checkpoint
	checkpointMu sync.Mutex

	// buffered collects UsageRecords during a poll cycle. Flushed to the
	// sink after all targets have been polled, then cleared.
	buffered   UsageBatch
	bufferedMu sync.Mutex

	log logr.Logger
}

// NewCollector creates a Collector with the given dependencies.
// All four parameters are required and must be non-nil.
func NewCollector(cfg CollectorConfig, discoverer Discoverer, statsClient StatsClient, sink UsageSink) *Collector {
	if discoverer == nil {
		panic("usagecollector: discoverer must not be nil")
	}
	if statsClient == nil {
		panic("usagecollector: statsClient must not be nil")
	}
	if sink == nil {
		panic("usagecollector: sink must not be nil")
	}

	return &Collector{
		cfg:         cfg,
		discoverer:  discoverer,
		statsClient: statsClient,
		sink:        sink,
		log:         logr.Discard(),
	}
}

// Run starts the collection loop and blocks until ctx is cancelled.
// It never returns an error for clean context cancellation; errors
// during individual poll cycles are logged but do not stop the loop.
// A non-nil error means an unrecoverable failure (e.g. checkpoint load
// failure on startup, or final-flush failure during shutdown that
// loses data).
func (c *Collector) Run(ctx context.Context) error {
	// 1. Load checkpoint from disk (best-effort).
	cp, err := LoadCheckpoint(c.cfg.CheckpointPath)
	if err != nil {
		return fmt.Errorf("loading checkpoint: %w", err)
	}
	c.checkpointMu.Lock()
	c.checkpoint = cp
	c.checkpointMu.Unlock()

	c.log.Info("Loaded checkpoint", "entries", len(cp.LastSeen))

	// 2. Start ticker.
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()

	c.log.Info("Starting collection loop",
		"interval", c.cfg.PollInterval,
		"maxBufferSize", c.cfg.MaxBufferSize)

	// 3. Main loop.
	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown: attempt one final flush of buffered records.
			c.log.Info("Context cancelled, starting graceful shutdown")

			// Acquire pollMu to ensure no concurrent poll is running.
			c.pollMu.Lock()
			c.flushBuffer(ctx)
			c.pollMu.Unlock()

			return nil

		case <-ticker.C:
			c.pollCycle(ctx)
		}
	}
}

// pollCycle executes a single discovery → query → compute → flush cycle.
// It is anti-reentrant: if another cycle is already running, this call
// returns immediately.
func (c *Collector) pollCycle(ctx context.Context) {
	// Fast-path anti-reentrance: if already in poll, skip.
	if c.inPoll.Load() {
		c.log.V(1).Info("Skipping poll cycle, previous cycle still running")
		return
	}

	c.pollMu.Lock()
	defer c.pollMu.Unlock()

	c.inPoll.Store(true)
	defer c.inPoll.Store(false)

	c.log.V(2).Info("Starting poll cycle")

	// -- Phase 1: Discover targets --
	targets, err := c.discoverer.Discover(ctx)
	if err != nil {
		c.log.Error(err, "Discovery failed, skipping cycle")
		return
	}

	if len(targets) == 0 {
		c.log.V(2).Info("No targets discovered")
		return
	}

	c.log.V(2).Info("Discovered targets", "count", len(targets))

	// -- Phase 2: Poll each target --
	collectedAt := time.Now()

	// Snapshot current checkpoint for this cycle.
	c.checkpointMu.Lock()
	cp := c.checkpoint
	c.checkpointMu.Unlock()

	var newRecords UsageBatch
	checkpointDirty := false

	for _, tgt := range targets {
		entries, err := c.statsClient.QueryUserStats(ctx, tgt.V2RayAPIAddr)
		if err != nil {
			c.log.Error(err, "Failed to query stats, skipping node",
				"node", tgt.NodeName,
				"addr", tgt.V2RayAPIAddr)
			continue
		}

		for _, entry := range entries {
			// Filter to user counters only.
			_, _, _, ok := ParseUserCounterName(entry.Name)
			if !ok {
				continue
			}

			// Compute delta against checkpoint.
			delta, updatedCP := ComputeDelta(entry.Name, entry.Value, cp)
			cp = updatedCP
			checkpointDirty = true

			if delta == 0 {
				continue
			}

			// Normalize to UsageRecord.
			record, ok := NormalizeCounterToRecord(entry.Name, delta, collectedAt)
			if !ok {
				continue
			}

			newRecords = append(newRecords, record)
		}
	}

	c.log.V(2).Info("Poll cycle produced records", "count", len(newRecords))

	// -- Phase 3: Buffer and flush --
	if len(newRecords) > 0 {
		c.bufferedMu.Lock()
		c.buffered = append(c.buffered, newRecords...)
		bufLen := len(c.buffered)
		c.bufferedMu.Unlock()

		// Backpressure: if buffer exceeds MaxBufferSize, drop oldest records.
		if bufLen > c.cfg.MaxBufferSize {
			c.bufferedMu.Lock()
			overflow := bufLen - c.cfg.MaxBufferSize
			c.log.Info("Buffer overflow, dropping oldest records",
				"dropped", overflow,
				"bufferBefore", bufLen,
				"maxBufferSize", c.cfg.MaxBufferSize)
			c.buffered = c.buffered[overflow:]
			c.bufferedMu.Unlock()
		}

		// Flush if buffer is non-empty. We flush after every cycle for simplicity
		// and to keep checkpoint write granularity aligned with sink writes.
		c.flushBuffer(ctx)
	}

	// -- Phase 4: Save checkpoint (only if sink write succeeded) --
	if checkpointDirty {
		c.checkpointMu.Lock()
		c.checkpoint = cp
		c.checkpointMu.Unlock()

		// Only persist if we flushed successfully. If flush failed, we don't
		// update the on-disk checkpoint — at-least-once semantics.
		c.bufferedMu.Lock()
		bufferedLen := len(c.buffered)
		c.bufferedMu.Unlock()

		if bufferedLen == 0 {
			// Buffer was flushed successfully (or empty from the start).
			if err := SaveCheckpoint(c.cfg.CheckpointPath, cp); err != nil {
				c.log.Error(err, "Failed to save checkpoint")
			}
		}
	}
}

// flushBuffer writes the current buffered records to the sink.
// It MUST be called while holding pollMu. On success the buffer is cleared
// and the on-disk checkpoint is persisted. On failure records stay buffered
// for the next cycle (at-least-once retry).
func (c *Collector) flushBuffer(ctx context.Context) {
	c.bufferedMu.Lock()
	batch := c.buffered
	c.bufferedMu.Unlock()

	if len(batch) == 0 {
		return
	}

	c.log.V(2).Info("Flushing records to sink", "count", len(batch))

	// Use a limited context for the flush so a stuck sink doesn't block
	// shutdown forever.
	flushCtx, cancel := context.WithTimeout(ctx, c.cfg.ShutdownTimeout)
	defer cancel()

	if err := c.sink.Write(flushCtx, batch); err != nil {
		c.log.Error(err, "Sink write failed, records will be retried next cycle",
			"count", len(batch))
		return
	}

	// Clear buffer on success.
	c.bufferedMu.Lock()
	c.buffered = c.buffered[:0]
	c.bufferedMu.Unlock()

	// Persist checkpoint now that write succeeded.
	c.checkpointMu.Lock()
	cp := c.checkpoint
	c.checkpointMu.Unlock()

	if err := SaveCheckpoint(c.cfg.CheckpointPath, cp); err != nil {
		c.log.Error(err, "Failed to save checkpoint after successful sink write")
	}
}
