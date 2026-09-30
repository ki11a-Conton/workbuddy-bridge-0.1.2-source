# workbuddy-bridge

A [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin that serves the **international** WorkBuddy product (`region=global` / `https://www.workbuddy.ai`, the "WorkBuddy AI" desktop identity) as a native Go provider — a clean-room port of the wire from [zlZayn/dsh-workbuddy-bridge](https://github.com/zlZayn/dsh-workbuddy-bridge) (MIT).

与 cpa-multi-plugins 的 `workbuddy` 插件完全独立并存：插件名、provider id、auth 文件前缀都不冲突，适合做 A/B 对比。

## 功能

- **Chat 流式反代** — 上游 `/v2/chat/completions` 返回 OpenAI 形状的 SSE，插件逐帧清洗后转发（不翻译协议）：
  - 强制 `stream: true`（上游只收流式）
  - `role: developer` → `role: system`
  - `tool_choice` 扁平化为上游的字符串形式（对象 `function` → 函数名，`none` → 删除并抑制工具列表）
  - 丢弃 `reasoning_effort: "off"`（全局网关在 GPT 系上拒绝该字面量）
  - 首条消息不是 system 时自动前置一条最小 system 消息（否则 400/11128）
- **模型目录** — `GET /v3/config`（app 形 User-Agent，网关按客户端身份拆分 roster），与 `cli` agent 的模型清单取交集后映射为 CPA 模型；尊重宿主的 `oauth.excluded-models` 排除表
- **积分/账单** — `/v2/billing/meter/get-user-resource`，在管理面板里按账号显示
- **管理面板** — CPA 管理界面侧边栏 "WorkBuddy Bridge"：账号（昵称/UID/域名/积分/状态）+ 手动刷新
- **Token 刷新** — `POST /v2/plugin/auth/token/refresh`（`X-Refresh-Token` 头，绝不在 chat 请求里带 refresh token）

## 安装

1. 把 `workbuddy-bridge.dll` 放进 CLIProxyAPI 的 `plugins/` 目录
2. `config.yaml`：

```yaml
plugins:
    enabled: true
    configs:
        workbuddy-bridge:
            enabled: true
```

3. 准备账号文件。插件认领文件名前缀为 `workbuddy-bridge-` 的凭据（例如 `workbuddy-bridge-<uuid>.json`），内容为国际版 WorkBuddy 凭据：

```json
{
  "provider": "workbuddy-bridge",
  "type": "workbuddy-bridge",
  "account": { "uid": "...", "enterpriseId": "", "nickname": "..." },
  "auth": {
    "accessToken": "...",
    "refreshToken": "...",
    "expiresAt": 1820000000000,
    "domain": "www.workbuddy.ai",
    "region": "global",
    "loginPlatform": "CLI"
  }
}
```

只认领 `domain` 为 `workbuddy.ai` 的凭据；CN 域名的文件不会被本插件认领。

## 模型暴露

目录默认暴露上游 `cli` roster 的全部模型。要只保留免费模型（例如 `deepseek-v4.1-flash`），用 CPA 的排除表：

```yaml
oauth:
    excluded-models:
        workbuddy-bridge:
            - default-model
            - fast-model
            # ... 其余所有不想暴露的模型 id
```

客户端调用的模型名为 `<provider>/deepseek-v4.1-flash`（如 `workbuddy-bridge/deepseek-v4.1-flash`）；插件会在出站前剥掉 provider 前缀。

## 构建

官方 flags（Windows，需要 mingw-w64 gcc + Go ≥ 1.26）：

```
go build -buildmode=c-shared -trimpath -ldflags="-s -w" -o workbuddy-bridge.dll .
```

依赖：`github.com/router-for-me/CLIProxyAPI/v7 v7.2.30`（SDK，仅用到 `sdk/pluginabi` / `sdk/pluginapi`）。

测试：`go test ./...`（夹具 `testdata/intl-config.json` 为脱敏后的真实 `/v3/config` 文档结构）。

## 作者

**killa-conton** · **zlZayn** · **Corrine Hu**

## 来源声明

- 上游 wire（端点、身份头、App 版本链、intl body 处理规则、目录/账单形状）移植自 [zlZayn/dsh-workbuddy-bridge](https://github.com/zlZayn/dsh-workbuddy-bridge)（MIT, Corrine Hu & zlZayn），经本地 spike 对真实上游验证。
- 插件 ABI 骨架参照 cpa-multi-plugins 各插件的公开实现。
- 本插件由 **killa-conton** 实现与发布。

## 免责声明

本项目仅供个人学习和研究使用，仅驱动使用者自己的 WorkBuddy 账号在本机调用，请勿用于商业用途或超出个人合理使用的场景。

使用者需遵守 WorkBuddy 的服务条款；因使用本项目产生的任何后果（包括但不限于账号被限制、额度被清空、服务中断），由使用者自行承担。

本项目作者不对任何因使用或滥用本项目产生的直接或间接损失负责。

本项目与腾讯、WorkBuddy、DeepSeek 均无关联，未获其授权或认可；文中出现的名称仅用于描述兼容关系，其商标权利归各自所有。

## 致谢

- [Sliverkiss/workbuddy2api](https://github.com/Sliverkiss/workbuddy2api)（MIT）— WorkBuddy 上游协议的参照实现。
- [franksong2702/dsh-codex-connect](https://github.com/franksong2702/dsh-codex-connect)（Apache-2.0）— DSH 插件结构与 provider 注册的参照。

## License

MIT
