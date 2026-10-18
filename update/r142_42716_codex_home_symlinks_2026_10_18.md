# `#42716` Allow trusted symlinks beneath CODEX_HOME on macOS

- Rust 依据：`d13aeb77ea0eb72a994324143e6cdffcba650963`。
- 配置：`allow_symlinked_codex_home` 只从顶层 user config 读取，默认关闭；
  project config、CLI overrides、profile 层不能开启。`CODEX_HOME`/`GCODE_HOME`
  别名只在解析到同一 home 时保留，否则回落为 canonical home。
- Seatbelt：`normalizeSeatbeltWritableRoot` 默认拒绝深度二以上的 symlink
  component，并输出包含 opt-out 风险说明的错误；opt-in 时解析当前目标，
  但只覆盖位于该 home 内或其下的 writable root。同步补上 Rust `#39599`
  的 writable-root anchor deny。
- 传播：`CommandRunRequest`、`ShellRequest`/`ShellValidationOptions`、
  `ApplyPatchExecutorOptions`、app-server turn 与 `command/exec`、
  `codex sandbox`、exec-server process/fs-helper、remote executor 和
  session handoff 均携带 host 解析后的值。`--ignore-user-config` 的 exec
  路径不会启用该例外。
- 本地 `apply_patch` 在 macOS 上复用同一 writable-root 校验，避免 shell
  被拒绝后 patch 仍直接写穿 symlink。

验证：

- `go test ./config -run 'AllowedSymlinked' -count=1`
- `go test ./sandbox ./tool ./execserver -run 'AllowedSymlinked|Seatbelt|WritableRoot|PrepareExecProcess|SessionServerCarries' -count=1`
- `go test ./appserver ./app ./exec -run '^$' -count=1`
- `go test ./parity -run 'ConfigSchemaSurface' -count=1`

限制：当前主机为 Windows，未执行真实 macOS Seatbelt 集成；Linux/WSL
沙箱集成同样不可用。
