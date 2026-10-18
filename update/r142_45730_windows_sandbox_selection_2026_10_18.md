# `#45730` executor Windows sandbox selection

- Rust 依据：`d4e11a9b97ce405c2e1172e3fbb5afc046f6589a`。
- `WindowsSandboxSelection` 表示 disabled / restricted-token / elevated /
  MXC；`windowsSandboxLevel` 继续作为兼容 wire 字段保留原值。
- `selectWindowsSandboxForExecutor` 同时用于 process launch 与 filesystem
  helper：MXC 直接选择 backend，FS helper 在无 native runner 时返回
  `native MXC is unavailable on this executor`。
- 非 Windows path convention 将选择降为 Disabled；capability discovery、
  skill resource reads 与普通 FS RPC 通过同一 `FileSystemSandboxContext`
  使用该选择。

验证：

- `go test ./execserver -run 'SelectWindowsSandbox|RejectsMXC|UnknownWindowsSandbox|FSOperation' -count=1`
- `go build ./...`
