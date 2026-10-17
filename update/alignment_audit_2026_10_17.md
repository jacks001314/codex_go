# Rust/Go 功能对齐审计（2026-10-17，round 91）

## 1. 结论摘要

- **Rust 上游目标**：`D:\qax\reagent\dev\git\codex` HEAD = `e16b631d08`（#54754），`git pull origin main` 返回 `Already up to date`。
- **Go 仓**：`D:\qax\reagent\dev\codex_go`，本轮从 `634daa4e` 继续。
- **parity 控制面**：本轮开始时 `go test ./parity` **6 项失败**，现已 **全绿**（见 §3）。
- **协议 oracle**：`appserver/schema/precomputed/*.zst` 已按 Rust 重新 vendored；Go 侧重新生成的 TypeScript/JSON/internal-JSON 导出树与 Rust 预计算产物**逐文件一致**（`TestPrecomputedExportsAreWrittenToDiskLikeRust` / `...Experimental...` 通过）。
- **提交账本覆盖**：`update/r90_commit_ledger_2026_10_16.tsv` 共 222 行；本轮把 2 行从 `implementable` 推进到 `landed`。账本**不是** `970b7f2ff4..e16b631d08` 全量 2639 提交的逐笔认证，只是按功能面筛选后的跟踪表，因此本文件按"协议/数据面 + 功能面"给出证据，不宣称逐提交认证。

### 账本状态分布（本轮更新后）

| 状态 | 行数 |
| --- | --- |
| `landed` | 78 |
| `not_applicable` | 75 |
| `blocked_precondition` | 20 |
| `complete` | 15 |
| `implementable` | 11 |
| `partial` | 12 |
| `AlreadyImplemented` | 10 |
| `unverified` | 1 |

## 2. 方法与证据源

1. **快照差分**（`parity/`）：Rust 工作区成员、关键文件 blob 哈希、golden fixture 目录文件数、TUI 快照清单、协作模式模板、预计算协议导出。
2. **协议 oracle**（`appserver/schema/precomputed/*.zst`）：Rust 生成的跨语言 schema 产物，Go 必须能重新生成同一棵树。
3. **提交账本**：`update/r90_commit_ledger_2026_10_16.tsv`，逐 PR 记录 landed / not_applicable / implementable / partial。
4. **行为测试**：每个落地项都附带 Go 单测，覆盖 Rust 提交描述的每条规则。

复现命令：

```powershell
$env:CODEX_RUST_ROOT='D:\qax\reagent\dev\git\codex\codex-rs'
cd D:\qax\reagent\dev\codex_go
go test ./parity -count=1
```

## 3. 本轮修复：parity 六项漂移

| # | 失败 | 根因 | 处理 |
| --- | --- | --- | --- |
| 1 | `TestRustCollaborationModeTemplatesMatchGo` | Go 工作树 `tui/chatwidget/prompt_for_plan_mode.md` 被 CRLF 化（`.gitattributes` 已声明 `eol=lf`，但工作树是旧 checkout） | 删除后按属性重新 checkout；内容与 Rust `63d213884d`/`HEAD` 的 `plan.md` 均 9184 字节且逐字节相等 |
| 2 | `TestRustGoldenFixtureRootsSnapshot` | `core/tests/suite` 新增 3 个文件（#54654 dynamic-tool 并行套件 + CodeModeOnly/Direct 快照） | 352 → 355，并补记来源 |
| 3 | `TestRustWorkspaceMembersSnapshot` | Cargo workspace 新增 `ext/multi-env-copy-file`（#54674） | 按 Cargo.toml 顺序加入快照 |
| 4 | `TestRustCriticalFileHashesSnapshot` | 哈希对 CRLF 敏感 + `Cargo.toml`/`exec/src/lib.rs` 上游变更 | 哈希前统一 CRLF→LF（与注释声明的 blob 语义一致，且跨平台稳定）；重算 `Cargo.toml`、`exec/src/lib.rs` 两个 pin |
| 5 | `TestPrecomputedAppServerExportsMatchRustTarget` | Rust 预计算产物在 #54686/#53784 后变化 | 按 Rust 树重新 vendored，并更新 `appserver/schema_test.go` 的 SHA 断言 |
| 6 | `TestRustTUISnapshotManifestCoversPrioritySurfaces` | #54742 新增 history-search absorbed-slash 快照 | composer 目录 30→31，总数 1432→1433 |

修复后 `go test ./parity -count=1` → `ok codex_go/parity`。

## 4. 本轮落地的功能项

### 4.1 #53784 转发客户端 voice session id（`6786dccbe7`）

- Rust：`ThreadRealtimeStartParams.voiceSessionId` → `ConversationStartParams.voice_session_id` → `voice-session-id` 请求头（WebSocket / WebRTC / existing-call），与 `realtimeSessionId` 的 `x-session-id` 相互独立。
- Go：
  - `realtime/realtime.go`: `StartParams.VoiceSessionID`（wire 为可空 `voiceSessionId`）、`SessionConfig.VoiceSessionID`，并在 `Normalized` 中解析。
  - `realtime/wire.go`: 抽出 `applyRealtimeSessionHeaders`，同时写 `x-session-id` 与 `voice-session-id`。
  - `realtime/transport.go`: WebRTC call 创建请求复用同一头集合（对应 Rust `RealtimeStart::extra_headers`）。
  - 测试：`realtime/voice_session_id_like_rust_test.go`（序列化可空语义、两 id 头互不覆盖、WebRTC call 请求头）。
- 协议证据：重新生成的导出树与 Rust 预计算产物一致，说明 `voiceSessionId` 的 schema 位点/可空性与上游一致。

### 4.2 #54379 TUI 输入预设配置（`1544ffc79d`）

- Rust：`tui.presets` 以单个数字 `0`–`9` 为键，值含 `input` 与可选 `label`；校验：非空、去前导空白后不得以 `!` 开头、UTF-8 字节数 ≤ 8000；配置 schema 暴露并在 TUI local settings 中保留。
- Go：
  - `config/tui_presets.go`: `TUIPreset`、`Validate`、`ParseTUIPresets`、`ValidateTUIPresets`、`(*Config).TUIPresets`；错误字符串与 Rust 一致（`tui.presets.<slot>: <reason>`）。
  - `config/config.go`: `tui.presets` 加入已知字段；`LoadEffectiveWithOptions` 装载时校验。
  - `tui/tea/model.go` + `app/interactive.go`: `QuickDials` 随 `SettingsWriteResult` 进入 TUI 本地设置，并在配置 reload 时被保留。
  - 测试：`config/tui_presets_test.go`（非法槽位/空输入/`!` 命令/超长/未知字段/缺失 input 六个拒绝用例 + 字节长度边界）。

### 4.3 #54315 TUI Markdown 颜色可读性（`86ee40c380`，markdown 面）

- Rust：`MarkdownStyles::for_theme` 对每个主题前景调用 `readable_color_on(fg, None)`；`readable_color_on` 在 background=None 时回退到终端默认背景。
- Go：
  - `tui/style.go`：`ReadableColorOn` 在 background 为 nil 时改用 `defaultTerminalColorsRGB()` 的默认背景（此前 nil 等于"不校正"，与 Rust 语义不符）。
  - `tui/theme_scope_foreground.go`：新增 `ThemeScopeForegroundValueReadable`。
  - `tui/markdown/render.go`：`applyThemeMarkupColors` 接收颜色层级并统一走可读化路径（内联代码仍保留原始语法色）。
  - 测试：`tui/readable_color_like_rust_test.go`（默认背景校正、未知背景保持原色、ada 主题标题色被校正到 ≥4.5:1）。
- **剩余 drift**：Go 的 `ProposedPlanCell` 直接渲染原始 Markdown、没有计划底纹（shaded plan surface）载体，因此 #54315 的"计划背景上的前景校正"这一半尚无落点；已在该行状态记为 `partial`。

## 5. 未落地缺口矩阵（本轮更新后）

### 5.1 implementable（可落地，尚未开工）

| sha | PR | 标题 | 证据摘要 |
| --- | --- | --- | --- |
| `b687844674` | #54264 | Track Guardian transcript admission across failed reviews (#54264) | TR-13 @5608e096: implementable (appserver face, medium, likely) - Rust core/src/guardian/review_session.rs keeps pending review input when inferenc... |
| `dffa0deb69` | #54216 | Show image previews in the fullscreen transcript (#54216) | TR-13 @5608e096: implementable (TUI face, large/R3) - Rust adds tui/src/transcript_image.rs (169 lines, ImagePlacement/FIRST_IMAGE_ID) and tui/src/... |
| `4ca84a1069` | #54135 | Allow large file writes over direct exec-server WebSockets (#54135) | TR-13 @5608e096: implementable (execserver face, small-medium) - Rust exec-server/src/connection.rs adds from_direct_websocket() which deliberately... |
| `dec54912f9` | #54114 | Report terminal completion states and blocking reasons (#54114) | TR-13 @5608e096: implementable (TUI face, medium) - Rust adds the bottom_pane_view trait method program_status_blocked_kind() (default None) and im... |
| `b7ba02f880` | #54059 | Support store-owned preparation when resuming threads (#54059) | TR-13 @5608e096: implementable (appserver face, medium, likely) - Rust thread-store/src/store.rs adds ThreadStoreResumePreparation (type-erased Box... |
| `534c70863c` | #54048 | Defer folder trust checks until starting a task from agents overview (#54048) | TR-13 @5608e096: implementable (app face, small-medium, likely) - Rust resolves directory trust before loading defaults when a session is started f... |
| `835cd7552d` | #53987 | Skip service backoff for stale Guardian authorization retries (#53987) | TR-13 @5608e096: implementable (appserver face, small-medium, likely) - Rust ext/guardian-reviewer/src/retry.rs treats GuardianReviewError::StaleAu... |
| `7eed30ad1b` | #53879 | Preserve running turn recovery across thread settings updates (#53879) | TR-13 @5608e096: implementable (appserver face, medium, likely) - Rust core/src/session/thread_settings.rs only clears last_started_turn_id when no... |
| `c22a06c180` | #53859 | Record bucketed terminal sizes in TUI telemetry (#53859) | Round 93 landed（见 §9）；原 TR-13 评估：implementable (telemetry + TUI faces, medium, two halves) - Rust otel/src/metrics/client.rs adds requires_analytics_consent (only ... |
| `2b2d792305` | #53797 | Refresh Guardian reviews when review context changes (#53797) | TR-13 @5608e096: implementable (appserver face, medium, likely) - Rust core/src/guardian/review_request.rs widens the invalidation predicate from a... |
| `440312f113` | #54681 | Keep TUI session selections bound to their backend (#54681) | AC-b5 @791cca8d: 唯一落在已授权写面(tui/**)的可落地大项。Rust 新增 tui/src/backend_id.rs:5 BackendId(uuid)(进程内逻辑后端身份, 不上 wire) + resume_picker.rs(+92) + lib.rs(+117)... |

### 5.2 partial（部分落地，存在 drift）

| sha | PR | 标题 | 证据摘要 |
| --- | --- | --- | --- |
| `f0c5077c34` | #54316 | Exclude non-message items from the Guardian transcript | appserver/guardian_reviewer.go:891-901（渲染层排除）；保留层排除 Go 无载体；队长 RC 发现无 shipped 测试守着，已回派车道 C 补回归 |
| `3ee99cde70` | #54046 | Record MCP approval cancellation sources | A 面 approval_source landed（telemetry/mcp_call_metrics.go:74,89,246）；B 面 context_changed Go 0 命中 ⇒ drift |
| `f87ba04ec3` | #53991 | Report the reason for ChatGPT token refresh requests | auth/oauth.go + remotecontrol/auth.go（ChatGPT 面）；proactive/scheduled 面 Go 无主动刷新循环 ⇒ drift；app 侧接线顺延（app/remote_tui.go 本轮归 TUI） |
| `80ad49e3b7` | #54056 | Isolate the native root pool and custom CAs per client | network/proxy_native_roots_pool.go + execserver/custom_ca.go + network/proxy_server.go；execserver 侧「跨客户端泄漏」在 Windows 本不发生（SystemCertPool 每次新空实例）⇒ 该... |
| `bb64b894fd` | #54175 | Retired feature overrides stay observable | features/legacy.go:31-40（features 面）；worktree/config/tui 三面未做（未开工+原因，不写延后） |
| `141bf7680d` | #54387 | Status-surface commands take inline items, --clear and --colors | tui/tea/status_controls.go + chatwidget/slash_dispatch.go + bottom_pane/slash_commands.go；队长 RC 撤 status_controls ⇒ :50/:88/:128/:160 FAIL、撤内联契约 ⇒ ... |
| `622e9e3696` | #51786 | Preserve sandbox overrides across execution requests (#51786) | landed: wire plus gating validation — new execserver/sandbox_override.go (47) + execserver/server.go:495-501,2136-2141 + sandbox_process_windows.go... |
| `816fa702ec` | #53670 | Carry response_id and usage on incomplete responses (#53670) | AC-1 (lane mc-r90-acore, r90ac1 @ fe19d8c5), integrated in 05fdcd50: model/responses_stream.go now carries response_id + usage on ResponseIncomplet... |
| `8c818cbed4` | #52164 | Make environment context date and timezone configurable | TR-12 (lane r90tri10 @ 3f3c44f7): the flag include_environment_context_time is registered and whitelisted (config/config.go:99-107, config_test.go:... |
| `12fd621f62` | #54094 | Preserve specific MCP failure codes in metrics and spans | TR-12 (lane r90tri10 @ 3f3c44f7): the main half is already implemented - telemetry/mcp_call_metrics.go:89-124 has WithRequestErrorCode and the 16-c... |
| `79773e8826` | #52215 | Enforce application network policy in standalone MCP commands | TR-12 (lane r90tri10 @ 3f3c44f7): already implemented for the command paths - app/mcp.go:868-927 publishes the application network policy per path ... |
| `86ee40c380` | #54315 | Keep TUI Markdown colors readable against their backgrounds (#54315) | round-91 (markdown half landed): tui.ReadableColorOn now resolves a nil surface against the terminal default background like Rust (`background_rgb(... |

### 5.3 unverified（待验证）

| sha | PR | 标题 | 证据摘要 |
| --- | --- | --- | --- |
| `8af93416dd` | #53795 | Use the bundled model catalog in the MCP startup invalidation test (#53795) | TR-13 @5608e096: unverified - Rust change is test-only (core/src/thread_manager_tests.rs +4) and pins config.model_catalog to codex_models_manager:... |

### 5.4 blocked_precondition（存在前置阻塞）

| sha | PR | 标题 | 证据摘要 |
| --- | --- | --- | --- |
| `2a6ecbb93c` | #54257 | Trim realtime transcript by frame recency | drift：Go wire 层不解析帧时间戳（realtime/realtime.go:1877-1880 TranscriptEntry 仅 Role/Text），累积器无 8 KiB 预算 ⇒ 帧新近度淘汰无载体（上游实为 app-server 端点侧改动） |
| `c2eb1f42a0` | #51842 | Add a sandbox integrity runner with outcome and timing metrics (#51842) | blocked-precondition (verified by lane B batch B-b6 reconnaissance, no code changed): the commit is an orchestration + telemetry layer over facts t... |
| `0ada5d8806` | #52350 | Expose experimental durable thread read state in the app server | blocked-precondition (lane A batch A-b7 reconnaissance, no code changed): the response fields (ThreadReadResponse.readState, ThreadListResponse.rea... |
| `c3d3b142d1` | #52778 | Prompt header and jump-to-entry (#52778) | T9 (TUI lane r90tui10 @ 50a53dc3): prompt_header / PromptHeader / JumpToEntry / cancel_search all 0 hits. Evidence: update/r90tui_2026_10_16.md sec... |
| `9f3d25588b` | #53201 | Restricted input mode for unavailable threads (#53201) | T9 (TUI lane r90tui10 @ 50a53dc3): RestrictedInputMode / UnavailableThread / available_when_thread_unavailable all 0 hits; agents_navigation appear... |
| `dcd9e95459` | #54380 | Add /presets quick dials (#54380) | T9-chain (TUI lane r90tui11 @ f6bf18a2), integrated in 05fdcd50: QuickDial / quickDial / quick_dial all 0 hits; the whole quick-dials subsystem (ch... |
| `111362a6a0` | #54381 | Preset shortcut HUD (#54381) | T9-chain (TUI lane r90tui11 @ f6bf18a2): ShortcutHud / shortcutHud / shortcut_hud all 0 hits (main face blocked on #54380); the chord half is N-A b... |
| `39f4277087` | #52270 | Footer text selection (#52270) | T9-chain (TUI lane r90tui11 @ f6bf18a2): RenderedSelection / FooterSelection / RegionAction etc all 0 hits, and this is a DESIGN-level block, not a... |
| `cc7ba33601` | #52990 | Config panel in the TUI (#52990) | T9-chain (TUI lane r90tui11 @ f6bf18a2): ConfigPanel / ConfigPreference / ConfigToggle all 0 hits. Only the persistence half has a Go carrier (tui/... |
| `3df96b085c` | #54363 | Filter uninspectable network roots (MXC) | TR-10 (same table): two preconditions, both external - (1) no Go carrier for the MXC network-root inspection (networkRoots / uninspectable / Implic... |
| `4aa94dce27` | #52707 | Split the Windows MXC crates (mxc-sandbox) | TR-10 (same table): the whole mxc-sdk / appcontainer_common / learning_mode_windows / wxc_common / SecurityEnvironmentApi surface is 0 hits in Go, ... |
| `1c7c43cd85` | #51819 | Move MXC to the 1.0.0 SDK | TR-10 (same table): same carrier gap as 52707 (SDK symbols 0 hits) plus the same Windows+MXC node requirement. |
| `5b3c1522f8` | #52381 | Per-session routing for gRPC code mode | TR-10 (same table): blocked on 52723 - the gRPC-over-stdio transport is not landed, so a per-session route has no host. x-code-mode-route / CodeMod... |
| `49517` | #d42056091a | keymap action agents.fork (Rust-only) | T-b12 (tui lane r90tui13 @ 63e3e264): Go has no fork in the agents dashboard - tui/tea/agents_overview.go:431-481 keyMatches list lacks it and the ... |
| `54381` | #111362a6a0 | keymap action chat.open_presets (Rust-only) | No carrier in Go: git grep -F open_presets -- '*.go' = 0 hits; the upstream carrier (#54380/#54381, quick_dials subsystem) does not exist in Go and... |
| `46734` | #df7f717c85 | keymap actions global.find_transcript + global.focus_activity (Rust-only) | No transcript search or activity-group focus behavior in Go; tui/tooltips.txt:43 still advertises find_transcript and tooltip_render.go:104 drops t... |
| `46749` | #de6ae71ad4 | keymap action pager.find (Rust-only) | Go pager context exposes only scroll/page/half_page/jump_top/jump_bottom/close/close_transcript (tui/keymap.go:137-148) and the transcript pager ha... |
| `42194` | #5a0419edb5 | keymap action vim_normal.enter_replace_mode (Rust-only name) | Behavior exists under the Go name vim_normal.replace_mode with consumer tui/tea/vim_mode.go:346; renaming or aliasing requires the same batch to to... |
| `40785` | #98ee29c7cd | keymap actions vim_normal.find_forward/find_backward/till_forward/till_backward (Rust-only names) | All four behaviors exist under the Go names find_char_forward/find_char_backward/till_char_forward/till_char_backward (consumers tui/tea/vim_mode.g... |
| `41586` | #63d213884d | keymap actions vim_search.forward/backward/next/previous (Rust-only context) | Go folds these into the vim_normal context as search_forward/backward/next/prev (tui/tea/vim_mode.go:264/269/272/277, consumers :369/379/381/383), ... |

## 6. 本轮顺手核对的上游提交

| SHA | PR | 结论 |
| --- | --- | --- |
| `0e67502123` | #54686 | Go 已实现 `McpServerStatus.toolListMeta`（`mcp/api.go`，含首屏 `_meta` 语义与保留缓存的测试 `mcp/tool_list_meta_test.go`）；本轮只需重新 vendored 预计算产物 |
| `a083a1e49e` | #54679 | Go 已有 `non_interactive` 取消原因载体 |
| `beae473be8` | #54654 | Go 已有 `app/dynamic_tools.go` 及并行执行覆盖 |

## 7. 风险与下一步

1. **剩余 11 个 implementable**：优先处理协议/核心面（#54059 thread-store 恢复准备、#54135 exec-server 直连 WebSocket 大写入、#53987 Guardian 重试退避、#53797 Guardian 审查失效、#53879 running turn 恢复），再处理 TUI 面。
2. **12 个 partial**：需要逐条收敛 drift，尤其是 #54316 Guardian transcript 排除、#54046 `context_changed`、#54175 retired feature overrides。
3. **20 个 blocked_precondition**：需在报告中逐条给出可验证的 N/A 理由，而不是保持 "blocked"。
4. **baseline 提升**：`parity/baseline.json` / `parity.json.rustBaseline` / `parity/domains.json.targetRustCommit` 仍指向 `970b7f2ff4`，待 open 项收敛后统一提升到 `e16b631d08`。
5. **账本覆盖范围**：若要求"逐提交认证"，需为 2639 个提交建立全量分类（当前 222 行仅为筛选跟踪）。

---

# Round 92 增量（2026-10-17，lead）

## 8.1 本轮落地

### #54681 TUI 会话选择绑定后端（`440312f113`，commit `dfb3397d`）

- 新增 `tui/backend_id.go`：进程内 `BackendID`（Rust `backend_id.rs`），`ProcessBackendID()` 代表启动所有者。
- `SessionTarget` 增加 `BackendID` 与 `EnsureBackend`，拒绝文案与 Rust 完全一致：
  `This session belongs to a different backend. Open the session picker again.`
- `SessionPickerState` 为每个选择打戳（`NewSessionPickerStateForBackend` 供显式所有者使用）；`tea.Model` 新增 `Options.BackendID`（缺省为进程所有者），六处交互式 target 全部打戳。
- 本地与 remote TUI 各自持有后端身份；两套 resume/fork/archive/unarchive/delete handler 在触碰 store 或打开客户端**之前**拒绝外来 target。
- 测试：`tui/backend_id_test.go`（3 个）、`app/backend_ownership_like_rust_test.go`（5 种 kind）。
- 结构 N/A：embedded fallback 抑制半。Go 没有由 picker 发起的辅助 app server（`app/daemon_startup.go` 启动时探测一次，`app/remote_tui.go` 复用启动 endpoint），因此没有可抑制的 fallback。

### #54114 终端完成态与阻塞原因（`dec54912f9`，commit `b72afee2`）

- Go 此前**完全没有** `terminal_program_status` 模块；本轮整体移植并叠加 #54114 扩展。
- `tui/program_status.go`：Idle/Working/Blocked(kind)/Done/Error；OSC 7501 输出 `state=done|error` 与 `:kind=permission|question|auth`；保留 OSC 21337 标签与指示色（done/error → Idle）；进程级去重缓存、invalidate/clear、iTerm detail 清洗与 240 字符上限。
- `tui/tea/program_status.go`：从 modal 栈分类阻塞（approval=permission；MCP elicitation / app-link 非 auth=question；app-link auth=auth；user_input=question；异步问题仅在空闲时=question）；pending steers / unified exec / active goal 保持 working；Done/Error 保留到键盘、粘贴、鼠标按下（鼠标移动不确认）。
- 状态迁移：turn.started→idle、实时完成→done、实时失败→error、中断→idle；非当前线程的完成事件不改变上报状态。
- 测试：`tui/program_status_test.go`（4 个）、`tui/tea/program_status_like_rust_test.go`（4 个）。
- 结构 N/A：Rust 的 `from_replay`（Go `TurnCompletedMsg` 无该标志）与 `delayed_approval_requests`（Go 直接打开审批 modal，无延迟队列）。

## 8.2 本轮新增的“缺失前置层”结论

| 项 | 结论 | 证据 |
| --- | --- | --- |
| `#54048` `534c70863c` | 降级为 `blocked_precondition` | Go 没有交互式目录信任同意步骤：`tui/onboarding.NewTrustDirectoryPrompt` 只有测试调用，信任只经 config layer stack 解析；`codex agents` 独立 dashboard 不初始化 session |
| `#53859` `c22a06c180` | Round 92 判定为 implementable；Round 93 已落地 | 补齐 `codex.tui.start`、尺寸桶、turn_start / 15s resize settle 与 consent 语义，见 §9 |

## 8.3 账本状态（round 92 更新后）

landed 80 / not_applicable 75 / blocked_precondition 21 / complete 15 / partial 12 / AlreadyImplemented 10 / implementable 8 / unverified 1（共 222 数据行）。

## 8.4 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./tui/ ./tui/tea -run "ProgramStatus|Backend"` | ok |
| `go test ./tui/...` | 仅既有 `TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge` 失败（pristine worktree 可复现） |
| `go test ./app -run BackendOwnership` | ok |

远端：`refs/heads/main` = `b72afee2`。

---

# Round 93 增量（2026-10-17，lead）

## 9.1 已落地

### #53859 终端尺寸分桶遥测（`c22a06c180`）

- `telemetry/metrics_client.go`：新增 `MetricsClientOptions.RequiresAnalyticsConsent` 与 `CounterWithAnalyticsConsent`，与 Rust `counter_with_analytics_consent` 一致：consent 只作用于 built-in exporter，explicit OTLP 不受 analytics 开关影响。
- `telemetry/otel_provider.go`：用未解析的 `settings.MetricsExporter` 判定是否 Statsig，把 `RequiresAnalyticsConsent` 带到 metrics client。
- `otelinit/otelinit.go`：metrics exporter 仅在 `Statsig && !analyticsEnabled` 时置 None；显式 OTLP 在 analytics 关闭时仍构建 provider（与 Rust #53859 对齐）。原 `TestBuildProviderRequiresAnalytics` 的“OTLP 必须 analytics”期望已按上游改掉。
- `tui/terminal_size_telemetry.go`：`TerminalSizeTags`（columns/rows 桶）、`TerminalSizeTelemetryEnabled`、`TerminalSizeRead`、`TerminalTelemetryName` / `TerminalMultiplexerTag`、`RecordTerminalSize`（追加 launch context + reason，走 consent-aware counter）。
- `app/tui_telemetry.go`：TUI 自建 OTEL provider，记录一次 `codex.tui.start`，标签包含 daemon settings、`terminal_columns` / `terminal_rows`、`daemon_selection_reason`、`daemon_auto_start`、`app_server_mode`、`terminal_name`、`multiplexer`；返回的 launch context 只剥掉两个尺寸桶。
- `app/interactive.go` / `app/remote_tui.go`：embedded / local daemon / explicit remote 三条启动路径都接入；mode 分别为 `in_process` / `local_daemon` / `remote`。
- `tui/tea/model.go` + `tui/tea/terminal_size_telemetry.go`：`turn.started` 仅对当前 active thread 的新 turn id 记录 `turn_start`；resize 变化启动 15s settle，settle 时重新读取实际尺寸，桶变化才记录 `resize`，同桶只更新 settled 状态；未启用/无 launch context 时清空 pending 并同步 settled 桶。
- 测试：
  - `telemetry/metrics_test.go::TestCounterWithAnalyticsConsentOnlyGatesBuiltInExporter`
  - `otelinit/otelinit_test.go::TestBuildProviderConsentOnlyGatesBuiltInExporter`
  - `tui/terminal_size_telemetry_test.go`（4 例：桶边界、enabled 矩阵、记录标签、终端/复用器分类）
  - `tui/tea/terminal_size_telemetry_like_rust_test.go`（3 例：turn_start 去重/线程过滤、resize settle/同桶静默/重新计时、exporter+consent 门禁）
  - `app/tui_telemetry_like_rust_test.go`（start 标签与 context、selection_reason 全分支）

### 结构差异（记录为适配，不改变可观察行为）

- Rust 的 `enabled(&app.config)` 在每轮循环读取可热重载的 Config；Go 的 tea Model 没有同等的 live Config 句柄，`terminalSizeExporter` / `terminalSizeAnalyticsEnabled` 取启动时解析值。记录时仍通过 `CounterWithAnalyticsConsent` 读取当前全局 client 的 exporter 属性，因此 provider 替换不会绕过 consent。
- Rust 的 `read_size()` Unix 用 `window_size()`、Windows 用 `size()`；Go 复用仓库既有的 `charmbracelet/x/term.GetSize(stdout)`，两侧都是“读取失败记 unknown 桶”。

## 9.2 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./tui -run "TerminalSize|TerminalTelemetry"` | ok |
| `go test ./tui/tea -run "TerminalSize"` | ok |
| `go test ./otelinit -run BuildProvider` | ok |
| `go test ./telemetry -run AnalyticsConsent` | ok |
| `go test ./app -run "InteractiveTUIStartTelemetry|InteractiveDaemonSelectionReason"` | ok |
| `go test ./tui ./tui/tea ./otelinit ./telemetry` | 仅既有环境失败 `TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge` |

# Round 94 增量（2026-10-17，lead）

## 10.1 已落地

### #53987 Guardian 过期授权重试跳过服务退避（`835cd7552d`）

- `appserver/guardian_reviewer.go`：`Review` 收敛为一次 `store.Start` + 统一 attempt 循环（`guardianMaxReviewAttempts = 3`），所有 attempt 共享同一个 `reviewCtx` 截止时间与 attempt 预算。
- stale authorization：agent 成功返回后 `guardianReviewContextChanged` 检测到 transcript / root authorization 变化时置 `contextChanged = true`，用 `runtime.Gosched()` 让出执行权，检查取消与共享 deadline 后直接重试，**不支付服务退避**（Rust `wait_before_guardian_retry` 的 StaleAuthorization 分支）。
- recoverable session error：`guardianReviewRetryableError` 按 Rust `should_retry_guardian_review` 的 session 分支判断（RateLimitExceeded / FlexUnavailable / ServerOverloaded；transport / stream / retryable / api 且 status 为空或 408/429/5xx），退避为 `200ms * 2^(attempt-1) * [0.9,1.1)`，并取 `Retry-After` 与退避的较大值；等待期间响应取消与 deadline。
- Parse 失败同样计入共享 attempt 预算，并沿用 Rust 的指数退避。
- 超时/中止收口抽成 `finishTimedOut` / `finishAborted`，多次 attempt 只产生一条 Guardian assessment 终态。

### #53797 评审上下文变化时刷新 Guardian 评审（`2b2d792305`）

- `appserver/guardian_reviewer.go`：`guardianReviewContextChanged` 在每次 attempt 结束后重新读取 host 的 transcript 与 root user authorization 快照，与本次 attempt 捕获的版本比较；变化即判定为 stale authorization 并在新 attempt 中用新证据重建 prompt。
- 结构差异：Rust 比较 `user_message_revision` / `guardian_review_context_revision` / root authorization version 等修订号；Go 线程层没有对应的修订计数器，因此比较渲染后的字符串快照（`slices.Equal`）。两者的观测点相同——每轮 attempt 从 host 的实时来源重新取证据。

### #54046 B 面 `context_changed` 指标标签（`3ee99cde70`）

- `appserver/guardian_review_metrics.go`：`guardianReviewMetricAttribution` 新增 `ContextChanged`；`codex.guardian.review.count` 追加 `context_changed=true|false`，duration / token / TTFT 直方图不带该标签，与 Rust `metrics.rs` 只在 counter 上 push `("context_changed", bool_tag(...))` 一致。
- Rust 的 `attempt_count` 只存在于 analytics 结果，不是指标标签；Go 同样不把它作为标签暴露。

### 测试

- `appserver/guardian_retry_like_rust_test.go`：
  - `TestGuardianReviewRefreshesChangedContextLikeRust`：首轮 transcript 变化 → 第二轮拿到新 transcript，最终 allow，counter `context_changed=true`。
  - `TestGuardianReviewStaleRetrySkipsServiceBackoffLikeRust`：上下文持续变化时正好 3 次 attempt，且总耗时远低于 200ms 服务退避。
  - `TestGuardianReviewRetriesParseFailureLikeRust`：首次坏 JSON、第二次合法 → 成功；全部坏 JSON → 3 次后 failed_closed，`failure_reason=parse_error`、`context_changed=false`。
  - `TestGuardianReviewContextChangedTagOnlyOnCounterLikeRust`：未变化时 counter 为 false，duration 直方图不含该标签。
  - `TestGuardianReviewSessionRetryClassificationLikeRust`：15 个 kind/status 分类断言 + 可恢复错误消耗一次 attempt 后成功。

## 10.2 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go vet ./appserver` | exit 0 |
| `go test ./appserver -run "GuardianReview"` | ok |
| `go test ./appserver -run "TestGuardianReview...(5 个新用例)"` | ok |
| `go test ./appserver -count=1` | 仅既有环境失败 `TestProjectRootMarkersDriveDiscoveryLikeRust`、`TestRuntimeRouterThreadStartElevatedSandboxPersistsProjectTrust`、`TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust` |

# Round 95 增量（2026-10-17，lead）

## 11.1 已落地

### #54264 失败评审仍推进 Guardian transcript admission（`b687844674`）

- `appserver/guardian_reviewer.go`：`guardianSessionRunner` 现在把「已接纳的 transcript 进度」与「已完成的评审数」分开记录，对应 Rust `ConversationState::cursor` / `completed_review_count`：
  - `admitTranscript()`：请求已提交、输入被 live conversation 保留，即使推理失败也推进 cursor，但**不**增加完成计数；
  - `completeReview()`：评审完成时推进 cursor 并 +1；
  - `markPriorReview()` 改为 `admitTranscript()` 的别名（任何被提交的 review turn 都接纳其输入）；
  - `Review` 只在 `store.Complete` 成功后调用 `completeReview()`，失败/超时/中止路径只保留 admission。
- 因此下一次评审 `had_prior_review_context` 为 true，与 Rust #54264 的可观察行为一致；失败评审不会被当成 completed review。
- 结构差异：Rust 用 `PendingReviewContext` 判断 host 是否 finalize 了输入；Go 的 `guardianSessionRunner` 没有独立的 submit/finalize 阶段，`agent.Run` 被调用即视为输入已提交。Rust 的 committed fork checkpoint 在 Go 没有对应物，故无需保持“未完成评审不替换 checkpoint”的约束。

### 测试

- `appserver/guardian_transcript_admission_like_rust_test.go`
  - `TestGuardianSessionAdmitsTranscriptWithoutCompletingFailedReviewLikeRust`：失败评审 → `had_prior_review_context=true`、`completedReviews=0`；随后成功评审 → completed=1。
  - `TestGuardianSessionCompletesSuccessfulReviewLikeRust`：成功评审同时推进 cursor 与完成计数。

## 11.2 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./appserver -run "TestGuardianSession"` | ok |
| `go test ./appserver -run "Guardian|guardian"` | ok |

# Round 96 增量（2026-10-17，lead）

## 12.1 验证与判定

### #53879 设置更新保留运行中 turn 的恢复血统（`7eed30ad1b`）— 已实现，补回归测试

- Rust 只在没有注册 task 时清空 `SessionState::last_started_turn_id`；Go 没有这个字段，daemon 恢复候选来自 `threads.ActiveTurn` + 已记录的输入（`daemonRecoveryInterruptedTurn`），`ThreadExtraService.UpdateSettings` 不触碰 turn 状态。
- 新增 `appserver/thread_settings_recovery_like_rust_test.go`：运行中 turn 在 settings 更新后仍是恢复候选、turn 的冻结参数不变、线程级 settings 已更新；`ConsumeTurn` 之后同一更新不再留下候选（对应 Rust 的 idle 分支）。

### #54135 direct exec-server 大文件写入（`4ca84a1069`）— 已实现，补回归测试

- Rust 的问题是 direct 连接沿用了客户端 8 KiB inbound request 上限。Go 的 `DialClient` 从不存在该上限，读上限为 transport 的 16 MiB（`execserver/client.go`）；256 KiB 上限只出现在 Noise harness/relay。
- 新增 `execserver/direct_websocket_write_test.go`：经 `DialClient` 发送 512 KiB `fs/writeFile`，脚本执行器完整收到并逐字节一致。

### #54059 store-owned resume preparation（`b7ba02f880`）— N/A

- Go 没有 `ThreadStore` trait、`ThreadStoreResumePreparation` 或 `StartThreadOptions`（grep 均 0 命中）；`session.Store` 是具体实现，`Load` 是 resume 的唯一读取点。Rust 该 PR 是给外部 store 的扩展点（默认 unsupported），Go 没有第二个 store 实现，也就不存在可省去的二次加载或消费者。无 CLI 可观察行为。

## 12.2 验证

| 命令 | 结果 |
| --- | --- |
| `go test ./appserver -run TestThreadSettingsUpdatePreservesRunningTurnRecoveryLikeRust` | ok |
| `go test ./execserver -run TestDirectWebSocketWritesAboveClientRequestLimitLikeRust` | ok（512 KiB） |

# Round 97 增量（2026-10-17，lead）

## 13.1 已落地

### #52164 `include_environment_context_time=false` 跳过时钟读取（`8c818cbed4`）

- 渲染层门禁此前已存在；本轮补上 Rust `core/src/session/world_state.rs` 的另一半语义：关闭时间字段时**根本不读取时钟**。
- `appserver/turn_runtime.go::environmentClockForTurn` 在 `IncludeEnvironmentContextTime()==false` 时直接返回 `(nil, false, nil)`，per-turn 与 compaction 两个调用点同时受益：外部时钟停摆既不会让上下文构建失败，也不会产生 `CurrentTimeUnavailable` 提示。
- 测试 `appserver/environment_context_time_clock_like_rust_test.go`：flag 关闭 + 未配置的外部时钟 ⇒ 无错误、只有环境上下文、无提示；flag 打开 ⇒ 同一配置立即返回时钟读取错误，证明前一次确实跳过了读取。

## 13.2 阻塞判定

### #54216 全屏 transcript 图片预览（`dffa0deb69`）— blocked_precondition

Go 侧前置条件缺失（均已用可复现检索确认）：

1. `tui/state.go` 的 `Message` 只携带 `UserPromptLocalImages` / `UserPromptRemoteImages`（用户附件），没有 Rust 预览所需的“查看过的图片 / 生成图片产物”字段——数据未进入 transcript。
2. `tui/chatwidget/transcript_overlay.go` 是纯字符串 viewport（`SetContent` + viewport），没有可挂 `ImagePlacement` 的行布局模型。
3. Kitty/Sixel 编码只存在于 `tui/pets`（`image_protocol.go`、`sixel.go`），分页器渲染路径不可达。
4. Go 无 `transcript_view` / `custom_terminal` 对应物（检索仅命中 `parity/rust_tui_snapshot_manifest_test.go`），而 Rust 该 PR 跨 30 个文件，含 `custom_terminal` 图形保留（+128 行）与布局/文本行改动。

结论：忠实移植需要先建立“富 transcript 条目模型 + 终端图形层”，不是现有分页器上的局部改动。

## 13.3 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./appserver` | exit 0 |
| `go test ./appserver -run "TestEnvironmentContextTime"` | ok（4 例） |
| `go test ./appserver -run TestCompactEnvironmentContextHonorsIncludeEnvironmentContextTimeLikeRust` | ok |

# Round 98 增量（2026-10-17，lead）

## 14.1 已落地

### #53991 app 侧 ChatGPT refresh reason 接线（`f87ba04ec3`）

- `model/responses_agent.go::refreshManagedChatGPTAuth`（对应 Rust 的 managed `UnauthorizedRecoveryStepRefreshToken`）现在传 `RefreshReasonUnauthorizedRecovery`，401 恢复触发的刷新在 wire 上报 `refresh_reason=unauthorized_recovery`，而不是默认的 `forced`。
- `app/remote_tui.go::chatGPTAuthTokensRefresh`（TUI 代 app server 处理未授权刷新）同样传 `unauthorized_recovery`。
- 测试：`model/auth_refresh_reason_test.go`、`app/remote_tui_auth_refresh_test.go` 都直接断言请求体里的 `refresh_reason`。
- 剩余结构差异：Go 没有 AuthManager 的 proactive-expiry / scheduled 主动刷新决策（相关检索 0 命中），这两个 reason 值在 Go 不可达；gateway 刷新仍不带该字段（与 Rust 一致）。

### #54316 回归测试确认（`f0c5077c34`）

- round-91 评审要求的回归已在 `c289c53d` 落地：`TestGuardianReviewTranscriptExcludesCompactionSummaryLikeRust` 断言 compaction summary 被排除/归到 assistant，而同文本的用户消息仍保留。

## 14.2 判定

### #53795 bundled model catalog test（`8af93416dd`）— N/A

- Rust 该 PR 仅改测试：把 `config.model_catalog` 固定为 bundled catalog，让模型在 MCP 生命周期钩子前本地解析。Go 对应的启动测试（`interactiveMCPStartupMessages`）不解析模型目录，不存在远端目录依赖，无需移植。

## 14.3 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./model ./app` | exit 0 |
| `go test ./model -run TestManagedAuthRecoveryReportsUnauthorizedRefreshReasonLikeRust` | ok |
| `go test ./app -run TestRemoteTUIAuthRefreshReportsUnauthorizedReasonLikeRust` | ok |
