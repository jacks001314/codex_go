# Round 749: restored permission profiles across `/cd`

Rust 依据：`c29bc99949f90687171611bb845c4c289fa85dde`（`#41192`）。

Go 的 `/cd` 通过 fork 替换 session：

- fork 复制完整 metadata，包括 `ApprovalPolicy`、`SandboxPolicy` 与
  `ActivePermissionProfile`；
- 随后只更新 replacement 的 CWD；
- 不重新从执行默认配置推导权限，因此 server-restored profile 得以保留。

验证：

- `go test ./app -run TestInteractiveWorkingDirectoryChangeHandlerForksAtNewCWD -count=1`
