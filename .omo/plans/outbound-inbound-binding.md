# outbound-inbound-binding - Work Plan

## TL;DR (For humans)
<!-- Fill this LAST, after the detailed plan below is written, so it summarizes the REAL plan. -->

**What you'll get:** 定义一个出站节点 A 后，可以指定只有某些入站节点（如 B）能把 A 当作出站使用；未指定则保持现有"同 region 自动配对"行为不变。

**Why this approach:** 在 A（出站侧）反向声明 `allowedInbounds` 列表，A 的所有者一眼可见谁能用它，且单一来源。空列表 = 允许所有（完全向后兼容），非空 = 只允许列出的入站节点。

**What it will NOT do:**
- 不引入 deniedInbounds（只做白名单）
- 不改变 CustomRoute 的加法语义
- 不移除同 region 自动配对
- 不新增 CRD
- 不在入站侧加字段

**Effort:** Short
**Risk:** Low - 加字段 + 过滤逻辑，空值完全向后兼容，不破坏存量配置
**Decisions to sanity-check:** 4 个 fork 均已用户确认（outbound 侧声明 / 保留自动配对 / 仅 allowlist / CustomRoute 保持加法）

Your next move: approve the plan, or run a high-accuracy review first. Full execution detail follows below.

---

> TL;DR (machine): Short, Low risk - add AllowedInbounds []string to SingBoxNodeSpec, filter in collectInput + configengine defensive, extend reconcile trigger for cross-region CustomRoutes, validate in webhook, TDD with Ginkgo covering 7 scenarios.

## Scope
### Must have
- `SingBoxNodeSpec.AllowedInbounds []string` 字段，`+optional` + `+listType=set`，每项 `+kubebuilder:validation:MinLength=1`
- `make manifests generate` 重新生成 CRD 与 deepcopy
- `collectInput`（singboxnode_controller.go）中：当 B（入站）收集 OutboundNodes 时，对每个候选出站节点 A 检查 `A.Spec.AllowedInbounds`，若非空且 B.Name 不在其中则跳过 A
- configengine `buildOutboundNodeOutbounds` 与 `buildRouteOutbounds` 中加防御性过滤（与现有 UserNodeRestrictions 防御模式一致）
- webhook 校验 `AllowedInbounds` 项为非空字符串；对不存在的入站节点名只发 warning 不报错（节点可任意顺序创建）
- TDD 测试覆盖：(a) 空 AllowedInbounds 向后兼容 (b) 非空限制生效 (c) 跨 region 不变 (d) CustomRoute 与绑定共存 (e) self-outbound（同节点入+出）不受影响

### Must NOT have (guardrails, anti-slop, scope boundaries)
- 不加 deniedInbounds
- CustomRoute 仍是"加法"语义（不改为排他），但**现在受 outbound 的 AllowedInbounds gate 约束**——这是一次微小的语义变化：CustomRoute B→A 在 A.AllowedInbounds 不含 B 时会被跳过并打 warning log。这是有意为之（binding 作为 deny-win gate），与 fork 4"CustomRoute 保持加法不变、绑定独立工作"一致：加法不变，但绑定是独立闸门
- 不移除同 region 自动配对
- 不改 UserGroup 语义
- 不新增 CRD
- 不在入站侧加字段
- 不把 AllowedInblocks 设为 required
- 不在 collectInput 中 silently 吞掉 outbound 节点而不打日志（必须 log.Info 记录被绑定排除的节点）
- 不在 webhook 中校验 AllowedInbounds 引用的入站节点是否存在（节点可任意顺序创建）

## Verification strategy
> Zero human intervention - all verification is agent-executed.
- Test decision: TDD + Ginkgo/envtest（项目既有约定，见 internal/controller/suite_test.go）
- Evidence: .omo/evidence/task-{N}-outbound-inbound-binding.{txt|json}

## Execution strategy
### Parallel execution waves
- Wave 1: API 字段 + 生成（task 1） — 必须先做，后续依赖 CRD 字段
- Wave 2: controller 过滤 + configengine 防御 + webhook 校验（task 2/3/4 可并行，但 task 2 是核心）
- Wave 3: 集成测试 + 文档（task 5/6）

### Dependency matrix
| Todo | Depends on | Blocks | Can parallelize with |
| --- | --- | --- | --- |
| 1 API field + generate | - | 2,3,4 | - |
| 2 controller filter | 1 | 5 | 3,4 |
| 3 configengine defensive | 1 | 5 | 2,4 |
| 4 webhook validation | 1 | 5 | 2,3 |
| 5 integration tests | 1,2,3,4 | 6 | - |
| 6 docs + samples | 1,2,3,4 | - | - |

## Todos
> Implementation + Test = ONE todo. Never separate.
<!-- APPEND TASK BATCHES BELOW THIS LINE WITH edit/apply_patch - never rewrite the headers above. -->
- [x] 1. Add AllowedInbounds field to SingBoxNodeSpec + regenerate
  What to do / Must NOT do: 在 `api/v1alpha1/singboxnode_types.go` 的 `SingBoxNodeSpec` 中添加 `AllowedInbounds []string` 字段，带 `+optional`、`+listType=set`、`+kubebuilder:validation:MinLength=1` 标记。注释说明：声明在出站节点上，限定哪些入站节点可以将其作为出站使用；空 = 允许所有（向后兼容）。然后运行 `make manifests generate` 重新生成 CRD（config/crd/bases/）与 deepcopy（zz_generated.deepcopy.go）。Must NOT 改 Roles 字段语义；Must NOT 设为 required；Must NOT 加 deniedInbounds。
  Parallelization: Wave 1 | Blocked by: - | Blocks: 2,3,4
  References (executor has NO interview context - be exhaustive): api/v1alpha1/singboxnode_types.go:54-91 (SingBoxNodeSpec 结构)；api/v1alpha1/usergroup_types.go:25-37 (AllowedNodes 用 +listType=set 的参考写法)；Makefile (make manifests / make generate 目标)；AGENTS.md "After editing *_types.go or markers" 段落
  Acceptance criteria (agent-executable): `make manifests` 成功且 `config/crd/bases/*.yaml` 中 SingBoxNode spec 出现 allowedInbounds 字段；`make generate` 成功且 `api/v1alpha1/zz_generated.deepcopy.go` 中含 AllowedInbounds 的 DeepCopy 逻辑；`git diff --stat` 显示 zz_generated 与 crd/bases 改动
  QA scenarios (name the exact tool + invocation): happy - `make manifests && make generate` 无错误且字段出现；failure - 故意删除 marker 后 `make manifests` 应仍能运行但字段缺失（用 grep 验证）；Evidence .omo/evidence/task-1-outbound-inbound-binding.txt
  Commit: Y | feat(api): add AllowedInbounds to SingBoxNodeSpec for outbound-inbound binding

- [x] 2. Apply binding filter in collectInput (controller) + extend reconcile trigger for cross-region CustomRoutes
  What to do / Must NOT do: 
  (a) 在 `internal/controller/singboxnode_controller.go` 的 `collectInput` 函数中，当 B（入站节点）收集同 region 的 OutboundNodes 时（当前 213-224 行的循环），对每个候选出站节点 A 增加 binding 检查：若 `len(A.Spec.AllowedInbounds) > 0` 且 `!slices.Contains(A.Spec.AllowedInbounds, B.Name)` 则跳过 A，并用 `log.Info("Skipping outbound node due to allowedInbounds binding", "outboundNode", A.Name, "inboundNode", B.Name)` 记录。
  (b) 在 collectInput 处理 CustomRoute 引用的 outbound 节点时（285-305 行），也应用同样的 binding 检查——若 CustomRoute 引用的 A 声明了 AllowedInbounds 且 B 不在其中，跳过该 route（不加入 input.Routes、不加入 input.OutboundNodesByName）并 `log.Info("Skipping CustomRoute due to allowedInbounds binding", "route", route.Name, "outboundNode", A.Name, "inboundNode", B.Name)`（注意：这里用 Info 而非 Warning，因为 controller log 风格不用 Warning 级别，但消息文本应说明这是 binding 阻止的显式路由）。
  (c) **修复跨 region reconcile 触发**（Metis Finding 3 CRITICAL）：当前 `sameRegionNodeMapper`（573-591 行）只在同 region 时触发 reconcile。当出站节点 A 的 AllowedInbounds 变化时，跨 region 的 CustomRoute B→A 的 B 不会被 reconcile，导致 B 的 config 过时。修复方案：新增一个 mapper 函数 `customRouteOutboundNodeMapper`，Watches `SingBoxNode` 变化（GenerationChangedPredicate），当出站节点 A 变化时，列出所有 `Spec.OutboundNode == A.Name` 的 CustomRoute，对每个 route 的 `Spec.InboundNode` enqueue reconcile。在 `SetupWithManager`（674-697 行）中注册该 Watch：
  ```go
  Watches(&proxyv1alpha1.SingBoxNode{},
      handler.EnqueueRequestsFromMapFunc(r.customRouteOutboundNodeMapper),
      builder.WithPredicates(predicate.GenerationChangedPredicate{}))
  ```
  mapper 实现：列出 namespace 内所有 CustomRoute，过滤 `route.Spec.OutboundNode == changedNode.Name`，对每个匹配的 route 返回 `reconcile.Request{NamespacedName: {Name: route.Spec.InboundNode, Namespace: route.Namespace}}`。
  Must NOT 改同 region 自动配对的整体逻辑；Must NOT silently 跳过而不打日志；Must NOT 改 UserNodeRestrictions 逻辑；Must NOT 在 mapper 中创建/更新任何资源（只返回 reconcile.Request）。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 5 | Can parallelize with: 3,4
  References (executor has NO interview context - be exhaustive): internal/controller/singboxnode_controller.go:190-318 (collectInput 全函数)；singboxnode_controller.go:213-224 (同 region OutboundNodes 收集循环)；singboxnode_controller.go:285-305 (CustomRoute 收集循环)；singboxnode_controller.go:573-591 (sameRegionNodeMapper 模式参考)；singboxnode_controller.go:646-654 (affectedByRouteMapper 模式参考)；singboxnode_controller.go:674-697 (SetupWithManager 注册 Watches)；internal/configengine/engine.go:188-205 (IsNodeAllowed 模式参考)；internal/configengine/engine.go:211-213 (hasRole helper)；slices.Contains 标准库
  Acceptance criteria (agent-executable): `make test` 通过；新增/修改的 controller 测试用例覆盖：(a) A.AllowedInbounds=[] 时 B 收集到 A；(b) A.AllowedInbounds=[B] 时 B 收集到 A；(c) A.AllowedInbounds=[OtherNode] 时 B 不收集 A；(d) CustomRoute B→A 在 A.AllowedInbounds=[OtherNode] 时被跳过且有 Info log；(e) **跨 region**：CustomRoute B(us-west)→A(us-east)，修改 A.AllowedInbounds 从 [] 到 [C] 后，B 被自动 reconcile（envtest 验证 B 的 config 不再含 outbound-A）
  QA scenarios: happy - A 无绑定，B 正常使用 A；failure - A 绑定到 OtherNode，B 的 sing-box config 中不应出现 outbound-A tag；cross-region - 修改 A 的 AllowedInbounds 后 B 自动 reconcile（验证 mapper）；Evidence .omo/evidence/task-2-outbound-inbound-binding.json
  Commit: Y | feat(controller): enforce AllowedInbounds binding and trigger cross-region reconcile

- [x] 3. Add defensive binding filter in configengine (buildOutboundNodeOutbounds + buildRouteOutbounds only)
  What to do / Must NOT do: 在 `internal/configengine/engine.go` 的 `buildOutboundNodeOutbounds`（445-470 行）和 `buildRouteOutbounds`（472-506 行）中，对每个 outNode 增加防御性检查：若 `len(outNode.Spec.AllowedInbounds) > 0` 且 `!slices.Contains(outNode.Spec.AllowedInbounds, input.Node.Name)` 则 continue 跳过。这是 defensive guard（与现有 UserNodeRestrictions 在 engine.go:335/361/382/486 的防御模式一致），正常情况下 controller 已过滤，此处兜底。
  
  **明确不需要加防御过滤的函数**（Metis Finding 1/8，避免过度修改）：
  - `buildExperimentalConfig`（533-583 行）：迭代 `input.OutboundNodes` 生成 v2ray stats users。由于 controller 已在 collectInput 中过滤了 OutboundNodes，此处无需重复过滤。在函数顶部加注释 `// NOTE: input.OutboundNodes is pre-filtered by controller's AllowedInbounds check; no defensive filter needed here.` 即可。
  - `buildRouteInbounds`（304-375 行）：迭代 `input.OutboundNodes` 和 `routes` 构建 inbound users 与 route rules。同理，controller 已过滤 OutboundNodes 和 Routes（task 2），无需防御。加同样注释。
  - `buildOutboundNodeOutbounds` 中 `routedNodes` map（448 行）：来自 `myRoutes`，已被 controller binding 过滤（task 2 在 collectInput 中跳过被 binding 阻止的 CustomRoute，不加入 input.Routes）。453 行的 `routedNodes[outNode.Name]` continue 是去重逻辑，与 binding 无关。加注释说明。
  
  Must NOT 重复打日志（controller 已记）；Must NOT 改 IsNodeAllowed 函数签名；Must NOT 在 buildExperimentalConfig/buildRouteInbounds 中加过滤逻辑（只需注释说明为何不需要）；Must NOT 影响 self-outbound（inbound+outbound 同节点时走 engine.go:118-123 的独立路径，不经 OutboundNodes 循环，binding 不影响）。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 5 | Can parallelize with: 2,4
  References: internal/configengine/engine.go:445-506 (buildOutboundNodeOutbounds + buildRouteOutbounds)；engine.go:533-583 (buildExperimentalConfig - 只加注释)；engine.go:304-375 (buildRouteInbounds - 只加注释)；engine.go:188-205 (IsNodeAllowed)；engine.go:335,361,382,486 (现有防御性 IsNodeAllowed 调用模式)；engine.go:99,118-123 (self-outbound 独立路径，不受影响)；engine.go:211-213 (hasRole)
  Acceptance criteria (agent-executable): `make test` 通过；engine_test.go 新增用例：(a) OutboundNodes 含一个 AllowedInbounds=[OtherNode] 的节点时，buildOutboundNodeOutbounds 输出不含该节点；(b) buildRouteOutbounds 同理；(c) AllowedInbounds=[] 时行为不变；(d) buildExperimentalConfig 与 buildRouteInbounds 在传入已过滤的 OutboundNodes 时行为不变（回归测试）
  QA scenarios: happy - 防御层放行空绑定节点；failure - 传入未过滤的 OutboundNodes（模拟 controller bug），防御层应剔除被绑定节点；Evidence .omo/evidence/task-3-outbound-inbound-binding.json
  Commit: Y | feat(configengine): add defensive AllowedInbounds filter in outbound builders

- [x] 4. Add webhook validation for AllowedInbounds
  What to do / Must NOT do: 在 `internal/webhook/singboxnode_webhook.go` 的 `validateSingBoxNode`（53-118 行）中增加对 `AllowedInbounds` 的校验：(a) 每项必须非空字符串（用 `+kubebuilder:validation:MinLength=1` marker 已在 API 层保证，但 webhook 兜底校验）；(b) 每项不能重复（dedup 检查，参考 64-70 行 seenProtocols 模式）；(c) 不校验引用的入站节点是否存在（节点可任意顺序创建，存在性校验留给 controller reconcile 时打 warning）。Must NOT 把"引用不存在的入站节点"设为校验错误（会破坏创建顺序）；Must NOT 校验 AllowedInbounds 只能引用 inbound 角色节点（角色可在之后修改，过度约束）。
  Parallelization: Wave 2 | Blocked by: 1 | Blocks: 5 | Can parallelize with: 2,3
  References: internal/webhook/singboxnode_webhook.go:53-118 (validateSingBoxNode)；webhook.go:64-70 (seenProtocols dedup 模式)；webhook.go:101-112 (Roles 校验模式参考)
  Acceptance criteria (agent-executable): `make test` 通过；webhook_test.go 新增用例：(a) AllowedInbounds=["B"] 校验通过；(b) AllowedInbounds=[""] 校验失败；(c) AllowedInbounds=["B","B"] 校验失败（重复）；(d) AllowedInbounds 引用不存在的节点名校验通过
  QA scenarios: happy - 合法 allowedInbounds 被接受；failure - 重复或空字符串被拒绝；Evidence .omo/evidence/task-4-outbound-inbound-binding.json
  Commit: Y | feat(webhook): validate AllowedInbounds entries are non-empty and unique

- [x] 5. Integration tests covering binding end-to-end
  What to do / Must NOT do: 在 `internal/controller/singboxnode_controller_test.go` 和 `internal/configengine/engine_test.go` 中新增端到端用例覆盖：(a) 空 AllowedInbounds 向后兼容——B 的 config 含同 region 所有 outbound；(b) A.AllowedInbounds=[B] 时 B 的 config 含 outbound-A，其他入站节点 C 的 config 不含 outbound-A；(c) 跨 region 行为不变——A.AllowedInbounds=[B] 但 B 与 A 不同 region，B 本来也不会收集 A，行为不变；(d) CustomRoute B→A 与 binding 共存——A.AllowedInbounds=[B] 时 CustomRoute 生效，A.AllowedInbounds=[C] 时 CustomRoute 被跳过且有 Info log；(e) self-outbound（同节点 inbound+outbound）——节点 S 同时有 inbound+outbound 角色，S.AllowedInbounds=[] 时 self-outbound 正常，S.AllowedInbounds=[S] 时也正常（自身在列表中）；**(e2) self-outbound 节点 S 设 AllowedInbounds=[S] 时，同 region 的另一个入站节点 X 不应看到 outbound-S**（Metis Finding 5）；**(f) 跨 region CustomRoute reconcile 触发**（Metis Finding 3 验证）——CustomRoute B(us-west)→A(us-east)，修改 A.AllowedInbounds 从 [] 到 [C]，验证 B 被 reconcile 且 config 不再含 outbound-A。Must NOT 删除或修改既有测试用例；Must NOT 引入真实集群依赖（用 envtest）。
  Parallelization: Wave 3 | Blocked by: 1,2,3,4 | Blocks: 6 | Can parallelize with: -
  References: internal/controller/singboxnode_controller_test.go (既有测试模式)；internal/configengine/engine_test.go (engine 测试模式)；internal/controller/suite_test.go (envtest 设置)；internal/configengine/engine.go:93-168 (Compute 函数入口)；engine.go:99,118-123 (self-outbound 路径)
  Acceptance criteria (agent-executable): `make test` 全部通过；新增用例覆盖上述 7 个场景（a/b/c/d/e/e2/f）；`go test ./internal/... -run TestAllowedInbounds -v` 输出可见所有用例 pass
  QA scenarios: happy - 所有 7 场景通过；failure - 删除 task 2 的过滤逻辑后场景 (b)(d)(e2) 应失败（验证测试有效性）；删除 task 2 的 customRouteOutboundNodeMapper 后场景 (f) 应失败；Evidence .omo/evidence/task-5-outbound-inbound-binding.txt
  Commit: Y | test: add e2e coverage for AllowedInbounds binding

- [x] 6. Update samples and README documentation
  What to do / Must NOT do: 在 `config/samples/singboxoperator_v1alpha1_singboxnode.yaml` 中补充一个带 `allowedInbounds` 的示例（注释掉，作为参考）。在 README.md 的 Description/Getting Started 中增加一段说明 AllowedInbounds 字段的语义和用法。Must NOT 把示例设为默认应用（会创建带绑定的节点影响测试）；Must NOT 改 AGENTS.md（那是开发指南不是用户文档）。
  Parallelization: Wave 3 | Blocked by: 1,2,3,4 | Blocks: - | Can parallelize with: -
  References: config/samples/singboxoperator_v1alpha1_singboxnode.yaml (当前 sample)；README.md (当前空 Description)
  Acceptance criteria (agent-executable): `kubectl apply --dry-run=client -f config/samples/` 成功；README.md 含 AllowedInbounds 说明段落
  QA scenarios: happy - sample dry-run 通过；failure - sample 格式错误时 dry-run 失败；Evidence .omo/evidence/task-6-outbound-inbound-binding.txt
  Commit: Y | docs: document AllowedInbounds binding in samples and README

## Final verification wave
> Runs in parallel after ALL todos. ALL must APPROVE. Surface results and wait for the user's explicit okay before declaring complete.
- [x] F1. Plan compliance audit
- [x] F2. Code quality review
- [x] F3. Real manual QA
- [x] F4. Scope fidelity

## Commit strategy
- 每个 task 一个原子 commit（见各 task 的 Commit 行）
- 全部完成后 squash 可选，保留 6 个清晰 commit 更利于 review
- commit message 遵循 Conventional Commits（feat/test/docs + scope）

## Success criteria
- `make manifests generate lint-fix test` 全部通过
- 新增字段在 CRD yaml 中可见
- 向后兼容：所有不带 AllowedInbounds 的存量 SingBoxNode 行为不变（测试验证）
- 新功能：A.AllowedInbounds=[B] 时只有 B 能用 A 作出站（测试验证）
- CustomRoute 与 binding 独立工作（测试验证）
