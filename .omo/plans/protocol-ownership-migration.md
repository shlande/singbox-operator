# Protocol Ownership Migration: User → Inbound SingBoxNode

## TL;DR

> **Quick Summary**: 将协议归属从 `User.Spec.Protocol` 迁移到 `SingBoxNode.Spec.InboundProtocol`，inbound 节点通过可选字段强制指定接入协议，默认使用 hysteria2；用户不再指定协议，所有用户被节点的有效协议服务。
>
> **Deliverables**:
> - `User.Spec.Protocol` 字段删除（CRD、webhook、CLI、metrics 全部清理）
> - `SingBoxNode.Spec.InboundProtocol` 新字段（可选，enum，默认 hysteria2）
> - configengine 重写：按节点有效协议服务所有用户，不再按用户协议过滤
> - controller 重写：用户匹配逻辑不再依赖协议，改为匹配所有 inbound 节点
> - client_config.go 重写：协议来源改为节点
> - 所有受影响测试文件更新
> - `make manifests && make generate` 重新生成 CRD
>
> **Estimated Effort**: Medium
> **Parallel Execution**: YES - 2 waves
> **Critical Path**: Task 1 (API 类型) → Task 2 (configengine) + Task 3 (controller) + Task 4 (apiserver) → Task 5 (webhook+CLI) → Task 6 (tests) → F1-F4

---

## Context

### Original Request
> 调整当前的协议模式，用户不允许指定协议，改为由inbound类型的节点支持指定自己的协议。默认情况下依然是使用hy2协议，但是如果节点强制指定了协议则inbound时只使用节点指定的协议。该行为不影响node之间进行forward时的socks协议

### Interview Summary
**Key Discussions**:
- 协议归属从 User 移到 SingBoxNode（inbound 角色）
- 默认协议：hysteria2
- 强制指定后：inbound 只用该协议服务所有用户
- inter-node SOCKS relay 不受影响

**Research Findings**:
- `user.Spec.Protocol` 被 4 个 engine 函数、3 个 controller 函数、1 个 apiserver 函数、1 个 webhook、1 个 CLI、1 个 metrics 调用点引用
- `SingBoxNodeSpec` 目前无 InboundProtocol 字段，需新增顶层 string 字段
- `matchingProtocolNodeMapper` 和 `usersInGroupToNodesMapper` 需重设计：协议字段消失后，改为对所有 inbound 节点触发 reconcile
- 6 个测试文件需更新

### Metis Review
**Identified Gaps** (addressed):
- apiserver/client_config.go line 37 (`protocol := input.User.Spec.Protocol`) — 已纳入 Task 4
- cli/user/init.go --protocol flag — 已纳入 Task 5
- buildExperimentalConfig() 第 4 个 engine 函数 — 已纳入 Task 2
- InboundProtocol 字段应在 SingBoxNodeSpec 顶层，非 ProtocolConfig 内 — 设计已采纳
- metrics.ProxyUsersTotal label 变更 — 已纳入 Task 5
- 5 个额外测试文件（非仅 engine_test.go）— 已纳入 Task 6
- +kubebuilder:printcolumn Protocol marker on User CRD — 已纳入 Task 1

---

## Work Objectives

### Core Objective
将协议归属从 `User.Spec.Protocol` 迁移到 `SingBoxNode.Spec.InboundProtocol`，使 inbound 节点成为协议决策者，用户不再指定协议，系统默认使用 hysteria2。

### Concrete Deliverables
- `api/v1alpha1/user_types.go`: 删除 `Protocol` 字段及其 kubebuilder markers
- `api/v1alpha1/singboxnode_types.go`: 新增 `InboundProtocol string` 字段（可选，enum）
- `internal/configengine/engine.go`: 4 个函数重写，不再按 user.Protocol 过滤
- `internal/controller/singboxnode_controller.go`: 3 处用户匹配逻辑重写
- `internal/controller/user_controller.go`: `findMatchingInboundNodes` + metrics 重写
- `internal/apiserver/client_config.go`: 协议来源改为节点
- `internal/webhook/user_webhook.go`: 删除协议 defaulting 和 validation
- `internal/cli/user/init.go`: 删除 --protocol flag
- `internal/metrics/metrics.go`: 调整 ProxyUsersTotal label
- 所有测试文件更新通过 `go test ./...`
- `make manifests && make generate` 重新生成 CRD

### Definition of Done
- [ ] `go build ./...` 无错误
- [ ] `go test ./...` 全部通过
- [ ] `make manifests` 无错误，生成的 CRD 不含 `protocol` 字段（User）且含 `inboundProtocol` 字段（SingBoxNode）
- [ ] `make generate` 无错误
- [ ] `grep -r "Spec.Protocol" --include="*.go" .` 返回空（除注释外）

### Must Have
- `SingBoxNode.Spec.InboundProtocol` 为可选字段，空值时 inbound 使用 hysteria2
- 当 `InboundProtocol` 有值时，inbound 只用该协议服务所有用户（不再看 SupportedProtocols 匹配）
- inter-node SOCKS relay（`buildRelayInbound`、`buildOutboundNodeOutbounds`）完全不变
- 所有现有测试需更新并通过（不允许删除测试）

### Must NOT Have (Guardrails)
- 不得修改 `buildRelayInbound` 或 `buildOutboundNodeOutbounds` 的 socks 逻辑
- 不得删除任何已有测试，只允许更新
- 不得在 `ProtocolConfig` 结构体内添加 `InboundProtocol`（应在 SingBoxNodeSpec 顶层）
- 不得为 SingBoxNode webhook 添加新的协议强制验证（超出本次范围）
- 不得修改 UserGroup 逻辑
- 不得修改 CustomRoute 逻辑
- 不得为 User 保留任何协议相关字段（彻底删除）

---

## Verification Strategy (MANDATORY)

> **ZERO HUMAN INTERVENTION** - ALL verification is agent-executed.

### Test Decision
- **Infrastructure exists**: YES (Go testing with `go test`)
- **Automated tests**: Tests-after (更新现有测试)
- **Framework**: `go test ./...`

### QA Policy
每个 Task 必须包含 agent-executed QA scenarios。证据保存到 `.omo/evidence/task-{N}-{scenario-slug}.txt`。

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (Start Immediately - API 类型变更，其他所有 task 的基础):
└── Task 1: API 类型变更 (user_types.go + singboxnode_types.go) [quick]

Wave 2 (After Wave 1 - 所有实现并行):
├── Task 2: configengine 重写 (engine.go 的 4 个函数) [unspecified-high]
├── Task 3: controller 重写 (singboxnode_controller.go + user_controller.go) [unspecified-high]
├── Task 4: apiserver client_config.go 重写 [quick]
└── Task 5: webhook + CLI + metrics 清理 [quick]

Wave 3 (After Wave 2 - 测试更新):
└── Task 6: 全量测试文件更新 [unspecified-high]

Wave FINAL (After ALL tasks):
├── Task F1: Plan Compliance Audit (oracle)
├── Task F2: Code Quality Review (unspecified-high)
├── Task F3: Real manual QA (unspecified-high)
└── Task F4: Scope Fidelity Check (deep)
```

### Dependency Matrix

| Task | Depends On | Blocks |
|------|-----------|--------|
| 1    | —         | 2, 3, 4, 5, 6 |
| 2    | 1         | 6 |
| 3    | 1         | 6 |
| 4    | 1         | 6 |
| 5    | 1         | 6 |
| 6    | 2, 3, 4, 5 | F1-F4 |
| F1-F4 | 6        | — |

### Agent Dispatch Summary

- **Wave 1**: 1 task — T1 → `quick`
- **Wave 2**: 4 tasks — T2 → `unspecified-high`, T3 → `unspecified-high`, T4 → `quick`, T5 → `quick`
- **Wave 3**: 1 task — T6 → `unspecified-high`
- **FINAL**: 4 tasks — F1 → `oracle`, F2 → `unspecified-high`, F3 → `unspecified-high`, F4 → `deep`

---

## TODOs

> **FORMAT**: Task labels MUST use bare numbers: `1.`, `2.`, `3.`
> Final Verification Wave labels MUST use `F1.`, `F2.`, etc.

- [x] 1. API 类型变更：删除 User.Protocol，新增 SingBoxNode.InboundProtocol

  **What to do**:
  - `api/v1alpha1/user_types.go`:
    - 删除 `UserSpec.Protocol` 字段（包括 kubebuilder 注释：`// +kubebuilder:validation:Enum=...`、`// +kubebuilder:default=hysteria2`、`// +optional`）
    - 删除 `// +kubebuilder:printcolumn:name="Protocol"...` marker（在 User 类型上方）
    - 删除 `// Protocol is the inbound proxy protocol...` 注释
  - `api/v1alpha1/singboxnode_types.go`:
    - 在 `SingBoxNodeSpec` 中新增字段（在 `SupportedProtocols` 之后）：
      ```go
      // InboundProtocol forces this inbound node to use a specific protocol for all users.
      // When set, only this protocol is used for inbound connections, regardless of SupportedProtocols.
      // When empty, defaults to "hysteria2".
      // Only meaningful for nodes with the inbound role.
      // +kubebuilder:validation:Enum=hysteria2;vless;trojan;socks5;http
      // +optional
      InboundProtocol string `json:"inboundProtocol,omitempty"`
      ```
  - 运行 `make manifests && make generate` 重新生成 CRD 和 DeepCopy

  **Must NOT do**:
  - 不得将 InboundProtocol 放在 ProtocolConfig 内
  - 不得修改 socks relay 相关字段
  - 不得删除 SupportedProtocols（仍需保留，用于端口配置）

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: 单文件字段增删，结构清晰
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO（必须先完成，其他 task 依赖）
  - **Parallel Group**: Wave 1 (独立)
  - **Blocks**: Tasks 2, 3, 4, 5, 6
  - **Blocked By**: None

  **References**:

  **Pattern References**:
  - `api/v1alpha1/singboxnode_types.go:42-52` — `ProtocolConfig` 结构体，新字段参考其 kubebuilder markers 风格
  - `api/v1alpha1/singboxnode_types.go:54-84` — `SingBoxNodeSpec` 结构体，新字段插入位置（SupportedProtocols 之后）
  - `api/v1alpha1/user_types.go:24-41` — 当前 `UserSpec.Protocol` 字段，需完全删除

  **Acceptance Criteria**:
  - [ ] `api/v1alpha1/user_types.go` 中不含 `Protocol` 字段及其所有 markers
  - [ ] `api/v1alpha1/singboxnode_types.go` 中含 `InboundProtocol string` 字段，带正确 enum marker
  - [ ] `make manifests` 成功，`config/crd/bases/` 中 User CRD 不含 `protocol` 字段，SingBoxNode CRD 含 `inboundProtocol` 字段
  - [ ] `make generate` 成功，`zz_generated.deepcopy.go` 更新

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: User CRD 不含 protocol 字段
    Tool: Bash
    Preconditions: make manifests 已运行
    Steps:
      1. grep -r "\"protocol\"" config/crd/bases/*user*.yaml
    Expected Result: 无匹配（返回空）
    Evidence: .omo/evidence/task-1-user-crd-no-protocol.txt

  Scenario: SingBoxNode CRD 含 inboundProtocol 字段
    Tool: Bash
    Preconditions: make manifests 已运行
    Steps:
      1. grep "inboundProtocol" config/crd/bases/*singboxnode*.yaml
    Expected Result: 至少 1 行匹配
    Evidence: .omo/evidence/task-1-sbn-crd-inboundprotocol.txt

  Scenario: Go 编译无错误
    Tool: Bash
    Steps:
      1. go build ./api/...
    Expected Result: 无错误输出，exit code 0
    Evidence: .omo/evidence/task-1-build.txt
  ```

  **Commit**: YES (独立提交)
  - Message: `feat(api): replace User.Protocol with SingBoxNode.InboundProtocol`
  - Files: `api/v1alpha1/user_types.go`, `api/v1alpha1/singboxnode_types.go`, `api/v1alpha1/zz_generated.deepcopy.go`, `config/crd/bases/`

---

- [x] 2. configengine 重写：4 个函数不再按 user.Spec.Protocol 过滤

  **What to do**:
  - 在 `internal/configengine/engine.go` 中新增辅助函数 `effectiveInboundProtocol(node *v1alpha1.SingBoxNode) string`：
    - 如果 `node.Spec.InboundProtocol != ""` 则返回该值
    - 否则返回 `"hysteria2"`
  - 重写 `buildRouteInbounds()`（line 288）：
    - 移除 `if user.Spec.Protocol != proto.Protocol { continue }` 过滤
    - 改为：取节点 `effectiveInboundProtocol(input.Node)` 作为唯一协议
    - 找到该协议对应的 `ProtocolConfig` 取端口（若找不到，跳过此节点的 inbound 生成）
    - 所有用户（不按协议过滤）都加入该协议的 inbound
  - 重写 `buildUsersBlock()`（line 364）：
    - 移除 `if user.Spec.Protocol != protocol { continue }` 过滤
    - 所有用户（满足节点限制）均加入
  - 重写 `buildUserInbounds()`（line 405）：
    - 不再按用户协议去重，改为：取 `effectiveInboundProtocol(input.Node)` 作为唯一协议
    - 找到该协议对应的端口
    - 所有用户加入该协议的 inbound
  - 重写 `buildExperimentalConfig()`（line 526）：
    - 移除 `if user.Spec.Protocol == proto.Protocol` 过滤
    - 所有用户均计入 stats

  **Must NOT do**:
  - 不得修改 `buildRelayInbound()`（socks relay，完全不变）
  - 不得修改 `buildOutboundNodeOutbounds()` 或 `buildRouteOutbounds()`（socks outbound，完全不变）
  - 不得删除 `SupportedProtocols` 的使用（仍需用于查找端口）

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: 逻辑重写，需理解现有 inbound 生成流程
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 3、4、5 并行）
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 6
  - **Blocked By**: Task 1

  **References**:

  **Pattern References**:
  - `internal/configengine/engine.go:288-362` — `buildRouteInbounds()` 当前实现，理解虚拟用户生成逻辑
  - `internal/configengine/engine.go:364-381` — `buildUsersBlock()` 当前实现
  - `internal/configengine/engine.go:405-425` — `buildUserInbounds()` 当前实现
  - `internal/configengine/engine.go:526-581` — `buildExperimentalConfig()` 当前实现
  - `internal/configengine/engine.go:383-403` — `buildInboundEntry()` 保持不变，仍接受 protocol 参数
  - `internal/configengine/engine.go:215-222` — `findProtocolPort()` 用于根据协议名查找端口

  **API/Type References**:
  - `api/v1alpha1/singboxnode_types.go` — 新增的 `InboundProtocol` 字段（Task 1 完成后）

  **Acceptance Criteria**:
  - [ ] `go build ./internal/configengine/...` 无错误
  - [ ] `effectiveInboundProtocol()` 函数存在：空 InboundProtocol → "hysteria2"，非空 → 返回该值
  - [ ] `buildRouteInbounds()` 不含 `user.Spec.Protocol` 引用
  - [ ] `buildUsersBlock()` 不含 `user.Spec.Protocol` 引用
  - [ ] `buildUserInbounds()` 不含 `user.Spec.Protocol` 引用
  - [ ] `buildExperimentalConfig()` 不含 `user.Spec.Protocol` 引用
  - [ ] `buildRelayInbound()` 代码完全不变（通过 diff 验证）

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: effectiveInboundProtocol 默认返回 hysteria2
    Tool: Bash (go test)
    Steps:
      1. 在 engine_test.go 中（或单独验证）：创建一个 InboundProtocol="" 的节点
      2. 验证生成的 inbound type 为 "hysteria2"
    Expected Result: inbound type = "hysteria2"
    Evidence: .omo/evidence/task-2-default-hy2.txt

  Scenario: effectiveInboundProtocol 强制协议生效
    Tool: Bash (go test)
    Steps:
      1. 创建 InboundProtocol="vless" 的节点（SupportedProtocols 含 vless:10443）
      2. 添加用户（无 Protocol 字段）
      3. 验证 inbound type 为 "vless"
    Expected Result: inbound type = "vless"
    Evidence: .omo/evidence/task-2-forced-vless.txt

  Scenario: buildRelayInbound 未被修改
    Tool: Bash
    Steps:
      1. grep -A 10 "func buildRelayInbound" internal/configengine/engine.go
      2. 确认仍为 socks 类型，listen_port = relayContainerPort
    Expected Result: 输出包含 "socks" 和 "relay-socks5"
    Evidence: .omo/evidence/task-2-relay-unchanged.txt
  ```

  **Commit**: YES（与 Task 3、4、5 合并提交或独立）
  - Message: `refactor(configengine): derive protocol from node InboundProtocol, not user`

---

- [x] 3. controller 重写：用户匹配不再依赖协议

  **What to do**:
  - `internal/controller/singboxnode_controller.go`:
    - `collectInput()` line 239: 删除 `if !nodeSupportsProtocol(node, user.Spec.Protocol) { continue }` 过滤。改为：所有用户（满足 UserGroup 限制）都加入 input，不再按协议过滤
    - `matchingProtocolNodeMapper()` line 596: 重写为 `allInboundNodeMapper`（或直接内联）：对所有 inbound 节点触发 reconcile，不再按协议过滤。函数签名保持不变（只改实现），在 `SetupWithManager` 中保持注册
    - `usersInGroupToNodesMapper()` line 616: 移除 `nodeSupportsProtocol(&n, user.Spec.Protocol)` 过滤，改为：对所有 inbound 节点触发 reconcile（只要用户属于该 UserGroup）
    - `nodeSupportsProtocol()` 函数：如果其他地方无调用则可删除，否则保留但不在 inbound 匹配中使用
  - `internal/controller/user_controller.go`:
    - `findMatchingInboundNodes()` line 88: 删除 `nodeSupportsProtocol(&node, user.Spec.Protocol)` 过滤，改为：所有 inbound 节点（满足 UserGroup 限制）均匹配
    - `updateStatus()` line 197: `metrics.ProxyUsersTotal.WithLabelValues(latest.Spec.Protocol).Set(1)` 改为 `metrics.ProxyUsersTotal.WithLabelValues("").Set(1)` 或使用固定标签（见 Task 5 的 metrics 决策）

  **Must NOT do**:
  - 不得修改 UserGroup 过滤逻辑（allowedSet/deniedSet）
  - 不得修改 CustomRoute 相关逻辑
  - 不得删除 `nodeSupportsProtocol` 函数（如有其他调用点）

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: 跨两个 controller 文件，涉及 reconcile 触发逻辑
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 2、4、5 并行）
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 6
  - **Blocked By**: Task 1

  **References**:

  **Pattern References**:
  - `internal/controller/singboxnode_controller.go:228-288` — `collectInput()` 用户收集逻辑
  - `internal/controller/singboxnode_controller.go:596-614` — `matchingProtocolNodeMapper()` 当前实现
  - `internal/controller/singboxnode_controller.go:616-648` — `usersInGroupToNodesMapper()` 当前实现
  - `internal/controller/singboxnode_controller.go:739-746` — `nodeSupportsProtocol()` 函数定义
  - `internal/controller/user_controller.go:88-131` — `findMatchingInboundNodes()` 当前实现
  - `internal/controller/user_controller.go:145-199` — `updateStatus()` 含 metrics 调用

  **Acceptance Criteria**:
  - [ ] `go build ./internal/controller/...` 无错误
  - [ ] `collectInput()` 不含 `user.Spec.Protocol` 引用
  - [ ] `matchingProtocolNodeMapper()` 不含 `user.Spec.Protocol` 引用
  - [ ] `usersInGroupToNodesMapper()` 不含 `user.Spec.Protocol` 引用
  - [ ] `findMatchingInboundNodes()` 不含 `user.Spec.Protocol` 引用
  - [ ] UserGroup allow/deny 逻辑代码未变（通过 grep 验证 `allowedSet`/`deniedSet` 仍存在）

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: collectInput 包含所有用户（不按协议过滤）
    Tool: Bash
    Steps:
      1. grep -n "Spec.Protocol" internal/controller/singboxnode_controller.go
    Expected Result: 无匹配（返回空）
    Evidence: .omo/evidence/task-3-no-protocol-ref.txt

  Scenario: UserGroup 过滤逻辑保留
    Tool: Bash
    Steps:
      1. grep -n "allowedSet\|deniedSet\|IsNodeAllowed" internal/controller/singboxnode_controller.go
    Expected Result: 至少 3 行匹配（allowedSet, deniedSet, IsNodeAllowed 各至少 1 次）
    Evidence: .omo/evidence/task-3-usergroup-preserved.txt
  ```

  **Commit**: YES
  - Message: `refactor(controller): match all inbound nodes for users, remove protocol filter`

---

- [x] 4. apiserver client_config.go 重写：协议来源改为节点

  **What to do**:
  - `internal/apiserver/client_config.go`:
    - line 37: 删除 `protocol := input.User.Spec.Protocol`
    - 在 `for _, inboundNode := range input.InboundNodes` 循环内，为每个 inboundNode 计算其有效协议：
      ```go
      protocol := effectiveNodeProtocol(inboundNode)
      ```
    - 新增辅助函数（或复用 configengine 的）：
      ```go
      func effectiveNodeProtocol(node *v1alpha1.SingBoxNode) string {
          if node.Spec.InboundProtocol != "" {
              return node.Spec.InboundProtocol
          }
          return "hysteria2"
      }
      ```
    - `supportsProtocol()` 调用保持不变（仍用于检查节点是否在 SupportedProtocols 中有该协议对应的端口）
    - `findEntryEndpoint()` 调用改为传入 `protocol`（现在是节点决定的协议）
    - `buildProxyOutbound()` 调用改为传入节点决定的 `protocol`

  **Must NOT do**:
  - 不得修改 `resolveOutboundNodes()` 逻辑（outbound 节点解析不变）
  - 不得修改 `buildProxyOutbound()` 函数签名或逻辑（hysteria2 TLS 处理保持不变）

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: 单文件，逻辑清晰，主要是变量来源切换
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 2、3、5 并行）
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 6
  - **Blocked By**: Task 1

  **References**:

  **Pattern References**:
  - `internal/apiserver/client_config.go:36-86` — `BuildClientConfig()` 当前实现，重点看 line 37-49
  - `internal/apiserver/client_config.go:88-95` — `supportsProtocol()` 保持不变
  - `internal/apiserver/client_config.go:157-178` — `buildProxyOutbound()` 保持不变

  **Acceptance Criteria**:
  - [ ] `go build ./internal/apiserver/...` 无错误
  - [ ] `client_config.go` 不含 `input.User.Spec.Protocol` 引用
  - [ ] `effectiveNodeProtocol()` 函数存在（或等效逻辑内联）
  - [ ] `supportsProtocol()` 和 `buildProxyOutbound()` 函数签名/实现未变

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: BuildClientConfig 不引用 User.Protocol
    Tool: Bash
    Steps:
      1. grep -n "Spec.Protocol\|User.Protocol" internal/apiserver/client_config.go
    Expected Result: 无匹配
    Evidence: .omo/evidence/task-4-no-user-protocol.txt

  Scenario: 编译通过
    Tool: Bash
    Steps:
      1. go build ./internal/apiserver/...
    Expected Result: exit code 0，无错误
    Evidence: .omo/evidence/task-4-build.txt
  ```

  **Commit**: YES
  - Message: `refactor(apiserver): derive protocol from inbound node, not user`

---

- [x] 5. webhook + CLI + metrics 清理

  **What to do**:
  - `internal/webhook/user_webhook.go`:
    - 删除 `Default()` 中的 `if user.Spec.Protocol == "" { user.Spec.Protocol = "hysteria2" }` 块
    - 删除 `validateUser()` 中的协议校验块（lines 63-69）
    - 删除 `validProtocols` map
    - 如果 `Default()` 和 `ValidateCreate/Update` 变空，保留函数签名但 body 只有 `return nil`
  - `internal/cli/user/init.go`:
    - 删除 `var protocol string` 声明
    - 删除 `validProtocols` 验证块（lines 29-33）
    - 删除 `Spec: proxyv1alpha1.UserSpec{Protocol: protocol, ...}` 中的 `Protocol` 字段
    - 删除 `cmd.Flags().StringVar(&protocol, "protocol", ...)` 行
  - `internal/metrics/metrics.go`:
    - `ProxyUsersTotal` 的 label `"protocol"` 改为 `"namespace"`（或直接删除该 label，改为无 label 的 Gauge）
    - 更新 `internal/controller/user_controller.go` 中对应的 `.WithLabelValues()` 调用
    - 推荐：将 `ProxyUsersTotal` 改为无 label：`prometheus.NewGauge(...)` 而非 `NewGaugeVec`，避免 label 语义混乱

  **Must NOT do**:
  - 不得修改 SingBoxNode webhook 的验证逻辑
  - 不得删除 `validateUser()` 函数本身（保留其他验证：authSecret、userGroupRef）

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: 多文件但每处改动简单，主要是删除
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 2、3、4 并行）
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 6
  - **Blocked By**: Task 1

  **References**:

  **Pattern References**:
  - `internal/webhook/user_webhook.go:33-88` — 当前实现，删除协议相关部分
  - `internal/cli/user/init.go:17-83` — 当前实现，删除 --protocol flag 相关
  - `internal/metrics/metrics.go:19-25` — `ProxyUsersTotal` 定义
  - `internal/controller/user_controller.go:197` — `metrics.ProxyUsersTotal.WithLabelValues(latest.Spec.Protocol).Set(1)` 需同步修改

  **Acceptance Criteria**:
  - [ ] `user_webhook.go` 的 `validateUser()` 不含 `Protocol` 相关验证
  - [ ] `user_webhook.go` 的 `Default()` 不含 `Protocol` 赋值
  - [ ] `cli/user/init.go` 不含 `--protocol` flag
  - [ ] `cli/user/init.go` 不含 `Protocol:` 字段赋值
  - [ ] `metrics.go` 的 `ProxyUsersTotal` 已调整（无 protocol label）
  - [ ] `go build ./internal/webhook/... ./internal/cli/... ./internal/metrics/... ./internal/controller/...` 无错误

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: CLI init 命令无 --protocol flag
    Tool: Bash
    Steps:
      1. grep -n "protocol" internal/cli/user/init.go
    Expected Result: 无匹配（或仅注释）
    Evidence: .omo/evidence/task-5-cli-no-protocol.txt

  Scenario: webhook validateUser 无协议验证
    Tool: Bash
    Steps:
      1. grep -n "validProtocols\|Spec.Protocol" internal/webhook/user_webhook.go
    Expected Result: 无匹配
    Evidence: .omo/evidence/task-5-webhook-no-protocol.txt

  Scenario: 全量编译通过
    Tool: Bash
    Steps:
      1. go build ./...
    Expected Result: exit code 0，无错误
    Evidence: .omo/evidence/task-5-build-all.txt
  ```

  **Commit**: YES
  - Message: `chore: remove user protocol from webhook, CLI, and metrics`

---

- [x] 6. 全量测试文件更新

  **What to do**:
  更新以下 6 个测试文件，使其适配新 API（User 无 Protocol 字段，SingBoxNode 有 InboundProtocol 字段）：

  **1. `internal/configengine/engine_test.go`**:
  - `makeUser(name, protocol string)` → `makeUser(name string)`（删除 protocol 参数）
  - 所有 `makeUser("name", "protocol")` 调用改为 `makeUser("name")`
  - 节点协议改为通过 `SingBoxNode.Spec.InboundProtocol` 或 `SupportedProtocols` 控制
  - 测试逻辑：节点指定 InboundProtocol 后，所有用户使用该协议
  - 现有测试 TestConfigEngine_Hysteria2Inbound、TestConfigEngine_Hysteria2VirtualUsers 等：节点设 InboundProtocol="hysteria2"（或留空），用户无 Protocol 字段，验证 inbound 仍为 hysteria2
  - 现有测试 TestConfigEngine_InboundNode（vless）：节点设 InboundProtocol="vless"，验证 inbound 为 vless

  **2. `api/v1alpha1/types_test.go`**:
  - line 64: `Protocol: "vless"` → 删除该行（UserSpec 无 Protocol 字段）

  **3. `internal/apiserver/handler_test.go`**:
  - `makeUser(name, protocol, secretName string)` → `makeUser(name, secretName string)`（删除 protocol 参数）
  - 所有 `makeUser("name", "protocol", "secret")` 调用改为 `makeUser("name", "secret")`
  - 节点协议改为通过 inboundNode.Spec.InboundProtocol 控制
  - `makeInboundNode` 调用可能需要新增 InboundProtocol 参数或在测试中直接设置

  **4. `internal/apiserver/client_config_filter_test.go`**:
  - `makeUser("user-alice", "vless", "secret-alice")` → `makeUser("user-alice", "secret-alice")`
  - 所有 `makeUser` 调用同步更新

  **5. `internal/webhook/webhook_test.go`**:
  - 找到 `Protocol:` 字段赋值（lines 336, 337, 351, 352 附近）并删除
  - 确保 User 创建不含 Protocol 字段

  **6. `internal/controller/user_controller_test.go`**:
  - 找到 `user.Spec.Protocol` 相关断言并更新
  - 节点匹配断言改为：所有 inbound 节点均匹配（不按协议过滤）

  **Must NOT do**:
  - 不得删除任何测试函数
  - 不得降低测试覆盖率（仅更新，不删除测试 case）

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: 6 个文件，需理解每个测试的语义并正确更新
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO（依赖 Tasks 2-5 完成）
  - **Parallel Group**: Wave 3
  - **Blocks**: F1-F4
  - **Blocked By**: Tasks 2, 3, 4, 5

  **References**:

  **Pattern References**:
  - `internal/configengine/engine_test.go:15-33` — `makeNode` 和 `makeUser` helper 函数，需修改 makeUser
  - `internal/configengine/engine_test.go:122-221` — Test 1 (vless inbound)，参考如何改为 InboundProtocol 驱动
  - `internal/configengine/engine_test.go:877-935` — Test 14 (hysteria2 inbound)，保留 TLS 验证
  - `internal/apiserver/handler_test.go:1-100` — makeUser helper 定义（在文件某处）
  - `internal/apiserver/client_config_filter_test.go:41-50` — setupBasicInput 使用 makeUser

  **Acceptance Criteria**:
  - [ ] `go test ./...` 全部通过（0 failures）
  - [ ] 6 个测试文件均不含 `Spec.Protocol` 引用
  - [ ] 测试数量不减少（`go test -v ./... | grep "^--- PASS" | wc -l` 不低于修改前）

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: 全量测试通过
    Tool: Bash
    Steps:
      1. go test ./... -v 2>&1 | tail -20
    Expected Result: 最后输出 "ok" 行，无 "FAIL" 行
    Evidence: .omo/evidence/task-6-test-all.txt

  Scenario: 测试文件不含 Spec.Protocol
    Tool: Bash
    Steps:
      1. grep -rn "Spec\.Protocol" --include="*_test.go" .
    Expected Result: 无匹配
    Evidence: .omo/evidence/task-6-no-protocol-in-tests.txt

  Scenario: 非测试文件不含 user.Spec.Protocol
    Tool: Bash
    Steps:
      1. grep -rn "\.Spec\.Protocol" --include="*.go" . | grep -v "_test.go"
    Expected Result: 无匹配（SingBoxNode.Spec.InboundProtocol 不匹配 \.Spec\.Protocol 的 user 上下文）
    Evidence: .omo/evidence/task-6-no-user-protocol-prod.txt
  ```

  **Commit**: YES
  - Message: `test: update all tests for User protocol removal and SingBoxNode.InboundProtocol`
  - Pre-commit: `go test ./...`

---

## Final Verification Wave (MANDATORY — after ALL implementation tasks)

> 4 review agents run in PARALLEL. ALL must APPROVE. Present consolidated results to user and get explicit "okay" before completing.

- [x] F1. **Plan Compliance Audit** — `oracle`
  Read the plan end-to-end. For each "Must Have": verify implementation exists (read files, run grep). For each "Must NOT Have": search codebase for forbidden patterns — reject with file:line if found. Check evidence files exist in .omo/evidence/. Compare deliverables against plan.
  Output: `Must Have [N/N] | Must NOT Have [N/N] | Tasks [N/N] | VERDICT: APPROVE/REJECT`

- [x] F2. **Code Quality Review** — `unspecified-high`
  Run `go build ./...` + `go vet ./...`. Review all changed files for: type assertions without check, empty error handling, unused imports. Check for AI slop: excessive comments, over-abstraction, generic names.
  Output: `Build [PASS/FAIL] | Vet [PASS/FAIL] | Tests [N pass/N fail] | Files [N clean/N issues] | VERDICT`

- [x] F3. **Real Manual QA** — `unspecified-high`
  Run `go test ./... -v`. Verify each task's QA scenario evidence files exist. Test that `grep -rn "Spec\.Protocol" --include="*.go" .` returns empty (no user protocol refs). Verify CRD files reflect changes.
  Output: `Tests [N/N pass] | Evidence [N/N present] | Protocol refs [CLEAN/N issues] | VERDICT`

- [x] F4. **Scope Fidelity Check** — `deep`
  For each task: read "What to do", check actual file changes. Verify 1:1 — everything in spec was built, nothing beyond spec was built. Check "Must NOT do" compliance. Verify SOCKS relay functions unchanged.
  Output: `Tasks [N/N compliant] | Relay unchanged [YES/NO] | Scope creep [CLEAN/N issues] | VERDICT`

---

## Commit Strategy

- **Task 1**: `feat(api): replace User.Protocol with SingBoxNode.InboundProtocol`
- **Task 2**: `refactor(configengine): derive protocol from node InboundProtocol, not user`
- **Task 3**: `refactor(controller): match all inbound nodes for users, remove protocol filter`
- **Task 4**: `refactor(apiserver): derive protocol from inbound node, not user`
- **Task 5**: `chore: remove user protocol from webhook, CLI, and metrics`
- **Task 6**: `test: update all tests for User protocol removal and SingBoxNode.InboundProtocol`

---

## Success Criteria

### Verification Commands
```bash
go build ./...                    # Expected: no output, exit 0
go test ./...                     # Expected: ok for all packages
grep -rn "Spec\.Protocol" --include="*.go" .  # Expected: empty (no user protocol refs)
grep "inboundProtocol" config/crd/bases/*singboxnode*.yaml  # Expected: at least 1 match
grep "\"protocol\"" config/crd/bases/*user*.yaml            # Expected: empty
```

### Final Checklist
- [ ] All "Must Have" present
- [ ] All "Must NOT Have" absent (SOCKS relay unchanged, UserGroup unchanged)
- [ ] All tests pass
- [ ] CRD manifests updated
- [ ] CLI --protocol flag removed
