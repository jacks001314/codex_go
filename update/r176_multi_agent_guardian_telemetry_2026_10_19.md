# Round 733: multi-agent and Guardian telemetry audits

本轮确认 5 个 telemetry 提交均已有 Go 载体和回归覆盖。

- `#51330` / `f5fa209bb0`：`codex.guardian.decision.duration_ms` 计时覆盖完整
  approval decision，包括 fast decision、准备、模型 review 与 cancellation。
- `#51331` / `41acdad246`：`codex.multi_agent.result_delivery` 记录 queued /
  failed outcome，覆盖 parent 接受、mailbox delivery 与 delivery failure。
- `#51332` / `c2ae67d769`：`codex.multi_agent.wait.duration_ms` 按 wait 观察到的
  有界 outcome tag 记录；被丢弃的 wait 不产生记录。
- `#51333` / `5ddd19e8a9`：turn analytics 的 `multi_agent_version` 为
  `disabled` / `v1` / `v2`，telemetry 与 app-server turn event 一致。
- `#51334` / `79cae5f7fb`：`codex.guardian.denial_limit_reached` 在达到 turn
  denial limit 并中断 review 时递增一次；低于 limit 的 denial 与 approval 不计数。

验证：

- `go test ./appserver -run GuardianDecision -count=1`
- `go test ./appserver -run ResultDelivery -count=1`
- `go test ./appserver -run MultiAgentWait -count=1`
- `go test ./telemetry -run MultiAgentVersion -count=1`
- `go test ./appserver -run MultiAgentVersion -count=1`
- `go test ./appserver -run GuardianDenialLimit -count=1`
- `go test ./parity -count=1`
