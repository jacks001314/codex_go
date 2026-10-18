# `#41209` executor-convention deny-read matching

- Rust 依据：`34e74fda0eb05ce918b6ce8778857807bc9b4a6a`。
- Permission path 与 glob 的 materialization/containment 现在按 executor
  convention 处理，而不是按 Go host 的 `runtime.GOOS`：
  - POSIX：大小写敏感、slash 分隔；
  - Windows：折叠大小写、统一 slash/backslash；
  - 未知 convention：fail closed。
- `sandbox.ResolveFileSystemEntryAccessForConvention` 供 integrity matcher
  使用；`ReadDenyMatcher` 对未知或不兼容的候选 convention 返回 denied。
- 保留 PathUri decoded-byte identity、executor-home `~` 展开、alias +
  canonical 双重检查。

验证：

- `go test ./sandbox ./sandbox/integrity -run 'Convention|DenyMatcher|Permission' -count=1`
- `go test ./sandbox ./sandbox/integrity -count=1`
- `go build ./...`
