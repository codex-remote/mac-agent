# Changelog

AI Coding Remote Mac Agent 的重要变更记录在此文件中。

格式参考 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)。正式发布后遵循 [Semantic Versioning](https://semver.org/spec/v2.0.0.html)。

> Release status: **Unreleased**
>
> 首个生产 GitHub Release 发布前不承诺向后兼容。预发布阶段的破坏性变更会直接移除旧实现，并记录在 `Changed` 或 `Removed`。

## [Unreleased]

### Added

- 支持配置多个工作区根目录并递归发现 Git 项目。
- 通过 Codex App Server 列出、创建和继续 Codex Thread。
- 支持启动、中断 Turn，并流式返回 assistant、stdout 和 stderr 事件。
- 支持单 Turn 超时、重连快照、最近日志和 Git Diff 结果收集。
- 提供真实 Codex 本地测试命令和临时示例项目脚本。

### Changed

- 远程执行模型升级为 Project、Thread、Turn，协议唯一版本为 `spec_version: "2.0"`。
- Codex 集成改为结构化 App Server JSON-RPC，不再为远程请求拼装临时 Shell 命令。

### Removed

- 删除 `spec_version: "1.0"` 的 `run.*` 消息。
- 删除固定 `--working-dir` 模式、`run` 命令和对应旧配置。

### Fixed

- `thread/resume` 只使用当前稳定参数，避免依赖实验性 API。
- Agent 与 Relay 的 WebSocket 消息大小限制保持一致。
