# Learnings — singbox-traffic-collection

## [2026-06-14] Session Start

### Codebase Conventions
- Package path: `github.com/shlande/singbox-operator`
- New collector package: `internal/usagecollector/`
- Existing runnable pattern: `internal/apiserver/server.go` — implements `Start(ctx) error` + `NeedLeaderElection() bool`
- Manager wiring: `cmd/main.go` — all runnables registered via `mgr.Add()`
- Test framework: Ginkgo/Gomega for controllers, standard `testing.T` for pure logic, fake client for K8s API
- Metrics: `internal/metrics/metrics.go` — `prometheus.NewXxx` + `metrics.Registry.MustRegister` in `init()`
- Logging: `log.FromContext(ctx).Info("msg", "key", val)` — title case, no trailing period, past tense

### sing-box v2rayapi Key Facts
- User counter naming: `user>>>{name}>>>traffic>>>uplink` and `user>>>{name}>>>traffic>>>downlink`
- Counters are in-memory; reset on sing-box restart/reload
- gRPC API: `QueryStats(patterns, reset)` — use `reset=false` for at-least-once with delta tracking
- Config: `experimental.v2ray_api.listen` + `stats.users` allowlist
- Build tag required: `-tags with_v2ray_api`
- No TLS/auth by default — keep gRPC endpoint cluster-internal only

### Virtual User Naming
- sing-box users are named as `{userName}#{outboundNodeName}` (virtualUserName pattern)
- Collector must parse this to extract canonical user and node from counter names
- Base user name is the part before `#`; node name is the part after `#`

### Architecture Decisions
- Collector is a `manager.Runnable` with `NeedLeaderElection() bool { return true }` (single-active)
- No new CRDs — configuration via CLI flags
- ES is the only concrete sink; sink interface must not leak ES types
- Checkpoint stored externally (not in-memory only) — ConfigMap or mounted volume
- at-least-once: use `QueryStats(reset=false)`, track deltas, checkpoint after successful sink write
