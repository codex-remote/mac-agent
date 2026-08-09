# AI Coding Remote - Mac Agent

AI Coding Remote 的本地执行端。它主动连接 Relay，发现 Mac 上允许访问的多个 Git 项目，通过本地 `codex app-server` 查询会话并执行 Turn，再流式返回状态、输出与 Git 结果。

## MVP 能力

- 多个 `--workspace-root`，递归发现 Git 项目并生成稳定 `project_id`。
- 查询所有 Codex CLI、VS Code、Exec 和 App Server 来源的 Thread。
- 在指定 Project 新建 Thread，或继续属于该 Project 的历史 Thread。
- 使用 Codex App Server 的 JSON-RPC 和事件通知，不拼装 Shell 命令。
- 全局单 Turn、超时、中断、最近输出 Ring Buffer 与重连快照。
- 收集修改文件、assistant 摘要和最大 128 KiB Git Diff。
- WebSocket 自动重连和 JSON 结构化服务日志。

当前无鉴权、数据库、业务 Task、队列和多 Mac 路由。协议唯一版本为 `2.0`，不兼容已删除的 `1.0 run.*`、`--working-dir` 和 `run` 命令。

## 环境

- macOS
- Go 1.23+
- 已安装并登录 Codex CLI
- 工作区根目录下至少有一个 Git 仓库

```bash
go version
codex --version
codex login status
```

Mac Agent 使用 Codex 官方 App Server 接口：`thread/list`、`thread/start`、`thread/resume`、`turn/start` 和 `turn/interrupt`。参见 [Codex App Server](https://developers.openai.com/codex/app-server/)。

## 构建与测试

```bash
make test
make test-race
make vet
make build
```

产物为 `bin/mac-agent`。

## 启动并管理多个项目

父目录本身不必是 Git 仓库。以下配置会发现 `codexremote` 下的 `iphone-app`、`relay-server`、`mac-agent`：

```bash
./bin/mac-agent serve \
  --relay-url ws://127.0.0.1:8080/ws/agent \
  --workspace-root /Users/leehooo/work/selftools/codexremote \
  --project-scan-depth 2 \
  --name leehoo-mac
```

可以重复传入根目录：

```bash
./bin/mac-agent serve \
  --relay-url ws://127.0.0.1:8080/ws/agent \
  --workspace-root /Users/leehooo/work/selftools \
  --workspace-root /Users/leehooo/work/work-projects
```

手机只能使用 Agent 返回的 `project_id`，不能传入路径。扫描目录的绝对路径只在 Mac 启动配置中出现。

## 不经过 Relay 测试真实 Codex

```bash
./bin/mac-agent turn \
  --project-dir /absolute/path/to/git-project \
  --prompt "检查当前修改，修复问题并运行相关测试，不要提交代码"
```

自动创建一个带失败测试的临时 Go 项目并让真实 Codex 修复：

```bash
./scripts/demo.sh
```

脚本保留临时项目并输出路径，不修改已有项目。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `AGENT_RELAY_URL` | 无 | `serve` 使用的 `/ws/agent` 地址 |
| `AGENT_WORKSPACE_ROOTS` | 无 | 路径列表；macOS 使用 `:` 分隔 |
| `AGENT_PROJECT_SCAN_DEPTH` | `4` | 项目发现最大目录深度 |
| `AGENT_CODEX_BINARY` | `codex` | Codex CLI 路径或命令名 |
| `AGENT_NAME` | hostname | iPhone 展示名称 |
| `AGENT_TURN_TIMEOUT` | `30m` | 单 Turn 最大时长 |
| `AGENT_LOG_BUFFER_LINES` | `500` | 重连快照保留行数 |

示例：

```bash
AGENT_RELAY_URL=ws://127.0.0.1:8080/ws/agent \
AGENT_WORKSPACE_ROOTS=/Users/leehooo/work/selftools/codexremote \
AGENT_PROJECT_SCAN_DEPTH=2 \
./bin/mac-agent serve
```

## 结构

```text
cmd/agent/          serve / turn 入口
internal/agent/     v2 消息应用服务
internal/codexapp/  Codex App Server 进程与 JSON-RPC Client
internal/inventory/ Project 与 Thread 快照
internal/workspace/ Git 项目 Catalog 与路径边界
internal/runner/    Thread/Turn 适配与事件转换
internal/turn/      单 Turn Controller
internal/result/    Git 文件与 Diff 收集
internal/relay/     WebSocket、重连与收发队列
internal/protocol/  v2 Project/Thread/Turn 模型
```

## 演进边界

- 鉴权、数据库、业务 Task 和多设备路由由 Relay 控制面新增。
- 多执行器通过 Adapter Registry 加入，不改变 Workspace Catalog。
- 队列和并发通过 Scheduler 包裹 Turn Controller。
- 可靠交付通过 Inbox/Outbox 包裹 Relay Client。

这些边界允许扩展，但不承诺预发布 Wire Protocol 向后兼容。
