# #61257 双执行器同名 MCP 作用域端到端验证

## 背景

注册表、状态、工具调用、资源读取、OAuth 与本地 exec 工具条目的环境作用域已实现。剩余验收缺口是：两个独立执行器注册同名 MCP 服务和工具时，请求是否真的沿各自执行器路由，而不是复用宿主 HTTP 客户端或另一环境的连接。

## 新增回归

`appserver/mcp_dual_executor_scope_like_rust_test.go`：

- 启动两个独立 exec-server 实例和两个真实 WebSocket 客户端，分别标识 `executor-a` / `executor-b`。
- 两个执行器各自发现一个同名 `notes` MCP 服务与同名 `lookup` 工具，MCP HTTP 客户端分别绑定 `executorHTTPDoer`。
- 显式环境调用分别返回各自上游来源；完整状态清单同时返回两条记录，`EnvironmentID` 与 `HTTPOrigin` 一一对应。
- 另建一个绑定已关闭执行器连接的服务：该环境调用必须失败，不能回退宿主 HTTP 客户端；同一服务中的另一个执行器仍可正常调用。

本地 exec 工具条目作用域由现有 `TestMCPToolCallProtocolItemCarriesEnvironmentScopeLikeRust` 复验：环境服务写入 `environmentId`，本地注册保持省略。

## 验证

- 双执行器用例通过 1.27s。
- `go test ./mcp -count=1` 通过 33.226s。
- `go test ./appserver -run "MCP|Mcp|Executor" -count=1` 通过 10.847s。
- `go vet ./mcp ./appserver ./exec` 退出 0。

日志见 `update/mcp_dual_executor_2026_11_14.log`。执行器实例运行在同一测试宿主进程中，但使用独立 exec-server、独立 WebSocket 连接和真实 HTTP MCP 上游；路由隔离已按用户可见调用路径验证。#61257 逐提交表转为已完成。