# Antigravity 上游 429：真实成因与身份提示词替换（fork 说明）

> 本文对应 `feat/antigravity-agy-prompt` 分支。日期：2026-11-15。
> 结论基于公开受控实验与对客户端本地产物的逆向，检测规则会持续变化，请以实测为准。

## 0. 结论先行

1. **429 不是「Google 检测到第三方反代」的正向校验，而是 systemInstruction 的句子级内容黑名单**。
   命中后上游返回裸 `429 RESOURCE_EXHAUSTED`（没有 `quotaResetDelay` / `RetryInfo`，账号其实空闲）。
   同一 key、同一模型、同一端点，**只改 system 文本**即可从稳定 429 变成 200。
2. 因此「把整段系统提示词换成 agy CLI 的」**不是解决 429 的正确杠杆**。
   黑名单句（Hermes 身份句、Claude Code 的 `Dual-use security tools ...` 等）不管你换不换身份补丁都会命中；
   正确做法是**删除/改写命中句**（本分支已内置，可用环境变量扩展）。
3. **身份提示词变体是另一层问题**。sub2api 原有 `antigravityIdentity` 是更老版本的近似文本，
   与当前官方 Antigravity CLI（agy 1.5.9，2026-11）实际发送的身份段不一致：
   多了「attach additional metadata…」句、缺 `User requests are enclosed within <USER_REQUEST> tags.`、
   把内容完全不同的 `<communication_style>` 拼进了 identity、且没有后方约 17.7 KB 的 10 个系统段。
   本分支用 **identity profile** 精确对齐官方客户端的身份段，默认使用 agy CLI 当前变体。
4. 已实现（均可用环境变量控制，默认不改变部署方式之外的行为）：
   - 可配置的 429 触发句清洗（默认含 Hermes 身份句 + Claude Code `Dual-use` 句）；
   - 身份 opener 清洗新增 Pi / Hermes，统一改写为 `You are an AI agent.`；
   - identity profile：`agy-cli`（默认）/ `agy-cli-coding` / `legacy`；支持文件覆盖（可放完整 20 KB 静态提示词）；
   - `identity_patch_prompt` 后台设置现在**作用于所有转发路径**（此前只对 Claude 转换路径生效）；
   - 可选完整 User-Agent 覆盖（`antigravity/cli/<ver> (aidev_client; ...)` 这种官方格式）。

## 1. 429 的成因（证据）

社区多方独立受控实验复现同一现象：上游按 `systemInstruction` 中的**句子/片段内容**拒绝请求，
并伪装成配额耗尽。删除或改写该句即恢复 200；只改 UA、代理、版本、tools、请求体大小均无效；
零宽字符混淆也无效。

| 触发句（滚动更新） | 出现时间 | 来源 |
| --- | --- | --- |
| `You are Hermes Agent, an intelligent AI assistant created by Nous Research.` | 2026-08 | CLIProxyAPI #4723、9router #3358 |
| `You are a Claude agent, built on Anthropic's Claude Agent SDK.` | 2026-09 | sub2api #7410 / PR #7411 |
| `You are Codex, a coding agent based on GPT-5.` | 2026-09 | sub2api #7307 |
| `You are a coding assistant running inside pi, a coding agent harness.` | 2026-11-14 | sub2api #8791 / PR #8793 |
| `Dual-use security tools (...) require clear authorization context: ...` | 2026-11-15 | sub2api #8824 / PR #8829 |

上游 sub2api 当前只中和了 4 类 opener（Claude Agent SDK / Claude Code / Codex / GPT-in-Codex），
`Dual-use` 句与 Pi 句对应的 PR **尚未合并**；本分支把两类都补上，并把清洗扩展成可配置正则。

## 2. 官方 agy CLI 的系统提示词（本地产物逆向）

- agy 版本：**1.5.9**；提示词由**客户端本地组装**（`agy.exe` 内嵌 Go `text/template`），
  渲染结果完整落在本地 SQLite：`~/.gemini/antigravity-cli/conversations/<uuid>.db`
  → `gen_metadata.data`（protobuf field 1，长度前缀）→ 完整 `systemInstruction` 明文。
- 结构：15 个 section 按固定顺序拼接、单字节 LF 分隔：
  `identity → artifacts → conversation_transcript → guidelines → communication_style → slash_commands
  → messaging → plan_execute_protocol → mcp_servers → subagents → skills → user_rules → user_information`；
  静态骨架约 20,007 B；动态段（skills/user_rules/user_information）含个人环境信息，**不进入本仓库**。
- 身份段有服务端实验开关 `generic-system-prompt` 控制的两套文案：
  - generic（当前默认，最新会话）：`agentic AI assistant` + `You are working with a USER to solve their task.`
  - coding（历史会话）：`agentic AI coding assistant … Advanced Agentic Coding.` + `pair programming`
  - 两套都以 `User requests are enclosed within <USER_REQUEST> tags.` 结尾。
- 模型身份**不在**提示词里（`You are Model X. ModelId is Y.` 在客户端中 0 命中）；
  sub2api 额外注入的 model identity 文本属于自造内容（见 §5 的后续建议）。

## 3. 本分支的配置项

| 环境变量 / 设置 | 默认 | 作用 |
| --- | --- | --- |
| `SUB2API_ANTIGRAVITY_STRIP_PATTERNS` | 空（叠加内置 2 条） | 追加 429 触发句清洗正则，每行一个 Go regexp；非法行会跳过并记日志 |
| `SUB2API_ANTIGRAVITY_IDENTITY_PROFILE` | `agy-cli` | `agy-cli` / `agy-cli-generic`（同前）/ `agy-cli-coding` / `legacy`（上游旧文本，等价 `upstream`） |
| `SUB2API_ANTIGRAVITY_IDENTITY_PROMPT_FILE` | 空 | 从文件读取完整身份提示词（≤ 256 KB），优先级高于 profile；可指向抓包得到的完整静态提示词 |
| `SUB2API_ANTIGRAVITY_USER_AGENT` | 空 | 完整 User-Agent 覆盖（≤ 512 字节、禁止换行）。用于对齐官方客户端 UA；注意 UA 影响模型门控/配额归属 |
| 后台设置 `enable_identity_patch` | `true` | 是否注入身份补丁（Antigravity 转发路径强制开启） |
| 后台设置 `identity_patch_prompt` | 空 | 非空时覆盖 profile，作为身份提示词正文 |

优先级：`identity_patch_prompt`（DB） > `..._PROMPT_FILE` > `..._PROFILE` > 内置默认。

### 3.1 切换成 agy CLI 身份（默认已生效）

```bash
# 默认就是 agy-cli，无需配置；要回退到 sub2api 旧文本：
SUB2API_ANTIGRAVITY_IDENTITY_PROFILE=legacy
# 使用历史 coding 变体（214 个历史会话在用的版本）：
SUB2API_ANTIGRAVITY_IDENTITY_PROFILE=agy-cli-coding
```

### 3.2 使用完整 20 KB 静态提示词（实验性）

完整提示词由 `recon/agy-prompt/system_prompt_static.txt` 提供（identity→subagents，20,007 B，
不含个人 skills/rules/user_information）。挂载到容器后：

```bash
SUB2API_ANTIGRAVITY_IDENTITY_PROMPT_FILE=/data/agy-cli-static.txt
```

**代价与风险**：每个请求多约 5k tokens；提示词描述的是 agy CLI 专属能力（artifacts / subagents /
skills / MCP），而 sub2api 的边界文本会把它声明为「可忽略的上文」，因此对模型行为基本无影响，
主要是外观层面的对齐。**不建议默认开启。**

### 3.3 通过后台设置替换（无需改 env）

`settings` 表是简单 KV，读取无缓存，直接 UPDATE 立即生效（Postgres 示例）：

```sql
INSERT INTO settings (key, value, updated_at)
VALUES ('identity_patch_prompt', '<你的提示词全文>', now())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
```

注意：不要用 `PUT /api/v1/admin/settings` 只提交部分字段——该接口的多数字段是非指针类型，
缺省字段会被写成零值（例如 `enable_identity_patch` 会被关掉）。如走 API，请先 GET 完整配置再改后整体 PUT。

## 4. 新增/修改的代码位置

- `backend/internal/pkg/antigravity/upstream_trigger_filter.go` — 触发句清洗（默认模式 + env 扩展）
- `backend/internal/pkg/antigravity/request_transformer.go` — `NeutralizeSystemIdentity` 接入清洗；opener 增加 Pi / Hermes
- `backend/internal/pkg/antigravity/identity_profiles.go` + `prompts/` — 身份 profile（`go:embed` 两个官方身份变体）
- `backend/internal/pkg/antigravity/oauth.go` — 完整 UA 覆盖
- `backend/internal/service/antigravity_gateway_service.go` — `injectIdentityPatchToGeminiRequest` 支持自定义提示词、清洗后空 part 物理移除
- `backend/internal/service/antigravity_gateway_gemini.go` / `antigravity_gateway_compat.go` — 两条 Gemini 链路使用后台配置的提示词
- `backend/internal/service/antigravity_gateway_claude.go` — 签名重试路径也强制注入身份（与主路径一致）

## 5. 验证与后续

受控实验（照抄社区已跑通范式）：

1. 单账号、单 key、单端点、单模型；每次失败后等 ≥ 65s 确保账号无冷却再发下一次。
2. 一次只改一个变量：只改 `systemInstruction`；对照组 = 完整原请求（预期 429），实验组 = 删除/改写命中句（预期 200）。
3. 记录响应是否带 `quotaResetDelay` / `RetryInfo`，区分真限流与伪 429。
4. 清洗必须幂等（多次经过不改变文本，避免破坏 prompt cache），且不得误改用户正文。
5. `/v1/messages`（Claude 兼容）与 `/v1beta/...:streamGenerateContent`（Gemini 原生）两条链路都要验证。

本地测试：

```bash
cd backend
go test -tags=unit ./internal/pkg/antigravity/
go test -tags=unit -run 'Antigravity|IdentityPatch|Attribution' ./internal/service/
go build ./...
```

后续可选项（未实现）：

- 上游 PR #8836：裸 429 只冷却失败模型，避免整池 503 级联；上游合并后可直接 rebase。
- 把 strip 列表迁移到后台设置（需要迁移 + 前端输入框），便于不重启调整。
- 删除自造的 model identity / silent-boundary / `[SYSTEM_PROMPT_END]` 段（官方客户端没有这些文本，
  目前无证据表明会触发 429，但属于可识别指纹）。

## 6. 风险提示

- 消费端 OAuth 令牌调用 `cloudcode-pa` 私有端点本身违反 Google 服务条款，已有封号公开报告；
  本分支只解决「内容误触发」的工程问题，不改变这一事实。
- 黑名单是滚动更新的；新增触发句时通过 `SUB2API_ANTIGRAVITY_STRIP_PATTERNS` 热配置，无需改代码。
- 任何指纹伪装都是军备竞赛，不能承诺长期有效。

## 7. 参考链接

- sub2api #8824 / PR #8829（Dual-use 句 + 可配置 strip 方向）：<https://github.com/Wei-Shaw/sub2api/issues/8824>
- sub2api #8791 / PR #8793（Pi 身份句）：<https://github.com/Wei-Shaw/sub2api/issues/8791>
- sub2api #7410 / #7307（Claude Agent SDK / Codex 身份句）
- CLIProxyAPI #4723（Hermes 身份句）、#6619（官方 UA 抓包）：<https://github.com/router-for-me/CLIProxyAPI/issues/4723>
- 9router #3358、OmniRoute #9030（同类复现）
- Antigravity-Manager `prompt_sanitizer.rs`（当前最完整的清洗实现）
- 官方 Antigravity CLI：<https://github.com/google-antigravity/antigravity-cli>
