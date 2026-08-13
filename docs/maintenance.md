# Mac Agent 维修手册

## 安装与升级检查

在新 Mac 安装或 ChatGPT/Codex App 升级后，先运行目标 profile：

```bash
./dev iphone
```

启动输出中的 `Codex CLI` 必须是展开符号链接后的真实路径。使用 ChatGPT App 时通常为：

```text
/Applications/ChatGPT.app/Contents/Resources/codex
```

`dev` 和 Mac Agent 会分别校验路径。对于 App bundle，还会检查同目录的 `codex-code-mode-host` 是否存在且可执行。任一检查失败时停止启动，不要继续部署 iPhone App。

升级后的最小验证：

```bash
make test
./dev simulator
./dev iphone
```

分别确认 `http://127.0.0.1:18767/status` 和 `http://127.0.0.1:18768/status` 返回 `agent_connected: true`。真机部署再使用 `../iphone-app/scripts/run-device.sh` 验证 App 连接。

日常刷新真机 App 默认复用运行中的 Relay 与 Mac Agent。只有后端二进制、配置或运行时发生变化时，才从 Mac 桌面或终端执行：

```bash
../iphone-app/scripts/run-device.sh --restart-stack
```

## 常见问题：缺少 codex-code-mode-host

症状：iPhone 发起任务后收到类似错误：

```text
代码执行器仍缺少 /Users/<user>/.local/bin/codex-code-mode-host
```

原因：旧启动逻辑将 `command -v codex` 返回的符号链接直接交给 App Server，导致它在链接目录而不是真实 App Resources 目录查找配套 Host。

当前版本会自动展开所有符号链接。快速确认：

```bash
ls -l "$(command -v codex)"
./dev iphone
ps -axo command= | grep '[c]odex app-server'
```

进程命令应使用 App bundle 内的真实 `codex` 路径。不要通过创建额外的 `~/.local/bin/codex-code-mode-host` 链接长期规避；该链接可能在 App 升级后指向过期文件。

如果 App bundle 内确实没有配套 Host，更新或重新安装 ChatGPT/Codex App，然后重新运行 `./dev iphone`。只重启桌面 App 不能修复 Mac Agent 已使用的错误路径。

## 显式选择 Codex 安装

机器上存在多个 Codex 安装时，指定真实可执行文件：

```bash
CODEX_BINARY=/Applications/ChatGPT.app/Contents/Resources/codex ./dev iphone
```

脚本仍会规范化该路径并执行相同校验。独立 CLI 不使用 App bundle 布局时，不强制要求同目录存在 `codex-code-mode-host`，但 Mac Agent 仍会验证 CLI 路径存在且可执行。

## 常见问题：iPhone 任务无法控制 Mac 或刷新真机

症状：从 iPhone 发起的任务可以读取或修改项目文件，但运行 `ps`、写入 `~/Library`、访问 Xcode CoreDevice 服务或刷新真机时出现 `Operation not permitted`、设备服务超时或缓存目录权限错误。

影响范围：需要宿主进程控制、用户 Library 写入、网络访问或 Xcode 真机控制的远程任务。普通的项目内代码修改不受影响。

已验证根因：旧版 Mac Agent 创建远程 Codex Thread 时固定请求 `workspace-write` 沙箱和 `never` 授权策略。新版默认仍保持该边界，但 iPhone 可以在任务提交前选择 App Server 对当前项目返回且管理策略允许的 permission profile。

快速检查：确认 iPhone 输入区出现执行档位，默认是“工作区权限”；选择“完整权限”后顶部应显示解锁警示。协议调试时先发送 `execution.profile.list`，确认 `execution.profile.snapshot` 中目标 ID 的 `allowed=true`，再检查 `turn.start.permission_profile_id`。Agent 产生的 `turn.rejected` 仍包含 `execution_context`。

恢复步骤：普通项目修改使用“工作区权限”。需要宿主机或真机控制时，仅在可信 Relay 网络中选择 App Server 明确允许的“完整权限”；若该档位未返回或 `allowed=false`，由 Mac 上的完整权限 Codex 会话执行对应操作。不要通过扩大工作区根目录、伪造 profile ID 或创建额外缓存链接规避策略。

预防与验证：Agent 仅接受其按可信项目路径从 `permissionProfile/list` 得到且 allowed 的 ID；iPhone 不发送路径或自定义沙箱结构。profile 必须同时传入 `thread/start|resume` 与 `turn/start`，并与 legacy `sandbox` 互斥。修改权限协商时，必须同步更新 App Server schema 检查、协议 fixture、三端测试、iPhone UI、真机 Skill 和本条目。

## 常见问题：iPhone 刷新任务停止输出且 Agent 离线

症状：从 iPhone Turn 请求“刷新真机 App”后，只看到准备刷新提示；随后输出停止，界面长期保持运行中，Relay `/status` 显示 `app_connected: true`、`agent_connected: false`。

影响范围：由 iPhone profile Mac Agent 承载、同时调用旧版 `run-device.sh` 的真机维护 Turn。Mac 桌面 Codex 或终端不受影响，因为它们不是被脚本停止的目标服务。

已验证根因：旧脚本默认调用 `mac-agent/dev iphone`。该命令通过 `launchctl remove com.ai-coding-remote.mac-agent.iphone` 停止了承载当前 Codex App Server 和 Turn 的父进程，导致脚本、Turn 和流式通道一起中断。这不是 iPhone WebSocket 先断开，也不是权限拒绝。

快速检查：Relay 日志会在同一时刻记录 Agent 断开；macOS unified log 可看到 `launchctl remove` 和 Agent 收到 SIGTERM；Codex rollout 只停留在 `run-device.sh` 工具调用，没有工具返回或 `task_complete`。

恢复步骤：从 Mac 桌面或终端使用 `./dev iphone` 恢复 Agent。更新后的日常部署直接运行 `../iphone-app/scripts/run-device.sh`，它默认复用现有服务；只有外部 Mac 会话可以显式传入 `--restart-stack`。

预防机制：脚本沿父进程链识别 iPhone Agent，拒绝同步自重启，并自动采用 deferred 模式。它先完成预检和构建，等待 iPhone 应用终态并发送 `turn.acknowledged`，再由独立 `launchd` 任务安装、终止旧 App、启动新 App。固定延迟不作为交付保证；失败、超时和结果记录在 `iphone-app/.build/device-refresh/`。

验证证据：Shell 回归测试覆盖默认不重启、自重启拒绝、ACK 前不安装和 ACK 后安装回连；Relay 与 Mac Agent 全量 Go 测试覆盖消息方向、状态快照和 ACK 接收；iPhone target 构建验证终态模型与 ACK 编码。

## 常见问题：异步真机刷新成功后仍反复重启

症状：延迟刷新已经显示 `completed`，App 也重新连接 Relay，但真机仍约每 10 秒被覆盖安装并重新启动；同一个 `.build/device-refresh/<job-id>.log` 重复出现 `Installing`、`Launching` 和 `iPhone app connected to Relay`。

影响范围：使用旧版延迟刷新脚本创建的 `com.ai-coding-remote.device-refresh.*` 作业。Relay、Mac Agent 和普通外部同步部署本身不受影响，但持续覆盖安装会中断真机操作。

已验证根因：旧脚本使用 `launchctl submit` 承载一个几秒内成功退出的辅助脚本。实际作业被标记为 `keepalive`，`launchctl print` 显示 `minimum runtime = 10`；辅助脚本写入 `completed` 后没有卸载作业，`launchd` 因而节流后再次拉起同一个安装命令。状态文件的业务终态不会自动改变 `launchd` 的生命周期。

快速检查：运行 `launchctl list | rg 'com\.ai-coding-remote\.device-refresh'`，再对精确 label 执行 `launchctl print "gui/$(id -u)/<label>"`。若输出包含 `properties = keepalive`、`runs` 持续增加，且同一日志重复安装，即命中此问题。

恢复步骤：对确认失控的精确 label 执行 `launchctl remove <label>`，然后再次确认 `launchctl list` 不再包含它。不要批量停止 Relay 或 Mac Agent，也不要删除其他刷新任务来替代精确卸载。

预防机制：延迟刷新使用临时 plist `bootstrap` 一个没有 `KeepAlive` 的作业，再通过 `kickstart` 明确执行一次；辅助脚本在成功、失败和取消路径结束时卸载该作业。禁止在该一次性工作流中恢复使用 `launchctl submit`，也不能用固定等待延长进程寿命来规避重启。

验证证据：Shell 回归测试检查 plist 不包含 `KeepAlive`、ACK 前不接触设备、ACK 后只有一次安装和启动，并确认终态触发 `bootout`。真实维护验证还应确认完成日志只有一组安装/启动记录，且精确 label 已从 `launchctl` 消失。

## 更新维护规则

当 ChatGPT/Codex 升级改变可执行文件布局、App Server 参数或配套进程时，同时更新：

1. `internal/codexapp/process.go` 的启动前置检查及其测试；
2. `dev` 的安装发现与启动检查；
3. 本维修手册和根 `README.md` 的启动说明；
4. iPhone 仓库的 `$run-codex-remote-on-iphone` skill；
5. `CHANGELOG.md` 的 Unreleased 记录。

以当前官方 `codex app-server --help` 和 OpenAI App Server 文档为准，不根据旧路径或历史错误信息推测新安装布局。

## 日志与 Admin Platform 演进检查

当 Mac Agent 日志字段、profile 日志路径、轮转方式或启动 label 发生变化时，同时更新：

1. 日志 Schema 与字段 allowlist；
2. Diagnostics Collector 的文件匹配、轮转和确认游标测试；
3. 本地 Admin 查询索引与线上 SLS/LoongCollector 配置；
4. `README.md`、本维修手册和 `CHANGELOG.md`；
5. 跨组件事故关联字段的端到端测试。

Collector 或 Admin 停止不应影响 Mac Agent 执行。排查采集故障时只能检查和恢复精确的 Collector 任务，不得通过重启 Relay、Mac Agent 或 iPhone App 隐式修复日志链路。CoreDevice 任务由独立 Collector 执行；Mac Agent 不应添加可从管理页面传入 Shell 或任意路径的维护接口。

## 故障沉淀判定

每次解决非简单故障后进行一次维护分流。满足下列任一条件时，应沉淀而不是只结束当前修复：

- 可能在其他 Mac、重新安装或升级后再次出现；
- 根因不直观，单看最终错误容易判断错误；
- 已经造成重复修改、重复构建或较长排查时间；
- 需要人工按固定顺序恢复；
- 可能让远程任务静默失败、连接错误或产生错误操作。

按问题性质选择落点：可自动发现的前置条件加入启动检查；已修复的行为加入回归测试；安装和升级条件加入检查清单；需要人工诊断或恢复的内容加入本手册；跨模块工程约束加入根 `AGENTS.md`；AI 执行流程加入对应 skill。

维修条目至少说明症状、影响范围、已验证根因、快速检查、恢复步骤、预防机制和验证结果。新方案替代旧方案时直接修订旧条目，避免保留互相冲突的临时办法。一次性外部故障或显而易见的操作失误可以不记录，除非它暴露了可复用的工程约束。
