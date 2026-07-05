# Learnings - outbound-inbound-binding

## 2026-07-05 Plan kickoff
- Project: sing-box-operator (kubebuilder-based Kubernetes operator for sing-box)
- Stack: Go + controller-runtime + Ginkgo/envtest + kubebuilder markers
- Key conventions:
  - `make manifests generate` after editing *_types.go markers (regenerates CRD + deepcopy)
  - `make lint-fix` after editing *.go
  - `make test` runs unit tests via envtest
  - Logging style: structured k-v pairs, capital first letter, no trailing period (per AGENTS.md)
  - Auto-generated files (DO NOT EDIT): config/crd/bases/, config/rbac/role.yaml, **/zz_generated.*.go, PROJECT
- Existing patterns to follow:
  - UserGroup.AllowedNodes uses `+listType=set` (api/v1alpha1/usergroup_types.go:25-37)
  - IsNodeAllowed helper with deny-win semantics (engine.go:188-205)
  - Defensive filtering pattern: UserNodeRestrictions checked in controller collectInput AND in configengine (engine.go:335/361/382/486)
  - Watch mapper pattern: sameRegionNodeMapper (controller.go:573-591) for GenerationChangedPredicate
  - `+kubebuilder:validation:MinItems=1` on `[]string` slices (NOT `MinLength` which only applies to string types)
## 2026-07-05 Wave 1: Add AllowedInbounds field
### File changed
- `api/v1alpha1/singboxnode_types.go`: Added `AllowedInbounds []string` field to `SingBoxNodeSpec` after `RelayPort`, before `TLSSecretName`
### Markers used
- `+optional` (backward compat: empty = allow all)
- `+listType=set` (prevent duplicate entries, consistent with UserGroup.AllowedNodes)
- `+kubebuilder:validation:MinItems=1` (if set, must have at least one entry)
### Regeneration commands
- `make manifests` → regenerates `config/crd/bases/singboxoperator.shlande.top_singboxnodes.yaml`
- `make generate` → regenerates `api/v1alpha1/zz_generated.deepcopy.go`
### Generated artifact verification
- CRD yaml: `allowedInbounds` appears at line 61 with `type: array`, `items: type: string`, `minItems: 1`, `x-kubernetes-list-type: set`
- Deepcopy: `in.AllowedInbounds != nil` check with `make([]string, len(*in))` + `copy` at lines 211-215
- Go build: `go build ./...` passes with zero output
## 2026-07-05 Wave 2: Binding filter in controller + cross-region mapper
### Files changed
- `internal/controller/singboxnode_controller.go`: Added binding filter in collectInput (same-region + CustomRoute paths), new `customRouteOutboundNodeMapper`, registered in SetupWithManager
- `internal/controller/singboxnode_controller_test.go`: 5 new test cases (a)-(e) for binding filter behavior
### Implementation details
- **Same-region filter** (collectInput, ~line 214): when iterating same-region outbound candidates, skip `other` if `len(other.Spec.AllowedInbounds) > 0 && !slices.Contains(other.Spec.AllowedInbounds, node.Name)`. Log with `log.Info("Skipping outbound node due to allowedInbounds binding", "outboundNode", other.Name, "inboundNode", node.Name)`.
- **CustomRoute filter** (collectInput, ~line 293): REORDERED from old pattern (append route first, then fetch outboundNode) to new pattern: fetch outboundNode first with `r.Get(...)`, check binding condition, THEN append route + outboundNode to input. Log with `log.Info("Skipping CustomRoute due to allowedInbounds binding", "route", route.Name, "outboundNode", outboundNode.Name, "inboundNode", node.Name)`.
- **customRouteOutboundNodeMapper**: watches SingBoxNode changes. When outbound node A changes, lists all CustomRoutes in same namespace, filters where `route.Spec.OutboundNode == changedNode.Name`, returns `reconcile.Request` for each `route.Spec.InboundNode`. This solves cross-region reactivity — `sameRegionNodeMapper` only enqueues same-region nodes, and `affectedByRouteMapper` fires on CustomRoute changes, not SingBoxNode changes.
- **SetupWithManager**: new Watch registered after existing `sameRegionNodeMapper` watch, with `GenerationChangedPredicate`.
### Test scenarios covered
- (a) AllowedInbounds=[nil/empty] → back compat, outbound collected
- (b) AllowedInbounds=[inboundName] → outbound collected (match)
- (c) AllowedInbounds=[OtherNode] → outbound NOT collected (mismatch)
- (d) CustomRoute defined but AllowedInbounds excludes inbound → route skipped
- (e) Cross-region: change AllowedInbounds from nil to [OtherNode] → inbound reconciled, outbound removed from config
### Verification
- `go build ./...` passes
- `make test` passes (all suites green, controller: 10.486s, 52.6% coverage)
## 2026-07-05 Wave 3: Defensive binding filter in configengine
### Files changed
- `internal/configengine/engine.go`: Added defensive AllowedInbounds filter in `buildOutboundNodeOutbounds` (after routedNodes dedup, before relay port check) and `buildRouteOutbounds` (after OutboundNodesByName lookup, before UserNodeRestrictions check). Added explanatory comments in `buildExperimentalConfig` and `buildRouteInbounds` explaining why no defensive filter is needed (controller pre-filters).
- `internal/configengine/engine_test.go`: 4 new test cases:
  - `TestConfigEngine_OutboundNodeOutbounds_AllowedInboundsFilter`: node with AllowedInbounds=[OtherNode] excluded from region-auto outbounds
  - `TestConfigEngine_RouteOutbounds_AllowedInboundsFilter`: node with AllowedInbounds=[OtherNode] excluded from route outbounds
  - `TestConfigEngine_AllowedInboundsEmpty_BackwardCompat`: empty AllowedInbounds = allow all
  - `TestConfigEngine_DefensiveFilterRegression`: buildExperimentalConfig + buildRouteInbounds produce correct output with pre-filtered OutboundNodes
### Belt-and-suspenders pattern
- Controller (task 2) filters OutboundNodes and CustomRoutes in collectInput before passing to configengine
- Configengine (task 3) adds same check defensively, matching the existing UserNodeRestrictions pattern (engine.go:335/361/382/486)
- The self-outbound path (engine.go:99/118-123) does NOT go through the OutboundNodes loop, so binding filter does NOT affect self-outbound — correct because a dual-role node always needs its own direct outbound regardless of AllowedInbounds
- No logging in configengine defensive filter (controller already logs; silent to avoid duplicate log spam)
### Verification
- `go build ./...` passes
- `go test ./internal/configengine/...` passes (all 25 tests green, including 4 new tests)
## 2026-07-05 Wave 4: Webhook validation for AllowedInbounds
### Files changed
- `internal/webhook/singboxnode_webhook.go`: Added AllowedInbounds validation block in `validateSingBoxNode` (after Roles block, before error check)
- `internal/webhook/webhook_test.go`: Added 5 new test cases in `TestSingBoxNodeWebhook_ValidateCreate`
### Validation rules
- Each entry must be non-empty string (`field.Invalid`)
- No duplicate entries (`field.Duplicate`), using `seenAllowedInbounds map[string]bool` (follows `seenProtocols` dedup pattern from lines 64-70)
- Empty/nil AllowedInbounds passes (backward compat — allow all)
- Non-existent node names pass (nodes can be created in any order)
- Error path used: `field.NewPath("spec", "allowedInbounds").Index(i)` — matches JSON field name `allowedInbounds`, not Go field name `AllowedInbounds`
### Regeneration commands
- No `make manifests` or `make generate` needed (no markers or types changed)
### Test scenarios covered
- (a) AllowedInbounds=["B"] → passes (valid entry)
- (b) AllowedInbounds=[""] → fails with `field.Invalid` (empty string)
- (c) AllowedInbounds=["B","B"] → fails with `field.Duplicate` (duplicate)
- (d) AllowedInbounds=["NonExistentNode"] → passes (existence not checked)
- (e) AllowedInbounds=nil or empty → passes (backward compat, allow all)
### Verification
- `go build ./...` passes
- `go test ./internal/webhook/...` passes (all tests green)
## 2026-07-05 Task 5: Integration tests covering binding end-to-end
### Scenario analysis
- Task 2's test (e) at line 710 ("cross-region: change AllowedInbounds from nil to [OtherNode] → inbound reconciled, outbound removed from config") is actually plan scenario (f), not (e). The labeling was off in task 2.
- Plan scenario (e) was defined as "self-outbound: node S has inbound+outbound roles, S.AllowedInbounds=[] → self-outbound works; S.AllowedInbounds=[S] → self-outbound works (self in list)". The `AllowedInbounds=[]` part is implicitly tested by TestConfigEngine_DualRoleNode_SelfDirect (no AllowedInbounds set), and the `AllowedInbounds=[S]` part is covered by new test (e2) below.
- Plan scenario (f) (cross-region CustomRoute reconcile with AllowedInbounds change) is already covered by task 2's test labeled (e).
### Files changed
- `internal/controller/singboxnode_controller_test.go`: Added scenario (e2) — self-outbound node S with AllowedInbounds=[S], another inbound X in same region must NOT see outbound-S in its config.
- `internal/configengine/engine_test.go`: Added 2 new engine integration tests.
### Scenario (e2) — controller test
- Creates self-outbound node S (roles: inbound+outbound, AllowedInbounds=[S], InboundProtocol="vless", port 30453, RelayPort 31970, region us-west)
- Creates inbound node X (inbound-only, port 30454, same region)
- Reconciles X → verifies ConfigMap JSON does NOT contain outbound-S tag, but DOES contain "direct" tag
- Uses JSON parsing instead of substring matching for robust assertion (envtest leaks old nodes across test boundaries in same Describe)
### Engine integration tests
- `TestConfigEngine_SelfOutbound_AllowedInboundsRestrictsPeers`: Compute() for inbound-only node X with outbound node S (dual-role, AllowedInbounds=[S]) in OutboundNodes. Verifies outbound-node-s NOT in output tags, direct IS present.
- `TestConfigEngine_SelfOutbound_AllowedInboundsWithPeer`: Compute() for inbound X with two outbound nodes: S (dual-role, AllowedInbounds=[S]) and Y (outbound-only, no restriction). Verifies outbound-node-s NOT present, outbound-node-y IS present (backward compat), direct IS present.
### Self-outbound path architecture
- The self-outbound path (engine.go:118-123) adds a direct outbound tagged `outbound-<nodeName>` for the dual-role node itself, bypassing the OutboundNodes loop. This path does NOT check AllowedInbounds — a dual-role node always gets its own direct outbound regardless of its AllowedInbounds setting. This is correct: the node always needs to route traffic to itself.
- When another inbound X computes its config, S is in OutboundNodes (has ProxyRoleOutbound), and the defensive filter in buildOutboundNodeOutbounds checks AllowedInbounds. Since X ∉ S.AllowedInbounds, outbound-S is excluded from X's config.
### Verification
- `go build ./...` passes
- `make test` passes — all suites green: controller 25/25 specs, configengine 34/34 tests (32 existing + 2 new)
- configengine coverage: 92.6%, controller coverage: 52.6%
## 2026-07-05 Task 6: Update samples and README documentation
### Files changed
- `config/samples/singboxoperator_v1alpha1_singboxnode.yaml`: Added commented-out `allowedInbounds` example after the `# TODO(user): Add fields here` placeholder. Format: `# #` prefix so the example is fully commented out and does not affect `kubectl apply`.
- `README.md`: Replaced both `// TODO(user):` placeholders with real content. Added a Features section documenting Node Roles, AllowedInbounds (empty=allow all, non-empty=restrict), and CustomRoute interaction (AND gate). Included a full YAML example snippet.
### Decisions
- Sample YAML stays a TODO template — commented-out only. No uncommented fields that would create real objects on `kubectl apply`.
- `TODO(user): Add simple overview of use/purpose` → became real subtitle under `# sing-box-operator-2`.
- `TODO(user): An in-depth paragraph...` → became Description section with prose about inbound/outbound roles, config generation, and AllowedInbounds.
- `TODO(user): Add detailed information on how you would like others to contribute` → left as-is (separate concern).
### Verification
- `kubectl apply --dry-run=client -f config/samples/singboxoperator_v1alpha1_singboxnode.yaml` passes
- README.md contains "AllowedInbounds" documentation section
