# Round 677：filesystem read/write sandbox selection（#46122）

Rust 依据：`a4ee536f0132092f20996c3810c14d090e4dded1`。

## 已落地

- `execserver` filesystem helper 新增按 operation 的 sandbox 选择：
  - readFile/getMetadata/canonicalize/readDirectory/walk 在存在 deny-read entries
    或读权限受限时 sandbox；
  - writeFile/createDirectory/remove/copy 在写权限受限时 sandbox；
  - full-disk-write policy 不再让 read 操作绕过 deny-read；
  - full-disk-read + restricted-write policy 的 permitted reads 可以直连。
- `fs/open` 按 read/replace 模式选择对应 operation。
- `TestFSOperationSandboxSelectionSeparatesReadsAndWritesLikeRust` 覆盖
  full-write+deny-read、restricted-write+full-read 与 disabled profile。

## 剩余

- capability discovery 与 skill resource reads 尚未走 read-specific sandbox
  selection。
- 尚未实现 executor path convention / `:slash_tmp` deny-aware 的 full-disk
  read 判定，也没有 Rust 的 `should_read_from_sandbox` /
  `should_write_into_sandbox` 公共 API。

## 验证

```text
go test ./execserver -run 'FSOperationSandboxSelection|SandboxOverride' -count=1
go test ./execserver -run 'FS|SandboxOverride|Filesystem' -count=1
go vet ./execserver
```
