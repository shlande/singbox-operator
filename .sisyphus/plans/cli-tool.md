# CLI Tool: sbctl — user init command

## TL;DR

> **Quick Summary**: 在现有 sing-box-operator 项目中新增一个独立的 `sbctl` CLI 二进制，入口为 `cmd/cli/main.go`，通过本地 kubeconfig 连接集群，提供 `user init <name>` 命令快速初始化一个 User 资源（自动生成 UUID、创建 Secret 和 User CR）。
>
> **Deliverables**:
> - `cmd/cli/main.go` — CLI 入口，cobra root command
> - `internal/cli/user/init.go` — `user init` 子命令实现
> - `bin/sbctl` — 可执行二进制（由 `go build` 生成）
>
> **Estimated Effort**: Quick
> **Parallel Execution**: NO — sequential (单任务，无并行必要)
> **Critical Path**: Task 1 → Task 2 → Task 3 (Final Verification)

---

## Context

### Original Request
增加一个配套的 CLI 工具，读取本地 kubeconfig 配置尝试连接集群，获取一些基本信息辅助运维。目前只需要增加一个简单的方法，用于快速初始化一个用户。

### Interview Summary
**Key Discussions**:
- **入口位置**: `cmd/cli/main.go`（与 operator 的 `cmd/main.go` 并列）
- **init-user 行为**: 自动生成 UUID + 创建 Secret + 创建 User CR + 支持 --protocol + 支持 --namespace/-n + 打印摘要
- **kubeconfig 加载**: 标准 client-go 方式（--kubeconfig flag → KUBECONFIG env → ~/.kube/config）

**Research Findings**:
- `cobra` 已在 go.mod 作为间接依赖，直接 import 即可提升为直接依赖
- `github.com/google/uuid` 已在 go.mod 中（`v1.6.0`）
- `User.Spec.Protocol` 字段存在，接受 `hysteria2|vless|trojan|socks5|http`，默认 `hysteria2`
- `User.Spec.AuthSecret` 是 `corev1.SecretReference{Name, Namespace}`
- credmanager 中 Secret 的 `uuid` key 是既定约定
- scheme 注册模式参考 `cmd/main.go`

### Metis Review
**Identified Gaps** (addressed):
- `generateUUID()` 不可导出 → CLI 自己实现（使用 `github.com/google/uuid` 包，已在 go.mod）
- 部分失败原子性（Secret 创建成功但 User 创建失败）→ 明确策略：尝试删除 Secret 做 best-effort cleanup，并打印错误
- 重复名称处理 → Secret 或 User 已存在时，返回明确错误并退出非零
- `--context` flag → 本次 out of scope，文档中注明

---

## Work Objectives

### Core Objective
新增 `cmd/cli/main.go` 和 `internal/cli/user/` 包，实现 `sbctl user init <name>` 命令，一键在指定 namespace 中创建认证 Secret 和 User CR。

### Concrete Deliverables
- `cmd/cli/main.go` — cobra root command，含 `--kubeconfig` 全局 flag
- `internal/cli/user/init.go` — `user init` 子命令，含 `--protocol`、`--namespace/-n` flag
- `bin/sbctl` 可由 `go build -o bin/sbctl ./cmd/cli/` 构建

### Definition of Done
- [ ] `go build -o bin/sbctl ./cmd/cli/` 退出码 0
- [ ] `./bin/sbctl user init --help` 输出包含 `--protocol`、`--namespace` 描述
- [ ] 对已安装 CRD 的集群执行 `user init alice`，kubectl 可查到 User CR 和 Secret

### Must Have
- cobra root command with `--kubeconfig` global flag
- `user init <name>` 子命令，flag: `--protocol`（枚举验证）、`--namespace/-n`
- 使用 `github.com/google/uuid` 生成 UUID v4
- 创建 Secret（key: `uuid`，name: `<name>-auth`，namespace 与 User 相同）
- 创建 User CR（`spec.authSecret.name=<name>-auth`，`spec.protocol=<protocol>`）
- 打印结果摘要（user name、uuid、namespace）
- Secret 创建成功但 User 创建失败时，best-effort 删除 Secret 并报错

### Must NOT Have (Guardrails)
- **不得**修改 `cmd/main.go` 或任何现有 operator 代码
- **不得**修改 `api/v1alpha1/*_types.go` 或其他 CRD 相关文件
- **不得**运行 `make manifests` / `make generate`
- **不得**添加 `user list`、`user delete`、`--output json/yaml` 等超出本次 scope 的功能
- **不得**添加 `--dry-run`、`--context`、shell completion 等扩展功能
- **不得**修改 `credmanager` 包（不导出 `generateUUID`，CLI 自己实现）
- **不得**添加针对资源名称格式的客户端校验（交给 API server）

---

## Verification Strategy

### Test Decision
- **Infrastructure exists**: YES (Ginkgo + envtest)
- **Automated tests**: NO（CLI 命令测试需要真实集群或 envtest 集成，本次 scope 仅验证编译和 help 输出）
- **Agent-Executed QA**: YES（编译验证 + help text 验证）

### QA Policy
每个 task 包含 agent-executed QA scenarios，通过 Bash 工具执行。

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (Sequential — 单任务):
└── Task 1: 实现 sbctl CLI（cmd/cli/main.go + internal/cli/user/init.go）[quick]

Wave FINAL:
├── Task F1: Plan Compliance Audit (oracle)
├── Task F2: Code Quality Review (unspecified-high)
└── Task F3: Real Build + QA (unspecified-high)
```

### Agent Dispatch Summary
- **Wave 1**: T1 → `quick`
- **FINAL**: F1 → `oracle`, F2 → `unspecified-high`, F3 → `unspecified-high`

---

## TODOs

- [x] 1. 实现 sbctl CLI：cmd/cli/main.go + internal/cli/user/init.go

  **What to do**:
  1. 创建 `internal/cli/user/init.go`，package `user`，实现 `NewInitCmd() *cobra.Command`：
     - 接受位置参数 `<name>`（必须，否则打印 usage 并退出）
     - flag `--protocol`（string，default `hysteria2`，valid: `hysteria2|vless|trojan|socks5|http`）
     - flag `--namespace` / `-n`（string，default `default`）
     - 在 `RunE` 中：
       a. 验证 `--protocol` 枚举值（非法值立即返回错误）
       b. 使用 `github.com/google/uuid` 生成 UUID v4 字符串
       c. 创建 Secret：`name=<name>-auth`，`namespace=<namespace>`，`data: {"uuid": <uuid>}`，类型 `corev1.SecretTypeOpaque`
       d. 创建 User CR：`name=<name>`，`namespace=<namespace>`，`spec.protocol=<protocol>`，`spec.authSecret={name: "<name>-auth", namespace: "<namespace>"}`
       e. 若 User 创建失败，best-effort 删除刚创建的 Secret，然后 `return err`
       f. 打印摘要：`User "<name>" initialized in namespace "<namespace>"\n  UUID: <uuid>`
  2. 创建 `internal/cli/user/user.go`，package `user`，实现 `NewUserCmd() *cobra.Command`（group command，`Use: "user"`，添加 `init` 子命令）
  3. 创建 `cmd/cli/main.go`：
     - 注册 scheme：`clientgoscheme.AddToScheme` + `proxyv1alpha1.AddToScheme`
     - root command `sbctl`，全局 persistent flag `--kubeconfig`（string，default `""`）
     - 在 PersistentPreRunE 中：用 `clientcmd.BuildConfigFromFlags("", kubeconfigPath)` 加载 config（优先 flag，否则 clientcmd.RecommendedHomeFile），构建 `client.New(cfg, client.Options{Scheme: scheme})`，注入到 cobra context
     - 添加 `user` subcommand

  **Must NOT do**:
  - 不得修改 `cmd/main.go` 或任何已有文件
  - 不得添加 `user list`、`user delete` 等额外命令
  - 不得添加 `--output`、`--dry-run`、`--context` flag
  - 不得修改 `credmanager` 包

  **Recommended Agent Profile**:
  > 单文件新增任务，逻辑清晰，无架构决策。
  - **Category**: `quick`
    - Reason: 新增 2-3 个文件，逻辑直接，无复杂依赖
  - **Skills**: []
  - **Skills Evaluated but Omitted**:
    - `git-master`: 不需要 git 操作

  **Parallelization**:
  - **Can Run In Parallel**: NO
  - **Parallel Group**: Wave 1 (唯一任务)
  - **Blocks**: Final Verification
  - **Blocked By**: None (can start immediately)

  **References**:

  **Pattern References** (existing code to follow):
  - `cmd/main.go:19-55` — scheme 注册模式（`clientgoscheme.AddToScheme` + `proxyv1alpha1.AddToScheme`），在 CLI 的 `main.go` 中复用相同模式
  - `cmd/main.go:174` — `ctrl.GetConfigOrDie()` 是 operator 用法；CLI 应改用 `clientcmd.BuildConfigFromFlags("", path)` 以支持 kubeconfig flag
  - `internal/credmanager/credmanager.go:127-134` — `generateUUID()` 的实现逻辑（但不可直接调用，需在 CLI 包中用 `github.com/google/uuid` 重新实现）
  - `internal/credmanager/credmanager.go:75-95` — Secret 创建模式（字段名 `uuid`、namespace 对齐）
  - `api/v1alpha1/user_types.go:25-35` — `UserSpec` 结构，`Protocol` 和 `AuthSecret` 字段定义

  **API/Type References**:
  - `api/v1alpha1/user_types.go:UserSpec` — `Protocol string`（枚举）、`AuthSecret corev1.SecretReference`
  - `api/v1alpha1/groupversion_info.go` — scheme 注册 `SchemeBuilder`

  **External References**:
  - `k8s.io/client-go/tools/clientcmd` — `BuildConfigFromFlags(masterUrl, kubeconfigPath string)` 加载 kubeconfig
  - `github.com/google/uuid` — `uuid.New().String()` 生成 UUID v4
  - `github.com/spf13/cobra` — `cobra.Command{Use, Short, Args, RunE}`

  **Acceptance Criteria**:

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: 二进制编译成功
    Tool: Bash
    Preconditions: 项目根目录，go 环境正常
    Steps:
      1. go build -o bin/sbctl ./cmd/cli/
      2. 检查退出码为 0
      3. 检查 bin/sbctl 文件存在
    Expected Result: 退出码 0，bin/sbctl 文件存在
    Failure Indicators: 编译错误输出，退出码非 0
    Evidence: .sisyphus/evidence/task-1-build.txt

  Scenario: help 文本包含必要 flag
    Tool: Bash
    Preconditions: bin/sbctl 已编译
    Steps:
      1. ./bin/sbctl user init --help
      2. 检查输出包含 "--protocol"
      3. 检查输出包含 "--namespace" 或 "-n"
      4. 检查输出包含 "name" 参数描述
    Expected Result: help 文本包含所有必要 flag 说明
    Failure Indicators: 缺少任一 flag 描述
    Evidence: .sisyphus/evidence/task-1-help.txt

  Scenario: 无参数时打印 usage 并退出非零
    Tool: Bash
    Preconditions: bin/sbctl 已编译
    Steps:
      1. ./bin/sbctl user init 2>&1; echo "exit: $?"
      2. 检查退出码非 0
      3. 检查输出包含 usage 信息
    Expected Result: 退出码非 0，显示 usage
    Failure Indicators: 退出码为 0，或 panic
    Evidence: .sisyphus/evidence/task-1-noargs.txt

  Scenario: 非法 protocol 被拒绝
    Tool: Bash
    Preconditions: bin/sbctl 已编译
    Steps:
      1. ./bin/sbctl user init testuser --protocol invalid 2>&1; echo "exit: $?"
      2. 检查退出码非 0
      3. 检查错误信息提及有效协议或 "invalid"
    Expected Result: 退出码非 0，错误信息清晰
    Failure Indicators: 退出码为 0，或静默忽略非法协议
    Evidence: .sisyphus/evidence/task-1-invalid-protocol.txt
  ```

  **Evidence to Capture**:
  - [ ] `task-1-build.txt` — `go build` 输出
  - [ ] `task-1-help.txt` — `./bin/sbctl user init --help` 输出
  - [ ] `task-1-noargs.txt` — 无参数时的错误输出
  - [ ] `task-1-invalid-protocol.txt` — 非法 protocol 的错误输出

  **Commit**: YES
  - Message: `feat(cli): add sbctl user init command`
  - Files: `cmd/cli/main.go`, `internal/cli/user/init.go`, `internal/cli/user/user.go`
  - Pre-commit: `go build ./cmd/cli/`

---

## Final Verification Wave (MANDATORY — after ALL implementation tasks)

> 3 review agents run in PARALLEL. ALL must APPROVE. Present consolidated results to user and get explicit "okay" before completing.

- [x] F1. **Plan Compliance Audit** — `oracle`
  读取 plan 中的 Must Have 和 Must NOT Have 列表。验证：(1) `cmd/cli/main.go` 存在且包含 cobra root command；(2) `internal/cli/user/init.go` 存在且包含 `--protocol`、`--namespace` flag；(3) 已有文件（`cmd/main.go`、`api/v1alpha1/*`、`credmanager/*`）未被修改（`git diff --name-only`）；(4) evidence 文件存在于 `.sisyphus/evidence/`。
  Output: `Must Have [N/N] | Must NOT Have [N/N] | VERDICT: APPROVE/REJECT`

- [x] F2. **Code Quality Review** — `unspecified-high`
  运行 `go build ./...` 和 `go vet ./...`。检查新增文件：无 `as any`/`@ts-ignore`，无 empty catch，无 commented-out code，无 unused imports。检查 AI slop：无过度注释、无泛型命名（data/result/item）。
  Output: `Build [PASS/FAIL] | Vet [PASS/FAIL] | Files [N clean/N issues] | VERDICT`

- [x] F3. **Real Build + QA** — `unspecified-high`
  从干净状态执行 task-1 中的所有 QA scenarios：编译、help 文本验证、无参数错误、非法 protocol 错误。保存 evidence 到 `.sisyphus/evidence/`。
  Output: `Scenarios [N/N pass] | VERDICT`

---

## Commit Strategy

- **Task 1**: `feat(cli): add sbctl user init command` — `cmd/cli/main.go`, `internal/cli/user/init.go`, `internal/cli/user/user.go`

---

## Success Criteria

### Verification Commands
```bash
go build -o bin/sbctl ./cmd/cli/  # Expected: exit 0, bin/sbctl exists
./bin/sbctl user init --help       # Expected: shows --protocol, --namespace flags
./bin/sbctl user init --protocol invalid 2>&1; echo $?  # Expected: exit non-zero
go vet ./...                       # Expected: no errors
```

### Final Checklist
- [ ] `cmd/cli/main.go` 存在，cobra root command，含 `--kubeconfig` flag
- [ ] `internal/cli/user/init.go` 存在，含 `user init` 逻辑
- [ ] `go build ./cmd/cli/` 成功
- [ ] `go vet ./...` 无错误
- [ ] 已有文件未被修改（`git diff --name-only` 只显示新增文件）
- [ ] 非法 protocol 返回非零退出码
- [ ] 无参数时返回非零退出码
