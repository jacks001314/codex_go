# `#53753` / `#49112` X11 PRIMARY copy-on-select

- Rust 依据：`3517671c6061e1704cf1c0df4a5039de23514df8`、
  `01964952881af1c1e68c8d02d16d373e1b33a6e2`。
- composer/footer copy-on-select 现在同时写 CLIPBOARD 与 PRIMARY；显式
  Ctrl+C/Ctrl+Insert 只写 CLIPBOARD。
- 新增 `OnPrimarySelectionWrite/Read`，Linux 使用
  `wl-copy/wl-paste`、`xclip`、`xsel` 依次回退；其他平台返回不可用。
- 无修饰 middle-click 异步读取 PRIMARY 并插入 composer；modal/popup/
  read-only 状态下不触发。
- helper 进程存活期间由其持有 PRIMARY ownership。
- 成功发布后记录 ownership；focus loss 时通过 release command 清理，thread
  resume 时异步 best-effort 清理，不阻塞 update loop。

剩余：

- 平台 helper 可用性与 ownership 的真实 Linux 集成测试。

验证：

- `go test ./tui/tea -run 'PrimarySelection|ComposerCopyOnSelect|MiddleClick' -count=1`
- `go test ./tui/... -count=1`
- `go vet ./app ./tui ./tui/tea`
