# `#46122` filesystem read/write sandbox routing

- Rust 依据：`a4ee536f0132092f20996c3810c14d090e4dded1`。
- 读、写分别选择 sandbox；full-disk writes 下的 deny-read 仍会限制读，
  仅写受限时 full-disk read 可走直接路径。
- 新增 `PermissionProfile.HasDenyReadEntriesForConvention`：`:slash_tmp`
  在 POSIX 与未知 convention 下是有效读拒绝，在 Windows executor 上忽略；
  未知 convention 保持 fail-closed。
- `prepareFSSandboxForOperation` 从 cwd 推断 executor path convention，
  capability discovery、skill resource reads 与 FS RPC 共享同一
  read/write 路由；`fs/open` 继续按 read/replace 分流。

验证：

- `go test ./execserver -run 'FSOperationSandboxSelection' -count=1`
- `go test ./sandbox -run 'Permission|SlashTmp' -count=1`
- `go vet ./execserver`
