# 验证门禁（2026-11-14，HEAD e3dc6e27 之后）

- `go vet ./...`：退出 0，日志 `vet_all_2026_11_14.log`。
- `go test ./tui/... ./state ./mcp -count=1`：退出 0，43 个包结果（含无测试文件的包）全部 ok，无 FAIL，日志 `broad_test_2026_11_14.log`。tui 30.251s、agents_overview 4.214s、analytics 3.251s、app 0.762s 等。
- 说明：这是当前树的验证门禁，不包含 appserver（其 250s 全包结果见上一轮 `mcp_startup_notify_2026_11_14.log`）与 Linux 原生 seccomp 项。
