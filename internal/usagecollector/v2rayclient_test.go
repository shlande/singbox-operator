package usagecollector

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/shlande/singbox-operator/internal/usagecollector/v2rayapi"
)

// ---------------------------------------------------------------------------
// TestParseUserCounterName — table-driven tests for counter name parsing
// ---------------------------------------------------------------------------

func TestParseUserCounterName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantUser string
		wantNode string
		wantDir  string
		wantOK   bool
	}{
		{
			name:     "valid-user-counter-with-virtual-user-has-hash",
			input:    "user>>>alice#node-a>>>traffic>>>uplink",
			wantUser: "alice",
			wantNode: "node-a",
			wantDir:  "uplink",
			wantOK:   true,
		},
		{
			name:     "valid-user-counter-without-hash-single-node",
			input:    "user>>>alice>>>traffic>>>downlink",
			wantUser: "alice",
			wantNode: "",
			wantDir:  "downlink",
			wantOK:   true,
		},
		{
			name:     "valid-user-counter-downlink",
			input:    "user>>>bob#node-b>>>traffic>>>downlink",
			wantUser: "bob",
			wantNode: "node-b",
			wantDir:  "downlink",
			wantOK:   true,
		},
		{
			name:     "non-user-counter-inbound",
			input:    "inbound>>>some-tag>>>traffic>>>uplink",
			wantUser: "",
			wantNode: "",
			wantDir:  "",
			wantOK:   false,
		},
		{
			name:     "non-user-counter-outbound",
			input:    "outbound>>>proxy>>>traffic>>>downlink",
			wantUser: "",
			wantNode: "",
			wantDir:  "",
			wantOK:   false,
		},
		{
			name:     "malformed-too-few-parts",
			input:    "user>>>alice>>>traffic",
			wantUser: "",
			wantNode: "",
			wantDir:  "",
			wantOK:   false,
		},
		{
			name:     "malformed-wrong-direction",
			input:    "user>>>alice#node-a>>>traffic>>>upload",
			wantUser: "",
			wantNode: "",
			wantDir:  "",
			wantOK:   false,
		},
		{
			name:     "malformed-empty-user-name",
			input:    "user>>>>>>traffic>>>uplink",
			wantUser: "",
			wantNode: "",
			wantDir:  "",
			wantOK:   false,
		},
		{
			name:     "user-with-hash-empty-node",
			input:    "user>>>alice#>>>traffic>>>uplink",
			wantUser: "alice",
			wantNode: "",
			wantDir:  "uplink",
			wantOK:   true,
		},
		{
			name:     "user-with-hash-only-hash-empty-user",
			input:    "user>>>#node-a>>>traffic>>>uplink",
			wantUser: "",
			wantNode: "node-a",
			wantDir:  "uplink",
			wantOK:   true,
		},
		{
			name:     "user-name-contains-hash-in-value",
			input:    "user>>>alice#bob#node-a>>>traffic>>>uplink",
			wantUser: "alice",
			wantNode: "bob#node-a",
			wantDir:  "uplink",
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUser, gotNode, gotDir, gotOK := ParseUserCounterName(tt.input)
			if gotOK != tt.wantOK {
				t.Fatalf("ParseUserCounterName(%q) ok = %v, want %v", tt.input, gotOK, tt.wantOK)
			}
			if gotUser != tt.wantUser {
				t.Fatalf("ParseUserCounterName(%q) user = %q, want %q", tt.input, gotUser, tt.wantUser)
			}
			if gotNode != tt.wantNode {
				t.Fatalf("ParseUserCounterName(%q) node = %q, want %q", tt.input, gotNode, tt.wantNode)
			}
			if gotDir != tt.wantDir {
				t.Fatalf("ParseUserCounterName(%q) direction = %q, want %q", tt.input, gotDir, tt.wantDir)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// fakeStatsServer implements v2rayapi.StatsServiceServer for testing.
// ---------------------------------------------------------------------------

type fakeStatsServer struct {
	v2rayapi.UnimplementedStatsServiceServer
	mu       sync.Mutex
	counters map[string]int64                        // counter name → cumulative value
	delay    time.Duration                           // injected delay for timeout tests
	queryErr error                                   // injected error for error tests
	validate func(*v2rayapi.QueryStatsRequest) error // optional validator
}

func newFakeStatsServer() *fakeStatsServer {
	return &fakeStatsServer{
		counters: make(map[string]int64),
	}
}

func (f *fakeStatsServer) QueryStats(ctx context.Context, req *v2rayapi.QueryStatsRequest) (*v2rayapi.QueryStatsResponse, error) {
	if f.validate != nil {
		if err := f.validate(req); err != nil {
			return nil, err
		}
	}

	// Simulate a slow/hung server for timeout tests.
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if f.queryErr != nil {
		return nil, f.queryErr
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	var stats []*v2rayapi.Stat
	if len(req.Patterns) == 0 {
		// Return all counters.
		for name, val := range f.counters {
			stats = append(stats, &v2rayapi.Stat{Name: name, Value: val})
		}
	} else {
		for _, pattern := range req.Patterns {
			for name, val := range f.counters {
				if strings.Contains(name, pattern) {
					stats = append(stats, &v2rayapi.Stat{Name: name, Value: val})
				}
			}
		}
	}
	return &v2rayapi.QueryStatsResponse{Stat: stats}, nil
}

func (f *fakeStatsServer) GetStats(ctx context.Context, req *v2rayapi.GetStatsRequest) (*v2rayapi.GetStatsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	val, ok := f.counters[req.Name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "%s not found", req.Name)
	}
	return &v2rayapi.GetStatsResponse{Stat: &v2rayapi.Stat{Name: req.Name, Value: val}}, nil
}

func (f *fakeStatsServer) GetSysStats(ctx context.Context, req *v2rayapi.SysStatsRequest) (*v2rayapi.SysStatsResponse, error) {
	return &v2rayapi.SysStatsResponse{Uptime: 42}, nil
}

// ---------------------------------------------------------------------------
// fake gRPC server helper — starts a buffered in-memory gRPC server and
// returns a client connection. Dial returns a StatsClient for the test.
// ---------------------------------------------------------------------------

func startFakeV2RayServer(t *testing.T, srv v2rayapi.StatsServiceServer) (v2rayapi.StatsServiceClient, func()) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	s := grpc.NewServer()
	v2rayapi.RegisterStatsServiceServer(s, srv)
	go func() {
		if err := s.Serve(lis); err != nil {
			// Server stopped normally.
		}
	}()

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to create gRPC client: %v", err)
	}

	client := v2rayapi.NewStatsServiceClient(conn)
	cleanup := func() {
		conn.Close()
		s.Stop()
	}
	return client, cleanup
}

// ---------------------------------------------------------------------------
// TestQueryUserStats — tests the real GRPCStatsClient against a fake
// gRPC server.
// ---------------------------------------------------------------------------

func TestQueryUserStats(t *testing.T) {
	t.Run("returns-correct-raw-stat-entries", func(t *testing.T) {
		fake := newFakeStatsServer()
		fake.counters = map[string]int64{
			"user>>>alice#node-a>>>traffic>>>uplink":   1000,
			"user>>>alice#node-a>>>traffic>>>downlink": 800,
			"user>>>bob#node-b>>>traffic>>>uplink":     500,
			"user>>>bob#node-b>>>traffic>>>downlink":   400,
			"inbound>>>proxy>>>traffic>>>uplink":       9999, // non-user, should not be returned
		}

		grpcClient, cleanup := startFakeV2RayServer(t, fake)
		defer cleanup()

		client := NewGRPCStatsClient(5 * time.Second)
		client.PooledClient = grpcClient
		ctx := context.Background()

		entries, err := client.QueryUserStats(ctx, "bufnet")
		if err != nil {
			t.Fatalf("QueryUserStats failed: %v", err)
		}

		if len(entries) != 4 {
			t.Fatalf("expected 4 user stat entries, got %d: %+v", len(entries), entries)
		}

		// Verify specific entries.
		for _, e := range entries {
			if e.Name == "user>>>alice#node-a>>>traffic>>>uplink" && e.Value != 1000 {
				t.Fatalf("expected alice uplink = 1000, got %d", e.Value)
			}
			if e.Name == "user>>>alice#node-a>>>traffic>>>downlink" && e.Value != 800 {
				t.Fatalf("expected alice downlink = 800, got %d", e.Value)
			}
			if e.Name == "user>>>bob#node-b>>>traffic>>>uplink" && e.Value != 500 {
				t.Fatalf("expected bob uplink = 500, got %d", e.Value)
			}
			if e.Name == "user>>>bob#node-b>>>traffic>>>downlink" && e.Value != 400 {
				t.Fatalf("expected bob downlink = 400, got %d", e.Value)
			}
			// Ensure non-user counters are not present.
			if e.Name == "inbound>>>proxy>>>traffic>>>uplink" {
				t.Fatal("non-user counter 'inbound>>>proxy>>>traffic>>>uplink' leaked into results")
			}
		}
	})

	t.Run("empty-server-returns-empty-slice", func(t *testing.T) {
		fake := newFakeStatsServer()
		// No counters set.

		grpcClient, cleanup := startFakeV2RayServer(t, fake)
		defer cleanup()

		client := NewGRPCStatsClient(5 * time.Second)
		client.PooledClient = grpcClient
		ctx := context.Background()

		entries, err := client.QueryUserStats(ctx, "bufnet")
		if err != nil {
			t.Fatalf("QueryUserStats failed: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("expected 0 entries from empty server, got %d", len(entries))
		}
	})

	t.Run("per-node-timeout-hung-server", func(t *testing.T) {
		fake := newFakeStatsServer()
		fake.delay = 5 * time.Second // server takes 5s to respond

		grpcClient, cleanup := startFakeV2RayServer(t, fake)
		defer cleanup()

		client := NewGRPCStatsClient(200 * time.Millisecond) // only wait 200ms
		client.PooledClient = grpcClient
		ctx := context.Background()

		before := time.Now()
		_, err := client.QueryUserStats(ctx, "bufnet")
		elapsed := time.Since(before)

		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}

		if elapsed > 500*time.Millisecond {
			t.Fatalf("timeout took too long: %v (expected < 500ms with 200ms timeout)", elapsed)
		}
	})

	t.Run("uses-user-pattern-in-query", func(t *testing.T) {
		fake := newFakeStatsServer()
		fake.counters = map[string]int64{
			"user>>>alice#node-a>>>traffic>>>uplink": 100,
			"inbound>>>proxy>>>traffic>>>uplink":     999,
		}

		var capturedPatterns []string
		fake.validate = func(req *v2rayapi.QueryStatsRequest) error {
			capturedPatterns = append(capturedPatterns, req.Patterns...)
			return nil
		}

		grpcClient, cleanup := startFakeV2RayServer(t, fake)
		defer cleanup()

		client := NewGRPCStatsClient(5 * time.Second)
		client.PooledClient = grpcClient
		ctx := context.Background()

		_, err := client.QueryUserStats(ctx, "bufnet")
		if err != nil {
			t.Fatalf("QueryUserStats failed: %v", err)
		}

		// Must use "user>>>" as pattern to filter on the server side.
		found := slices.Contains(capturedPatterns, "user>>>")
		if !found {
			t.Fatalf("expected QueryStats to use 'user>>>' pattern, got patterns: %v", capturedPatterns)
		}
	})
}

// ---------------------------------------------------------------------------
// TestV2RayAPI_StatsClient_Interface — verify StatsClient interface contract
// ---------------------------------------------------------------------------

func TestV2RayAPI_StatsClient_Interface(t *testing.T) {
	t.Run("GRPCStatsClient-satisfies-StatsClient", func(t *testing.T) {
		// Compile-time check.
		var _ StatsClient = (*GRPCStatsClient)(nil)
	})
}

// ---------------------------------------------------------------------------
// UnimplementedStatsServiceServer — minimal embeddable for fake servers
// (avoids referencing the large protobuf runtime types).
// ---------------------------------------------------------------------------

var _ v2rayapi.StatsServiceServer = (*fakeStatsServer)(nil)
