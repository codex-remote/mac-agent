# AI Coding Remote - Mac Agent

AI Coding Remote 的本地执行端。使用 Go 开发，主动连接 Relay Server，在固定工作目录中调用本机已有的 Codex CLI，并流式回传状态、输出和结果。

## 当前状态

仓库已初始化，尚未生成 Go Module 或业务代码。

MVP 只支持一台 Mac、一个固定工作目录、一个 Codex Runner 和一个正在执行的 Run。不包含设备注册、数据库、队列和执行历史。

## 本仓库负责

- 主动连接 Relay `/ws/agent`。
- WebSocket 自动重连和当前状态同步。
- 维护唯一的 `CurrentRun`。
- 在本地固定工作目录启动 Codex CLI。
- 流式读取 stdout/stderr 并发送 `run.output`。
- 保存最近一段内存日志并发送 `run.snapshot`。
- 处理超时和 `run.cancel`，终止整个子进程组。
- 完成后收集退出码、修改文件和截断后的 Git Diff。
- 校验远程消息不能改变工作目录、可执行文件和启动参数。

## 本仓库不负责

- iPhone 界面和用户输入体验。
- Relay 的连接注册和消息路由。
- 用户鉴权、设备绑定和 Task 管理。
- 离线队列、持久 Run、ACK 和可靠重放。
- 自动提交、推送、创建 PR 或部署。

## 项目边界

本仓库不依赖 `iphone-app` 或 `relay-server` 的源码，只实现版本化 JSON 协议：

- 协议版本：`spec_version: "1.0"`
- Agent 连接端点：`/ws/agent`
- 只消费 `run.start` 和 `run.cancel`。
- 只产生 `agent.*` 与 `run.*` 事件。
- Relay 发布的 Schema 和 Fixtures 用于契约测试。
- `RunController`、`Runner`、`WorkspaceResolver` 必须保持解耦。

本地架构说明位于：`../Codex Remote/05-架构决策/ADR-003 MVP 演进兼容边界.md`。

## 计划中的目录结构

```text
.
├── cmd/agent/main.go
├── internal/
│   ├── config/config.go
│   ├── relay/client.go
│   ├── run/controller.go
│   ├── runner/codex.go
│   ├── workspace/fixed.go
│   ├── buffer/ring.go
│   └── observability/logging.go
├── packaging/launchd/
├── go.mod
└── README.md
```

## 计划中的配置

| 环境变量 | 必填 | 说明 |
| --- | --- | --- |
| `AGENT_RELAY_URL` | 是 | Relay 的 `wss://.../ws/agent` 地址 |
| `AGENT_WORKING_DIR` | 是 | 唯一允许 Codex 访问的工作目录 |
| `AGENT_CODEX_BINARY` | 否 | Codex 可执行文件，默认从 PATH 查找 |
| `AGENT_RUN_TIMEOUT` | 否 | 单次 Run 最大执行时间 |
| `AGENT_LOG_BUFFER_LINES` | 否 | 重连快照保留的最近日志行数 |
| `AGENT_LOG_LEVEL` | 否 | 结构化日志级别 |

手机端不能覆盖任何上述配置。

## 执行边界

```text
run.start(prompt)
    -> RunController 检查 idle
    -> WorkspaceResolver 返回固定目录
    -> Codex Runner 启动受控子进程组
    -> EventSink 发送 run.started / run.output
    -> 收集结果
    -> run.completed / run.failed / run.cancelled
```

`Runner` 只接收结构化 `RunRequest`，不能把远程 payload 拼接为 Shell 命令。

## MVP 验收

- Relay 重启后 Agent 自动重连并上报状态。
- 空闲时 Prompt 能启动 Codex，忙碌时拒绝第二个 Run。
- stdout/stderr 可以持续流式发送。
- Stop 和超时都能清理 Codex 及其子进程。
- Agent 重连时可以恢复当前状态和最近日志。
- 手机无法修改工作目录、二进制或启动参数。
- 完成事件包含退出码、文件列表和受大小限制的 Diff。

## 后续扩展

未来 Task、鉴权和数据库主要位于 Relay；Mac 端通过替换 Connection Registry 关联信息、Workspace Resolver、Scheduler 和 Runner Registry 扩展，不重写现有 Codex Runner。
