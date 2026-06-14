# sing-box v2rayapi 用户流量采集与 Elasticsearch Data Stream 推送计划

## TL;DR

> **Quick Summary**: 为当前 operator 规划一个独立的、由 controller-runtime manager 托管的后台 collector，周期性从 sing-box v2rayapi 拉取每个用户在每个节点上的上下行流量，并以 at-least-once 语义推送到 Elasticsearch data stream。
>
> **Deliverables**:
> - 独立 collector 子系统设计与接线方案（不塞进 `SingBoxNodeReconciler`）
> - v2rayapi 配置注入与节点发现方案
> - ES data stream sink 与未来多 sink 扩展边界
> - checkpoint / idempotency / backpressure / shutdown flush 的验证方案
> - TDD 驱动的测试与最终验证波次
>
> **Estimated Effort**: Large
> **Parallel Execution**: YES - 3 implementation waves + final verification wave
> **Critical Path**: 1 → 4 → 8 → 11 → F1-F4

---

## Context

### Original Request
用户希望在本项目中增加基于 sing-box v2rayapi 的用户使用流量收集逻辑，支持将用户使用用量直接推送到 Elasticsearch 对应的 data stream 中，并且这部分设计需要支持未来扩展到 MySQL 等其他目标数据源。

### Interview Summary
**Key Discussions**:
- 采集拓扑固定为 **operator pull**：由 operator 定时拉取 sing-box v2rayapi，而不是 sidecar push。
- Elasticsearch data stream 写入能力属于本次正式范围，需要进入项目的正式配置/能力面。
- 记录粒度固定为：`user + node + uplink bytes + downlink bytes + timestamp`。
- 测试策略固定为 **TDD**。
- 交付语义固定为 **at-least-once**。
- 计划阶段默认假设当前 sing-box 镜像/构建已支持 v2rayapi。

**Research Findings**:
- 当前仓库适合把长生命周期服务注册到 `cmd/main.go`，参考 `internal/apiserver/server.go` 的 `manager.Runnable` 模式。
- `internal/controller/singboxnode_controller.go` 已经承担配置/Deployment/Service 生命周期，不适合继续承载流量采集职责。
- 仓库测试基础设施成熟：`make test`、envtest、fake client、httptest、Ginkgo/Gomega 均已可用。
- sing-box v2rayapi 通过 gRPC 暴露用户计数器，命名形如 `user>>>{name}>>>traffic>>>uplink/downlink`，但计数器在内存中维护，sing-box reload/restart 时可能丢失未刷出的统计。
- v2rayapi 默认无 TLS / 无认证，因此 collector 与 sing-box 间的网络暴露边界必须受控。

### Metis Review
**Identified Gaps** (addressed in this plan):
- 明确把 collector 设计为 `manager.Runnable`，并纳入 leader-election-safe 的单活策略。
- 明确把 ES 作为唯一具体 sink，MySQL 仅保留接口扩展能力，不在本次实现中落地。
- 增加 checkpoint、幂等写入、poll timeout、shutdown flush、backpressure 等显式任务。
- 锁定 scope，避免把 dashboard、billing/quota、MySQL 实现、无关 controller 重构混入本次工作。

---

## Work Objectives

### Core Objective
为 operator 增加一套独立于现有 reconciler 的后台流量采集子系统：周期性发现可采集的 sing-box 节点、从 v2rayapi 拉取 per-user/per-node 流量、规范化为稳定记录、通过 at-least-once 语义写入 Elasticsearch data stream，并保留未来切换/扩展 sink 的架构边界。

### Concrete Deliverables
- `internal/` 下新的 usage collector 子系统（collector / source client / sink / checkpoint / record model / config）
- `cmd/main.go` 中的 collector 注册、flag/config 接入
- sing-box 配置生成能力扩展，使节点配置启用 v2rayapi stats 所需字段
- 面向 ES data stream 的写入实现与文档化记录模型
- TDD 测试集：unit / integration / envtest / wiring / regression
- deployment/config patch 方案，使 manager 能持有 ES 参数与 checkpoint 存储

### Definition of Done
- [ ] `make test` 通过，且新增 collector 测试全部 PASS
- [ ] `make lint` 通过，无新增 lint 违规
- [ ] 生成的 sing-box config 明确包含 v2rayapi/stats 所需配置，且用户统计目标与 operator 数据模型一致
- [ ] collector 在无节点、节点动态新增、节点删除、单节点 gRPC 超时、ES 暂时失败场景下均有可验证行为
- [ ] ES sink 对相同业务记录重复写入时满足幂等预期
- [ ] manager 关闭时具备受控 flush/shutdown 行为

### Must Have
- 独立 collector 必须以 `manager.Runnable` 方式挂接到 `cmd/main.go`
- 采集逻辑必须 **不进入** `SingBoxNodeReconciler`
- sink 边界必须支持未来扩展，但本次仅实现 ES sink
- 交付语义必须面向 **at-least-once**，显式考虑 checkpoint、重试、幂等写入
- 记录模型必须至少包含 `user`、`node`、`uplink_bytes`、`downlink_bytes`、`timestamp`
- 测试必须采用 TDD，且每个关键模块都有 agent-executable QA 场景

### Must NOT Have (Guardrails)
- 不实现 MySQL sink
- 不新增 dashboard / Kibana / 可视化工作
- 不实现 billing、quota enforcement、自动封禁等业务能力
- 不把 collector 逻辑混入现有 controller reconcile 主流程
- 不新增新的 CRD，除非在执行中经验证是配置面的唯一合理边界（默认禁止）
- 不把 ES 专属语义硬编码进 collector 核心流程，避免未来 sink 扩展被锁死

---

## Verification Strategy

> **ZERO HUMAN INTERVENTION** - ALL verification is agent-executed. No exceptions.
> Acceptance criteria requiring manual Kibana inspection or manual cluster poking are FORBIDDEN.

### Test Decision
- **Infrastructure exists**: YES
- **Automated tests**: TDD
- **Framework**: `go test` + Ginkgo/envtest + fake client/httptest
- **If TDD**: 每个核心模块遵循 RED → GREEN → REFACTOR

### QA Policy
每个任务都必须包含 agent-executed QA scenarios，并在 `.omo/evidence/` 下产出证据。

- **Library/Module**: `go test` 定点测试 + 输出文件
- **API/Collector lifecycle**: Bash + `go test` / fake server / mocked ES
- **K8s integration**: envtest + fake client + manager wiring tests
- **Config verification**: 配置生成测试直接断言 JSON 结构，不依赖人工查看

---

## Execution Strategy

### Parallel Execution Waves

> 最大化并行度，但保留关键依赖顺序：先模型/配置与基础设施，再 source/sink/checkpoint，再 orchestration/wiring，最后做并行审查。

```
Wave 1 (Start Immediately - foundations + contracts):
├── 1. Collector record model + sink interface + task invariants [quick]
├── 2. Runtime config/flags contract for ES + polling + checkpoint [quick]
├── 3. sing-box v2rayapi config generation design update [unspecified-high]
└── 4. Checkpoint/idempotency strategy and test harness [deep]

Wave 2 (After Wave 1 - source/sink/core modules, MAX PARALLEL):
├── 5. v2rayapi client + parser + timeout behavior [unspecified-high]
├── 6. Elasticsearch data stream sink implementation path [unspecified-high]
├── 7. Node/user discovery and normalization pipeline [quick]
└── 8. Collector orchestration loop (poll → normalize → checkpoint → sink) [deep]

Wave 3 (After Wave 2 - wiring + deployment + resilience):
├── 9. Manager runnable wiring and leader-election behavior [quick]
├── 10. Deployment/config patches for ES/checkpoint/runtime settings [unspecified-high]
└── 11. Integration/regression suite across configengine + collector + shutdown/backpressure [deep]

Wave FINAL (After ALL tasks — 4 parallel reviews):
├── F1. Plan compliance audit (oracle)
├── F2. Code quality review (unspecified-high)
├── F3. Real QA execution (unspecified-high)
└── F4. Scope fidelity check (deep)

Critical Path: 1 → 4 → 8 → 11 → F1-F4
Parallel Speedup: ~55% faster than naive sequential execution
Max Concurrent: 4
```

### Dependency Matrix

- **1**: Blocked By `None` → Blocks `5,6,7,8`
- **2**: Blocked By `None` → Blocks `6,8,9,10`
- **3**: Blocked By `None` → Blocks `11`
- **4**: Blocked By `1` → Blocks `6,8,11`
- **5**: Blocked By `1` → Blocks `8,11`
- **6**: Blocked By `1,2,4` → Blocks `8,11`
- **7**: Blocked By `1` → Blocks `8,11`
- **8**: Blocked By `1,2,4,5,6,7` → Blocks `9,10,11`
- **9**: Blocked By `2,8` → Blocks `11`
- **10**: Blocked By `2,8` → Blocks `11`
- **11**: Blocked By `3,4,5,6,7,8,9,10` → Blocks `F1,F2,F3,F4`

### Agent Dispatch Summary

- **Wave 1**: 4 tasks — `1→quick`, `2→quick`, `3→unspecified-high`, `4→deep`
- **Wave 2**: 4 tasks — `5→unspecified-high`, `6→unspecified-high`, `7→quick`, `8→deep`
- **Wave 3**: 3 tasks — `9→quick`, `10→unspecified-high`, `11→deep`
- **FINAL**: 4 tasks — `F1→oracle`, `F2→unspecified-high`, `F3→unspecified-high`, `F4→deep`

---

## TODOs

> Implementation + Test = ONE Task. Never separate.
> EVERY task MUST have: Recommended Agent Profile + Parallelization info + QA Scenarios.
> **FORMAT**: Task labels use bare numbers (`1.`, `2.` ...). Final wave uses `F1.`, `F2.` ...

- [x] 1. Define usage record and sink contracts

  **What to do**:
  - 在新的 collector 子系统下定义统一记录模型，例如 `UsageRecord` / `UsageBatch` / `SinkResult` 等，用于承载 `user + node + uplink/downlink + timestamp`。
  - 定义 sink 边界接口，至少覆盖批量写入、幂等键输入、flush/close 生命周期需求，但不要把 ES bulk API 细节泄漏进 collector 核心。
  - 定义记录唯一性策略所需字段，例如 document ID 组成（推荐包含 node、user、time bucket/collection timestamp、counter window 标识）。
  - 先写 RED tests，锁定重复记录处理、空批次行为、字段完整性要求，再实现最小合同。

  **Must NOT do**:
  - 不实现 MySQL sink
  - 不把 ES request/response 类型直接作为 collector 核心接口类型
  - 不使用 `map[string]any` 作为长期核心记录模型

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: 以文件内数据结构与接口契约为主，变更面集中且适合快速精确定义
  - **Skills**: `[]`
  - **Skills Evaluated but Omitted**:
    - `refactor`: 当前重点是新契约设计，不是大规模重构

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with Tasks 2, 3, 4)
  - **Blocks**: 5, 6, 7, 8
  - **Blocked By**: None

  **References**:
  - `internal/configengine/engine.go:29-44` - `Input` / `Output` 模型展示了本仓库偏好的显式 typed contract 风格
  - `internal/credmanager/credmanager.go:35-46` - 凭据模型使用小而稳定的 struct，而不是弱类型 map
  - `internal/apiserver/server.go:11-38` - 长生命周期组件需要明确 Start/Shutdown 生命周期边界
  - sing-box source reference: `experimental/v2rayapi/stats.go` - 用户统计最终来源是 v2rayapi counters，而非 operator 内部状态

  **Acceptance Criteria**:
  - [ ] sink 接口能表达批量写入、flush/close 和错误返回，不依赖 ES 特有类型
  - [ ] 记录模型明确包含 user/node/uplink/downlink/timestamp 字段
  - [ ] 幂等键策略在测试中有明确断言

  **QA Scenarios**:
  ```
  Scenario: Record contract rejects incomplete business data
    Tool: Bash (go test)
    Preconditions: record model validation tests written first
    Steps:
      1. Run `go test ./internal/... -run TestUsageRecordValidation -v`
      2. Assert test case with missing user fails validation
      3. Assert test case with missing node fails validation
      4. Assert full record passes validation and preserves exact uplink/downlink values
    Expected Result: PASS with explicit validation assertions
    Failure Indicators: missing-field case silently accepted or bytes/timestamp mutated unexpectedly
    Evidence: .omo/evidence/task-1-record-contract.txt

  Scenario: Sink contract handles duplicate business key deterministically
    Tool: Bash (go test)
    Preconditions: fake sink/unit contract tests exist
    Steps:
      1. Run `go test ./internal/... -run TestUsageSinkContractDuplicateKey -v`
      2. Feed two records with the same logical document key into the sink contract fixture
      3. Assert the fixture reports deterministic duplicate handling semantics
    Expected Result: PASS with one documented semantic path for duplicates
    Failure Indicators: ambiguous duplicate behavior or contract requiring caller-side type switch
    Evidence: .omo/evidence/task-1-sink-duplicate.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-1-record-contract.txt`
  - [ ] `task-1-sink-duplicate.txt`

  **Commit**: NO

- [x] 2. Define runtime configuration surface for collector and ES

  **What to do**:
  - 规划并实现 manager flags / config entry：poll interval、per-node timeout、ES endpoint、auth material、data stream 名称、checkpoint 路径/开关、buffer limits。
  - 让配置面与 `cmd/main.go` 当前 flag 风格一致，避免直接新增 CRD。
  - 先写测试，验证默认值、非法值、空值禁用行为，以及 config parsing/wiring 的可预测性。
  - 明确哪些配置为空时 collector 禁用，哪些为空时报错退出。

  **Must NOT do**:
  - 不通过新 CRD 承载配置
  - 不把 Secret 原文写入日志
  - 不写出只能支持 ES 的硬编码 flag 命名，如果未来会扩展 sink，则至少保留命名空间边界

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: 以 flag/config contract 和默认值策略为主，依赖清晰
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with Tasks 1, 3, 4)
  - **Blocks**: 6, 8, 9, 10
  - **Blocked By**: None

  **References**:
  - `cmd/main.go:61-105` - 现有 flags 定义风格与默认值书写方式
  - `config/manager/manager.yaml:61-102` - Deployment args 注入点与运行时资源限制基线
  - `config/default/kustomization.yaml:17-43` - deployment patch 进入默认发布面的方式

  **Acceptance Criteria**:
  - [ ] 所有 collector/ES 运行参数都有明确默认值或显式必填语义
  - [ ] 非法 poll interval / timeout / buffer limit 有测试覆盖
  - [ ] 配置解析测试可断言 collector 启用/禁用逻辑

  **QA Scenarios**:
  ```
  Scenario: Collector config defaults are stable and explicit
    Tool: Bash (go test)
    Preconditions: config parsing tests exist
    Steps:
      1. Run `go test ./internal/... -run TestCollectorConfigDefaults -v`
      2. Assert default poll interval is non-zero and documented
      3. Assert default per-node timeout is less than poll interval
      4. Assert missing optional ES auth fields do not leak into logs or zero-value panic paths
    Expected Result: PASS with deterministic defaults
    Failure Indicators: zero/negative defaults, inconsistent enablement logic, panic on missing values
    Evidence: .omo/evidence/task-2-config-defaults.txt

  Scenario: Invalid runtime config fails fast
    Tool: Bash (go test)
    Preconditions: config validation test cases exist
    Steps:
      1. Run `go test ./internal/... -run TestCollectorConfigValidation -v`
      2. Pass poll interval `0s`, timeout `-1s`, or empty data stream when collector enabled
      3. Assert each case returns the intended validation error
    Expected Result: PASS with exact error assertions
    Failure Indicators: invalid config accepted or generic unhelpful errors returned
    Evidence: .omo/evidence/task-2-config-validation.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-2-config-defaults.txt`
  - [ ] `task-2-config-validation.txt`

  **Commit**: NO

- [x] 3. Extend sing-box config generation for v2rayapi stats enablement

  **What to do**:
  - 识别并规划 `configengine` 输出中需要新增的 experimental/v2rayapi 配置块，使每个可采集节点正确开启 stats service。
  - 让用户统计名单与当前 operator 管理的用户集合对齐，避免 sing-box 未跟踪用户导致 collector 永远读不到数据。
  - 决定 listen 地址暴露方式，优先集群内受限访问，避免对外暴露无认证 gRPC。
  - 用 TDD 锁定配置 JSON：启用时正确输出 experimental/v2rayapi；禁用时不污染现有 config。

  **Must NOT do**:
  - 不破坏当前入站/出站/route 配置生成结果
  - 不把 v2rayapi 直接暴露到不受控公网地址
  - 不把 collector 行为逻辑写进 `SingBoxNodeReconciler`

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: 会触碰 configengine 结构与 sing-box 配置语义，需要较强准确性
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with Tasks 1, 2, 4)
  - **Blocks**: 11
  - **Blocked By**: None

  **References**:
  - `internal/configengine/engine.go:46-67` - 顶层 sing-box config 结构当前只覆盖 log/inbounds/outbounds/route
  - `internal/controller/singboxnode_controller.go:301-389` - config 通过 ConfigMap 注入 Deployment，任何 v2rayapi 配置都必须经由 configengine 落盘
  - sing-box docs/source: `option/experimental.go` - `experimental.v2ray_api.listen` 与 `stats.users` 结构定义
  - sing-box source: `box.go:419-428` - v2ray stats service 注册与 tracker append 机制

  **Acceptance Criteria**:
  - [ ] 开启 collector 时，config JSON 中存在 v2rayapi/stats 配置且用户集合与 operator 用户集合一致
  - [ ] 禁用 collector 时，旧 config 行为保持兼容
  - [ ] listen 地址设计可被测试证明不会默认暴露到公网

  **QA Scenarios**:
  ```
  Scenario: Config engine emits v2rayapi block when usage collection is enabled
    Tool: Bash (go test)
    Preconditions: configengine tests updated first
    Steps:
      1. Run `go test ./internal/configengine/... -run TestCompute_UsageCollectionEnabled -v`
      2. Build config for an inbound node with multiple users
      3. Assert output JSON contains experimental.v2ray_api.listen and stats.users entries
      4. Assert each managed user name is included exactly once in stats.users
    Expected Result: PASS with exact JSON assertions
    Failure Indicators: missing block, duplicate users, or wrong listen scope
    Evidence: .omo/evidence/task-3-config-enabled.txt

  Scenario: Disabled mode preserves previous config compatibility
    Tool: Bash (go test)
    Preconditions: regression tests cover old config shape
    Steps:
      1. Run `go test ./internal/configengine/... -run TestCompute_UsageCollectionDisabled -v`
      2. Assert existing inbounds/outbounds/route remain unchanged when collector is disabled
      3. Assert no experimental.v2ray_api block is present
    Expected Result: PASS with backward-compatibility assertions
    Failure Indicators: unexpected config drift when feature disabled
    Evidence: .omo/evidence/task-3-config-disabled.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-3-config-enabled.txt`
  - [ ] `task-3-config-disabled.txt`

  **Commit**: NO

- [x] 4. Build checkpoint and idempotency foundation

  **What to do**:
  - 设计并实现 collector 的持久化 checkpoint/state 模型，用于支撑 at-least-once 语义。
  - 明确 sing-box counter reset/restart 的处理策略，例如 delta 计算、counter 回绕/归零保护、上次成功发送位点记录。
  - 增加 corruption handling、empty checkpoint bootstrap、shutdown flush state write 等行为。
  - 先写测试覆盖 save/load round-trip、corruption fallback、重复发送后的幂等键稳定性。

  **Must NOT do**:
  - 不把 checkpoint 仅保存在进程内内存中
  - 不假设 sing-box counter 永不 reset
  - 不把 ES 写入成功与 checkpoint 落盘顺序设计成明显会丢数的路径而不加说明

  **Recommended Agent Profile**:
  - **Category**: `deep`
    - Reason: 涉及失败恢复、幂等和时序语义，是整个方案最关键的 correctness 部分
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 1 (with Tasks 1, 2, 3)
  - **Blocks**: 6, 8, 11
  - **Blocked By**: 1

  **References**:
  - sing-box v2rayapi caveat: counters are in-memory and may reset on reload/restart
  - `internal/apiserver/server.go:18-35` - shutdown 需要显式跟随 manager context
  - `Makefile:62-65` - `make test` 是整体回归门禁，checkpoint tests 必须可进入标准测试流

  **Acceptance Criteria**:
  - [ ] checkpoint save/load round-trip 测试通过
  - [ ] corrupted checkpoint 有明确 fallback 行为并被测试覆盖
  - [ ] duplicate business record 的 document ID / 幂等键生成逻辑稳定
  - [ ] reset counter 场景下不会产生负字节数

  **QA Scenarios**:
  ```
  Scenario: Checkpoint survives save/load round-trip
    Tool: Bash (go test)
    Preconditions: checkpoint unit tests written first
    Steps:
      1. Run `go test ./internal/... -run TestCheckpointSaveLoadRoundTrip -v`
      2. Persist checkpoint containing user/node cursor and last-success metadata
      3. Reload checkpoint from disk fixture
      4. Assert fields are byte-for-byte equivalent to the saved state
    Expected Result: PASS
    Failure Indicators: lost fields, reordered identity keys, or timestamp corruption
    Evidence: .omo/evidence/task-4-checkpoint-roundtrip.txt

  Scenario: Counter reset does not yield negative usage
    Tool: Bash (go test)
    Preconditions: delta computation tests exist
    Steps:
      1. Run `go test ./internal/... -run TestDeltaHandlesCounterReset -v`
      2. Simulate last counter = 1000, current counter = 200 after sing-box restart
      3. Assert computed usage delta is non-negative and follows documented reset rule
    Expected Result: PASS with exact delta semantics
    Failure Indicators: negative bytes or silent double-counting
    Evidence: .omo/evidence/task-4-counter-reset.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-4-checkpoint-roundtrip.txt`
  - [ ] `task-4-counter-reset.txt`

  **Commit**: NO

- [x] 5. Implement v2rayapi source client and stats parsing

  **What to do**:
  - 实现面向 sing-box v2rayapi 的 gRPC client 层，负责连接、超时控制、QueryStats/GetStats 调用、结果解析。
  - 增加对 `user>>>{name}>>>traffic>>>uplink/downlink` 命名约定的解析逻辑，把原始 counter 转换为内部 typed records/input samples。
  - 覆盖异常路径：gRPC 连接失败、单节点超时、部分统计缺失、空响应。
  - 用 fake/mock gRPC server 做 TDD，避免真实外部依赖。

  **Must NOT do**:
  - 不在该层耦合 ES 语义
  - 不把单节点卡死扩散为整个 poll cycle 卡死
  - 不把“用户不存在于返回结果中”直接解释为“该用户流量为 0”

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: 涉及外部协议、超时和解析正确性，复杂度中高
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 2 (with Tasks 6, 7, 8)
  - **Blocks**: 8, 11
  - **Blocked By**: 1

  **References**:
  - sing-box source: `experimental/v2rayapi/stats.go` - `QueryStats` / `GetStats` 的主要调用入口
  - sing-box source: `adapter/inbound.go` + `route/route.go` - 用户计数由 connection tracker 基于 metadata.User 驱动
  - sing-box issue context: reload/restart 可能造成 counter 丢失/重置

  **Acceptance Criteria**:
  - [ ] gRPC client 能正确解析用户上下行计数器
  - [ ] 单节点超时不会阻塞整个采集批次
  - [ ] “缺失 counter”与“返回 0 counter”可被区分并测试覆盖

  **QA Scenarios**:
  ```
  Scenario: v2rayapi client parses user traffic counters correctly
    Tool: Bash (go test)
    Preconditions: mock gRPC stats server fixture exists
    Steps:
      1. Run `go test ./internal/... -run TestV2RayAPIClientParsesUserStats -v`
      2. Mock server returns user>>>alice>>>traffic>>>uplink/downlink counters
      3. Assert parsed output yields user=alice with exact uplink/downlink bytes
    Expected Result: PASS with exact field assertions
    Failure Indicators: parsing drops direction labels or merges multiple users incorrectly
    Evidence: .omo/evidence/task-5-parse-user-stats.txt

  Scenario: Per-node timeout isolates hung sing-box endpoint
    Tool: Bash (go test)
    Preconditions: timeout test fixture exists
    Steps:
      1. Run `go test ./internal/... -run TestV2RayAPIClientNodeTimeout -v`
      2. Simulate one node hanging beyond configured timeout while another responds normally
      3. Assert timed-out node returns controlled error and responsive node result still completes
    Expected Result: PASS with bounded timeout behavior
    Failure Indicators: entire poll batch blocks indefinitely or responsive node result is lost
    Evidence: .omo/evidence/task-5-node-timeout.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-5-parse-user-stats.txt`
  - [ ] `task-5-node-timeout.txt`

  **Commit**: NO

- [x] 6. Implement Elasticsearch data stream sink

  **What to do**:
  - 实现 ES sink：接受内部 `UsageRecord`/batch，转换为 data stream 写入请求。
  - 设计并实现幂等 document ID 策略，确保 at-least-once 下重试不会制造不可控重复。
  - 处理认证、批量写入、部分失败、retry/backoff、flush 行为。
  - 使用 fake transport / mock server 做 TDD，测试 duplicate write、partial failure、auth failure、backoff path。

  **Must NOT do**:
  - 不把 retry 无限堆积在内存中没有上限
  - 不要求人工登录 Kibana 验证结果
  - 不让 ES sink 直接控制 collector 主循环节奏

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: 网络 IO、批量写入、错误分类和幂等语义都需要谨慎实现
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 2 (with Tasks 5, 7, 8)
  - **Blocks**: 8, 11
  - **Blocked By**: 1, 2, 4

  **References**:
  - `cmd/main.go:61-105` - ES 参数需要通过 flag/config 进入 manager
  - `config/manager/manager.yaml:61-102` - manager runtime args / volumeMounts 扩展点
  - Metis guidance - 本次只做 ES 具体实现，但接口必须对未来 sink 开放

  **Acceptance Criteria**:
  - [ ] 相同业务记录重复写入时，mock/fake ES 最终只保留预期幂等语义结果
  - [ ] 部分失败可被识别并返回可重试错误
  - [ ] auth/endpoint 配置错误时 sink fail-fast 且错误明确

  **QA Scenarios**:
  ```
  Scenario: ES sink writes usage records idempotently
    Tool: Bash (go test)
    Preconditions: fake ES transport tests written first
    Steps:
      1. Run `go test ./internal/... -run TestElasticsearchSinkIdempotentWrite -v`
      2. Submit the same logical usage record twice with identical document key inputs
      3. Assert fake ES observes one stable target document identity and no semantic duplicate explosion
    Expected Result: PASS with exact duplicate-write assertion
    Failure Indicators: document identity changes across retries or duplicate records accumulate unexpectedly
    Evidence: .omo/evidence/task-6-es-idempotent.txt

  Scenario: ES sink surfaces partial bulk failure for retry
    Tool: Bash (go test)
    Preconditions: bulk failure fixture exists
    Steps:
      1. Run `go test ./internal/... -run TestElasticsearchSinkPartialFailure -v`
      2. Simulate one document accepted and one document rejected in the same bulk request
      3. Assert sink returns structured retryable failure information for the rejected subset
    Expected Result: PASS with explicit retry path assertion
    Failure Indicators: partial failure treated as success or total failure without detail
    Evidence: .omo/evidence/task-6-es-partial-failure.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-6-es-idempotent.txt`
  - [ ] `task-6-es-partial-failure.txt`

  **Commit**: NO

- [x] 7. Implement discovery and normalization pipeline

  **What to do**:
  - 规划并实现 collector 所需的节点发现和用户映射逻辑：找出哪些 `SingBoxNode` 应被采集、如何得到节点访问端点、如何把 operator 用户与 sing-box counter 用户名关联起来。
  - 定义节点新增/删除时的行为：新节点在下一个 poll 周期纳入；已删除节点停止采集并按策略处理残余状态。
  - 为“无节点”“无用户”“部分用户未出现在计数器中”等情况编写 RED tests。

  **Must NOT do**:
  - 不把节点发现逻辑写进现有 `SingBoxNodeReconciler`
  - 不在用户缺失时直接记 0 并覆盖真实未知状态
  - 不假设所有节点都天然有可达 gRPC 地址而不做显式建模

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: 主要是 K8s 对象发现、过滤和归一化，依赖明确
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 2 (with Tasks 5, 6, 8)
  - **Blocks**: 8, 11
  - **Blocked By**: 1

  **References**:
  - `internal/controller/singboxnode_controller.go:180-267` - 当前收集输入的模式可复用为只读发现逻辑参考
  - `api/v1alpha1/singboxnode_types.go:54-109` - 节点 spec/status 的可用字段
  - `api/v1alpha1/user_types.go:24-53` - 用户协议与状态字段
  - `api/v1alpha1/customroute_types.go:23-49` - route 结构可辅助理解 user/node 关系

  **Acceptance Criteria**:
  - [ ] 发现逻辑可区分“无可采集节点”和“发现流程异常”
  - [ ] 节点增删行为有测试覆盖
  - [ ] 归一化后的记录能稳定映射回 operator 中的 user/node 语义

  **QA Scenarios**:
  ```
  Scenario: Discovery handles empty cluster state gracefully
    Tool: Bash (go test)
    Preconditions: fake client discovery tests exist
    Steps:
      1. Run `go test ./internal/... -run TestCollectorDiscoveryNoNodes -v`
      2. Provide fake cluster state with zero eligible SingBoxNode objects
      3. Assert collector discovery returns empty work set without error/panic
    Expected Result: PASS with explicit idle behavior
    Failure Indicators: nil dereference, error on empty set, or implicit busy loop semantics
    Evidence: .omo/evidence/task-7-discovery-empty.txt

  Scenario: Deleted node is removed from active collection set
    Tool: Bash (go test)
    Preconditions: fake client update sequence tests exist
    Steps:
      1. Run `go test ./internal/... -run TestCollectorDiscoveryNodeDeletion -v`
      2. Start with one eligible node and then remove it from the fake cluster state
      3. Assert next discovery cycle no longer schedules polling for that node and state is handled per plan
    Expected Result: PASS with explicit node-removal behavior
    Failure Indicators: deleted node remains polled or residual state causes repeated errors
    Evidence: .omo/evidence/task-7-node-deletion.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-7-discovery-empty.txt`
  - [ ] `task-7-node-deletion.txt`

  **Commit**: NO

- [x] 8. Implement collector orchestration loop

  **What to do**:
  - 把 poll → parse → normalize → dedupe/idempotent key → sink write → checkpoint update 串成独立 collector 主循环。
  - 实现 poll interval 调度、防重入、单周期错误隔离、buffer/backpressure 控制、shutdown flush。
  - 明确记录写入成功前后的 checkpoint 更新顺序，并用测试锁定 at-least-once 语义。
  - 覆盖“一个节点失败但其他节点成功”“ES 暂时失败后重试”“关闭时 flush”这些关键路径。

  **Must NOT do**:
  - 不允许重入 poll cycle 造成重复并发采集
  - 不允许 shutdown 直接丢弃已在 buffer 中且可 flush 的记录而无测试/说明
  - 不把 source/sink 细节硬编码进 orchestration，破坏后续扩展性

  **Recommended Agent Profile**:
  - **Category**: `deep`
    - Reason: orchestration 是整个系统的核心协调层，涉及时序、失败恢复和资源管理
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 2 (with Tasks 5, 6, 7)
  - **Blocks**: 9, 10, 11
  - **Blocked By**: 1, 2, 4, 5, 6, 7

  **References**:
  - `internal/apiserver/server.go:18-38` - runnable 生命周期模板
  - `internal/metrics/metrics.go:27-54` - 可复用的自定义 metrics 注册风格
  - Metis/oracle findings - 单节点 timeout、single-active collector、backpressure、shutdown flush 均需显式验证

  **Acceptance Criteria**:
  - [ ] collector 单周期不会并发重入
  - [ ] sink 成功/失败与 checkpoint 更新顺序被测试覆盖
  - [ ] shutdown flush 在限定时间内完成并有确定行为
  - [ ] backpressure 超限时行为可预测且被测试覆盖

  **QA Scenarios**:
  ```
  Scenario: Full poll-to-write cycle succeeds and advances checkpoint
    Tool: Bash (go test)
    Preconditions: orchestration integration tests exist with fake source/sink/checkpoint
    Steps:
      1. Run `go test ./internal/... -run TestCollectorPollCycleSuccess -v`
      2. Execute one full cycle with two user records from a fake source
      3. Assert sink receives the normalized batch
      4. Assert checkpoint advances only after successful sink write
    Expected Result: PASS with strict call-order assertions
    Failure Indicators: checkpoint moves before sink success or sink receives malformed records
    Evidence: .omo/evidence/task-8-poll-cycle-success.txt

  Scenario: Shutdown flush drains buffered records within timeout
    Tool: Bash (go test)
    Preconditions: shutdown/flush tests exist
    Steps:
      1. Run `go test ./internal/... -run TestCollectorShutdownFlush -v`
      2. Enqueue buffered usage records and cancel collector context
      3. Assert collector attempts final flush and returns within configured shutdown bound
    Expected Result: PASS with bounded shutdown behavior
    Failure Indicators: buffered records abandoned silently or shutdown hangs indefinitely
    Evidence: .omo/evidence/task-8-shutdown-flush.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-8-poll-cycle-success.txt`
  - [ ] `task-8-shutdown-flush.txt`

  **Commit**: NO

- [x] 9. Wire collector into manager lifecycle with single-active behavior

  **What to do**:
  - 按 `internal/apiserver/server.go` 的模式实现 collector 的 `Start(ctx context.Context) error` 与 `NeedLeaderElection() bool`。
  - 在 `cmd/main.go` 中注册 collector runnable，并与现有 flags/config 绑定。
  - 明确 leader election 行为：collector 默认作为单活组件运行，避免多副本 manager 重复采集。
  - 通过测试锁定 wiring：启用配置时注册，禁用配置时不注册或空转行为可预测。

  **Must NOT do**:
  - 不把 collector wiring 塞回 controller setup 中
  - 不让多副本在默认配置下同时写入 ES
  - 不在 Start 中做不受控 goroutine 泄漏

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: 主要是 manager wiring 与生命周期对齐，改动文件数少且模式清晰
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 3 (with Tasks 10, 11)
  - **Blocks**: 11
  - **Blocked By**: 2, 8

  **References**:
  - `cmd/main.go:176-267` - manager 初始化、controller/webhook/runnable 注册的标准位置
  - `internal/apiserver/server.go:11-38` - runnable 生命周期与 leader election 样板
  - `config/manager/manager.yaml:61-66` - 默认 deployment 已启用 `--leader-elect`

  **Acceptance Criteria**:
  - [ ] collector runnable 只在配置启用时注册
  - [ ] `NeedLeaderElection()` 策略与单活采集目标一致
  - [ ] Start/Shutdown 生命周期测试通过，无 goroutine 泄漏迹象

  **QA Scenarios**:
  ```
  Scenario: Collector runnable registers only when enabled
    Tool: Bash (go test)
    Preconditions: manager wiring tests exist
    Steps:
      1. Run `go test ./cmd/... ./internal/... -run TestCollectorRegistration -v`
      2. Build manager options with collector disabled
      3. Assert collector runnable is not registered or remains explicitly disabled
      4. Re-run with collector enabled and assert registration occurs
    Expected Result: PASS with deterministic enablement behavior
    Failure Indicators: collector always-on regardless of config or missing registration when enabled
    Evidence: .omo/evidence/task-9-registration.txt

  Scenario: Collector declares single-active leader election behavior
    Tool: Bash (go test)
    Preconditions: lifecycle tests exist
    Steps:
      1. Run `go test ./internal/... -run TestCollectorNeedLeaderElection -v`
      2. Assert the runnable returns the expected leader-election flag
      3. Assert lifecycle comments/documentation in tests match single-active semantics
    Expected Result: PASS
    Failure Indicators: leader-election behavior contradicts at-least-once single-writer design
    Evidence: .omo/evidence/task-9-leader-election.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-9-registration.txt`
  - [ ] `task-9-leader-election.txt`

  **Commit**: NO

- [x] 10. Update deployment and runtime manifests for collector operation

  **What to do**:
  - 更新 manager deployment/kustomize 相关配置，使 ES 参数、checkpoint 路径、必要 volume/volumeMount、资源限制与 graceful shutdown 要求可被部署。
  - 设计 checkpoint 存储介质（如 mounted volume）在当前 operator 部署中的落点。
  - 确保 secret/config 参数通过正确渠道注入，而不是硬编码在代码里。
  - 为 manifest-level 变化加回归验证，避免破坏现有 deployment。

  **Must NOT do**:
  - 不把 ES 凭据硬编码在 `config/manager/manager.yaml`
  - 不让 checkpoint 默认写入只读根文件系统
  - 不绕过现有 kustomize/deploy 工作流

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
    - Reason: 涉及 deployment runtime、volume、安全配置和发布路径，需更谨慎
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 3 (with Tasks 9, 11)
  - **Blocks**: 11
  - **Blocked By**: 2, 8

  **References**:
  - `config/manager/manager.yaml:53-102` - 当前 deployment securityContext、resources、volumeMounts、grace period 基线
  - `config/default/kustomization.yaml:17-43` - 默认发布面资源与 patch 入口
  - `Makefile:169-176` - `make deploy` 使用 kustomize 输出，manifest 变更必须与此兼容

  **Acceptance Criteria**:
  - [ ] manager deployment 有合法的 collector runtime args 注入点
  - [ ] checkpoint 路径与 volume/volumeMount 一致且不依赖只读根文件系统
  - [ ] 凭据注入方式不在 manifest 中明文泄露 secret 值

  **QA Scenarios**:
  ```
  Scenario: Kustomize output includes collector runtime configuration safely
    Tool: Bash
    Preconditions: manifest changes implemented
    Steps:
      1. Run `make manifests`
      2. Run `"$(pwd)/bin/kustomize" build config/default` via test helper or manifest assertion test
      3. Assert manager container args include collector-related flags without embedding raw secret values
      4. Assert volumeMounts/volumes include checkpoint storage when enabled
    Expected Result: PASS with manifest assertions
    Failure Indicators: broken kustomize output, missing mounts, or leaked secret literals
    Evidence: .omo/evidence/task-10-kustomize-output.txt

  Scenario: Deployment remains compatible with restricted security context
    Tool: Bash (go test or manifest assertions)
    Preconditions: security-focused manifest checks exist
    Steps:
      1. Run `go test ./internal/... -run TestCollectorDeploymentSecurityAssumptions -v`
      2. Assert checkpoint path is writable without violating readOnlyRootFilesystem assumptions after intended patching
      3. Assert terminationGracePeriodSeconds remains sufficient for shutdown flush design
    Expected Result: PASS
    Failure Indicators: checkpoint path unwritable or shutdown window inconsistent with flush contract
    Evidence: .omo/evidence/task-10-security-runtime.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-10-kustomize-output.txt`
  - [ ] `task-10-security-runtime.txt`

  **Commit**: NO

- [ ] 11. Build end-to-end regression and integration verification suite

  **What to do**:
  - 把 configengine、source client、sink、checkpoint、orchestration、manager wiring 串起来，形成端到端 integration/regression suite。
  - 验证空集群、动态节点变更、partial sink failure、shutdown flush、feature disabled compatibility、`make test` 回归。
  - 补充必要 collector metrics 测试或最小可观察性断言，确保故障时有诊断入口。
  - 收敛所有测试命令，使执行 agent 能通过标准命令完成验证。

  **Must NOT do**:
  - 不新增需要人工确认的 acceptance criteria
  - 不把 integration 测试写成只能依赖真实 ES/真实 sing-box 集群运行
  - 不跳过 `make test` 总回归

  **Recommended Agent Profile**:
  - **Category**: `deep`
    - Reason: 这是跨模块整合与最终回归任务，需全局一致性
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: NO
  - **Parallel Group**: Sequential
  - **Blocks**: F1, F2, F3, F4
  - **Blocked By**: 3, 4, 5, 6, 7, 8, 9, 10

  **References**:
  - `internal/controller/suite_test.go:57-95` - envtest suite bootstrap 模式
  - `internal/apiserver/handler_test.go` - fake client + httptest 模式可借鉴做 collector-facing integration fixtures
  - `Makefile:62-65` - 总回归命令必须仍为 `make test`
  - `internal/metrics/metrics.go:8-65` - collector metrics 若增加，应遵循同样注册方式

  **Acceptance Criteria**:
  - [ ] 存在至少一个 full-pipeline integration test：mock v2rayapi → collector → fake ES → checkpoint advance
  - [ ] feature disabled 模式下现有 configengine / manager 行为回归通过
  - [ ] `make test` 通过且不引入现有测试退化
  - [ ] 关键 collector metrics/日志路径具备最小验证

  **QA Scenarios**:
  ```
  Scenario: End-to-end pipeline writes records and advances checkpoint
    Tool: Bash (go test)
    Preconditions: integration suite exists
    Steps:
      1. Run `go test ./internal/... -run TestUsageCollectorEndToEnd -v`
      2. Start fake v2rayapi source returning two users across one node
      3. Run collector cycle into fake ES sink
      4. Assert expected normalized records are written and checkpoint advances exactly once after success
    Expected Result: PASS with full pipeline assertions
    Failure Indicators: records malformed, checkpoint order incorrect, or sink never receives batch
    Evidence: .omo/evidence/task-11-end-to-end.txt

  Scenario: Repository-wide regression gate stays green
    Tool: Bash
    Preconditions: all implementation tasks complete
    Steps:
      1. Run `make test`
      2. Assert command exits 0
      3. Capture summary showing newly added collector tests and existing suites all pass
    Expected Result: PASS
    Failure Indicators: existing controller/config/webhook/apiserver tests regress or envtest suite fails
    Evidence: .omo/evidence/task-11-make-test.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-11-end-to-end.txt`
  - [ ] `task-11-make-test.txt`

  **Commit**: YES
  - Message: `feat(usage-collector): collect sing-box traffic and push to elasticsearch`
  - Files: `cmd/main.go`, `internal/**`, `config/**`, tests
  - Pre-commit: `make test && make lint`

---

## Final Verification Wave

> 4 review agents run in PARALLEL. ALL must APPROVE. Present consolidated results to user and get explicit okay before completing.

- [ ] F1. Plan Compliance Audit
  Read the final implementation against this plan. Verify the collector is a `manager.Runnable`, not embedded in `SingBoxNodeReconciler`; ES is the only concrete sink; checkpoints/idempotency/backpressure/shutdown semantics are all represented; evidence files exist for task QA scenarios.
  Output: `Must Have [N/N] | Must NOT Have [N/N] | Tasks [N/N] | VERDICT: APPROVE/REJECT`

- [ ] F2. Code Quality Review
  Run `make lint` and inspect changed files for weak typing, hidden ES coupling in collector core, silent error swallowing, unbounded retries, and poor logging.
  Output: `Build [PASS/FAIL] | Lint [PASS/FAIL] | Tests [PASS/FAIL] | Files [N clean/N issues] | VERDICT`

- [ ] F3. Real QA Execution
  Execute every task QA scenario and the full regression gate. Confirm fake source/fake sink integration, config generation assertions, manager wiring behavior, and shutdown flush path all produce expected evidence.
  Output: `Scenarios [N/N pass] | Integration [N/N] | Edge Cases [N tested] | VERDICT`

- [ ] F4. Scope Fidelity Check
  Compare actual diff to scope boundaries. Ensure there is no MySQL sink, no Kibana/dashboarding, no quota enforcement, and no collector logic buried in existing reconciler flows. Flag any unplanned CRD additions.
  Output: `Tasks [N/N compliant] | Contamination [CLEAN/N issues] | Unaccounted [CLEAN/N files] | VERDICT`

---

## Commit Strategy

- **1-10**: Prefer local incremental commits only if execution flow benefits, but keep scope grouped by subsystem (contracts/config/configengine/core/source+sink/wiring/manifests)
- **11**: `feat(usage-collector): collect sing-box traffic and push to elasticsearch`

---

## Success Criteria

### Verification Commands
```bash
go test ./internal/... -run TestUsageCollectorEndToEnd -v   # Expected: PASS
go test ./internal/configengine/... -run TestCompute_UsageCollectionEnabled -v   # Expected: PASS
go test ./internal/... -run TestElasticsearchSinkIdempotentWrite -v   # Expected: PASS
make test   # Expected: exit 0
make lint   # Expected: exit 0
```

### Final Checklist
- [ ] All "Must Have" items are implemented
- [ ] All "Must NOT Have" items remain absent
- [ ] ES sink is the only concrete sink implementation
- [ ] Collector is wired as a single-active `manager.Runnable`
- [ ] Checkpoint/idempotency/reset/backpressure/shutdown semantics are tested
- [ ] All automated tests and lint checks pass
