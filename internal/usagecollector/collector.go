package usagecollector

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Collector struct {
	cfg         CollectorConfig
	discoverer  Discoverer
	statsClient StatsClient
	sink        UsageSink

	pollMu sync.Mutex
	inPoll atomic.Bool

	log logr.Logger
}

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

func (c *Collector) Run(ctx context.Context) error {
	c.log = log.FromContext(ctx)

	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()

	c.log.Info("Starting collection loop",
		"interval", c.cfg.PollInterval)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.pollCycle(ctx)
		}
	}
}

func (c *Collector) pollCycle(ctx context.Context) {
	if c.inPoll.Load() {
		return
	}

	c.pollMu.Lock()
	defer c.pollMu.Unlock()

	c.inPoll.Store(true)
	defer c.inPoll.Store(false)

	c.log.Info("Starting poll cycle")

	targets, err := c.discoverer.Discover(ctx)
	if err != nil {
		c.log.Error(err, "Discovery failed, skipping cycle")
		return
	}
	if len(targets) == 0 {
		c.log.Info("No targets discovered, skipping cycle")
		return
	}
	c.log.Info("Discovered targets", "count", len(targets))

	collectedAt := time.Now()

	// recordIndex merges uplink and downlink counters for the same
	// (inbound, user, node) into a single UsageRecord per poll cycle.
	recordIndex := make(map[string]*UsageRecord)

	for _, tgt := range targets {
		c.log.Info("Querying node stats", "node", tgt.NodeName, "addr", tgt.V2RayAPIAddr)
		entries, err := c.statsClient.QueryUserStats(ctx, tgt.V2RayAPIAddr)
		if err != nil {
			c.log.Error(err, "Failed to query stats, skipping node",
				"node", tgt.NodeName, "addr", tgt.V2RayAPIAddr)
			continue
		}
		c.log.Info("Got stats entries from node", "node", tgt.NodeName, "entries", len(entries))

		for _, entry := range entries {
			user, node, direction, ok := ParseUserCounterName(entry.Name)
			if !ok {
				continue
			}
			if entry.Value == 0 {
				continue
			}

			c.log.Info("Counter value", "inbound", tgt.NodeName, "counter", entry.Name,
				"user", user, "node", node, "direction", direction, "value", entry.Value)

			key := tgt.NodeName + "\x00" + user + "\x00" + node
			rec, exists := recordIndex[key]
			if !exists {
				recordIndex[key] = &UsageRecord{
					Timestamp:   collectedAt,
					User:        user,
					InboundNode: tgt.NodeName,
					Node:        node,
					CollectedAt: collectedAt,
				}
				rec = recordIndex[key]
			}
			if direction == "uplink" {
				rec.UplinkBytes += entry.Value
			} else {
				rec.DownlinkBytes += entry.Value
			}
		}
	}

	var batch UsageBatch
	for _, rec := range recordIndex {
		batch = append(batch, *rec)
	}

	if len(batch) == 0 {
		c.log.Info("Poll cycle produced no records")
		return
	}

	c.log.Info("Poll cycle produced records", "count", len(batch))

	writeCtx, cancel := context.WithTimeout(ctx, c.cfg.ShutdownTimeout)
	defer cancel()

	if err := c.sink.Write(writeCtx, batch); err != nil {
		c.log.Error(err, "Sink write failed", "count", len(batch))
		return
	}
	c.log.Info("Sink write succeeded", "count", len(batch))
}
