# Round 724: Tokio tracing and async Guardian classifier audits

- `#51775` Prevent recursive Tokio tracing in telemetry and SQLite logs：
  Go 使用 slog/OpenTelemetry，没有 Tokio tracing subscriber 或 SQLite log
  callback 重入 tracing 的路径，记为 `not_applicable`。
- `#53698` Interrupt and drain canceled classifier responses before socket reuse：
  Go 没有 async Guardian V2 classifier / classifier WebSocket session，Guardian
  是同步 model review，记为 `not_applicable`。
- `#53699` Cancel Guardian V2 classifications that exceed the tool-call lag
  window：Go 只有同步 score-progress lag 跟踪，没有 pending async score queue，
  记为 `not_applicable`。
- `#53733` Default Guardian v2 async classification to conversation mode：
  Go 没有 async classifier input-mode 配置，记为 `not_applicable`。

验证：

- `go test ./parity -count=1`
