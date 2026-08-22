# inbound-protocol-consistency-webhook - Work Plan

## TL;DR (For humans)

**What you'll get:** 给 sing-box-operator 加一道"防呆"校验：当用户创建或修改一个 inbound 角色的 SingBoxNode 时，如果声明的 `inboundProtocol`（或默认的 hysteria2）不在 `supportedProtocols` 列表里，webhook 会直接拒绝这个操作并给出清晰的错误提示，而不是像现在这样静默接受、然后节点在 client-config 里凭空消失。同时修正现有测试中构造了"不一致配置"的 fixture（它们本身就是 bug 的范例）。

**Why this approach:** sc-hk 的 naive 改造已经成功（数据正确、shlande 的 client-config 已包含 naive 协议的 sc-hk）。但根因分析发现 `InboundProtocol` 和 `SupportedProtocols` 两个 spec 字段之间没有任何一致性校验——用户改一个不改另一个时，operator 不报错，只是静默地不为该节点生成 inbound，导致节点从 client-config 消失且无任何错误提示。最小改动是加 webhook 校验，不需要改 CRD、不需要数据迁移、不影响 socks5 relay。已验证集群中所有 6 个 inbound 节点当前配置一致，部署后不会锁死任何节点更新。

**What it will NOT do:**
- 不修改任何 SingBoxNode 的 spec/status 数据（sc-hk 已正确）
- 不修改 CRD schema、不合并字段、不做破坏性变更
- 不改动 socks5 relay 逻辑（`buildRelayInbound`/`buildOutboundNodeOutbounds` 完全不变）
- 不改动 controller / configengine / apiserver 的现有逻辑
- 不清理 shlande User CR 里残留的 `spec.protocol` 字段（migration 遗留，CRD 已不认，无功能影响）

**Effort:** Quick
**Risk:** Low — 只增校验逻辑，不改任何现有行为；唯一风险是校验过严误拒合法配置，通过测试覆盖缓解
**Decisions to sanity-check:** 校验只在 inbound 角色节点上触发（outbound-only 节点不需要协议）；`InboundProtocol` 为空时默认 hysteria2，校验的是 hysteria2 ∈ SupportedProtocols

Your next move: 直接执行（`/start-work`）。范围小、风险低，无需高精度评审。

---

> TL;DR (machine): Quick | Low risk | 在 SingBoxNode webhook 加 inbound 协议一致性校验 + 补 6 个测试用例，防静默失败

## Scope
### Must have
- 在 `internal/webhook/singboxnode_webhook.go` 的 `validateSingBoxNode()` 中新增校验逻辑：
  - 当节点 roles 包含 `inbound` 时：
    - `SupportedProtocols` 不得为空（否则拒绝，错误指明 inbound 节点必须声明至少一个协议端口）
    - `EffectiveInboundProtocol`（即 `InboundProtocol` 若非空则用它，否则默认 `"hysteria2"`）必须存在于 `SupportedProtocols` 的某个 `protocol` 字段中（否则拒绝，错误指明 effective protocol 不在 supportedProtocols 中）
  - 当节点 roles 不包含 `inbound` 时（纯 outbound）：不校验 `SupportedProtocols` 和 `InboundProtocol`（向后兼容现有纯 outbound 节点）
- 在 `internal/webhook/webhook_test.go` 中补充测试用例覆盖新校验
- 现有所有测试继续通过

### Must NOT have (guardrails, anti-slop, scope boundaries)
- 不得修改 `Default()` webhook（保持空实现）
- 不得修改 `ValidateUpdate` 的签名或 old/new 对比逻辑（仍是只校验 newNode）
- 不得修改 controller / configengine / apiserver 任何文件
- 不得修改 CRD schema 或重新生成 manifests（不需要 `make manifests`）
- 不得修改 socks5 relay 相关任何代码
- 不得引入对 `internal/configengine` 的 import（webhook 不应依赖 configengine；在 webhook 包内内联 effective protocol 逻辑，3 行代码）
- 不得清理或修改集群中任何存量 CR 数据
- 不得删除任何现有测试用例；但**允许且必须**更新那些构造了"inbound 节点 InboundProtocol="" + SupportedProtocols=[{vless,...}]"的不一致测试 fixture——这些 fixture 本身就是我们要防止的不一致模式，必须修正为一致配置（加 `InboundProtocol: "vless"` 或改 SupportedProtocols 含 hysteria2）

## Verification strategy
> Zero human intervention - all verification is agent-executed.
- Test decision: tests-after（在现有校验逻辑后追加，先写实现再补测试）
- Framework: Go 标准 `testing` + `go test ./internal/webhook/...`
- Evidence: `.omo/evidence/task-1-inbound-protocol-consistency-webhook.<ext>`

## Execution strategy
### Parallel execution waves
> 单一任务，无需并行化。1 个 todo 涵盖实现 + 测试。

### Dependency matrix
| Todo | Depends on | Blocks | Can parallelize with |
| --- | --- | --- | --- |
| 1 | — | F1-F4 | — |

## Todos
> Implementation + Test = ONE todo. Never separate.
<!-- APPEND TASK BATCHES BELOW THIS LINE WITH edit/apply_patch - never rewrite the headers above. -->
- [x] 1. 在 SingBoxNode webhook 加 inbound 协议一致性校验 + 补测试

  **What to do:**

  **1.1 修改 `internal/webhook/singboxnode_webhook.go`**

  在 `validateSingBoxNode()` 函数末尾（`return nil` 之前，即当前 line 153 之前）插入新的校验块。注意：webhook 包不得 import `internal/configengine`（避免不必要的依赖），在 webhook 包内内联 effective protocol 逻辑。

  在 `validateSingBoxNode` 函数内、现有所有校验之后、`if len(allErrs) > 0` 之前，加入以下逻辑（伪代码，执行者需适配实际语法）：

  ```go
  // 校验 inbound 角色节点的协议一致性
  isInbound := false
  for _, role := range node.Spec.Roles {
      if role == v1alpha1.ProxyRoleInbound {
          isInbound = true
          break
      }
  }
  if isInbound {
      // inbound 节点必须声明 SupportedProtocols
      if len(node.Spec.SupportedProtocols) == 0 {
          allErrs = append(allErrs, field.Required(
              field.NewPath("spec", "supportedProtocols"),
              "inbound nodes must declare at least one supported protocol with a port",
          ))
      } else {
          // effective inbound protocol 必须在 SupportedProtocols 中
          effectiveProto := node.Spec.InboundProtocol
          if effectiveProto == "" {
              effectiveProto = "hysteria2"
          }
          found := false
          for _, p := range node.Spec.SupportedProtocols {
              if p.Protocol == effectiveProto {
                  found = true
                  break
              }
          }
          if !found {
              allErrs = append(allErrs, field.Invalid(
                  field.NewPath("spec", "inboundProtocol"),
                  node.Spec.InboundProtocol,
                  fmt.Sprintf("effective inbound protocol %q must be present in spec.supportedProtocols (add an entry with protocol=%q and the desired port)", effectiveProto, effectiveProto),
              ))
          }
      }
  }
  ```

  **1.2 在 `internal/webhook/webhook_test.go` 补充测试用例**

  在 `TestSingBoxNodeWebhook_ValidateCreate` 函数内追加以下子测试（用 `t.Run`），参考现有测试的构造方式（如 line 245-261 "accepts valid SingBoxNode with IP" 的构造模式）。**所有 ProtocolConfig 必须用完整 Go struct literal：`v1alpha1.ProtocolConfig{Protocol: "xxx", Port: 12345}`**：

  1. `"rejects inbound node with empty supportedProtocols"` — `Roles: []v1alpha1.ProxyRole{v1alpha1.ProxyRoleInbound}`, `SupportedProtocols: nil` → 期望 error 含 "supportedProtocols"
  2. `"rejects inbound node when InboundProtocol not in SupportedProtocols"` — `Roles: [inbound]`, `InboundProtocol: "naive"`, `SupportedProtocols: []v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 50443}}` → 期望 error 含 "inboundProtocol" 且含 "naive"
  3. `"rejects inbound node when default hysteria2 not in SupportedProtocols"` — `Roles: [inbound]`, `InboundProtocol: ""` (空,默认hy2), `SupportedProtocols: []v1alpha1.ProtocolConfig{{Protocol: "vless", Port: 10443}}` → 期望 error 含 "hysteria2"
  4. `"accepts inbound node with InboundProtocol in SupportedProtocols"` — `Roles: [inbound]`, `InboundProtocol: "naive"`, `SupportedProtocols: []v1alpha1.ProtocolConfig{{Protocol: "naive", Port: 50443}}` → 期望 nil error
  5. `"accepts inbound node with default hysteria2 in SupportedProtocols"` — `Roles: [inbound]`, `InboundProtocol: ""` (空), `SupportedProtocols: []v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 50443}}` → 期望 nil error
  6. `"does not validate SupportedProtocols for outbound-only node"` — `Roles: [outbound]`, `SupportedProtocols: nil`, `InboundProtocol: "naive"` → 期望 nil error（纯 outbound 节点不校验协议一致性）
  7. `"accepts dual-role node with consistent protocol config"` — `Roles: [inbound, outbound]`, `InboundProtocol: "hysteria2"`, `SupportedProtocols: []v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 50443}}` → 期望 nil error（dual-role 节点只要有合法 inbound 配置就通过）

  在 `TestSingBoxNodeWebhook_ValidateUpdate` 函数内追加 1 个子测试：
  8. `"rejects update that creates protocol mismatch"` — old 节点 `InboundProtocol: "hysteria2"` + `SupportedProtocols: []v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 50443}}`（一致），newNode 改 `InboundProtocol: "naive"` 但 `SupportedProtocols` 不变 `[]v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 50443}}`（不一致）→ 期望 error 含 "naive"

  **Must NOT do:**
  - 不得 import `internal/configengine`（在 webhook 包内内联 effective protocol 逻辑）
  - 不得修改 `Default()` 函数
  - 不得修改 `ValidateUpdate` 签名
  - 不得删除任何现有测试用例
  - 不得运行 `make manifests` 或 `make generate`（不涉及 CRD 变更）
  - 新校验块必须插入在 line 148（`seenAllowedOutbounds` 循环的 `}` 之后）与 line 150（`if len(allErrs) > 0`）之间——**不是** line 153 的 `return nil` 之前（那样新校验只在没有其他错误时才执行，不对）

  **必须更新的现有测试 fixture（Metis review 发现的 breaking changes）：**

  新校验会拒绝 "inbound 节点 + InboundProtocol 为空(默认 hysteria2) + SupportedProtocols 不含 hysteria2" 的配置。以下现有测试用例的 fixture 属于这种不一致模式，必须修正为一致配置：

  1. `webhook_test.go:245-261` `"accepts valid SingBoxNode with IP"` — 当前 `Roles=[inbound]`, `InboundProtocol=""`(默认hy2), `SupportedProtocols=[{vless, 8443}]` → **会被新校验拒绝**。修正：加 `InboundProtocol: "vless"`（让 effective protocol = vless ∈ SupportedProtocols）
  2. `webhook_test.go:458-471` `"accepts both inbound and outbound roles"` — 当前 `Roles=[inbound,outbound]`, `SupportedProtocols=nil` → **会被新校验拒绝**（inbound 角色 + 空 SupportedProtocols）。修正：加 `SupportedProtocols: []v1alpha1.ProtocolConfig{{Protocol: "hysteria2", Port: 50443}}` 和 `InboundProtocol: "hysteria2"`（或留空让默认值生效）
  3. **检查所有其他现有测试**：用 `grep -n "ProxyRoleInbound" internal/webhook/webhook_test.go` 找到所有 inbound 测试 fixture，逐一核对 InboundProtocol + SupportedProtocols 一致性。特别注意 port-conflict / duplicate-protocol / NodePort-range 测试（它们用 `SupportedProtocols=[{vless,...}]` 但 `InboundProtocol=""`）—— 这些测试期望**被拒绝**（因为有 port 冲突等问题），所以新校验的拒绝不会让它们失败（它们本就期望 error），但错误消息会变。如果测试断言了特定错误消息（如 `strings.Contains(err.Error(), "conflict")`），需确认新校验不会改变该断言结果（新校验在 `allErrs` 里追加，不替换，所以 "conflict" 仍会在错误消息里）。

  **集群兼容性（已验证）：** 所有 6 个 inbound-capable 集群节点的 InboundProtocol/SupportedProtocols 已一致（dc-jp/hytron-hk/xtom-jp/xtom-sjc: effective=hysteria2 ∈ [vless,trojan,hysteria2]; lightcore-hk: effective=hysteria2 ∈ [hysteria2]; sc-hk: effective=naive ∈ [naive]）。部署 webhook 后不会锁死任何现有节点的更新。

  **Parallelization:** Wave 1 | Blocked by: 无 | Blocks: F1-F4

  **References (executor has NO interview context - be exhaustive):**
  - `internal/webhook/singboxnode_webhook.go:37-39` — `Default()` 空实现，保持不变
  - `internal/webhook/singboxnode_webhook.go:45-47` — `ValidateUpdate` 签名，保持不变
  - `internal/webhook/singboxnode_webhook.go:53-154` — `validateSingBoxNode` 当前完整实现，新校验插入在 line 148（`seenAllowedOutbounds` 循环结束）之后、line 150（`if len(allErrs) > 0`）之前
  - `internal/webhook/singboxnode_webhook.go:19-29` — 当前 import 块，注意已 import `fmt`、`net`、`validation/field`、`admission`；需要确认 `v1alpha1` 已 import（line 28，是）
  - `api/v1alpha1/singboxnode_types.go:27-30` — `ProxyRoleInbound` 常量定义
  - `api/v1alpha1/singboxnode_types.go:42-52` — `ProtocolConfig` struct（`Protocol string` + `Port int32`）
  - `api/v1alpha1/singboxnode_types.go:68-77` — `SupportedProtocols []ProtocolConfig` + `InboundProtocol string` 字段定义
  - `internal/configengine/engine.go:227-232` — `EffectiveInboundProtocol` 参考实现（webhook 内联同样逻辑：空则 "hysteria2"）
  - `internal/webhook/webhook_test.go:14-37` — `TestSingBoxNodeWebhook_Default` 测试构造模式
  - `internal/webhook/webhook_test.go:39-276` — `TestSingBoxNodeWebhook_ValidateCreate` 现有子测试，新子测试追加在其末尾（line 276 之前）
  - `internal/webhook/webhook_test.go:245-261` — "accepts valid SingBoxNode with IP" 子测试，作为合法配置的构造参考
  - `internal/webhook/webhook_test.go:474-500` — `TestSingBoxNodeWebhook_ValidateUpdate` 当前实现，新子测试 7 追加在其末尾（line 500 之前）
  - `internal/webhook/webhook_test.go:1-12` — 测试文件 import 块（已有 `context`、`strings`、`testing`、`corev1`、`v1alpha1`、`webhook`）

  **Acceptance criteria (agent-executable):**
  - `go build ./internal/webhook/...` 退出码 0
  - `go vet ./internal/webhook/...` 退出码 0
  - `go test ./internal/webhook/... -v` 全部通过（含新增 7 个子测试 + 所有现有测试）
  - `grep -n "configengine" internal/webhook/singboxnode_webhook.go` 返回空（未引入 configengine 依赖）
  - `grep -c "func TestSingBoxNodeWebhook_ValidateCreate" internal/webhook/webhook_test.go` = 1（未新增函数，只在现有函数内追加子测试）

  **QA scenarios (name the exact tool + invocation):**

  ```
  Scenario (happy): 合法的 inbound 节点配置通过校验
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/webhook/ -run TestSingBoxNodeWebhook_ValidateCreate -v 2>&1 | grep -E "accepts inbound node with (InboundProtocol|default hysteria2) in SupportedProtocols"
    Expected Result: 2 行匹配，且无 FAIL
    Evidence: .omo/evidence/task-1-happy-valid-config.txt

  Scenario (happy): 纯 outbound 节点不校验协议一致性
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/webhook/ -run TestSingBoxNodeWebhook_ValidateCreate -v 2>&1 | grep "does not validate SupportedProtocols for outbound-only"
    Expected Result: 1 行匹配，且无 FAIL
    Evidence: .omo/evidence/task-1-happy-outbound-skip.txt

  Scenario (failure): inbound 节点 SupportedProtocols 为空被拒绝
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/webhook/ -run TestSingBoxNodeWebhook_ValidateCreate -v 2>&1 | grep "rejects inbound node with empty supportedProtocols"
    Expected Result: 1 行匹配，且无 FAIL
    Evidence: .omo/evidence/task-1-fail-empty-protocols.txt

  Scenario (failure): InboundProtocol 不在 SupportedProtocols 中被拒绝
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/webhook/ -run TestSingBoxNodeWebhook_ValidateCreate -v 2>&1 | grep "rejects inbound node when InboundProtocol not in SupportedProtocols"
    Expected Result: 1 行匹配，且无 FAIL
    Evidence: .omo/evidence/task-1-fail-protocol-mismatch.txt

  Scenario (failure): 默认 hysteria2 不在 SupportedProtocols 中被拒绝
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/webhook/ -run TestSingBoxNodeWebhook_ValidateCreate -v 2>&1 | grep "rejects inbound node when default hysteria2 not in SupportedProtocols"
    Expected Result: 1 行匹配，且无 FAIL
    Evidence: .omo/evidence/task-1-fail-default-mismatch.txt

  Scenario (failure): update 导致协议不一致被拒绝
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/webhook/ -run TestSingBoxNodeWebhook_ValidateUpdate -v 2>&1 | grep "rejects update that creates protocol mismatch"
    Expected Result: 1 行匹配，且无 FAIL
    Evidence: .omo/evidence/task-1-fail-update-mismatch.txt

  Scenario (regression): 所有现有 webhook 测试仍通过
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/webhook/... -v 2>&1 | tail -5
    Expected Result: 末行含 "ok" 且无 "FAIL"
    Evidence: .omo/evidence/task-1-regression-all.txt

  Scenario (no configengine import): webhook 未引入 configengine 依赖
    Tool: Bash
    Steps:
      1. grep -n "configengine" internal/webhook/singboxnode_webhook.go
    Expected Result: 无匹配（返回空，退出码 1）
    Evidence: .omo/evidence/task-1-no-configengine-import.txt
  ```

  **Commit:** Y | `feat(webhook): validate inbound protocol consistency between InboundProtocol and SupportedProtocols`

## Final verification wave
> Runs in parallel after ALL todos. ALL must APPROVE. Surface results and wait for the user's explicit okay before declaring complete.
- [x] F1. Plan compliance audit — Must Have [7/7] | Must NOT Have [7/7] | VERDICT: APPROVE
- [x] F2. Code quality review — Build [PASS] | Vet [PASS] | Tests [61 pass/0 fail] | VERDICT: APPROVE (pre-existing issues noted: isValidHostname permissive, 4 dead User subtests — not from this change)
- [x] F3. Real manual QA — Tests [8/8 new pass] | Evidence [6/6 present] | configengine import [CLEAN] | VERDICT: APPROVE
- [x] F4. Scope fidelity check — Files changed [2/2 expected] | Relay unchanged [YES] | Scope creep [CLEAN] | VERDICT: APPROVE

## Commit strategy
- 单次提交：`feat(webhook): validate inbound protocol consistency between InboundProtocol and SupportedProtocols`
- 文件：`internal/webhook/singboxnode_webhook.go`, `internal/webhook/webhook_test.go`
- Pre-commit hook：`go test ./internal/webhook/...`

## Success criteria

### Verification commands
```bash
go build ./...                                              # 预期: 无输出，exit 0
go test ./internal/webhook/... -v                           # 预期: 所有测试 PASS（含 8 个新子测试 + 修正后的现有测试）
go test ./...                                               # 预期: 全部 ok（回归无破坏）
grep -n "configengine" internal/webhook/singboxnode_webhook.go  # 预期: 空（未引入依赖）
git diff --name-only                                        # 预期: 只有 2 个文件改动
```

### Final checklist
- [ ] inbound 节点 SupportedProtocols 为空时被拒绝
- [ ] inbound 节点 InboundProtocol 不在 SupportedProtocols 时被拒绝
- [ ] inbound 节点默认 hysteria2 不在 SupportedProtocols 时被拒绝
- [ ] 合法配置（InboundProtocol ∈ SupportedProtocols）通过校验
- [ ] 纯 outbound 节点不触发协议校验
- [ ] dual-role 节点（inbound+outbound）有合法 inbound 配置时通过校验
- [ ] update 导致不一致时被拒绝
- [ ] 现有测试 fixture 已修正为一致配置（"accepts valid SingBoxNode with IP" 加 InboundProtocol="vless"，"accepts both inbound and outbound roles" 加 SupportedProtocols）
- [ ] 所有现有测试通过（无回归）
- [ ] Default() 未改、ValidateUpdate 签名未改
- [ ] 无 configengine import、无 CRD 变更、无 relay 改动
