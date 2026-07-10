# Codex Go Rust 全量功能对齐开发与测试计划

本文档用于接替下一轮总账本，目标是把本项目 Go 版 Codex 与 Rust 版 Codex 做到 100% 功能、协议、行为和测试对齐。后续每轮开发前先读本文件，开发后把完成项、验证命令和剩余差异写回本文件。

### Rust 基础版本

- Rust 参考目录：`D:\qax\reagent\dev\codex-main\codex-rs`
- Go 工作目录：`D:\qax\reagent\dev\codex_go`
- 审计日期：2026-07-07
- Rust workspace：`Cargo.toml` 中 123 个 workspace member，当前可见约 2446 个 `.rs` 文件，约 364 个 Rust test/suite 文件。
- Go workspace：当前 `go list -buildvcs=false ./...` 通过，56 个 Go package，781 个 `.go` 文件，324 个 Go test 文件。

## 使用规则

- 本文件是新的全量对齐计划账本；`plan.md` 和 `next_plan.md` 继续作为历史依据，不再作为下一步唯一入口。
- 每轮编码前先确认本文件中的阶段、P0 缺口和最近验证命令。
- 每轮完成代码变更后，必须追加“工作日志”，写明对照的 Rust crate/source、Go package、验证命令和剩余差异。
- 完成一个 todo 后把 checkbox 改成 `[x]`；有阻塞时保留 todo 并标记 `BLOCKED:`，不要删除。
- `go list -buildvcs=false ./...` 是每轮包加载闸门。
- 默认全量测试必须使用仓库内 cache，避免 Windows 默认 cache 或临时目录权限干扰。
- 真机、凭据、网络、系统状态相关测试必须 gated，默认 `go test ./...` 不得自动修改 ACL、WFP/firewall、keychain、系统用户、真实云端资源。
- 不能把“主链路可跑”当作 100%；只有 Rust command/RPC surface、wire shape、错误码、状态语义、持久化格式、fixture、mock integration、live/platform gated 测试都通过时，才能关闭对应功能域。

## 当前总览

- 综合对齐进度：约 93%。
- 非 TUI 主链路实现进度：约 87%-89%。
- Rust fixture/golden 高保真测试进度：约 60%-65%。
- live/provider/MCP/plugin/sandbox/SDK 真机验收进度：约 40%-50%。
- 最大产品缺口：TUI 主链路已经进入 Rust `tui`/`chatwidget` 子模块长尾阶段；Go 侧已有 Bubble Tea root model、真实 TTY path、streaming 回流、approval/MCP/user-input modal、model/session picker、attachment draft/structured wire、composer queue、external editor、keymap persistence、interrupt/focus/resize/restore smoke、remote app-server TUI、goal/permissions/settings runtime 接线、rate-limit warning/usage reset/model-switch prompt、OSC9/BEL desktop notification runtime hook、Rust 同款 `[tui] notifications/notification_method/notification_condition` 配置接线、`/theme`/`/pets` picker/direct command 与 `tui.theme`/`tui.pet` settings 写入、pets catalog/asset path/image support/animation/ambient draw 核心、permissions requirements 校验、Windows sandbox setup TUI/RPC/completion 接口、Windows ConPTY terminal restore gated smoke + host output probe、remote `/hooks` app-server 清单接线、remote `/plugins` app-server catalog 接线、remote `/skills` app-server inventory 接线、remote `/apps` app-server catalog 接线、remote `/review` app-server start 接线、review branch/commit picker git inventory、remote `/side` app-server fork/inject/parent-status/discard 接线、remote `/agent` app-server loaded/read picker 与 thread switch 接线，以及 `internal/tui/chatwidget` 对 transcript/interaction/rate-limits/usage/status/goal/permissions/settings/plugins/skills/review/side/session header/windows sandbox prompt 的大量纯逻辑与部分 Tea runtime 接线。剩余缺口收敛为支持 ConPTY 输出宿主上的 Windows restore 完整通过记录，以及把轻量接口壳逐步替换为 Rust 完全一致业务实现和 golden/snapshot。
- 本轮继续对齐 Rust `chatwidget/rate_limits.rs`：Go 新增 `chatwidget.RateLimitWarningState`，支持 codex-only、workspace credits skip、75/90/95 阈值只提示一次、100% 封顶不提示、5h/daily/weekly/monthly/annual 窗口标签；并接通 Responses stream `response.rate_limits` -> exec protocol -> interactive JSON writer -> Tea history warning。剩余 rate-limit 长尾限定为 status/usage/tokens 面板、reset 信息和 lower-cost model prompt 等 surface 行为。
- 本轮继续对齐 Rust `chatwidget/usage.rs`、`chatwidget/tokens.rs`、`chatwidget/status_state.rs`：Go 新增 usage/reset selection view model、token activity view 参数解析与 summary 格式化、12 个月 daily heatmap/weekly bar/cumulative running total chart、guardian review status 聚合；Tea `/usage` 空参数打开 Usage 菜单，`/usage daily|weekly|cumulative` 插入 token activity history card，modal 支持 disabled options。剩余 usage 长尾限定为 app-server account usage/read/reset consume RPC 与 live refresh。
- 本轮继续对齐 Rust `chatwidget/warnings.rs` 与 `chatwidget/notifications.rs`：Go 新增 `WarningDisplayState`，对 fallback model metadata warning 按 model slug 去重；Tea 将 `StatusMsg{Status:"warning: ..."}` 提升为 warning history cell，并复用去重状态；Go 新增 desktop notification view model/coalescing，覆盖 agent preview、approval/edit/elicitation/plan prompt display、custom allowlist 和高优先级覆盖规则。
- 本轮继续对齐 Rust `chatwidget/user_messages.rs` 与 `chatwidget/input_queue.rs`：Go 新增结构化 `UserMessage`/history override/queued message/pending steer/thread composer/input snapshot、消息 merge/remap/restore/display helper、pending steer compare key，以及 `InputQueueState` 的 queued/pending/rejected preview、clear、follow-up 判断；锁定 TextElement 图片占位符 remap 和 history override 语义。
- 本轮继续对齐 Rust `chatwidget/status_surfaces.rs` 与 `chatwidget/status_controls.rs`：Go 已覆盖 status line/terminal title item id 与 alias 解析、invalid item 去重、默认项、preview placeholder/live/suppress、rate-limit preview copy、terminal title separator/action-required 拼接、spinner frame/title truncation，并新增 `StatusControlsState` runtime/setup 控制层，覆盖 `set_status`、status-line setup、terminal-title preview/revert/commit、git branch/summary stale 丢弃、context/limit/reasoning helper 和 setup view item metadata；剩余限定为 Tea command/modal 事件接线、真实 git/project lookup、OSC title write 和 status output/account RPC 长尾。
- 本轮继续对齐 Rust `chatwidget/goal_menu.rs` 与 `chatwidget/goal_status.rs`：Go 已有 app-server goal summary lines、status label/command hint、edited-goal status mapping、resume paused goal selection view、goal status indicator 与 active turn elapsed-time 聚合；本轮继续补齐 Tea `/goal` command、remote app-server `thread/goal/get|set|clear` RPC callbacks、`thread/goal/updated|cleared` notification 映射和 status surface `task-progress` 接入。
- 本轮继续对齐 Rust `chatwidget/permissions_menu.rs` 与 `chatwidget/permission_popups.rs`：Go 新增 builtin approval presets、permissions popup/profile popup/full-access confirmation view model、Guardian “Approve for me”、Windows non-admin sandbox hint、current item 判断、custom profile disabled reason；Tea/runtime session menu、settings 回填、requirements 校验和 Windows sandbox setup TUI/RPC 接线已补，剩余限定为 Windows sandbox setup completion/app-server prompt live smoke 与后端深行为。
- 本轮完成 Rust `chatwidget/settings.rs` 与 `chatwidget/settings_popups.rs` Tea runtime persistence：Go 侧 `/personality` 与 `/experimental` 已支持 popup/direct command、config 写入、effective config 回读、local/remote/line fallback 状态同步；剩余 settings 长尾仅保留 theme/model capability 相关 polish。
- 本轮继续对齐 Rust `tui/src/status/{account,remote_connection,format,helpers,rate_limits}.rs`：Go `internal/tui/status` 已从薄壳扩展为账号显示、远端连接脱敏、字段对齐、model/account/token/plan/reset 格式化、rate-limit rows/progress/credits/stale/missing/unavailable 数据层；剩余 status 长尾限定为完整 `/status` history card、app-server account usage/RPC 和 snapshot/golden。
- 本轮继续对齐 Rust `tui/src/app/{resize_reflow,agent_message_consolidation,pending_interactive_replay,replay_filter}.rs`：Go `internal/tui/app` 已补 transcript 尾部 streaming run 识别、initial replay row cap、source-backed resize reflow 行重建、agent transient message consolidation、pending interactive replay 状态机和 notice/interactive request 过滤；剩余 app 子模块长尾继续按文件把薄壳替换为 Rust 业务和 snapshot。
- 本轮继续对齐 Rust `tui/src/app/{loaded_threads,agent_status_feed}.rs`：Go `internal/tui/app` 已补 loaded thread spawn-tree 子线程发现、确定性排序、agent metadata 输出，以及 `/agent` running status 最近活动预览的 item 去重、摘要文案、bounded summary 和 preview line cap。
- 默认 exec 路径已收口：普通 `codex exec` 现在默认按 config/auth/provider 构造真实 `ResponsesAgentRunner`，`LocalAgentRunner` 仅保留为显式测试/离线 stub；后续继续补 Rust CLI/parser/output fixture 与 live provider matrix。
- remote exec-server registration 已有 registry 注册 + rendezvous websocket 直连 JSON-RPC 最小闭环；剩余为 Rust Noise relay/多路复用严格对齐、streamResponse 与 live matrix。
- 最近本轮实际验证：`go test ./internal/tui/chatwidget ./internal/tui/tea -run "TestTranscriptOverlay|TestLastAssistantMarkdown|TestModelTranscriptOverlay|TestModelCopies|TestModelCopy|TestRealPTYTerminalRestoreSmoke" -count=1 -v` 通过（Windows ConPTY smoke 默认 gated skip）；`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell -count=1` 通过；`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/app ./internal/appserver ./internal/session ./internal/config -count=1` 通过；`go test ./... -count=1` 通过。
- 最新新增验证：`go test ./internal/tui/chatwidget ./internal/protocol ./internal/exec ./internal/tui/tea ./internal/app -run "TestRateLimit|TestExecStreamEventCollectorBuildsRateLimit|TestModelAppliesRateLimit|TestInteractiveStreamEventWriter" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过；`go test ./... -count=1` 通过。
- 最新 usage/status_state 验证：`go test ./internal/tui/chatwidget ./internal/tui/tea -run "TestPendingGuardian|TestStatusState|TestStatusIndicatorState|TestUsage|TestTokenActivity|TestModelUsage" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过；`go test ./... -count=1` 仅 `internal/appserver/TestProcessServiceSpawnTTYStreamsAndResizes` 命中既有 Windows ConPTY 时序抖动，失败用例单独重跑按 Rust legacy ConPTY CI limitation skip 通过，`go test ./internal/appserver -count=1` 通过。
- 最新 chatwidget user/input/status/goal/permissions/settings 验证：`go test ./internal/tui/chatwidget -run "TestInputQueue|TestUserMessage|TestThreadComposer|TestMerge|TestPendingSteer" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestStatusSurface|TestTerminalTitle" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "Test.*Goal" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestPermission|TestBuiltinApproval|TestFullAccess" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestPersonality|TestExperimental" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/app -run "TestParseCommand|TestModelGoal|TestInteractiveRemoteGoal|TestRemoteAppServerTUIClientMapsGoal" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- 最新 status/pets/onboarding 验证：`go test ./internal/tui/status -count=1 -v` 通过；`go test ./internal/tui/status ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea -count=1` 通过；`go list -buildvcs=false ./...` 通过；`go test ./... -count=1` 通过。
- 最新 app 子模块验证：`go test ./internal/tui/app -count=1 -v` 通过；`go test ./internal/tui/app ./internal/tui/status ./internal/tui ./internal/tui/tea -count=1` 通过；`go list -buildvcs=false ./...` 通过；`go test ./... -count=1` 通过。
- 最新 loaded/agent-status 验证：`go test ./internal/tui/app -run "TestFindLoadedSubagent|TestAgentActivity|TestAgentStatus" -count=1 -v` 通过；`go test ./internal/tui/app ./internal/tui ./internal/tui/tea -count=1` 通过；`go test ./... -count=1` 通过。
- 最近历史全量测试：`plan.md`/`next_plan.md` 记录 `go test ./...` 与 `go test ./... -count=1` 已多次通过；最近非 TUI 抖动集中在 app/appserver/execserver 的进程、TempDir 或 ConPTY 时序清理路径。

## 完成度统计

统计口径：按用户可见产品能力和系统集成权重估算，不按代码行数或 crate 数直接折算。

| 功能域 | 权重 | Rust 对照 | Go 对应 | 状态 | 估算 | 100% 前剩余缺口 |
| --- | ---: | --- | --- | --- | ---: | --- |
| Core turn runtime | 9% | `core`、`core-api`、`protocol` | `internal/turn`、`internal/tool`、`internal/protocol` | GREEN/IN_PROGRESS | 92% | Rust turn loop、tool ordering、interrupt、compact、guardian、multi-agent fixture 全量回归 |
| App-server V2 RPC | 12% | `app-server`、`app-server-protocol`、`app-server-transport` | `internal/appserver` | GREEN/IN_PROGRESS | 89% | config/write 业务错误 data/code 与出站 JSON-RPC envelope 已按 Rust 锁定；剩余更多业务错误、全量 schema field diff、SDK e2e、更多 notification 顺序 fixture |
| CLI/exec/review | 8% | `cli`、`exec`、`utils/cli` | `cmd/codex`、`internal/cli`、`internal/app`、`internal/exec`、`internal/review` | IN_PROGRESS | 85% | Rust parser/help/error tests、human/JSONL output fixture、review output fixture |
| Model/provider/auth | 9% | `codex-api`、`model-provider`、`chatgpt`、`aws-auth`、`ollama`、`lmstudio` | `internal/model`、`internal/codexapi`、`internal/chatgptapi`、`internal/auth` | IN_PROGRESS | 78% | live provider matrix、ChatGPT/OAuth e2e、Azure/Bedrock role/IMDS、request body golden |
| Tool/hooks/approval | 8% | `core/src/tools`、`shell-command`、`apply-patch`、`hooks` | `internal/tool`、`internal/applypatch`、`internal/shell` | GREEN/IN_PROGRESS | 88% | Rust tool suite 翻译、approval amendment、network approval、tool output/error JSON shape 锁定 |
| Session/rollout/state | 8% | `rollout`、`thread-store`、`message-history`、`state` | `internal/session`、`internal/rollout`、`internal/history`、`internal/state` | GREEN/IN_PROGRESS | 86% | rollout/thread-store fixture、migration、Windows handle cleanup、cursor/page 边界 |
| Config/features | 5% | `config`、`cloud-config`、`features`、`codex-home` | `internal/config`、`internal/features` | IN_PROGRESS | 80% | profile v2、edit preserve、managed/system/MDM requirements、strict unknown key path |
| MCP | 5% | `mcp-server`、`codex-mcp`、`rmcp-client`、`ext/mcp` | `internal/mcp` | IN_PROGRESS | 78% | live stdio/HTTP/SSE servers、OAuth dynamic registration e2e、session delete/rebuild、error mapping fixtures |
| Plugin/skills/apps/connectors | 6% | `plugin`、`core-plugins`、`skills`、`core-skills`、`connectors`、`ext/*` | `internal/plugin`、`internal/prompt`、`internal/apps`、`internal/mcp` | IN_PROGRESS | 76% | remote marketplace live、plugin cache/materialization、skills telemetry/budget、connector accept e2e |
| Sandbox/exec-server/network | 9% | `sandboxing`、`linux-sandbox`、`windows-sandbox-rs`、`exec-server`、`network-proxy` | `internal/sandbox`、`internal/execserver`、`internal/network` | IN_PROGRESS | 70% | Windows/Linux 真机矩阵、Rust Noise relay/streamResponse 对齐、MITM/proxy/certs/policy reload |
| Daemon/remote-control/SDK | 6% | `app-server-daemon`、`app-server-client`、`app-server-test-client` | `internal/appserverdaemon`、`internal/remotecontrol` | IN_PROGRESS | 72% | Python/TypeScript SDK contract、connection file watch、capability token、remote app-server TUI |
| Doctor/install/update/telemetry | 5% | `cli/src/doctor`、`install-context`、`otel`、`analytics` | `internal/doctor`、`internal/install`、`internal/telemetry` | IN_PROGRESS | 78% | snapshot fixture、platform diagnostics、OTEL/live exporter、update/install e2e |
| TUI/interactive product | 8% | `tui` | `internal/tui`、`internal/tui/chatwidget`、`internal/tui/tea`、`internal/tui/markdown`、`internal/tui/bottom_pane`、`internal/tui/history_cell`、`internal/tui/streaming`、`internal/tui/exec_cell`、`internal/app/interactive.go`、`docs/tui_tech_selection.md` | GAP/IN_PROGRESS | 99.99% | Windows ConPTY restore 支持宿主完整通过记录、业务/golden 收口 |
| Utility/ext/migration | 2% | `utils/*`、`external-agent-*`、`file-*`、`realtime-webrtc`、`code-mode` | `internal/utils`、`internal/filesearch`、`internal/realtime`、`internal/codemode` | IN_PROGRESS | 70% | Rust utility fixtures、external-agent migration、large repo/search perf、IDE integration |

## 全量 Rust/Go 模块覆盖矩阵

| Rust workspace 覆盖 | Go 对应 | 状态 | 备注 |
| --- | --- | --- | --- |
| `cli`、`exec`、`utils/cli`、`arg0`、`apply-patch` | `cmd/codex`、`internal/cli`、`internal/app`、`internal/exec`、`internal/applypatch` | IN_PROGRESS | 命令树基本覆盖；默认 exec 已走真实 Responses/OSS provider runner，parser/help/error fixture 仍是 P0 |
| `tui`、`collaboration-mode-templates` | `internal/tui`、`internal/tui/chatwidget`、`internal/tui/tea`、`internal/tui/markdown`、`internal/tui/bottom_pane`、`internal/tui/history_cell`、`internal/tui/streaming`、`internal/tui/exec_cell`、`internal/app/interactive.go`、`internal/prompt` | GAP/IN_PROGRESS | 本地/remote Bubble Tea TUI、history/streaming/diff/exec/composer/picker/modal/request_user_input/session action/attachment/keymap/status_controls/usage/account/goal/permissions/settings 主路径已对齐；`internal/tui/chatwidget` 已按 Rust 独立包建账并覆盖 transcript、interaction、rate_limits、usage、tokens、status_state/surfaces/controls、goal、permissions、settings、plugins、skills、review、side、session_header、windows_sandbox_prompts 大量 view model，Tea 已接入 rate-limit lower-cost model switch prompt、hidden notice persistence、OSC9/BEL desktop notification runtime hook、`[tui]` notification settings、`/theme`/`/pets` picker/direct command 与 `tui.theme`/`tui.pet` settings 写入、pets catalog/asset path/image support/animation/ambient draw 核心、permissions requirements、Windows sandbox setup TUI/RPC/completion 接口、Windows ConPTY restore gated smoke + host output probe、remote `/hooks` app-server 清单、remote `/plugins` app-server catalog、remote `/skills` app-server inventory、remote `/apps` app-server catalog、remote `/review` app-server start、review branch/commit picker git inventory、remote `/side` app-server fork/inject/parent-status/discard 和 remote `/agent` app-server loaded/read picker/switch。仍缺支持 ConPTY 输出宿主上的完整通过记录，以及把 Rust `tui/src` 轻量接口壳替换为完整业务/golden |
| `core`、`core-api`、`protocol`、`tools`、`prompts`、`context-fragments` | `internal/turn`、`internal/tool`、`internal/protocol`、`internal/prompt`、`internal/context` | GREEN/IN_PROGRESS | 主链路强，但仍需 Rust fixture 全面锁定 |
| `codex-api`、`codex-client`、`backend-client`、`codex-backend-openapi-models`、`chatgpt` | `internal/codexapi`、`internal/chatgptapi`、`internal/model` | IN_PROGRESS | Responses runner 已有，CLI 默认接线和 live matrix 待收口 |
| `model-provider`、`model-provider-info`、`models-manager`、`ollama`、`lmstudio`、`aws-auth`、`cloud-config` | `internal/model`、`internal/auth`、`internal/config` | IN_PROGRESS | provider catalog/auth 基础覆盖；多 provider live/e2e 待补 |
| `login`、`keyring-store`、`codex-home`、`secrets` | `internal/auth`、`internal/config`、`internal/safety` | IN_PROGRESS | auth.json/ChatGPT/PAT 基础覆盖；OS keyring 和 OAuth/device e2e 待补 |
| `app-server`、`app-server-protocol`、`app-server-transport`、`stdio-to-uds`、`uds` | `internal/appserver`、`internal/app` | GREEN/IN_PROGRESS | V2 RPC 主体已深度对齐；schema/business error/SDK e2e 待补 |
| `app-server-daemon`、`app-server-client`、`app-server-test-client` | `internal/appserverdaemon`、`internal/remotecontrol` | IN_PROGRESS | daemon lifecycle/remote-control 基础覆盖；SDK contract 和 remote TUI 待补 |
| `rollout`、`rollout-trace`、`thread-store`、`message-history`、`state` | `internal/rollout`、`internal/session`、`internal/history`、`internal/state` | GREEN/IN_PROGRESS | JSONL/thread store 主体可用；fixture 和 migration 待补 |
| `memories/read`、`memories/write`、`ext/memories` | `internal/memories`、`internal/state`、`internal/doctor` | IN_PROGRESS | usage/citation 基础覆盖；迁移和 live behavior 待补 |
| `mcp-server`、`codex-mcp`、`rmcp-client`、`ext/mcp` | `internal/mcp`、`internal/appserver` | IN_PROGRESS | stdio/HTTP/OAuth 大量路径已有；live servers 和 error mapping 待补 |
| `plugin`、`core-plugins`、`skills`、`core-skills`、`ext/skills` | `internal/plugin`、`internal/prompt`、`internal/tool` | IN_PROGRESS | marketplace/manifest/skills roots 主链路已有；remote marketplace/materialization/telemetry 待补 |
| `connectors`、`ext/connectors`、`ext/web-search`、`ext/image-generation`、`ext/goal`、`ext/guardian`、`ext/extension-api` | `internal/apps`、`internal/mcp`、`internal/tool`、`internal/appserver` | IN_PROGRESS | connector/app 元数据和动态工具已有；live connector 和 extension fixtures 待补 |
| `shell-command`、`shell-escalation`、`execpolicy`、`execpolicy-legacy` | `internal/shell`、`internal/tool`、`internal/execpolicy` | IN_PROGRESS | Windows safety、execpolicy parser 已大量对齐；完整 Starlark/load/diagnostic 待补 |
| `sandboxing`、`linux-sandbox`、`bwrap`、`windows-sandbox-rs`、`process-hardening` | `internal/sandbox`、`internal/sandbox/linuxsandbox`、`internal/sandbox/windowssandbox` | IN_PROGRESS | unit/elevated smoke 基础有；平台真机矩阵是主要缺口 |
| `exec-server`、`exec-server-protocol`、`network-proxy`、`responses-api-proxy` | `internal/execserver`、`internal/network`、`internal/app` | IN_PROGRESS | fs/process/http 基础有；remote registration 最小闭环已接通，Noise relay/streamResponse/proxy live 待补 |
| `file-search`、`file-system`、`file-watcher`、`git-utils` | `internal/filesearch`、`internal/appserver`、`internal/review`、`internal/utils` | GREEN/IN_PROGRESS | 基础行为覆盖；大仓库性能、ignore rules、watcher e2e 待补 |
| `feedback`、`realtime-webrtc`、`code-mode`、`code-mode-host`、`code-mode-protocol` | `internal/appserver`、`internal/realtime`、`internal/codemode` | IN_PROGRESS | 基础服务已有；IDE/runtime integration 待补 |
| `analytics`、`otel`、`features`、`hooks`、`install-context`、`terminal-detection` | `internal/telemetry`、`internal/eventmap`、`internal/features`、`internal/doctor`、`internal/install` | IN_PROGRESS | doctor/features/hooks 基础深；OTEL/export/install live 待补 |
| `agent-graph-store`、`agent-identity`、`external-agent-migration`、`external-agent-sessions` | `internal/agent`、`internal/auth`、`internal/state` | IN_PROGRESS | graph/identity 基础有；external agent ledger/migration 需专门 fixture |
| `ansi-escape`、`async-utils`、`utils/*`、`test-binary-support`、`thread-manager-sample`、`v8-poc` | `internal/utils`、各 package 内 helper | PARTIAL | 常用工具已覆盖；低层 utility fixture 和未使用实验 crate 需确认是否纳入 100% |

## 明确 P0 差异

- [x] CLI `codex exec` 默认必须走真实 Responses/OSS provider runner，不得默认返回 `Go Codex exec stub received: ...`。
- [ ] Root interactive 必须提供 Rust `tui` 等价体验；当前行式 session 只能算临时 fallback。
- [ ] `--remote` interactive app-server mode 必须完整实现；ws/wss+unix TUI 主路径、核心 server requests、ChatGPT auth refresh 真刷新、server request/notification targeted sink、fs/watch app-server 操作与真实外部文件变化触发通知、unsupported server-request 长尾确定错误码与 remote TUI slash session action handoff 已接通，仍缺真实终端 smoke。
- [x] `resume/fork/archive/delete/unarchive --remote` 已接远端 app-server；支持 UUID 和精确 name 解析，走 `thread/list/read/archive/unarchive/delete/fork` RPC，且不再回退本地 store。
- [x] `exec-server --remote` / remote registration 已实现最小可运行闭环：registry `/cloud/environment/{id}/register`、ChatGPT/API key/agent identity Bearer headers、rendezvous websocket JSON-RPC 服务；剩余 Rust Noise relay/多路复用严格对齐列入后续 exec-server 差异。
- [ ] App-server schema fixture 不能只比 method+params；出站 response/error/notification envelope 和 config/write 业务错误 data/code 已补，仍必须覆盖 result、更多 notification payload、更多业务错误 data/code 和 thread item union 全字段。
- [ ] Rust CLI parser/help/error/exit-code tests 必须翻译为 Go 回归，避免 Go parser 和 Rust clap surface 分叉。
- [ ] Rust rollout/thread-store fixture 必须导入，确保 CLI、app-server、TUI 对同一历史和 state DB 语义一致。
- [ ] Windows/Linux sandbox 不能只靠 unit；必须有 gated 真机矩阵证明不会在 unsupported host 静默降级成全权限执行。

## 开发计划

## 阶段 0：基线冻结与差异台账

目标：把当前状态变成可重复、可追踪的基线，并为后续 100% 对齐建立差异账本。

- [x] 统计 Rust workspace member、Rust 文件、Rust test-like 文件数量。
- [x] 统计 Go package、Go 文件、Go test 文件数量。
- [x] 本次运行 `go list -buildvcs=false ./...` 并确认通过。
- [ ] 用脚本生成 Rust crate 到 Go package 的机器可读映射，输出到 `docs/parity/crate_package_matrix.json` 或同等位置。
- [ ] 建立 `docs/parity/gaps.md`，每个 gap 记录 Rust source、Go source、测试入口、状态、owner/下一步。
- [ ] 固定默认测试 cache 命令，避免不同终端或 Windows 临时目录造成假失败。
- [ ] 把 `not implemented in the Go port yet`、`codex_go`、`LocalAgentRunner` fallback 全部列入 P0/P1 差异清单。

验收：

- [ ] 任意开发者能用同一组命令复现包加载和当前测试基线。
- [ ] 所有 P0 差异都有对应 Go test 或待建 test 入口。

## 阶段 1：CLI、Exec、Review 默认路径收口

目标：对齐 Rust `cli`、`exec`、`utils/cli` 的用户可见行为。

- [x] 将 `codex exec` 默认 runner 从 local stub 改为按 config/auth/provider 构造真实 `ResponsesAgentRunner`。
- [x] 保留 local stub 仅作为测试注入或显式离线 fallback，并确保用户默认路径不误用 stub。
- [ ] 翻译 Rust `cli/tests/*.rs`：login、features、exec_server、execpolicy、delete、plugin_cli、mcp、marketplace、app_server、update。
- [ ] 翻译 Rust `exec` human output、JSONL events、final JSON、error event、exit code fixtures。
- [ ] 收口 `review` prompt、diff、commit/base/uncommitted、title、exit code 和 output format。
- [ ] 补齐 `debug models`、`debug prompt-input`、`debug trace-reduce`、`debug clear-memories` 的 Rust hidden/visible 行为。
- [ ] 对 root flags、subcommand flags、aliases、hidden commands、strict-config、remote flag 支持矩阵做 golden。

验收：

- [ ] Go CLI parser/help/error fixture 与 Rust clap 输出一致或有明确兼容说明。
- [ ] 无凭据环境下默认测试全绿；有凭据环境下 `codex exec` 可真实请求模型。
- [ ] `codex exec` 不再默认输出 local stub 文案。

## 阶段 2：App-Server V2 与 SDK 高保真

目标：对齐 Rust `app-server`、`app-server-protocol`、`app-server-transport`，确保 IDE/SDK 可稳定使用 Go app-server。

- [ ] 扩展 schema fixture diff，从 method+params 扩展到 params/result/notification payload 全字段。
- [ ] 补业务模块专项错误映射：thread、turn、command/process、fs、config、MCP、plugin、account、feedback、realtime。
- [ ] 锁定 JSON-RPC `-32600/-32601/-32602/-32603` 和 Rust custom error data shape。
- [ ] 对 `ThreadItem` union 做全量 Rust fixture：message、reasoning、commandExecution、fileChange、mcpToolCall、dynamicToolCall、webSearch、image、contextCompaction、review mode。
- [ ] 跑 Python SDK contract：initialize、thread start、turn start/steer/cancel、command exec、MCP call、shutdown。
- [ ] 跑 TypeScript SDK contract：WebSocket auth、capability token、initialize、thread/turn、command/process、subscription。
- [ ] 完成 remote app-server capability token 与 SDK 端到端测试；真实 OS fs/watch 外部事件 watcher 主路径和 server request/notification targeted sink 已有 Go 回归覆盖。

验收：

- [ ] Rust JSON schema stable fixtures 全量通过。
- [ ] Rust TypeScript protocol surface diff 全量通过。
- [ ] Python/TypeScript SDK 最小 e2e 通过。

## 阶段 3：TUI 与远端交互入口

目标：补齐 Rust `tui` 产品级交互入口，而不是长期用行式 interactive 替代。

- [x] 确定 Go TUI 技术栈。见 `docs/tui_tech_selection.md`；采用 Bubble Tea + Bubbles + Lip Gloss + Glamour 作为 raw terminal adapter，保留 `internal/tui` 作为无依赖状态核心。
- [x] 建立本地 TUI MVP 状态/渲染层，提供 transcript、status/footer 和 slash commands 基础。
- [x] 引入 Bubble Tea 依赖并建立 root model，确保 `go mod tidy`、`go list -buildvcs=false ./...` 和 TUI/app 快速回归通过。
- [x] 建立 `internal/tui/tea` root model：message viewport、textarea composer、status/footer、slash commands、submit hook、turn completion message。
- [x] 建立 `internal/tui/markdown` Glamour `notty` 渲染入口，作为 Rust `markdown_render` 后续 fixture 对齐起点。
- [x] 将 Bubble Tea root model 接入 `internal/app/interactive.go` 的真实 TTY path，用 Bubble Tea command 包装 turn runner，并保留非真实 TTY/测试行式 fallback。
- [x] 扩展 root interactive raw terminal TUI：streaming message list、status/footer、最小 bottom pane、pending/running/idle 状态回流。
- [x] 建立通用 modal/picker 交互基础和 approval modal UI：选项列表、上下/Tab 导航、数字/快捷键选择、Enter 确认、Esc 取消、response callback，并阻断 composer 误输入。
- [x] 接入 shell approval/permission request 来源：tool output metadata 可打开 TUI approval modal，interactive approval broker 可把 allow once/allow session/deny 回灌到 shell executor，并缓存本会话 allow。
- [x] 按 Rust `codex-rs/tui/src` 补 Go foundation 模块：`width`、`line_truncation`、`wrapping`、`key_hint`、`selection_list`、`token_usage`、`status_indicator_widget`、`text_formatting`、`terminal_title`、`terminal_hyperlinks`、`terminal_palette`、`color`、`style`、`motion`、`shimmer`、`resize_reflow_cap`、`table_detect`、`live_wrap`、`markdown_text_merge`、`clipboard_copy/paste`。
- [x] 按 Rust `bottom_pane` 补 Go 基础包：`scroll_state`、`paste_burst`、`pending_input_preview`、`status_line_style`。
- [x] 按 Rust `history_cell` 补 Go 基础包：`base`、`messages`、`plans`、`exec`、`hooks`、`approvals`、`request_user_input`、`notices`、`separators`、`mcp`、`patches`、`search`、`session`，支持 display/raw lines、web hyperlink 标注、user/agent/reasoning、plan update/proposed plan、background terminal summary、hook lifecycle、approval decision、completed request_user_input、notice、final separator、MCP tool/inventory、patch/image/search/session header。
- [x] 按 Rust `streaming` 补 Go 基础包：`table_holdback`、`chunking`、`controller`、`commit_tick`，具备 table mutable tail、adaptive catch-up drain、stable queue/tail 两区模型。
- [x] 按 Rust `diff_model`、`diff_render`、`exec_cell` 补 Go 基础模块：FileChange、diff summary/render、exec call lifecycle、exploring grouping、output truncation 和 unified exec interaction preview。
- [ ] 扩展 composer 与终端细节：Ctrl+J 显式换行、Ctrl+G/$VISUAL/$EDITOR 外部编辑器终端 handoff、paste-like burst Enter 换行、Ctrl+C running interrupt、terminal focus tracking、streaming resize reflow、VT100-style terminal snapshot、真实 PTY terminal restore gated smoke 入口、chatwidget transcript overlay/pager/滚动保持/Home/End/鼠标滚轮、Ctrl+O 复制最后 agent response、rate-limit 阈值 warning 与 Responses stream routing、`/usage` 菜单/token summary/status_state/token chart、`user_messages/input_queue`、status_surfaces、status_controls 纯逻辑和 Tea command/modal/OSC 已对齐；Windows ConPTY 已补 gated smoke + host output probe，仍需支持 ConPTY 输出宿主上的完整通过记录。
- [ ] 补齐剩余 approval/permission 语义：network approval、approval amendment、非 shell tool approval、session-level policy persistence 与 Rust fixture。
- [x] 完善 request_user_input notes/unanswered confirmation 和结构化 answer list。
- [x] 完善本地 session picker `/fork`、`/archive`、`/unarchive`、`/delete` 交互与 store mutation hook。
- [ ] 完善 MCP elicitation richer form editing。
- [x] 支持 TUI image/file attachment draft UI 和 prompt text carry-forward。
- [x] 支持 structured image/file attachment wire：TUI SubmitRequest 保留附件，interactive bridge 转 `turn.TurnUserInput`，exec 构造 user input item，本地图片转 data URL，session content 保留 image/localImage 路径。
- [x] 支持 composer running queue 主路径：任务运行中 Enter/Tab 入队，空闲 Tab 等价提交，turn 完成后自动提交下一条 queued request。
- [x] 支持 remote app-server TUI ws/wss 主路径：`--remote ws://...`/`wss://...` 进入 Bubble Tea TUI，初始化 app-server，首轮空建 thread，用户输入走 `turn/start`，结构化 text/file/localImage/remote image 输入保留在 `TurnStartParams.input`，远端 thread/turn/item/delta/error/warning 通知转成 TUI stream events，支持 auth token env。
- [x] 支持 remote app-server TUI `unix://` transport：TUI client 抽象 websocket/JSON-line transport，`unix://` 走 Go app-server 现有 UDS JSON-RPC line 协议，保留与 ws/wss 相同 initialize/thread/start/turn/start/notification 语义。
- [x] 补齐 remote app-server TUI Rust-supported 核心 server requests：commandExecution/fileChange/permissions approval、MCP elicitation、request_user_input 可从远端请求打开 TUI modal 或返回结构化结果；dynamic tool、attestation、currentTime/read、legacy applyPatch/exec approval 按 Rust TUI unsupported 语义返回 `-32000`。
- [x] 补齐 CLI session remote app-server handoff：`resume/fork/archive/delete/unarchive --remote` 通过远端 app-server `thread/list/read/archive/unarchive/delete/fork` RPC 实现 UUID、精确 name 和 picker/last 主路径。
- [x] 补齐 remote Bubble Tea TUI 内部 slash session handoff：`/resume` picker 从远端 `thread/list` 拉取 active+archived sessions，`/fork`/`/archive`/`/unarchive`/`/delete` 通过远端 app-server `thread/fork/archive/unarchive/delete` RPC 执行并同步 TUI session items。
- [ ] 支持 diff/file change display、terminal/background terminal panel。
- [ ] 支持 interrupt/cancel/continue、turn steering、compact、rollback、fork/resume/archive/delete。
- [x] 补齐 remote app-server TUI unsupported server-request 确定响应：dynamic tool call、attestation generate 不再落入泛化 `-32601`，改为 Rust TUI 同类 `-32000` reject；`currentTime/read` 保持成功响应。
- [x] 补齐 remote app-server TUI ChatGPT auth token refresh 真刷新：remote client 解码 `account/chatgptAuthTokens/refresh`，使用本地 ChatGPT refresh token 刷新并返回 `accessToken/chatgptAccountId/chatgptPlanType`。
- [x] 补齐 remote app-server TUI targeted sink/fs changed 主路径：server request 和 notification sink 均支持 connection target；`fs/watch` 现在在 app-server fs 写/建目录/删/拷贝成功后按 connection 发送 `fs/changed`。
- [x] 补齐 remote app-server TUI 真实 OS fs/watch 外部事件：`fs/watch` 现在会对 app-server 外部文件写入/创建/删除变化按 connection 发送 `fs/changed`。
- [x] 补齐 remote app-server TUI hook lifecycle 主路径：`hook/started` 与 `hook/completed` 通知映射为 Tea `HookRunMsg`，TUI 用 Rust `chatwidget/hook_lifecycle.rs` 同类文案展示 running/completed/warning/context/error。
- [x] 补齐 remote app-server TUI `/hooks` browser 主路径：slash command 通过 `hooks/list` 拉取 registry/discovery 清单，展示 hook metadata、managed/review badge、warnings/errors；无 app-server reader 时回退本地 hook lifecycle 浏览。
- [x] 补齐 remote app-server TUI `/plugins` catalog 主路径：slash command 通过 `plugin/list` 拉取 marketplace/plugin catalog，保留 installed/enabled/marketplace/version/share/load error 状态，并修正 `includeInstalled` JSON-RPC wire 字段。
- [x] 补齐 remote app-server TUI `/skills` manage 主路径：slash command 选择 Enable/Disable Skills 后通过 `skills/list` 拉取 cwd inventory，展示 enabled/disabled、scope、plugin、path 与 skill errors；无 app-server reader 时保留空 inventory fallback。
- [ ] 补齐 remote app-server TUI 真长尾：Rust `PendingAppServerRequests`/remote client 更细行为。
- [ ] 实现 session commands 的 remote app-server handoff。
- [x] 补 TUI View 级 snapshot/golden smoke：主视图、approval modal、session picker、request_user_input 终端 View 已由 inline golden 锁定。
- [x] 补 TUI VT100-style snapshot/golden：主视图、approval modal、ANSI SGR、clear、cursor move、pending wrap 和固定宽度状态行/页脚已由虚拟终端快照锁定。
- [ ] 补 TUI 真实 PTY terminal restore smoke：Unix 侧已新增 gated 真 PTY alt-screen restore smoke；Windows 侧已补 `CODEX_GO_TUI_PTY_SMOKE=1` gated ConPTY restore smoke，先跑 host output probe，probe 通过后启动真实 TUI child 并断言 alt-screen enter/leave；当前宿主 probe 无可读输出时按 Rust legacy ConPTY limitation skip，仍需在支持 ConPTY 输出的 Windows 真机矩阵记录完整通过。
- [ ] 修复 `request_plugin_install` 在 `codex-tui` client 下被禁用的问题，或与 Rust 当前产品策略完全一致并补 fixture。

验收：

- [ ] Go root `codex` 交互体验达到 Rust `tui` 等价功能。
- [ ] remote TUI 和 local TUI 共用 app-server/turn/runtime 语义；ws/wss+unix remote turn 主链路、核心 server requests、ChatGPT auth refresh 真刷新、server request/notification targeted sink、fs/watch app-server 操作与真实外部文件变化触发通知、unsupported server-request 确定响应、CLI session remote handoff 与 remote TUI slash session action 已接入，仍需真实终端 smoke 与 Rust remote client 长尾 fixture。
- [ ] 行式 interactive 只作为 fallback 或测试工具，不作为 100% TUI 完成依据。

## 阶段 4：Model Provider、Auth、Account

目标：让真实 provider/auth 环境中的行为与 Rust 一致。

- [ ] 建立 provider request golden：OpenAI、ChatGPT、OSS、Ollama、LM Studio、Bedrock、Azure Responses endpoint。
- [ ] 建立 auth matrix：API key、ChatGPT OAuth、device code、external `chatgptAuthTokens`、PAT、agent identity、auth.command。
- [ ] 完成 OS keyring/keychain 存取 e2e 和 fallback 行为。
- [ ] 补 ChatGPT OAuth/device-code route config、proxy、cancel、port retry、forced workspace/login method e2e。
- [ ] 补 token refresh：permanent failure、transient failure、timeout/cancel、app-server server request、CLI status。
- [ ] 补 AWS role/web identity/SSO/IMDS/SigV4 和 Bedrock bearer 的 live-gated tests。
- [ ] 补 retry/timeout/rate-limit/stream idle timeout/debug context fixtures。
- [ ] 补 structured output、`store`、`previous_response_id`、service tier、prompt cache key 请求体 golden。

验收：

- [ ] Mock provider tests 默认全绿。
- [ ] Live provider tests 在缺凭据时清晰 skip，在有凭据时覆盖真实请求。
- [ ] CLI 和 app-server 使用相同 provider/auth selection 语义。

## 阶段 5：Tool Runtime、Hooks、MCP、Plugin、Skills、Apps

目标：对齐模型可见工具、discoverable/deferred 工具和插件生态。

- [ ] 翻译 Rust tool suite：shell、apply_patch、tool_search、dynamic tools、MCP resource/tool、multi-agent、agent jobs、request_user_input、request_permissions、plan、sleep/current_time。
- [ ] 锁定 tool request/output/error JSON shape，包含 fatal/non-fatal、model-visible、approval request、retry context。
- [ ] 补 network approval、approval amendment、Windows command safety、shell escalation、hook fatal/blocking/rewrite fixtures。
- [ ] MCP live：stdio、streamable HTTP、SSE、roots/list、elicitation、progress、resources、resourceTemplates、tools。
- [ ] MCP OAuth live：discovery、dynamic registration、PKCE、callback server、refresh、invalid_grant cleanup、cancel。
- [ ] MCP session cache：HTTP DELETE、stale 404/410 rebuild、stdio process reuse、config reload cleanup。
- [ ] Plugin marketplace：local/git/remote add/read/install/uninstall/share/upgrade/materialize/cache rebuild。
- [ ] Skills：frontmatter、openai.yaml aliases、remote skill root、dependencies、implicit invocation、budget、telemetry。
- [ ] Apps/connectors：ChatGPT directory live、codex_apps accessibility cache、connector accept force refresh、synthetic/disabled filtering。

验收：

- [ ] `internal/mcp`、`internal/plugin`、`internal/apps`、`internal/prompt`、`internal/tool` 有 fixture、mock integration、live-gated 三层测试。
- [ ] Rust MCP/plugin/skills/connectors wire shape 关键字段全量通过。

## 阶段 6：Session、Rollout、Thread Store、State

目标：会话持久化、历史重建和 state DB 与 Rust 完全兼容。

- [ ] 导入 Rust rollout JSONL fixtures，覆盖 message/tool/reasoning/diff/compact/review/agent/subagent。
- [ ] 对 Go 生成 rollout 做 Rust-compatible snapshot。
- [ ] 补 resume/fork/rollback/archive/unarchive/delete 的 file move、metadata、source、relation 字段 fixture。
- [ ] 收口 thread list/search cursor、sourceKinds、relation filter、sortKey、sortDirection、limit clamp、historyMode。
- [ ] 补 compact 后继续 turn：summary history、compacted marker、token metadata、hook notification。
- [ ] 补 state/memories/history 旧文件迁移、Windows handle cleanup、path canonical、并发读写。
- [ ] 补 external-agent sessions ledger/migration 的 Rust fixture。

验收：

- [ ] CLI、TUI、app-server 对同一 thread store 读写一致。
- [ ] Rust rollout/thread-store/state fixtures 在 Go 侧通过。

## 阶段 7：Sandbox、Exec-Server、Network Proxy

目标：收口 Rust sandboxing、exec-server、network-proxy 的系统集成。

- [ ] Windows restricted token：覆盖 admin/non-admin、`WRITE_RESTRICTED` capability、unsupported host skip。
- [ ] Windows elevated sandbox：setup/provision、read ACL refresh、user secret、runner IO、exit code、cleanup。
- [ ] Windows WFP/firewall：offline/online/proxy/loopback allow、规则回收、文案、失败恢复。
- [ ] Windows ACL/deny-read/workspace：read-only、workspace-write、full-access、missing path、glob、stale revoke、junction cwd。
- [ ] Windows ConPTY：TTY、resize、interrupt、terminate、output drain；terminal restore 已有 gated smoke/probe，仍需支持宿主完整通过记录。
- [ ] Linux bwrap：read-only、workspace-write、full-access、tmpfs masking、ro-bind-data、cwd/env/exit code。
- [ ] Linux Landlock/seccomp：capability detection、network deny、filesystem allow/deny、fallback/unsupported 文案。
- [ ] Linux execve wrapper：DGRAM/STREAM handshake、SCM_RIGHTS FD passing、Run/Escalate/Deny。
- [ ] Exec-server：process lifecycle、PTY/resize、stdout/stderr streaming、fs/http remote env、sandbox backend request、Rust Noise relay/streamResponse 严格对齐。
- [x] 实现 remote exec-server registration 最小闭环，替换当前 `not implemented`。
- [ ] Network proxy：credential broker、MITM/upstream/certs/policy reload、sandbox no-network env。

验收：

- [ ] unsupported host 不静默全权限执行。
- [ ] tool、app-server、exec-server 三条入口共用 permission profile 语义。
- [ ] 平台测试默认 gated，且有 cleanup 和 skip reason。

## 阶段 8：Doctor、Install、Update、Telemetry、外围

目标：补齐 Rust 外围诊断、安装、更新、遥测和 IDE 辅助能力。

- [ ] Doctor snapshot：auth、config、git、terminal、state、rollout DB parity、sandbox、MCP、network、provider、installation、updates。
- [ ] Install/update：npm/bun/cargo/managed package root proof、update target、mismatch、download/reexec。
- [ ] Telemetry/eventmap：Rust event map diff、privacy boundary、accepted lines、memory usage、OTEL exporter mock/live。
- [ ] Realtime：WebRTC/auth/app-server RPC integration。
- [ ] Code mode：protocol fixture、host lifecycle、IDE integration。
- [ ] File search/context/file system：ignore rules、大仓库性能、watcher、remote environment edge。

验收：

- [ ] Doctor human/JSON 输出与 Rust snapshot 高保真一致。
- [ ] install/update/telemetry 默认 mock 全绿，live-gated 有明确 skip 条件。

## 阶段 9：Rust 回归用例翻译顺序

优先翻译高信号、低环境依赖测试，然后再补 live/smoke。

1. [ ] App-server protocol/schema fixtures：method、params、result、notification、business error、ThreadItem union；已补 response/error/notification envelope 与 config/write error code 子集。
2. [ ] CLI parser/help/error/exit code fixtures。
3. [ ] Exec/review JSONL/human/final output fixtures。
4. [ ] Core turn runtime：tool ordering、usage、interrupt、compact、guardian、multi-agent。
5. [ ] Rollout/thread-store/state：JSONL、resume/fork/rollback、cursor、migration。
6. [ ] Tools/apply-patch/hooks/approval policy/network approval。
7. [ ] Model provider/auth request body/header/retry fixtures。
8. [ ] MCP protocol fixtures：status/tool/resource/OAuth/error mapping。
9. [ ] Plugin/skills/connectors wire/discovery/materialization fixtures。
10. [ ] Exec-server protocol fixtures。
11. [ ] Sandbox permission profile/platform unsupported fixtures。
12. [ ] Doctor/install/update/telemetry snapshots。
13. [ ] TUI snapshots and keyboard interaction fixtures。

验收：

- [ ] 每翻译一批 Rust 测试，记录 Rust 文件、Go test、命令、结果、剩余差异。
- [ ] 不用 Go 当前行为反向改测试，除非确认 Rust 行为已经变更。

## 阶段 10：100% 对齐发布门禁

目标：所有功能域关闭后，建立最终 release candidate 验收。

- [ ] `go list -buildvcs=false ./...` 连续 3 次通过。
- [ ] 默认 `go test ./... -count=1` 连续 3 次通过。
- [ ] Rust fixture/golden suites 全量通过。
- [ ] CLI command tree/help/error/exit code 与 Rust 对齐。
- [ ] App-server Rust schema + SDK e2e 通过。
- [ ] TUI local/remote smoke 通过。
- [ ] Provider/auth live matrix 在有凭据环境通过，缺凭据环境清晰 skip。
- [ ] MCP/plugin/skills/apps live matrix 在有配置环境通过，缺配置环境清晰 skip。
- [ ] Windows sandbox gated matrix 通过或给出 Rust 等价 unsupported reason。
- [ ] Linux sandbox gated matrix 通过或给出 Rust 等价 unsupported reason。
- [ ] Exec-server/network proxy live matrix 通过。
- [ ] 删除或降级所有用户默认路径上的 `not implemented`、local stub、silent fallback。

验收：

- [ ] 全量产品口径达到 100%。
- [ ] 新增差异必须先进入 gap 台账，不能隐式放行。

## 测试命令基线

包加载闸门：

```powershell
go list -buildvcs=false ./...
```

默认全量测试：

```powershell
& {
  $env:GOCACHE = (Join-Path $PWD '.gocache')
  $env:GOTMPDIR = (Join-Path $PWD '.gotmp')
  New-Item -ItemType Directory -Force $env:GOCACHE, $env:GOTMPDIR | Out-Null
  go test ./... -count=1
}
```

P0 快速回归：

```powershell
& {
  $env:GOCACHE = (Join-Path $PWD '.gocache')
  $env:GOTMPDIR = (Join-Path $PWD '.gotmp')
  New-Item -ItemType Directory -Force $env:GOCACHE, $env:GOTMPDIR | Out-Null
  go test ./cmd/codex ./internal/cli ./internal/app ./internal/exec ./internal/review -count=1
  go test ./internal/appserver -count=1
  go test ./internal/turn ./internal/tool ./internal/model ./internal/session ./internal/rollout -count=1
}
```

MCP/plugin/skills/apps 编译闸门：

```powershell
& {
  $env:GOCACHE = (Join-Path $PWD '.gocache')
  $env:GOTMPDIR = (Join-Path $PWD '.gotmp')
  New-Item -ItemType Directory -Force $env:GOCACHE, $env:GOTMPDIR | Out-Null
  go test -run '^$' ./internal/mcp ./internal/plugin ./internal/apps ./internal/prompt ./internal/tool ./internal/appserver
}
```

Windows sandbox gated smoke：

```powershell
& {
  $env:CODEX_WINDOWS_SANDBOX_SMOKE = 'elevated'
  $env:GOCACHE = (Join-Path $PWD '.gocache')
  $env:GOTMPDIR = (Join-Path $PWD '.gotmp')
  New-Item -ItemType Directory -Force $env:GOCACHE, $env:GOTMPDIR | Out-Null
  go test ./internal/sandbox/windowssandbox -run TestWindowsSandboxSmoke -count=1
}
```

```powershell
& {
  $env:CODEX_WINDOWS_SANDBOX_SMOKE = 'restricted'
  $env:GOCACHE = (Join-Path $PWD '.gocache')
  $env:GOTMPDIR = (Join-Path $PWD '.gotmp')
  New-Item -ItemType Directory -Force $env:GOCACHE, $env:GOTMPDIR | Out-Null
  go test ./internal/sandbox/windowssandbox -run TestWindowsSandboxSmoke -count=1
}
```

Linux sandbox 编译/真机入口：

```bash
go test ./internal/sandbox ./internal/sandbox/linuxsandbox -count=1
go test -c ./internal/sandbox
```

Live provider/MCP/plugin/SDK/sandbox/network 测试原则：

- [ ] 默认无凭据、无系统修改时必须 skip，不得 fail。
- [ ] skip reason 必须说明缺少的环境变量、凭据、平台能力或管理员权限。
- [ ] live 测试必须有 cleanup，不能留下远端 task、plugin install、WFP rule、ACL、临时用户或 keychain 污染。

## 风险与阻塞

- TUI 是最大明确功能缺口；没有完整 TUI 时不能宣称产品级 100%。
- CLI `exec` 默认 runner 已改为真实 Responses/OSS provider；剩余风险转为 parser/help/error、JSONL/human output fixture 与 live provider matrix。
- App-server 已实现很深，但 SDK/IDE contract 和 business error data 如果没有 fixture，容易产生隐蔽 wire 分叉。
- Windows sandbox/WFP/ACL 和 Linux bwrap/Landlock/seccomp 依赖宿主能力，必须用 gated 真机矩阵而不是普通 unit 代替。
- Live provider/auth/MCP/plugin/connector 测试依赖网络和凭据，默认全量只能覆盖 mock/fixture。
- Rust workspace 包含部分实验或辅助 crate，如 `v8-poc`、`thread-manager-sample`、`test-binary-support`；100% 前需明确是否需要 Go 等价实现、测试替代，或记录为非产品目标。

## 下一轮执行顺序

1. [x] 修复 `codex exec` 默认 runner，接入真实 Responses/OSS provider，保留 local stub 仅用于测试。
2. [ ] 翻译 Rust CLI parser/help/error/exit-code tests，锁定命令树；已补 app-server global flag/auth/remote-auth 子命令名与 feature toggle 错误文案子集。
3. [ ] 扩展 app-server schema fixture 到 result/notification/business error 全字段。
4. [ ] 继续推进 `internal/tui/chatwidget` + `internal/tui/tea`：Windows ConPTY restore 支持宿主完整通过记录，以及 Rust `tui/src` 轻量接口壳业务/golden 收口。
5. [x] remote Bubble Tea TUI 内部 `/resume`/`/fork`/`/archive`/`/delete` 已 handoff 到远端 app-server，并复用远端 session client/helper。
6. [x] 实现 remote exec-server registration。
7. [ ] 建立 provider/auth mock golden + live-gated matrix。
8. [ ] 建立 Windows/Linux sandbox 真机矩阵。
9. [ ] 建立 MCP/plugin/skills/apps live-gated matrix。
10. [ ] 导入 rollout/thread-store/state Rust fixtures。

## 工作日志

### 2026-07-09

- [x] Continued P0 Rust app-server v2 parity with unrestricted local Rust reads: extended `TestProtocolPayloadsValidateAgainstRustSchemas` beyond `ThreadStartResponse` to cover JSON-RPC envelopes, `ThreadReadResponse`, `ThreadListResponse`, `ThreadLoadedListResponse`, `ThreadResumeResponse`, `ThreadForkResponse`, `ThreadRollbackResponse`, `ThreadMetadataUpdateResponse`, command/exec params and control responses, and the fs params/results/changed notification surface.
- [x] Matched Rust `app-server/tests/suite/v2/initialize.rs` opt-out behavior by adding `TestRuntimeRouterInitializeOptOutNotificationMethodsFiltersThreadStarted`, locking that `optOutNotificationMethods: ["thread/started"]` suppresses the `thread/started` notification while preserving successful `thread/start`.
- [x] Matched Rust `app-server/tests/suite/v2/command_exec.rs` router-level timeout conflict semantics by extending `TestRuntimeRouterCommandExecInvalidRequestAndParamsCodes` for `timeoutMs` plus `disableTimeout`, expecting JSON-RPC invalid params `-32602` with `command/exec cannot set both timeoutMs and disableTimeout`.
- [x] Verification: `$env:GOCACHE='D:\qax\reagent\dev\codex_go\.gocache'; go test ./internal/appserver -run TestProtocolPayloadsValidateAgainstRustSchemas -count=1 -v` passed; `go test ./internal/appserver -run "TestRuntimeRouter(InitializeRejectsInvalidClientName|InitializeUserAgentOriginator|InitializeOptOutNotificationMethodsFiltersStatusChanged|InitializeOptOutNotificationMethodsFiltersThreadStarted|RejectsRemoteImageTurnInputs)|TestRouterInjectItemsRejectsRemoteImageURLs" -count=1 -v` passed; `go test ./internal/appserver -run "Test(CommandExecParamsValidateMessagesMatchRust|RuntimeRouterCommandExecInvalidRequestAndParamsCodes|CommandExecParamsJSONMatchesRustShape)" -count=1 -v` passed; `go test ./internal/appserver -count=1` passed.

- [x] Continue P0 app-server protocol parity from `plan_code.md`: expanded Go `TestProtocolPayloadsValidateAgainstRustSchemas` to validate a `TurnCompletedNotification` containing every Rust `ThreadItem` union branch (`userMessage`, `hookPrompt`, `agentMessage`, `plan`, `reasoning`, `commandExecution`, `fileChange`, `mcpToolCall`, `dynamicToolCall`, `collabAgentToolCall`, `subAgentActivity`, `webSearch`, `imageView`, `sleep`, `imageGeneration`, `enteredReviewMode`, `exitedReviewMode`, `contextCompaction`) plus `ItemStartedNotification` and `ItemCompletedNotification` payloads.
- [x] Fixed the Go Rust-schema test harness to support JSON Schema boolean nodes (`true` accepts any value, `false` rejects), which Rust uses for open JSON fields such as tool `arguments`; without this, valid Rust `mcpToolCall`/`dynamicToolCall` payloads were falsely rejected.
- [x] Verification: `$env:GOCACHE='D:\qax\reagent\dev\codex_go\.gocache'; go test ./internal/appserver -run "Test(BuildTypeScriptProtocolSchemaMatchesRustFixtures|BuildProtocolSchemaMatchesRustStableFixtures|ProtocolPayloadsValidateAgainstRustSchemas)" -count=1 -v` passed; `go test ./internal/appserver -count=1` first hit a known Windows TempDir cleanup race in `TestRuntimeRouterTurnStartAppliesExplicitPersonality`, and the package rerun passed.

- [x] 按用户最新反馈继续暂停 TUI 长尾，优先对齐 Rust 默认启动读取本地 `config.toml`/`auth.json` 的 provider/auth 行为；定位到 Rust auth 存储使用 `auth_mode = "apikey"`，Go 之前只识别内部 `"api-key"`，导致本地已有 `OPENAI_API_KEY` 时仍未生成 Authorization header，并在真实 Responses 请求上报 401 `API_KEY_REQUIRED`。
- [x] Go `internal/auth`：`AuthDotJSON.Mode()` 增加 Rust 存储别名归一化，支持 `apikey`/`apiKey`、`chatgptAuthTokens`、`agentIdentity`、`personalAccessToken`、`bedrockApiKey` 等 wire/storage 形状；`FromAPIKey`、agent identity、Bedrock API key 写回 Rust 同款 storage value，保持内部判断继续使用 `"api-key"`、`"agent-identity"` 等规范值。
- [x] Go `internal/exec`：默认 `NewRunner` 现在用本地配置解析 `model`、`model_provider`、`model_providers.*.base_url`、`wire_api = "responses"` 与 `requires_openai_auth`，并用本地 `auth.json` 的 Rust `apikey` 形状生成 `Authorization: Bearer ...`；当 OpenAI auth 必需但本地/环境/provider auth 都缺失时，在发请求前返回清晰错误，避免无 Authorization 的远端 401。
- [x] Go `internal/auth/account.go`、`internal/appserver/auth_status.go`、`internal/doctor`：账号通知/状态/doctor 输出对外使用 Rust wire auth mode（例如 `apikey`、`agentIdentity`），内部校验复用规范化后的 `Mode()`；doctor 不再把 `apikey` 误判成 ChatGPT token 缺失。
- [x] 新增/调整回归：`TestResolveReadsRustAPIKeyAuthModeAlias`、`TestAuthModeAliasesMatchRustStorage`、`TestNewRunnerReadsRustAuthModeAliasAndConfiguredProvider`、`TestNewRunnerFailsBeforeRequestWhenOpenAIAuthMissing`、`TestApplyAuthSnapshotUsesRustWireAuthMode`、doctor auth-mode alias 覆盖；同时保留 no-auth、configured provider、Responses path/header、last message 等启动主链路断言。
- [x] 验证：`go test ./internal/auth ./internal/model ./internal/exec ./internal/appserver -count=1` 通过；`go test ./internal/doctor -count=1` 通过；`go test ./... -count=1` 通过；临时构建主命令并运行 `features list` 二进制 smoke 通过。
- [x] 修复用户实测第二个启动错误：HTTP/SSE Responses 请求携带 `previous_response_id` 会被真实服务拒绝并返回 `previous_response_id is only supported on Responses WebSocket v2`；对齐 Rust `core/src/client.rs` 中只有 Responses WebSocket v2 增量 create 才使用 previous response id 的行为，Go `internal/model/ResponsesAgentRunner` 现在不再把上层 `PreviousResponseID` 序列化到 HTTP/SSE body，继续通过完整 `input` 历史保持上下文。
- [x] 新增/调整回归：`TestResponsesAgentRunnerSendsStoreAndMetadataFieldsWithoutHTTPPreviousResponseID` 锁定非流式 HTTP 不带 `previous_response_id`；`TestResponsesAgentRunnerStreamsResponsesSSE` 覆盖 SSE 流式请求即便上层传入 `PreviousResponseID` 也不带该字段。
- [x] 验证：`go test ./internal/model -run "TestResponsesAgentRunnerSendsStoreAndMetadataFieldsWithoutHTTPPreviousResponseID|TestResponsesAgentRunnerStreamsResponsesSSE" -count=1 -v` 通过；`go test ./internal/model ./internal/turn ./internal/exec ./internal/appserver ./internal/app -count=1` 通过；`go test ./... -count=1` 通过。
- [x] 按用户要求暂停 TUI 长尾，优先修复非 TUI 系统运行闭环；对照 Rust `exec/src/lib.rs` 的 `run_main`/`run_exec_session` 与 `core/src/client.rs` 的真实 model provider client 构造路径，确认 Rust 默认通过 in-process app-server/core session 请求真实 Responses/OSS provider，不存在默认 local stub 回答器。
- [x] Go `internal/exec`：`NewRunner` 默认启用 `UseResponsesAPI`，按 config/auth/provider 构造 `ResponsesAgentRunner`；新增 `NewLocalRunner` 作为显式测试/离线 stub 入口，避免普通 `codex exec` 默认输出 `Go Codex exec stub received: ...`。
- [x] Go `internal/app`：`exec`、`review`、interactive prompt/TUI runner、MCP codex tool runner 统一走 `newCodexExecRunner` factory；生产默认仍是真实 `NewRunner`，单测用 `NewLocalRunner` 显式替代旧 stub。
- [x] 新增/调整回归：`TestNewRunnerDefaultsToResponsesAPI` 用 httptest 验证默认 runner 请求 `/v1/responses`、携带 API key、输出真实 mock 响应且不含 stub 文案；`TestExecJSONEndToEnd` 改为 mock Responses server 覆盖 CLI app 入口；旧 stub 相关 exec/app 测试改为显式 `NewLocalRunner`。
- [x] 验证：`go list -buildvcs=false ./...` 通过；使用仓库内 `.gocache/.gotmp` 运行 `go test ./internal/exec ./internal/app -run "TestNewRunnerDefaultsToResponsesAPI|TestExecJSONEndToEnd|TestRunJSONAndLastMessage|TestRunWarnsForRemovedFullAuto|TestRunExecReview|TestRunEphemeralSkipsSessionPersistence|TestInteractivePromptUsesExecRunner|TestInteractiveWithoutPromptRunsLineSession|TestInteractiveSlashCommandsUpdateTUIState|TestExecPromptFromStdinEndToEnd|TestReviewEndToEnd|TestExecReviewEndToEnd|TestReviewCustomPromptEndToEnd" -count=1 -v` 通过；`go test ./internal/exec ./internal/app -count=1` 通过；`go test ./... -count=1` 通过。
- [x] 说明：首次未设置仓库内 cache 的定向测试在 Windows 默认 Go build cache 命中 `Access is denied`，按本文件规则改用 `.gocache/.gotmp` 后通过。剩余非 TUI 差异：Rust CLI parser/help/error/exit-code fixture、exec human/JSONL output fixture、provider/auth live-gated matrix、rollout/thread-store fixture 与 sandbox 真机矩阵。
- [x] 对照 Rust `cli/src/main.rs` 的 `run_exec_server_command`/`load_exec_server_remote_auth_provider` 与 `exec-server/src/remote.rs`、`environment_registry.rs`、`tests/relay.rs`，实现 Go `exec-server --remote` 最小可运行闭环：按 `/cloud/environment/{environment_id}/register` 发送 `security_profile=noise_hybrid_ik_v1` 与 executor public key，校验 registry 返回的 environment/security profile/registration id，并连接 registry 下发的 rendezvous websocket 服务现有 exec-server JSON-RPC。
- [x] Go `internal/execserver`：新增 `RemoteEnvironmentConfig`、`RunRemoteEnvironment`、registry client/error 处理、X25519+ML-KEM-768 形状公钥生成、rendezvous reconnect/backoff；当前先使用直连 JSON-RPC websocket transport，后续再补 Rust Noise hybrid IK、多路复用和 harness key validation 的严格等价实现。
- [x] Go `internal/app`：`runExecServerRemote` 不再返回 `remote exec-server registration is not implemented in codex_go`；ChatGPT/API key/agent identity auth 统一转为 registry `Authorization: Bearer ...`，ChatGPT 带 `ChatGPT-Account-ID`，API key 继续复用 Rust host 限制校验。
- [x] 新增/调整回归：`TestRunRemoteEnvironmentRegistersAndServesRendezvousWebSocket` 覆盖 registry request/header/body、公钥形状和 rendezvous `initialize` RPC；`TestRegisterRemoteEnvironmentReportsRegistryErrors` 覆盖 auth error；`TestExecServerRemoteValidationLikeRust` 改为本地 mock registry/rendezvous 成功闭环并保留 no-auth/API-key host/agent identity env 错误校验。
- [x] 验证：使用仓库内 `.gocache/.gotmp` 运行 `go test ./internal/execserver ./internal/app -run "TestRunRemoteEnvironmentRegistersAndServesRendezvousWebSocket|TestRegisterRemoteEnvironmentReportsRegistryErrors|TestExecServerRemoteValidationLikeRust" -count=1 -v` 通过；`go test ./internal/execserver ./internal/app -count=1` 通过；`go test ./... -count=1` 通过。
- [x] 对照 Rust `cli/src/main.rs` 内联 parser tests 的 app-server listen/auth/remote 子命令与 feature toggles 子集，修复 Go `internal/cli` 在 app-server 子命令解析时提前返回导致全局 `--stdio/--listen` 冲突、websocket auth path/hash 校验被跳过的问题；`--remote-auth-token-env` 对 app-server tooling 子命令的错误名现在包含 `app-server proxy`、`app-server daemon version`、`app-server generate-internal-json-schema` 等 Rust 同款 surface。
- [x] 对齐 Rust feature toggle 错误文案：`features.Validate` 与 app 入口未知 feature 现在返回 `Unknown feature flag: ...`，覆盖 `--enable`、`--disable` 与 compound key `multi_agent_v2.subagent_usage_hint_text`。
- [x] 新增/调整回归：`TestParseAppServerListenValidation` 补 app-server subcommand 后仍校验 `--stdio`/`--listen` 冲突；`TestParseAppServerWebSocketAuthFlags` 补 subcommand 后仍校验 websocket auth flags 与 removed insecure non-loopback flag；`TestRejectRemoteAuthEnvForAppServerSubcommandsNamesRustSurface` 锁定 remote-auth 错误名；`TestKnownFeature`/`TestUnknownFeatureToggleFails` 锁定 feature 文案。
- [x] 验证：`go test ./internal/cli -run "TestParseAppServerListenValidation|TestParseAppServerWebSocketAuthFlags|TestRejectRemoteAuthEnvForAppServerSubcommandsNamesRustSurface" -count=1 -v` 通过；`go test ./internal/cli ./internal/features ./internal/app -run "TestParseAppServerListenValidation|TestParseAppServerWebSocketAuthFlags|TestRejectRemoteAuthEnvForAppServerSubcommandsNamesRustSurface|TestKnownFeature|TestUnknownFeatureToggleFails" -count=1 -v` 通过；`go test ./internal/cli ./internal/features ./internal/app -count=1` 通过；`go list -buildvcs=false ./...` 通过；`go test ./... -count=1` 首次命中既有 `internal/appserver/TestRuntimeRouterTurnStartSendsAppServerAttestationHeader` Windows TempDir cleanup 抖动，失败用例与 `go test ./internal/appserver -count=1` 单独重跑通过，第二次 `go test ./... -count=1` 通过。
- [x] 对照 Rust `app-server-protocol/src/rpc.rs`、`app-server-transport/src/outgoing_message.rs` 与 transport overload/configWarning fixture，Go `internal/appserver` 的出站 `OK`、`ErrorResponse`、`NewNotification` 不再填充 `"jsonrpc":"2.0"`，保持 Rust “既不发送也不要求 jsonrpc 字段”的 wire shape；新增 `TestOutgoingMessagesMatchRustJSONRPCShape` 锁定 success response、error response 与 `configWarning` notification 的精确 JSON。
- [x] 对照 Rust `app-server/src/request_processors/config_processor.rs`、`config_manager_service.rs` 与 `tests/suite/v2/config_rpc.rs`，Go `internal/config` 新增 `ConfigWriteErrorCode`/`ConfigWriteError`，通过结构化 `JSONRPCErrorData()` 返回 `config_write_error_code`；`config/value/write` 和 `config/batchWrite` 现在按 Rust 只允许写当前 user/profile config path，其他绝对路径返回 `configLayerReadonly`，版本冲突返回 `configVersionConflict`，legacy profile/profile tables 返回 `configValidationError`。
- [x] 新增/调整回归：`TestServiceWriteValidation` 覆盖 config write code、版本冲突、只写 user config 且不触碰其它路径；`TestRuntimeRouterConfigRejectsLegacyProfileWrite` 增加 app-server error data 断言；新增 `TestRuntimeRouterConfigWriteErrorDataMatchesRust` 覆盖 app-server 层 `configVersionConflict` 与 `configLayerReadonly`。
- [x] 验证：`go test ./internal/appserver -run "TestOutgoingMessagesMatchRustJSONRPCShape|TestStdioServerHandlesJSONRPCLine|TestWebSocketServerHealthAndInitialize|TestServerRequestBrokerPropagatesErrorResponse" -count=1 -v` 通过；`go test ./internal/config ./internal/appserver -run "TestServiceWriteValidation|TestRuntimeRouterConfigRejectsLegacyProfileWrite|TestRuntimeRouterConfigWriteErrorDataMatchesRust" -count=1 -v` 通过；`go test ./internal/config ./internal/appserver -count=1` 通过。
- [x] 对照 Rust `exec/src/exec_events.rs` 与 `exec/tests/event_processor_with_json_output.rs` 的失败事件语义，Go `internal/exec` 在 JSON 模式 agent turn 失败时保留现有 `error` 事件，同时补发 Rust 同款 `turn.failed` 事件；`--output-last-message` 失败时不覆盖既有文件内容。
- [x] 新增/调整回归：`TestRunJSONEmitsErrorEventWhenTurnFails` 现在同时锁定 lifecycle events、兼容 `error` event、Rust `turn.failed` event，以及 failed turn 不覆盖 last-message file。
- [x] 验证：`go test ./internal/exec -run "TestRunJSONEmitsErrorEventWhenTurnFails" -count=1 -v` 通过；`go test ./internal/protocol -run "TestTurnTerminalEventJSONShape" -count=1 -v` 通过；`go test ./internal/exec -count=1` 通过；`go test ./internal/protocol ./internal/exec ./internal/cli ./internal/features ./internal/config ./internal/appserver ./internal/app ./internal/execserver -count=1` 通过；`go list -buildvcs=false ./...` 通过；`go test ./... -count=1` 通过。

### 2026-07-07

- [x] 按用户要求创建 `plan_new.md`，参考 `plan.md` 的账本格式重新组织全量开发与测试计划。
- [x] 读取 Rust `Cargo.toml`，确认当前 Rust workspace 有 123 个 member。
- [x] 统计 Rust 当前可见 `.rs` 文件约 2446 个、Rust test-like 文件约 364 个。
- [x] 统计 Go 当前 56 个 package、781 个 `.go` 文件、324 个 Go test 文件。
- [x] 运行 `go list -buildvcs=false ./...` 通过。
- [x] 复核 `plan.md`/`next_plan.md` 最新日志，确认阶段 1/6/8/9/10 已推进到深度对齐，但仍存在 TUI、默认 exec runner、remote app-server、remote exec-server、live/platform matrix 等缺口。
- [x] 明确当前综合对齐估算约 76%，非 TUI 主链路约 86%-88%，Rust fixture/golden 约 60%-65%，live/platform 验收约 40%-50%。
- [x] 启动 TUI 缺口收口：新增 `internal/tui` 状态/渲染层，root interactive 现在有 transcript、状态栏、底部命令提示和 `/help`、`/status`、`/new`、`/clear`、`/model`、`/approval`、`/sandbox`、`/exit` slash commands。
- [x] TUI MVP 已接入 `internal/app/interactive.go`，保持现有 prompt flow 兼容，并把 `/model`、`/approval`、`/sandbox` 变更写回后续 turn 的 shared options。
- [x] 新增 `internal/tui` 单元测试和 `internal/app` interactive slash command 回归。
- [x] 验证：`go test ./internal/tui -count=1` 通过。
- [x] 验证：使用仓库内 `.gocache/.gotmp` 运行 `go test ./internal/app -run "TestInteractive" -count=1 -v` 通过。
- [x] 验证：使用仓库内 `.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/cli ./internal/app -count=1` 通过。
- [x] 验证：`go list -buildvcs=false ./...` 通过。
- [x] 验证：首次全量 `go test ./... -count=1` 遇到 `internal/appserver` Windows 进程清理抖动；单独重跑失败用例和 `go test ./internal/appserver -count=1` 均通过，第二次全量 `go test ./... -count=1` 通过。
- [x] 对齐 Rust TUI 技术基线：复核 `tui/Cargo.toml`、`tui/src/lib.rs`、`tui/src/tui.rs`，确认 Rust 侧依赖 `ratatui`、`crossterm`、`tokio`、`vt100`、`insta`，并包含 chatwidget、bottom_pane、selection_list、resume_picker、theme_picker、markdown、diff、status 等模块。
- [x] 新增 `docs/tui_tech_selection.md`：推荐 Bubble Tea + Bubbles + Lip Gloss + Glamour 作为 Go raw terminal adapter，保留 `internal/tui` 为无依赖状态核心，并明确不采用 tview 主线或手写 raw mode。
- [x] TUI 进度估算从 30% 调整到 33%；本轮完成技术路线锁定，尚未引入 Bubble Tea 依赖和 root model。
- [x] 验证：技术选型文档落地后运行 `go list -buildvcs=false ./...` 通过。
- [x] 引入 TUI 技术栈依赖：`github.com/charmbracelet/bubbletea v1.3.10`、`github.com/charmbracelet/bubbles v1.0.0`、`github.com/charmbracelet/lipgloss v1.1.1-0.20250404203927-76690c660834`、`github.com/charmbracelet/glamour v1.0.0`；`go` directive 随依赖约束从 1.24.0 调整为 1.24.2，保留 `toolchain go1.24.3`。
- [x] 新增 `internal/tui/tea` Bubble Tea root model：viewport transcript、textarea composer、status/footer、slash command handling、submit hook、`StatusMsg`、`TurnCompletedMsg`，为 app 层接入 runner 留出无环依赖边界。
- [x] 新增 `internal/tui/markdown` Glamour `notty` markdown renderer，作为 Rust `markdown_render`、`markdown_stream`、code block/diff fixture 后续对齐入口。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/tui/markdown ./internal/app -count=1` 通过。
- [x] 验证：`go mod tidy` 通过。
- [x] 验证：`go list -buildvcs=false ./...` 通过。
- [x] 验证：首次全量 `go test ./... -count=1` 遇到 `internal/execserver` 的 `TestStdioProcessReadWaitsForOutput` 输出等待抖动；单独重跑失败用例和 `go test ./internal/execserver -count=1` 均通过，第二次全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 33% 调整到 40%；本轮完成依赖引入、root model 骨架和 markdown 入口，尚未把 Bubble Tea program 接入默认 interactive runtime。
- [x] 接入默认 interactive runtime：`internal/app/interactive.go` 在 stdin/stdout 均为真实 `*os.File` 终端时启动 `internal/tui/tea` Bubble Tea program；非真实 TTY、管道和现有测试继续使用行式 fallback。
- [x] 新增 app 层 Bubble Tea command 封装：按 `internal/tui.State` 构造 `codex exec`/`resume` request，执行结果通过 `TurnCompletedMsg` 回流，避免 runner stdout/stderr 直接打穿 TUI 画面。
- [x] 新增回归：真实 TTY gating 不会误用 fake terminal、TUI state 会写入 exec shared options、resume thread 会正确设置、nil runner 会回传 TUI error message。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/app -run "TestInteractive" -count=1 -v` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/tui/markdown ./internal/app -count=1` 通过。
- [x] 验证：`go list -buildvcs=false ./...` 通过。
- [x] 验证：本轮全量 `go test ./... -count=1` 多次遇到非 TUI Windows 时序抖动：`internal/execserver/TestStdioProcessReadWaitsForOutput`、`internal/app/TestResponsesAPIProxyStreamsResponsesRequest`、`internal/appserver/TestProcessServiceSpawnTTYStreamsAndResizes`；对应失败用例和 `internal/app`、`internal/appserver`、`internal/execserver` 包单独重跑均通过或按既有 ConPTY 限制 skip。
- [x] TUI 进度估算从 40% 调整到 48%；本轮完成真实 TTY path 接入和 runner command 回流边界，尚未接入 streaming 增量、modal/picker、remote app-server TUI 和 Rust terminal snapshot。
- [x] 扩展 `internal/tui/tea` streaming 能力：新增 `ThreadEventMsg`、`StreamStartedMsg`、stream channel 消费、assistant delta 合并、turn completed 去重、thread/status 回流和最小 bottom pane。
- [x] 接入 `internal/app/interactive.go` JSON event bridge：interactive runner 以 `Exec.JSON=true` 执行，stdout JSONL 解析为 `protocol.ThreadEvent` 并转为 `codextea.ThreadEventMsg`，最终结果继续通过 `TurnCompletedMsg` 收口。
- [x] 新增 streaming 回归：TUI 可消费 `thread.started`、`turn.started`、`item.delta`、tool started、`turn.completed`；app 层 event writer 可解析分段 JSON line；interactive command 会发送 stream start、thread event、delta 和 final completion。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea ./internal/app -run "TestModel|TestInteractive" -count=1 -v` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/tui/markdown ./internal/app -count=1` 通过。
- [x] 验证：`go list -buildvcs=false ./...` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 48% 调整到 55%；本轮完成 streaming event 回流和最小 bottom pane，尚未补 approval/MCP elicitation modal、model/session picker、remote app-server TUI 和 Rust terminal snapshot。
- [x] 新增 `internal/tui/tea` 通用 modal 层和 approval modal 基础：`ModalRequestMsg`、`ApprovalRequestMsg`、`ModalResponse`、`OnModalResponse`、默认 allow once/allow session/deny 选项、方向键/Tab/数字/快捷键/Enter/Esc 交互，并在 modal 打开时阻断 composer 输入。
- [x] 新增 TUI modal 回归：approval shortcut 会回调选择值，通用 modal 可导航/取消，modal 打开时普通输入不会污染 composer。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea -count=1 -v` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/tui/markdown ./internal/app -count=1` 通过。
- [x] 验证：`go list -buildvcs=false ./...` 通过。
- [x] 验证：本轮全量 `go test ./... -count=1` 遇到非 TUI Windows 抖动：`internal/app/TestAppServerListenOffUsesPersistedRemoteControlPreference` 和 `internal/appserver/TestRuntimeRouterTurnStartNullServiceTierClearsConfigDefault`；失败用例及对应包单独重跑均通过。
- [x] TUI 进度估算从 55% 调整到 60%；本轮完成 modal/picker 基础和 approval modal UI，尚未接入真实 permission request、MCP elicitation schema/form、model/session picker、remote app-server TUI 和 Rust terminal snapshot。
- [x] 接通真实 shell approval/permission request：新增 `ShellApprovalFunc`/`ShellApprovalRequest`/`ShellApprovalDecision`，`internal/exec.Runner` 注入 approval callback，tool output metadata 保留 approval request/retry context，TUI approval modal 选择可回传 broker 并支持本会话 allow 缓存。
- [x] 按 Rust `tui/src` 低层模块逐个补齐 Go 对应文件和测试：`width.go`、`line_truncation.go`、`wrapping.go`、`selection_list.go`、`token_usage.go`、`status_indicator.go`、`text_formatting.go`、`terminal_title.go`、`terminal_hyperlinks.go`、`terminal_palette.go`、`color.go`、`style.go`、`motion.go`、`shimmer.go`、`resize_reflow_cap.go`、`table_detect.go`、`live_wrap.go`、`markdown_text_merge.go`、`clipboard.go`，并修正缩进长 URL wrap 空前缀问题。
- [x] 按 Rust `bottom_pane` 补 `internal/tui/bottom_pane`：`scroll_state.go`、`paste_burst.go`、`pending_input_preview.go`、`status_line_style.go`，覆盖 wrap navigation、page/jump、paste burst hold/flush、pending/rejected/queued input preview、status line segment/accent。
- [x] 按 Rust `history_cell` 补 `internal/tui/history_cell`：`base.go`、`messages.go`、`plans.go`、`exec.go`，覆盖 plain/composite/web hyperlink、prefixed wrap、user/agent/reasoning message、plan update/proposed plan、unified exec/background process summary。
- [x] 按 Rust `streaming` 补 `internal/tui/streaming`：table holdback scanner、adaptive chunking policy、stream/plan controller、commit tick drain，覆盖 markdown table pending/confirmed tail、non-markdown fence ignore、catch-up hysteresis、batch drain。
- [x] 按 Rust `diff_model`/`diff_render`/`exec_cell` 补 Go 基础模块：`internal/tui/diff_model.go`、`internal/tui/diff_render.go`、`internal/tui/exec_cell/model.go`、`internal/tui/exec_cell/render.go`，覆盖 diff 统计/summary/render、rename display、exec lifecycle/exploring/output truncation/unified interaction。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/tui/markdown ./internal/tui/bottom_pane ./internal/tui/history_cell ./internal/tui/streaming ./internal/tui/exec_cell ./internal/app ./internal/exec ./internal/protocol ./internal/tool -count=1` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go list -buildvcs=false ./...` 通过，新增 `internal/tui/bottom_pane`、`internal/tui/history_cell`、`internal/tui/streaming`、`internal/tui/exec_cell` 均进入包图。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 60% 调整到 72%；本轮完成 shell approval 接线和大量 Rust TUI module-level Go 对应包，尚未完成 MCP elicitation、model/session picker、remote app-server TUI、full chatwidget/composer 集成和 Rust terminal snapshot。
- [x] 按 Rust `resume_picker` / `theme_picker` 补 Go 基础模块：`internal/tui/resume_picker.go`、`internal/tui/theme_picker.go`，覆盖 session filter/sort/density/expanded rows/next-page threshold、theme discovery/live preview/cancel-restore/confirm/layout preview。
- [x] 按 Rust `bottom_pane/mcp_server_elicitation` 补 Go 基础模块：`internal/tui/bottom_pane/mcp_server_elicitation.go`，覆盖 schema field 解析、required validation、text/secret/boolean/select/multi-select、approval accept/decline/cancel 与 persist 决策。
- [x] 按 Rust `bottom_pane/chat_composer*` 补 Go 基础模块：`internal/tui/bottom_pane/chat_composer.go`，覆盖 draft cursor/edit、unicode backspace、attachments、history search、slash input、running queue 与 footer mode/status。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/bottom_pane -count=1` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/tui/markdown ./internal/tui/bottom_pane ./internal/tui/history_cell ./internal/tui/streaming ./internal/tui/exec_cell ./internal/app ./internal/exec ./internal/protocol ./internal/tool -count=1` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go list -buildvcs=false ./...` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 72% 调整到 76%；本轮继续按 Rust `tui/src` 单模块补齐 picker、elicitation form 与 composer state，尚未完成 MCP elicitation/app-server/tea 接线、model catalog picker、remote app-server TUI、chatwidget 全量集成和 Rust terminal snapshot。
- [x] 对齐 Rust `chatwidget/tool_requests.rs` 的 MCP elicitation 入口：新增 `internal/tui/tea/elicitation.go`、扩展 `ModalResponse` 携带 elicitation decision/content/persist，Bubble Tea 可渲染 URL、approval action、form content 三类 MCP 请求。
- [x] 接通 interactive MCP elicitation broker：`internal/app/interactive.go` 新增 `interactiveElicitationBroker`，可把 `mcp.MCPElicitationRequest` 转成 `ElicitationRequestMsg`，等待 TUI modal 响应并回传 `mcp.MCPElicitationResponse`。
- [x] 接通 exec runner 注入点：`internal/exec.Runner` 新增 `MCPService`、`MCPTools`、`MCPConnectors`、`MCPElicitation` 字段，在 MCP service/tools 存在时启用 MCP tool router 并注入 elicitation handler。
- [x] 新增回归：TUI elicitation approval/session persist、form default content submit、required field invalid submit 保持 modal 打开、interactive broker 返回 MCP accept/meta persist。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea ./internal/app ./internal/exec -count=1` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/tui/markdown ./internal/tui/bottom_pane ./internal/tui/history_cell ./internal/tui/streaming ./internal/tui/exec_cell ./internal/app ./internal/exec ./internal/protocol ./internal/tool ./internal/mcp -count=1` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go list -buildvcs=false ./...` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 76% 调整到 78%；本轮完成 MCP elicitation tea/app/exec 接线，尚未完成 model catalog picker、session picker app-server 数据源、request_user_input UI、remote app-server TUI、chatwidget 全量集成和 Rust terminal snapshot。
- [x] 对齐 Rust `chatwidget/model_popups.rs` 主入口：新增 `internal/tui/model_picker.go` 和 `internal/tui/tea/model_picker.go`，`/model` 空参现在打开模型 picker，可从 bundled model catalog 生成选项，过滤 hidden 模型，标记 current/default，并在选择后写回 `State.Model`。
- [x] 对齐 Rust `bottom_pane/request_user_input` 主路径：新增 `internal/tui/request_user_input.go` 和 `internal/tui/tea/request_user_input.go`，支持 1-3 个问题、最多 3 个选项、自由文本问题、逐题提交和 modal 响应。
- [x] 接通 `request_user_input` app/exec 路径：`internal/exec.Runner` 新增 `UserInput` responder 注入，`internal/app/interactive.go` 新增 `interactiveUserInputBroker`，可把 tool handler 请求转成 TUI modal 并回传 `tool.UserInputResponse`。
- [x] 新增回归：model picker 过滤/当前项、`/model` 空参 picker 选择、request_user_input 状态流转、tea modal 两题提交、interactive broker 回传 tool response。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/app ./internal/exec ./internal/tool ./internal/turn -count=1` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/tui/markdown ./internal/tui/bottom_pane ./internal/tui/history_cell ./internal/tui/streaming ./internal/tui/exec_cell ./internal/app ./internal/exec ./internal/turn ./internal/protocol ./internal/tool ./internal/mcp ./internal/model -count=1` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go list -buildvcs=false ./...` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 78% 调整到 82%；本轮完成 model picker 和 request_user_input tea/app/exec 主路径，尚未完成 model reasoning 二级 picker、session picker app-server 数据源、request_user_input notes/auto-timeout/unanswered confirmation 高级 overlay、remote app-server TUI、chatwidget 全量集成和 Rust terminal snapshot。
- [x] 对齐 Rust `tui/src/chatwidget/model_popups.rs` 的 model reasoning 二级 picker 主路径：`ModelPreset` 保留 default/supported reasoning fields，TUI state 和 CLI shared options 增加 `ModelReasoningEffort`，`/model` 选择多 reasoning 模型时切换到 “Select Reasoning Level for ...” 二级 modal，确认后写回 state 并传入后续 interactive exec request。
- [x] 新增回归：catalog reasoning 字段保留、model picker reasoning option/default/current、Bubble Tea `/model` 二级 reasoning modal、interactive TUI state 到 exec shared options、exec effective reasoning effort 覆盖优先级。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行受影响包 `go test ./internal/model ./internal/tui ./internal/tui/tea ./internal/app ./internal/exec ./internal/cli -count=1` 通过。
- [x] 验证：TUI/app/exec/turn/protocol/tool/mcp/model 宽回归通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 最终通过。期间 `internal/appserver/TestProcessServiceSpawnTTYStreamsAndResizes` 一次 Windows PTY 时序抖动，单测重跑通过，全量重跑通过。
- [x] TUI 进度估算从 82% 调整到 84%；本轮完成 model reasoning 二级 picker tea/app/exec 主路径，尚未完成 Plan scope reasoning picker、session picker app-server 数据源、request_user_input notes/auto-timeout/unanswered confirmation 高级 overlay、remote app-server TUI、chatwidget 全量集成和 Rust terminal snapshot。
- [x] 对齐 Rust `tui/src/chatwidget/model_popups.rs` 的 `open_plan_reasoning_scope_prompt` 主路径：Go TUI state 增加 Plan mode 与 Plan reasoning override，reasoning 二级选择后在 Plan mode/同模型/有效差异场景打开 “Apply reasoning change” scope picker，支持 Plan-only 和 All-modes 两种写回语义。
- [x] 接通 Plan-only effective reasoning：Plan-only 只更新 `PlanModeReasoningEffort`，不改全局 `ReasoningEffort`；interactive exec request 使用 `State.EffectiveReasoningEffort()`，确保 Plan override 进入下一轮请求。
- [x] 新增回归：Plan scope picker 文案/选项、Plan mode effective reasoning 判断、Bubble Tea `/model -> reasoning -> Plan scope` Plan-only 与 All-modes 按键流、interactive request 使用 Plan override。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/app -count=1` 通过；TUI/app/exec/turn/protocol/tool/mcp/model 宽回归通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 84% 调整到 85%；本轮完成 Plan scope reasoning picker tea/app/exec 主路径，尚未完成 session picker app-server 数据源、request_user_input notes/auto-timeout/unanswered confirmation 高级 overlay、remote app-server TUI、chatwidget 全量集成和 Rust terminal snapshot。
- [x] 对齐 Rust `tui/src/resume_picker.rs` 的数据源主路径：新增 `internal/tui/session_source.go`，可从 `session.Store` 生成 `SessionSummary`，并提供 app-server `ThreadListParams` builder 和 `Thread` DTO 到 TUI summary 的 adapter。
- [x] 接通本地 `/resume` TUI 主路径：`/resume` 打开 session picker modal，按 CWD 过滤可见会话，选择后写回 `State.ThreadID`；后续用户输入 prompt 时复用现有 interactive bridge 走 `codex exec resume`。
- [x] app 层启动 TUI 时预加载默认 `sessions` store 的 session summaries；加载失败不阻断 TUI，保持 picker 空状态。
- [x] 新增回归：session store 数据源过滤/排序/limit、app-server Thread adapter 和 thread/list params、Bubble Tea `/resume` picker 展示/选择/ThreadID 写回。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/app ./internal/appserver ./internal/session -count=1` 通过；TUI/app/appserver/session/exec/turn/protocol/tool/mcp/model/cli 宽回归通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 85% 调整到 86%；本轮完成 session picker thread-store/app-server adapter 和本地 `/resume` 主路径，尚未完成 remote app-server TUI、request_user_input notes/auto-timeout/unanswered confirmation、session fork/archive/delete 完整交互、chatwidget 全量集成和 Rust terminal snapshot。
- [x] 对齐 Rust `tui/src/bottom_pane/request_user_input` 的 auto-timeout 主路径：`RequestUserInputMsg.AutoResolutionMS` 现在会注册 Bubble Tea timeout tick；到期且 modal 仍打开时自动关闭并通过 broker 返回 `TimedOut=true` 和已提交的 partial answers。
- [x] 新增回归：request_user_input modal 展示 auto resolution，忽略无关 timeout id，匹配 timeout id 后关闭 modal 并返回 `UserInputDecision.TimedOut=true`。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/app ./internal/tool ./internal/exec -count=1` 通过；TUI/app/appserver/session/exec/turn/protocol/tool/mcp/model/cli 宽回归通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 最终通过。期间 `internal/appserver/TestCommandExecTTYStreamsAndResizes` 一次 Windows PTY 时序抖动，单测重跑通过，全量重跑通过。
- [x] TUI 进度估算从 86% 调整到 87%；本轮完成 request_user_input auto-timeout，尚未完成 request_user_input notes/unanswered confirmation、remote app-server TUI、session fork/archive/delete 完整交互、chatwidget 全量集成和 Rust terminal snapshot。
- [x] 对齐 Rust `tui/src/bottom_pane/request_user_input` 的 notes 与 unanswered confirmation：选项题支持 Tab 进入 notes、Esc/Tab 清 notes、提交时把 `user_note: ...` 作为第二条 answer；空自由输入题保持 unanswered，最后提交时弹出 `Proceed/Go back` 确认，Esc/Go back 返回第一个未回答问题。
- [x] 对齐 Rust `ToolRequestUserInputAnswer { answers: [...] }` 结构化响应语义：TUI `UserInputDecision`、interactive broker、tool response 和 app-server bridge 均保留 per-question answer list，同时保留旧 `answers` map 作为兼容摘要。
- [x] 新增回归：request_user_input 状态层 option notes/unanswered、Bubble Tea option notes、unanswered confirmation go-back/proceed、auto-timeout 空响应、tool structured response、interactive broker structured response。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/tool ./internal/app ./internal/appserver -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。期间 `internal/appserver/TestRuntimeRouterTurnStartSendsAppServerAttestationHeader` 出现一次 Windows TempDir cleanup 抖动，单测、appserver 包和最终全量复跑均通过。
- [x] TUI 进度估算从 87% 调整到 88%；本轮完成 request_user_input notes/unanswered confirmation 和结构化 answer list，尚未完成 remote app-server TUI、session fork/archive/delete 完整交互、composer/terminal polish、chatwidget 全量集成和 Rust terminal snapshot。
- [x] 对齐 Rust `resume_picker.rs` 的 Fork action 与 session lifecycle slash commands：TUI 新增 `/fork`、`/archive`、`/unarchive`、`/delete`，session picker action 会按 active/archived/delete 场景过滤列表；archive/delete 打开确认框，fork 通过 action hook 返回新 session 并切换 `State.ThreadID`。
- [x] 接入本地 interactive session store mutation hook：`/fork` 调 `session.Store.Fork`，`/archive`/`/unarchive` 调 metadata mutation，`/delete` 按 subtree delete order 删除本地 session；TUI 内存 `sessionItems` 同步更新，真实 TTY 启动时预加载 active+archived sessions。
- [x] 新增回归：session picker action filter/selection kind、Bubble Tea `/fork` action callback、`/delete` confirmation/remove、interactive session action handler archive/unarchive/fork/delete store mutation。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/app -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 88% 调整到 89%；本轮完成本地 session picker fork/archive/unarchive/delete 交互和 store mutation hook，尚未完成 remote app-server TUI、image/file attachment UI、composer/terminal polish、chatwidget 全量集成和 Rust terminal snapshot。
- [x] 对齐 Rust `chat_composer` attachment draft 的可见入口：TUI 新增 `/attach PATH`、`/image PATH`、`/url-image URL`、`/clear-attachments`，支持 file/local image/remote image draft、底部 attachment strip 和提交后清空。
- [x] 当前 Go root `SubmitFunc` 仍是纯文本，attachment 本轮以 `Attachments:` block 形式随 prompt carry-forward；后续需升级为结构化 local image/remote image/file context wire，才能完全等价 Rust `local_images`/`remote_image_urls`。
- [x] 新增回归：Bubble Tea attachment commands render/submit/clear；提交 prompt 包含 file/image/image_url attachment block，提交后 pending attachments 清空。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea ./internal/tui ./internal/app -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 89% 调整到 90%；本轮完成 attachment draft UI 和 prompt carry-forward，尚未完成 structured attachment wire、remote app-server TUI、composer/terminal polish、chatwidget 全量集成和 Rust terminal snapshot。
- [x] 对齐 Rust `chat_composer` 的结构化附件提交链路：`internal/tui/tea` 新增 `SubmitRequest`/`OnSubmitRequest`，保留 file/local image/remote image 附件；`internal/app/interactive.go` 把附件转成 `turn.TurnUserInput` 并在有附件时按 Rust 顺序把 prompt 作为最后一个 text input；`internal/exec` 支持 `Request.Input`，构造单条 mixed-content user message，本地图读取为 data URL，session content 保留 `image`/`localImage`/`input_text` 以便 resume/history 重建。
- [x] 新增回归：Bubble Tea attachment structured submit、interactive bridge structured inputs、exec structured input item/session content。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea ./internal/app ./internal/exec -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 90% 调整到 91%；本轮完成 structured attachment wire，尚未完成 remote app-server TUI、composer/terminal polish、chatwidget 深集成和 Rust terminal snapshot。
- [x] 对齐 Rust `chat_composer` running queue 主路径：Bubble Tea root model 新增 queued submissions，任务 running 时 Enter/Tab 清空 composer 并入队，空闲 Tab 等价提交，成功 `TurnCompletedMsg` 后自动提交下一条；底部 pane 显示 queued count，保留 queued `SubmitRequest` 附件。
- [x] 新增回归：running Enter queue 后 completion 自动提交、idle Tab submit、running Tab queue。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea -count=1`、`go test ./internal/tui ./internal/tui/tea ./internal/app ./internal/exec -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 最终通过。期间 `internal/appserver/TestRuntimeRouterThreadStartEmptyInstructionOverrideSuppressesModelInstructions` 出现一次 Windows TempDir cleanup 抖动，单测和 appserver 包重跑通过，最终全量重跑通过。
- [x] TUI 进度估算从 91% 调整到 92%；本轮完成 composer running queue 主路径，尚未完成 remote app-server TUI、剩余 composer/terminal polish、chatwidget 深集成和 Rust terminal snapshot。
- [x] 对齐 Rust `tui/src/app_server_session.rs` 远端 TUI 主链路：新增 `internal/app/remote_tui.go`，`--remote ws://...`/`wss://...` 不再报未实现；远端 TUI 初始化 app-server，首轮空 `thread/start` 建会话，后续用户消息通过 `turn/start` 提交，结构化附件保留为 `turn.TurnUserInput`，app-server thread/turn/item/delta/error/warning notifications 转成现有 TUI stream events，并支持 `--remote-auth-token-env` Bearer header。
- [x] 新增回归：首次 remote submit 会按 initialize -> thread/start -> turn/start 顺序执行并保留 remote image/file/text input；已有 `State.ThreadID` 时直接 turn/start，不新建 thread；远端 delta 和 turn completed 会回流 TUI 并触发 queued submission drain。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/app -run "TestInteractiveRemoteTurn|TestResolveInteractiveRemoteEndpoint|TestReadRemoteAuthToken" -count=1` 通过；`go test ./internal/app ./internal/tui/tea ./internal/tui -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 92% 调整到 94%；本轮完成 remote app-server TUI ws/wss 主路径，尚未完成 remote unix:// transport、remote server requests/approval/elicitation、remote session action handoff、剩余 composer/terminal polish、chatwidget 深集成和 Rust snapshot/真实终端 smoke fixture。
- [x] 对齐 Rust `app-server-client/src/remote.rs`、`tui/src/app_server_session.rs`、`tui/src/app_server_requests.rs` 远端 TUI 交互：`internal/app/remote_tui.go` 抽象 websocket/JSON-line transport，`--remote unix://` 走 Go app-server UDS JSON-RPC line；远端 server request 不再统一 `-32601`，Rust-supported approval/request_user_input/MCP 请求可进入 TUI modal 或返回结构化结果。
- [x] 新增回归：remote command approval server request 会打开 `ApprovalRequestMsg` 并回传 `acceptForSession`；remote request_user_input server request 会打开 `RequestUserInputMsg` 并回传 `ToolRequestUserInputAnswer{answers:[...]}`；unix JSON-line transport 用 `net.Pipe` 覆盖 initialize/thread/start/turn/start/turn.completed 主链路。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/app -run "TestInteractiveRemoteTurn|TestRemoteAppServerTUIClientUsesUnix" -count=1` 通过；`go test ./internal/app ./internal/tui/tea ./internal/tui -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度估算从 94% 调整到 96%；本轮完成 remote unix:// transport 与 remote app-server 核心 server requests，尚未完成 remote session action handoff、server-request 长尾（ChatGPT auth refresh/dynamic tool/attestation/targeted sink）、剩余 composer/terminal polish、chatwidget 深集成和 Rust snapshot/真实终端 smoke fixture。
- [x] 对齐 Rust `tui/src/session_archive_commands.rs` 与 `tui/src/app_server_session.rs` 的 session CLI remote handoff：`internal/app/session.go` 不再把 `--remote` session 命令挡在未实现错误，改为复用 `remoteAppServerTUIClient` 连接 ws/wss/unix app-server，支持 `thread/list` 精确 name 解析、`thread/read` resume、`thread/archive`、`thread/unarchive`、`thread/delete`、`thread/fork`。
- [x] 新增回归：remote archive UUID 会发 initialize -> thread/archive 且不 mutate 本地 store；remote archive/unarchive/fork/resume 按精确 name 通过 `thread/list` 解析并回传原 CLI summary/成功文案。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/app -run "TestSessionRemote|TestInteractiveRemoteTurn|TestRemoteAppServerTUIClientUsesUnix" -count=1` 通过；`go test ./internal/app ./internal/tui/tea ./internal/tui ./internal/appserver ./internal/session -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 本轮出现 `internal/appserver` 既有 Windows 时序抖动（stdin final output、PTY outputDelta），失败用例与 `go test ./internal/appserver -count=1` 重跑均通过。
- [x] TUI 进度估算从 96% 调整到 97%；本轮完成 CLI session remote app-server handoff，尚未完成 remote Bubble Tea TUI 内部 slash session handoff、server-request 长尾（ChatGPT auth refresh/dynamic tool/attestation/targeted sink）、剩余 composer/terminal polish、chatwidget 深集成和 Rust snapshot/真实终端 smoke fixture。
- [x] 对齐 Rust `tui/src/app_server_session.rs` 与 `tui/src/resume_picker.rs` 的 remote Bubble Tea TUI session action：`internal/app/remote_tui.go` 启动远端 TUI 时预加载 active+archived `thread/list` 作为 session picker items，并把 `/fork`、`/archive`、`/unarchive`、`/delete` action handoff 到远端 app-server `thread/fork/archive/unarchive/delete`。
- [x] 新增回归：remote TUI session picker 会按 CWD 发 active/archived `thread/list` 并生成 active/archived summaries；remote session action handler 会对 fork/archive/unarchive/delete 分别发对应 app-server RPC，并把 fork/unarchive 返回的远端 thread 转回 TUI `SessionSummary`。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/app -run "TestInteractiveRemoteSession|TestSessionRemote|TestInteractiveRemoteTurn|TestRemoteAppServerTUIClientUsesUnix" -count=1` 通过；`go test ./internal/app ./internal/tui/tea ./internal/tui ./internal/appserver ./internal/session -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 本轮仅出现 `internal/appserver/TestCommandExecStreamStdinBuffersFinalOutputWhenNotStreamingStdout` 既有 Windows 时序抖动，失败用例和 `go test ./internal/appserver -count=1` 重跑均通过。
- [x] TUI 进度估算从 97% 调整到 98%；本轮完成 remote Bubble Tea TUI 内部 slash session handoff，尚未完成 remote app-server server-request 长尾（ChatGPT auth refresh/dynamic tool/attestation/targeted sink）、剩余 composer/terminal polish、chatwidget 深集成和 Rust snapshot/真实终端 smoke fixture。
- [x] 对齐 Rust `tui/src/app/app_server_requests.rs` 的 unsupported server-request reject 语义：`internal/app/remote_tui.go` 对 dynamic tool call、attestation generate、currentTime/read、legacy applyPatch approval、legacy exec approval 和未知 server request 增加显式 `-32000` 分支，不再落入 `-32601` 或 stale `not implemented` 文案；ChatGPT auth refresh 保持 Rust 的 supported request 路径。
- [x] 新增回归：`TestRemoteServerRequestLongTailResponses` 覆盖 dynamic tool、ChatGPT auth refresh、attestation、malformed params、unknown request 和 `currentTime/read`，锁定错误码、错误文案和成功结果。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/app -run "TestRemoteServerRequestLongTailResponses|TestInteractiveRemoteTurn|TestRemoteAppServerTUIClientUsesUnix|TestInteractiveRemoteSession|TestSessionRemote" -count=1` 通过；`go test ./internal/app ./internal/tui/tea ./internal/tui ./internal/appserver ./internal/session -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度维持 98%；本轮把 remote server-request 长尾从“泛化未实现/可能挂起”收窄为“已确定响应”，真正剩余为 ChatGPT auth refresh 真刷新、targeted sink/connection-file watch、剩余 composer/terminal polish、chatwidget 深集成和 Rust snapshot/真实终端 smoke fixture。
- [x] 对齐 Rust `tui/src/chatwidget/tests/*` 与 `bottom_pane/approval_overlay.rs` 的 snapshot 覆盖思路：新增 `internal/tui/tea/snapshot_test.go`，用归一化 View inline golden 锁定主视图、approval modal、session picker、request_user_input 四类高价值终端画面。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea -run TestModelTerminalSnapshots -count=1` 通过；`go test ./internal/tui/tea ./internal/tui ./internal/app -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度维持 98%；本轮完成 View 级 terminal snapshot smoke，尚未完成真实 PTY/vt100 snapshot、ChatGPT auth refresh 真刷新、targeted sink/connection-file watch、剩余 composer/terminal polish 和 chatwidget 深集成。
- [x] 对齐 Rust `tui/src/bottom_pane/chat_composer.rs` 与 `tui/src/bottom_pane/footer.rs` 的显式插入换行主路径：Bubble Tea root model 新增 `Ctrl+J` 直接向 textarea 插入 `\n`，不会触发 submit/queue；root footer 与 `bottom_pane.ComposerFooterState` 同步展示 `Ctrl+J newline`。
- [x] 新增回归：`TestModelCtrlJInsertsComposerNewlineWithoutSubmitting` 锁定 Ctrl+J 不提交、Enter 后提交多行 prompt；更新 View snapshot footer golden，保留主视图、approval modal、session picker、request_user_input 画面覆盖。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea -run "TestModelCtrlJInsertsComposerNewlineWithoutSubmitting|TestModelTerminalSnapshots|TestModelViewRendersState" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane ./internal/tui/tea ./internal/tui -count=1` 通过。
- [x] TUI 进度维持 98%；本轮把 composer polish 中的“显式多行换行/快捷键提示”收口，真正剩余收窄为 terminal paste/focus/resize/interrupt/restore、ChatGPT auth refresh 真刷新、targeted sink/connection-file watch、chatwidget 深集成和真实 PTY/vt100 snapshot fixture。
- [x] 对齐 Rust `tui/src/bottom_pane/chat_composer.rs` 的 paste-like burst Enter 抑制主路径：Bubble Tea root model 对多 rune paste 打开 Enter 抑制窗口，窗口内 Enter 插入换行、不提交、不入队；slash command 首行以 `/` 开头时忽略抑制并照常执行命令。
- [x] 新增回归：`TestModelPasteBurstEnterInsertsNewlineWithoutSubmitting` 锁定 paste 后 Enter 变换行且窗口过期后正常提交；`TestModelPasteBurstDoesNotBlockSlashCommandEnter` 锁定 `/status` 等 slash command 不被 paste 抑制误挡。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea -run "TestModelSubmitPrompt|TestModelCtrlJInsertsComposerNewlineWithoutSubmitting|TestModelPasteBurstEnterInsertsNewlineWithoutSubmitting|TestModelPasteBurstDoesNotBlockSlashCommandEnter|TestModelTerminalSnapshots" -count=1 -v` 通过；`go test ./internal/tui/tea ./internal/tui ./internal/tui/bottom_pane ./internal/app -count=1` 通过；`go test ./... -count=1` 首次遇到 `internal/appserver` Windows TempDir cleanup 抖动，失败用例、appserver 包和最终全量重跑均通过。
- [x] TUI 进度维持 98%；本轮把 composer paste/newline polish 的 root 提交行为收口，真正剩余收窄为 terminal focus/resize/interrupt/restore、ChatGPT auth refresh 真刷新、targeted sink/connection-file watch、chatwidget 深集成和真实 PTY/vt100 snapshot fixture。
- [x] 对齐 Rust `tui/src/chatwidget/interaction.rs` 与 `tui/src/app_server_session.rs` 的 running interrupt 主路径：Bubble Tea root model 在 task running 时 `Ctrl+C` 调 `OnInterrupt` 而不是 quit，空闲时仍 quit；本地 TUI 每 turn 注册 cancellable context，interrupt 后回出 `TurnInterruptedMsg`，并修复 context canceled 后中断消息可能被 send helper 丢弃的竞态；remote TUI 记录 active thread/turn 并通过 app-server `turn/interrupt` RPC 中断，`turn/completed(status=interrupted)` 映射为 TUI interrupted。
- [x] 新增回归：`TestModelCtrlCInterruptsRunningTaskWithoutQuitting`、`TestModelCtrlCQuitsWhenIdle`、`TestInteractiveTurnCommandInterruptCancelsRunningContext`、`TestInteractiveRemoteTurnInterruptSendsTurnInterrupt` 锁定 local/remote Ctrl+C interrupt 与 idle quit 语义。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea ./internal/app -run "TestModelCtrlCInterruptsRunningTaskWithoutQuitting|TestModelCtrlCQuitsWhenIdle|TestInteractiveTurnCommandInterruptCancelsRunningContext|TestInteractiveRemoteTurnInterruptSendsTurnInterrupt" -count=1 -v` 通过；`go test ./internal/app -run "TestInteractiveRemoteTurn|TestInteractiveTurnCommand|TestSessionRemote|TestRemoteAppServerTUIClientUsesUnix" -count=1` 通过；`go test ./internal/tui/tea ./internal/tui ./internal/tui/bottom_pane ./internal/app -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度维持 98%；本轮把 terminal interrupt/cancel 主路径收口，真正剩余收窄为 terminal focus/resize/restore、ChatGPT auth refresh 真刷新、targeted sink/connection-file watch、chatwidget 深集成和真实 PTY/vt100 snapshot fixture。
- [x] 对齐 Rust `tui/src/tui.rs` 的 terminal focus bookkeeping 基础：Bubble Tea Program 开启 `WithReportFocus`，root model 记录 `FocusMsg`/`BlurMsg`，并提供 `TerminalFocused()` 供后续 notification/unfocused 行为复用。
- [x] 新增回归：`TestModelTracksTerminalFocusMessages` 锁定 focus/blur 状态切换且不污染 composer draft。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea -run "TestModelTracksTerminalFocusMessages|TestModelCtrlCInterruptsRunningTaskWithoutQuitting|TestModelTerminalSnapshots" -count=1 -v` 通过；`go test ./internal/tui/tea ./internal/tui ./internal/tui/bottom_pane ./internal/app -count=1` 通过；`go list -buildvcs=false ./...` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度维持 98%；本轮把 terminal focus bookkeeping 收口，真正剩余收窄为 terminal resize/restore、ChatGPT auth refresh 真刷新、targeted sink/connection-file watch、chatwidget 深集成和真实 PTY/vt100 snapshot fixture。
- [x] 对齐 Rust `tui/src/streaming/controller.rs` 与 `tui/src/chatwidget.rs::on_terminal_resize` 的 streaming resize reflow：Go `StreamController`/`PlanStreamController` 的 `SetWidth` 现在会按当前宽度重渲染 source、重建 stable queue/live tail，并在半提交 resize 时保留 pending queue；基础 wrapping 修正为短词先换行、只有超过整行宽度的长词才 hard break，更贴近 Rust/textwrap 行为。
- [x] 新增回归：`TestStreamControllerSetWidthRebuildsQueuedLines`、`TestStreamControllerSetWidthPartialDrainKeepsPendingQueue`、`TestStreamControllerSetWidthAfterFirstLineEmitDoesNotRequeueFirstLine`、`TestStreamControllerSetWidthPartialWrappedEmitPreservesRemainder`、`TestStreamControllerSetWidthPreservesInFlightTail` 和 `TestAdaptiveWrapMovesWordBeforeBreakingIt` 锁定 resize reflow、半提交不丢队列、不重复首行、in-flight tail 保留与断词语义。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/streaming -run "TestAdaptiveWrap|TestStreamControllerSetWidth|TestStreamControllerTableHoldback" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/tui/bottom_pane ./internal/tui/tea -count=1` 通过。
- [x] TUI 进度从 98% 调整到 98.5%；本轮把 streaming resize reflow 收口，真正剩余收窄为 ChatGPT auth refresh 真刷新、targeted sink/connection-file watch、terminal restore polish、chatwidget 深集成和真实 PTY/vt100 snapshot fixture。
- [x] 对齐 Rust `tui/src/external_editor.rs`、`tui/src/app/input.rs` 与 `bottom_pane/footer.rs` 的外部编辑器主路径：Bubble Tea root model 新增 `Ctrl+G` 与 `/editor`，`VISUAL` 优先、`EDITOR` fallback，临时 `codex-editor-*.md` seed/读取，使用 `bubbletea.Exec` 释放/恢复终端并继承 stdio；编辑期间显示 “Save and close external editor to continue.”，完成后 trim trailing whitespace 并写回 composer，缺编辑器/命令失败显示 Rust 同类错误文案。
- [x] 新增回归：`TestModelCtrlGExternalEditorAppliesEditedDraft`、`TestModelSlashEditorOpensExternalEditor`、`TestModelExternalEditorReportsError`、`TestResolveExternalEditorCommand*`、`TestSplitEditorCommandLine*`、`TestExternalEditorCommandRunReturnsUpdatedContent`、`TestParseCommand` 和 terminal snapshot/footer 更新，锁定快捷键、slash command、错误提示、env 解析、临时文件编辑返回和 footer。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea ./internal/tui ./internal/tui/bottom_pane -run "TestModelCtrlGExternalEditorAppliesEditedDraft|TestModelSlashEditorOpensExternalEditor|TestModelExternalEditorReportsError|TestResolveExternalEditorCommand|TestSplitEditorCommandLine|TestExternalEditorCommandRunReturnsUpdatedContent|TestParseCommand|TestChatComposerDraftHistoryQueueAndFooter|TestModelTerminalSnapshots" -count=1 -v` 通过；`go test ./internal/tui/tea ./internal/tui ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell -count=1` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度从 98.5% 调整到 98.8%；本轮把 external editor/terminal handoff 主路径收口，真正剩余收窄为 ChatGPT auth refresh 真刷新、targeted sink/connection-file watch、chatwidget 深集成和真实 PTY/vt100 snapshot/terminal restore fixture。
- [x] 对齐 Rust `tui/src/keymap_setup/actions.rs` 与 `tui/src/keymap.rs` 的只读 action inventory：新增 `internal/tui/keymap.go`，完整覆盖 Rust global/chat/composer/editor/vim_normal/vim_operator/vim_text_object/pager/list/approval action catalog、display label、默认 binding summary 与 Fast mode gate；新增 `/keymap`/`/keys` slash command，Bubble Tea 和 line fallback 均可展示 catalog。
- [x] 对齐 Rust `chat.interrupt_turn` 默认 `Esc` 行为：Bubble Tea root model 在 task running 且没有 modal 时按 Esc 调 `OnInterrupt`，modal Esc 仍保持原取消语义，Ctrl+C local/remote interrupt 保持兼容。
- [x] 新增回归：`TestKeymapActionCatalogIncludesRustActions`、`TestRenderKeymapCatalog`、`TestKeymapActionLabel`、`TestModelKeymapCommandRendersCatalog`、`TestModelEscInterruptsRunningTaskWithoutQuitting`、`TestInteractiveSlashCommandsUpdateTUIState`，锁定 Rust action catalog、Fast mode gate、slash command、line fallback 和 Esc interrupt。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/app -run "TestKeymap|TestRenderKeymapCatalog|TestParseCommand|TestModelKeymapCommandRendersCatalog|TestModelEscInterruptsRunningTaskWithoutQuitting|TestInteractiveSlashCommandsUpdateTUIState" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/app -count=1` 通过；全量 `go test ./... -count=1` 通过。
- [x] TUI 进度从 98.8% 调整到 98.9%；本轮把 keymap action catalog 和 Esc interrupt 收口，真正剩余收窄为 keymap remap/persistence/config editor、ChatGPT auth refresh 真刷新、targeted sink/connection-file watch、chatwidget 深集成和真实 PTY/vt100 snapshot/terminal restore fixture。
- [x] 对齐 Rust `config/src/tui_keymap.rs`、`tui/src/keymap.rs` 与 `tui/src/keymap_setup/picker.rs` 的 keymap remap/persistence 主路径：新增 `internal/tui/keymap_config.go`，实现 Rust canonical key spec normalize、`tui.keymap` context/action schema 校验、string/array/empty-array 三态绑定、composer global fallback、duplicate 检查、`/keymap show/set/unbind/unset/help` helper。
- [x] 接通运行时 remap：Bubble Tea root model 现在按解析后的 keymap 判断 `global.open_external_editor`、`composer.submit`、`composer.queue`、`editor.insert_newline`、`chat.interrupt_turn`；真实 TUI 启动时加载 effective config，`/keymap set/unbind/unset` 通过 `ConfigService` 写入 user/profile `config.toml`，line fallback 同步支持；`ConfigService` 增加 nil value 删除 key path，strict config 允许 top-level `tui`。
- [x] 新增回归：`TestNormalizeKeybindingSpecRustAliases`、`TestKeymapConfigFromConfigValues`、`TestKeymapConfigRejectsUnknownAndMisplacedActions`、`TestHandleKeymapCommandSetUnbindUnset`、`TestModelKeymapCommandAppliesRuntimeRemap`、`TestModelKeymapSubmitAndInterruptRemap`、`TestInteractiveKeymapCommandPersistsConfig`、`TestServiceWriteValueNilDeletesKeyPath`、`TestLoadEffectiveStrictConfigAllowsTUI`。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea ./internal/app ./internal/config -run "TestKeymap|TestNormalizeKeybindingSpec|TestHandleKeymapCommand|TestModelKeymap|TestInteractiveKeymapCommandPersistsConfig|TestServiceWriteValueNilDeletesKeyPath|TestLoadEffectiveStrictConfigAllowsTUI|TestInteractiveSlashCommandsUpdateTUIState" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/app ./internal/config -count=1` 通过；`go test ./internal/appserver -count=1` 通过；全量 `go test ./... -count=1` 两次仅因既有 `internal/appserver` Windows 时序抖动失败，失败用例或 appserver 包重跑通过。
- [x] TUI 进度从 98.9% 调整到 99.0%；本轮把 keymap remap/persistence/config edit 主路径收口，真正剩余收窄为 ChatGPT auth refresh 真刷新、targeted sink/connection-file watch、chatwidget 深集成和真实 PTY/vt100 snapshot/terminal restore fixture。
- [x] 对齐 Rust `tui/src/app/app_server_requests.rs` 与 Go app-server external auth refresh：`internal/app/remote_tui.go` 的 `account/chatgptAuthTokens/refresh` 不再返回 TUI unsupported，改为读取本地 ChatGPT auth snapshot、调用 `auth.RefreshChatGPTTokens`、按 app-server 协议返回 `accessToken/chatgptAccountId/chatgptPlanType`，并尊重 config 中的 auth store mode。
- [x] 新增回归：`TestRemoteServerRequestChatGPTAuthRefreshUsesLocalAuth` 覆盖 refresh_token grant 请求、client_id、response wire shape 和刷新后 auth store 持久化；`TestRemoteServerRequestLongTailResponses` 更新为 dynamic tool/attestation/unknown/currentTime 长尾覆盖。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui -run "TestHandleKeymapCommandSupportsGlobalComposerFallback|TestHandleKeymapCommandSetUnbindUnset" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/app ./internal/config -run "TestKeymap|TestNormalizeKeybindingSpec|TestHandleKeymapCommand|TestModelKeymap|TestInteractiveKeymapCommandPersistsConfig|TestServiceWriteValueNilDeletesKeyPath|TestLoadEffectiveStrictConfigAllowsTUI|TestInteractiveSlashCommandsUpdateTUIState" -count=1 -v` 通过；`go test ./internal/app -run "TestInteractiveRemoteTurn|TestRemoteAppServerTUIClientUsesUnix|TestInteractiveRemoteSession|TestSessionRemote|TestRemoteServerRequest" -count=1 -v` 通过。
- [x] TUI 进度从 99.0% 调整到 99.1%；本轮把 remote ChatGPT auth refresh 真刷新收口，真正剩余收窄为 targeted sink/connection-file watch、chatwidget 深集成和真实 PTY/vt100 snapshot/terminal restore fixture。
- [x] 对齐 Rust `app-server/src/fs_watch.rs` 与 `tui/src/app/app_server_event_targets.rs` 的 connection targeted 主路径：新增 `TargetedNotificationSink`/`connectionNotificationSink`，websocket、stdio/json-line 和 remote-control transport 均支持按 connection 发送 notification；server request sink 也补成 connection-aware，避免 `RequestToConnection` 退化为错投广播。
- [x] 补齐 Go `fs/watch` 的 app-server 操作触发通知：`FSService.ChangedForPath` 按 Rust 非递归 watch 语义匹配文件和目录直接子项；`fs/writeFile`、`fs/createDirectory`、`fs/remove`、`fs/copy` 成功后按 connection 发送 `fs/changed`。
- [x] 新增回归：`TestConnectionServerRequestSinkHonorsTargetConnection`、`TestServiceChangedForPathMatchesFileAndDirectDirectoryWatch`、`TestRuntimeRouterFSWriteFileEmitsTargetedChangedNotifications`，并覆盖 currentTime targeted request、stdio/websocket sink 主路径。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/appserver -run "TestServiceChangedForPathMatchesFileAndDirectDirectoryWatch|TestServiceWatchIsConnectionScoped|TestRuntimeRouterFSWatchUsesConnectionScope|TestRuntimeRouterFSWriteFileEmitsTargetedChangedNotifications|TestServerRequestBrokerTargetsConnectionWhenSinkSupportsIt|TestConnectionServerRequestSinkHonorsTargetConnection|TestRuntimeRouterRequestCurrentTimeBridge|TestStdioServerRoutesServerRequestResponse|TestWebSocketServerHealthAndInitialize" -count=1 -v` 通过；`go test ./internal/appserver -count=1` 通过。
- [x] TUI 进度从 99.1% 调整到 99.2%；本轮把 targeted sink 和 fs/watch app-server 操作通知主路径收口，真正剩余收窄为真实 OS fs/watch 外部事件、chatwidget 深集成和真实 PTY/vt100 snapshot/terminal restore fixture。
- [x] 对齐 Rust `app-server/src/fs_watch.rs` / `file-watcher` 与 TUI remote fs/watch 外部事件语义：`FSService` 新增 connection-scoped watcher callback 和可停止轮询 snapshot，目录 watch 按非递归直接子项、文件 watch 按自身创建/修改/删除触发；`RuntimeRouter` 在已有 targeted notification sink 上发送外部变化 `fs/changed`。
- [x] 新增回归：`TestRuntimeRouterFSWatchReportsExternalFileChanges` 锁定非 app-server 写入也能触发 connection 定向 `fs/changed`；同步保持 `TestRuntimeRouterFSWriteFileEmitsTargetedChangedNotifications`、`TestServiceChangedForPathMatchesFileAndDirectDirectoryWatch`、`TestServiceWatchIsConnectionScoped` 通过。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/appserver -run "TestRuntimeRouterFSWatchReportsExternalFileChanges|TestRuntimeRouterFSWriteFileEmitsTargetedChangedNotifications|TestServiceChangedForPathMatchesFileAndDirectDirectoryWatch|TestServiceWatchIsConnectionScoped" -count=1 -v` 通过；`go test ./internal/appserver -count=1` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/app ./internal/appserver ./internal/session ./internal/config -count=1` 通过。
- [x] TUI 进度从 99.2% 调整到 99.3%；本轮把真实 OS fs/watch 外部事件主路径收口，真正剩余收窄为 chatwidget 深集成和真实 PTY/vt100 snapshot/terminal restore fixture。
- [x] 对齐 Rust `tui/src/custom_terminal.rs` / `VT100Backend` snapshot 思路：新增 Go `internal/tui/tea` 测试内虚拟终端 parser，覆盖 ANSI SGR ignore、clear screen、cursor movement、OSC skip、CR/LF、pending wrap 和固定宽高 screen snapshot；同时让 status/footer 固定单行按终端宽度裁剪，避免真实终端自动换行挤掉顶部内容。
- [x] 新增回归：`TestVirtualTerminalAppliesVT100CursorAndClear`、`TestModelVT100TerminalSnapshotMainView`、`TestModelVT100TerminalSnapshotApprovalModal`，并同步更新 `TestModelTerminalSnapshots` golden 以锁定宽度裁剪后的主视图、approval、session picker、request_user_input。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/tea -run "TestModelTerminalSnapshots|TestVirtualTerminalAppliesVT100CursorAndClear|TestModelVT100TerminalSnapshot" -count=1 -v` 通过；`go test ./internal/tui/tea -count=1` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/app ./internal/appserver ./internal/session ./internal/config -count=1` 通过；`go test ./... -count=1` 通过。
- [x] TUI 进度从 99.3% 调整到 99.4%；本轮把 VT100-style snapshot/golden 与固定行宽度约束收口，真正剩余收窄为 chatwidget 深集成和真实 PTY terminal restore smoke fixture。
- [x] 对齐 Rust `tui/src/chatwidget/hook_lifecycle.rs` 与 app-server `hook/started`/`hook/completed` 通知：新增 Go `history_cell` hook lifecycle cell，覆盖 Running/Completed 状态和 warning/context/error 等 hook output；Tea root model 新增 `HookRunMsg` 并保留底部输出缩进；remote TUI client 将 app-server hook 通知映射到 Tea。
- [x] 新增回归：`TestHookHistoryCells`、`TestModelAppliesHookRunMessages`、`TestRemoteAppServerTUIClientMapsHookNotifications`，锁定 Rust 同类文案 `Running PreToolUse hook`、`PostToolUse hook (failed)`、`warning:`、`hook context:`、`error:`。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/history_cell ./internal/tui/tea ./internal/app -run "TestHook|TestModelAppliesHookRunMessages|TestRemoteAppServerTUIClientMapsHookNotifications" -count=1 -v` 通过；`go test ./internal/tui/history_cell ./internal/tui/tea ./internal/app -count=1` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/app ./internal/appserver ./internal/session ./internal/config -count=1` 通过。
- [x] TUI 进度从 99.4% 调整到 99.5%；本轮把 chatwidget hook lifecycle 主路径收口，真正剩余收窄为真实 PTY terminal restore smoke fixture 和 chatwidget transcript/terminal 长尾。
- [x] 对齐 Rust `tui/src/history_cell/{approvals,request_user_input,notices,separators}.rs`：新增 Go approval decision transcript cell、completed request_user_input result cell、warning/info/error/update/safety/deprecation notice cells 和 final message separator/runtime metrics label。
- [x] 新增回归：`TestApprovalHistoryCells`、`TestRequestUserInputHistoryCell`、`TestNoticeHistoryCells`、`TestFinalMessageSeparatorHistoryCell`，锁定 approved/denied、secret answer masking、unanswered/interrupted summary、safety trusted access links、separator elapsed/runtime metrics 等 Rust 同类输出。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/history_cell -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/history_cell ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/exec_cell -count=1` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/app ./internal/appserver ./internal/session ./internal/config -count=1` 通过。
- [x] TUI 进度从 99.5% 调整到 99.6%；本轮把 history_cell transcript 长尾继续收口，真正剩余收窄为真实 PTY terminal restore smoke fixture、history_cell mcp/patches/search/session 与 chatwidget transcript/terminal 长尾。
- [x] 对齐 Rust `tui/src/history_cell/{mcp,patches,search,session}.rs`：新增 Go MCP tool call/inventory/loading/image-output cell、patch summary/apply failure/view image/image generation cell、web-search cell、session header/session info card，并复用 Go diff summary/wrapping。
- [x] 新增回归：`TestMCPHistoryCells`、`TestPatchSearchAndSessionHistoryCells`，锁定 MCP Calling/Called/error/inventory、patch summary、image generation、web search find-in-page、session header YOLO/fast/help rows。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui/history_cell -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/app ./internal/appserver ./internal/session ./internal/config -count=1` 通过。
- [x] TUI 进度从 99.6% 调整到 99.7%；本轮把剩余 standalone history_cell 模块基本收口，真正剩余收窄为真实 PTY terminal restore smoke fixture 和 chatwidget transcript/terminal 长尾。
- [x] 对齐 Rust `tui/src/app/history_ui.rs` 的 committed history cell 管线：`codextui.State` 新增 `RoleHistory`/`AddHistoryLines`，Bubble Tea root model 新增 `HistoryCellMsg`，history cell 进入 transcript 时不再渲染伪 `System:`/`History:` 头；completed hook lifecycle 从底部活动状态进入主 transcript。
- [x] 新增回归：`TestStateRenderWelcomeAndFrame` 覆盖 history 行渲染；`TestModelAppliesHistoryCellMessages` 覆盖 Tea `HistoryCellMsg`；`TestModelAppliesHookRunMessages` 覆盖 completed hook 进入 `RoleHistory`。
- [x] 验证：使用仓库内 `.gopath/.gocache/.gotmp` 运行 `go test ./internal/tui ./internal/tui/tea -run "TestStateRenderWelcomeAndFrame|TestModelAppliesHookRunMessages|TestModelAppliesHistoryCellMessages|TestModelTerminalSnapshots|TestModelVT100TerminalSnapshot" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/app ./internal/appserver ./internal/session ./internal/config -count=1` 通过。
- [x] TUI 进度从 99.7% 调整到 99.8%；本轮把 history_cell transcript 管线收口，真正剩余收窄为真实 PTY terminal restore smoke fixture 和 chatwidget terminal 长尾。
- [x] TUI 进度从 99.8% 调整到 99.9%；本轮新增真实 PTY terminal restore gated smoke 入口（Unix 真 PTY alt-screen enter/leave 校验，Windows ConPTY 默认 skip 入口），并补齐 chatwidget transcript 滚动保持、PageUp/PageDown、Home/End 与鼠标滚轮事件入口。
- [x] 验证：`go test ./internal/tui/tea -run "TestModelTranscriptNavigationPreservesScrollPosition|TestRealPTYTerminalRestoreSmoke" -count=1 -v` 通过，Windows ConPTY smoke 按环境变量 gated skip。
- [x] 重新按 Rust 独立 `chatwidget` 包审计：Rust `tui/src/chatwidget` 是 TUI 中枢而非一个输入框；Go 新增 `internal/tui/chatwidget` 包，先承接 transcript overlay/pager 与 last-agent-response copy 这两条真实主路径，后续不再把 chatwidget 长尾笼统写成“terminal 长尾”。
- [x] 对齐 Rust `chatwidget/transcript.rs` + `pager_overlay.rs` 行为：Ctrl+T 打开全屏 transcript overlay，overlay 隐藏 composer，默认定位底部；pager 支持 up/down/k/j、PgUp/PgDn、Ctrl+B/F、Ctrl+U/D、Home/End、q/Ctrl+C/Ctrl+T 关闭；滚动离开底部时新内容保持当前位置，回到底部后继续跟随 live tail；鼠标滚轮进入 overlay viewport。
- [x] 对齐 Rust `chatwidget/interaction.rs` 的复制最后 agent response 主路径：`global.copy`/Ctrl+O 现在扫描可见 transcript 中最后一条 assistant response，调用 clipboard writer，并对无响应/失败给出 TUI notice；测试可注入 clipboard writer，避免真实剪贴板依赖。
- [x] Windows PTY smoke 调整为 Rust 等价策略：Rust 当前 Windows/ConPTY interactive fixture 仍是 TODO，Go 不再保留不稳定自造 ConPTY 子进程 harness，默认和 `CODEX_GO_TUI_PTY_SMOKE=1` 均明确 skip，避免假失败。
- [x] 验证：`go test ./internal/tui/chatwidget ./internal/tui/tea -run "TestTranscriptOverlay|TestLastAssistantMarkdown|TestModelTranscriptOverlay|TestModelCopies|TestModelCopy|TestRealPTYTerminalRestoreSmoke" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell -count=1` 通过；`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/app ./internal/appserver ./internal/session ./internal/config -count=1` 通过；`go test ./... -count=1` 通过。
- [x] 对齐 Rust `chatwidget/rate_limits.rs` 的 warning state 主路径：新增 `internal/tui/chatwidget.RateLimitWarningState`，实现 codex-only、workspace credits skip、75/90/95 阈值推进、100% 封顶不提示、5h/daily/weekly/monthly/annual 近似窗口标签和 warning 文案；补 `rate_limits_test.go` 锁定跨阈值、非 codex、credits、cap 与窗口识别。
- [x] 接通 Responses rate limit stream：`internal/model` 的 `ResponsesStreamEventRateLimits` 现在由 `internal/exec` 转为 protocol `response.rate_limits`，`internal/app` interactive JSON stream writer 能解析并转发，`internal/tui/tea` 将 snapshot 转为 chatwidget warning history cell，且阈值不会重复刷屏。
- [x] 新增回归：`TestRateLimitSnapshotEventJSONShape`、`TestExecStreamEventCollectorBuildsRateLimitProtocolEvent`、`TestModelAppliesRateLimitWarnings`、`TestModelAppliesRateLimitProtocolEvent`、`TestInteractiveStreamEventWriterParsesJSONLines`，锁定 model -> protocol -> exec -> app writer -> Tea 的完整路径。
- [x] 验证：`go test ./internal/tui/chatwidget ./internal/protocol ./internal/exec ./internal/tui/tea ./internal/app -run "TestRateLimit|TestExecStreamEventCollectorBuildsRateLimit|TestModelAppliesRateLimit|TestInteractiveStreamEventWriter" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过；`go test ./... -count=1` 通过。
- [x] 对齐 Rust `chatwidget/usage.rs` 的 usage menu/reset view model：新增 Go `SelectionView`、`UsageMenuState`、`RateLimitReset*View`、consume outcome view 和 reset hint 文案，覆盖 available/zero/unknown/signed-out、monthly/free/go reset 描述、loading/consuming/success/error/nothing/no-credit 状态。
- [x] 对齐 Rust `chatwidget/tokens.rs` 与 `tokens/chart.rs` 的纯逻辑：新增 `TokenActivityView` alias 解析、loading/error/loaded lines、summary 字段 packing、`FormatTokensCompact`、streak/duration 格式化、12 个月 daily heatmap、weekly bar chart、cumulative running total chart、month labels、legend/caption/footer、duplicate bucket sum、negative/future/invalid bucket 过滤；Tea `/usage daily|weekly|cumulative` 可插入 token activity history card。
- [x] 对齐 Rust `chatwidget/status_state.rs` 纯状态：新增 `StatusIndicatorState`、`TerminalTitleStatusKind`、`PendingGuardianReviewStatus`、`StatusState`，覆盖 guardian review 聚合、多条截断、finish/update 和 retry header take-once 语义。
- [x] Tea 接入 `/usage`：`internal/tui.State` command/help 新增 `/usage`；Bubble Tea modal 支持 disabled options；`/usage` 空参数打开 Usage 菜单，reset unavailable/available 状态按 view model 渲染，`Show usage` 进入 token activity card。
- [x] 新增回归：`TestUsageMenuViewResetAvailability`、`TestRateLimitResetViews`、`TestRateLimitResetConsumeResultView`、`TestTokenActivityViewAliasesParse`、`TestFormatTokensCompactMatchesRust`、`TestTokenActivitySummaryLines`、`TestPendingGuardianReviewStatusAggregatesParallelReviews`、`TestModelUsageCommandOpensMenuAndShowsTokenActivity`、`TestModelUsageMenuResetStates`。
- [x] 验证：`go test ./internal/tui/chatwidget ./internal/tui/tea -run "TestPendingGuardian|TestStatusState|TestStatusIndicatorState|TestUsage|TestTokenActivity|TestModelUsage" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过；`go test ./... -count=1` 本轮仅 `internal/appserver/TestProcessServiceSpawnTTYStreamsAndResizes` 命中既有 Windows ConPTY 时序抖动，失败用例单独重跑 skip/pass，`go test ./internal/appserver -count=1` 通过。
- [x] 对齐 Rust `chatwidget/warnings.rs`：新增 fallback model metadata warning slug 解析与 `WarningDisplayState` 去重；Tea `StatusMsg` 的 `warning:` 前缀现在进入 warning history cell，并对同一 fallback model slug 只展示一次。
- [x] 新增回归：`TestWarningDisplayStateDeduplicatesFallbackModelMetadataWarnings`、`TestFallbackModelMetadataWarningSlug`、`TestModelStatusWarningUsesWarningDisplayState`。
- [x] 验证：`go test ./internal/tui/chatwidget ./internal/tui/tea -run "TestWarning|TestFallbackModel|TestModelStatusWarning|TestPendingGuardian|TestUsage|TestTokenActivity|TestModelUsage" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过；`go test ./... -count=1` 本轮仅 `internal/execserver/TestStdioProcessReadWaitsForOutput` 命中既有 Windows 时序抖动，失败用例和 `go test ./internal/execserver -count=1` 重跑通过。
- [x] 对齐 Rust `chatwidget/notifications.rs` 纯显示与 coalescing：新增 `Notification`/`NotificationState`/`NotificationsSetting`，实现 agent response preview 空白归一化、approval/edit/elicitation/plan prompt 文案、custom allowlist、高优先级 approval 不被低优先级 agent completion 覆盖。
- [x] 新增回归：`TestNotificationDisplay`、`TestNotificationAllowedFor`、`TestNotificationStateCoalescesByPriority`、`TestAgentTurnPreviewAndUserInputSummary`。
- [x] 验证：`go test ./internal/tui/chatwidget -run "TestNotification|TestAgentTurnPreview|TestWarning" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] 对齐 Rust `chatwidget/user_messages.rs` 与 `chatwidget/input_queue.rs` 纯状态/队列基础：新增 Go `UserMessage`、history override、queued message、pending steer、thread composer/input snapshot、merge/remap/restore/display helper、pending steer compare key 与 `InputQueueState` preview/clear/follow-up 判断。
- [x] 新增回归：`TestInputQueuePreviewKeepsQueueCategoriesSeparate`、`TestInputQueuePreviewUsesHistoryOverrides`、`TestInputQueueClearResetsAllInputQueues`、`TestUserMessagePreviewText`、`TestThreadComposerStateHasContent`、`TestMergeUserMessagesWithTextElements`、`TestMergeUserMessagesWithHistoryRecord`、`TestPendingSteerCompareKeyFromItems`、`TestUserMessageDisplayFromInputs`。
- [x] 对齐 Rust `chatwidget/status_surfaces.rs` 与 `chatwidget/status_controls.rs` 纯逻辑：新增 status line/terminal title item id alias 解析、invalid 去重、默认配置、preview placeholder/live/suppress、rate-limit preview copy、title separator/action-required 拼接、spinner frame 与 truncation。
- [x] 新增回归：`TestStatusSurfaceSelectionsDefaultsAliasesAndInvalids`、`TestStatusSurfaceSelectionsUsageFlags`、`TestStatusSurfacePreviewDataPlaceholdersLiveAndSuppression`、`TestStatusSurfacePreviewRateLimitCopy`、`TestStatusLineForItemsUsesPreviewValues`、`TestTerminalTitleTextSeparatorsAndActionRequired`、`TestTerminalTitleFrameAndTruncate`。
- [x] 验证：`go test ./internal/tui/chatwidget -run "TestInputQueue|TestUserMessage|TestThreadComposer|TestMerge|TestPendingSteer" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestStatusSurface|TestTerminalTitle" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.1% 调整到 99.2%；本轮把 chatwidget user/input queue 与 status surfaces 纯逻辑收口，真正剩余收窄为 status_controls runtime/setup、完整 token activity chart/account RPC、rate-limit reset/model-switch surfaces、desktop notification OS hook、permissions/settings/plugins/skills/goal/review/side popups 和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/goal_menu.rs` 与 `chatwidget/goal_status.rs` 纯逻辑：新增 app-server `Goal` summary lines、status label/command hint、edited-goal status mapping、resume paused goal selection view、goal status indicator 和 active turn elapsed-time 聚合。
- [x] 新增回归：`TestActiveGoalUsagePrefersTokenBudget`、`TestActiveGoalUsageReportsTimeWithoutBudget`、`TestStoppedGoalBudgetUsageReportsBudgetedTokens`、`TestCompletedGoalUsage`、`TestActiveGoalStatusIncludesCurrentTurnElapsedTime`、`TestActiveGoalStatusDoesNotCountIdleTimeBeforeTurnStart`、`TestGoalSummaryLinesAndHints`、`TestEditedGoalStatus`、`TestResumePausedGoalView`。
- [x] 验证：`go test ./internal/tui/chatwidget -run "Test.*Goal" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.2% 调整到 99.25%；本轮把 goal menu/status 纯逻辑收口，真正剩余收窄为 goal runtime RPC prompt 接线、status_controls runtime/setup、完整 token activity chart/account RPC、rate-limit reset/model-switch surfaces、desktop notification OS hook、permissions/settings/plugins/skills/review/side popups 和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/permissions_menu.rs` 与 `chatwidget/permission_popups.rs` 纯 view model：新增 builtin approval presets、permissions popup、explicit permission profiles popup、full-access confirmation、Guardian Approve for me、Windows non-admin sandbox hint、current item 判断、custom profile disabled reason。
- [x] 新增回归：`TestBuiltinApprovalPresetsMatchRustOrderAndCopy`、`TestPermissionsPopupViewSkipsReadOnlyOffWindowsAndAddsGuardian`、`TestPermissionsPopupViewIncludesReadOnlyAndWindowsHint`、`TestPermissionProfilesPopupViewIncludesBuiltinsAndCustomProfiles`、`TestFullAccessConfirmationView`、`TestPermissionPresetMatchesCurrentAliases`。
- [x] 验证：`go test ./internal/tui/chatwidget -run "TestPermission|TestBuiltinApproval|TestFullAccess" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.25% 调整到 99.3%；本轮把 permissions menu/popups 纯 view model 收口，真正剩余收窄为 permissions runtime config 写入/requirements/Windows prompt 接线、goal runtime RPC、status_controls runtime/setup、完整 token activity chart/account RPC、rate-limit reset/model-switch surfaces、desktop notification OS hook、settings/plugins/skills/review/side popups 和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/settings.rs` 与 `chatwidget/settings_popups.rs` popup 纯逻辑：新增 personality popup result/view、Friendly/Pragmatic/None 文案、startup/model unsupported 提示、experimental features view model。
- [x] 新增回归：`TestPersonalityPopupDisabledUntilSessionConfigured`、`TestPersonalityPopupRejectsUnsupportedModel`、`TestPersonalityPopupView`、`TestPersonalityLabelsAndDescriptions`、`TestExperimentalFeaturesViewUsesRegistryExperimentalStage`。
- [x] 验证：`go test ./internal/tui/chatwidget -run "TestPersonality|TestExperimental" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.3% 调整到 99.35%；本轮把 settings/personality/experimental popup 纯 view model 收口，真正剩余收窄为 settings runtime persistence/model capability 接线、permissions runtime config/requirements/Windows prompt、goal runtime RPC、status_controls runtime/setup、完整 token activity chart/account RPC、rate-limit reset/model-switch surfaces、desktop notification OS hook、plugins/skills/review/side popups 和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/plugins.rs`、`chatwidget/plugin_catalog.rs` 与 `chatwidget/skills.rs` 纯 view model：新增 Go 插件 marketplace product 分类、tab id/merge remote helpers、display/description/status/detail/source/auth/version/share/capability summaries、plugin install auth follow-up popup、loading/error views，以及 skills 菜单、manage skills toggle view、变更摘要、skill display/description helper。
- [x] 对齐 Rust skills mention 解析：新增 `[$name](path)` linked mention、`skill://` normalize、app path 提取、常见环境变量忽略、linked skill path 优先于同名 plain mention、app mention 仅匹配 accessible/enabled 且唯一 slug，并避免与 skill name 冲突。
- [x] 新增回归：`TestMarketplaceProductLabelsAndDisplayNamesMatchRust`、`TestPluginStatusLabelMatchesRust`、`TestPluginSourceAuthVersionAndShareSummaries`、`TestPluginInstallAuthPopupView`、`TestMergeRemoteMarketplacesRemovesLocalCuratedAndStaleRemoteSections`、`TestSkillsMenuViewUsesRustCopyAndShortcut`、`TestManageSkillsViewAndChangeSummary`、`TestCollectToolMentionsIgnoresEnvVarsAndParsesLinkedPaths`、`TestFindSkillMentionsPrefersLinkedPathsThenNames`、`TestFindAppMentionsRequiresAccessibleEnabledUniqueAndNoSkillCollision`。
- [x] 验证：`go test ./internal/tui/chatwidget -run "TestMarketplace|TestPlugin|TestSkills|TestManageSkills|TestSkill|TestCollectTool|TestFind" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -count=1` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.35% 调整到 99.4%；本轮把 plugins/plugin_catalog/skills 纯逻辑收口，真正剩余收窄为 settings runtime persistence/model capability 接线、permissions runtime config/requirements/Windows prompt、goal runtime RPC、status_controls runtime/setup、完整 token activity chart/account RPC、rate-limit reset/model-switch surfaces、desktop notification OS hook、review/side/session_header/windows_sandbox_prompts 和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/review.rs`、`chatwidget/review_popups.rs`、`chatwidget/side.rs`、`chatwidget/session_header.rs`、`chatwidget/windows_sandbox_prompts.rs` 纯 view model：新增 review preset/branch/commit/custom prompt view、review target mapping、side conversation placeholder/context 状态、plain user turn shell escape policy、session header model setter、Windows sandbox enable/fallback/world-writable confirmation views 和 setup status。
- [x] 新增回归：`TestReviewPresetViewMatchesRustOrder`、`TestReviewBranchAndCommitPickers`、`TestReviewCustomPromptTarget`、`TestSideConversationStateAndPlainSubmitPolicy`、`TestChatSessionHeaderSetModel`、`TestWindowsSandboxEnablePromptView`、`TestWindowsSandboxFallbackPromptView`、`TestWorldWritableWarningConfirmationView`、`TestWindowsSandboxSetupStatus`。
- [x] 验证：`go test ./internal/tui/chatwidget -run "TestReview|TestSide|TestChatSessionHeader|TestWindowsSandbox|TestWorldWritable" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -count=1` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.4% 调整到 99.45%；本轮把 review/side/session_header/windows_sandbox_prompts 纯逻辑收口，真正剩余收窄为 settings runtime persistence/model capability 接线、permissions runtime config/requirements/Windows prompt、goal runtime RPC、status_controls runtime/setup、完整 token activity chart/account RPC、rate-limit reset/model-switch surfaces、desktop notification OS hook 和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/status_controls.rs` runtime/setup 控制层：新增 Go `StatusControlsState`、`StatusTokenUsage`、`StatusLineGitSummary`、status line/terminal title setup view model，覆盖 `set_status` details trim/capitalize、terminal title status refresh、status-line setup id 持久化、terminal-title preview/revert/cancel/commit、git branch/git summary stale cwd 丢弃、status surface preview data、context remaining/used、rate-limit window fallback、reasoning label、PR hyperlink 和 setup item description/preview。
- [x] 新增回归：`TestStatusControlsSetStatusRefreshesTitleWhenConfigured`、`TestStatusControlsStatusLineSetupBranchGitAndLimits`、`TestStatusControlsStaleBranchAndGitSummaryAreIgnored`、`TestStatusControlsTerminalTitlePreviewRevertAndCommit`、`TestStatusControlsHelpersAndPreviewData`、`TestStatusSetupViewsExposeRustItemMetadataAndPreviews`。
- [x] 验证：`go test ./internal/tui/chatwidget -run TestStatusControls -count=1 -v` 通过；`go test ./internal/tui/chatwidget -count=1` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.45% 调整到 99.5%；本轮把 `status_controls` 纯 runtime/setup 收口，真正剩余收窄为 Tea command/modal 事件接线、完整 token activity chart/account RPC、rate-limit reset/model-switch surfaces、desktop notification OS hook、permissions/settings/goal runtime 接线和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/tokens/chart.rs` 完整 token activity chart：Go `TokenActivityLines` 不再输出 graph pending，改为 52 周 fixed window；支持 daily heatmap、weekly bar、cumulative running total、month label、weekday/Y-axis gutter、legend、bar caption、view footer、duplicate date 累加、negative/future/invalid bucket 忽略和窄终端提示。
- [x] 新增回归：`TestTokenActivityDailyValuesDuplicateDatesAndNegativeClamp`、`TestTokenActivityBarLevelsFillFromBottom`、`TestTokenActivityChartLinesRenderDailyWeeklyAndCumulative`、`TestTokenActivityLoadedLinesRenderChartInsteadOfPending`。
- [x] 验证：`go test ./internal/tui/chatwidget -run TestTokenActivity -count=1 -v` 通过；`go test ./internal/tui/chatwidget -count=1` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.5% 调整到 99.55%；本轮把 token activity calendar/chart 纯渲染收口，真正剩余收窄为 account usage/read/reset consume RPC、rate-limit reset/model-switch surfaces、desktop notification OS hook、permissions/settings/goal runtime 接线、status_controls Tea command/modal/OSC 接线和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust TUI account usage/read/reset consume RPC：remote Bubble Tea TUI 的 `/usage daily|weekly|cumulative` 现在通过 app-server `account/usage/read` 拉取真实 token activity，Usage reset availability 通过 `account/rateLimits/read` 读取 reset credits，redeem reset 通过 `account/rateLimitResetCredit/consume` 带 UUID idempotency key 执行，并复用 chatwidget token chart/reset view model 渲染。
- [x] 新增回归：`TestInteractiveRemoteUsageCallbacksCallAccountRPC` 覆盖 remote helper initialize、account usage summary/buckets 转换、reset credits 读取、consume idempotency key 和 outcome 映射；已有 `TestModelUsageCommandReadsTokenActivity`、`TestModelUsageRateLimitResetCallbacks` 继续锁定 Tea 回调层。
- [x] 验证：`go test ./internal/app ./internal/tui/tea ./internal/tui/chatwidget -run "TestInteractiveRemoteUsage|TestModelUsage|TestTokenActivity|TestRateLimitReset|TestRuntimeRouter.*Account|TestRuntimeRouterConsumeRateLimit" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.55% 调整到 99.6%；本轮把 app-server account usage/read/reset consume RPC 接入 remote TUI，真正剩余收窄为 rate-limit reset/status surfaces/lower-cost model prompt、desktop notification OS posting hook、permissions/settings/goal runtime 接线、status_controls Tea command/modal/OSC 接线和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/status_controls.rs` Tea command/modal/OSC 接线：Go 新增 `/statusline`、`/title` slash command surface 和 help；Bubble Tea modal 支持 status-line/terminal-title 多选、Space toggle、Enter save、Esc cancel；terminal title setup 支持 preview/revert/commit 并通过 OSC 写入，status line 可切换 Rust-style header 且默认不打破既有主视图 snapshot。
- [x] 新增回归：`TestModelStatusLineAndTitleCommands`、`TestModelStatusControlsSetupHistoryAndInvalidItem`、`TestModelStatusLineCommandConfiguresHeader`、`TestModelStatusLineSetupModalTogglesAndSaves`、`TestModelTerminalTitleCommandWritesOSC`、`TestParseCommand` 更新覆盖 `/statusline`/`/title`。
- [x] 验证：`go test ./internal/tui/tea ./internal/tui ./internal/tui/chatwidget -run "TestModelStatus|TestParseCommand|TestStatusControls|TestStatusSurface|TestTerminalTitle" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.6% 调整到 99.65%；本轮把 status_controls Tea command/modal/OSC 接线收口，真正剩余收窄为 rate-limit reset/status surfaces/lower-cost model prompt、desktop notification OS posting hook、permissions/settings/goal runtime 接线和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/goal_menu.rs` / `chatwidget/goal_status.rs` runtime 接线：新增 Tea `/goal` slash command，支持 `status|set|edit|pause|resume|clear`，通过 callbacks 接入 remote app-server `thread/goal/get|set|clear`，并把 `thread/goal/updated|cleared` notification 映射为 Tea goal 状态刷新；goal status 也接入 status surface `task-progress`。
- [x] 修复 TUI `/diff` overlay 长尾：区分 transcript overlay 与 diff overlay，避免 View 刷新时把 diff pager 内容覆盖为 transcript。
- [x] 新增回归：`TestModelGoalCommandsCallRuntimeCallbacks`、`TestModelGoalReadAndNotifications`、`TestInteractiveRemoteGoalCallbacksCallAppServer`、`TestRemoteAppServerTUIClientMapsGoalNotifications`，并覆盖 `/goal` parser。
- [x] 验证：`go test ./internal/tui ./internal/tui/tea ./internal/app -run "TestParseCommand|TestModelGoal|TestInteractiveRemoteGoal|TestRemoteAppServerTUIClientMapsGoal" -count=1 -v` 通过；`go test ./internal/tui/tea -run "TestModelRaw|TestModelDiff|TestModelGoal" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.65% 调整到 99.70%；本轮把 goal runtime RPC/notification 接线收口，真正剩余收窄为 rate-limit reset/model-switch/status surfaces、desktop notification OS posting hook、permissions/settings runtime persistence 和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/permissions_menu.rs` / `chatwidget/permission_popups.rs` Tea runtime 接线：新增 `/permissions` slash command，打开 permissions modal，支持 Read Only/Default/Full Access session runtime 应用，Full Access 走二次确认；line fallback 也支持 `/permissions read-only|default|full-access`，后续 turn 通过现有 shared options 继承权限状态。
- [x] 新增回归：`TestModelPermissionsMenuAppliesRuntimeState`、`TestParseCommand` 覆盖 `/permissions`、`TestInteractiveSlashCommandsUpdateTUIState` 覆盖 line fallback `/permissions full-access`。
- [x] 验证：`go test ./internal/tui ./internal/tui/tea ./internal/app -run "TestParseCommand|TestModelPermissions|TestInteractiveSlashCommandsUpdateTUIState" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app -count=1` 通过。
- [x] TUI 进度从 99.70% 调整到 99.75%；本轮把 permissions runtime session menu 接线收口，真正剩余收窄为 rate-limit reset/model-switch/status surfaces、desktop notification OS posting hook、permissions requirements/Windows prompt、settings runtime persistence 和 Windows ConPTY 真机 restore 完整 smoke。
- [x] 对齐 Rust `chatwidget/settings.rs` / `chatwidget/settings_popups.rs` Tea runtime persistence：`/personality` 支持 popup 与直设，写入顶层 `personality` config 并回填 TUI state；`/experimental` 改为 Rust 式 Space 多选、Enter 批量保存，写入 `features.<key>`；local TUI、line fallback 和 remote TUI 均接入 settings write callback，保存后回读 effective config，同步 overrides/managed 状态；remote turn/thread start 会带上当前 personality。
- [x] 修复 settings 对齐时暴露的 chatwidget 编译长尾：去掉重复 `UserMessageForRestore`，补齐 MCP startup `normalizeStringSet` trim/dedupe/sort helper。
- [x] 新增回归：`TestModelSettingsCommandsPersistSelections`、`TestInteractiveSlashCommandsUpdateTUIState` 覆盖 personality/experimental config 写入、`TestLoadEffectiveStrictConfigAllowsPersonality` 锁定 Rust 支持的 `personality` strict config key。
- [x] 验证：`go test ./internal/tui/tea ./internal/app ./internal/config -run "TestModelRustSlashSettings|TestModelSettingsCommandsPersistSelections|TestInteractiveSlashCommandsUpdateTUIState|TestLoadEffectiveStrictConfigAllowsPersonality" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config -count=1` 通过。
- [x] TUI 进度从 99.75% 调整到 99.80%；本轮把 settings runtime persistence 收口，真正剩余收窄为 rate-limit reset/model-switch/status surfaces、desktop notification OS posting hook、permissions requirements/Windows prompt 和 Windows ConPTY 真机 restore 完整 smoke。
- [x] TUI 进度从 99.80% 调整到 99.90%；本轮按 Rust `tui/src/chatwidget/*.rs` 一层源码文件逐个补齐 Go `internal/tui/chatwidget` 对应接口边界，新增/显式化 `model_popups`、`mcp_startup`、`pets`、`input_restore`、`interrupts`、`plan_implementation`、`safety_buffering`、`protocol`、`protocol_requests`、`input_submission`、`input_flow`、`ide_context`、`keymap_picker`、`service_tiers`、`exec_state`、`command_lifecycle`、`hooks`、`turn_lifecycle`、`tool_requests`、`tool_lifecycle`、`replay`、`rendering`、`streaming`、`connectors`、`constructor`、`reasoning_shortcuts`、`goal_menu`、`hook_lifecycle`、`interaction`、`permission_popups`、`permissions_menu`、`plugin_catalog`、`review_popups`、`session_flow`、`settings_popups`、`slash_dispatch`、`tokens`、`transcript`、`turn_runtime` 等纯 Go view/state/dispatch 接口，并把 Tea selection modal 适配到 item ID/current/default/disabled reason。
- [x] TUI 验证：`go test ./internal/tui/... -count=1` 通过；额外用文件级扫描确认 Rust `chatwidget/*.rs` 一层源码在 Go 侧已有同名或等价接口文件。
- [x] 对齐 Rust `chatwidget/rate_limits.rs` lower-cost model switch prompt：Go 新增 `RateLimitSwitchPromptState`、`ShouldQueueRateLimitSwitchPrompt`、`NewRateLimitSwitchPromptView`，Tea 在 Codex 限额 >=90%、无 workspace credits、当前模型不是 `gpt-5.4-mini` 且未隐藏时打开 “Approaching rate limits” 弹窗；选择 Switch 会更新 model/default reasoning 并发出 picker decision，选择 never-show 会写入 `notices.hide_rate_limit_model_nudge` 并回读 settings。
- [x] 顺手补齐当前工作树暴露的 `SlashCommandFrames` catalog 编译缺口，`SlashCommandNames` 现在有 Rust-style 无前导 slash 的 frame source。
- [x] 新增回归：`TestRateLimitSwitchPromptQueueAndView`、`TestModelRateLimitSwitchPromptSwitchesModel`、`TestModelRateLimitSwitchPromptHidePersistsNotice`；`TestLoadEffectiveStrictConfigAllowsPersonality` 扩展到 `notices.hide_rate_limit_model_nudge` strict config key。
- [x] 验证：`go test ./internal/tui/chatwidget ./internal/tui/tea ./internal/config -run "TestRateLimitSwitch|TestModelRateLimitSwitch|TestLoadEffectiveStrictConfigAllowsPersonality|TestRateLimitWarning|TestModelAppliesRateLimit" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config -count=1` 通过；`go list -buildvcs=false ./...` 通过。
- [x] TUI 进度从 99.90% 调整到 99.92%；本轮把 rate-limit lower-cost model switch prompt 和 hidden notice persistence 收口，剩余继续限定为深接线/真机类缺口。
- [x] TUI 进度从 99.92% 调整到 99.96%；本轮继续按 Rust `tui/src` 递归文件级边界补齐 Go 侧接口壳，覆盖顶层 `additional_dirs/app/app_command/app_event/.../workspace_messages`，`bottom_pane`、`history_cell`、`render`、`notifications`、`onboarding`、`pets`、`status`、`app`、`app_server_session`、`markdown_render` 以及二级 `bottom_pane/chat_composer`、`mentions_v2`、`request_user_input`、`textarea`、`chatwidget/tokens/chart`、`ide_context`、`keymap_setup`、`public_widgets`、`tui` 等目录。
- [x] TUI 验证：`go test ./internal/tui/... -count=1` 通过；递归扫描 Rust `tui/src` 非测试 `.rs` 文件到 Go `internal/tui` 对应 `.go` 路径，缺失列表为空。
- [x] 对齐 Rust `tui/src/notifications/{mod,osc9,bel}.rs` 与 `chatwidget/notifications.rs` runtime posting hook：Go 新增 OSC9/BEL notification sequence backend、tmux DCS wrapper、focus condition，Tea 接入 agent-turn-complete / approval / elicitation pending notification，local/remote TUI 通过 `OnPostNotification` 写终端通知序列。
- [x] 新增回归：`TestNotificationSequenceOSC9AndBEL`、`TestNotificationAutoAndCondition`、`TestNotificationEnvSupportsOSC9`、`TestModelPostsTurnCompleteNotificationWhenUnfocused`、`TestModelPostsTurnCompleteNotificationFromTranscriptWhenMessageEmpty`、`TestModelSuppressesNotificationWhenFocused`、`TestModelPostsApprovalNotificationWhenUnfocused`、`TestModelPostsElicitationNotificationWhenUnfocused`、`TestInteractiveNotificationPosterWritesOSC9`。
- [x] 验证：`go test ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget ./internal/app -run "TestNotification|TestModel.*Notification|TestInteractiveNotificationPosterWritesOSC9|TestInteractiveSlashCommandsUpdateTUIState" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config -count=1` 通过。
- [x] TUI 进度从 99.96% 调整到 99.97%；desktop notification OS/terminal posting hook 收口，剩余继续限定为深行为/真机类缺口。
- [x] 对齐 Rust `TuiNotificationSettings` 配置接线：Go 本地/remote TUI 现在解析 `[tui] notifications = true|false|[...]`、`notification_method = "auto"|"osc9"|"bel"`、`notification_condition = "unfocused"|"always"`，并把 settings write/config read 的结果实时回填到 Tea notification setting/method/condition；remote turn completed 空消息会从 transcript 最后一条 assistant 文本提取 notification preview。
- [x] 新增回归：`TestModelNotificationUsesConfiguredMethod`、`TestInteractiveSettingsFromConfigParsesTUINotifications`、`TestInteractiveSettingsFromConfigDefaultsTUINotifications`。
- [x] 验证：`go test ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget ./internal/app -run "TestNotification|TestModel.*Notification|TestInteractiveNotificationPosterWritesOSC9|TestInteractiveSettingsFromConfig.*Notifications|TestInteractiveSlashCommandsUpdateTUIState" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config -count=1` 通过；`go list -buildvcs=false ./...` 通过。
- [x] TUI 进度从 99.97% 调整到 99.98%；desktop notification 配置、method/condition 和 remote preview 收口，剩余继续限定为深行为/真机类缺口。
- [x] 对齐 Rust `chatwidget/permissions_menu.rs` / `chatwidget/permission_popups.rs` requirements 与 Windows sandbox setup 接口：Go `PermissionRequirements` 现在覆盖 approval policy、reviewer、permission profile 和 `allowedWindowsSandboxImplementations`；permissions modal 会按 requirements 禁用 disallowed item；`/setup-default-sandbox` 接入 Tea callback、setup in-progress status、requirements 拒绝文案和 remote app-server `windowsSandbox/setupStart` RPC。
- [x] 新增回归：`TestWindowsSandboxModeAllowedHonorsRequirements`、`TestModelWindowsSandboxSetupUsesCallbackAndCompletion`、`TestModelWindowsSandboxSetupHonorsRequirements`、`TestInteractiveSettingsFromConfigParsesTUINotifications` 扩展 Windows sandbox requirements、`TestInteractiveRemoteLoadSettingsReadsRequirements` 扩展 remote requirements、`TestInteractiveRemoteStartWindowsSandboxSetupCallsAppServer`。
- [x] 验证：`go test ./internal/tui/chatwidget ./internal/tui/tea ./internal/app -run "TestWindowsSandboxModeAllowed|TestModelWindowsSandboxSetup|TestInteractiveSettingsFromConfigParsesTUINotifications|TestInteractiveRemoteLoadSettingsReadsRequirements|TestInteractiveRemoteStartWindowsSandboxSetupCallsAppServer" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config -count=1` 通过；`go list -buildvcs=false ./...` 通过。
- [x] TUI 进度从 99.98% 调整到 99.99%；permissions requirements/Windows sandbox setup TUI runtime 接线收口，剩余继续限定为深行为/真机类缺口。
- [x] 对齐 Rust TUI `/hooks` / `chatwidget/hook_lifecycle.rs` browser app-server 清单接线：Tea 新增 `OnReadHooks` runtime callback、`HooksListResultMsg`、本地 hook lifecycle fallback；remote TUI 通过 app-server `hooks/list` 拉取真实 registry/discovery 清单，并把 cwd、source、path、plugin、trust、disabled、status message、warnings/errors 映射到 `chatwidget.NewHooksBrowserView`。
- [x] 新增回归：`TestModelHooksCommandReadsRuntimeHooks`、`TestModelHooksCommandUsesLifecycleFallback`、`TestInteractiveRemoteReadHooksCallsAppServer`。
- [x] 验证：`go test ./internal/tui/tea ./internal/app -run "TestModelHooksCommand|TestInteractiveRemoteReadHooks" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config -count=1` 通过；`go list -buildvcs=false ./...` 通过。
- [x] TUI 进度保持 99.99%；`/hooks` 不再是占位提示，已纳入 remote app-server-backed 真实清单与本地生命周期浏览器，剩余继续限定为深行为/真机类缺口。
- [x] 对齐 Rust TUI `/plugins` / plugin catalog app-server 主路径：Go 新增 `chatwidget.NewPluginsCatalogView`，Tea 新增 `OnReadPlugins`/`PluginListResultMsg`，remote TUI 通过 app-server `plugin/list` 读取 marketplace catalog；同时修正 `PluginListParams.includeInstalled` JSON-RPC 序列化，确保 installed/enabled 状态不会在 remote TUI 中丢失。
- [x] 新增回归：`TestPluginsCatalogViewUsesRuntimeCatalog`、`TestPluginsCatalogViewEmptyAndErrors`、`TestModelPluginsCommandReadsRuntimeCatalog`、`TestInteractiveRemoteReadPluginsCallsAppServer`、`TestPluginLifecycleAndShares` 更新 includeInstalled wire 断言。
- [x] 验证：`go test ./internal/tui/chatwidget ./internal/tui/tea ./internal/app ./internal/plugin -run "TestPluginsCatalogView|TestModelPluginsCommand|TestInteractiveRemoteReadPlugins|TestPluginLifecycleAndShares" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config ./internal/plugin -count=1` 通过；`go list -buildvcs=false ./...` 通过。
- [x] TUI 进度保持 99.99%；`/plugins` 不再是占位提示，已纳入 remote app-server-backed catalog 浏览，剩余继续限定为 review/side/agent/theme/pets 深行为与真机类缺口。

### 2026-07-08

- [x] 对齐 Rust TUI `/skills` / skills inventory app-server 主路径：Go 新增 `chatwidget.NewSkillsBrowserView`，Tea `/skills` 的 Enable/Disable Skills 分支新增 `OnReadSkills`/`SkillsListResultMsg`，remote TUI 通过 app-server `skills/list` 按 cwd 读取 inventory，并展示 enabled/disabled、scope、plugin、path 和 skill errors。
- [x] 新增回归：`TestSkillsBrowserViewUsesRuntimeInventory`、`TestModelSkillsManageReadsRuntimeInventory`、`TestInteractiveRemoteReadSkillsCallsAppServer`。
- [x] 验证：`go test ./internal/tui/chatwidget ./internal/tui/tea ./internal/app -run "TestSkillsBrowserView|TestModelSkillsManage|TestInteractiveRemoteReadSkills" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config ./internal/plugin -count=1` 通过；`go list -buildvcs=false ./...` 通过。
- [x] TUI 进度保持 99.99%；`/skills` manage 不再是占位提示，已纳入 remote app-server-backed inventory 浏览，剩余继续限定为 review/side/agent/theme/pets 深行为与真机类缺口。
- [x] 对齐 Rust TUI `/apps` / connector catalog app-server 主路径：Go 新增 `chatwidget.NewAppsCatalogView`、loading/error view、installed/disabled/installable 状态文案；Tea `/apps` 新增 `OnReadApps`/`AppListResultMsg`；remote TUI 通过 app-server `app/list` 按 threadID 与 forceRefetch 读取 connector catalog。
- [x] 新增回归：`TestAppsCatalogViewUsesRuntimeCatalog`、`TestAppsLoadingAndErrorViews`、`TestModelAppsCommandReadsRuntimeCatalog`、`TestInteractiveRemoteReadAppsCallsAppServer`。
- [x] 验证：`go test ./internal/tui/chatwidget ./internal/tui/tea ./internal/app -run "TestAppsCatalogView|TestAppsLoading|TestModelAppsCommand|TestInteractiveRemoteReadApps" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config ./internal/plugin ./internal/apps -count=1` 通过；`go list -buildvcs=false ./...` 通过。
- [x] TUI 进度保持 99.99%；`/apps` 不再是占位提示，已纳入 remote app-server-backed catalog 浏览，剩余继续限定为 review/side/agent/theme/pets 深行为与真机类缺口。
- [x] 对齐 Rust TUI `/review` / app-server `review/start` 主路径：Go 新增 Tea `OnStartReview`/`ReviewStartResultMsg`，`/review <custom>` 与 “Review uncommitted changes” preset 会生成 inline `review.StartParams`，remote TUI 通过 app-server `review/start` 启动 review turn；无 callback 时保留明确 fallback 文案。
- [x] 新增回归：`TestModelReviewCustomStartsRuntimeReview`、`TestModelReviewUncommittedPresetStartsRuntimeReview`、`TestInteractiveRemoteStartReviewCallsAppServer`，并更新 `TestModelRustSlashLongTailCommandSurfaces` fallback 断言。
- [x] 验证：`go test ./internal/tui/tea ./internal/app -run "TestModelReview|TestInteractiveRemoteStartReview" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config ./internal/plugin ./internal/apps ./internal/review -count=1` 通过；`go list -buildvcs=false ./...` 通过。
- [x] TUI 进度保持 99.99%；`/review` start 不再只是本地 transcript 占位，剩余 review 缺口收敛为 branch/commit picker git inventory 与更完整 review mode/golden。
- [x] 对齐 Rust TUI `/review` branch/commit picker git inventory：Go 新增 `review.LocalBranches`/`review.RecentCommits`，Tea review picker 支持异步 branch/commit reader，选择 branch/sha 后继续调用 app-server `review/start`。
- [x] 新增回归：`TestModelReviewBranchPickerStartsRuntimeReview`、`TestModelReviewCommitPickerStartsRuntimeReview`、`TestGitInventoryBranchesAndCommits`。
- [x] 验证：`go test ./internal/tui/tea ./internal/review -run "TestModelReview|TestGitInventory|TestGitDiff" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell ./internal/protocol ./internal/exec ./internal/app ./internal/config ./internal/plugin ./internal/apps ./internal/review -count=1` 通过；`go list -buildvcs=false ./...` 通过。
- [x] TUI 进度保持 99.99%；`/review` branch/commit picker 已具备 Rust-style git inventory，剩余继续限定为 side/agent/theme/pets 深行为与真机类缺口。
- [x] 对齐 Rust TUI 输入框 slash command popup：Go Tea composer 现在输入 `/` 会打开 Rust 顺序的命令联想，支持 prefix 过滤、上下/Ctrl-P/Ctrl-N 循环选择、Esc 关闭、Tab 补全、Enter 直接分发选中命令，并为当前选择渲染蓝色高亮行/选择颜色条。
- [x] 新增回归：`TestModelSlashCommandPopupFiltersCompletesAndDispatches` 覆盖 `/` 默认首项 `/model`、`/mo` 过滤、Tab 补全、上下选择高亮、Enter 打开 model picker。
- [x] 验证：`go test ./internal/tui/tea -run "TestModelSlashCommandPopup|TestModelSlashCommands" -count=1 -v` 通过；`go test ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui -count=1` 通过。
- [x] 统一 Rust-style selection color bar：新增 `tui.RenderSelectedRow` 强制 ANSI 亮蓝选中行，Tea modal、slash popup、SelectionList、ThemePicker、SessionPicker 主行全部复用，覆盖 model/permissions/usage/review/skills/apps/plugins/hooks/session/statusline/title/theme 等选择型 UI。
- [x] 新增回归：`TestModelModalSelectionRendersColorBar`、`TestSelectionListSkipsDisabledAndRendersRows` 颜色条断言，确保选中行带终端颜色并随上下选择移动。
- [x] 验证：`go test ./internal/tui ./internal/tui/tea -run "TestSelectionListSkipsDisabledAndRendersRows|TestModelModalSelectionRendersColorBar|TestModelSlashCommandPopup" -count=1 -v` 通过；`go test ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui ./internal/tui/chatwidget -count=1` 通过；`go list -buildvcs=false ./...` 通过。
- [x] 对齐 Rust TUI `/side` / `tui/src/app/side.rs` app-server 主路径：Go Tea 新增 `SideStartFunc`、Rust 同款 side boundary prompt/developer instructions/error mapping，`/side` 与 `/btw` 会 fork ephemeral side thread、注入 hidden boundary item、可选首条消息按 plain user turn 提交；进入 side 后清空可见 transcript、显示 side placeholder/footer，idle 时 Ctrl+C 返回主线程并恢复父 transcript。
- [x] Remote TUI 接入 app-server `thread/fork` + `thread/inject_items`：fork 继承当前 model、reasoning、approval、sandbox、cwd/config overrides，并设置 `ephemeral=true` 与 side developer instructions；首条 side prompt 复用既有 `turn/start` streaming 路径。
- [x] 新增回归：`TestModelSideCommandStartsRuntimeSideConversation`、`TestModelBtwCommandStartsSideWithPlainUserTurn`、`TestModelSideCommandRequiresStartedThread`、`TestModelSideStartErrorMapsMissingConversation`、`TestInteractiveRemoteStartSideForksAndInjectsBoundary`。
- [x] 验证：`go test ./internal/tui/tea ./internal/app -run "TestModelSide|TestModelBtw|TestInteractiveRemoteStartSide" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget ./internal/app -count=1` 通过；`go list -buildvcs=false ./...` 通过；`go test ./... -count=1` 通过。
- [x] 继续对齐 Rust TUI side 深行为：Go Tea 新增 `SideCloseFunc`、`SideParentStatusChangeMsg`、`ThreadScopedEventMsg`、side footer 父线程状态标签、父线程后台 transcript 快照、side 内 slash command allowlist、running 时 `/side`/`/btw` 即时 fork；remote TUI 关闭 side 时调用 app-server `thread/unsubscribe`，父线程 `turn/completed`、`thread/closed`、approval/user-input server request 与 resolved 通知只更新 side footer/父快照，不再污染当前 side transcript 或误切 `state.ThreadID`。
- [x] 新增回归：`TestModelSideReturnClosesRuntimeSideConversation`、`TestModelSideCloseFailureKeepsSideVisible`、`TestModelSideParentStatusUpdatesFooter`、`TestModelSideKeepsParentTranscriptSnapshotUpdated`、`TestModelSideRejectsUnavailableSlashCommands`、`TestModelSideCommandStartsWhileParentTaskRunning`、`TestInteractiveRemoteCloseSideUnsubscribesThread`、`TestRemoteNotificationForParentThreadUpdatesSideStatusOnly`。
- [x] 验证：`go test ./internal/tui/tea ./internal/app -run "TestModelSide|TestRemoteNotificationForParentThread|TestInteractiveRemoteCloseSide|TestInteractiveRemoteStartSide" -count=1 -v` 通过；`go test ./internal/tui/tea ./internal/tui/chatwidget ./internal/app -count=1` 通过；`go list -buildvcs=false ./...` 通过；`go test ./... -count=1` 通过。
- [x] 对齐 Rust TUI `/agent` / `multi_agents.rs` / `app/agent_navigation.rs` app-server 主路径：Go 新增 `AgentThreadEntry` Rust 同款 picker label 规则、Tea `OnReadAgents`/`OnSwitchAgent`、`ModalKindAgent`、`/agent` loading/error/selection modal、active agent footer label；remote TUI 通过 app-server `thread/loaded/list` + `thread/read` 发现主线程与 loaded 子线程，选择后用 `thread/read includeTurns=true` 切换 thread 并恢复基础 transcript。
- [x] 新增回归：`TestFormatAgentPickerItemNameMatchesRustRules`、`TestModelAgentCommandLoadsPickerAndSwitchesThread`、`TestInteractiveRemoteAgentThreadEntriesLoadsLoadedSubagents`、`TestInteractiveRemoteSwitchAgentThreadReadsTranscript`。
- [x] 验证：`go test ./internal/tui ./internal/tui/tea ./internal/app -run "TestFormatAgent|TestModelAgent|TestInteractiveRemoteAgent" -count=1 -v` 通过；`go test ./internal/app -run "TestInteractiveRemoteSwitchAgentThreadReadsTranscript" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/tea ./internal/app -count=1` 通过；`go list -buildvcs=false ./...` 通过；`go test ./internal/appserver -run "TestCommandExecTTYStreamsAndResizes" -count=1 -v` 单独重跑按既有 Windows ConPTY gated skip/pass；第二次 `go test ./... -count=1` 通过。
- [x] TUI 进度保持 99.99%；`/agent` 不再是占位提示，已纳入 remote app-server-backed loaded/read picker 与 thread switch，剩余继续限定为真机类缺口与业务/golden 收口。
- [x] 对齐 Rust TUI `/theme` / `theme_picker.rs` 与 `/pets` / `pets/picker.rs` settings runtime：Go 新增 Rust 内置 theme 列表、`/theme` picker/direct command 写入 `tui.theme` 并回填 current selection；`/pets` picker/direct command 写入 `tui.pet`，默认 pet id 改为 Rust 的 `codex`，禁用项固定置顶，内置 catalog 覆盖 `codex/dewey/fireball/rocky/seedy/stacky/bsod/null-signal`，local/remote TUI settings load/write 均同步 `tui.theme` 和 `tui.pet`。
- [x] 新增回归：`TestModelThemeAndPetsCommandsPersistSelections`、`TestModelThemeAndPetsPickersUseCurrentSelections`、`TestInteractiveSettingsFromConfigParsesTUINotifications` 扩展 theme/pet config 解析，`TestPetsPickerView` 覆盖 Rust 默认 pet id。
- [x] 验证：`go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/app -run "Test.*(Theme|Pets|SettingsFromConfig|RustSlashLongTail)|TestPetsPickerView" -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea ./internal/app -count=1` 通过；`go list -buildvcs=false ./...` 通过；`go test ./... -count=1` 通过。
- [x] TUI 进度保持 99.99%；`/theme` 和 `/pets` 不再是占位提示，已纳入 Rust-style picker/direct command 与 settings persistence，剩余继续限定为真机类缺口与业务/golden 收口。
- [x] 对齐 Rust TUI Windows sandbox setup completion / app-server prompt live smoke：Tea 覆盖 `/setup-default-sandbox` setupStart 返回 `started=true` 后继续保持 in-progress、随后接收 `WindowsSandboxSetupCompletedMsg` 清掉输入禁用状态并显示完成；remote TUI client 覆盖 app-server `windowsSandbox/setupCompleted` notification 到 Tea completion message 的映射。
- [x] 新增回归：`TestModelWindowsSandboxSetupCompletionNotificationClearsStatus`、`TestRemoteAppServerTUIClientMapsWindowsSandboxSetupCompleted`。
- [x] 验证：`go test ./internal/tui/tea ./internal/app -run "TestModelWindowsSandboxSetup|TestRemoteAppServerTUIClientMapsWindowsSandboxSetupCompleted|TestInteractiveRemoteStartWindowsSandboxSetup" -count=1 -v` 通过；`go test ./internal/tui/tea ./internal/app -count=1` 通过。
- [x] TUI 进度保持 99.99%；Windows sandbox setup completion/app-server prompt 已有 live-ish 回归覆盖，剩余继续限定为 Windows ConPTY 真机 restore 与业务/golden 收口。
- [x] 补齐 Windows ConPTY terminal restore gated smoke：Windows 侧 `TestRealPTYTerminalRestoreSmoke` 不再只是占位 skip；默认保持 gated skip，设置 `CODEX_GO_TUI_PTY_SMOKE=1` 后先跑 `cmd /c echo` host output probe，probe 通过才启动真实 TUI child，经 ConPTY 写入 Ctrl+C 并断言 alt-screen enter/leave；child 通过 `TestMain` 提前进入 TUI，避免 testing harness 干扰，并尝试 `CONIN$`/`CONOUT$` 作为终端句柄。
- [x] 新增 host limitation 处理：当前宿主 ConPTY output probe 返回空输出时按 Rust legacy ConPTY limitation 明确 skip/pass；同时保留 `STATUS_DLL_INIT_FAILED` skip 分支，避免把宿主 ConPTY 初始化限制误报为 TUI 退化。
- [x] 验证：`go test ./internal/tui/tea -run "TestRealPTYTerminalRestoreSmoke" -count=1 -v` 通过（默认 gated skip）；`CODEX_GO_TUI_PTY_SMOKE=1 go test ./internal/tui/tea -run "^TestRealPTYTerminalRestoreSmoke$" -count=1 -v` 通过（当前宿主 output probe skip）。
- [x] TUI 进度保持 99.99%；Windows ConPTY 已有真实 gated smoke/probe 夹具，剩余继续限定为支持 ConPTY 输出宿主上的完整通过记录与业务/golden 收口。
- [x] 对齐 Rust `tui/src/pets/{catalog,asset_pack,image_protocol,model,ambient}.rs` 纯业务核心：Go `internal/tui/pets` 现在包含 Rust 内置 pet catalog、spritesheet cache/CDN path、Kitty/KittyLocalFile/Sixel support detection、默认 8x9 spritesheet animation 表、frame timing/loop_start 计算、notification lifecycle、ambient/picker draw request 布局；`chatwidget.BuiltinPetOptions` 改为复用 pets catalog，避免 `/pets` picker 与 runtime catalog 漂移。
- [x] 继续对齐 Rust `pets/image_protocol.rs`、`pets/preview.rs` 与 `pets/mod.rs` render state：Go 新增 Kitty inline PNG/local-file/delete image 序列、tmux passthrough 包裹、payload chunking、`/pets` picker preview state 的 Hidden/Loading/Disabled/Ready/Error、last area 和 centered text area，并补 `PetImageRenderState`、ambient/picker image id、Kitty 旧图删除、Sixel clear area、光标 save/move/restore 写入。
- [x] 新增回归：`TestBuiltinCatalogMatchesRustPets`、`TestDefaultAnimationsMatchRustSpriteRows`、`TestCurrentAnimationFrameUsesDurationsAndLoopStart`、`TestImageSupportDetectionMatchesRustBranches`、`TestKittyImageProtocolSequences`、`TestAmbientPetNotificationAndDrawRequest`、`TestPetPickerPreviewStateRendersRustStatuses`、`TestRenderPetImageWritesKittyPayloadAndDeletesOnClear`、`TestRenderPetImageClearsSixelArea`。
- [x] 验证：`go test ./internal/tui/pets -count=1 -v` 通过；`go test ./internal/tui/pets ./internal/tui/chatwidget -run "TestBuiltin|TestDefaultAnimations|TestCurrentAnimation|TestImageSupport|TestAmbientPet|TestPetsPickerView" -count=1 -v` 通过；`go test ./internal/tui/pets ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea -count=1` 通过。
- [x] TUI 进度保持 99.99%；pets 不再只是 picker/settings 壳，已具备 Rust-style runtime 核心、Kitty/preview/render state 纯逻辑，剩余为真实图片下载/解码/Sixel 编码/Tea 终端输出接线与 golden。
- [x] 对齐 Rust `tui/src/onboarding/{welcome,trust_directory,keys,onboarding_screen,auth}.rs` 与 `auth/headless_chatgpt_login.rs` 纯状态/文案核心：Go `internal/tui/onboarding` 现在覆盖 Welcome step hidden/complete 规则、动画尺寸断点、固定快捷键清单、Trust Directory trust/quit selection state、Git root warning/Windows sandbox hint 文案、screen current-step 截断逻辑、auth option 列表和 device-code login copyable lines。
- [x] 新增回归：`TestWelcomeStateAndAnimationBreakpoint`、`TestTrustDirectoryPromptStateRenderAndSelection`、`TestOnboardingScreenCurrentStepsAndDone`、`TestAuthOptionsAndStepState`、`TestHeadlessChatGPTLoginStateLines`。
- [x] 验证：`go test ./internal/tui/onboarding ./internal/tui/onboarding/auth -count=1 -v` 通过；`go test ./internal/tui/onboarding ./internal/tui/onboarding/auth ./internal/tui ./internal/tui/tea -count=1` 通过。
- [x] TUI 进度保持 99.99%；onboarding 不再只是几个空 struct/function，已具备 Rust-style 首启状态和核心文案，剩余为 app-server login async/ratatui 级 snapshot 对齐。
- [x] 对齐 Rust `tui/src/status/{account,remote_connection,format,helpers,rate_limits}.rs` 纯业务核心：Go `internal/tui/status` 现在覆盖 ChatGPT/API key account display、websocket user/pass/query/fragment 脱敏、unix/version display、FieldFormatter label width/value indent、model reasoning detail、plan type display、compact token formatting、reset timestamp、rate-limit Available/Stale/Unavailable/Missing rows、non-codex label 合并、credits/monthly credit limit、progress bar 与 summary。
- [x] 新增回归：`TestAccountStatusDisplayMatchesRustStatusAccountDisplay`、`TestRemoteConnectionStatusValueMatchesRust`、`TestFieldFormatterMatchesRustSpacing`、`TestHelpersMatchRustFormatting`、`TestRateLimitRowsMatchRustStatusComposition`、`TestRateLimitRenderingAndCreditFormatting`。
- [x] 验证：`go test ./internal/tui/status -count=1 -v` 通过；`go test ./internal/tui/status ./internal/tui/chatwidget ./internal/tui ./internal/tui/tea -count=1` 通过；`go list -buildvcs=false ./...` 通过；`go test ./... -count=1` 通过。
- [x] TUI 进度保持 99.99%；status 不再只是 account/remote/rate-limit 空 struct，已具备 Rust-style status 数据和格式核心，剩余为完整 `/status` history card 接线、app-server account/rate-limit live refresh 和 snapshot/golden。
- [x] 对齐 Rust `tui/src/app/resize_reflow.rs` 与 `agent_message_consolidation.rs` 纯算法：Go `internal/tui/app` 新增 `TranscriptCell`、`TrailingRunStart`、initial history replay row cap、`RenderTranscriptLinesForReflow`、stream-time reflow 判定和 `ConsolidateAgentMessageRun`，覆盖 Rust “从尾部按 row cap 重建、补齐 continuation run 头、最后裁剪”和 finalized agent markdown consolidation 语义。
- [x] 对齐 Rust `tui/src/app/pending_interactive_replay.rs` 与 `replay_filter.rs` 状态机：Go 新增 pending exec approval/patch approval/MCP elicitation/permissions/request_user_input sets、turn-indexed FIFO、request-id 反查、outbound op/server notification/eviction 清理、pending approvals/user-input 查询、snapshot interactive request 与 notice 过滤。
- [x] 新增回归：`TestTrailingRunStartMatchesRustStreamingRun`、`TestRenderTranscriptLinesForReflowRestoresSeparatorsAndRowCap`、`TestRenderTranscriptLinesForReflowExtendsRetainedSuffixToRunHead`、`TestConsolidateAgentMessageRunMatchesRustReplacement`、`TestPendingInteractiveReplayKeepsAndDropsRequestUserInputFIFO`、`TestPendingInteractiveReplayTurnCompletedClearsTurnScopedPrompts`、`TestReplayFiltersMatchRustHelpers` 等。
- [x] 验证：`go test ./internal/tui/app -count=1 -v` 通过；`go test ./internal/tui/app ./internal/tui/status ./internal/tui ./internal/tui/tea -count=1` 通过；`go list -buildvcs=false ./...` 通过；`go test ./... -count=1` 通过。
- [x] TUI 进度保持 99.99%；`internal/tui/app` 不再全是薄壳，已补上 transcript reflow/consolidation/replay-filter 的 Rust-style 核心，剩余继续按 `app/*.rs` 文件补 session/routing/events/config/history UI 深行为。
- [x] 对齐 Rust `tui/src/app/loaded_threads.rs`：Go 新增 `FindLoadedSubagentThreadsForPrimary`，从 flat loaded thread list 按 `subagent_thread_spawn` parent edge 广度式递归找出 primary thread 的所有子孙 subagent，排除 primary/self/unrelated/non-spawn，并按 thread id 确定性排序输出 nickname/role/path。
- [x] 对齐 Rust `tui/src/app/agent_status_feed.rs` 预览核心：Go 新增 `AgentStatusThreadPreview`、`AgentActivitySummary`、bounded summary 和 wrap/last-3-lines 逻辑，覆盖 agent message/plan/reasoning/command/file change/MCP/dynamic tool/collab tool/subagent activity/web search/image/review/context compaction 文案，按最新事件去重并限制 6 个 item。
- [x] 新增回归：`TestFindLoadedSubagentThreadsForPrimaryWalksSpawnTree`、`TestAgentActivitySummaryMatchesRustCases`、`TestAgentStatusThreadPreviewUsesRecentUniqueItems`、`TestAgentStatusThreadPreviewWrapsAndKeepsLastThreeLines`、`TestBoundedAgentActivitySummaryTruncatesAndCompactsWhitespace`。
- [x] 验证：`go test ./internal/tui/app -count=1 -v` 通过；`go test ./internal/tui/app ./internal/tui ./internal/tui/tea -count=1` 通过；`go test ./... -count=1` 通过。
- [x] TUI 进度保持 99.99%；`/agent` running status feed 与 loaded subagent backfill 已有 Rust-style 纯逻辑，剩余是接真实 app-server Thread/ThreadItem 映射和 history cell snapshot。
- [x] 对齐 Rust `tui/src/{update_versions,update_action,updates_cache,updates,update_prompt}.rs` 纯逻辑：Go `internal/tui` 现在具备 Rust-style `rust-v` tag 解析、plain semver 比较、source build `0.0.0` 跳过、20h update cache TTL、`version.json` latest/last_checked_at/dismissed_version 格式、dismiss 写回、update action command args/string，以及 update prompt 三项选择状态机、数字键/上下键/Ctrl-C/Esc/Enter 行为和选中行色条渲染。
- [x] 新增回归：`TestUpdateVersionsMatchRust`、`TestUpdateAvailableUsesRustSemverRules`、`TestUpdateActionCommandStringsMatchRust`、`TestUpdatesCacheDismissVersionMatchesRust`、`TestUpdatesPopupEligibilityMatchesRust`、`TestUpdatePromptScreenMatchesRustKeys`、`TestUpdatePromptRowsUseSelectedColorBar`。
- [x] 对齐 Rust `tui/src/{workspace_messages,workspace_command,terminal_visualization_instructions,windows_sandbox}.rs` 轻量业务核心：Go 新增 workspace headline refresh interval/response extraction、workspace command argv/cwd/env/timeout/output cap boundary、terminal visualization instructions 拼接、Windows sandbox mode/feature 到 level 的纯逻辑映射。
- [x] 新增回归：`TestWorkspaceHeadlineFromResponseMatchesRust`、`TestWorkspaceCommandBuilderMatchesRustDefaults`、`TestWorkspaceCommandOutputAndRunnerBoundary`、`TestTerminalVisualizationInstructionsMatchRust`、`TestWindowsSandboxLevelFromConfigMatchesRust`。
- [x] 验证：`go test ./internal/tui -run "TestUpdate|TestUpdates" -count=1 -v` 通过；`go test ./internal/tui -run "TestWorkspace|TestTerminalVisualization|TestWindowsSandbox|TestUpdate|TestUpdates" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/app ./internal/tui/status ./internal/tui/chatwidget ./internal/tui/tea -count=1` 通过。
- [x] TUI 进度保持 99.99%；updates/workspace 相关文件不再是占位壳，已具备 Rust-style 纯逻辑和选择色条覆盖，剩余继续限定为深行为/真机类缺口与 golden/snapshot 收口。
- [x] 对齐 Rust `tui/src/mention_codec.rs`：Go 新增 `LinkedMention`、`DecodedHistoryText`、`EncodeHistoryMentions`、`DecodeHistoryMentionsWithAtMentions`，覆盖 `$`/`@` mention round-trip、plugin legacy fallback、common env var skip、tool/app/mcp/plugin/skill/SKILL.md path 判断、句末/括号/Unicode 空白/路径后缀边界和 sigil-specific queue。
- [x] 对齐 Rust `tui/src/oss_selection.rs` 纯状态：Go 新增 LM Studio/Ollama provider IDs、status、默认端口、自动选择规则、左右/Ctrl-H/Ctrl-L/L/O/Esc/Ctrl-C/Enter 选择状态机，以及带色条的 provider rows。
- [x] 对齐 Rust `tui/src/transcript_reflow.rs` 状态机：Go 新增 75ms debounce、observed width 与 actual reflow width 分离、pending target 去重、immediate schedule、stream-time reflow flags、finish-drain 和 clear/clear-stream 语义。
- [x] 对齐 Rust `tui/src/session_archive_commands.rs` 共享纯逻辑：Go 新增 archive/delete/unarchive action、delete confirmation、success/cancel/no-match 文案、active/archived 搜索范围和 delete prompt/answer helper。
- [x] 对齐 Rust `tui/src/branch_summary.rs` 纯解析边界：Go 新增 `GitBranchDiffStats`、`StatusLineGitSummary`、`StatusLinePullRequest`、默认分支解析、remote origin 排序、`git diff --numstat` 汇总、`gh pr view/api` open PR 解析和 parent-first repo search order。
- [x] 对齐 Rust `tui/src/tooltips.rs` 启动提示核心：Go 新增 Rust 同源 `tooltips.txt`、静态 tip 平台过滤、paid/free/go/unknown plan 分流、Fast mode promo 抑制、公告 TOML `[[announcements]]` parser、最后匹配、日期/版本/target_app/plan/OS gate 和无网络纯选择接口。
- [x] 对齐 Rust `tui/src/terminal_probe.rs` parser/state 层：Go 新增 100ms 默认探测预算、OSC 10/11 默认色解析、BEL/ST 终止、2/4 位 RGB/RGBA component 解析、cursor position 零基转换、keyboard enhancement flags/PDA fallback 判定和 startup probe 聚合/finish 语义。
- [x] 对齐 Rust `tui/src/clipboard_copy.rs` 注入式 copy 状态机：Go 新增 SSH/TMUX/WSL/native/OSC52 fallback 顺序、tmux forwarding readiness、OSC52 payload 精确错误、writer helper、环境变量识别和 native/WSL/terminal 错误文案组合。
- [x] 对齐 Rust `tui/src/model_migration.rs` 纯状态：Go 新增 migration copy/markdown placeholder、can-opt-out 菜单、Try new/use existing 选项、Esc/Enter/1/2/Up/Down/Ctrl-C/Ctrl-D 行为，以及选中行色条。
- [x] 对齐 Rust `tui/src/status_indicator_widget.rs` 轻量状态：Go `StatusIndicatorWidgetState` 接入 elapsed compact、details capitalization/max lines、inline message、interrupt hint toggle、pause/resume timer 和 header update。
- [x] 对齐 Rust `tui/src/get_git_diff.rs` 安全命令构造：Go 新增 fsmonitor override、hooks disable config、tracked/untracked diff args、filter clean/process/required override、diff exit-code 1 成功语义、stdout error 语义和 untracked diff 拼接。
- [x] 新增回归：`TestDecodeHistoryMentions*`、`TestEncodeHistoryMentions*`、`TestOSSSelection*`、`TestTranscriptReflow*`、`TestSessionArchive*`、`TestParseGitNumstatMatchesRust`、`TestPullRequestParsersRequireOpenPR`、`TestDefaultTooltipsMatchRustFiltering`、`TestAnnouncementTipTOML*`、`TestParseOSCColorsMatchesRust`、`TestParseKeyboardEnhancementSupportMatchesRust`、`TestClipboardCopyWith*`、`TestModelMigration*`、`TestStatusIndicatorWidget*`、`TestBuildGitDiffCommandMatchesRust`。
- [x] 验证：`go test ./internal/tui -run "Test.*(Mention|OSS|TranscriptReflow|SessionArchive)" -count=1 -v` 通过；`go test ./internal/tui -run "Test.*(Branch|PullRequest|RepoSearch|GitNumstat|OrderedGit)" -count=1 -v` 通过；`go test ./internal/tui -run "Test.*Tooltip|TestAnnouncement" -count=1 -v` 通过；`go test ./internal/tui -run "Test.*(Terminal|OSC|Keyboard|StartupProbe|DefaultColors|CursorPosition)" -count=1 -v` 通过；`go test ./internal/tui -run "Test.*Clipboard|TestOSC52|TestTmuxClipboard" -count=1 -v` 通过；`go test ./internal/tui -run "Test.*ModelMigration|TestMigration" -count=1 -v` 通过；`go test ./internal/tui -run "TestStatusIndicator|TestFormatElapsedCompact" -count=1 -v` 通过；`go test ./internal/tui -run "Test.*Git.*Diff|TestDiffFilter|TestGitCapture|TestParseUntracked" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/app ./internal/tui/status ./internal/tui/chatwidget ./internal/tui/tea -count=1` 通过。
- [x] TUI 进度保持 99.99%；mention/history round-trip、OSS provider startup selection、scrollback reflow scheduling、session archive/delete 文案、branch status/PR summary parser、startup tooltip/announcement、terminal probe parser、clipboard copy fallback、model migration prompt、status indicator state 和 git diff command safety 不再是占位壳，剩余继续限定为真终端/深渲染/golden snapshot 收口。
- [x] 对齐 Rust `tui/src/external_agent_config_migration*.rs` 状态机与分组文案：Go 新增 migration group/item label、type label、count summary、details、focus/action/view、空格选择、Enter/数字快捷键、Skip/Proceed outcome，并统一选中行色条。
- [x] 对齐 Rust `tui/src/debug_config.rs` 可读输出：Go `internal/tui` 新增 Rust-style layer stack/requirements renderer、非文件层原始值、session flags key 展开、network constraints、managed hooks summary、session proxy `all_proxy`；本地 Tea `/debug-config` 接入 config service + project layer + `requirements.toml`，app-server requirements 读取复用同一 loader。
- [x] 新增回归：`TestExternalAgentConfigMigration*`、`TestDebugConfigOutput*`、`TestDebugConfigSessionAllProxyURLMatchesRust`、`TestLoadRequirementsFile*`、`TestInteractiveDebugConfigReaderUsesRustStyleRenderer`、`TestAppServerRequirementsFromFileParsesFullRequirements`。
- [x] 验证：`go test ./internal/tui -run "TestExternalAgentConfigMigration" -count=1 -v` 通过；`go test ./internal/tui ./internal/tui/app ./internal/tui/status ./internal/tui/chatwidget ./internal/tui/tea -count=1` 通过；`go test ./internal/config ./internal/tui ./internal/app -run "TestLoadRequirementsFile|TestDebugConfig|TestInteractiveDebugConfigReaderUsesRustStyleRenderer|TestAppServerRequirementsFromFileParsesFullRequirements|TestAppServerRemoteControl|TestAppServerListenOffRequirementsError" -count=1 -v` 通过。
- [x] 对齐 Rust `tui/src/clipboard_paste.rs` 纯逻辑：Go `internal/tui` 新增 paste image error kind/display、WSL 环境判定、Windows 路径到 `/mnt/<drive>` 转换、shell quote/escape 清理和粘贴路径规范化接口。
- [x] 对齐 Rust `tui/src/hooks_rpc.rs` 与 `tui/src/startup_hooks_review.rs` 纯状态层：Go 新增 `HookTrustUpdate`、`HooksListEntryForCWD`、`HookNeedsReview`、`BuildHookTrustWriteParams`，以及启动 hook 审查 outcome、review-needed 判定、trust-all 更新列表、三项选择器、trusting/error 状态和选中行色条渲染。
- [x] 新增回归：`TestPasteImageErrorMessagesMatchRust`、`TestNormalizePastedPathWindowsWSLMatchesRust`、`TestConvertWindowsPathToWSLMatchesRust`、`TestHookNeedsReviewMatchesRust`、`TestHooksListEntryForCWDMatchesRust`、`TestBuildHookTrustWriteParamsMatchesRust`、`TestStartupHooksReview*`。
- [x] 验证：`go test ./internal/tui -run "Test.*Clipboard|Test.*Pasted|TestPasteImage|TestConvertWindowsPath|TestIsProbablyWSL" -count=1 -v` 通过；`go test ./internal/tui -run "TestStartupHooks|TestHookNeedsReview|TestHooksListEntryForCWD|TestBuildHookTrust" -count=1 -v` 通过；`go test ./internal/config ./internal/tui ./internal/tui/app ./internal/tui/status ./internal/tui/chatwidget ./internal/tui/tea ./internal/app -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/app_link_view.rs` 纯状态与快照文案：Go `bottom_pane.AppLinkView` 从 `Ready()` 占位扩展为 URL elicitation 参数构造、安全 URL 校验、Codex Apps auth meta 解析、Install/Enable/Auth/ExternalAction 四类 suggestion、Link/InstallConfirmation 双屏、OpenURL/RefreshConnectors/SetAppEnabled/ResolveElicitation 事件、dismiss matching request、terminal-title action 判定、键盘选择和选中行色条。
- [x] 新增回归：`TestAppLinkCodexAppsAuthURLRequestMatchesRust`、`TestAppLinkGenericURLRequestMatchesRust`、`TestAppLinkURLRequestRejectsUntrustedURLs`、`TestAppLinkRowsInstallEnableAuthAndGenericMatchRust`、`TestAppLinkGenericURLResolvesWithoutConnectorRefresh`、`TestAppLinkInstallToolSuggestionResolvesAfterConfirmation`、`TestAppLinkDeclineAndEnableToolSuggestionMatchRust`、`TestAppLinkLocalToggleAndSelectionKeysMatchRust`、`TestAppLinkDismissAndTerminalTitleActionMatchRust`、`TestAppLinkConfirmationRowsMatchRust`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestAppLink" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/approval_overlay.rs` 纯状态核心：Go `bottom_pane.ApprovalOverlay` 从标题/消息/选项占位扩展为 Exec/Permissions/ApplyPatch/MCP elicitation 请求模型、可用 decision 到 Rust 文案的 option 构建、permission rule 格式化、network approval prompt、队列推进、快捷键选择、cancel 语义、cross-thread open 事件、resolved request dismiss 和选中行色条。
- [x] 新增回归：`TestExecApprovalOptionsMatchRustLabels`、`TestPermissionsPatchAndElicitationOptionsMatchRustLabels`、`TestFormatApprovalPermissionsRuleMatchesRust`、`TestApprovalOverlayRowsMatchRustSnapshots`、`TestApprovalOverlayNetworkPromptHidesCommandAndUsesNetworkLabels`、`TestApprovalOverlayShortcutsEmitDecisionsAndAdvanceQueue`、`TestApprovalOverlayCancelAndElicitationSemanticsMatchRust`、`TestApprovalOverlayOpenThreadAndDismissMatchRust`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "Test.*Approval" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1 -v` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget ./internal/app -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/custom_prompt_view.rs` 与测试文件的 paste-burst 提交语义：Go `CustomPromptView` 从文本占位扩展为标题/占位/context、文本插入、paste 处理、Esc cancel、Enter submit、`PasteBurst.DirectInsertNewlineShouldInsert` 换行抑制、提交记录、完成状态和简易 rows。
- [x] 新增回归：`TestCustomPromptPasteBurstNewlineDoesNotSubmitShortFirstLine`、`TestCustomPromptPasteBurstNewlineAfterTabDoesNotSubmit`、`TestCustomPromptDelayedEnterAfterTypingSubmits`、`TestCustomPromptEmptySubmitCancelPasteAndRows`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestCustomPrompt" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget ./internal/app -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/action_required_title.rs` 纯拼接逻辑：Go `bottom_pane` 保留 `ActionRequiredTitle` 兼容结构，同时新增 `ActionRequiredPreviewPrefix`、`BuildActionRequiredTitleText`、`BuildActionRequiredTitleTextFromValues`，覆盖 prefix 起始、跳过 spinner/activity、excluded items 过滤、空值省略和 ` | ` join 语义。
- [x] 新增回归：`TestBuildActionRequiredTitleTextMatchesRust`、`TestBuildActionRequiredTitleTextSkipsSpinnerMissingAndEmptyValues`、`TestActionRequiredTitleTextCompatibility`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestActionRequired|TestBuildActionRequired" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/slash_commands.rs` 与 `command_popup.rs` 状态核心：Go `bottom_pane` 新增 `ServiceTierCommand`、`SlashCommandItem`、`BuiltinCommandFlags`、`BuiltinsForInput`、`CommandsForInput`、`FindBuiltinCommand`、`FindSlashCommand`、`HasSlashCommandPrefix`，并将 `CommandPopup` 从字符串过滤扩展为 Rust 顺序 catalog、feature gating、service tier 插入、alias 默认隐藏但 prefix 可见、filter reset、scroll selection、selected row 色条和 rows 渲染。
- [x] 新增回归：`TestCommandPopupFiltersExactPrefixAndKeepsRustOrder`、`TestCommandPopupServiceTierCommandUsesCatalogNameAndDescription`、`TestCommandPopupAliasesHiddenByDefaultButShownForPrefix`、`TestCommandPopupFlagsMatchRustVisibility`、`TestCommandPopupRowsUseSelectedColorBarAndScroll`、`TestSlashCommandHelpersMatchRustGatingAndAliases`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestCommandPopup|TestSlashCommand" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/prompt_args.rs`、`selection_tabs.rs`、`unified_exec_footer.rs` 小型共享逻辑：Go 新增 `ParseSlashName` 的 name/rest/rest byte offset 解析、tab bar active bracket/2-cell gap/窄宽换行/height 计算、`UnifiedExecFooter` process diff、单复数 summary 文案、行截断、`UnifiedExecFooterState` 兼容 summary。
- [x] 新增回归：`TestParseSlashNameMatchesRust`、`TestTabBarLinesWrapAndMarkActiveLikeRust`、`TestTabBarEmptyAndNarrow`、`TestUnifiedExecFooterSummaryMatchesRustGrammar`、`TestUnifiedExecFooterRenderLinesAndStateCompatibility`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestParseSlashName|TestTabBar|TestUnifiedExecFooter" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/file_search_popup.rs` 与 `skills_toggle_view.rs` 状态核心：Go 新增 `FileSearchPopup` 的 pending/display query 分离、waiting/empty prompt、stale result 丢弃、第一页 match cap、选择/滚动/高度/rows 色条；`SkillsToggleView` 从 Enabled/Disabled 壳扩展为 item/search/fuzzy fallback/排序/selection/toggle/close reload event/rows 色条状态机，并补 `TruncateSkillName`/`MatchSkill` helper。
- [x] 新增回归：`TestFileSearchPopupMatchesRustStateFlow`、`TestFileSearchPopupNavigationRowsAndSelectionColorBar`、`TestSkillsToggleViewFiltersSortsAndRowsMatchRust`、`TestSkillsToggleViewToggleNavigationCloseAndSearchKeys`、`TestSkillsToggleHelpersMatchRust`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestFileSearchPopup" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -run "TestSkillsToggle" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/memories_settings_view.rs` 纯状态核心：Go `MemoriesSettingsView` 从单 bool 扩展为 Use/Generate 双设置、reset confirmation 子状态、active scroll state、空格 toggle、Enter 保存/选择、Esc/Ctrl-C 取消/回退、UpdateMemorySettings/ResetMemories 事件、docs link 和 rows 色条渲染。
- [x] 新增回归：`TestMemoriesSettingsToggleAndSaveMatchesRust`、`TestMemoriesSettingsResetConfirmationMatchesRust`、`TestMemoriesSettingsCancelCompletesOutsideReset`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestMemoriesSettings" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/hooks_browser_view.rs` 业务状态层：Go `HooksBrowserView` 从行结构扩展为 appserver `HookListEntry` 驱动的 Events/Handlers 双页、display_order 排序、event installed/active/review 计数、review-needed 默认选中、managed/review-needed 禁止 toggle、单 hook trust、trust-all update 列表、Esc 返回原 event、footer hint、source/trust/event label helper 和 rows 色条。
- [x] 新增回归：`TestHooksBrowserEventCountsAndDefaultSelectionMatchRust`、`TestHooksBrowserRowsOpenReturnAndSelectionColorBar`、`TestHooksBrowserToggleTrustAndTrustAllMatchRust`、`TestHooksBrowserManagedAndReviewNeededHandlersDoNotToggle`、`TestHooksBrowserHelpersMatchRustLabels`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestHooksBrowser" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget ./internal/appserver -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/status_surface_preview.rs`、`status_line_setup.rs`、`title_setup.rs` 数据层：Go `bottom_pane` 新增 status preview item/placeholder/live/suppress/rate-limit copy/status-line preview，补齐 StatusLine item id/legacy alias/description/preview item 映射，以及 TerminalTitle item id/legacy alias/all-or-nothing parse/spinner action-required preview/普通 title separator preview。
- [x] 新增回归：`TestStatusSurfacePreviewDataMatchesRustPlaceholdersAndRateLimitCopy`、`TestStatusLineSetupParsingAndPreviewMatchesRust`、`TestTerminalTitleParsingAndPreviewMatchesRust`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestStatusSurface|TestStatusLineSetup|TestTerminalTitle" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/skill_popup.rs` 与 `experimental_features_view.rs` 纯状态：Go 新增 `MentionItem`/`SkillPopup` 的 query、mention catalog、display/search_terms fuzzy 排序、sort_rank、滚动选择、selected mention、height、hint 和 rows 色条；`ExperimentalFeaturesView` 从行结构扩展为 feature items、toggle、导航、save-on-close updates、空列表文案和 rows 色条。
- [x] 新增回归：`TestSkillPopupFilteringSortingAndSelectionMatchRust`、`TestSkillPopupRowsScrollAndHeightMatchRust`、`TestExperimentalFeaturesViewToggleSaveRowsMatchRust`、`TestExperimentalFeaturesEmptyAndNavigation`，并复跑 `TestSkillsToggle*` 验证共享 fuzzy helper。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestSkillPopup|TestExperimentalFeatures|TestSkillsToggle" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/textarea.rs` 基础编辑状态子集：Go `TextAreaState` 从 Text/Cursor 壳扩展为 UTF-8 边界安全的 insert/replace/delete、cursor movement、line begin/end、backward-word deletion、Ctrl-K/Ctrl-U kill buffer、yank、wrapped height、cursor viewport scroll 和 string-key handler。
- [x] 新增回归：`TestTextAreaStateEditsClampToUTF8Boundaries`、`TestTextAreaStateKillYankAndWordDeleteMatchRustCoreBehavior`、`TestTextAreaStateWrapHeightCursorAndHandleKeys`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestTextAreaState" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/pending_thread_approvals.rs` 纯状态/渲染子集：Go 新增 `PendingThreadApprovals` 的 thread 列表、变更检测、兼容 `PendingThreadApproval` 输入、最多三条 pending approval 行、窄宽度空渲染、adaptive wrap 和 `/agent to switch threads` 提示。
- [x] 新增回归：`TestPendingThreadApprovalsEmptyAndSetThreadsMatchRust`、`TestPendingThreadApprovalsRowsMatchRustShape`、`TestPendingThreadApprovalsWrapAndApprovalCompatibility`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestPendingThreadApprovals" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/feedback_view.rs` 纯状态/文案层：Go `FeedbackChoice` 补齐 safety_check，新增 `FeedbackAudience`、`FeedbackNoteView`、trimmed submit/cancel/paste/key handling、title/placeholder/classification、feedback category/disabled params、upload consent header/items、connectivity diagnostics 展示规则、internal/external issue URL 和 success rows。
- [x] 新增回归：`TestFeedbackTitlePlaceholderAndClassificationMatchRust`、`TestFeedbackNoteViewSubmitCancelPasteAndRowsMatchRustCore`、`TestFeedbackCategoryAndDisabledParamsMatchRust`、`TestFeedbackUploadConsentRowsDiagnosticsAndItemsMatchRust`、`TestFeedbackConnectivityDetailsHiddenForGoodResult`、`TestFeedbackIssueURLAndSuccessRowsMatchRust`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestFeedback" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget ./internal/appserver -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/selection_popup_common.rs` 与 `list_selection_view.rs` 纯状态/渲染核心：Go 新增 `GenericDisplayRow`、`ColumnWidthMode/Config`、menu surface inset、wrapped/single-line row 渲染、disabled reason/current/default/tag/toggle/shortcut 文案、选中色条；`ListSelectionView` 从数据壳扩展为 filter/index mapping、current/default initial selection、disabled navigation skip、page/jump、toggle、accept/cancel、search query、rows/height。
- [x] 新增回归：`TestGenericDisplayRowsColumnModesSelectedAndDisabled`、`TestListSelectionInitialSelectionNavigationAndDisabledMatchRust`、`TestListSelectionSearchToggleAcceptCancelAndRowsMatchRustCore`、`TestListSelectionRowsWrapAndNoMatches`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestGenericDisplay|TestListSelection" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/multi_select_picker.rs` 纯状态核心：Go `MultiSelectItem` 从 ID/Selected 扩展为 name/description/enabled/orderable/section break，新增 `MultiSelectPicker` 的 fuzzy search、filtered index、scroll selection、toggle、selected IDs、confirm/cancel、left/right reordering、preview、single-line rows、section divider 和选中色条；保留 `ToggleMultiSelect` 兼容 helper。
- [x] 新增回归：`TestToggleMultiSelectCompatibility`、`TestMultiSelectPickerSearchToggleConfirmCancelRowsMatchRustCore`、`TestMultiSelectPickerOrderingAndSectionBreakMatchRustCore`、`TestMultiSelectPickerNavigationPreviewNoMatchesAndTruncation`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestMultiSelect|TestToggleMultiSelect" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/chat_composer_history.rs` 本地历史/搜索核心：Go 新增 `HistoryEntry` draft metadata、mention/paste 占位结构、`ChatComposerHistory` 的本地 submission 去重、clone-safe entries、Up/Down shell-style navigation、recalled text boundary gate、reset、Ctrl-R search unique match cache、older/newer boundary 语义；保留 `ComposerHistoryEntries`/`PushComposerHistory` 兼容 helper。
- [x] 新增回归：`TestChatComposerHistoryRecordDedupAndClone`、`TestChatComposerHistoryNavigationMatchesRustBoundaries`、`TestChatComposerHistorySearchUniqueMatchesAndBoundaries`、`TestChatComposerHistoryCompatibilityHelpers`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestChatComposerHistory" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/footer.rs` 纯渲染决策核心：Go `FooterState` 扩展为 `FooterMode`、`FooterProps`、`FooterKeyHints`、collaboration/goal indicator、mode toggle/esc/reset、status-line + active-agent context 拼接、shortcut overlay、queue/draft/quit/esc/history search rows、footer height、single-line footer collapse 和窄宽 truncation。
- [x] 新增回归：`TestFooterModeTransitionsMatchRust`、`TestFooterLinesStatusAgentAndShortcutOverlayMatchRustCore`、`TestFooterDraftQueueEscQuitAndHeight`、`TestSingleLineFooterLayoutCollapseAndRender`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestFooter|TestSingleLineFooter" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/bottom_pane_view.rs` 默认接口语义：Go `BottomPaneViewState` 从 visible 壳扩展为 completion/cancellation、dismiss-after-child-accept、stable view id、selected index、active tab、Esc routing、interrupt flag、paste handling/paste burst flush、pre-draw tick、resolved app-server request dismiss、terminal title action 和 next-frame delay。
- [x] 新增回归：`TestBottomPaneViewStateCompletionAndCancellationMatchRustDefaults`、`TestBottomPaneViewStateMetadataPasteAndFrameHooks`、`TestBottomPaneViewStateDismissAppServerRequest`。
- [x] 验证：`go test ./internal/tui/bottom_pane -run "TestBottomPaneViewState" -count=1 -v` 通过；`go test ./internal/tui/bottom_pane -count=1` 通过；`go test ./internal/tui/bottom_pane ./internal/tui ./internal/tui/tea ./internal/tui/chatwidget -count=1` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/chat_composer/{draft_state,attachment_state,popup_state,footer_state}.rs` 纯状态核心：Go 子包 `chatcomposer` 补齐 draft 输入启停、pending paste、mention binding、元素 payload 替换、local/remote image placeholder `[Image #N]`、remote image selection/delete/relabel/prune/take、popup active kind 与 dismissed token、footer flash/quit reminder/status/context/key state。
- [x] 新增回归：`TestDraftStateInputPastesAndMentionsMatchRustCore`、`TestAttachmentStateLocalRemoteRelabelAndTakeMatchRustCore`、`TestAttachmentStateRemoteSelectionDeleteAndLinesMatchRustCore`、`TestPopupStateLifecycleDismissTokensMatchRustCore`、`TestFooterStateFlashContextAndQuitReminderMatchRustCore`。
- [x] 验证：`go test ./internal/tui/bottom_pane/chat_composer -count=1 -v` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/chat_composer/{slash_input,history_search}.rs` 纯状态核心：Go 子包 `chatcomposer` 补齐 slash submission validation、bare/inline command、dequeue action、command element range、editing detection、popup filter/completion、args text element 重映射，以及 reverse-i-search session 的 original draft snapshot、query/status、newest-first unique match、older/newer boundary、accept/cancel、footer line、cursor column 和 highlight ranges。
- [x] 新增回归：`TestSlashInputSubmissionBareInlineAndDequeueMatchRustCore`、`TestSlashInputCommandElementEditingPopupAndCompletionMatchRustCore`、`TestSlashInputPreparedArgsQueuedActionAndElementsMatchRustCore`、`TestHistorySearchSessionQueryNavigationAndHighlightsMatchRustCore`、`TestHistorySearchSessionCancelAcceptNoMatchAndQueryEditingMatchRustCore`、`TestHistorySearchFooterCursorCompatibilityAndCaseInsensitiveRangesMatchRustCore`。
- [x] 验证：`go test ./internal/tui/bottom_pane/chat_composer -run "TestHistorySearch|TestSlashInput" -count=1 -v` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/mentions_v2` 状态/渲染核心：Go `mentionsv2` 补齐 `Selection`、`MentionType`、`Candidate/SearchResult`、Results/FilesystemOnly/Tools 搜索模式循环、skill/plugin catalog 构建、plugin mention name 分段/title-case、capability description、fuzzy direct/search-term fallback、file match 注入与 stale/cap 处理、popup selection/scroll/search-mode、footer indicator 和 selected row 色条渲染。
- [x] 新增回归：`TestSearchModeCycleLabelsAndAcceptsMatchRust`、`TestSearchCatalogPluginAndSkillCandidatesMatchRust`、`TestFilteredCandidatesSortFuzzyTermsAndFileMatchesMatchRust`、`TestPopupFileSearchSelectionModesAndRenderingMatchRustCore`。
- [x] 验证：`go test ./internal/tui/bottom_pane/mentions_v2 -count=1 -v` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/request_user_input/{layout,render}.rs` 纯 helper：Go 子包 `requestuserinput` 补齐 layout sections/progress/question/options/notes/footer height 分配、options notes hidden/visible 策略、tight no-options 截断、desired height、bottom-aligned rows、footer tip wrapping、word-boundary truncation 和 RenderUI section placement。
- [x] 新增回归：`TestLayoutSectionsWithOptionsNotesHiddenMatchRustCore`、`TestLayoutSectionsWithoutOptionsTightAndNormalMatchRustCore`、`TestRenderHelpersWrapBottomAlignFooterAndTruncateMatchRustCore`、`TestRenderUIPlacesSectionsAndFooter`。
- [x] 验证：`go test ./internal/tui/bottom_pane/request_user_input -count=1 -v` 通过。
- [x] 对齐 Rust `tui/src/bottom_pane/textarea/vim.rs` text object 核心：Go 子包 `textarea` 补齐 Vim mode/operator/pending/motion/text-object 类型、TextArea cursor/element range、small word/big word、inner/around word 扩展、paired bracket/brace/parentheses、quoted/backtick object、escaped quote 和 skip placeholder element 逻辑。
- [x] 新增回归：`TestVimWordTextObjectsMatchRustCore`、`TestVimPairedAndQuotedTextObjectsMatchRustCore`、`TestVimTextObjectsSkipElementsAndClampCursor`。
- [x] 验证：`go test ./internal/tui/bottom_pane/textarea -count=1 -v` 通过。
- [x] 对齐 Rust `tui/src/tui/{event_stream,frame_rate_limiter,frame_requester,keyboard_modes,job_control,terminal_stderr}.rs` 纯状态核心：Go 子包 `internal/tui/tui` 补齐 event broker pause/resume/start/running、terminal event 到 TUI event 映射、draw/input round-robin、120 FPS deadline clamp、frame request coalescing、keyboard enhancement env/WSL+VSCode/tmux 判断和 ANSI reset/modifyOtherKeys 序列、suspend resume intent、terminal stderr guard pause/resume/finish 状态。
- [x] 新增回归：`TestFrameRateLimiterAndRequesterMatchRustCore`、`TestEventBrokerStreamMappingPauseResumeAndFairness`、`TestKeyboardModeDetectionAndAnsiMatchRust`、`TestSuspendContextAndTerminalStderrStateMatchRustCore`。
- [x] 验证：`go test ./internal/tui/tui -count=1 -v` 通过。
- [x] 对齐 Rust `tui/src/notifications/{osc9,bel}.rs` 命令层：Go `internal/tui/notifications` 新增 OSC9/BEL backend 与 post notification，覆盖 plain OSC9、tmux DCS passthrough、ESC payload 翻倍、BEL ANSI 输出，并保留旧兼容 helper。
- [x] 对齐 Rust `tui/src/ide_context/{prompt,ipc,windows_pipe}.rs` 核心协议/提示词层：Go `internal/tui/ide_context` 新增 `IdeContext/ActiveFile/FileDescriptor/Range/Position` 模型、IDE prompt 渲染、`## My request for Codex:` delimiter 注入与反抽取、text element byte range 偏移、4 字节小端 IPC frame、request/response loop、unsupported request/discovery response、error hint、Windows pipe 默认路径/timeout/owner 校验壳。
- [x] 对齐 Rust `tui/src/pets/sixel.rs` 编码器：Go `internal/tui/pets` 新增 RGB332 palette、透明像素省略、6px band、run-length encoding、buffer length 校验和扩展 sixel terminal 判断。
- [x] 继续对齐 Rust `tui/src/chatwidget/slash_dispatch.rs` 与 `tui/src/slash_command.rs` 决策层：Go `internal/tui/chatwidget` 新增 queued slash prompt dispatch、inline args 支持表、side/task availability 表、queued drain result、args text element 重映射、service-tier/unknown/submit 分支；同步 `bottom_pane/slash_commands.go` 的 inline/task 能力表，避免 composer 与 dispatch 分歧。
- [x] 新增回归：`TestOSC9PostNotificationWritesPlainSequenceMatchRust`、`TestOSC9PostNotificationWritesTmuxWrappedSequenceMatchRust`、`TestNotificationBackendsAndCompatibilityHelpers`、`TestRenderPromptContextMatchesAppFormatRust`、`TestApplyIDEContextUsesPromptDelimiterAndRebasesElementsMatchRust`、`TestReadResponseFrameHandlesInterleavedMessagesMatchRust`、`TestWindowsPipeConfigTimeoutAndAvailabilityMatchRustCore`、`TestEncodeRGBARedPixelPaletteAndDataMatchRust`、`TestEncodeRGBAMultiBandAdvancesMatchRust`、`TestQueuedSlashPromptDispatchesInlineArgsAndRebasesElementsMatchRust`、`TestSlashCommandCapabilityTablesMatchRust`。
- [x] 验证：`go test ./internal/tui/notifications -count=1 -v` 通过；`go test ./internal/tui/ide_context -count=1 -v` 通过；`go test ./internal/tui/pets -count=1 -v` 通过；`go test ./internal/tui/chatwidget ./internal/tui/bottom_pane ./internal/tui/bottom_pane/chat_composer -count=1 -v` 通过。
- [x] 对齐 Rust `tui/src/chatwidget/streaming.rs` 与 `tui/src/streaming/controller.rs` 的 ChatWidget 封装层：Go `ChatStreamingState` 从计数壳扩展为 answer/plan live tail、status restore、reasoning bold header、plan item finalize、stream finished、commit/catch-up tick、active tail 清理、usage insertion request 和 history cell 落盘的纯状态核心。
- [x] 对齐 Rust `tui/src/chatwidget/reasoning_shortcuts.rs`：Go 新增 advertised reasoning choices、raise/lower 快捷键、unsupported current effort anchor、plan mode 禁用、reasoning-bound model 提示和用户可见文案。
- [x] 对齐 Rust `tui/src/chatwidget/session_flow.rs`：Go `SessionFlowState` 扩展为 session configured display、quiet/side conversation header 策略、thread change reset、initial message drain、fork event、thread name update、rename/fork 文案和 redraw/prefetch flags。
- [x] 对齐 Rust `tui/src/chatwidget/turn_runtime.rs`：Go `TurnRuntimeState` 新增 task running 派生、task start/complete/finalize 收尾、runtime metrics merge/final separator、plan implementation prompt gate、context used label、queued follow-up/active goal notification 抑制、非重试错误分类、safety access block JSON 识别、rate-limit reached 类型转换、workspace owner nudge 和 interrupted turn 文案。
- [x] 对齐 Rust `tui/src/chatwidget/connectors.rs`：Go `ConnectorsState` 从能力 bool 壳扩展为 Uninitialized/Loading/Ready/Failed cache、partial snapshot、prefetch in-flight、pending force-refetch、mentions snapshot 选择、`/apps` output reducer、load success/failure fallback、enabled-state preserve、selected connector index 和 Rust 文案的 Apps catalog view。
- [x] 对齐 Rust `tui/src/chatwidget/rendering.rs` 组合决策层：Go `ChatWidgetRenderState` 新增 active transcript/hook、pending token activity、pending rate-limit hint、bottom pane reserve、ambient pet right reserve、section flex/top inset、last rendered width、transcript tail scroll offset 和 visible rows helper。
- [x] 对齐 Rust `tui/src/chatwidget/constructor.rs` 初始 wiring 纯模型：Go `ChatWidgetSnapshot` 新增 default/loading model header、deterministic Rust placeholder catalog、side placeholder、default collaboration mode、placeholder session header、welcome banner、connectors/token activity/prevent-idle-sleep 派生、runtime streaming 默认和 config clone。
- [x] 对齐 Rust `tui/src/chatwidget/transcript.rs` 纯状态核心：Go `TranscriptState` 新增 active cell revision、agent copy history ring、visible user turn count、rollback eviction marker、copy-source turn flag、latest proposed plan、final separator/work/plan flags、last plan progress、plan delta buffer 和 plan item active 状态，并补齐 record/truncate/reset/progress helper。
- [x] 对齐 Rust `tui/src/chatwidget/permission_popups.rs` 弹窗状态子集：Go 新增 auto-review denials popup outcome/view、denial approve/take 状态、full-access confirmation header/warning，并新增通用 `SelectionViewRows`/`PermissionMenuViewRows` helper，确保 chatwidget 选择类视图可验证选中色条。
- [x] 对齐 Rust `tui/src/chatwidget/interaction.rs` key routing 纯决策层：Go 新增 Ctrl+C/Ctrl+D 双击退出状态机、modal/active bottom-pane bypass、copy binding、queued composer restore、review steer warning、pending steer interrupt、plan mode nudge dismiss、plugin popup/collaboration mode branch、Ctrl+Alt+V image paste 和 image-input model gate。
- [x] 对齐 Rust `tui/src/chatwidget/command_lifecycle.rs` 纯状态核心：Go `CommandLifecycleState` 补齐 unified exec wait streak flush、terminal interaction wait/input 分支、shell command display stripping、recent output line ring、duplicate unified wait suppression、running command completion 和 user-shell follow-up trigger 标记。
- [x] 对齐 Rust `tui/src/chatwidget/tool_requests.rs` 纯 reducer 子集：Go `ToolRequestRuntimeState` 新增 guardian assessment in-progress 聚合、terminal approved/denied/timed-out 历史摘要、recent auto-review denial 入队、request-user-input notification title，以及 exec/MCP/permissions request resolved metadata 映射。
- [x] 对齐 Rust `tui/src/chatwidget/protocol_requests.rs` 通知/request 分发边界：Go 新增 guardian review started/completed、shutdown complete、turn diff、deprecation notice 的纯 route decision，补 replay suppression 和 request id trim/legacy stub 语义。
- [x] 对齐 Rust `tui/src/chatwidget/safety_buffering.rs` 上下文 gate 与 retry 状态：Go 新增 resume replay/非当前 turn 忽略、无 thread/retry 时 dismiss prompt、turn start reset、prepare retry 和 fail retry 的 input queue/cancel edit 回滚 helper。
- [x] 对齐 Rust `tui/src/chatwidget/plugin_catalog.rs` tab/fallback 核心：Go 新增 marketplace tab 模型、remote workspace/shared loading/empty/error fallback tabs、tab order、saved tab fallback 到 remote section、chrome tabs 和 disabled fallback item 文案。
- [x] 对齐 Rust `tui/src/chatwidget/input_restore.rs` 中断恢复核心：Go 新增 pending/rejected/queued/composer 合并恢复、history override 应用、paste placeholder 冲突重映射、pending steer interrupt 后立即提交、cancel-edit prompt 回传、notice mode 和 pending preview 刷新。
- [x] 对齐 Rust `tui/src/chatwidget/input_submission.rs` 提交决策核心：Go 新增 session 未配置 push-front 队列语义、模型不可用 restore 信号、skill/plugin/app mention catalog 输入项生成、agent-turn pending steer compare key、UserTurnPendingStart 前台标记和结构化 mention 顺序。
- [x] 对齐 Rust `tui/src/chatwidget/interrupts.rs` 队列管理：Go 修正 exec approval effective approval id 匹配、MCP elicitation server/request 双键匹配、lifecycle item 不参与 resolved prompt 删除，并补齐 push helpers 与 FIFO flush。
- [x] 补齐 Rust `tui/src/chatwidget/session_flow.rs` 首条消息 gate：Go `SessionFlowState` 新增 suppress initial submit 与 elevated Windows sandbox setup defer，支持配置完成后继续保留 initial prompt、gate 解除后手动提交。
- [x] 对齐 Rust `tui/src/chatwidget/hook_lifecycle.rs` active hook 纯状态：Go 新增 active hook cell visible/revision、completed persistent run flush、final separator/usage insertion request、start/complete/clear/idle finish 副作用结果。
- [x] 对齐 Rust `tui/src/chatwidget/replay.rs` item route 分类层：Go 新增 `ReplayThreadSnapshot`、ThreadItem kind/route 枚举、reasoning replay raw/summary/live finalize 分支、command/patch/MCP in-progress 分流、review/context/hook/dynamic/sleep replay 行为和 snapshot 空 turn redraw。
- [x] 新增回归：`TestChatStreamingAnswerTailCommitFinalizeMatchesRustCore`、`TestChatStreamingPlanDeltaCompletionAndStatusRestoreMatchesRustCore`、`TestChatStreamingReasoningHeaderSectionAndFinalMatchesRustCore`、`TestNextReasoningEffortMatchesRustAdvertisedOrder`、`TestReasoningChoicesAndCurrentAnchor`、`TestDecideReasoningShortcutMatchesRustStateGates`、`TestSessionFlowConfigureNormalHeaderInitialMessageAndFork`、`TestSessionFlowQuietAndSideHeaderDisplay`、`TestTurnRuntimeTaskStartedMatchesRustStateReset`、`TestTurnRuntimeTaskCompleteFollowUpSeparatorAndNotificationMatchRust`、`TestTurnRuntimePlanImplementationPromptGateAndContextLabel`、`TestTurnRuntimeHandleNonRetryErrorClassifiesRustBranches`、`TestConnectorsBeginRefreshMatchesRustInFlightForceRefetch`、`TestConnectorsAddOutputUsesCacheAndQueuesFetchLikeRust`、`TestConnectorsLoadedPartialFinalPreservesEnabledAndSelection`、`TestConnectorsLoadedFailureFallsBackLikeRust`、`TestConnectorsUpdateEnabledAndCatalogTextMatchRust`、`TestChatWidgetRenderPlanMatchesRustSectionOrderAndReserve`、`TestTranscriptAreaScrollAndVisibleRowsMatchRustTailBehavior`、`TestNewChatWidgetSnapshotInitializesRustDefaults`、`TestChatWidgetSnapshotClonesConfig`、`TestTranscriptStateActiveCellRevisionWraps`、`TestTranscriptStateCopyHistoryTracksLatestVisibleTurnAndRollback`、`TestTranscriptStateCopyHistoryCapsAndEviction`、`TestTranscriptStateResetTurnFlagsPreservesSeparatorAndPlanProgress`、`TestTranscriptStateRecordPlanProgressClampAndClear`、`TestAutoReviewDenialsPopupOutcomesRowsAndSelectionMatchRust`、`TestAutoReviewDenialEntriesFromSummariesAndApproveMatchRust`、`TestFullAccessConfirmationHeaderAndSelectedRowsMatchRust`、`TestInteractionCtrlCQuitShortcutAndInterruptMatchRust`、`TestInteractionCtrlDComposerGateMatchesRust`、`TestInteractionPendingSteersAndReviewWarningMatchRust`、`TestInteractionRoutingBranchesMatchRustOrder`、`TestInteractionImageAttachGateMatchesRust`、`TestCommandLifecycleUnifiedExecProcessDisplayChunksAndFooterMatchRust`、`TestCommandLifecycleTerminalInteractionWaitStreakMatchesRust`、`TestCommandLifecycleTerminalInteractionSwitchFlushesWaitMatchRust`、`TestCommandLifecycleDuplicateUnifiedWaitAndCompletionMatchRust`、`TestCommandLifecycleCommandDisplayParsingMatchesRustCore`、`TestToolRequestToInterruptCarriesResolvedMetadataMatchRust`、`TestGuardianAssessmentInProgressAggregatesStatusMatchRust`、`TestGuardianAssessmentTerminalMessagesAndDenialsMatchRust`、`TestGuardianActionSummaryAndRequestUserInputTitleMatchRust`、`TestProtocolNotificationRoutesGuardianShutdownDiffAndDeprecationMatchRust`、`TestProtocolNotificationReplaySuppressionAndRequestTrimMatchRust`、`TestSafetyBufferingContextGatesAndDismissPromptMatchRust`、`TestSafetyBufferingResetPrepareAndFailRetryMatchRust`、`TestPluginCatalogTabsRemoteFallbacksMatchRust`、`TestPluginCatalogTabsSkipFallbackWhenMarketplacePresentAndSavedIDFallsBack`、`TestPluginCatalogTabsOrderAndChromeTabsMatchRust`。
- [x] 新增回归：`TestInputRestoreDrainPendingMessagesOrderAndHistoryMatchRust`、`TestInputRestoreDrainPendingMessagesRemapsCollidingPastesMatchRust`、`TestInputRestoreInterruptedTurnSubmitsPendingSteersImmediatelyMatchRust`、`TestInputRestoreInterruptedTurnRestoresPendingQueuedAndComposerMatchRust`、`TestInputRestoreInterruptedTurnReturnsArmedCancelPromptAndSuppressesNoticeMatchRust`、`TestInputSubmissionMentionItemsOrderMatchesRust`、`TestInputSubmissionUnavailableModelRestoresComposerMatchRust`、`TestInputSubmissionAgentTurnCreatesPendingSteerCompareKeyMatchRust`、`TestInputSubmissionQueueBeforeConfiguredPushesFrontMatchRust`、`TestInterruptManagerResolvedPromptMatchingMatchesRust`、`TestInterruptManagerElicitationAndLifecycleMatchingMatchesRust`、`TestInterruptManagerFlushAllPreservesFIFOOrderMatchRust`、`TestSessionFlowInitialMessageSuppressionAndSandboxGateMatchRust`、`TestHookLifecycleStartFlushesCompletedAndStartsActiveMatchRust`、`TestHookLifecycleCompleteExistingFlushesAndClearsIdleMatchRust`、`TestHookLifecycleClearActiveHookCellDropsTransientStateMatchRust`、`TestReplayThreadItemRoutesMatchRustCore`、`TestReplayReasoningAndThreadSnapshotRedrawMatchRust`、`TestReplaySuppressionKindsMatchRust`。
- [x] 验证：`go test ./internal/tui/chatwidget -run "TestInputSubmission|TestSubmissionAndInputFlow|TestInputRestore" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestInterruptManager|TestProtocol|TestToolRequest" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestSessionFlow" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestHook|TestHooks" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestReplay|TestProtocol" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -count=1` 通过。
- [x] 验证：`go test ./internal/tui/chatwidget ./internal/tui/streaming -run "TestChatStreaming|TestExtractFirstBold|TestStreamController|TestPlanStreamController|TestAdaptive|TestCommit" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestNextReasoning|TestReasoningChoices|TestDecideReasoning|TestChatStreaming|TestExtractFirstBold|TestQueuedSlash|TestSlashCommandCapability" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestSessionFlow|TestNextReasoning|TestReasoningChoices|TestDecideReasoning|TestChatStreaming" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestTurnRuntime" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestConnectors|TestApps" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestChatWidgetRender|TestTranscriptArea|TestRenderStatus" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestNewChatWidget|TestChatWidgetSnapshot|TestChatWidgetPlaceholder" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestTranscriptState|TestLastAssistantMarkdown|TestTranscriptOverlay" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestAutoReviewDenialEntries|TestAutoReviewDenials|TestFullAccessConfirmation|TestInteraction" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestCommandLifecycle" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestToolRequest|TestGuardian" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestProtocol" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestSafetyBuffering" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -run "TestPluginCatalog|TestPluginsCatalog|TestMarketplace" -count=1 -v` 通过；`go test ./internal/tui/chatwidget -count=1` 通过。
- [ ] TUI 剩余缺口：Rust `tui/src` 文件/接口边界已递归对齐；剩余转为深行为/真机类缺口：Windows ConPTY restore 支持宿主完整通过记录，以及把本轮轻量接口壳逐步替换为与 Rust 完全一致的业务实现和 golden/snapshot。

## 工作日志 2026-07-09 非 TUI 启动/exec 主链路修复

- [x] 按用户要求暂停 TUI，优先让 Go 版系统跑起来；实测 Rust `codex exec --json --ephemeral --skip-git-repo-check "请分析一下当前项目的功能，简短回答，最多列 5 点"` 可正常执行 PowerShell 工具并完成，旧 Go `fuck.exe` 会在 `exec_command` 工具输入流后进入 `agent tool loop exceeded 8 iterations`。
- [x] 对齐 Rust 默认启动上下文：Go `internal/exec` 现在为 exec turn 前置 Rust 风格 `<permissions instructions>` 和 `<environment_context>` input item，包含绝对 cwd、shell、current_date、timezone、workspace root 和权限 profile；同时 `requestCWD` 默认从空值改为 `"."`，避免 shell 工具在未显式 `--cd` 时返回 `cwd must not be empty`。
- [x] 对齐 Responses SSE 工具参数流：Go `internal/model/responses_stream.go` 现在累积 `response.function_call_arguments.delta` / `response.custom_tool_call_input.delta`，并在 `response.output_item.done` 回填到最终 `AgentItem`，修复工具调用参数只在 delta 中出现时 dispatcher 收不到完整参数的问题。
- [x] 新增/更新回归：`TestParseResponsesStreamAccumulatesFunctionCallArgumentDeltas`、`TestRunAddsStartupEnvironmentContextInputItems`，并调整 exec 结构化输入和工具循环测试以接受前置启动上下文。
- [x] 验证命令：`go test ./internal/model -run "Test(ParseResponsesStreamAccumulatesFunctionCallArgumentDeltas|ResponsesAgentRunnerStreamsResponsesSSE)" -count=1 -v` 通过；`go test ./internal/exec -count=1` 通过；`go test ./internal/model ./internal/turn ./internal/exec -count=1` 通过；`go test ./internal/app ./internal/appserver ./internal/exec ./internal/model ./internal/turn ./internal/tool -count=1` 通过。
- [x] 全量验证：`go list -buildvcs=false ./...` 通过；`go test ./... -count=1` 通过。
- [x] 真实验收：`go build -o .\fuck-dev.exe .\cmd\codex` 后运行 `.\fuck-dev.exe exec --json --ephemeral --skip-git-repo-check "请分析一下当前项目的功能，简短回答，最多列 5 点"` 已成功完成并输出 `turn.completed`，不再出现 401、HTTP `previous_response_id` 400 或 `agent tool loop exceeded 8 iterations`。
- [x] 待收尾已完成：当前已可用同一源码直接 `go build -o .\fuck.exe .\cmd\codex` 覆盖 Go 版入口；同时保留 `fuck-dev.exe` 作为本轮验证二进制。

## 工作日志 2026-07-09 TUI transcript / 工具展示对齐

- [x] 按用户反馈对比 Go `fuck-dev.exe` 与 Rust `codex-cli 0.142.5`：Go 版原先把 `thread.started`、`turn.started`、`Tool started: ...`、`Tool input streaming`、`Turn completed` 等协议事件刷到底部，视觉效果明显偏调试日志；Rust 交互入口在相同 `TERM=dumb` PTY 下有相同安全提示，但正常 Rust 首屏还包含 onboarding/登录选择，Go 版这块仍是后续缺口。
- [x] Go TUI `internal/tui/tea` 现在把工具调用 lifecycle 接到 Rust parity `exec_cell`：维护 tool call state、累积 `item.delta.input`、用 `call_id` 将流式 function_call 与后续 tool execution/output 对齐，并在 transcript 中更新同一个 history cell 为 `Running/Ran <cmd>` + 输出摘要，不再显示 `Tool input streaming`。
- [x] 协议/exec 事件补齐可选 `call_id`：`protocol.ThreadItem` 增加 `call_id`，新增 `ToolCallItemWithCallID` / `ToolOutputItemWithCallID`；`internal/exec` 的 stream item、tool call completed、tool output completed 都携带 call id，避免 Responses 流式 item id 与本地执行 item id 不一致导致 TUI 无法合并。
- [x] transcript 普通消息改用 Rust parity history cells：user/assistant 不再渲染为 `User:` / `Assistant:` 标签，而是用 bullet-style `UserHistoryCell` / `AgentMessageCell`；raw transcript 仍保留纯文本，便于复制。
- [x] 计划更新从底部日志改为 history `PlanUpdateCell`；审批请求保留弹窗，同时只设置 notice，不再追加底部协议日志。
- [x] 新增/更新回归：`TestModelStreamsToolInputIntoHistoryCell`、`TestModelAppliesThreadEvents`、raw transcript 断言、terminal snapshot / VT100 snapshot，锁住“工具输入流进入 history cell、旧事件日志不再出现”的行为。
- [x] 验证：`go test ./internal/tui/tea -count=1` 通过；`go test ./internal/protocol ./internal/exec ./internal/tui/exec_cell ./internal/tui/history_cell ./internal/tui/chatwidget -count=1` 通过；`go test ./... -count=1` 通过。
- [x] 真实验收：`go build -o .\fuck-dev.exe .\cmd\codex` 通过；运行 `.\fuck-dev.exe exec --json --ephemeral --skip-git-repo-check "请只运行一个很小的命令来验证工具展示链路，比如 pwd，然后用一句中文总结。"` 成功完成，JSON 事件中可见同一 tool call 的 `call_id`、参数 delta、最终 tool output 和 `turn.completed`。
- [x] 按用户最新 TUI 截图修复失败轮次收口：Go `internal/tui/tea` 现在在 `turn.failed`、stream error、`TurnCompletedMsg.Err` 和中断路径把仍处于 `Running <cmd>` 的 active tool history cell 更新为失败输出，显示 `Ran <cmd>` + `Error: ...`，避免网络中断后 transcript 长期残留一串 Running 工具调用。
- [x] 对齐 Rust 失败后会话复用语义：TUI model 新增本轮 `thread.started` 与成功完成 thread 的最小跟踪；首轮新建 thread 如果失败且从未成功完成，会清空 `State.ThreadID`，下一次提交重新建 thread；已有/已完成会话在普通网络错误下保留，明确 `thread not found: ...` 错误则强制清空，避免下一轮继续复用坏 thread。
- [x] 错误展示从 `System:` 调试块改为 Rust 风格 history error cell（`Error: ...`），中断展示改为 history info cell，减少 transcript 中协议/系统日志感。
- [x] 同一轮内重复错误去重：当底层同时上报 `error`、`turn.failed` 和最终 `TurnCompletedMsg.Err` 的同文本网络错误时，transcript 只落一条 history error，避免截图里同一错误重复三次。
- [x] 新增回归：`TestModelFailsRunningToolCellsAndClearsUnconfirmedThread`、`TestModelKeepsConfirmedThreadOnTransientTurnFailure`、`TestModelClearsThreadNotFoundFailures`、`TestModelDedupesRepeatedTurnErrors`，并调整中断路径断言为 history cell。
- [x] 验证：`go test ./internal/tui/tea -run "TestModel(AppliesTurnCompleted|AppliesThreadEvents|StreamsToolInputIntoHistoryCell|FailsRunningToolCellsAndClearsUnconfirmedThread|KeepsConfirmedThreadOnTransientTurnFailure|ClearsThreadNotFoundFailures|DedupesRepeatedTurnErrors|CtrlCInterruptsRunningTaskWithoutQuitting)$" -count=1 -v` 通过；`go test ./internal/tui/tea -count=1` 通过；`go test ./internal/protocol ./internal/exec ./internal/tui/tea ./internal/tui/exec_cell ./internal/tui/history_cell ./internal/tui/chatwidget -count=1` 通过；`go test ./... -count=1` 通过；`go build -o .\fuck-dev.exe .\cmd\codex` 通过。
- [ ] 后续缺口：Go 根命令帮助/interactive onboarding 与 Rust 仍不一致（Go `--help`/`exec --help` 目前返回 unknown option，Rust 无 auth 首屏是登录选择）；Rust 读取当前用户配置时报 `tui.model_availability_nux` 类型不兼容，需后续单独处理配置 schema 兼容。

## 工作日志 2026-07-09 Rust TUI 运行展示对齐补丁

- [x] 对照本地 Rust `codex-rs` 实现确认两处核心差异：Rust `update_plan` handler 会立即发送 `PlanUpdate` 事件而不是把 `update_plan {...}` 当普通 exec 命令展示；Rust sampling/tool loop 不存在 Go 默认 8 轮就终止的硬失败路径，长工具链会持续运行到最终响应或真实错误。
- [x] Go `internal/turn`：默认 `AgentLoop` 工具轮次上限从 8 提高到 64，保留显式 `MaxTurns` 的测试语义；新增 `TestAgentLoopDefaultAllowsLongToolChains` 覆盖 12 轮工具调用后正常完成，避免再次出现截图中的 `agent tool loop exceeded 8 iterations`。
- [x] Go `internal/tui/tea`：流式 `tool_call` 阶段识别 `update_plan`，在 JSON 参数完整前静默等待，完整后直接渲染 `PlanUpdateCell`；计划更新不再进入 `ExecCell`，也不会在后续 `turn.failed` 时被标成 `Ran update_plan` + 错误输出。
- [x] 新增 TUI 回归：`TestModelStreamsUpdatePlanIntoPlanCell` 锁定分片输入的计划卡片展示；`TestModelDoesNotMarkUpdatePlanFailedOnTurnFailure` 锁定失败回合下计划卡片不被工具失败渲染污染。
- [x] 验证：`go test ./internal/turn -run "TestAgentLoop(DefaultAllowsLongToolChains|StopsAtIterationLimit|RunsToolsAndContinuesSampling)" -count=1 -v` 通过；`go test ./internal/tui/tea -run "TestModel(StreamsUpdatePlanIntoPlanCell|DoesNotMarkUpdatePlanFailedOnTurnFailure|StreamsToolInputIntoHistoryCell|FailsRunningToolCellsAndClearsUnconfirmedThread)" -count=1 -v` 通过；`go test ./internal/turn ./internal/exec ./internal/tui/tea -count=1` 通过；`go test ./internal/protocol ./internal/exec ./internal/turn ./internal/tui/tea ./internal/tui/exec_cell ./internal/tui/history_cell ./internal/tui/chatwidget -count=1` 通过。
- [x] 全量验证：第一次 `go test ./... -count=1` 命中 `internal/appserver/TestRuntimeRouterSessionStartHookInjectsAdditionalContextOnce` 临时 JSONL 读取抖动；单独重跑该用例通过，第二次 `go test ./... -count=1` 全量通过。
- [x] 构建与真实运行：`go build -o .\fuck.exe .\cmd\codex` 与 `go build -o .\fuck-dev.exe .\cmd\codex` 均通过；`.\fuck.exe exec --json --ephemeral --skip-git-repo-check "请只回复 OK"` 成功输出 `turn.completed`；同机 Rust `codex exec --json --ephemeral --skip-git-repo-check "请只回复 OK"` 也成功输出 `turn.completed`。
- [x] TUI smoke：`.\fuck.exe` 在当前 `TERM=dumb` PTY 下确认继续启动后可发送 `say OK`，回合完成后状态回到 `idle`，transcript 显示 assistant bullet `OK`，未再出现 8 轮工具循环错误或重复系统错误块。

## Work Log 2026-07-09 App-Server MCP Rust Parity

- [x] Continued P0 Rust app-server v2 MCP parity from `plan_code.md`: expanded `TestProtocolPayloadsValidateAgainstRustSchemas` with Rust schema fixtures for `ListMcpServerStatusParams/Response`, `McpResourceReadParams/Response`, `McpServerToolCallParams/Response`, and `McpServerElicitationRequestParams/Response` form and URL branches.
- [x] Locked Rust MCP status naming behavior from `app-server/tests/suite/v2/mcp_server_status.rs`: added `TestMCPServerStatusPreservesRawServerAndToolNames` so raw server names (`some-server` versus `some_server`) and raw tool names (`look-up.raw`) survive the v2 JSON status map shape without sanitized-name collisions.
- [x] Locked Rust MCP tool result passthrough from `app-server/tests/suite/v2/mcp_tool.rs`: extended `TestHTTPMCPToolListCallAndResource` to assert HTTP MCP `tools/call` returns `content`, `structuredContent`, explicit `isError:false`, and `_meta`, while request `_meta.threadId` is still overwritten with the live thread id.
- [x] Verification: `$env:GOCACHE='D:\qax\reagent\dev\codex_go\.gocache'; go test ./internal/appserver -run TestProtocolPayloadsValidateAgainstRustSchemas -count=1 -v` passed; `go test ./internal/mcp -run "Test(ListStatusAndToolCall|MCPServerStatusPreservesRawServerAndToolNames|MCPServerStatusResourceWireShapeMatchesRustV2|MCPServerStatusDetailZeroValueMatchesToolsAndAuthOnly|MCPParamsMarshalRustV2Shape|MCPToolCallMetaWithThreadID)" -count=1 -v` passed; `go test ./internal/mcp -run "TestHTTPMCPToolListCallAndResource|TestMCPServerStatusPreservesRawServerAndToolNames" -count=1 -v` passed; package regressions `go test ./internal/mcp -count=1` and `go test ./internal/appserver -count=1` passed.

- [x] Continued P0 business error envelope parity: added `TestRuntimeRouterMCPRemoteErrorsIncludeRustErrorData` so app-server router responses for MCP `tools/call` and `resources/read` remote JSON-RPC errors preserve the remote error code as `error.code` and expose Rust-compatible `error.data` with `type=mcp_remote_error`, `method`, `message`, `code`, and decoded remote `data`.
- [x] Matched Rust `app-server/src/command_exec.rs` missing process control error wording: Go `command/exec/write`, `command/exec/terminate`, and `command/exec/resize` now return invalid request `-32600` with `command/exec <processId> is no longer running`, replacing the previous Go-only `no active command/exec ...` text.
- [x] Verification: `go test ./internal/appserver -run TestRuntimeRouterMCPRemoteErrorsIncludeRustErrorData -count=1 -v` passed; `go test ./internal/appserver -run "TestRuntimeRouterCommandExec|TestRuntimeRouterCommandExecInvalidRequestAndParamsCodes|TestCommandExecStreamingSessionOperations" -count=1 -v` passed; `go test ./internal/appserver -count=1` passed.

- [x] Refined command/exec control parity after re-reading Rust `CommandExecManager::send_control`: client-supplied process ids now use Rust JSON string error representation (`command/exec "missing" is no longer running`) when the session is gone, while cross-connection control attempts still return `no active command/exec for process id "..."` and do not cancel another connection's active process.
- [x] Verification: `$env:GOCACHE='D:\qax\reagent\dev\codex_go\.gocache'; go test ./internal/appserver -run "TestCommandExecSessionsAreConnectionScoped|TestCommandExecConnectionClosedCancelsOnlyThatConnection|TestRuntimeRouterCommandExec|TestRuntimeRouterCommandExecInvalidRequestAndParamsCodes|TestCommandExecStreamingSessionOperations|TestRuntimeRouterMCPRemoteErrorsIncludeRustErrorData" -count=1 -v` passed; `go test ./internal/appserver -count=1` passed.
- [x] Matched Rust `thread_start_accepts_metrics_service_name`: added `TestRouterThreadStartAcceptsMetricsServiceName` so v2 `thread/start` accepts the metrics `serviceName` field and still creates a thread.
- [x] Verification: `$env:GOCACHE='D:\qax\reagent\dev\codex_go\.gocache'; go test ./internal/appserver -run "TestRouterThreadStart(AcceptsMetricsServiceName|AllowsOmittedCWD|RejectsPaginatedHistoryMode|DropsUnsupportedServiceTier)|TestRuntimeRouterThreadStart(ServiceTierFiltersByModelCatalog|ProviderModelFallbackUsesBedrockStaticCatalog|ElevatedSandboxPersistsProjectTrust|ProjectTrustWriteGuards)" -count=1 -v` passed; first `go test ./internal/appserver -count=1` hit the known Windows TempDir cleanup race in `TestRuntimeRouterTurnStartRepairsRolloutOnlyThread`, and the immediate rerun passed.
- [x] Matched Rust `experimentalFeature/enablement/set` wire/result shape: Go now accepts the Rust `enablement` map as the primary input, keeps legacy `enabled`/`disabled` compatibility, ignores unsupported feature keys, and returns `{"enablement": {...}}` with only the effective changes from the current request.
- [x] Verification: `$env:GOCACHE='D:\qax\reagent\dev\codex_go\.gocache'; go test ./internal/features ./internal/appserver -run "Test(SetEnablementIgnoresUnknownKeys|FeatureWireShapeMatchesRust|RuntimeRouterDispatchesExperienceAPIs)" -count=1 -v` passed after rerunning the exact appserver test name; `go test ./internal/features ./internal/appserver -count=1` passed.
- [x] Added router-level Rust contract coverage for `modelProvider/capabilities/read`: `TestRuntimeRouterModelProviderCapabilitiesReadMatchesRust` now locks the default-provider all-true response and the Amazon Bedrock-style `imageGeneration=false`, `webSearch=false`, `namespaceTools=true` response.
- [x] Verification: `$env:GOCACHE='D:\qax\reagent\dev\codex_go\.gocache'; go test ./internal/model ./internal/appserver -run "TestProviderCapabilities|TestRuntimeRouterModelProviderCapabilitiesReadMatchesRust" -count=1 -v` passed; `go test ./internal/model ./internal/features ./internal/appserver -count=1` hit the known Windows PTY timing flake in `TestCommandExecTTYStreamsAndResizes`, and the immediate `go test ./internal/appserver -count=1` rerun passed.
- [x] Added first SDK/IDE contract smoke at the real `RuntimeRouter` layer: `TestRuntimeRouterSDKContractSmoke` runs one initialized connection through `initialize`, `thread/start`, `thread/read`, `thread/list`, `turn/start`, `turn/steer`, `turn/interrupt`, buffered `command/exec`, and HTTP MCP `mcpServer/tool/call`, including the MCP initialize/inventory/tool-call method sequence.
- [x] Verification: `$env:GOCACHE='D:\qax\reagent\dev\codex_go\.gocache'; go test ./internal/appserver -run TestRuntimeRouterSDKContractSmoke -count=1 -v` passed; expanded smoke plus `go test ./internal/appserver -count=1` passed. Note: Go app-server currently has no `shutdown` JSON-RPC method, so shutdown remains a transport/daemon-level SDK follow-up rather than part of this router smoke.

### 2026-07-09 Rust parity progress - account wire shape and turn steer metadata
- Locked `GetAccountResponse` Rust JSON union shape in Go auth tests, including null account, apiKey, chatgpt email/null email, missing plan -> unknown, and amazonBedrock credentialSource.
- Tightened appserver account login notification assertions so cancel emits `{ loginId, success:false, error }` and API key login emits `{ loginId:null, success:true, error:null }`, matching Rust account/login/completed semantics.
- Aligned `turn/steer` Responses API client metadata propagation with Rust: steer requests now enqueue client metadata through `SteerMailbox`, and the next agent sampling uses the steer-provided metadata instead of the original turn/start metadata.
- Added turn/appserver regression coverage for steer metadata updates and kept existing `SteerMailbox.Drain()` compatibility for older call sites.

### 2026-07-09 Rust parity review - request validation
- Reviewed Rust `app-server/tests/suite/v2/request_validation.rs` remote image URL rejection cases.
- Go already rejects remote image URLs for `turn/start`, `turn/steer`, and `thread/inject_items` with the Rust-compatible `-32600` message: `remote image URLs are not supported; use an inline data URL instead`.
- Verification: `go test ./internal/appserver -run "Test(RuntimeRouterRejectsRemoteImageTurnInputs|RouterInjectItemsRejectsRemoteImageURLs)" -count=1 -v`.

### 2026-07-09 Rust parity progress - thread status notifications
- Reviewed Rust `thread_status.rs` and `thread_unsubscribe.rs` against Go runtime-router coverage.
- Added Go coverage for the Rust-visible `thread/status/changed` notification sequence: active status is emitted when a turn starts and idle follows after completion.
- Confirmed existing Go coverage already handles thread unsubscribe connection scoping, repeat unsubscribe -> `notSubscribed`, cold thread -> `notLoaded`, runtime loaded-list pagination, and opt-out filtering.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouter(ThreadStatusChangedEmitsActiveThenIdle|InitializeOptOutNotificationMethodsFiltersStatusChanged|TurnFailureClearsActiveStateAndAllowsNextTurn)" -count=1 -v`.

### 2026-07-09 Rust parity progress - thread turns itemsView
- Reviewed Rust `thread_read.rs::thread_turns_list_supports_requested_items_view`.
- Added Go router coverage for `thread/turns/list.itemsView` variants:
  - `full` returns all turn items.
  - `summary` keeps the user item and latest assistant item.
  - `notLoaded` preserves turn identity/status/timestamps while clearing items.
- Verification: `go test ./internal/appserver -run "TestRouterThreadTurnsListSupportsRequestedItemsView" -count=1 -v` plus adjacent thread read/list/runtime turns tests.

### 2026-07-09 Rust parity progress - permission profile list
- Reviewed Rust `permission_profile_list.rs` and aligned Go `permissionProfile/list` with Rust's effective-config behavior.
- Go now lists Rust-ordered built-ins first (`:read-only`, `:workspace`, `:danger-full-access`), appends configured `[permissions.*]` profiles sorted by id, and returns built-in descriptions as `null` on the wire.
- App-server listing now reads the effective config for request `cwd`, including trusted project `.codex/config.toml` layers, so project-scoped permission profiles are visible even without `default_permissions`.
- Added router tests for configured profiles, trusted project profile pagination, and trusted project discovery without default selection.
- Verification: `go test ./internal/sandbox ./internal/config ./internal/appserver -run "Test(RuntimeRouterPermissionProfileList|RuntimeRouterDispatchesCatalogAPIs|ListProfiles|PermissionProfileSummary|LoadWithOptionsAppliesProjectConfigLayers|ProjectConfigRequiresTrustedProject|ProjectConfigTrustUsesActiveProjectRoot)" -count=1`.

### 2026-07-09 Rust parity progress - experimental feature list/set
- Reviewed Rust `experimental_feature_list.rs` and aligned Go router/config behavior for thread-scoped feature listing and feature enablement writes.
- `experimentalFeature/list` now resolves `threadId` through the stored thread CWD and reads trusted project config before computing feature enabled states; unknown thread ids return `-32600` with `thread not found: ...`.
- `experimentalFeature/enablement/set` now returns the applied `enablement` map, ignores Rust-invalid feature names, and makes applied values visible through `config/read` as defaults while preserving explicit user/project feature values.
- Added router regressions for project-config `memories=true`, unknown thread id, config/read propagation, user config non-override, and invalid feature filtering.
- Verification: `go test ./internal/features ./internal/config ./internal/appserver -run "Test(RuntimeRouterExperimentalFeature|RuntimeRouterDispatchesExperienceAPIs|SetEnablementIgnoresUnknownKeys|FeatureWireShapeMatchesRust|ListPaginatesFeatures|ListRejectsInvalidCursor|ServiceReadIncludesProjectConfigForCWD)" -count=1 -v`; `go test ./internal/features ./internal/config ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - collaboration mode list
- Reviewed Rust `collaboration_mode_list.rs` and `models-manager/src/collaboration_mode_presets.rs`.
- Go default collaboration modes now match Rust's visible presets: `Plan` first with `reasoning_effort = "medium"`, then `Default`; the Go-only `Agentic` preset was removed from the app-server list.
- Added service/router regression coverage for the exact Rust preset list and retained existing nullable field wire-shape coverage.
- Verification: `go test ./internal/appserver -run "Test(CollaborationModeList|RuntimeRouterCollaborationModeList|RuntimeRouterDispatchesCatalogAPIs)" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - thread unarchive response shape
- Reviewed Rust `thread_unarchive.rs::thread_unarchive_moves_rollout_back_into_sessions_directory`.
- Go already restored rollout-only archived threads to the active sessions directory and bumped `updatedAt`; added router-level wire-shape coverage for Rust-visible response details.
- `thread/unarchive` regression now asserts the returned thread is `status.type = "notLoaded"` and the serialized response includes an explicit `thread.name: null` when the rollout has no title.
- Verification: `go test ./internal/appserver -run "TestRouterArchiveUnarchiveAndDeleteRolloutOnlyThread|TestRuntimeRouterThreadArchiveDeleteUnloadRuntimeStatus" -count=1 -v`.

### 2026-07-09 Rust parity progress - thread start wire shape
- Reviewed Rust `thread_start.rs::thread_start_creates_persistent_thread` response and `thread/started` notification contracts.
- Go router coverage now asserts `thread/start` serializes `thread.name: null`, `thread.ephemeral: false`, and does not emit a top-level `sessionId`.
- Go runtime-router coverage now asserts `thread/started` notification serializes `thread.name: null`, `thread.ephemeral: false`, preserves `thread.threadSource = "user"`, and does not emit a top-level `sessionId` in params.
- Verification: `go test ./internal/appserver -run "TestRouterStartReadListAndItems|TestRuntimeRouterThreadStartStartedNotificationMatchesRustWireShape|TestRuntimeRouterInitializeOptOutNotificationMethodsFiltersThreadStarted" -count=1 -v`.

### 2026-07-09 Rust parity progress - thread rollback response shape
- Reviewed Rust `thread_rollback.rs` rollback response contract.
- Go rollout-only rollback coverage now asserts the response thread preserves `sessionId` and serializes unset title as explicit `thread.name: null`.
- Verification: `go test ./internal/appserver -run "TestRouterInjectItemsAndRollbackRepairRolloutOnlyThread|TestRouterSearchLoadedTurnsRollbackAndInjectItems|TestRuntimeRouterThreadRollback" -count=1 -v`.

### 2026-07-09 Rust parity progress - thread name read/list/resume shape
- Reviewed Rust `thread_read.rs::thread_name_set_is_reflected_in_read_list_and_resume`.
- Go `thread/name/set` coverage now asserts `thread/read`, `thread/list`, and `thread/resume` all serialize the updated title as `thread.name`.
- Go `thread/list` coverage also asserts the Rust-visible `thread.ephemeral: false` field is present for the named persistent thread.
- Verification: `go test ./internal/appserver -run "TestRouterSetNameAndMetadata|TestRuntimeRouterSetNameLifecycleNotifications|TestRouterMetadataWritesMissingThreadUseRustErrors" -count=1 -v`.

### 2026-07-09 Rust parity progress - pathless thread metadata
- Reviewed Rust `thread_read.rs` and `thread_unarchive.rs` pathless store metadata cases.
- Added Go coverage for store-only threads with no rollout path: `thread/read` and `thread/list` now assert explicit `path: null`, empty `preview`, and preserved `thread.name`.
- Added Go coverage for archived pathless store threads: `thread/unarchive` now asserts `path: null`, preserved `forkedFromId`, preserved `name`, and empty preview.
- Verification: `go test ./internal/appserver -run "TestRouterThread(ReadAndListPreservePathlessStoreMetadata|UnarchivePreservesPathlessStoreMetadata)|TestRouterReadAndResumeFallbackToRollout" -count=1 -v`.

### 2026-07-09 Rust parity progress - archive/delete empty results
- Reviewed Rust `thread_archive.rs` and `thread_delete.rs` response usage.
- Go router coverage now asserts `thread/archive` and `thread/delete` serialize empty result objects `{}` even though Go keeps internal, unexported lifecycle IDs for notification ordering.
- Verification: `go test ./internal/appserver -run "TestRouterArchiveUnarchiveAndDelete$|TestRouterThread(ReadAndListPreservePathlessStoreMetadata|UnarchivePreservesPathlessStoreMetadata)|TestRuntimeRouterThread(ArchiveDeleteUnloadRuntimeStatus|ArchiveDeleteSpawnedDescendants)" -count=1 -v`.

### 2026-07-09 Rust parity progress - thread resume remote redaction
- Reviewed Rust `thread_resume.rs::thread_resume_redacts_payloads_for_chatgpt_remote_clients`.
- Fixed Go `thread/resume` so ChatGPT remote client redaction applies to both `thread.turns` and `initialTurnsPage.data`; previously only `thread.turns` was redacted.
- Added regression coverage that remote clients redact MCP `arguments` and `result`, remove structured/meta payloads, and drop `imageGeneration` items from both response surfaces, while non-remote clients keep the original payloads.
- Verification: `go test ./internal/appserver -run "TestRouterThreadResumeRedactsRemoteClientInitialTurnsPage|TestRouterResumeInitialTurnsPageWithExcludeTurns|TestRouterResumeHistoryInitialTurnsPageWithExcludeTurns" -count=1 -v`.

### 2026-07-09 Rust parity progress - config batch write legacy profile rejection
- Reviewed Rust `config_rpc.rs::config_batch_write_rejects_legacy_profile_tables`.
- Added Go runtime-router regression coverage that `config/batchWrite` rejects legacy `profiles.*` writes with `config_write_error_code = configValidationError` and does not partially write earlier valid edits.
- Verification: `go test ./internal/appserver ./internal/config -run "TestRuntimeRouterConfig(WriteErrorDataMatchesRust|RejectsLegacyProfileWrite|BatchWriteRejectsLegacyProfilesAtomicallyLikeRust)|TestServiceWriteValueValidation" -count=1 -v`.

### 2026-07-09 Rust parity progress - config origins for arrays/tools/apps
- Reviewed Rust `config_rpc.rs::config_read_includes_tools` and `config_read_includes_apps`.
- Fixed Go config origins so array elements receive per-index origin entries such as `tools.web_search.allowed_domains.0`, matching Rust.
- Added service regression coverage for tools/app config values and origin paths including web search allowed domain indices and app approval settings.
- Verification: `go test ./internal/config -run "TestServiceRead(ConfigWithLayersAndOrigins|ToolsAndAppsOriginsMatchRustConfigRPC)|TestConfigReadResponseMarshalRustShape" -count=1 -v`.

### 2026-07-09 Rust parity progress - app/list accessible readiness
- Reviewed Rust `app_list.rs` readiness and force-refetch notification cases.
- Fixed Go app merging so `CodexAppsReady=false` no longer exposes remote directory-only connector state as an interim app/list result or cached notification payload.
- Preserved static/plugin app connectors while accessible data is still not ready, matching Rust's distinction between local capabilities and remote directory snapshots.
- Added service coverage for unready accessible responses and cached notification data.
- Verification: `go test ./internal/apps ./internal/appserver -run "Test(ListWaitsForAccessibleReadyBeforeMergingDirectoryLikeRust|CachedListForNotificationSkipsDirectoryWhenAccessibleNotReadyLikeRust)|TestRuntimeRouterTurnStartInjectsEnabledPluginInstructions|TestRuntimeRouterAppList" -count=1 -v`; `go test ./internal/apps ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - app/list thread feature config
- Reviewed Rust `app_list.rs::list_apps_uses_thread_feature_flag_when_thread_id_is_provided`.
- Fixed Go `app/list` to read effective config from the supplied `threadId`'s stored CWD, so trusted project feature config can override the current global config for that thread.
- `features.connectors=false` now maps through the Rust legacy alias to `apps=false` and returns an empty app/list without touching directory providers or cached connector state.
- Added runtime-router coverage for global `connectors=false` plus thread/project `connectors=true`.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterAppList(UsesThreadProjectFeatureConfigLikeRust|LoadsChatGPTDirectory|EmitsUpdatedNotificationWithFullList|ForceRefetchEmitsCachedThenFreshNotification|UsesPluginAppMetadata|MergesMCPAccessibleConnectors)|TestRuntimeRouterExperimentalFeatureListResolvesThreadProjectConfig" -count=1 -v`; `go test ./internal/apps ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - app/list force-refetch failure cache
- Reviewed Rust `app_list.rs::list_apps_force_refetch_preserves_previous_cache_on_failure`.
- Go already retained provider caches on failed directory refresh; added service-level regression coverage so a failed `forceRefetch` cannot clear the previous successful app list.
- Verification: `go test ./internal/apps -run "TestForceRefetchPreservesPreviousCacheOnDirectoryFailureLikeRust|TestListMergesProvidersPluginConnectorsAndCache|TestListWaitsForAccessibleReadyBeforeMergingDirectoryLikeRust" -count=1 -v`; `go test ./internal/apps ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - hooks/list per-cwd feature enablement
- Reviewed Rust `hooks_list.rs::hooks_list_uses_each_cwds_effective_feature_enablement`.
- Fixed Go hook discovery to honor each requested CWD's effective `hooks` feature setting before loading user/project/plugin hooks.
- Added coverage for global `hooks=false` with a trusted project `.codex/config.toml` re-enabling hooks for only that workspace CWD.
- Verification: `go test ./internal/appserver -run "TestHookDiscovery(UsesEachCWDEffectiveFeatureEnablementLikeRust|UsesTrustedProjectConfigLayers|SkipsUntrustedProjectHooksWhenConfigServicePresent|LinkedWorktreeUsesRootCheckoutHooks)|TestRuntimeRouterHooksList" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - skills/list cwd .codex roots and cache
- Reviewed Rust `skills_list.rs` cwd-local skill root, relative cwd/order, and force-reload cache cases.
- Fixed Go `skills/list` to include `cwd/.codex/skills` as a repo skill root, matching Rust.
- Added coverage for requested CWD order and relative CWD preservation, and for cached results remaining unchanged until `forceReload=true`.
- Verification: `go test ./internal/appserver -run "TestSkillsList(IncludesCWDCodeXSkillsRootLikeRust|PreservesRequestedCWDOrderAndRelativeCWDLikeRust|UsesCachedResultUntilForceReloadLikeRust)|TestSetExtraRoots|TestListSkillsAndConfig" -count=1 -v`; `go test ./internal/appserver -count=1`.
- Note: an existing Windows ConPTY timing test (`TestProcessServiceSpawnTTYStreamsAndResizes`) flaked twice during full-package verification, passed/skipped when isolated, and the final full appserver run passed.

### 2026-07-09 Rust parity progress - thread/shellCommand history filtering
- Reviewed Rust `thread_shell_command.rs::thread_shell_command_history_responses_exclude_persisted_command_executions`.
- Go already persisted user shell commands as user message records instead of `commandExecution`; added runtime-router response coverage that `thread/read` and `thread/turns/list` do not leak `commandExecution` items after a user shell command.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterThreadShellCommand(PersistsUserShellRecord|EmitsUserShellNotifications|EnqueuesActiveTurnContext)|TestRuntimeRouterColdThreadOperationsReturnThreadNotFound" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - selected capability unavailable environment skills
- Reviewed Rust `selected_capability_stack.rs`, especially the selected executor unavailable/resume assertions.
- Fixed Go selected capability skill discovery so a selected environment root is not discovered through its local path fallback when an `EnvironmentManager` is present and the non-local environment is not connected.
- Preserved the existing local/no-environment-service compatibility path and remote connected environment skill discovery.
- Added runtime-router coverage proving an unavailable selected executor does not expose its selected skill description/body to the model.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(UsesSelectedCapabilitySkillRoots|SkipsUnavailableSelectedEnvironmentSkillRootsLikeRust|UsesRemoteEnvironmentSkillRoot)" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - memory/reset runtime RPC
- Reviewed Rust `memory_reset.rs::memory_reset_clears_memory_files_and_rows_preserves_threads`.
- Go already cleared the `memories` directory and preserved session threads at router level; added runtime-router coverage for the initialized app-server RPC path and explicit empty `{}` result object.
- Note: Go does not currently carry Rust's sqlite stage1 memory table implementation, so this locks the shared filesystem/thread behavior without inventing a fake DB layer.
- Verification: `go test ./internal/appserver -run "Test(RuntimeRouterMemoryResetClearsMemoriesAndPreservesThreadsLikeRust|RouterMemoryResetClearsMemoriesAndPreservesThreads|RuntimeRouterModelProviderCapabilitiesReadMatchesRust)" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - turn/start output schema per-turn
- Reviewed Rust `output_schema.rs::turn_start_accepts_output_schema_v2` and `turn_start_output_schema_is_per_turn_v2`.
- Go already maps `OutputSchema` into Responses `text.format` with `name=codex_output_schema`, `type=json_schema`, and `strict=true`; added runtime-router coverage that the schema is passed only for the turn that supplied it.
- Added a two-turn regression where the first `turn/start` includes an object output schema and the second omits it, asserting the second agent request has `OutputSchema == nil`.
- Verification: `go test ./internal/appserver ./internal/model -run "Test(RuntimeRouterTurnStartOutputSchemaIsPerTurnLikeRust|ResponsesAgentRunnerSendsOutputSchemaTextFormat)" -count=1 -v`; `go test ./internal/appserver ./internal/model -count=1`.

### 2026-07-09 Rust parity progress - external clock sleep items
- Reviewed Rust `sleep.rs::external_sleep_polls_current_time_and_emits_items`.
- Added a turn tool-dispatch started callback and app-server mapping for `clock.sleep`, so long external sleeps emit a `sleep` `item/started` immediately and persist/complete a dedicated `sleep` ThreadItem instead of generic function_call/tool_output records.
- Ensured the sleep item `durationMs` preserves the requested duration, not the measured wall-clock polling duration.
- Added runtime-router regression coverage for external current-time polling, sleep started/completed notifications, and final turn completion.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouter(ExternalClockSleepEmitsSleepItemsLikeRust|TurnStartInjectsExternalCurrentTimeReminder|RequestCurrentTimeBridge|RequestCurrentTimeRequiresSingleSubscriber|RequestCurrentTimeWaitsForSubscriber)|TestRegisterCoreHandlersWithOptionsClockTools" -count=1 -v`; `go test ./internal/appserver ./internal/turn ./internal/tool -count=1`.

### 2026-07-09 Rust parity progress - fs runtime RPC surface
- Reviewed Rust `app-server/tests/suite/v2/fs.rs` and `request_processors/fs_processor.rs`.
- Added Rust-style JSON parameter validation for FS path params: relative paths now fail at decode time with `Invalid request: AbsolutePathBuf deserialized without a base path`, while direct `FSService` calls still return `ErrInvalidFSRequest`.
- Added local FS availability gating for app-server `fs/*` RPCs. When `CODEX_EXEC_SERVER_URL=none`, Go now returns `local filesystem is not configured`, matching Rust's disabled local environment behavior.
- Added runtime-router coverage for exact `fs/getMetadata` response fields, invalid base64 write errors, relative path errors across all path-bearing `fs/*` methods, and disabled local filesystem behavior.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterFS(GetMetadataReturnsOnlyUsedFieldsLikeRust|MethodsReturnErrorWhenLocalEnvironmentDisabledLikeRust|WriteFileRejectsInvalidBase64LikeRust|MethodsRejectRelativePathsLikeRust)|TestRuntimeRouterDispatchesThreadAndFS|TestRuntimeRouterFSWatch|TestRuntimeRouterFSWriteFile|TestService(ReadWriteFile|RejectsRelativePath|DirectoryMetadataCopyAndRemove|CopyDirectoryRequiresRecursive|CopyDirectoryRejectsDescendant|WatchChangedAndUnwatch|ChangedForPathMatchesFileAndDirectDirectoryWatch)" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - process/command local environment disabled
- Reviewed Rust `process_exec.rs::process_spawn_returns_error_when_local_environment_is_disabled` and `command_exec.rs::command_exec_returns_error_when_local_environment_is_disabled`.
- Promoted Go runtime local availability to `LocalEnvironmentEnabled`, shared by FS, process, and command exec RPCs.
- `process/spawn` and `command/exec` now return `local environment is not configured` when `CODEX_EXEC_SERVER_URL=none`, matching Rust's app-server behavior before any local process is started.
- Added runtime-router regressions for both disabled process spawn and disabled command exec.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouter(ProcessSpawnNotifications|ProcessSpawnReturnsErrorWhenLocalEnvironmentDisabledLikeRust|ProcessControlInvalidRequestAndParamsCodes|CommandExec|CommandExecReturnsErrorWhenLocalEnvironmentDisabledLikeRust|CommandExecInvalidRequestAndParamsCodes|FSMethodsReturnErrorWhenLocalEnvironmentDisabledLikeRust)|Test(CommandExecExecuteBuffered|ProcessServiceSpawnEmitsExitNotification)" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - process/command validation coverage
- Reviewed Rust `process_exec.rs::process_spawn_reports_buffered_output_cap_reached` and `command_exec.rs::command_exec_rejects_sandbox_policy_with_permission_profile`.
- Added Go regression coverage that `process/spawn` caps buffered stdout/stderr independently, returns the truncated text, and marks both cap flags.
- Added runtime-router coverage for the Rust `permissionProfile` plus `sandboxPolicy` rejection message and invalid-request code.
- Verification: `go test ./internal/appserver -run "Test(ProcessServiceSpawnReportsBufferedOutputCapReachedLikeRust|RuntimeRouterCommandExecRejectsSandboxPolicyWithPermissionProfileLikeRust|RuntimeRouterCommandExecInvalidRequestAndParamsCodes|RuntimeRouterCommandExecReturnsErrorWhenLocalEnvironmentDisabledLikeRust|RuntimeRouterProcessSpawnReturnsErrorWhenLocalEnvironmentDisabledLikeRust)" -count=1 -v`.

### 2026-07-09 Rust parity progress - command/exec non-streaming termination
- Reviewed Rust `command_exec.rs::command_exec_without_streams_can_be_terminated`.
- Fixed Go `CommandExecService` so a non-streaming `command/exec` with a client `processId` is registered as an active command before it starts, allowing a concurrent `command/exec/terminate` to cancel it.
- Calls without `processId` keep the legacy buffered synchronous behavior.
- Added regression coverage that a sleeping non-streaming command with `processId` can be terminated and returns a non-zero exit code with empty buffered output.
- Verification: `go test ./internal/appserver -run "TestCommandExec(WithoutStreamsCanBeTerminatedLikeRust|StreamingSessionOperations|StreamingStdin|ExecuteBuffered|SessionsAreConnectionScoped|ConnectionClosedCancelsOnlyThatConnection)|TestRuntimeRouterCommandExec" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - command/exec caps, env merge, streaming output
- Reviewed Rust `command_exec.rs::command_exec_env_overrides_merge_with_server_environment_and_support_unset`, `command_exec_non_streaming_respects_output_cap`, and `command_exec_streaming_does_not_buffer_output`.
- Added regression coverage for request env overrides adding, overriding, and unsetting variables while preserving base env values.
- Added coverage for non-streaming `command/exec` with `processId` respecting `outputBytesCap` independently for stdout and stderr.
- Added streaming coverage that capped stdout emits a `command/exec/outputDelta` with `capReached=true`, and the final streaming response does not include buffered stdout/stderr.
- Verification: `go test ./internal/appserver -run "TestCommandExec(StreamingDoesNotBufferOutputLikeRust|StreamingSessionOperations|WithoutStreamsCanBeTerminatedLikeRust|NonStreamingWithProcessIDRespectsOutputCapLikeRust|EnvOverridesMergeAndUnsetLikeRust)" -count=1 -v`.

### 2026-07-09 Rust parity progress - command/exec custom permission profiles
- Reviewed Go command/exec sandbox resolution against Rust app-server behavior where `permissionProfile` is resolved from the effective config, not only built-ins.
- Added an injectable permission profile resolver to `CommandExecService`; direct service calls still use built-ins, while `RuntimeRouter` reads effective config for the command cwd and compiles custom `[permissions.<id>]` profiles through the existing config resolver.
- Added regressions proving a custom `networked` profile preserves profile ID/cwd and reaches the sandbox runner with network enabled.
- Verification: `go test ./internal/appserver -run "TestCommandExec(CustomPermissionProfileResolverLikeRust|SandboxPolicyRequiringRunnerUsesSandboxRunner|FullAccessPermissionProfileRuns|SandboxDangerFullAccessRunsAndInjectsProfile|EnvOverridesMergeAndUnsetLikeRust)|TestRuntimeRouterCommandExec(ResolvesCustomPermissionProfileFromConfigLikeRust|RejectsSandboxPolicyWithPermissionProfileLikeRust|ReturnsErrorWhenLocalEnvironmentDisabledLikeRust|InvalidRequestAndParamsCodes|$)" -count=1 -v`.

### 2026-07-09 Rust parity progress - command/exec selected network proxy marker
- Reviewed Rust `command_exec_permission_profile_starts_selected_network_proxy` and `command_exec_permission_profile_does_not_reuse_default_network_proxy`.
- Go `command/exec` now clears inherited `CODEX_NETWORK_PROXY_ACTIVE` per launch and sets it only when the current resolved permission profile is sandboxed and allows network access.
- Added coverage that a selected custom `networked` profile marks the command environment active, while an explicit read-only profile stays unset even when `default_permissions = "networked"` and the server environment already had the marker.
- Verification: `go test ./internal/appserver -run "TestCommandExecCustomPermissionProfileResolverLikeRust|TestRuntimeRouterCommandExec(ResolvesCustomPermissionProfileFromConfigLikeRust|PermissionProfileDoesNotReuseDefaultNetworkProxyLikeRust)$" -count=1 -v`.

### 2026-07-09 Rust parity progress - command/exec project roots use command cwd
- Reviewed Rust `command_exec_permission_profile_project_roots_use_command_cwd`.
- Go now resolves relative `command/exec.cwd` against the app-server default cwd, matching Rust request behavior.
- Preserved custom permission profile runtime JSON from config resolution through command exec and tool runner into sandbox planning, so `:workspace_roots` path rules stay precise instead of being reduced to legacy writable roots.
- Added regression that a profile with `:workspace_roots = "write"` materializes write access at the relative command cwd and not at the server default cwd.
- Verification: `go test ./internal/appserver ./internal/tool -run "TestRuntimeRouterCommandExec(ResolvesCustomPermissionProfileFromConfigLikeRust|PermissionProfileDoesNotReuseDefaultNetworkProxyLikeRust|PermissionProfileProjectRootsUseCommandCWDLikeRust)|TestCommandExecCustomPermissionProfileResolverLikeRust|TestLocalShellRunnerUsesWindowsSandboxForSandboxedProfile|TestBuildShellRequestBuildsSandboxProfileFromPermissionProfile" -count=1 -v`.

### 2026-07-09 Rust parity progress - command/exec validation and pipe streaming
- Reviewed Rust `command_exec_rejects_negative_timeout_ms` and `command_exec_pipe_streams_output_and_accepts_write`.
- Added runtime-router coverage for negative `timeoutMs` with Rust's exact error message and invalid-params code.
- Added a pipe streaming regression that verifies pre-write stdout/stderr deltas, stdin write response `{}`, post-write stdout/stderr deltas, and empty final streaming buffers.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterCommandExecInvalidRequestAndParamsCodes|TestCommandExec(PipeStreamsOutputAndAcceptsWriteLikeRust|StreamingStdin|StreamStdinBuffersFinalOutputWhenNotStreamingStdout)" -count=1 -v`.

### 2026-07-09 Rust parity progress - process/spawn returns before exit
- Reviewed Rust `process_spawn_returns_before_exit_and_emits_exit_notification`.
- Added a process service probe/release regression proving `process/spawn` returns before the child exits, does not emit `process/exited` early, and later emits buffered stdout/stderr after release.
- Verification: `go test ./internal/appserver -run "TestProcessService(SpawnReturnsBeforeExitAndEmitsExitNotificationLikeRust|SpawnEmitsExitNotification|SpawnReportsBufferedOutputCapReachedLikeRust|DuplicateKillAndResize|ControlErrorsMatchRust)|TestRuntimeRouterProcessSpawnReturnsErrorWhenLocalEnvironmentDisabledLikeRust" -count=1 -v`.

### 2026-07-09 Rust parity progress - Responses client metadata lineage
- Reviewed Rust `client_metadata.rs` turn/start, thread fork lineage, cold resumed subagent lineage, and turn/steer follow-up metadata cases.
- Extended Go Responses client metadata construction to carry Rust-owned lineage fields into `x-codex-turn-metadata`: `forked_from_thread_id`, `parent_thread_id`, `subagent_kind`, and `thread_source`.
- Runtime turn metadata now reads lineage from the stored thread record so ordinary forks send `forked_from_thread_id`, while subagent records preserve parent/thread/session identity and emit `x-openai-subagent` / `x-codex-parent-thread-id` compatibility keys.
- Added Rust display-form subagent parsing for sources such as `subagent_guardian` and `subagent_thread_spawn_*_dN`, while preserving existing colon-form compatibility.
- Verification: `go test ./internal/codexapi ./internal/turn -run "TestClientSubagent|TestBuildResponsesClientMetadata" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(PassesResponsesAPIClientMetadata|SendsForkLineageInClientMetadataLikeRust|SendsSubagentLineageAfterColdResumeLikeRust)$" -count=1 -v`; `go test ./internal/codexapi ./internal/turn -count=1`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - turn/steer validation
- Reviewed Rust `turn_steer.rs` active-turn, oversized text, accepted steer, and context-only rejection cases.
- Added runtime-router coverage for oversized `turn/steer` input with Rust's invalid-params error data shape.
- Added active-turn coverage that context-only steer requests reject with `input must not be empty` and do not append user input or merge `additionalContext` into persisted thread items.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnSteer(PersistsUserInput|RejectsOversizedInputWithRustErrorData|RejectsContextOnlyInputWithoutMergingContextLikeRust|DeliveredToNextAgentSampling)$" -count=1 -v`.

### 2026-07-09 Rust parity progress - thread read/resume runtime edges
- Reviewed Rust `thread_read.rs` and `thread_resume.rs` around initial turns pages, incomplete rollout turns, restored token usage, updated-at deferral, and pending approval replay.
- Added coverage that `thread/resume.initialTurnsPage` equals `thread/turns/list` for the same page params while `excludeTurns=true` omits inline turns.
- Added token usage replay guards for metadata-only resumes and stale interrupted tails.
- Added runtime coverage that rollout-only incomplete turns surface as `interrupted` on both resume and read, and that resume itself does not refresh `updatedAt`/`recencyAt`.
- Implemented pending server-request replay on runtime `thread/resume`, so unresolved approval requests for the resumed thread are sent again.
- Verification: `go test ./internal/appserver -run "Test(RuntimeRouterThreadResumeAndReadInterruptIncompleteRolloutTurnWhenIdleLikeRust|RouterThreadResumeInitialTurnsPageMatchesRequestedTurnsListPage|RuntimeRouterNotifyRestoredTokenUsageFromRecord|RuntimeRouterThreadResumeDefersUpdatedAtUntilTurnStartLikeRust|RuntimeRouterThreadResumeReplaysPendingServerRequestApprovalLikeRust)$" -count=1 -v`.

### 2026-07-09 Rust parity progress - thread/resume personality override
- Reviewed Rust `thread_resume.rs::thread_resume_accepts_personality_override`.
- Runtime `thread/resume` now persists idle resume overrides into thread settings, including `cwd`, `model`, `serviceTier`, and `personality`, so the next `turn/start` uses the resumed settings without repeating them.
- Preserved running-thread rejoin behavior by skipping persistent resume settings while a turn is active, so mismatched resume overrides do not replace the active turn model/cwd.
- Resume-origin personality settings now carry the explicit personality presence marker, producing Rust-style `<personality_spec>` instructions for resume-supplied personality while leaving ordinary settings updates and config default personality behavior unchanged.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouter(ThreadResumeAppliesPersonalityOverrideLikeRust|ThreadResumeRunningIgnoresOverrideMismatch|TurnStartAppliesExplicitPersonality|TurnStartUsesConfigPersonalityTemplate|ThreadResumeDefersUpdatedAtUntilTurnStartLikeRust)$" -count=1 -v`.

### 2026-07-09 Rust parity progress - turn/interrupt pending approvals
- Reviewed Rust `turn_interrupt.rs::turn_interrupt_resolves_pending_command_approval_request`.
- Added a runtime-router regression that models a pending command approval request inside the active turn context, interrupts the turn, and verifies `serverRequest/resolved` is emitted for the same request ID.
- Confirmed existing Go broker context cancellation already matches Rust: the pending request exits with `context.Canceled`, and the turn emits `interrupted`.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnInterrupt(ResolvesPendingCommandApprovalLikeRust|WritesRolloutTurnLifecycle|CancelsActiveRuntimeAndRejectsConcurrentStart)$|TestRuntimeRouterThreadResumeReplaysPendingServerRequestApprovalLikeRust|TestServerRequestBrokerResolvedCallbackOnContextCancel" -count=1 -v`.

### 2026-07-09 Rust parity progress - turn/start personality mid-thread
- Reviewed Rust `turn_start.rs::turn_start_change_personality_mid_thread_v2`.
- Added a runtime-router regression for two turns in one thread: the first uses the default personality template without `<personality_spec>`, and the second explicitly switches to `friendly` and emits the Rust personality update block.
- This protects the resume-origin personality marker work from leaking explicit personality behavior into default/config-driven turns.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartAppliesExplicitPersonality|TurnStartChangesPersonalityMidThreadLikeRust|ThreadResumeAppliesPersonalityOverrideLikeRust|TurnStartUsesConfigPersonalityTemplate)$" -count=1 -v`.

### 2026-07-09 Rust parity progress - turn/start CWD rebinding
- Reviewed the CWD portion of Rust `turn_start.rs::turn_start_updates_sandbox_and_cwd_between_turns_v2`.
- Added runtime-router coverage that explicit per-turn CWD switches load different trusted project instruction files, and that a following turn without `cwd` inherits the latest CWD from thread settings.
- Confirmed existing Go behavior already matches the Rust CWD rebinding contract; this round adds executable coverage rather than production changes.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartUpdatesCWDBetweenTurnsLikeRust|ThreadSettingsUpdateAffectsFutureTurn|TurnStartSettingsOverrideEmitsThreadSettingsUpdated)$" -count=1 -v`.

### 2026-07-09 Rust parity progress - turn steer/interrupt errors
- Reviewed Rust `turn_steer.rs::turn_steer_requires_active_turn` and `turn_interrupt.rs::turn_interrupt_rejects_completed_turn`.
- Added coverage that no-active `turn/steer` returns Rust's invalid-request code and message.
- Fixed inactive/completed `turn/interrupt` to return JSON-RPC `-32600` instead of `-32603`, preserving the existing `turn ... is not active` message.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurn(SteerRequiresActiveTurnLikeRust|InterruptRejectsCompletedTurnLikeRust|InterruptResolvesPendingCommandApprovalLikeRust|InterruptWritesRolloutTurnLifecycle|SteerRejectsOversizedInputWithRustErrorData|SteerRejectsContextOnlyInputWithoutMergingContextLikeRust)$" -count=1 -v`.

### 2026-07-09 Rust parity progress - exec headless approval policy
- Reviewed Rust `exec/tests/suite/approval_policy.rs`, `exec/src/lib.rs::build_exec_config`, and human-output `config_summary_entries`.
- Go `codex exec` now computes Rust-style headless approval mode: default exec is `never`, bypass/full-auto stay `never`, and `approvals_reviewer = "auto_review"` preserves the configured approval policy such as `on-request`.
- Wired that policy into the exec tool router so `exec_command` `require_escalated` requests are rejected under default headless mode but surface as approval requests when auto-review/on-request is active.
- Added human stderr `approval: ...` summary output for non-JSON exec and removed stale direct-run `not implemented in the Go port yet` wording for unknown internal exec subcommands.
- Verification: `go test ./internal/exec -run "Test(EffectiveExecApprovalPolicyMatchesRustHeadless|ToolRouterUsesExecHeadlessApprovalPolicyLikeRust|RunRejectsUnknownExecSubcommandWithoutGoPortMessage|RunJSONAndLastMessage|NewRunnerDefaultsToResponsesAPI)$" -count=1 -v`; `go test ./internal/exec -count=1`; `go test ./internal/app -run "Test(AppExecJSONEndToEnd|RunExecPromptFromStdin|RunReview|RunExecReview|RunRootReview|RunExecServer|Exec)" -count=1 -v`.

### 2026-07-09 Rust parity progress - debug unknown subcommands
- Reviewed Rust `cli/src/main.rs::DebugSubcommand`; supported debug subcommands are `models`, `app-server`, `prompt-input`, hidden `trace-reduce`, and hidden `clear-memories`.
- Go `cli.Parse` now rejects unknown `debug` subcommands immediately with `unknown debug subcommand ...`, matching Rust's parser-level rejection instead of falling through to app-layer `not implemented`.
- Go `runDebug` and the generic app fallback no longer expose `is not implemented in the Go port yet`; added CLI/app regressions to lock the stale wording out.
- Verification: `go test ./internal/cli ./internal/app -run "Test(ParseDebugTooling|ParseDebugRejectsUnknownSubcommandLikeRust|DebugPromptInput|DebugUnknownSubcommandDoesNotExposeGoPortMessage)$" -count=1 -v`.

### 2026-07-09 Rust parity progress - remote TUI server request long tail
- Reviewed Rust `tui/src/app/app_server_requests.rs::PendingAppServerRequests::note_server_request` and `App::reject_app_server_request`.
- Go remote TUI now rejects Rust-unsupported server requests with JSON-RPC `-32000`: dynamic tool calls, attestation generation, external current time, legacy patch approval, and legacy command approval.
- `currentTime/read` no longer returns a local timestamp in Go remote TUI; legacy `applyPatchApproval` and `execCommandApproval` no longer open approval modals because Rust only supports the newer fileChange/commandExecution approval requests in TUI.
- Unknown remote TUI server request methods now return `Unsupported app-server request: ...` instead of stale `-32601 not implemented` / `Go TUI remote client` wording.
- Verification: `go test ./internal/app -run "TestRemoteServerRequestLongTailResponses|TestInteractiveRemoteTurnHandlesCommandApprovalServerRequest|TestInteractiveRemoteTurnHandlesUserInputServerRequest" -count=1 -v`.

### 2026-07-09 Rust parity progress - Windows sandbox setup backend
- Reviewed Rust `app-server/src/request_processors/windows_sandbox_processor.rs`, `app-server/tests/suite/v2/windows_sandbox_setup.rs`, and `core/src/windows_sandbox.rs::run_windows_sandbox_setup`.
- Go `windowsSandbox/setupStart` now follows Rust's two-phase contract: validate mode/cwd, respond `{started:true}`, run setup asynchronously, and emit `windowsSandbox/setupCompleted` to the originating connection.
- Added an injectable app-server `WindowsSandboxSetupRunner` plus a default runner for elevated setup and unelevated legacy preflight using resolved permission profile, workspace roots, codex home, cwd, and environment.
- Successful setup persists Rust's `windows.sandbox` config value and clears legacy Windows sandbox feature keys; failures, including persist failures, are reported via `success:false` completion notifications.
- Relative setup cwd now returns Rust-style `-32600 Invalid request: AbsolutePathBuf deserialized without a base path`; stale Windows sandbox backend `Go port yet` wording is removed.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterWindowsSandboxSetupStart|TestRuntimeRouterDispatchesRemoteEnvironmentAndWindows" -count=1 -v`; `go test ./internal/sandbox ./internal/sandbox/windowssandbox -count=1`; `go test ./internal/config -run "TestResolveSandboxPermissionProfile|TestConfig|Test.*Write" -count=1`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - Windows sandbox readiness config
- Reviewed Rust `windows_sandbox_processor.rs::determine_windows_sandbox_readiness` and `core/src/windows_sandbox.rs::WindowsSandboxLevelExt`.
- Go `windowsSandbox/readiness` now computes readiness from effective config instead of only the in-memory manager, with Rust precedence for `[windows].sandbox`, legacy `windows_sandbox`, and legacy feature flags.
- Non-Windows returns `notConfigured`; Windows unelevated returns `ready`; Windows elevated checks the setup marker and returns `ready` or `updateRequired`.
- Verification: `go test ./internal/appserver -run "Test(RuntimeRouterWindowsSandbox|WindowsSandboxLevelFromConfigValues)" -count=1 -v`; `go test ./internal/appserver -count=1`; `go test ./internal/config ./internal/sandbox ./internal/sandbox/windowssandbox -count=1`.

### 2026-07-09 Rust parity progress - Windows sandbox setup workspace roots
- Reviewed Rust `windows_sandbox_processor.rs` setup request construction, especially `config.effective_workspace_roots()`.
- Go `SandboxPermissionProfileResolution` now exposes materialized profile workspace roots, and `windowsSandbox/setupStart` passes cwd plus profile roots into the setup runner.
- Added a regression for a custom `default_permissions` profile with `[permissions.dev.workspace_roots]`, proving setup receives both cwd and the configured profile root.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterWindowsSandboxSetupStart|TestWindowsSandboxLevelFromConfigValues" -count=1 -v`; `go test ./internal/config -run "TestResolveSandboxPermissionProfile|Test.*Permission|TestConfig" -count=1`; `go test ./internal/appserver -count=1`; `go test ./internal/config ./internal/sandbox ./internal/sandbox/windowssandbox -count=1`.

### 2026-07-09 Rust parity progress - memory sqlite reset and thread memory mode
- Reviewed Rust `app-server/tests/suite/v2/memory_reset.rs`, `thread_memory_mode_set.rs`, `app-server/src/request_processors/thread_processor.rs`, and `state/src/runtime/memories.rs`.
- Added a narrow Rust-state SQLite compatibility layer in Go app-server: if `CODEX_SQLITE_HOME/state_5.sqlite` exists, `thread/memoryMode/set` updates `threads.memory_mode`; if `memories_1.sqlite` exists, `memory/reset` clears `stage1_outputs` and only memory pipeline `jobs` rows.
- Preserved existing file-session behavior for Go-only runs without sqlite files, while matching Rust's important data invariant: memory reset deletes memory outputs/jobs and keeps thread memory modes intact.
- Added regressions for sqlite-backed memory reset and sqlite-backed thread memory mode updates.
- Verification: `go test ./internal/appserver -run "TestRouter(MemoryResetClearsRustMemoriesSQLiteRowsLikeRust|ThreadMemoryModeSetUpdatesRustStateSQLiteLikeRust|MemoryResetClearsMemoriesAndPreservesThreads)|TestRuntimeRouterMemoryResetClearsMemoriesAndPreservesThreadsLikeRust" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - thread metadata sqlite git fields
- Reviewed Rust `thread_metadata_update.rs`, `thread_processor.rs::thread_metadata_update_response_inner`, and `state/src/runtime/threads.rs` thread metadata update paths.
- Extended the Go sqlite compatibility layer so `thread/metadata/update` writes final git metadata into existing `state_5.sqlite.threads` columns `git_sha`, `git_branch`, and `git_origin_url`.
- Null git patches now clear the Rust sqlite columns to NULL while preserving Go session-store and rollout metadata behavior.
- Added regression coverage for setting and clearing sqlite git metadata.
- Verification: `go test ./internal/appserver -run "TestRouter(ThreadMetadataUpdateUpdatesRustStateSQLiteLikeRust|SetNameAndMetadata|ThreadMetadataUpdateRejectsEmptyGitInfoPatch|MetadataWritesMissingThreadUseRustErrors|MemoryResetClearsRustMemoriesSQLiteRowsLikeRust|ThreadMemoryModeSetUpdatesRustStateSQLiteLikeRust)" -count=1 -v`; `go test ./internal/appserver -count=1` after one transient Windows TempDir cleanup retry.

### 2026-07-09 Rust parity progress - model/list default remote refresh
- Reviewed Rust `app-server/tests/suite/v2/model_list.rs`, `app-server/src/request_processors/catalog_processor.rs`, `app-server/src/models.rs`, and `models-manager/src/manager.rs`.
- Go `model/list` now treats an omitted internal refresh strategy as Rust `OnlineIfUncached`, so ChatGPT-backed model catalogs can fetch `/models` on first list request and use the remote catalog as source of truth.
- Explicit internal `RefreshOffline` remains available for service-tier and local picker paths that intentionally avoid network refresh.
- Added regressions proving default model list refreshes remote once and reuses the cached catalog, while explicit offline list does not hit the endpoint.
- Verification: `go test ./internal/model -run "TestListModels(Default|Explicit|Filters|Pagination)|TestRemoteModelsManager(CanUseRemoteCatalogAsSourceOfTruth|KeepsMerging)|TestConfiguredProviderModelsManagerUsesChatGPTRemoteCatalogAsSourceOfTruth" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterDispatchesCatalogAPIs|TestRuntimeRouterModelProviderCapabilitiesReadMatchesRust" -count=1 -v`; `go test ./internal/model -count=1`.

### 2026-07-09 Rust parity progress - recommended plugins after external login
- Reviewed Rust `app-server/tests/suite/v2/recommended_plugins.rs`, `core-plugins/src/manager.rs::recommended_plugin_candidates_for_config`, and `core-plugins/src/remote.rs::fetch_recommended_plugins`.
- Added Go support for the ChatGPT `/ps/plugins/suggested?scope=GLOBAL` recommended-plugin endpoint, mapping remote suggestions into `plugin@openai-curated-remote` install candidates.
- `turn/start` now configures the suggested-plugin provider from effective config and ChatGPT auth; the first turn after external `chatgptAuthTokens` login waits for the endpoint before sending the model request, so `<recommended_plugins>` and `request_plugin_install` are available immediately.
- Account login/session switch/logout clear the recommended-plugin cache to avoid carrying suggestions across auth changes.
- Endpoint failures or `enabled != true` fall back to the existing local discovery path, matching Rust's legacy-mode fallback.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouter(FirstTurnAfterExternalLoginWaitsForRecommendedPluginsLikeRust|TurnStartInjectsEnabledPluginInstructions|TurnStartDoesNotRecommendConnectorOnlyCandidates)" -count=1 -v`; `go test ./internal/plugin -count=1`; `go test ./internal/appserver -run "Test(RuntimeRouterTurnStart.*Plugin|PluginInstallRuntime|PluginInstallCandidatesForTurnApplyDisabledAndLoadedConnectorConfig|RuntimeRouterDispatchesCatalogAPIs)" -count=1 -v`; `go test ./internal/appserver -count=1` after one unrelated Windows TempDir cleanup retry.

### 2026-07-09 Rust parity progress - account rate limits fixture coverage
- Reviewed Rust `app-server/tests/suite/v2/rate_limits.rs`, `rate_limit_reset_credits.rs`, account processor handlers, and backend-client rate-limit helpers.
- Confirmed Go already matched the Rust request routing/auth/error surface for `getAccountRateLimits`, `consumeRateLimitResetCredit`, and `sendAddCreditsNudgeEmail`, including reset-credit auth failures and idempotency-key validation.
- Added a Rust-fixture-shaped app-server regression for `/api/codex/usage`, covering the primary `codex` snapshot, secondary windows, spend-control individual limit, plan type, reached type, reset credits, and `rateLimitsByLimitId`.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouter(GetAccountRateLimitsReturnsSnapshotLikeRust|SendAddCreditsNudgeEmail|ConsumeRateLimitResetCredit|AccountBackendReadsRequireChatGPTAuth|AccountBackendTimeoutsMatchRust|PersonalAccessTokenBackendReadsHydrateAccountRouting|AccountBackendClientConstructionErrorIsWrapped)" -count=1 -v`.

### 2026-07-09 Rust parity progress - standalone web search
- Reviewed Rust `app-server/tests/suite/v2/web_search.rs`, `ext/web-search/src/tool.rs`, `extension.rs`, `history.rs`, and `output.rs`.
- Added Go `web.run` as a model-visible namespace tool gated by `features.standalone_web_search`, using the current provider/auth to POST `/alpha/search` with Rust-style `SearchRequest` commands, `allowed_callers: ["direct"]`, and recent input including the current user message.
- Responses tool serialization now treats `web.run` as a namespace tool instead of a flattened `web__run` function, and the schema includes Rust's `time` description.
- Runtime tool lifecycle now emits `item/started` / `item/completed` as `webSearch` ThreadItems and persists one completed `webSearch` history item, avoiding ordinary function-call/tool-output history items for the search call.
- The model follow-up receives `function_call_output` content items shaped as `[{"type":"input_text","text":...}]`, matching Rust's standalone web search output path.
- Follow-up parity in the same pass: `web.run` now prunes search input like Rust's `recent_input` tail (previous visible user turn plus current user text, user images removed, contextual environment user messages ignored) and locks Rust command-action cases for multi-query image search, URL open, URL/non-URL find, and non-literal open.
- Verification: `go test ./internal/model ./internal/turn -count=1`; `go test ./internal/appserver -run "TestRuntimeRouter(StandaloneWebSearchMatchesRustFixture|TurnStartRunsRuntimeAndPersistsItems|TurnStartInjectsExternalCurrentTimeReminder|TurnStartNullServiceTierClearsConfigDefault)" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - output schema HTTP fixture
- Reviewed Rust `app-server/tests/suite/v2/output_schema.rs`.
- Existing Go runtime already kept `outputSchema` per turn and the model runner already serialized it as Responses `text.format`.
- Added app-server HTTP/SSE integration coverage using the real `ResponsesAgentRunner`, proving `turn/start` sends Rust's exact `text.format` object with `name: "codex_output_schema"`, `type: "json_schema"`, `strict: true`, and the requested schema.
- The same regression verifies the following turn omits `text.format` when `outputSchema` is absent, preventing schema leakage across turns.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(OutputSchemaIsPerTurnLikeRust|SendsOutputSchemaTextFormatLikeRust)" -count=1 -v`; `go test ./internal/appserver -count=1`.

### 2026-07-09 Rust parity progress - current time reminder and clock namespace
- Reviewed Rust `app-server/tests/suite/v2/current_time.rs`, `core/tests/suite/current_time_reminder.rs`, `core/src/context/current_time_reminder.rs`, `core/src/session/time_reminder.rs`, and `core/src/tools/handlers/current_time.rs`.
- Go current-time reminders now enter the model as bare developer `input_text` messages (`It is ... UTC.`) instead of tagged instruction text, matching Rust's contextual developer fragment.
- Delivered reminders are persisted into session history and participate in later `InputItemsFromRecord` history, so interval-suppressed requests still carry the last reminder and interval-expired requests append a fresh one.
- Added Rust-shaped coverage for the app-server external `currentTime/read` round trip, interval persistence (`[first]`, `[first]`, `[first, third]`), zero-interval time moving backward, single-tool `after_user_or_tool_output` post-tool injection, and the `clock.curr_time` tool returning the latest external time.
- Responses tool serialization now exposes the `clock` namespace as a namespace tool (`clock.curr_time`) like Rust instead of flattening it to `clock__curr_time`.
- Verification: `go test ./internal/model ./internal/appserver -run "TestResponses(ToolsFromSpecs|ToolNames)|TestRuntimeRouter(CurrentTime(ReadAddsDeveloperInputLikeRust|RemindersFollowIntervalAndPersistInHistoryLikeRust|ToolReturnsLatestTimeLikeRust|ReminderFollowsToolOutputDeliveryModeLikeRust)|ZeroCurrentTimeReminderIntervalDeliversWhenTimeMovesBackwardLikeRust|TurnStartInjectsExternalCurrentTimeReminder|ExternalClockSleepEmitsSleepItemsLikeRust)$" -count=1 -v`; `go test ./internal/... -count=1`.
- Remaining current-time follow-ups: multi-tool batch semantics for `after_user_or_tool_output`, assistant-only `end_turn=false` continuations, and compaction/window refresh semantics.

### 2026-07-09 Rust parity progress - config requirements new-thread defaults
- Reviewed Rust `app-server/tests/suite/v2/config_rpc.rs::config_requirements_read_includes_new_thread_model_defaults` and the app-server README contract for `configRequirements/read`.
- Go `config.NewConfigService` now loads `${CODEX_HOME}/requirements.toml` during construction, so managed requirements from disk are visible to app-server RPCs without test-only injection.
- Added service and router regressions for `[models.new_thread]` covering `model`, `model_reasoning_effort`, and `service_tier` through the Rust-shaped `models.newThread` response.
- Verification: `go test ./internal/config ./internal/appserver -run "Test(NewConfigServiceLoadsRequirementsFileLikeRust|RuntimeRouterConfigRequirementsReadIncludesNewThreadModelDefaultsLikeRust)" -count=1 -v`; `go test ./internal/config ./internal/appserver -count=1`.
- Next config follow-ups: continue `config_rpc.rs` translation for `config/read` effective/layers, nested web-search config, apps, desktop settings, project/system layer precedence, and remaining write conflict/error-data fixtures.

### 2026-07-09 Rust parity progress - config/read web search tool config
- Reviewed Rust `app-server/tests/suite/v2/config_rpc.rs::config_read_includes_nested_web_search_tool_config` and `config_read_ignores_bool_web_search_tool_config`.
- Added router-level JSON regression coverage for nested `[tools.web_search]` values, including `context_size`, `allowed_domains`, and `location.region: null`.
- Fixed Go config response normalization so legacy `[tools] web_search = true` serializes as `tools.web_search: null`, matching Rust's typed `ToolsV2.web_search = None` behavior instead of leaking a boolean.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterConfigReadWebSearchToolConfigMatchesRust" -count=1 -v`; `go test ./internal/config ./internal/appserver -count=1` hit the known Windows ConPTY flake once, then `go test ./internal/appserver -count=1` passed on rerun.
- Next config follow-ups: apps, desktop settings, project layers, managed/system layer overrides, and write conflict/error-data fixtures from the same Rust file.

### 2026-07-09 Rust parity progress - config/read apps and desktop settings
- Reviewed Rust `app-server/tests/suite/v2/config_rpc.rs::config_read_includes_apps` and `config_read_includes_desktop_settings`.
- Added app-server router JSON coverage for Rust app config defaults and nullable fields, plus origin/layer assertions for app keys.
- Added router coverage for opaque desktop config preservation, including `appearanceTheme`, hyphenated `selected-avatar-id`, and nested `desktop.workspace` values.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterConfigReadAppsAndDesktopSettingsMatchRust" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterConfig(Read(WebSearchToolConfig|AppsAndDesktopSettings)|RequirementsReadIncludesNewThreadModelDefaults)" -count=1 -v`; `go test ./internal/appserver -count=1`.
- Next config follow-ups: project layer origin metadata and managed/system layer override precedence from `config_rpc.rs`.

### 2026-07-09 Rust parity progress - config/read managed layer overrides
- Reviewed Rust `app-server/tests/suite/v2/config_rpc.rs::config_read_includes_system_layer_and_overrides` and protocol layer precedence comments.
- `config.NewConfigService` now loads explicit `CODEX_APP_SERVER_MANAGED_CONFIG_PATH` as a `legacyManagedConfigTomlFromFile` layer, matching the app-server fixture path.
- Config layer merge ordering now follows Rust precedence semantics, so higher numeric precedence layers apply later and override lower ones; existing managed override tests were adjusted to use Rust's legacy managed-file source instead of lower-precedence `enterpriseManaged`.
- Added service and router regressions covering managed overrides for `model`, `approval_policy`, and nested `sandbox_workspace_write.writable_roots`, while preserving user-origin `sandbox_mode` and `network_access`.
- Verification: `go test ./internal/config -run "Test(NewConfigServiceLoadsManagedConfigFromAppServerEnvLikeRust|ServiceManagedLayersOverride|ServiceWriteReportsOverriddenByManagedLayer|ServiceReadIncludesProjectConfigForCWD|LayerSourcePrecedence)" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterConfigReadIncludesManagedLayerOverridesLikeRust" -count=1 -v`; `go test ./internal/config ./internal/appserver -count=1`.
- Next config follow-ups: router-level project layer fixture, then remaining config write/desktop batch/reload cases.

### 2026-07-09 Rust parity progress - config/read project layer
- Reviewed Rust `app-server/tests/suite/v2/config_rpc.rs::config_read_includes_project_layers_for_cwd`.
- Added router-level coverage for trusted project `.codex/config.toml` loading through `config/read` with `cwd`, including project-origin metadata and user/project layer ordering.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterConfigReadIncludesProjectLayerForCWDLikeRust" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterConfig(Read(WebSearchToolConfig|AppsAndDesktopSettings|IncludesManagedLayerOverrides|IncludesProjectLayerForCWD)|RequirementsReadIncludesNewThreadModelDefaults)" -count=1 -v`; `go test ./internal/config ./internal/appserver -count=1` hit a known Windows TempDir cleanup race once, then `go test ./internal/appserver -count=1` passed on rerun.
- Next config follow-ups: value write replacement, desktop write, batch write, version conflicts, and hot reload fixtures from `config_rpc.rs`.

### 2026-07-09 Rust parity progress - config write success paths
- Reviewed Rust `app-server/tests/suite/v2/config_rpc.rs` value write, desktop write, batch write, and desktop batch write fixtures.
- Added router-level coverage for `config/value/write` with `expectedVersion`, desktop key writes, sandbox batch writes, and desktop batch writes.
- Fixed nested value comparison for config writes so JSON numeric request values and TOML reload values compare structurally; this prevents false `okOverridden` when writing desktop maps like `{ width: 320 }`.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterConfigWriteSuccessPathsMatchRust" -count=1 -v`; `go test ./internal/config ./internal/appserver -run "Test(ConfigReadResponseMarshalRustShape|ServiceWriteValueAndBatchWrite|ServiceWriteReportsOverriddenByManagedLayer|RuntimeRouterConfig(WriteSuccessPathsMatchRust|WriteErrorDataMatchesRust|BatchWriteRejectsLegacyProfilesAtomicallyLikeRust))" -count=1 -v`; `go test ./internal/config ./internal/appserver -count=1` hit a known ConPTY flake once, then `go test ./internal/appserver -count=1` passed on rerun.
- Next config follow-ups: pipelined write/read ordering and `reloadUserConfig=true` hot reload behavior.

### 2026-07-09 Rust parity progress - config/read forced workspace ids
- Reviewed Rust `app-server/tests/suite/v2/config_rpc.rs::config_read_accepts_legacy_forced_chatgpt_workspace_id` and `config_read_accepts_forced_chatgpt_workspace_id_list`.
- Added router-level JSON coverage proving `forced_chatgpt_workspace_id` preserves Rust's untagged shape: single string for the legacy scalar and array for multiple workspace ids.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterConfigReadForcedWorkspaceIDsMatchRust" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterConfig(Read(WebSearchToolConfig|ForcedWorkspaceIDs|AppsAndDesktopSettings|IncludesManagedLayerOverrides|IncludesProjectLayerForCWD)|RequirementsReadIncludesNewThreadModelDefaults|WriteSuccessPathsMatchRust|WriteErrorDataMatchesRust|BatchWriteRejectsLegacyProfilesAtomicallyLikeRust)" -count=1 -v`; `go test ./internal/config ./internal/appserver -count=1`.
- Next config follow-ups: finish any remaining `config_rpc.rs` edge cases, then resume thread/turn/app-server error-data fixtures.

### 2026-07-09 Rust parity progress - config/read effective layers
- Reviewed Rust `app-server/tests/suite/v2/config_rpc.rs::config_read_returns_effective_and_layers`.
- Added router-level coverage for effective user config, `origins.model` user-file metadata, and returned `layers` when `includeLayers=true`.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterConfig(Read(WebSearchToolConfig|ForcedWorkspaceIDs|ReturnsEffectiveAndLayers|AppsAndDesktopSettings|IncludesManagedLayerOverrides|IncludesProjectLayerForCWD)|RequirementsReadIncludesNewThreadModelDefaults|WriteSuccessPathsMatchRust|WriteErrorDataMatchesRust|BatchWriteRejectsLegacyProfilesAtomicallyLikeRust)" -count=1 -v`.
- Next app-server follow-ups: move back to Rust `thread.rs`, `turn.rs`, and remaining business error-data fixtures.

### 2026-07-09 Rust parity progress - requirements clone empty slices
- Full `go test ./internal/... -count=1` exposed that `ConfigService.Requirements()` collapsed explicit empty requirement slices to nil during cloning.
- Fixed `cloneRequirements` to preserve nil-vs-empty slice semantics, so `allowed_web_search_modes = []` remains an explicit disabled web-search requirement and debug-config renders `allowed_web_search_modes: disabled`.
- Verification: `go test ./internal/app ./internal/config -run "TestInteractiveDebugConfigReaderUsesRustStyleRenderer|TestNewConfigServiceLoadsRequirementsFileLikeRust|TestRequirementsClone|TestLoadRequirementsFileParsesRustStyleTOML" -count=1 -v`; `go test ./internal/... -count=1`.
- Next app-server follow-ups: Rust `thread.rs`, `turn.rs`, and remaining business error-data fixtures.

### 2026-07-09 Rust parity progress - request validation remote image urls
- Reviewed Rust `app-server/tests/suite/v2/request_validation.rs::request_handlers_reject_remote_image_urls`.
- Upgraded the Go runtime-router regression to mirror the Rust fixture across `turn/start`, `turn/steer`, and `thread/inject_items` using raw JSON-shaped params and a real loaded thread.
- Locked the remote image URL JSON-RPC error shape to Rust parity: `-32600`, exact remote-image message, nil `error.data`, and no serialized `data` field.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterRequestHandlersRejectRemoteImageURLsLikeRust" -count=1 -v`; `go test ./internal/appserver -run "RequestHandlersRejectRemoteImageURLsLikeRust|InjectItemsRejectsRemoteImageURLs" -count=1 -v`.
- Next app-server follow-ups: continue Rust `turn.rs` request/error semantics and remaining thread lifecycle boundary fixtures.

### 2026-07-09 Rust parity progress - turn/start skills budget warning
- Reviewed Rust `app-server/tests/suite/v2/turn_start.rs::turn_start_emits_thread_scoped_warning_notification_for_trimmed_skills` and `core-skills/src/render.rs`.
- Fixed Go skill rendering warnings so token-budget truncation says `Exceeded skills context budget of 2%.`, matching Rust's app-server warning text; character-budget warnings keep the non-percent prefix.
- Strengthened app-server warning coverage to require a thread-scoped `warning` notification with `threadId` and the Rust token-budget prefix.
- Verification: `go test ./internal/prompt ./internal/appserver -run "TestRenderAvailableSkillsTokenBudgetWarningMentionsPercentLikeRust|TestRuntimeRouterSkillsContextEmitsBudgetWarning" -count=1 -v`; `go test ./internal/prompt ./internal/appserver -run "Test(RenderAvailableSkills|DefaultSkillMetadataBudget|RuntimeRouterSkillsContext|RuntimeRouterTurnStartInjectsAvailableSkills|RuntimeRouterImplicitSkillInvocationFromShellCommand)" -count=1 -v`.
- Next app-server follow-ups: continue `turn_start.rs` service-tier/originator/approval notification parity and remaining thread lifecycle fixtures.

### 2026-07-09 Rust parity progress - turn/start service tier forwarding
- Reviewed Rust `app-server/tests/suite/v2/turn_start.rs::turn_start_sends_service_tier_id_to_model_request`.
- Added app-runtime coverage proving explicit `turn/start.serviceTier` survives model/catalog validation and reaches `model.AgentRequest.ServiceTier` for a model that supports non-default tiers.
- This locks the runtime side of the Rust fixture and complements the existing Responses-agent HTTP serialization test for the `service_tier` request field.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnStartSendsServiceTierIDToModelRequestLikeRust|TestRuntimeRouterTurnStartNullServiceTierClearsConfigDefault|TestRuntimeRouterThreadSettingsUpdateAffectsFutureTurn" -count=1 -v`.
- Next app-server follow-ups: continue `turn_start.rs` originator header, analytics, and command/file-change approval notification parity.

### 2026-07-09 Rust parity progress - turn/start notifications and model override
- Reviewed Rust `app-server/tests/suite/v2/turn_start.rs::turn_start_emits_notifications_and_accepts_model_override`, plus Rust completed-turn notification construction in `bespoke_event_handling.rs` and `turn_processor.rs`.
- Go runtime `turn/completed` notifications now match Rust's lightweight turn payload: `itemsView:"notLoaded"` and `items:[]` for normal completion, failed turns, interrupted turns, and standalone `thread/shellCommand` completion.
- Added a Rust-fixture-shaped runtime-router test with two turns on one thread: first turn uses the thread model, second turn supplies `model:"mock-model-override"`, both started/completed notifications are `notLoaded` empty items, and the second agent request carries the override model.
- Added `waitForTurnStartedStatus` to make notification tests key off the specific turn id.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnStartEmitsNotificationsAndAcceptsModelOverrideLikeRust|TestRuntimeRouterTurnStartRunsRuntimeAndPersistsItems" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartEmitsNotificationsAndAcceptsModelOverrideLikeRust|TurnStartRunsRuntimeAndPersistsItems|TurnFailureClearsActiveStateAndAllowsNextTurn|TurnInterruptCancelsActiveRuntime|ThreadShellCommandStandaloneCompletes|ThreadShellCommand)" -count=1 -v`; `go test ./internal/appserver -count=1`.
- Next app-server follow-ups: continue `turn_start.rs` collaboration-mode override, analytics/client metadata, and command/file-change approval notification parity.

### 2026-07-09 Rust parity progress - turn/start collaboration mode override
- Reviewed Rust `app-server/tests/suite/v2/turn_start.rs::turn_start_accepts_collaboration_mode_override_v2`, `turn_processor.rs::normalize_collaboration_mode`, `protocol/src/config_types.rs::CollaborationMode`, and `collaboration-mode-templates/templates/default.md`.
- `turn/start.collaborationMode` now updates thread settings and can be inherited by later turns.
- Runtime turn preparation now applies `collaborationMode.settings.model` and `collaborationMode.settings.reasoning_effort` as the effective model and reasoning effort, so a collaboration-mode model overrides a same-request `turn/start.model` like Rust.
- Default collaboration mode with `developer_instructions: null` injects Rust's built-in Default mode developer block as a `<collaboration_mode>` developer input item, including the `request_user_input` availability warning.
- Added `TestRuntimeRouterTurnStartAcceptsCollaborationModeOverrideLikeRust`.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnStartAcceptsCollaborationModeOverrideLikeRust" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartAcceptsCollaborationModeOverrideLikeRust|TurnStartEmitsNotificationsAndAcceptsModelOverrideLikeRust|TurnStartSendsServiceTierIDToModelRequestLikeRust|ThreadSettingsUpdateAffectsFutureTurn|TurnStartChangesPersonalityMidThreadLikeRust)" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterPlanModeStreamsProposedPlanItem" -count=1 -v`; `go test ./internal/appserver -count=1` passed on rerun after one Windows TempDir cleanup race.
- Next app-server follow-ups: continue `turn_start.rs` feature-overridden `request_user_input` descriptions, analytics/client metadata, and command/file-change approval notification parity.

### 2026-07-09 Rust parity progress - turn/start request_user_input feature override
- Reviewed Rust `app-server/tests/suite/v2/turn_start.rs::turn_start_uses_thread_feature_overrides_for_request_user_input_tool_description_v2` and `core/src/tools/handlers/request_user_input_spec.rs`.
- `thread/start.config` is now persisted and inherited by later turns; runtime config overlays expand dotted keys such as `features.default_mode_request_user_input`, so thread-level feature overrides affect turn tool schemas.
- `request_user_input` now exposes the Rust tool description with `autoResolutionMs` guidance and mode availability text; app-server switches the description from `Plan mode` to `Default or Plan mode` when the feature is enabled.
- Added `RequestUserInputAvailableModes` through appserver -> turn -> tool registry and guarded cached default tool-router reuse when the feature-specific schema is needed.
- Added `TestRequestUserInputHandlerSpecDescriptionMatchesRustModes` and `TestRuntimeRouterTurnStartUsesThreadFeatureOverridesForRequestUserInputToolDescriptionLikeRust`.
- Verification: `go test ./internal/tool -run "TestRequestUserInput" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(UsesThreadFeatureOverridesForRequestUserInputToolDescriptionLikeRust|AcceptsCollaborationModeOverrideLikeRust)" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartUsesThreadFeatureOverridesForRequestUserInputToolDescriptionLikeRust|TurnStartAcceptsCollaborationModeOverrideLikeRust|TurnStartEmitsNotificationsAndAcceptsModelOverrideLikeRust|TurnStartSendsServiceTierIDToModelRequestLikeRust|ThreadSettingsUpdateAffectsFutureTurn|TurnStartChangesPersonalityMidThreadLikeRust)" -count=1 -v`; `go test ./internal/tool ./internal/turn -count=1`; `go test ./internal/appserver -count=1`.
- Next app-server follow-ups: continue `turn_start.rs` analytics/client metadata fields and command/file-change approval notification parity.

### 2026-07-09 Rust parity progress - turn/start analytics event shape
- Reviewed Rust `analytics/src/events.rs::CodexTurnEventParams`, `analytics/src/reducer.rs::codex_turn_event_params`, and `turn_start.rs::turn_start_tracks_thread_originator_in_analytics`.
- Added `internal/telemetry` turn event payload types and `NewCodexTurnEvent` to lock the local `codex_turn_event` contract before app-server transport wiring.
- The builder matches Rust's nested `app_server_client` shape, serializes optional fields as explicit `null`, defaults missing `service_tier` to `default`, and lets thread originator override `app_server_client.product_client_id`.
- Added exact JSON-shape coverage and a thread-originator override regression matching the Rust fixture's `codex_work_desktop` assertion. This is payload-shape parity; actual app-server analytics delivery remains to be wired.
- Verification: `go test ./internal/telemetry -run "TestCodexTurnEvent" -count=1 -v`; `go test ./internal/telemetry -count=1`; `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartUsesThreadFeatureOverridesForRequestUserInputToolDescriptionLikeRust|TurnStartPassesResponsesAPIClientMetadata|TurnStartPreservesThreadOriginator|ThreadStartUsesConnectionClientInfoOriginator)" -count=1 -v`.
- Next app-server follow-ups: wire app-server runtime facts into telemetry turn-event delivery, then continue command/file-change approval notification parity.

### 2026-07-09 Rust parity progress - turn/start command and file-change approvals
- Reviewed Rust `app-server/tests/suite/v2/turn_start.rs::turn_start_exec_approval_toggle_v2`, `turn_start_exec_approval_decline_v2`, and `turn_start_file_change_approval_v2`.
- Go shell execution now treats `approvalPolicy:"untrusted"` like Rust: ordinary shell calls request `item/commandExecution/requestApproval`; `approvalPolicy:"never"` with danger-full-access does not prompt.
- App-server tool routers now inject broker-backed approval callbacks for command execution and apply_patch file changes, producing Rust request params with `threadId`, `turnId`, `itemId`, and `environmentId:"local"` for commands.
- Runtime now emits `item/started` for commandExecution/fileChange before requesting approval, uses call ids as the external item ids, and suppresses duplicate completed notifications for the in-progress tool-call item.
- Declined command approvals complete as `status:"declined"` with no exit code or aggregated output; accepted file-change approvals emit `serverRequest/resolved` before `item/completed` and write the patch afterward.
- `apply_patch` now requests approval before applying changes and exposes absolute file paths in file-change `changes`, matching the Rust app-server fixture.
- Added regressions: `TestRuntimeRouterTurnStartExecApprovalToggleLikeRust`, `TestRuntimeRouterTurnStartExecApprovalDeclineLikeRust`, and `TestRuntimeRouterTurnStartFileChangeApprovalLikeRust`.
- Verification: `go test ./internal/tool -run "Test(ShellExecutor|BuildShellRequest|ApplyPatchExecutor)" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(ExecApprovalToggleLikeRust|ExecApprovalDeclineLikeRust|FileChangeApprovalLikeRust)$" -count=1 -v`; `go test ./internal/tool -count=1`; `go test ./internal/appserver -run "Test(RuntimeRouterTurnStart|RuntimeRouterApplyPatch|RuntimeRouterResponsesStreaming|ThreadItem|ServerRequest|Notification|Schema)" -count=1`; `go test ./internal/appserver -count=1`; `go test ./internal/turn ./internal/tool -count=1`.
- Next app-server follow-ups: wire the analytics payload into real delivery, then continue approval-for-session cache/granular policy/network approval/amendment parity.

### 2026-07-09 Rust parity progress - turn/start analytics delivery
- Reviewed Rust `analytics/src/reducer.rs::codex_turn_event_params`, `analytics/src/events.rs::CodexTurnEventParams`, `analytics/src/client.rs`, and `app-server/tests/suite/v2/turn_start.rs::turn_start_tracks_thread_originator_in_analytics`.
- App-server runtime now emits a typed `codex_turn_event` to an injectable analytics sink when a turn completes, using the existing Rust-shaped payload builder instead of leaving analytics at payload-only parity.
- The event includes Rust fixture fields from connection client metadata, experimental API enablement, thread originator override, session/thread lineage, effective model/provider, service tier, approval policy/reviewer, sandbox policy/network access, collaboration mode, personality, workspace kind, input-image count, token usage, timing profile, and tool counts.
- Added `TestRuntimeRouterTurnStartEmitsCodexTurnAnalyticsLikeRust` and the local sink/helper coverage for the completed-turn event.
- Verification: `go test ./internal/telemetry -count=1`; `go test ./internal/appserver -run "TestRuntimeRouterTurnStartEmitsCodexTurnAnalyticsLikeRust|TestRuntimeRouterTurnStartPreservesThreadOriginator|TestRuntimeRouterThreadStartUsesConnectionClientInfoOriginator" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterTurnStart" -count=1`; `go test ./internal/appserver ./internal/telemetry -count=1`.
- Next app-server follow-ups: implement the full Rust analytics HTTP queue/export path, add failed/interrupted turn analytics and turn/thread initialized events, then continue approval-for-session cache/granular policy/network approval/amendment parity.

### 2026-07-09 Rust parity progress - file-change approval acceptForSession and decline
- Reviewed Rust `turn_start.rs::turn_start_file_change_approval_accept_for_session_persists_v2` and `turn_start_file_change_approval_decline_v2`.
- App-server now records session-scoped approval grants by thread id; `acceptForSession` for file changes skips later `item/fileChange/requestApproval` prompts on the same thread, including later turns.
- Added a command approval session cache path at the same layer so future command `acceptForSession` parity can reuse the same mechanism.
- Fixed completed file-change status mapping so declined patch approvals emit Rust's `status:"declined"` instead of being collapsed to `failed`, and verified the declined patch is not applied.
- Added `TestRuntimeRouterTurnStartFileChangeApprovalAcceptForSessionPersistsLikeRust` and `TestRuntimeRouterTurnStartFileChangeApprovalDeclineLikeRust`.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnStartFileChangeApproval(LikeRust|AcceptForSessionPersistsLikeRust|DeclineLikeRust)$" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(ExecApprovalToggleLikeRust|ExecApprovalDeclineLikeRust|FileChangeApprovalLikeRust|FileChangeApprovalAcceptForSessionPersistsLikeRust|FileChangeApprovalDeclineLikeRust)$" -count=1 -v`; `go test ./internal/appserver -count=1`.
- Next app-server follow-ups: continue `turn_start.rs` command execution process id/output-shape fixtures and the network/amendment approval paths.

### 2026-07-09 Rust parity progress - turn sandbox/cwd and personality migration
- Reviewed Rust `turn_start_updates_sandbox_and_cwd_between_turns_v2`, `turn_start_with_elevated_override_does_not_persist_project_trust`, `turn_start_uses_migrated_pragmatic_personality_without_override_v2`, and `core/src/personality_migration.rs`.
- Added `TestRuntimeRouterTurnStartUpdatesSandboxAndCWDBetweenTurnsLikeRust`; the first turn applies workspace-write/cwd settings without invoking the Windows workspace sandbox runner, and the second turn verifies a real commandExecution item uses the second turn cwd under danger-full-access.
- Added `TestRuntimeRouterTurnStartElevatedSandboxDoesNotPersistProjectTrustLikeRust`, locking that turn-level elevated sandbox overrides do not persist project trust to `config.toml`.
- Implemented `config.MaybeMigratePersonality` with Rust-like `.personality_migration` marker behavior, explicit global personality skip, no-session skip, active/archived rollout user-session detection, and global `personality = "pragmatic"` persistence.
- `NewRuntimeRouter` now runs the personality migration on startup; `TestRuntimeRouterStartupMigratesPragmaticPersonalityLikeRust` verifies the migrated pragmatic template is baked into model instructions without emitting `<personality_spec>`.
- Verification: `go test ./internal/config -run "TestMaybeMigratePersonality" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(StartupMigratesPragmaticPersonalityLikeRust|TurnStartUsesConfigPersonalityTemplate|TurnStartAppliesExplicitPersonality|TurnStartChangesPersonalityMidThreadLikeRust)" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartIgnoresDeprecatedMultiAgentMode|ThreadStartIgnoresDeprecatedMultiAgentMode)$" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterTurnStartUpdates(CWDBetweenTurnsLikeRust|SandboxAndCWDBetweenTurnsLikeRust)$" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterTurnStartElevatedSandboxDoesNotPersistProjectTrustLikeRust|TestRuntimeRouterThreadStartElevatedSandboxPersistsProjectTrust|TestRuntimeRouterThreadStartProjectTrustWriteGuards" -count=1 -v`; `go test ./internal/config ./internal/appserver -count=1`.
- Next app-server follow-ups: platform-gated command process-id notification parity, network/amendment approval paths, and analytics HTTP queue/export.

### 2026-07-09 Rust parity progress - apply_patch streaming feature gate
- Reviewed Rust `turn_start_does_not_stream_apply_patch_change_updates_without_feature_v2` and `turn_start_streams_apply_patch_change_updates_v2` expectations around `item/fileChange/patchUpdated`.
- Go Responses streaming now gates apply-patch `item/fileChange/patchUpdated` notifications on `features.apply_patch_streaming_events`, using the effective per-turn config including JSON `Config` overrides.
- Apply-patch custom tool input deltas remain consumed by the stream handler when the feature is off, so they do not leak as assistant text or MCP progress, but no file-change patch update notification is emitted.
- Updated the existing positive streaming notification test to enable the feature explicitly and added `TestRuntimeRouterResponsesStreamingSkipsApplyPatchPatchUpdatedWithoutFeatureLikeRust` for the default/off Rust fixture.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterResponsesStreaming(EmitsDeltaNotifications|SkipsApplyPatchPatchUpdatedWithoutFeatureLikeRust)$" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterResponsesStreaming" -count=1 -v`; `go test ./internal/appserver -count=1`.
- Next app-server follow-ups: continue command execution process-id/output-shape fixtures with Windows gating, then network/amendment approval and analytics export transport.

### 2026-07-09 Rust parity progress - command approval amendment/session cache
- Reviewed Rust `protocol/src/approvals.rs::ExecPolicyAmendment` and approval suite expectations for prefix-rule amendments.
- Go app-server command approval requests now include `proposedExecpolicyAmendment` in Rust's transparent array shape when `exec_command` carries a valid `prefix_rule`.
- Added `TestRuntimeRouterTurnStartExecApprovalIncludesPrefixRuleAmendmentLikeRust` to lock the real runtime-router -> server request broker payload.
- Added `TestRuntimeRouterTurnStartExecApprovalAcceptForSessionPersistsLikeRust` so command approval session caching has the same regression protection as the file-change approval session path.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnStartExecApproval(ToggleLikeRust|DeclineLikeRust|IncludesPrefixRuleAmendmentLikeRust|AcceptForSessionPersistsLikeRust)$" -count=1 -v`; `go test ./internal/appserver -count=1`.
- Next app-server follow-ups: network approval / network policy amendment parity, then analytics export transport; unified exec process-id notification remains platform-gated until Go has an equivalent process manager.

### 2026-07-09 Rust parity progress - network approval protocol wire shape
- Reviewed Rust `protocol/src/approvals.rs::NetworkApprovalProtocol`, which serializes protocol names with snake_case.
- Fixed Go app-server `NetworkApprovalSocks5TCP` and `NetworkApprovalSocks5UDP` constants to serialize as `socks5_tcp` and `socks5_udp` instead of Go-only camelCase.
- Extended `TestServerRequestMarshalShape` to lock `networkApprovalContext.protocol` inside command approval server requests to the Rust wire shape.
- Verification: `go test ./internal/appserver -run "TestServerRequestMarshalShape|TestRuntimeRouterTurnStartExecApproval" -count=1 -v`; `go test ./internal/appserver -count=1`.
- Next app-server follow-ups: implement actual managed-network approval request/policy amendment persistence after the Go network proxy runtime is ready, and continue analytics HTTP queue/export transport.

### 2026-07-10 Rust parity progress - analytics HTTP queue/export
- Reviewed Rust `analytics/src/client.rs`, `analytics/src/events.rs::TrackEventsRequest`, and `app-server/src/analytics_utils.rs`.
- Added Go `telemetry.AnalyticsEventsClient` with a Rust-style buffered non-blocking queue, disabled behavior for `analytics.enabled=false`, 10s HTTP timeout, graceful close, and `TrackEventsRequest { events }` envelope.
- Added HTTP export to `{chatgpt_base_url}/codex/analytics-events/events` with `Content-Type: application/json`, static/dynamic auth support, and no delivery attempt when app-server auth is missing or not a Codex backend auth mode.
- Added `config.AnalyticsEnabled(default)` / `AnalyticsEnabledValue()` so Go preserves Rust's `Option<bool>` default semantics for `analytics.enabled`.
- App-server default router now wires the analytics client when enabled, resolves auth for each send, and CLI `--analytics-default-enabled` now reaches `RuntimeRouterOptions`.
- Added unit coverage for the HTTP envelope and disabled behavior, plus `TestRuntimeRouterConfiguredAnalyticsPostsRustTrackEventsRequest` proving a completed turn posts the Rust-shaped payload with ChatGPT auth headers.
- Verification: `go test ./internal/telemetry -count=1`; `go test ./internal/config -count=1`; `go test ./internal/appserver -count=1`; `go test ./internal/app -count=1`; `go test ./internal/cli -run "TestParseAppServer|TestParse" -count=1`; `git diff --check` only reported existing CRLF normalization warnings.
- Next app-server follow-ups: failed/interrupted turn analytics, thread/turn initialized analytics, accepted-line fingerprints/tool-item analytics, then the actual managed-network approval request and policy amendment path.

### 2026-07-10 Rust parity progress - failed/interrupted turn analytics
- Reviewed Rust `analytics/src/reducer.rs::analytics_turn_status` and `analytics_client_tests.rs` failed/interrupted turn lifecycle cases.
- Active Go runtime turns now retain connection id and resolved run config so non-success completions can emit the same `codex_turn_event` payload shape as successful turns.
- Runtime failures after config resolution now emit `status:"failed"` analytics with timing fields; generic Go failures keep `turn_error` and `codex_error_*` null until structured CodexErrorInfo/CodexErrKind mapping is added.
- `turn/interrupt` now emits `status:"interrupted"` analytics from the active turn context, with null error fields like Rust.
- Added `TestRuntimeRouterTurnStartFailedEmitsCodexTurnAnalyticsLikeRust` and `TestRuntimeRouterTurnInterruptedEmitsCodexTurnAnalyticsLikeRust`.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurn(StartEmitsCodexTurnAnalyticsLikeRust|StartFailedEmitsCodexTurnAnalyticsLikeRust|InterruptedEmitsCodexTurnAnalyticsLikeRust)$" -count=1 -v`; `go test ./internal/appserver -count=1`.
- Next app-server follow-ups: structured CodexErrorInfo/CodexErrKind analytics mapping, thread/turn initialized analytics, accepted-line fingerprints, and tool-item analytics.

### 2026-07-10 Rust parity progress - thread initialized analytics
- Reviewed Rust `analytics/src/events.rs::ThreadInitializedEventParams`, `analytics/src/reducer.rs::emit_thread_initialized`, and app-server suite fixtures for thread start/resume/fork initialized analytics.
- Added Go `codex_thread_initialized` telemetry types with Rust's nested `app_server_client`, runtime, model, source, initialization mode, lineage, and `created_at` shape.
- Changed the analytics HTTP `TrackEventsRequest` body to a raw event union so the same queue/export path can send both `codex_turn_event` and `codex_thread_initialized`.
- Runtime router now emits thread initialized analytics for `thread/start`, `thread/resume`, and `thread/fork`, preserving connection client metadata and record/request originator overrides; fork events carry `forked_from_thread_id` and keep `parent_thread_id` null like Rust.
- Added serialization, HTTP union, and app-server regressions: `TestRuntimeRouterThreadStartEmitsThreadInitializedAnalyticsLikeRust`, `TestRuntimeRouterThreadResumeEmitsThreadInitializedAnalyticsLikeRust`, and `TestRuntimeRouterThreadForkEmitsThreadInitializedAnalyticsLikeRust`.
- Verification: `go test ./internal/telemetry -count=1`; `go test ./internal/appserver -run "TestRuntimeRouterThread(Start|Resume|Fork)EmitsThreadInitializedAnalyticsLikeRust|TestRuntimeRouterConfiguredAnalyticsPostsRustTrackEventsRequest" -count=1 -v`; `go test ./internal/appserver -count=1`; `go test ./internal/telemetry ./internal/config ./internal/app ./internal/cli -count=1`.
- Next app-server follow-ups: structured CodexErrorInfo/CodexErrKind analytics mapping, accepted-line fingerprints, tool-item analytics, steer-count/subagent edge cases, and managed-network approval parity.

### 2026-07-10 Rust parity progress - turn analytics steer count
- Reviewed Rust `analytics/src/reducer.rs` steer-count state and `analytics_client_tests.rs::accepted_steers_increment_turn_steer_count`.
- Active Go runtime turns now track accepted `turn/steer` calls by thread/turn id after app-server steer handling succeeds.
- Completed, failed, and interrupted `codex_turn_event` emission now carries the real accepted steer count instead of always sending `0`.
- Added `TestRuntimeRouterTurnAnalyticsCountsAcceptedSteersLikeRust`, which steers an active turn twice and verifies the final analytics payload has `steer_count: 2`.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurn(StartEmitsCodexTurnAnalyticsLikeRust|StartFailedEmitsCodexTurnAnalyticsLikeRust|InterruptedEmitsCodexTurnAnalyticsLikeRust|AnalyticsCountsAcceptedSteersLikeRust)$" -count=1 -v`; `go test ./internal/turn -run "TestAgentLoopDrainsSteerMailboxBeforeNextSampling|TestSteer" -count=1 -v`; `go test ./internal/appserver -count=1`; `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`.
- Next app-server follow-ups: separate `codex_turn_steer_event` accepted/rejected analytics, accepted-line fingerprints, tool-item analytics, and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - turn steer analytics event
- Reviewed Rust `analytics/src/events.rs::CodexTurnSteerEventParams`, `analytics/src/facts.rs::TurnSteerResult` / `TurnSteerRejectionReason`, and reducer fixtures for accepted/rejected steer analytics.
- Added Go `codex_turn_steer_event` telemetry types with Rust's nested `app_server_client`, runtime metadata, expected/accepted turn ids, thread lineage, `num_input_images`, `result`, `rejection_reason`, and `created_at` shape.
- The analytics queue/export path now sends steer events through the same raw event union and Rust `{"events":[...]}` envelope used by turn and thread-initialized analytics.
- Runtime router now emits accepted steer analytics after `turn/steer` is accepted and queued, and rejected analytics for `TurnService.Steer` failures with Rust reason names for `no_active_turn`, `expected_turn_mismatch`, `empty_input`, and `input_too_large`.
- Added `TestRuntimeRouterTurnSteerEmitsAcceptedAnalyticsLikeRust`, `TestRuntimeRouterTurnSteerRejectedEmitsAnalyticsLikeRust`, and `TestTurnSteerAnalyticsRejectionReasonMatchesRust`, plus telemetry serialization/HTTP union coverage.
- Verification: `go test ./internal/telemetry -count=1`; `go test ./internal/appserver -run "TestRuntimeRouterTurnSteer(EmitsAcceptedAnalyticsLikeRust|RejectedEmitsAnalyticsLikeRust|RejectsOversizedInputWithRustErrorData)|TestRuntimeRouterTurnAnalyticsCountsAcceptedSteersLikeRust|TestTurnSteerAnalyticsRejectionReasonMatchesRust" -count=1 -v`; `go test ./internal/appserver -count=1`; `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`.
- Next app-server follow-ups: non-steerable review/compact rejection parity when Go has matching active-turn states, accepted-line fingerprints, tool-item analytics, and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - accepted-line analytics transport
- Reviewed Rust `analytics/src/accepted_lines.rs`, `analytics/src/events.rs::CodexAcceptedLineFingerprintsEventRequest`, and `analytics/src/client.rs::track_event_request_batches`.
- Corrected Go accepted-line analytics payloads to Rust's outer `event_type:"codex_accepted_line_fingerprints"` shape with inner `event_params.event_type:"codex.accepted_line_fingerprints"`.
- Optional accepted-line fields now serialize as explicit `null` like Rust, and uploaded `line_fingerprints` remains an empty array even though local parsing still computes hashes for counts/tests.
- `HTTPAnalyticsExporter.SendTrackEvents` now isolates accepted-line fingerprint events into their own one-event HTTP requests while batching adjacent regular events together, matching Rust's `should_send_in_isolated_request` path.
- Added `TestAcceptedLineFingerprintEventSerializesExpectedRustShape`, `TestAcceptedLineFingerprintEventSerializesNullOptionFieldsLikeRust`, and `TestHTTPAnalyticsExporterIsolatesAcceptedLineFingerprintEventsLikeRust`.
- Verification: `go test ./internal/telemetry -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterConfiguredAnalyticsPostsRustTrackEventsRequest|TestRuntimeRouterTurnSteer(EmitsAcceptedAnalyticsLikeRust|RejectedEmitsAnalyticsLikeRust)|TestRuntimeRouterTurnAnalyticsCountsAcceptedSteersLikeRust" -count=1 -v`; `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`.
- Next app-server follow-ups: emit accepted-line events from completed turns once Go has latest-diff/repo-hash completion context, then continue tool-item analytics and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - accepted-line runtime emission
- Reviewed Rust `analytics/src/reducer.rs::accepted_line_event_input` and the completed-turn accepted-line emission path.
- Go runtime turn completion now snapshots the active diff tracker before clearing it and emits `codex_accepted_line_fingerprints` when the completed turn produced accepted added/deleted lines.
- The emitted event carries `product_surface:"codex"`, effective model slug, completion time, aggregate added/deleted counts, and an intentionally empty `line_fingerprints` array.
- Added `TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust`, which applies a patch through the real runtime, observes the diff notification, waits for completion, and verifies the analytics aggregate.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust|TestRuntimeRouterApplyPatchEmitsTurnDiffUpdated" -count=1 -v`; `go test ./internal/telemetry -count=1`; `go test ./internal/appserver -count=1`.
- Next app-server follow-ups: repo hash lookup for accepted-line events, command/file-change tool-item analytics, and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - command execution analytics event
- Reviewed Rust `analytics/src/events.rs::CodexCommandExecutionEventRequest`, `CodexToolItemEventBase`, and `analytics/src/reducer.rs::tool_item_event`.
- Added Go `codex_command_execution_event` telemetry types with Rust's flattened tool-item base fields, terminal status, failure kind, approval outcome, command source, exit code, and command action counts.
- The analytics client/exporter now accepts command execution events through the same Rust `{"events":[...]}` envelope.
- Runtime completion now emits command execution analytics for ordinary completed agent shell commands using initialized connection client metadata, thread lineage, item timing, execution duration, exit code, and action counts.
- Added `TestCodexCommandExecutionEventSerializesExpectedRustShape` and `TestRuntimeRouterCommandExecutionEmitsAnalyticsLikeRust`.
- Verification: `go test ./internal/telemetry -run "TestCodexCommandExecutionEventSerializesExpectedRustShape|TestAnalyticsEventsClientPosts" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(CommandExecutionEmitsAnalyticsLikeRust|TurnCompletedEmitsAcceptedLineFingerprintsLikeRust)" -count=1 -v`; `go test ./internal/telemetry -count=1`; `go test ./internal/appserver -count=1`; `go test ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`.
- Next app-server follow-ups: declined/failed/reviewed command analytics with review summaries, file-change tool-item analytics, MCP/dynamic tool-call analytics, and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - file-change analytics event
- Reviewed Rust `analytics/src/events.rs::CodexFileChangeEventRequest`, `CodexToolItemEventBase`, and the reducer's `patch_apply_outcome` mapping for file-change tool items.
- Added Go `codex_file_change_event` telemetry types with Rust's flattened tool-item base fields and aggregate file add/update/delete/move counters.
- The analytics client/exporter now accepts file-change events through the same Rust `{"events":[...]}` envelope.
- Runtime completion now emits file-change analytics for completed `apply_patch` items using initialized connection client metadata, thread lineage, item timing, terminal status/failure kind, and parsed file-change counts.
- Added `TestCodexFileChangeEventSerializesExpectedRustShape` and `TestRuntimeRouterFileChangeEmitsAnalyticsLikeRust`.
- Verification: `go test ./internal/telemetry -run "TestCodex(CommandExecution|FileChange)EventSerializesExpectedRustShape|TestAnalyticsEventsClientPosts" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(FileChangeEmitsAnalyticsLikeRust|CommandExecutionEmitsAnalyticsLikeRust|TurnCompletedEmitsAcceptedLineFingerprintsLikeRust)" -count=1 -v`.
- Next app-server follow-ups: user/guardian review summary denormalization, MCP/dynamic tool-call analytics, and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - tool-item review summaries
- Reviewed Rust `analytics/src/reducer.rs::record_item_review_summary`, `tool_item_base`, `command_execution_review_result`, `file_change_review_result`, and `final_approval_outcome`.
- Go runtime now records per-tool-item review summaries when command execution and file-change approval responses resolve, then denormalizes those summaries onto the completed `codex_command_execution_event` / `codex_file_change_event`.
- No-review tool-item analytics now uses Rust reducer default `final_approval_outcome:"unknown"` instead of the earlier placeholder `not_needed`.
- User approval decisions now map to Rust outcomes: `user_approved`, `user_approved_for_session`, `user_denied`, and `user_aborted`.
- Added `TestRuntimeRouterCommandExecutionAnalyticsIncludesUserReviewSummaryLikeRust` and `TestRuntimeRouterFileChangeAnalyticsIncludesUserReviewSummaryLikeRust`.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouter(CommandExecutionEmitsAnalyticsLikeRust|FileChangeEmitsAnalyticsLikeRust|CommandExecutionAnalyticsIncludesUserReviewSummaryLikeRust|FileChangeAnalyticsIncludesUserReviewSummaryLikeRust)" -count=1 -v`; `go test ./internal/telemetry -run "TestCodex(CommandExecution|FileChange)EventSerializesExpectedRustShape" -count=1 -v`.
- Next app-server follow-ups: guardian/permissions/network review paths and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - MCP/dynamic tool-call analytics
- Reviewed Rust `CodexMcpToolCallEventRequest`, `CodexDynamicToolCallEventRequest`, and reducer mappings for MCP/dynamic outcomes and dynamic content counts.
- Added Go telemetry types for `codex_mcp_tool_call_event` and `codex_dynamic_tool_call_event`, both flattening Rust's shared tool-item base fields.
- The analytics client/exporter now accepts MCP and dynamic tool-call events through the same Rust `{"events":[...]}` envelope.
- Runtime completion now emits MCP and dynamic analytics for completed `mcpToolCall` / `dynamicToolCall` items, including timing, review summary fields, terminal status/failure kind, and event-specific metadata.
- Dynamic tool analytics now counts output content items by total/text/image and carries the optional `success` field; app-server coverage uses the real `item/tool/call` server request path.
- Added `TestCodexMCPToolCallEventSerializesExpectedRustShape`, `TestCodexDynamicToolCallEventSerializesExpectedRustShape`, and `TestRuntimeRouterDynamicToolCallEmitsAnalyticsLikeRust`.
- Verification: `go test ./internal/telemetry -run "TestCodex(MCPToolCall|DynamicToolCall|Review|CommandExecution|FileChange)EventSerializesExpectedRustShape|TestAnalyticsEventsClientPostsReviewUnionEventLikeRust" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(DynamicToolCallEmitsAnalyticsLikeRust|CommandExecutionEmitsAnalyticsLikeRust|FileChangeEmitsAnalyticsLikeRust|CommandExecutionAnalyticsIncludesUserReviewSummaryLikeRust|FileChangeAnalyticsIncludesUserReviewSummaryLikeRust)" -count=1 -v`.
- Next app-server follow-ups: guardian/permissions/network review paths and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - review analytics event
- Reviewed Rust `analytics/src/events.rs::CodexReviewEventRequest` / `CodexReviewEventParams` and `analytics/src/reducer.rs::emit_review_event`.
- Added Go `codex_review_event` telemetry types with Rust's app-server client/runtime metadata, thread lineage, subject kind/name, reviewer, trigger, status, resolution, and timing fields.
- The analytics client/exporter now accepts review events through the same Rust `{"events":[...]}` envelope.
- `ServerRequestBroker` now has a response-aware resolved callback so app-server can derive Rust review ids as `user:<request_id>` while preserving the existing `serverRequest/resolved` notification.
- Runtime router now emits review analytics when command execution and file-change approval responses resolve, and keeps those review results denormalized onto the later tool-item analytics event.
- Added `TestCodexReviewEventSerializesExpectedRustShape`, `TestAnalyticsEventsClientPostsReviewUnionEventLikeRust`, and app-server assertions in the command/file-change review summary tests.
- Verification: `go test ./internal/telemetry -run "TestCodexReviewEventSerializesExpectedRustShape|TestAnalyticsEventsClientPostsReviewUnionEventLikeRust|TestCodex(CommandExecution|FileChange)EventSerializesExpectedRustShape" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(CommandExecutionAnalyticsIncludesUserReviewSummaryLikeRust|FileChangeAnalyticsIncludesUserReviewSummaryLikeRust|CommandExecutionEmitsAnalyticsLikeRust|FileChangeEmitsAnalyticsLikeRust)" -count=1 -v`.
- Next app-server follow-ups: guardian/permissions/network review paths and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - collab/web/image tool-item analytics
- Reviewed Rust `CodexCollabAgentToolCallEventRequest`, `CodexWebSearchEventRequest`, `CodexImageGenerationEventRequest`, and reducer mappings for collab tool names/outcomes, web-search action/query counts, and image-generation failed/error handling.
- Added Go telemetry types for `codex_collab_agent_tool_call_event`, `codex_web_search_event`, and `codex_image_generation_event`, all flattening Rust's shared tool-item base fields.
- The analytics client/exporter now accepts collab-agent, web-search, and image-generation events through the same Rust `{"events":[...]}` envelope.
- Runtime completion now emits collab/web/image analytics alongside command/file/MCP/dynamic tool-item analytics.
- Web-search analytics is covered through the real standalone `web.run` runtime fixture; collab/image generation are covered at router emission level with Rust-shaped ThreadItems until Go exposes full equivalent runtime product paths.
- Added `TestCodexCollabAgentToolCallEventSerializesExpectedRustShape`, `TestCodexWebSearchEventSerializesExpectedRustShape`, `TestCodexImageGenerationEventSerializesExpectedRustShape`, and `TestRuntimeRouterCollabAndImageToolAnalyticsLikeRust`; extended `TestRuntimeRouterStandaloneWebSearchMatchesRustFixture` to assert `codex_web_search_event`.
- Verification: `go test ./internal/telemetry -run "TestCodex(CollabAgentToolCall|WebSearch|ImageGeneration|MCPToolCall|DynamicToolCall)EventSerializesExpectedRustShape" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(StandaloneWebSearchMatchesRustFixture|CollabAndImageToolAnalyticsLikeRust|DynamicToolCallEmitsAnalyticsLikeRust)$" -count=1 -v`.
- Next app-server follow-ups: guardian/permissions/network review paths and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - MCP runtime analytics fixture
- Added full app-server runtime coverage for `codex_mcp_tool_call_event`: a model-visible MCP HTTP tool is discovered via `tools/list`, invoked by a real agent `function_call`, executed through `tools/call`, persisted as a completed `mcpToolCall` ThreadItem, and emitted as Rust-shaped analytics.
- The regression asserts Rust parity for ids, `tool_name`, `mcp_server_name`, `mcp_tool_name`, `mcp_error_present`, `plugin_id`, terminal outcome, no-review default approval outcome, observed duration, and execution duration.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterMCPToolCallEmitsAnalyticsLikeRust" -count=1 -v`.
- Next app-server follow-ups: guardian/permissions/network review paths and structured CodexErrorInfo/CodexErrKind mapping.

### 2026-07-10 Rust parity progress - failed turn structured error analytics
- Reviewed Rust `CodexTurnEventParams` failed-turn fields, reducer extraction from `TurnCompletedNotification.turn.error.codex_error_info`, and `CodexErrKind` / HTTP status fact propagation.
- Go runtime failures now classify equivalent errors and populate both `turn/completed` notification `codexErrorInfo` and `codex_turn_event` fields: `turn_error`, `codex_error_kind`, and `codex_error_http_status_code`.
- Added mappings for `codexapi.APIError` variants, HTTP status fallbacks, deadline/cancel errors, invalid request errors, and sandbox request errors. Generic unknown Go failures intentionally keep structured error fields null.
- Added failed-turn regressions for deadline timeout and API invalid request parity, including checks for notification error payload and analytics payload.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouterTurnStartFailed(EmitsCodexTurnAnalyticsLikeRust|AnalyticsClassifiesCodexAPIErrorLikeRust)$" -count=1 -v`; `go test ./internal/appserver -count=1`; `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`.
- Next app-server follow-ups: guardian/permissions/network review paths and remaining Rust-specific `CodexErrorInfo` variants that need matching Go runtime states.

### 2026-07-10 Rust parity progress - permissions/network/guardian review analytics
- Reviewed Rust reducer paths for `PermissionsRequestApproval`, command approval amendment decisions, guardian completed-review notifications, and `item_review_summary_key`.
- Go now emits `codex_review_event` for `item/permissions/requestApproval` responses with Rust subject `permissions`, trigger classification, and turn/session resolution.
- Command approval responses now parse object-shaped Rust decisions: `acceptWithExecpolicyAmendment` approves and runs the command with `exec_policy_amendment` review resolution; `applyNetworkPolicyAmendment` maps allow/deny actions to approved/denied `network_policy_amendment`.
- `network_access` and `permissions` reviews no longer denormalize into command tool-item review summaries, matching Rust's summary key filtering.
- Guardian completed-review notifications now emit guardian reviewer `codex_review_event` records and denormalize only command/file/MCP guardian reviews into tool-item summaries.
- Added regressions for exec-policy amendment execution, network-policy amendment review classification, permissions approval review events, and guardian completed-review analytics.
- Verification: `go test ./internal/appserver -run "TestRuntimeRouter(CommandApprovalExecPolicyAmendmentRunsAndEmitsReviewAnalyticsLikeRust|CommandNetworkPolicyAmendmentReviewAnalyticsLikeRust|PermissionsApprovalEmitsReviewAnalyticsLikeRust|GuardianReviewCompletedEmitsReviewAnalyticsLikeRust)$" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(CommandExecutionAnalyticsIncludesUserReviewSummaryLikeRust|FileChangeAnalyticsIncludesUserReviewSummaryLikeRust|CommandExecutionEmitsAnalyticsLikeRust|FileChangeEmitsAnalyticsLikeRust|TurnStartExecApprovalIncludesPrefixRuleAmendmentLikeRust)$" -count=1 -v`; `go test ./internal/appserver -count=1`; `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`.
- Next app-server follow-ups: detailed `codex_guardian_review` telemetry when Go has guardian subagent payloads, remaining Rust-specific `CodexErrorInfo` variants, and managed-network policy persistence once Go exposes matching runtime state.

### 2026-07-10 Rust parity progress - accepted-line repo hash
- Reviewed Rust `accepted_line_repo_hash_for_cwd` and git remote canonicalization.
- Go accepted-line runtime emission now probes `git remote -v` from the thread CWD and fills `repo_hash` when a remote is available.
- Canonicalization now matches Rust's common cases: prefer `origin`, support scp-style remotes, strip `.git`, remove default ports, lowercase GitHub owner/repo paths, and hash with the existing `FingerprintHash("repo", canonicalRemote)`.
- Uploaded `line_fingerprints` remains empty, matching Rust's current privacy-preserving event payload.
- Added helper tests for canonicalization/hash/remote parsing and upgraded the runtime accepted-line regression to assert repo hash when `git` is available.
- Verification: `go test ./internal/appserver -run "Test(CanonicalizeAcceptedLineGitRemoteURLMatchesRust|AcceptedLineRepoHashFromRemoteURLUsesCanonicalRemote|AcceptedLineParseGitRemoteURLs|RuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust)$" -count=1 -v`; `go test ./internal/appserver -count=1` passed on rerun after one Windows TempDir cleanup race; `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`.
- Next app-server follow-ups: detailed `codex_guardian_review` telemetry only when Go surfaces guardian subagent payloads, and remaining Rust-specific `CodexErrorInfo` variants when matching runtime states exist.

### 2026-07-10 Rust parity progress - compaction analytics event
- Reviewed Rust `CodexCompactionEventRequest`, reducer `ingest_compaction`, and `core/src/compact.rs::CompactionAnalyticsAttempt`.
- Added Go telemetry types for Rust-shaped `codex_compaction_event`, including app-server client/runtime metadata, thread lineage, trigger/reason/implementation/phase/strategy/status, error kind/status fields, context-token before/after counts, optional retained image/summary/cache token fields, and timing.
- The analytics client/exporter now accepts compaction events through the same Rust `{"events":[...]}` envelope.
- Runtime manual `thread/compact/start` now passes the initialized JSON-RPC connection id into compaction analytics; auto compaction passes the originating turn connection id and active-context-token snapshot.
- Compaction success emits a completed event after persistence; compact failures emit failed events with Go's existing Rust-style error-kind/status mapping where available.
- Go internal compact enums now map to Rust snake_case values such as `context_limit`, `standalone_turn`, `mid_turn`, `responses`, and `responses_compact`.
- Added manual and auto compaction runtime regressions so trigger/reason/turn id/client metadata are locked.
- PreCompact hook stop now preserves the existing `ErrInvalidHook` caller behavior while adding an internal sentinel so analytics emits Rust-style `status:"interrupted"` and `codex_error_kind:"turn_aborted"`.
- Verification: `go test ./internal/telemetry -run "Test(CodexCompactionEventSerializesExpectedRustShape|AnalyticsEventsClientPostsCompactionUnionEventLikeRust)$" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouter(ThreadCompactStartEmitsCompactionAnalyticsLikeRust|AutoCompactionEmitsCompactionAnalyticsLikeRust|PreCompactStoppedEmitsInterruptedCompactionAnalyticsLikeRust|ThreadCompactStartRunsHooks)$" -count=1 -v`; `go test ./internal/telemetry -count=1`; `go test ./internal/appserver -run "TestRuntimeRouterThreadCompactStart|TestRuntimeRouterThreadCompactStartEmitsCompactionAnalyticsLikeRust|TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust|TestRuntimeRouterConfiguredAnalyticsPostsRustTrackEventsRequest" -count=1 -v`; `go test ./internal/appserver -count=1`; `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`.
- Next app-server follow-ups: exact post-compaction cumulative token accounting, PostCompact stopped timing audit, detailed `codex_guardian_review` once Go has guardian subagent payloads, and managed-network policy persistence when runtime support exists.

### 2026-07-10 Rust parity progress - goal analytics event
- Reviewed Rust `CodexGoalEventRequest`, `GoalEventKind`, reducer `ingest_goal`, and `ext/goal/src/analytics.rs`.
- Added Go telemetry types for Rust-shaped `codex_goal_event`, including app-server client/runtime metadata, thread lineage, stable `goal_id`, event kind, snake_case goal status, token-budget presence, and nullable cumulative accounting fields.
- The analytics client/exporter now accepts goal events through the same Rust `{"events":[...]}` envelope.
- Go goals now persist an internal UUID `goalId` in thread metadata extra while keeping `thread/goal/set/get` response JSON aligned with Rust v2 schema, where `goal_id` is not exposed.
- Runtime `thread/goal/set` now emits `created` for new goals and `status_changed` only when status changes; `thread/goal/clear` emits `cleared` using the removed goal snapshot.
- Status mapping is telemetry-only, so public API values like `budgetLimited` remain unchanged while analytics emits Rust state values like `budget_limited`.
- Added telemetry serialization/null-optionals/HTTP union coverage and app-server coverage for created/status_changed/cleared events plus hidden internal goal id.
- Verification: `go test ./internal/telemetry -run "Test(CodexGoalEventSerializesExpectedRustShape|CodexGoalEventSerializesNullOptionalsLikeRust|AnalyticsEventsClientPostsGoalUnionEventLikeRust)$" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterThreadGoalSetAndClearEmitGoalAnalyticsLikeRust$" -count=1 -v`; `go test ./internal/appserver -run "TestRuntimeRouterThreadGoal(PersistsInThreadStoreAndNotifies|RepairsRolloutOnlyThread|SetAndClearEmitGoalAnalyticsLikeRust)$" -count=1 -v`; `go test ./internal/telemetry -count=1`; `go test ./internal/appserver -count=1`.
- Next app-server follow-ups: implement turn-linked goal accounting and `usage_accounted` telemetry when Go has equivalent runtime accounting state, then continue exact compaction token accounting and remaining Rust-specific error/guardian/policy gaps.
