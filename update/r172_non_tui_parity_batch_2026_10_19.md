# Round 729: non-TUI parity backfill batch

本轮复核并回写 9 个非 TUI 提交：6 个已有等价实现，3 个在 Go 架构中无载体。

## Equivalent implementations

- `#51794` / `87be737b66`：Bedrock Mantle/Runtime GPT-6.1 Sol 广告
  `ultrafast`，默认 tier 仍为空。
- `#52234` / `54685110a7`：environment/add 的 `websocketRequestId` 仅在
  wss/loopback executor 上转发为 `X-Request-ID`，registration 重连保留该值，
  exec-server upgrade response 回显。
- `#52245` / `c9e0fffbbc`：skills list/read、get_context_remaining、plugin catalog
  与 multi-agent list_agents 为 parallel；mutation/goal/plan/request_user_input
  保持串行。
- `#52278` / `2c3156adf4`：analytics consent 只 gate 内置 statsig metrics；
  显式 OTLP HTTP/gRPC exporter 继续工作，`none` 才关闭 metrics。
- `#53369` / `6c372fbef4`：message board 不可用时 turn 继续；相关工具不广告，
  调用返回 model-visible 错误，且不创建 board database。
- `#53706` / `cb7091b180`：external-agent 术语迁移保留 URL、email、Markdown
  destination/title、bare www 与相对 `AGENTS.md` 引用。

## Architecture N/A

- `#53633` / `90f00bd89e`：Go 没有 async Guardian Decisions scorer 或 cached
  Decisions API-key 路径。
- `#53657` / `ab82af0b2c`：Rust hook clone 的 Arc/copy-on-write 分配优化无行为
  变化；Go clone 已复制 slices。
- `#54119` / `d292f72124`：Go 没有 per-environment active granted-permissions
  集合与 filesystem grant-order 比较；retained permissions 仍进入 review。

验证：

- `go test ./model -run Ultrafast -count=1`
- `go test ./appserver -run TestEnvironmentAddWebsocketRequestIDLikeRust -count=1`
- `go test ./execserver -run TestExecServerEchoesWebSocketRequestIDLikeRust -count=1`
- `go test ./turn -run Parallel -count=1`
- `go test ./tool -run Parallel -count=1`
- `go test ./agent -count=1`
- `go test ./otelinit -count=1`
- `go test ./appserver -run TestDisableDirectMessageAllowsUnavailableBoardLikeRust -count=1`
- `go test ./config -run ExternalRewrite -count=1`
- `go test ./appserver -run WriteStdin -count=1`
- `go test ./parity -count=1`
