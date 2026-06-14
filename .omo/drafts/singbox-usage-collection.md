# Draft: sing-box usage collection

## Requirements (confirmed)
- 需要在当前项目中规划并落地“用户使用流量收集逻辑”的实现方案
- 数据来源基于 sing-box v2rayapi
- 需要支持将使用量直接推送到 Elasticsearch 对应的 datastream
- 收集层需要支持多种目标数据源，后续可扩展到 MySQL 等
- 参考代码仓库：`/Users/shlande/GolandProjects/sing-box`

## Technical Decisions
- 暂未确定：采集触发方式（轮询 / 事件流 / 混合）
- 暂未确定：是否由 Operator 直接推送 ES，还是经由抽象 sink / exporter 层路由
- 暂未确定：多数据源抽象边界放在采集端、转换端还是输出端

## Research Findings
- 当前仓库根目录下未检测到 OpenSpec、Spec Kit、BMAD 规格驱动目录
- 项目为 Kubebuilder/operator 结构仓库，后续需结合 CRD、controller、配置样例与测试方式来规划实现

## Open Questions
- 初始版本除 ES datastream 外，首批必须支持哪些目标源？
- ES datastream 的字段模型、索引命名、认证方式是否已有约束？
- 采集对象粒度是“按用户累计流量”还是“按用户+时间窗口+入/出站维度”?
- 期望通过 CRD 配置 sink/source，还是先固化在 controller 配置中？
- 是否要求补历史数据，还是只处理增量上报？

## Scope Boundaries
- INCLUDE: sing-box v2rayapi 流量使用量采集、可扩展 sink 设计、ES datastream 推送路径
- EXCLUDE: 暂未确认的账单/计费逻辑、前端展示、非流量类指标
