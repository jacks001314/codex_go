# Agent 树挂起与恢复核查（#61198）

日期：2026-11-14
上游提交：`fae87361f44705ac524b7ce950ee531920779533` Add graceful agent-tree suspension and recovery (#61198)

## 上游接口与调用方

- 新 API：`ThreadManager::suspend_agent_tree`（停止本地受控树、关闭 writer、返回根与命名 V2 子线程中未完成的 turn id 以及待唤醒的根回合）、`ThreadManager::load_suspended_agent`（按记录的祖先恢复命名 V2 子线程，不启动模型回合）。
- core-api 导出 `SuspendedAgentTree`、`LoadedSuspendedAgent`。
- 仓库内调用方只有 core 集成测试（`core/tests/suite/abort_tasks.rs`、`host_turn_admission.rs`），app-server 与 CLI 没有生产调用路径。
- 同提交的语义还包括：挂起成功前把终端子结果持久化到父历史（含父已被驱逐的情况）；mailbox 在驱逐期间保持消息顺序；`continue_turn_if_idle` 允许空输入靠已保存结果唤醒，并以先前往返 id 作守卫。

## Go 现状映射

- 树关闭：`runtimeAgentController.CloseAgent` 用 `SpawnGraph` 列出开放后代并逐个关停，`agent.AgentTreeShutdownState` 记录清理失败报告。这是关闭语义，不保留线程供恢复。
- 回合管理：`ThreadManager` 维护每线程活动回合（`ReserveTurn`/`RegisterTurn`/`ActiveTurn`/`ConsumeTurn`），带 cancel；没有挂起/恢复 API。
- 跨进程恢复：`daemonrecovery` 快照 + `continueDaemonTurn` 覆盖未完成根回合的恢复（重读 rollout、校验未完成与权限一致、写 `TurnAborted`、以 `daemon_recovery` 触发器继续），并有对应回归 `daemon_recovery_continue_test.go`。
- 身份恢复：`ResumeAgent` 在 `ThreadNotFound` 时用 `threadRecord` 从 rollout 恢复记录并注册 agent 路径；空闲驱逐/冷恢复由 idle resume 路径覆盖。
- 排队消息：`r.agentMessages` 按线程保存队列，空闲根由 `maybeWakeRootForPendingAgentMessages` 唤醒；队列只在进程内存中。

## 结论与缺口

- Go 已有“未完成回合恢复 + 线程身份恢复”的能力组合，但没有 Rust 的 embedder 级 `suspend_agent_tree` / `load_suspended_agent` 成对 API；Rust 侧这两个 API 也只有测试调用，因此对 app-server/CLI 行为没有直接差异。
- 真实差异在挂起边界：Rust 在挂起成功前把终端子结果写入父历史；Go 的子结果进入父线程的进程内队列，跨进程挂起/重启会丢失尚未投递的子结果。Go 的 daemon recovery 只恢复被中断的根回合，不重放这些未投递结果。
- 若后续需要 embedder 级挂起，需要在 agent controller 上新增：中断树内活动回合并返回 turn id、保留线程记录与队列、按 `threadRecord`/registry 恢复身份，以及在挂起前持久化未投递子结果。

## 本轮验证

```
go test ./execserver ./realtime ./app ./mcp ./tui/tea -count=1
ok  codex_go/execserver  22.874s
ok  codex_go/realtime    4.606s
ok  codex_go/app         42.395s
ok  codex_go/mcp         31.291s
ok  codex_go/tui/tea     48.321s
```