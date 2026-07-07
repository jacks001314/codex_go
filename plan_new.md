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

- 综合对齐进度：约 89%-90%。
- 非 TUI 主链路实现进度：约 86%-88%。
- Rust fixture/golden 高保真测试进度：约 60%-65%。
- live/provider/MCP/plugin/sandbox/SDK 真机验收进度：约 40%-50%。
- 最大产品缺口：Go 侧已有本地 TUI MVP 状态/渲染层、Bubble Tea 技术路线、root model、Glamour markdown 入口、真实 TTY path 接入、streaming event 回流、approval modal 与 shell permission 回调接线，并已补齐一批 Rust `tui/src` foundation、bottom_pane、history_cell、streaming、diff、exec_cell、resume_picker、theme_picker、mcp_server_elicitation、chat_composer 对应 Go 模块；MCP elicitation 已有 tea modal、interactive broker 和 exec handler 注入；model catalog picker、model reasoning 二级 picker、Plan scope reasoning picker、session picker thread-store/app-server adapter、本地 `/resume`/`/fork`/`/archive`/`/unarchive`/`/delete` picker/action、`/attach`/`/image`/`/url-image` attachment draft UI、结构化 attachment wire、composer running queue 主路径、remote app-server TUI ws/wss 主链路、request_user_input 主路径、auto-timeout、notes、unanswered confirmation 和结构化 answer list 已接入 tea/app/exec 主路径；仍缺 remote unix/server-request/session action 完整对齐、Rust 终端 snapshot、剩余 composer/terminal polish 与更完整的 chatwidget 集成。
- 最大默认路径缺口：普通 `codex exec` 当前在未显式注入 Responses runner 时仍回退 `LocalAgentRunner` stub；Rust 默认是实际 agent/model 运行路径。
- 明确未实现入口：interactive remote app-server 的 unix:// transport/server-request 完整语义、session remote app-server、remote exec-server registration。
- 最近本轮实际验证：`go list -buildvcs=false ./...` 通过；使用仓库内 `.gopath/.gocache/.gotmp` 的 TUI/app/exec/turn/protocol/tool/mcp/model 宽回归通过；全量 `go test ./... -count=1` 本轮通过。历史全量测试中 `internal/appserver` TTY/PTY outputDelta 用例出现过 Windows 时序抖动，单测重跑通过，随后全量重跑通过。
- 最近历史全量测试：`plan.md`/`next_plan.md` 记录 `go test ./...` 与 `go test ./... -count=1` 已多次通过；最近非 TUI 抖动集中在 app/appserver/execserver 的进程、TempDir 或 ConPTY 时序清理路径。

## 完成度统计

统计口径：按用户可见产品能力和系统集成权重估算，不按代码行数或 crate 数直接折算。

| 功能域 | 权重 | Rust 对照 | Go 对应 | 状态 | 估算 | 100% 前剩余缺口 |
| --- | ---: | --- | --- | --- | ---: | --- |
| Core turn runtime | 9% | `core`、`core-api`、`protocol` | `internal/turn`、`internal/tool`、`internal/protocol` | GREEN/IN_PROGRESS | 92% | Rust turn loop、tool ordering、interrupt、compact、guardian、multi-agent fixture 全量回归 |
| App-server V2 RPC | 12% | `app-server`、`app-server-protocol`、`app-server-transport` | `internal/appserver` | GREEN/IN_PROGRESS | 88% | 业务错误 data/code、全量 schema field diff、SDK e2e、更多 notification 顺序 fixture |
| CLI/exec/review | 8% | `cli`、`exec`、`utils/cli` | `cmd/codex`、`internal/cli`、`internal/app`、`internal/exec`、`internal/review` | IN_PROGRESS | 83% | 默认 exec 真实 Responses runner、Rust parser/help/error tests、human/JSONL output fixture |
| Model/provider/auth | 9% | `codex-api`、`model-provider`、`chatgpt`、`aws-auth`、`ollama`、`lmstudio` | `internal/model`、`internal/codexapi`、`internal/chatgptapi`、`internal/auth` | IN_PROGRESS | 78% | live provider matrix、ChatGPT/OAuth e2e、Azure/Bedrock role/IMDS、request body golden |
| Tool/hooks/approval | 8% | `core/src/tools`、`shell-command`、`apply-patch`、`hooks` | `internal/tool`、`internal/applypatch`、`internal/shell` | GREEN/IN_PROGRESS | 88% | Rust tool suite 翻译、approval amendment、network approval、tool output/error JSON shape 锁定 |
| Session/rollout/state | 8% | `rollout`、`thread-store`、`message-history`、`state` | `internal/session`、`internal/rollout`、`internal/history`、`internal/state` | GREEN/IN_PROGRESS | 86% | rollout/thread-store fixture、migration、Windows handle cleanup、cursor/page 边界 |
| Config/features | 5% | `config`、`cloud-config`、`features`、`codex-home` | `internal/config`、`internal/features` | IN_PROGRESS | 80% | profile v2、edit preserve、managed/system/MDM requirements、strict unknown key path |
| MCP | 5% | `mcp-server`、`codex-mcp`、`rmcp-client`、`ext/mcp` | `internal/mcp` | IN_PROGRESS | 78% | live stdio/HTTP/SSE servers、OAuth dynamic registration e2e、session delete/rebuild、error mapping fixtures |
| Plugin/skills/apps/connectors | 6% | `plugin`、`core-plugins`、`skills`、`core-skills`、`connectors`、`ext/*` | `internal/plugin`、`internal/prompt`、`internal/apps`、`internal/mcp` | IN_PROGRESS | 75% | remote marketplace live、plugin cache/materialization、skills telemetry/budget、connector accept e2e |
| Sandbox/exec-server/network | 9% | `sandboxing`、`linux-sandbox`、`windows-sandbox-rs`、`exec-server`、`network-proxy` | `internal/sandbox`、`internal/execserver`、`internal/network` | IN_PROGRESS | 68% | Windows/Linux 真机矩阵、remote exec-server registration、MITM/proxy/certs/policy reload |
| Daemon/remote-control/SDK | 6% | `app-server-daemon`、`app-server-client`、`app-server-test-client` | `internal/appserverdaemon`、`internal/remotecontrol` | IN_PROGRESS | 72% | Python/TypeScript SDK contract、connection file watch、capability token、remote app-server TUI |
| Doctor/install/update/telemetry | 5% | `cli/src/doctor`、`install-context`、`otel`、`analytics` | `internal/doctor`、`internal/install`、`internal/telemetry` | IN_PROGRESS | 78% | snapshot fixture、platform diagnostics、OTEL/live exporter、update/install e2e |
| TUI/interactive product | 8% | `tui` | `internal/tui`、`internal/tui/tea`、`internal/tui/markdown`、`internal/tui/bottom_pane`、`internal/tui/history_cell`、`internal/tui/streaming`、`internal/tui/exec_cell`、`internal/app/interactive.go`、`docs/tui_tech_selection.md` | GAP/IN_PROGRESS | 94% | remote unix/server requests/session actions、snapshot smoke、terminal polish、chatwidget 深集成 |
| Utility/ext/migration | 2% | `utils/*`、`external-agent-*`、`file-*`、`realtime-webrtc`、`code-mode` | `internal/utils`、`internal/filesearch`、`internal/realtime`、`internal/codemode` | IN_PROGRESS | 70% | Rust utility fixtures、external-agent migration、large repo/search perf、IDE integration |

## 全量 Rust/Go 模块覆盖矩阵

| Rust workspace 覆盖 | Go 对应 | 状态 | 备注 |
| --- | --- | --- | --- |
| `cli`、`exec`、`utils/cli`、`arg0`、`apply-patch` | `cmd/codex`、`internal/cli`、`internal/app`、`internal/exec`、`internal/applypatch` | IN_PROGRESS | 命令树基本覆盖；默认 exec stub、parser/help/error fixture 是 P0 |
| `tui`、`collaboration-mode-templates` | `internal/tui`、`internal/tui/tea`、`internal/tui/markdown`、`internal/tui/bottom_pane`、`internal/tui/history_cell`、`internal/tui/streaming`、`internal/tui/exec_cell`、`internal/app/interactive.go`、`internal/prompt` | GAP/IN_PROGRESS | 已有本地 TUI MVP 状态层、slash commands、Bubble Tea root model、Glamour markdown 入口、真实 TTY path 接入、streaming event 回流、approval modal 与 shell permission 回调接线；Rust `tui/src` foundation/bottom_pane/history_cell/streaming/diff/exec_cell/resume_picker/theme_picker/mcp_server_elicitation/chat_composer 已建立 Go 对应包；MCP elicitation、model picker、model reasoning 二级 picker、Plan scope reasoning picker、session picker thread-store/app-server adapter、本地 `/resume`/`/fork`/`/archive`/`/unarchive`/`/delete` picker/action、`/attach`/`/image`/`/url-image` attachment draft UI、structured attachment wire、composer running queue、remote app-server TUI ws/wss 主路径、request_user_input 主路径、auto-timeout、notes、unanswered confirmation 和结构化 answer list 已接入 tea/app/exec 主路径；仍缺 remote unix/server-request/session action、snapshot 完整产品体验和 chatwidget 深集成 |
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
| `exec-server`、`exec-server-protocol`、`network-proxy`、`responses-api-proxy` | `internal/execserver`、`internal/network`、`internal/app` | IN_PROGRESS | fs/process/http 基础有；remote registration、streamResponse、proxy live 待补 |
| `file-search`、`file-system`、`file-watcher`、`git-utils` | `internal/filesearch`、`internal/appserver`、`internal/review`、`internal/utils` | GREEN/IN_PROGRESS | 基础行为覆盖；大仓库性能、ignore rules、watcher e2e 待补 |
| `feedback`、`realtime-webrtc`、`code-mode`、`code-mode-host`、`code-mode-protocol` | `internal/appserver`、`internal/realtime`、`internal/codemode` | IN_PROGRESS | 基础服务已有；IDE/runtime integration 待补 |
| `analytics`、`otel`、`features`、`hooks`、`install-context`、`terminal-detection` | `internal/telemetry`、`internal/eventmap`、`internal/features`、`internal/doctor`、`internal/install` | IN_PROGRESS | doctor/features/hooks 基础深；OTEL/export/install live 待补 |
| `agent-graph-store`、`agent-identity`、`external-agent-migration`、`external-agent-sessions` | `internal/agent`、`internal/auth`、`internal/state` | IN_PROGRESS | graph/identity 基础有；external agent ledger/migration 需专门 fixture |
| `ansi-escape`、`async-utils`、`utils/*`、`test-binary-support`、`thread-manager-sample`、`v8-poc` | `internal/utils`、各 package 内 helper | PARTIAL | 常用工具已覆盖；低层 utility fixture 和未使用实验 crate 需确认是否纳入 100% |

## 明确 P0 差异

- [ ] CLI `codex exec` 默认必须走真实 Responses/OSS provider runner，不得默认返回 `Go Codex exec stub received: ...`。
- [ ] Root interactive 必须提供 Rust `tui` 等价体验；当前行式 session 只能算临时 fallback。
- [ ] `--remote` interactive app-server mode 必须完整实现；ws/wss TUI 主路径已接通，仍缺 unix:// transport、远端 server requests/approval/elicitation 完整语义与真实终端 smoke。
- [ ] `resume/fork/archive/delete/unarchive --remote` 必须接远端 app-server；当前返回 `session remote app-server mode is not implemented in the Go port yet`。
- [ ] `exec-server --remote` / remote registration 必须实现；当前返回 `remote exec-server registration is not implemented in codex_go`。
- [ ] App-server schema fixture 不能只比 method+params，必须覆盖 result、notification payload、业务错误 data/code 和 thread item union 全字段。
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

- [ ] 将 `codex exec` 默认 runner 从 local stub 改为按 config/auth/provider 构造真实 `ResponsesAgentRunner`。
- [ ] 保留 local stub 仅作为测试注入或显式离线 fallback，并确保用户默认路径不误用 stub。
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
- [ ] 完成 remote app-server connection file watch、capability token、server request targeted sink 的端到端测试。

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
- [x] 按 Rust `history_cell` 补 Go 基础包：`base`、`messages`、`plans`、`exec`，支持 display/raw lines、web hyperlink 标注、user/agent/reasoning、plan update/proposed plan、background terminal summary。
- [x] 按 Rust `streaming` 补 Go 基础包：`table_holdback`、`chunking`、`controller`、`commit_tick`，具备 table mutable tail、adaptive catch-up drain、stable queue/tail 两区模型。
- [x] 按 Rust `diff_model`、`diff_render`、`exec_cell` 补 Go 基础模块：FileChange、diff summary/render、exec call lifecycle、exploring grouping、output truncation 和 unified exec interaction preview。
- [ ] 扩展 composer 与终端细节：多行提交策略、paste/focus/resize polish、interrupt/cancel、terminal restore smoke。
- [ ] 补齐剩余 approval/permission 语义：network approval、approval amendment、非 shell tool approval、session-level policy persistence 与 Rust fixture。
- [x] 完善 request_user_input notes/unanswered confirmation 和结构化 answer list。
- [x] 完善本地 session picker `/fork`、`/archive`、`/unarchive`、`/delete` 交互与 store mutation hook。
- [ ] 完善 MCP elicitation richer form editing。
- [x] 支持 TUI image/file attachment draft UI 和 prompt text carry-forward。
- [x] 支持 structured image/file attachment wire：TUI SubmitRequest 保留附件，interactive bridge 转 `turn.TurnUserInput`，exec 构造 user input item，本地图片转 data URL，session content 保留 image/localImage 路径。
- [x] 支持 composer running queue 主路径：任务运行中 Enter/Tab 入队，空闲 Tab 等价提交，turn 完成后自动提交下一条 queued request。
- [x] 支持 remote app-server TUI ws/wss 主路径：`--remote ws://...`/`wss://...` 进入 Bubble Tea TUI，初始化 app-server，首轮空建 thread，用户输入走 `turn/start`，结构化 text/file/localImage/remote image 输入保留在 `TurnStartParams.input`，远端 thread/turn/item/delta/error/warning 通知转成 TUI stream events，支持 auth token env。
- [ ] 支持 diff/file change display、terminal/background terminal panel。
- [ ] 支持 interrupt/cancel/continue、turn steering、compact、rollback、fork/resume/archive/delete。
- [ ] 补齐 `--remote unix://` app-server transport：Go 当前 Unix socket app-server 是 JSON-RPC line 协议，需要单独客户端，不是 websocket transport。
- [ ] 补齐 remote app-server TUI server requests：command/file/network approval、MCP elicitation、request_user_input、auth refresh、targeted response 与 Rust `PendingAppServerRequests` 行为。
- [ ] 实现 session commands 的 remote app-server handoff。
- [ ] 补 TUI snapshot/golden：快捷键、布局、状态提示、approval、MCP elicitation、error display。
- [ ] 修复 `request_plugin_install` 在 `codex-tui` client 下被禁用的问题，或与 Rust 当前产品策略完全一致并补 fixture。

验收：

- [ ] Go root `codex` 交互体验达到 Rust `tui` 等价功能。
- [ ] remote TUI 和 local TUI 共用 app-server/turn/runtime 语义；ws/wss remote turn 主链路已接入，unix/server request/session action 尚未完全对齐。
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
- [ ] Windows ConPTY：TTY、resize、interrupt、terminate、output drain。
- [ ] Linux bwrap：read-only、workspace-write、full-access、tmpfs masking、ro-bind-data、cwd/env/exit code。
- [ ] Linux Landlock/seccomp：capability detection、network deny、filesystem allow/deny、fallback/unsupported 文案。
- [ ] Linux execve wrapper：DGRAM/STREAM handshake、SCM_RIGHTS FD passing、Run/Escalate/Deny。
- [ ] Exec-server：process lifecycle、PTY/resize、stdout/stderr streaming、fs/http remote env、sandbox backend request。
- [ ] 实现 remote exec-server registration，替换当前 `not implemented`。
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

1. [ ] App-server protocol/schema fixtures：method、params、result、notification、business error、ThreadItem union。
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
- CLI `exec` 默认 local stub 会掩盖真实 provider/runtime 差异，是当前最高优先级风险。
- App-server 已实现很深，但 SDK/IDE contract 和 business error data 如果没有 fixture，容易产生隐蔽 wire 分叉。
- Windows sandbox/WFP/ACL 和 Linux bwrap/Landlock/seccomp 依赖宿主能力，必须用 gated 真机矩阵而不是普通 unit 代替。
- Live provider/auth/MCP/plugin/connector 测试依赖网络和凭据，默认全量只能覆盖 mock/fixture。
- Rust workspace 包含部分实验或辅助 crate，如 `v8-poc`、`thread-manager-sample`、`test-binary-support`；100% 前需明确是否需要 Go 等价实现、测试替代，或记录为非产品目标。

## 下一轮执行顺序

1. [ ] 修复 `codex exec` 默认 runner，接入真实 Responses/OSS provider，保留 local stub 仅用于测试。
2. [ ] 翻译 Rust CLI parser/help/error/exit-code tests，锁定命令树。
3. [ ] 扩展 app-server schema fixture 到 result/notification/business error 全字段。
4. [ ] 继续推进 `internal/tui/tea`：composer polish、chatwidget 深集成、remote app-server server requests/unix transport 和 raw terminal smoke/snapshot。
5. [ ] 实现 session remote app-server mode，并把 `/resume`/`/fork`/`/archive`/`/delete` handoff 到远端 app-server。
6. [ ] 实现 remote exec-server registration。
7. [ ] 建立 provider/auth mock golden + live-gated matrix。
8. [ ] 建立 Windows/Linux sandbox 真机矩阵。
9. [ ] 建立 MCP/plugin/skills/apps live-gated matrix。
10. [ ] 导入 rollout/thread-store/state Rust fixtures。

## 工作日志

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
- [ ] TUI 剩余缺口：remote unix:// transport、remote app-server server requests/approval/elicitation、remote session action handoff、剩余 composer/terminal polish、chatwidget 深集成、Rust snapshot/真实终端 smoke fixture。
