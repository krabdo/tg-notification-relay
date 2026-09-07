# Telegram Notification Relay

使用 Go 将 Telegram 用户账号收到的关键消息转发到 [Shoutrrr](https://containrrr.dev/shoutrrr/v0.8/) 通知渠道。支持 Linux amd64、arm64，以单进程运行，无需外部数据库。

## 通知规则

- 私聊、群组中被 @、匹配 `MY_USERNAME` 的提及、回复自己的消息触发通知。
- 默认忽略自己发出的消息、普通群消息及无关频道消息。
- 使用 Telegram 通知渠道时，忽略该通知 bot 回送的本程序摘要，防止循环转发；其他 bot 消息仍按正常规则处理。
- 通知包含姓名、群名和消息正文的前 50 个 Unicode 字符；超过部分直接截断，不追加省略号。媒体消息使用其文字说明，没有正文时显示“点击查看”，不转发媒体文件。
- Bark 通知可直接打开 Telegram；其他渠道收到文本形式的 `tg://`，是否可点击由接收端决定。
- 在自己的收藏夹 / Saved Messages 发送 `/on`、`/off`、`/status`、`/help` 控制通知，开关重启后保留。其他聊天中的命令无效。

## Docker Compose 快速开始

从仓库获取 `compose.yaml` 和 `.env.example`，在同一目录执行：

```sh
cp .env.example .env
# 编辑 .env，填写 Telegram API 凭据和通知 URL
docker compose pull
docker compose run --rm relay login
docker compose up -d
docker compose logs -f --tail=100
```

在 [my.telegram.org](https://my.telegram.org) 创建 API 应用取得 `TG_API_ID` 和 `TG_API_HASH`。`login` 依次请求手机号、验证码和可选的二步验证密码；需要交互式终端。请使用已有的 Telegram 账号。

会话与开关保存在命名卷 `relay-data` 的 `/data` 中。容器以 UID/GID `10001:10001` 运行；如果改用绑定目录挂载，需要让该用户有写权限。不要并行运行使用同一会话的多个实例。

手动拉取镜像：

```sh
docker pull ghcr.io/krabdo/tg-notification-relay:v0.1.1
```

更新时修改 Compose 中的版本，然后运行 `docker compose pull && docker compose up -d`。保留数据卷，无需重新登录。会话失效时先 `docker compose down`，再运行 `login` 并启动服务。不要删除持久卷来执行普通更新。

## 配置

程序读取当前目录 `.env`，已有环境变量优先。`version` 和 `services` 无需配置；`login` 无需通知渠道配置。

| 变量 | 用途 / 默认值 |
| --- | --- |
| `TG_API_ID` / `TG_API_HASH` | 必填，Telegram API 凭据 |
| `SHOUTRRR_URLS` | 通知 URL 的 JSON 字符串数组，至少一个目标 |
| `MY_USERNAME` | 可选，额外文本提及匹配，允许带 `@`，忽略大小写 |
| `PUSH_SELF_MESSAGES` | 默认 `false` |
| `TG_SESSION` | 默认 `data/telegram.session.json`；镜像默认 `/data/telegram.session.json` |
| `STATE_FILE` | 默认 `data/state.json`；镜像默认 `/data/state.json` |
| `GOMEMLIMIT` | 可选，Go 运行时软内存目标，不是 RSS 硬上限 |

`SHOUTRRR_URLS` 示例（URL 中的凭据及特殊字符需按渠道文档编码）：

```dotenv
SHOUTRRR_URLS='["bark://:KEY@api.day.app/?group=Telegram&sound=healthnotification", "ntfy://ntfy.sh/YOUR_TOPIC"]'
```

支持 Shoutrrr v0.8.0 的全部正式注册渠道，包括 Bark、Discord、Generic Webhook、Gotify、Google Chat、IFTTT、Join、Matrix、Mattermost、ntfy、Opsgenie、Pushbullet、Pushover、Rocket.Chat、Slack、SMTP、Teams、Telegram 和 Zulip，以及 logger 和兼容别名。查看实际注册列表：

```sh
docker compose run --rm relay services
```

各渠道 URL 参数由 Shoutrrr 解析，参见 [官方渠道配置](https://containrrr.dev/shoutrrr/v0.8/services/overview/)。服务商接口可能变化；渠道支持表示保留上游适配器，不代表所有第三方账号均已实测。实验性、未完整实现的适配器不启用。

Bark URL 可自行设置 `group`、`sound`、`icon` 等参数；示例使用 `Telegram` 分组和 `healthnotification` 声音，通知点击后打开 Telegram。

## 运行与资源边界

实体缓存最多分别保存 1024 个用户、1024 个聊天；去重缓存最多 4096 条消息标识，不保留正文。Telegram 入口最多排队 64 个普通更新包及 16 个优先命令更新包，每目标通知队列最多 64 条；队列满时丢弃最旧待处理项目并记录累计数量。

每目标独立发送、最多三次尝试，失败后间隔两秒，成功渠道不会跟随其他渠道重发。超时后等待该目标底层请求退出，再决定是否重试，避免无限堆积任务。永久卡住的渠道需要重启程序恢复，其他渠道仍工作。第三方可能已收到超时请求，重试不保证恰好一次送达。

`/off` 清空待发通知并使旧任务失效；已发出的网络请求可能完成。队列不持久化，停机期间和更新缺口不保证补发。支持传输断线重连，程序不主动扫描历史消息。状态文件缺失默认开启；损坏或无法读取时警告并默认开启，保存失败时不更改内存开关。

实际内存取决于 Telegram 账号流量及通知渠道。项目不承诺固定 RSS；CI 使用模拟消息测量空闲、持续消息和故障渠道下的 RSS、峰值内存和 goroutine 数，结果存于 `simulated-resource-profile` 构建产物。该测试不连接真实 Telegram，不能替代账号实际运行测量。

## 本地构建与测试

需要 Go 1.26.5 或兼容更新版本：

```sh
go build -trimpath -ldflags="-s -w" -o bin/tg-notification-relay .
./bin/tg-notification-relay login
./bin/tg-notification-relay run
go vet ./...
go test -race ./...
go test -run '^TestResourceProfile$' -v -count=1 ./...
```

支持 `login`、`run`（默认）、`version`、`services`。所有真实配置和会话文件应仅存于本机或持久卷，不能放进镜像。程序日志不会输出通知 URL、消息正文或上游错误响应；显式使用 `logger` 渠道会按该渠道用途打印通知摘要。

## 发布

主分支和 PR 运行检查；`v*` 标签触发检查、两个架构的容器冒烟测试及 GHCR 发布。镜像包含版本标签、`latest` 和 `sha-<完整提交哈希>`，通过 OCI 标签关联本仓库。发布使用 GitHub Actions 的 `GITHUB_TOKEN`，无需在仓库保存个人访问令牌。
