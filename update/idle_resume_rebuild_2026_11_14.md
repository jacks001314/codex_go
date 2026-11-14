# 无订阅者 idle reload 重建（#61136 关闭补齐，2026-11-14）

## 契约

Rust `thread_processor.rs` 在 resume 一个已加载线程时：

1. 用 `collect_resume_override_mismatches` 比较 model、model_provider、service_tier、cwd、runtime_workspace_roots、approval_policy、approvals_reviewer、sandbox 标签、permissions（有就视为不匹配）、personality、config、base/developer instructions。
2. 仅当存在不匹配、线程可接受直接输入（排除 parent-owned V2 子线程）、无订阅者、状态 Idle、非 Running、无 active turn 时，先等待线程优雅关闭；关闭完成后移除线程并重新读取 rollout 做 cold resume。
3. 关闭失败（SubmitFailed/TimedOut）只告警，保留 live 线程，resume 继续但忽略覆盖。
4. 无订阅者之外的其它情况保持 live 运行时，覆盖被忽略。

## Go 实现

新增 `appserver/idle_resume_reload.go`，并接入 `thread/resume` 处理序列（在 `prepareSubscribedIdleResume` 之前）：

- `resumeOverrideMismatchDetails`：逐项比较上述字段；sandbox 复用 `resumeSandboxLabelMatches`（external 兼容 danger-full-access）；`permissions` 出现即不匹配；cwd/roots 用路径归一比较。
- `prepareUnsubscribedIdleResume`：只在 `threadIsLoaded && activeTurn==nil && Subscribers==0` 时判定；parent-owned V2 子线程的覆盖已在更早的 `prepareParentOwnedV2ChildResume` 中剥离，因此不会触发重建。
- `reloadIdleThreadForResume`：在 `idleLifecycle.pending` 上登记关闭中标记（阻止并发 resume 与定时驱逐），先关闭 rollout writer（失败则告警并保留线程），再关闭 code-mode/MCP/managed network，最后 `markThreadUnloaded` 与 `ReleaseLiveThreads`。
- `ThreadManager.CloseLiveThreadsFor`：在仍登记于 manager 时关闭 writer 并返回首个错误，供上面的关闭结果判定使用；原有 `ReleaseLiveThreads` 仍是“移除后关闭”。

## 回归

`appserver/idle_resume_reload_test.go`：

- 同名 resume 携带匹配 sandbox：live 线程保持不变。
- 携带 `workspace-write` 而 live 为 read-only：线程被卸载，随后真实 resume 冷启动并报告 workspace-write。
- 携带 named `permissions`：视为不匹配并重建。
- 有订阅者：保持 live 线程。

命令与结果：

- `go test ./appserver -run 'TestUnsubscribedIdleResume|TestSubscribedIdleResume' -count=1` 通过。
- `go vet ./appserver` 退出 0。
- `go test ./appserver -count=1` 全包通过，日志 `idle_reload_full_2026_11_14.log`。
