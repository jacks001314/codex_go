# #57267 云会话第二阶段（align/cloud2）实施报告

基线：`9e6d9004`（含第一阶段 `37db51cc`）。分支：`align/cloud2`。
Rust 只读参考：`D:\qax\reagent\dev\git\codex`，第二阶段全部行为对应
`654b2ec1f5 Keep cloud conversation settings server-owned in the TUI (#57267)`。

前提不变：Go 仍没有 cloud app-server 传输，因此生产路径不会进入 Cloud 模式。
本阶段只实现“不依赖传输”的所有权语义，并用真实 RPC 捕获 / 注入 seam 的测试锁住。

## 判定总览

| 项 | 判定 | commit |
| --- | --- | --- |
| 1. turn/start Server 所有权省略 | 完成 | `edae2a7a` |
| 2. configRequirements -32601 仅 Cloud 容忍 | 完成 | `8b8a9c34` |
| 3. recap / thread_title / model_defaults 不走本地 | 完成 | `a4ee030b` |
| 4. Server 模式跳过本地工作区/工具初始化 | 完成 | `fb2cdae7` |
| 5. 本地 daemon / remote 行为不变 | 完成（对照用例） | 随 1-4 |
| cloud app-server 传输、总览/新会话 cwd、其它 fork 路径 | 不做（按约定） | — |

## 1. turn/start 的 Server 所有权省略

- Rust：`codex-rs/tui/src/app_server_session.rs:1611-1634`（`654b2ec1f5`）。
  只有 `settings_owner() == Client` 才发送 cwd、workspace roots、approval、
  approvals reviewer、sandbox、permissions、model、service tier、effort、
  summary、collaboration mode；threadId/input/turnTrigger/clientUserMessageId/
  additionalContext/cyberAccessProgram 恒定发送。
- Go：`app/remote_tui.go:5742 remoteTurnStartParamsForMode`；`startTurn`
  （`app/remote_tui.go:3730`）按 `client.threadParamsMode` 取参数。
  Server 模式在写入客户端字段之前返回，因此这些键根本不会序列化。
- 证据：`app/cloud_turn_params_like_rust_test.go`
  - `TestCloudTurnStartOmitsClientSettingsLikeRust`：websocket 假服务器捕获
    真实 `turn/start` JSON，断言 `cwd/model/approvalPolicy/sandboxPolicy/
    permissions/config/effort/summary/serviceTier/collaborationMode/
    runtimeWorkspaceRoots` 全部缺席，`threadId/input/turnTrigger/
    clientUserMessageId/additionalContext` 存在。
  - `TestClientOwnedTurnStartKeepsSettingsLikeRust`：Embedded/Remote 仍发送
    `cwd/model/effort/serviceTier/collaborationMode`，model 为提交覆盖值。
- 风险：`turn/start` 的 cloud 分支同样无法在生产触达；若未来传输实现后
  server 仍要求 `additionalContext` 之外的字段，需以真实 cloud 服务回归。

## 2. configRequirements/read 的 -32601 容忍绑定 Cloud

- Rust：`app_server_session.rs:815-833`（`654b2ec1f5`）：仅
  `mode == Cloud && source.code == -32601` 返回 `Ok(None)`，其余错误上抛。
- Go：`app/remote_tui.go:1219 interactiveRemoteReadSettings` 不再吞掉所有错误；
  仅在 `cloudConfigRequirementsUnsupported`（`app/cloud_thread_mode.go:89`）
  为真时继续。此前 Go 对任何模式的任何错误都静默忽略，属于行为缺口。
- 证据：`app/cloud_requirements_tolerance_like_rust_test.go`
  - Cloud：真实发出 `config/read` + `configRequirements/read`，后者回 -32601，
    设置读取成功且 requirements 为空。
  - Embedded/Remote：同一 RPC 回 -32601 时返回 `*remoteRPCError` code -32601。
- 风险：非 Cloud 服务器若真的缺少该方法，现在会让 TUI 启动失败——与 Rust 一致，
  但比此前 Go 行为更严格；`daemon_compatibility` 预检仍保留自己的兼容降级。

## 3. recap / thread_title / model_defaults 不走本地

- Rust：
  - `codex-rs/tui/src/app/recap.rs:190-195`：Server 所有权直接返回，手动 recap
    报 `CLOUD_RECAP_UNAVAILABLE_MESSAGE`（`recap.rs:43-44`）。
  - `codex-rs/tui/src/app/thread_title.rs:71-77`：Server 所有权不生成标题。
  - `codex-rs/tui/src/app/model_defaults.rs:124-133`：`thread/settings/update`
    失败且 Server 所有权时提示云会话不支持，不回退本地配置。
- Go：
  - `app/recap.go:63 recapGenerateFuncForMode` + `cloudRecapUnavailableMessage`；
    `app/remote_tui.go:593-594` 的 `OnGenerateRecap/OnSuggestThreadName` 经该 seam。
  - `app/model_selection.go:89 interactiveRemoteModelSelectionCommandForMode`：
    Server 所有权跳过 `config/batchWrite`，只发 `thread/settings/update`；
    `app/remote_tui.go:671` 按会话模式接线。
  - `app/interactive.go` 的本地生成器属于 Embedded 路径（Rust Embedded→Client），
    按 Rust 映射保持原样，不作为 Server 分支。
- 证据：`app/cloud_local_generation_like_rust_test.go`
  - `TestCloudSkipsLocalRecapGenerationLikeRust`：注入计数生成器，Cloud 调用 0 次
    且返回 Rust 原文错误；Embedded/Remote 各调用 1 次。
  - `TestCloudModelSelectionSkipsLocalConfigWriteLikeRust`：捕获真实 RPC，Cloud 有
    `thread/settings/update`、无 `config/batchWrite`。
  - `TestClientOwnedModelSelectionPersistsLikeRust`：Remote 仍写客户端配置。
- 风险：Go 的 `ModelSelectionSyncMsg.ThreadSettingsErr` 只回传错误，不生成 Rust 的
  “cloud conversation does not support changing its model” 提示文案；展示层需在
  接云入口时补齐。

## 4. Server 模式跳过本地工具/工作区初始化

- Rust：`app_server_session.rs:557-565`（`654b2ec1f5`）：Cloud 在
  `start_dynamic_tool_mcp` 直接返回，不启动本地动态工具 MCP / worktree 工具。
- Go：`app/remote_tui.go:415 remoteTUITaskToolsHostForSession`；`paramsMode`
  （`app/remote_tui.go:480`）在 `runInteractiveRemoteTUI` 中只解析一次，Cloud
  直接返回 nil，不构造 `taskToolsMCPHost`，也不创建 worktree 工具。第一阶段已在
  `startThread` 跳过 `applyTaskToolTransport` 与动态工具，本项补齐会话层入口。
- 证据：`app/cloud_task_tools_like_rust_test.go`
  - Cloud + loopback endpoint：host 为 nil，构造函数计数 0。
  - Embedded + loopback endpoint：host 非 nil，计数 1。
  - Remote + 远程 endpoint：host 为 nil，计数 0（保持 callback transport）。
- 风险：`runInteractiveRemoteTUI` 整体仍需真实传输才能端到端验证；本项锁住的是
  唯一构造本地 host 的入口。

## 5. 本地 daemon / remote 回归

与 1-4 成对出现的对照用例即回归锁：

- turn/start：Embedded/Remote 参数形状不变（`TestClientOwnedTurnStartKeepsSettingsLikeRust`）。
- requirements：Embedded/Remote 的 -32601 仍报错（`TestNonCloudConfigRequirementsMethodNotFoundFailsLikeRust`）。
- recap：Embedded/Remote 仍调用本地生成器（同上）。
- model selection：Remote 仍写 `config/batchWrite`（同上）；既有
  `TestInteractiveRemoteModelSelectionSkipsPersistenceForReserve` 等未改动。
- task tools：Embedded 仍构造 host，Remote 仍用 callback（同上）。

## 明确不做（本轮）

- cloud app-server 传输本身（生产不可达，等真实入口需求）。
- agents overview / 新会话 cwd（`new_session.rs:68-74`、`agents_overview.rs:1006-1013`）。
- side / compact / review / safety-retry 的 fork 路径（仍只覆盖 UI fork 与 side fork）。
- 权限 profile 的 Server 拒绝文案（`permission_discovery.rs:92-98`）与 turn/start 的
  `permissions` 字段在 Go 侧本就未发送，留待传输落地时接。

## 验证

- `go build ./...`：通过（最终提交前复跑）。
- `go test ./app/ -count=1`：通过（每项提交前均跑过，最终 57.0s）。
- `go test ./app/ ./appserver/ -count=1`：`ok codex_go/app 57.023s`、
  `ok codex_go/appserver 298.138s`。
