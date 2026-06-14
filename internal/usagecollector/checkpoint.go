package usagecollector

import (
	"encoding/json"
	"errors"
	"os"
)

// Checkpoint persists the last-seen cumulative counter values across
// collector restarts. Each key is a sing-box counter name (e.g.
// "user>>>alice#node-a>>>traffic>>>uplink"), and the value is the
// cumulative byte count at the time of the most recent successful
// sink write.
type Checkpoint struct {
	// LastSeen maps counter names to the most recently observed
	// cumulative counter value. Always non-nil after LoadCheckpoint
	// or ComputeDelta.
	LastSeen map[string]int64 `json:"last_seen"`
}

// SaveCheckpoint writes the checkpoint to the given path atomically.
// The data is written to a temporary file (path + ".tmp") first,
// then renamed to path. POSIX rename is atomic on the same filesystem,
// preventing partial-write corruption.
func SaveCheckpoint(path string, cp Checkpoint) error {
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// LoadCheckpoint reads the checkpoint from the given path.
//
//   - On success, returns the deserialized Checkpoint.
//   - If the file does not exist, returns an empty Checkpoint with
//     LastSeen initialized to a non-nil map (nil error).
//   - If the file exists but contains invalid JSON, returns an empty
//     Checkpoint with LastSeen initialized to a non-nil map (nil error).
//     This ensures the collector can recover from corruption without
//     panicking.
func LoadCheckpoint(path string) (Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyCheckpoint(), nil
		}
		return emptyCheckpoint(), nil
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return emptyCheckpoint(), nil
	}

	if cp.LastSeen == nil {
		cp.LastSeen = make(map[string]int64)
	}

	return cp, nil
}

// emptyCheckpoint returns a Checkpoint with an initialized (non-nil)
// but empty LastSeen map, safe to use immediately.
func emptyCheckpoint() Checkpoint {
	return Checkpoint{LastSeen: make(map[string]int64)}
}

// ComputeDelta calculates the delta between the current cumulative
// counter value and the last seen value in the checkpoint.
//
// Counter-reset detection:
//   - If currentValue >= cp.LastSeen[counterName], it is a normal
//     increment: delta = currentValue - lastSeen.
//   - If currentValue < cp.LastSeen[counterName], sing-box restarted
//     and the counter was reset to zero. The delta is the entire
//     current value (delta = currentValue), not a negative number.
//   - If counterName is not in LastSeen, this is the first observation:
//     delta = currentValue.
//
// The returned delta is always non-negative (>= 0).
//
// The returned Checkpoint contains the updated LastSeen with
// counterName → currentValue. Callers should persist this
// checkpoint after a successful sink write.
func ComputeDelta(counterName string, currentValue int64, cp Checkpoint) (delta int64, newCheckpoint Checkpoint) {
	lastSeen, exists := cp.LastSeen[counterName]

	if !exists {
		// First observation for this counter.
		delta = currentValue
	} else if currentValue >= lastSeen {
		// Normal case: counter accumulated.
		delta = currentValue - lastSeen
	} else {
		// Counter reset: sing-box restarted, counter wrapped to zero.
		// The entire current value counts as new usage.
		delta = currentValue
	}

	// Build the updated checkpoint. Share the existing map and add/update
	// the counter entry. This is safe because ComputeDelta is not called
	// concurrently for the same checkpoint (collector is single-threaded).
	if cp.LastSeen == nil {
		cp.LastSeen = make(map[string]int64)
	}
	cp.LastSeen[counterName] = currentValue

	return delta, cp
}
