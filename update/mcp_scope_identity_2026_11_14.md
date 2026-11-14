# MCP 状态/通知携带环境身份（#61257 第二步，2026-11-14）

## 本轮交付

- `mcp.MCPServerStatus` 新增 `environmentId`（可选）：状态列表在配置存在时用 `mcpStatusEnvironmentID` 从注册配置填充；线程级/本地注册保持为空，老客户端看到的形状不变。
- `appserver.MCPServerStatusUpdatedNotification` 新增 `environmentId` 并在自定义 `MarshalJSON` 中透出；未设置时不输出该字段。
- `appserver.MCPElicitationRequestParams` 新增 `environmentId`，普通 elicitation 与 `openai/userVerification` 两个序列化分支都携带。

与 Rust 的差异：Rust 对 `environment_id` 恒定序列化为 `null`；Go 使用 `omitempty`，未设置时省略字段，避免破坏既有精确形状断言。已记录为后续可收紧点。

## 回归

- `TestMCPServerStatusCarriesEnvironmentIdentityLikeRust`：env-a 注册的状态带 `environmentId=env-a`，线程级注册为 nil。
- `TestMCPRegistrationScopeSurvivesNotificationsLikeRust`：通知带环境身份、未设置时不出现该键；elicitation 携带环境身份。
- `go vet ./mcp ./appserver` 退出 0；`go test ./mcp ./appserver -count=1` 结果见 `mcp_scope_identity_2026_11_14.log`。

## 仍未完成（#61257 本体）

`MCPService` 的 `servers/configs/disabledConfigs/dynamicConfig/required/starting/httpClients/stdioClients/oauthLogins` 仍按 server name 索引，同名不同环境注册不能共存；状态通知在 Go 侧目前也没有生产发射点（只有 TUI 消费端）。下一步是引入 `(name, environmentId)` 复合键与旧路由解析，并补两个环境同名 server 的端到端集成测试。
