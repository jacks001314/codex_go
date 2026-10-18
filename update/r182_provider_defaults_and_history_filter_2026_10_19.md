# Round 739: app-server provider defaults and history filtering

Rust 依据：`3226512d470d3a35f27e8b23369672bbec75e336`（`#49161`）。

Go 对齐内容：

- fresh local `thread/start` 只转发 CLI/profile/OSS 明确选择的
  `modelProvider`；普通 user/project 配置中的默认 provider 留给 app-server 解析。
- local session picker 按当前有效 provider 过滤历史。
- remote session picker 在没有显式 provider 时读取服务端 `config/read`，再用
  服务端有效 provider 过滤 `thread/list`。
- remote side fork 保留当前 session 的 `modelProvider`。

验证：

- `go test ./app -run 'TestLocalThreadStartForwardsOnlyExplicitModelProviderLikeRust|TestInteractiveSessionPickerFiltersByConfiguredProviderLikeRust|TestInteractiveRemoteSessionPickerItemsLoadFromAppServer|TestRemoteSideForkPreservesSessionProviderLikeRust' -count=1`
- `go vet ./app ./tui`
- `go test ./parity -count=1`
