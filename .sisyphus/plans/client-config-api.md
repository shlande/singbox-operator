# 客户端 sing-box 配置生成 API

## TL;DR

> **Quick Summary**: 在现有 sing-box Operator 的 controller-runtime manager 中嵌入一个 HTTP API 服务，提供 `GET /api/v1/client-config/{namespace}/{uuid}` 接口，根据 ProxyUser 凭证和 inbound 节点信息自动生成完整的客户端 sing-box 配置文件（含分流路由、selector outbound、每出口节点独立 derived UUID）。
>
> **Deliverables**:
> - `internal/apiserver/handler.go` — HTTP handler，核心配置生成逻辑
> - `internal/apiserver/server.go` — HTTP server，实现 controller-runtime Runnable 接口
> - `internal/apiserver/client_config.go` — 客户端配置结构体 + 生成函数
> - `internal/apiserver/template.go` — ConfigMap 模板加载 + 合并逻辑
> - `internal/configengine/export.go` — 导出 `DerivePassword` 函数（当前 unexported）
> - `cmd/main.go` 修改 — 注册 API server，新增 `--api-bind-address` 和 `--client-config-template` flags
> - `internal/apiserver/handler_test.go` — 单元测试
>
> **Estimated Effort**: Medium
> **Parallel Execution**: YES — 2 waves
> **Critical Path**: Task 1 (export DerivePassword) → Task 2 (client config logic) → Task 3 (HTTP server) → Task 4 (main.go integration) → Task 5 (tests)

---

## Context

### Original Request
增加一个 API 服务，用于基于用户配置以及 inbound 节点信息生成客户端使用的 sing-box 接口。

### Interview Summary
**Key Discussions**:
- **API 路径**: `GET /api/v1/client-config/{namespace}/{uuid}` — uuid 是 ProxyUser authSecret 中 `uuid` 字段的值
- **配置内容**: 完整客户端 sing-box 配置（inbound socks5/http + proxy outbounds + 分流路由）
- **模板机制**: 管理员通过 `--client-config-template namespace/name` 指定 ConfigMap 模板；模板的 outbounds 数组**全部被生成的 outbounds 替换**，inbounds + route.rules 保留
- **Outbound 选择**: 生成 selector outbound（tag="proxy"），用户通过 clash API 手动切换
- **认证流程**: 每个出口节点一个独立 outbound，UUID = `DeriveUUID(userBaseUUID, outboundNodeName)`（与服务端虚拟用户机制完全一致）
- **服务集成**: 嵌入 controller-runtime manager，新增 `--api-bind-address`（默认 `:8082`）

**Research Findings**:
- `configengine.DeriveUUID()` 已导出，但 `derivePassword()` 未导出 — 必须先导出
- `configengine.UserCredential` 和 `credmanager.UserCredential` 是两个相同结构体 — apiserver 直接使用 `credmanager` 包
- `ProxyNode.Status.EntryEndpoints` 格式: `protocol:address:port`
- `credmanager.GetUserCredential()` 可复用读取用户凭证
- controller-runtime manager 通过 `manager.Add(runnable)` 添加自定义 runnable

### Metis Review
**Identified Gaps** (addressed):
- `derivePassword` 未导出 → Task 1 专门处理导出
- UUID 为空时安全问题 → handler 中验证 UUID 格式（RFC 4122）
- 多个 ProxyUser 相同 UUID → 返回第一个匹配，记录 warning
- EntryEndpoints 为空时 → 跳过该节点，不报错
- ProxyRoute 引用不存在节点 → 跳过该路由，不报错
- 模板 ConfigMap 不存在 → 启动时 warn，请求时使用内置默认模板
- ConfigMap 模板 key 约定 → 读取 `data["config.json"]`

---

## Work Objectives

### Core Objective
提供一个 HTTP API，客户端通过 UUID 查询自己的完整 sing-box 客户端配置，配置中包含所有可用的代理出口（每出口一个 outbound，使用 derived UUID 认证），支持管理员自定义模板。

### Concrete Deliverables
- `GET /api/v1/client-config/{namespace}/{uuid}` → 返回 `application/json` 格式的 sing-box 客户端配置
- `--api-bind-address` flag（默认 `:8082`）
- `--client-config-template` flag（可选，格式 `namespace/name`）
- 内置默认模板（含 socks5/http inbound + 基础分流规则）

### Definition of Done
- [ ] `curl http://localhost:8082/api/v1/client-config/default/{valid-uuid}` 返回 HTTP 200 + 有效 sing-box JSON
- [ ] 返回的 JSON 包含 selector outbound（tag="proxy"）和所有 proxy outbounds
- [ ] 每个 proxy outbound 的 UUID 等于 `DeriveUUID(userBaseUUID, outboundNodeName)`
- [ ] `go test ./internal/apiserver/...` PASS
- [ ] `go build ./...` PASS（无编译错误）

### Must Have
- UUID 格式验证（RFC 4122 正则，防路径注入）
- EntryEndpoints 为空时跳过节点（不报 500）
- HTTP 404 当 UUID 不匹配任何 ProxyUser
- 生成的 outbounds 包含：所有 proxy outbounds + selector("proxy") + direct
- 模板合并：模板的 outbounds 全部替换为生成的 outbounds
- 内置默认模板（无管理员模板时使用）
- 使用 manager 的 cached client（不创建新 client）

### Must NOT Have (Guardrails)
- **禁止修改** `internal/configengine/engine.go`（服务端逻辑）
- **禁止新增 CRD 类型**或 kubebuilder markers
- **禁止实现缓存**（使用 manager informer cache 即可）
- **禁止 TLS**（由 ingress/LB 处理）
- **禁止在 HTTP 响应中暴露** Secret 名称或 Kubernetes 错误详情
- **禁止高基数 Prometheus metrics**（本期不加 metrics）
- **禁止支持 Clash/V2Ray/Xray 格式**（仅 sing-box JSON）
- **禁止 AI slop**：无无意义注释，无过度包装，无空接口

---

## Verification Strategy

### Test Decision
- **Infrastructure exists**: YES（Go test + envtest 已存在）
- **Automated tests**: Tests-after（实现后补测试）
- **Framework**: `go test`（单元测试，httptest.NewRecorder）

### QA Policy
- **HTTP API**: Bash（curl）
- **Handler 逻辑**: Go test（httptest，fake client）
- **UUID 推导正确性**: Go test（纯单元）

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (立即开始 — 基础层，可并行):
├── Task 1: 导出 DerivePassword + 整理 configengine 导出 [quick]
└── Task 2: 客户端配置生成逻辑 (client_config.go + template.go) [unspecified-high]

Wave 2 (Wave 1 后 — 集成层):
├── Task 3: HTTP server + handler (server.go + handler.go) [unspecified-high]
└── Task 4: main.go 集成 + flags [quick]

Wave 3 (Wave 2 后 — 测试):
└── Task 5: 单元测试 (handler_test.go) [unspecified-high]

Wave FINAL (Task 5 后 — 验证):
├── F1: Plan Compliance Audit (oracle)
├── F2: Code Quality Review (unspecified-high)
└── F3: Real Manual QA (unspecified-high)
→ 呈现结果 → 获取用户明确 okay
```

### Dependency Matrix

| Task | Depends On | Blocks |
|------|-----------|--------|
| 1 | - | 2, 3 |
| 2 | 1 | 3, 5 |
| 3 | 1, 2 | 4, 5 |
| 4 | 3 | F1-F3 |
| 5 | 2, 3 | F1-F3 |

### Agent Dispatch Summary
- **Wave 1**: T1→`quick`, T2→`unspecified-high`
- **Wave 2**: T3→`unspecified-high`, T4→`quick`
- **Wave 3**: T5→`unspecified-high`
- **FINAL**: F1→`oracle`, F2→`unspecified-high`, F3→`unspecified-high`

---

## TODOs

- [x] 1. 导出 `DerivePassword` 函数

  **What to do**:
  - 在 `internal/configengine/engine.go` 中将 `derivePassword` 重命名为 `DerivePassword`（大写导出）
  - 更新文件内部所有调用处（`buildRouteInbounds` 中的调用）
  - 确认 `DeriveUUID` 已导出（已存在，无需修改）

  **Must NOT do**:
  - 不修改函数逻辑，只改名（大写 D）
  - 不修改任何测试逻辑，只更新函数名引用

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 2 并行，但 Task 2 依赖此函数导出）
  - **Parallel Group**: Wave 1
  - **Blocks**: Task 2, Task 3
  - **Blocked By**: None

  **References**:
  - `internal/configengine/engine.go:212-215` — `derivePassword` 函数定义
  - `internal/configengine/engine.go:256,263,269` — 调用处（buildRouteInbounds 内）

  **Acceptance Criteria**:
  - [ ] `go build ./internal/configengine/...` PASS
  - [ ] `go test ./internal/configengine/...` PASS（无回归）
  - [ ] `configengine.DerivePassword("password", "suffix")` 可从外部包调用

  **QA Scenarios**:
  ```
  Scenario: 编译验证导出
    Tool: Bash (go build)
    Steps:
      1. go build ./internal/configengine/...
      2. go test ./internal/configengine/... -v
    Expected Result: PASS，无编译错误，所有测试通过
    Evidence: .sisyphus/evidence/task-1-export-derivepw.txt
  ```

  **Commit**: YES
  - Message: `refactor(configengine): export DerivePassword for client config generation`
  - Files: `internal/configengine/engine.go`
  - Pre-commit: `go test ./internal/configengine/...`

- [x] 2. 客户端配置生成逻辑

  **What to do**:

  创建 `internal/apiserver/` 包，包含两个文件：

  **`internal/apiserver/client_config.go`**:

  定义客户端配置生成函数：
  ```go
  package apiserver

  import (
      "fmt"
      "github.com/shlande/singbox-operator/api/v1alpha1"
      "github.com/shlande/singbox-operator/internal/configengine"
      "github.com/shlande/singbox-operator/internal/credmanager"
  )

  // ClientConfigInput 包含生成客户端配置所需的所有数据
  type ClientConfigInput struct {
      User        *v1alpha1.ProxyUser
      UserCred    credmanager.UserCredential
      InboundNodes []*v1alpha1.ProxyNode  // 所有 inbound ProxyNodes
      RoutesByInbound map[string][]*v1alpha1.ProxyRoute  // inbound node name → routes
      OutboundsByName map[string]*v1alpha1.ProxyNode     // outbound node name → node
  }

  // BuildClientConfig 生成客户端 sing-box 配置的 outbounds 数组
  // 返回: proxy outbounds + selector("proxy") + direct
  func BuildClientConfig(input ClientConfigInput) ([]interface{}, error)
  ```

  生成规则：
  - 遍历所有 inbound 节点，过滤出 `nodeSupportsProtocol(node, user.Spec.Protocol)` 的节点
  - 对每个 inbound 节点：
    - 解析 `EntryEndpoints`（格式 `protocol:address:port`），找到匹配 user.Spec.Protocol 的端点
    - 若 EntryEndpoints 为空或无匹配协议 → 跳过该节点（log warning）
    - 获取该节点的出口节点列表：
      - 若有 `RoutesByInbound[node.Name]`（ProxyRoute），为每个 route 的 outbound 节点生成 outbound
      - 若无 ProxyRoute，检查 `OutboundsByName` 中同 region 的出口节点（同 region 自动关联）
    - 对每个出口节点生成一个 outbound：
      - tag = `{inboundNodeName}-{outboundNodeName}`
      - server = inbound 节点的 address（从 EntryEndpoints 解析）
      - server_port = inbound 节点的 protocol port（从 EntryEndpoints 解析）
      - type = user.Spec.Protocol（vless/trojan/socks5/http）
      - UUID/password = `configengine.DeriveUUID(userCred.UUID, outboundNodeName)` 或 `configengine.DerivePassword(userCred.Password, outboundNodeName)`
  - 生成 selector outbound（tag="proxy"，outbounds=所有 proxy outbound tags）
  - 追加 direct outbound（tag="direct"）

  **`internal/apiserver/template.go`**:

  ```go
  // DefaultTemplate 内置默认客户端模板（无管理员模板时使用）
  var DefaultTemplate = `{
    "log": {"level": "info"},
    "inbounds": [
      {"type": "socks", "tag": "socks-in", "listen": "127.0.0.1", "listen_port": 1080},
      {"type": "http", "tag": "http-in", "listen": "127.0.0.1", "listen_port": 1081}
    ],
    "outbounds": [],
    "route": {
      "rules": [
        {"type": "logical", "mode": "or", "rules": [{"geoip": "cn"}, {"geosite": "cn"}], "outbound": "direct"}
      ],
      "final": "proxy",
      "auto_detect_interface": true
    }
  }`

  // MergeOutbounds 将生成的 outbounds 注入到模板配置中
  // 模板的 outbounds 数组被完全替换为 generatedOutbounds
  func MergeOutbounds(templateJSON []byte, generatedOutbounds []interface{}) ([]byte, error)
  ```

  **Must NOT do**:
  - 不访问 K8s API（所有数据由 handler 传入）
  - 不修改 configengine 包中的任何现有函数
  - 不在 outbound 中使用 relay port（客户端直连 inbound 节点的公开端口）

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 1 并行开始，但需要 Task 1 完成后才能使用 DerivePassword）
  - **Parallel Group**: Wave 1（实际需等 Task 1 完成）
  - **Blocks**: Task 3, Task 5
  - **Blocked By**: Task 1

  **References**:
  - `internal/configengine/engine.go:183-215` — `DeriveUUID` + `DerivePassword` 函数
  - `internal/configengine/engine.go:208-210` — `virtualUserName` 函数（格式参考）
  - `internal/controller/proxynode_controller.go:424-428` — EntryEndpoints 格式（`protocol:address:port`）
  - `internal/credmanager/credmanager.go:36-46` — `UserCredential` 结构体
  - `api/v1alpha1/proxynode_types.go:43-68` — ProxyNodeSpec（address, region, supportedProtocols）
  - `api/v1alpha1/proxyroute_types.go:24-31` — ProxyRouteSpec（inboundNode, outboundNode）
  - sing-box vless outbound: https://sing-box.sagernet.org/configuration/outbound/vless/
  - sing-box selector outbound: https://sing-box.sagernet.org/configuration/outbound/selector/

  **Acceptance Criteria**:
  - [ ] `go build ./internal/apiserver/...` PASS
  - [ ] `BuildClientConfig` 对有 2 个出口节点的 inbound 节点生成 2 个 proxy outbounds + 1 个 selector + 1 个 direct = 4 个 outbounds
  - [ ] 每个 proxy outbound 的 UUID = `DeriveUUID(userBaseUUID, outboundNodeName)`
  - [ ] `MergeOutbounds` 正确替换模板的 outbounds 数组

  **QA Scenarios**:
  ```
  Scenario: 2个出口节点生成4个outbounds
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/apiserver/... -run TestBuildClientConfig -v
    Expected Result: PASS，outbounds 数组长度=4（2 proxy + 1 selector + 1 direct）
    Evidence: .sisyphus/evidence/task-2-client-config-build.txt

  Scenario: derived UUID 正确性
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/apiserver/... -run TestBuildClientConfig_DerivedUUID -v
    Expected Result: PASS，proxy outbound UUID = configengine.DeriveUUID(baseUUID, outboundNodeName)
    Evidence: .sisyphus/evidence/task-2-client-config-uuid.txt

  Scenario: MergeOutbounds 模板替换
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/apiserver/... -run TestMergeOutbounds -v
    Expected Result: PASS，模板的 outbounds 被替换，inbounds 保留
    Evidence: .sisyphus/evidence/task-2-template-merge.txt
  ```

  **Commit**: YES（与 Task 3 合并）

- [x] 3. HTTP server + handler

  **What to do**:

  **`internal/apiserver/server.go`**:

  实现 `controller-runtime Runnable` 接口：
  ```go
  package apiserver

  import (
      "context"
      "net/http"
      "sigs.k8s.io/controller-runtime/pkg/client"
  )

  // Server 是嵌入 manager 的 HTTP API server
  type Server struct {
      BindAddress      string
      TemplateRef      string  // "namespace/name" 或 ""
      Client           client.Client
  }

  // Start 实现 manager.Runnable 接口
  func (s *Server) Start(ctx context.Context) error

  // NeedLeaderElection 返回 false（API server 不需要 leader election）
  func (s *Server) NeedLeaderElection() bool { return false }
  ```

  **`internal/apiserver/handler.go`**:

  ```go
  // handleClientConfig 处理 GET /api/v1/client-config/{namespace}/{uuid}
  func (s *Server) handleClientConfig(w http.ResponseWriter, r *http.Request)
  ```

  Handler 逻辑：
  1. 从 URL path 提取 `{namespace}` 和 `{uuid}`（使用 `strings.Split` 或 `http.ServeMux` path parsing）
  2. 验证 UUID 格式（正则 `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`，大小写不敏感）
     - 不合法 → HTTP 400
  3. 列出 namespace 下所有 ProxyUsers
  4. 对每个 ProxyUser 调用 `credmanager.GetUserCredential()` 读取凭证
     - 若 `cred.UUID == requestUUID` → 找到匹配用户
     - 若 `cred.UUID == ""` → 跳过（空 UUID 不参与匹配）
  5. 若未找到 → HTTP 404
  6. 列出 namespace 下所有 ProxyNodes（过滤出 inbound 角色）
  7. 列出 namespace 下所有 ProxyRoutes
  8. 构建 `ClientConfigInput`
  9. 调用 `BuildClientConfig()` 生成 outbounds
  10. 加载模板（若 TemplateRef 非空，读取 ConfigMap；否则使用 DefaultTemplate）
  11. 调用 `MergeOutbounds()` 合并
  12. 返回 HTTP 200，`Content-Type: application/json`，JSON body

  错误处理：
  - K8s API 错误 → HTTP 500，log 详细错误，响应只返回 `{"error": "internal server error"}`
  - 模板 ConfigMap 不存在 → log warning，使用 DefaultTemplate 继续
  - 找到多个匹配 UUID 的 ProxyUser → log warning，使用第一个

  **Must NOT do**:
  - 不在 HTTP 响应中暴露 Secret 名称、K8s 错误详情
  - 不实现任何形式的认证（UUID 即唯一凭证）
  - 不实现 TLS

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 4 并行）
  - **Parallel Group**: Wave 2
  - **Blocks**: Task 4, Task 5
  - **Blocked By**: Task 1, Task 2

  **References**:
  - `internal/credmanager/credmanager.go:111-127` — `GetUserCredential()` 函数
  - `internal/controller/proxynode_controller.go:169-253` — `collectInput()` 参考（同类型 K8s 查询模式）
  - controller-runtime Runnable: `sigs.k8s.io/controller-runtime/pkg/manager.Runnable`
  - `cmd/main.go:158-176` — manager 初始化参考

  **Acceptance Criteria**:
  - [ ] `go build ./internal/apiserver/...` PASS
  - [ ] UUID 格式验证：`../evil` → 400，`not-a-uuid` → 400，`f47ac10b-58cc-4372-a567-0e02b2c3d479` → 不报 400
  - [ ] 未找到 UUID → 404
  - [ ] 找到 UUID → 200 + 有效 JSON

  **QA Scenarios**:
  ```
  Scenario: UUID 格式验证拒绝非法输入
    Tool: Bash (go test with httptest)
    Steps:
      1. go test ./internal/apiserver/... -run TestHandler_InvalidUUID -v
    Expected Result: PASS，非法 UUID 返回 400
    Evidence: .sisyphus/evidence/task-3-handler-uuid-validation.txt

  Scenario: UUID 不存在返回 404
    Tool: Bash (go test with httptest + fake client)
    Steps:
      1. go test ./internal/apiserver/... -run TestHandler_UUIDNotFound -v
    Expected Result: PASS，返回 HTTP 404
    Evidence: .sisyphus/evidence/task-3-handler-notfound.txt
  ```

  **Commit**: YES
  - Message: `feat(apiserver): add client sing-box config generation HTTP API`
  - Files: `internal/apiserver/`
  - Pre-commit: `go build ./... && go test ./internal/apiserver/...`

- [x] 4. main.go 集成 + flags

  **What to do**:
  - 在 `cmd/main.go` 中新增两个 flag：
    ```go
    var apiBindAddress string
    var clientConfigTemplate string
    flag.StringVar(&apiBindAddress, "api-bind-address", ":8082",
        "The address the client config API endpoint binds to.")
    flag.StringVar(&clientConfigTemplate, "client-config-template", "",
        "ConfigMap reference for client config template in namespace/name format.")
    ```
  - 在 manager 启动前注册 API server：
    ```go
    apiSrv := &apiserver.Server{
        BindAddress: apiBindAddress,
        TemplateRef: clientConfigTemplate,
        Client:      mgr.GetClient(),
    }
    if err := mgr.Add(apiSrv); err != nil {
        setupLog.Error(err, "Failed to register API server")
        os.Exit(1)
    }
    ```
  - 添加 import `"github.com/shlande/singbox-operator/internal/apiserver"`

  **Must NOT do**:
  - 不修改现有 flag 或 controller 注册逻辑
  - 不在 main.go 中实现任何业务逻辑

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: YES（与 Task 5 并行）
  - **Parallel Group**: Wave 2
  - **Blocks**: F1-F3
  - **Blocked By**: Task 3

  **References**:
  - `cmd/main.go:57-87` — 现有 flag 定义区域
  - `cmd/main.go:182-215` — controller/webhook 注册区域（在 `// +kubebuilder:scaffold:builder` 前添加）
  - controller-runtime manager.Add: `sigs.k8s.io/controller-runtime/pkg/manager.Manager.Add()`

  **Acceptance Criteria**:
  - [ ] `go build ./cmd/...` PASS
  - [ ] `./manager --help` 显示 `--api-bind-address` 和 `--client-config-template` flags
  - [ ] manager 启动后 `:8082` 端口可访问

  **QA Scenarios**:
  ```
  Scenario: manager 启动后 API 端口可访问
    Tool: Bash (curl)
    Steps:
      1. 启动 manager（本地或 envtest 模式）
      2. curl -s -o /dev/null -w "%{http_code}" http://localhost:8082/api/v1/client-config/default/nonexistent
    Expected Result: 返回 404（不是 connection refused）
    Evidence: .sisyphus/evidence/task-4-main-integration.txt
  ```

  **Commit**: YES
  - Message: `feat(cmd): register client config API server with manager`
  - Files: `cmd/main.go`
  - Pre-commit: `go build ./cmd/...`

- [x] 5. 单元测试

  **What to do**:

  创建 `internal/apiserver/handler_test.go`，覆盖以下场景：

  使用 `net/http/httptest` + controller-runtime fake client：

  ```go
  // TestHandler_InvalidUUID: 非法 UUID 格式 → 400
  // TestHandler_MissingNamespace: 缺少 namespace 或 uuid → 404
  // TestHandler_UUIDNotFound: UUID 不匹配任何 ProxyUser → 404
  // TestHandler_Success: 正常场景，1 inbound + 2 outbound nodes → 200 + 正确 JSON
  // TestHandler_EmptyEntryEndpoints: inbound 节点 EntryEndpoints 为空 → 跳过，200（可能 0 proxy outbounds）
  // TestBuildClientConfig_DerivedUUID: 验证 proxy outbound UUID = DeriveUUID(baseUUID, outboundNodeName)
  // TestBuildClientConfig_TrojanPassword: 验证 trojan outbound password = DerivePassword(basePassword, outboundNodeName)
  // TestMergeOutbounds_ReplaceOutbounds: 模板 outbounds 被完全替换
  // TestMergeOutbounds_PreserveInbounds: 模板 inbounds 保留不变
  // TestMergeOutbounds_InvalidTemplate: 模板 JSON 无效 → error
  ```

  测试数据构造：
  ```go
  func makeProxyNode(name, region, address string, roles []v1alpha1.ProxyRole, protocols []v1alpha1.ProtocolConfig) *v1alpha1.ProxyNode
  func makeProxyUser(name, protocol, secretName string) *v1alpha1.ProxyUser
  func makeUserSecret(name, uuid, password string) *corev1.Secret
  func makeProxyRoute(name, inbound, outbound string) *v1alpha1.ProxyRoute
  ```

  **Must NOT do**:
  - 不使用真实 K8s 集群（使用 fake client）
  - 不测试 main.go 集成（已有 e2e 测试覆盖）

  **Recommended Agent Profile**:
  - **Category**: `unspecified-high`
  - **Skills**: []

  **Parallelization**:
  - **Can Run In Parallel**: NO（依赖 Task 2, 3 完成）
  - **Parallel Group**: Wave 3（串行）
  - **Blocks**: F1-F3
  - **Blocked By**: Task 2, Task 3

  **References**:
  - `internal/controller/proxynode_controller_test.go` — envtest 测试模式参考
  - `internal/configengine/engine_test.go` — 纯单元测试模式参考（无 K8s 依赖）
  - `net/http/httptest` — Go 标准库 HTTP 测试

  **Acceptance Criteria**:
  - [ ] `go test ./internal/apiserver/... -v` PASS（≥ 9 test cases）
  - [ ] 覆盖率 > 80%（`go test -cover`）
  - [ ] 无 race condition（`go test -race`）

  **QA Scenarios**:
  ```
  Scenario: 完整测试套件
    Tool: Bash (go test)
    Steps:
      1. go test ./internal/apiserver/... -v -race -cover
    Expected Result: 所有测试 PASS，无 race，覆盖率 > 80%
    Evidence: .sisyphus/evidence/task-5-unit-tests.txt
  ```

  **Commit**: YES
  - Message: `test(apiserver): add unit tests for client config handler`
  - Files: `internal/apiserver/handler_test.go`
  - Pre-commit: `go test ./internal/apiserver/... -race`

---

## Final Verification Wave

> 3 review agents run in PARALLEL. ALL must APPROVE. Present consolidated results to user and get explicit "okay" before completing.

- [x] F1. **Plan Compliance Audit** — `oracle`
  Read the plan end-to-end. For each "Must Have": verify implementation exists. For each "Must NOT Have": search codebase for forbidden patterns. Check evidence files exist.
  Output: `Must Have [N/N] | Must NOT Have [N/N] | Tasks [N/N] | VERDICT: APPROVE/REJECT`

- [x] F2. **Code Quality Review** — `unspecified-high`
  Run `go build ./...` + `go vet ./...`. Review all changed files for: `as any`/`@ts-ignore`, empty catches, unused imports. Check AI slop: excessive comments, over-abstraction.
  Output: `Build [PASS/FAIL] | Vet [PASS/FAIL] | Tests [N pass/N fail] | VERDICT`

- [x] F3. **Real Manual QA** — `unspecified-high`
  Start from clean state. Execute EVERY QA scenario from EVERY task. Save to `.sisyphus/evidence/final-qa/`.
  Output: `Scenarios [N/N pass] | VERDICT`

---

## Commit Strategy

- **1**: `refactor(configengine): export DerivePassword for client config generation`
- **2+3**: `feat(apiserver): add client sing-box config generation HTTP API`
- **4**: `feat(cmd): register client config API server with manager`
- **5**: `test(apiserver): add unit tests for client config handler`

---

## Success Criteria

### Verification Commands
```bash
go build ./...                                    # Expected: no errors
go test ./internal/apiserver/... -v               # Expected: all PASS
go test ./internal/configengine/... -v            # Expected: all PASS (no regression)
curl http://localhost:8082/api/v1/client-config/default/nonexistent  # Expected: 404
curl http://localhost:8082/api/v1/client-config/default/{valid-uuid} # Expected: 200 + JSON
```

### Final Checklist
- [ ] `DerivePassword` 已导出
- [ ] HTTP API 在 `:8082` 监听
- [ ] UUID 格式验证正常工作
- [ ] 每个出口节点生成独立 outbound with derived UUID
- [ ] selector outbound（tag="proxy"）包含所有 proxy outbound tags
- [ ] 模板合并正确（outbounds 替换，其余保留）
- [ ] 内置默认模板包含 socks5/http inbound + geoip:cn/geosite:cn 分流规则
- [ ] 所有单元测试通过
