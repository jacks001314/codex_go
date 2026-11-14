# #61283 显式 MCP 贡献上下文收口

## 对齐结论

Go 的 `mcp.MCPServerContributionContext` 已覆盖 Rust `McpServerContributionContext` 的可观察输入：

- `HostOnly` 明确排除活动回合的实时执行器清单，不从 live step 状态派生 host 投影。
- `Originator` 只改写兼容层 `codex_apps` 的 HTTP `originator` 头，不污染其它 MCP 服务或线程级投影。
- `SessionSource` 与 thread ID 作为主机身份随上下文保留。
- `WithDisabledPluginIDs` 记录显式快照，空列表清除继承限制；未提供时才回退线程当前设置。
- `WithSelectedEnvironments` 保留显式空选择和 pending/failed 状态；`runtimeMCPConfigForThread` 在所有 overlay 之后统一应用环境权威，不可用环境的注册保留诊断但禁用。

Rust `thread_init` 扩展初始数据在 Go 中没有同构载体。Go 的等价边界是：活动回合使用 `runtimeStepCapabilitySnapshot` 冻结当步插件目录；host-only 路径没有 live step，因此使用进程级已启用插件贡献，并且只通过上下文显式禁令/环境快照收窄。该投影不会写回或修改共享插件列表。

## 回归

- `TestMCPExplicitDisabledPluginsOverrideThreadProjection`：inherited/explicit/clear/host/host restricted/unrelated 六种上下文；host 投影忽略 live 空清单，显式禁令只影响对应投影，调用后共享插件列表仍为原值。
- `TestHostContributionContextPreservesIdentityAcrossOverrides`：thread/originator/session source、显式空环境和禁用快照跨 With 调用保持。
- `TestMCPExplicitOriginatorOnlyChangesCompatibilityApps` 与 `TestMCPExplicitOriginatorReachesHTTPTransport`：真实 HTTP 请求头只改变兼容 Apps 服务。
- `TestMCPHostContextEnvironmentReadiness`、`TestMCPHostEmptySelectionRestrictsRemoteRegistrations`、`TestMCPHostReadyEnvironmentHonorsOwnerDenyPolicy`：ready/pending/failed、显式空选择和所有者拒绝策略。
- `TestDisabledRegistrationIsNameVeto`：plugin/selected_plugin/config/compatibility/extension 五类注册的禁用语义。

## 验证

- `go test ./mcp -count=1` 通过 33.225s。
- appserver 显式上下文用例通过 1.371s；本轮 appserver 全包此前已通过 255.692s。
- `go vet ./mcp ./appserver` 退出 0。

日志 update/mcp_context_completion_2026_11_14.log。据此将逐提交表 #61283 转为已完成；剩余未完成项不含本提交。