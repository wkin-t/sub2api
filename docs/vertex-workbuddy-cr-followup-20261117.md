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
