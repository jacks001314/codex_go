# Round 746: startup presentation and local-settings decoupling audit

Rust 依据：`7b88d09d61b862e229832e446c87863ade890b48`（`#49875`）。

Go 已有独立的 client-owned LocalSettings 生命周期：

- `interactiveLocalSettings` 将 TUI 偏好与执行配置分开暂存；
- `interactiveNewThreadLocalSettings` / `ReloadedLocalSettings` 在 reload 失败时
  保留 live 值；
- trust check 仅在确实发生 reload 时替换 staged record；
- `applyLocalSettings` 只应用呈现相关字段，不把执行配置重新灌入 Model。

验证：

- `go test ./app -run 'InteractiveLocalSettings|NewThreadLocalSettings|LocalSettings' -count=1`
- `go test ./tui/tea -run 'LocalSettings' -count=1`
