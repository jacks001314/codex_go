# Round 734: paginated task stop and world-writable scan telemetry

## `#40787` Respect paginated history when stopping background tasks

Rust 依据：`37f4bb94c9f4e180535a12e3f2c2f93f4a773df0`。

Go 的 `remoteAgentsDashboardSource.Stop` 已按历史模式选择 turn 来源：

- 先通过 `thread/read`（`includeTurns=false`）读取线程摘要；
- paginated thread 使用 `thread/turns/list`，legacy thread 使用内嵌 turns；
- 旧 server 拒绝 turns/list 时回退到 `includeTurns=true`；
- 只中断选中的首个 `inProgress` turn，空闲线程不发送 `turn/interrupt`。

验证：

- `go test ./app -run TestRemoteAgentsStopRespectsPaginatedHistoryLikeRust -count=1`

## `#40983` Record Windows world-writable scan telemetry

Rust 依据：`37a5149822296c52414ccf8ad3ca00bfb7424260`。

`ApplyWorldWritableScanAndDenies` 现在为每次实际扫描记录：

- metric：`codex.windows_sandbox.world_writable_scan_flagged_directories`
- 成功：`WorldWritableAuditResult.FlaggedCount`，tag `result=success`
- 扫描或应用 deny ACE 失败：0，tag `result=error`

没有安装全局 recorder 时保持 no-op，与 Go 其他全局 metric 一致。

验证：

- `go test ./sandbox/windowssandbox -run TestWorldWritableScanTelemetryLikeRust -count=1`
- `go test ./sandbox/windowssandbox -count=1`
- `go vet ./sandbox/windowssandbox`
- `go test ./parity -count=1`

`go test ./app -count=1` 仍受当前 Windows 环境下的既有测试失败影响（非绝对路径、
daemon socket 私有 ACL、云端测试端点等），与本轮改动无关；相关定向测试通过。
