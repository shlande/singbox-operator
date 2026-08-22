# allowed-outbounds - Work Plan

## TL;DR (For humans)

**What you'll get:** 一个新的入站节点配置项，让你能声明"这个入站节点只能把流量转发给指定的几个出站节点（或它自己）"。设置后，入站节点不会再自动挂载同区域的其它出站节点，只走你指定的。

**Why this approach:** 完全对称复制现有的"出站节点限制入站节点"（AllowedInbounds）功能。两个字段一个管出站侧、一个管入站侧，组合起来是"双方都同意才能连通"的 AND 门，与现有 CustomRoute 的过滤逻辑一致。学习成本为零，向后兼容（不填就是现在的行为）。

**What it will NOT do:** 不改现有 AllowedInbounds 行为；不在 webhook 做跨节点交叉验证（与现状一致，运行时过滤）；不改 UserGroup/CustomRoute 类型；不手动编辑自动生成文件。

**Effort:** Short
**Risk:** Low — 纯对称复制已有模式，无新设计决策
**Decisions to sanity-check:** 非空时统一 gate 所有出站路径（含 CustomRoute），而非"抑制自动发现 + 单独 gate CustomRoute"

Your next move: 批准后执行（`$start-work`），或运行高精度复核。详见下方。

---

> TL;DR (machine): Short, Low risk. Add inbound-side `AllowedOutbounds` whitelist field symmetric to existing `AllowedInbounds`, 9 code change points + 4 test suites + docs, AND-gate semantics.

## Scope
### Must have
- `SingBoxNodeSpec.AllowedOutbounds []string` 新字段，`+listType=set` marker
- 语义：空/省略 = 现状（同 region 自动发现 + CustomRoute，向后兼容）；非空 = 排他白名单，**统一 gate 所有出站路径**（同 region 自动发现 + CustomRoute 都受限于该白名单）
- AND 门：入站 A 用出站 B 需 `(A.AllowedOutbounds 为空 或 包含 B)` 且 `(B.AllowedInbounds 为空 或 包含 A)`
- webhook 校验：非空字符串、去重（对称于 `singboxnode_webhook.go:114-130`）；**不改 `Default` 函数**（对称于 AllowedInbounds，无 defaulting）
- controller `collectInput` 两处 filter：同 region 自动发现段 + CustomRoute 段
- configengine 两处 belt-and-suspenders 防御 filter
- apiserver `resolveOutboundNodes` 两处 filter
- CRD/DeepCopy 重生成（`make manifests generate`）并验证 `x-kubernetes-list-type: set`
- 测试：webhook 5 子用例、controller 5 用例、apiserver、configengine（含 AND 门双非空场景）
- README 新章节 + samples 注释示例

### Must NOT have (guardrails, anti-slop, scope boundaries)
- 不手编 `zz_generated.*`、`config/crd/bases/*`、`config/rbac/role.yaml`、`PROJECT`
- 不删 `// +kubebuilder:scaffold:*` 标记
- 不改 `AllowedInbounds` 语义或字段
- 不改 `UserGroup`、`CustomRoute` 类型
- 不在 webhook 做 cross-field 跨节点验证（与现有 AllowedInbounds 模式一致，运行时过滤）
- 不在 webhook `Default` 函数加 defaulting（对称于 AllowedInbounds）
- 不新增 CRD kind
- 不改 `naive` 协议的 `username` override 逻辑

## Verification strategy
> Zero human intervention - all verification is agent-executed.
- Test decision: tests-after（对称复制现有测试模式）+ Ginkgo/Gomega（controller）、testing（webhook/apiserver/configengine）
- Evidence: .omo/evidence/task-<N>-allowed-outbounds.<ext>
- 关键验证命令：`make manifests generate && make lint-fix && make test`

## Execution strategy
### Parallel execution waves
- Wave 1: task 1 (types) — 阻塞所有后续
- Wave 2: tasks 2-10 (实现 + samples/README) — 全部依赖 task 1，互相独立可并行
- Wave 3: task 11 (manifests generate, 依赖 1-8) + tasks 12-15 (测试, 依赖各自实现 task)
- Wave 4: task 16 (全量测试) — 依赖所有测试 task

### Dependency matrix
| Todo | Depends on | Blocks | Can parallelize with |
| --- | --- | --- | --- |
| 1 (types) | — | 2-15 | — |
| 2 (webhook) | 1 | 12 | 3-11,15 |
| 3 (controller same-region) | 1 | 13 | 2,4-11,15 |
| 4 (controller customroute) | 1 | 13 | 2,3,5-11,15 |
| 5 (configengine outbounds) | 1 | 14 | 2-4,6-11,15 |
| 6 (configengine routes) | 1 | 14 | 2-5,7-11,15 |
| 7 (apiserver same-region) | 1 | 15 | 2-6,8-14 |
| 8 (apiserver customroute) | 1 | 15 | 2-7,9-14 |
| 9 (samples) | 1 | — | 2-8,10,11,15 |
| 10 (README) | 1 | — | 2-9,11,15 |
| 11 (manifests generate) | 1-8 | 16 | 9,10,15 |
| 12 (webhook tests) | 2 | 16 | 9,10,11,13,14,15 |
| 13 (controller tests) | 3,4 | 16 | 9,10,11,12,14,15 |
| 14 (configengine tests) | 5,6 | 16 | 9,10,11,12,13,15 |
| 15 (apiserver tests) | 7,8 | 16 | 9,10,11,12,13,14 |
| 16 (full test suite) | 11-15 | — | — |

## Todos
> Implementation + Test = ONE todo. Never separate.
<!-- APPEND TASK BATCHES BELOW THIS LINE WITH edit/apply_patch - never rewrite the headers above. -->

- [x] 1. 新增 `AllowedOutbounds` 字段到 SingBoxNodeSpec
  What to do / Must NOT do: 在 `api/v1alpha1/singboxnode_types.go` 的 `SingBoxNodeSpec` 中，紧跟现有 `AllowedInbounds` 字段（第 90 行后）新增 `AllowedOutbounds` 字段。字段定义对称复制 `AllowedInbounds`（第 85-90 行）但语义改为入站侧。注释明确：当设置在入站节点上时，限制该入站节点可用的出站节点；空=允许所有（向后兼容）；非空=统一 gate 所有出站路径（同 region 自动发现 + CustomRoute）。`+listType=set` marker 必须保留。Must NOT: 不改 AllowedInbounds 字段；不加 defaulting marker。
  Parallelization: Wave 1 | Blocked by: 无 | Blocks: 2-15
  References (executor has NO interview context - be exhaustive):
  - `api/v1alpha1/singboxnode_types.go:85-90` (现有 AllowedInbounds 字段，对称模板)
  - `api/v1alpha1/singboxnode_types.go:54-97` (SingBoxNodeSpec 全貌)
  - 字段模板（紧接第 90 行后插入）:
    ```go
    // AllowedOutbounds, when set on an inbound node, restricts which outbound nodes
    // it may use as upstream. Empty means allow all (backward compatible).
    // When non-empty, ALL outbound paths (same-region auto-discovery AND CustomRoute
    // bindings) are gated by this whitelist — only outbounds whose names appear here
    // are usable. May include the node's own name when it has both inbound+outbound roles.
    // Only meaningful for nodes with the inbound role.
    // +optional
    // +listType=set
    AllowedOutbounds []string `json:"allowedOutbounds,omitempty"`
    ```
  Acceptance criteria (agent-executable): `go build ./...` 成功；`grep -n "AllowedOutbounds" api/v1alpha1/singboxnode_types.go` 返回新字段行。
  QA scenarios (go build): happy: go build 通过; failure: 字段缺失则 build 失败。Evidence .omo/evidence/task-1-allowed-outbounds.txt
  Commit: Y | feat(api): add AllowedOutbounds field to SingBoxNodeSpec

- [x] 2. webhook 校验 AllowedOutbounds
  What to do / Must NOT do: 在 `internal/webhook/singboxnode_webhook.go` 的 `validateSingBoxNode` 函数中，紧跟现有 `AllowedInbounds` 校验块（第 114-130 行）后，新增 `AllowedOutbounds` 校验块。逻辑完全对称复制：校验空串条目、去重。错误路径用 `field.NewPath("spec", "allowedOutbounds").Index(i)`。Must NOT: 不改 `Default` 函数（第 37-39 行保持 nil 返回）；不做 cross-field 跨节点验证；不改现有 AllowedInbounds 校验。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 12
  References:
  - `internal/webhook/singboxnode_webhook.go:114-130` (现有 AllowedInbounds 校验，对称模板)
  - 新增代码模板（紧接第 130 行后，`if len(allErrs) > 0` 之前插入）:
    ```go
    seenAllowedOutbounds := make(map[string]bool)
    for i, entry := range node.Spec.AllowedOutbounds {
        if entry == "" {
            allErrs = append(allErrs, field.Invalid(
                field.NewPath("spec", "allowedOutbounds").Index(i),
                entry,
                "allowedOutbounds entry must not be empty",
            ))
        }
        if seenAllowedOutbounds[entry] {
            allErrs = append(allErrs, field.Duplicate(
                field.NewPath("spec", "allowedOutbounds").Index(i),
                entry,
            ))
        }
        seenAllowedOutbounds[entry] = true
    }
    ```
  Acceptance criteria: `go build ./...` 成功；`go vet ./internal/webhook/...` 通过。
  QA scenarios: happy: 合法值通过; failure: 空串/重复被拒。Evidence .omo/evidence/task-2-allowed-outbounds.txt
  Commit: N (随 task 12 测试一起提交)

- [x] 3. controller collectInput 同 region 自动发现段 filter
  What to do / Must NOT do: 在 `internal/controller/singboxnode_controller.go` 的 `collectInput` 函数，同 region 自动发现段（第 213-219 行），在现有 `AllowedInbounds` 检查（第 215 行）之后、`input.OutboundNodes = append`（第 219 行）之前，新增 `AllowedOutbounds` 检查：若 `node.Spec.AllowedOutbounds` 非空且不包含 `other.Name`，则 skip（log + continue）。语义：非空 AllowedOutbounds 统一 gate 所有出站路径。Must NOT: 不改现有 AllowedInbounds 检查逻辑；不改 CustomRoute 段（task 4 处理）。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 13
  References:
  - `internal/controller/singboxnode_controller.go:213-219` (现有同 region 段，含 AllowedInbounds 检查模板)
  - 新增代码（在第 218 行 `continue` 之后、第 219 行 `input.OutboundNodes = append` 之前插入）:
    ```go
    if len(node.Spec.AllowedOutbounds) > 0 && !slices.Contains(node.Spec.AllowedOutbounds, other.Name) {
        log.Info("Skipping outbound node due to allowedOutbounds whitelist", "outboundNode", other.Name, "inboundNode", node.Name)
        continue
    }
    ```
  Acceptance criteria: `go build ./...` 成功。
  QA scenarios: happy: 非空 AllowedOutbounds 包含 other.Name 时收集; failure: 不包含时 skip。Evidence .omo/evidence/task-3-allowed-outbounds.txt
  Commit: N (随 task 13 测试一起提交)

- [x] 4. controller collectInput CustomRoute 段 filter
  What to do / Must NOT do: 在 `internal/controller/singboxnode_controller.go` 的 `collectInput` 函数，CustomRoute 段（第 294-317 行），在现有 `AllowedInbounds` 检查（第 304 行）之后、`input.Routes = append`（第 308 行）之前，新增 `AllowedOutbounds` 检查：若 `node.Spec.AllowedOutbounds` 非空且不包含 `outboundNode.Name`，则 skip。语义统一：CustomRoute 也被 AllowedOutbounds gate。Must NOT: 不改现有 AllowedInbounds 检查。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 13
  References:
  - `internal/controller/singboxnode_controller.go:294-317` (现有 CustomRoute 段)
  - `internal/controller/singboxnode_controller.go:303-307` (现有 AllowedInbounds 检查模板)
  - 新增代码（在第 307 行 `continue` 之后、第 308 行 `input.Routes = append` 之前插入）:
    ```go
    if len(node.Spec.AllowedOutbounds) > 0 && !slices.Contains(node.Spec.AllowedOutbounds, outboundNode.Name) {
        log.Info("Skipping CustomRoute due to allowedOutbounds whitelist", "route", route.Name, "outboundNode", outboundNode.Name, "inboundNode", node.Name)
        continue
    }
    ```
  Acceptance criteria: `go build ./...` 成功。
  QA scenarios: happy: CustomRoute 目标在白名单时收集; failure: 不在白名单时 skip。Evidence .omo/evidence/task-4-allowed-outbounds.txt
  Commit: N (随 task 13 测试一起提交)

- [x] 5. configengine buildOutboundNodeOutbounds 防御 filter
  What to do / Must NOT do: 在 `internal/configengine/engine.go` 的 `buildOutboundNodeOutbounds` 函数（第 447-480 行），在现有 `AllowedInbounds` 防御检查（第 463-465 行）之后、`if outNode.Spec.RelayPort == 0`（第 466 行）之前，新增 `AllowedOutbounds` 防御检查：若 `input.Node.Spec.AllowedOutbounds` 非空且不包含 `outNode.Name`，则 continue。belt-and-suspenders 模式，对称于现有 AllowedInbounds 防御。Must NOT: 不改现有 AllowedInbounds 防御；不改 buildRouteOutbounds（task 6 处理）。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 14
  References:
  - `internal/configengine/engine.go:447-480` (buildOutboundNodeOutbounds 全函数)
  - `internal/configengine/engine.go:460-465` (现有 AllowedInbounds 防御模板，注释说明 belt-and-suspenders)
  - 新增代码（在第 465 行 `continue` 之后、第 466 行 `if outNode.Spec.RelayPort == 0` 之前插入）:
    ```go
    // Defensive: skip outbound nodes not in this inbound node's AllowedOutbounds whitelist.
    // Controller already filters in collectInput; this is a belt-and-suspenders guard
    // matching the AllowedInbounds defensive pattern above.
    if len(input.Node.Spec.AllowedOutbounds) > 0 && !slices.Contains(input.Node.Spec.AllowedOutbounds, outNode.Name) {
        continue
    }
    ```
  Acceptance criteria: `go build ./...` 成功。
  QA scenarios: happy: 白名单包含时生成 outbound; failure: 不包含时跳过。Evidence .omo/evidence/task-5-allowed-outbounds.txt
  Commit: N (随 task 14 测试一起提交)

- [x] 6. configengine buildRouteOutbounds 防御 filter
  What to do / Must NOT do: 在 `internal/configengine/engine.go` 的 `buildRouteOutbounds` 函数（第 482-522 行），在现有 `AllowedInbounds` 防御检查（第 495-497 行）之后、`if len(input.Users) > 0`（第 499 行）之前，新增 `AllowedOutbounds` 防御检查：若 `input.Node.Spec.AllowedOutbounds` 非空且不包含 `outNode.Name`，则 continue。Must NOT: 不改现有 AllowedInbounds 防御。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 14
  References:
  - `internal/configengine/engine.go:482-522` (buildRouteOutbounds 全函数)
  - `internal/configengine/engine.go:492-497` (现有 AllowedInbounds 防御模板)
  - 新增代码（在第 497 行 `continue` 之后、第 498 行注释 `// Skip outbound entries...` 之前插入）:
    ```go
    // Defensive: skip outbound nodes not in this inbound node's AllowedOutbounds whitelist.
    // Controller already filters CustomRoutes in collectInput; belt-and-suspenders.
    if len(input.Node.Spec.AllowedOutbounds) > 0 && !slices.Contains(input.Node.Spec.AllowedOutbounds, outNode.Name) {
        continue
    }
    ```
  Acceptance criteria: `go build ./...` 成功。
  QA scenarios: happy: 白名单包含时生成 route outbound; failure: 不包含时跳过。Evidence .omo/evidence/task-6-allowed-outbounds.txt
  Commit: N (随 task 14 测试一起提交)

- [x] 7. apiserver resolveOutboundNodes 同 region 段 filter
  What to do / Must NOT do: 在 `internal/apiserver/client_config.go` 的 `resolveOutboundNodes` 函数，同 region 自动发现段（第 127-140 行），在现有 `configengine.IsNodeAllowed` 调用（第 130 行）的条件中，追加 `AllowedOutbounds` 检查。修改第 129 行的 if 条件，增加 `&& (len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, n.Name))`。语义统一：非空 AllowedOutbounds gate 同 region 自动发现。Must NOT: 不改 CustomRoute 段（task 8 处理）；不改 `BuildClientConfig` 主循环。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 15
  References:
  - `internal/apiserver/client_config.go:115-154` (resolveOutboundNodes 全函数)
  - `internal/apiserver/client_config.go:127-140` (同 region 段)
  - 修改第 129 行 if 条件，从:
    ```go
    if n.Spec.Region == inboundNode.Spec.Region && !seen[n.Name] && !input.OfflineNodeNames[n.Name] &&
        configengine.IsNodeAllowed(n.Name, input.AllowedNodeNames, input.DeniedNodeNames) {
    ```
    改为:
    ```go
    if n.Spec.Region == inboundNode.Spec.Region && !seen[n.Name] && !input.OfflineNodeNames[n.Name] &&
        configengine.IsNodeAllowed(n.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
        (len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, n.Name)) {
    ```
  - 注意：`slices` 包已在第 6 行 import（`client_config.go:6`），无需新增 import。
  Acceptance criteria: `go build ./...` 成功。
  QA scenarios: happy: 白名单包含时出现在 client config; failure: 不包含时缺席。Evidence .omo/evidence/task-7-allowed-outbounds.txt
  Commit: N (随 task 15 测试一起提交)

- [x] 8. apiserver resolveOutboundNodes CustomRoute 段 filter
  What to do / Must NOT do: 在 `internal/apiserver/client_config.go` 的 `resolveOutboundNodes` 函数，CustomRoute 段（第 142-148 行），在现有 `configengine.IsNodeAllowed` 调用（第 144 行）的条件中，追加 `AllowedOutbounds` 检查。语义统一：CustomRoute 也被 AllowedOutbounds gate。Must NOT: 不改同 region 段（task 7 处理）。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 15
  References:
  - `internal/apiserver/client_config.go:142-148` (CustomRoute 段)
  - 修改第 143 行 if 条件，从:
    ```go
    if n, ok := input.OutboundsByName[r.Spec.OutboundNode]; ok && !seen[n.Name] && !input.OfflineNodeNames[n.Name] &&
        configengine.IsNodeAllowed(n.Name, input.AllowedNodeNames, input.DeniedNodeNames) {
    ```
    改为:
    ```go
    if n, ok := input.OutboundsByName[r.Spec.OutboundNode]; ok && !seen[n.Name] && !input.OfflineNodeNames[n.Name] &&
        configengine.IsNodeAllowed(n.Name, input.AllowedNodeNames, input.DeniedNodeNames) &&
        (len(inboundNode.Spec.AllowedOutbounds) == 0 || slices.Contains(inboundNode.Spec.AllowedOutbounds, n.Name)) {
    ```
  Acceptance criteria: `go build ./...` 成功。
  QA scenarios: happy: CustomRoute 目标在白名单时出现; failure: 不在时缺席。Evidence .omo/evidence/task-8-allowed-outbounds.txt
  Commit: N (随 task 15 测试一起提交)

- [x] 9. samples 注释示例
  What to do / Must NOT do: 在 `config/samples/singboxoperator_v1alpha1_singboxnode.yaml` 现有 `allowedInbounds` 注释示例（第 11-15 行）之后，新增 `allowedOutbounds` 注释示例。对称于现有注释风格。Must NOT: 不取消注释（保持注释状态作为示例）；不改现有 allowedInbounds 注释。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 无
  References:
  - `config/samples/singboxoperator_v1alpha1_singboxnode.yaml:11-15` (现有 allowedInbounds 注释模板)
  - 新增内容（在第 15 行后追加）:
    ```yaml

      # # Example: restrict which outbound nodes this inbound node may use.
      # # Empty or omitted = allow all same-region outbounds (backward compatible).
      # # When set, ALL outbound paths (same-region auto-discovery AND CustomRoute)
      # # are gated by this whitelist.
      # allowedOutbounds:
      #   - "my-outbound-node"
    ```
  Acceptance criteria: `grep -A6 "allowedOutbounds" config/samples/singboxoperator_v1alpha1_singboxnode.yaml` 返回注释块。
  QA scenarios: happy: 注释存在且格式正确。Evidence .omo/evidence/task-9-allowed-outbounds.txt
  Commit: Y | docs(samples): add AllowedOutbounds comment example

- [x] 10. README 新增 AllowedOutbounds 章节
  What to do / Must NOT do: 在 `README.md` 现有 `### AllowedInbounds` 章节（第 19-49 行）之后，新增 `### AllowedOutbounds` 章节。内容对称：说明字段位置（入站节点）、语义（空=允许所有向后兼容；非空=排他白名单，统一 gate 同 region 自动发现 + CustomRoute）、AND 门与 AllowedInbounds 的交互、self-as-outbound 示例。更新 README 顶部第 7 行的描述段落，提及 AllowedOutbounds。Must NOT: 不改现有 AllowedInbounds 章节；不改其它段落。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 无
  References:
  - `README.md:7` (顶部描述，提及 AllowedInbounds)
  - `README.md:19-49` (现有 ### AllowedInbounds 章节，对称模板)
  - `README.md:49` (CustomRoute 交互段落，AND 门描述模板)
  - 新增章节内容（在第 49 行 CustomRoute 交互段落之后插入），包含：`### AllowedOutbounds` 标题、字段说明（empty=allow all backward compatible；non-empty=exclusive whitelist gating all outbound paths）、YAML 示例（入站节点 `us-west-inbound-a` 设 `allowedOutbounds: ["us-west-outbound"]`）、`### AllowedOutbounds interaction with AllowedInbounds` 子节（AND 门说明）、`### Self-as-outbound` 子节（双角色节点 `allowedOutbounds: ["<self-name>"]` 说明）
  - 同时更新第 7 行顶部描述，在 "A key access-control feature is **AllowedInbounds**" 后追加 "and **AllowedOutbounds** (inbound-side whitelist of permitted outbounds)"
  Acceptance criteria: `grep -n "AllowedOutbounds" README.md` 返回新增章节标题及内容。
  QA scenarios: happy: 章节存在且对称。Evidence .omo/evidence/task-10-allowed-outbounds.txt
  Commit: Y | docs(readme): add AllowedOutbounds section

- [x] 11. manifests 生成 + CRD marker 验证
  What to do / Must NOT do: 运行 `make manifests generate` 重生成 CRD/DeepCopy/RBAC。然后验证生成的 CRD 包含 `allowedOutbounds` 字段且 `x-kubernetes-list-type: set` marker 正确。Must NOT: 不手编 `config/crd/bases/*.yaml`、`config/rbac/role.yaml`、`zz_generated.*.go`。
  Parallelization: Wave 3 | Blocked by: 1,2,3,4,5,6,7,8 | Blocks: 16
  References:
  - `Makefile` (`manifests` 和 `generate` target)
  - `config/crd/bases/singboxoperator.shlande.top_singboxnodes.yaml` (生成目标，验证 allowedOutbounds + listType=set)
  - `api/v1alpha1/zz_generated.deepcopy.go` (生成目标，验证 DeepCopySlice for AllowedOutbounds)
  Acceptance criteria (agent-executable):
  - `make manifests` 成功退出 0
  - `make generate` 成功退出 0
  - `grep -c "allowedOutbounds" config/crd/bases/singboxoperator.shlande.top_singboxnodes.yaml` 返回 ≥1
  - `grep -A2 "allowedOutbounds" config/crd/bases/singboxoperator.shlande.top_singboxnodes.yaml` 显示 `x-kubernetes-list-type: set`
  - `grep "AllowedOutbounds" api/v1alpha1/zz_generated.deepcopy.go` 返回 DeepCopySlice 相关行
  QA scenarios: happy: 生成成功且 marker 正确; failure: marker 缺失或生成失败。Evidence .omo/evidence/task-11-allowed-outbounds.txt
  Commit: Y | chore(manifests): regenerate CRD and DeepCopy for AllowedOutbounds

- [x] 12. webhook 测试（5 子用例对称）
  What to do / Must NOT do: 在 `internal/webhook/webhook_test.go` 现有 AllowedInbounds 测试块（第 278-362 行）之后，新增 5 个对称子测试：(1) accepts AllowedOutbounds with valid entry；(2) rejects empty string entry；(3) rejects duplicate entries；(4) accepts non-existent node name；(5) accepts nil/empty (backward compat)。完全对称复制现有测试结构，仅替换字段名 `AllowedInbounds`→`AllowedOutbounds`、错误路径 `allowedInbounds`→`allowedOutbounds`。Must NOT: 不改现有 AllowedInbounds 测试。
  Parallelization: Wave 3 | Blocked by: 2 | Blocks: 16
  References:
  - `internal/webhook/webhook_test.go:278-362` (现有 5 个 AllowedInbounds 子测试，对称模板)
  - 测试结构：每个子测试用 `t.Run(...)`，构造 `SingBoxNode{Spec: SingBoxNodeSpec{...}}`，调用 `wh.ValidateCreate`，断言 error 有/无。
  Acceptance criteria (agent-executable): `go test ./internal/webhook/... -run TestSingBoxNodeWebhook -v` 通过，5 个新子测试全 PASS。
  QA scenarios: happy: 5 子测试 PASS; failure: 校验逻辑错误时 FAIL。Evidence .omo/evidence/task-12-allowed-outbounds.txt
  Commit: Y | test(webhook): add AllowedOutbounds validation tests

- [x] 13. controller 测试（5 用例对称）
  What to do / Must NOT do: 在 `internal/controller/singboxnode_controller_test.go` 现有 AllowedInbounds 用例（第 471-856 行，a-e2）之后，新增对称用例：(a) AllowedOutbounds 空=收集所有同 region 出站；(b) AllowedOutbounds 包含入站自身=收集该出站；(c) AllowedOutbounds 排除该出站=skip；(d) CustomRoute 被 AllowedOutbounds gate；(e) AllowedOutbounds 变更触发重新 reconcile。用 Ginkgo `It(...)` 风格，对称复制现有用例结构。Must NOT: 不改现有 AllowedInbounds 用例。
  Parallelization: Wave 3 | Blocked by: 3,4 | Blocks: 16
  References:
  - `internal/controller/singboxnode_controller_test.go:471-856` (现有 a-e2 用例，对称模板)
  - `internal/controller/singboxnode_controller_test.go:471-526` (用例 a 模板)
  - `internal/controller/singboxnode_controller_test.go:527-583` (用例 b 模板)
  - `internal/controller/singboxnode_controller_test.go:584-640` (用例 c 模板)
  - `internal/controller/singboxnode_controller_test.go:641-705` (用例 d 模板)
  - `internal/controller/singboxnode_controller_test.go:706-856` (用例 e/e2 模板)
  Acceptance criteria (agent-executable): `go test ./internal/controller/... -run TestSingBoxNodeReconciler -v` 通过，新用例全 PASS。
  QA scenarios: happy: 5 用例 PASS; failure: filter 逻辑错误时 FAIL。Evidence .omo/evidence/task-13-allowed-outbounds.txt
  Commit: Y | test(controller): add AllowedOutbounds filter tests

- [x] 14. configengine 测试（含 AND 门双非空场景）
  What to do / Must NOT do: 在 `internal/configengine/engine_test.go` 新增测试用例：(1) AllowedOutbounds 空时生成所有同 region outbound（回归）；(2) AllowedOutbounds 非空时只生成白名单内 outbound（同 region 自动发现被抑制）；(3) AllowedOutbounds 非空且 CustomRoute 目标不在白名单时该 route outbound 不生成；(4) **AND 门双非空场景**：入站 A `allowedOutbounds: ["B"]`，出站 B `allowedInbounds: ["A"]` → 连通；入站 A `allowedOutbounds: ["B"]`，出站 B `allowedInbounds: ["C"]`（不含 A）→ 断开；(5) self-as-outbound：双角色节点 `allowedOutbounds: ["<self>"]` 只生成自身 outbound。使用现有 `makeNode`/`makeRoute` helper（第 15-43 行）。Must NOT: 不改现有测试。
  Parallelization: Wave 3 | Blocked by: 5,6 | Blocks: 16
  References:
  - `internal/configengine/engine_test.go:15-43` (makeNode/makeRoute helper)
  - `internal/configengine/engine_test.go:46-80` (parseConfig/inboundsOf/outboundsOf helper)
  - `internal/configengine/engine.go:447-522` (被测函数 buildOutboundNodeOutbounds/buildRouteOutbounds)
  - `internal/configengine/engine.go:463-465,495-497` (现有 AllowedInbounds 防御，AND 门参考)
  Acceptance criteria (agent-executable): `go test ./internal/configengine/... -v` 通过，新用例全 PASS，含 AND 门双非空场景。
  QA scenarios: happy: 5 场景 PASS; failure: filter 或 AND 门逻辑错误时 FAIL。Evidence .omo/evidence/task-14-allowed-outbounds.txt
  Commit: Y | test(configengine): add AllowedOutbounds filter and AND-gate tests

- [x] 15. apiserver 测试
  What to do / Must NOT do: 在 `internal/apiserver/node_restriction_test.go` 新增 table 风格子测试，对称于现有 `TestBuildClientConfig_WithNodeRestrictions` 结构。场景：(1) 入站节点设 `allowedOutbounds: ["B"]`，client config 只含 B 出站，不含同 region 的 C；(2) 入站节点 `allowedOutbounds` 空，client config 含所有同 region 出站（回归）；(3) 入站节点 `allowedOutbounds: ["A"]`（self），双角色节点 A 的 client config 只含自身出站。Must NOT: 不改现有测试。
  Parallelization: Wave 3 | Blocked by: 7,8 | Blocks: 16
  References:
  - `internal/apiserver/node_restriction_test.go:1-126` (现有 TestBuildClientConfig_WithNodeRestrictions，对称模板)
  - `internal/apiserver/node_restriction_test.go:14-26` (makeInboundNode/makeOutboundNode helper)
  - `internal/apiserver/client_config.go:37-86` (BuildClientConfig 主函数)
  - `internal/apiserver/client_config.go:115-154` (resolveOutboundNodes 被测函数)
  Acceptance criteria (agent-executable): `go test ./internal/apiserver/... -run TestBuildClientConfig -v` 通过，新子测试全 PASS。
  QA scenarios: happy: 3 场景 PASS; failure: filter 逻辑错误时 FAIL。Evidence .omo/evidence/task-15-allowed-outbounds.txt
  Commit: Y | test(apiserver): add AllowedOutbounds client config tests

- [x] 16. 全量测试 + lint + 向后兼容验证
  What to do / Must NOT do: 运行全量验证套件：(1) `make lint-fix`；(2) `make test`（含 envtest）；(3) 向后兼容 grep 验证：`grep -rn "AllowedOutbounds" internal/ api/` 确认所有预期位置（types/webhook/controller/configengine/apiserver）有匹配，无遗漏；(4) 确认未触碰 Must NOT have 清单（`git diff --name-only` 不含 `zz_generated.*`、`config/crd/bases/*`、`config/rbac/role.yaml` 的手编，只含由 `make manifests generate` 产生的变更）。Must NOT: 不手编自动生成文件。
  Parallelization: Wave 4 | Blocked by: 11,12,13,14,15 | Blocks: 无
  References:
  - `Makefile` (lint-fix, test target)
  - 所有前序 task 的产出
  Acceptance criteria (agent-executable):
  - `make lint-fix` 退出 0
  - `make test` 退出 0，所有测试 PASS
  - `grep -rn "AllowedOutbounds" internal/ api/` 返回匹配在：singboxnode_types.go、singboxnode_webhook.go、singboxnode_controller.go (×2)、engine.go (×2)、client_config.go (×2)
  - `git diff --name-only` 不含手编的 `zz_generated.*`、`config/crd/bases/*`、`config/rbac/role.yaml`（这些只应由 `make manifests generate` 产生 diff）
  QA scenarios: happy: 全量通过; failure: 任何测试失败或 lint 错误。Evidence .omo/evidence/task-16-allowed-outbounds.txt
  Commit: Y | chore: run full test suite and lint for AllowedOutbounds

## Final verification wave
> Runs in parallel after ALL todos. ALL must APPROVE. Surface results and wait for the user's explicit okay before declaring complete.
- [x] F1. Plan compliance audit — 所有 16 个 todo 完成，每个有 evidence 文件
- [x] F2. Code quality review — `make lint-fix` 通过，无新增 lint 警告
- [x] F3. Real manual QA — `make test` 全量通过；`make manifests generate` 后 CRD 含 `allowedOutbounds` + `x-kubernetes-list-type: set`
- [x] F4. Scope fidelity — 未触碰 Must NOT have 清单中任何文件/语义

## Commit strategy
- 每个 Wave 内的独立改动单独原子提交（task 1、task 9、task 10、task 11、tasks 12-15 各自提交）
- 实现类 task（2-8）随其对应测试 task 一起提交（2+12、3+4+13、5+6+14、7+8+15）
- 提交信息前缀：`feat(api)`、`feat(controller)`、`feat(configengine)`、`feat(apiserver)`、`test(...)`、`docs(...)`、`chore(manifests)`

## Success criteria
1. `go build ./...` 成功
2. `make manifests generate` 成功，CRD 含 `allowedOutbounds` 字段且 `x-kubernetes-list-type: set`
3. `make test` 全量通过，含新增的 webhook/controller/configengine/apiserver 测试
4. README 含 `### AllowedOutbounds` 章节
5. samples 含 `allowedOutbounds` 注释示例
6. `grep -rn "AllowedOutbounds" internal/ api/` 在预期位置返回匹配，无遗漏
