package usagecollector

import "context"

// CollectorRunnable wraps a *Collector as a manager.Runnable that only
// runs on the leader replica (NeedLeaderElection returns true).
type CollectorRunnable struct {
	Collector *Collector
}

// compile-time check: CollectorRunnable implements manager.Runnable
var _ Runnable = (*CollectorRunnable)(nil)

// Runnable is the interface a manager.Runnable must implement.
// We define it locally to avoid importing controller-runtime in tests.
type Runnable interface {
	Start(ctx context.Context) error
	NeedLeaderElection() bool
}

// Start implements manager.Runnable. It delegates to Collector.Run and
// blocks until ctx is cancelled or Run returns an error.
func (r *CollectorRunnable) Start(ctx context.Context) error {
	return r.Collector.Run(ctx)
}

// NeedLeaderElection returns true so the collector runs only on the
// elected leader replica. This ensures single-active collection.
func (r *CollectorRunnable) NeedLeaderElection() bool { return true }
