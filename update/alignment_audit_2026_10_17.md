# Rust/Go 功能对齐审计（最新：round 112，2026-10-17）

> **当前状态只以本节为准。** 从 §2 开始的各节是历轮快照（round 91 起），其中的计数、上游 SHA 与
> "冻结在 970b7f2ff4" 之类表述都是**当时**的事实，保留它们仅为追溯每轮证据，不应读作当前值。

## 1. 结论摘要（当前）

- **Rust 上游目标**：`D:\qax\reagent\dev\git\codex` 的 `origin/main` = `ce0323f2f2`（`git fetch origin main` 后确认；`3b8f43a06b` 是它的前两个提交，`e16b631d08` 更早）。
- **parity 锚点**：`parity/baseline.json` 的 `verified.rustCommit` 与 `target.rustCommit` 都是 `ce0323f2f2d3b25e82806cd555237da3db4e1897`；`parity.json.rustBaseline` = `ce0323f2f2`；`parity/alignment_control_test.go` 的 `candidateRustTo` 同值；`parity/commits.json` 覆盖 `bd19459358..ce0323f2f2` 共 2695 条。
- **结论**：功能**尚未全部对齐**。当前量化缺口是 `implementation_pending` 435 条 + `blocked_precondition` 15 条 + `partial` 1 条（2695 条口径）；按面以 TUI 最大，其次 Guardian / appserver / core。本文件不宣称逐提交认证。

### 1.1 当前账本状态分布（`update/r90_commit_ledger_2026_10_16.tsv`，228 数据行）

| 状态 | 行数 |
| --- | --- |
| `landed` | 104 |
| `complete` | 27 |
| `not_applicable` | 79 |
| `blocked_precondition` | 17 |
| `partial` | 1 |
| **合计** | **228** |

不存在 `implementable` / `AlreadyImplemented` / `unverified` 行：12 条 `AlreadyImplemented` 已在 round 112 统一改记为 `complete`（证据文本保留原始判定）。

### 1.2 当前 2695 提交覆盖分布（`update/r109_symbol_coverage_2026_10_17.tsv`）

| 状态 | 行数 |
| --- | --- |
| `complete` | 443 |
| `not_applicable` | 1171 |
| `audit_pending` | 630 |
| `implementation_pending` | 435 |
| `blocked_precondition` | 15 |
| `partial` | 1 |

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

# Round 99 增量（2026-10-17，lead）

## 15.1 已落地

### #54175 退役 worktree / voice 特性开关（`bb64b894fd`）— landed

Rust 把 `worktrees` / `realtime_conversation` 从可用性门里彻底移除（features 面此前已落地），本轮补齐其余三面：

- **managed worktree 创建**：`exec/exec.go` 删除 `--worktree requires the worktrees feature` 的报错分支；`app/interactive.go::interactiveWorktreeSettings` 不再因 `features.worktrees=false` 返回未启用，仍保留本地工作区与 trust 检查。
- **repository-aware session discovery**：`app/agents.go` 的本地 agents dashboard 直接按仓库分组（删除 `localAgentsWorktreesEnabled`）；`app/interactive.go::interactiveSessionPickerItems` 固定展开 linked checkouts；`tui/tea/session_picker.go` 改用模型的本地/远端作用域（`m.worktreesEnabled`）而不是读特性开关；`tui/tea/agents_overview.go` 只看 `localSession`。
- **remote 语义**：`app/remote_tui.go` 的远端会话列表固定单目录（`SessionPickerWorktreeCWDs(cwd, false)`），`WorktreesEnabled` 固定为 `false`，与 Rust `repository_cwd_filter(cwd, uses_remote_filesystem=true)` 一致。
- **/experimental 隐藏**：`tui/tea/experimental_features.go` 即便旧 server 仍把两个 key 报成 beta，也从目录中过滤掉。
- **managed requirements 警告**：`config/startup_warnings.go` + `features/legacy.go::RetiredRequirementKey`，对引用这两个 key 的 managed 要求输出 `Ignoring removed `features` requirement `<key>` from managed requirements; availability no longer depends on this feature flag.`，按 key 排序。
- **voice 可用性**：Go 侧 `/voice` 与 TUI 语音入口已只看平台支持（`voicehost.IsSupported()`，`FeatureEnabled: true`），无需再改。

测试：

- `tui/tea/experimental_features_retired_test.go`：两个退役 key 即使 stage=beta 也不出现，其他 beta 仍列出。
- `tui/tea/agents_overview_retired_test.go`：本地即使 `features.worktrees=false` 仍分组，远端不分组。
- `config/feature_requirement_warnings_test.go::TestRemovedFeatureRequirementWarningsLikeRust`：两条 removed 警告按字典序输出。
- 既有 `features/features_test.go::TestRetiredWorktreeAndVoiceConfigOverridesAreIgnoredLikeRust` 覆盖配置覆盖被忽略。

## 15.2 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./config -run "TestRemovedFeatureRequirementWarningsLikeRust|TestManagedFeatureRequirementWarningsLikeRust|TestFeatureRequirementWarningsThroughRequirementsFile"` | ok |
| `go test ./tui/tea -run "TestRetiredWorktreeAndVoiceCatalogEntriesAreHiddenLikeRust|TestAgentsOverviewWorktreesEnabledFollowsLocalScopeLikeRust"` | ok |
| `go test ./tui -run "Worktree|SessionPicker"` | ok |
| `go test ./features` | ok |
| `go test ./exec` | FAIL（既有 `TestRunExecReviewUsesRustReviewRubricInstructions`，与本轮改动无关） |

# Round 100 增量（2026-10-17，lead）

## 16.1 已落地

### #54094 在指标与 span 中保留具体 MCP 失败码（`12fd621f62`）— landed

- **请求失败分类（rmcp `ServiceError` 对应物）**：新增 `mcp/request_error.go`。`mcpRequestErrorCode` 不解析错误文本，只按类型判定：`context.DeadlineExceeded` / `net.Error.Timeout()` ⇒ `request_timeout`；`io.ErrClosedPipe` / `net.ErrClosed` ⇒ `transport_closed`；`*MCPRemoteError` ⇒ `protocol_error`；`io.ErrUnexpectedEOF` / `io.EOF` ⇒ `unexpected_response`；其它 net 错误 ⇒ `transport_error`；未知错误保持无码（与 Rust 的 `_ => None` 一致）。`requestError` 包装保留原始 `Error()` 与 `Unwrap()` 链。
- **调用边界**：`mcp/stdio_client.go`、`mcp/http_client.go` 的 `tools/call` 失败统一经 `withMCPRequestErrorCode` 包装。
- **进入遥测前不丢类型**：`tool` 新增 `RequestErrorCoder` 与 `RequestErrorCodeTelemetryTag`；`turn/tool_dispatcher.go` 在错误被压平成模型文本之前把分类码写入 `ToolExecutionResult.TelemetryTags`。同时按 Rust `CallToolResult::from_result(Err(..))` 的语义，把带分类码的 MCP 请求失败作为模型可见的工具失败返回（非致命 turn 错误）；turn 级 deadline/cancel 仍走 `context.Cause` 的致命路径，不受影响。
- **指标与 span**：`telemetry/mcp_call_metrics.go::MCPCallOutcomeForExecution` 读取该 tag 并 `WithRequestErrorCode`，`codex.mcp.call.error` 与 `MCPCallSpanAttributes` 因此得到 `request_timeout` / `transport_closed` / … 而不是 `unknown`。
- **`codex/telemetry` meta 回退**：Go 已在 round-90 实现 `codexTelemetryErrorCode` + 16 项允许列表（结构化内容 → connector auth → telemetry meta 的优先级），本轮复核未改动，由既有 `telemetry/mcp_call_metrics_test.go` 覆盖。
- **trusted-access 超时**：Go 的 `TrustedAccessContext.fetchStatus` 在超时时按 Rust 语义回退为 `unknown` 状态并继续调用，不产生错误，因此不存在“保留类型化超时”的载体。

测试：

- `mcp/request_error_test.go`：9 种错误分类 + `%w` 包装后的 deadline 仍可识别 + 包装后消息/错误链不变。
- `turn/tool_request_error_code_test.go`：executor 返回带码错误 ⇒ `TelemetryTags[request_error_code]=request_timeout`，模型输出为原始消息且 turn 不失败。
- `telemetry/mcp_request_error_code_test.go`：`MCPCallOutcomeForExecution` 采用该码并写入 span 属性；tool-result 结果的码不被覆盖。

## 16.2 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./mcp` | ok |
| `go test ./turn` | ok |
| `go test ./telemetry` | ok |
| `go test ./appserver -run "MCP"` | ok |

# Round 101 增量（2026-10-17，lead）

## 17.1 已落地

### #54315 Proposed Plan 的 Markdown 底色与可读前景（`86ee40c380`）— landed

- **计划底色（Rust `style.rs::proposed_plan_style` / `plans.rs::plan_style`）**：`tui/style.go` 新增 `ProposedPlanSurface(level)`，用终端默认背景按 `ProposedPlanBackground`（浅色终端混 4% 黑、深色混 12% 白）解析出 plan fill；终端默认色未知时返回 `ok=false`，与 Rust 无 palette 时 `Style::default()` 不铺底色一致。
- **整块着色（Rust `style_proposed_plan_line`）**：`tui/markdown/render.go` 新增 `RenderProposedPlanWithThemeCwd`。先按主题正常渲染 Markdown，再逐行套 plan fill：
  - 每行行首写入 `48;2;r;g;b` / `48;5;n`，空白布局行也铺底色（Rust 对 `plan_lines` 的所有行调用 style patch）。
  - 逐条改写 SGR：`38;2;r;g;b` 与 `38;5;n` 前景先经 `tui.ReadableColorOn(..., &fill)` 做对比度修正再重新编码；`49` 与 `0` 重置后重新断言 plan fill，避免内部 span 的 reset 打出空洞。
  - SGR 参数以纯数字拼接（`planForegroundParams` / `planBackgroundParams`），不把完整 ESC 序列嵌进参数列表。
- **cell 接线**：`tui/history_cell/plans.go` 的 `ProposedPlanCell` 增加 `ThemeID` / `CWD` 与 `NewProposedPlanWithTheme`，有主题时按 `width-4` 渲染并缩进两格，块首尾用 `markdown.ProposedPlanBlankLine()` 铺底（Rust 的 `plan_lines` 首尾各有一个 padded space 行）；无主题构造器保留 raw fallback。`tui/tea/model.go::renderProposedPlanState` 传入 `m.activeTUITheme()` 与 `m.sessionCWD`。
- **测试钩子**：`tui/terminal_palette.go` 新增 `SetStdoutColorLevelForTest`，对应 Rust 在 `TEST_DEFAULT_COLORS` 存在时把 `effective_stdout_color_level()` 提升为 TrueColor 的行为。

测试：

- `tui/markdown/plan_surface_test.go::TestApplyPlanSurfacePaintsFillAndAdaptsForegrounds`：行首为 plan fill；`200/200/200` 前景在 `244/244/244` 底色上对比度不足被改写；`0` 重置后输出 `0;48;2;...`；无嵌套 ESC；空白行铺底。
- `tui/markdown/plan_surface_test.go::TestRenderProposedPlanWithThemeCwdShadesMarkdown`：表格内容保留，且每一行（含空白行）都以 `48;2;` 开头。
- `tui/history_cell/proposed_plan_surface_test.go::TestProposedPlanCellRendersShadedMarkdownLikeRust`：表体行与 padding 行都带底色，表头仍是 `• Proposed Plan`；无主题构造器保持 raw fallback。

## 17.2 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./tui/markdown ./tui/history_cell` | ok |
| `go test ./tui` | ok |
| `go test ./tui/tea` | 仅既有 `TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge` 失败（本机无剪贴板，copied=""），与本轮改动无关 |


# Round 102 增量（2026-10-17，lead）

## 18.1 已落地

### #54387 状态面命令的会话级 override 改为按线程保存（`141bf7680d`）— landed

- **漂移点**：round 691 已落地 `/title`、`/statusline` 的内联 items、`--clear`、`colors` 布尔设置与错误提示，但 override 只有“一个进程级槽位 + threadID 标记”。Rust 把 `StatusSurfaceOverrides` 放进每个线程的 `ThreadInputState`（`chatwidget/input_restore.rs:601` 捕获、`:621` 回放），因此 A 线程设置后切到 B 再设置，A 的记录会被覆盖丢失。
- **实现**：`tui/tea/status_controls.go` 的 `statusSurfaceOverrides` 改为 `byThread map[string]*statusSurfaceOverrideRecord`；每条记录独立保存 `terminalTitle`、`statusLine`、`statusLineUseColors`，`record` / `lookup` / `existing` 按线程 id 读写，`restoreStatusSurfaceOverrides` 只回放被恢复线程自己的记录。编辑器保存与保存设置写回时，`clearStatusLine` / `clearTerminalTitle` / `clearStatusLineUseColors` 都作用于当前线程（Rust `status_controls.rs:136-137,180`、`config_panel.rs:103`）。
- **保留行为**：内联参数解析、`--clear` 与 items 互斥、未知/歧义 item 提示、`slash_dispatch` 与 `bottom_pane/slash_commands` 接线均未改动。

测试：

- 新增 `tui/tea/status_surface_overrides_like_rust_test.go::TestStatusSurfaceOverridesKeepEveryThreadLikeRust`：A 设 `model` / `project-name` → 切 B 时保存布局不受影响，B 设 `hostname` / `status` → 回 A 得到 `model` / `project-name` → 再回 B 得到 `hostname` / `run-state`。
- 既有四个用例继续通过：恢复线程回放、其他线程隔离、编辑器保存覆盖 override、保存的颜色偏好覆盖 override。

## 18.2 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./tui/tea -run "TestStatusSurfaceOverrides|TestSavedStatusLineSetup|TestSavedColorPreference|TestStatusLineHostname"` | ok |
| `go test ./tui/tea` | 仅既有 `TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge` 失败（本机无剪贴板，copied=""），与本轮改动无关 |


# Round 103 增量（2026-10-17，lead）

## 19.1 已落地

### #53670 不完整响应的压缩侧记账与 rollout 载体（`816fa702ec`）— landed

- **压缩请求的 incomplete 记账（Rust `compact_remote_v2.rs:397-420`）**：`appserver/turn_runtime.go` 在 `compact.CompactRemotely` 返回终止错误时调用新增的 `recordIncompleteCompactionUsage`。用 `errors.As(model.ResponseIncompleteError)` 取 usage，缺失/畸形则忽略、不估算：
  - **Background（Rust `CompactionRequestKind::Background`）**：自动压缩随 turn 内联运行（Go `Trigger != TriggerManual`），只通过新的 `chargeRolloutBudgetUsage` 扣共享 rollout budget，不写 `total_token_usage` / `last_token_usage` / `token_usage_info`，与 Rust 的 `record_rollout_budget_usage` 一致。
  - **Foreground（手工 / standalone 压缩）**：复用 `recordIncompleteResponseUsage`（SpendingOnly：累计总量 + 预算 + token-usage contributors），基线保持不变。
  - 两种分支都保留原始终止错误；即使扣费耗尽预算，也不把 `ErrSessionBudgetExceeded` 传给调用方（Rust 的 `let _ = ...`）。自动压缩失败后走本地 fallback 时 `CompactRemotely` 本身不返回错误，因此该路径不触发记账（Rust 的 V2 手工路径没有本地 fallback）。
- **rollout `token_usage` 记录**：Rust 为每个响应写 `RolloutItem::TokenUsageRecord`；Go 的等价账本载体是线程元数据 `total_token_usage` + `token_usage_info`（round-90 AC-2 已落地），且 Go 的 rollout budget 不依赖 rollout 行重建（`rolloutBudgetForSession` 从配置构造并内存扣费），因此不存在需要该行类型的消费方，属于载体差异而非功能缺失。

测试：

- 新增 `appserver/incomplete_compaction_usage_like_rust_test.go`：
  - `TestForegroundIncompleteCompactionSpendsWithoutReplacingTheBaselineLikeRust`：累计 1100→1250，基线/`auto_compact_window_prefill` 不变，预算扣费并记录耗尽，错误仍是 incomplete。
  - `TestBackgroundIncompleteCompactionOnlyChargesTheBudgetLikeRust`：元数据完全不 变，只扣预算。
  - `TestIncompleteCompactionWithoutUsageIsNotEstimatedLikeRust`：无 usage 时不扣费、不改总量。

## 19.2 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./appserver -run "Compact|compact"` | ok |
| `go test ./appserver -run "TestForegroundIncompleteCompaction|TestBackgroundIncompleteCompaction|TestIncompleteCompactionWithoutUsage"` | ok |


# Round 104 增量（2026-10-17，lead）

## 20.1 已落地

### #52215 独立 MCP 命令的应用网络策略（`79773e8826`）— landed（复核后 already-implemented）

- **命令路径**：`app/mcp.go` 的 `mcpCLIStore.Load` / `LoadManaged` / `LoadWithCloudConfig` 每条路径都调用 `publishApplicationNetworkPolicy` 并用 `policyHTTPClient` 构造客户端（`:868-927`）。既有 `app/application_network_test.go::TestMCPCLIStoreBindsApplicationNetworkPolicyLikeRust` 断言传输层是 `*network.PolicyRoundTripper`，被拒目的地返回 `network.ErrNetworkPolicyDestination`。
- **Rust `bind_bootstrap_auth` 的对应语义**：Rust 把本地策略绑定到 cloud config bootstrap 的 `AuthConfig`，并把 auth-route factory 限制到 config bundle 端点。Go 没有 `AuthRouteConfig`，cloud bootstrap 期间唯一的网络请求就是 bundle GET：`config.scopeCloudConfigFetchOptions` 会把 `HTTPClient` / `FallbackHTTPClient` 包成 `RestrictToEndpoints([bundleURL])` 的 `network.PolicyHTTPDoer`，授权只添加静态 header 或本地签名，不发起 auth-route 网络调用。因此不存在未绑定策略的 bootstrap 认证客户端，覆盖由 `config/cloud_config_storage_test.go::TestCloudConfigFetchIsScopedToBundleEndpointLikeRust` 固定。
- **结论**：功能已对齐，残余项是载体名差异（Rust `AuthRouteConfig` vs Go 端点级 policy doer），不是缺口。

## 20.2 验证

| 命令 | 结果 |
| --- | --- |
| `go test ./app -run "TestMCPCLIStoreBindsApplicationNetworkPolicyLikeRust|TestMCP"` | ok |
| `go test ./config -run "CloudConfig"` | ok |


# Round 105 增量（2026-10-17，lead）

## 21.1 已落地

### #54056 原生根证书加载合并与自定义 CA 隔离（`80ad49e3b7`）— landed（RC 收口）

- **Rust 语义**（`http-client/src/native_roots.rs`）：并发构建 TLS 客户端时共享一次进行中的平台根读取；完成的快照不保留，后来的调用重新读取；`SSL_CERT_FILE` / `SSL_CERT_DIR` 覆盖时绕过共享；自定义 CA bundle 每次构建独立读取，且 `(*load()).clone()` 保证叠加 CA 不会污染进程级根池。
- **Go 现状核对**：`network/proxy_native_roots_pool.go` 已实现共享 in-flight 读取、完成后丢弃、override 绕过、每次返回独立 `CertPool`；`execserver/custom_ca.go:34` 在 clone 上 `AppendCertsFromPEM`，客户端之间不共享可变根池。Windows 上 `x509.SystemCertPool()` 每次返回新实例，execserver 的跨客户端泄漏在该平台结构上不可达。
- **本轮修复的剩余缺口**：原实现用 `sync.Once` + 只在成功路径 `close(done)`，loader panic 时 `done` 永不关闭，等待者会永久阻塞（Rust 的 `OnceLock` 在初始化 panic 后保持未初始化）。现改为在 `platformRootsCell.load` 内 recover、记录 `panicValue` 并始终 `close(done)`；初始化调用者继续向外 panic，等待者检测失败后重新发起新读取。对应 Rust `native_roots_tests.rs::recovers_from_a_panicking_loader`。

测试：

- 新增 `TestPlatformRootPoolCloneRecoversFromAPanickingLoadLikeRust`：首次读取 panic 由调用者观察，第二次调用重新读取并成功。
- 新增 `TestPlatformRootPoolCloneUnblocksWaitersWhenTheSharedReadPanics`：leader panic 时等待者不会卡死，能拿到重试后的池。
- 既有 `CoalescesInFlightLoads` / `AlwaysReturnsAnIndependentPool` / `BypassesCoalescingForOverrides` 继续通过。

## 21.2 验证

| 命令 | 结果 |
| --- | --- |
| `go test ./network -count=1` | ok |
| `go test ./network -run "TestPlatformRootPoolClone"` | ok |
| `go test ./execserver -run "CA|Cert"` | ok |

# Round 106 增量（2026-10-17，lead）

## 22.1 上游与范围

- `git -C D:\qax\reagent\dev\git\codex pull origin main`：Rust 上游由 `e16b631d08` 推进到 `3b8f43a06b`，新增三个提交（#54761 / #54763 / #54766）。
- 本轮同时闭环上一窗口遗留的 #53991（`f87ba04ec3`）与 #51786（`622e9e3696`）。
- Go 仓基线仍为 `db882bd7`（parity baseline 保持冻结在 `970b7f2ff4`，见 §22.6）。

## 22.2 已落地

### #53991 ChatGPT token 刷新原因（`f87ba04ec3`）— landed

- **Rust 行为**：`AuthManager::proactive_refresh_reason` 对 managed ChatGPT auth 判定刷新原因——access token 可解析 `exp` 且落在 5 分钟窗口内报 `proactive_expiry`；可解析但超出窗口则不再回退；`exp` 不可解析时回退到 `last_refresh` 超过 8 天报 `scheduled`。`AuthManager::auth()` 在请求前按该原因刷新，失败只记录日志并继续使用缓存 auth。
- **Go 落地**：新增 `auth/proactive_refresh.go`（`ChatGPTAccessTokenRefreshWindowMinutes=5`、`ChatGPTTokenRefreshIntervalDays=8`、`JWTAccessTokenExpiration`、`ProactiveRefreshReason`、`ProactiveRefreshManagedChatGPT`）；两条请求准备路径接线：`appserver/agent_runtime.go::responsesAgentForTurn`（刷新后经 `AccountManager.ApplyAuthSnapshot` 广播）与 `exec/exec.go::RunContext`。
- **HTTP 客户端**：`exec` 侧新增 `policyHTTPClientForConfig`，与 `appserver` 同名方法一致，用 `network.PolicyHTTPClient` 绑定应用网络策略后再交给刷新请求（此前用类型断言会静默退回 nil client，绕过策略）；`exec/application_network_test.go::TestExecPolicyHTTPClientForConfigEnforcesApplicationPolicyLikeRust` 固定该行为。
- **测试**：`auth/proactive_refresh_test.go`（5 分钟边界、8 天 scheduled、可解析 exp 不回退 scheduled、fresh auth 不发请求、失败保留缓存 auth、wire reason）、`appserver/proactive_refresh_like_rust_test.go`。

### #54766 TUI 未知配置字段交给 shared strict-config（`3b8f43a06b`）— landed

- **Rust 行为**：非 strict 配置忽略未知 keymap context / action / 错位 action；strict 配置按 `serde_ignored` 路径报告 `tui.keymap.<context>`、`tui.keymap.<context>.<action>`、`profiles.<name>.tui.<key>`。
- **Go 落地**：新增叶子包 `keymapschema`（11 context / 154 action，`global` 额外含 `submit`/`queue`/`toggle_shortcuts`）；`config/config.go` 在 `validateKnownTuiConfigFields` 中新增 `validateKnownTuiKeymapFields`，profile 的 tui 仅允许 `session_picker_view`；`tui/keymap_config.go` 改为忽略未知 context / 未知 action / 根下错位 action（已知 context 但值非 table 仍报错）。
- **测试**：`tui/keymap_schema_parity_like_rust_test.go`（schema 与 `keymapActionCatalog` 完全一致）、`config/keymap_strict_like_rust_test.go`（strict 报告三类路径、非 strict 忽略、已知字段可加载）、`tui/keymap_test.go::TestKeymapConfigIgnoresUnknownAndMisplacedActionsLikeRust`。

### #51786 跨执行请求保留 sandbox override（`622e9e3696`）— landed（本轮补齐 producer）

- **Rust 行为**：`SandboxOverride` 进入共享 protocol，由 `sandbox_override_for_first_attempt` 产出，经 tool attempt / exec 请求 / exec-server 进程与文件系统沙箱准备一路透传，最终以 `sandboxOverride` 出现在 filesystem sandbox context（`skip_serializing_if` 省略 `NoOverride`），仅用于观测，不放宽权限、不改沙箱命令。
- **此前已完成**：`execserver/sandbox_override.go` 枚举与 wire 形状、`FileSystemSandboxContext.SandboxOverride`、`execserver/server.go` / `sandbox_process_windows.go` / `fs_helper.go` 的合法性校验。
- **本轮补齐**：`tool/unified_exec.go::sandboxOverrideForFirstAttempt` 逐条对齐 Rust 判定——策略不允许无沙箱执行（存在 denied reads）时，已批准的 `require_escalated` 报 `EscalatedSandboxWithRestrictions`，否则 `NoOverride`；允许无沙箱执行时，已批准的 `require_escalated` 报 `BypassSandboxFirstAttempt`。选择经 `FileSystemSandboxContextOptions.SandboxOverride` 写入 wire，`NoOverride` 时省略。
- **测试**：`tool/unified_exec_test.go::TestUnifiedExecSandboxOverrideForFirstAttemptLikeRust`（含 nil / 默认 / 两种升级 / denied-reads 非升级共 5 例）、`TestUnifiedExecSandboxContextCarriesSandboxOverrideLikeRust`（wire 携带值与省略两种形态）。
- **仍未移植的载体**：`SandboxAttempt` / `SandboxDirectSpawnTransformRequest` / `SandboxExecRequest.sandbox_override` 在 Go 为 0 命中（Go 没有 attempt/orchestrator 层），其唯一消费者 sandbox integrity runner 对应上游 #51842（`c2eb1f42a0`），该提交本身 `blocked_precondition`（前置 #51831/#51840/#51841）。override 在 Go 的语义因此与上游一致：观测字段，不改变执行。

## 22.3 不适用（N/A）

- **`df8990e261` #54761 zip 9.0.0 与插件归档解压**：Go 插件包不使用 zip（`git grep archive/zip -- '*.go'` 0 命中），走 tar.gz（`plugin/bundle_archive.go::UnpackPluginBundleTarGz`）；解压已用 `os.FileMode(header.Mode)&0o777`（`plugin/bundle_archive.go:212`），保留可执行位并剥掉 setuid/setgid/sticky，与 zip 9 后的契约一致。Cargo/Bazel 锁文件变更无 Go 对应物。
- **`d13db678cb` #54763 移除 macOS daemon `taskpolicy -b`**：Go 从未使用 `taskpolicy`（0 命中），`appserverdaemon/pid_process_unix.go` 直接 `exec.Command` + `Setsid`，正是该提交之后的形状。

## 22.4 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./auth -run "ProactiveRefresh"` | ok |
| `go test ./appserver -run "TestResponsesAgentProactivelyRefreshesManagedChatGPTLikeRust|TestResponsesAgentKeepsFreshManagedChatGPTAuth"` | ok |
| `go test ./config` | ok |
| `go test ./tui` | ok |
| `go test ./tool -run "SandboxOverride"` | ok |
| `go test ./exec -run "TestExecRunPublishesApplicationNetworkPolicyLikeRust|TestExecPolicyHTTPClientForConfigEnforcesApplicationPolicyLikeRust"` | ok |
| `go test ./execserver -run "SandboxOverride"` | ok |
| `go test ./model -run "TestApiKeyCyberAccessProgramsFromConfigLikeRust"` | ok |


**既有失败（不属本轮引入，已在 detached `db882bd7` 的干净 worktree 复现）**：`TestDriveLetterPathsAreNotReparseRejectedAndJunctionsStayBlocked`、`TestWindowsSandboxPowerShellFallbackWithoutCompatibleShellLikeRust`（execserver，Windows 环境/提权相关）、`TestUnifiedExecRestoresExecutorPathDirsForLoginShellLikeRust`、`TestLocalShellLaunchRestoresExecutorPathDirsLikeRust`（tool，依赖 `/bin/sh`）、`TestRunExecReviewUsesRustReviewRubricInstructions`（exec）、`TestRuntimeAgentControllerSpawnUsesCapturedStepSettingsLikeRust`、`TestProjectRootMarkersDriveDiscoveryLikeRust`、`TestRuntimeRouterThreadStartElevatedSandboxPersistsProjectTrust`、`TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust`（appserver）、`TestAgentsDashboardSearchAndGrouping`、`TestFeaturesEnableUnderDevelopmentWarnsLikeRust`、`TestNoDaemonRejectionsLikeRust`（app）。

## 22.5 账本

**（历史快照，round 106/107 时点；当前值见 §1）** `update/r90_commit_ledger_2026_10_16.tsv`：landed 97 / not_applicable 79 / blocked_precondition 22 / complete 15 / AlreadyImplemented 12，partial 归零。

## 22.6 baseline 说明

**（历史快照，round 106 时点；当前锚点见 §1 = `ce0323f2f2`）** parity baseline（`parity.json.rustBaseline`、`parity/baseline.json`、`parity/domains.json`）当时冻结在 `970b7f2ff4`：`parity/alignment_control_test.go` 同时硬编码 candidate SHA 与 `parity/commits.json` 的 52 条提交、README 与 plan 文本，单改 baseline 会让该测试失败。本轮只做功能同步，不移动 baseline。

# Round 107 增量（2026-10-17，lead）

## 23.1 系统性 parity 套件回归修复

上一轮只跑了定向测试，漏掉了仓库自带的 parity 对比套件；`go test ./parity` 当时为红，本轮先把它跑绿，因为它是「系统比对」的主闸门。

- `TestGoTopLevelDirectoryInventory`：新增的 `keymapschema` 顶层包未登记，已加入 `parity/rust_inventory_test.go` 的 Go 目录快照。
- `TestRustCriticalFileHashesSnapshot`：上游拉到 `3b8f43a06b` 后 `Cargo.toml` 变化（zip 9.0.0，即 #54761）。按既有惯例用 `git cat-file blob 3b8f43a06b:codex-rs/<path>` 重算九个 pin，只有 `Cargo.toml` 变化（`1d477861…` → `97f33232…`），其余八项同一 head 下不变；注释已标注 Round-106 re-anchor。
- 结果：`go test ./parity -count=1` → ok（此前 FAIL）。

## 23.2 只读会话返回键漂移修正（Rust #48132 家族）

`tui/tea/read_only_thread.go` 的返回命令中心判定只比较 `msg.Type == KeyLeft`，而 Rust `app/input.rs:600-605` 要求 `KeyEventKind::Press` 且 `modifiers == NONE`。因此 Go 里 `Alt+Left`（composer 的 agent 导航组合键）会误开 command center。

- 改为 `PlainKey(bubbletea.KeyLeft).IsPress(msg)`，与同文件其它按键判定一致；bubbletea 无法区分 Press/Repeat，修饰键这半边已对齐，重复键这半无载体。
- 新增 `tui/tea/read_only_thread_command_center_test.go::TestReadOnlyViewIgnoresModifiedLeftLikeRust`：Alt+Left 不打开面板，plain Left 仍打开。

## 23.3 上游 keymap 名称/上下文兼容（Rust #54766 的 strict-config 补全）

Go 的 keymap catalog 与 Rust `config/src/tui_keymap.rs` 有 9 个「同行为不同名」的 action（以及一个 `vim_search` 上下文）。此前这些 Rust 名称既不能绑定（`/keymap set` 报未知），又会被刚落地的 strict-config 误报为未知字段 —— 等于 Rust 合法配置在 Go 里失效。

- 新增 `keymapschema.ActionAliases`（9 条，单一事实源）：`vim_normal.enter_replace_mode → replace_mode`、`find_forward/backward → find_char_forward/backward`、`till_forward/backward → till_char_forward/backward`、`vim_search.{forward,backward,next,previous} → vim_normal.search_{forward,backward,next,prev}`。
- `keymapschema.KnownContext/KnownAction` 接受只存在于别名的上下文与动作，strict-config 不再误报。
- `tui/keymap_config.go`：`KeymapConfigFromConfigValues` 在未知动作过滤前解析别名并写入 Go 目标；`normalizeKeymapTarget` 同样解析，使 `/keymap set|unset vim_search.forward` 可用；同一目标被别名与正式名同时配置时报冲突，避免 map 迭代顺序决定结果。
- 测试：`tui/keymap_test.go::TestKeymapConfigAppliesUpstreamVimActionAliasesLikeRust`、`TestKeymapConfigRejectsAliasCanonicalConflictLikeRust`；`tui/keymap_schema_parity_like_rust_test.go::TestKeymapSchemaAliasesMatchCatalogLikeRust`（别名必须指向真实 catalog 条目、数量与 `acceptedKeymapRenames` 一致）；`config/keymap_strict_like_rust_test.go` 增加 Rust 拼写的接受用例。

## 23.4 账本校正

复核后 5 条 TUI keymap 账目由 `blocked_precondition` 改为 `landed`，1 条更新为「一半已落地」：

| 账目 | 新状态 | 依据 |
| --- | --- | --- |
| #49517 `agents.fork` | landed | `tui/keymap.go:163` + `tui/tea/agents_overview.go:463` |
| #46749 `pager.find` | landed | `tui/keymap.go:148` + `tui/chatwidget/transcript_overlay_find.go` |
| #42194 `enter_replace_mode` | landed | 别名解析（本轮） |
| #40785 `find_/till_` 四名 | landed | 别名解析（本轮） |
| #41586 `vim_search` 上下文 | landed | 别名上下文（本轮） |
| #46734 `find_transcript`/`focus_activity` | 仍 blocked | find_transcript 已落地；focus_activity 无活动分组聚焦载体 |

统计：landed 102 / not_applicable 79 / blocked_precondition 17 / complete 15 / AlreadyImplemented 12（partial = 0）。

## 23.5 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./parity -count=1` | ok（本轮修复前 FAIL） |
| `go test ./tui ./config ./keymapschema -count=1` | ok |
| `go test ./tui/tea -run "TestReadOnlyView" -count=1` | ok |
| `go test ./... -count=1` | 仅剩既有/环境失败，已在 detached `db882bd7` worktree 复现同一组（app 3、appserver 4-5、doctor 3、exec 1、execserver 3、gitutil 3、memories 1、model 3、prompt 3、recordreplay 1、state 1、tool 2、tui/tea 1、sandbox/linuxsandbox 构建） |

`memories::TestMemoryV2TemplateMatchesRust` 的根因已定位：`go:embed` 读取工作区 CRLF 文件，而测试只把 Rust 侧归一化成 LF；属于 Windows 检出环境问题，不是本轮上游漂移。

---

# Round 108（2026-10-17，lead）· parity baseline 提升 + 架构级缺口

## 24.1 背景

上一轮 verifier 驳回理由：parity baseline 冻结在 `970b7f2ff4`，账本仍有 open 项，因此不能宣称功能对齐。
本轮先解决**结构性**问题：把对齐控制面全部提升到上游 HEAD，并建立**逐提交全量账本**。

## 24.2 parity 控制面提升到 `ce0323f2f2`

上游 `origin/main` 本轮由 `3b8f43a06b` 前进到 **`ce0323f2f2`**（#54776 Support typed content in contextual prompt fragments）。

| 文件 | 变更 |
| --- | --- |
| `parity/alignment_control_test.go` | `candidateRustTo` → `ce0323f2f2d3b25e82806cd555237da3db4e1897`；账本条数断言 52 → 2695 |
| `parity/baseline.json` | `frozenAt=2026-10-17`、`plan=update/plan_2026_10_17.md`、`verified/target.rustCommit` 指向 HEAD、blocker 文本改为「逐提交认证进行中」 |
| `parity/commits.json` | **重新生成 2695 条**（`git rev-list --reverse bd19459358..ce0323f2f2`），每条含 sha/subject/kind/domains/status/evidence |
| `parity.json` | `rustBaseline=ce0323f2f2`、`rustUpstreamHead/Tag` 指向 HEAD |
| `parity/domains.json`、`parity/contracts/manifest.json` | `targetRustCommit` 指向 HEAD |
| `README.md`、`update/plan_2026_10_17.md` | 目标 SHA 文本同步 |
| `parity/parity_manifest_test.go` | `parityRustBaseline` → `ce0323f2f2` |

全量账本状态分布（2695 条）：

| 状态 | 条数 | 来源 |
| --- | --- | --- |
| complete | 385 | 198 条既有账本（93 landed + 14 complete + 12 AlreadyImplemented + 79 not_applicable）+ 187 条规则判定的 Rust-only test/build/release 变更 |
| implementation_pending | 15 | 既有 `blocked_precondition` 行 |
| audit_pending | 2295 | 尚未逐条比对的提交（本次新增的完整缺口清单） |

`parity/commits.json` 因此第一次成为**与上游区间逐条对齐的完整清单**，而不是 52 条抽样。

## 24.3 新增落地（#53617 disable_native_tools）

- Rust `features.multi_agent_v2.disable_native_tools`（#53617）：隐藏 native 多 Agent 工具，但保留 V2 runtime 与指令。
- Go：
  - `config/multi_agent_v2.go`：新增 `MultiAgentV2Config.DisableNativeTools` 与 `features.multi_agent_v2.disable_native_tools` 解析（默认 false）。
  - `appserver/runtime_router.go`：在 `toolRouterForTurnContext` 里提前解析 `MultiAgentV2Config`，并把工具面判定收敛为 `runtimeCollabToolsEnabled(version, depthLimited, disableNativeTools, subagentToolsEnabled)`，逐分支镜像 Rust `collab_tools_enabled`（V1 受 depth 限制、V2 忽略 depth、disable_native_tools 对所有线程生效、子 Agent 需模型声明 V2）。
  - 测试：`config/multi_agent_v2_test.go::TestMultiAgentV2ConfigDisableNativeToolsLikeRust`、`appserver/collab_tools_gate_like_rust_test.go`（9 组组合矩阵 + 配置半）。

## 24.4 上游 HEAD 暴露的 schema 缺口（19 个迁移）

`go test ./parity` 在 HEAD 上新增三处红：`apply_patch.lark` 路径迁移、`Cargo.toml` pin、**state 迁移清单落后 19 个文件**。已全部补齐：

- `state/migrations/state/`：0051 `thread_artifacts`、0052 `idx_threads_project_recency`、0053 `threads.originator`、0054 `threads.daybreak_enabled`、0055 `thread_attachments` 重命名、0056 `threads.creator_user_id/creator_account_id`、0057 Guardian 线程元数据清理、0058 归档排序索引 ×3、0059 反向查找索引、0060 `guardian_review_feedback`、0061 `thread_read_receipts`。
- `state/migrations/memories/`：0002 `consolidation_progress`。
- `state/migrations/thread_history/`：0005 `thread_realtime_items`、0006 `idx_thread_turns_end_page`、0007 item 生命周期时间戳、0008 `thread_turns.root`。
- `state/thread_history_test.go`：SQLx checksum 扩到 8 条（新 4 条按 sha384 计算）。
- `parity/apply_patch_spec_test.go`：grammar 路径改为 `core/assets/tools/apply_patch.lark`（上游已迁移）。
- `parity/rust_snapshot_test.go`：workspace member 增加 `protocol/tests/support`；`Cargo.toml` pin = `05c9740164…`。

## 24.5 验证

```
go build ./...                                  # exit 0
go test ./parity -count=1                       # ok
go test ./state -run 'ThreadHistory|Migrations' # ok
go test ./config -run MultiAgentV2              # ok
go test ./appserver -run 'CollabToolsGate|DisableNativeToolsKeepsV2' # ok
```

## 24.6 结论与剩余

- 结构性阻塞（baseline 冻结）已解除：控制面、账本、manifest 全部指向 `ce0323f2f2`。
- 真实功能缺口仍以账本形式显式存在：**2295 条 audit_pending + 15 条 implementation_pending**，本轮新增 #54776 为 implementable（Go 侧 fragment body 仍返回 string，缺少 ContentItem 返回路径）。
- 该数量就是「功能对齐」尚未达成的量化证据，不再用抽样账本掩盖。

## 24.7 #54776 typed content（部分落地）

- Rust：`ContextualUserFragment::body()` / `render()` 返回 `ContentItem`；`render()` 只对 `InputText` 套 marker，其它内容原样透传；扩展 prompt / world-state 贡献可带类型内容；非文本 world-state 用序列化内容+role 做哈希；共享 content→tool-output 转换保留图片引用/细节与音频。
- Go 本轮落地（`context/fragments.go`）：
  - `BodyItemProvider`（`BodyItems() []eventmap.ContentItem`）让片段输出类型化内容；
  - `RenderedFragment.ContentItems`、`RenderItems()`：marker 只包裹 `InputText`，图片/音频原样透传；
  - `ToolOutputParts()`：把类型化内容转成 `input_text` / `input_image`（含 `detail`）/ `input_audio` 载荷；
  - 测试：`context/typed_fragment_content_like_rust_test.go` 三例（marker 只作用于文本、纯字符串片段仍为单个 InputText、音频转换）。
- 未完成：`appserver/turn_runtime.go` 的 world-state 贡献仍以字符串拼接与文本哈希为主，extension prompt 贡献也还是纯文本；code-mode 输出尚未复用 `ToolOutputParts`。账本记为 `partial`。

---

# Round 109（2026-10-17，lead）· 全量符号覆盖审计 + #47540

## 25.1 方法（可复现）

脚本：`update/r109_symbol_coverage_2026_10_17.py`；结果：`update/r109_symbol_coverage_2026_10_17.tsv`。

1. 对 `bd19459358..ce0323f2f2` 的每个提交，从**非测试文件**的新增行里提取 Rust 新增的**公共 API 标识符**（`pub fn/struct/enum/trait/const/static/type`），以及新增的 snake_case 字符串字面量（配置键）。
2. 把全部 Go 源码的标识符与字符串字面量做「去下划线 + 小写」归一化，构成匹配集合。
3. 计算 `matched/total` 覆盖率，并记录未匹配符号。
4. 判定：覆盖率 ≥0.9 → `regression_pending`（Go 已暴露同一公共面，回归未逐条验证）；覆盖率 =0 → `implementation_pending`（明确缺口）；其余 → `audit_pending`；无新增公共符号 → 私有/行为型改动，保持待审。

**这是比对辅助，不是正确性证明**：符号存在只说明 Go 暴露了同名能力，不说明行为逐条对齐。

## 25.2 结果（`parity/commits.json` 状态重算）

| status | 条数 | 含义 |
| --- | --- | --- |
| complete | 386 | 既有账本已落地 + Rust-only test/build/release 变更 |
| regression_pending | 319 | Go 已暴露上游新增公共面（覆盖率 ≥0.9） |
| implementation_pending | 454 | **Go 完全没有对应公共符号**（明确缺口） |
| audit_pending | 1536 | 部分覆盖或无新增公共符号，需人工比对 |

缺口按面分布（覆盖率 =0）：tui 161、guardian 73、appserver 60、core 43、sandbox 27、mcp 23、execserver 22、persistence 21、model 8、config 6、telemetry 5、plugins 3、realtime 2。

## 25.3 本轮落地：#47540 `disable_direct_message`

- Rust：`features.multi_agent_v2.disable_direct_message`，默认 false；开启时省略 `send_message` 与 `followup_task`，保留 spawn / 自动子结果 / 其余管理工具；启用时要求会话具备 message-board `post` 工具（internal worker 豁免）。
- Go：
  - `config/multi_agent_v2.go`：新增 `DisableDirectMessage` 与解析。
  - `agent/tools.go` / `agent/tools_v2.go`：选项透传，`registerMultiAgentV2Handlers` 在开启时跳过 send/followup。
  - `turn/tools.go`：`Options.DisableDirectMessage` 接线。
  - `appserver/runtime_router.go`：`v2Config.DisableDirectMessage` 写入 turn 选项；路由构建后按 Rust 文案 `disable_direct_message requires an available agent message board in this session` 校验 `post` 工具，internal 源（`internal_*`）豁免。
  - 测试：`config/multi_agent_v2_test.go`（默认 false / 解析 true / 不影响其它开关）、`agent/tools_v2_direct_message_like_rust_test.go`（缺省暴露、开启后 send/followup 消失且 spawn/wait/interrupt/list 保留）。

## 25.4 验证

```
go build ./...                              # exit 0
go test ./parity -count=1                   # ok
go test ./agent -run 'DirectMessage|DirectToolsDefault' -count=1   # ok
go test ./config -run MultiAgentV2Config -count=1                  # ok
```

## 25.5 剩余

454 条 `implementation_pending` 是当前「功能对齐」的量化缺口（TUI 161 条为主，其次 Guardian 73、appserver 60）。下一轮建议按面推进：appserver → core → sandbox → mcp → execserver → persistence → TUI（遵循「核心优先，TUI 最后」）。

## 26. Round 110/111（2026-10-17）：catalog 覆盖两笔 + 账本口径收敛

### 26.1 #47773 `request_user_input_async` 使用 catalog schema（landed）

- Rust：`send_user_message_async.parameters` 同时作为 `request_user_input_async` 的 schema 覆盖；无效覆盖告警并回退 bundled，description 独立保留（`catalog_parameters::parse`：合法 JSON、object 根、`type:"object"`、支持的 schema 子集）。
- Go：新增 `tool/catalog_parameters.go`（`catalogParametersParse` / `ParseCatalogParameters`，复用 `jsonschema.SubsetSchema` 做子集归一化），`RequestUserInputAsyncHandler.Parameters`，`model.ModelMessages.RequestUserInputAsyncDescription/ParametersOverride`，`turn.ToolRegistryOptions.RequestUserInputAsync*`，appserver 路由从 catalog 取值。
- 测试：`tool/request_user_input_async_catalog_schema_like_rust_test.go`（合法覆盖、未知关键字丢弃、9 种非法覆盖回退且保留 catalog description）、`turn/tools_test.go`、`model/catalog_test.go`。

### 26.2 #47677 MCP 资源 helper 的 catalog 覆盖（landed）

- Rust：`model_messages.tools.mcp_resources` 的三个条目分别覆盖 `list_mcp_resources` / `list_mcp_resource_templates` / `read_mcp_resource` 的 description 与 parameters；二者独立生效，非法 schema 告警回退 bundled（`mcp_resource_spec::with_model_messages`）。
- Go：`model.McpResourceToolMessages` + `ToolMessages.McpResources` + `ModelMessages.MCPResources()`，`turn.registerMCPResourceHandlers` 接收并按工具应用覆盖（`applyMCPResourceCatalogMessage`），appserver 路由接线；direct 与 CodeModeOnly 两条注册路径共用。
- 测试：`model/catalog_test.go` `TestModelMessagesMCPResourcesLikeRust`、`turn/tools_test.go` `TestBuildToolRegistrySourcesMCPResourceCatalogEntriesLikeRust`。

### 26.2b #52696 marketplace 路径归一化匹配（landed）

- Rust：本地 marketplace 源在文件系统归一化后比较；已添加源的请求路径优先（junction 重定向不改返回根）；Windows 托管根同时接受配置根与规范目标。
- Go：`plugin/marketplace_source.go` 新增 `pathsMatchAfterNormalization`（`filepath.Abs` + `EvalSymlinks` + Windows 大小写折叠，失败时退回直接相等，对应 `codex_utils_path`）；`plugin/api.go` 新增 `marketplaceForSourceLocked`，按「源类型 + 归一化路径（git 用 URL+ref）+ sparse paths」定位已添加 marketplace，`AddMarketplace` 命中时返回既有根而不是重复注册。
- 测试：`plugin/api_test.go` `TestAddMarketplaceMatchesEquivalentLocalSourceLikeRust`、`TestAddMarketplaceJunctionSourceKeepsRecordedRootLikeRust`、`TestPathsMatchAfterNormalizationLikeRust`。
- 未覆盖：Windows 托管根 canonical target 分支（Go 无 managed marketplace root 概念）。

### 26.3 账本口径收敛

- 新增可复现脚本 `update/r110_recompute_status_2026_10_17.py`：从 `update/r90_commit_ledger_2026_10_16.tsv` 的 evidence 状态重新推导 `update/r109_symbol_coverage_2026_10_17.tsv` 的 verdict（120 行被纠正，例如 #47540 / #52065 / #52302 / #54135 / #54686 由 pending 变为 complete）。
- `r90` 账本中 12 条 `AlreadyImplemented` 统一改为 `complete`（证据文本保留并标注原始判定），状态词汇与 TSV/commits.json 对齐。
- 当前账本分布：landed 104 / complete 27 / not_applicable 79 / blocked_precondition 17 / partial 1。
- 覆盖面（TSV，2695 提交）：complete 442、not_applicable 1171、audit_pending 630、implementation_pending 436、blocked_precondition 15、partial 1。

### 26.4 锚点核对

- Rust `origin/main` = `ce0323f2f2`（`git fetch origin main` 后确认；`3b8f43a06b` 为其前两个提交）。
- `parity/baseline.json`：`verified.rustCommit` = `target.rustCommit` = `ce0323f2f2d3b25e82806cd555237da3db4e1897`，`parity.json.rustBaseline` = `ce0323f2f2`，`parity/alignment_control_test.go` 的 `candidateRustTo` 同值。
- verifier 上一轮引用的「baseline 冻结在 970b7f2ff4」与当前工作树不符：`970b7f2ff4` 仅出现在历史审计文本与 `update/plan_2026_08_22.md` 中，不是当前锚点。

### 26.5 验证

```
go build ./...                                                        # exit 0
go test ./parity -count=1                                             # ok
go test ./turn ./model ./mcp ./config ./agent -count=1                # all ok
go test ./appserver -run 'ManifestCoversRustModules|Guardian' -count=1 # ok
go test ./tool -run 'RequestUserInputAsync|CatalogParameters' -count=1 # ok
```

### 26.6 剩余（目标未完成）

`implementation_pending` 436 条 + `blocked_precondition` 15 条 + `partial` 1 条。按面：TUI 仍占最大头，其次 Guardian / appserver / core。下一轮继续按「appserver → core → sandbox → mcp → execserver → persistence → TUI」推进；`blocked_precondition` 中 #54048（交互式目录信任确认本身缺失）、#54216（全屏转录图片预览）、#54380/#54381（presets 子系统）需要先补底层能力。
