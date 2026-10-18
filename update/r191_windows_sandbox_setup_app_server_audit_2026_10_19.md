# Round 748: Windows sandbox setup routed through the app server

Rust 依据：`cebdb732eacfeae9823be93aad273c242428b8a0`（`#44945`）。

Go 已对齐：

- embedded 与 remote TUI 的 setup 均调用 app-server
  `windowsSandbox/setupStart`；
- 本地 readiness 通过 app server 查询，不直接执行原 setup 路径；
- completion 按 initiating mode 匹配，持久化并应用有效 mode；
- fallback、requirements、status 与 setup-active 线程切换守卫均有 tea model
  处理。

验证：

- `go test ./app -run 'WindowsSandbox' -count=1`
- `go test ./tui/tea -run 'WindowsSandbox' -count=1`
