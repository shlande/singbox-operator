# Draft: inbound-protocol-consistency-webhook

## Intent routing
- intent: clear
- review_required: false
- 用户明确知道目标：加 webhook 校验防止 InboundProtocol 与 SupportedProtocols 不一致

## Decisions (recorded during interview)
1. 修复方案 = 方向 A：保留 InboundProtocol(string) + SupportedProtocols([]ProtocolConfig) 双字段，加 webhook 一致性校验 + 补测试
2. 数据修复不需要了 — sc-hk 当前配置已正确（inboundProtocol=naive, supportedProtocols=[{naive,50443}], entryEndpoints=[naive:103.240.198.26:50443]），shlande 的 client-config 已包含 sc-hk + naive（type=naive, server=103.240.198.26:50443, tls.enabled=true, server_name=sing-box.shlande.top, username=shlande#hytron-hk, tag=hytron-hk#sc-hk）
3. SupportedProtocols 里定义 InboundProtocol 不支持的协议 = 无意义且有害（占用 hostPort、触发不必要的 TLS 挂载）—— 用户已确认理解
4. 删除 SupportedProtocols 会影响 socks5 relay？= 不会，relay 用独立的 spec.relayPort + 固定容器端口 10808 + 固定 socks 协议，已用代码验证
5. 不做方向 B（合并字段），因为破坏性 CRD 变更 + 存量迁移 + 10+ 文件改动，工作量 5-10x

## Root cause (verified by 3 parallel explore agents + direct code reading + live cluster inspection)
协议由三个独立字段共同决定，无自动同步：
1. spec.inboundProtocol → EffectiveInboundProtocol() 决定实际生成哪种 inbound（默认 hysteria2）
2. spec.supportedProtocols[].protocol → supportsProtocol() 检查生效协议是否在列表里
3. status.entryEndpoints → findEntryEndpoint() 查找 protocol:address:port

三者不一致时 BuildClientConfig 静默跳过节点（client_config.go:49-51）。

webhook Default() 是空实现（singboxnode_webhook.go:37-39）
ValidateUpdate 不做 old-vs-new 对比（singboxnode_webhook.go:45-47）
controller 从不写 spec.supportedProtocols（.SupportedProtocols = 零次赋值）
controller 只在 updateStatus() 把 spec.supportedProtocols 派生成 status.entryEndpoints

## Cluster state (verified via kubectl through Sisyphus agent)
sc-hk (namespace=sing-box-operator):
- spec.inboundProtocol: naive
- spec.supportedProtocols: [{naive, 50443}]
- spec.region: hk, spec.roles: [inbound], spec.address: 103.240.198.26
- spec.relayPort: 未设置（inbound-only 节点）
- status.entryEndpoints: [naive:103.240.198.26:50443]
- status.conditions: NodeReady=True, Ready=True, observedGeneration=4
- 已正确 reconcile

shlande (namespace=sing-box-operator):
- spec.userGroupRef: 未设置（无限制，allow all）
- spec.authSecret: {name: shlande-secret, namespace: sing-box-operator}
- spec.protocol: hysteria2（migration 遗留残留字段，CRD 已不认，无影响 — 本次不清理）
- status.activeNodes: [dc-jp, hytron-hk, lightcore-hk, xtom-sjc]（陈旧，不含 sc-hk，但 client-config 实时计算不受影响）
- UUID: f0a5a0d6-951a-4936-a7e7-93a8f86f2fb8

shlande client-config（通过 curl 实际验证）:
- 包含 hytron-hk#sc-hk outbound: type=naive, server=103.240.198.26, server_port=50443, tls.enabled=true, server_name=sing-box.shlande.top, username=shlande#hytron-hk
- ✓ 已正确使用 naive 协议

同 region (hk) outbound 节点: hytron-hk (outbound+inbound, relayPort 存在)
shlande 通过 hytron-hk 的 socks relay 连到 sc-hk 的 naive inbound

UserGroup prod: deniedNodes=[sc-hk]（但 shlande 不属于任何 UserGroup，不受影响）
CustomRoute: 无

## Key code references (verified)
- api/v1alpha1/singboxnode_types.go:42-52  ProtocolConfig struct
- api/v1alpha1/singboxnode_types.go:68-77  SupportedProtocols + InboundProtocol 字段
- internal/configengine/engine.go:227-232  EffectiveInboundProtocol（默认 hysteria2）
- internal/webhook/singboxnode_webhook.go:37-39  Default() 空
- internal/webhook/singboxnode_webhook.go:45-47  ValidateUpdate 不对比 old/new
- internal/webhook/singboxnode_webhook.go:53-154  validateSingBoxNode 当前校验逻辑（无 InboundProtocol 校验）

## Plan scope
只加 webhook 一致性校验 + 测试。不修数据。不动 CRD。不动 relay。

## Approval gate
- status: approved（用户已选"只加 webhook 校验"）
- pending action: write .omo/plans/inbound-protocol-consistency-webhook.md
- approach: 在 validateSingBoxNode 中加校验：inbound 角色 → EffectiveInboundProtocol ∈ SupportedProtocols 且 SupportedProtocols 非空

## Metis review (completed)
关键发现已纳入计划：
1. CRITICAL: 现有测试 fixture "accepts valid SingBoxNode with IP"(line 245) 和 "accepts both inbound and outbound roles"(line 458) 会因新校验而失败——它们的 fixture 构造了不一致配置(InboundProtocol="" + SupportedProtocols=[vless] 或空)。修正：更新这些 fixture 为一致配置。这不是"修改现有测试"违规，因为这些 fixture 本身就是 bug 的范例。
2. CRITICAL: 插入点统一为 line 148 之后、line 150 之前（if len(allErrs) > 0 之前），不是 line 153 的 return nil 之前。
3. HIGH: 集群兼容性已验证——所有 6 个 inbound 节点配置一致，部署后不会锁死任何节点。
4. MEDIUM: 测试 fixture 必须用完整 Go struct literal `v1alpha1.ProtocolConfig{Protocol: "xxx", Port: 12345}`。
5. 新增 dual-role 测试用例（test 7）。
6. LOW: hysteria2 默认值在 configengine 和 webhook 两处重复——加注释互相引用。

## review_required = false
用户未要求高精度评审。范围小（2 文件）、风险低（只增校验）。Metis 已完成，无需 Momus/Oracle 双审。
