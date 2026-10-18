# `#45837` Hide WSLg's duplicate root in restricted Linux sandboxes

Rust 依据：`8ece31a7bf849ddb1d1f01aa72033cd106a1c2ba`。

## 落地内容

- `wslgDuplicateRootPresent` 通过 `symlink_metadata("/mnt/wslg/distro")` 与 `/`
  的 device/inode 比较识别 duplicate root；路径不存在或不是目录时不处理。
- restricted filesystem 构建前拒绝以下路径使用 WSLg alias：
  - readable roots 与 writable roots；
  - sandbox policy cwd 与 command cwd；
  - 含 `/` 的命令可执行文件（相对路径按 command cwd 解析）；
  - symlink 解析后落到 alias 下的路径同样拒绝。
- 在所有 filesystem grants/denials 之后追加：
  `--perms 000 --tmpfs /mnt/wslg/distro --remount-ro /mnt/wslg/distro`。
- 若已有 deny-read ancestor 覆盖该目录，则不再为子目录创建 mount。
- 当没有 fresh procfs（`NoProc`）且 `/run/WSL` 未提供已有 mask 时，追加
  `--tmpfs /proc`，避免 `/proc/<host-pid>/root` 恢复被遮蔽的视图。
- full-disk-write 策略不执行 WSLg 检测或 mask。

## 验证

- `GOOS=linux GOARCH=amd64 go build ./...`
- `GOOS=linux GOARCH=amd64 go test -c ./sandbox/linuxsandbox`

新增覆盖：

- `TestEnsureNoWSLGDistroAliasLikeRust`
- `TestWSLGMaskArgsOrderingLikeRust`

本机是 Windows，WSLg 真实 mount namespace 集成不可执行；代码与测试二进制以
Linux 交叉编译验证。
