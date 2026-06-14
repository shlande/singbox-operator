package usagecollector

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// TestCheckpointSaveLoad — round-trip: save then load preserves exact values
// ---------------------------------------------------------------------------

func TestCheckpointSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := Checkpoint{
		LastSeen: map[string]int64{
			"user>>>alice#node-a>>>traffic>>>uplink":   1000,
			"user>>>alice#node-a>>>traffic>>>downlink": 800,
			"user>>>bob#node-b>>>traffic>>>uplink":     500,
		},
	}

	t.Run("save", func(t *testing.T) {
		if err := SaveCheckpoint(path, cp); err != nil {
			t.Fatalf("SaveCheckpoint failed: %v", err)
		}
		// Verify the file exists (not a tmp file).
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Fatalf("checkpoint file not found at %s after save", path)
		}
	})

	t.Run("load", func(t *testing.T) {
		loaded, err := LoadCheckpoint(path)
		if err != nil {
			t.Fatalf("LoadCheckpoint failed: %v", err)
		}
		if len(loaded.LastSeen) != len(cp.LastSeen) {
			t.Fatalf("expected %d entries, got %d", len(cp.LastSeen), len(loaded.LastSeen))
		}
		for k, want := range cp.LastSeen {
			got, ok := loaded.LastSeen[k]
			if !ok {
				t.Fatalf("missing key in loaded checkpoint: %q", k)
			}
			if got != want {
				t.Fatalf("key %q: want %d, got %d", k, want, got)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// TestCheckpointLoadMissing — loading a file that does not exist returns
// an empty Checkpoint with nil error.
// ---------------------------------------------------------------------------

func TestCheckpointLoadMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	cp, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint returned error for missing file: %v", err)
	}
	if cp.LastSeen == nil {
		t.Fatal("LastSeen map is nil, want empty non-nil map")
	}
	if len(cp.LastSeen) != 0 {
		t.Fatalf("expected empty LastSeen, got %d entries", len(cp.LastSeen))
	}
}

// ---------------------------------------------------------------------------
// TestCheckpointLoadCorrupted — loading a file with invalid JSON returns
// an empty Checkpoint with nil error (corruption fallback).
// ---------------------------------------------------------------------------

func TestCheckpointLoadCorrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupted.json")

	corruptData := []byte("this is not valid json {{{")
	if err := os.WriteFile(path, corruptData, 0644); err != nil {
		t.Fatalf("failed to write corrupted file: %v", err)
	}

	cp, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint returned error for corrupted file: %v", err)
	}
	if cp.LastSeen == nil {
		t.Fatal("LastSeen map is nil for corrupted file, want empty non-nil map")
	}
	if len(cp.LastSeen) != 0 {
		t.Fatalf("expected empty LastSeen for corrupted file, got %d entries", len(cp.LastSeen))
	}
}

// ---------------------------------------------------------------------------
// TestComputeDelta — table-driven tests covering normal, counter-reset,
// zero-delta, first-ever, and negative-bytes protection scenarios.
// ---------------------------------------------------------------------------

func TestComputeDelta(t *testing.T) {
	tests := []struct {
		name         string
		counter      string
		currentValue int64
		lastSeenInit map[string]int64 // nil means empty checkpoint
		wantDelta    int64
		wantLastSeen int64
	}{
		{
			name:         "normal-increment",
			counter:      "uplink",
			currentValue: 1000,
			lastSeenInit: map[string]int64{"uplink": 600},
			wantDelta:    400,
			wantLastSeen: 1000,
		},
		{
			name:         "counter-reset",
			counter:      "uplink",
			currentValue: 200,
			lastSeenInit: map[string]int64{"uplink": 1000},
			wantDelta:    200,
			wantLastSeen: 200,
		},
		{
			name:         "zero-delta",
			counter:      "uplink",
			currentValue: 600,
			lastSeenInit: map[string]int64{"uplink": 600},
			wantDelta:    0,
			wantLastSeen: 600,
		},
		{
			name:         "first-ever-no-lastseen",
			counter:      "uplink",
			currentValue: 500,
			lastSeenInit: nil, // empty checkpoint
			wantDelta:    500,
			wantLastSeen: 500,
		},
		{
			name:         "first-ever-for-this-counter",
			counter:      "downlink",
			currentValue: 300,
			lastSeenInit: map[string]int64{"uplink": 1000},
			wantDelta:    300,
			wantLastSeen: 300,
		},
		{
			name:         "never-negative-from-reset",
			counter:      "uplink",
			currentValue: 50,
			lastSeenInit: map[string]int64{"uplink": 5000},
			wantDelta:    50,
			wantLastSeen: 50,
		},
		{
			name:         "large-normal-increment",
			counter:      "uplink",
			currentValue: 9223372036854775807, // max int64
			lastSeenInit: map[string]int64{"uplink": 9223372036854775000},
			wantDelta:    807,
			wantLastSeen: 9223372036854775807,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := Checkpoint{LastSeen: make(map[string]int64)}
			if tt.lastSeenInit != nil {
				for k, v := range tt.lastSeenInit {
					cp.LastSeen[k] = v
				}
			}

			delta, newCP := ComputeDelta(tt.counter, tt.currentValue, cp)

			if delta != tt.wantDelta {
				t.Fatalf("delta = %d, want %d", delta, tt.wantDelta)
			}
			if delta < 0 {
				t.Fatalf("delta = %d, must never be negative", delta)
			}

			gotLastSeen, ok := newCP.LastSeen[tt.counter]
			if !ok {
				t.Fatalf("counter %q not found in new checkpoint LastSeen", tt.counter)
			}
			if gotLastSeen != tt.wantLastSeen {
				t.Fatalf("newCP.LastSeen[%q] = %d, want %d", tt.counter, gotLastSeen, tt.wantLastSeen)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestCheckpointSaveOverwrite — saving twice should overwrite the file
// ---------------------------------------------------------------------------

func TestCheckpointSaveOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp1 := Checkpoint{LastSeen: map[string]int64{"a": 1, "b": 2}}
	if err := SaveCheckpoint(path, cp1); err != nil {
		t.Fatalf("first SaveCheckpoint failed: %v", err)
	}

	cp2 := Checkpoint{LastSeen: map[string]int64{"x": 100}}
	if err := SaveCheckpoint(path, cp2); err != nil {
		t.Fatalf("second SaveCheckpoint failed: %v", err)
	}

	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint after overwrite failed: %v", err)
	}
	if len(loaded.LastSeen) != 1 {
		t.Fatalf("expected 1 entry after overwrite, got %d", len(loaded.LastSeen))
	}
	if v, ok := loaded.LastSeen["x"]; !ok || v != 100 {
		t.Fatalf("expected x=100 after overwrite, got x=%d (ok=%v)", v, ok)
	}
	if _, ok := loaded.LastSeen["a"]; ok {
		t.Fatal("key 'a' should not exist after overwrite")
	}
}
