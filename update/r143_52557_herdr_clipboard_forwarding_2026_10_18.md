# `#52557` Herdr clipboard forwarding

- Rust 依据：`5fe4fc8f7cd16688b5c661d8cdb76e2a340b046d`。
- `ClipboardEnvironment` 新增 `HerdrSession`，由 `HERDR_ENV=1` 识别；
  tmux、Herdr、SSH 都触发终端转发。
- native 复制始终先执行；成功时仍发送 OSC 52，并保留 native lease；
  native 失败时回退到 tmux/OSC 52。
- TUI 的 `/copy` 等异步复制路径接入终端转发：Unix 优先写
  `/dev/tty`，不可用时写 stdout，tmux 下使用 passthrough 包装。
- 复制环境与转发函数可通过 `Options` 注入，测试覆盖 native 成功后的
  Herdr 转发和 native 失败回退。

验证：

- `go test ./tui/... -count=1`
- `go vet ./tui/...`
