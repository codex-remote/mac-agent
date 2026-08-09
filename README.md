# AI Coding Remote - Mac Agent

AI Coding Remote 的本地执行端。它主动连接 Relay Server，在预先配置的 Git 工作目录中调用本机已有的 Codex CLI，并流式返回运行状态、输出与 Git 变更结果。

## 当前状态

Mac Agent MVP 已实现并通过单元测试、竞态检测和真实 Codex 端到端测试。

当前刻意保持简单：一台 Mac、一个固定工作目录、一个 Codex Runner、同一时间一个 Run。没有鉴权、数据库、任务队列或历史记录。模块边界已经为这些能力预留扩展点，不需要在未来重写 Codex Runner 或 WebSocket Client。

## 已实现能力

- `serve`：主动连接 Relay，断线后指数退避重连。
- `run`：跳过 Relay，在终端直接验证完整执行链路。
- 使用版本化 JSON 消息处理 `run.start`、`run.cancel` 和 `agent.*` / `run.*` 事件。
- 固定工作目录，远程消息不能覆盖目录、二进制或 Codex 参数。
- 使用参数数组启动 `codex exec`，不经过 Shell 拼接。
- 分别流式读取 stdout/stderr，保留有限内存日志。
- 忙碌保护、运行超时和取消时清理整个 Codex 子进程组。
- 收集退出码、修改文件、最终摘要和最大 128 KiB 的 Git Diff。
- 重连时恢复当前运行快照；空闲时重放最近一次终态。
- JSON 结构化服务日志。

## 环境要求

- macOS
- Go 1.23 或更新版本
- 已安装并完成登录的 Codex CLI
- 目标目录已经初始化为 Git 仓库

检查环境：

```bash
go version
codex --version
codex login status
```

Mac Agent 使用 Codex 官方的非交互模式 `codex exec`，并以 `workspace-write` sandbox 运行。参考 [Codex non-interactive mode](https://learn.chatgpt.com/docs/non-interactive-mode.md)。

## 构建与测试

```bash
make test
make test-race
make vet
make build
```

构建产物位于 `bin/mac-agent`。

## 立即测试：自动创建示例项目

```bash
./scripts/demo.sh
```

脚本会：

1. 在系统临时目录创建一个独立的 `greeting-service` Git 项目。
2. 写入一个失败的 Go 测试，要求空白名字返回 `Hello, stranger!`。
3. 构建 Mac Agent，并通过它调用真实 Codex 完成需求。
4. 再次运行测试，输出修改文件和 Git Diff。

脚本结束时会打印项目路径，并保留项目供你检查。它不会修改或提交本仓库之外的已有项目。

## 对任意本地项目执行需求

```bash
./bin/mac-agent run \
  --working-dir /absolute/path/to/your/git-project \
  --prompt "修复登录接口的失败测试，并运行相关测试，不要提交代码"
```

也可以把 Prompt 作为位置参数：

```bash
./bin/mac-agent run --working-dir /absolute/path/to/repo "解释并修复当前失败测试"
```

按 `Ctrl+C` 会请求取消运行，并等待 Codex 子进程退出。

## 连接 Relay

Relay Server 实现 `/ws/agent` 后可以启动长连接模式：

```bash
AGENT_RELAY_URL=ws://127.0.0.1:8080/ws/agent \
AGENT_WORKING_DIR=/absolute/path/to/your/git-project \
./bin/mac-agent serve
```

也支持等价命令行参数：

```bash
./bin/mac-agent serve \
  --relay-url ws://127.0.0.1:8080/ws/agent \
  --working-dir /absolute/path/to/your/git-project \
  --name my-mac
```

## 配置

| 环境变量 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `AGENT_RELAY_URL` | `serve` 必填 | - | Relay 的 `ws://` 或 `wss://` 地址 |
| `AGENT_WORKING_DIR` | 是 | - | 唯一允许 Codex 操作的 Git 工作目录 |
| `AGENT_CODEX_BINARY` | 否 | `codex` | Codex CLI 路径或命令名 |
| `AGENT_NAME` | 否 | 本机 hostname | Agent 展示名称 |
| `AGENT_RUN_TIMEOUT` | 否 | `30m` | 单次运行最大时长 |
| `AGENT_LOG_BUFFER_LINES` | 否 | `500` | 重连快照保留的最近输出行数 |

手机端消息不能覆盖这些配置。

## 项目结构

```text
.
├── cmd/agent/             # serve / run 命令入口
├── internal/
│   ├── agent/             # Relay 消息到 RunController 的应用服务
│   ├── buffer/            # 有界内存日志
│   ├── config/            # 环境变量与默认配置
│   ├── protocol/          # 版本化 JSON 消息及校验
│   ├── relay/             # WebSocket、心跳、重连与收发队列
│   ├── result/            # Git 文件列表和 Diff 收集
│   ├── run/               # 单 Run 状态机、超时、取消和快照
│   ├── runner/            # Codex CLI 进程适配器
│   └── workspace/         # 固定 Git 工作目录解析
├── scripts/demo.sh        # 真实 Codex 端到端示例
├── Makefile
├── go.mod
└── README.md
```

## 设计边界与演进

本仓库不依赖 `iphone-app` 或 `relay-server` 的源码，只依赖 `spec_version: "1.0"` 的 JSON 协议。核心接口 `RunController`、`Runner`、`WorkspaceResolver`、`GitCollector` 和 Relay Handler 彼此解耦。

未来能力按以下方式加入：

- 数据库、鉴权、设备归属和持久任务主要由 Relay 承担。
- 多工作区通过替换 `WorkspaceResolver` 加入，不改变 Codex Runner。
- 多 Agent 类型通过 Runner Registry 加入，不改变传输层。
- 排队和并发策略通过 Scheduler 加入，不改变 WebSocket Client。
- 可靠消息与 ACK 可以扩展消息协议；当前终态快照已覆盖最常见的 Relay 短暂断线场景。

本地架构决策文档位于 `../Codex Remote/05-架构决策/ADR-003 MVP 演进兼容边界.md`。

## MVP 明确不做

- 自动提交、推送、创建 PR 或部署。
- 用户鉴权和设备绑定。
- 离线任务队列和永久执行历史。
- 从手机指定任意工作目录或可执行文件。
- 对 Codex 本身的 AI 能力做二次实现。
