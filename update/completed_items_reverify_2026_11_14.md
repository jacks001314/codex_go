# 已完成项复验与状态收口（2026-11-14）

计划表里有 11 条记录写着“已实现/已有等效实现”，但状态列没有标完成。本轮用当前树重新跑对应包，全部通过后统一改为已完成：

| 提交 | 项 | 复验命令 |
| --- | --- | --- |
| bbd0d2b2 | #61181 未索引线程按 recency 升序分页 | `go test ./appserver -run TestAttachedThreads` |
| cdfcc18d | #61197 远程消息板/时钟文档与行为 | `go test ./agentboard -count=1` |
| 16e27626 | #61200 StatusLine 内容与布局缓存（Rust 内部重构） | 审计项，无 Go API 变更；`go test ./tui/...` 未受影响 |
| d0221bc9 | #61186 计划项 ID 在完成后仍唯一 | `go test ./appserver -run 'TestPlanItemIdentities|TestRuntimeRouterPlanModeStreamsProposedPlanItem'` |
| c432cf02 | #61182 云线程重命名 | `go test ./cloudclient ./cloudbackend -count=1` |
| f562a7b3 | #61210 TUI 委派提示保留 | `go test ./app -run 'Delegation|Retention'` |
| 0f26d21f | #61260 ERS v1/v2 隧道 JWT | `go test ./tcptunnel -count=1` |
| 16280e19 | #61264 共享 HTTP 客户端 gzip/zstd | `go test ./network -count=1` |
| 69231e11 | #61286 marketplace 自动更新设置 | `go test ./config ./plugin -count=1` |
| 818bde09 | #61227 MXC 启动失败带 Win32 错误码 | `go test ./sandbox/windowssandbox -count=1` |
| 672e99fe | #61153 code-mode 代理 503 准入重试 | `go test ./codemode -count=1` |

批量结果：

```
ok  codex_go/network
ok  codex_go/tcptunnel
ok  codex_go/codemode
ok  codex_go/sandbox/windowssandbox
ok  codex_go/config
ok  codex_go/plugin
ok  codex_go/cloudclient
ok  codex_go/cloudbackend
ok  codex_go/agentboard
ok  codex_go/app (Delegation/Retention)
ok  codex_go/appserver (AttachedThreads / PlanItem)
```

完整输出见 `completed_items_reverify_2026_11_14.log`。

## 未收口的相邻项

- `#61256`（7eaaee42，request_key seccomp）：Go 侧实现与 BPF VM/交叉编译已验证，真实 Linux 原生执行仍未做，保持“部分”。
- `#61206`（x32 seccomp）、`#662daaba`（Node 真实进程）同属需要 Linux/真实进程环境才能完成的验证，未标完成。
