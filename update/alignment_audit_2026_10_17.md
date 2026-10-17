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
| `c22a06c180` | #53859 | Record bucketed terminal sizes in TUI telemetry (#53859) | TR-13 @5608e096: implementable (telemetry + TUI faces, medium, two halves) - Rust otel/src/metrics/client.rs adds requires_analytics_consent (only ... |
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
