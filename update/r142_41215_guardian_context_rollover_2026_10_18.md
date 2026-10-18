# `#41215` Guardian context rollover

- Rust 依据：`bae69125df2de0ee70b0599c7dae9fc34fb97f03`。
- reviewer 返回 `ErrorContextWindowExceeded` 时，Guardian session 清空
  previous/seeded response chain，并在同一 attempt budget 内重试。
- 下一次 prompt 会重建并重放完整 transcript、follow-up reminder、Node REPL
  policy 和 multimodal evidence，因此不依赖已失效的 delta cursor。
- Go 没有独立的 reviewer `Op::Compact` 或 persisted context-window status
  载体；reviewer 是进程内 model-agent session。

验证：

- `go test ./appserver -run 'ContextWindowErrorRollsOver|Guardian.*Retry|Guardian' -count=1`
