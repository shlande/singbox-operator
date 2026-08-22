# outbound-grouping - Work Plan

## TL;DR (For humans)

**What you'll get:** 客户端拿到的 sing-box 配置里，出站节点不再平铺在一个 `proxy` 选择器下，而是按入站节点的 `tag` 分成多个子选择器（如 `cdn`、`us`、`default`），最外层仍保留一个 `proxy` 总选择器串联所有子组。客户端 UI 上看到三级结构：`proxy → 各 group → 具体节点`。

**Why this approach:** 出站 tag 已经编码了 `outboundName#inboundName`（client_config.go:60-69），分组所需信息在生成时已具备，只需按入站 tag 分桶。`spec.tag` 单值字段是最小且向后兼容的 CRD 变更；保留顶层 `proxy` 选择器是为了兼容现有模板的 `route.final="proxy"` 和 `clash_mode` 规则（template.go:46-47,68），无需改模板或路由。

**What it will NOT do:**
- 不改节点端 config（configengine）——分组纯属客户端侧概念
- 不支持一个入站挂多个 tag（单值 `tag string`）
- 不改 `MergeOutbounds` 或 `DefaultTemplate`
- 不改路由规则 / final outbound（`proxy` 仍是 final）

**Effort:** Short
**Risk:** Low - 改动集中在 `client_config.go` 一个函数 + 一个 CRD 字段 + webhook 校验；现有测试断言需调整但行为等价
**Decisions to sanity-check:** tag 命名直接用原始值（不加 `group-` 前缀）；`default` 作为保留字禁止用户使用；无 tag 的入站合并进单个 `default` group

Your next move: 直接 `/start-work` 执行，或先跑一次 high-accuracy review。完整执行细节见下。

---

> TL;DR (machine): Short / Low - 加 spec.tag 字段 + webhook 禁 default + BuildClientConfig 按 tag 分组发 selector + TDD 测试

## Scope

### Must have
- `SingBoxNodeSpec` 新增 `Tag string` 字段（optional），CRD marker：`MaxLength=63`，`Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"`
- webhook `validateSingBoxNode` 拒绝 `tag == "default"`（仅对 inbound 角色节点校验，outbound-only 节点的 tag 无意义但允许设置不影响）
- `BuildClientConfig` 重构：按入站 tag 分桶 → 每个 tag 一个 `selector`（tag=原始值，无 tag 用 `default`）→ 顶层 `selector("proxy")` 列出所有 group tag（按字典序排序）→ `direct`
- **空 group 不发 selector**：若某 inbound 因所有 outbound 离线/无匹配而产出 0 个 proxy outbound，则其 tag 不进入 `groupOutbounds` map，不生成对应 group selector（避免空 outbounds 数组的 selector）
- **group 内 outbound tag 去重**：同一 group 内若出现重复 outbound tag（self-as-outbound 场景：两个同 tag 的 dual-role inbound 各自产出无 `#` 后缀的自身 tag），需 sort + compact 去重后再放入 group selector 的 outbounds
- 分组顺序确定性：group selector 按 tag 字典序排序；group 内 outbound tag 按 client_config.go:157-159 现有排序 + 去重
- 现有 apiserver 测试断言更新（详见 T4 完整断言清单，含 `TestBuildClientConfig_ExplicitRoutes`、`TestBuildClientConfig_DualRoleNode_IncludesSelf`、`client_config_filter_test.go:247` 等）
- 新增测试：多 group、default group 合并、tag=="default" 被拒、tag pattern 校验、空 group 不发 selector、group 内去重
- `make manifests generate` 重新生成 CRD + deepcopy

### Must NOT have (guardrails, anti-slop, scope boundaries)
- 不改 `internal/configengine/`（节点端 config 完全不动）
- 不引入 `tags []string`（多值）
- 不改 `internal/apiserver/template.go`（DefaultTemplate / MergeOutbounds 不动）
- 不改 `handler.go` 的调用流程（BuildClientConfig 签名不变，仍是 `([]any, error)`）
- 不加 `group-` 前缀（group selector 的 tag 直接用入站 tag 值）
- 不改 e2e 测试（本次仅单元测试范围）
- 不在 CRD marker 里做 `!= "default"` 校验（marker 不支持不等式；该语义校验放 webhook）

## Verification strategy
> Zero human intervention - all verification is agent-executed.
- Test decision: TDD（先改/写测试为红，再改实现转绿）+ Ginkgo/test 框架
- Evidence: .omo/evidence/task-{N}-outbound-grouping.{txt,json}
- 每个任务先跑 `go test ./internal/...` 确认红/绿；最终跑 `make manifests generate && make test`

## Execution strategy

### Parallel execution waves
- Wave 1（串行依赖链）：T1 CRD 字段 → T2 webhook 校验 → T3 BuildClientConfig 重构(+2 测试) → T4 测试断言更新(+4 新测试)
- Wave 1 内部严格串行（T2 依赖 T1 字段存在；T3 依赖 T1 字段可读且自带 2 测试可独立验证；T4 依赖 T3 行为完成）
- 无并行空间（线性链）

### Dependency matrix
| Todo | Depends on | Blocks | Can parallelize with |
| --- | --- | --- | --- |
| T1 CRD field | — | T2,T3,T4 | — |
| T2 webhook | T1 | T4 | — |
| T3 BuildClientConfig(+2 tests) | T1 | T4 | — |
| T4 tests(+4 tests) | T1,T2,T3 | F1-F4 | — |

## Todos
> Implementation + Test = ONE todo. Never separate.
<!-- APPEND TASK BATCHES BELOW THIS LINE WITH edit/apply_patch - never rewrite the headers above. -->

- [x] 1. Add Tag field to SingBoxNodeSpec + regenerate CRD/deepcopy
  What to do / Must NOT do:
  - 在 `api/v1alpha1/singboxnode_types.go` 的 `SingBoxNodeSpec` 中 `AllowedOutbounds` 字段之后、`TLSSecretName` 之前新增字段：
    ```go
    // Tag is an optional grouping label for inbound nodes. When set, client configs
    // group outbounds that use this node as inbound under a selector named after this tag.
    // Empty means the "default" group. Must not be "default" (reserved).
    // Only meaningful for nodes with the inbound role.
    // +optional
    // +kubebuilder:validation:MaxLength=63
    // +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
    Tag string `json:"tag,omitempty"`
    ```
  - 运行 `make manifests generate` 重新生成 CRD（config/crd/bases/）和 deepcopy（zz_generated.deepcopy.go）
  - Must NOT do: 不要在 marker 里加 `!= "default"` 校验（marker 不支持）；不要改 webhook（那是 T2）；不要改任何 controller
  Parallelization: Wave 1 | Blocked by: — | Blocks: T2,T3,T4
  References (executor has NO interview context - be exhaustive):
  - api/v1alpha1/singboxnode_types.go:54-106（SingBoxNodeSpec 现有字段顺序，插入点在 AllowedOutbounds 之后 line 99，TLSSecretName 之前 line 105）
  - api/v1alpha1/singboxnode_types.go:91-99（参考 AllowedOutbounds 的 optional + listType 风格，但 Tag 是单值不加 listType）
  - AGENTS.md "After editing *_types.go or markers" 段：`make manifests` + `make generate`
  - config/crd/bases/（生成产物，DO NOT EDIT）
  - api/v1alpha1/zz_generated.deepcopy.go（生成产物，DO NOT EDIT）
  Acceptance criteria (agent-executable):
  - `make manifests` 成功，config/crd/bases/*.yaml 中 SingBoxNode CRD spec 包含 `tag` 字段且带 maxLength: 63 + pattern
  - `make generate` 成功，无 diff 残留
  - `go build ./...` 通过
  QA scenarios (name the exact tool + invocation):
  - happy: `make manifests generate && git diff --stat` 显示 CRD yaml + deepcopy 被更新；`grep -r "tag:" config/crd/bases/ | grep -i singbox` 命中
  - failure: 临时把 Pattern 改成非法正则 `*[`，跑 `make manifests` 应报错（验证 marker 生效）；还原
  Evidence: .omo/evidence/task-1-outbound-grouping.txt（保存 `make manifests generate` 输出 + `git diff --stat` + grep 结果）
  Commit: Y | feat(api): add optional Tag field to SingBoxNodeSpec for inbound grouping

- [x] 2. Add webhook validation: reject tag=="default"
  What to do / Must NOT do:
  - 在 `internal/webhook/singboxnode_webhook.go` 的 `validateSingBoxNode` 函数中，在现有的 inbound 角色校验块（line 150-186 的 `if isInbound {}` 块内）追加 tag 校验：
    ```go
    if isInbound && node.Spec.Tag == "default" {
        allErrs = append(allErrs, field.Invalid(
            field.NewPath("spec", "tag"),
            node.Spec.Tag,
            `"default" is reserved for untagged inbound nodes; choose a different tag value`,
        ))
    }
    ```
  - 放在 `if isInbound {}` 块内、`SupportedProtocols` 校验之前或之后均可（语义独立）
  - 新增测试 `TestSingBoxNodeWebhook_TagDefaultRejected`：构造 inbound 节点 `tag: "default"`，断言 `ValidateCreate` 返回 error 包含 "default" is reserved
  - 新增测试 `TestSingBoxNodeWebhook_TagAllowed`：构造 inbound 节点 `tag: "cdn"`，断言 `ValidateCreate` 返回 nil
  - 新增测试 `TestSingBoxNodeWebhook_TagEmptyAllowed`：构造 inbound 节点不设 tag，断言 `ValidateCreate` 返回 nil
  - 新增测试 `TestSingBoxNodeWebhook_TagOnOutboundOnlyNode`：构造 outbound-only 节点 `tag: "default"`，断言 `ValidateCreate` 返回 nil（outbound-only 节点不校验 tag）
  - Must NOT do: 不在 CRD marker 里做此校验；不改 Default() webhook 方法；不改其他资源的 webhook
  Parallelization: Wave 1 | Blocked by: T1 | Blocks: T4
  References:
  - internal/webhook/singboxnode_webhook.go:53-192（validateSingBoxNode 全貌，field.ErrorList 模式）
  - internal/webhook/singboxnode_webhook.go:150-186（`if isInbound {}` 块，插入点）
  - internal/webhook/singboxnode_webhook.go:41-47（ValidateCreate/ValidateUpdate 签名）
  - internal/webhook/webhook_test.go:39-90（现有 ValidateCreate 测试模式：构造 node → 调 w.ValidateCreate(ctx, node) → 断言 err）
  - internal/webhook/webhook_test.go:1-13（import 与 setup 风格）
  Acceptance criteria:
  - `go test ./internal/webhook/ -run TestSingBoxNodeWebhook_Tag -v` 4 个新测试全绿
  - `go test ./internal/webhook/ -v` 全部既有测试仍绿（无回归）
  QA scenarios:
  - happy: 上述 4 测试通过
  - failure: 临时删掉校验代码，`TestSingBoxNodeWebhook_TagDefaultRejected` 应转红；还原
  Evidence: .omo/evidence/task-2-outbound-grouping.txt（go test -v 输出）
  Commit: Y | feat(webhook): reject reserved "default" tag value on SingBoxNode

- [x] 3. Refactor BuildClientConfig to group outbounds by inbound tag (impl + tests together per Metis)
  What to do / Must NOT do:
  - 重构 `internal/apiserver/client_config.go::BuildClientConfig`（line 37-86）：
    1. 维护 `groupOutbounds map[string][]string`（tag → 该 group 的 proxy outbound tags，保持追加顺序）
    2. 遍历 `input.InboundNodes` 时，对每个**产出至少 1 个 proxy outbound** 的有效 inbound（注意：必须在确认该 inbound 确实产出了 outbound 之后再记录 tag，避免空 group）：计算 `tag := inboundNode.Spec.Tag; if tag == "" { tag = "default" }`，将该 inbound 的所有 outbound tag 追加到 `groupOutbounds[tag]`
    3. 遍历结束后，对每个 group 的 outbound tag slice 做 `sort.Strings` + `slices.Compact` 去重（处理 self-as-outbound 同名 tag 重复）
    4. 对 `groupOutbounds` 的 key 排序（字典序，确定性），为每个 tag 生成 `{"type":"selector","tag":<tag>,"outbounds":<deduped tags>}`，收集 group tag 到 `groupTags`
    5. 顶层 `{"type":"selector","tag":"proxy","outbounds":groupTags}`（groupTags 已排序）
    6. 最后 `direct`
  - 结果顺序：所有 proxy outbound 条目（保持现有顺序，即 inbound 循环 × outbound 排序）→ 各 group selector（按 tag 字典序）→ proxy selector → direct
  - **空 group 规则**：若某 inbound 产出 0 个 proxy outbound（所有 outbound 离线/无匹配 endpoint/不支持协议），其 tag 不进入 `groupOutbounds`，不生成 group selector
  - 必须确保：当所有 inbound 都无产出时，`groupOutbounds` 为空 → `groupTags` 为空 → `selector("proxy")` 的 outbounds 为空数组 `[]`（而非 nil），仍发出 proxy selector（template route.final="proxy" 依赖它存在）
  - Must NOT do: 不改 `buildProxyOutbound`、`resolveOutboundNodes`、`findEntryEndpoint`、`supportsProtocol`；不改 `BuildClientConfig` 函数签名；不改 `ClientConfigInput` 结构；不改 `MergeOutbounds`/`DefaultTemplate`；不在 group 内对 outbound 重新排序（在去重时 sort 是必要的，但去重后恢复原追加顺序——实际上为简化，group 内 outbound tag 直接 sort 去重即可，因为原顺序无语义意义，仅确定性有意义）
  - **本 todo 同时写实现 + 测试**（Metis 要求 T3 可独立验证）：在 `internal/apiserver/client_config_group_test.go` 新建文件，写 `TestBuildClientConfig_MultiGroup`（2 inbound tag=cdn/us + 共享 outbound，断言 2 group selector + proxy + direct）、`TestBuildClientConfig_EmptyGroupNotEmitted`（inbound 所有 outbound 离线，断言无 group selector，proxy.outbounds 为空数组）
  Parallelization: Wave 1 | Blocked by: T1 | Blocks: T4
  References:
  - internal/apiserver/client_config.go:37-86（BuildClientConfig 当前实现，单 selector("proxy")）
  - internal/apiserver/client_config.go:41-71（主循环：遍历 inbound → resolveOutboundNodes → buildProxyOutbound → 收集 proxyTags）
  - internal/apiserver/client_config.go:60-69（outbound tag 格式：outboundName 或 outboundName#inboundName；self-as-outbound 无 # 后缀）
  - internal/apiserver/client_config.go:73-85（当前结果组装：proxyOutbounds + selector("proxy") + direct）
  - internal/apiserver/client_config.go:157-159（resolveOutboundNodes 内 sort.Slice）
  - internal/apiserver/template.go:46-47,68（route.final="proxy", clash_mode Proxy→proxy，证明顶层 proxy selector 必须保留，即使 outbounds 为空）
  - internal/apiserver/handler_test.go:421-440（makeInboundNode helper，测试内 `.Spec.Tag=` 赋值）
  - internal/apiserver/handler_test.go:442-477（makeOutboundNode helper）
  Acceptance criteria:
  - `go build ./internal/apiserver/` 通过
  - `go test ./internal/apiserver/ -run "TestBuildClientConfig_MultiGroup|TestBuildClientConfig_EmptyGroupNotEmitted" -v` 2 个新测试全绿
  QA scenarios:
  - happy: 上述 2 测试通过
  - failure: 临时让空 group 也发 selector（去掉"产出至少 1 个才记录"判断），EmptyGroupNotEmitted 应转红；还原
  Evidence: .omo/evidence/task-3-outbound-grouping.txt（go test -v 输出）
  Commit: Y | feat(apiserver): group client-config outbounds by inbound tag into selectors

- [x] 4. Update all existing test assertions + add remaining test cases (TDD)
  What to do / Must NOT do:
  - **完整断言更新清单**（Metis 验证后，逐 site 列出）：
  
  **A. `internal/apiserver/handler_test.go` 需更新：**
  - `TestBuildClientConfig_TwoOutboundNodes` (line 28-73)：
    - line 54: `len(result) != 4` → `!= 5`（2 proxy + 1 default group selector + proxy selector + direct）
    - line 58-72: 现有 selector.outbounds 断言（`selectorOutbounds` 收集的是第一个 selector）需改为：区分 `selector("default")`（outbounds=["node-b1#node-a","node-b2#node-a"]）和 `selector("proxy")`（outbounds=["default"]）。改为按 tag 分别断言两个 selector
  - `TestBuildClientConfig_ExplicitRoutes` (line 197-252)：
    - line 232: `len(result) != 4` → `!= 5`（2 proxy + 1 default group selector + proxy selector + direct）
    - 新增断言：`selector("proxy").outbounds == ["default"]`；`selector("default").outbounds == ["outbound-x#node-a","outbound-y#node-a"]`
  - `TestBuildClientConfig_DualRoleNode_IncludesSelf` (line 905-969)：
    - line 927: `len(result) != 3` → `!= 4`（1 proxy + 1 default group selector + proxy selector + direct）
    - **line 951-953 关键改写**：现有 `selectorOutbounds` 收集逻辑（line 941-944 遍历找 `type=="selector"`）会命中两个 selector（default group + proxy）。需改为：找到 `tag=="default"` 的 selector，断言其 outbounds==["node-x"]；找到 `tag=="proxy"` 的 selector，断言其 outbounds==["default"]
  - 其他 `TestBuildClientConfig_*` 中若有 `len(result)` 断言需逐个核对（用 `grep -n "len(result)" internal/apiserver/*_test.go` 全量扫描，每个按"产出 proxy outbound 数 + group selector 数 + 1(proxy selector) + 1(direct)"公式计算新期望值）
  
  **B. `internal/apiserver/client_config_filter_test.go` 需更新：**
  - line 93 (`TestNodeReadiness_NodeNotReady_ExcludesOutbound`)：**无需改**——所有 outbound 离线 → 0 proxy → 无 group selector → 仍是 selector("proxy", outbounds=[]) + direct = 2 项（Metis Finding 2 确认）
  - line 247 (`TestNodeReadiness_MultipleUnhealthyNodes`)：`len(result) != 3` → `!= 4`（1 proxy + 1 default group selector + proxy selector + direct）
  - line 320 (`TestNodeReadiness_DualRoleNodeUnhealthy_ExcludesSelf`)：**无需改**——同 line 93 逻辑，0 proxy 无 group selector，仍 2 项
  - 其他 `len(result)` 断言用上述公式核对（含 line 125/177 的 `len(offline)==len(recovered)` 比较断言：offline 2 项 vs recovered 4 项，不等仍成立，无需改）
  
  **C. `TestBuildClientConfig_UnsupportedProtocol` / `TestBuildClientConfig_BadEndpointFormat` / `TestBuildClientConfig_EmptyEntryEndpoints`（若有 len 断言）：**
  - 这些场景 inbound 产出 0 proxy outbound → 无 group selector → `len` 仍为 2（selector + direct），**无需改**
  
  - **新增测试**（部分已在 T3 的 client_config_group_test.go，本 todo 补充其余）：
    - `TestBuildClientConfig_DefaultGroupMerge`：2 inbound 均不设 tag + 各自 outbound，断言：单个 selector(default) 含两 inbound 的所有 outbound tag（去重后）；selector(proxy).outbounds==["default"]
    - `TestBuildClientConfig_TagOnOutboundIgnored`：outbound 节点设 tag，inbound 不设，断言：仍走 default group（outbound 的 tag 字段不影响分组）
    - `TestBuildClientConfig_GroupOrderDeterministic`：2 inbound tag=zebra, tag=apple，断言 proxy.outbounds==["apple","zebra"]（字典序）；两次调用 BuildClientConfig 输出 JSON byte-equal
    - `TestBuildClientConfig_GroupDedup`：2 个同 tag=cdn 的 dual-role inbound（同名 self-outbound 场景），断言 group-cdn 的 outbounds 无重复 tag
  - Must NOT do: 不改 `makeInboundNode`/`makeOutboundNode`/`makeUser` helper 签名（如需 tag 可在测试内直接 `node.Spec.Tag = "cdn"` 赋值）；不改 e2e 测试；不引入新依赖
  Parallelization: Wave 1 | Blocked by: T1,T2,T3 | Blocks: F1-F4
  References:
  - internal/apiserver/handler_test.go:28-73（TestBuildClientConfig_TwoOutboundNodes）
  - internal/apiserver/handler_test.go:197-252（TestBuildClientConfig_ExplicitRoutes，line 232 len 断言）
  - internal/apiserver/handler_test.go:905-969（TestBuildClientConfig_DualRoleNode_IncludesSelf，line 927 + 951 关键断言）
  - internal/apiserver/handler_test.go:421-440（makeInboundNode helper）
  - internal/apiserver/handler_test.go:442-477（makeOutboundNode helper）
  - internal/apiserver/handler_test.go:478-492（makeUser helper）
  - internal/apiserver/client_config_filter_test.go:11-39（collectTags / countProxyOutbounds helper）
  - internal/apiserver/client_config_filter_test.go:93（0-proxy 场景，无需改）
  - internal/apiserver/client_config_filter_test.go:247（1-proxy 场景，3→4）
  - internal/apiserver/client_config_filter_test.go:320（0-proxy 场景，无需改）
  - internal/apiserver/client_config.go:37-86（被测函数）
  Acceptance criteria:
  - `go test ./internal/apiserver/ -v` 全绿（含更新的旧测试 + T3 的 2 测试 + 本 todo 的 4 新测试）
  - `go test ./internal/... -v` 全绿（无回归）
  - `grep -rn "len(result)" internal/apiserver/*_test.go` 每个 site 已按公式核对
  QA scenarios:
  - happy: `go test ./internal/apiserver/ -v` 所有测试 PASS
  - failure: 临时回退 T3 分组逻辑（单 selector），跑 MultiGroup/DefaultGroupMerge 应转红；还原
  - failure: 临时把 group 顺序改成非字典序，跑 GroupOrderDeterministic 应转红；还原
  Evidence: .omo/evidence/task-4-outbound-grouping.txt（go test -v 全输出 + grep 核对结果）
  Commit: Y | test(apiserver): update assertions and add multi-group client config tests

## Final verification wave
> Runs in parallel after ALL todos. ALL must APPROVE. Surface results and wait for the user's explicit okay before declaring complete.
- [x] F1. Plan compliance audit
- [x] F2. Code quality review
- [x] F3. Real manual QA
- [x] F4. Scope fidelity

## Commit strategy
- 4 个原子 commit（每 todo 一个），按 T1→T2→T3→T4 顺序
- commit message 遵循 conventional commits：`feat(api)` / `feat(webhook)` / `feat(apiserver)` / `test(apiserver)`
- 不 squash（保持 TDD 红绿可追溯）

## Success criteria
- `make manifests generate && make test` 全绿
- 客户端 config 输出结构：proxy outbounds → group selectors（按 tag 字典序）→ selector("proxy", outbounds=group tags) → direct
- 无 tag 的 inbound 归入 `default` group；`tag: "default"` 被 webhook 拒绝
- configengine（节点端）零改动
- 现有测试断言已对齐新行为，新增 4 个多 group 专项测试
