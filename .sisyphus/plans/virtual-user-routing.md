# Virtual User Per-Route Routing for sing-box inbound config

## TL;DR

> **Quick Summary**: Replace per-route-per-port inbound model with a single inbound per protocol containing virtual users (`shlande#acck-jp`, `shlande#xtom-jp`), each with a deterministically derived UUID, routed via `auth_user` rules to their corresponding outbound.
>
> **Deliverables**:
> - `internal/configengine/engine.go`: new `deriveUUID` helper + rewritten `buildRouteInbounds` + updated `buildUsersBlock`
> - `internal/configengine/engine_test.go`: updated tests for new virtual-user model
> - Git tag `v0.0.1-beta.4` pushed → CI builds image → cluster upgraded

---

## Context

### Original Request
User wants a single inbound port to support one user selecting different exit nodes. The mechanism: create virtual users `shlande#acck-jp` / `shlande#xtom-jp` inside the inbound `users` array. Each virtual user has a deterministically derived UUID. The client connects with the UUID for the desired exit.

### Current Broken Model
```
inbound-vless-acck-jp  port=30080  users=[{name:"shlande", uuid:"original"}]
inbound-vless-xtom-jp  port=30082  users=[{name:"shlande", uuid:"original"}]
route.rules: [{inbound:[inbound-vless-acck-jp], auth_user:[shlande]} → outbound-acck-jp]
```
Problem: two separate ports, same UUID on both — user cannot distinguish which exit to use.

### Correct Target Model
```
inbound-vless  port=30080  users=[
  {name:"shlande#acck-jp", uuid: deriveUUID("original-uuid", "acck-jp")},
  {name:"shlande#xtom-jp", uuid: deriveUUID("original-uuid", "xtom-jp")}
]
route.rules:
  [{auth_user:["shlande#acck-jp"]} → outbound-acck-jp]
  [{auth_user:["shlande#xtom-jp"]} → outbound-xtom-jp]
route.final: "direct"
```

Single port, multiple virtual users, each UUID deterministically derived → client picks UUID to select exit.

### UUID Derivation Algorithm
UUID v5 (SHA-1 namespace): `deriveUUID(baseUUID, outboundNodeName)`
- Parse baseUUID as 16-byte namespace
- SHA-1(namespace || name)
- Format result as UUID v5 (version bits = 0x50, variant = 0x80)
- Pure stdlib: `crypto/sha1` + `encoding/binary` — no external deps

This is deterministic: same (userUUID, outboundName) always produces same derived UUID. Operators can pre-compute client configs.

---

## Work Objectives

### Core Objective
Rewrite inbound config generation so a single inbound per protocol contains all virtual users (one per user×route combination), each with a derived UUID, with `auth_user` routing rules mapping each virtual user to its outbound.

### Concrete Deliverables
- `internal/configengine/engine.go`: `deriveUUID` + `virtualUserName` + rewritten `buildRouteInbounds` + updated `buildUsersBlock` (no longer called from route path) + `Compute` simplified (no per-route port offset)
- `internal/configengine/engine_test.go`: updated Test 4, Test 11; new Test 12 verifying UUID derivation determinism

### Definition of Done
- [ ] `make test` passes with 0 failures
- [ ] claw-jp-2 ConfigMap shows single inbound on port 30080 with 2 virtual users
- [ ] route.rules has 2 entries (one per route), each with `auth_user` = virtual user name

### Must Have
- Single inbound per protocol (port unchanged = `SupportedProtocols[proto].Port`)
- Virtual user name format: `{userName}#{outboundNodeName}`
- Derived UUID: UUID v5 of (baseUUID-as-namespace, outboundNodeName)
- Trojan: derived password = `sha256hex(basePassword + "#" + outboundNodeName)[:32]`
- `auth_user` routing rules (no `inbound` field needed since single inbound)
- Fallback (no Routes): single inbound with real users and real UUIDs (unchanged)

### Must NOT Have
- Multiple inbound ports per protocol
- Port offset arithmetic (removed entirely from route path)
- `inbound` field in routing rules (redundant when single inbound)
- External dependencies (use stdlib only)

---

## Verification Strategy

### Test Decision
- **Infrastructure exists**: YES (Go test + envtest)
- **Automated tests**: Tests-after (update existing + add new)
- **Framework**: `go test`

### QA Scenarios

```
Scenario: Single inbound with 2 virtual users (user shlande, routes to acck-jp + xtom-jp)
  Tool: Bash (go test -v -run TestConfigEngine_MultiRouteInbounds)
  Steps:
    1. Run test
    2. Assert inbound count == 1 (tag="inbound-vless")
    3. Assert users array has 2 entries: "shlande#acck-jp" and "shlande#xtom-jp"
    4. Assert UUIDs differ between the two virtual users
    5. Assert route.rules has 2 entries with correct auth_user and outbound
  Expected Result: PASS
  Evidence: test output

Scenario: UUID derivation is deterministic
  Tool: Bash (go test -v -run TestDeriveUUID)
  Steps:
    1. Call deriveUUID("f0a5a0d6-951a-4936-a7e7-93a8f86f2fb8", "acck-jp") twice
    2. Assert both calls return identical UUID
    3. Call deriveUUID(..., "xtom-jp") and assert it differs from acck-jp result
  Expected Result: PASS

Scenario: Cluster config verification
  Tool: Bash (kubectl)
  Steps:
    1. kubectl annotate proxynode claw-jp-2 ... --overwrite (trigger reconcile)
    2. kubectl get configmap claw-jp-2-config -o jsonpath='{.data.config\.json}' | python3 -m json.tool
    3. Assert inbounds array length == 1
    4. Assert inbounds[0].users length == 1 (only acck-jp route exists; xtom-jp has no ProxyRoute)
    5. Assert route.rules[0].auth_user == ["shlande#acck-jp"]
    6. Assert route.rules[0].outbound == "outbound-acck-jp"
  Expected Result: Correct config
  Evidence: kubectl output
```

---

## Execution Strategy

Sequential (single file change + test + deploy).

---

## TODOs

- [x] 1. Rewrite `internal/configengine/engine.go`

  **What to do**:

  1. Add `deriveUUID(baseUUID, suffix string) string` function:
     - Parse baseUUID hex into 16-byte slice (strip dashes)
     - Compute `sha1.Sum(append(namespaceBytes, []byte(suffix)...))`
     - Set version bits: `hash[6] = (hash[6] & 0x0f) | 0x50`
     - Set variant bits: `hash[8] = (hash[8] & 0x3f) | 0x80`
     - Format as UUID string using `encoding/binary` or fmt.Sprintf
     - Import: `crypto/sha1` + `encoding/hex` (stdlib only)

  2. Add `virtualUserName(userName, outboundNodeName string) string`:
     - Returns `fmt.Sprintf("%s#%s", userName, outboundNodeName)`

  3. Rewrite `buildRouteInbounds(input Input, routes []*v1alpha1.ProxyRoute) ([]interface{}, []routeRule)`:
     - Group routes by protocol (one inbound per protocol, not per route)
     - For each protocol in `input.Node.Spec.SupportedProtocols`:
       - tag = `fmt.Sprintf("inbound-%s", proto.Protocol)`
       - port = `proto.Port` (base port, no offset)
       - users = for each route × each user of that protocol:
         - `name = virtualUserName(user.Name, route.Spec.OutboundNode)`
         - `uuid = deriveUUID(userCred.UUID, route.Spec.OutboundNode)` (for vless)
         - `password = derivePassword(userCred.Password, route.Spec.OutboundNode)` (for trojan)
       - Build one inbound entry with ALL virtual users
     - For each route, build one routeRule:
       - `auth_user = [virtualUserName(user.Name, route.Spec.OutboundNode) for each user of matching protocol]`
       - `outbound = "outbound-{route.Spec.OutboundNode}"`
       - No `inbound` field (omitempty handles it)

  4. Add `derivePassword(basePassword, suffix string) string`:
     - `sha256hex(basePassword + "#" + suffix)[:32]`

  5. Remove port offset arithmetic entirely from route path.

  6. Add import `crypto/sha1` and `encoding/hex`.

  **Must NOT do**:
  - Do not add external dependencies
  - Do not change the fallback path (`buildUserInbounds`)
  - Do not change `Compute`, `buildRelayInbound`, outbound builders, or dedup logic

  **References**:
  - Current `buildRouteInbounds`: `engine.go:187-219`
  - Current `buildUsersBlock`: `engine.go:221-254`
  - UUID v5 spec: SHA-1 of (namespace bytes || name bytes), version=5 (0x50), variant=0x80

  **Acceptance Criteria**:
  - [ ] `go build ./internal/configengine/...` succeeds
  - [ ] `deriveUUID("f0a5a0d6-951a-4936-a7e7-93a8f86f2fb8", "acck-jp")` returns a valid UUID v5 string
  - [ ] Two calls with same args return identical result

  **Commit**: YES (groups with Task 2)

- [x] 2. Update `internal/configengine/engine_test.go`

  **What to do**:

  1. Update `TestConfigEngine_ManualRoute` (Test 4):
     - Inbound tag = `"inbound-vless"` (not `"inbound-vless-node-b"`)
     - `users` array has 1 entry: `{name: "user-dave#node-b", uuid: <derived>}`
     - `route.rules[0].auth_user = ["user-dave#node-b"]`
     - No `inbound` field in rule (omitempty)

  2. Update `TestConfigEngine_MultiRouteInbounds` (Test 11):
     - Inbound count = 1 (not 2)
     - `users` array has 2 entries: `{name:"user-frank#node-b", ...}` and `{name:"user-frank#node-c", ...}`
     - UUIDs differ between the two virtual users
     - `route.rules` has 2 entries, each with correct `auth_user` and `outbound`
     - Both rules reference same inbound (or no inbound field)

  3. Add `TestDeriveUUID` (Test 12):
     - Export `DeriveUUID` or test via `Compute` output
     - Verify determinism: same input → same UUID
     - Verify uniqueness: different suffix → different UUID

  4. Update `TestConfigEngine_InboundNode` (Test 1):
     - No Routes → fallback path unchanged: `inbound-vless` with real users and real UUIDs

  **References**:
  - Current test file: `engine_test.go`
  - `DeriveUUID` needs to be exported (capital D) for direct testing, OR test via `Compute` output

  **Acceptance Criteria**:
  - [ ] `go test ./internal/configengine/... -v` → all tests PASS

  **Commit**: `fix(configengine): virtual user per-route routing with derived UUIDs`
  - Message: explains virtual user model, UUID v5 derivation, single inbound per protocol
  - Files: `internal/configengine/engine.go`, `internal/configengine/engine_test.go`

- [x] 3. Push tag `v0.0.1-beta.4` and deploy to cluster

  **What to do**:
  1. `git tag v0.0.1-beta.4 && git push origin main && git push origin v0.0.1-beta.4`
  2. Wait for CI: `gh run watch <release-run-id> --repo shlande/singbox-operator`
  3. `helm upgrade singbox-operator oci://ghcr.io/shlande/charts/singbox-operator --version 0.0.1-beta.4 -n sing-box-operator --reuse-values --force-conflicts`
  4. `kubectl set image deployment/singbox-operator-controller-manager manager=ghcr.io/shlande/singbox-operator:0.0.1-beta.4 -n sing-box-operator`
  5. `kubectl rollout status deployment/singbox-operator-controller-manager -n sing-box-operator --timeout=90s`
  6. `kubectl annotate proxynode claw-jp-2 -n sing-box-operator reconcile-trigger="$(date +%s)" --overwrite`
  7. `kubectl get configmap claw-jp-2-config -n sing-box-operator -o jsonpath='{.data.config\.json}' | python3 -m json.tool`

  **Acceptance Criteria**:
  - [ ] ConfigMap shows single `inbound-vless` on port 30080
  - [ ] `users` array has entry `{name: "shlande#acck-jp", uuid: <derived>}`
  - [ ] `route.rules` has `auth_user: ["shlande#acck-jp"]` → `outbound-acck-jp`
  - [ ] `outbound-xtom-jp` still present in outbounds (as region-auto fallback)

---

## Commit Strategy

- **1+2**: `fix(configengine): virtual user per-route routing with derived UUIDs`
- **3**: no commit (deploy only)

---

## Success Criteria

```bash
go test ./internal/configengine/... -v  # all PASS
kubectl get configmap claw-jp-2-config -n sing-box-operator \
  -o jsonpath='{.data.config\.json}' | python3 -m json.tool
# Expected: single inbound, virtual users, auth_user routing rules
```
