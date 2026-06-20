# UserGroup Node-Level Restriction Feature

## TL;DR

> **Quick Summary**: Add a new `UserGroup` CRD that restricts which SingBoxNodes a User can access at the **node level** — if a node is restricted, the user cannot use it as either an inbound (no auth entry) or an outbound (no virtual user / no relay). Enforcement spans both the server-side config generation path (configengine) and the client config API path (apiserver).
>
> **Deliverables**:
> - `api/v1alpha1/usergroup_types.go` — new CRD with `allowedNodes`/`deniedNodes`
> - `api/v1alpha1/user_types.go` — add `userGroupRef` field
> - `api/v1alpha1/zz_generated.deepcopy.go` — regenerated
> - `config/crd/bases/singboxoperator.shlande.top_usergroups.yaml` — generated CRD
> - `config/rbac/role.yaml` — updated RBAC
> - `internal/configengine/engine.go` — add `UserNodeRestrictions` to `Input` struct; filter in `buildRouteInbounds()` and `buildUserInbounds()`
> - `internal/controller/singboxnode_controller.go` — add UserGroup pre-filter + restriction map in `collectInput()`; add UserGroup watch in `SetupWithManager()`
> - `internal/controller/user_controller.go` — add UserGroupNotFound condition; filter `findMatchingInboundNodes()`
> - `internal/controller/usergroup_controller.go` — new controller (watches UserGroup → triggers SingBoxNode reconcile)
> - `internal/webhook/usergroup_webhook.go` — new webhook (format validation)
> - `internal/webhook/user_webhook.go` — add `userGroupRef` format validation
> - `internal/apiserver/handler.go` + `client_config.go` — filter restricted inbound/outbound nodes
> - `cmd/main.go` — register controller, webhook, field index
> - Tests for all new code
>
> **Estimated Effort**: Medium-Large
> **Parallel Execution**: YES — 3 waves
> **Critical Path**: Task 1 (scaffold) → Task 2 (types) → Task 3 (generate) → Task 4 (configengine Input) → Task 5+6+7+8 parallel → Task 9 (main.go) → Task 10 (tests) → F1-F4

---

## Context

### Original Request
Add a `UserGroup` resource to restrict which SingBoxNodes users can access. Restriction is at the **node level**: if a node is restricted for a user, the user cannot use that node as either an inbound or outbound. Users bind to a UserGroup via `spec.userGroupRef`. No group = no restrictions.

### Interview Summary
**Key Discussions**:
- **Binding direction**: User.spec.userGroupRef (string, same namespace, optional)
- **Restriction granularity**: NODE level — restricting a node blocks the user from using it as both inbound AND outbound
- **Fields**: `allowedNodes []string` (whitelist; empty = allow all) + `deniedNodes []string` (blacklist; deny-wins on conflict)
- **CustomRoute**: Yes, CustomRoute-sourced outbound nodes are also restricted
- **Multi-group**: Not allowed — one User → at most one UserGroup
- **Missing group behavior**: Fail-open (allow all) + UserGroupNotFound condition on User
- **apiserver**: Yes, client config API also filters restricted nodes

**Research Findings**:
- Two distinct enforcement paths: (1) server-side configengine path, (2) client config API path
- Virtual users `user#outboundNode` are created per (user, outbound) pair in `buildRouteInbounds()`
- `collectInput()` in SingBoxNodeReconciler aggregates users and outbound nodes — correct pre-filter point
- `resolveOutboundNodes()` in apiserver handles both same-region AND CustomRoute outbound nodes
- `buildRouteOutbounds()` in configengine handles CustomRoute outbound entries (must also be filtered)
- `user_controller.findMatchingInboundNodes()` drives `User.status.ActiveNodes` — must also filter

### Metis Review
**Identified Gaps** (addressed):
- CustomRoute outbound nodes are a separate code path → explicitly in scope; filter in both `buildRouteOutbounds()` and `resolveOutboundNodes()`
- `User.status.ActiveNodes` would be inaccurate without filtering in `user_controller` → added to scope
- Option A (remove user from input.Users entirely) fails for partial outbound restrictions → use Option C Hybrid
- `buildUserInbounds()` fallback path also needs filtering (easy to miss) → explicitly in scope
- UserGroup watch on SingBoxNodeReconciler is missing → added to `SetupWithManager()`
- `map[string]bool` set for O(1) lookups instead of `[]string` linear scan

---

## Work Objectives

### Core Objective
Add a UserGroup CRD that restricts which SingBoxNodes a User can access at the node level (both as inbound and outbound), enforced in both the sing-box server config generation and the client config API.

### Concrete Deliverables
- `api/v1alpha1/usergroup_types.go` with UserGroup, UserGroupSpec (allowedNodes, deniedNodes), UserGroupStatus
- `api/v1alpha1/user_types.go` with `UserGroupRef string` field added to UserSpec
- `api/v1alpha1/zz_generated.deepcopy.go` regenerated
- `config/crd/bases/singboxoperator.shlande.top_usergroups.yaml` generated
- `config/rbac/role.yaml` updated with usergroups permissions
- `internal/configengine/engine.go` — `Input.UserNodeRestrictions map[string]map[string]bool`; filtering in `buildRouteInbounds()`, `buildUserInbounds()`, `buildRouteOutbounds()`
- `internal/controller/singboxnode_controller.go` — UserGroup pre-filter + restriction map in `collectInput()`; UserGroup watch in `SetupWithManager()`
- `internal/controller/user_controller.go` — UserGroupNotFound condition; filtered `findMatchingInboundNodes()`
- `internal/controller/usergroup_controller.go` — full reconciler
- `internal/webhook/usergroup_webhook.go` — format validation
- `internal/webhook/user_webhook.go` — userGroupRef format validation
- `internal/apiserver/handler.go` + `internal/apiserver/client_config.go` — node restriction filtering
- `cmd/main.go` — UserGroup controller, webhook, field index registration
- Tests: configengine (engine_test.go additions), controller tests, webhook tests, apiserver tests

### Definition of Done
- [ ] `make manifests` exits 0; `config/rbac/role.yaml` contains `usergroups` resource
- [ ] `make generate` exits 0; `zz_generated.deepcopy.go` has UserGroup DeepCopy
- [ ] `make test` exits 0; all existing tests pass; new tests pass
- [ ] `make lint-fix` exits 0
- [ ] A User with `userGroupRef: "group-a"` where group-a has `deniedNodes: ["node-b"]` causes node-a's ConfigMap to NOT contain user's inbound entry or virtual user `user#node-b`
- [ ] A User with no `userGroupRef` has unrestricted access (regression: existing behavior unchanged)
- [ ] Missing UserGroup reference is fail-open + sets `UserGroupNotFound` condition on User
- [ ] `User.status.activeNodes` does NOT include restricted nodes

### Must Have
- UserGroup CRD with `allowedNodes []string` and `deniedNodes []string` fields
- `User.spec.userGroupRef` field (optional string, same namespace)
- `Input.UserNodeRestrictions map[string]map[string]bool` in configengine
- Inbound pre-filter in `collectInput()`: if current inbound node is denied for user → skip user entirely
- Outbound restriction map in `collectInput()`: build `UserNodeRestrictions` per user
- Filtering in `buildRouteInbounds()`: skip virtual user `user#outboundNode` if outbound denied
- Filtering in `buildUserInbounds()`: skip user if current node denied (redundant with pre-filter but defensive)
- Filtering in `buildRouteOutbounds()`: skip CustomRoute outbound nodes that are denied for ALL users
- Filtering in `resolveOutboundNodes()` (apiserver): skip denied inbound and outbound nodes
- Filtering in `findMatchingInboundNodes()` (user_controller): so `User.status.activeNodes` is accurate
- UserGroup watch in `SingBoxNodeReconciler.SetupWithManager()` → triggers affected SingBoxNodes
- UserGroup controller (watches UserGroup → triggers User reconcile for cascade)
- UserGroup webhook (format validation: valid DNS names in allowedNodes/deniedNodes)
- User webhook update: validate userGroupRef format if non-empty
- Fail-open when UserGroup not found + UserGroupNotFound condition on User
- All existing tests pass without modification
- `isNodeAllowed(nodeName string, allowed, denied map[string]bool) bool` pure function

### Must NOT Have (Guardrails)
- NO manual edits to `zz_generated.deepcopy.go`, `config/crd/bases/*.yaml`, `config/rbac/role.yaml`
- NO changes to virtual user naming scheme (`user#outboundNode`)
- NO changes to `Compute()`'s output schema — only filtering behavior
- NO UserGroup status conditions beyond `ObservedGeneration` (no member tracking, no node validation)
- NO cross-namespace `userGroupRef` support (webhook must reject)
- NO `client.Client` injected into `BuildClientConfig()` or `ClientConfigInput` (fetch in handler, pass as data)
- NO finalizer on UserGroup
- NO label-selector-based group membership
- NO multi-group membership (`userGroupRef` is a single string)
- NO breaking changes to any existing configengine test

---

## Verification Strategy (MANDATORY)

> **ZERO HUMAN INTERVENTION** — ALL verification is agent-executed.

### Test Decision
- **Infrastructure exists**: YES (Ginkgo + Gomega for controllers, standard Go testing.T for configengine/webhooks/apiserver)
- **Automated tests**: Tests-after (implement then add tests matching existing patterns)
- **Framework**: `make test`

### QA Policy
Every task includes agent-executed QA scenarios. Evidence saved to `.omo/evidence/task-{N}-{scenario-slug}.txt`.

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (Start Immediately — scaffold + types):
├── Task 1: Scaffold UserGroup CRD via kubebuilder CLI [quick]
└── Task 2: Define UserGroup types + update User types [quick]

Wave 2 (After Wave 1 — generate + core changes, MAX PARALLEL):
├── Task 3: make manifests && make generate [quick]
├── Task 4: configengine Input struct + filtering (buildRouteInbounds, buildUserInbounds, buildRouteOutbounds) [unspecified-high]
├── Task 5: SingBoxNodeReconciler collectInput() pre-filter + restriction map + UserGroup watch [unspecified-high]
├── Task 6: user_controller UserGroupNotFound condition + filtered findMatchingInboundNodes() [unspecified-high]
├── Task 7: UserGroup controller [unspecified-high]
├── Task 8: UserGroup webhook + user_webhook.go userGroupRef validation [quick]
└── Task 9: apiserver handler.go + client_config.go node restriction filtering [unspecified-high]

Wave 3 (After Wave 2 — wire up + tests):
├── Task 10: cmd/main.go registration (controller, webhook, field index) [quick]
└── Task 11: Tests for all new code [unspecified-high]

Wave FINAL (After ALL tasks — 4 parallel reviews):
├── Task F1: Plan compliance audit (oracle)
├── Task F2: Code quality review (unspecified-high)
├── Task F3: Real manual QA (unspecified-high)
└── Task F4: Scope fidelity check (deep)
-> Present results -> Get explicit user okay
```

### Dependency Matrix

- **1**: none → blocks 2
- **2**: 1 → blocks 3, 4, 5, 6, 7, 8, 9
- **3**: 2 → blocks 4, 5, 6, 7, 8, 9
- **4**: 2, 3 → blocks 10, 11
- **5**: 2, 3 → blocks 10, 11
- **6**: 2, 3 → blocks 10, 11
- **7**: 2, 3 → blocks 10, 11
- **8**: 2, 3 → blocks 10, 11
- **9**: 2, 3 → blocks 10, 11
- **10**: 3, 4, 5, 6, 7, 8, 9 → blocks 11
- **11**: 4, 5, 6, 7, 8, 9, 10 → blocks F1-F4

### Agent Dispatch Summary

- **Wave 1**: T1 → `quick`, T2 → `quick`
- **Wave 2**: T3 → `quick`, T4 → `unspecified-high`, T5 → `unspecified-high`, T6 → `unspecified-high`, T7 → `unspecified-high`, T8 → `quick`, T9 → `unspecified-high`
- **Wave 3**: T10 → `quick`, T11 → `unspecified-high`
- **FINAL**: F1 → `oracle`, F2 → `unspecified-high`, F3 → `unspecified-high`, F4 → `deep`

---

## TODOs

> **FORMAT**: Task labels use bare numbers: `1.`, `2.`, `3.` — NOT `T1.`, `Task 1.`, `Phase 1:`.
> Final Verification Wave labels use `F1.`, `F2.`, etc.

---

- [x] 1. Scaffold UserGroup CRD via kubebuilder CLI

  **What to do**:
  - Run: `kubebuilder create api --group singboxoperator --version v1alpha1 --kind UserGroup --resource=true --controller=true`
  - Answer YES to creating resource, YES to creating controller
  - Verify scaffolded files exist: `api/v1alpha1/usergroup_types.go`, `internal/controller/usergroup_controller.go`
  - Verify `PROJECT` file now lists 4 resources
  - Do NOT edit scaffolded files yet — that is Task 2 and Task 7

  **Must NOT do**:
  - Do NOT manually create type files — always use kubebuilder CLI first
  - Do NOT run `make manifests` or `make generate` yet — that is Task 3

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: Single CLI command + verification
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO
  - **Parallel Group**: Wave 1 (first step)
  - **Blocks**: Task 2
  - **Blocked By**: None

  **References**:

  **External References**:
  - `AGENTS.md` "CLI Commands Cheat Sheet" → `kubebuilder create api` syntax

  **Acceptance Criteria**:
  - [ ] `api/v1alpha1/usergroup_types.go` exists (scaffolded)
  - [ ] `internal/controller/usergroup_controller.go` exists (scaffolded)
  - [ ] `PROJECT` file contains `kind: UserGroup` entry
  - [ ] `go build ./...` exits 0

  **QA Scenarios**:

  ```
  Scenario: Verify scaffold and compilation
    Tool: Bash
    Steps:
      1. Run: kubebuilder create api --group singboxoperator --version v1alpha1 --kind UserGroup --resource=true --controller=true
      2. Run: ls api/v1alpha1/usergroup_types.go internal/controller/usergroup_controller.go
      3. Run: grep "kind: UserGroup" PROJECT
      4. Run: go build ./...
    Expected Result: Files exist, PROJECT has entry, build succeeds
    Evidence: .omo/evidence/task-1-scaffold.txt
  ```

  **Commit**: NO (groups with Task 2)

---

- [x] 2. Define UserGroup types + update User types

  **What to do**:
  - Edit `api/v1alpha1/usergroup_types.go` (replace scaffolded stub):
    - `UserGroupSpec` struct:
      ```go
      // AllowedNodes is the whitelist of SingBoxNode names this group may use.
      // If empty, all nodes are allowed (subject to DeniedNodes).
      // +listType=set
      AllowedNodes []string `json:"allowedNodes,omitempty"`

      // DeniedNodes is the blacklist of SingBoxNode names this group may NOT use.
      // Deny-wins: if a node appears in both AllowedNodes and DeniedNodes, it is denied.
      // +listType=set
      DeniedNodes []string `json:"deniedNodes,omitempty"`
      ```
    - `UserGroupStatus` struct:
      ```go
      ObservedGeneration int64              `json:"observedGeneration,omitempty"`
      Conditions         []metav1.Condition `json:"conditions,omitempty"`
      ```
    - Kubebuilder markers on UserGroup type:
      ```go
      // +kubebuilder:object:root=true
      // +kubebuilder:subresource:status
      // +kubebuilder:resource:scope=Namespaced
      ```
  - Edit `api/v1alpha1/user_types.go` — add to `UserSpec`:
    ```go
    // UserGroupRef is the name of the UserGroup in the same namespace.
    // If empty, no node restrictions apply to this user.
    // +kubebuilder:validation:MaxLength=253
    UserGroupRef string `json:"userGroupRef,omitempty"`
    ```
  - Do NOT run `make manifests` or `make generate` yet

  **Must NOT do**:
  - Do NOT add `members []string` or member tracking to UserGroupStatus
  - Do NOT add namespace field to userGroupRef (same-namespace only)
  - Do NOT add finalizer marker
  - Do NOT change any existing fields in UserSpec

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: Type definition edits following well-established patterns
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO (depends on Task 1)
  - **Parallel Group**: Wave 1
  - **Blocks**: Tasks 3, 4, 5, 6, 7, 8, 9
  - **Blocked By**: Task 1

  **References**:

  **Pattern References**:
  - `api/v1alpha1/singboxnode_types.go` — full CRD type with markers, Status with Conditions, ObservedGeneration
  - `api/v1alpha1/user_types.go:UserSpec` — existing struct to add UserGroupRef to
  - `api/v1alpha1/customroute_types.go` — simpler CRD example (plain string fields)

  **API/Type References**:
  - `k8s.io/apimachinery/pkg/apis/meta/v1.Condition` — use for Status.Conditions

  **Acceptance Criteria**:
  - [ ] `UserGroupSpec` has `AllowedNodes []string` and `DeniedNodes []string` with API comments
  - [ ] `UserGroupStatus` has `Conditions []metav1.Condition` and `ObservedGeneration int64`
  - [ ] `UserSpec` has `UserGroupRef string` with `omitempty` json tag
  - [ ] `// +kubebuilder:subresource:status` marker present on UserGroup
  - [ ] `go build ./api/...` exits 0

  **QA Scenarios**:

  ```
  Scenario: Types compile with correct fields
    Tool: Bash
    Steps:
      1. Run: go build ./api/...
      2. Run: grep -n "AllowedNodes\|DeniedNodes\|UserGroupRef" api/v1alpha1/usergroup_types.go api/v1alpha1/user_types.go
      3. Run: grep "subresource:status" api/v1alpha1/usergroup_types.go
    Expected Result: Build succeeds; all 3 fields present; subresource marker present
    Evidence: .omo/evidence/task-2-types.txt
  ```

  **Commit**: YES (groups with Task 1+3)
  - Message: `feat(api): add UserGroup CRD and userGroupRef field on User`
  - Files: `api/v1alpha1/usergroup_types.go`, `api/v1alpha1/user_types.go`
  - Pre-commit: `go build ./api/...`

---

- [x] 3. make manifests && make generate

  **What to do**:
  - Run: `make manifests` — regenerates CRD YAML and RBAC from kubebuilder markers
  - Run: `make generate` — regenerates `zz_generated.deepcopy.go`
  - Verify:
    - `config/crd/bases/singboxoperator.shlande.top_usergroups.yaml` exists
    - `config/rbac/role.yaml` contains `usergroups` resource
    - `api/v1alpha1/zz_generated.deepcopy.go` has `DeepCopyInto` for `UserGroup`, `UserGroupSpec`, `UserGroupStatus`, and updated `UserSpec`

  **Must NOT do**:
  - Do NOT manually edit any generated files

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: Shell commands only
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO (depends on Task 2)
  - **Parallel Group**: Wave 2 (must complete before Tasks 4-9 can start)
  - **Blocks**: Tasks 4, 5, 6, 7, 8, 9
  - **Blocked By**: Tasks 1, 2

  **References**:

  **External References**:
  - `AGENTS.md` "After Making Changes" section

  **Acceptance Criteria**:
  - [ ] `config/crd/bases/singboxoperator.shlande.top_usergroups.yaml` exists
  - [ ] `grep "usergroups" config/rbac/role.yaml` returns entries
  - [ ] `grep "UserGroup" api/v1alpha1/zz_generated.deepcopy.go` returns DeepCopy methods
  - [ ] `make manifests` exits 0
  - [ ] `make generate` exits 0

  **QA Scenarios**:

  ```
  Scenario: Generated files are correct
    Tool: Bash
    Steps:
      1. Run: make manifests && make generate
      2. Run: ls config/crd/bases/singboxoperator.shlande.top_usergroups.yaml
      3. Run: grep "usergroups" config/rbac/role.yaml
      4. Run: grep "func (in \*UserGroup)" api/v1alpha1/zz_generated.deepcopy.go
    Expected Result: All files exist with expected content
    Evidence: .omo/evidence/task-3-generate.txt
  ```

  **Commit**: YES (groups with Task 2)
  - Message: `chore: regenerate CRDs, RBAC, and DeepCopy for UserGroup`
  - Files: `config/crd/bases/singboxoperator.shlande.top_usergroups.yaml`, `config/rbac/role.yaml`, `api/v1alpha1/zz_generated.deepcopy.go`
  - Pre-commit: `make manifests && make generate`

---

- [x] 4. configengine: Input struct + filtering in buildRouteInbounds, buildUserInbounds, buildRouteOutbounds

  **What to do**:

  **Step A — Add `UserNodeRestrictions` to `Input` struct** in `internal/configengine/engine.go`:
  ```go
  // UserNodeRestrictions maps userName → set of denied SingBoxNode names.
  // A nil or missing entry means no restrictions (allow all nodes).
  // Populated by the controller from UserGroup resources.
  UserNodeRestrictions map[string]map[string]bool
  ```

  **Step B — Add `isNodeAllowed()` pure helper function**:
  ```go
  // isNodeAllowed reports whether a user may access the given node.
  // allowedNodes: whitelist (nil/empty = allow all).
  // deniedNodes: blacklist (deny-wins if node in both).
  // Uses map[string]bool sets for O(1) lookups.
  func isNodeAllowed(nodeName string, allowedNodes, deniedNodes map[string]bool) bool {
      if deniedNodes[nodeName] {
          return false // deny-wins
      }
      if len(allowedNodes) == 0 {
          return true // empty allowlist = allow all
      }
      return allowedNodes[nodeName]
  }
  ```

  **Step C — Filter in `buildRouteInbounds()`** (line ~263):
  When iterating `(user, outboundNode)` pairs to create virtual users `user#outboundNode`:
  - Check `isNodeAllowed(outboundNode.Name, allowedSet, deniedSet)` where sets come from `input.UserNodeRestrictions[user.Name]`
  - If not allowed → skip this (user, outboundNode) pair; do NOT generate the virtual user or the route rule entry for this pair

  **Step D — Filter in `buildUserInbounds()`** (line ~379):
  When building plain user entries (no-outbound-peer fallback):
  - Check `isNodeAllowed(input.Node.Name, allowedSet, deniedSet)` for each user
  - If not allowed → skip this user (they were pre-filtered in collectInput, but this is defensive)
  - Note: This case is normally handled by the pre-filter in collectInput(), but add it here for correctness

  **Step E — Filter in `buildRouteOutbounds()`** (line ~439):
  When building SOCKS5 outbound entries for CustomRoute targets:
  - A CustomRoute outbound entry should be skipped only if ALL users are restricted from it
  - Check: for each outbound node in routes, if every user in `input.Users` has that node denied → skip the outbound entry
  - If at least one user can use it → keep the entry (the virtual user filter in Step C handles per-user routing)

  **Must NOT do**:
  - Do NOT change `Compute()`'s function signature
  - Do NOT change the virtual user naming scheme (`user#outboundNode`)
  - Do NOT change the output JSON schema
  - Do NOT break any existing configengine test (zero-value `UserNodeRestrictions` = nil = no restrictions)

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: Core config generation logic, multi-point changes, must not break existing tests
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Tasks 5, 6, 7, 8, 9 — all after Task 3)
  - **Parallel Group**: Wave 2
  - **Blocks**: Tasks 10, 11
  - **Blocked By**: Tasks 2, 3

  **References**:

  **Pattern References**:
  - `internal/configengine/engine.go:88` — `Compute()` function entry point
  - `internal/configengine/engine.go:263` — `buildRouteInbounds()` — the main virtual user generation loop
  - `internal/configengine/engine.go:379` — `buildUserInbounds()` — the fallback path
  - `internal/configengine/engine.go:439` — `buildRouteOutbounds()` — CustomRoute outbound entries
  - `internal/configengine/engine.go:30` — `Input` struct definition (add field here)
  - `internal/configengine/engine.go:deduplicateByTag()` — example of a pure helper function
  - `internal/configengine/engine_test.go` — 17 existing tests; all must pass after changes

  **Acceptance Criteria**:
  - [ ] `Input` struct has `UserNodeRestrictions map[string]map[string]bool` field
  - [ ] `isNodeAllowed()` function exists and handles all 4 cases (deny-wins, empty=allow-all, in-allowlist, not-in-allowlist)
  - [ ] `buildRouteInbounds()` skips virtual users for denied outbound nodes
  - [ ] `buildUserInbounds()` skips users denied from current inbound node
  - [ ] `buildRouteOutbounds()` skips outbound entries where all users are denied
  - [ ] `go build ./internal/configengine/...` exits 0
  - [ ] All 17 existing configengine tests still pass: `go test ./internal/configengine/...`

  **QA Scenarios**:

  ```
  Scenario: Existing tests unaffected (nil UserNodeRestrictions = no restrictions)
    Tool: Bash
    Steps:
      1. Run: go test ./internal/configengine/... -v 2>&1 | grep -E "PASS|FAIL|---"
    Expected Result: All existing tests PASS, 0 FAIL
    Evidence: .omo/evidence/task-4-existing-tests.txt

  Scenario: isNodeAllowed() handles all cases
    Tool: Bash (go test)
    Steps:
      1. Add test cases to engine_test.go or a new usergroup_test.go in the configengine package
      2. Run: go test ./internal/configengine/... -run TestIsNodeAllowed -v
      3. Verify: isNodeAllowed("node-b", nil, map{"node-b": true}) == false (deny)
      4. Verify: isNodeAllowed("node-b", nil, nil) == true (no restrictions)
      5. Verify: isNodeAllowed("node-b", map{"node-b": true}, nil) == true (in allowlist)
      6. Verify: isNodeAllowed("node-c", map{"node-b": true}, nil) == false (not in allowlist)
      7. Verify: isNodeAllowed("node-b", map{"node-b": true}, map{"node-b": true}) == false (deny-wins)
    Expected Result: All 5 assertions pass
    Evidence: .omo/evidence/task-4-isNodeAllowed.txt

  Scenario: Virtual user NOT generated for denied outbound
    Tool: Bash (go test)
    Steps:
      1. Add test: construct Input with UserNodeRestrictions = {"alice": {"node-b": true}}
         and OutboundNodes = [node-b, node-c], Users = [alice]
      2. Call Compute(input)
      3. Assert output config does NOT contain "alice#node-b" in any inbound users list
      4. Assert output config DOES contain "alice#node-c"
    Expected Result: Restricted virtual user absent, allowed virtual user present
    Evidence: .omo/evidence/task-4-virtual-user-filter.txt
  ```

  **Commit**: YES
  - Message: `feat(configengine): add UserNodeRestrictions to Input and filter restricted users/nodes`
  - Files: `internal/configengine/engine.go`
  - Pre-commit: `go test ./internal/configengine/...`

---

- [x] 5. SingBoxNodeReconciler: collectInput() pre-filter + restriction map + UserGroup watch

  **What to do**:

  **Part A — collectInput() in `internal/controller/singboxnode_controller.go`**:

  After collecting the list of users (protocol match), add UserGroup pre-filter and restriction map building:

  ```
  For each user in matched users:
    1. If user.Spec.UserGroupRef is empty → no restrictions, add to input.Users normally
    2. If user.Spec.UserGroupRef is non-empty:
       a. Fetch UserGroup: r.Get(ctx, types.NamespacedName{Namespace: node.Namespace, Name: user.Spec.UserGroupRef}, &ug)
       b. If not found → log warning, add user to input.Users with no restrictions (fail-open)
       c. If found:
          - Convert ug.Spec.AllowedNodes []string → allowedSet map[string]bool
          - Convert ug.Spec.DeniedNodes []string → deniedSet map[string]bool
          - Check isNodeAllowed(node.Name, allowedSet, deniedSet):
            * If false → skip this user entirely (don't add to input.Users) — this handles inbound restriction
            * If true → add to input.Users; also add to input.UserNodeRestrictions[user.Name] = deniedSet
              (only store the denied set; the configengine will use it to filter outbound virtual users)
  ```

  Note: `input.UserNodeRestrictions` only needs to store the denied nodes (not allowed nodes) because the inbound pre-filter already handled the inbound node check. The configengine only needs to know which outbound nodes each user cannot use.

  **Part B — SetupWithManager() in `internal/controller/singboxnode_controller.go`**:

  Add a watch on UserGroup changes that triggers reconciliation of all SingBoxNodes whose users reference the changed UserGroup:

  ```go
  .Watches(
      &proxyv1alpha1.UserGroup{},
      handler.EnqueueRequestsFromMapFunc(r.usersInGroupToNodesMapper),
  )
  ```

  Implement `usersInGroupToNodesMapper`:
  1. List all Users in the namespace with `spec.userGroupRef == userGroup.Name` (use field index on `spec.userGroupRef`)
  2. For each such User, find all SingBoxNodes that match the user's protocol (same as existing `matchingProtocolNodeMapper`)
  3. Return the union of all those SingBoxNode names as reconcile requests

  **Must NOT do**:
  - Do NOT use `r.Update()` for annotation triggers — use `r.Patch()` with `client.MergeFrom`
  - Do NOT add UserGroup awareness to `configengine.Compute()` directly — pass via `Input` struct
  - Do NOT fail-closed on missing UserGroup — always fail-open

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: Core reconciler modification, watch chain setup, multi-step logic
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Tasks 4, 6, 7, 8, 9 — all after Task 3)
  - **Parallel Group**: Wave 2
  - **Blocks**: Tasks 10, 11
  - **Blocked By**: Tasks 2, 3

  **References**:

  **Pattern References**:
  - `internal/controller/singboxnode_controller.go:191` — `collectInput()` function to modify (find the user-collection loop at ~lines 226-245)
  - `internal/controller/singboxnode_controller.go:554` — `matchingProtocolNodeMapper` — template for `usersInGroupToNodesMapper`
  - `internal/controller/singboxnode_controller.go:SetupWithManager()` — where to add the new `.Watches()` call
  - `internal/controller/singboxnode_controller.go:isSingBoxNodeReady()` — example of fail-open pattern

  **API/Type References**:
  - `api/v1alpha1/usergroup_types.go:UserGroupSpec` — AllowedNodes, DeniedNodes
  - `internal/configengine/engine.go:Input.UserNodeRestrictions` — the field to populate (Task 4)

  **Acceptance Criteria**:
  - [ ] `collectInput()` fetches UserGroup per user and builds `input.UserNodeRestrictions`
  - [ ] Users denied from the current inbound node are excluded from `input.Users`
  - [ ] `usersInGroupToNodesMapper` function exists and is registered in `SetupWithManager()`
  - [ ] `go build ./internal/controller/...` exits 0

  **QA Scenarios**:

  ```
  Scenario: collectInput() pre-filter and restriction map build
    Tool: Bash
    Steps:
      1. Run: go build ./internal/controller/...
      2. Run: grep -n "UserNodeRestrictions\|UserGroupRef\|usersInGroupToNodesMapper" internal/controller/singboxnode_controller.go
    Expected Result: Build succeeds; all 3 identifiers present
    Evidence: .omo/evidence/task-5-controller-build.txt
  ```

  **Commit**: YES
  - Message: `feat(controller): add UserGroup pre-filter and restriction map in collectInput; add UserGroup watch`
  - Files: `internal/controller/singboxnode_controller.go`
  - Pre-commit: `go build ./internal/controller/...`

---

- [x] 6. user_controller: UserGroupNotFound condition + filtered findMatchingInboundNodes()

  **What to do**:

  **Part A — UserGroupNotFound condition** in `internal/controller/user_controller.go`:

  In the `Reconcile()` function, after finding matching inbound nodes:
  1. If `user.Spec.UserGroupRef` is non-empty:
     a. Attempt to fetch the UserGroup
     b. If not found → set condition: `Type: "UserGroupReady"`, `Status: metav1.ConditionFalse`, `Reason: "UserGroupNotFound"`, `Message: fmt.Sprintf("UserGroup %q not found in namespace %q", user.Spec.UserGroupRef, user.Namespace)`
     c. If found → set condition: `Type: "UserGroupReady"`, `Status: metav1.ConditionTrue`, `Reason: "UserGroupFound"`, `Message: ""`
  2. Use `meta.SetStatusCondition(&user.Status.Conditions, condition)` to set the condition
  3. Update user status with `r.Status().Update(ctx, user)`

  **Part B — filtered findMatchingInboundNodes()** in `internal/controller/user_controller.go`:

  Currently `findMatchingInboundNodes()` returns all SingBoxNodes that support the user's protocol. Update it to also apply UserGroup restrictions:
  1. If `user.Spec.UserGroupRef` is empty → return all protocol-matching nodes (no change)
  2. If `user.Spec.UserGroupRef` is non-empty:
     a. Fetch UserGroup (if not found → return all nodes, fail-open)
     b. Build `allowedSet` and `deniedSet` from UserGroup spec
     c. Filter the node list: only include nodes where `isNodeAllowed(node.Name, allowedSet, deniedSet)` is true
  3. The filtered list is used to update `user.Status.ActiveNodes` — so the status accurately reflects which nodes the user can actually access

  The `isNodeAllowed` function: import from configengine package (it must be exported as `IsNodeAllowed`) or duplicate the logic. Prefer export.

  **Must NOT do**:
  - Do NOT change the existing protocol-matching logic
  - Do NOT fail-closed on missing UserGroup — always fail-open

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: Controller modification with condition management, status update logic
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Tasks 4, 5, 7, 8, 9 — all after Task 3)
  - **Parallel Group**: Wave 2
  - **Blocks**: Tasks 10, 11
  - **Blocked By**: Tasks 2, 3

  **References**:

  **Pattern References**:
  - `internal/controller/user_controller.go` — full file; `findMatchingInboundNodes()` is the function to modify
  - `internal/controller/singboxnode_controller.go:isSingBoxNodeReady()` — fail-open pattern
  - `k8s.io/apimachinery/pkg/api/meta.SetStatusCondition()` — for setting conditions

  **API/Type References**:
  - `api/v1alpha1/usergroup_types.go:UserGroupSpec` — AllowedNodes, DeniedNodes

  **Acceptance Criteria**:
  - [ ] `user_controller.go` compiles with UserGroupNotFound condition logic
  - [ ] `findMatchingInboundNodes()` applies UserGroup filter
  - [ ] `go build ./internal/controller/...` exits 0

  **QA Scenarios**:

  ```
  Scenario: user_controller compiles with condition and filter
    Tool: Bash
    Steps:
      1. Run: go build ./internal/controller/...
      2. Run: grep -n "UserGroupNotFound\|UserGroupReady\|IsNodeAllowed" internal/controller/user_controller.go
    Expected Result: Build succeeds; all 3 identifiers present
    Evidence: .omo/evidence/task-6-user-controller-build.txt
  ```

  **Commit**: YES
  - Message: `feat(controller): add UserGroupNotFound condition and UserGroup filter in user_controller`
  - Files: `internal/controller/user_controller.go`
  - Pre-commit: `go build ./internal/controller/...`

---

- [x] 7. UserGroup controller

  **What to do**:
  - Replace scaffolded `internal/controller/usergroup_controller.go` with full implementation:
    - `UserGroupReconciler` struct with `client.Client` and `Scheme *runtime.Scheme`
    - RBAC markers:
      ```go
      // +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=usergroups,verbs=get;list;watch;create;update;patch;delete
      // +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=usergroups/status,verbs=get;update;patch
      // +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=usergroups/finalizers,verbs=update
      // +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=users,verbs=get;list;watch;patch
      ```
    - `Reconcile()` method:
      1. Fetch UserGroup by NamespacedName; handle not-found (deleted)
      2. Update `status.observedGeneration`
      3. Find all Users in namespace with `spec.userGroupRef == usergroup.Name` (use field index on `spec.userGroupRef`)
      4. For each User, annotate with `singboxoperator.shlande.top/reconcile-trigger: <timestamp>` using `r.Patch()` (NOT `r.Update()`)
      5. Return reconcile.Result{}
    - `SetupWithManager()`: register controller with manager; UserGroup IS the primary resource, no additional watches needed here (the SingBoxNodeReconciler watches UserGroup for the cascade to nodes)

  **Must NOT do**:
  - Do NOT use `r.Update()` — use `r.Patch()` with `client.MergeFrom`
  - Do NOT add a finalizer to UserGroup
  - Do NOT add complex status conditions

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: Controller implementation with field index usage, patch semantics
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Tasks 4, 5, 6, 8, 9 — all after Task 3)
  - **Parallel Group**: Wave 2
  - **Blocks**: Tasks 10, 11
  - **Blocked By**: Tasks 2, 3

  **References**:

  **Pattern References**:
  - `internal/controller/user_controller.go` — entire file (very similar structure)
  - `internal/controller/customroute_controller.go` — simpler controller example
  - `internal/controller/singboxnode_controller.go:triggerNodeReconcile()` — annotation trigger pattern (use Patch not Update)

  **Acceptance Criteria**:
  - [ ] `internal/controller/usergroup_controller.go` compiles with full implementation
  - [ ] RBAC markers include usergroups and users resources
  - [ ] `go build ./internal/controller/...` exits 0

  **QA Scenarios**:

  ```
  Scenario: Controller compiles with correct RBAC
    Tool: Bash
    Steps:
      1. Run: go build ./internal/controller/...
      2. Run: grep -n "rbac:groups" internal/controller/usergroup_controller.go
      3. Verify markers include both usergroups and users
    Expected Result: Build succeeds, RBAC markers present
    Evidence: .omo/evidence/task-7-usergroup-controller-build.txt
  ```

  **Commit**: YES
  - Message: `feat(controller): add UserGroup controller`
  - Files: `internal/controller/usergroup_controller.go`
  - Pre-commit: `go build ./internal/controller/...`

---

- [x] 8. UserGroup webhook + user_webhook.go userGroupRef validation

  **What to do**:
  - Create `internal/webhook/usergroup_webhook.go`:
    - Implement `UserGroupCustomValidator` (validating webhook only, no defaulting)
    - `ValidateCreate()` and `ValidateUpdate()`:
      1. Each name in `AllowedNodes` must be a valid DNS subdomain (`validation.IsDNS1123Subdomain()`)
      2. Each name in `DeniedNodes` must be a valid DNS subdomain
      3. Warn (not error) if a name appears in both lists (deny-wins is valid but unusual — log a warning event)
    - `ValidateDelete()` → return nil
    - `SetupUserGroupWebhookWithManager()` function
  - Edit `internal/webhook/user_webhook.go`:
    - In `ValidateCreate()` and `ValidateUpdate()`, add:
      - If `user.Spec.UserGroupRef` is non-empty, validate it is a valid DNS subdomain name (no spaces, no uppercase, max 253 chars)
      - Do NOT check if the UserGroup actually exists (no client.Client in webhook)

  **Must NOT do**:
  - Do NOT add `client.Client` to any webhook for existence checking
  - Do NOT add defaulting webhook for UserGroup
  - Do NOT block UserGroup deletion

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: Webhook validation follows exact same pattern as existing webhooks
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Tasks 4, 5, 6, 7, 9 — all after Task 3)
  - **Parallel Group**: Wave 2
  - **Blocks**: Tasks 10, 11
  - **Blocked By**: Tasks 2, 3

  **References**:

  **Pattern References**:
  - `internal/webhook/user_webhook.go` — exact pattern to follow
  - `internal/webhook/customroute_webhook.go` — simpler validating-only example
  - `internal/webhook/singboxnode_webhook.go` — more complex validation example

  **External References**:
  - `k8s.io/apimachinery/pkg/util/validation.IsDNS1123Subdomain()` — for name validation

  **Acceptance Criteria**:
  - [ ] `internal/webhook/usergroup_webhook.go` exists and compiles
  - [ ] `internal/webhook/user_webhook.go` validates userGroupRef format
  - [ ] `go build ./internal/webhook/...` exits 0

  **QA Scenarios**:

  ```
  Scenario: Webhook compiles and has required functions
    Tool: Bash
    Steps:
      1. Run: go build ./internal/webhook/...
      2. Run: grep -n "SetupUserGroupWebhookWithManager\|ValidateCreate\|ValidateUpdate" internal/webhook/usergroup_webhook.go
    Expected Result: Build succeeds; all 3 function names present
    Evidence: .omo/evidence/task-8-webhook-build.txt
  ```

  **Commit**: YES
  - Message: `feat(webhook): add UserGroup validation webhook and userGroupRef format validation`
  - Files: `internal/webhook/usergroup_webhook.go`, `internal/webhook/user_webhook.go`
  - Pre-commit: `go build ./internal/webhook/...`

---

- [x] 9. apiserver: handler.go + client_config.go node restriction filtering

  **What to do**:

  **Part A — `internal/apiserver/handler.go` in `handleClientConfig()`**:
  After matching the User by UUID, before building `ClientConfigInput`:
  1. Initialize `allowedNodeNames map[string]bool` and `deniedNodeNames map[string]bool` as nil (no restrictions)
  2. If `user.Spec.UserGroupRef` is non-empty:
     a. Fetch UserGroup: `s.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: user.Spec.UserGroupRef}, &ug)`
     b. If not found → log warning, proceed with nil maps (fail-open)
     c. If found → build `allowedNodeNames` and `deniedNodeNames` from `ug.Spec.AllowedNodes` and `ug.Spec.DeniedNodes`
  3. Add to `ClientConfigInput`: `AllowedNodeNames map[string]bool` and `DeniedNodeNames map[string]bool`

  **Part B — `internal/apiserver/client_config.go`**:
  - Add `AllowedNodeNames map[string]bool` and `DeniedNodeNames map[string]bool` to `ClientConfigInput` struct
  - In `BuildClientConfig()` main loop over inbound nodes (line ~36-61):
    - Before processing each inbound node, call `IsNodeAllowed(inboundNode.Name, input.AllowedNodeNames, input.DeniedNodeNames)`
    - If not allowed → skip this inbound node entirely (no outbound entries for this inbound)
  - In `resolveOutboundNodes()` (line ~106-139):
    - Filter each outbound node: call `IsNodeAllowed(outboundNode.Name, input.AllowedNodeNames, input.DeniedNodeNames)`
    - Skip outbound nodes that are not allowed
    - This handles both same-region outbound nodes AND CustomRoute outbound nodes (they're both resolved here)

  Note: `BuildClientConfig()` must remain a pure function — no `client.Client` added. The UserGroup data is fetched in `handleClientConfig()` and passed via `ClientConfigInput`.

  Note: `IsNodeAllowed` must be exported from configengine package (capital I) for use in apiserver. If it was added as `isNodeAllowed` in Task 4, rename it to `IsNodeAllowed`.

  **Must NOT do**:
  - Do NOT add `client.Client` to `BuildClientConfig()` or `ClientConfigInput`
  - Do NOT filter inbound nodes in `resolveOutboundNodes()` — only outbound nodes
  - Do NOT modify `template.go`

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: Multi-file apiserver changes, careful integration with existing handler pattern
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Tasks 4, 5, 6, 7, 8 — all after Task 3)
  - **Parallel Group**: Wave 2
  - **Blocks**: Tasks 10, 11
  - **Blocked By**: Tasks 2, 3

  **References**:

  **Pattern References**:
  - `internal/apiserver/handler.go:handleClientConfig()` — the function to modify (full function ~lines 22-154)
  - `internal/apiserver/handler.go:90-96` — offline node filtering pattern (analogous to group filtering)
  - `internal/apiserver/client_config.go:BuildClientConfig()` — the pure function to modify
  - `internal/apiserver/client_config.go:ClientConfigInput` — struct to add AllowedNodeNames/DeniedNodeNames
  - `internal/apiserver/client_config.go:resolveOutboundNodes()` — where to filter outbound nodes
  - `internal/configengine/engine.go:IsNodeAllowed()` — function to call (must be exported)

  **Acceptance Criteria**:
  - [ ] `ClientConfigInput` has `AllowedNodeNames map[string]bool` and `DeniedNodeNames map[string]bool`
  - [ ] `handleClientConfig()` fetches UserGroup and populates these fields
  - [ ] `BuildClientConfig()` skips denied inbound nodes
  - [ ] `resolveOutboundNodes()` skips denied outbound nodes (both region-based and CustomRoute)
  - [ ] `go build ./internal/apiserver/...` exits 0

  **QA Scenarios**:

  ```
  Scenario: apiserver compiles with group filtering
    Tool: Bash
    Steps:
      1. Run: go build ./internal/apiserver/...
      2. Run: grep -n "AllowedNodeNames\|DeniedNodeNames\|IsNodeAllowed" internal/apiserver/client_config.go internal/apiserver/handler.go
    Expected Result: Build succeeds; filter identifiers present in both files
    Evidence: .omo/evidence/task-9-apiserver-build.txt
  ```

  **Commit**: YES
  - Message: `feat(apiserver): filter restricted nodes in client config based on UserGroup`
  - Files: `internal/apiserver/handler.go`, `internal/apiserver/client_config.go`
  - Pre-commit: `go build ./internal/apiserver/...`

---

- [x] 10. cmd/main.go registration (controller, webhook, field index)

  **What to do**:
  - Edit `cmd/main.go`:
    1. Register `UserGroupReconciler` with manager (follow existing controller registrations)
    2. Register UserGroup webhook: call `SetupUserGroupWebhookWithManager(mgr)`
    3. Add field index for `spec.userGroupRef` on User objects (needed by `usersInGroupToNodesMapper` in Task 5):
       ```go
       if err := mgr.GetFieldIndexer().IndexField(context.Background(), &proxyv1alpha1.User{}, "spec.userGroupRef", func(rawObj client.Object) []string {
           user := rawObj.(*proxyv1alpha1.User)
           if user.Spec.UserGroupRef == "" {
               return nil
           }
           return []string{user.Spec.UserGroupRef}
       }); err != nil {
           setupLog.Error(err, "unable to create field index for spec.userGroupRef")
           os.Exit(1)
       }
       ```
    4. Verify UserGroup type is registered in scheme (auto-registered via `proxyv1alpha1.AddToScheme` since it's in the same package)
  - Run `go build ./cmd/...` to verify

  **Must NOT do**:
  - Do NOT remove any existing controller or webhook registrations
  - Do NOT change the existing field index for `spec.nodeRef`

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: Registration boilerplate following exact existing patterns
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO (depends on Tasks 4-9 all being complete)
  - **Parallel Group**: Wave 3
  - **Blocks**: Task 11
  - **Blocked By**: Tasks 3, 4, 5, 6, 7, 8, 9

  **References**:

  **Pattern References**:
  - `cmd/main.go` — existing controller/webhook registrations and field index setup (look for `IndexField` and `SetupWithManager` calls)
  - `internal/controller/usergroup_controller.go:SetupWithManager()` — the function to call (Task 7)
  - `internal/webhook/usergroup_webhook.go:SetupUserGroupWebhookWithManager()` — the webhook setup function (Task 8)

  **Acceptance Criteria**:
  - [ ] `cmd/main.go` has UserGroupReconciler registration
  - [ ] `cmd/main.go` has UserGroup webhook registration
  - [ ] `cmd/main.go` has field index for `spec.userGroupRef`
  - [ ] `go build ./cmd/...` exits 0

  **QA Scenarios**:

  ```
  Scenario: Full binary compiles with all registrations
    Tool: Bash
    Steps:
      1. Run: go build ./cmd/...
      2. Run: grep -n "UserGroup\|userGroupRef" cmd/main.go
    Expected Result: Build succeeds; UserGroup appears in registrations
    Evidence: .omo/evidence/task-10-main-build.txt
  ```

  **Commit**: YES
  - Message: `feat(main): register UserGroup controller, webhook, and field index`
  - Files: `cmd/main.go`
  - Pre-commit: `go build ./cmd/...`

---

- [x] 11. Tests for all new code

  **What to do**:

  **A. configengine tests** (add to `internal/configengine/engine_test.go` or new file):
  - Test `IsNodeAllowed()` with all 5 cases:
    1. `IsNodeAllowed("node-b", nil, map{"node-b": true})` → false (deny)
    2. `IsNodeAllowed("node-b", nil, nil)` → true (no restrictions)
    3. `IsNodeAllowed("node-b", map{"node-b": true}, nil)` → true (in allowlist)
    4. `IsNodeAllowed("node-c", map{"node-b": true}, nil)` → false (not in allowlist)
    5. `IsNodeAllowed("node-b", map{"node-b": true}, map{"node-b": true})` → false (deny-wins)
  - Test `Compute()` with `UserNodeRestrictions`:
    - Input: 2 users (alice, bob), 2 outbound nodes (node-b, node-c), alice denied from node-b
    - Assert: `alice#node-b` absent from output config
    - Assert: `alice#node-c` present in output config
    - Assert: `bob#node-b` and `bob#node-c` both present (bob has no restrictions)
  - Test regression: `Compute()` with nil `UserNodeRestrictions` produces same output as before (compare against existing test cases)

  **B. controller tests** (Ginkgo + Gomega + envtest, add to `internal/controller/`):
  - `usergroup_controller_test.go`:
    - Test: Create UserGroup, create User with userGroupRef → User gets annotation trigger within 10s
    - Test: Update UserGroup allowedNodes → SingBoxNode reconciles within 30s
    - Test: Delete UserGroup → User gets `UserGroupNotFound` condition within 10s; user is still active (fail-open)
  - `singboxnode_controller_test.go` (additions):
    - Test: User with UserGroup denying current inbound node → user absent from generated ConfigMap
    - Test: User with UserGroup denying outbound node-b → virtual user `user#node-b` absent, `user#node-c` present
    - Test: User with no UserGroup → all virtual users present (regression)
  - `user_controller_test.go` (additions):
    - Test: User with UserGroup denying node-a → node-a absent from `User.status.activeNodes`
    - Test: User with no UserGroup → all matching nodes in `User.status.activeNodes` (regression)

  **C. webhook tests** (standard Go testing.T, add to `internal/webhook/`):
  - `usergroup_webhook_test.go`:
    - Valid UserGroup with valid node names → no error
    - UserGroup with invalid node name (uppercase) → validation error
    - UserGroup with empty allowedNodes and deniedNodes → no error (valid)
  - `user_webhook_test.go` (additions):
    - User with valid `userGroupRef` format → no error
    - User with invalid `userGroupRef` (spaces) → validation error

  **D. apiserver tests** (add to `internal/apiserver/`):
  - `BuildClientConfig()` with `AllowedNodeNames: map{"node-b": true}` → only node-b outbound present
  - `BuildClientConfig()` with `DeniedNodeNames: map{"node-b": true}` → node-b absent, node-c present
  - `BuildClientConfig()` with nil maps → all outbounds present (regression)
  - `BuildClientConfig()` with denied inbound node → that inbound's outbounds absent

  **E. Run all tests**:
  - `make test` — must exit 0 with all existing tests passing

  **Must NOT do**:
  - Do NOT modify any existing test files (only add new test files or add test cases to existing files)
  - Do NOT write tests requiring a live cluster or kubectl

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: Multi-package test writing, envtest setup, Ginkgo patterns
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO (depends on all implementation tasks)
  - **Parallel Group**: Wave 3 (with Task 10)
  - **Blocks**: F1-F4
  - **Blocked By**: Tasks 4, 5, 6, 7, 8, 9, 10

  **References**:

  **Pattern References**:
  - `internal/configengine/engine_test.go` — standard Go test style for configengine tests (17 existing tests)
  - `internal/controller/user_controller_test.go` — Ginkgo+Gomega+envtest controller test pattern
  - `internal/controller/singboxnode_controller_test.go` — SingBoxNode controller test pattern
  - `internal/webhook/webhook_test.go` — standard Go test style for webhook tests

  **Acceptance Criteria**:
  - [ ] `make test` exits 0
  - [ ] All 17 existing configengine tests still pass
  - [ ] New `IsNodeAllowed` tests pass (5 cases)
  - [ ] New configengine integration test (restricted virtual user absent) passes
  - [ ] New UserGroup controller tests pass
  - [ ] New SingBoxNode controller tests (inbound/outbound restriction) pass
  - [ ] New User controller tests (filtered activeNodes) pass
  - [ ] New webhook tests pass
  - [ ] New apiserver tests pass

  **QA Scenarios**:

  ```
  Scenario: Full test suite passes
    Tool: Bash
    Steps:
      1. Run: make test
      2. Verify: exit code 0
      3. Verify: no "FAIL" in output
      4. Verify: new test names appear (grep for "IsNodeAllowed", "UserGroup")
    Expected Result: make test exits 0, all tests pass
    Evidence: .omo/evidence/task-11-make-test.txt

  Scenario: Existing configengine tests unmodified and passing
    Tool: Bash
    Steps:
      1. Run: go test ./internal/configengine/... -v 2>&1 | grep -E "PASS|FAIL|---"
    Expected Result: 17+ PASS lines, 0 FAIL lines
    Evidence: .omo/evidence/task-11-configengine-tests.txt

  Scenario: Restricted virtual user absent from config (key integration test)
    Tool: Bash (go test)
    Steps:
      1. Run: go test ./internal/configengine/... -run TestRestrictedVirtualUser -v
    Expected Result: Test PASS — alice#node-b absent, alice#node-c present, bob#node-b present
    Evidence: .omo/evidence/task-11-virtual-user-restriction.txt
  ```

  **Commit**: YES
  - Message: `test: add tests for UserGroup node restriction across configengine, controller, webhook, apiserver`
  - Files: new test files in configengine, controller, webhook, apiserver packages
  - Pre-commit: `make test`

---

## Final Verification Wave (MANDATORY — after ALL implementation tasks)

> 4 review agents run in PARALLEL. ALL must APPROVE. Present consolidated results to user and get explicit "okay" before completing.
>
> **Do NOT auto-proceed after verification. Wait for user's explicit approval.**

- [x] F1. **Plan Compliance Audit** — `oracle`
  Read the plan end-to-end. For each "Must Have": verify implementation exists (read file, grep). For each "Must NOT Have": search codebase for forbidden patterns — reject with file:line if found. Check evidence files exist in `.omo/evidence/`. Compare deliverables against plan.
  Specifically check: `configengine.Compute()` signature unchanged, `BuildClientConfig()` has no `client.Client`, `zz_generated.deepcopy.go` was NOT manually edited, virtual user naming unchanged.
  Output: `Must Have [N/N] | Must NOT Have [N/N] | Tasks [N/N] | VERDICT: APPROVE/REJECT`

- [x] F2. **Code Quality Review** — `unspecified-high`
  Run `go build ./...` + `go vet ./...` + `make lint-fix` + `make test`. Review all changed files for: empty error handling, unused imports, commented-out code. Check AI slop: excessive comments, over-abstraction, generic names. Verify `IsNodeAllowed` is properly exported and documented. Verify `UserNodeRestrictions` map is nil-safe.
  Output: `Build [PASS/FAIL] | Vet [PASS/FAIL] | Tests [N pass/N fail] | Files [N clean/N issues] | VERDICT`

- [x] F3. **Real Manual QA** — `unspecified-high`
  Execute EVERY QA scenario from EVERY task. Test integration: create UserGroup + bind User + verify ConfigMap changes. Test edge cases: empty allowedNodes (allow all), deny-wins conflict, missing UserGroup (fail-open), dual-role node restriction (blocked as both inbound AND outbound). Save to `.omo/evidence/final-qa/`.
  Output: `Scenarios [N/N pass] | Integration [N/N] | Edge Cases [N tested] | VERDICT`

- [x] F4. **Scope Fidelity Check** — `deep`
  For each task: read "What to do", read actual diff (git log/diff). Verify 1:1 — everything in spec was built, nothing beyond spec was built. Check "Must NOT do" compliance: verify `Compute()` signature unchanged, `SingBoxNodeSpec` unchanged, no `client.Client` in `BuildClientConfig()`. Flag unaccounted changes.
  Output: `Tasks [N/N compliant] | Contamination [CLEAN/N issues] | Unaccounted [CLEAN/N files] | VERDICT`

---

## Commit Strategy

- **After Tasks 1+2+3**: `feat(api): add UserGroup CRD and userGroupRef field on User` + `chore: regenerate CRDs, RBAC, and DeepCopy for UserGroup`
- **After Task 4**: `feat(configengine): add UserNodeRestrictions to Input and filter restricted users/nodes`
- **After Task 5**: `feat(controller): add UserGroup pre-filter and restriction map in collectInput; add UserGroup watch`
- **After Task 6**: `feat(controller): add UserGroupNotFound condition and UserGroup filter in user_controller`
- **After Task 7**: `feat(controller): add UserGroup controller`
- **After Task 8**: `feat(webhook): add UserGroup validation webhook and userGroupRef format validation`
- **After Task 9**: `feat(apiserver): filter restricted nodes in client config based on UserGroup`
- **After Task 10**: `feat(main): register UserGroup controller, webhook, and field index`
- **After Task 11**: `test: add tests for UserGroup node restriction across configengine, controller, webhook, apiserver`

---

## Success Criteria

### Verification Commands
```bash
make manifests      # Expected: exits 0; config/rbac/role.yaml has usergroups
make generate       # Expected: exits 0; zz_generated.deepcopy.go has UserGroup
go build ./...      # Expected: exits 0
go vet ./...        # Expected: exits 0
make test           # Expected: exits 0, all tests pass
```

### Final Checklist
- [ ] All "Must Have" present (UserGroup CRD, userGroupRef, UserNodeRestrictions, filtering in all 5 code paths, fail-open, conditions, UserGroup watch)
- [ ] All "Must NOT Have" absent (no configengine.Compute() signature change, no SingBoxNodeSpec changes, no client.Client in BuildClientConfig, no finalizer)
- [ ] All tests pass (`make test` exits 0)
- [ ] `make manifests && make generate` exits 0
- [ ] `go build ./...` exits 0
