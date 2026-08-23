# AI Coding Remote - Mac Agent

AI Coding Remote 的本地执行端。它主动连接 Relay，读取 Codex Desktop 中已经登记的本地项目，通过本地 `codex app-server` 查询会话并执行 Turn，再流式返回状态、输出与 Git 结果。

## MVP 能力

- 从 Codex Desktop 侧边栏项目读取名称、顺序、根目录和 Thread 归属。
- 多个 `--workspace-root` 作为本机访问允许列表，并为允许的 Codex 项目生成稳定 `project_id`。
- 查询所有 Codex CLI、VS Code、Exec 和 App Server 来源的 Thread。
- 使用 Codex `thread/turns/list` 的有界摘要页生成最新可见消息预览，并按 `updated_at` 缓存；不把 reasoning、commentary 或 tool 内容作为列表副标题。
- 通过稳定的 Codex `thread/read(includeTurns: true)` 读取历史 Turns、消息、命令和文件变更，并验证 Project 归属。
- 在指定 Project 新建 Thread，或继续属于该 Project 的历史 Thread。
- 使用 Codex App Server 的 JSON-RPC 和事件通知，不拼装 Shell 命令。
- 全局单 Turn、超时、中断、最近输出 Ring Buffer 与重连快照。
- 收集修改文件、assistant 摘要和最大 128 KiB Git Diff；一个 Codex 项目可包含多个 Git 仓库。
- WebSocket 自动重连和 JSON 结构化服务日志。
- 连接时发布 `agent.capabilities` 执行权限快照，并在 Agent 拒绝请求时附带相同的 `execution_context`。
- 按项目查询 App Server `permissionProfile/list`，把允许的执行档位提供给 iPhone，并在 Thread 与 Turn 两层应用所选 profile。
- 处理 Run Server 内部发送的 `source.read`，只读取可信项目根目录内的当前文本源码，并返回带目标行、范围、哈希和修改时间的有界窗口。

当前无鉴权、数据库、业务 Task、队列和多 Mac 路由。协议唯一版本为 `2.0`，不兼容已删除的 `1.0 run.*`、`--working-dir` 和 `run` 命令。

## Admin 与日志边界

Mac Agent 生产 JSON 结构化服务日志，但不负责把日志直接写入 Admin 数据库或 SLS。已接受的下一阶段由独立 Diagnostics Collector 跟踪各 profile 的滚动日志文件，维护确认游标并批量提交 Admin Platform；线上环境可以由 LoongCollector 采集相同 JSON Schema。

Mac Agent 日志必须使用稳定事件名、`event_id`、时间、profile/实例、`session_id`、`trace_id`、脱敏 `turn_ref` 和版本字段。禁止记录 Prompt、响应、Transcript、Console 正文、Diff、凭据、授权头、Relay 完整 URL、完整文件路径和永久设备标识。高频 Codex delta、重试和资源事件必须采样或聚合，日志队列不得无限占用内存。

Diagnostics Collector 不是 Mac Agent 子模块。它属于独立 `admin-platform`，并承担 CoreDevice 的白名单设备采集。Mac Agent 不接收来自后台的任意 Shell 命令，也不因为 Admin 或日志采集不可用而停止 Turn。

## 发布状态

当前版本为 **0.0.1**，是首个最小可用快照。`0.x` 阶段继续快速迭代，后续变更按实际影响决定是否兼容，并在 [CHANGELOG.md](CHANGELOG.md) 中记录；此版本不代表兼容性冻结。

## 环境

- macOS
- Go 1.23+
- 已安装并登录 Codex CLI
- Codex Desktop 已创建至少一个本地项目
- `~/.codex/.codex-global-state.json` 存在

```bash
go version
codex --version
codex login status
```

Mac Agent 使用 Codex 官方 App Server 接口：`permissionProfile/list`、`thread/list`、`thread/read`、`thread/turns/list`、`thread/start`、`thread/resume`、`turn/start` 和 `turn/interrupt`。`permissionProfile/list` 与 `thread/turns/list` 为 experimental，Agent 初始化时显式启用 `experimentalApi`。参见 [Codex App Server](https://developers.openai.com/codex/app-server/)。官方 App Server 当前没有 `project/list`，因此项目元数据由隔离的 Codex Desktop State Adapter 读取；该内部 JSON 格式不会进入 Relay Wire Protocol。

未选择 permission profile 时，远程 Turn 保持 `workspace-write` + `never` 的兼容默认值。支持新协议的 iPhone 会先请求当前项目允许的 profile，默认选择 `:workspace`，也可显式选择 `:read-only` 或 App Server/管理策略允许的 `:danger-full-access`。Agent 在提交前重新查询 allowed 状态，不接受任意 profile ID，并把同一个 `permissions` 值传给 `thread/start|resume` 与 `turn/start`；使用 profile 时不会同时发送 legacy `sandbox`。

## 构建与测试

```bash
make test
make test-race
make vet
make build
```

产物为 `bin/mac-agent`。

## 启动并管理多个项目

在项目根目录一键重启 Relay 与 Mac Agent：

```bash
./dev debug
./dev simulator
./dev iphone
./dev mobileweb
```

`debug` 使用 `18765`，供 Apifox 和手动协议调试；`simulator` 使用 `18767`，供本机 iPhone Simulator；`iphone` 使用 `18768`，供真机 iPhone；`mobileweb` 使用 `18775`，供 Mobile Web Runtime。每个 profile 有独立的 Relay、Mac Agent、`launchctl` label、PID 和日志，可以同时运行；重复执行只重启指定 profile。脚本会等待旧 Agent 完全退出后再启动新实例，避免并发占用 Runtime SQLite。

两套 Agent 会访问相同工作区。MVP 尚无跨 profile 的 Turn 锁，请勿同时对同一个 Git 项目发起修改任务。

Codex 安装在其他位置时，可显式指定：

```bash
CODEX_BINARY=/absolute/path/to/codex ./dev simulator
```

安装、升级和 Code Mode Host 故障的快速检查见 [维修手册](docs/maintenance.md)。

默认只允许访问 `/Users/leehooo/work` 下的 Codex Desktop 项目。它不会再扫描该目录寻找 Git 仓库：

```bash
./bin/mac-agent serve \
  --relay-url ws://127.0.0.1:18765/ws/agent \
  --name leehoo-mac
```

需要允许其他目录时，可以传入一个或多个根目录；第一次显式传参会清除默认值：

```bash
./bin/mac-agent serve \
  --relay-url ws://127.0.0.1:18765/ws/agent \
  --workspace-root /Users/leehooo/work/selftools \
  --workspace-root /Users/leehooo/work/work-projects
```

手机只能选择 Agent 返回的 `project_id`。源码查看可以附带项目相对路径；历史回答中的绝对路径仅作为兼容输入，并且必须在解析符号链接后仍位于该项目根目录内。项目必须同时存在于 Codex Desktop 的 `project-order` 中，且根目录位于允许列表内；已经从侧边栏移除但仍残留在状态文件中的项目不会返回。

源码查看读取点击时的当前工作树，不保存回答生成时的历史快照。Agent 拒绝目录、路径穿越、逃逸项目的符号链接、二进制、敏感凭据文件模式和超过 1 MiB 的文件；返回内容按 Relay WebSocket 帧上限裁剪到目标行附近。源码正文和完整路径不得进入服务日志。

项目列表的展示名和顺序来自 Codex Desktop。Thread 元数据和运行状态来自官方 `thread/list`；`preview` 保留其原始会话预览语义，`latest_message_preview` 由最新摘要 Turn 中最后一个用户或助手消息生成。Mac Agent 会优先使用 Desktop 保存的 Thread-Project 归属，再按最具体的项目根目录归属 Mac Agent 新建的 Thread。

`thread.read` 返回平台自己的稳定 `thread.detail` 模型，不把 Codex 原始 union JSON 直接暴露给 App。单字段最大 24 KiB，详情最大 200 KiB；发生裁剪时通过 `truncated` 标记，给 Relay 256 KiB 帧上限保留 Envelope 余量。

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
| `AGENT_WORKSPACE_ROOTS` | `/Users/leehooo/work` | Codex 项目访问允许列表；macOS 使用 `:` 分隔 |
| `AGENT_CODEX_STATE_FILE` | `$CODEX_HOME/.codex-global-state.json` 或 `~/.codex/.codex-global-state.json` | Codex Desktop 全局状态文件 |
| `AGENT_CODEX_BINARY` | `codex` | Codex CLI 路径或命令名 |
| `AGENT_NAME` | hostname | iPhone 展示名称 |
| `AGENT_TURN_TIMEOUT` | `3h` | 单 Turn 最大时长 |
| `AGENT_LOG_BUFFER_LINES` | `500` | 重连快照保留行数 |

示例：

```bash
AGENT_RELAY_URL=ws://127.0.0.1:18765/ws/agent \
AGENT_WORKSPACE_ROOTS=/Users/leehooo/work/selftools/codexremote \
./bin/mac-agent serve
```

## 结构

```text
cmd/agent/          serve / turn 入口
internal/agent/     v2 消息应用服务
internal/codexapp/  Codex App Server 进程与 JSON-RPC Client
internal/inventory/ Project 与 Thread 快照
internal/workspace/ Codex Desktop 项目适配器、Thread 归属与路径边界
internal/runner/    Thread/Turn 适配与事件转换
internal/source/    项目范围内的只读源码窗口
internal/turn/      单 Turn Controller
internal/result/    Git 文件与 Diff 收集
internal/relay/     WebSocket、重连与收发队列
internal/protocol/  v2 Project/Thread/Turn 模型
```

## 演进边界

- Relay 实时身份和多设备路由留在 Relay；用户、后台权限、诊断数据、行为查询和管理审计由独立 Admin Platform 提供。
- Codex Desktop 状态读取被限制在 Workspace Adapter 内；未来官方项目接口可替换该 Adapter，不改变 Relay 协议。
- 多执行器通过 Adapter Registry 加入，不改变 Project Catalog。
- 队列和并发通过 Scheduler 包裹 Turn Controller。
- 可靠交付通过 Inbox/Outbox 包裹 Relay Client。
- 标准 JSONL 是服务日志生产契约；Collector、SLS 和数据库实现不得进入 Turn Controller 或 Codex Adapter。

这些边界允许扩展，但不承诺预发布 Wire Protocol 向后兼容。
