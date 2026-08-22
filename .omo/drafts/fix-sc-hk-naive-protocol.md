# Draft: fix-sc-hk-naive-protocol

## Intent routing
- intent: clear
- review_required: false
- 用户明确知道目标：让 shlande 的 client-config 包含 sc-hk 节点且使用 naive 协议
- 唯一的 fork 是修复范围和语义设计方向，已通过 interview 解决

## Decisions (recorded during interview)
1. 修复方案 = 方向 A：保留 InboundProtocol(string) + SupportedProtocols([]ProtocolConfig) 双字段，加 webhook 一致性校验 + 数据修复 sc-hk + 补测试
2. naive 端口 = 50443（用户指定）
3. SupportedProtocols 里定义 InboundProtocol 不支持的协议 = 无意义且有害（占用 hostPort、触发不必要的 TLS 挂载）—— 用户已确认理解
4. 删除 SupportedProtocols 会影响 socks5 relay？= 不会，relay 用独立的 spec.relayPort + 固定容器端口 10808 + 固定 socks 协议，已用代码验证
5. 不做方向 B（合并字段），因为破坏性 CRD 变更 + 存量迁移 + 10+ 文件改动，工作量 5-10x

## Root cause (verified by 3 parallel explore agents + direct code reading)
协议由三个独立字段共同决定，无自动同步：
1. spec.inboundProtocol → EffectiveInboundProtocol() 决定实际生成哪种 inbound（默认 hysteria2）
2. spec.supportedProtocols[].protocol → supportsProtocol() 检查生效协议是否在列表里
3. status.entryEndpoints → findEntryEndpoint() 查找 protocol:address:port

三者不一致时 BuildClientConfig 静默跳过节点（client_config.go:49-51）。

webhook Default() 是空实现（singboxnode_webhook.go:37-39）
ValidateUpdate 不做 old-vs-new 对比（singboxnode_webhook.go:45-47）
controller 从不写 spec.supportedProtocols（.SupportedProtocols = 零次赋值）
controller 只在 updateStatus() 把 spec.supportedProtocols 派生成 status.entryEndpoints

用户改 hy2→naive 时最可能只改了 inboundProtocol 没同步 supportedProtocols，导致 supportsProtocol(naive)=false，节点被跳过。

## Key code references (verified)
- api/v1alpha1/singboxnode_types.go:42-52  ProtocolConfig struct
- api/v1alpha1/singboxnode_types.go:68-77  SupportedProtocols + InboundProtocol 字段
- internal/configengine/engine.go:227-232  EffectiveInboundProtocol
- internal/configengine/engine.go:215-222  findProtocolPort
- internal/configengine/engine.go:436-445  buildRelayInbound (relay 不受影响)
- internal/configengine/engine.go:447-486  buildOutboundNodeOutbounds (relay 用 RelayPort)
- internal/apiserver/client_config.go:48-51  EffectiveInboundProtocol + supportsProtocol 检查
- internal/apiserver/client_config.go:88-95  supportsProtocol
- internal/apiserver/client_config.go:97-113 findEntryEndpoint
- internal/apiserver/client_config.go:179-181  naive username override (user#node)
- internal/controller/singboxnode_controller.go:548-552  updateStatus 派生 entryEndpoints
- internal/controller/singboxnode_controller.go:742-761  buildHostPorts (每项 supportedProtocols 都开 hostPort)
- internal/controller/singboxnode_controller.go:778-785  needsTLS (任意 TLS 协议就挂载)
- internal/webhook/singboxnode_webhook.go:37-39  Default() 空
- internal/webhook/singboxnode_webhook.go:45-47  ValidateUpdate 不对比 old/new
- internal/webhook/singboxnode_webhook.go:53-154  validateSingBoxNode 当前校验逻辑

## Pending data (need kubectl output)
- sc-hk 当前 spec.supportedProtocols 残留内容
- sc-hk 当前 status.entryEndpoints
- sc-hk namespace / region / roles
- shlande 的 UserGroup 限制
- 同 region outbound 节点及 allowedInbounds
- CustomRoute 绑定

## Approval gate
- status: awaiting-approval
- pending action: write .omo/plans/fix-sc-hk-naive-protocol.md
- approach: 方向 A（数据修复 + webhook 一致性校验 + 测试）
