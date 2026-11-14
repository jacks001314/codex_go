# `/mcp login --environment`（#61257 第三步，2026-11-14）

## 交付

- TUI `/mcp login <name>` 支持可选 `--environment <id>` 与 `--environment=<id>`；缺值或未知参数按用法错误处理，提示更新为 `/mcp [verbose | login <name> [--environment <id>]]`。
- `tea.Options.OnStartMCPLogin` / `Model.onStartMCPLogin` 回调扩展为 `(name, environmentID, threadID)`；`MCPLoginStartedMsg` 与 pending 请求都携带环境身份，迟到/不匹配的回执不会清理当前尝试。
- app 侧本地与远端 starter 把 `EnvironmentID` 传入 `mcp.MCPServerOauthLoginParams`，因此远端 RPC 与本地 OAuth 都会带上作用域。

## 回归

- `TestMCPLoginCommandAcceptsEnvironmentLikeRust`：两种写法都被解析，回调收到 `env-a`，pending 记录一致；缺值与未知参数被拒绝。
- 既有 `TestMCPLoginCommandStartsAndCompletesLikeRust`、`TestMCPLoginCommandErrorsAndUsageLikeRust` 适配新签名并通过。
- `go vet ./tui/tea ./app` 退出 0；`go test ./tui/tea ./app -count=1` 结果见 `mcp_login_environment_2026_11_14.log`。

## 仍未完成（#61257）

内部注册表仍按 server name 索引，`RuntimeConfig.Servers` 也是 `map[string]ServerRegistration`，配置层一次只解析一个环境，因此同名跨环境注册在进入 `MCPService` 之前就被合并。要真正隔离需要环境感知的注册目录（复合键 + 每环境贡献聚合），再补两个环境同名 server 的端到端测试。状态通知在 Go 侧也仍无生产发射点。
