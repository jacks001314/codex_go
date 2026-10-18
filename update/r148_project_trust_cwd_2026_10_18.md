# Fix project trust for explicit thread/start CWD

Rust 依据：`#46328`（`608e4cc9a1`，projectless directories must not persist
implicit project trust）。

## 根因

`effectiveMCPConfigForThreadStartRequest` 在解析请求后无条件用
`hostMCPPathContextForTurn("", ...)` 的 `CWD` 覆盖 `params.CWD`。当请求没有
local environment 选择时，该 helper 会回退到 `DefaultCWD` 或进程 cwd，导致：

- thread/start 明明指定了 projectless 临时 workspace；
- config 发现与 trust 判定却使用测试进程所在的仓库目录；
- `appendProjectTrustLevel` 把测试仓库写成 `trust_level = "trusted"`。

这使 `TestRuntimeRouterThreadStartProjectlessDoesNotPersistProjectTrustLikeRust`
和 `TestRuntimeRouterThreadStartProjectTrustWriteGuards` 长期失败。

## 修复

- host MCP path fallback 仅在请求 `CWD` 为空时生效；显式请求 CWD 始终拥有
  project discovery 与 trust。
- 新增 `TestThreadStartTrustTargetProjectlessUsesCWDLikeRust`，钉住
  projectless 目录的 trust target 就是该目录本身。

## 验证

- `go test ./appserver -run 'ThreadStartProjectlessDoesNotPersistProjectTrust|ThreadStartProjectTrustWriteGuards|ThreadStartTrustTargetProjectless' -count=1`
- `go test ./appserver -count=1`（整包通过，119.6s）
