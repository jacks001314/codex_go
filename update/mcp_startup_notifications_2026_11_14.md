# MCP startup 状态通知生产端（#61257 第四步，2026-11-14）

## 交付

- `mcp.MCPService` 新增 `AddStartupStatusListener`：注册后返回注销函数；监听器收到 `(name, environmentID, status, failureReason, err)`，环境取自注册配置（`EffectiveEnvironmentID`）。
- 触发点：`markServerStartupStarted` 发 `starting`；`recordInventoryStatus` 只在状态相对上一次发生变化时发 `ready`/`failed`，避免重复刷新刷屏。
- appserver 侧新增 `observeMCPStartupStatus`，在 `mcpRuntimes.serviceForThread` 创建服务时注册，转成 `mcpServer/startupStatus/updated` 通知：携带 threadId、name、environmentId（非默认环境才带）、status、error 与 failureReason。

## 回归

- `TestMCPStartupStatusListenerCarriesEnvironmentLikeRust`（mcp）：starting/ready 事件带 `env-a`，状态不变不重复发，注销后不再触发。
- `TestMCPStartupStatusNotificationCarriesEnvironmentLikeRust`（appserver）：用一个不存在的命令触发真实启动失败，通知里 threadId、environmentId=env-a、status=failed 均正确。
- `go vet ./mcp ./appserver` 退出 0；`go test ./mcp ./appserver -count=1` 见 `mcp_startup_notify_2026_11_14.log`。

## 仍未完成（#61257）

注册表仍按 server name 索引，同名跨环境注册在配置层就会被合并；监听器当前每个 name 只有一份注册。要真正隔离需要环境感知目录（复合键 + 按环境聚合配置贡献）和相应端到端用例。
