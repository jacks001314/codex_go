# 分页历史、Code Mode transport 与 recap 审计

本轮关闭 7 笔上游提交：

- `5cb7a35de9` Promote paginated thread history APIs (#40673)
- `d132b69219` Deprecate full-history hydration for paginated threads (#40676)
- `17c4c47dfa` Default durable threads to paginated history (#40677)
- `a6e63f9f32` Respect config layers when removing plugin marketplaces (#40683)
- `94967e03e5` Add per-turn cyber access program selection (#40687)
- `725b3a44f8` Remove WebSocket transport from code-mode hosts (#40692)
- `7c1e36c23f` Prepare the TUI for conversation recaps (#40696)

## 已有实现

- `thread/turns/list`、`thread/items/list`、`thread/revert` 与
  `thread/reverted` 已在 Go schema 中稳定暴露；resume/fork 返回
  `historyMode` 与分页 cursor。
- 本地 durable thread 默认使用 paginated history，显式 legacy 保留。
- marketplace 移除会检查其他 enabled config layer，避免删除仍被引用的快照。
- cyber access program 已贯穿 turn context、rollout、compaction 与子 agent，
  并通过 `AccessProgramsForAuth` 处理 ChatGPT/API-key 边界。
- TUI recap 已覆盖 focus 窗口、completed-turn 进度、prompt 预算、自动/手动
  触发、重试和历史单元格。

## 本轮修复

### #40676

`thread/read`、`thread/resume`、`thread/fork` 在 paginated thread 请求完整历史时
发送 connection-scoped `deprecationNotice`，元数据请求保持静默。新增
`TestPaginatedFullHistoryRequestsEmitDeprecationNoticesLikeRust`。

### #40692

- `app-server --code-mode-host` 只接受 `http://` / `https://` gRPC endpoint；
- 拒绝 `ws/wss`、credentials、fragment、path/query；
- runtime 对配置的 host 固定使用 `GrpcCodeModeSessionProvider`；
- WebSocket provider 不再通过 CLI/runtime 暴露，相关 appserver 测试改为显式
  注入内部 provider。

## 保留待办

- `#40672` Windows private desktop 跨命令复用：需要新增 Windows desktop cache、
  policy key 与 elevated/legacy runner 的 desktop-name 传递。
- `#40679` Bedrock TUI onboarding：需要 AWS profile/env 发现、交互式凭据录入、
  Region 收集和 provider 重启。

## 验证

- `go test ./appserver -run 'PaginatedFullHistoryRequestsEmitDeprecationNotices|DefaultsLocalThreadsToPaginated' -count=1`
- `go test ./appserver -run 'MarketplaceRemove|AppCyberAccessProgram|AgentCompactRunnerCarries' -count=1`
- `go test ./cli -run 'ParseAppServerCodeModeHostValidation' -count=1`
- `go test ./appserver -run 'SelectsCodeModeProviderByURL|RemoteCodeMode' -count=1`
- `go test ./tui/tea -run 'Recap' -count=1`
