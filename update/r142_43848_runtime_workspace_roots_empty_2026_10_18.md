# `#43848` runtime workspace roots empty semantics

- Rust 依据：`6515a72db7a82e8cdebed940aad4ba1a159ce245`。
- `runtimeWorkspaceRoots` 现在区分 omitted（nil）与 explicit empty（`[]`）：
  - omitted：恢复保存的 roots；
  - explicit empty：清除额外 roots，仅保留 effective cwd。
- cwd 变化时 retarget 保存的旧 cwd root，保留 additional roots 并去重；
  resume/settings update/turn-start persistence 使用同一规则。
- 剩余 partial：完整 restored-host path validation 与 resume/revert
  checkpoint 语义尚未完全覆盖。

验证：

- `go test ./appserver -run 'RuntimeWorkspaceRoots|RuntimeWorkspaceRootsEmptyVsOmitted' -count=1`
