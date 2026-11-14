# #61178 Cloud 会话级速度选择

## 缺口

Go 侧协议层已支持 `AddUserMessageRequest.ServiceTier/ServiceTierSet`，但 TUI 的 Cloud 回合在 `SettingsOwnerServer` 分支提前返回，完全没有发送速度。另有两条隐藏在序列化层的问题：

1. `TurnStartParams.MarshalJSON` 只用于指针接收者，`startTurn` 传值时会绕过自定义 marshal，显式 `null` 无法表达。
2. 自定义 marshal 白名单遗漏 `additionalContext` 等后来加入的字段；若直接改用指针又会泄漏 Rust 已移除的遗留字段。

TUI 选择路径还会把速度当作用户默认配置写入服务端，而不是作为当前会话下一回合的选择。

## 实现

- Cloud `turn/start` 现在携带会话速度：空值继承服务端默认，`default` 发出显式 `null` 清除，其它 tier 作为字符串发送；其它服务端所有字段继续省略。
- `startTurn` 通过指针序列化，使 `ServiceTierSet` 生效。
- `TurnStartParams.MarshalJSON` 改为基于全部 JSON tag 序列化后删除 `responsesapiClientMetadata*`、`originator`、`runtimeWorkspaceRoots`、`multiAgentMode` 等不属于 turn/start 线的遗留字段，并保留显式 `serviceTier:null`。
- tea `Options.ServerOwnedSettings` 在 Cloud 会话中启用：选择速度只更新会话状态并提示 “Selected … speed for the next new turn in this conversation”，不触发配置写入；客户端自有会话仍保持原有持久化路径。

## 回归

- `TestCloudTurnStartOmitsClientSettingsLikeRust`：Cloud 仍省略 cwd/model/permissions/config 等客户端字段，但携带 `serviceTier:"priority"`。
- `TestCloudTurnStartClearsServiceTierWithExplicitNullLikeRust`：选择 standard 后真实 wire 上出现 `"serviceTier":null`。
- `TestCloudServiceTierSelectionStaysConversationScopedLikeRust`：选择与切回 standard 都不发生 settings 写入，状态与提示正确。
- `TestClientOwnedServiceTierSelectionStillWritesSettings`：非 Cloud 模式仍走配置写入。
- `TestTurnParamsMarshalRustStableRequestShape`：遗留字段仍不外泄。

## 验证

- `go test ./turn` 通过 1.405s；`go test ./tui/tea` 通过 47.439s；`go test ./app` 通过 39.632s。
- `go test ./cloudbackend ./cloudclient` 通过；`go vet ./turn ./app ./tui/tea ./cloudbackend ./cloudclient` 退出 0。
- `go test ./appserver` 通过 255.692s，`go vet ./appserver` 退出 0；日志 update/cloud_tier_appserver_full_2026_11_14.log。

明细日志 update/cloud_service_tier_2026_11_14.log。据此 #61178 的 TUI 选择、回合携带与显式清除已闭合，逐提交表转为已完成。