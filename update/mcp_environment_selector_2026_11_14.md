# MCP 环境作用域选择器（#61257 第一步，2026-11-14）

## 本轮交付

协议层四个请求现在接受可选 `environmentId`：

- `MCPListServerStatusParams`：显式作用域只返回该环境下的注册；未知环境得到空页；省略时保持按名返回。
- `MCPToolCallParams`、`MCPResourceReadParams`、`MCPServerOauthLoginParams`：显式作用域与解析到的注册环境不一致时报错 `mcp server is not registered in environment <id>`；空字符串视为省略。

实现集中在 `mcp/api.go`：新增 `mcpEnvironmentSelectorError`，状态列表在名称过滤后追加环境过滤。

## 仍未完成

`MCPService` 的 `servers/configs/disabledConfigs/dynamicConfig/required/starting/httpClients/stdioClients/oauthLogins` 仍按 server name 索引，同名不同环境的注册会互相覆盖。要完成 #61257 还需：

- 以 `(serverName, EffectiveEnvironmentID())` 为内部键重建上述映射；
- 状态通知、tool-call 历史、elicitations 与 TUI 标签携带环境身份；
- 两个环境同名 server 的端到端集成测试（独立 inventory、显式路由、省略时的旧路由、未知环境报错）。

本轮是协议入口与错误语义的可用切片，不宣称 #61257 完成。

## 验证

- `go test ./mcp -run 'TestMCPEnvironmentSelector' -count=1` 通过。
- `go vet ./mcp` 退出 0。
- `go test ./mcp -count=1` 全包结果见 `mcp_env_selector_2026_11_14.log`。
