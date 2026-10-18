# Round 722: stdin review active-grants audit

`#54119` 要求在 granular sandbox approval 关闭时，如果终端保留权限仍与当前
environment 的 active grants 完全一致，则允许 `write_stdin` 进入 review；仍要求
每次输入 review，并在 grant 过期或 approval policy 变为 Never 时拒绝。

Go 现状：

- `writeStdinApproval` 对 retained additional permissions 会进入 hooks /
  Guardian / user review，并拒绝 NUL 与超限输入；
- app-server 没有 per-environment active granted-permissions 集合，也没有
  filesystem grant 顺序比较，因此没有 Rust 的 `additional_permissions_preapproved`
  状态可匹配；
- 该提交的 grant-expiry 预批准分支无 Go 载体，记为 `not_applicable`。

验证：

- `go test ./appserver -run 'WriteStdin|StdinReview|TerminalWrite' -count=1`
