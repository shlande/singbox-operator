# Draft: allowed-outbounds

- **slug**: allowed-outbounds
- **intent**: clear
- **review_required**: false
- **size**: Standard (1-5 files core, clear feature, symmetric to existing AllowedInbounds)
- **status**: plan-complete (awaiting execution via $start-work)

## User request
方案 A：在 `SingBoxNodeSpec` 新增 `AllowedOutbounds` 字段（入站侧出站白名单），与现有 `AllowedInbounds` 对称。实现"入站 A 只允许出站 B（或 A 自身）"的排他语义。

## Key facts (with paths)
- 字段定义: `api/v1alpha1/singboxnode_types.go:85-90` (`AllowedInbounds` 现有字段，新字段对称放在其后)
- Webhook 校验: `internal/webhook/singboxnode_webhook.go:114-130` (AllowedInbounds 校验逻辑，对称复制)
- Controller collectInput 同 region 自动发现: `internal/controller/singboxnode_controller.go:213-219`
- Controller collectInput CustomRoute 段: `internal/controller/singboxnode_controller.go:294-317`
- ConfigEngine buildOutboundNodeOutbounds 防御: `internal/configengine/engine.go:447-480` (已有 AllowedInbounds 防御 at :463-465)
- ConfigEngine buildRouteOutbounds 防御: `internal/configengine/engine.go:482-522` (已有 AllowedInbounds 防御 at :495-497)
- Apiserver resolveOutboundNodes 同 region 段: `internal/apiserver/client_config.go:127-140`
- Apiserver resolveOutboundNodes CustomRoute 段: `internal/apiserver/client_config.go:142-148`
- 现有测试模式参考:
  - webhook: `internal/webhook/webhook_test.go:278-362` (5 个 AllowedInbounds 子测试)
  - controller: `internal/controller/singboxnode_controller_test.go:471-856` (5 个 AllowedInbounds 用例 a-e2)
  - apiserver: `internal/apiserver/node_restriction_test.go` (BuildClientConfig 过滤测试)
  - configengine: `internal/configengine/engine_test.go` (helper makeNode/makeRoute 已就绪)
- 文档: `README.md:7,19-49` (AllowedInbounds 章节，新增对称章节)
- 样本: `config/samples/singboxoperator_v1alpha1_singboxnode.yaml:11-15` (注释示例)

## Approach
对称复制 `AllowedInbounds` 的全部实现模式到入站侧：
1. 新增 `AllowedOutbounds []string` 字段（入站节点上设置，限制可用出站）
2. 语义：空=允许所有同 region 出站（向后兼容）；非空=排他白名单（抑制同 region 自动发现，仅允许列表中的出站，可包含自身名）
3. AND 门：入站 A 用出站 B 需 A.AllowedOutbounds 允许 B 且 B.AllowedInbounds 允许 A（与 CustomRoute+AllowedInbounds 既有一致）
4. 改动 9 处：types/webhook/controller(×2)/configengine(×2)/apiserver(×2)/samples/README
5. 测试：webhook 5 子用例对称、controller 5 用例对称、apiserver 与 configengine 各加用例
6. `make manifests generate` 重生成 CRD/DeepCopy/RBAC

## Decisions (all resolved by symmetric pattern, no surviving forks)
- 字段名: `AllowedOutbounds` (对称)
- JSON key: `allowedOutbounds` (对称)
- marker: `+listType=set` (对称)
- 校验: 非空/去重/空串 (对称复制 webhook:114-130)
- 语义: 排他白名单（非空时抑制同 region 自动发现）— 这是对用户需求的直接表达
- AND 门: 与 AllowedInbounds 一致
- 向后兼容: 空=现状
- 自身作为出站: A 同时是入站+出站时，`allowedOutbounds: ["A"]` 即"只用自身"

## Pending action
write `.omo/plans/allowed-outbounds.md` (scaffold + append todos)

## Approval gate
等用户明确批准后写 plan 文件。
