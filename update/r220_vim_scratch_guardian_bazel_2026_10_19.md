# Vim buffer jump、macOS scratch 与 Guardian computer-use scoring

本轮关闭：

- `74772623db` Stop overriding Rust debuginfo in Bazel CI (#40864) — N/A
- `d47e5cc0e2` Add Vim buffer jump motions (#40958)
- `7f82397363` Restrict macOS scratch access to process sandboxes (#40961)
- `daa3eaf10f` Allow Guardian scoring for required computer-use models (#40967)

## 结论

- `tui/tea` 已实现 `gg`/`G` buffer jump motions，并通过 grapheme-aware 行边界和
  operator range 覆盖。
- Seatbelt 的 `/tmp`、`/var/tmp`、`/private/tmp`、`/private/var/tmp` scratch
  授权仅进入 process sandbox，并经过排除路径排序。
- Guardian scoring 使用模型 `ComputerUseReviewRequired`/review mode，不再拒绝
  required computer-use 模型。
- `#40864` 是 Rust Bazel CI 配置，Go 无 Bazel 工具链载体。

## 验证

- `go test ./tui/tea -run 'VimBufferJump' -count=1`
- `go test ./sandbox -run 'Seatbelt' -count=1`
- `go test ./model -run 'ComputerUse|Guardian' -count=1`
- `go test ./appserver -run 'Guardian.*Computer|Guardian.*Score' -count=1`
