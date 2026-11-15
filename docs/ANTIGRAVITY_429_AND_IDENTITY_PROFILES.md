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
| `SUB2API_ANTIGRAVITY_SESSION_ID_MODE` | `official` | `official`（官方常量 `-3750763034362895579`）/ `content`（旧行为：首条用户文本 sha256）/ `random` |
| `SUB2API_ANTIGRAVITY_UA_PROFILE` | `hub` | `hub`（官方 hub 格式）/ `agy-cli`（CLI 格式，`auth_method=consumer`）/ `legacy`（旧无元组格式） |
| `SUB2API_ANTIGRAVITY_UA_CL` | 按版本映射 | Piper changelist；默认按版本映射（1.5.9→1021519450、2.23.0→998057262、2.28.0→1008300195） |
| `ANTIGRAVITY_USER_AGENT_VERSION` | `2.23.0` | 官方客户端版本（也可用后台 `antigravity_user_agent_version` 覆盖）。默认保持上游验证过的 2.23.0；显式设 `2.28.0` 可对齐最新 hub，但会改变上游模型门控 |
| `SUB2API_ANTIGRAVITY_UA_OS` / `_ARCH` | `windows` / `amd64` | UA 元组中的平台标识；抓包样本是 darwin/arm64，部署默认 windows/amd64，可 env 覆盖 |
| `SUB2API_ANTIGRAVITY_HTTP2` | `1` | Antigravity 推理是否使用 HTTP/2（与官方 Go 客户端一致，属有意的默认行为变更）；`0` 回退 HTTP/1.1 |
| `SUB2API_ANTIGRAVITY_MODEL_ENUM_MAP` | 空 | 追加 `labels.model_enum` 映射（JSON：`{"模型":"MODEL_PLACEHOLDER_Mxxx"}`） |
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

## 4. Wire 身份对齐（sessionId / requestId / labels）

真实客户端的请求不是「一个 sessionId + systemInstruction」那么简单。对照真机抓包
（Antigravity-Manager `docs/JEIKL_ANTIGRAVITY_CLIENT_CAPTURE.md`：三份 Windows 原生客户端报文）
与 agy CLI 本机实测（`AGY_LLM_GATEWAY_URL` 指向本地明文服务，零计费抓到完整请求）：

- **sessionId 是常量**：三份样本与 277 个本地会话库、125,533 处取值全部是
  `-3750763034362895579` = `int64(FNV-1a 64(""))` = `int64(0xcbf29ce484222325)`。
  外部说法「空字符串 hash」成立，但不是 sha256；sub2api 原来的「首条用户消息 sha256」是错的。
- **requestId**：`agent/<agentId>/<step>`（step 为该 trajectory 的步数；真机样本 21/13/22）。
- **labels**（`request.labels`，真机三份都带，sub2api 原来完全没有）：
  `last_step_index`、`model_enum`（如 `MODEL_PLACEHOLDER_M298` / `M35`）、
  `request_id = <trajectory_id>-<n>`、`trajectory_id`、`used_claude`、
  `used_claude_conservative`、`used_non_gemini_model`；历史轮次还带 `last_execution_id`。
- **会话绑定**：`trajectory_id` 是独立 UUID；CLI 侧实测 `conversation_id == cascade_id == root_cascade_id`，
  `request_id = <trajectory_id>-<genIdx>`，另有 `client="antigravity-cli"` label
  （见 `recon/agy-wire-fingerprint.md`）。
- 真机 `request` 内部键序：`contents, systemInstruction, tools, labels, generationConfig, sessionId`
  （Claude 转换链路由结构体字段顺序保证；Gemini 原生链路经 map 重排，暂未完全对齐）。
- 真机样本**没有 `toolConfig`**，sub2api 恒发 `mode=VALIDATED`（上游历史验证为「不带会 400」），
  这里保留现状，属于已知差异。
- 控制面请求（`setUserSettings` / `fetchUserInfo`）已移除多余的 `X-Goog-Api-Client: gl-node/…`
  （Node 指纹，官方 Go 客户端不发）。

本分支实现（`backend/internal/pkg/antigravity/envelope.go` + `service/antigravity_wire_identity.go`）：

- sessionId 默认官方常量；`SUB2API_ANTIGRAVITY_SESSION_ID_MODE=content/random` 可回退/实验。
- agent/trajectory UUID 由 `WireIdentity{ConversationSeed, AccountID}` 派生：优先用客户端 session id，
  缺失时用 `sha256(首条用户文本 + systemInstruction)` 作为会话种子，再拼服务账号；
  同一会话稳定，账号切换时重新起 cascade（避免同一 cascade 出现在多个账号上、把号池关联成一个客户端）。
  已知残余：无 session header 且首条用户文本与 systemInstruction 都相同的两个会话仍会碰撞。
- CLI 形态（`SUB2API_ANTIGRAVITY_UA_PROFILE=agy-cli`）的 labels 额外带
  `client="antigravity-cli"` / `conversation_id` / `cascade_id` / `root_cascade_id`；
  默认 hub 形态按 Windows 原生客户端抓包保持 8 个 label，不臆造 cascade 字段。
- `step` / `request_id` 的计数由 contents 长度近似（无状态网关无法复现客户端侧真实计数器）；
  `last_execution_id` 暂缺（需要跨请求状态存储）。
- labels 中 `model_enum` 只对已抓包确认的模型映射（`gemini-3.7-flash-high`→`M298`、
  `gemini-4-hydrogen`→`M462`、`claude-sonnet-4-6`→`M35`），其余留空（上游接受缺省）；可用 env 追加。
- Gemini 原生链路统一走 `pkg/antigravity.WrapGeminiRequest`：补 sessionId/labels/requestId、
  保留未知字段；`request` 对象仍会按键名排序（JSON map 无法保序，属已知差异）。
- system 角色消息块（`messages[].role=="system"`）也会经过触发句清洗，不再只清洗顶层 `system` 字段。
- 客户端已带 Antigravity 身份时，先被改写为中性句、再注入本次配置的身份段（用户要求：只替换身份段）。
- 控制面请求（`setUserSettings` / `fetchUserInfo`）已移除多余的 `X-Goog-Api-Client: gl-node/…`
  （Node 指纹，官方 Go 客户端不发）。

## 5. UA 与 cl

官方 UA 实测（`recon/agy-wire-fingerprint.md`）：

- CLI：`antigravity/cli/1.5.9 (aidev_client; os_type=windows; arch=amd64; cl=1021519450; auth_method=consumer)`
- Hub：`antigravity/hub/2.23.0 (aidev_client; os_type=darwin; arch=arm64; cl=998057262)`、
  `antigravity/hub/2.28.0 (…; cl=1008300195)`（Antigravity-Manager 校准值）

sub2api 原来发的是旧格式 `antigravity/hub/2.23.0 windows/amd64`（没有 `aidev_client` 元组和 `cl`）。
本分支默认改为 **hub 2.23.0 + cl=998057262**（保留上游验证过的版本，避免默认改变模型门控），
版本与 cl 按映射表配对（`1.5.9`→`1021519450`、`2.23.0`→`998057262`、`2.28.0`→`1008300195`）；
未知版本回退到默认版本的 cl，保证永远是已观测到的一对。
要更贴近最新官方客户端可显式设置 `ANTIGRAVITY_USER_AGENT_VERSION=2.28.0`
（文档 `recon/agy-wire-fingerprint.md` 的样本是 windows/amd64，Antigravity-Manager 记录的 hub 样本是 darwin/arm64，
平台元组可用 `SUB2API_ANTIGRAVITY_UA_OS/_ARCH` 覆盖）。
`SUB2API_ANTIGRAVITY_UA_PROFILE=agy-cli` 可切成 CLI 格式（`auth_method=consumer`）。
**注意**：上游按 UA 家族 + 版本做模型门控（sub2api #8062：裸 UA 26 个模型、hub/2.23.0 29 个模型），
改版本会改变可用模型集合，升级前先确认。

## 6. HTTP/2 与 TLS 指纹

- 官方 agy 的 `ClientHello` 是**纯 Go stdlib 默认指纹**（无 GREASE、ALPN `h2+http/1.1`、TLS 1.3）：
  JA3 `03117a8ed39ef02427ebbc39f121275c`、JA4 `t13d1311hh_f57a46bbacb6_f43d0ea13f66`
  （本机 agy 1.5.9 抓取；agy 用 `go1.28-20261102-RC00` 构建）。
- sub2api 的 Antigravity 推理请求原来走 **HTTP/1.1**：`buildUpstreamTransport` 设置了自定义
  `DialContext`，Go 不会自动启用 HTTP/2，因此 ALPN 只报 `http/1.1`，与官方（h2）不一致。
  本分支默认对 Antigravity 请求启用 HTTP/2（复用 `HTTPUpstreamProfileLongStream` 传输，
  带 h2 PING keepalive），可用 `SUB2API_ANTIGRAVITY_HTTP2=0` 回退。
- **不要**给 Antigravity 套现有的 utls 指纹模板：内置默认 profile 是 Node.js 24.x（Claude Code）
  的 JA3/JA4，套在 Go 客户端上反而更不像官方；而且 utls 传输固定 HTTP/1.1，会和 h2 的 ALPN 冲突。
  当前 Antigravity 链路没有接入 `DoWithTLS`（账号 `enable_tls_fingerprint` 也只对 Anthropic 生效）。
- 若未来要做到 JA3/JA4 字节级一致：agy 用 `go1.28-20261102-RC00` 构建，而本仓库 `backend/go.mod`
  要求 **Go 1.27.3**（CI 断言 1.27.x；`go version` 显示的本地工具链版本可能不同，因为 Go 会按 go.mod
  自动切换 toolchain）。需要把构建工具链对齐到 Go 1.28，或用 utls 定制 ClientHello 并同时实现 h2。
  **优先级低于 wire 字段与 UA/cl**。



## 7. 新增/修改的代码位置

- `backend/internal/pkg/antigravity/upstream_trigger_filter.go` — 触发句清洗（默认模式 + env 扩展）
- `backend/internal/pkg/antigravity/request_transformer.go` — `NeutralizeSystemIdentity` 接入清洗（含 system 角色消息块）；opener 增加 Pi / Antigravity；生成 wire 身份
- `backend/internal/pkg/antigravity/identity_profiles.go` + `prompts/` — 身份 profile（`go:embed` 两个官方身份变体）
- `backend/internal/pkg/antigravity/envelope.go` — wire 身份：`WireIdentity`、官方 sessionId 常量、agent/trajectory UUID 派生、labels、requestId、model_enum、`WrapGeminiRequest`
- `backend/internal/pkg/antigravity/oauth.go` — 官方 UA 家族类型（hub/agy-cli/legacy）+ 版本/CL/平台元组
- `backend/internal/pkg/antigravity/gemini_types.go` — 请求结构体字段顺序 + `labels` 字段
- `backend/internal/service/antigravity_wire_identity.go` — 会话种子/账号作用域、HTTP/2 传输标记
- `backend/internal/service/antigravity_gateway_wire.go` — `wrapV1InternalRequestWithContext`（Gemini 链路 wire 身份）
- `backend/internal/service/antigravity_gateway_service.go` — `injectIdentityPatchToGeminiRequest`（自定义提示词、幂等、清洗后空 part 物理移除）、连接测试请求携带 wire 身份
- `backend/internal/service/antigravity_gateway_gemini.go` / `antigravity_gateway_compat.go` — 两条 Gemini 链路使用后台配置的提示词 + wire 身份
- `backend/internal/service/antigravity_gateway_claude.go` — 签名重试路径也强制注入身份（与主路径一致）
- `backend/internal/service/antigravity_gateway_retry.go` / `antigravity_credits_overages.go` — 推理请求携带 HTTP/2 传输标记
- `backend/internal/repository/http_upstream.go` — TLS 指纹 client 缓存键并入 profile 指纹（改绑 profile 立即生效、避免跨账号串用）

## 8. 验证与后续

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
go test -tags=unit ./...        # 合并前跑全量 unit
go build ./...
```

wire 身份可用本地网关对照验证：把 agy 指向本地明文服务（`AGY_LLM_GATEWAY_URL=http://127.0.0.1:<port>`，
零计费）抓一份真实请求，再与 sub2api 出站 body 逐字段 diff；
抓包与复现脚本见工作区 `recon/agy-wire-fingerprint.md` 与 `recon/agy-prompt/_evidence/wire-capture/`。

已知未对齐（按影响排序）：

- `<user_information>` 的 `Conversation ID`：真机会话身份的一部分，sub2api 目前只替换 identity 段。
- `last_execution_id`：真机历史轮次带上一轮 response id，sub2api 无跨请求状态，暂缺。
- `labels.last_step_index / request_id` 计数：由 contents 长度近似，非客户端真实计数器。
- hub 形态没有 `cascade_id` / `root_cascade_id` / `client` labels：这几项只在 CLI 形态
  （`SUB2API_ANTIGRAVITY_UA_PROFILE=agy-cli`）按 agy 抓包补上；hub 抓包样本本身没有这些字段。
- 无 session header 时，若首条用户文本与 systemInstruction 完全相同，不同会话仍可能撞同一身份
  （无状态网关的固有限制；真实客户端用本地会话状态保证唯一）。
- Gemini 原生链路的 `request` 内部键序经 map 重排，与真机顺序不同。
- `toolConfig`：真机样本没有，sub2api 恒发 `mode=VALIDATED`（上游历史验证为必需，保留）。
- TLS：官方是 Go 1.28 默认指纹；本仓库按 go.mod 用 Go 1.27.3 构建，ClientHello 可能因版本差异
  略有不同；utls 定制 + h2 一致是后续工作。

后续可选项（未实现）：

- 上游 PR #8836：裸 429 只冷却失败模型，避免整池 503 级联；上游合并后可直接 rebase。
- 把 strip 列表迁移到后台设置（需要迁移 + 前端输入框），便于不重启调整。
- 删除自造的 model identity / silent-boundary / `[SYSTEM_PROMPT_END]` 段（官方客户端没有这些文本，
  目前无证据表明会触发 429，但属于可识别指纹）。
- 给 Antigravity 增加 `DoWithTLS` 接入（需要先用 Go 1.28 构建/utls 抓取官方 ClientHello 并支持 h2，否则默认 Node profile 反而更不像）。

## 9. 风险提示

- 消费端 OAuth 令牌调用 `cloudcode-pa` 私有端点本身违反 Google 服务条款，已有封号公开报告；
  本分支只解决「内容误触发」的工程问题，不改变这一事实。
- 黑名单是滚动更新的；新增触发句时通过 `SUB2API_ANTIGRAVITY_STRIP_PATTERNS` 追加，
  但 env 在进程启动时读取一次，**改完需要重启**（不是热配置）。
- 任何指纹伪装都是军备竞赛，不能承诺长期有效。

## 10. 参考链接

- sub2api #8824 / PR #8829（Dual-use 句 + 可配置 strip 方向）：<https://github.com/Wei-Shaw/sub2api/issues/8824>
- sub2api #8791 / PR #8793（Pi 身份句）：<https://github.com/Wei-Shaw/sub2api/issues/8791>
- sub2api #7410 / #7307（Claude Agent SDK / Codex 身份句）
- CLIProxyAPI #4723（Hermes 身份句）、#6619（官方 UA 抓包）：<https://github.com/router-for-me/CLIProxyAPI/issues/4723>
- 9router #3358、OmniRoute #9030（同类复现）
- Antigravity-Manager `prompt_sanitizer.rs`（当前最完整的清洗实现）
- 官方 Antigravity CLI：<https://github.com/google-antigravity/antigravity-cli>
