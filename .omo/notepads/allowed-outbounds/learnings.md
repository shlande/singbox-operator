# AllowedOutbounds — Wave 1

## Field Definition
Added `AllowedOutbounds []string` to `SingBoxNodeSpec` in `api/v1alpha1/singboxnode_types.go`.

- **Location**: Immediately after `AllowedInbounds` (line 90 → now line 99 after insertion).
- **Markers**: `+optional`, `+listType=set` — symmetric to `AllowedInbounds`.
- **JSON tag**: `json:"allowedOutbounds,omitempty"`
- **Semantics**: Inbound-side whitelist. Empty = allow all (backward compatible). Non-empty = gating both same-region auto-discovery and CustomRoute bindings.
- **Verified**: `go build ./...` passes.

## Wave 2 — collectInput same-region auto-discovery filter

**Status**: Done
**Date**: 2026-07-05

### What was implemented

Inserted `AllowedOutbounds` whitelist check in `collectInput` (singboxnode_controller.go) immediately after the existing `AllowedInbounds` check in the same-region outbound discovery loop.

### Exact location

Around line 218 (after `AllowedInbounds` continue, before `input.OutboundNodes = append`):

```go
if len(node.Spec.AllowedOutbounds) > 0 && !slices.Contains(node.Spec.AllowedOutbounds, other.Name) {
    log.Info("Skipping outbound node due to allowedOutbounds whitelist", "outboundNode", other.Name, "inboundNode", node.Name)
    continue
}
```

### Behavior

- If `node.Spec.AllowedOutbounds` is **empty** → allow all (backward compatible).
- If **non-empty** → only outbound nodes whose names appear in the list are auto-discovered within the same region.
- Logs at Info level when skipping.

### Dependencies

- `slices` already imported.
- `log` variable in scope (created via `log.FromContext(ctx)` earlier in the same block).
- `node.Spec.AllowedOutbounds` field exists from Task 1.

## 2026-07-05 — Wave 2: Webhook Validation

- Added `AllowedOutbounds` validation block in `validateSingBoxNode` (internal/webhook/singboxnode_webhook.go), symmetric to existing `AllowedInbounds` validation.
- Validates: empty string entries rejected, duplicate entries rejected.
- Uses `field.Invalid` for empty entries and `field.Duplicate` for duplicates, same pattern as `AllowedInbounds`.
- File state validated via `go build ./...` and `go vet ./internal/webhook/...` — both pass.
- No changes to `Default()`, `ValidateCreate`, `ValidateUpdate`, `ValidateDelete`, or existing `AllowedInbounds` validation.

## Wave 2 — `resolveOutboundNodes` CustomRoute segment filter

**Status**: Done
**Date**: 2026-07-05

### What was implemented

Inserted `AllowedOutbounds` whitelist check in `resolveOutboundNodes` (`internal/apiserver/client_config.go`) in the CustomRoute loop (~line 142-148).

### Exact location

Lines 143-144 of the CustomRoute segment:
```go
if n, ok := input.OutboundsByName[r.Spec.OutboundNode]; ok && !seen[n.Name] && !input.OfflineNodeNames[n.Name] &&
    configengine.IsNodeAllowed(n.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
    (len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, n.Name)) {
```

### Behavior

- If `inboundNode.Spec.AllowedOutbounds` is **empty** → allow all (backward compatible).
- If **non-empty** → even though a CustomRoute explicitly binds inbound to outbound, it is still gated by the whitelist.
- `slices` already imported in the file (line 6).

### Dependencies

- `inboundNode.Spec.AllowedOutbounds` field exists from Task 1.
- `inboundNode` already resolved in scope earlier in the function.

## Wave 2 — `collectInput` CustomRoute segment filter

**Status**: Done
**Date**: 2026-07-05

### What was implemented

Inserted `AllowedOutbounds` whitelist check in the CustomRoute loop within `collectInput` (`internal/controller/singboxnode_controller.go`), immediately after the existing `AllowedInbounds` check (line 311).

### Exact location

Lines 312-315:

```go
if len(node.Spec.AllowedOutbounds) > 0 && !slices.Contains(node.Spec.AllowedOutbounds, outboundNode.Name) {
    log.Info("Skipping CustomRoute due to allowedOutbounds whitelist", "route", route.Name, "outboundNode", outboundNode.Name, "inboundNode", node.Name)
    continue
}
```

### Behavior

- If `node.Spec.AllowedOutbounds` is **empty** → allow all (backward compatible).
- If **non-empty** → even though a CustomRoute explicitly binds inbound to outbound, it is still gated by the inbound node's `AllowedOutbounds` whitelist.
- The check is placed after the existing `AllowedInbounds` check but before `input.Routes = append`.
- Uses `log := log.FromContext(ctx)` which is created at line 307 (in scope for the for loop).

### Dependencies

- `node.Spec.AllowedOutbounds` field exists from Task 1.
- `slices` already imported.
- `log` variable in scope (line 307).
- `outboundNode` already resolved via `r.Get` at line 304.

### Verified

- `go build ./...` passes on first attempt.

## 2026-07-05 — Wave 2, Task 5: Defensive filter in buildOutboundNodeOutbounds

- Added belt-and-suspenders guard in `buildOutboundNodeOutbounds` (internal/configengine/engine.go), immediately after the existing `AllowedInbounds` defensive check (line 465).
- Pattern: if `input.Node.Spec.AllowedOutbounds` is non-empty and does NOT contain `outNode.Name`, continue (skip this outbound node).
- Comments match the sibling `AllowedInbounds` defensive pattern convention.
- `slices` was already imported (line 9) — no new import needed.
- Verified: `go build ./...` passes.

## Wave 2, Task 9: Sample YAML comment for AllowedOutbounds

**Status**: Done
**Date**: 2026-07-05

### What was done

Added a commented `allowedOutbounds` example block to `config/samples/singboxoperator_v1alpha1_singboxnode.yaml`, immediately after the existing `allowedInbounds` comment block.

### Exact content

```yaml
  # # Example: restrict which outbound nodes this inbound node may use.
  # # Empty or omitted = allow all same-region outbounds (backward compatible).
  # # When set, ALL outbound paths (same-region auto-discovery AND CustomRoute)
  # # are gated by this whitelist.
  # allowedOutbounds:
  #   - "my-outbound-node"
```

### Verification

- `grep -A6 "allowedOutbounds" config/samples/singboxoperator_v1alpha1_singboxnode.yaml` returns the comment block.

### Dependencies

None — purely documentation.

## 2026-07-05 — Wave 2, Task 7: apiserver resolveOutboundNodes same-region filter

**Status**: Done

### What was implemented

Added `AllowedOutbounds` whitelist check in `resolveOutboundNodes` (internal/apiserver/client_config.go) in the same-region auto-discovery loop (line 130 after `IsNodeAllowed` check).

### Exact location

```go
// line 129-130 in resolveOutboundNodes
if n.Spec.Region == inboundNode.Spec.Region && !seen[n.Name] && !input.OfflineNodeNames[n.Name] &&
    configengine.IsNodeAllowed(n.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
    (len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, n.Name)) {
```

### Behavior

- If `inboundNode.Spec.AllowedOutbounds` is **empty** → allow all (backward compatible).
- If **non-empty** → only outbound nodes whose names appear in the list pass through.
- `slices.Contains` already imported.
- Verified: `go build ./...` passes.

## Wave 2 — Task 6: buildRouteOutbounds defensive filter

- Added `AllowedOutbounds` defensive check in `buildRouteOutbounds` after the existing `AllowedInbounds` check.
- Pattern matches the belt-and-suspenders approach: controller filters CustomRoutes in `collectInput`, this is a defensive double-check.
- Inserted at line 498 (after AllowedInbounds block), before the user denial filter.
- Key line: `if len(input.Node.Spec.AllowedOutbounds) > 0 && !slices.Contains(input.Node.Spec.AllowedOutbounds, outNode.Name) { continue }`
- `slices` was already imported.
- `go build ./...` passed.

## Wave 2, Task 10: README AllowedOutbounds section

**Status**: Done
**Date**: 2026-07-05

### What was done

Added three new subsections under `## Features` in README.md:

1. **`### AllowedOutbounds`** (line 51): Documents the field semantics, empty/non-empty behavior, and includes a full YAML example with `us-west-inbound-a`. Same-region auto-discovery suppression called out.

2. **`### AllowedOutbounds interaction with AllowedInbounds`** (line 77): AND-gate semantics when both fields are set — mirrors existing CustomRoute + AllowedInbounds language.

3. **`### Self-as-outbound`** (line 81): Documents the dual-role node self-routing pattern.

### Top description update

Line 7 updated from mentioning only AllowedInbounds to mentioning both AllowedInbounds and AllowedOutbounds with parenthetical qualifiers.

### Verification

```
grep -n "AllowedOutbounds" README.md
```

Returns 5 matches:
- Line 7: top description
- Line 51: section heading
- Line 53: body text
- Line 77: interaction heading
- Line 79: interaction body

## Wave 3 — Task 11: manifests generation + CRD marker verification

**Status**: Done
**Date**: 2026-07-05

### What was done

1. Ran `make manifests` → regenerated `config/crd/bases/singboxoperator.shlande.top_singboxnodes.yaml` and `config/rbac/role.yaml` (controller-gen, no errors).
2. Ran `make generate` → regenerated `api/v1alpha1/zz_generated.deepcopy.go` (controller-gen object, no errors).

### Verification results

| Check | Result |
|---|---|
| `grep -c "allowedOutbounds" CRD` | 1 (present) |
| `x-kubernetes-list-type: set` in CRD | ✅ Found on line after `type: array` in `allowedOutbounds:` block |
| `AllowedOutbounds` in `zz_generated.deepcopy.go` | ✅ DeepCopySlice logic present (`in.AllowedOutbounds != nil` → `DeepCopySlice`) |

### Generated output

The CRD YAML `allowedOutbounds` block:
```yaml
              allowedOutbounds:
                description: |-
                  AllowedOutbounds, when set on an inbound node...
                items:
                  type: string
                type: array
                x-kubernetes-list-type: set
```

### Notes

- Both commands are idempotent — re-running produces identical output.
- No source `.go` files were modified.
- `x-kubernetes-list-type: set` was correctly generated from the `+listType=set` kubebuilder marker.
- DeepCopy correctly uses `DeepCopySlice` for `[]string` — no pointer aliasing issues.

## Wave 3 — Task 12: Webhook test 5 subtests for AllowedOutbounds

**Status**: Done
**Date**: 2026-07-05

### What was done

Added 5 symmetric subtests in `internal/webhook/webhook_test.go` testing `AllowedOutbounds` validation, mirroring the existing `AllowedInbounds` subtests (lines 278-366).

### Test results (all PASS)

| # | Subtest | Expected | Result |
|---|---------|----------|--------|
| 1 | `accepts_AllowedOutbounds_with_valid_entry` | `AllowedOutbounds: ["B"]` → no error | ✅ PASS |
| 2 | `rejects_AllowedOutbounds_with_empty_string_entry` | `AllowedOutbounds: [""]` → error containing "allowedOutbounds" | ✅ PASS |
| 3 | `rejects_AllowedOutbounds_with_duplicate_entries` | `AllowedOutbounds: ["B","B"]` → error containing "allowedOutbounds" | ✅ PASS |
| 4 | `accepts_AllowedOutbounds_with_non-existent_node_name` | `AllowedOutbounds: ["NonExistentNode"]` → no error | ✅ PASS |
| 5 | `accepts_AllowedOutbounds_nil_or_empty_(backward_compat)` | nil and `[]string{}` → no error | ✅ PASS (2 sub-subtests) |

### Key detail

- Inserted after line 366 (closing brace of AllowedInbounds backward compat test), before the "accepts both inbound and outbound roles" test.
- Exactly mirrors AllowedInbounds test structure, only replacing field name and error path string.
- The validation logic being tested is at `singboxnode_webhook.go:132-147`.

## Wave 3 — Task 13: Controller test 5 AllowedOutbounds cases (symmetric)

**Status**: Done
**Date**: 2026-07-05

### What was done

Added 5 symmetric Ginkgo `It(...)` test cases for `AllowedOutbounds` filtering in `internal/controller/singboxnode_controller_test.go`, inserted after line 875 (end of `e2` case) and before line 876 (the `should remove finalizer` test). These mirror the existing `AllowedInbounds` cases (lines 471-875, cases a-e2) with the key symmetry inversion: where `AllowedInbounds` is set on the **outbound** node, `AllowedOutbounds` is set on the **inbound** node.

### Test results (all PASS)

| # | Spec | Expected | Result |
|---|------|----------|--------|
| a_out | `AllowedOutbounds` nil/empty → backward compat | config contains outbound IP `20.0.0.1` | ✅ PASS |
| b_out | `AllowedOutbounds` includes outbound → collected | config contains outbound IP `20.0.1.1` | ✅ PASS |
| c_out | `AllowedOutbounds` excludes outbound → skipped | config does NOT contain `20.0.2.1` | ✅ PASS |
| d_out | CustomRoute with `AllowedOutbounds` excluding outbound → route skipped | config does NOT contain `20.0.3.1` | ✅ PASS |
| e_out | Change `AllowedOutbounds` from nil → exclusion list → reconcile | first contains `20.0.4.1`, after change does NOT | ✅ PASS |

### Run

```bash
go test ./internal/controller/... -v
```

Output: `Ran 30 of 30 Specs in 11.512 seconds — SUCCESS! — 30 Passed | 0 Failed`

### Key design decisions

- **Naming convention**: Used `_out` suffix on spec labels (`a_out`, `b_out`, etc.) to avoid conflict with existing tests.
- **IP range**: Used `20.0.x.x` range to avoid collision with existing AllowedInbounds tests (`10.0.x.x`).
- **Port range**: Used ports `31971`–`31975` and `30460`–`30464` to avoid collision.
- **K8s node names**: Used unique names like `k8s-node-empty-allowed-out`, `k8s-node-match-allowed-out`, etc.
- **No e2_out case**: Skipped the self-outbound variant (e2) — the existing task spec only requires cases a-e.
- **Field placement**: AllowedOutbounds is set on the **inbound** node (not outbound), matching the controller filter at `singboxnode_controller.go:219` where `node.Spec.AllowedOutbounds` is checked against `other.Name` (the outbound node being iterated).
- **e_out uses cross-region**: Like its AllowedInbounds counterpart, case `e_out` uses cross-region nodes (inbound in `us-west`, outbound in `us-east`) with a `CustomRoute`, verifying the change trigger in the CustomRoute filter path (`singboxnode_controller.go:312`).

## Wave 3 — Task 14: configengine AllowedOutbounds + AND-gate tests

**Status**: Done
**Date**: 2026-07-05

### What was done

Added 5 test scenarios (6 test cases including subtests) in `internal/configengine/engine_test.go` testing `AllowedOutbounds` filtering in `Compute` output.

### Test results (all PASS — full suite, zero failures)

| # | Scenario | Key assertions | Result |
|---|----------|---------------|--------|
| 1 | **`TestConfigEngine_AllowedOutbounds_Empty_Regression`** | Empty `AllowedOutbounds` → both `outbound-B` and `outbound-C` appear | ✅ PASS |
| 2 | **`TestConfigEngine_AllowedOutbounds_WhitelistFilter`** | `AllowedOutbounds=["B"]` → `outbound-B` appears, `outbound-C` does NOT | ✅ PASS |
| 3 | **`TestConfigEngine_AllowedOutbounds_CustomRouteGated`** | `AllowedOutbounds=["B"]` + CustomRoute to C → `outbound-C` does NOT appear | ✅ PASS |
| 4a | **`TestConfigEngine_AllowedOutbounds_AndGate/both-allow-connected`** | A allows B, B allows A → `outbound-B` appears | ✅ PASS |
| 4b | **`TestConfigEngine_AllowedOutbounds_AndGate/outbound-rejects-disconnected`** | A allows B, B allows C (rejects A) → `outbound-B` does NOT appear | ✅ PASS |
| 5 | **`TestConfigEngine_AllowedOutbounds_SelfAsOutbound`** | Dual-role node S `AllowedOutbounds=["S"]` → `outbound-S` appears, `outbound-Y` does NOT | ✅ PASS |

### Key design notes

- All tests use the existing `makeNode`/`makeUser`/`makeRoute`/`parseConfig`/`outboundTags`/`containsTag` helper pattern, consistent with the rest of the test file.
- Tests cover both `buildOutboundNodeOutbounds` (same-region filter at engine.go:469) and `buildRouteOutbounds` (CustomRoute filter at engine.go:506).
- The AND-gate subtest 4b validates that BOTH sides' defensive filters work together: `AllowedOutbounds` on the inbound side + `AllowedInbounds` on the outbound side.
- `AllowedOutbounds` must be set on the inbound node (not the outbound nodes) — self-as-outbound scenario 5 uses the inbound node's own name in its `AllowedOutbounds` list.
- `OutboundNodes` input field is used for same-region auto-discovery; `Routes` + `OutboundNodesByName` for CustomRoute paths.
- Full suite: `go test ./internal/configengine/... -v -count=1` — 0 FAILs across all tests.

## Plan Task 15 (Wave 3): apiserver tests for AllowedOutbounds filtering

### What was done
Added `TestBuildClientConfig_WithAllowedOutbounds` to `internal/apiserver/node_restriction_test.go` with 3 table-style subtests:

1. **whitelist-allowed-outbounds**: inbound `node-a` with `AllowedOutbounds: ["node-b"]` → only `node-b#node-a` present, `node-c#node-a` absent, `countProxyOutbounds == 1`
2. **empty-allowed-outbounds-allows-all-regression**: empty/nil `AllowedOutbounds` → both `node-b#node-a` and `node-c#node-a` present, `countProxyOutbounds == 2`
3. **self-allowed-outbounds-dual-role-includes-only-self**: dual-role `node-a` with `AllowedOutbounds: ["node-a"]` → self tag `node-a` present, `node-b#node-a` absent, `countProxyOutbounds == 1`

### Test results
All 3 new subtests + all 15 existing TestBuildClientConfig tests PASS.

## 2026-07-05 — F2 Code Quality Review Fixes (Wave 3)

### Fix 1: Nil guard for inboundNode in CustomRoute loop (client_config.go)

**Issue**: The `resolveOutboundNodes` function had a `for _, r := range input.RoutesByInbound[inboundName]` loop that was OUTSIDE the `if inboundNode != nil` guard. When we added the `AllowedOutbounds` check on line 146 (`inboundNode.Spec.AllowedOutbounds`), it introduced a nil pointer dereference risk.

**Fix**: Moved the entire CustomRoute loop inside the `if inboundNode != nil { }` block (moved from line 143 to line 144, now inside the existing nil guard).

**Why this is correct**: The loop uses `inboundName` (a string key) to look up routes, but dereferences `inboundNode` for the AllowedOutbounds check. If `inboundNode` is nil (inbound not found by search), the code would panic. Since the route data is already unavailable in a meaningful way when inbound doesn't exist, guarding is the right approach.

### Fix 2a: Gate self-as-outbound with AllowedOutbounds in client_config.go

**Issue**: The self-as-outbound branch (lines 136-140) adds `inboundNode` to the outbound list when it has outbound role, but bypassed the `AllowedOutbounds` check. This meant a dual-role node with `AllowedOutbounds: ["other-node"]` would still add itself as an outbound.

**Fix**: Added `(len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, inboundNode.Name))` to the if-condition.

### Fix 2b: Gate self-as-outbound with AllowedOutbounds in engine.go

**Issue**: Same as Fix 2a but in the Compute function — the `isSelfOutbound` block (line 118-123) unconditionally added a direct outbound without checking AllowedOutbounds.

**Fix**: Gated with `(len(node.Spec.AllowedOutbounds) == 0 || slices.Contains(node.Spec.AllowedOutbounds, node.Name))`.

### Test: SelfAsOutboundDenied

Added `TestConfigEngine_AllowedOutbounds_SelfAsOutboundDenied` — dual-role node S with `AllowedOutbounds: ["other-node"]` (self excluded) → asserts `outbound-node-s` does NOT appear, `outbound-other-node` does appear.

### Verification (all PASS)
- `go build ./...` ✅
- `go test ./internal/configengine/... -count=1` ✅
- `go test ./internal/apiserver/... -count=1` ✅
- `go test ./internal/controller/... -count=1 -timeout 600s` ✅
- `go test ./internal/webhook/... -count=1` ✅
