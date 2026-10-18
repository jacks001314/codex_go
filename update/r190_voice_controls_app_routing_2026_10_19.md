# Round 747: TUI voice controls routed through the app

Rust 依据：`72ec34ee9aced612c4fac9a4b0acc49825324a7d`（`#47380`）。

Go 已对齐：

- F8 与配置化 mute shortcut 通过 TUI voice handler 分发；
- `/voice` start/stop/settings/mute 复用同一 handler；
- local 与 remote host 的 start/stop/append-speech 均走
  `thread/realtime/*` app-server RPC；
- voice 操作保留 composer draft；
- Go keymap 没有 Rust 的 chord-prefix 运行时，因此 chord 专属分派无载体，
  直接键行为已覆盖。

验证：

- `go test ./tui/tea -run 'Voice' -count=1`
- `go test ./app -run 'Voice' -count=1`
