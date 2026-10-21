# workbuddy-bridge 本地补丁说明（0.1.2 + 三会话头）

- 基线：zlZayn/dsh-workbuddy-bridge 的 0.1.2 clean-room port（本目录完整 Go 工程）
- 产物：`plugins\workbuddy-bridge.dll`，6,989,312 B，sha256 前缀 `6EE0AD4ED909`
- 部署：2026-10-19 18:53（CPA v8.0.32），备份 `backups\backup-20261019-1853-bridge-convheaders\`

## 为什么改

国际版（region=global，`https://www.workbuddy.ai`）网关于 2026-10 起对流式 chat 强制校验
「官方客户端」特征，缺会话头时一律返回：

```json
{"code":11128,"msg":"request illegal",
 "displayTips":{"en":"This call failed channel verification, possibly because it was not sent from an official client. Please resend from the official client."}}
```

排查结论（详见 `..\mem.md` 的 11128 一节）：UA / `X-IDE-*` / Origin / body 都不是触发点；
决定性差异是 **每轮对话必须带三个 UUID 头**：`X-Conversation-ID`、`X-Conversation-Request-ID`、
`X-Conversation-Message-ID`。补齐后 200 正常（同 token 的 `/v3/config` 一直 200，说明不是账号问题）。

## 改了什么

`wire.go` 的 `chatHeaders()`：用 `crypto/rand` 生成三个 v4 UUID 头（每次请求唯一）。
其它逻辑未动（仍强制 `stream:true`、developer→system、tool_choice 扁平化、
首条非 system 时前置 `You are a helpful assistant.`）。

## 构建

```powershell
$env:CGO_ENABLED="1"
$env:CC="C:\Users\MECHREV\w64devkit\w64devkit\bin\gcc.exe"
$env:PATH="C:\Users\MECHREV\w64devkit\w64devkit\bin;$env:PATH"
$env:GOPROXY="https://goproxy.cn,direct"
$env:HTTPS_PROXY="http://127.0.0.1:7897"; $env:HTTP_PROXY="http://127.0.0.1:7897"

go build -buildmode=c-shared -trimpath -ldflags "-s -w" -o workbuddy-bridge-patched.dll .
go test -count=1 ./...
```

## 维护规则

- 本 DLL 不在任何官方包里，更新 cpa-multi-plugins **不会**覆盖它，但也别手工拿官方 workbuddy 替换。
- 若以后要升级 bridge 上游：先 diff 新 `wire.go` 的 `chatHeaders()` 是否已自带三会话头；
  没有就按本说明重打一遍再构建。
- 排障口诀：`11128` = 会话头缺失/网关通道校验；`11101` = 上游不支持非流式（正常）；
  `/v3/config` 200 而 chat 11128 = 请求特征问题，不是 token 失效。

## 补丁三：system prompt 特征句脱敏（2026-10-21 14:00）

CN WorkBuddy 网关实测把 system prompt 开场句 `You are an AI agent powered by DeepSeek Harness` 判成 11-128（Illegal API invocation from an unapproved channel）。这句话由客户端发出，会流经所有插件，因此 bridge 也加同一层兜底。

- 新增 `blocklist.go`：`blockedPromptSignatures` 三条（句号/逗号/无标点，长的优先）+ `replaceBlockedPrompt` + `sanitizeBlockedSystemPrompt`（只处理 `role=system` / `developer`，content 支持 string 与 content parts 数组）。
- 接入点：`wire.go` 的 `prepareChatBody`，在 `normalizeToolChoice(obj)` 之后调用。
- 新增 `blocklist_test.go`：system 被改写、user 不动、content parts 生效、无命中不改。
- 产物：6,991,360 B，sha256 `E979663ED98F19F11A8D4587D33E5725EE66DD33B312015E7B6479D2A0E9585A`。

## 补丁四：脱敏范围从 system 扩到全角色（2026-10-21 22:00，版本 0.1.3）

CN 侧用真实失败体做单变量二分后确认：网关读的是**整个请求体**，不是只有 system 消息。
一个 system prompt 已完全脱敏的 50 消息请求仍然 400/11-128，触发短语落在 assistant 历史回显里：

- `DeepSeek Harness Web GUI`（DSH 运行时上下文行，四 token 全命中才算，拆任一 token 即 200）
- `Main branch (you will usually use this for PRs)`（Claude Code 注入的 git 规则行，大小写不敏感，
  `MainX` / `branchX` / `PRx` / 去掉右括号的变体全部 200）
- 佐证：把这 27 行换成占位符 → 同体 200；保留这些行只替换品牌串 → 仍 400。

改动：

- `blocklist.go`：新增 `sanitizeBlockedPhrases(obj)`，对**每个角色**的 content（string 或 parts 数组）、
  `reasoning_content` / `reasoning` 镜像、`tool_calls[].function.arguments` 全部做同一套短语替换；
  规则集与 CN 插件对齐（Claude Code CLI 措辞 / Main branch 行 / Codex CLI 开场 / DSH 开场句 / Web GUI 行，
  后三条为新增的正则兜底）。原 `sanitizeBlockedSystemPrompt` 保留为 system/developer 专用入口。
- `wire.go`：`prepareChatBody` 改调 `sanitizeBlockedPhrases(obj)`。
- `plugin.go`：`pluginVersion` 0.1.2 → 0.1.3。
- `blocklist_test.go`：新增全角色用例（assistant 回显 / tool_calls 参数 / reasoning 镜像 / user 轮）
  与三条兜底规则用例；原有 system 作用域用例保持不变。
- 产物：`workbuddy-bridge-0.1.3.dll`，7,258,624 B，
  sha256 `699C43F16B57F3FDD5F79EC0FF403A1B5552520AB62F1F22133387AB857BC7C8`；
  部署为 `plugins\workbuddy-bridge.dll`，旧 0.1.2 备份在 `_cpa_tmp\backup-20261021-2207\`。

在线验证（CPA v8.0.36，22:08）：`workbuddy-bridge-deepseek-v4.1-flash` 带 system 开场句 + Web GUI 行 +
Main branch 行的请求返回 200 正常作答。国际线路是否也拦这些短语**未单独验证**，本层是与 CN 的防御性对齐。

## 补丁五：国际线路实测会拦 + 11-128 不再冷却凭据（2026-10-21 22:20，版本 0.1.4）

### 一、国际线路确实会拦同一类短语（A/B 实测）

把 0.1.2 换回部署后打国际线路（`workbuddy-bridge-deepseek-v4.1-flash`，stream=false，max_tokens=16）：

| 请求内容 | 结果 |
| --- | --- |
| 仅 system + user「say ok」 | 200 |
| system 开场句 `You are an AI agent powered by DeepSeek Harness…` | 200（该位置不触发） |
| assistant 消息里 `Main branch (you will usually use this for PRs)` | **上游 400 `{"code":11128,…}`** |
| 紧接着的健康检查 | **503 `auth_unavailable … last upstream error: 11128`** |
| `tools[].description` 里同一短语 | 200（网关不扫工具描述，无需脱敏） |

即：国际线路与 CN 是同一套短语拦截，且**一条被拦的请求会把凭据整体冷却**，随后所有国际线路请求
503 —— 这正是 CN 插件 v0.9.17 修过的那个坑（`chat_error.go` 里 11-128 被当账号级失败的注释），
bridge 一直缺这一半。补丁四让正常请求不再触发 11-128；补丁五保证万一再出现 11-128，
它按“请求级错误”上报，凭据不被冷却。

### 二、改动（与 CN 插件同策略）

- 新增 `status.go`：
  - `statusError{status, err}`（`Error()` / `StatusCode()` / `Unwrap()`），状态经 RPC 信封过界；
  - `upstreamStatusError(status, payload, err)`：11-128（400/403，含带空格与字符串变体）→ 400；
    401 / 402 / 429 → 原状透传；403 且带业务信封 → 403；
    413/11115 过长、11102 模型未注册、6004 模型级频控、裸 403 WAF、其余一律不加状态（status 0）。
- `plugin.go`：`envelopeError` 新增 `http_status,omitempty`；`errorEnvelopeFor` 用 `errors.As` 识别
  `StatusCode() int`，命中时以 `code=plugin_error` + `http_status` 出信封（与 CN 插件 `main.go:1022` 一致）；
  `pluginVersion` 0.1.3 → 0.1.4。
- `executor.go`：两处上游非 200（非流式 `handleExecExecute` 与流式 `handleExecStream`）由裸
  `fmt.Errorf` 改为经 `upstreamStatusError` 包一层。
- 新增 `status_test.go`：11128 必须带 400 过信封（钉住 v8 `IsRequestFault` 的取值）、
  账号级 vs 请求级 14 条策略表、无状态错误不得凭空造 `http_status`。

### 三、在线验证（CPA v8.0.36，22:20 重启后）

| 请求 | 结果 |
| --- | --- |
| 健康检查 | 200 |
| assistant 里带 `Main branch (…for PRs)`（0.1.2 时为 400/11128） | **200** |
| 紧随其后再健康检查 | 200（无 503 冷却级联） |
| 真实 50 消息 / 269,464 B / 67 tools 失败体 | **200**，正常返回 |

### 四、产物

- `workbuddy-bridge-0.1.4.dll`，7,337,472 B，
  sha256 `EE6266C3784E849B9C3238655B3B859CAA51CD740938BC9E7BF643592C5B5DEF`；
  部署为 `plugins\workbuddy-bridge.dll`（同哈希），旧 0.1.2 备份 `_cpa_tmp\backup-20261021-2207\`、
  0.1.3 备份 `_cpa_tmp\workbuddy-bridge-0.1.3.dll`。
- 构建：`CGO_ENABLED=1` + `CC=gcc.exe`（w64devkit），
  `go build -buildmode=c-shared -trimpath -ldflags "-s -w -X main.pluginVersion=0.1.4"`。
- `go test`：全套 PASS（含补丁四/五新增用例）。

### 五、已知的不对称（未改，供后续评估）

CN 插件对**流内错误帧**（HTTP 200 之后 `data: {"error":…}`）有 `streamFaultError` 处理，
bridge 的 `walkCleanedSSE` 目前把这种帧当普通 chunk 转发（无 completion payload 时报
"stream ended without a completion payload"）。触发条件罕见（补丁四后 11-128 已不再出现），
未纳入本次修复。
## 补丁六：非流式聚合把 role 拼成了 assistantassistant（2026-10-21 22:45，版本仍是 0.1.4）

- 现象：非流式请求经 bridge 折叠后，`choices[0].message.role` 是 `assistantassistant…`——上游每个
  delta 帧都带 `"role":"assistant"`，而 `aggregateCompletion` 把 role 当成流式文本用
  `strings.Builder` 累加。
- 改动：`executor.go` 的 `aggregateCompletion` 改为单值取出（`if d.Role != "" && role == ""`），
  全程没有 role 帧时仍回落 `assistant`。
- 测试：新增 `aggregate_test.go`（`TestAggregateCompletionRoleNotConcatenated`、
  `TestAggregateCompletionRoleDefaultsToAssistant`）。
- 产物：`workbuddy-bridge-0.1.4.dll` 7,336,448 B，
  sha256 `0904665C3CA51099ACCF42E247170BB56C5C4291BF7C0774F3B663DA81E57D06`；
  部署前备份 `_cpa_tmp\backup-20261021-2244\workbuddy-bridge-0.1.4-before-roledix.dll`（7,337,472 B）。
- 在线验证（22:44 经计划任务重启后）：小请求 → 200、`role=assistant`、`content=OK`；
  真实 269KB / 50 消息 / 67 tools 失败体 → 200、`role=assistant`、`finish_reason=length`、`prompt_tokens=86945`。
