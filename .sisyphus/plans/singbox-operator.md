# sing-box Kubernetes Operator（v2 — 配置编排引擎）

## TL;DR

> **Quick Summary**: 构建一个 Kubernetes Operator，作为**配置编排引擎**，通过三层 CRD（ProxyNode / ProxyUser / ProxyRoute）自动计算并生成每个代理节点上 sing-box 的完整配置，支持多租户、入口→出口两角色代理链路、地域自动关联，节点间通过公网 IP 直连转发。
>
> **Deliverables**:
> - `ProxyNode` CRD：声明代理节点（角色 inbound/outbound、地域、公网 IP、支持协议）
> - `ProxyUser` CRD：声明租户（协议+认证信息，协议匹配自动关联入口节点）
> - `ProxyRoute` CRD：声明手动补充的转发路径（指定具体入口节点 → 出口节点，同地域路径无需此 CR）
> - `ConfigEngine` 核心包：根据三层 CRD 自动计算每个节点的完整 sing-box config.json
> - 3 个 Reconciler（ProxyNode / ProxyUser / ProxyRoute）+ 交叉触发机制
> - 每个 ProxyNode 对应生成：Deployment + ConfigMap + NodePort Service（inbound 节点额外含入口对外 Service）
> - ValidatingWebhook + MutatingWebhook
> - Prometheus /metrics 端点
> - Helm Chart 完整打包
> - 单元测试（envtest）+ e2e 测试（kind）
>
> **Estimated Effort**: XL
> **Parallel Execution**: YES — 5 waves
> **Critical Path**: Task 1 (CRD types) → Task 2 (ConfigEngine) → Task 5 (ProxyNode Reconciler) → Task 8 (Webhook) → Task 11 (Helm) → Task 12 (e2e) → Final

---

## Context

### Original Request
构建基于 K8s 的 Operator，用于在 K8s 集群上调度 sing-box，核心诉求：
1. 快速为新上线节点自动部署 sing-box 服务（用户创建 CRD 触发）
2. 提供代理转发功能（入口→出口直连，无中转层），支持自定义转发路径和入站协议
3. 纯自定义网络转发服务，非 CNI 插件，不解决集群内 Pod 互联

### 架构演进
- **v1 方案（已废弃）**：SingBoxCluster + SingBoxNode，用户手写完整 sing-box JSON，Operator 透传生成 ConfigMap。无法满足自动链路编排、多租户、地域关联需求。
- **v2 方案（当前）**：ProxyNode + ProxyUser + ProxyRoute，Operator 作为配置编排引擎，自动计算每个节点的完整 sing-box 配置。

### 关键设计决策
- **地址模型**：ProxyNode.spec.address 手动填写宿主机公网 IP；节点间转发走公网，不走 ClusterIP
- **端口模型**：两类 NodePort 分离——对外入口端口（per-protocol）+ 节点互联端口（relayPort，独立 SOCKS5）
- **关联逻辑**：ProxyUser 协议匹配 ProxyNode.supportedProtocols → 自动注入 inbound；同 region 入口→出口自动关联，跨 region 或手动指定具体节点需显式 ProxyRoute（指定 inboundNode + outboundNode 名称）
- **配置生成**：入口节点 inbounds 完全由匹配的 ProxyUser 生成，ProxyNode 只声明支持协议+端口
- **节点间认证**：系统自动生成节点级 SOCKS5 认证凭证，存入 K8s Secret

### Metis Review
**Identified Gaps** (addressed in plan):
- 节点间 Secret 生命周期：ProxyNode Finalizer 确保删除时清理节点级 Secret
- 出口节点变化时的级联更新：ProxyNode Controller Watch 同 region 其他节点，触发入口节点重算
- relayPort 端口冲突：MutatingWebhook 检测端口冲突，ValidatingWebhook 拒绝重复端口
- 多角色节点配置合并：ConfigEngine 按角色叠加 inbounds/outbounds，同一节点可同时是 inbound+outbound（无 relay 角色）

---

## Work Objectives

### Core Objective
实现配置编排引擎：监听 ProxyNode/ProxyUser/ProxyRoute 三类 CR 变化，自动计算并维护每个 ProxyNode 上 sing-box 的完整配置，支持多租户、链式代理、地域自动关联。

### Concrete Deliverables
- `api/v1alpha1/proxynode_types.go` — ProxyNode CRD 类型
- `api/v1alpha1/proxyuser_types.go` — ProxyUser CRD 类型
- `api/v1alpha1/proxyroute_types.go` — ProxyRoute CRD 类型
- `internal/configengine/` — 核心配置计算引擎
- `internal/controller/proxynode_controller.go` — ProxyNode Reconciler
- `internal/controller/proxyuser_controller.go` — ProxyUser Reconciler
- `internal/controller/proxyroute_controller.go` — ProxyRoute Reconciler
- `internal/webhook/` — Validating + Mutating Webhook
- `internal/credmanager/` — 节点级认证凭证自动管理
- `charts/singbox-operator/` — Helm Chart
- `test/` — 单元测试 + e2e 测试

### Definition of Done
- [ ] 创建 ProxyNode (inbound, region=us-west) + ProxyNode (outbound, region=us-west) → 自动生成 Deployment/ConfigMap/Service，入口节点 outbound 自动指向出口节点公网 IP:relayPort
- [ ] 创建 ProxyUser (protocol=vless) → 自动注入到所有 supportedProtocols 含 vless 的入口节点 inbounds
- [ ] 删除 ProxyUser → 入口节点 sing-box 配置中该用户 inbound 被移除，触发滚动更新
- [ ] 创建 ProxyRoute (inboundNode=inbound-us-west-1, outboundNode=outbound-jp-east-1) → 指定入口节点 outbound 增加到该出口节点的路径
- [ ] `go test ./...` 全部通过
- [ ] e2e 测试在 kind 集群验证完整生命周期

### Must Have
- ProxyNode/ProxyUser/ProxyRoute 三层 CRD，含完整 Status Conditions
- ConfigEngine：根据三层 CRD 自动计算 sing-box config.json（inbound/outbound 两种角色）
- 交叉触发：ProxyUser 变化 → 触发关联入口 ProxyNode 重算；ProxyNode 变化 → 触发同 region 其他节点重算
- 两类 NodePort Service：入口对外（per-protocol）+ 节点互联（relayPort）
- 节点间 SOCKS5 认证凭证自动生成（K8s Secret），ProxyNode 删除时自动清理
- 同 region 入口→出口自动关联（无需 ProxyRoute）
- ProxyUser 协议匹配自动关联入口节点
- Namespace-scoped，K8s 1.28+
- ValidatingWebhook + MutatingWebhook
- Prometheus /metrics
- Helm Chart
- 单元测试（envtest）+ e2e 测试（kind）

### Must NOT Have (Guardrails)
- **禁止使用 ClusterIP 作为节点间转发地址**：outbound server 必须使用 ProxyNode.spec.address（公网 IP）
- **禁止 TUN/TPROXY 模式**：不添加 NET_ADMIN/SYS_MODULE，不设置 hostNetwork
- **禁止在入口 ProxyNode 中硬编码用户配置**：inbounds 完全由 ProxyUser 动态生成
- **禁止 ProxyUser 直接引用 ProxyNode**：关联通过协议匹配，不通过 nodeRef
- **禁止在 ConfigMap 中明文存储认证凭证**：凭证存 Secret，ConfigMap 通过 secretKeyRef 引用
- **禁止过度抽象**：ConfigEngine 不为"未来协议"预留接口，只实现已知协议（VLESS/Trojan/SOCKS5）
- **禁止 AI slop**：无无意义注释，无空接口，无过度包装

---

## Verification Strategy (MANDATORY)

> **ZERO HUMAN INTERVENTION** - ALL verification is agent-executed.

### Test Decision
- **Infrastructure**: YES（kubebuilder 生成）
- **Automated tests**: TDD（RED→GREEN→REFACTOR）
- **Framework**: Go testing + envtest（单元）+ kind（e2e）

### QA Policy
- **K8s 资源验证**: Bash（kubectl）
- **ConfigEngine 逻辑**: Go test（纯单元，无 K8s 依赖）
- **Reconciler 逻辑**: envtest（内存 K8s）
- **完整流程**: kind（真实集群）
- **Metrics**: Bash（curl）

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (立即开始 — 基础类型 + 核心引擎):
├── Task 1: CRD 类型定义（ProxyNode/ProxyUser/ProxyRoute）[quick]
├── Task 2: ConfigEngine 核心包（TDD）[unspecified-high]
└── Task 3: CredManager 凭证管理包 + 测试基础设施 [quick]

Wave 2 (Wave 1 后 — 三个 Reconciler，最大并行):
├── Task 4: ProxyNode Reconciler（TDD）[unspecified-high]
├── Task 5: ProxyUser Reconciler（TDD）[unspecified-high]
└── Task 6: ProxyRoute Reconciler（TDD）[unspecified-high]

Wave 3 (Wave 2 后 — Webhook + Metrics):
├── Task 7: ValidatingWebhook + MutatingWebhook（TDD）[unspecified-high]
└── Task 8: Prometheus Metrics 集成 [quick]

Wave 4 (Wave 3 后 — 打包 + e2e):
├── Task 9: Helm Chart 完整打包 [unspecified-high]
└── Task 10: e2e 测试套件（kind）[unspecified-high]

Wave FINAL (所有任务后 — 4 路并行审查):
├── F1: Plan Compliance Audit (oracle)
├── F2: Code Quality Review (unspecified-high)
├── F3: Real Manual QA (unspecified-high)
└── F4: Scope Fidelity Check (deep)
→ 呈现结果 → 获取用户明确 okay
```

### Dependency Matrix

| Task | Depends On | Blocks |
|------|-----------|--------|
| 1 | - | 2, 3, 4, 5, 6 |
| 2 | 1 | 4, 5, 6 |
| 3 | 1 | 4, 5, 6, 10 |
| 4 | 1, 2, 3 | 7, 9 |
| 5 | 1, 2, 3 | 7, 9 |
| 6 | 1, 2, 3 | 7, 9 |
| 7 | 4, 5, 6 | 9 |
| 8 | 4, 5, 6 | 9 |
| 9 | 4, 5, 6, 7, 8 | 10 |
| 10 | 3, 9 | F1-F4 |

### Agent Dispatch Summary
- **Wave 1**: T1→`quick`, T2→`unspecified-high`, T3→`quick`
- **Wave 2**: T4→`unspecified-high`, T5→`unspecified-high`, T6→`unspecified-high`
- **Wave 3**: T7→`unspecified-high`, T8→`quick`
- **Wave 4**: T9→`unspecified-high`, T10→`unspecified-high`
- **FINAL**: F1→`oracle`, F2→`unspecified-high`, F3→`unspecified-high`, F4→`deep`

---

## TODOs

- [x] 1. CRD 类型定义（ProxyNode / ProxyUser / ProxyRoute）

  **What to do**:
  - `kubebuilder init --domain proxy.io --repo github.com/shlande/singbox-operator`
  - `kubebuilder create api --group proxy --version v1alpha1 --kind ProxyNode`
  - `kubebuilder create api --group proxy --version v1alpha1 --kind ProxyUser`
  - `kubebuilder create api --group proxy --version v1alpha1 --kind ProxyRoute`

  **ProxyNodeSpec 字段**:
  ```go
  NodeRef            string                  // 绑定的 K8s Node 名称
  Address            string                  // 宿主机公网 IP（手动填写）
  Region             string                  // 地域标签（如 "us-west"）
  Roles              []ProxyRole             // [inbound, outbound]（可多选，无 relay 角色）
  SupportedProtocols []ProtocolConfig        // 入口协议声明（仅 inbound 角色有意义）
  RelayPort          int32                   // 节点间互联端口，默认 10808
  RelayProtocol      string                  // 默认 "socks5"
  ```
  ```go
  type ProtocolConfig struct {
    Protocol string // "vless" | "trojan" | "socks5" | "http"
    Port     int32  // 对外 NodePort，如 10443
    // 注意：无认证配置字段，认证完全由 ProxyUser 提供
  }
  ```

  **ProxyUserSpec 字段**:
  ```go
  Protocol  string            // 入站协议，必须存在于某 ProxyNode.supportedProtocols
  AuthSecret corev1.SecretRef // 引用含认证信息的 Secret（uuid/password/等）
  // 无 nodeSelector：协议匹配即自动关联
  ```

  **ProxyRouteSpec 字段**:
  ```go
  InboundNode  string   // 入口 ProxyNode 名称（必填）
  OutboundNode   string   // 出口 ProxyNode 名称（必填）
  // 注意：无 Via 字段（已去掉中转角色），无 FromRegion/ToRegion（路由指定具体节点）
  ```

  **Status 模式（三个 CRD 统一）**:
  - `+kubebuilder:subresource:status`
  - Conditions: `Ready`, `Progressing`, `Degraded`（含 ObservedGeneration）
  - ProxyNode.Status 额外字段：`ConfigHash string`, `Phase string`（Pending/Running/Failed）, `EntryEndpoints []string`（对外端点列表）
  - ProxyUser.Status 额外字段：`ActiveNodeCount int32`, `ActiveNodes []string`
  - ProxyRoute.Status 额外字段：`ResolvedInboundNode string`, `ResolvedOutboundNode string`（解析后的节点名称，用于确认路由生效）

  **kubebuilder markers**:
  - `+kubebuilder:validation:Enum=inbound;outbound` on ProxyRole（无 relay）
  - `+kubebuilder:validation:Enum=vless;trojan;socks5;http` on Protocol
  - `+kubebuilder:validation:MinItems=1` on Roles
  - `+kubebuilder:printcolumn` for Phase, Region, Roles, Age

  - `make generate && make manifests` 生成 CRD YAML 和 DeepCopy
  - 编写类型单元测试：DeepCopy 无 nil 指针、Status Conditions 初始化

  **Must NOT do**:
  - 不在 ProxyNode spec 中添加任何认证配置字段（认证由 ProxyUser 管理）
  - 不在 ProxyUser spec 中添加 nodeSelector/nodeRef（关联通过协议匹配）
  - 不添加 TUN/TPROXY 相关字段

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO（所有后续任务依赖此类型定义）
  - **Parallel Group**: Wave 1 起点
  - **Blocks**: Task 2, 3, 4, 5, 6
  - **Blocked By**: None

  **References**:
  - kubebuilder markers: https://book.kubebuilder.io/reference/markers
  - Status Conditions: https://pkg.go.dev/k8s.io/apimachinery/pkg/api/meta#SetStatusCondition

  **Acceptance Criteria**:
  - [ ] `kubectl get crd | grep proxy.io` 显示 3 个 CRD
  - [ ] `kubectl explain proxynode.spec` 显示 address, region, roles, supportedProtocols, relayPort 字段
  - [ ] `go test ./api/...` PASS

  **QA Scenarios**:
  ```
  Scenario: CRD 安装和字段验证
    Tool: Bash (kubectl)
    Preconditions: kind 集群运行中
    Steps:
      1. kubectl apply -f config/crd/bases/
      2. kubectl get crd | grep proxy.io
      3. kubectl explain proxynode.spec.supportedProtocols
    Expected Result: 3 个 CRD 存在，字段说明正确
    Evidence: .sisyphus/evidence/task-1-crd-install.txt

  Scenario: 无效 Role 被拒绝
    Tool: Bash (kubectl)
    Steps:
      1. kubectl apply -f test/fixtures/proxynode-invalid-role.yaml (roles: [invalid])
    Expected Result: 返回 ValidationError，包含 "Unsupported value: invalid"
    Evidence: .sisyphus/evidence/task-1-crd-validation.txt
  ```

  **Commit**: YES
  - Message: `feat(api): add ProxyNode, ProxyUser, ProxyRoute CRD type definitions`
  - Files: `api/v1alpha1/`, `config/crd/`
  - Pre-commit: `make generate && make manifests && go test ./api/...`

- [x] 2. ConfigEngine 核心包（TDD）

  **What to do**:
  创建 `internal/configengine/` 包，这是整个系统的核心——给定一个 ProxyNode 及其关联的 ProxyUser 列表和 ProxyRoute 列表，计算出完整的 sing-box config.json。

  **RED 阶段**（先写测试）：
  - `TestConfigEngine_InboundNode`：给定 inbound 角色节点 + 2 个 ProxyUser(vless) + 1 个同 region 出口节点 → 验证生成的 config.json 包含 2 个 vless inbound + 1 个 socks5 outbound（指向出口节点公网 IP:relayPort）
  - `TestConfigEngine_OutboundNode`：给定 outbound 角色节点 → 验证生成的 config.json 包含 socks5 inbound(relayPort) + direct outbound
  - `TestConfigEngine_MultiRoleNode`：给定 inbound+outbound 双角色节点 → 验证 inbounds 包含用户 inbound 和 socks5 inbound，outbounds 包含 direct
  - `TestConfigEngine_ManualRoute`：给定 ProxyRoute(inboundNode=A, outboundNode=B)，当前节点为 A → 验证节点 A 的 outbounds 包含指向节点 B 公网 IP:relayPort 的路径
  - `TestConfigEngine_NoUsersOnEntry`：入口节点无匹配 ProxyUser → 验证 inbounds 为空列表（不报错）
  - `TestConfigEngine_MultipleOutboundNodes`：同 region 有 2 个出口节点 → 验证入口节点 outbounds 包含 2 个出口节点的路径（URLTest selector 或顺序列表，由设计决定）

  **GREEN 阶段**（实现）：

  ```go
  // internal/configengine/engine.go
  type Input struct {
    Node       *v1alpha1.ProxyNode
    Users      []*v1alpha1.ProxyUser      // 协议匹配的用户
    UserCreds  map[string]UserCredential  // 用户凭证（从 Secret 解析）
    OutboundNodes  []*v1alpha1.ProxyNode      // 同 region 出口节点（自动关联）
    Routes     []*v1alpha1.ProxyRoute     // 适用的 ProxyRoute
    NodeCreds  map[string]NodeCredential  // 节点级 SOCKS5 凭证（从 Secret 解析）
  }

  type Output struct {
    Config []byte // sing-box config.json
    Hash   string // sha256[:16]，用于触发滚动更新
  }

  func Compute(input Input) (Output, error)
  ```

  **配置生成规则（仅 inbound / outbound 两种角色）**：
  - inbound 角色：
    - inbounds：遍历 input.Users，按 user.spec.protocol 生成对应 inbound（vless/trojan/socks5/http），认证信息从 UserCreds 取
    - outbounds：
      1. 同 region 出口节点（OutboundNodes）：为每个生成 `socks5://[outboundNode.spec.address]:[outboundNode.spec.relayPort]`，tag=`outbound-{outboundNode.name}`
      2. 手动 ProxyRoute（Routes，inboundNode=本节点）：为 outboundNode 生成 socks5 路径，tag=`route-{route.name}`
      3. 最后追加 `direct`
    - route.final：指向第一个出口 outbound（若无出口节点则 direct）
  - outbound 角色：
    - inbounds：socks5 监听 node.spec.relayPort，认证从 NodeCreds 取（节点级凭证）
    - outbounds：[direct]
    - route.final：direct
  - 多角色叠加（inbound+outbound）：inbounds 合并（用户 inbound + socks5 inbound），outbounds 合并，去重 tag

  **辅助函数**：
  - `func ComputeHash(config []byte) string`
  - `func ExtractNodePorts(node *v1alpha1.ProxyNode) []int32`（返回所有需要 NodePort 的端口）

  **Must NOT do**:
  - ConfigEngine 不访问 K8s API（纯函数，输入全部由 Reconciler 传入）
  - 不硬编码端口默认值（默认值由 MutatingWebhook 注入到 CRD，ConfigEngine 只读 spec）

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 3 并行）
  - **Parallel Group**: Wave 1（与 Task 3）
  - **Blocks**: Task 4, 5, 6
  - **Blocked By**: Task 1

  **References**:
  - sing-box inbounds: https://sing-box.sagernet.org/configuration/inbound/
  - sing-box outbounds: https://sing-box.sagernet.org/configuration/outbound/
  - sing-box route: https://sing-box.sagernet.org/configuration/route/
  - VLESS inbound: https://sing-box.sagernet.org/configuration/inbound/vless/
  - Trojan inbound: https://sing-box.sagernet.org/configuration/inbound/trojan/

  **Acceptance Criteria**:
  - [ ] `go test ./internal/configengine/...` PASS（≥ 7 test cases，覆盖率 > 85%）
  - [ ] inbound 节点生成的 config 包含正确的 vless inbound（含用户 uuid）和 socks5 outbound（含出口节点公网 IP）
  - [ ] outbound 节点生成的 config 包含 socks5 inbound 和 direct outbound
  - [ ] 多角色节点（inbound+outbound）config 正确合并 inbounds/outbounds

  **QA Scenarios**:
  ```
  Scenario: 入口节点配置生成
    Tool: Bash (go test -v -run TestConfigEngine_InboundNode)
    Steps:
      1. go test ./internal/configengine/... -run TestConfigEngine_InboundNode -v
    Expected Result: PASS，生成 JSON 中 inbounds[0].type="vless"，outbounds[0].server="1.2.3.4"（出口节点公网IP）
    Evidence: .sisyphus/evidence/task-2-configengine-inbound.txt

  Scenario: 出口节点配置生成
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/configengine/... -run TestConfigEngine_OutboundNode -v
    Expected Result: PASS，inbounds[0].type="socks5"，outbounds 只有 direct，无 socks5 outbound
    Evidence: .sisyphus/evidence/task-2-configengine-outbound.txt

  Scenario: 手动 ProxyRoute 路径注入
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/configengine/... -run TestConfigEngine_ManualRoute -v
    Expected Result: PASS，入口节点 outbounds 包含 ProxyRoute 指定的出口节点路径（tag="route-{routeName}"）
    Evidence: .sisyphus/evidence/task-2-configengine-manualroute.txt
  ```

  **Commit**: YES
  - Message: `feat(configengine): implement core sing-box config computation engine`
  - Files: `internal/configengine/`
  - Pre-commit: `go test ./internal/configengine/...`

- [x] 3. CredManager 凭证管理包 + 测试基础设施

  **What to do**:

  **CredManager** (`internal/credmanager/`):
  - 实现 `EnsureNodeCredential(ctx, client, node *ProxyNode) (NodeCredential, error)`：
    - 查找名为 `proxynode-{node.name}-relay-cred` 的 Secret
    - 若不存在：生成随机 UUID（作为 SOCKS5 用户名）+ 随机密码（32字节 base64），创建 Secret，设置 OwnerReference → ProxyNode
    - 若存在：直接读取返回
    - Secret 结构：`data.username`, `data.password`
  - 实现 `GetNodeCredential(ctx, client, nodeName, namespace string) (NodeCredential, error)`：从 Secret 读取
  - 实现 `GetUserCredential(ctx, client, user *ProxyUser) (UserCredential, error)`：从 user.spec.authSecret 引用的 Secret 读取

  **测试基础设施**:
  - `internal/controller/suite_test.go`：配置 envtest.Environment，注册 3 个 CRD，启动 fake K8s API server
  - `test/e2e/suite_test.go`：kind 集群 BeforeSuite/AfterSuite
  - `test/helpers/`：CreateProxyNode(), CreateProxyUser(), WaitForCondition(), AssertConfigContains() 等工具函数
  - `Makefile` 目标：`make test`（单元）、`make test-e2e`（e2e）
  - `.github/workflows/test.yml`：PR 时运行单元测试

  **Must NOT do**:
  - 不在 CredManager 中生成节点间认证以外的凭证（ProxyUser 凭证由用户自己管理）

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 2 并行）
  - **Parallel Group**: Wave 1（与 Task 2）
  - **Blocks**: Task 4, 5, 6, 10
  - **Blocked By**: Task 1

  **References**:
  - envtest: https://book.kubebuilder.io/reference/envtest
  - crypto/rand: https://pkg.go.dev/crypto/rand

  **Acceptance Criteria**:
  - [ ] `go test ./internal/credmanager/...` PASS
  - [ ] 调用 EnsureNodeCredential 两次，第二次返回相同凭证（幂等）
  - [ ] ProxyNode 删除后，关联 Secret 通过 OwnerReference GC 自动清理

  **QA Scenarios**:
  ```
  Scenario: 凭证幂等创建
    Tool: Bash (go test with envtest)
    Steps:
      1. go test ./internal/credmanager/... -run TestEnsureNodeCredential -v
    Expected Result: 两次调用返回相同 username/password，Secret 只创建一次
    Evidence: .sisyphus/evidence/task-3-credmanager.txt
  ```

  **Commit**: YES
  - Message: `feat(credmanager): add node credential auto-management and test infrastructure`
  - Files: `internal/credmanager/`, `internal/controller/suite_test.go`, `test/`

- [x] 4. ProxyNode Reconciler（TDD）

  **What to do**:

  **RED 阶段**（先写测试 `internal/controller/proxynode_controller_test.go`）：
  - 创建 ProxyNode (inbound, region=us-west, supportedProtocols=[vless:10443]) → 期望：Deployment 创建、ConfigMap 创建、2 个 NodePort Service（入口 Service port=10443 + 互联 Service port=10808）
  - 创建 ProxyNode (outbound, region=us-west) → 期望：Deployment 创建、ConfigMap 包含 socks5 inbound（relayPort）+ direct outbound、1 个 NodePort Service（互联 port=10808；outbound 节点无入口对外 Service）
  - 同 region 新增出口 ProxyNode → 期望：同 region 入口 ProxyNode 的 ConfigMap 被更新（新增出口 outbound），触发滚动更新
  - 删除 ProxyNode → 期望：Deployment/ConfigMap/Service 被清理（OwnerReference GC），关联 Secret 被清理
  - ProxyNode spec.address 变更 → 期望：依赖该节点的其他节点 ConfigMap 更新

  **GREEN 阶段**（实现 `internal/controller/proxynode_controller.go`）：

  主 Reconcile 流程：
  ```
  1. Fetch ProxyNode，处理 NotFound
  2. 处理删除（Finalizer cleanup）
  3. EnsureNodeCredential（若 relay/outbound 角色）
  4. collectInput()：查询同 region 出口节点、匹配 ProxyUser 列表、适用 ProxyRoute
  5. configengine.Compute(input) → 生成 config.json
  6. reconcileConfigMap()：CreateOrPatch，注入 config-hash annotation
  7. reconcileDeployment()：CreateOrPatch，挂载 ConfigMap，设置 nodeSelector（绑定 spec.nodeRef 对应的 K8s Node）
  8. reconcileServices()：
     - 若 roles 含 inbound：为每个 supportedProtocol 创建/更新 NodePort Service（对外入口）
     - 始终创建/更新互联 NodePort Service（port=relayPort，供上游节点 outbound 连接）
  9. updateStatus()：更新 Ready/Progressing/Degraded，记录 ConfigHash、EntryEndpoints
  ```

  **交叉触发机制**（关键）：
  ```go
  // 当同 region 的 ProxyNode 变化时，触发本节点重算
  ctrl.NewControllerManagedBy(mgr).
    For(&v1alpha1.ProxyNode{}).
    Watches(&v1alpha1.ProxyNode{},
      handler.EnqueueRequestsFromMapFunc(r.sameRegionNodeMapper)).
    Watches(&v1alpha1.ProxyUser{},
      handler.EnqueueRequestsFromMapFunc(r.matchingProtocolNodeMapper)).
    Watches(&v1alpha1.ProxyRoute{},
      handler.EnqueueRequestsFromMapFunc(r.affectedByRouteMapper)).
    Complete(r)
  ```

  **nodeSelector 绑定**：
  ```yaml
  # Deployment.spec.template.spec.nodeSelector
  kubernetes.io/hostname: <proxyNode.spec.nodeRef>
  ```

  **Must NOT do**:
  - 不在 outbound server 中使用 ClusterIP 或 Service DNS 名称，必须使用 `outboundNode.spec.address`（公网 IP）
  - 不直接调用 configengine 时传入 K8s API 查询，所有数据在 collectInput() 中预先查询

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 5, 6 并行）
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 7, 9
  - **Blocked By**: Task 1, 2, 3

  **References**:
  - `internal/configengine/engine.go:Compute()` — Task 2 产物
  - `internal/credmanager/` — Task 3 产物
  - controller-runtime Watch: https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/builder#Builder.Watches
  - CreateOrPatch: https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/controller/controllerutil#CreateOrPatch

  **Acceptance Criteria**:
  - [ ] `go test ./internal/controller/... -run TestProxyNodeReconciler` PASS（≥ 5 cases）
  - [ ] 创建 inbound ProxyNode 后：ConfigMap 含有效 sing-box JSON，Deployment 的 nodeSelector 指向正确 K8s Node，NodePort Service 端口正确（入口 Service + 互联 Service）
  - [ ] 创建 outbound ProxyNode 后：同 region 的 inbound ProxyNode ConfigMap 中 outbounds 包含新出口节点的 `socks5://[outbound.spec.address]:[outbound.spec.relayPort]`（公网 IP，非 ClusterIP）
  - [ ] 出口节点 spec.address 变更后：入口节点 ConfigMap 中对应 outbound server 地址更新

  **QA Scenarios**:
  ```
  Scenario: 创建 inbound ProxyNode 生成完整资源
    Tool: Bash (go test with envtest)
    Preconditions: envtest 运行中，已有 outbound ProxyNode (region=us-west, address="2.3.4.5", relayPort=10808)
    Steps:
      1. 创建 inbound ProxyNode (region=us-west, address="1.2.3.4", supportedProtocols=[{vless,10443}], relayPort=10808)
      2. go test ./internal/controller/... -run TestProxyNodeReconciler/CreateEntry -v
      3. 验证 ConfigMap data["config.json"] 包含 "type":"vless" inbound 和 "server":"2.3.4.5" outbound
      4. 验证 Deployment.spec.template.spec.nodeSelector["kubernetes.io/hostname"] = entry节点的 nodeRef
      5. 验证 NodePort Service 存在 port=10443（入口）和 port=10808（互联）
    Expected Result: 所有资源正确创建，配置指向出口节点公网 IP
    Evidence: .sisyphus/evidence/task-4-proxynode-create.txt

  Scenario: 新增出口节点触发入口节点配置更新
    Tool: Bash (go test with envtest)
    Preconditions: inbound ProxyNode 已 Ready，无出口节点
    Steps:
      1. 创建新 outbound ProxyNode (region=us-west, address="3.4.5.6")
      2. 等待 inbound ProxyNode Reconcile 完成
      3. 验证 inbound 节点 ConfigMap 中 outbounds 包含 "server":"3.4.5.6"
    Expected Result: 入口节点配置自动更新，config-hash annotation 变化
    Evidence: .sisyphus/evidence/task-4-proxynode-cascade-update.txt

  Scenario: 出口节点公网 IP 变更级联更新
    Tool: Bash (go test with envtest)
    Steps:
      1. 修改 outbound ProxyNode spec.address 为 "9.9.9.9"
      2. 等待 inbound ProxyNode Reconcile
      3. 验证 inbound ConfigMap outbound server 更新为 "9.9.9.9"
    Expected Result: 级联更新正确，不使用旧 IP
    Evidence: .sisyphus/evidence/task-4-proxynode-address-update.txt
  ```

  **Commit**: YES
  - Message: `feat(controller): implement ProxyNode reconciler with cross-node cascade updates`
  - Files: `internal/controller/proxynode_controller.go`, `internal/controller/proxynode_controller_test.go`
  - Pre-commit: `go test ./internal/controller/...`

- [x] 5. ProxyUser Reconciler（TDD）

  **What to do**:

  **RED 阶段**（`internal/controller/proxyuser_controller_test.go`）：
  - 创建 ProxyUser (protocol=vless) → 期望：所有 supportedProtocols 含 vless 的 inbound ProxyNode 被触发重算，ConfigMap 中新增该用户的 inbound
  - 更新 ProxyUser 的 authSecret（用户更换密码）→ 期望：关联入口节点 ConfigMap 更新（inbound 中的认证信息变化），触发滚动更新
  - 删除 ProxyUser → 期望：关联入口节点 ConfigMap 中该用户 inbound 被移除，触发滚动更新
  - ProxyUser.Status.ActiveNodes 正确反映关联的入口节点列表

  **GREEN 阶段**（实现）：

  ProxyUser Reconciler 职责：
  - 不直接创建 K8s 工作负载（由 ProxyNode Reconciler 负责）
  - 核心职责：**触发关联 ProxyNode 的 Reconcile**
  ```go
  func (r *ProxyUserReconciler) Reconcile(ctx, req) (Result, error) {
    user := fetch ProxyUser
    // 找到所有 supportedProtocols 包含 user.spec.protocol 的 inbound ProxyNode
    matchingNodes := r.findMatchingInboundNodes(ctx, user)
    // 为每个匹配节点触发重算（通过 annotation 或直接 Reconcile 入队）
    for _, node := range matchingNodes {
      r.triggerNodeReconcile(ctx, node)
    }
    // 更新 ProxyUser Status
    user.Status.ActiveNodeCount = len(matchingNodes)
    user.Status.ActiveNodes = nodeNames(matchingNodes)
    r.Status().Update(ctx, user)
  }
  ```

  **Must NOT do**:
  - ProxyUser Reconciler 不直接修改 ConfigMap（只触发 ProxyNode Reconciler）
  - 不在 ProxyUser 中存储任何认证凭证明文

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 4, 6 并行）
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 7, 9
  - **Blocked By**: Task 1, 2, 3

  **References**:
  - `api/v1alpha1/proxynode_types.go:SupportedProtocols` — Task 1 产物
  - controller-runtime EnqueueRequestsFromMapFunc: https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/handler

  **Acceptance Criteria**:
  - [ ] `go test ./internal/controller/... -run TestProxyUserReconciler` PASS
  - [ ] 创建 ProxyUser(vless) 后，inbound ProxyNode ConfigMap 中新增 vless inbound
  - [ ] 删除 ProxyUser 后，inbound ProxyNode ConfigMap 中对应 inbound 被移除
  - [ ] ProxyUser.Status.ActiveNodes 正确列出关联节点

  **QA Scenarios**:
  ```
  Scenario: ProxyUser 创建触发入口节点 inbound 注入
    Tool: Bash (go test with envtest)
    Preconditions: inbound ProxyNode (supportedProtocols=[vless:10443]) 已 Ready，无 ProxyUser
    Steps:
      1. 创建 ProxyUser (protocol=vless, authSecret=user-a-secret)
      2. 等待 ProxyNode Reconcile 完成
      3. 验证 ConfigMap data["config.json"] 中 inbounds 包含 type=vless 且 users[0].uuid 来自 user-a-secret
    Expected Result: inbound 正确注入，包含用户认证信息
    Evidence: .sisyphus/evidence/task-5-proxyuser-inject.txt

  Scenario: ProxyUser 删除触发 inbound 移除
    Tool: Bash (go test with envtest)
    Steps:
      1. 删除已关联的 ProxyUser
      2. 等待 ProxyNode Reconcile
      3. 验证 ConfigMap inbounds 中不再包含该用户的 uuid
    Expected Result: 配置更新，滚动更新触发（config-hash 变化）
    Evidence: .sisyphus/evidence/task-5-proxyuser-remove.txt
  ```

  **Commit**: YES
  - Message: `feat(controller): implement ProxyUser reconciler with protocol-based node association`
  - Files: `internal/controller/proxyuser_controller.go`, `internal/controller/proxyuser_controller_test.go`

- [x] 6. ProxyRoute Reconciler（TDD）

  **What to do**:

  **RED 阶段**（`internal/controller/proxyroute_controller_test.go`）：
  - 创建 ProxyRoute (inboundNode=inbound-a, outboundNode=outbound-b) → 期望：inbound-a 的 ConfigMap 中新增指向 outbound-b 的 outbound（使用 outbound-b.spec.address + outbound-b.spec.relayPort），tag=`route-{routeName}`
  - 删除 ProxyRoute → 期望：inbound-a 的 ConfigMap 中移除对应 outbound，触发滚动更新
  - ProxyRoute 引用不存在的 inboundNode 或 outboundNode → 期望：Status 置 Degraded，错误信息说明哪个节点不存在
  - ProxyRoute.Status 正确反映 ResolvedInboundNode、ResolvedOutboundNode

  **GREEN 阶段**（实现）：
  - 主要职责：触发 spec.inboundNode 对应的 ProxyNode 重算
  ```go
  func (r *ProxyRouteReconciler) Reconcile(ctx, req) (Result, error) {
    route := fetch ProxyRoute
    // 验证 inboundNode 和 outboundNode 均存在
    inboundNode := fetch ProxyNode(route.spec.inboundNode)
    outboundNode  := fetch ProxyNode(route.spec.outboundNode)
    // 触发 inboundNode 重算（入口节点的 outbounds 会在 collectInput 中读取所有适用 ProxyRoute）
    r.triggerNodeReconcile(ctx, inboundNode)
    // 更新 Status
    route.Status.ResolvedInboundNode = inboundNode.Name
    route.Status.ResolvedOutboundNode  = outboundNode.Name
    r.Status().Update(ctx, route)
  }
  ```
  - Watch：当 ProxyNode 变化时，触发引用了该节点的 ProxyRoute 重算（反向验证存在性）

  **Must NOT do**:
  - ProxyRoute 不直接修改 ConfigMap
  - ProxyRoute spec 中不添加 region/via 字段（已明确只指定具体节点）

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 4, 5 并行）
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 7, 9
  - **Blocked By**: Task 1, 2, 3

  **Acceptance Criteria**:
  - [ ] `go test ./internal/controller/... -run TestProxyRouteReconciler` PASS
  - [ ] 创建 ProxyRoute(inboundNode=A, outboundNode=B) 后，节点 A 的 ConfigMap outbounds 包含指向节点 B 公网 IP 的路径
  - [ ] 删除 ProxyRoute 后，节点 A 的 ConfigMap 中对应 outbound 被移除
  - [ ] ProxyRoute.Status.ResolvedInboundNode / ResolvedOutboundNode 正确填写

  **QA Scenarios**:
  ```
  Scenario: 手动路由创建注入 outbound
    Tool: Bash (go test with envtest)
    Preconditions: inbound-a (region=cn-north) 和 outbound-b (region=jp-east, address="20.0.0.1", relayPort=10808) 均已创建
    Steps:
      1. 创建 ProxyRoute (inboundNode=inbound-a, outboundNode=outbound-b)
      2. 等待 inbound-a Reconcile 完成
      3. 验证 inbound-a ConfigMap outbounds 包含 server="20.0.0.1", port=10808, tag="route-{routeName}"
    Expected Result: 手动路由路径注入正确，使用 outbound-b 公网 IP
    Evidence: .sisyphus/evidence/task-6-proxyroute-manual.txt

  Scenario: 删除路由移除 outbound
    Tool: Bash (go test with envtest)
    Steps:
      1. 删除上述 ProxyRoute
      2. 等待 inbound-a Reconcile
      3. 验证 inbound-a ConfigMap outbounds 中不再包含 tag="route-{routeName}" 的条目
    Expected Result: outbound 被移除，config-hash 变化触发滚动更新
    Evidence: .sisyphus/evidence/task-6-proxyroute-delete.txt

  Scenario: 引用不存在节点时 Degraded
    Tool: Bash (go test with envtest)
    Steps:
      1. 创建 ProxyRoute (inboundNode=nonexistent, outboundNode=outbound-b)
      2. 验证 ProxyRoute.Status.Conditions 包含 Degraded，message 含 "inboundNode not found"
    Expected Result: 优雅降级，不 panic
    Evidence: .sisyphus/evidence/task-6-proxyroute-notfound.txt
  ```

  **Commit**: YES
  - Message: `feat(controller): implement ProxyRoute reconciler for manual node-to-node forwarding`
  - Files: `internal/controller/proxyroute_controller.go`, `internal/controller/proxyroute_controller_test.go`

- [x] 7. ValidatingWebhook + MutatingWebhook（TDD）

  **What to do**:
  - `kubebuilder create webhook --group proxy --version v1alpha1 --kind ProxyNode --programmatic-validation --defaulting`
  - `kubebuilder create webhook --group proxy --version v1alpha1 --kind ProxyUser --programmatic-validation`
  - `kubebuilder create webhook --group proxy --version v1alpha1 --kind ProxyRoute --programmatic-validation`

  **MutatingWebhook (ProxyNode)**：
  - 若 `spec.relayPort` 为 0，注入默认值 10808
  - 若 `spec.relayProtocol` 为空，注入 "socks5"
  - 若 `spec.supportedProtocols[i].port` 为 0，按协议注入默认端口（vless=10443, trojan=10444, socks5=10808, http=10080）
  - 若 `spec.resources` 未设置，注入默认资源限制（requests: cpu=100m,mem=128Mi; limits: cpu=1,mem=512Mi）

  **ValidatingWebhook (ProxyNode)**：
  - `spec.address` 非空且为合法 IP 或域名（使用 net.ParseIP 或 regexp）
  - `spec.relayPort` 在 1024-65535 范围内
  - `spec.supportedProtocols` 中无重复协议
  - 各 `supportedProtocols[i].port` 与 `relayPort` 不冲突（端口不重复）
  - `spec.nodeRef` 对应的 K8s Node 存在（通过 webhook client 查询）
  - `spec.roles` 至少包含一个角色，且只允许 `inbound` 和 `outbound`（不允许 `relay`）

  **ValidatingWebhook (ProxyUser)**：
  - `spec.protocol` 非空且为已知协议（vless/trojan/socks5/http）
  - `spec.authSecret.name` 非空
  - 引用的 Secret 存在（通过 webhook client 查询）

  **ValidatingWebhook (ProxyRoute)**：
  - `spec.inboundNode` 和 `spec.outboundNode` 均非空
  - 引用的 ProxyNode 均存在（通过 webhook client 查询）
  - inboundNode 必须含 inbound 角色，outboundNode 必须含 outbound 角色

  **Must NOT do**:
  - 不在 MutatingWebhook 中修改用户明确设置的字段
  - 不在 Webhook 中调用 configengine（保持 Webhook 轻量）

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 8 并行）
  - **Parallel Group**: Wave 3
  - **Blocks**: Task 9
  - **Blocked By**: Task 4, 5, 6

  **References**:
  - kubebuilder webhook: https://book.kubebuilder.io/cronjob-tutorial/webhook-implementation
  - net.ParseIP: https://pkg.go.dev/net#ParseIP

  **Acceptance Criteria**:
  - [ ] `go test ./internal/webhook/...` PASS
  - [ ] 提交 address="" 的 ProxyNode → 被拒绝，错误含 "address must not be empty"
  - [ ] 提交 relayPort 与 supportedProtocols port 冲突的 ProxyNode → 被拒绝
  - [ ] 提交无 relayPort 的 ProxyNode → MutatingWebhook 注入默认值 10808

  **QA Scenarios**:
  ```
  Scenario: 地址验证
    Tool: Bash (kubectl apply)
    Preconditions: Operator 在 kind 集群运行，Webhook 已注册
    Steps:
      1. kubectl apply -f test/fixtures/proxynode-no-address.yaml
    Expected Result: 返回 400，错误含 "address"
    Evidence: .sisyphus/evidence/task-7-webhook-address.txt

  Scenario: 端口冲突检测
    Tool: Bash (kubectl apply)
    Steps:
      1. kubectl apply -f test/fixtures/proxynode-port-conflict.yaml (relayPort=10443, supportedProtocols=[{vless,10443}])
    Expected Result: 返回 400，错误含 "port conflict"
    Evidence: .sisyphus/evidence/task-7-webhook-port-conflict.txt
  ```

  **Commit**: YES
  - Message: `feat(webhook): add validating and mutating webhooks for ProxyNode and ProxyUser`
  - Files: `internal/webhook/`, `config/webhook/`

- [x] 8. Prometheus Metrics 集成

  **What to do**:
  - 在 `internal/metrics/` 定义自定义指标：
    - `singbox_proxy_nodes_total` (gauge)：节点数，labels: `region`, `role`, `phase`
    - `singbox_proxy_users_total` (gauge)：用户数，labels: `protocol`
    - `singbox_reconcile_duration_seconds` (histogram)：reconcile 耗时，labels: `controller`, `result`
    - `singbox_reconcile_errors_total` (counter)：错误计数，labels: `controller`, `error_type`
    - `singbox_config_updates_total` (counter)：配置更新次数（触发滚动更新），labels: `node_region`, `trigger`（user_change/route_change/node_change）
  - 在 3 个 Reconciler 中埋点（补充 Task 4/5/6 的 metrics 调用）
  - 确认 `cmd/main.go` 中 /metrics 端点已启用（controller-runtime 默认 :8080/metrics）

  **Must NOT do**:
  - 不添加高基数 label（如 node 名称、user 名称）

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 7 并行）
  - **Parallel Group**: Wave 3
  - **Blocks**: Task 9
  - **Blocked By**: Task 4, 5, 6

  **Acceptance Criteria**:
  - [ ] `curl http://localhost:8080/metrics | grep singbox_` 返回 5 个自定义指标

  **QA Scenarios**:
  ```
  Scenario: /metrics 端点返回自定义指标
    Tool: Bash (curl)
    Preconditions: Operator 在 kind 运行，port-forward 8080
    Steps:
      1. kubectl port-forward -n singbox-system deploy/singbox-operator-controller-manager 8080:8080 &
      2. curl -s http://localhost:8080/metrics | grep "singbox_"
    Expected Result: 5 个自定义指标存在，singbox_proxy_nodes_total 值 > 0
    Evidence: .sisyphus/evidence/task-8-metrics.txt
  ```

  **Commit**: YES（与 Task 7 合并）
  - Message: `feat(metrics): add prometheus metrics for proxy operator observability`
  - Files: `internal/metrics/`

- [x] 9. Helm Chart 完整打包

  **What to do**:
  ```
  charts/singbox-operator/
  ├── Chart.yaml
  ├── values.yaml          # operator.image, webhook.enabled, certManager.enabled,
  │                        # defaults.relayPort, defaults.protocols.*
  ├── crds/
  │   ├── proxynodes.yaml
  │   ├── proxyusers.yaml
  │   └── proxyroutes.yaml
  ├── templates/
  │   ├── deployment.yaml
  │   ├── serviceaccount.yaml
  │   ├── role.yaml                              # namespace-scoped
  │   ├── rolebinding.yaml
  │   ├── clusterrole.yaml                       # 读取 K8s Node（验证 nodeRef）
  │   ├── clusterrolebinding.yaml
  │   ├── validatingwebhookconfiguration.yaml
  │   ├── mutatingwebhookconfiguration.yaml
  │   ├── webhook-certificate.yaml               # cert-manager Certificate
  │   └── NOTES.txt
  └── examples/
      ├── proxynode-inbound.yaml                   # 含完整注释
      ├── proxynode-outbound.yaml
      ├── proxyuser.yaml
      └── proxyroute.yaml
  ```

  `examples/` 必须包含完整注释示例：
  ```yaml
  # examples/proxynode-inbound.yaml
  apiVersion: proxy.io/v1alpha1
  kind: ProxyNode
  metadata:
    name: inbound-us-west-1
  spec:
    nodeRef: k8s-node-1
    address: "1.2.3.4"       # 宿主机公网 IP（必填）
    region: us-west
    roles: [inbound]           # inbound / outbound，可多选，无 relay
    supportedProtocols:
      - protocol: vless
        port: 10443           # 对外 NodePort（客户端连接）
  ---
  # examples/proxyroute.yaml
  apiVersion: proxy.io/v1alpha1
  kind: ProxyRoute
  metadata:
    name: us-west-to-jp-east
  spec:
    inboundNode: inbound-us-west-1   # 必须含 inbound 角色
    outboundNode: outbound-jp-east-1     # 必须含 outbound 角色
  ```

  **Must NOT do**:
  - CRD 不放在 templates/（放 crds/，避免 helm upgrade 删除）
  - 不硬编码命名空间

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 10 并行，但 Task 10 依赖此 Task）
  - **Parallel Group**: Wave 4
  - **Blocks**: Task 10, F1-F4
  - **Blocked By**: Task 4, 5, 6, 7, 8

  **Acceptance Criteria**:
  - [ ] `helm lint charts/singbox-operator/` 无 ERROR
  - [ ] `helm install singbox-operator charts/singbox-operator/ -n singbox-system --create-namespace` 成功

  **QA Scenarios**:
  ```
  Scenario: Helm 安装完整 Operator
    Tool: Bash (helm + kubectl)
    Preconditions: kind 集群，cert-manager 已安装
    Steps:
      1. helm install singbox-operator charts/singbox-operator/ -n singbox-system --create-namespace
      2. kubectl wait --for=condition=ready pod -l app=singbox-operator -n singbox-system --timeout=60s
      3. kubectl get crd | grep proxy.io
      4. kubectl get validatingwebhookconfigurations | grep singbox
    Expected Result: Operator Ready，3 个 CRD 存在，Webhook 已注册
    Evidence: .sisyphus/evidence/task-9-helm-install.txt
  ```

  **Commit**: YES
  - Message: `feat(helm): add complete helm chart with examples`
  - Files: `charts/singbox-operator/`

- [x] 10. e2e 测试套件（kind）[SKIPPED — 环境未准备好，已规划为独立工作]

  **What to do**:
  使用 Ginkgo + Gomega，覆盖完整业务场景：

  **场景 1 — 同地域自动关联**：
  - 创建 outbound ProxyNode (region=cn-north, address="10.0.0.1", roles=[outbound])
  - 创建 inbound ProxyNode (region=cn-north, address="10.0.0.2", roles=[inbound], supportedProtocols=[{vless,10443}])
  - 等待两个节点 Ready
  - 验证 inbound 节点 ConfigMap 中 outbounds 包含 `"server":"10.0.0.1","port":10808`（出口节点公网 IP + relayPort）
  - 验证 inbound 节点有 NodePort Service port=10443（入口）和 port=10808（互联）

  **场景 2 — ProxyUser 协议匹配注入**：
  - 创建 ProxyUser (protocol=vless, authSecret=test-user-secret)
  - 等待 inbound 节点 Reconcile
  - 验证 inbound ConfigMap inbounds 包含 vless inbound，uuid 来自 test-user-secret

  **场景 3 — 手动 ProxyRoute（跨节点指定）**：
  - 创建 outbound ProxyNode (region=jp-east, address="20.0.0.1", relayPort=10808)
  - 创建 ProxyRoute (inboundNode=inbound-cn-north-1, outboundNode=outbound-jp-east-1)
  - 验证 inbound-cn-north-1 的 ConfigMap outbounds 新增 `"server":"20.0.0.1","port":10808`，tag 含 route 名称
  - 删除 ProxyRoute → 验证 outbound 被移除

  **场景 4 — 删除级联清理**：
  - 删除 ProxyUser → 验证 inbound ConfigMap 中对应 inbound 移除
  - 删除 outbound ProxyNode → 验证 inbound ConfigMap 中对应同地域 outbound 移除
  - 删除 inbound ProxyNode → 验证 Deployment/Service/ConfigMap 在 30s 内清理

  **场景 5 — Webhook 验证**：
  - 提交无 address 的 ProxyNode → 验证被拒绝
  - 提交 roles=[relay] 的 ProxyNode → 验证被拒绝（不支持 relay 角色）
  - 提交无 relayPort 的 ProxyNode → 验证 MutatingWebhook 注入 10808
  - 提交 ProxyRoute(inboundNode=outbound-node) → 验证被拒绝（inboundNode 必须含 inbound 角色）

  **Must NOT do**:
  - 不依赖外部网络（不实际发送代理流量）
  - 不使用 sleep，使用 Eventually + Gomega

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO（依赖 Task 9 Helm Chart）
  - **Parallel Group**: Wave 4（串行）
  - **Blocks**: F1-F4
  - **Blocked By**: Task 3, 9

  **Acceptance Criteria**:
  - [ ] `make test-e2e` 全部 5 个场景 PASS

  **QA Scenarios**:
  ```
  Scenario: 完整 e2e 套件
    Tool: Bash (make test-e2e)
    Preconditions: Docker 运行中，kind 和 helm 已安装
    Steps:
      1. make test-e2e
    Expected Result: 所有 Ginkgo 测试 PASS，输出无 FAIL/panic
    Evidence: .sisyphus/evidence/task-10-e2e-results.txt
  ```

  **Commit**: YES
  - Message: `test(e2e): add kind-based end-to-end test suite for proxy operator`
  - Files: `test/e2e/`


