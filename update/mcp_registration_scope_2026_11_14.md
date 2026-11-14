# MCP 注册作用域隔离（#61257）

Rust 6fcfa75a9（PR #61257）要求同名 MCP 服务在不同环境中保持独立的清单、状态与路由。Go 之前的三处合并点会让第二个环境的同名注册提前消失：

1. `mcp.Manager.RuntimeConfig` 用服务名做 map 键，overlay 与基础配置互相覆盖；
2. `MCPService` 的 `servers/configs/required/httpClients/stdioClients` 全部按名字索引；
3. `appserver.applyExecutorMCPServers` 用 `registered[name]` 跨环境去重，第二个环境直接跳过。

## 本轮改动

- 新增 `mcp/registration_scope.go`：`ServerRegistrationKey`/`SplitServerRegistrationKey`/`RegistrationEnvironmentID`/`ScopedServerKeys`/`RuntimeConfig.ServerRegistrationFor`。本地注册继续使用裸服务名，额外作用域使用 `name\x00environment` 键，因此单环境配置的身份、日志与既有调用点保持不变。
- `Manager.RuntimeConfig` 先按 `(name, environment)` 建立作用域集合，再按归属顺序发布：每个名字的第一条注册保留裸键（与 Rust “省略环境选择器时走第一条注册”一致），其余同名注册保留作用域键。来源优先级、显式禁用与 requirements 否决仍按作用域生效。
- `MCPService` 的全部内部索引改为注册键，并新增 `serverOrder`/`serverNames`、`serverKeyLocked`、`serverKeyForEnvironmentLocked`、`claimRegistrationKeyLocked`。名字查询解析到第一条注册；显式 `environmentId` 必须命中该作用域，否则返回 `unknown MCP server`（与 Rust 错误文案一致）。
- `ListStatus` 保持归属顺序输出，同名条目按 `(name, environment)` 稳定排序；带名字但无环境选择器时只返回第一条注册，有环境选择器时返回该作用域条目。
- `CallTool`、`ReadResource` 按作用域解析连接与缓存键（`httpClients`/`stdioClients`/资源缓存），同名不同环境的连接、工具清单与资源内容互不串用。
- OAuth 登录/取消按作用域维护 `oauthLogins`，凭据名继续由 `OAuthCredentialName`（含环境）生成。
- `appserver.applyExecutorMCPServers` 按作用域投影：不同环境的同名声明都保留，同环境内宿主机非插件配置仍然优先。
- TUI：`protocol.ThreadItem` 新增 `environmentId`，历史单元格在调用行与 `/mcp` 清单中显示 `(environment <id>)`；远程事件、恢复会话与 inventory 复制路径都带上作用域。

## 验证

- 新增 `mcp/environment_scope_inventory_like_rust_test.go`：两个同名 `notes` 注册（env-a/env-b，工具名与资源 URI 相同）验证清单两条、显式环境路由、名字路由取第一条、未知环境报错、工具结果与资源内容各自来自对应服务。
- 新增 `appserver` 投影用例 `TestExecutorMCPProjectionKeepsSameNamedServerPerEnvironmentLikeRust`；既有 ownership 用例保持通过。
- 新增 `tui/history_cell/mcp_environment_label_like_rust_test.go` 覆盖清单与调用行的环境标签及排序。
- `go test ./mcp`、`./appserver -run "MCP|Mcp"`、`./tui/tea`、`./tui/history_cell`、`./protocol`、`./app -run "MCP|Mcp"` 全部通过；`go vet ./mcp ./appserver` 退出 0；`go build ./...` 通过。日志：`update/mcp_registration_scope_2026_11_14.log`。

## 仍未完成

- 本地执行链路已补上：`ToolExecutor` 把环境作用域写入工具输出数据，`exec.mcpToolCallProtocolItem` 落到 `protocol.ThreadItem`，app-server v2 `mcpToolCall` 条目序列化 `environmentId`；canonical rollout 记录是否原样保留该字段尚未单独核对。
- `OauthCancel` 协议参数没有 `environmentId`，取消时按第一条注册解析。
- 宿主机 `mcp_servers` 表本身仍是“一名一条”，跨环境同名声明来自执行器发现路径；这与 Rust 的配置来源一致，但多环境端到端 app-server 集成测试仍只有单元级覆盖，尚未跑真实双执行器进程。
- #61257 仍为“部分”：真实 SDK/app-server 双环境端到端认证未执行。

