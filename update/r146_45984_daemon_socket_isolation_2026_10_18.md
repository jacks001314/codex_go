# `#45984` Isolate app-server Unix sockets from filesystem-restricted commands

Rust 依据：`49305d74b4c0048ee44702e3dd058e08ad3b4a51`。

## 落地内容

- `codexuds` 新增固定 rendezvous 根：
  `canonicalize(/tmp)/codex-daemon-<euid>`。创建时使用 0700，并校验
  `symlink_metadata` 的所有者、目录类型与权限；不依赖 HOME/TMPDIR/CODEX_HOME。
- app-server Unix listener 不再直接在广告路径 bind：
  - 广告路径的 parent 保留既有权限，但必须由当前用户或 root 拥有，且不能被
    其他用户替换（group/other writable 且非 sticky 时拒绝）；
  - 物理 socket 路径 = shared dir + `sha256(canonical parent/name)`；
  - 广告路径发布为指向物理 socket 的确定性 symlink；
  - 仅删除自己的 symlink 或已失效的旧 socket，任意其他文件/外部 symlink 拒绝；
  - 退出时只清理自己的 symlink，不删除被替换的广告路径。
- `verifiedLocalDaemonSocket` 现在同时要求广告路径是确定性 symlink、shared dir
  为用户所有且 0700，直接绑定在广告路径上的 SSH/socat forward 不再被认可。
- macOS Seatbelt 在 Unix-socket allowlist 之后追加：
  - `deny file-read* file-write*`（literal + subpath）；
  - `deny network-outbound (remote unix-socket ...)`；
  - shared dir 各祖先目录的 directory unlink deny，防止 rename 绕过。
- Linux bubblewrap 在所有 bind 之后 `--tmpfs` shared dir，并额外遮蔽
  `/mnt/wslg/distro` 下的同一路径；解析 `/proc/self/mountinfo`，拒绝该目录下的
  nested mount 和 root 指向该目录的 bind alias（WSLg 视图除外）。
- `use_legacy_landlock` 下拒绝任何 filesystem-restricted policy，错误提示要求
  使用 bubblewrap；full-disk-write 仍是唯一允许 legacy Landlock 的形态。

## 验证

- `GOOS=linux GOARCH=amd64 go build ./...`
- `GOOS=darwin GOARCH=arm64 go build ./...`
- `GOOS=linux GOARCH=amd64 go test -c ./appserver`
- `GOOS=linux GOARCH=amd64 go test -c ./sandbox/linuxsandbox`
- `GOOS=darwin GOARCH=arm64 go test -c ./sandbox`
- `go test ./codexuds ./appserver -run 'UnixSocket|Protected|VerifiedLocalDaemon' -count=1`

新增覆盖：

- `TestProtectedUnixSocketPathIsDeterministic`
- `TestPrepareUnixSocketRendezvousRemovesOwnedAlias`
- `TestPrepareUnixSocketRendezvousRejectsForeignSymlink`
- `TestVerifiedLocalDaemonSocketRequiresProtectedAlias`
- `TestVerifiedLocalDaemonSocketRejectsUnsafeDirectory`
- `TestSeatbeltDeniesDaemonSocketDirectoryLikeRust`
- `TestValidateDaemonSocketMountIsolationLikeRust`
- `TestValidateDaemonSocketMountIsolationRejectsMalformedMountinfo`

本机为 Windows，Linux bwrap/macOS Seatbelt 的真实运行集成不可执行；对应代码以
Linux/Darwin 交叉编译和测试二进制编译验证。
