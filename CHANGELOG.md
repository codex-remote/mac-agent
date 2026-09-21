# Changelog

Codex Remote Mac Agent 的重要变更记录在此文件中。

格式参考 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)。正式发布后遵循 [Semantic Versioning](https://semver.org/spec/v2.0.0.html)。

> Release status: **0.0.1**
>
> `0.x` 阶段继续快速迭代。后续变更按实际影响决定是否兼容，并记录在 `Changed` 或 `Removed`；`0.0.1` 不构成兼容性冻结。

## [Unreleased]

### Added

- Adopted Apache License 2.0, portable user-home defaults, public CI, and Dependabot updates.
- Added WAL-backed Runtime SQLite with `agent_runs`, `result_outbox` and `bootstrap_syncs` durable state.
- Added monotonic Agent event sequences, receipt/durable ACK handling and reconnect recovery.
- Added in-connection replay of events that have not received a PostgreSQL durable ACK.
- Added resumable Bootstrap Project/Thread/Turn batches with checksums and per-batch durable ACKs.
- Bootstrap snapshots now freeze the session manifest before detail reads and report processed/total session counts for determinate client progress.
- Bootstrap completion now explicitly permits destructive reconciliation only for an unresumed manifest; reconnect recovery remains import-only so a regenerated batch order cannot hide valid Runtime Projects or Sessions.
- Added authenticated Runtime source viewing support through `source.read`, with project-root and symlink containment, sensitive-file and binary rejection, bounded line windows, content hashes, and no durable source storage.
- Added the `mobileweb` Relay/Agent launcher profile on port `18775` and an exact process-exit barrier before replacement instances open the shared Runtime SQLite.
- Added the isolated `mobileweb-debug` Relay/Agent launcher profile on port `18875` for source-worktree `devrun crweb` deployments, leaving the release-compatible `mobileweb` profile unchanged.

### Fixed

- Preserved App Server notification order by processing terminal and content events through one queue, preventing a fast `turn/completed` notification from dropping the final assistant summary.
- The managed Mac Agent now starts before Codex has recorded its first project, so a fresh Homebrew setup remains healthy while waiting for the user to open a workspace.
- Bootstrap history import now bounds each Thread detail read and falls back to session metadata, so one unavailable or oversized Thread cannot block the remaining catalog.

## [0.0.1] - 2026-08-13

### Added

- 支持读取 Codex Desktop 侧边栏项目、项目顺序和 Thread-Project 归属。
- Thread 快照新增最新可见消息摘要，与 Codex 原始 `preview` 分离，并使用有界查询和缓存。
- 支持配置多个工作区根目录作为 Codex 项目访问允许列表。
- 通过 Codex App Server 列出、创建和继续 Codex Thread。
- 支持 `thread.read -> thread.detail`，读取并规范化持久化 Turn/Item 历史，包含项目归属校验和大小边界。
- 支持启动、中断 Turn，并流式返回 assistant、stdout 和 stderr 事件。
- 支持单 Turn 超时、重连快照、最近日志和 Git Diff 结果收集。
- 提供真实 Codex 本地测试命令和临时示例项目脚本。
- 提供复用 Relay `run` 脚本的 `./dev`，隔离运行 `debug`、`simulator` 和 `iphone` 三套 Mac Agent。
- 支持从包含多个 Git 仓库的 Codex 项目根目录聚合修改文件和 Diff。
- 新增 `agent.capabilities` 权限快照；Agent 拒绝消息附带实际沙箱和授权上下文，供 iPhone 在提交任务前提示受限执行。
- 接入 App Server `permissionProfile/list`，支持 `execution.profile.list/snapshot` 和 `turn.start.permission_profile_id`；按项目校验 allowed profile，并同步应用到 Thread 与 Turn。
- 接受 iPhone 的 `turn.acknowledged` 终态确认，供独立维护任务在 Turn 完成后安全刷新客户端。

### Changed

- 远程执行模型升级为 Project、Thread、Turn，协议唯一版本为 `spec_version: "2.0"`。
- Codex 集成改为结构化 App Server JSON-RPC，不再为远程请求拼装临时 Shell 命令。
- 文档明确 Mac Agent 只生产结构化日志；独立 Admin Platform/Collector 负责本地采集、CoreDevice 与未来 SLS 接入，采集故障不得影响 Turn。
- Mac Agent 未显式配置工作区时默认只允许访问当前用户 `~/work` 下的 Codex Desktop 项目。
- `project.list` 改为返回允许访问的 Codex Desktop 侧边栏项目，不再返回工作区内所有 Git 仓库。
- Thread 列表优先使用 Codex Desktop 保存的项目归属，未登记的 Thread 按最具体的项目根目录归属。

### Removed

- 删除 `spec_version: "1.0"` 的 `run.*` 消息。
- 删除固定 `--working-dir` 模式、`run` 命令和对应旧配置。
- 删除 `AGENT_PROJECT_SCAN_DEPTH` 和 `--project-scan-depth`，项目发现不再递归扫描目录。

### Fixed

- `thread/resume` 只使用当前稳定参数，避免依赖实验性 API。
- Agent 与 Relay 的 WebSocket 消息大小限制保持一致。
- 启动 App Server 前展开 Codex 可执行文件符号链接，并校验 App bundle 的 Code Mode Host，避免远程任务在错误目录查找配套执行器。
- 真机维护不再要求当前 Turn 同步重启承载它的 iPhone Mac Agent；自重启由脚本前置检查拒绝。
