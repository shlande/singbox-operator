# Node Readiness Detection & Auto-Offline

## TL;DR

> **Quick Summary**: 添加 Kubernetes Node 就绪状态自动检测机制，当节点 NotReady 或被手动标记 `singboxoperator.shlande.top/offline=true` 时，自动将关联节点的 outbound 从客户端配置中排除。
>
> **Deliverables**:
> - 新增 Node watch + predicate 过滤（controller）
> - `resolveOutboundNodes()` 过滤离线节点（API server）
> - SingBoxNode Status 新增 `NodeReady` condition
> - Node 注解 `singboxoperator.shlande.top/offline` 支持强制下线
> - 9 个验收测试覆盖所有场景
>
> **Estimated Effort**: Medium
> **Parallel Execution**: YES — 3 waves
> **Critical Path**: Task 1 → Task 3 → Task 5 → Task 8

---

## Context

### Original Request
Operator 自动检测节点的状态，如果对应的节点当前不是 ready 状态，则自动下线该节点（生成的 client 配置中排除与这个节点关联 outbound 配置项）。

### Interview Summary
**Key Discussions**:
- **触发方式**: 自动检测 Node NotReady + 手动 annotation `singboxoperator.shlande.top/offline=true`
- **恢复策略**: 节点 Ready 后自动上线（除非被 annotation 覆盖）
- **过滤范围**: 只过滤客户端配置（`resolveOutboundNodes`），controller 端 `collectInput()` 不变
- **Controller 行为**: Node NotReady 时跳过 ConfigMap/Deployment 调和，只更新 status
- **健康检查**: 只检查 Node Condition `Type=Ready`, `Status=True`
- **优先级**: 手动 annotation 覆盖自动检测
- **状态反映**: SingBoxNode.Status.Conditions 新增独立 `NodeReady` condition
- **Annotation key**: `singboxoperator.shlande.top/offline`（与 finalizer 域名一致）
- **测试策略**: TDD — RED → GREEN → REFACTOR

**Research Findings**:
- SingBoxNode `spec.nodeRef`: 第 49 行，指向 K8s Node 名
- RBAC for nodes 已声明但未使用：singboxnode_controller.go:69
- `resolveOutboundNodes()`: client_config.go:97 — 客户端配置过滤注入点
- `SetupWithManager`: singboxnode_controller.go:517-532 — Watch 注册点
- 现有测试模式: fake client + `BuildClientConfig()` 直接调用 / Ginkgo envtest
- Best practice: `Watches(source.Kind(mgr.GetCache(), &corev1.Node{}), ...)` + 自定义 predicate

### Metis Review
**Identified Gaps** (addressed):
- 两处配置生成路径需要搞清楚过滤范围 → 只过滤客户端配置
- Annotation key 域名不统一 → 统一为 `singboxoperator.shlande.top/offline`
- NodeReady condition 新增 vs 修改现有 → 新增独立 condition
- Controller 行为在 Node 不健康时 → 跳过 ConfigMap/Deployment 调和
- Field index 注册顺序 → 在 `main.go` 中注册后 `SetupWithManager`
- Node 删除场景处理 → 等同 NotReady

---

## Work Objectives

### Core Objective
让 operator 能够感知底层 Kubernetes Node 的 Ready 状态，在节点不可用时自动将其从客户端代理配置中移除，并在节点恢复时自动重新加入。

### Concrete Deliverables
- `internal/controller/node_health.go` — 新的 Node 健康检查辅助模块
- `api/v1alpha1/singboxnode_types.go` — 新增 `NodeReady` condition 类型常量
- `internal/controller/singboxnode_controller.go` — Node Watch + Reconcile 逻辑修改
- `internal/apiserver/client_config.go` — `resolveOutboundNodes()` 过滤离线节点
- `internal/apiserver/client_config_test.go` — 节点过滤单元测试 (fake client)
- `internal/controller/singboxnode_controller_test.go` — envtest 集成测试
- `cmd/main.go` — 注册 `spec.nodeRef` 字段索引

### Definition of Done
- [ ] `make test` 全部通过
- [ ] 9 个验收测试覆盖所有场景（创建/恢复/annotation/删除/幂等等）
- [ ] Node NotReady → 客户端配置不包含该节点的 outbound
- [ ] Node Ready → 客户端配置重新包含
- [ ] Annotation 强制覆盖自动检测
- [ ] SingBoxNode Status 正确反映 NodeReady condition

### Must Have
- Node watch + `NodeReadyConditionChangedPredicate`
- `resolveOutboundNodes()` 过滤不健康节点的 outbound
- `BuildClientConfig()` 跳过不健康节点的 EntryEndpoints
- Node 注解 `singboxoperator.shlande.top/offline` 支持
- SingBoxNode Status 新增 `NodeReady` condition
- Controller 在 Node 不健康时跳过调和
- 9 个验收测试

### Must NOT Have (Guardrails)
- 不过滤 controller 端 `collectInput()` 中的 outbound（服务端 relay 路由保持不变）
- 不修改现有 `Ready` condition 语义
- 不添加 DiskPressure/MemoryPressure/PIDPressure 等检查
- 不添加 operator 级别 CLI flag（如 `--node-health-check-interval`）
- 不跨命名空间协调
- 不过度抽象（直接内联到现有函数中，不创建多层嵌套的辅助包）

---

## Verification Strategy

> **ZERO HUMAN INTERVENTION** — ALL verification is agent-executed. No exceptions.

### Test Decision
- **Infrastructure exists**: YES
- **Automated tests**: TDD
- **Framework**: Ginkgo/Gomega (envtest) + Go testing (fake client)
- **RED → GREEN → REFACTOR**: 每个任务先写失败测试，然后最小实现

### QA Policy
Every task MUST include agent-executed QA scenarios.
Evidence saved to `.sisyphus/evidence/task-{N}-{scenario-slug}.{ext}`.

- **Backend/API**: Use Bash (go test) — Run specific test, assert output
- **Integration**: Use Bash (make test) — Full test suite pass

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (Start Immediately — 类型定义 + 健康检查辅助 + 字段索引):
├── Task 1: 添加 NodeReady condition 类型常量 [quick]
├── Task 2: 新建 node_health.go 健康检查模块 [quick]
└── Task 3: 注册 spec.nodeRef 字段索引 [quick]

Wave 2 (After Wave 1 — 核心过滤逻辑 + 测试, MAX PARALLEL):
├── Task 4: resolveOutboundNodes 过滤离线节点 [deep]
├── Task 5: BuildClientConfig 跳过离线节点 EntryEndpoints [deep]
├── Task 6: Watch Node + Predicate + Controller 检查 [deep]
└── Task 7: Controller 跳过调和 + NodeReady status [deep]

Wave 3 (After Wave 2 — 验收测试 + 集成验证):
├── Task 8: 客户端配置过滤验收测试 (9 scenarios) [deep]
└── Task 9: Controller 行为验收测试 [deep]

Wave FINAL:
├── Task F1: Plan Compliance Audit (oracle)
├── Task F2: Code Quality Review (unspecified-high)
├── Task F3: Real Manual QA (unspecified-high)
└── Task F4: Scope Fidelity Check (deep)
```

### Dependency Matrix

| Task | Depends On | Blocks | Wave |
|------|-----------|--------|------|
| 1    | -         | 4,6,7,8 | 1    |
| 2    | -         | 4,6,7,8 | 1    |
| 3    | -         | 6,9     | 1    |
| 4    | 1,2       | 8       | 2    |
| 5    | 1,2       | 8       | 2    |
| 6    | 1,2,3     | 9       | 2    |
| 7    | 1,2       | 9       | 2    |
| 8    | 4,5       | F1-F4   | 3    |
| 9    | 6,7       | F1-F4   | 3    |

---

## TODOs

- [x] 1. 添加 `NodeReady` condition 类型常量和 annotation key 常量

  **What to do**:
  - 在 `api/v1alpha1/singboxnode_types.go` 中定义:
    ```go
    const (
        NodeReadyConditionType = "NodeReady"
        OfflineAnnotation      = "singboxoperator.shlande.top/offline"
    )
    ```
  - 运行 `make generate` 重新生成 DeepCopy（无 schema 变更，但确保一致性）
  - 运行现有测试确认无回归

  **Must NOT do**:
  - 不要添加额外的 condition 类型（DiskPressure 等）
  - 不要修改现有 `Ready` condition 语义
  - 不要修改 CRD schema 结构（只用常量定义）

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with Tasks 2, 3)
  - **Blocks**: Tasks 4, 6, 7, 8
  - **Blocked By**: None

  **References**:
  - `api/v1alpha1/singboxnode_types.go:27-31` — 现有 ProxyRole 常量定义模式，按此风格添加新常量
  - `api/v1alpha1/singboxnode_types.go:96` — `Conditions []metav1.Condition` 使用 `+listType=map`/`+listMapKey=type`，新 condition 直接兼容

  **Acceptance Criteria**:
  - [ ] 常量定义在 `api/v1alpha1/singboxnode_types.go` 中可见
  - [ ] `make generate` 无错误
  - [ ] `make test` 现有测试全部通过

  **QA Scenarios**:
  ```
  Scenario: 常量可在其他包中正确引用
    Tool: Bash (go build)
    Preconditions: 常量已定义
    Steps:
      1. go build ./... 验证编译通过
    Expected Result: 编译成功，无错误
    Failure Indicators: 编译错误或 panic
    Evidence: .sisyphus/evidence/task-1-build.pass

  Scenario: 不会破坏现有测试
    Tool: Bash (make test)
    Preconditions: 常量已定义
    Steps:
      1. make test 运行全部测试
    Expected Result: 所有测试 PASS
    Failure Indicators: 任何 FAIL
    Evidence: .sisyphus/evidence/task-1-test.pass
  ```

  **Commit**: YES
  - Message: `feat(api): add NodeReady condition type and offline annotation constants`
  - Files: `api/v1alpha1/singboxnode_types.go`, `api/v1alpha1/zz_generated.deepcopy.go`
  - Pre-commit: `make test`

---

- [x] 2. 新建 `internal/controller/node_health.go` 健康检查模块

  **What to do**:
  - 创建 `internal/controller/node_health.go` 文件
  - 实现 `isNodeReady(node *corev1.Node) bool`：
    - 遍历 `node.Status.Conditions`，找到 `Type == corev1.NodeReady`
    - 检查 `Status == corev1.ConditionTrue`
    - 如果条件不存在或 Status != True，返回 false
  - 实现 `isNodeOfflineAnnotated(node *corev1.Node) bool`：
    - 检查 `node.Annotations["singboxoperator.shlande.top/offline"] == "true"`
  - 实现 `isNodeAvailable(node *corev1.Node) bool`：
    - 组合逻辑：`isNodeReady(node) && !isNodeOfflineAnnotated(node)`
  - 实现 `NodeReadyConditionChangedPredicate` 结构体（内嵌 `predicate.Funcs`）：
    - 覆盖 `UpdateFunc`：比较 old/new Node 的 Ready condition status
    - `CreateFunc` 默认 true
    - `DeleteFunc` 默认 true

  **Must NOT do**:
  - 不要定义额外的 condition 检查（DiskPressure 等）
  - 不要在 predicate 中做多余过滤（只比较 Ready condition 变化）

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with Tasks 1, 3)
  - **Blocks**: Tasks 4, 6, 7, 8
  - **Blocked By**: None

  **References**:
  - `internal/controller/singboxnode_controller.go:41` — `predicate` 包导入方式
  - `internal/controller/singboxnode_controller.go:566-573` — `hasRole` 函数风格，按此模式写 `isNodeReady`
  - Research finding: Node condition 遍历标准模式 — `for _, c := range node.Status.Conditions { if c.Type == corev1.NodeReady { return c.Status == corev1.ConditionTrue } }`
  - Research finding: Predicate 模式 — 使用 `predicate.Funcs` 只覆盖 `UpdateFunc`

  **Acceptance Criteria**:
  - [ ] `isNodeReady()` 对 Ready=True 节点返回 true
  - [ ] `isNodeReady()` 对 Ready=False 节点返回 false
  - [ ] `isNodeReady()` 对无 Ready condition 节点返回 false
  - [ ] `isNodeOfflineAnnotated()` 对 annotation=true 返回 true
  - [ ] `isNodeOfflineAnnotated()` 对无 annotation 返回 false
  - [ ] `isNodeAvailable()` 正确组合 Ready 和 annotation 检查
  - [ ] Predicate UpdateFunc 只在 Ready status 变化时返回 true

  **QA Scenarios**:
  ```
  Scenario: Ready node passes availability check
    Tool: Bash (go test -run TestIsNodeAvailable)
    Preconditions: Node with Ready=True, no annotation
    Steps:
      1. go test -run TestIsNodeAvailable ./internal/controller/ -v
      2. 断言 isNodeAvailable 返回 true
    Expected Result: test PASS
    Evidence: .sisyphus/evidence/task-2-ready.pass

  Scenario: Offline-annotated node fails availability despite Ready
    Tool: Bash (go test -run TestIsNodeOfflineAnnotated)
    Preconditions: Node with Ready=True + annotation offline=true
    Steps:
      1. go test -run TestIsNodeOfflineAnnotated ./internal/controller/ -v
      2. 断言 isNodeAvailable 返回 false
    Expected Result: test PASS
    Evidence: .sisyphus/evidence/task-2-offline.pass
  ```

  **Commit**: YES (groups with Task 1)
  - Message: `feat(controller): add node health check module`
  - Files: `internal/controller/node_health.go`
  - Pre-commit: `go test ./internal/controller/ -run "TestIsNode" -count=1`

---

- [x] 3. 在 `cmd/main.go` 中注册 `spec.nodeRef` 字段索引

  **What to do**:
  - 在 `cmd/main.go` 中，`mgr` 创建后、`SetupWithManager` 之前注册字段索引：
    ```go
    if err := mgr.GetFieldIndexer().IndexField(ctx, &proxyv1alpha1.SingBoxNode{}, "spec.nodeRef", ...); err != nil {
        ...
    }
    ```
  - 验证：`go build ./cmd/...` 编译通过

  **Must NOT do**:
  - 不要在任何 controller 的 `SetupWithManager` 中注册索引

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with Tasks 1, 2)
  - **Blocks**: Tasks 6, 9
  - **Blocked By**: None

  **References**:
  - `cmd/main.go:196-205` — `SingBoxNodeReconciler` 初始化位置，在之前插入索引注册
  - Research finding: CloudNativePG 模式 — O(1) Node→SingBoxNode 映射

  **Acceptance Criteria**:
  - [ ] `go build ./cmd/...` 编译通过
  - [ ] `make test` 现有测试全部通过

  **QA Scenarios**:
  ```
  Scenario: 编译通过且现有测试不中断
    Tool: Bash (go build + make test)
    Steps:
      1. go build ./cmd/...
      2. make test
    Expected Result: 编译成功，所有测试 PASS
    Evidence: .sisyphus/evidence/task-3-build.pass
  ```

  **Commit**: YES (groups with Tasks 1-2)
  - Message: `feat(main): register spec.nodeRef field index`
  - Files: `cmd/main.go`
  - Pre-commit: `go build ./cmd/...`

---

- [x] 4. `resolveOutboundNodes()` 过滤离线节点的 outbound

  **What to do**:
  - 在 `ClientConfigInput` 中新增 `UnavailableNodeNames map[string]bool` 字段
  - 修改 `handleClientConfig()` (handler.go)：列出 namespace 中所有 SingBoxNode，通过 `spec.nodeRef` 获取对应 K8s Node，调用 `isNodeAvailable()`，填充 `UnavailableNodeNames`
  - 修改 `resolveOutboundNodes()`：在添加到 nodes 列表前检查 `input.UnavailableNodeNames[n.Spec.NodeRef]`

  **Must NOT do**:
  - 不要修改 controller 端的 `collectInput()` 函数
  - 不要修改配置生成引擎 `configengine`

  **Recommended Agent Profile**:
  - **Category**: `deep`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Task 5)
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 8
  - **Blocked By**: Tasks 1, 2

  **References**:
  - `internal/apiserver/client_config.go:14-20` — `ClientConfigInput` 结构体
  - `internal/apiserver/client_config.go:97-130` — `resolveOutboundNodes()` 完整实现
  - `internal/apiserver/handler.go:77-108` — `handleClientConfig()`
  - `internal/controller/node_health.go` — 使用 `isNodeAvailable()` 函数

  **Acceptance Criteria**:
  - [ ] Node Ready → node 的 outbound 出现在 BuildClientConfig 结果中
  - [ ] Node NotReady → node 的 outbound 不出现在结果中
  - [ ] Annotation offline=true → node 不出现在结果中

  **QA Scenarios**:
  ```
  Scenario: 健康节点的 outbound 正常包含
    Tool: Bash (go test -run TestResolveOutboundNodes_NodeReady)
    Steps:
      1. go test -run TestResolveOutboundNodes_NodeReady ./internal/apiserver/ -v
    Expected Result: test PASS
    Evidence: .sisyphus/evidence/task-4-ready.pass

  Scenario: NotReady 节点的 outbound 被排除
    Tool: Bash (go test -run TestResolveOutboundNodes_NodeNotReady)
    Steps:
      1. go test -run TestResolveOutboundNodes_NodeNotReady ./internal/apiserver/ -v
    Expected Result: test PASS
    Evidence: .sisyphus/evidence/task-4-notready.pass
  ```

  **Commit**: YES
  - Message: `feat(apiserver): filter offline nodes from client config outbounds`
  - Files: `internal/apiserver/client_config.go`, `internal/apiserver/handler.go`
  - Pre-commit: `go test ./internal/apiserver/ -count=1`

---

- [x] 5. `BuildClientConfig()` 跳过离线节点的 EntryEndpoints

  **What to do**:
  - 修改 `BuildClientConfig()` 中 inbound node 遍历循环 (client_config.go:30-53)
  - 在 `findEntryEndpoint` 后检查 inbound node 的 `spec.nodeRef` 是否在 `UnavailableNodeNames` 中
  - 如果在，跳过该 inbound node

  **Must NOT do**:
  - 不要修改 `buildProxyOutbound()` 或 `findEntryEndpoint()` 函数

  **Recommended Agent Profile**:
  - **Category**: `deep`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Task 4)
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 8
  - **Blocked By**: Tasks 1, 2

  **References**:
  - `internal/apiserver/client_config.go:30-38` — `BuildClientConfig` 循环体起点

  **Acceptance Criteria**:
  - [ ] 不健康 inbound node 不生成任何 proxy outbounds
  - [ ] 其他健康 inbound node 的 outbounds 不受影响

  **QA Scenarios**:
  ```
  Scenario: 不健康 inbound node 不生成 proxy outbound
    Tool: Bash (go test -run TestBuildClientConfig_UnhealthyInbound)
    Steps:
      1. go test -run TestBuildClientConfig_UnhealthyInbound ./internal/apiserver/ -v
    Expected Result: test PASS
    Evidence: .sisyphus/evidence/task-5-unhealthy.pass
  ```

  **Commit**: YES (groups with Task 4)
  - Message: `feat(apiserver): skip unhealthy inbound node entry endpoints`
  - Files: `internal/apiserver/client_config.go`
  - Pre-commit: `go test ./internal/apiserver/ -count=1`

---

- [x] 6. 添加 Node Watch + 自定义 Predicate + Controller 内双重检查

  **What to do**:
  - 在 `SetupWithManager()` 追加 `Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.nodeToSingBoxNodesMapper), builder.WithPredicates(NodeReadyConditionChangedPredicate{}))`
  - 实现 `nodeToSingBoxNodesMapper()`：使用 `client.MatchingFields{"spec.nodeRef": node.Name}` 查询受影响的 SingBoxNode
  - 在 `Reconcile()` 中获取对应 K8s Node 状态
  - Node 不可用时跳过调和，可用时正常调和

  **Must NOT do**:
  - 不要在 predicate 中做复杂逻辑
  - 不要使用 `source.Kind()` — 直接 `Watches()` 即可

  **Recommended Agent Profile**:
  - **Category**: `deep`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Tasks 4, 5, 7)
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 9
  - **Blocked By**: Tasks 1, 2, 3

  **References**:
  - `internal/controller/singboxnode_controller.go:517-532` — 现有 `SetupWithManager`
  - `internal/controller/singboxnode_controller.go:467-485` — `sameRegionNodeMapper` 函数签名
  - `internal/controller/node_health.go` — `NodeReadyConditionChangedPredicate`

  **Acceptance Criteria**:
  - [ ] Node Watch 正确注册到 manager
  - [ ] `go build ./cmd/...` 编译通过

  **QA Scenarios**:
  ```
  Scenario: Node Watch 注册不崩溃
    Tool: Bash (go build ./cmd/...)
    Steps:
      1. go build ./cmd/...
    Expected Result: 编译成功
    Evidence: .sisyphus/evidence/task-6-build.pass
  ```

  **Commit**: YES
  - Message: `feat(controller): add Node watch with Ready-condition predicate`
  - Files: `internal/controller/singboxnode_controller.go`
  - Pre-commit: `go test ./internal/controller/ -count=1`

---

- [x] 7. Controller 在 Node 不健康时跳过调和 + 更新 NodeReady status

  **What to do**:
  - 修改 `Reconcile()`：Node 不可用时跳过 `ensureCredential`/`collectInput`/`reconcile*`，直接更新 status
  - 修改 `updateStatus()`：添加 `NodeReady=True` condition
  - 新增 `updateStatusWithNodeNotReady()`：设置 `NodeReady=False, Reason="NodeNotReady"`

  **Must NOT do**:
  - 不要改变现有 Phase 含义
  - 不要修改 `setDegraded` 函数

  **Recommended Agent Profile**:
  - **Category**: `deep`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Tasks 4, 5, 6)
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 9
  - **Blocked By**: Tasks 1, 2

  **References**:
  - `internal/controller/singboxnode_controller.go:78-155` — `Reconcile()` 流程
  - `internal/controller/singboxnode_controller.go:413-446` — `updateStatus()`
  - `internal/controller/singboxnode_controller.go:448-465` — `setDegraded()` 模式

  **Acceptance Criteria**:
  - [ ] Node NotReady → 不生成 ConfigMap/Deployment
  - [ ] Node NotReady → Status 有 NodeReady=False
  - [ ] Node Ready → 正常生成资源
  - [ ] Node Ready → Status 有 NodeReady=True

  **QA Scenarios**:
  ```
  Scenario: Node NotReady 跳过调和但更新 status
    Tool: Bash (make test)
    Steps:
      1. go test ./internal/controller/ -run "TestNodeReadiness" -v
    Expected Result: test PASS
    Evidence: .sisyphus/evidence/task-7-skip-reconcile.pass
  ```

  **Commit**: YES (groups with Task 6)
  - Message: `feat(controller): skip reconciliation on unhealthy node`
  - Files: `internal/controller/singboxnode_controller.go`
  - Pre-commit: `go test ./internal/controller/ -count=1`

---

- [x] 8. 客户端配置过滤完整验收测试 (9 scenarios)

  **What to do**:
  - 新建 `internal/apiserver/client_config_filter_test.go`
  - 实现 9 个测试场景（使用 fake client）：
    1. 健康节点 → outbound 包含
    2. NotReady → excluded
    3. Recovery → re-included
    4. Annotation offline=true → excluded (even if Ready)
    5. Annotation removal → re-included
    6. Node deleted → excluded
    7. Multiple unhealthy → all excluded
    8. CustomRoute unhealthy → excluded
    9. Dual-role node unhealthy → excluded

  **Must NOT do**:
  - 不要创建需要真实 K8s 集群的测试
  - 不要修改现有测试的断言

  **Recommended Agent Profile**:
  - **Category**: `deep`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Task 9)
  - **Parallel Group**: Wave 3
  - **Blocks**: F1-F4
  - **Blocked By**: Tasks 4, 5

  **References**:
  - `internal/apiserver/handler_test.go:28-73` — `TestBuildClientConfig_TwoOutboundNodes`
  - `internal/apiserver/handler_test.go:421-515` — test helpers
  - `internal/apiserver/client_config.go:14-20` — `ClientConfigInput`

  **Acceptance Criteria**:
  - [ ] `go test ./internal/apiserver/ -run "TestBuildClientConfig" -count=1 -v` → 9 PASS

  **QA Scenarios**:
  ```
  Scenario: 所有 9 个过滤测试通过
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/apiserver/ -count=1 -v
    Expected Result: All PASS
    Evidence: .sisyphus/evidence/task-8-all-tests.pass
  ```

  **Commit**: YES
  - Message: `test(apiserver): add 9 node readiness filtering acceptance tests`
  - Files: `internal/apiserver/client_config_filter_test.go`
  - Pre-commit: `go test ./internal/apiserver/ -count=1`

---

- [x] 9. Controller 行为验收测试 (envtest, 5 scenarios)

  **What to do**:
  - 在 `internal/controller/singboxnode_controller_test.go` 中新增 5 个 envtest 测试：
    1. Skip ConfigMap when Node NotReady
    2. Create ConfigMap when Node Ready
    3. Status transition on Node status change
    4. Multiple SingBoxNodes same NodeRef all affected
    5. Idempotence of NodeReady condition

  **Must NOT do**:
  - 不要修改 `suite_test.go`

  **Recommended Agent Profile**:
  - **Category**: `deep`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Task 8)
  - **Parallel Group**: Wave 3
  - **Blocks**: F1-F4
  - **Blocked By**: Tasks 6, 7

  **References**:
  - `internal/controller/suite_test.go` — envtest 设置
  - `internal/controller/singboxnode_controller_test.go` — 现有测试模式

  **Acceptance Criteria**:
  - [ ] `go test ./internal/controller/ -run "TestNodeReadiness" -count=1 -v` → 5 PASS

  **QA Scenarios**:
  ```
  Scenario: 所有集成测试通过
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/controller/ -run "TestNodeReadiness" -count=1 -v
    Expected Result: 5 PASS
    Evidence: .sisyphus/evidence/task-9-integration.pass
  ```

  **Commit**: YES
  - Message: `test(controller): add envtest integration tests for node readiness`
  - Files: `internal/controller/singboxnode_controller_test.go`
  - Pre-commit: `go test ./internal/controller/ -count=1`

---
