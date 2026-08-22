# Outbound Grouping - Learnings

## T1: Add Tag field to SingBoxNodeSpec

- Tag field inserted between AllowedOutbounds and TLSSecretName in SingBoxNodeSpec
- kubebuilder markers: +optional, +kubebuilder:validation:MaxLength=63, +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
- json tag: json:"tag,omitempty"
- CRD regenerated successfully — tag field appears with description, maxLength:63, and pattern
- make manifests generate runs controller-gen twice (rbac+crd then object)
- go build ./... passes without errors

## T2: Add webhook validation rejecting tag=="default" on inbound nodes

- Validation inserted inside the existing `if isInbound {}` block (line 160) — before the SupportedProtocols check
- Uses `field.Invalid(field.NewPath("spec", "tag"), node.Spec.Tag, message)` — consistent with existing error pattern
- Outbound-only nodes with `tag: "default"` pass validation (the check is inside `if isInbound {}`)
- 4 tests added to webhook_test.go:
  1. `rejects_inbound_node_with_tag=default` — inbound, tag="default" → error
  2. `accepts_inbound_node_with_custom_tag` — inbound, tag="cdn" → nil
  3. `accepts_inbound_node_with_empty_tag` — inbound, no tag → nil
  4. `accepts_outbound-only_node_with_tag=default` — outbound-only, tag="default" → nil
- All 4 new tests + all existing tests pass: `go test ./internal/webhook/ -v` shows full green
- The CRD pattern marker (`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`) permits "default" syntactically — the semantic rejection must be in the webhook, not the marker

## T3: Refactor BuildClientConfig to group outbounds by inbound tag

- BuildClientConfig now:
  1. Groups proxy outbound tags by inbound node's Spec.Tag (empty → "default")
  2. Only records a group when the inbound produced ≥1 proxy outbound (empty-group rule)
  3. Emits per-group selectors sorted by tag (dict order) — each with sorted+deduped outbounds
  4. Emits a top-level "proxy" selector over the group tags (always, even if empty)
  5. Emits "direct" last
- Uses `sort.Strings` + `slices.Compact` on group outbound slices (dedup for dual-role self-collisions)
- `import ("slices", "sort")` were already present, no new imports needed
- `groupOutbounds` is `map[string][]string`, nil-safe via `make`
- Result order: proxyOutbounds (original order) → group selectors (sorted) → proxy selector → direct
- 2 new tests:
  1. `TestBuildClientConfig_MultiGroup`: 2 inbounds (tag="cdn", "us"), 1 shared outbound → 6 items, per-group selectors with correct tags, proxy selector with ["cdn","us"]
  2. `TestBuildClientConfig_EmptyGroupNotEmitted`: 1 inbound (no tag), 1 offline outbound → 2 items, no group selector, proxy selector with outbounds=[]
- Both tests pass: `go test ./internal/apiserver/ -run "TestBuildClientConfig_MultiGroup|TestBuildClientConfig_EmptyGroupNotEmitted" -v` → PASS
- Existing tests WILL break (T4 fixes them), e.g. `TestBuildClientConfig_TwoOutboundNodes` expects old structure
- Evidence saved to `.omo/evidence/task-3-outbound-grouping.txt`

## T4: Update existing tests + add new group tests

- Fixed 4 failing tests in handler_test.go and client_config_filter_test.go: updated len(result) expectations and selector assertions to match new per-group + proxy selector structure
- General formula for len(result): proxy_outbound_count + group_selector_count + 1 (proxy selector) + 1 (direct). For single untagged inbound: group_selector_count=1.
- Added 4 new tests to client_config_group_test.go:
  1. `TestBuildClientConfig_DefaultGroupMerge`: 2 inbounds (no tag, different regions), 2 outbounds → 5 items, default group has both outbounds
  2. `TestBuildClientConfig_TagOnOutboundIgnored`: 1 inbound (no tag), 1 outbound with tag="cdn" → group selector is "default" (not "cdn")
  3. `TestBuildClientConfig_GroupOrderDeterministic`: 2 inbounds (tag="zebra", "apple") → proxy selector has ["apple","zebra"] (dict order), JSON byte-equality verified across 2 calls
  4. `TestBuildClientConfig_GroupDedup`: 2 dual-role inbounds (tag="cdn", different regions) → cdn group has 2 outbounds, no duplicates
- All 43 tests in internal/apiserver/ PASS, all internal/... tests PASS (no regression in webhook, configengine, controller, credmanager, usagecollector)
- Key insight: dual-role nodes in the same region cross-discover each other, producing more proxy outbounds than just self-outbounds. For tests with 2 dual-role nodes in same region + same tag, total = 7 items (4 proxy + 1 group + proxy selector + direct). For different regions, total = 5 (2 self-outbounds + 1 group + proxy selector + direct).
- Another insight: outbound nodes in the same region as multiple inbounds produce outbound-per-inbound entries. E.g., out-1 in "us" with in-a("us") and in-b("us") produces both out-1#in-a AND out-1#in-b.
- Evidence saved to `.omo/evidence/task-4-outbound-grouping.txt`
