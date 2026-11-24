# Vertex / WorkBuddy 修复的 CR 后续事项

日期：2026-11-17。审查基线：官方 v0.2.20，首轮修复提交 cf1b58b47。

后续入口：由仓库维护者跟踪本文的 P2-ARCH-001 和 P2-ARCH-002；两项均是首轮修复引入的直接 Claude→Gemini 翻译行为，当前仍未满足 RelayKit 约束。

## 静态检查修复

CR 使用仓库 CI 对应的 golangci-lint 2.14.0，按 v0.2.20 过滤新增问题，发现19条 errcheck 类型断言检查失败：生产代码2条、测试17条。部分嵌套断言在同一行只报告一个位置，修复应逐层检查，不能只消除已显示的最外层诊断。

处置：生产转换器显式检查生成的 part 类型，异常时返回转换错误；测试逐层检查断言的 ok，并保留原有内容、数量、顺序、图片、签名和消息尾部断言。不禁用检查、不添加 nolint、不移除测试。

验证：仓库完整启用的 lint 配置以 v0.2.20 过滤新增问题，结果为0 issues；相关3个 Go 包回归通过；定向5组测试在 race 下通过；Linux/amd64 完整 embed 构建通过。这项静态问题不代表此前已观察到线上 panic。

## P2-ARCH-001：消息轮次修复尚未采用 RelayKit

- 状态：未修复，已记录。
- 位置：`backend/internal/service/gemini_messages_compat_service.go`，`convertClaudeMessagesToGeminiContents` 中对连续 functionResponse / 非 functionResponse 分组的逻辑。
- 依据：`docs/agent-resource-safety.md` 第13行要求新增翻译行为先经过 RelayKit；旧转换器的存储维护例外不覆盖该行为变更。
- 现状：当前 Go 源码和 `backend/go.mod` 没有 RelayKit 实现、依赖或适配入口。本次只修复已部署热修的静态检查，不引入未经验证的协议框架迁移。
- 解除条件：提供可用的 RelayKit 集成路径并迁移这一行为，验证合法工具调用、并行响应、图片、签名和对话尾部兼容性；或者由维护者调整该条架构约束，再按新规则复审。

## P2-ARCH-002：Schema 关键字清理尚未采用 RelayKit

- 状态：未修复，已记录。
- 位置：同一生产文件的 `cleanToolSchemaNode`，移除不被当前 Gemini parameters Schema 接受的 propertyNames 关键字。
- 依据：与 P2-ARCH-001 相同，这属于参数翻译行为的变更。
- 现状：与 P2-ARCH-001 共用同一缺失的框架集成前提。本轮保留已验证的局部兼容修复，并保留现行文档规则，不声称已经完成框架迁移或获得架构例外。
- 解除条件：在明确的框架集成或规则调整后复审；迁移时必须继续保留合法的 `properties["propertyNames"]` 参数名以及 required/default 普通数据。

上述两项是架构规范与现有实现路径的差异，当前 CR 未证实它们对应新增的运行时功能故障。记录未完成项不会自动豁免后续新增协议转换行为。

## v0.2.22 合并时的兼容验证

上游新增的 Antigravity 旧模型工具图片回归发现，共享转换器不能对所有入口统一拆分工具响应 Content。修复通过显式 `splitToolResultTurns` 参数选择行为：Gemini/Vertex 正常及签名降级重试路径启用拆分，Antigravity 保持原消息格式。上游回归及原 Vertex 回归的输入、期望均保留。

完整单测还在 Windows 上复现了上游原有 `TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort` 的时间构造问题：相邻 `time.Now()` 可能产生相同的冷却到期值，未真正构造出新 CAS 版本。未加本 fork 补丁的 v0.2.22 也失败。测试改为从既有到期值显式增加一秒，保留全部拒绝过期回调的断言；未改 Ollama 生产逻辑。

同类时钟精度问题也会使 `TestCommandCodeGatewayPassesThroughCatalogProtocols` 的多个场景复用缓存键。测试 URL 增加场景序号，保留原协议和负载断言；未改协议目录生产逻辑。两条测试分别重复10次通过；最终完整单测61个包、13,092个顶层测试（含子测试28,222项）通过，前端关键及变更集合38文件782项通过。

## v0.2.24 合并说明

上游 `44db3dd72` 已原生合入末尾工具响应轮次的拆分逻辑，因此本次同步移除 `splitToolResultTurns` 参数分叉与循环内拆分实现，P2-ARCH-001 对应的自定义生产差异已消除；当前生产代码相对 `upstream/main` 仅保留 `cleanToolSchemaNode` 移除 `propertyNames` 关键字（P2-ARCH-002）。
