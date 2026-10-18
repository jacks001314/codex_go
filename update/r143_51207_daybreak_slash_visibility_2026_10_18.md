# `#51207` Daybreak slash visibility and dispatch gate

- Rust 依据：`5ad689169645a953ec7380762c4ae596712057ee`。
- `BuiltinCommandFlags` / `CommandPopupFlags` 新增
  `DaybreakCommandDescription *string`：nil 时 `/daybreak` 不进入 popup，
  非 nil 时覆盖该行描述（Rust
  `BuiltinCommandFlags::daybreak_command_description`）。
- `find_builtin_command` 仍把 `/daybreak` 解析为可执行命令；特性关闭时
  dispatch 静默返回，不再产生 Go 本地错误消息。
- 描述文案按 Rust 状态选择：开启时为 Disable，catalog 支持时为 Enable，
  catalog 明确不支持时为 Learn，元数据未知时为 Manage；账户不符合条件时
  隐藏。
- `cli_daybreak` 在 `/experimental` 切换或设置 reload 时重新应用实时偏好并
  刷新 status surfaces。
- `daybreak_red` 与 `daybreak_blue` 都视为 Daybreak 支持；选择时优先
  `daybreak_blue`，其次 `daybreak_red`。

验证：

- `go test ./tui/... -count=1`
- `go vet ./tui/...`
