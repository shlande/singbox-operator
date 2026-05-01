# Learnings

## [2026-05-01] Session Start

### Codebase Structure
- Package: `configengine` (internal/configengine/)
- Main file: `engine.go` (394 lines)
- Test file: `engine_test.go` (744 lines)
- Module: `github.com/shlande/singbox-operator`

### Current Architecture
- `buildRouteInbounds`: Creates one inbound per protocol per route (old model)
  - Port = basePort + routeIdx*numProtocols + protoIdx (port offset arithmetic)
  - Tag = `inbound-{proto}-{outboundNode}`
  - Rule has both `inbound` and `auth_user` fields
- `buildUsersBlock`: Creates users array for a given protocol (real users, real UUIDs)
- `buildUserInbounds`: Fallback path (no routes) - creates one inbound per protocol

### Target Architecture
- `buildRouteInbounds`: Creates ONE inbound per protocol (not per route)
  - Port = proto.Port (base port, no offset)
  - Tag = `inbound-{proto}`
  - Users = all virtual users (one per user×route combination)
  - Rules use only `auth_user` (no `inbound` field)
- `deriveUUID(baseUUID, suffix)`: UUID v5 derivation
- `virtualUserName(userName, outboundNodeName)`: Returns `{userName}#{outboundNodeName}`
- `derivePassword(basePassword, suffix)`: sha256hex(basePassword + "#" + suffix)[:32]

### Key Constraints
- stdlib only (no external deps)
- Fallback path (`buildUserInbounds`) unchanged
- `Compute`, `buildRelayInbound`, outbound builders, dedup logic unchanged
- `DeriveUUID` must be exported (capital D) for direct testing

## [2026-05-02] Task 1+2 Complete
- DeriveUUID exported, UUID v5 implementation complete
- buildRouteInbounds rewritten: single inbound per protocol, virtual users
- Tests updated: Test 4, Test 11, new Test 12
- go test ./internal/configengine/... -v: ALL PASS

## [2026-05-02] Task 3 Complete
- Committed: fix(configengine): virtual user per-route routing with derived UUIDs
- Tag v0.0.1-beta.4 pushed
- CI completed successfully (amd64+arm64 build, manifest, helm chart, github release)
- Helm upgraded to 0.0.1-beta.4 (required --reset-values due to stale image.tag user value causing ErrImagePull with v0.0.1-beta.1)
- ConfigMap verified: single inbound-vless on port 30080 with shlande#acck-jp virtual user
  - UUID: ade09766-4f2a-5204-9c6c-f1454a1e2897 (UUID v5 derived)
  - route.rules: auth_user: ["shlande#acck-jp"] → outbound-acck-jp
- NOTE: helm upgrade with --reuse-values can cause issues if old image.tag values exist; prefer --reset-values for clean upgrades

## [2026-05-01] Auto-route fix
- Added reconcileAutoRoutes to ProxyNodeReconciler
- inbound node reconcile → auto-create ProxyRoute for each same-region outbound node
- Owner = inbound ProxyNode → GC cleans up routes when inbound deleted
- Route name: {inboundNode}-to-{outboundNode}
- RBAC updated: proxyroutes now has create;update;patch;delete
