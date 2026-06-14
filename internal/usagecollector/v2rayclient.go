package usagecollector

import (
	"context"
	"strings"
	"time"

	"github.com/shlande/singbox-operator/internal/usagecollector/v2rayapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type RawStatEntry struct {
	Name  string
	Value int64
}

type StatsClient interface {
	QueryUserStats(ctx context.Context, addr string) ([]RawStatEntry, error)
}

type GRPCStatsClient struct {
	nodeTimeout  time.Duration
	PooledClient v2rayapi.StatsServiceClient
}

func NewGRPCStatsClient(nodeTimeout time.Duration) *GRPCStatsClient {
	return &GRPCStatsClient{nodeTimeout: nodeTimeout}
}

func (c *GRPCStatsClient) QueryUserStats(ctx context.Context, addr string) ([]RawStatEntry, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.nodeTimeout)
	defer cancel()

	var statsClient v2rayapi.StatsServiceClient
	var conn *grpc.ClientConn
	var cleanup func()

	if c.PooledClient != nil {
		statsClient = c.PooledClient
		cleanup = func() {}
	} else {
		dialCtx, dialCancel := context.WithTimeout(ctx, c.nodeTimeout)
		defer dialCancel()

		var dialErr error
		conn, dialErr = grpc.DialContext(dialCtx, addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		if dialErr != nil {
			return nil, dialErr
		}
		statsClient = v2rayapi.NewStatsServiceClient(conn)
		cleanup = func() { conn.Close() }
	}
	defer cleanup()

	resp, err := statsClient.QueryStats(callCtx, &v2rayapi.QueryStatsRequest{
		Patterns: []string{"user>>>"},
		Reset_:   true,
		Regexp:   false,
	})
	if err != nil {
		return nil, err
	}

	entries := make([]RawStatEntry, 0, len(resp.Stat))
	for _, s := range resp.Stat {
		entries = append(entries, RawStatEntry{
			Name:  s.Name,
			Value: s.Value,
		})
	}

	return entries, nil
}

func ParseUserCounterName(name string) (user, node, direction string, ok bool) {
	if !strings.HasPrefix(name, "user>>>") {
		return "", "", "", false
	}

	rest := name[len("user>>>"):]

	parts := strings.Split(rest, ">>>")
	if len(parts) != 3 {
		return "", "", "", false
	}

	virtualUserName := parts[0]
	if virtualUserName == "" {
		return "", "", "", false
	}

	if parts[1] != "traffic" {
		return "", "", "", false
	}

	direction = parts[2]
	if direction != "uplink" && direction != "downlink" {
		return "", "", "", false
	}

	hashIdx := strings.Index(virtualUserName, "#")
	if hashIdx < 0 {
		user = virtualUserName
		node = ""
	} else {
		user = virtualUserName[:hashIdx]
		node = virtualUserName[hashIdx+1:]
	}

	return user, node, direction, true
}
