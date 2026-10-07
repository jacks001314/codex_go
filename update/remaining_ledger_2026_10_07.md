# 剩余项台账（权威重扫，2026-10-07）

> 生成者 `verify51482`（队长派出，只读审计）。**未 commit、未 push、未改任何既有文件**，本文件为新建。
> 固定点：Go main **`c30b56ca`**（`plan: record round 79 (#48575 reclassified from structural block to small core gap; dispatched with acceptance)`）；上游窗口 `5f3180c793..5a3140176e` = **499 commits / 499 PR**。
> ⚠️ Go 仓是移动靶：审计期间 main 从队长给的 `c7e9d38f` 前进到 `c30b56ca`。下表只对该固定点成立。

## 0. 口径、规则与复跑命令（全部本次实跑）

```bash
cd /home/jacks/jacks_dev/codex
git rev-list --count 5f3180c793..5a3140176e                                  # 499
git log --format='%s' 5f3180c793..5a3140176e | grep -vc '#'                  # 0（每个提交都带 PR 号）
cd /home/jacks/jacks_dev/codex_go
rg -o --no-filename -g 'update/*.md' '#[0-9]{4,6}' | tr -d '#' | sort -u | wc -l   # 2282（语料覆盖）
comm -23 <(窗口 PR 集) <(语料 PR 集) | wc -l                                  # 0 缺失 → 复现队长口径
```

| 判定 | 规则 | 你可用同一条命令复跑 |
|---|---|---|
| `✅ 已落地` | Go main 提交标题含 `(#PR)`，或 Go `.go` 源码注释含 `#PR` | `git log --format='%h %s' main \| grep '#PR'`；`rg -n -g '*.go' '#PR' .` |
| `➖ N/A` | Rust 改动**全部**落在测试/快照/CI/文档/lock | `git -C ../codex show --stat <sha>` |
| `⬜ 未落地` | ①`git log --grep '#PR'` 0 命中 **且** ②Go 非测试 `.go` 里找不到该 PR 新增标识符（取最长标识符的 snake/camel/Camel 三种写法） | 见 §2 每行的证据列 |

**局限（必读）**

1. `✅` 只是**下界**：Go 大量落地不写 PR 号（例：`#48611` 的载体 `context/persistent_mode.go` 引的是 #41050）。所以 `✅` 不会把已落地误报为未落地；反向风险在 `⬜`。
2. 为压低假阴性，`⬜` 逐行做了**符号面检查**。`⬜[符号面=0]`（261 条）表示「PR 号 0 命中 **且** Rust 新增标识符在 Go 0 命中」；`⬜(待裁定)`（2 条）表示 Go 有近似符号面，**不得**直接派单。
3. **本台账不做逐条对抗式语义复核**（499 项 × 读 diff + 找落点）。抽样实测见 §5：机制结论与人工判断一致，但**未达证据级**。请把它当「派单队列」，而非终判。
4. 队长第七十七轮的 B 类（有 Go 表面的待审计项）已并入 §2.B。

## 1. 汇总

| 判定 | 条数 |
|---|---|
| `✅ 已落地`（Go 提交/源码引用） | **200** |
| `➖ N/A`（Rust 改动非生产代码） | **36** |
| `⬜ 未落地` | **263** |
| ├ 双证据齐（`⬜[符号面=0]`） | 261 |
| └ Go 有近似符号面（`⬜(待裁定)`，§2.B） | 2 |
| 合计 | 499 |

`⬜` 的 Rust 改动面分布：`tui` 94，`core` 56，`ext` 18，`exec-server` 11，`windows-sandbox-rs` 10，`cli` 7，`app-server` 6，`rmcp-client` 5，`rollout` 5，`guardian-context` 4，`state` 4，`analytics` 3，`codex-api` 2，`app-server-transport` 2

## 2. ⬜ 未落地（263 条）

### 2.A 双证据齐（261 条）—— 可直接据此派单

| #PR | 上游 SHA | 一句话语义 | 判定 | 决定性证据（命令 + 输出要点） |
|---|---|---|---|---|
| #48549 | `75a714843b` | Preserve Markdown tables and whitespace when copying TUI respons | ⬜ 未落地 | ①`git log --grep="#48549"`=0 ②`rg '#48549' -g '*.go'`=0 ③最长标识符 `empty_selected_rows_keep_copy_spacing_without_newline_highlights` 在 Go 0 文件 ④`git show --stat 75a714843b`=14 文件（codex-rs/tui/src/app/tests/transcript_selection.rs; codex-rs/tui/src/chatwidget/tests/copy_export_picker_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #48565 | `228ae3da8d` | Allow macOS TLS trust evaluation in network-enabled Seatbelt pro | ⬜ 未落地 | ①`git log --grep="#48565"`=0 ②`rg '#48565' -g '*.go'`=0 ③最长标识符 `trust_evaluation_agent_access_follows_network_policy` 在 Go 0 文件 ④`git show --stat 228ae3da8d`=2 文件（codex-rs/sandboxing/src/seatbelt.rs; codex-rs/sandboxing/src/seatbelt_tls_tests.rs）→ 落点候选 （sandboxing：无直接 Go 包） |
| #48575 | `985cf47a4e` | Allow provisioned executors more time to come online (#48575) | ✅ 已落地（队长核） — 3ba8ede6 | ①`git log --grep="#48575"`=0 ②`rg '#48575' -g '*.go'`=0 ③最长标识符 `provisioned_environment_waits_for_offline_executor_on_the_same_handle` 在 Go 0 文件 ④`git show --stat 985cf47a4e`=4 文件（codex-rs/exec-server/src/client.rs; codex-rs/exec-server/src/client_refresh_tests.rs）→ 落点候选 execserver/  **[队长核 · 第 84–85 轮]** |
| #48604 | `9db8162d65` | Remove the bundled `plugin-creator` skill (#48604) | ✅ 已落地（队长核） — 34b5faea | ①`git log --grep="#48604"`=0 ②`rg '#48604' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 9db8162d65`=13 文件（codex-rs/app-server/tests/suite/v2/turn_start.rs; codex-rs/skills/src/assets/samples/plugin-creator/SKILL.md）→ 落点候选 appserver/；（skills：无直接 Go 包）  **[队长核 · 第 84–85 轮]** |
| #48611 | `814de47b69` | Centralize persistent mode enablement checks (#48611) | ⬜ 未落地 | ①`git log --grep="#48611"`=0 ②`rg '#48611' -g '*.go'`=0 ③最长标识符 `persistent_instructions_follow_mode_and_catalog_updates_without_duplicates` 在 Go 0 文件 ④`git show --stat 814de47b69`=6 文件（codex-rs/core/src/context/world_state/persistent_mode.rs; codex-rs/core/src/context/world_state/persistent_mode_tests.rs）→ 落点候选 context//session//turn//rollout//state/；（features：无直接 Go 包） |
| #48626 | `449d42ced9` | Stop showing previous-session summaries when switching TUI sessi | ✅ 已落地（队长核） — a10be6aa | ①`git log --grep="#48626"`=0 ②`rg '#48626' -g '*.go'`=0 ③最长标识符 `start_fresh_session` 在 Go 0 文件 ④`git show --stat 449d42ced9`=8 文件（codex-rs/tui/src/app.rs; codex-rs/tui/src/app/empty_state_animation_tests.rs）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #48628 | `8f195c93d7` | Preserve blank TUI sessions when switching tasks (#48628) | ⬜ 未落地 | ①`git log --grep="#48628"`=0 ②`rg '#48628' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 8f195c93d7`=8 文件（codex-rs/tui/src/app/agents_overview.rs; codex-rs/tui/src/app/app_server_events.rs）→ 落点候选 tui/（部分在 app/） |
| #48686 | `41f9084b30` | Remove WebSocket headers and tool payloads from info logs (#4868 | ⬜ 未落地 | ①`git log --grep="#48686"`=0 ②`rg '#48686' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 41f9084b30`=2 文件（codex-rs/codex-api/src/endpoint/responses_websocket.rs; codex-rs/core/src/stream_events_utils.rs）→ 落点候选 codexapi/；context//session//turn//rollout//state/ |
| #48725 | `88235f881d` | Retain confirmed Code Mode messages for Guardian reviews (#48725 | ⬜ 未落地 | ①`git log --grep="#48725"`=0 ②`rg '#48725' -g '*.go'`=0 ③最长标识符 `delivered_assistant_context_invalidates_reviews_without_changing_authorization` 在 Go 0 文件 ④`git show --stat 88235f881d`=55 文件（codex-rs/Cargo.lock; codex-rs/app-server-protocol/schema/precomputed/app-server-exports-stable.json.zst）→ 落点候选 （codex-rs：无直接 Go 包）；（app-server-protocol：无直接 Go 包） |
| #48727 | `18344a972d` | Centralize executable fixture creation to avoid Linux ETXTBSY ra | ⬜ 未落地 | ①`git log --grep="#48727"`=0 ②`rg '#48727' -g '*.go'`=0 ③最长标识符 `WriteExecutable` 在 Go 0 文件 ④`git show --stat 18344a972d`=10 文件（codex-rs/cli/tests/app_server_daemon.rs; codex-rs/cli/tests/doctor_path_safety.rs）→ 落点候选 cli/；execserver/ |
| #48754 | `819cdb726d` | Render `/status` without borders and wrap long values (#48754) | ⬜ 未落地 | ①`git log --grep="#48754"`=0 ②`rg '#48754' -g '*.go'`=0 ③最长标识符 `appending_to_unchanged_history_tail_preserves_existing_rows` 在 Go 0 文件 ④`git show --stat 819cdb726d`=32 文件（codex-rs/tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__slash_copy_whole_status.snap; codex-rs/tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__status_command_estimated_thread_usage.snap）→ 落点候选 tui/（部分在 app/） |
| #48757 | `71b38795d8` | Match TUI status shimmer timing to desktop headers (#48757) | ✅ 已落地（队长核） — e81682ce | ①`git log --grep="#48757"`=0 ②`rg '#48757' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 71b38795d8`=3 文件（codex-rs/tui/src/snapshots/codex_tui__status_indicator_widget__summary_shimmer__tests__short_and_long_labels_sweep_smoothly_in_both_themes.snap; codex-rs/tui/src/summary_shimmer.rs）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #48761 | `e75b6b1e0c` | Show hidden output line counts in compact terminal activity (#48 | ⬜ 未落地 | ①`git log --grep="#48761"`=0 ②`rg '#48761' -g '*.go'`=0 ③最长标识符 `terminal_output_disclosure_follows_live_history_and_keymap` 在 Go 0 文件 ④`git show --stat e75b6b1e0c`=12 文件（codex-rs/tui/src/chatwidget/activity_presentation.rs; codex-rs/tui/src/exec_cell/compact.rs）→ 落点候选 tui/（部分在 app/） |
| #48772 | `fdbce2080c` | Fix Unix socket connections through long symlink paths (#48772) | ⬜ 未落地 | ①`git log --grep="#48772"`=0 ②`rg '#48772' -g '*.go'`=0 ③最长标识符 `long_control_socket_paths_connect_to_distinct_daemons` 在 Go 0 文件 ④`git show --stat fdbce2080c`=2 文件（codex-rs/app-server-transport/src/transport/unix_socket_tests.rs; codex-rs/uds/src/lib.rs）→ 落点候选 appserver/（transport 面）；（uds：无直接 Go 包） |
| #48775 | `596f8c5c3c` | Match pinned transcript headers to the original prompt style (#4 | ⬜ 未落地 | ①`git log --grep="#48775"`=0 ②`rg '#48775' -g '*.go'`=0 ③最长标识符 `pinned_prompt_matches_the_original_prompt_style` 在 Go 0 文件 ④`git show --stat 596f8c5c3c`=4 文件（codex-rs/tui/src/transcript_view/prompt_header.rs; codex-rs/tui/src/transcript_view/prompt_header_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #48776 | `89bf86d0bd` | Remove the `current` badge from TUI task rows (#48776) | ✅ 已落地（队长核） — 3426c4d9 | ①`git log --grep="#48776"`=0 ②`rg '#48776' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 89bf86d0bd`=5 文件（codex-rs/tui/src/app/agent_center/rows.rs; codex-rs/tui/src/app/agents_overview_tests.rs）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #48779 | `21eb35513d` | Preserve independent Guardian history across parent compaction ( | ⬜ 未落地 | ①`git log --grep="#48779"`=0 ②`rg '#48779' -g '*.go'`=0 ③最长标识符 `rollback_discards_assistant_sources_without_ordering_in_old_backups` 在 Go 0 文件 ④`git show --stat 21eb35513d`=29 文件（codex-rs/app-server-protocol/schema/precomputed/app-server-exports-stable.json.zst; codex-rs/app-server/tests/suite/v2/guardian_v2_history_tests.rs）→ 落点候选 （app-server-protocol：无直接 Go 包）；appserver/ |
| #48799 | `4c8cf3964d` | Fix SGR mouse reporting for Windows terminal capture (#48799) | ⬜ 未落地 | ①`git log --grep="#48799"`=0 ②`rg '#48799' -g '*.go'`=0 ③最长标识符 `mouse_capture_restores_console_mode_and_encoding` 在 Go 0 文件 ④`git show --stat 4c8cf3964d`=3 文件（codex-rs/tui/src/tui/alternate_screen.rs; codex-rs/tui/src/tui/alternate_screen_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #48805 | `d9487a2930` | Allow transcript wheel scrolling while a modal is open (#48805) | ✅ 已落地（队长核） — 3ba8ede6 | ①`git log --grep="#48805"`=0 ②`rg '#48805' -g '*.go'`=0 ③最长标识符 `plan_menu_allows_transcript_wheel_scrolling_and_keeps_keyboard_ownership` 在 Go 0 文件 ④`git show --stat d9487a2930`=3 文件（codex-rs/tui/src/app/owned_transcript.rs; codex-rs/tui/src/app/owned_transcript_input_tests.rs）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #48812 | `3f4668da20` | Add history-aware prewarming for idle threads (#48812) | ⬜ 未落地 | ①`git log --grep="#48812"`=0 ②`rg '#48812' -g '*.go'`=0 ③最长标识符 `schedule_startup_prewarm_inner` 在 Go 0 文件 ④`git show --stat 3f4668da20`=9 文件（codex-rs/core/src/client.rs; codex-rs/core/src/codex_thread.rs）→ 落点候选 context//session//turn//rollout//state/；（features：无直接 Go 包） |
| #48819 | `456212ca21` | Use explicit histogram buckets for tool and skill context metric | ⬜ 未落地 | ①`git log --grep="#48819"`=0 ②`rg '#48819' -g '*.go'`=0 ③最长标识符 `context_histograms_preserve_zero_and_family_ranges` 在 Go 0 文件 ④`git show --stat 456212ca21`=6 文件（codex-rs/core/src/context/world_state/tools.rs; codex-rs/core/src/context/world_state/tools_tests.rs）→ 落点候选 context//session//turn//rollout//state/；（Go 无 ext/ 目录） |
| #48824 | `84cc4b3fb5` | Keep voice RTP timestamps aligned to 20 ms packets (#48824) | ⬜ 未落地 | ①`git log --grep="#48824"`=0 ②`rg '#48824' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 84cc4b3fb5`=3 文件（codex-rs/voice-host/src/audio_track.rs; codex-rs/voice-host/src/audio_track_tests.rs）→ 落点候选 voicehost/ |
| #48827 | `6af89155d0` | Show a hand pointer over transcript links in Ghostty and Kitty ( | ⬜ 未落地 | ①`git log --grep="#48827"`=0 ②`rg '#48827' -g '*.go'`=0 ③最长标识符 `hover_transitions_are_deduplicated_and_restore_after_leaving_a_link` 在 Go 0 文件 ④`git show --stat 6af89155d0`=9 文件（codex-rs/tui/src/app.rs; codex-rs/tui/src/app/link_hover.rs）→ 落点候选 tui/（部分在 app/） |
| #48828 | `81d5405882` | Allow archiving threads before their first turn (#48828) | ⬜ 未落地 | ①`git log --grep="#48828"`=0 ②`rg '#48828' -g '*.go'`=0 ③最长标识符 `thread_archive_without_turns` 在 Go 0 文件 ④`git show --stat 81d5405882`=2 文件（codex-rs/app-server/src/request_processors/thread_processor.rs; codex-rs/app-server/tests/suite/v2/thread_archive.rs）→ 落点候选 appserver/ |
| #48829 | `e6f4af1d92` | Wait briefly for the Windows sandbox provisioning service to sta | ⬜ 未落地 | ①`git log --grep="#48829"`=0 ②`rg '#48829' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat e6f4af1d92`=1 文件（codex-rs/windows-sandbox-rs/src/provisioning_client.rs）→ 落点候选 sandbox/（Windows 面） |
| #48830 | `1cc7e23612` | Show a short, neutral TUI interruption notice (#48830) | ✅ 已落地（队长核） — f6bfeab0 | ①`git log --grep="#48830"`=0 ②`rg '#48830' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 1cc7e23612`=11 文件（codex-rs/tui/src/app/tests/snapshots/codex_tui__app__tests__math_interruption_tests__answer_math_narrow.snap; codex-rs/tui/src/app/tests/snapshots/codex_tui__app__tests__math_interruption_tests__plan_math_narrow.snap）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #48895 | `44fe510ce3` | Expand native Mermaid flowchart syntax support (#48895) | ⬜ 未落地 | ①`git log --grep="#48895"`=0 ②`rg '#48895' -g '*.go'`=0 ③最长标识符 `equivalent_flowchart_forms_preserve_graph` 在 Go 0 文件 ④`git show --stat 44fe510ce3`=6 文件（codex-rs/mermaid/README.md; codex-rs/mermaid/src/families_tests.rs）→ 落点候选 tui/markdown/；tui/（部分在 app/） |
| #48982 | `06971ec9aa` | Prevent message-board notifications from reopening final answers | ⬜ 未落地 | ①`git log --grep="#48982"`=0 ②`rg '#48982' -g '*.go'`=0 ③最长标识符 `board_notifications_do_not_reopen_a_final_answer` 在 Go 0 文件 ④`git show --stat 06971ec9aa`=4 文件（codex-rs/core/src/agent_message_board.rs; codex-rs/core/src/session/input_queue.rs）→ 落点候选 context//session//turn//rollout//state/；（Go 无 ext/ 目录） |
| #48983 | `c0d26949be` | Avoid full metadata rewrites for thread timestamp updates (#4898 | ⬜ 未落地 | ①`git log --grep="#48983"`=0 ②`rg '#48983' -g '*.go'`=0 ③最长标识符 `timestamp_updates_repair_missing_rows_then_touch_only_timestamp_columns` 在 Go 0 文件 ④`git show --stat c0d26949be`=4 文件（codex-rs/thread-store/src/local/mod.rs; codex-rs/thread-store/src/local/timestamp_metadata_tests.rs）→ 落点候选 session/ |
| #49019 | `4fd5745e84` | Use a compatible PowerShell fallback for the Windows MXC sandbox | ⬜ 未落地 | ①`git log --grep="#49019"`=0 ②`rg '#49019' -g '*.go'`=0 ③最长标识符 `prepare_powershell_command_for_windows_sandbox_with_fallback` 在 Go 0 文件 ④`git show --stat 4fd5745e84`=5 文件（codex-rs/core/src/tools/runtimes/mod.rs; codex-rs/core/src/tools/runtimes/unified_exec.rs）→ 落点候选 context//session//turn//rollout//state/；（shell-command：无直接 Go 包） |
| #49028 | `f817e16905` | Use the numeric ioctl value in the macOS sandbox policy (#49028) | ➖ N/A（队长核）：Go 无 `debug sandbox` 子命令（`cli/completion.go:134` debug 子项 = models/app-server/prompt-input/clear-memories/config），且 `grep -rn TIOCSTI --include=*.go .` = 0 ⇒ 上游该 policy 片段在 Go 不存在 | ①`git log --grep="#49028"`=0 ②`rg '#49028' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat f817e16905`=1 文件（codex-rs/cli/src/debug_sandbox.rs）→ 落点候选 cli/ |
| #49031 | `d8fc718809` | Clarify ChatGPT sign-in success copy (#49031) | ✅ 已落地（队长核） — 9951066a | ①`git log --grep="#49031"`=0 ②`rg '#49031' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat d8fc718809`=1 文件（codex-rs/tui/src/onboarding/auth.rs）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #49032 | `46fdd5ef39` | Avoid SQLite stalls from connection setup and stderr span loggin | ⬜ 未落地 | ①`git log --grep="#49032"`=0 ②`rg '#49032' -g '*.go'`=0 ③最长标识符 `writable_pool_reads_do_not_wait_for_an_existing_writer` 在 Go 0 文件 ④`git show --stat 46fdd5ef39`=4 文件（codex-rs/app-server/src/lib.rs; codex-rs/app-server/src/stderr_logging_tests.rs）→ 落点候选 appserver/；state/ |
| #49037 | `64bf4e7e62` | Show the Plan mode cycling hint in the fullscreen status line (# | ⬜ 未落地 | ①`git log --grep="#49037"`=0 ②`rg '#49037' -g '*.go'`=0 ③最长标识符 `fullscreen_plan_indicator_keeps_the_cycle_hint_when_it_fits` 在 Go 0 文件 ④`git show --stat 64bf4e7e62`=4 文件（codex-rs/tui/src/bottom_pane/chat_composer.rs; codex-rs/tui/src/bottom_pane/chat_composer/snapshots/codex_tui__bottom_pane__chat_composer__status_surface__tests__fullscreen_plan_cycle_hint.snap）→ 落点候选 tui/（部分在 app/） |
| #49038 | `368e5eae2f` | Preserve encrypted agent messages in Guardian reviews (#49038) | ⬜ 未落地 | ①`git log --grep="#49038"`=0 ②`rg '#49038' -g '*.go'`=0 ③最长标识符 `transcript_preserves_encrypted_agent_messages_and_omits_other_encrypted_fields` 在 Go 0 文件 ④`git show --stat 368e5eae2f`=22 文件（codex-rs/core/src/guardian/input_budget.rs; codex-rs/core/src/guardian/prompt.rs）→ 落点候选 context//session//turn//rollout//state/；（Go 无 ext/ 目录） |
| #49041 | `3074be908a` | Copy selections within inline code as plain text (#49041) | ⬜ 未落地 | ①`git log --grep="#49041"`=0 ②`rg '#49041' -g '*.go'`=0 ③最长标识符 `inline_code_selection_copies_only_selected_content` 在 Go 0 文件 ④`git show --stat 3074be908a`=5 文件（codex-rs/tui/src/markdown_copy.rs; codex-rs/tui/src/markdown_copy/table.rs）→ 落点候选 tui/（部分在 app/） |
| #49043 | `4f63088cce` | Update Pro plan display names in the TUI (#49043) | ⬜ 未落地 | ①`git log --grep="#49043"`=0 ②`rg '#49043' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 4f63088cce`=3 文件（codex-rs/tui/src/analytics/render.rs; codex-rs/tui/src/analytics/summary_panel.rs）→ 落点候选 tui/（部分在 app/） |
| #49058 | `df3e439c02` | Fix Windows sandbox ACL repair for long runtime paths (#49058) | ⬜ 未落地 | ①`git log --grep="#49058"`=0 ②`rg '#49058' -g '*.go'`=0 ③最长标识符 `runtime_repair_handles_long_directory_and_file_paths` 在 Go 0 文件 ④`git show --stat df3e439c02`=3 文件（codex-rs/windows-sandbox-rs/src/acl.rs; codex-rs/windows-sandbox-rs/src/setup_provisioning/setup_runtime_bin.rs）→ 落点候选 sandbox/（Windows 面） |
| #49067 | `5a5a4aa796` | Keep configuration values out of Windows sandbox policy events ( | ⬜ 未落地 | ①`git log --grep="#49067"`=0 ②`rg '#49067' -g '*.go'`=0 ③最长标识符 `policy_event_excludes_real_parser_values_but_retains_safe_codes` 在 Go 0 文件 ④`git show --stat 5a5a4aa796`=2 文件（codex-rs/windows-sandbox-service/src/ipc.rs; codex-rs/windows-sandbox-service/src/ipc_tests.rs）→ 落点候选 （Rust-only 服务，未移植） |
| #49069 | `33a0f766a6` | Reclaim unused SQLite log database pages in the background (#490 | ⬜ 部分落地（阶段 A/B 已并入 sync543 `88aa753e` + sync544 `116c4376`；阶段 C 指标 + 打断续跑回归未落） | ①`git log --grep="#49069"`=0 ②`rg '#49069' -g '*.go'`=0 ③最长标识符 `interrupted_reclamation_releases_writer_and_resumes_without_data_loss` 在 Go 0 文件 ④`git show --stat 33a0f766a6`=6 文件（codex-rs/state/src/lib.rs; codex-rs/state/src/runtime.rs）→ 落点候选 state/  **[队长核 · 第 84–85 轮]** |
| #49073 | `15c08beee2` | Surface realtime voice catalog failures in the TUI (#49073) | ⬜ 未落地 | ①`git log --grep="#49073"`=0 ②`rg '#49073' -g '*.go'`=0 ③最长标识符 `remote_voice_catalog_success_and_failure` 在 Go 0 文件 ④`git show --stat 15c08beee2`=6 文件（codex-rs/tui/src/app/realtime_settings.rs; codex-rs/tui/src/app/tests/realtime_requests.rs）→ 落点候选 tui/（部分在 app/） |
| #49074 | `22d1d9336f` | Propagate Cargo package versions to Bazel Rust targets (#49074) | ➖ N/A（队长核）：仅 `MODULE.bazel`/`defs.bzl`/`patches/rules_rs_workspace_package_version.patch`（`git show --stat 22d1d9336f` = 4 文件），Bazel+Rust 目标构建面，Go 无对应物 | ①`git log --grep="#49074"`=0 ②`rg '#49074' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 22d1d9336f`=4 文件（MODULE.bazel; defs.bzl）→ 落点候选 （MODULE.bazel：无直接 Go 包）；（defs.bzl：无直接 Go 包） |
| #49075 | `3749d1eff7` | Preserve pending environments when spawning subagents (#49075) | ⬜ 未落地 | ①`git log --grep="#49075"`=0 ②`rg '#49075' -g '*.go'`=0 ③最长标识符 `updating_another_environment_retries_the_executor_without_canceling_pending_config` 在 Go 0 文件 ④`git show --stat 3749d1eff7`=14 文件（codex-rs/core/src/agent/control/spawn.rs; codex-rs/core/src/agent/control_tests.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49076 | `011f803f3c` | Avoid collecting unused Git metadata in skill analytics (#49076) | ⬜ 未落地 | ①`git log --grep="#49076"`=0 ②`rg '#49076' -g '*.go'`=0 ③最长标识符 `get_git_origin_url` 在 Go 0 文件 ④`git show --stat 011f803f3c`=3 文件（codex-rs/analytics/src/reducer.rs; codex-rs/git-utils/src/info.rs）→ 落点候选 telemetry/（Go 无 analytics/ 目录）；（git-utils：无直接 Go 包） |
| #49079 | `fe50d010e2` | Update and centralize TUI subscription labels (#49079) | ⬜ 未落地 | ①`git log --grep="#49079"`=0 ②`rg '#49079' -g '*.go'`=0 ③最长标识符 `SubscriptionDisplay` 在 Go 0 文件 ④`git show --stat fe50d010e2`=5 文件（codex-rs/tui/src/analytics/render.rs; codex-rs/tui/src/analytics/summary_panel.rs）→ 落点候选 tui/（部分在 app/） |
| #49082 | `46d2585ea4` | Skip remote Git discovery for Guardian diff paths (#49082) | ⬜ 未落地 | ①`git log --grep="#49082"`=0 ②`rg '#49082' -g '*.go'`=0 ③最长标识符 `guardian_revalidates_allow_with_offline_secondary_executor` 在 Go 0 文件 ④`git show --stat 46d2585ea4`=2 文件（codex-rs/core/src/session/turn.rs; codex-rs/core/tests/suite/guardian_environments_tests.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49089 | `222e24b737` | Render follow-up directive labels in the TUI and copied response | ⬜ 未落地 | ①`git log --grep="#49089"`=0 ②`rg '#49089' -g '*.go'`=0 ③最长标识符 `followup_preview_resumes_after_the_directive_leaves_the_window` 在 Go 0 文件 ④`git show --stat 222e24b737`=11 文件（codex-rs/tui/src/assistant_directives.rs; codex-rs/tui/src/assistant_directives_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #49093 | `19892aee0c` | Simplify startup promotions to platform-specific desktop app tip | ⬜ 未落地 | ①`git log --grep="#49093"`=0 ②`rg '#49093' -g '*.go'`=0 ③最长标识符 `desktop_app_tips_render_at_narrow_width` 在 Go 0 文件 ④`git show --stat 19892aee0c`=13 文件（codex-rs/tui/src/app/tests.rs; codex-rs/tui/src/app/tests/session_lifecycle_requests.rs）→ 落点候选 tui/（部分在 app/） |
| #49096 | `2ce64843be` | Update `h2` from 0.4.16 to 0.4.19 in Cargo and Bazel lockfiles ( | ➖ N/A（队长核）：仅 `MODULE.bazel.lock` + `codex-rs/Cargo.lock`（`git show --stat 2ce64843be` = 2 文件 3/3），纯 Cargo/Bazel 锁文件升级，Go 无该构建面 | ①`git log --grep="#49096"`=0 ②`rg '#49096' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 2ce64843be`=2 文件（MODULE.bazel.lock; codex-rs/Cargo.lock）→ 落点候选 （MODULE.bazel.lock：无直接 Go 包）；（codex-rs：无直接 Go 包） |
| #49097 | `9563713df2` | Notify lifecycle extensions of compaction usage limits (#49097) | ⬜ 未落地 | ①`git log --grep="#49097"`=0 ②`rg '#49097' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 9563713df2`=2 文件（codex-rs/core/src/session/turn.rs; codex-rs/core/src/tasks/compact.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49099 | `1260716393` | Cache parsed plugin manifests across plugin workflows (#49099) | ⬜ 未落地 | ①`git log --grep="#49099"`=0 ②`rg '#49099' -g '*.go'`=0 ③最长标识符 `oversized_revision_invalidates_previous_result_without_being_cached` 在 Go 0 文件 ④`git show --stat 1260716393`=13 文件（codex-rs/app-server/tests/suite/v2/mod.rs; codex-rs/app-server/tests/suite/v2/plugin_manifest_cache.rs）→ 落点候选 appserver/；（core-plugins：无直接 Go 包） |
| #49100 | `bfdb157178` | Reuse the HTTP connection pool for remote plugin requests (#4910 | ⬜ 未落地 | ①`git log --grep="#49100"`=0 ②`rg '#49100' -g '*.go'`=0 ③最长标识符 `repeated_service_configs_and_early_clones_share_one_lazy_pool` 在 Go 0 文件 ④`git show --stat bfdb157178`=2 文件（codex-rs/core-plugins/src/manager.rs; codex-rs/core-plugins/src/plugins_config_input_tests.rs）→ 落点候选 （core-plugins：无直接 Go 包） |
| #49102 | `c2d2f422e6` | Preserve SQLite vacuum modes and surface pool initialization err | ✅ 已落地（队长核） — dd900377 | ①`git log --grep="#49102"`=0 ②`rg '#49102' -g '*.go'`=0 ③最长标识符 `open_read_write_pool_preserves_existing_settings_under_write_lock` 在 Go 0 文件 ④`git show --stat c2d2f422e6`=3 文件（codex-rs/state/src/migrations_tests.rs; codex-rs/state/src/sqlite.rs）→ 落点候选 state/  **[队长核 · 第 84–85 轮]** |
| #49105 | `136391a23e` | Resume unsent TUI input after reconnecting (#49105) | ⬜ 未落地 | ①`git log --grep="#49105"`=0 ②`rg '#49105' -g '*.go'`=0 ③最长标识符 `reconnect_resumes_unsent_input_and_reconciles_confirmed_submissions` 在 Go 0 文件 ④`git show --stat 136391a23e`=20 文件（codex-rs/tui/src/app/event_dispatch.rs; codex-rs/tui/src/app/reconnect.rs）→ 落点候选 tui/（部分在 app/） |
| #49106 | `8f6517772b` | Add history pagination to the agent command center (#49106) | ⬜ 未落地 | ①`git log --grep="#49106"`=0 ②`rg '#49106' -g '*.go'`=0 ③最长标识符 `overview_show_more_refills_archived_rows_and_retries_without_losing_rows` 在 Go 0 文件 ④`git show --stat 8f6517772b`=22 文件（codex-rs/tui/src/app.rs; codex-rs/tui/src/app/agent_center/mod.rs）→ 落点候选 tui/（部分在 app/） |
| #49112 | `0196495288` | Add X11 primary selection and middle-click paste support (#49112 | ⬜ 未落地 | ①`git log --grep="#49112"`=0 ②`rg '#49112' -g '*.go'`=0 ③最长标识符 `cancelling_owners_after_poll_drops_deferred_work_without_releasing_worker_early` 在 Go 0 文件 ④`git show --stat 0196495288`=20 文件（codex-rs/tui/src/app.rs; codex-rs/tui/src/app/clipboard.rs）→ 落点候选 tui/（部分在 app/） |
| #49114 | `69043f05c4` | Point remote compaction tests at the mock ChatGPT server (#49114 | ⬜ 未落地 | ①`git log --grep="#49114"`=0 ②`rg '#49114' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 69043f05c4`=1 文件（codex-rs/core/src/session/tests.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49117 | `2e6cc4ed8d` | Attribute analytics requests to each thread's product SKU (#4911 | ⬜ 未落地 | ①`git log --grep="#49117"`=0 ②`rg '#49117' -g '*.go'`=0 ③最长标识符 `buffered_tool_events_preserve_attribution_or_drop_it_on_queue_overflow` 在 Go 0 文件 ④`git show --stat 2e6cc4ed8d`=9 文件（codex-rs/analytics/src/client.rs; codex-rs/analytics/src/client_product_tests.rs）→ 落点候选 telemetry/（Go 无 analytics/ 目录）；context//session//turn//rollout//state/ |
| #49118 | `6c49240565` | Correct provider authentication storage documentation (#49118) | ⬜ 未落地 | ①`git log --grep="#49118"`=0 ②`rg '#49118' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 6c49240565`=2 文件（codex-rs/core/config.schema.json; codex-rs/model-provider-info/src/lib.rs）→ 落点候选 context//session//turn//rollout//state/；（model-provider-info：无直接 Go 包） |
| #49119 | `8bd5a136ff` | Add recovery guidance to content-filter retries (#49119) | ⬜ 未落地 | ①`git log --grep="#49119"`=0 ②`rg '#49119' -g '*.go'`=0 ③最长标识符 `map_api_error_preserves_content_filter_retry_and_public_error` 在 Go 0 文件 ④`git show --stat 8bd5a136ff`=23 文件（codex-rs/app-server/tests/common/models_cache.rs; codex-rs/cli/src/doctor.rs）→ 落点候选 appserver/；cli/ |
| #49127 | `13f580ef09` | Deduplicate cloud and executor skill listings before budgeting ( | ⬜ 未落地 | ①`git log --grep="#49127"`=0 ②`rg '#49127' -g '*.go'`=0 ③最长标识符 `cloud_preference_preserves_executor_aliases_and_description_budget` 在 Go 0 文件 ④`git show --stat 13f580ef09`=10 文件（codex-rs/core/tests/suite/scenarios.rs; codex-rs/core/tests/suite/scenarios_skill_catalog_dedup.rs）→ 落点候选 context//session//turn//rollout//state/；（Go 无 ext/ 目录） |
| #49130 | `a9118edae8` | Move content-filter guidance into the shared Responses retry han | ⬜ 未落地 | ①`git log --grep="#49130"`=0 ②`rg '#49130' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat a9118edae8`=4 文件（codex-rs/core/src/compact_remote_v2.rs; codex-rs/core/src/responses_retry.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49135 | `458f7046a5` | Treat explicit provider model catalogs as authoritative (#49135) | ⬜ 未落地 | ①`git log --grep="#49135"`=0 ②`rg '#49135' -g '*.go'`=0 ③最长标识符 `authoritative_catalog_failure_invalidates_cache_until_refresh_succeeds` 在 Go 0 文件 ④`git show --stat 458f7046a5`=10 文件（codex-rs/app-server/tests/suite/v2/model_list.rs; codex-rs/core/src/session/mod.rs）→ 落点候选 appserver/；context//session//turn//rollout//state/ |
| #49138 | `f53f5a6fed` | Expose original error details to turn lifecycle contributors (#4 | ⬜ 未落地 | ①`git log --grep="#49138"`=0 ②`rg '#49138' -g '*.go'`=0 ③最长标识符 `turn_error_details_preserve_usage_reset_and_compaction_outcomes` 在 Go 0 文件 ④`git show --stat f53f5a6fed`=9 文件（codex-rs/core/src/session/tests.rs; codex-rs/core/src/session/turn.rs）→ 落点候选 context//session//turn//rollout//state/；（Go 无 ext/ 目录） |
| #49144 | `ff3c82c8a9` | Preserve server reasoning summary and verbosity settings in the  | ⬜ 未落地 | ①`git log --grep="#49144"`=0 ②`rg '#49144' -g '*.go'`=0 ③最长标识符 `config_overrides_forward_explicit_summary_and_verbosity` 在 Go 0 文件 ④`git show --stat ff3c82c8a9`=2 文件（codex-rs/tui/src/app_server_session.rs; codex-rs/tui/src/app_server_session/reasoning_defaults_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #49145 | `8d48f71922` | Hide reasoning summary settings in `/status` for server connecti | ⬜ 未落地 | ①`git log --grep="#49145"`=0 ②`rg '#49145' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 8d48f71922`=5 文件（codex-rs/tui/src/status/card.rs; codex-rs/tui/src/status/snapshots/codex_tui__status__tests__status_snapshot_local_background_server.snap）→ 落点候选 tui/（部分在 app/） |
| #49147 | `3a16c0b707` | Simplify cloud task base URL normalization (#49147) | ⬜ 未落地 | ①`git log --grep="#49147"`=0 ②`rg '#49147' -g '*.go'`=0 ③最长标识符 `normalize_base_url_normalizes_urls` 在 Go 0 文件 ④`git show --stat 3a16c0b707`=1 文件（codex-rs/cloud-tasks/src/util.rs）→ 落点候选 （cloud-tasks：无直接 Go 包） |
| #49153 | `c6c7c8d270` | Omit blockquote markers when copying quoted selections in the TU | ⬜ 未落地 | ①`git log --grep="#49153"`=0 ②`rg '#49153' -g '*.go'`=0 ③最长标识符 `blockquote_selection_copies_only_selected_content` 在 Go 0 文件 ④`git show --stat c6c7c8d270`=5 文件（codex-rs/tui/src/markdown_copy.rs; codex-rs/tui/src/markdown_render.rs）→ 落点候选 tui/（部分在 app/） |
| #49160 | `0d7b8117d3` | Support projectless TUI sessions with workspace defaults (#49160 | 🟡 部分（c1 已落） | c1 = `77732afa` sync554（`TrustCancelCurrentTask` + "Keep current directory" + `Escape()`）；a 已派 `syncnext4`，b/c2 先侦察后定 | ①`git log --grep="#49160"`=0 ②`rg '#49160' -g '*.go'`=0 ③最长标识符 `local_projectless_defaults_respect_trust_scope_and_explicit_settings` 在 Go 0 文件 ④`git show --stat 0d7b8117d3`=55 文件（codex-rs/cli/tests/doctor_path_safety.rs; codex-rs/config/src/loader/mod.rs）→ 落点候选 cli/；config/ |
| #49161 | `3226512d47` | Honor app-server provider defaults in the TUI (#49161) | ⬜ 未落地 | ①`git log --grep="#49161"`=0 ②`rg '#49161' -g '*.go'`=0 ③最长标识符 `history_lookup_uses_server_provider_with_local_and_embedded_servers` 在 Go 0 文件 ④`git show --stat 3226512d47`=19 文件（codex-rs/tui/src/app/event_dispatch.rs; codex-rs/tui/src/app/reconnect.rs）→ 落点候选 tui/（部分在 app/） |
| #49164 | `fbc169827e` | Suppress Windows console windows for background subprocesses (#4 | ⬜ 未落地 | ①`git log --grep="#49164"`=0 ②`rg '#49164' -g '*.go'`=0 ③最长标识符 `background_launches_keep_consoles_hidden_when_containment_fails` 在 Go 0 文件 ④`git show --stat fbc169827e`=44 文件（codex-rs/Cargo.lock; codex-rs/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；appserver/ |
| #49171 | `c248f6d48b` | Fix model provider lookup for TUI history (#49171) | ⬜ 未落地 | ①`git log --grep="#49171"`=0 ②`rg '#49171' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat c248f6d48b`=1 文件（codex-rs/tui/src/app_server_session/provider_selection.rs）→ 落点候选 tui/（部分在 app/） |
| #49246 | `0462dcc062` | Use executable fixture copying in the bundled bwrap test (#49246 | ⬜ 未落地 | ①`git log --grep="#49246"`=0 ②`rg '#49246' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 0462dcc062`=3 文件（codex-rs/Cargo.lock; codex-rs/linux-sandbox/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；（Go 用外部 codex-linux-sandbox 二进制） |
| #49257 | `4f16bdc265` | Allow Guardian cached approvals with incomplete root context (#4 | ⬜ 未落地 | ①`git log --grep="#49257"`=0 ②`rg '#49257' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 4f16bdc265`=2 文件（codex-rs/ext/guardian-v2/src/async_scorer/approval.rs; codex-rs/ext/guardian-v2/src/async_scorer/extension_cached_delivery_tests.rs）→ 落点候选 （Go 无 ext/ 目录） |
| #49260 | `af0d68a236` | Restrict enterprise MCP auth and fail closed on config refresh ( | ⬜ 未落地 | ①`git log --grep="#49260"`=0 ②`rg '#49260' -g '*.go'`=0 ③最长标识符 `rejected_mcp_refresh_then_corrected_user_config_blocks_ordinary_replacement` 在 Go 0 文件 ④`git show --stat af0d68a236`=46 文件（codex-rs/app-server/README.md; codex-rs/app-server/src/mcp_refresh.rs）→ 落点候选 appserver/；（codex-mcp：无直接 Go 包） |
| #49261 | `f35a0fdc5d` | Preserve Windows sandbox runner launch errors (#49261) | ⬜ 未落地 | ①`git log --grep="#49261"`=0 ②`rg '#49261' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat f35a0fdc5d`=1 文件（codex-rs/windows-sandbox-rs/src/elevated/runner_client.rs）→ 落点候选 sandbox/（Windows 面） |
| #49267 | `68e1a421f5` | Support remote agent message boards in multi-agent sessions (#49 | ⬜ 未落地 | ①`git log --grep="#49267"`=0 ②`rg '#49267' -g '*.go'`=0 ③最长标识符 `remote_board_uses_the_existing_tools_and_session_identity` 在 Go 0 文件 ④`git show --stat 68e1a421f5`=14 文件（codex-rs/Cargo.lock; codex-rs/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；config/ |
| #49269 | `0b1b78a4f1` | Preserve thread overrides and cloud policy validity during confi | ⬜ 未落地 | ①`git log --grep="#49269"`=0 ②`rg '#49269' -g '*.go'`=0 ③最长标识符 `user_reload_promotes_plugin_and_feature_requirements_without_server_changes` 在 Go 0 文件 ④`git show --stat 0b1b78a4f1`=29 文件（codex-rs/app-server/src/application_network.rs; codex-rs/app-server/src/config_manager.rs）→ 落点候选 appserver/；（cloud-config：无直接 Go 包） |
| #49280 | `18194bfd35` | Restrict capability roots to captured turn environments (#49280) | ⬜ 未落地 | ①`git log --grep="#49280"`=0 ②`rg '#49280' -g '*.go'`=0 ③最长标识符 `selected_capability_stack_tracks_environment_selection_and_resume` 在 Go 0 文件 ④`git show --stat 18194bfd35`=4 文件（codex-rs/app-server/tests/suite/v2/selected_capability_stack.rs; codex-rs/core/src/session/mcp.rs）→ 落点候选 appserver/；context//session//turn//rollout//state/ |
| #49290 | `4773a132c3` | Add `/mcp login <name>` to the TUI (#49290) | ⬜ 未落地 | ①`git log --grep="#49290"`=0 ②`rg '#49290' -g '*.go'`=0 ③最长标识符 `mcp_login_retry_suppresses_old_completion_and_late_browser_open` 在 Go 0 文件 ④`git show --stat 4773a132c3`=23 文件（codex-rs/tui/src/app.rs; codex-rs/tui/src/app/app_server_event_targets.rs）→ 落点候选 tui/（部分在 app/） |
| #49294 | `d79a95bdf8` | Record Guardian context mode in review and classification teleme | ⬜ 未落地 | ①`git log --grep="#49294"`=0 ②`rg '#49294' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat d79a95bdf8`=10 文件（codex-rs/analytics/src/events.rs; codex-rs/analytics/src/guardian_v2.rs）→ 落点候选 telemetry/（Go 无 analytics/ 目录）；context//session//turn//rollout//state/ |
| #49295 | `cf12c86dc5` | Simplify configuration fingerprint canonicalization (#49295) | ⬜ 未落地 | ①`git log --grep="#49295"`=0 ②`rg '#49295' -g '*.go'`=0 ③最长标识符 `fingerprint_preserves_nested_key_order_independence_and_existing_hash` 在 Go 0 文件 ④`git show --stat cf12c86dc5`=2 文件（codex-rs/config/src/fingerprint.rs; codex-rs/config/src/fingerprint_tests.rs）→ 落点候选 config/ |
| #49300 | `a6f09397aa` | Compact the inline hidden tag buffer once per chunk (#49300) | ⬜ 未落地 | ①`git log --grep="#49300"`=0 ②`rg '#49300' -g '*.go'`=0 ③最长标识符 `generic_inline_parser_preserves_output_across_every_chunk_boundary` 在 Go 0 文件 ④`git show --stat a6f09397aa`=1 文件（codex-rs/utils/stream-parser/src/inline_hidden_tag.rs）→ 落点候选 （utils：无直接 Go 包） |
| #49305 | `c2837d8ece` | Batch metadata reads when resolving thread names (#49305) | ⬜ 未落地 | ①`git log --grep="#49305"`=0 ②`rg '#49305' -g '*.go'`=0 ③最长标识符 `get_threads_preserves_valid_metadata_across_batches` 在 Go 0 文件 ④`git show --stat c2837d8ece`=4 文件（codex-rs/state/src/runtime.rs; codex-rs/state/src/runtime/thread_metadata.rs）→ 落点候选 state/；session/ |
| #49308 | `50d9c5deac` | Run piped legacy Windows sandbox processes without a console (#4 | ⬜ 未落地 | ①`git log --grep="#49308"`=0 ②`rg '#49308' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 50d9c5deac`=3 文件（codex-rs/windows-sandbox-rs/src/lib.rs; codex-rs/windows-sandbox-rs/src/unified_exec/backends/legacy.rs）→ 落点候选 sandbox/（Windows 面） |
| #49312 | `63475131ce` | Notify parent agents when Guardian stops a subagent (#49312) | ⬜ 未落地 | ①`git log --grep="#49312"`=0 ②`rg '#49312' -g '*.go'`=0 ③最长标识符 `guardian_interruption_message_stays_below_manual_review_threshold` 在 Go 0 文件 ④`git show --stat 63475131ce`=7 文件（codex-rs/core/src/agent/api.rs; codex-rs/core/src/agent/control/completion.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49318 | `b1e72963c3` | Add GPT-6.1 Sol as the default catalog model (#49318) | ⬜ 未落地 | ①`git log --grep="#49318"`=0 ②`rg '#49318' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat b1e72963c3`=22 文件（codex-rs/core/tests/suite/snapshots/all__suite__scenarios__astra_active_environment_selection.snap; codex-rs/core/tests/suite/snapshots/all__suite__scenarios__astra_async_question_and_answer.snap）→ 落点候选 context//session//turn//rollout//state/；（models-manager：无直接 Go 包） |
| #49325 | `26dd19ef47` | Retry Windows sandbox runner logon once on error 1056 (#49325) | ✅ 已落地（队长核） — fe88a5a8 | ①`git log --grep="#49325"`=0 ②`rg '#49325' -g '*.go'`=0 ③最长标识符 `retry_uses_original_unified_exec_request_and_stops_after_second_failure` 在 Go 0 文件 ④`git show --stat 26dd19ef47`=2 文件（codex-rs/windows-sandbox-rs/src/elevated/runner_client.rs; codex-rs/windows-sandbox-rs/src/unified_exec/backends/elevated_tests.rs）→ 落点候选 sandbox/（Windows 面）  **[队长核 · 第 84–85 轮]** |
| #49339 | `a6e9eaa9bd` | Add GPT-6.1 Sol to Bedrock catalogs and make it the default (#49 | ✅ 已落地（队长核） — e721afed | ①`git log --grep="#49339"`=0 ②`rg '#49339' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat a6e9eaa9bd`=9 文件（codex-rs/app-server/tests/suite/v2/thread_start.rs; codex-rs/model-provider-info/src/lib.rs）→ 落点候选 appserver/；（model-provider-info：无直接 Go 包）  **[队长核 · 第 84–85 轮]** |
| #49345 | `8ffd91e42a` | Enable multi-agent V2 and Ultra reasoning on Amazon Bedrock (#49 | ⬜ 未落地 | ①`git log --grep="#49345"`=0 ②`rg '#49345' -g '*.go'`=0 ③最长标识符 `bedrock_catalog_selects_v2_by_default` 在 Go 0 文件 ④`git show --stat 8ffd91e42a`=9 文件（codex-rs/core/tests/suite/bedrock_multi_agent_tests.rs; codex-rs/core/tests/suite/mod.rs）→ 落点候选 context//session//turn//rollout//state/；（model-provider：无直接 Go 包） |
| #49353 | `804d6306e8` | Allow approved filesystem escalation while preserving denied rea | ⬜ 未落地 | ①`git log --grep="#49353"`=0 ②`rg '#49353' -g '*.go'`=0 ③最长标识符 `deny_read_preserves_the_sandbox_for_explicit_escalation_and_blocks_policy_bypass` 在 Go 0 文件 ④`git show --stat 804d6306e8`=10 文件（codex-rs/core/src/tools/orchestrator.rs; codex-rs/core/src/tools/runtimes/unified_exec.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49357 | `1983c48fd1` | Continue Markdown blockquotes when pasting multiline text (#4935 | ✅ 已落地（队长核） — 98d4c7cb | ①`git log --grep="#49357"`=0 ②`rg '#49357' -g '*.go'`=0 ③最长标识符 `target_text_preserves_vim_replace_and_backspace_recovery` 在 Go 0 文件 ④`git show --stat 1983c48fd1`=10 文件（codex-rs/tui/src/bottom_pane/chat_composer.rs; codex-rs/tui/src/bottom_pane/chat_composer/blockquote_paste_tests.rs）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #49384 | `a44afa527c` | Track credential storage outcomes and redact sensitive errors (# | ⬜ 未落地 | ①`git log --grep="#49384"`=0 ②`rg '#49384' -g '*.go'`=0 ③最长标识符 `storage_metrics_distinguish_explicit_file_from_secure_failure_fallback` 在 Go 0 文件 ④`git show --stat a44afa527c`=24 文件（codex-rs/Cargo.lock; codex-rs/app-server/src/message_processor.rs）→ 落点候选 （codex-rs：无直接 Go 包）；appserver/ |
| #49389 | `9212b3eca8` | Serialize tests that share Windows sandbox accounts (#49389) | ⬜ 未落地 | ①`git log --grep="#49389"`=0 ②`rg '#49389' -g '*.go'`=0 ③最长标识符 `windows_sandbox_account_test_guard` 在 Go 0 文件 ④`git show --stat 9212b3eca8`=13 文件（codex-rs/.config/nextest.toml; codex-rs/Cargo.lock）→ 落点候选 （.config：无直接 Go 包）；（codex-rs：无直接 Go 包） |
| #49392 | `05ea5f757e` | Add attributed MCP OAuth credential storage telemetry (#49392) | ⬜ 未落地 | ①`git log --grep="#49392"`=0 ②`rg '#49392' -g '*.go'`=0 ③最长标识符 `policy_and_pinned_operations_keep_distinct_outcomes_and_originator` 在 Go 0 文件 ④`git show --stat 05ea5f757e`=25 文件（codex-rs/Cargo.lock; codex-rs/app-server/src/request_processors/mcp_processor.rs）→ 落点候选 （codex-rs：无直接 Go 包）；appserver/ |
| #49401 | `87d3e06847` | Preserve live tool-call metadata across request windows (#49401) | ⬜ 未落地 | ①`git log --grep="#49401"`=0 ②`rg '#49401' -g '*.go'`=0 ③最长标识符 `direct_metadata_follows_output_ids_with_reused_call_ids_and_reverse_completion` 在 Go 0 文件 ④`git show --stat 87d3e06847`=13 文件（codex-rs/core/src/client_tests.rs; codex-rs/core/src/session/mod.rs）→ 落点候选 context//session//turn//rollout//state/；（protocol：无直接 Go 包） |
| #49411 | `eefe0ce1a8` | Bind the app-server time provider to a local variable (#49411) | ⬜ 未落地 | ①`git log --grep="#49411"`=0 ②`rg '#49411' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat eefe0ce1a8`=1 文件（codex-rs/app-server/src/message_processor.rs）→ 落点候选 appserver/ |
| #49416 | `ab84d71f57` | Omit payloads from multiline ANSI warnings (#49416) | ⬜ 未落地 | ①`git log --grep="#49416"`=0 ②`rg '#49416' -g '*.go'`=0 ③最长标识符 `multiline_warning_contains_counts_and_preserves_the_styled_first_line` 在 Go 0 文件 ④`git show --stat ab84d71f57`=4 文件（codex-rs/Cargo.lock; codex-rs/ansi-escape/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；（ansi-escape：无直接 Go 包） |
| #49425 | `8ea2c0e0d4` | Prune diagnostic logs periodically by age and database size (#49 | ✅ 已落地（队长核） — 65f28ce0 | ①`git log --grep="#49425"`=0 ②`rg '#49425' -g '*.go'`=0 ③最长标识符 `periodic_cleanup_prunes_without_new_log_writes_or_restart` 在 Go 0 文件 ④`git show --stat 8ea2c0e0d4`=4 文件（codex-rs/state/src/runtime.rs; codex-rs/state/src/runtime/logs.rs）→ 落点候选 state/  **[队长核 · 第 84–85 轮]** |
| #49432 | `d8f69ea8bc` | Preserve bootstrap discovery across authentication changes (#494 | ⬜ 未落地 | ①`git log --grep="#49432"`=0 ②`rg '#49432' -g '*.go'`=0 ③最长标识符 `bootstrap_discovery_survives_identity_installation_but_account_access_is_revoked` 在 Go 0 文件 ④`git show --stat d8f69ea8bc`=4 文件（codex-rs/app-server/src/in_process_bootstrap.rs; codex-rs/app-server/src/in_process_bootstrap_tests.rs）→ 落点候选 appserver/；（login：无直接 Go 包） |
| #49441 | `6ba4bf9e64` | Honor server retry advice across Responses retries and fallback  | ⬜ 未落地 | ①`git log --grep="#49441"`=0 ②`rg '#49441' -g '*.go'`=0 ③最长标识符 `pre_sampling_compact_advised_errors_fall_back_then_retry_on_selected_model` 在 Go 0 文件 ④`git show --stat 6ba4bf9e64`=6 文件（codex-rs/codex-api/src/api_bridge_tests.rs; codex-rs/codex-api/src/endpoint/responses_websocket.rs）→ 落点候选 codexapi/；context//session//turn//rollout//state/ |
| #49444 | `8c3612fb63` | Use `memrchr` to find newlines in reverse JSONL scans (#49444) | ⬜ 未落地 | ①`git log --grep="#49444"`=0 ②`rg '#49444' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 8c3612fb63`=3 文件（codex-rs/Cargo.lock; codex-rs/rollout/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；rollout/ |
| #49467 | `76a6e55d5a` | Restore executor tool paths after login shell startup (#49467) | ⬜ 未落地 | ①`git log --grep="#49467"`=0 ②`rg '#49467' -g '*.go'`=0 ③最长标识符 `test_get_command_does_not_change_nested_login_or_powershell` 在 Go 0 文件 ④`git show --stat 76a6e55d5a`=5 文件（codex-rs/core/src/shell.rs; codex-rs/core/src/shell_tests.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49472 | `b588812e8c` | Use server-authoritative permissions in the TUI (#49472) | ⬜ 未落地 | ①`git log --grep="#49472"`=0 ②`rg '#49472' -g '*.go'`=0 ③最长标识符 `custom_permission_selection_uses_server_definition_and_preserves_state_on_rejection` 在 Go 0 文件 ④`git show --stat b588812e8c`=32 文件（codex-rs/tui/src/app/agents_overview.rs; codex-rs/tui/src/app/agents_overview_new.rs）→ 落点候选 tui/（部分在 app/） |
| #49473 | `c9b3924a62` | Use the rmcp SDK for enterprise-managed token exchanges (#49473) | ⬜ 未落地 | ①`git log --grep="#49473"`=0 ②`rg '#49473' -g '*.go'`=0 ③最长标识符 `sdk_validation_rejects_unsupported_authorization_before_returning_a_bearer` 在 Go 0 文件 ④`git show --stat c9b3924a62`=9 文件（MODULE.bazel.lock; codex-rs/Cargo.lock）→ 落点候选 （MODULE.bazel.lock：无直接 Go 包）；（codex-rs：无直接 Go 包） |
| #49478 | `15fd656ddb` | Discover and validate MCP authorization servers before ID-JAG ex | ⬜ 未落地 | ①`git log --grep="#49478"`=0 ②`rg '#49478' -g '*.go'`=0 ③最长标识符 `discovery_pins_resource_issuer_and_public_jwt_bearer_before_identity` 在 Go 0 文件 ④`git show --stat 15fd656ddb`=11 文件（codex-rs/network-proxy/src/lib.rs; codex-rs/network-proxy/src/policy.rs）→ 落点候选 （network-proxy：无直接 Go 包）；（protocol：无直接 Go 包） |
| #49489 | `bcd6d9ab6b` | Add regression coverage for account switches between analytics b | ⬜ 未落地 | ①`git log --grep="#49489"`=0 ②`rg '#49489' -g '*.go'`=0 ③最长标识符 `account_switch_between_batches_does_not_send_old_credentials` 在 Go 0 文件 ④`git show --stat bcd6d9ab6b`=3 文件（codex-rs/analytics/src/client_tests.rs; codex-rs/core/src/tools/registry.rs）→ 落点候选 telemetry/（Go 无 analytics/ 目录）；context//session//turn//rollout//state/ |
| #49517 | `d42056091a` | Add a fork shortcut to the TUI command center (#49517) | ⬜ 未落地 | ①`git log --grep="#49517"`=0 ②`rg '#49517' -g '*.go'`=0 ③最长标识符 `overview_fork_preserves_an_open_side_conversation` 在 Go 0 文件 ④`git show --stat d42056091a`=32 文件（codex-rs/config/src/tui_keymap.rs; codex-rs/core/config.schema.json）→ 落点候选 config/；context//session//turn//rollout//state/ |
| #49564 | `0b43721d8d` | Copy selected file paths as plain text in the TUI (#49564) | ⬜ 未落地 | ①`git log --grep="#49564"`=0 ②`rg '#49564' -g '*.go'`=0 ③最长标识符 `mixed_file_targets_preserve_markdown_escaping` 在 Go 0 文件 ④`git show --stat 0b43721d8d`=5 文件（codex-rs/tui/src/markdown_copy.rs; codex-rs/tui/src/markdown_copy/table.rs）→ 落点候选 tui/（部分在 app/） |
| #49584 | `a5cce8895a` | Skip host skill discovery for Guardian reviews (#49584) | ⬜ 未落地 | ①`git log --grep="#49584"`=0 ②`rg '#49584' -g '*.go'`=0 ③最长标识符 `guardian_reviews_with_offline_primary_executor` 在 Go 0 文件 ④`git show --stat a5cce8895a`=4 文件（codex-rs/core/src/session/session.rs; codex-rs/core/src/session/turn_context.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49599 | `d7b0d4aa66` | Return authoritative replay history when resuming a thread (#495 | ⬜ 未落地 | ①`git log --grep="#49599"`=0 ②`rg '#49599' -g '*.go'`=0 ③最长标识符 `resume_thread_reopens_live_writer_and_appends_with_stale_sqlite_path` 在 Go 0 文件 ④`git show --stat d7b0d4aa66`=10 文件（codex-rs/core/src/session/session.rs; codex-rs/core/tests/suite/guardian_persistence_tests.rs）→ 落点候选 context//session//turn//rollout//state/；session/ |
| #49600 | `92bc601ad6` | Reuse unchanged history snapshots when resuming threads (#49600) | ⬜ 未落地 | ①`git log --grep="#49600"`=0 ②`rg '#49600' -g '*.go'`=0 ③最长标识符 `resolve_requested_rollout_path` 在 Go 0 文件 ④`git show --stat 92bc601ad6`=47 文件（codex-rs/app-server/src/request_processors/thread_processor.rs; codex-rs/core/src/agent/control/spawn.rs）→ 落点候选 appserver/；context//session//turn//rollout//state/ |
| #49624 | `7219fd735b` | Use server authentication for explicit remote session commands ( | ⬜ 未落地 | ①`git log --grep="#49624"`=0 ②`rg '#49624' -g '*.go'`=0 ③最长标识符 `remote_session_commands_with_workload_identity_use_server_auth` 在 Go 0 文件 ④`git show --stat 7219fd735b`=5 文件（codex-rs/Cargo.lock; codex-rs/cli/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；cli/ |
| #49642 | `67727e7cf1` | Allow managed requirements to disable the Windows MXC sandbox (# | ⬜ 未落地 | ①`git log --grep="#49642"`=0 ②`rg '#49642' -g '*.go'`=0 ③最长标识符 `managed_mxc_opt_out_blocks_preferred_and_explicit_selection` 在 Go 0 文件 ④`git show --stat 67727e7cf1`=7 文件（codex-rs/app-server/src/request_processors/config_processor.rs; codex-rs/config/src/config_requirements.rs）→ 落点候选 appserver/；config/ |
| #49675 | `ed0cc1a4ab` | Serialize Responses routing fields before large inputs (#49675) | ⬜ 未落地 | ①`git log --grep="#49675"`=0 ②`rg '#49675' -g '*.go'`=0 ③最长标识符 `responses_client_stream_request_sends_routing_fields_ahead_of_large_input` 在 Go 0 文件 ④`git show --stat ed0cc1a4ab`=3 文件（codex-rs/codex-api/src/common.rs; codex-rs/codex-api/src/endpoint/responses_websocket.rs）→ 落点候选 codexapi/ |
| #49678 | `fcbed044c8` | Escape command drafts when recovering question answers (#49678) | ⬜ 未落地 | ①`git log --grep="#49678"`=0 ②`rg '#49678' -g '*.go'`=0 ③最长标识符 `question_turn_end_escapes_command_drafts_before_submission` 在 Go 0 文件 ④`git show --stat fcbed044c8`=5 文件（codex-rs/tui/src/bottom_pane/chat_composer.rs; codex-rs/tui/src/bottom_pane/chat_composer/inline_input.rs）→ 落点候选 tui/（部分在 app/） |
| #49686 | `49be2c7ab0` | Deliver remote message board notifications to active turns (#496 | ⬜ 未落地 | ①`git log --grep="#49686"`=0 ②`rg '#49686' -g '*.go'`=0 ③最长标识符 `InstallNotifications` 在 Go 0 文件 ④`git show --stat 49be2c7ab0`=12 文件（codex-rs/Cargo.lock; codex-rs/agent-message-board-client/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；（agent-message-board-client：无直接 Go 包） |
| #49690 | `7e8878f605` | Preserve PowerShell relative paths in the elevated Windows sandb | ⬜ 未落地 | ①`git log --grep="#49690"`=0 ②`rg '#49690' -g '*.go'`=0 ③最长标识符 `noninheriting_read_attributes_preserve_unprotected_child_dacl` 在 Go 0 文件 ④`git show --stat 7e8878f605`=7 文件（codex-rs/core/tests/suite/windows_sandbox.rs; codex-rs/windows-sandbox-rs/src/acl.rs）→ 落点候选 context//session//turn//rollout//state/；sandbox/（Windows 面） |
| #49692 | `d1c4e3c3e6` | Keep compressed rollout snippet searches on one blocking worker  | ⬜ 未落地 | ①`git log --grep="#49692"`=0 ②`rg '#49692' -g '*.go'`=0 ③最长标识符 `compressed_search_preserves_first_visible_snippet_and_no_match` 在 Go 0 文件 ④`git show --stat d1c4e3c3e6`=5 文件（codex-rs/rollout/src/compression.rs; codex-rs/rollout/src/compression/blocking_reader.rs）→ 落点候选 rollout/ |
| #49694 | `5aa92804d2` | Batch rollout listing scans on cancellable blocking workers (#49 | ⬜ 未落地 | ①`git log --grep="#49694"`=0 ②`rg '#49694' -g '*.go'`=0 ③最长标识符 `updated_candidates_preserve_layout_and_plain_sibling_preference` 在 Go 0 文件 ④`git show --stat 5aa92804d2`=5 文件（codex-rs/rollout/src/compression.rs; codex-rs/rollout/src/compression/path_metadata.rs）→ 落点候选 rollout/ |
| #49702 | `3b16b5a5b0` | Rename exec-server file handle management identifiers (#49702) | ⬜ 未落地 | ①`git log --grep="#49702"`=0 ②`rg '#49702' -g '*.go'`=0 ③最长标识符 `validate_file_handle_id` 在 Go 0 文件 ④`git show --stat 3b16b5a5b0`=3 文件（codex-rs/exec-server/src/file_handle.rs; codex-rs/exec-server/src/lib.rs）→ 落点候选 execserver/ |
| #49706 | `17e5a4dbcb` | Upgrade the argument comment lint toolchain and Dylint (#49706) | ⬜ 未落地 | ①`git log --grep="#49706"`=0 ②`rg '#49706' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 17e5a4dbcb`=14 文件（.github/workflows/rust-ci-full.yml; .github/workflows/rust-ci.yml）→ 落点候选 （.github：无直接 Go 包）；（MODULE.bazel：无直接 Go 包） |
| #49708 | `4f699cd642` | Move session index I/O off async runtime threads (#49708) | ⬜ 未落地 | ①`git log --grep="#49708"`=0 ②`rg '#49708' -g '*.go'`=0 ③最长标识符 `cancelled_index_update_holds_lock_until_worker_finishes` 在 Go 0 文件 ④`git show --stat 4f699cd642`=2 文件（codex-rs/rollout/src/session_index.rs; codex-rs/rollout/src/session_index_tests.rs）→ 落点候选 rollout/ |
| #49713 | `18131270fe` | Remove repository-local Codex guidance, skills, and environment  | ⬜ 未落地 | ①`git log --grep="#49713"`=0 ②`rg '#49713' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 18131270fe`=19 文件（.codex/environments/environment.toml; .codex/skills/babysit-pr/SKILL.md）→ 落点候选 （.codex：无直接 Go 包）；（AGENTS.md：无直接 Go 包） |
| #49714 | `f151a0f5c2` | Decouple API-key cyber access programs from model discovery (#49 | ⬜ 未落地 | ①`git log --grep="#49714"`=0 ②`rg '#49714' -g '*.go'`=0 ③最长标识符 `api_key_cyber_access_program_requires_only_forwarding_feature` 在 Go 0 文件 ④`git show --stat f151a0f5c2`=2 文件（codex-rs/app-server/tests/suite/v2/cyber_access_program.rs; codex-rs/core/src/cyber_access_program.rs）→ 落点候选 appserver/；context//session//turn//rollout//state/ |
| #49715 | `60947e2341` | Add account security setup reminders to the TUI (#49715) | ⬜ 未落地 | ①`git log --grep="#49715"`=0 ②`rg '#49715' -g '*.go'`=0 ③最长标识符 `security_setup_fetch_with_default_features_uses_authenticated_codex_endpoint` 在 Go 0 文件 ④`git show --stat 60947e2341`=23 文件（codex-rs/tui/src/app/app_server_events.rs; codex-rs/tui/src/app/event_dispatch.rs）→ 落点候选 tui/（部分在 app/） |
| #49778 | `875bf9209b` | Define exec-server protocol types for streamed file writes (#497 | ⬜ 未落地 | ①`git log --grep="#49778"`=0 ②`rg '#49778' -g '*.go'`=0 ③最长标识符 `filesystem_open_accepts_legacy_request_without_mode` 在 Go 0 文件 ④`git show --stat 875bf9209b`=6 文件（codex-rs/exec-server-protocol/src/protocol.rs; codex-rs/exec-server/src/lib.rs）→ 落点候选 （exec-server-protocol：无直接 Go 包）；execserver/ |
| #49781 | `8953de1f1a` | Include the environment's MXC backend in MCP sandbox metadata (# | ⬜ 未落地 | ①`git log --grep="#49781"`=0 ②`rg '#49781' -g '*.go'`=0 ③最长标识符 `mcp_runtime_keeps_local_backend_without_a_selected_local_environment` 在 Go 0 文件 ④`git show --stat 8953de1f1a`=9 文件（codex-rs/codex-mcp/src/binding_tests.rs; codex-rs/codex-mcp/src/mcp/mod.rs）→ 落点候选 （codex-mcp：无直接 Go 包）；context//session//turn//rollout//state/ |
| #49782 | `1a913493dc` | Clean up process groups for failed shell snapshot captures (#497 | ⬜ 未落地 | ①`git log --grep="#49782"`=0 ②`rg '#49782' -g '*.go'`=0 ③最长标识符 `shell_snapshot_preserves_successful_startup_output_and_services` 在 Go 0 文件 ④`git show --stat 1a913493dc`=5 文件（codex-rs/exec-server/src/lib.rs; codex-rs/exec-server/src/shell_snapshot.rs）→ 落点候选 execserver/ |
| #49783 | `106772e3c6` | Preserve background thread requests when forking in the TUI (#49 | ⬜ 未落地 | ①`git log --grep="#49783"`=0 ②`rg '#49783' -g '*.go'`=0 ③最长标识符 `local_daemon_fork_dispatch_preserves_idle_source_and_children` 在 Go 0 文件 ④`git show --stat 106772e3c6`=8 文件（codex-rs/tui/src/app/app_server_events.rs; codex-rs/tui/src/app/event_dispatch.rs）→ 落点候选 tui/（部分在 app/） |
| #49785 | `1f77c0cfa6` | Persist empty paginated threads when naming them (#49785) | ⬜ 未落地 | ①`git log --grep="#49785"`=0 ②`rg '#49785' -g '*.go'`=0 ③最长标识符 `naming_empty_paginated_thread_propagates_rollout_persistence_failure` 在 Go 0 文件 ④`git show --stat 1f77c0cfa6`=4 文件（codex-rs/app-server/tests/suite/v2/mod.rs; codex-rs/app-server/tests/suite/v2/thread_name_persistence.rs）→ 落点候选 appserver/；session/ |
| #49786 | `f542ba68e5` | Clarify V2 spawn model override guidance for context catalogs (# | ⬜ 未落地 | ①`git log --grep="#49786"`=0 ②`rg '#49786' -g '*.go'`=0 ③最长标识符 `spawn_agent_tool_keeps_model_controls_when_spawn_metadata_is_hidden` 在 Go 0 文件 ④`git show --stat f542ba68e5`=3 文件（codex-rs/core/src/tools/handlers/multi_agents_spec.rs; codex-rs/core/src/tools/handlers/multi_agents_spec_tests.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49787 | `83cf88306e` | Remove `AGENTS.md` from Bazel core test data (#49787) | ⬜ 未落地 | ①`git log --grep="#49787"`=0 ②`rg '#49787' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 83cf88306e`=2 文件（BUILD.bazel; codex-rs/core/BUILD.bazel）→ 落点候选 （BUILD.bazel：无直接 Go 包）；context//session//turn//rollout//state/ |
| #49792 | `5f300d3f74` | Add retained conversation support to Guardian async sampling (#4 | ⬜ 未落地 | ①`git log --grep="#49792"`=0 ②`rg '#49792' -g '*.go'`=0 ③最长标识符 `preparation_preserves_committed_prefix_and_selects_only_new_entries_and_images` 在 Go 0 文件 ④`git show --stat 5f300d3f74`=11 文件（codex-rs/ext/guardian-reviewer/src/conversation.rs; codex-rs/ext/guardian-reviewer/src/conversation_tests.rs）→ 落点候选 （Go 无 ext/ 目录）；appserver/（guardian 面） |
| #49793 | `726f1492db` | Add conversation mode to Guardian v2 async classification (#4979 | ⬜ 未落地 | ①`git log --grep="#49793"`=0 ②`rg '#49793' -g '*.go'`=0 ③最长标识符 `configured_reset_limit_rebuilds_history_without_rejecting_fresh_evidence` 在 Go 0 文件 ④`git show --stat 726f1492db`=25 文件（codex-rs/app-server/tests/suite/v2/guardian_stateful_async_tests.rs; codex-rs/app-server/tests/suite/v2/guardian_v2.rs）→ 落点候选 appserver/；context//session//turn//rollout//state/ |
| #49795 | `47a8bd7321` | Avoid duplicate sync reviews in Guardian classifier continuation | ⬜ 未落地 | ①`git log --grep="#49795"`=0 ②`rg '#49795' -g '*.go'`=0 ③最长标识符 `review_delivery_requires_complete_host_metadata_and_preserves_distinct_completions` 在 Go 0 文件 ④`git show --stat 47a8bd7321`=21 文件（codex-rs/app-server-protocol/schema/precomputed/app-server-exports-stable.json.zst; codex-rs/app-server/tests/suite/v2/guardian_stateful_async_tests.rs）→ 落点候选 （app-server-protocol：无直接 Go 包）；appserver/ |
| #49796 | `5ca55db09c` | Deduplicate Guardian retained-context omission notices (#49796) | ⬜ 未落地 | ①`git log --grep="#49796"`=0 ②`rg '#49796' -g '*.go'`=0 ③最长标识符 `coalesced_repl_text_cannot_attest_to_host_omission_delivery` 在 Go 0 文件 ④`git show --stat 5ca55db09c`=7 文件（codex-rs/app-server-protocol/schema/precomputed/app-server-exports-stable.json.zst; codex-rs/app-server/tests/suite/v2/guardian_v2.rs）→ 落点候选 （app-server-protocol：无直接 Go 包）；appserver/ |
| #49799 | `606b139565` | Preserve server web-search settings in the TUI (#49799) | ⬜ 未落地 | ①`git log --grep="#49799"`=0 ②`rg '#49799' -g '*.go'`=0 ③最长标识符 `config_overrides_forward_explicit_summary_verbosity_and_web_search` 在 Go 0 文件 ④`git show --stat 606b139565`=5 文件（codex-rs/tui/src/app/tests/session_lifecycle_requests.rs; codex-rs/tui/src/app_server_session.rs）→ 落点候选 tui/（部分在 app/） |
| #49800 | `ec0cfa5da8` | Allow cleanup of replay-only side conversations with missing thr | ⬜ 未落地 | ①`git log --grep="#49800"`=0 ②`rg '#49800' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat ec0cfa5da8`=2 文件（codex-rs/tui/src/app/side.rs; codex-rs/tui/src/app/tests/session_lifecycle_requests.rs）→ 落点候选 tui/（部分在 app/） |
| #49805 | `e7798c9944` | Add capability-gated writable file streams to the exec-server cl | ✅ 已落地（队长核） — 97157c51 | ①`git log --grep="#49805"`=0 ②`rg '#49805' -g '*.go'`=0 ③最长标识符 `writable_file_streams_require_executor_capability` 在 Go 0 文件 ④`git show --stat e7798c9944`=1 文件（codex-rs/exec-server/src/client.rs）→ 落点候选 execserver/  **[队长核 · 第 84–85 轮]** |
| #49806 | `a5d56d8120` | Accept unknown Codex error variants in the app-server protocol ( | ⬜ 未落地 | ①`git log --grep="#49806"`=0 ②`rg '#49806' -g '*.go'`=0 ③最长标识符 `codex_error_info_deserializes_known_object_without_falling_back` 在 Go 0 文件 ④`git show --stat a5d56d8120`=26 文件（codex-rs/app-server-protocol/schema/json/ServerNotification.json; codex-rs/app-server-protocol/schema/json/codex_app_server_protocol.schemas.json）→ 落点候选 （app-server-protocol：无直接 Go 包）；（sdk：无直接 Go 包） |
| #49809 | `2fde968de8` | Preserve local launch permissions across TUI sessions and reconn | ⬜ 未落地 | ①`git log --grep="#49809"`=0 ②`rg '#49809' -g '*.go'`=0 ③最长标识符 `new_sessions_preserve_yolo_launch_and_later_permission_choices` 在 Go 0 文件 ④`git show --stat 2fde968de8`=8 文件（codex-rs/tui/src/app/agents_overview.rs; codex-rs/tui/src/app/reconnect.rs）→ 落点候选 tui/（部分在 app/） |
| #49810 | `cca440697f` | Flush expired paste bursts before handling Enter (#49810) | ⬜ 未落地 | ①`git log --grep="#49810"`=0 ②`rg '#49810' -g '*.go'`=0 ③最长标识符 `plain_enter_submits_expired_vim_insert_input` 在 Go 0 文件 ④`git show --stat cca440697f`=4 文件（codex-rs/tui/src/bottom_pane/chat_composer.rs; codex-rs/tui/src/bottom_pane/chat_composer/paste_input.rs）→ 落点候选 tui/（部分在 app/） |
| #49811 | `c51f5bfb82` | Handle unsupported `fs/writeBlock` requests in exec-server (#498 | ⬜ 未落地 | ①`git log --grep="#49811"`=0 ②`rg '#49811' -g '*.go'`=0 ③最长标识符 `fs_write_block` 在 Go 0 文件 ④`git show --stat c51f5bfb82`=3 文件（codex-rs/exec-server/src/server/file_system_handler.rs; codex-rs/exec-server/src/server/handler.rs）→ 落点候选 execserver/ |
| #49812 | `0f8df3d214` | Move shadow skill ranking off the turn preparation path (#49812) | ⬜ 未落地 | ①`git log --grep="#49812"`=0 ②`rg '#49812' -g '*.go'`=0 ③最长标识符 `ranking_budget_is_shared_and_held_until_blocking_work_finishes` 在 Go 0 文件 ④`git show --stat 0f8df3d214`=8 文件（codex-rs/ext/skills/Cargo.toml; codex-rs/ext/skills/src/extension.rs）→ 落点候选 （Go 无 ext/ 目录） |
| #49814 | `8e44ad94b0` | Add coordinated shutdown for local agent trees (#49814) | ⬜ 未落地 | ①`git log --grep="#49814"`=0 ②`rg '#49814' -g '*.go'`=0 ③最长标识符 `tree_shutdown_waits_for_children_without_affecting_other_roots` 在 Go 0 文件 ④`git show --stat 8e44ad94b0`=22 文件（codex-rs/core-api/src/lib.rs; codex-rs/core/src/agent/control.rs）→ 落点候选 （core-api：无直接 Go 包）；context//session//turn//rollout//state/ |
| #49816 | `1182834d13` | Remove browser-open success messages from the TUI (#49816) | ⬜ 未落地 | ①`git log --grep="#49816"`=0 ②`rg '#49816' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 1182834d13`=1 文件（codex-rs/tui/src/app/history_ui.rs）→ 落点候选 tui/（部分在 app/） |
| #49818 | `a73898c249` | Use dedicated parameters for sandboxed file opens (#49818) | ⬜ 未落地 | ①`git log --grep="#49818"`=0 ②`rg '#49818' -g '*.go'`=0 ③最长标识符 `fs_helper_open_params` 在 Go 0 文件 ④`git show --stat a73898c249`=2 文件（codex-rs/exec-server/src/fs_helper.rs; codex-rs/exec-server/src/sandboxed_file_open.rs）→ 落点候选 execserver/ |
| #49846 | `408f48ce0c` | Capture host-supplied extension data for each turn (#49846) | ⬜ 未落地 | ①`git log --grep="#49846"`=0 ②`rg '#49846' -g '*.go'`=0 ③最长标识符 `turn_extension_data_is_captured_for_automatic_turns` 在 Go 0 文件 ④`git show --stat 408f48ce0c`=20 文件（codex-rs/app-server/src/request_processors/thread_processor_tests.rs; codex-rs/app-server/src/request_processors/turn_processor.rs）→ 落点候选 appserver/；（core-api：无直接 Go 包） |
| #49847 | `b44ca872b1` | Persist world-state snapshots alongside rendered context (#49847 | ⬜ 未落地 | ①`git log --grep="#49847"`=0 ②`rg '#49847' -g '*.go'`=0 ③最长标识符 `world_state_transitions_persist_changed_state_and_skip_unchanged_state` 在 Go 0 文件 ④`git show --stat b44ca872b1`=29 文件（codex-rs/core/src/compact.rs; codex-rs/core/src/context/world_state/apps_instructions_tests.rs）→ 落点候选 context//session//turn//rollout//state/；（Go 无 ext/ 目录） |
| #49852 | `236be1ad9f` | Improve diagnostics for report attachment failures (#49852) | ⬜ 未落地 | ①`git log --grep="#49852"`=0 ②`rg '#49852' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 236be1ad9f`=1 文件（codex-rs/feedback/src/lib.rs）→ 落点候选 （feedback：无直接 Go 包） |
| #49855 | `bc197b77bc` | Use embedded mode for elevated Windows TUI sessions (#49855) | ⬜ 未落地 | ①`git log --grep="#49855"`=0 ②`rg '#49855' -g '*.go'`=0 ③最长标识符 `elevated_local_tui_uses_embedded_without_starting_daemon` 在 Go 0 文件 ④`git show --stat bc197b77bc`=8 文件（codex-rs/app-server-daemon/src/backend/windows.rs; codex-rs/app-server-daemon/src/lib.rs）→ 落点候选 appserverdaemon/；cli/ |
| #49857 | `f58ed54a9d` | Use the model catalog to select TUI cyber refusal guidance (#498 | ⬜ 未落地 | ①`git log --grep="#49857"`=0 ②`rg '#49857' -g '*.go'`=0 ③最长标识符 `refusal_guidance_uses_the_model_catalog` 在 Go 0 文件 ④`git show --stat f58ed54a9d`=12 文件（codex-rs/tui/src/app/app_server_events.rs; codex-rs/tui/src/app/reconnect.rs）→ 落点候选 tui/（部分在 app/） |
| #49858 | `e1522188e9` | Add a persistent `/daybreak` toggle to the TUI (#49858) | ⬜ 未落地 | ①`git log --grep="#49858"`=0 ②`rg '#49858' -g '*.go'`=0 ③最长标识符 `turn_program_uses_catalog_and_preserves_legacy_models` 在 Go 0 文件 ④`git show --stat e1522188e9`=51 文件（codex-rs/tui/src/app.rs; codex-rs/tui/src/app/config_persistence.rs）→ 落点候选 tui/（部分在 app/） |
| #49859 | `cd4a9cbd25` | Honor Daybreak settings in TUI continuations and background task | ⬜ 未落地 | ①`git log --grep="#49859"`=0 ②`rg '#49859' -g '*.go'`=0 ③最长标识符 `daybreak_refusal_offers_enable_for_the_next_turn` 在 Go 0 文件 ④`git show --stat cd4a9cbd25`=15 文件（codex-rs/tui/src/app/misalignment_policy.rs; codex-rs/tui/src/app/tests/misalignment_policy_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #49861 | `8ea2428c38` | Add Daybreak state to the status line and terminal title (#49861 | ⬜ 未落地 | ①`git log --grep="#49861"`=0 ②`rg '#49861' -g '*.go'`=0 ③最长标识符 `daybreak_status_surfaces_follow_the_thread_preference` 在 Go 0 文件 ④`git show --stat 8ea2428c38`=10 文件（codex-rs/cli/src/doctor/title.rs; codex-rs/tui/src/bottom_pane/status_line_setup.rs）→ 落点候选 cli/；tui/（部分在 app/） |
| #49874 | `bdfbab9f4f` | Point usage and credit links to ChatGPT settings (#49874) | ⬜ 未落地 | ①`git log --grep="#49874"`=0 ②`rg '#49874' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat bdfbab9f4f`=33 文件（codex-rs/protocol/src/error.rs; codex-rs/protocol/src/error_tests.rs）→ 落点候选 （protocol：无直接 Go 包）；tui/（部分在 app/） |
| #49875 | `7b88d09d61` | Decouple TUI startup presentation from execution configuration ( | ⬜ 未落地 | ①`git log --grep="#49875"`=0 ②`rg '#49875' -g '*.go'`=0 ③最长标识符 `startup_session_header` 在 Go 0 文件 ④`git show --stat 7b88d09d61`=14 文件（codex-rs/tui/src/app/agents_overview.rs; codex-rs/tui/src/app/agents_overview_new.rs）→ 落点候选 tui/（部分在 app/） |
| #49876 | `d2b254fd17` | Remove personality plumbing from the TUI (#49876) | ⬜ 未落地 | ①`git log --grep="#49876"`=0 ②`rg '#49876' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat d2b254fd17`=29 文件（codex-rs/tui/src/app/config_persistence.rs; codex-rs/tui/src/app/event_dispatch.rs）→ 落点候选 tui/（部分在 app/） |
| #49880 | `1f52d40704` | Bind permission grants to the originating turn (#49880) | ⬜ 未落地 | ①`git log --grep="#49880"`=0 ②`rg '#49880' -g '*.go'`=0 ③最长标识符 `background_cell_uses_permissions_requested_after_next_turn_starts` 在 Go 0 文件 ④`git show --stat 1f52d40704`=22 文件（codex-rs/app-server/tests/suite/v2/request_permissions.rs; codex-rs/core/src/mcp_openai_file.rs）→ 落点候选 appserver/；context//session//turn//rollout//state/ |
| #49894 | `08a7031b6a` | Return world-state snapshots and context updates together (#4989 | ⬜ 未落地 | ①`git log --grep="#49894"`=0 ②`rg '#49894' -g '*.go'`=0 ③最长标识符 `RenderDiff` 在 Go 0 文件 ④`git show --stat 08a7031b6a`=30 文件（codex-rs/core/src/context/token_budget_context.rs; codex-rs/core/src/context/world_state/agents_md.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49898 | `da2e174a66` | Scope extension filesystem access to callback permissions (#4989 | ⬜ 未落地 | ①`git log --grep="#49898"`=0 ②`rg '#49898' -g '*.go'`=0 ③最长标识符 `windows_executor_skill_read_requires_a_requested_sandbox` 在 Go 0 文件 ④`git show --stat da2e174a66`=22 文件（codex-rs/core/src/session/mod.rs; codex-rs/core/src/session/step_context.rs）→ 落点候选 context//session//turn//rollout//state/；（Go 无 ext/ 目录） |
| #49912 | `444da310e1` | Respect approval policies in temporary structured threads (#4991 | ⬜ 未落地 | ①`git log --grep="#49912"`=0 ②`rg '#49912' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 444da310e1`=5 文件（codex-rs/tui/src/app/app_server_events.rs; codex-rs/tui/src/app/tests/recap_generation_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #49939 | `6b4daafdb4` | Add per-turn Cyber access program selection to exec and the SDK  | ⬜ 未落地 | ①`git log --grep="#49939"`=0 ②`rg '#49939' -g '*.go'`=0 ③最长标识符 `pre_sampling_compact_falls_back_after_previous_model_invalid_request_on_downshift` 在 Go 0 文件 ④`git show --stat 6b4daafdb4`=19 文件（codex-rs/cli/src/exec_args_tests.rs; codex-rs/cli/src/main.rs）→ 落点候选 cli/；context//session//turn//rollout//state/ |
| #49946 | `dd90f160ed` | Prevent stale file search results from being labeled with a new  | ⬜ 未落地 | ①`git log --grep="#49946"`=0 ②`rg '#49946' -g '*.go'`=0 ③最长标识符 `query_update_does_not_relabel_pending_matches` 在 Go 0 文件 ④`git show --stat dd90f160ed`=3 文件（codex-rs/file-search/src/lib.rs; codex-rs/file-search/src/matcher_tests.rs）→ 落点候选 （file-search：无直接 Go 包） |
| #49951 | `f70810bcd2` | Include preceding assistant context in Guardian sender reviews ( | ⬜ 未落地 | ①`git log --grep="#49951"`=0 ②`rg '#49951' -g '*.go'`=0 ③最长标识符 `guardian_sender_exchange` 在 Go 0 文件 ④`git show --stat f70810bcd2`=4 文件（codex-rs/core/src/agent/control/sender_context.rs; codex-rs/core/src/context/guardian_sender_messages.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #49972 | `a933dd77db` | Share byte buffers across exec-server output chunks (#49972) | ⬜ 未落地 | ①`git log --grep="#49972"`=0 ②`rg '#49972' -g '*.go'`=0 ③最长标识符 `shared_byte_chunks_preserve_base64_and_owned_bytes` 在 Go 0 文件 ④`git show --stat a933dd77db`=7 文件（codex-rs/codex-mcp/src/trusted_access_tests.rs; codex-rs/exec-server-protocol/src/protocol.rs）→ 落点候选 （codex-mcp：无直接 Go 包）；（exec-server-protocol：无直接 Go 包） |
| #49987 | `ecc78e4cf5` | Add renewable EMA HTTP authentication and credential versioning  | ⬜ 未落地 | ①`git log --grep="#49987"`=0 ②`rg '#49987' -g '*.go'`=0 ③最长标识符 `cached_credentials_require_the_current_version_after_waiting_for_a_writer` 在 Go 0 文件 ④`git show --stat ecc78e4cf5`=23 文件（codex-rs/exec-server/src/client.rs; codex-rs/exec-server/src/client/connection_failure.rs）→ 落点候选 execserver/；mcp/ |
| #49993 | `57ac6f5163` | Preserve the async Guardian history prefix as retained context c | ⬜ 未落地 | ①`git log --grep="#49993"`=0 ②`rg '#49993' -g '*.go'`=0 ③最长标识符 `changing_retained_context_and_attestations_preserves_history_before_the_current_action` 在 Go 0 文件 ④`git show --stat 57ac6f5163`=4 文件（codex-rs/app-server/tests/suite/v2/guardian_v2.rs; codex-rs/ext/guardian-v2/src/async_scorer/extension_tests.rs）→ 落点候选 appserver/；（Go 无 ext/ 目录） |
| #50013 | `b527ce4734` | Honor server model defaults when starting fresh TUI threads (#50 | ⬜ 未落地 | ①`git log --grep="#50013"`=0 ②`rg '#50013' -g '*.go'`=0 ③最长标识符 `startup_launch_choices` 在 Go 0 文件 ④`git show --stat b527ce4734`=9 文件（codex-rs/tui/src/app/startup.rs; codex-rs/tui/src/app/startup_prompts.rs）→ 落点候选 tui/（部分在 app/） |
| #50018 | `595534314f` | Use descriptor-safe helpers for executable test fixtures (#50018 | ⬜ 未落地 | ①`git log --grep="#50018"`=0 ②`rg '#50018' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 595534314f`=76 文件（codex-rs/Cargo.lock; codex-rs/app-server-daemon/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；appserverdaemon/ |
| #50019 | `9552906b2b` | Protect the guardian decisions API key from environment forwardi | ✅ 已落地（队长核） — 6de1fa45 | ①`git log --grep="#50019"`=0 ②`rg '#50019' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 9552906b2b`=4 文件（codex-rs/exec-server/src/client/route_aware_http_client.rs; codex-rs/exec-server/tests/http_request.rs）→ 落点候选 execserver/；（protocol：无直接 Go 包）  **[队长核 · 第 84–85 轮]** |
| #50026 | `2685e3a4ce` | Preserve user restrictions in Guardian handoff context (#50026) | ⬜ 未落地 | ①`git log --grep="#50026"`=0 ②`rg '#50026' -g '*.go'`=0 ③最长标识符 `heartbeat_keeps_user_restriction_after_status_questions` 在 Go 0 文件 ④`git show --stat 2685e3a4ce`=6 文件（codex-rs/core/src/agent/control/root_handoff.rs; codex-rs/core/src/agent/control/user_authorization.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #50039 | `a97fb78085` | Keep transcript Find results readable after closing the query (# | ⬜ 未落地 | ①`git log --grep="#50039"`=0 ②`rg '#50039' -g '*.go'`=0 ③最长标识符 `reading_without_a_match_searches_from_the_visible_entry_in_each_direction` 在 Go 0 文件 ④`git show --stat a97fb78085`=27 文件（codex-rs/tui/src/app/empty_state_policy.rs; codex-rs/tui/src/app/input.rs）→ 落点候选 tui/（部分在 app/） |
| #50045 | `1cc9917508` | Keep transcript Find expansion scoped to the current match (#500 | ⬜ 未落地 | ①`git log --grep="#50045"`=0 ②`rg '#50045' -g '*.go'`=0 ③最长标识符 `selected_hidden_live_match_keeps_its_revision_without_persisting_expansion` 在 Go 0 文件 ④`git show --stat 1cc9917508`=22 文件（codex-rs/tui/src/app/owned_transcript.rs; codex-rs/tui/src/app/owned_transcript_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #50050 | `9561a34531` | Keep plugin and skill snapshots scoped to each step (#50050) | ⬜ 未落地 | ①`git log --grep="#50050"`=0 ②`rg '#50050' -g '*.go'`=0 ③最长标识符 `astra_omits_disabled_executor_and_plugin_skills_from_model_context` 在 Go 0 文件 ④`git show --stat 9561a34531`=20 文件（codex-rs/Cargo.lock; codex-rs/core/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；context//session//turn//rollout//state/ |
| #50052 | `57a38c1beb` | Preserve question context in recovered TUI answer drafts (#50052 | ⬜ 未落地 | ①`git log --grep="#50052"`=0 ②`rg '#50052' -g '*.go'`=0 ③最长标识符 `taking_pending_drafts_bounds_question_titles_without_truncating_answers` 在 Go 0 文件 ④`git show --stat 57a38c1beb`=10 文件（codex-rs/tui/src/bottom_pane/async_questions/state.rs; codex-rs/tui/src/bottom_pane/async_questions/state_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #50058 | `b06b7d2f77` | Upgrade Windows bindings to `windows-sys` 0.61.2 (#50058) | ⬜ 未落地 | ①`git log --grep="#50058"`=0 ②`rg '#50058' -g '*.go'`=0 ③最长标识符 `non_owning_impersonation_token` 在 Go 0 文件 ④`git show --stat b06b7d2f77`=99 文件（codex-rs/Cargo.lock; codex-rs/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；appserverdaemon/ |
| #50066 | `d91294c39e` | Add a bounded Decisions transport for Guardian comparison classi | ⬜ 未落地 | ①`git log --grep="#50066"`=0 ②`rg '#50066' -g '*.go'`=0 ③最长标识符 `cancelling_a_decisions_request_releases_thread_capacity` 在 Go 0 文件 ④`git show --stat d91294c39e`=3 文件（codex-rs/ext/guardian-v2/src/async_scorer/decisions.rs; codex-rs/ext/guardian-v2/src/async_scorer/decisions_tests.rs）→ 落点候选 （Go 无 ext/ 目录） |
| #50105 | `e58b493329` | Consolidate chat composer footer logic in `footer_state` (#50105 | ⬜ 未落地 | ①`git log --grep="#50105"`=0 ②`rg '#50105' -g '*.go'`=0 ③最长标识符 `quit_shortcut_hint_visible` 在 Go 0 文件 ④`git show --stat e58b493329`=2 文件（codex-rs/tui/src/bottom_pane/chat_composer.rs; codex-rs/tui/src/bottom_pane/chat_composer/footer_state.rs）→ 落点候选 tui/（部分在 app/） |
| #50109 | `6ece7bfc21` | Keep fullscreen prompts bounded and scrollable (#50109) | ⬜ 未落地 | ①`git log --grep="#50109"`=0 ②`rg '#50109' -g '*.go'`=0 ③最长标识符 `wheel_at_edges_preserves_caret_following_or_browsing_after_resize` 在 Go 0 文件 ④`git show --stat 6ece7bfc21`=19 文件（codex-rs/tui/src/app/owned_transcript.rs; codex-rs/tui/src/app/owned_transcript_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #50112 | `a0a7a63002` | Centralize TUI loading glyphs and frame scheduling (#50112) | ✅ 已落地（队长核） — ba951807 | ①`git log --grep="#50112"`=0 ②`rg '#50112' -g '*.go'`=0 ③最长标识符 `loading_glyph_with_delay` 在 Go 0 文件 ④`git show --stat a0a7a63002`=2 文件（codex-rs/tui/src/bottom_pane/voice_strip.rs; codex-rs/tui/src/motion.rs）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #50113 | `b707714ae4` | Add a native gRPC client for cloud thread resume and attach (#50 | ⬜ 未落地 | ①`git log --grep="#50113"`=0 ②`rg '#50113' -g '*.go'`=0 ③最长标识符 `https_enforces_unmanaged_endpoint_restrictions_before_connecting` 在 Go 0 文件 ④`git show --stat b707714ae4`=11 文件（codex-rs/Cargo.lock; codex-rs/Cargo.toml）→ 落点候选 （codex-rs：无直接 Go 包）；（cloud-client：无直接 Go 包） |
| #50140 | `cb6da58876` | Use the server permission catalog for TUI permission shortcuts ( | ⬜ 未落地 | ①`git log --grep="#50140"`=0 ②`rg '#50140' -g '*.go'`=0 ③最长标识符 `permission_shortcuts_load_once_and_reuse_the_picker_catalog` 在 Go 0 文件 ④`git show --stat cb6da58876`=13 文件（codex-rs/tui/src/chatwidget.rs; codex-rs/tui/src/chatwidget/constructor.rs）→ 落点候选 tui/（部分在 app/） |
| #50148 | `e7ea5f4a86` | Add managed worktree tools to the TUI (#50148) | ⬜ 未落地 | ①`git log --grep="#50148"`=0 ②`rg '#50148' -g '*.go'`=0 ③最长标识符 `creation_attaches_to_original_task_and_survives_service_restart` 在 Go 0 文件 ④`git show --stat e7ea5f4a86`=14 文件（codex-rs/tui/src/app/reconnect.rs; codex-rs/tui/src/app/side.rs）→ 落点候选 tui/（部分在 app/） |
| #50166 | `7135b303d9` | Upgrade `age` to 0.12.1 and remove the obsolete advisory excepti | ⬜ 未落地 | ①`git log --grep="#50166"`=0 ②`rg '#50166' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 7135b303d9`=5 文件（MODULE.bazel.lock; codex-rs/.cargo/audit.toml）→ 落点候选 （MODULE.bazel.lock：无直接 Go 包）；（.cargo：无直接 Go 包） |
| #50189 | `a20fe6335f` | Replace the Figma OAuth exception with Mercado Pago (#50189) | ⬜ 未落地 | ①`git log --grep="#50189"`=0 ②`rg '#50189' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat a20fe6335f`=2 文件（codex-rs/rmcp-client/src/oauth/issuer_binding.rs; codex-rs/rmcp-client/src/oauth_client_registration_tests.rs）→ 落点候选 mcp/ |
| #50199 | `3f97f2b3bd` | Restore the account email in `/status` after account updates (#5 | ⬜ 未落地 | ①`git log --grep="#50199"`=0 ②`rg '#50199' -g '*.go'`=0 ③最长标识符 `on_account_email_loaded` 在 Go 0 文件 ④`git show --stat 3f97f2b3bd`=13 文件（codex-rs/tui/src/app.rs; codex-rs/tui/src/app/account_status.rs）→ 落点候选 tui/（部分在 app/） |
| #50200 | `a75987455a` | Report the configured TUI mode in `codex doctor` (#50200) | ⬜ 未落地 | ①`git log --grep="#50200"`=0 ②`rg '#50200' -g '*.go'`=0 ③最长标识符 `doctor_reports_configured_tui_mode` 在 Go 0 文件 ④`git show --stat a75987455a`=4 文件（codex-rs/cli/src/doctor.rs; codex-rs/cli/src/doctor/output.rs）→ 落点候选 cli/ |
| #50207 | `e3c2a83937` | Release stable Markdown tables into scrollback during streaming  | ⬜ 未落地 | ①`git log --grep="#50207"`=0 ②`rg '#50207' -g '*.go'`=0 ③最长标识符 `flush_answer_stream_requests_scrollback_reflow_for_tables` 在 Go 0 文件 ④`git show --stat e3c2a83937`=7 文件（codex-rs/tui/src/chatwidget/streaming.rs; codex-rs/tui/src/chatwidget/tests/status_and_layout.rs）→ 落点候选 tui/（部分在 app/） |
| #50209 | `4dd51f4a5f` | Make transcript mouse scroll speed configurable (#50209) | ✅ 已落地（队长核） — 52d5f5f1 | ①`git log --grep="#50209"`=0 ②`rg '#50209' -g '*.go'`=0 ③最长标识符 `mouse_scroll_speed_scales_rows_and_accumulates_fractional_movement` 在 Go 0 文件 ④`git show --stat 4dd51f4a5f`=22 文件（codex-rs/config/src/lib.rs; codex-rs/config/src/tui_mouse_scroll.rs）→ 落点候选 config/；context//session//turn//rollout//state/  **[队长核 · 第 84–85 轮]** |
| #50216 | `37f53199a6` | Use the shared text editor for command-center task renaming (#50 | ⬜ 未落地 | ①`git log --grep="#50216"`=0 ②`rg '#50216' -g '*.go'`=0 ③最长标识符 `single_line_vim_newline_commands_are_noops_and_yanks_stay_inline` 在 Go 0 文件 ④`git show --stat 37f53199a6`=15 文件（codex-rs/tui/src/app/agent_center/input.rs; codex-rs/tui/src/app/agent_center/render.rs）→ 落点候选 tui/（部分在 app/） |
| #50219 | `14a477ea89` | Bound tmux option probes to one second (#50219) | ⬜ 未落地 | ①`git log --grep="#50219"`=0 ②`rg '#50219' -g '*.go'`=0 ③最长标识符 `completed_probe_does_not_wait_for_descendant_holding_stdout` 在 Go 0 文件 ④`git show --stat 14a477ea89`=2 文件（codex-rs/tui/src/tui/tmux.rs; codex-rs/tui/src/tui/tmux_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #50273 | `ca466061d6` | Record Guardian V2 Decisions agreement and latency metrics (#502 | ⬜ 未落地 | ①`git log --grep="#50273"`=0 ②`rg '#50273' -g '*.go'`=0 ③最长标识符 `record_decisions_comparison` 在 Go 0 文件 ④`git show --stat ca466061d6`=5 文件（codex-rs/ext/guardian-v2/src/async_scorer/classification.rs; codex-rs/ext/guardian-v2/src/async_scorer/decisions.rs）→ 落点候选 （Go 无 ext/ 目录） |
| #50345 | `84d5437b6e` | Keep the subagent picker consistent with thread archive state (# | ⬜ 未落地 | ①`git log --grep="#50345"`=0 ②`rg '#50345' -g '*.go'`=0 ③最长标识符 `handle_agent_picker_visibility_notification` 在 Go 0 文件 ④`git show --stat 84d5437b6e`=10 文件（codex-rs/tui/src/app/agent_navigation.rs; codex-rs/tui/src/app/agent_picker.rs）→ 落点候选 tui/（部分在 app/） |
| #50348 | `9d2b60303e` | Back off automatic remote control reconnects with jitter (#50348 | ✅ 已落地（队长核） — bbf423f3 | ①`git log --grep="#50348"`=0 ②`rg '#50348' -g '*.go'`=0 ③最长标识符 `reconnect_backoff_covers_refresh_and_resets_after_healthy_connection` 在 Go 0 文件 ④`git show --stat 9d2b60303e`=4 文件（codex-rs/app-server-transport/src/transport/remote_control/tests.rs; codex-rs/app-server-transport/src/transport/remote_control/tests/retry_tests.rs）→ 落点候选 appserver/（transport 面）  **[队长核 · 第 84–85 轮]** |
| #50359 | `91168365a5` | Render ANSI styles in TUI hook system messages (#50359) | ⬜ 未落地 | ①`git log --grep="#50359"`=0 ②`rg '#50359' -g '*.go'`=0 ③最长标识符 `completed_hook_system_message_renders_ansi_styles` 在 Go 0 文件 ④`git show --stat 91168365a5`=2 文件（codex-rs/tui/src/history_cell/hook_cell.rs; codex-rs/tui/src/history_cell/snapshots/codex_tui__history_cell__hook_cell__tests__completed_hook_system_message_ansi_styles.snap）→ 落点候选 tui/（部分在 app/） |
| #50389 | `f88a6efe43` | Honor configured keybindings before transcript navigation (#5038 | ⬜ 未落地 | ①`git log --grep="#50389"`=0 ②`rg '#50389' -g '*.go'`=0 ③最长标识符 `configured_ctrl_space_submit_wins_over_transcript_selection` 在 Go 0 文件 ④`git show --stat f88a6efe43`=4 文件（codex-rs/tui/src/app/owned_transcript.rs; codex-rs/tui/src/app/owned_transcript_input_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #50396 | `d61c7a824f` | Honor pager bindings for transcript page keys (#50396) | ⬜ 未落地 | ①`git log --grep="#50396"`=0 ②`rg '#50396' -g '*.go'`=0 ③最长标识符 `page_keys_follow_pager_scroll_bindings` 在 Go 0 文件 ④`git show --stat d61c7a824f`=3 文件（codex-rs/tui/src/snapshots/codex_tui__transcript_view__tests__page_keys_follow_pager_scroll_bindings.snap; codex-rs/tui/src/transcript_view/input.rs）→ 落点候选 tui/（部分在 app/） |
| #50416 | `dff5270b29` | Clarify Git worktree choices for new and forked conversations (# | ⬜ 未落地 | ①`git log --grep="#50416"`=0 ②`rg '#50416' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat dff5270b29`=4 文件（codex-rs/tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__worktrees_fork_choices.snap; codex-rs/tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__worktrees_new_choices.snap）→ 落点候选 tui/（部分在 app/） |
| #50431 | `b6903c0669` | Preserve terminal hyperlinks in agents overview previews (#50431 | ⬜ 未落地 | ①`git log --grep="#50431"`=0 ②`rg '#50431' -g '*.go'`=0 ③最长标识符 `agents_overview_preview_links_survive_wrapping_and_clipping` 在 Go 0 文件 ④`git show --stat b6903c0669`=3 文件（codex-rs/tui/src/app/agents_overview_tests.rs; codex-rs/tui/src/app/agents_overview_view.rs）→ 落点候选 tui/（部分在 app/） |
| #50433 | `66561d0301` | Allow API-key accounts to use Daybreak in the TUI (#50433) | ⬜ 未落地 | ①`git log --grep="#50433"`=0 ②`rg '#50433' -g '*.go'`=0 ③最长标识符 `daybreak_account_eligible` 在 Go 0 文件 ④`git show --stat 66561d0301`=12 文件（codex-rs/tui/src/app/daybreak.rs; codex-rs/tui/src/app/misalignment_policy.rs）→ 落点候选 tui/（部分在 app/） |
| #50434 | `c536ffcb18` | Add keyboard copy selection to the owned transcript (#50434) | ⬜ 未落地 | ①`git log --grep="#50434"`=0 ②`rg '#50434' -g '*.go'`=0 ③最长标识符 `copy_source_survives_raw_rich_rendering_and_whitespace_normalization` 在 Go 0 文件 ④`git show --stat c536ffcb18`=38 文件（codex-rs/tui/src/app/agent_message_consolidation.rs; codex-rs/tui/src/app/event_dispatch.rs）→ 落点候选 tui/（部分在 app/） |
| #50437 | `12a30d4e6d` | Add a CLI command to uninstall the legacy Windows sandbox (#5043 | ⬜ 未落地 | ①`git log --grep="#50437"`=0 ②`rg '#50437' -g '*.go'`=0 ③最长标识符 `uninstall_help_and_invalid_arguments_preserve_user_data` 在 Go 0 文件 ④`git show --stat 12a30d4e6d`=6 文件（codex-rs/cli/src/main.rs; codex-rs/cli/src/sandbox_uninstall.rs）→ 落点候选 cli/；sandbox/（Windows 面） |
| #50445 | `d4eed6dca5` | Assert that only direct tool calls emit timing events (#50445) | ⬜ 未落地 | ①`git log --grep="#50445"`=0 ②`rg '#50445' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat d4eed6dca5`=1 文件（codex-rs/core/src/tools/parallel.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #50454 | `3c3a990da0` | Measure rollout persistence size reductions (#50454) | ⬜ 未落地 | ①`git log --grep="#50454"`=0 ②`rg '#50454' -g '*.go'`=0 ③最长标识符 `record_item_bytes` 在 Go 0 文件 ④`git show --stat 3c3a990da0`=1 文件（codex-rs/rollout/src/persistence_metrics.rs）→ 落点候选 rollout/ |
| #50465 | `ef8cfe5e96` | Retry registry authentication outages and jitter executor reconn | ✅ 已落地（队长核） — 9b6ac1ab | ①`git log --grep="#50465"`=0 ②`rg '#50465' -g '*.go'`=0 ③最长标识符 `registration_retries_preserve_noise_identity_and_initialized_session` 在 Go 0 文件 ④`git show --stat ef8cfe5e96`=7 文件（codex-rs/exec-server/src/remote.rs; codex-rs/exec-server/src/remote/reconnect_backoff.rs）→ 落点候选 execserver/；mcp/  **[队长核 · 第 84–85 轮]** |
| #50467 | `f5a2272c6c` | Copy transcript selections as literal text while preserving rich | ⬜ 未落地 | ①`git log --grep="#50467"`=0 ②`rg '#50467' -g '*.go'`=0 ③最长标识符 `LiteralSelection` 在 Go 0 文件 ④`git show --stat f5a2272c6c`=10 文件（codex-rs/tui/src/chatwidget/copy_picker.rs; codex-rs/tui/src/clipboard_copy.rs）→ 落点候选 tui/（部分在 app/） |
| #50472 | `604061ce51` | Enable Ultrafast service tiers for Amazon Bedrock Astra models ( | ✅ 已落地（Mantle 半 sync542 `d4922ab5`；Runtime 变体 + 自定义目录归一 = N/A，Go 无对应入口） | ①`git log --grep="#50472"`=0 ②`rg '#50472' -g '*.go'`=0 ③最长标识符 `bedrock_model_list_advertises_ultrafast_without_changing_default` 在 Go 0 文件 ④`git show --stat 604061ce51`=12 文件（codex-rs/app-server/tests/suite/v2/bedrock_service_tier_tests.rs; codex-rs/app-server/tests/suite/v2/mod.rs）→ 落点候选 appserver/；context//session//turn//rollout//state/  **[队长核 · 第 84–85 轮]** |
| #50477 | `6c15cc4aaf` | Use the app-server default output cap for TUI workspace commands | ✅ 已落地（队长核） — 6866d3ed | ①`git log --grep="#50477"`=0 ②`rg '#50477' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 6c15cc4aaf`=2 文件（codex-rs/tui/src/get_git_diff.rs; codex-rs/tui/src/workspace_command.rs）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #50480 | `8f7a0f7a87` | Skip managed config loading for registered Windows sandbox refre | ⬜ 未落地 | ①`git log --grep="#50480"`=0 ②`rg '#50480' -g '*.go'`=0 ③最长标识符 `only_registered_refresh_skips_configuration_loading` 在 Go 0 文件 ④`git show --stat 8f7a0f7a87`=3 文件（codex-rs/windows-sandbox-service/src/ipc/authentication.rs; codex-rs/windows-sandbox-service/src/machine_policy.rs）→ 落点候选 （Rust-only 服务，未移植） |
| #50503 | `9ce35d337a` | Use Enter to accept transcript Find results and Escape to cancel | ⬜ 未落地 | ①`git log --grep="#50503"`=0 ②`rg '#50503' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 9ce35d337a`=15 文件（codex-rs/tui/src/app/owned_transcript_browsing_tests.rs; codex-rs/tui/src/app/owned_transcript_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #50504 | `d42aecc56b` | Center TUI confirmations over their retained backdrop (#50504) | ⬜ 未落地 | ①`git log --grep="#50504"`=0 ②`rg '#50504' -g '*.go'`=0 ③最长标识符 `inline_confirmation_preserves_compact_viewport_without_replaying_history` 在 Go 0 文件 ④`git show --stat d42aecc56b`=47 文件（codex-rs/tui/src/app/agents_overview_actions.rs; codex-rs/tui/src/app/agents_overview_actions_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #50505 | `47379efd52` | Keep Command Center selection adjacent after task removal (#5050 | ✅ 已落地（队长核） — 5df0c760 | ①`git log --grep="#50505"`=0 ②`rg '#50505' -g '*.go'`=0 ③最长标识符 `external_removals_preserve_adjacent_selection` 在 Go 0 文件 ④`git show --stat 47379efd52`=10 文件（codex-rs/tui/src/app/agents_overview.rs; codex-rs/tui/src/app/agents_overview_actions.rs）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #50507 | `c542fb93ef` | Record Windows sandbox service stop diagnostics (#50507) | ⬜ 未落地 | ①`git log --grep="#50507"`=0 ②`rg '#50507' -g '*.go'`=0 ③最长标识符 `fatal_diagnostics_keep_typed_codes_without_error_text` 在 Go 0 文件 ④`git show --stat c542fb93ef`=6 文件（codex-rs/windows-sandbox-rs/src/lib.rs; codex-rs/windows-sandbox-rs/src/provisioning_client.rs）→ 落点候选 sandbox/（Windows 面）；（Rust-only 服务，未移植） |
| #50510 | `af5d95f255` | Require GovCloud guidance acknowledgment after Bedrock setup (#5 | ⬜ 未落地 | ①`git log --grep="#50510"`=0 ②`rg '#50510' -g '*.go'`=0 ③最长标识符 `gov_cloud_guidance_requires_acknowledgement` 在 Go 0 文件 ④`git show --stat af5d95f255`=5 文件（codex-rs/tui/src/onboarding/bedrock.rs; codex-rs/tui/src/onboarding/bedrock_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #50525 | `b65ab465ce` | Reject unknown TUI keys in strict config validation (#50525) | ✅ 已落地（队长核） — 42fbf7f3 | ①`git log --grep="#50525"`=0 ②`rg '#50525' -g '*.go'`=0 ③最长标识符 `config_error_from_ignored_toml_value_fields_for_source_name` 在 Go 0 文件 ④`git show --stat b65ab465ce`=4 文件（codex-rs/cli/tests/features.rs; codex-rs/config/src/loader/mod.rs）→ 落点候选 cli/；config/  **[队长核 · 第 84–85 轮]** |
| #50531 | `7d5f55bdad` | Persist realtime transcript tails before closure without inferen | ⬜ 未落地 | ①`git log --grep="#50531"`=0 ②`rg '#50531' -g '*.go'`=0 ③最长标识符 `inbound_handoff_request_updates_realtime_state_during_active_turn` 在 Go 0 文件 ④`git show --stat 7d5f55bdad`=4 文件（codex-rs/app-server/tests/suite/v2/realtime_conversation.rs; codex-rs/core/src/realtime_conversation.rs）→ 落点候选 appserver/；context//session//turn//rollout//state/ |
| #50564 | `b741e480e2` | Allow transcript selection and copying while bottom modals are o | ⬜ 未落地 | ①`git log --grep="#50564"`=0 ②`rg '#50564' -g '*.go'`=0 ③最长标识符 `plan_menu_allows_transcript_selection_and_copy_but_respects_popup_bounds` 在 Go 0 文件 ④`git show --stat b741e480e2`=5 文件（codex-rs/tui/src/app/owned_transcript.rs; codex-rs/tui/src/app/owned_transcript_input_tests.rs）→ 落点候选 tui/（部分在 app/） |
| #50720 | `447eac3b81` | Decode Windows Terminal's mapped Shift+Enter sequence (#50720) | ⬜ 未落地 | ①`git log --grep="#50720"`=0 ②`rg '#50720' -g '*.go'`=0 ③最长标识符 `delayed_poll_consumes_queued_suffix_without_waiting_for_more_input` 在 Go 0 文件 ④`git show --stat 447eac3b81`=8 文件（MODULE.bazel.lock; codex-rs/Cargo.lock）→ 落点候选 （MODULE.bazel.lock：无直接 Go 包）；（codex-rs：无直接 Go 包） |
| #50727 | `3e238776e8` | Show model and reasoning effort near the top of task details (#5 | ⬜ 未落地 | ①`git log --grep="#50727"`=0 ②`rg '#50727' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 3e238776e8`=15 文件（codex-rs/tui/src/app/agents_overview_tests.rs; codex-rs/tui/src/app/agents_overview_view.rs）→ 落点候选 tui/（部分在 app/） |
| #50756 | `cd7d9e128c` | Show unavailable slash commands when searched in side conversati | ⬜ 未落地 | ①`git log --grep="#50756"`=0 ②`rg '#50756' -g '*.go'`=0 ③最长标识符 `side_conversation_tab_does_not_queue_unavailable_match_while_task_running` 在 Go 0 文件 ④`git show --stat cd7d9e128c`=4 文件（codex-rs/tui/src/bottom_pane/chat_composer/slash_input.rs; codex-rs/tui/src/bottom_pane/command_popup.rs）→ 落点候选 tui/（部分在 app/） |
| #50781 | `f365d5754b` | Restrict TUI MCP startup notifications to owned threads (#50781) | ⬜ 未落地 | ①`git log --grep="#50781"`=0 ②`rg '#50781' -g '*.go'`=0 ③最长标识符 `subagent_approval_respects_root_ownership` 在 Go 0 文件 ④`git show --stat f365d5754b`=5 文件（codex-rs/tui/src/app.rs; codex-rs/tui/src/app/app_server_events.rs）→ 落点候选 tui/（部分在 app/） |
| #50786 | `acf9818fae` | Remember Command Center grouping across launches (#50786) | ⬜ 未落地 | ①`git log --grep="#50786"`=0 ②`rg '#50786' -g '*.go'`=0 ③最长标识符 `overview_grouping_persists_across_config_reloads` 在 Go 0 文件 ④`git show --stat acf9818fae`=13 文件（codex-rs/config/src/types.rs; codex-rs/core/config.schema.json）→ 落点候选 config/；context//session//turn//rollout//state/ |
| #50788 | `b8dceb0d4f` | Open slash commands from empty drafts in Vim Normal mode (#50788 | ⬜ 未落地 | ①`git log --grep="#50788"`=0 ②`rg '#50788' -g '*.go'`=0 ③最长标识符 `empty_vim_normal_slash_opens_commands` 在 Go 0 文件 ④`git show --stat b8dceb0d4f`=5 文件（codex-rs/tui/src/app/owned_transcript_tests.rs; codex-rs/tui/src/bottom_pane/chat_composer.rs）→ 落点候选 tui/（部分在 app/） |
| #50803 | `8f82b8a31c` | Use the managed daemon for eligible remote-control launches (#50 | ⬜ 未落地 | ①`git log --grep="#50803"`=0 ②`rg '#50803' -g '*.go'`=0 ③最长标识符 `remote_control_subcommand` 在 Go 0 文件 ④`git show --stat 8f82b8a31c`=5 文件（codex-rs/app-server-daemon/README.md; codex-rs/cli/src/main.rs）→ 落点候选 appserverdaemon/；cli/ |
| #50804 | `ab45264919` | Preserve review lifecycle ordering on failure (#50804) | ⬜ 未落地 | ①`git log --grep="#50804"`=0 ②`rg '#50804' -g '*.go'`=0 ③最长标识符 `failed_turn_completion_preserves_queued_review_start` 在 Go 0 文件 ④`git show --stat ab45264919`=4 文件（codex-rs/core/src/session/review.rs; codex-rs/core/tests/suite/review.rs）→ 落点候选 context//session//turn//rollout//state/；tui/（部分在 app/） |
| #50808 | `e57fc9ea5a` | Prune TUI snapshots and consolidate behavior tests (#50808) | ⬜ 未落地 | ①`git log --grep="#50808"`=0 ②`rg '#50808' -g '*.go'`=0 ③最长标识符 `side_aliases_request_forked_questions_while_task_running` 在 Go 0 文件 ④`git show --stat e57fc9ea5a`=97 文件（codex-rs/tui/src/app/agents_overview_actions_tests.rs; codex-rs/tui/src/app/snapshots/codex_tui__app__agents_overview__tests__actions__deleting_selects_the_next_displayed_task.snap）→ 落点候选 tui/（部分在 app/） |
| #50811 | `afb436df8b` | Honor server reasoning summary defaults in new TUI threads (#508 | ✅ 已落地（队长核） — 8cc01f75 | ①`git log --grep="#50811"`=0 ②`rg '#50811' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat afb436df8b`=5 文件（codex-rs/tui/src/app/tests/new_session_tests.rs; codex-rs/tui/src/app/tests/startup_defaults_tests.rs）→ 落点候选 tui/（部分在 app/）  **[队长核 · 第 84–85 轮]** |
| #50913 | `c2f7fe89d8` | Use server model defaults for connected TUI fresh starts (#50913 | ⬜ 未落地 | ①`git log --grep="#50913"`=0 ②`rg '#50913' -g '*.go'`=0 ③最长标识符 `uses_server_owned_fresh_bootstrap` 在 Go 0 文件 ④`git show --stat c2f7fe89d8`=8 文件（codex-rs/tui/src/app.rs; codex-rs/tui/src/app/startup.rs）→ 落点候选 tui/（部分在 app/） |
| #51063 | `cacdc46619` | Honor prior cancellation before starting Codex delegates (#51063 | ⬜ 未落地 | ①`git log --grep="#51063"`=0 ②`rg '#51063' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat cacdc46619`=2 文件（codex-rs/core/src/codex_delegate.rs; codex-rs/core/src/codex_delegate_tests.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #51065 | `315f0efb34` | Scope Guardian V2 response timing to snapshot sampling (#51065) | ⬜ 未落地 | ①`git log --grep="#51065"`=0 ②`rg '#51065' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 315f0efb34`=1 文件（codex-rs/ext/guardian-v2/src/async_scorer/classification.rs）→ 落点候选 （Go 无 ext/ 目录） |
| #51067 | `39e013c8b7` | Use issuing-step context for Guardian MCP elicitation reviews (# | ⬜ 未落地 | ①`git log --grep="#51067"`=0 ②`rg '#51067' -g '*.go'`=0 ③最长标识符 `deferred_executor_guardian_uses_newly_ready_step_environment` 在 Go 0 文件 ④`git show --stat 39e013c8b7`=7 文件（codex-rs/core/src/guardian/mod.rs; codex-rs/core/src/mcp_tool_call.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #51070 | `823ea830c0` | Preserve trusted-tool context in Guardian Decisions requests (#5 | ⬜ 未落地 | ①`git log --grep="#51070"`=0 ②`rg '#51070' -g '*.go'`=0 ③最长标识符 `adapter_preserves_trusted_tool_authority_and_scope` 在 Go 0 文件 ④`git show --stat 823ea830c0`=3 文件（codex-rs/ext/guardian-v2/src/async_scorer/decisions.rs; codex-rs/ext/guardian-v2/src/async_scorer/decisions_tests.rs）→ 落点候选 （Go 无 ext/ 目录）；appserver/（guardian 面） |
| #51117 | `8571b9eaa4` | Install full context in compaction replacement history (#51117) | ⬜ 未落地 | ①`git log --grep="#51117"`=0 ②`rg '#51117' -g '*.go'`=0 ③最长标识符 `should_retry_with_current_model` 在 Go 0 文件 ④`git show --stat 8571b9eaa4`=28 文件（codex-rs/core/src/compact.rs; codex-rs/core/src/compact_model_fallback.rs）→ 落点候选 context//session//turn//rollout//state/；（Go 无 ext/ 目录） |
| #51126 | `28a264fbc7` | Add promise settlement streaming helpers to code mode (#51126) | ⬜ 未落地 | ①`git log --grep="#51126"`=0 ②`rg '#51126' -g '*.go'`=0 ③最长标识符 `accepts_iterables_values_thenables_and_duplicate_promises` 在 Go 0 文件 ④`git show --stat 28a264fbc7`=9 文件（codex-rs/code-mode-host/tests/settled.rs; codex-rs/code-mode-runtime/BUILD.bazel）→ 落点候选 （code-mode-host：无直接 Go 包）；codemode/ |
| #51133 | `4c9f42f4f8` | Allow Guardian Decisions to fall back to `OPENAI_API_KEY` (#5113 | ⬜ 未落地 | ①`git log --grep="#51133"`=0 ②`rg '#51133' -g '*.go'`=0 ③最长标识符 `decisions_key_fallback_preserves_precedence_and_provider_boundary` 在 Go 0 文件 ④`git show --stat 4c9f42f4f8`=2 文件（codex-rs/ext/guardian-v2/src/async_scorer/startup.rs; codex-rs/ext/guardian-v2/src/async_scorer/startup_tests.rs）→ 落点候选 （Go 无 ext/ 目录） |
| #51137 | `5f8b37cc65` | Recover Guardian reviews from parent checkpoints (#51137) | ⬜ 未落地 | ①`git log --grep="#51137"`=0 ②`rg '#51137' -g '*.go'`=0 ③最长标识符 `guardian_history_uses_deltas_between_eviction_batches` 在 Go 0 文件 ④`git show --stat 5f8b37cc65`=11 文件（codex-rs/core/src/guardian/input_budget.rs; codex-rs/core/src/guardian/mod.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #51139 | `16cb72218c` | Force fresh Guardian sessions for parent-checkpoint recovery (#5 | ⬜ 未落地 | ①`git log --grep="#51139"`=0 ②`rg '#51139' -g '*.go'`=0 ③最长标识符 `fresh_attempt_bypasses_cached_session_and_snapshot` 在 Go 0 文件 ④`git show --stat 16cb72218c`=3 文件（codex-rs/core/src/guardian/review_session_setup.rs; codex-rs/ext/guardian-reviewer/src/pool.rs）→ 落点候选 context//session//turn//rollout//state/；（Go 无 ext/ 目录） |
| #51140 | `3f1ccb7ceb` | Isolate Guardian checkpoint recovery flags per review attempt (# | ⬜ 未落地 | ①`git log --grep="#51140"`=0 ②`rg '#51140' -g '*.go'`=0 ③最长标识符 `ReviewerSelection` 在 Go 0 文件 ④`git show --stat 3f1ccb7ceb`=2 文件（codex-rs/core/src/guardian/input_budget.rs; codex-rs/core/src/guardian/review_session_setup.rs）→ 落点候选 context//session//turn//rollout//state/ |
| #51156 | `c9253c4977` | Send base instructions as Responses input messages (#51156) | ⬜ 未落地 | ①`git log --grep="#51156"`=0 ②`rg '#51156' -g '*.go'`=0 ③最长标识符 `responses_websocket_creates_when_instructions_change` 在 Go 0 文件 ④`git show --stat c9253c4977`=81 文件（codex-rs/codex-api/README.md; codex-rs/codex-api/src/common.rs）→ 落点候选 codexapi/；context//session//turn//rollout//state/ |
| #51185 | `aa6635ece5` | Retry transient gRPC code-mode session admission failures (#5118 | ⬜ 未落地 | ①`git log --grep="#51185"`=0 ②`rg '#51185' -g '*.go'`=0 ③最长标识符 `session_admission_retries_only_transient_failures_and_is_bounded` 在 Go 0 文件 ④`git show --stat aa6635ece5`=4 文件（codex-rs/code-mode-host/tests/grpc.rs; codex-rs/code-mode-host/tests/grpc/admission_tests.rs）→ 落点候选 （code-mode-host：无直接 Go 包）；（code-mode：无直接 Go 包） |
| #51192 | `0d3868a30c` | Wait for SIGCONT when resuming the TUI (#51192) | ⬜ 未落地 | ①`git log --grep="#51192"`=0 ②`rg '#51192' -g '*.go'`=0 ③最长标识符 `record_job_resumed` 在 Go 0 文件 ④`git show --stat 0d3868a30c`=1 文件（codex-rs/tui/src/tui/job_control.rs）→ 落点候选 tui/（部分在 app/） |
| #51200 | `ade17c62b0` | Upgrade Bazel to 9.2.0 and refresh the module lockfile (#51200) | ⬜ 未落地 | ①`git log --grep="#51200"`=0 ②`rg '#51200' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat ade17c62b0`=2 文件（.bazelversion; MODULE.bazel.lock）→ 落点候选 （.bazelversion：无直接 Go 包）；（MODULE.bazel.lock：无直接 Go 包） |
| #51206 | `c19525e55e` | Record initialization analytics for resumed subagents (#51206) | ⬜ 未落地 | ①`git log --grep="#51206"`=0 ②`rg '#51206' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat c19525e55e`=11 文件（codex-rs/analytics/src/events.rs; codex-rs/analytics/src/facts.rs）→ 落点候选 telemetry/（Go 无 analytics/ 目录）；context//session//turn//rollout//state/ |
| #51221 | `2dbcab90e2` | Separate environment requests from runtime selections (#51221) | ⬜ 未落地 | ①`git log --grep="#51221"`=0 ②`rg '#51221' -g '*.go'`=0 ③最长标识符 `toml_default_thread_environment_requests_include_local_and_remote` 在 Go 0 文件 ④`git show --stat 2dbcab90e2`=46 文件（codex-rs/app-server/src/request_processors.rs; codex-rs/app-server/src/request_processors/thread_processor.rs）→ 落点候选 appserver/；（core-api：无直接 Go 包） |
| #51230 | `80e0b51c9e` | Make session lookup pagination stable and report listing failure | ⬜ 未落地 | ①`git log --grep="#51230"`=0 ②`rg '#51230' -g '*.go'`=0 ③最长标识符 `later_page_error_preserves_loaded_sessions` 在 Go 0 文件 ④`git show --stat 80e0b51c9e`=13 文件（codex-rs/rollout/src/recorder.rs; codex-rs/rollout/src/recorder_tests.rs）→ 落点候选 rollout/；state/ |
| #51256 | `580b18cb74` | Start the Windows sandbox service during registered Core setup ( | ⬜ 未落地 | ①`git log --grep="#51256"`=0 ②`rg '#51256' -g '*.go'`=0 ③最长标识符 `start_windows_sandbox_service_for_setup` 在 Go 0 文件 ④`git show --stat 580b18cb74`=3 文件（codex-rs/app-server/src/request_processors/windows_sandbox_processor.rs; codex-rs/windows-sandbox-rs/src/lib.rs）→ 落点候选 appserver/；sandbox/（Windows 面） |
| #51257 | `162fcb3976` | Fix installer checksum verification under Windows PowerShell (#5 | ⬜ 未落地 | ①`git log --grep="#51257"`=0 ②`rg '#51257' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 162fcb3976`=1 文件（scripts/install/install.ps1）→ 落点候选 （scripts：无直接 Go 包） |
| #51330 | `f5fa209bb0` | Measure total Guardian approval decision duration (#51330) | ✅ 已落地（队长核） — af855f66 | ①`git log --grep="#51330"`=0 ②`rg '#51330' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat f5fa209bb0`=1 文件（codex-rs/ext/guardian-reviewer/src/routing.rs）→ 落点候选 （Go 无 ext/ 目录）  **[队长核 · 第 84–85 轮]** |
| #51331 | `41acdad246` | Track sub-agent result delivery outcomes (#51331) | ✅ 已落地（队长核） — 6b989619 | ①`git log --grep="#51331"`=0 ②`rg '#51331' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 41acdad246`=1 文件（codex-rs/core/src/agent/control/completion.rs）→ 落点候选 context//session//turn//rollout//state/  **[队长核 · 第 84–85 轮]** |
| #51332 | `c2ae67d769` | Record multi-agent wait duration by outcome (#51332) | ✅ 已落地（队长核） — 55750e88 | ①`git log --grep="#51332"`=0 ②`rg '#51332' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat c2ae67d769`=1 文件（codex-rs/core/src/tools/handlers/multi_agents_v2/wait.rs）→ 落点候选 context//session//turn//rollout//state/  **[队长核 · 第 84–85 轮]** |
| #51333 | `5ddd19e8a9` | Record the multi-agent version in turn analytics (#51333) | ✅ 已落地（队长核） — 8807c307 | ①`git log --grep="#51333"`=0 ②`rg '#51333' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 5ddd19e8a9`=6 文件（codex-rs/analytics/src/events.rs; codex-rs/analytics/src/facts.rs）→ 落点候选 telemetry/（Go 无 analytics/ 目录）；context//session//turn//rollout//state/  **[队长核 · 第 84–85 轮]** |
| #51334 | `79cae5f7fb` | Count Guardian denial-limit interruptions in telemetry (#51334) | ✅ 已落地（队长核） — 11be9802 | ①`git log --grep="#51334"`=0 ②`rg '#51334' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 79cae5f7fb`=1 文件（codex-rs/ext/guardian-reviewer/src/review.rs）→ 落点候选 （Go 无 ext/ 目录）  **[队长核 · 第 84–85 轮]** |
| #51347 | `588f616e8b` | Measure shell snapshot use and wait time per command (#51347) | ✅ 已落地（队长核） — f2f9fe4f | ①`git log --grep="#51347"`=0 ②`rg '#51347' -g '*.go'`=0 ③最长标识符 `shell_snapshot_command` 在 Go 0 文件 ④`git show --stat 588f616e8b`=6 文件（codex-rs/core/src/tools/runtimes/unified_exec.rs; codex-rs/core/src/tools/runtimes/unified_exec/snapshot_metrics.rs）→ 落点候选 context//session//turn//rollout//state/；execserver/  **[队长核 · 第 84–85 轮]** |
| #51350 | `e32365a2c6` | Allow larger shell snapshots when replaying from a file (#51350) | ⬜ 未落地 | ①`git log --grep="#51350"`=0 ②`rg '#51350' -g '*.go'`=0 ③最长标识符 `snapshot_replay_size_limits` 在 Go 0 文件 ④`git show --stat e32365a2c6`=3 文件（codex-rs/exec-server/src/shell_snapshot.rs; codex-rs/exec-server/src/shell_snapshot_tests.rs）→ 落点候选 execserver/ |
| #51355 | `c0c230e673` | Add bounded diagnostics for agent spawn failures (#51355) | ⬜ 未落地 | ①`git log --grep="#51355"`=0 ②`rg '#51355' -g '*.go'`=0 ③最长标识符 `spawn_failure_context_distinguishes_execution_and_residency_capacity` 在 Go 0 文件 ④`git show --stat c0c230e673`=16 文件（codex-rs/core/src/agent/control/execution.rs; codex-rs/core/src/agent/control/residency.rs）→ 落点候选 context//session//turn//rollout//state/；telemetry//otelinit/ |
| #51378 | `73178e7ca6` | Keep Guardian v2 WebSocket pools warm with concurrent replenishm | ⬜ 未落地 | ①`git log --grep="#51378"`=0 ②`rg '#51378' -g '*.go'`=0 ③最长标识符 `stalled_handshake_does_not_delay_other_warm_sockets` 在 Go 0 文件 ④`git show --stat 73178e7ca6`=9 文件（codex-rs/Cargo.lock; codex-rs/codex-api/src/endpoint/responses_websocket.rs）→ 落点候选 （codex-rs：无直接 Go 包）；codexapi/ |
| #51396 | `57d57df608` | Let late low-risk scores complete pending Guardian reviews (#513 | ⬜ 未落地 | ①`git log --grep="#51396"`=0 ②`rg '#51396' -g '*.go'`=0 ③最长标识符 `cached_approval_is_current` 在 Go 0 文件 ④`git show --stat 57d57df608`=12 文件（codex-rs/app-server/tests/suite/v2/guardian_v2.rs; codex-rs/core/src/guardian/review_request.rs）→ 落点候选 appserver/；context//session//turn//rollout//state/ |
| #51400 | `a4ebc509f4` | Prevent later Guardian scores from releasing earlier pending rev | ⬜ 未落地 | ①`git log --grep="#51400"`=0 ②`rg '#51400' -g '*.go'`=0 ③最长标识符 `later_low_does_not_release_earlier_review` 在 Go 0 文件 ④`git show --stat a4ebc509f4`=5 文件（codex-rs/app-server/tests/suite/v2/guardian_pending_score_tests.rs; codex-rs/app-server/tests/suite/v2/guardian_v2.rs）→ 落点候选 appserver/；（Go 无 ext/ 目录） |
| #51458 | `858aea3449` | Make URLs clickable in user verification prompts (#51458) | ⬜ 未落地 | ①`git log --grep="#51458"`=0 ②`rg '#51458' -g '*.go'`=0 ③最长标识符 `n/a` 在 Go 0 文件 ④`git show --stat 858aea3449`=2 文件（codex-rs/tui/src/bottom_pane/user_verification.rs; codex-rs/tui/src/bottom_pane/user_verification_tests.rs）→ 落点候选 tui/（部分在 app/） |

### 2.B Go 侧有近似符号面（2 条）—— 需语义裁定

| #PR | 上游 SHA | 一句话语义 | 判定 | 证据（Go 符号面） |
|---|---|---|---|---|
| #49360 | `995138d71a` | Carry shell invocation metadata and report executor PATH directo | ⬜(待裁定) | ①`git log --grep="#49360"`=0；但标识符 `ShellInvocation` 在 Go 命中 1 文件（./exec/exec.go）；`git show --stat`=13 文件（codex-rs/Cargo.lock; codex-rs/cli/tests/exec_server.rs）→ 落点候选 （codex-rs：无直接 Go 包）；cli/ |
| #49798 | `c538fbabe5` | Share cached exec-server environment info with Arc (#49798) | ⬜(待裁定) | ①`git log --grep="#49798"`=0；但标识符 `EnvironmentInfo` 在 Go 命中 9 文件（./tool/unified_exec.go; ./appserver/openai_file_environment.go; ./appserver/protocol.go）；`git show --stat`=2 文件（codex-rs/cli/tests/exec_server.rs; codex-rs/exec-server/src/client.rs）→ 落点候选 cli/；execserver/ |

## 3. ➖ N/A（36 条）

| #PR | 上游 SHA | 一句话语义 | 判定 | 决定性证据 |
|---|---|---|---|---|
| #48643 | `d8ec479c34` | Set the provisioned macOS CLI bundle name to ChatGPT (#48643) | ➖ N/A（docs-only） | `git show --stat d8ec479c34` → 1 文件全为测试/快照/文档：.github/scripts/macos-signing/provisioned_macos_cli_package.py |
| #48646 | `67a709665a` | Fix the session-start helper call in the command center test (#4 | ➖ N/A（test-only） | `git show --stat 67a709665a` → 1 文件全为测试/快照/文档：codex-rs/tui/src/app/tests/background_task_defaults_tests.rs |
| #48724 | `274d41a398` | Prevent Linux ETXTBSY races in MCP stdio tests (#48724) | ➖ N/A（test-only） | `git show --stat 274d41a398` → 1 文件全为测试/快照/文档：codex-rs/rmcp-client/tests/mcp_2026_stdio.rs |
| #48973 | `1b1835f751` | Isolate realtime auth fallback test from startup prewarm (#48973 | ➖ N/A（test-only） | `git show --stat 1b1835f751` → 1 文件全为测试/快照/文档：codex-rs/core/tests/suite/realtime_conversation.rs |
| #49000 | `69f7140559` | Isolate the memory startup metadata test from Git enrichment (#4 | ➖ N/A（test-only） | `git show --stat 69f7140559` → 1 文件全为测试/快照/文档：codex-rs/memories/write/src/startup_tests.rs |
| #49060 | `e07e58c842` | Update Guardian messaging snapshots for native agent messages (# | ➖ N/A（test-only） | `git show --stat e07e58c842` → 2 文件全为测试/快照/文档：codex-rs/core/tests/suite/snapshots/all__suite__scenarios__guardian_agent_messages__encrypted_parent_reply_survives_incremental_guardian_reviews.snap; codex-rs/core/tests/suite/snapshots/all__suite__scenarios__guardian_code_mode_messaging.snap |
| #49065 | `a1f05faddb` | Update Guardian handoff snapshot for separate agent messages (#4 | ➖ N/A（test-only） | `git show --stat a1f05faddb` → 1 文件全为测试/快照/文档：codex-rs/core/tests/suite/snapshots/all__suite__scenarios__guardian_handoff__guardian_handoff_delegation.snap |
| #49103 | `7e049b3eaa` | Balance Windows Bazel test shards using duration estimates (#491 | ➖ N/A（docs-only） | `git show --stat 7e049b3eaa` → 4 文件全为测试/快照/文档：.github/scripts/select_windows_bazel_targets.py; .github/scripts/test_select_windows_bazel_targets.py |
| #49266 | `5f7d5add2a` | Remove the remote agent message board client README (#49266) | ➖ N/A（docs-only） | `git show --stat 5f7d5add2a` → 1 文件全为测试/快照/文档：codex-rs/agent-message-board-client/README.md |
| #49275 | `677b07eca0` | Isolate realtime conversation tests from Responses prewarm conne | ➖ N/A（test-only） | `git show --stat 677b07eca0` → 1 文件全为测试/快照/文档：codex-rs/core/tests/suite/realtime_conversation.rs |
| #49277 | `d515b2f85e` | Avoid a turn teardown race in the Guardian agent message test (# | ➖ N/A（test-only） | `git show --stat d515b2f85e` → 1 文件全为测试/快照/文档：codex-rs/core/tests/suite/scenarios_guardian_agent_messages_tests.rs |
| #49316 | `193632d244` | Bump taiki-e/install-action to v2.87.21 in CI setup (#49316) | ➖ N/A（docs-only） | `git show --stat 193632d244` → 1 文件全为测试/快照/文档：.github/actions/setup-ci/action.yml |
| #49369 | `2a34aef794` | Update Bedrock GPT-6 Sol catalog tests to expect multi-agent V2  | ➖ N/A（test-only） | `git show --stat 2a34aef794` → 1 文件全为测试/快照/文档：codex-rs/model-provider/src/amazon_bedrock/runtime_catalog_tests.rs |
| #49408 | `bd5185820d` | Compare tool call metadata in the recorder refresh test (#49408) | ➖ N/A（test-only） | `git show --stat bd5185820d` → 1 文件全为测试/快照/文档：codex-rs/core/src/tools/executed_tool_calls/request_metadata_tests.rs |
| #49595 | `1fc8d54807` | Make the strict network approval test independent of request ord | ➖ N/A（test-only） | `git show --stat 1fc8d54807` → 1 文件全为测试/快照/文档：codex-rs/core/tests/suite/network_approval.rs |
| #49704 | `5602705c96` | Prevent npm alpha dist-tags from moving backward (#49704) | ➖ N/A（docs-only） | `git show --stat 5602705c96` → 3 文件全为测试/快照/文档：.github/scripts/npm_alpha_tag.py; .github/scripts/test_releases.py |
| #49801 | `33aea33b39` | Update the Rust toolchain action for argument-comment linting (# | ➖ N/A（docs-only） | `git show --stat 33aea33b39` → 2 文件全为测试/快照/文档：.github/workflows/rust-ci-full.yml; .github/workflows/rust-release-argument-comment-lint.yml |
| #49822 | `799324821d` | Box the resume future in the agents overview permissions test (# | ➖ N/A（test-only） | `git show --stat 799324821d` → 1 文件全为测试/快照/文档：codex-rs/tui/src/app/agents_overview_tests.rs |
| #49867 | `90d7f2715a` | Update elevated-launch warning snapshot to use `⌃o` for copy (#4 | ➖ N/A（test-only） | `git show --stat 90d7f2715a` → 1 文件全为测试/快照/文档：codex-rs/tui/src/app/snapshots/codex_tui__app__owned_transcript__warning_notice_tests__elevated_launch_warning_center.snap |
| #49959 | `d6c3b448a4` | Test session index thread-name append and removal (#49959) | ➖ N/A（test-only） | `git show --stat d6c3b448a4` → 1 文件全为测试/快照/文档：codex-rs/rollout/src/session_index_tests.rs |
| #50046 | `5fa5aaf0ff` | Stabilize Windows voice build cache keys across tool reinstalls  | ➖ N/A（docs-only） | `git show --stat 5fa5aaf0ff` → 1 文件全为测试/快照/文档：.github/scripts/voice_windows_tools.py |
| #50183 | `b997c72a99` | Add `dots` to issue labeler guidance (#50183) | ➖ N/A（docs-only） | `git show --stat b997c72a99` → 1 文件全为测试/快照/文档：.github/workflows/issue-labeler.yml |
| #50339 | `4cedd0caac` | Add end-to-end coverage for MCP sandbox state enforcement (#5033 | ➖ N/A（test-only） | `git show --stat 4cedd0caac` → 2 文件全为测试/快照/文档：codex-rs/core/tests/suite/mcp_sandbox_tests.rs; codex-rs/core/tests/suite/rmcp_client.rs |
| #50384 | `2739e82858` | Allow opting into 16 KiB ARM64 code pages for macOS signing (#50 | ➖ N/A（docs-only） | `git show --stat 2739e82858` → 1 文件全为测试/快照/文档：.github/scripts/macos-signing/sign_macos_code.sh |
| #50443 | `a4bfd07d51` | Stabilize paused-time code-mode service tests (#50443) | ➖ N/A（test-only） | `git show --stat a4bfd07d51` → 1 文件全为测试/快照/文档：codex-rs/code-mode-runtime/src/service_tests.rs |
| #50516 | `86a54b051c` | Add scenario coverage for remote `/compact` context preservation | ➖ N/A（test-only） | `git show --stat 86a54b051c` → 3 文件全为测试/快照/文档：codex-rs/core/tests/suite/scenarios.rs; codex-rs/core/tests/suite/scenarios_compaction_tests.rs |
| #50977 | `7f892275e3` | Isolate tracing in the strict third-party tool deferral test (#5 | ➖ N/A（test-only） | `git show --stat 7f892275e3` → 1 文件全为测试/快照/文档：codex-rs/core/src/tools/spec_plan_strict_third_party_tests.rs |
| #51061 | `435d1b3f19` | Gate Guardian continuation tests on classifier request capture ( | ➖ N/A（test-only） | `git show --stat 435d1b3f19` → 1 文件全为测试/快照/文档：codex-rs/app-server/tests/suite/v2/guardian_v2.rs |
| #51064 | `c8949e55c2` | Ignore stale refresh responses in agents overview tests (#51064) | ➖ N/A（test-only） | `git show --stat c8949e55c2` → 2 文件全为测试/快照/文档：codex-rs/tui/src/app/agents_overview_actions_tests.rs; codex-rs/tui/src/app/agents_overview_tests.rs |
| #51158 | `7c2ce90716` | Sign the PowerShell installer in Windows releases (#51158) | ➖ N/A（docs-only） | `git show --stat 7c2ce90716` → 3 文件全为测试/快照/文档：.github/actions/windows-code-sign/action.yml; .github/workflows/rust-release-windows.yml |
| #51184 | `7f21b5ee9c` | Remove obsolete Guardian thread-context enables from tests (#511 | ➖ N/A（test-only） | `git show --stat 7f21b5ee9c` → 4 文件全为测试/快照/文档：codex-rs/core/tests/suite/guardian_heartbeat_authorization.rs; codex-rs/core/tests/suite/guardian_retained_context.rs |
| #51186 | `5cbcf810e5` | Prevent stable release pointers from moving backward (#51186) | ➖ N/A（docs-only） | `git show --stat 5cbcf810e5` → 6 文件全为测试/快照/文档：.github/scripts/check_github_canary.py; .github/scripts/npm_alpha_tag.py |
| #51193 | `b4e3726d73` | Test thread archiving before the first turn (#51193) | ➖ N/A（test-only） | `git show --stat b4e3726d73` → 1 文件全为测试/快照/文档：codex-rs/app-server/tests/suite/v2/thread_metadata_update.rs |
| #51198 | `54dd21741d` | Allow concurrent release builds while serializing publication (# | ➖ N/A（docs-only） | `git show --stat 54dd21741d` → 1 文件全为测试/快照/文档：.github/workflows/rust-release.yml |
| #51335 | `dab2cd6056` | Update collaboration snapshots for full-history fork instruction | ➖ N/A（test-only） | `git show --stat dab2cd6056` → 2 文件全为测试/快照/文档：codex-rs/core/tests/suite/snapshots/all__suite__scenarios__code_mode_catalog_messages_ranked_search.snap; codex-rs/core/tests/suite/snapshots/all__suite__scenarios__partial_answers__partial_answer_fork.snap |
| #51426 | `6c2c8d5cfe` | Remove the Bazel JVM override for Windows ARM64 voice builds (#5 | ➖ N/A（docs-only） | `git show --stat 6c2c8d5cfe` → 2 文件全为测试/快照/文档：.github/scripts/run-bazel-ci.sh; .github/workflows/rust-release-windows.yml |

## 4. ✅ 已落地（200 条）

| #PR | 上游 SHA | 一句话语义 | 判定 | Go 证据 |
|---|---|---|---|---|
| #48229 | `10fd75bba7` | Extract Responses failure parsing into a dedicated module (#4822 | ✅ | Go 源码引用: ./model/responses_failed_error_test.go:10（共 2 处） |
| #48238 | `4b9e0cc77f` | Suppress console windows for local Windows MCP servers (#48238) | ✅ | Go 源码引用: ./mcp/http_headers_helper.go:118（共 7 处） |
| #48272 | `dfdb40cd0b` | Prevent Windows daemon launches from retaining launcher stdio (# | ✅ | Go commit: af4f4114 `sync239: the detached daemon does not retain the launcher's stdio (#48272)` |
| #48318 | `25270df261` | Keep TUI reconnect attempts running until the shared deadline (# | ✅ | Go commit: 4a912eaa `sync244: re-pin the static layer to 25270df261 and triage #48318` |
| #48344 | `c9e2520707` | Preserve tool metadata for OpenAI provider endpoint overrides (# | ✅ | Go commit: deba17e4 `sync248: the runtime-only internal-metadata provider grant (#48344)` |
| #48350 | `09a3d2108f` | Display reconnect commands on a separate line (#48350) | ✅ | Go commit: dbeb44c7 `sync249: the reconnect command on its own line (#48350)` |
| #48352 | `ca41ed337f` | Show turn tips while working and after completion in the TUI (#4 | ✅ | Go 源码引用: ./parity/rust_tui_snapshot_manifest_test.go:40（共 6 处） |
| #48353 | `e72da2b538` | Stabilize skill catalogs across executor availability changes (# | ✅ | Go 源码引用: ./parity/rust_fixture_manifest_test.go:140（共 1 处） |
| #48469 | `b334d5b3f2` | Default to copying transcript selections in more terminals (#484 | ✅ | Go commit: ad588229 `sync268: the copy-on-select terminal defaults (#48469)` |
| #48483 | `1a89aec960` | Prevent console windows for piped Windows child processes (#4848 | ✅ | Go commit: 4291e281 `windows: suppress the console windows of the remaining piped helpers (#48483)` |
| #48489 | `6e1ab4d294` | Fix Mermaid shape, relationship, and state description parsing ( | ✅ | Go commit: ce512f4c `sync277: the Mermaid crate (codex-rs/mermaid) with #48489` |
| #48491 | `a6bd19261c` | Fall back to embedded mode under restrictive Windows launchers ( | ✅ | Go commit: d02f963e `sync276: embedded fallback under restrictive Windows launchers (#48491)` |
| #48502 | `0fbf0bedc2` | Fix ChatGPT browser sign-in for local app servers (#48502) | ✅ | Go commit: 1de214b7 `sync281: the TUI ChatGPT sign-in browser flow (#48502)` |
| #48508 | `12de0e395d` | Preserve WebSocket continuations when steering a turn (#48508) | ✅ | Go commit: 5692a593 `sync282: interrupted incomplete responses complete the turn (#48508)` |
| #48513 | `7f6c0f9387` | Refresh the TUI welcome screen for new sessions (#48513) | ✅ | Go commit: 442857ca `sync284: the welcome banner and its greeting (#48513)` |
| #48531 | `06f97622f8` | Add context to Windows sandbox runtime registration errors (#485 | ✅ | Go commit: 56ff8d55 `plan: triage #48531 (windows-sandbox-service registration context)` |
| #48544 | `7ed14a27db` | Make onboarding login links easier to copy (#48544) | ✅ | Go commit: 446e66fa `sync308: the onboarding login-link copy shortcut (#48544)` |
| #48547 | `d6f25a54ba` | Fade blossom replays back to the idle state (#48547) | ✅ | Go 源码引用: ./parity/rust_tui_snapshot_manifest_test.go:32（共 1 处） |
| #48548 | `16c21e3f36` | Preserve table cell source metadata through TUI rendering (#4854 | ✅ | Go 源码引用: ./parity/rust_tui_snapshot_manifest_test.go:33（共 1 处） |
| #48551 | `7b7d934408` | Fix TUI math rendering for zero and big wedge expressions (#4855 | ✅ | Go commit: 9b91e5e7 `sync309: the inline math recogniser with #48551's \\$ allowance` |
| #48560 | `6a39914e37` | Keep working tips stable during transcript interaction (#48560) | ✅ | Go commit: 5b4cec71 `sync312: stable working tips during transcript interaction (#48560)` |
| #48562 | `e8fdbf1f7c` | Use a consistent borderless session header in the TUI (#48562) | ✅ | Go commit: 785284d4 `sync313: the borderless session header (#48562)` |
| #48568 | `b8d5e3f12e` | Allow exec-server to proxy permitted private IPs upstream (#4856 | ✅ | Go commit: c5e71adf `plan: record sync474 (#48568 end-to-end) and the #51188 position-semantics follo` |
| #48574 | `0f9a731ade` | Preserve deferred tool namespace names before descriptions (#485 | ✅ | Go commit: cbe9eb7f `plan: record sync368-371 (#51439/#51391/#49084/#48574)` |
| #48621 | `334b6e7321` | Remove follow-up prompt suggestions from the TUI (#48621) | ✅ | Go 源码引用: ./parity/rust_tui_snapshot_manifest_test.go:102（共 1 处） |
| #48623 | `98072cf5f6` | Preserve empty Markdown list markers in the TUI (#48623) | ✅ | Go commit: f6e16d38 `sync473: preserve empty Markdown list markers in the TUI (#48623)` |
| #48764 | `32f5784851` | Preserve MCP app resource URIs without defaulting display mode ( | ✅ | Go commit: 37f490a1 `plan: record sync372 (#48764) and #49475/#49693 dispositions` |
| #48783 | `ea64727556` | Add single-server MCP status discovery with thread connection re | ✅ | Go commit: 9c5b884d `sync432: restrict MCP status discovery by serverName (#48783)` |
| #48796 | `d7748e1185` | Add opt-in structured errors for Guardian circuit-breaker interr | ✅ | Go 源码引用: ./parity/rust_enum_surface_test.go:107（共 2 处） |
| #48800 | `abc8f0c9a1` | Use the terminal palette for ordered Markdown list markers (#488 | ✅ | Go commit: f8b37cb7 `sync472: use the terminal palette for ordered Markdown list markers (#48800)` |
| #48807 | `99f7758a57` | Show short turn durations in TUI completion footers (#48807) | ✅ | Go commit: 6a9cd62d `sync436: show short turn durations in the completion footer (#48807)` |
| #48814 | `659b35f131` | Preserve punctuation and semicolons in Mermaid labels (#48814) | ✅ | Go commit: e727d02a `sync332: preserve punctuation and semicolons in Mermaid labels (#48814)` |
| #49036 | `41ed72c32b` | Add opt-in conversation history retrieval to Guardian reviews (# | ✅ | Go 源码引用: ./features/features.go:336（共 1 处） |
| #49057 | `6288753b46` | Add handoff-aware root context for Guardian reviews (#49057) | ✅ | Go 源码引用: ./features/features.go:333（共 1 处） |
| #49084 | `88e9a8329d` | Track app-server running turns incrementally (#49084) | ✅ | Go commit: cbe9eb7f `plan: record sync368-371 (#51439/#51391/#49084/#48574)` |
| #49098 | `f5430515a8` | Resolve Windows sandbox PowerShell fallbacks on the exec server  | ✅ | Go commit: 3b0a75b9 `sync457: resolve Windows sandbox PowerShell fallbacks on the exec server (#49098` |
| #49136 | `d7f7fa4106` | Remove the plus separator after Option symbols in TUI key hints  | ✅ | Go commit: 46c64284 `sync434: keep option glyphs compact in TUI key hints (#49136)` |
| #49262 | `a7660cd154` | Trace turn phases and correlate accepted input with turns (#4926 | ✅ | 三段齐全：① `46d9a9eb` sync488（mailbox_preemption 1/3）② `195943ab` sync553（`codex.turn_input` span + `codex.turn.phase`/sampling/tool_blocking span）③ `5aedba53` sync555（手动 compaction `codex.compaction` phase span，= 车道 `555c0f57` 的纯增量 111 行） | |
| #49276 | `4994306e9f` | Enable enterprise MCP sign-in and account-scoped grant cleanup ( | ✅ | Go commit: f28cf25f `sync469: mint the MCP OAuth login id in the MCP layer (#49276)` |
| #49286 | `65c3f40bef` | Model exec-server session attachment state as an enum (#49286) | ✅ | Go commit: e0ea0574 `sync458: pin exec-server session attachment-state invariants (#49286)` |
| #49297 | `d5e6526362` | Scan the session index backwards for batch thread name lookups ( | ✅ | Go commit: 4d860670 `plan: record sync373 (#49297) and #49956/#49415/#49910 dispositions` |
| #49330 | `5937592c07` | Keep remote control reconnect backoff capped during sustained fa | ✅ | Go commit: 86b02a7f `plan: record sync378-380 (#49330/#49379/#49689)` |
| #49332 | `ed9e5a26a8` | Clean up canceled exec-server RPC requests immediately (#49332) | ✅ | Go commit: a26f97f9 `sync459: prove canceled exec-server RPC calls clear pending immediately (#49332)` |
| #49361 | `94d642d8b4` | Clarify credential storage wording across authentication UI and  | ✅ | Go commit: 399b28d8 `plan: record sync449-454 (#51211, #51402, #49361, #51235) and the compaction-che` |
| #49379 | `bd4204efc2` | Compile hook matchers during discovery (#49379) | ✅ | Go commit: 86b02a7f `plan: record sync378-380 (#49330/#49379/#49689)` |
| #49388 | `d6571322d2` | Fix Windows path inference for opaque URIs with slash prefixes ( | ✅ | Go commit: 47d2006c `sync317: opaque Windows path inference with slash prefixes (#49388)` |
| #49395 | `ceea67163f` | Remove randomized greetings from TUI session headers (#49395) | ✅ | Go commit: 67a32d02 `sync490: remove randomized greetings from TUI session headers (#49395)` |
| #49403 | `17a9df60e4` | Add an experimental flag for bundled tools in login shells (#494 | ✅ | Go 源码引用: ./features/features.go:300（共 1 处） |
| #49406 | `7c35e1551f` | Support explicit cyber access programs with OpenAI API keys (#49 | ✅ | Go 源码引用: ./features/features.go:321（共 1 处） |
| #49407 | `67352b5b4a` | Recover exec-server sessions after environment info timeouts (#4 | ✅ | Go commit: 8c6b7621 `sync455: recover exec-server sessions after environment/info timeouts (#49407)` |
| #49414 | `16a7c0f0eb` | Filter graceful shutdown guard and trigger traces from SQLite lo | ✅ | Go commit: 4f06860f `sync315: the graceful shutdown log targets (#49414)` |
| #49415 | `d940fb21f3` | Truncate input text in protocol debug output (#49415) | ✅ | Go commit: 4d860670 `plan: record sync373 (#49297) and #49956/#49415/#49910 dispositions` |
| #49424 | `d1d0e89558` | Infer Windows UNC paths with forward and mixed slashes (#49424) | ✅ | Go commit: c0348559 `sync316: UNC app path inference with forward and mixed slashes (#49424)` |
| #49426 | `2cc65cdd4c` | Enable analytics by default for daemon-launched app servers (#49 | ✅ | Go commit: d94ba44a `sync327: enable analytics by default for daemon-launched app servers (#49426)` |
| #49437 | `fc81a7154b` | Add local audio device selection to TUI voice settings (#49437) | ✅ | Go commit: f129146a `sync489: wire local audio device settings in the TUI hosts (#49437/#49836)` |
| #49475 | `9ef9cb1d9f` | Complete turn abort callbacks before emitting terminal events (# | ✅ | Go commit: 37f490a1 `plan: record sync372 (#48764) and #49475/#49693 dispositions` |
| #49480 | `90abcfac02` | Add experimental thread prediction protocol types (#49480) | ✅ | Go commit: 2b69a0a6 `sync336: add thread/attachmentOwner/list and thread prediction protocol types (#` |
| #49560 | `2e5fea64ee` | Add an opt-in model catalog to multi-agent context (#49560) | ✅ | Go 源码引用: ./features/features.go:324（共 1 处） |
| #49598 | `de02016798` | Persist explicit user goal edits in model history (#49598) | ✅ | Go commit: 5f722add `sync482: prove TUI goal edits reach the model history as user.goal (#49598)` |
| #49683 | `6996cde697` | Add a managed feature gate for in-app voice (#49683) | ✅ | Go 源码引用: ./features/features.go:330（共 1 处） |
| #49689 | `0a76a2520f` | Export skill invocation events through OpenTelemetry (#49689) | ✅ | Go commit: 86b02a7f `plan: record sync378-380 (#49330/#49379/#49689)` |
| #49693 | `ecc205edac` | Move thread history projection into one blocking task (#49693) | ✅ | Go commit: 37f490a1 `plan: record sync372 (#48764) and #49475/#49693 dispositions` |
| #49696 | `f2b2e5b2a9` | Make exec-server file reads cancellable between chunks (#49696) | ✅ | Go commit: 166d23b0 `sync456: make exec-server file reads cancellable between chunks (#49696)` |
| #49701 | `3620b2caf8` | Detect SQLite corruption during startup and preserve recovery ba | ✅ | Go commit: bbff5394 `sync496: recover or report runtime SQLite corruption by database policy (#49701)` |
| #49710 | `596ae94fb0` | Classify SQLite corruption using typed error codes (#49710) | ✅ | Go commit: 7f515c85 `plan: record sync471-473 (#49710 typed corruption, TUI list markers)` |
| #49712 | `5e96aabd68` | Avoid full-string scans in token-budget truncation (#49712) | ✅ | Go commit: d26b6d3f `sync318: avoid full-string scans in token-budget truncation (#49712)` |
| #49784 | `3bbf8ec3a1` | Add a requirements feature gate for the browser annotation API ( | ✅ | Go 源码引用: ./features/features.go:327（共 1 处） |
| #49804 | `d2f2c40095` | Use platform-specific modifier labels in TUI shortcut hints (#49 | ✅ | Go commit: edfabadd `sync486: route key hints through the shared platform modifier label table (#4980` |
| #49807 | `7d4c7a0767` | Enable API-key model discovery by default (#49807) | ✅ | Go 源码引用: ./features/features.go:112（共 1 处） |
| #49813 | `d4a475adda` | Support AWS GovCloud regions for Amazon Bedrock Mantle (#49813) | ✅ | Go commit: 3151f0be `sync430: port the Amazon Bedrock account RPC family (#39277/#49813/#49817)` |
| #49817 | `a5eac80150` | Add an advisory Bedrock GovCloud requirements check (#49817) | ✅ | Go commit: 3151f0be `sync430: port the Amazon Bedrock account RPC family (#39277/#49813/#49817)` |
| #49819 | `cda82a2c68` | Recover daemon startup and updater re-exec after cwd deletion (# | ✅ | Go commit: 6c7b29af `sync331: log scheduled daemon update failures (#49819)` |
| #49835 | `08e2b58b07` | Clarify service tier default save errors in the TUI (#49835) | ✅ | Go commit: a4928e6e `sync437: clarify service tier default save errors in the TUI (#49835)` |
| #49836 | `e53e932dc8` | Allow microphone channel selection for voice conversations (#498 | ✅ | Go commit: f129146a `sync489: wire local audio device settings in the TUI hosts (#49437/#49836)` |
| #49843 | `08b07eef12` | Preserve daemon diagnostics and include updater logs in reports  | ✅ | Go commit: ba037b0c `plan: note #49843 (daemon diagnostics + updater logs in reports) as a dedicated ` |
| #49850 | `e2a7d53c9c` | Launch Windows daemon children in a dedicated working directory  | ✅ | Go commit: 231c4738 `sync328: launch Windows daemon children in a dedicated working directory (#49850` |
| #49856 | `c41a72cd3f` | Support Daybreak selection in `codex exec` (#49856) | ✅ | Go 源码引用: ./config/config.go:163（共 4 处） |
| #49910 | `819efd7273` | Preserve validation errors for invalid TUI keybindings (#49910) | ✅ | Go commit: 4d860670 `plan: record sync373 (#49297) and #49956/#49415/#49910 dispositions` |
| #49956 | `8a400e7812` | Cache the placeholder regex for MCP hook argument expansion (#49 | ✅ | Go commit: 4d860670 `plan: record sync373 (#49297) and #49956/#49415/#49910 dispositions` |
| #50035 | `cc31e374fc` | Follow MCP tool pagination in legacy protocol mode (#50035) | ✅ | Go commit: 6d32da72 `plan: record #50035 disposition (Go already follows MCP pagination in both modes` |
| #50054 | `d9cf203890` | Check token estimate filtering directly on the tracing subscribe | ✅ | Go commit: 978004de `plan: record sync340 (#50054 filter probe)` |
| #50059 | `ae7aad586e` | Fix Linux sandbox startup with multiple denied files (#50059) | ✅ | Go commit: b894dee3 `plan: record sync338 (#50059 verified present)` |
| #50082 | `e4e33a56b0` | Enable dynamic tool inheritance for fresh V2 subagents (#50082) | ✅ | Go commit: 9e5a2332 `plan: record sync343 (#50082 fresh V2 dynamic tool inheritance)` |
| #50083 | `26a49b3882` | Add paginated reverse lookup for thread attachments (#50083) | ✅ | Go commit: e7893db4 `plan: record sync345 (#50402) and mark #50083/#50380/#50129 dispositions` |
| #50087 | `960e878df4` | Preserve queued agent mail across session eviction (#50087) | ✅ | Go commit: 811f160f `plan: record #50087 disposition (Go lacks the eviction mailbox model)` |
| #50093 | `9d53dd91b5` | Prevent shared instruction providers from delegating to themselv | ✅ | Go commit: 8c016274 `plan: record sync335 and note #50093/#50536 dispositions` |
| #50094 | `2635431edb` | Add attachment owner lookup to the app-server (#50094) | ✅ | Go commit: 2b69a0a6 `sync336: add thread/attachmentOwner/list and thread prediction protocol types (#` |
| #50099 | `59f18e8133` | Add opt-in Decisions comparison for Guardian V2 (#50099) | ✅ | Go 源码引用: ./features/features.go:339（共 1 处） |
| #50128 | `4f99920b8c` | Expose the model selected for a running turn's next step (#50128 | ✅ | Go commit: 3231809a `sync334: expose the running turn's next-step model (#50128)` |
| #50129 | `7d3e696c4c` | Preserve Windows environment variables for remote MCP servers (# | ✅ | Go commit: e7893db4 `plan: record sync345 (#50402) and mark #50083/#50380/#50129 dispositions` |
| #50131 | `8d44977aa2` | Add opt-in JSON diagnostics for TCP tunnels (#50131) | ✅ | Go commit: 8fc58430 `sync337: add opt-in JSON diagnostics for TCP tunnels (#50131)` |
| #50162 | `d25c114d49` | Bound in-flight file opens in exec-server (#50162) | ✅ | Go commit: 1fc01e30 `sync339: bound in-flight file opens in exec-server (#50162)` |
| #50177 | `c39bfa4c8f` | Enable writable file streaming in exec-server (#50177) | ✅ | Go commit: 5668aa44 `plan: record sync352 (#50177 exec-server writable file streaming)` |
| #50215 | `d64360f628` | Support Ctrl+Insert for copying TUI selections (#50215) | ✅ | Go commit: 2acd6bb8 `plan: record the A1 #50215 structural-N/A ruling and the audit update` |
| #50354 | `f4e18a95bb` | Skip unrelated subtrees during config alias normalization (#5035 | ✅ | Go commit: 368beeb2 `sync319: skip unrelated subtrees during config alias normalization (#50354)` |
| #50360 | `c73775f19e` | Remove initial messages from session configuration events (#5036 | ✅ | Go commit: 5773973c `plan: record #50360/#51191 dispositions and #51211 deferral evidence` |
| #50375 | `50b4c5e58c` | Use printable ASCII terminal titles under GNU Screen (#50375) | ✅ | Go commit: ab4c1ed1 `sync435: use printable ASCII terminal titles under GNU Screen (#50375)` |
| #50380 | `1ab8c6ef28` | Fix thread unloading after disconnect during MCP startup (#50380 | ✅ | Go commit: e7893db4 `plan: record sync345 (#50402) and mark #50083/#50380/#50129 dispositions` |
| #50402 | `c5d242fa79` | Consolidate command execution output into `aggregated_output` (# | ✅ | Go commit: e7893db4 `plan: record sync345 (#50402) and mark #50083/#50380/#50129 dispositions` |
| #50418 | `44dd77b71e` | Honor Retry-After headers in failed Responses events (#50418) | ✅ | Go commit: f2715b53 `plan: record sync344 (#50418 Retry-After in failed Responses events)` |
| #50427 | `bee28e8a06` | Cap persisted command output in paginated history at 64 KiB (#50 | ✅ | Go commit: 7cdd1f43 `sync323: cap persisted command output in paginated history (#50427)` |
| #50435 | `602d2add6e` | Persist additional tool definitions in rollout history (#50435) | ✅ | Go commit: e18065f3 `sync333: persist additional tool definitions in rollout history (#50435)` |
| #50441 | `0df76892b1` | Support ordered response items in world-state context updates (# | ✅ | Go 源码引用: ./session/top_level_tools.go:18（共 2 处） |
| #50442 | `3629508849` | Preserve native USD amounts in thread usage responses (#50442) | ✅ | Go commit: 0269d580 `sync408: carry native USD amounts in thread usage responses (#50442)` |
| #50446 | `9b0a676d5d` | Bundle rollout attachments into a gzip tar archive (#50446) | ✅ | Go commit: a9284006 `sync409: bundle rollout attachments into a gzip tar archive (#50446)` |
| #50447 | `fd75aa116c` | Remove the provider capability gate for tool namespaces (#50447) | ✅ | Go commit: 17ba9332 `sync322: remove the provider capability gate for tool namespaces (#50447)` |
| #50458 | `820f85cf59` | Truncate oversized MCP results in paginated thread history (#504 | ✅ | Go commit: d19f47cb `sync324: truncate oversized MCP tool results in paginated history (#50458/#50470` |
| #50459 | `c6049ebd96` | Add capability overrides for custom model providers (#50459) | ✅ | Go commit: 2206ae2e `sync330: add capability overrides for custom model providers (#50459)` |
| #50462 | `09bced5ad9` | Populate thread previews from delegated task inputs (#50462) | ✅ | Go commit: a8e27b73 `sync428: populate thread previews from delegated task inputs (#50462)` |
| #50464 | `a0bfaac70c` | Add the `incremental_tools` feature flag (#50464) | ✅ | Go commit: 38a8c680 `sync321: add the incremental_tools feature flag (#50464)` |
| #50470 | `be48ae396e` | Account for JSON overhead when truncating MCP tool results (#504 | ✅ | Go commit: d19f47cb `sync324: truncate oversized MCP tool results in paginated history (#50458/#50470` |
| #50499 | `55922b7984` | Include installer stderr in daemon update failures (#50499) | ✅ | Go commit: 9283dc5e `plan: record sync355 (#50499 installer stderr in daemon update failures)` |
| #50536 | `e1b5b56a4f` | Keep shared MCP types stable in Code Mode exec descriptions (#50 | ✅ | Go commit: 65ddeb49 `plan: record sync342 (#50536 default MCP preamble)` |
| #50540 | `6326163b9a` | Send incremental tool catalog updates in Responses Lite (#50540) | ✅ | Go commit: 1c76de2f `sync419: cover the lite catalog, window scoping and diff helpers (#50540, #51188` |
| #50546 | `55b6f282a8` | Keep MCP resource helpers available in code mode (#50546) | ✅ | Go commit: 6f312534 `plan: record sync357 (#50546 MCP resource helpers in code mode)` |
| #50555 | `6b43e6fe1f` | Skip daemon auto-start for Windows-mounted WSL homes (#50555) | ✅ | Go commit: b1c8c287 `plan: record sync361 (#50555 WSL DrvFS daemon auto-start exclusion)` |
| #50558 | `19e554bb70` | Avoid reading the current directory when resolving absolute path | ✅ | Go commit: d33b864c `plan: mark #50558 N/A and note #50559/#50964 as dedicated passes` |
| #50559 | `1a169eda11` | Distinguish daemon release identity from executable contents (#5 | ✅ | Go commit: d33b864c `plan: mark #50558 N/A and note #50559/#50964 as dedicated passes` |
| #50562 | `58ca099b03` | Keep Code Mode tool discovery guidance stable across catalog cha | ✅ | Go commit: e95d1b8b `plan: record sync348 (#50562 Code Mode guidance stability)` |
| #50687 | `58ae3ba611` | Keep third-party tools deferred in strict Code Mode Only (#50687 | ✅ | Go 源码引用: ./features/features.go:315（共 1 处） |
| #50695 | `3a69ec3ef8` | Preserve local Markdown link labels in the TUI (#50695) | ✅ | Go commit: 6d5bf97d `sync464: preserve local Markdown link labels as label (target) (#50695)` |
| #50700 | `b172810921` | Let the transport create the Windows remote-control socket direc | ✅ | Go commit: bc9f2f06 `plan: record sync354 (#50700 Windows remote-control socket directory)` |
| #50741 | `550eb50545` | Keep environment-backed tools exposed across readiness changes ( | ✅ | Go commit: 993e27c0 `sync477: gate environment-backed tools by turn readiness (#50741, #50962)` |
| #50764 | `dde5f8c5ae` | Allow `/archive` while a turn is running (#50764) | ✅ | Go commit: e936a8ee `sync479: keep /archive available during a running turn in the TUI (#50764)` |
| #50782 | `d0759639f2` | Retry Windows daemon release publication on transient file locks | ✅ | Go commit: df8654db `sync326: retry Windows daemon release publication on transient file locks (#5078` |
| #50802 | `b989f795b1` | Fall back to mklink when Windows daemon junction updates are den | ✅ | Go commit: f45a587f `plan: record sync358 (#50802 mklink fallback for daemon junctions)` |
| #50940 | `de3721a7be` | Recover malformed Windows deny-read ACL state safely (#50940) | ✅ | Go commit: da2f6c46 `plan: record sync360 (#50940 Windows deny-read ACL state recovery)` |
| #50962 | `335c7f8eca` | Gate stable environment tool exposure behind a feature flag (#50 | ✅ | Go commit: 993e27c0 `sync477: gate environment-backed tools by turn readiness (#50741, #50962)` |
| #50964 | `4ad985e2ca` | Track inference tool changes in turn analytics (#50964) | ✅ | Go commit: 3745b9c4 `sync441: count inference tool changes on the turn profile (#50964)` |
| #51119 | `402f5b6fdf` | Clarify incremental tool namespace updates in Responses Lite (#5 | ✅ | Go commit: 25c02f75 `sync410: port the world-state tool catalog diff engine (#50540/#51188/#51202/#51` |
| #51157 | `42312d4ff4` | Enforce required environment skills before model inference (#511 | ✅ | Go commit: 8bac91b5 `plan: record sync463-468 (#51157 wiring, #51402 read/write loop, #49598 compacti` |
| #51188 | `062439b2f8` | Record base instructions in incremental tool history (#51188) | ✅ | Go commit: b90d75cd `sync484: cover the legacy Router compaction path with the tool catalog placement` |
| #51191 | `36df9544a3` | Clean up Unix app-server control-socket startup lock files (#511 | ✅ | Go commit: 5773973c `plan: record #50360/#51191 dispositions and #51211 deferral evidence` |
| #51194 | `a6c5f3a9fa` | Add browser extension request headers to config requirements (#5 | ✅ | Go commit: fde0c0fb `sync325: add browser extension request headers to config requirements (#51194)` |
| #51202 | `93f8e79fd2` | Distinguish namespace removals in incremental tool updates (#512 | ✅ | Go commit: 25c02f75 `sync410: port the world-state tool catalog diff engine (#50540/#51188/#51202/#51` |
| #51203 | `685270a56a` | Make apply_patch preserve line endings unconditionally (#51203) | ✅ | Go commit: 9008d9ab `sync335: make apply_patch preserve line endings unconditionally (#51203)` |
| #51207 | `5ad6891696` | Gate CLI Daybreak controls and selection behind an opt-in featur | ✅ | Go 源码引用: ./config/config.go:164（共 3 处） |
| #51209 | `4d15794336` | Add ranked tool discovery to JavaScript code mode (#51209) | ✅ | Go 源码引用: ./features/features.go:312（共 1 处） |
| #51211 | `7aa8f51049` | Reject sandbox-writable bubblewrap executables from PATH (#51211 | ✅ | Go commit: bbd92988 `sync467: pass the sandbox policy cwd to the exec bwrap warning (#51211)` |
| #51215 | `a811c72969` | Measure raw MCP tool catalog sizes in telemetry (#51215) | ✅ | Go commit: 3af27db0 `sync492: record MCP catalog telemetry on a thread-stamped span of mcpServer/stat` |
| #51217 | `4171362e44` | Preserve review targets and scope misalignment continuation meta | ✅ | Go commit: d9e046ed `sync429: parse the opaque review target from misalignment errors (#51217)` |
| #51220 | `28b91c7c31` | Honor the OTLP metrics temporality preference (#51220) | ✅ | Go commit: 5c03bab0 `plan: record sync362 (#51220 OTLP metrics temporality preference)` |
| #51223 | `d63a9b8344` | Remove legacy personality template metadata (#51223) | ✅ | Go commit: 2ad71f5d `plan: record sync363 (#51223 legacy personality template metadata)` |
| #51235 | `2f412e60d7` | Remove default model labels from TUI model pickers (#51235) | ✅ | Go commit: 399b28d8 `plan: record sync449-454 (#51211, #51402, #49361, #51235) and the compaction-che` |
| #51241 | `8b6bb1c77d` | Add a partial answer message phase (#51241) | ✅ | Go commit: dca3fd35 `sync476: add the partial answer message phase to the app-server, realtime and hi` |
| #51249 | `989c01a41a` | Handle partial answers consistently across agent workflows (#512 | ✅ | Go commit: 46d9a9eb `sync488: wire defer_mailbox_preemption into the app-server turn runtime (#49262,` |
| #51253 | `7ac954ea24` | Enforce Fast and Ultra Fast policies independently (#51253) | ✅ | Go commit: 3955ee0d `plan: record sync461-462 (#51402 root writer, #51253 speed-policy host wiring)` |
| #51260 | `822e58cc3d` | Handle partial answers in realtime routing and thread search (#5 | ✅ | Go commit: dca3fd35 `sync476: add the partial answer message phase to the app-server, realtime and hi` |
| #51329 | `6221a217e2` | Remove partial-history subagent forks (#51329) | ✅ | Go commit: a12d6412 `sync483: remove partial-history subagent forks from the fork-turn modes (#51329)` |
| #51391 | `594283af5c` | Make URLs in asynchronous question titles clickable (#51391) | ✅ | Go commit: cbe9eb7f `plan: record sync368-371 (#51439/#51391/#49084/#48574)` |
| #51402 | `551bd409eb` | Preserve turn attribution across recovery and compaction (#51402 | ✅ | Go commit: 29907e15 `plan: correct round 76 (#51402 landed across sync450/451/452/461/468, not outsta` |
| #51407 | `ccde2fc8b7` | Protect ripgrep lookup during Linux sandbox construction (#51407 | ✅ | Go commit: c1e03539 `sync346: freeze that deny-read glob expansion never runs an external binary (#51` |
| #51411 | `ff9ab4aed9` | Suppress repeated image paste presses in legacy terminals (#5141 | ✅ | Go commit: 7407412e `sync347: suppress repeated image-paste presses in legacy terminals (#51411)` |
| #51415 | `cfc946f4e1` | Expose and persist turn lineage across the app server (#51415) | ✅ | Go commit: 96fb7ec6 `plan: record sync349 (#51415 turn lineage exposure)` |
| #51419 | `f1dd04959c` | Preserve turn attribution when queued mail wakes durable sleep ( | ✅ | Go commit: 6bd1d342 `plan: record sync350 (#51421) and #51419/#51420/#51422 dispositions` |
| #51420 | `946bb6c583` | Finish idle thread unloads after slow shutdown (#51420) | ✅ | Go commit: 6bd1d342 `plan: record sync350 (#51421) and #51419/#51420/#51422 dispositions` |
| #51421 | `2c3565ebd8` | Include root turn IDs in host-owned Apps tool calls (#51421) | ✅ | Go commit: 6bd1d342 `plan: record sync350 (#51421) and #51419/#51420/#51422 dispositions` |
| #51422 | `404cd42aa5` | Accept environment requests at thread and turn settings boundari | ✅ | Go commit: 6bd1d342 `plan: record sync350 (#51421) and #51419/#51420/#51422 dispositions` |
| #51425 | `f71935b7f0` | Skip stable installer alias publishing for prereleases (#51425) | ✅ | Go commit: 8c34322c `plan: record sync351 (#51425 prerelease npm latest guard)` |
| #51427 | `0ef50c9f66` | Prevent invalidated wakeups from starting a turn (#51427) | ✅ | Go commit: b4e395bd `plan: record sync353 (#51427 invalidated wakeup reservation)` |
| #51433 | `8e23d1836f` | Show hook status messages as titles in the hooks browser (#51433 | ✅ | Go commit: ad515ccf `plan: record sync356 (#51433 hooks browser status titles)` |
| #51439 | `bc3ebcb77d` | Preserve clickable URLs in TUI approval headers (#51439) | ✅ | Go commit: cbe9eb7f `plan: record sync368-371 (#51439/#51391/#49084/#48574)` |
| #51440 | `f6cf05af1d` | Honor Retry-After in WebSocket error events (#51440) | ✅ | Go commit: 8d083ba7 `plan: record sync359 and #51439/#51441 dispositions (#51440)` |
| #51441 | `a9abdeaff1` | Fix core integration tests for updated turn APIs (#51441) | ✅ | Go commit: 8d083ba7 `plan: record sync359 and #51439/#51441 dispositions (#51440)` |
| #51449 | `44fe0289c6` | Make URLs clickable in TUI user input questions (#51449) | ✅ | Go commit: 1e9cb1f3 `plan: record sync364 (#51449 clickable URLs in user input questions)` |
| #51450 | `eb2a33aa18` | Make URLs clickable in MCP elicitation prompts (#51450) | ✅ | Go commit: 05bcc8a6 `plan: record sync365-367 (#51450/#51451/#51452 clickable URLs)` |
| #51451 | `5da229e54d` | Make URLs in the TUI warnings viewer clickable (#51451) | ✅ | Go commit: 05bcc8a6 `plan: record sync365-367 (#51450/#51451/#51452 clickable URLs)` |
| #51452 | `4b5c111349` | Make banner URLs clickable across wrapped lines (#51452) | ✅ | Go commit: 05bcc8a6 `plan: record sync365-367 (#51450/#51451/#51452 clickable URLs)` |
| #51457 | `e44b9bb3da` | Preserve status usage hyperlinks when the URL wraps (#51457) | ✅ | Go commit: d5568ee9 `plan: record sync374-376 (#51457/#51459/#51460)` |
| #51459 | `414d165b47` | Preserve wrapped help links in Windows sandbox prompts (#51459) | ✅ | Go commit: d5568ee9 `plan: record sync374-376 (#51457/#51459/#51460)` |
| #51460 | `44984d2081` | Retry realtime sideband attachment while an existing call activa | ✅ | Go commit: d5568ee9 `plan: record sync374-376 (#51457/#51459/#51460)` |
| #51463 | `b0a6b8d86f` | Record resolved model and reasoning effort in sub-agent activity | ✅ | Go commit: d8d43638 `plan: record sync377 (#51463) and #51465/#51466 dispositions` |
| #51465 | `61969074a7` | Add an optional JSON transcript format for Guardian (#51465) | ✅ | Go commit: d8d43638 `plan: record sync377 (#51463) and #51465/#51466 dispositions` |
| #51466 | `b2592701e1` | Keep Guardian transcript records structured through budget recov | ✅ | Go commit: d8d43638 `plan: record sync377 (#51463) and #51465/#51466 dispositions` |
| #51467 | `5679e675f4` | Keep submission logs useful without exposing payloads (#51467) | ✅ | Go commit: aadf40b1 `plan: record head 5679e675f4 and #51467 disposition` |
| #51470 | `8519cde1ba` | Raise the managed app-server file descriptor limit on Unix (#514 | ✅ | Go commit: d1987a05 `plan: record sync381-384 (#51470/#51471/#51472/#51473)` |
| #51471 | `4854cfa643` | Preserve clickable URLs in pending input previews (#51471) | ✅ | Go commit: d1987a05 `plan: record sync381-384 (#51470/#51471/#51472/#51473)` |
| #51472 | `b0a5191bd6` | Preserve clickable URLs in TUI selection rows (#51472) | ✅ | Go commit: d1987a05 `plan: record sync381-384 (#51470/#51471/#51472/#51473)` |
| #51473 | `0b863c69f5` | Preserve URL destinations in wrapped hook details (#51473) | ✅ | Go commit: d1987a05 `plan: record sync381-384 (#51470/#51471/#51472/#51473)` |
| #51480 | `e79c498b5e` | Preserve tool declaration mode across resumed context windows (# | ✅ | Go commit: 2369662d `plan: correct the stale #51480/#51539 ledger rows` |
| #51482 | `c9870d0157` | Use PathUri for skill identity and path matching (#51482) | ✅ | Go commit: 5389fc51 `sync423: match plugin skill paths by PathUri identity (#51482)` |
| #51483 | `0b47040dc9` | Add correlated, credential-free rendezvous connection diagnostic | ✅ | Go commit: d9e72c35 `sync387: add correlated rendezvous connection diagnostics (#51483)` |
| #51491 | `b3b83ace17` | Classify executor capability root ownership independently of par | ✅ | Go commit: 42cf46d2 `sync399: classify executor capability root ownership independently of parsing (#` |
| #51492 | `28cd3e2501` | Remove obsolete fields from persisted turn context (#51492) | ✅ | Go commit: b2113191 `sync411: re-vendor and re-pin the static protocol parity layer to upstream 18e28` |
| #51493 | `9479e1fdb7` | Bind capability roots to environment selections (#51493) | ✅ | Go commit: e21fcd3d `plan: record sync445-448 (voice selection both halves, #51493/#51517 evidence, G` |
| #51499 | `a9bc7bebfa` | Load rollout history on a single blocking worker (#51499) | ✅ | Go commit: c2548671 `sync386: load rollout history in a single synchronous pass (#51499)` |
| #51500 | `7298a6e020` | Add shared task pinning to the agent command center (#51500) | ✅ | Go commit: 3c38561f `sync424: wire shared task pinning into the command center host (#51500)` |
| #51502 | `ddabe594e6` | Bound relay connection attempts and handle pongs during blocked  | ✅ | Go commit: a4ea319b `sync391: bound relay writes and retain reconnect backoff across flapping connect` |
| #51503 | `044da98b4a` | Expose selected environments to MCP contributors (#51503) | ✅ | Go commit: 18a9c908 `sync401: expose selected environments to MCP contributors (#51503)` |
| #51510 | `eb2d3c6416` | Preserve live TUI settings when configuration reloads fail (#515 | ✅ | Go commit: 8a5e9e62 `sync425: keep live TUI settings on a failed reload in the app layer (#51510)` |
| #51511 | `9545947c6d` | Fix Windows 10 drive-letter opens for no-follow filesystem opera | ✅ | Go commit: 022dece8 `sync390: freeze drive-letter no-follow opens against reparse rejection (#51511)` |
| #51512 | `7efc49b258` | Align Windows sandbox temp permissions with the child environmen | ✅ | Go commit: cb185d38 `sync392: resolve Windows sandbox temp roots from the workload environment (#5151` |
| #51515 | `24edd7b890` | Expose detailed agent tree shutdown failure reports (#51515) | ✅ | Go commit: daa3af97 `sync398: expose detailed agent tree shutdown failure reports (#51515)` |
| #51517 | `19c4793964` | Pass thread persistence intent to attachment uploads (#51517) | ✅ | Go commit: c7e9d38f `plan: record round 76 (coverage corpus correction, #51517/#51539 re-verified as ` |
| #51525 | `ed59a6c1cd` | Preserve the CLI MXC preference in executor config reads (#51525 | ✅ | Go commit: 77edc71b `sync394: implement the executor-local environmentConfig/read (#51525)` |
| #51527 | `ac9b5b8380` | Ignore ripgrep configuration when expanding sandbox deny globs ( | ✅ | Go commit: efac0a9b `sync385: ignore ripgrep configuration when expanding Linux deny masks (#51527)` |
| #51539 | `4aaee872e3` | Add completion-aware realtime attachment and session-scoped deta | ✅ | Go commit: c7e9d38f `plan: record round 76 (coverage corpus correction, #51517/#51539 re-verified as ` |
| #51547 | `e95abcdf49` | Add a Windows MXC sandbox opt-out (#51547) | ✅ | Go commit: 4481ffd8 `sync389: add the Windows MXC sandbox opt-out (#51547)` |
| #51556 | `18e28fe1b9` | Complete dynamic tool lifecycles on cancellation (#51556) | ✅ | Go commit: 533f66f5 `sync412: re-pin the remaining static parity snapshots to upstream 18e28fe1b9 (#5` |
| #51575 | `5a3140176e` | Expose package assembly helpers and support gzip DotSlash artifa | ✅ | Go commit: 0e47d0b7 `plan: record the MCP telemetry, serverName and TUI speed-tier merges plus the up` |

## 5. 队长点名 14 项逐条复核（全部本次实跑）

| #PR | 队长口径 | 本台账结论 | 决定性证据（命令 + 输出要点） |
|---|---|---|---|
| #51402 | 真·剩余（仍 ⛔），请复核或推翻 | **推翻 → ✅ 已落地** | `git log --oneline main \| grep '#51402'` → 5 个 sync commit：`560925b3 sync450`、`66e7545c sync451`、`ad3e8d60 sync452`、`d9254908 sync461`、`ffabbdb4 sync468`；`git merge-base --is-ancestor 560925b3 main` → **YES**。旧结论 `update/plan_2026_10_07.md:2287`（第七十六轮「仍 ⛔」）已被同文件 `:2290`（第七十六轮·更正）自我推翻。 |
| #50477 | 真·剩余（⬜） | **确认 ⬜ 未落地** | Rust `git show --stat 6c15cc4aaf` → 2 文件（`tui/src/get_git_diff.rs`、`tui/src/workspace_command.rs`，+6/−10）；Go `rg -n '50477' -g '*.go' .` → **0 命中**；Go 的 workspace 命令没有改成「用 app-server 默认输出上限」。 |
| #49357 | 真·剩余（⬜） | **确认 ⬜ 未落地** | Rust `git show --stat 1983c48fd1` → 10 文件 / +380 −44（`bottom_pane/chat_composer.rs`、`chat_composer/paste_input.rs`、`textarea/editing.rs`、`startup_draft.rs` + 2 snap）；Go `rg -n '49357' -g '*.go' .` → **0 命中**；`rg -n -i blockquote tui/` 只命中 streaming/markdown **渲染**面，无 composer「粘贴续行」逻辑。 |
| #50209 | 真·剩余（「消费侧存在、producer=0」） | **确认 ⬜ 未落地，且比队长口径更强** | `rg -n 'mouse_scroll_speed\|mouseScrollSpeed' .` → **代码 0 命中（连消费侧也没有）**，仅 `update/plan_2026_10_07.md` 4 处文档提及（:1342/:2106/:2215/:2273）。Rust `git show --stat 4dd51f4a5f` → 12 文件，新增 `config/src/tui_mouse_scroll.rs`（+26）、`config/src/types.rs`、`tui/src/app/owned_transcript.rs` 等。⇒ 派单需**连同消费侧**一起补，不能只补 producer。 |
| #51517 | 已知已落地（sync402） | ✅ | corpus→sync402→Go `0d5dbdc8`（`plan: record the attachment upload persistence intent`）；`git merge-base --is-ancestor 0d5dbdc8 main` → YES。 |
| #51539 | 已知已落地（sync395） | ✅ | corpus→sync395→Go `2e4c58bb`（`plan: record the realtime replacement/detach alignment`）；ancestor → YES。 |
| #51480 | 已知已落地 | ✅ | Go `97d5500c sync400: reuse recorded tool declarations in an existing window (#51480)`（提交标题直接带 PR 号）；ancestor → YES。 |
| #51493 | 已知已落地 | ✅ | Go `e21fcd3d`（第七十六轮更正提到）+ corpus→sync396→`40f7a3a3`；ancestor → YES。 |
| #51482 | 已知已落地 | ✅ | Go `5389fc51 sync423: match plugin skill paths by PathUri identity (#51482)`；ancestor → YES。 |
| #51491 | 已知已落地 | ✅ | Go `42cf46d2 sync399: classify executor capability root ownership ... (#51491)`；ancestor → YES。 |
| #51500 | 已知已落地 | ✅ | Go `3c38561f sync424: wire shared task pinning into the command center (#51500)`；ancestor → YES。 |
| #51503 | 已知已落地 | ✅ | Go `18a9c908 sync401: expose selected environments to MCP contributors (#51503)`；ancestor → YES。 |
| #51510 | 已知已落地 | ✅ | Go `8a5e9e62 sync425: keep live TUI settings on a failed reload ... (#51510)`；ancestor → YES。 |

复跑命令（一次跑完 9 条已落地项）：
```bash
cd /home/jacks/jacks_dev/codex_go
for s in 0d5dbdc8 2e4c58bb 97d5500c 40f7a3a3 5e402ebc ad19d6b6 15ae7564 8a5e9e62 5389fc51; do
  git merge-base --is-ancestor $s main && echo "$s YES $(git log --format='%s' -1 $s)"; done
```

## 6. 与队长第七十七轮的交叉核对（3 处需要纠正/降权）

| 项 | 第七十七轮口径（`update/plan_2026_10_07.md:2311`） | 本台账 | 依据 |
|---|---|---|---|
| #51331/#51332/#51333/#51334 | A 类「确认未落地」，已派 syncmcpcfg | **一致 ⬜** | `git log --grep` = 0；Go 无 `analytics/` 目录；符号面 0。 |
| #51347/#51350 | A 类，已派 sync51480 | **一致 ⬜** | 同上（符号面 0；`telemetry/metric_names.go` 只有 `codex.shell_snapshot`，无逐命令 use/wait 面）。 |
| **#51335** | A 类「确认未落地」，已派 syncmcpcfg | **应改判 ➖ N/A（建议撤单）** | `git -C ../codex show --stat dab2cd6056` → **只有 2 个 `.snap`**（+4/−4，`code_mode_catalog_messages_ranked_search.snap`、`partial_answer_fork.snap`），**无生产代码**。它是快照刷新，不构成 Go 缺口。 |
| #51156 / #51117 | B 类「有 Go 表面，待独立审计」 | **降权为 `⬜(待裁定)`，不派单** | Go 确有表面：`session/top_level_tools.go` 的 `BaseInstructionsContentKind`/`BaseInstructionsSnapshot`、`rollout/rollout.go:230 replacement_history`。机械判定（PR 号 grep）为 0 不足以定性。 |
| #48575 | B 类「最可能小而真」 | **一致 ⬜**（旧表不能算 ✅） | 旧表引的 `985cf47a4e` 是**上游** sha，`git cat-file -e 985cf47a4e` 在 Go 仓不存在；Go `rg -n 'provisionedExecutor\|ProvisioningState'` 有 provisioning 状态机但无「等待窗口」改动。 |
| #50803 | C 类「核心但改动面大」 | **一致 ⬜(待裁定)** | Go 有 `app/daemon_startup_wsl_*.go`、`remotecontrol/`，标识符 `remoteControlSubcommand` 有命中 → 属语义裁定面。 |

## 7. 抽样实测（估误差）与下一步建议

抽样 4 条 `⬜[符号面=0]` 做人工独立复核（读 Rust diff + Go grep）：

| PR | 人工结论 | 证据 |
|---|---|---|
| #49294 Guardian context mode 遥测 | 与台账一致 ⬜ | Go 无 `analytics/` 目录；`rg -n 'guardianContextMode\|guardian_context_mode' -g '*.go'` = **0 命中**。 |
| #50811 新线程继承 reasoning summary 默认 | 与台账一致 ⬜ | `rg -n 'reasoningSummaryDefault\|serverReasoningSummary' -g '*.go'` = **0 命中**。 |
| #49694 rollout 列表扫描批处理 | 与台账一致 ⬜ | `rg -n 'cancellable\|blockingWorker\|batch.*scan' rollout/*.go` = **0 命中**。 |
| #48549 复制保留 Markdown 表格与空白 | **边界项，需裁定** | Go 有 `tui/streaming/table_holdback.go` + `tui/markdown/table.go`（表格渲染/holdback），但未找到「复制时按 Markdown 原样导出」路径。 |

**误差估计**：4 条抽样中 3 条与机械台账一致、1 条为需读代码才能定论的边界项 ⇒ `⬜[符号面=0]` 的**机制结论可用作派单队列**，但**不能当终判**（上界误差未量化）。

**下一步（建议按性价比排序）**

1. 先处理 §2.B 的 2 条（`EnvironmentInfo`、`ShellInvocation`）——最可能「已部分覆盖」，先裁定可避免重复派单。
2. 对 §2.A 的 261 条按 crate 分批做「Rust diff → Go 落点」逐条裁定（`tui` 89 / `core` 54 / `ext` 17 / `windows-sandbox-rs` 10 / `exec-server` 10 / `cli` 7 为前六批）。
3. `windows-sandbox-rs` / `windows-sandbox-service` / `.github` 等纯 Windows/CI 面建议先批量裁定 N/A，压低队列。
4. 本文件生成后 main 仍在前进（`c7e9d38f` → `c30b56ca`）；任何派单前请以**当时 main** 重跑 §0 两条命令。

---
*本文件由 `verify51482` 只读生成；未 commit、未 push、未修改任何既有文件。*

## 8. 对抗式裁定（第二轮，verify51482 独立复核，main=`c30b56ca`）

> 本节对象是 §7「下一步建议 1」点名的 §2.B 两条待裁定项；每条都给本次实跑命令与输出要点。

### 8.A §2.B 两条裁定

| #PR | SHA | 旧判定 | 新判定 | 决定性证据（本次实跑） |
|---|---|---|---|---|
| #49360 | `995138d71a` | ⬜(待裁定) | **⬜ 未落地（确认真缺口，且是 wire 面）** | ①Rust `exec-server-protocol/src/protocol.rs` 给 `EnvironmentInfo` 新增 `pub prepend_path_dirs: Vec<PathUri>`（`#[serde(default, skip_serializing_if="Vec::is_empty")]`），示例 JSON 出现 `"prependPathDirs": ["file:///C:/tools/bin", ...]` ②Go `rg -n -i 'prependPathDirs\|prepend_path_dirs' --glob '*.go' .` → **0 命中**；`execserver/server.go:413 EnvironmentInfo` 字段为 {Shell,ExecutorVersion,ProviderID,CWD,PlatformOS,UserHomeDir,TemporaryDirectories,Capabilities}，**无 prependPathDirs** ③同 PR 新增 `ShellInvocation{shell,use_login_shell}`（`core/src/shell.rs`）供 shell snapshot 与凭据代理使用；Go `rg -n 'use_login_shell\|UseLoginShell' --glob '*.go' .` → **0 命中**（Go 只有 `AllowLoginShell` 布尔，且从 argv 反推：`exec/exec.go:3301 commandFromShellInvocation`，语义与 Rust「显式携带」不同）。落点：`execserver/server.go`、`execserver/*client*.go`、`appserver/environment.go`、`tool/unified_exec.go`。 |
| #49798 | `c538fbabe5` | ⬜(待裁定) | **➖ N/A（Rust 所有权重构，无 wire/行为差异）→ 建议不派单** | ①`git show c538fbabe5 \| grep -E '^[+-]' \| wc -l` = **21 行**，2 文件（`exec-server/src/client.rs`、`cli/tests/exec_server.rs`）②唯一签名改动 `pub async fn environment_info(&self) -> Result<Arc<EnvironmentInfo>, ExecServerError>`；`git show c538fbabe5 -- '*protocol*'` → **空**（未触碰任何协议/结构体定义）③本质是 `OnceCell<EnvironmentInfo>`→`OnceCell<Arc<EnvironmentInfo>>` + 测试 `Arc::ptr_eq`。Go 侧 `execserver/client.go:919 EnvironmentInfo()` 返回 `*EnvironmentInfo` 指针天然共享引用，无 Rust 的 clone 开销问题。⇒ 不构成 Go 功能缺口。 |

**结论**：#49360 由「待裁定」升为**可派单 ⬜**（wire 字段 + 显式 shell 元数据）；#49798 由「待裁定」降为 **N/A**（可从派单队列移除）。§2.B 至此清空。

---

# 第二轮：对抗式裁定（verify51482 + 5 个只读子审计，2026-10-07 下午）

> 本节由 `verify51482` 组织，5 个只读子审计分片执行（全程未 commit / 未 push / 未改文件）。
> **参考点**：Rust `5a3140176e`；Go main 是**移动靶**，五个分片分别落在 `38ecbbf5`（A/B/C/E 起点）、`ac106a9f`（D）。
> ⚠️ 本节结论**取代** §2/§5 的机械判定（见 §10 的口径更正直）。

## 9. 分片判定汇总

| 分片 | 覆盖 crate | 条数 | ✅ | ➖ | ⬜ | Go 参考 HEAD |
|---|---|---|---|---|---|---|
| A | `ext/` `windows-sandbox-rs` `windows-sandbox-service` `.github`/Bazel | 19 | 2 | 12 | 5 | `38ecbbf5` |
| B | `cli` `exec-server` `rmcp-client` `app-server-daemon` `thread-store` | 24 | 4 | 2 | 18 | `38ecbbf5` |
| C | `state` `analytics` `config` `telemetry` `rollout` `codex-api` … | 26 | 0 | 6 | 20 | `38ecbbf5` |
| D | `app-server` | 34 | 8 | 2 | 24 | `ac106a9f` |
| E | `tui` | 83 | 9 | 0 | 74 | `38ecbbf5` |
| **合计** | | **186** | **23** | **22** | **141** | |

### 9.A 判定为 ✅ 已落地（推翻台账的 ⬜，共 23 条）

| #PR | Go 证据 |
|---|---|
| #49261 | `elevated/runner_client_windows.go:88-95` 在 `SetErrorMode` 前取 `createProcessWithLogon` 错误（无 Rust 那处 bug） |
| #49308 | `sandbox/windowssandbox/process_windows.go:44,74` piped 与常规都走 `CREATE_NO_WINDOW` |
| #49702 | `execserver/server.go:90,197-199,2414,2455` 已是 file-handle 术语 + 128 上限（纯重命名对齐） |
| #49778 | `execserver/server.go:50 MethodFSWriteBlock`、`:97 fs/open` modes、`:437-439 FileWriteStreaming` |
| #49811 | `execserver/server.go:1467` 分派 + `:2544 writeBlock`（Go 实现比 Rust 占位更完整） |
| #49939 | `turn/api.go:38 CyberAccessProgram` + `model/catalog.go:394-395` + `turn/agent_loop.go:428` |
| #48779 | `features/features.go:214` + `appserver/guardian_reviewer.go:329-350 ResetAfterParentCompaction` + `retainedctx/` |
| #49280 | `appserver/environment_capability_roots.go:70 restrictCapabilityRootsToSelections`（测试引 Rust 测试名） |
| #49642 | `config/windows_sandbox_mode.go:104-140`（`windows.allow_mxc` 显式+自动两条路径） |
| #49796 | `state/guardian_retained_context.go:37,265`（`DeduplicateRetainedInstructions`） |
| #49806 | `appserver/common_types.go:36 type CodexErrorInfo any`（未知变体天然被接受） |
| #49880 | `state/state.go:110-138 TurnState.RecordGrantedPermissions`（授权挂在 turn 上） |
| #50531 | `realtime/transport.go:766,781,793`（`FlushTranscriptTailOnEnd`） |
| #51221 | `appserver/runtime_router.go:3421-3475 turnEnvironmentSelections`（+ `mcp.TurnEnvironmentSelection`） |
| #49144 | `app/interactive.go:1957`（`git log -S` 首现于上游 `ff3c82c8a9`） |
| #49472 | `tui/tea/permissions.go:63`（`git log -S` 首现于上游 `b588812e8c`） |
| #49857 | `tui/history_cell/notices.go:94`（首现于上游 `f58ed54a9d`） |
| #50140 | `tui/tea/permissions.go:108`（首现于上游 `cb6da58876`） |
| #50396 | `tui/keymap.go:140-141`（`half_page_up/down`，最近改动为上游 `d61c7a824f`） |
| #50431 | `tui/markdown/render.go:267,343`（`mark_buffer_hyperlinks`） |
| #50503 | `tui/bottom_pane/chat_composer/history_search.go:244`（首现于上游 `9ce35d337a`） |
| #50505 | Go 提交 `5df0c760 sync499`（标题带 `#50505`）+ `app/agents_dashboard.go:817` |
| #50811 | Go 提交 `8cc01f75 sync507` + `app/interactive.go:1956` |

### 9.B 判定为 ➖ N/A（共 22 条，均可从派单队列移除）

- **Go 无对应子系统**（`ext/` 不存在；Go 自述 `model/catalog.go:124` 无 async scorer；无 `windows-sandbox-service`）：
  `#48829` `#49067` `#49257` `#49792` `#50066` `#50273` `#50480` `#50507` `#51065` `#51070` `#51133` `#49489` `#51256`
- **纯测试/CI/Bazel/lock，或删除日志行**：
  `#48727`（Rust 测试支撑 crate）`#49706`（`.github`/Bazel/Dylint）`#48686`（删 info 日志，Go 无该行）
- **Rust 运行时/所有权专属，无可观测行为差**（子审计已给 Go 侧决定性证据）：
  `#49692` `#49694` `#49708`（tokio `spawn_blocking` 调度；Go 同步实现且有等价断言）`#49972`（`Arc<Vec<u8>>`，线格式不变）`#49411`（借用生命周期修复）`#49798`（`OnceCell<Arc>`；见 §8.A）

### 9.C 分片内部「需人工裁定 / 勿直接派单」（Go 有近似符号面）

- D：`#48779`（✅ 但注释引 `c2bcb9a26b`，需比对）`#49993` `#49795` `#49785` `#49432` `#49260`
- E：`#50105` `#50112` `#50359` `#49112` `#50564` `#50467` `#49079`（Go 文件存在但归因到更早的 PR）
- C：`#49305`（Go 本地列举是单次整表查询，可能本就不存在 Rust 的 N+1 路径）；`#49675`（部分满足：`model` 已排首位，`stream`/`service_tier` 仍在 `input` 后）

### 9.D 各分片剩余 ⬜（141 条，清单见各分片原始报告）

分片 ⬜ 数：A 5、B 18、C 20、D 24、E 74。**注意** E 的 74 条里 `#50505`/`#50811` 在扫描后已被 sync499/sync507 落地（见 §10），实际应减 2。

## 10. 口径更正与移动靶（**本节优先级最高**）

### 10.A 台账 §5「真·剩余 4 条」**已全部落地**（本次实跑，HEAD `98d4c7cb`）

| #PR | 最新状态 | 命令证据 |
|---|---|---|
| #51402 | ✅ | 5 个 sync commit（sync450/451/452/461/468） |
| #50477 | ✅ | `6866d3ed sync508: use the host default output cap for TUI workspace commands (#50477)`；`tui/workspace_command.go:15-24 DefaultWorkspaceCommandOutputBytesCap` |
| #49357 | ✅ | `98d4c7cb sync509: continue Markdown blockquotes when pasting multiline text (#49357)` |
| #50209 | ✅ | `25842147 sync505` + `52d5f5f1 sync506`；`app/interactive.go:1931-1941 interactiveMouseScrollSpeed` |

⇒ §5 的四条旧结论（含我 2026-10-07 上午的复核）**全部过期**，不要把 `#50477/#49357/#50209` 再当缺口派单。

### 10.B 机械 ⬜ 判据的系统性偏差（重要）

台账 §2 的 ⬜ 判据是「PR 号 grep=0 **且** 最长标识符在 Go 0 命中」。但**最长标识符多为 Rust 测试函数名**（`snake_case`、Go 必为 0 命中）⇒ **系统性假阴性**。
实测量化：分片抽样的 186 条里，**45 条（24%）并非真缺口**（23 ✅ + 22 ➖）。E 分片独立复核也独立指出同一结论（其原话：台账「固定点是旧 main，判据取最长标识符…系统性假阴性」）。

**更正后的口径建议**：⬜ 只能当**派单队列**；派单前必须先跑 §9 式的「生产 diff → Go 行为定位」，或直接重扫下面这条命令。

### 10.C 对当前 main 的重扫（可复跑）

```bash
cd /home/jacks/jacks_dev/codex_go
head=$(git rev-parse --short HEAD)     # 本次 = 98d4c7cb
while read pr; do
  printf '%s\t%s\n' "$pr" "$(git log --format='%h %s' --grep "$pr" main | head -1)"
done < <(awk -F'\t' '/^\| #[0-9]/ && $5 ~ /⬜/ {gsub(/ /,"",$2); print $2}' update/remaining_ledger_2026_10_07.md)
```
本次结果：263 条里 **6 条**已有提交提及（`#49357 #50209 #50477 #50505 #50811` 为真落地；`#48575` 是 `plan:` 提及、**仍是 ⬜**）⇒ **257 条仍无提交提及**（其中含 §9 已判 ➖ 的 22 条与「待裁定」若干）。

### 10.D 结论边界
- 本台账的 `✅` 是下界、`⬜` 是上界；**唯一权威口径 = 派单时以当时 main 重跑 §10.C**。
- 未 commit / 未 push / 未改任何既有文件（本文件为新建，`git status` 显示 `?? update/remaining_ledger_2026_10_07.md`）。

## 11. 新鲜度戳（verify51482，重扫时 HEAD=`11be9802`）

台账冻结在 `c30b56ca` 后，main 又落地了 7 条原 ⬜：`#48776`（`3426c4d9 sync510`）、`#49357`（`98d4c7cb sync509`）、`#50209`（`52d5f5f1 sync506`）、`#50477`（`6866d3ed sync508`）、`#50505`（`5df0c760 sync499`）、`#50811`（`8cc01f75 sync507`）、`#51334`（`11be9802 sync511`）。
另 `#48575` 只有 `plan:` 提交提及，**仍未落地**。
⇒ 263 条原 ⬜ 中 **7 条已真落地、256 条仍无提交提及**（后者仍含 §9 已判 ➖ 的 22 条）。

---

## 12. 队长第二批派单裁定（windows / CI / ext 面，verify51482）

> ⚠️ 编号说明：队长来信称本章为「§9」，但**本文件 §9 已被第二轮 5 分片裁定占用**（队长来信所据快照为 635 行，此后已增至 700+ 行）。故本章记为 **§12**，对应队长来信第 2 批的 48 条。
> 其中 **22 条已在 §9 裁定**，本章不重复论证、只给结论并指向 §9；**26 条为本章新裁**。
> 参考点：Rust `5a3140176e`；Go main 扫描时 `f048c5ed`（本文件定稿前已到 `c5fd7e60`）。

### 12.0 计数

| 组 | 条数 | ✅ | ➖ N/A | ⬜ |
|---|---|---|---|---|
| 1 windows-sandbox-rs | 10 | 2 | 5 | 3 |
| 2 windows-sandbox-service | 2 | 0 | 2 | 0 |
| 3 CI / lock / 非生产 | 11 | 0 | 11 | 0 |
| 4 ext（guardian 等） | 18 | 1 | 7 | 10 |
| **合计** | **41**（另 7 条为队长口径差异，见 12.4） | **3** | **25** | **13** |

### 12.1 组 1：windows-sandbox-rs（10 条）

| #PR | SHA | 一句话语义 | 最终判定 | 决定性证据 |
|---|---|---|---|---|
| #49261 | `f35a0fdc5d` | 保留 runner 启动错误码 | ✅ | Go `elevated/runner_client_windows.go:88-95` 在 `SetErrorMode` 前取 `createProcessWithLogon` err（见 §9.A） |
| #49308 | `50d9c5deac` | piped legacy 进程不开控制台 | ✅ | Go `sandbox/windowssandbox/process_windows.go:44,74` 走 `CREATE_NO_WINDOW`（见 §9.A） |
| #48829 | `e6f4af1d92` | 等 provisioning 服务启动 | ➖ N/A | `git show --stat`=1 文件 `windows-sandbox-rs/src/provisioning_client.rs`；Go `rg -i 'WaitNamedPipe\|SandboxProvisioningResponse'`=**0**，无该 client |
| #49389 | `9212b3eca8` | 串行化共享 Windows 账户的测试 | ➖ N/A | `git show --name-only` = `.config/nextest.toml` + `Cargo.toml`×4 + `**/tests/**` + `windows-sandbox-rs/BUILD.bazel`，**零生产 .rs**（生产代码过滤后为空） |
| #50058 | `b06b7d2f77` | 升级 `windows-sys` 到 0.61.2 | ➖ N/A | `git show --stat`=99 文件，全部是 `Cargo.toml` + 随新 API 改写的 Windows 调用点；Go `go.mod:59 golang.org/x/sys v0.47.0` 是**独立**依赖，无对应动作（纯 Rust 依赖迁移） |
| #50507 | `c542fb93ef` | 记录沙箱服务停止诊断 | ➖ N/A | `git show --name-only` 全在 `windows-sandbox-rs`+`windows-sandbox-service`；Go `rg 'service_diagnostics\|ServiceStopReason'`=**0** |
| #51256 | `580b18cb74` | 注册 Core 安装时启动沙箱服务 | ➖ N/A | `git show --name-only` 全在 `windows-sandbox-rs`；Go 无 `windows-sandbox-service`/`CreateService`（`rg`=0） |
| #49058 | `df3e439c02` | ACL 修复支持超长运行时路径 | ⬜ | Go **有**对应物 `sandbox/windowssandbox/acl_windows.go:178,103,141`，但全用按路径 `Get/SetNamedSecurityInfo`；`rg -i 'long.?path\|verbatim\|ExtendedPath' sandbox/`=**0**。落点 `sandbox/windowssandbox/acl_windows.go` |
| #49325 | `26dd19ef47` | logon 失败 1056 重试一次 | ✅ 已落地（队长核） — fe88a5a8 | Go `elevated/runner_client.go:150-171 RetryRunnerSpawnOnce` 只按 `isRefreshableWindowsError`（:194，仅 1326/1312）重试；`rg '1056\|SERVICE_ALREADY_RUNNING'`=**0**。落点 `elevated/runner_client.go`  **[队长核 · 第 84–85 轮]** |
| #49690 | `7e8878f605` | 提权沙箱保留 PowerShell 相对路径 | ⬜ | `git show --name-only`=`windows-sandbox-rs/src/acl.rs`、`setup.rs`、`setup_provisioning/*`（**生产**）；Go `rg -i 'relative_path\|relativePath' sandbox/`=**0**。落点 `sandbox/windowssandbox/` |

### 12.2 组 2：windows-sandbox-service（2 条）

| #PR | SHA | 语义 | 判定 | 证据 |
|---|---|---|---|---|
| #49067 | `5a5a4aa796` | 策略事件剔除配置值 | ➖ N/A | `git show --stat`=2 文件全在 `windows-sandbox-service/src/ipc.rs`；Go 无该包 |
| #50480 | `8f7a0f7a87` | 已注册刷新跳过托管配置加载 | ➖ N/A | 全在 `windows-sandbox-service/src/{ipc/authentication.rs,machine_policy.rs}`；`rg 'machine_policy\|only_registered_refresh'`=**0** |

### 12.3 组 3：CI / lock / 非生产（11 条，**全部 ➖ N/A**）

判定命令（对 263 条统一跑）：`git show --stat --format= --name-only <sha> | grep -E '\.rs$' | grep -v -E '(^|/)tests?/|(^|/)tests\.rs$|_tests\.rs$'` → 为空即「零生产 .rs」。
> 注：第一版漏了 basename 就叫 `tests.rs` 的文件（`core/src/session/tests.rs`、`windows-sandbox-rs/src/unified_exec/tests.rs`），已补进过滤式；补齐后 11 条全部为 0 生产 .rs（见 §12.5 ②）。

| #PR | SHA | 文件面 | 判定 |
|---|---|---|---|
| #49074 | `22d1d9336f` | `MODULE.bazel`、`defs.bzl`、`BUILD.bazel`、`patches/*.patch` | ➖ N/A（Bazel） |
| #49096 | `2ce64843be` | `MODULE.bazel.lock`、`Cargo.lock` | ➖ N/A（lock） |
| #49114 | `69043f05c4` | `tests.rs`（唯一文件） | ➖ N/A（测试） |
| #49246 | `0462dcc062` | `Cargo.lock`、`linux-sandbox/Cargo.toml`、`linux-sandbox/tests/suite/bundled_bwrap.rs` | ➖ N/A（测试 + manifest） |
| #49318 | `b1e72963c3` | 22 个 `.snap` | ➖ N/A（快照） |
| #49713 | `18131270fe` | `.codex/skills/babysit-pr/**`（SKILL.md/py/toml） | ➖ N/A（仓库内开发者工具） |
| #49787 | `83cf88306e` | `BUILD.bazel`×2 | ➖ N/A（Bazel） |
| #50166 | `7135b303d9` | `MODULE.bazel.lock`、`audit.toml`、`Cargo.{lock,toml}` | ➖ N/A（lock/审计配置） |
| #51200 | `ade17c62b0` | `.bazelversion`、`MODULE.bazel.lock` | ➖ N/A（Bazel 版本钉） |
| #51257 | `162fcb3976` | `scripts/install/install.ps1` | ➖ N/A（Rust 安装脚本；Go 无该脚本面） |
| #49389 | `9212b3eca8` | 见 §12.1（同时属组 1） | ➖ N/A |

**单独标出（不是 CI/lock）**：`#48604`（`9db8162d65`）先被 CI 过滤命中，实为**内容面**——删除内置 `plugin-creator` skill（`skills/src/assets/samples/plugin-creator/**` 的 SKILL.md/references/scripts + 一个 app-server 测试）。Go **仍内嵌**该 skill（`systemskills/systemskills.go:23 //go:embed assets/samples`、`systemskills_test.go:19` 断言含 `plugin-creator/references/plugin-json-spec.md`）⇒ 维持 **⬜**，落点 `systemskills/`。

> 口径差异：队长说是 18 条纯 CI/lock，我按「零生产 .rs」实测**只有 11 条**（+`#48604` 属内容面）。多出的 7 条若在你的清单里，请把它们的 PR 号发我再裁。

### 12.4 组 4：ext（18 条）

| #PR | SHA | 语义 | 判定 | 证据 / 落点 |
|---|---|---|---|---|
| #51334 | `79cae5f7fb` | denial-limit 中断遥测 | **✅ 已落地** | 复核成立：`11be9802 sync511: count guardian denial-limit interruptions in telemetry (#51334)`；`telemetry/metric_names.go:78-81 GuardianDenialLimitReachedMetric` + `appserver/guardian_denial_limit_metric_test.go:15 TestGuardianDenialLimitReachedMetricLikeRust` |
| #49257 | `4f16bdc265` | 缓存批准含不完整 root 上下文 | ➖ N/A | 全在 `ext/guardian-v2/src/async_scorer/`；Go 无 async scorer（`model/catalog.go:124` 自述） |
| #49792 | `5f300d3f74` | async sampling 的 retained conversation | ➖ N/A | 全在 `ext/guardian-v2`、`ext/guardian-reviewer`、`guardian-context` |
| #50066 | `d91294c39e` | Decisions 有界传输 | ➖ N/A | 全在 `ext/guardian-v2/src/async_scorer/decisions.rs`（Rust 侧 `#[cfg(test)]`） |
| #50273 | `ca466061d6` | Decisions 一致率/时延指标 | ➖ N/A | 全在 `ext/guardian-v2/src/async_scorer/` |
| #51065 | `315f0efb34` | 响应计时限定 snapshot sampling | ➖ N/A | `classification.rs`；`rg 'responses_duration\|sampling_started'`=0 |
| #51070 | `823ea830c0` | Decisions 保留 trusted-tool 上下文 | ➖ N/A | `ext/guardian-v2/.../decisions.rs` + `guardian-context/src/trusted_tool.rs`；`rg 'trusted_tool\|TrustedTool'`=0 |
| #51133 | `4c9f42f4f8` | Decisions 回退 `OPENAI_API_KEY` | ➖ N/A | 全在 `ext/guardian-v2/src/async_scorer/startup.rs` |
| #49127 | `13f580ef09` | budget 前去重 cloud/executor 技能清单 | ⬜ | Rust `ext/skills/src/render_dedup.rs` + `world_state_catalogs.rs`（**生产**，非 Go 缺 `ext/` 就能免判）；Go `rg -i 'render_dedup\|dedup_before_budgeting\|WorldStateCatalog'`=**0**，`appserver/skills_test.go:652` 的 dedupe 是**路径去重**（#51482），语义不同。落点 `prompt/` + `appserver/skills_prompt.go:29-35` |
| #49294 | `d79a95bdf8` | 评审/分类遥测记 Guardian context mode | ⬜ | `rg -F 'guardian_context_mode'`=**0**；`telemetry/guardian_v2_event.go` 仅 Outcome/RiskLevel。落点 `telemetry/guardian_v2_event.go` |
| #49793 | `726f1492db` | Guardian v2 异步分类对话模式 | ⬜ | `rg 'async_classifier_mode\|async_classifier_conversation_token_limit'`=**0**。落点 `config/` + `appserver/guardian_reviewer.go` |
| #49812 | `0f8df3d214` | 影子技能排序移出 turn 准备路径 | ⬜ | Go 有 `appserver/skill_shadow_selection.go`，但由 `appserver/turn_runtime.go:9100`**同步**调用，无 goroutine/信号量。落点 `appserver/skill_shadow_selection.go` |
| #49898 | `da2e174a66` | 扩展文件系统访问限定到回调权限 | ⬜ | Rust 改 `core/src/session/*` + `ext/skills/*`；Go `rg -i 'CallbackPermission\|FileSystemAuthority\|ExtensionTool'`=**0**。落点 `session/` + `tool/` |
| #51139 | `16cb72218c` | 父 checkpoint 恢复强制全新 Guardian 会话 | ⬜ | Rust `core/src/guardian/review_session_setup.rs` + `ext/guardian-reviewer/src/pool.rs`；Go `rg 'FreshParentCheckpoint\|ReviewerSelection\|guardianReviewerPool'`=**0**。落点 `appserver/guardian_reviewer.go` |
| #51330 | `f5fa209bb0` | 度量 Guardian 审批决策总时长 | ✅ 已落地（队长核） — af855f66 | `rg 'codex.guardian.decision.duration_ms'`=**0**；`telemetry/metric_names.go:40` 只有 `codex.guardian.review.duration_ms`。落点 `telemetry/metric_names.go` + `appserver/guardian_reviewer.go:418`  **[队长核 · 第 84–85 轮]** |
| #51378 | `73178e7ca6` | Guardian v2 WS 池保活 + 并发补充 | ⬜ | Rust `codex-api/.../responses_websocket/connector.rs` + `ext/guardian-v2/.../sampler/connection_pool.rs`；Go `rg -i 'connectionPool\|replenish'`=**0**。落点 `codexapi/` |
| #51396 | `57d57df608` | 迟到低风险分数完成 pending review | ⬜ | `appserver/guardian_reviewer.go:100-149` 只有 `GuardianV2ScoreProgress`；`rg 'completePending\|pendingReview'`=**0**。落点 `appserver/guardian_reviewer.go` |
| #51400 | `a4ebc509f4` | 阻止后续分数释放更早的 pending review | ⬜ | `rg 'wrapperLag\|wrapper_lag\|pendingScore'`=**0**。落点 `appserver/guardian_reviewer.go` |

### 12.5 复跑命令（3 条最能支撑本章裁定的）

```bash
# ① 零生产 .rs 判定（组 3 全部 11 条 + 组 1 的 #49389）
cd /home/jacks/jacks_dev/codex
for s in 22d1d9336f 2ce64843be 69043f05c4 0462dcc062 b1e72963c3 18131270fe 83cf88306e 7135b303d9 ade17c62b0 162fcb3976 9212b3eca8; do
  echo "$s -> $(git show --stat --format= --name-only $s | grep -E '\.rs$' | grep -v -E '(^|/)tests?/|(^|/)tests\.rs$|_tests\.rs$' | wc -l) prod-rs"
done
# 实测输出：11 个 sha 全部 -> 0 prod-rs
# ② ext 组新裁 4 条的 Go 符号面（全 0）
cd /home/jacks/jacks_dev/codex_go
rg -n -i 'render_dedup|dedup_before_budgeting|CallbackPermission|FileSystemAuthority|FreshParentCheckpoint|ReviewerSelection|connectionPool|replenish' --glob '*.go' .
# ③ #51334 已落地
git log --format='%h %s' --grep '#51334' main | head -1   # 11be9802 sync511: ...
```

---

## 13. 第三批：§9.D 的 141 条 ⬜ 对抗式复核（verify51482，只追加）

> 依据队长派单 `msg-1791354712261284000-1680`。对象 = **§9.D 的 141 条 ⬜**（E 74 + B 18 + C 20 + A 5 + D 24）。
> 优先级：① E 分片 tui 74 条 → ② B 分片 18 条（cli/exec-server/rmcp-client/thread-store）→ ③ D 分片 app-server 24 条；另含 C 20 + A 5。
> 参考点：Rust `5a3140176e`；Go main 复核窗口 `15c54dfa` → `edcfd073`（各分片在各自 HEAD 上复跑过 `git log --grep '#PR' main`）。
> **判定口径**：`✅` 必须有 Go 侧 `file:line` 或 `git log -S` 硬锚点；无锚点但行为一致者记 **`✅(弱证据)`** 并单列，**不计入强 ✅**；`➖ N/A` 必须给「改动落在 Go 机制性不存在的面」的决定性证据，禁用「结构性等价」当理由。
> **`➖ N/A` 判据（队长口径已确认）**：必须是**对 Go 树的正向陈述 + 可复跑命令**（如 `ls -d ext` 非零退出、`rg -n -i '<子系统入口标识符>' --glob '*.go' .` = 0 命中），**不能**只用「该 PR 号在 Go 提交里 0 命中」；理由：Rust 被改 crate 在 Go 移植中不存在 ⇒ 无落点，属结构性 N/A，区别于用户禁止的「结构性等价冻结」（后者是宣称已有等价物却不给证据）。
> **行号漂移提醒**：本节 `file:line` 按复核时 HEAD 钉定，main 前进会漂；故 **§13.H 对每条 ✅ 另给「不依赖行号」的锚点（函数/常量/测试名 + 落点提交）**，复核者勿把「行号对不上」误当反证。

### 13.0 计数

| 分片 | 面 | 条数 | 强 ✅ | ✅(弱证据) | ➖ N/A | 维持 ⬜ |
|---|---|---|---|---|---|---|
| E | tui（48549–49810） | 37 | 4 | 5 | 1 | 27 |
| E | tui（49816–51458） | 37 | 3 | 1 | 3 | 30 |
| B | cli / exec-server / rmcp-client / thread-store | 18 | 1 | 0 | 4 | 13 |
| C+A | 跨面（sqlite / analytics / config / model / mcp …） | 25 | 4 | 2 | 1 | 18 |
| D | app-server | 24 | 0 | 1 | 0 | 23 |
| **合计** | | **141** | **12** | **9** | **9** | **111** |

- **强 ✅ 12**（有 PR 号/SHA 注释或 `git log -S` 首现的硬锚点）；
- **✅(弱证据) 9**（行为已一致，但无 PR 锚点、或归因到相邻的另一 PR）——**不计入强 ✅ 计数**；
- **➖ N/A 9**；
- **⬜ 111**（真缺，均附「Go 缺什么 + 落点候选」）。

> 队长点名复核项：`#51333` 在 C 分片，本轮独立复核 = **✅**（`8807c307 sync518`）。`#51331` **不在 §9.D 清单内**；顺带核实其真实落地提交为 `6b989619 sync522`（非 sync517），供队长更正。

### 13.A E 分片 tui（48549–49810，37 条）

| #PR | 上游 SHA | 一句话语义 | 判定 | 决定性证据 |
|---|---|---|---|---|
| #48549 | `75a714843b` | 复制 TUI 响应时保留 Markdown 表格与空白 | ✅(弱证据) | `rg '#48549' -g '*.go'` → `parity/rust_tui_snapshot_manifest_test.go:33`「…`#48548/#48549` added the table-copy continuation」；⚠️ Go 无「拖选转写复制」（`rg -F 'CopySelection\|SelectionText\|LogicalLineSource'`=0），表格复制只走 `/copy` whole-response |
| #48626 | `449d42ced9` | 切换会话时不再显示上一会话摘要 | ✅ 已落地（队长核） — a10be6aa | Go **仍保留**该 PR 删掉的机制：`tui/app/session_lifecycle.go:319 SessionSummaryForThread` + `:331 ResumeHintForResumableThread` + `session_lifecycle_test.go:216`（`git log -S` 首现 `9273d358`）；未见生产调用点  **[队长核 · 第 84–85 轮]** |
| #48628 | `8f195c93d7` | 切任务时保留空白会话 | ✅(弱证据) | `tui/tea/agents_overview.go:598 setAgentsOverviewBlankSession`（注释引 Rust `agents_overview.blank_sessions`）、`model.go:1682 agentsOverviewBlankSessions`；测试 `agents_overview_test.go:156` |
| #48754 | `819cdb726d` | `/status` 去边框 + 长值换行 | ⬜ | Go `/status` **仍画框**：`tui/state.go:432`（`╭─…╮`）、`:436`（`│ `）、`:438`（`╰`），`RenderStatusCardWidth`；该 PR 正是删边框 |
| #48757 | `71b38795d8` | 状态 shimmer 节奏对齐桌面 | ✅ | `tui/summary_shimmer.go:11/18/74` 多处 `Rust #48757` + `summary_shimmer_test.go`；`git log --grep '#48757'` → `e81682ce sync514` |
| #48761 | `e75b6b1e0c` | 紧凑活动显示隐藏输出行数 | ✅(弱证据) | `tui/exec_cell/render.go:232`「shared three-row preview with a hidden-line count」+ `exec_cell_test.go:165`；⚠️ 归因注释写的是 `#46492`；Rust `ActivityDisclosure`/「+ Show details」Go 0 命中 |
| #48775 | `596f8c5c3c` | 固定转写头样式对齐原始 prompt | ⬜ | `rg -F 'PromptHeader\|prompt_header'`=0；Go `tui/` 无 `transcript_view/prompt_header` 等价物 |
| #48776 | `89bf86d0bd` | 移除任务行 `current` 徽标 | ✅ | `tui/agents_overview/current_badge_like_rust_test.go:8`「Rust #48776 (89bf86d0bd, …)」；`git log --grep '#48776'` → `3426c4d9 sync510` |
| #48799 | `4c8cf3964d` | 修 Windows 终端采集的 SGR 鼠标上报 | ➖ N/A | Go TUI **不自行开鼠标采集**：`rg -F 'EnablePointerCapture\|?1006\|MouseCellMotion\|EnableMouse'` 生产 0 命中（`tui/tea/right_click_paste.go:21` 注释说明「Go 故意不开 mouse tracking」）⇒ 机制性不存在 |
| #48805 | `d9487a2930` | 模态框打开时仍可滚轮滚转写 | ✅ 已落地（队长核） — 3ba8ede6 | Go 有 overlay 时鼠标全交 overlay：`tui/tea/model.go:3095 if m.overlay != nil { … updateTranscriptOverlayMouse }`；`rg -F 'is_modal_scroll\|has_active_modal'`=0  **[队长核 · 第 84–85 轮]** |
| #48827 | `6af89155d0` | Ghostty/Kitty 链接上显示手型指针 | ⬜ | `rg -F 'link_hover\|LinkHover\|set_link_pointer\|LinkAt'`=0 |
| #48830 | `1cc7e23612` | 更短、中性的中断提示 | ✅ 已落地（队长核） — f6bfeab0 | Go 仍是旧长文案：`tui/chatwidget/turn_runtime.go:703`「Conversation interrupted - tell the model what to do differently…」；`rg -F '■ Conversation interrupted'`=0  **[队长核 · 第 84–85 轮]** |
| #49031 | `d8fc718809` | 澄清 ChatGPT 登录成功文案 | ✅ | `tui/onboarding/auth_flow.go:695`「Rust #49031 …」+ `auth_flow_test.go:371`；`git log --grep '#49031'` → `9951066a sync520` |
| #49037 | `64bf4e7e62` | 全屏状态行显示 Plan 循环提示 | ✅(弱证据) | `tui/bottom_pane/footer.go:186/192/449 CollaborationModeLabel(…, ShowCycleHint)` + `:23 FooterModeCycleHint`；⚠️ 注释归 `Rust #49804`（相邻 PR），Rust 49037 的宽度门控无对应 |
| #49041 | `3074be908a` | 行内代码选区按纯文本复制 | ⬜ | `rg -F 'IsInlineCode\|isInlineCode'`=0；`/copy` 只抽 whole/fenced code/blockquote（`copy_target.go:28`） |
| #49043 | `4f63088cce` | 更新 Pro 套餐显示名（Pro Extra/Standard/Max） | ⬜ | `rg -F 'Pro Extra\|Pro Standard\|Pro (Max)'`=0；`tui/status/helpers.go:79` 仍 `PlanProlite → "Pro Lite"` |
| #49073 | `15c08beee2` | 暴露 realtime 语音目录失败 | ⬜ | `app/voice.go:403-410` 远端取目录出错即静默 `return realtime.BuiltinVoices()`；无 `voice_catalog_failure_*` |
| #49079 | `fe50d010e2` | 集中 TUI 订阅标签 | ⬜ | `rg 'subscription.rs\|SubscriptionDisplay'`=0；Go `Business Premium`/`Enterprise (Automation)` 另出 `#40301`/`02b7c37b` |
| #49089 | `222e24b737` | 渲染 follow-up 指令标签 | ⬜ | `rg -F 'codex-followup'`=0；`tui/assistant_directives.go` 无 `[label]` 解析 |
| #49093 | `19892aee0c` | 启动推广简化为平台化桌面端提示 | ⬜ | Go 仍旧文案 + Fast 促销：`tui/tooltips.go:23 AppTooltip`、`:159 PickPaidTooltip`、`tui/tea/model.go:6799 sessionShowFastStatus` |
| #49105 | `136391a23e` | 重连后恢复未发送输入 | ⬜ | `rg -F 'reconnect_pending'`=0；重连仅走 `"Reconnecting..."` 提示 |
| #49106 | `8f6517772b` | 命令中心历史翻页（Show more） | ⬜ | `rg -F 'ShowMore\|show_more'`=0；Go 的 `NextCursor` 在 `app/agents_dashboard.go:200`（`#49` 面板，不同面） |
| #49112 | `0196495288` | X11 primary selection + 中键粘贴 | ⬜ | `rg -F 'PrimarySelection\|PasteSource\|middle-click'` 生产 0；`tui/tea/right_click_paste.go:209` 注释明说只走 clipboard |
| #49145 | `8d48f71922` | 服务器连接下 `/status` 隐藏推理摘要设置 | ⬜ | `tui/state.go:288` 无条件 `" (reasoning …, summaries auto)"`，无 remote/server 门控 |
| #49153 | `c6c7c8d270` | 复制引用选区时去掉 blockquote 标记 | ✅(弱证据) | `tui/chatwidget/copy_target.go:123 blockquoteTargetText`「strips the leading `>` markers … matching Rust CopyTarget::Quote」；⚠️ Go 走 `/copy` 选取目标 |
| #49161 | `3226512d47` | 遵守 app-server provider 默认值 | ⬜ | `rg -F 'ProviderSelection\|ProviderDefaults'` 生产 0；Go 仅 `tui/oss_selection.go`（不同面） |
| #49171 | `c248f6d48b` | 修 TUI 历史的 model provider 查找 | ⬜ | 同 #49161：Go 无 `provider_selection` |
| #49290 | `4773a132c3` | TUI 新增 `/mcp login <name>` | ⬜ | slash 命令表（`tui/slash_command.go`）无 `login` |
| #49357 | `1983c48fd1` | 粘贴多行文本时续写 Markdown 引用 | ✅ | `tui/tea/composer_paste.go:14`「Rust #49357, upstream 1983c48fd1」+ `composer_paste_test.go:13`；`git log --grep '#49357'` → `98d4c7cb sync509` |
| #49564 | `0b43721d8d` | 复制选中文件路径为纯文本 | ⬜ | `rg -F 'Literal'`=0；`copy_target.go` 无文件路径目标 |
| #49678 | `fcbed044c8` | 恢复问题答案时转义命令草稿 | ⬜ | `rg -F 'EscapeDraft\|escapeDraft'` 生产 0；composer 无 `\!`/`\\` 前缀转义 |
| #49715 | `60947e2341` | TUI 账户安全设置提醒 | ⬜ | `rg -F 'SecuritySetup\|security setup'`=0，无 `security_setup` 模块/banner |
| #49783 | `106772e3c6` | fork 时保留后台线程请求 | ⬜ | `rg -F 'dispatched_requests'` 生产 0；Go `RegisterBackgroundThread` 出 `#31`（dynamic-tools 宿主，不同面） |
| #49799 | `606b139565` | TUI 保留服务器 web-search 设置 | ⬜ | `rg -F 'web_search' tui/app tui/app_server_session`=0（仅 `app/recap.go` 固定 `"web_search":"disabled"`） |
| #49800 | `ec0cfa5da8` | 允许清理缺线程的 replay-only 侧会话 | ⬜ | `rg -F 'thread not found' tui/`=0；`tui/app/side.go` 无该容错 |
| #49809 | `2fde968de8` | 跨会话/重连保留本地启动权限 | ⬜ | `rg -F 'rememberLaunch\|LaunchPermission\|ApprovalPolicyOverride'`=0 |
| #49810 | `cca440697f` | 处理 Enter 前冲刷过期粘贴突发 | ⬜ | `FlushPasteBurstIfDue` 已定义（`tui/bottom_pane/bottom_pane_view.go:119`）但仅测试引用，未在 Enter 前调用 |

**表尾：强 ✅ 4（48757 / 48776 / 49031 / 49357）/ ✅(弱证据) 5（48549 / 48628 / 48761 / 49037 / 49153）/ ➖ 1（48799）/ ⬜ 27。**

### 13.B E 分片 tui（49816–51458，37 条）

> 复核时 HEAD `f9ed456c`；37 条中只有 `#50112`/`#50477` 有带 PR 号的 Go 提交，`#50105` 有强锚点。

| #PR | 上游 SHA | 一句话语义 | 判定 | 决定性证据 |
|---|---|---|---|---|
| #49816 | `1182834d13` | 删除浏览器打开成功提示 | ✅(弱证据) | `rg -F 'in your browser'` 无成功文案；打开路径仅失败时报错：`app/app_link_wiring.go:27-28`、`tui/tea/plugins.go:723-725`；⚠️ 无 #49816 注释/`git -S` 锚点 |
| #49858 | `e1522188e9` | 持久化 `/daybreak` TUI 开关 | ⬜ | `rg -n '"daybreak"'` 仅 `config/config.go:170` 等；TUI slash 表无 daybreak。落点 `tui/slash_command.go`+`tui/app/config_persistence.go` |
| #49859 | `cd4a9cbd25` | continuation/后台 turn 遵守 Daybreak 设置 | ⬜ | `tui/daybreak.go` 仅只读 `Notice{Apply,Astra,Limited}`；自述「the TUI never configures the app's access program」 |
| #49875 | `7b88d09d61` | 启动呈现与执行配置解耦 | ⬜ | `rg 'StartupPresentation\|startup_presentation\|StartupDraft'`=0。落点 `tui/app/startup_*.go` |
| #49876 | `d2b254fd17` | 移除 TUI personality 管线 | ⬜ | Go **仍保留** personality：`tui/chatwidget/settings.go:9-14,69`、`app/interactive.go:3569`（注入 `personality=` override） |
| #49912 | `444da310e1` | 临时结构化线程遵守审批策略 | ⬜ | `app/recap.go:72` 仍硬编码 `ApprovalPolicy: "never"` |
| #50013 | `b527ce4734` | 新建 TUI 线程遵守服务端模型默认值 | ⬜ | `rg 'StartupLaunchChoices'`=0；`app/remote_tui.go:4533` 无条件 `Model: shared.Model` |
| #50039 | `a97fb78085` | Find 关闭查询后结果仍可读 | ⬜ | Go 无 `tui/transcript_view/`、无 `owned_transcript`。落点 `tui/`（需新建） |
| #50045 | `1cc9917508` | Find 展开仅限当前匹配 | ⬜ | 同上；无 `search_presentation`/`disclosure` |
| #50052 | `57a38c1beb` | 恢复草稿保留问题上下文 | ⬜ | `rg 'recovered_draft\|take_pending_drafts'`=0。落点 `tui/tea/async_questions.go` |
| #50105 | `e58b493329` | 合并 composer footer 到 footer_state | ✅ | `tui/bottom_pane/chat_composer/footer_state.go:9`（"Rust parity … footer_state.rs"）+`:18 ComposerFooterModeQuitShortcutReminder`；`footer_test.go:10 TestFooterModeTransitionsMatchRust`（台账因最长标识符 `quit_shortcut_hint_visible` 假阴性） |
| #50109 | `6ece7bfc21` | 全屏提示限高可滚动 | ⬜ | `rg 'wheel_at_edges'`=0。落点 `tui/tea/model.go` |
| #50112 | `a0a7a63002` | 集中 loading glyph 与帧调度 | ✅ | `tui/motion.go:76-89 LoadingGlyph*` + `tui/loading_glyph_like_rust_test.go:8`「Rust #50112」；Go 提交 `ba951807 sync524 (#50112)` |
| #50148 | `e7ea5f4a86` | TUI 增加 managed worktree 工具 | ⬜ | `rg 'create_worktree\|list_worktrees\|managed_worktree_tool_specs'`=0。落点 `mcp/`+`tui/tea/worktree_browser.go` |
| #50199 | `3f97f2b3bd` | 账号更新后 `/status` 恢复 email | ⬜ | `rg 'refreshAccountEmail\|AccountEmailLoaded'`=0。落点 `tui/app/app_server_events.go` |
| #50207 | `e3c2a83937` | 流式中稳定表格提前释放到 scrollback | ⬜ | `rg 'NeedsScrollbackReflow\|CompletedSourceLen'`=0。落点 `tui/streaming/` |
| #50216 | `37f53199a6` | 任务重命名改用共享文本编辑器 | ⬜ | `tui/agents_overview/overview.go:1082` 手写 `editInput`；`rg 'new_single_line'`=0 |
| #50219 | `14a477ea89` | tmux 选项探测限 1s | ➖ N/A | Go **无 tmux 选项探测**：`rg -i 'show-options\|mouse_capture\|show-hooks'`=0；tmux 仅用于 clipboard/notifications/size_monitor |
| #50345 | `84d5437b6e` | subagent 选择器反映归档态 | ⬜ | `rg -i 'archiv' tui/tea/agent.go`=0 |
| #50359 | `91168365a5` | hook 系统消息渲染 ANSI | ⬜ | `tui/history_cell/hooks.go` 纯文本；`rg -i 'ParseAnsi'` 仅生成器/测试 |
| #50389 | `f88a6efe43` | transcript 导航前先看配置键位 | ⬜ | 属 owned_transcript；`tui/tea/model.go updateTranscriptOverlayKey` 无配置绑定优先级 |
| #50416 | `dff5270b29` | 新/分叉会话 worktree 选项文案 | ⬜ | `rg -F 'Use current Git worktree'`=0；`tui/tea/worktree_browser.go:112` 用另一组标签 |
| #50433 | `66561d0301` | API-key 账号可用 Daybreak | ⬜ | 同 #49858：TUI 无 `/daybreak` |
| #50434 | `c536ffcb18` | owned transcript 键盘复制选区 | ⬜ | `rg -i 'copyMode\|copy_mode\|LiteralSelection'`=0。落点 `tui/tea/model.go` |
| #50467 | `f5a2272c6c` | 选区复制为字面文本并保留富 HTML | ⬜ | `rg -i 'LiteralSelection\|render_selection'`=0 |
| #50477 | `6c15cc4aaf` | TUI 工作区命令用 app-server 默认输出上限 | ✅ | Go 提交 `6866d3ed sync508 (#50477)`；`tui/workspace_command.go:19`「Rust #50477」+ `tui/workspace_parity_test.go:49/157` |
| #50504 | `d42aecc56b` | 确认弹窗居中且保留背景 | ⬜ | `rg 'ViewStack\|CenteredView'`=0；`tui/tea/modal.go renderModal` 无居中/背景保留 |
| #50510 | `af5d95f255` | Bedrock 后强制 GovCloud 确认 | ⬜ | `rg -i 'bedrock' tui/`=0。落点 `tui/onboarding/` |
| #50564 | `b741e480e2` | 底部弹窗打开时仍可选择/复制 transcript | ⬜ | Go 无 transcript_view 子系统（同 #50434） |
| #50727 | `3e238776e8` | 任务详情顶部显示 model+reasoning | ⬜ | `rg -n 'Reasoning:' tui/agents_overview/render.go`=0 |
| #50756 | `cd7d9e128c` | 侧会话搜索时显示不可用 slash 命令 | ⬜ | `tui/bottom_pane/command_popup.go` filtered 时直接过滤 unavailable |
| #50781 | `f365d5754b` | MCP 启动通知限自有线程 | ⬜ | `rg -i 'owns_thread\|ownsThread'`=0 |
| #50788 | `b8dceb0d4f` | Vim Normal 空草稿 `/` 打开 slash | ⬜ | `tui/tea/vim_mode.go:264-270` 一律 `startVimSearch` |
| #50808 | `e57fc9ea5a` | 精简 TUI 快照并合并行为测试 | ➖ N/A | 生产 `.rs` 各 hunk 头均落 `mod tests`（`git show … \| grep '^@@'` 全为 `… mod tests`）；97 文件 +285/−2243，多数 `.snap` 删除 |
| #50913 | `c2f7fe89d8` | 已连接 TUI 新建线程用服务端模型默认值 | ⬜ | `rg 'uses_server_owned_fresh_bootstrap'`=0。落点 `app/remote_tui.go`+`tui/app/startup_*` |
| #51192 | `0d3868a30c` | 恢复 TUI 时等待 SIGCONT | ➖ N/A | `rg -i 'SIGTSTP\|SIGCONT' --glob '*.go'`=0；`SuspendContext` 只被测试引用，无生产 caller |
| #51458 | `858aea3449` | 用户核验提示中的 URL 可点击 | ⬜ | `rg -i 'userVerification' tui/`=0。落点 `tui/bottom_pane/` |

**表尾：强 ✅ 3（50105 / 50112 / 50477）/ ✅(弱证据) 1（49816）/ ➖ 3（50219 / 50808 / 51192）/ ⬜ 30。**

### 13.C B 分片（cli / exec-server / rmcp-client / thread-store，18 条）

> 逐条 `git log --format='%h %s' --grep '#<PR>' main`：**仅 `#48575` 命中**（`c5fd7e60` 为 `plan:` 提及），其余 17 条为空。

| #PR | 上游 SHA | 一句话语义 | 判定 | 决定性证据 |
|---|---|---|---|---|
| #48575 | `985cf47a4e` | 已置备 executor 初始连接给 5 分钟窗口 | ✅ | `execserver/remote_harness.go:49 provisionedEnvironmentConnectTimeout = 5*time.Minute`、`:59`、`:363`（注释点名 Rust #48575）；消费方 `appserver/environment.go:1339`；测试 `execserver/provisioned_connect_test.go`、`appserver/environment_provisioned_connect_test.go`；`git log -S → f048c5ed sync516 (#48575)` |
| #48983 | `c0d26949be` | 仅更新时间戳时不做整条元数据重写 | ⬜ | `rg 'touch_thread_updated_at\|is_empty_except_updated_at'`=0；`session/store.go:1196 updateMetadataLocked` 改记录、无时间戳快路径。落点 `session/store.go` |
| #49028 | `f817e16905` | macOS seatbelt 用数值 `TIOCSTI` 生成 ioctl 拒绝规则 | ➖ N/A | Go 无 `debug sandbox` 命令（`cli/cli.go:3357 parseDebug`）；`rg 'TIOCSTI\|file-ioctl'`=0；改动全在 Go 不存在的 `cli/src/debug_sandbox.rs` |
| #49160 | `0d7b8117d3` | projectless TUI 会话 + workspace 默认权限 | ⬜ | `rg tui/ projectless`=0；`rg 'keep_current_directory'`=0。落点 `tui/`,`app/` |
| #49782 | `1a913493dc` | 失败 shell 快照捕获清理进程组 | ➖ N/A | Go `execserver/` 无快照子系统：`rg 'capture_too_large\|waitid\|WNOWAIT\|kill_process_group'`=0；`ExecParams`（`execserver/server.go:430`）无 `shellSnapshot` 字段 |
| #49805 | `e7798c9944` | 可写文件流按能力门控（缺能力先报错） | ✅ 已落地（队长核） — 97157c51 | `execserver/client.go:1064 FSOpen`、`:1187 FSWriteBlock` 都不查 `Capabilities.FileWriteStreaming`。落点 `execserver/client.go`  **[队长核 · 第 84–85 轮]** |
| #49818 | `a73898c249` | sandboxed file open 用专用 `FsHelperOpenParams` | ➖ N/A（临界） | `rg 'FsHelperOpenParams'`=0；`execserver/fs_helper.go` 操作集无 `fs/open`；`execserver/server.go:2430` 明确拒绝沙箱化流式 open ⇒ 被改路径 Go 不存在 |
| #49855 | `bc197b77bc` | 提权 Windows TUI 会话改用 embedded 模式 | ⬜ | 无「提权→embedded」选择；`rg 'Running as administrator'`=0；`app/daemon_startup.go` 无提权分支。落点 `app/daemon_startup.go` |
| #49861 | `8ea2428c38` | 状态栏/终端标题加 Daybreak 项 | ⬜ | `tui/bottom_pane/status_surface_preview.go` 枚举无 Daybreak；`rg 'Daybreak on'`=0。落点 `tui/bottom_pane/` |
| #49987 | `ecc78e4cf5` | 可续期 EMA HTTP 认证 + 凭据版本化 | ⬜ | `rg 'EmaCredentialLease\|EmaAuthenticated\|resourceBearer\|enterprise-credential-version'`=0。落点 `mcp/` |
| #50019 | `9552906b2b` | 保护 `CODEX_GUARDIAN_DECISIONS_API_KEY` 不外泄 | ✅ 已落地（队长核） — 6de1fa45 | `envutil/envutil.go:23 nonInheritableEnvVars` 不含该键；`rg 'CODEX_GUARDIAN_DECISIONS_API_KEY'`=0；`execserver/server.go:729 HTTPHeader` 无 `value_env_var` ⇒ denylist 无落点（仅默认 `*KEY*` 通配在 `ignore_default_excludes=false` 时覆盖）  **[队长核 · 第 84–85 轮]** |
| #50189 | `a20fe6335f` | OAuth 例外 Figma→Mercado Pago | ⬜ | `rg 'mercadopago\|robinhood'`=0；Go 已部分移植 issuer_binding，但**无授权/令牌端点 origin 绑定校验**。落点 `mcp/oauth_discovery.go` |
| #50200 | `a75987455a` | `codex doctor` 报告配置的 TUI 模式 | ⬜ | `doctor/doctor.go:1013-1024` 无该明细；`rg 'configured TUI mode'`=0。落点 `doctor/doctor.go` |
| #50437 | `12a30d4e6d` | `codex sandbox uninstall`（旧版 Windows 沙箱） | ⬜ | `rg 'uninstall'`（sandbox/ 与 cli/）=0；sandbox CLI 只解析 setup。落点 `cli/cli.go` |
| #50465 | `ef8cfe5e96` | 重试注册表鉴权故障 + 抖动重连 | ✅ 已落地（队长核） — 9b6ac1ab | 抖动半边已在 Go（`execserver/remote.go:121 nextDelay`，来自更晚的 `#51502`）；**鉴权故障重试缺失**：`remote.go:315` 只重试 503；`rg 'authentication_service_unavailable'`=0。落点 `execserver/remote.go`  **[队长核 · 第 84–85 轮]** |
| #50525 | `b65ab465ce` | strict 校验拒绝未知 TUI 键 | ✅ 已落地（队长核） — 42fbf7f3 | `validateKnownTopLevelConfigFields`（`config/config.go:801`）**无 `[tui]` 子键校验**；`rg 'unknown_tui_toml_value_path'`=0。落点 `config/config.go`  **[队长核 · 第 84–85 轮]** |
| #50803 | `8f82b8a31c` | 合资格 remote-control 启动改用托管 daemon | ⬜ | `cli/cli.go:496 RemoteControlOptions` 无 `NoDaemon`；裸 `remote-control` 恒走前台（`app/remote_control.go:37-38`）；`rg 'daemon_eligible'`=0 |
| #51350 | `e32365a2c6` | from-file 回放放宽 snapshot 体积上限 | ➖ N/A | 同 #49782：Go `execserver/` 无快照模块；`rg 'capture_too_large\|MAX_FILE_SNAPSHOT'`=0 |

**表尾：强 ✅ 1（48575）/ ✅(弱证据) 0 / ➖ 4（49028 / 49782 / 49818 / 51350）/ ⬜ 13。**

### 13.D C 分片 20 条 + A 分片 5 条

> `git log --grep '#<PR>' main`：仅 `#49102 → dd900377`、`#50209 → 52d5f5f1`、`#51333 → 8807c307`、`#51334 → 11be9802` 命中。

| #PR | 上游 SHA | 一句话语义 | 判定 | 决定性证据 |
|---|---|---|---|---|
| #49069 | `33a0f766a6` | 后台增量 vacuum 回收 SQLite 日志库空闲页 + 回收遥测 | ⬜ 部分落地（阶段 A/B 已并入 sync543 `88aa753e` + sync544 `116c4376`；阶段 C 指标 + 打断续跑回归未落） | `rg 'reclaim\|incremental_vacuum\|codex.sqlite.reclamation'`=0；`state/sqlite.go` 只有 #49102 的 auto_vacuum  **[队长核 · 第 84–85 轮]** |
| #49076 | `011f803f3c` | skill analytics 只取 git origin URL（免多余 git 命令） | ✅(弱证据) | `appserver/turn_runtime.go:10404 skillInvocationRepo` → `utils/gitinfo.go:22 CollectGitInfoFromDir` 纯读文件得 URL（**不跑 git 命令**）；⚠️ 无同名 helper |
| #49102 | `c2d2f422e6` | 保留既有 vacuum 模式 + 直接返回 pool 初始化错误 | ✅ | `state/sqlite.go:176/189/205-220`、`state/sqlite_settings_test.go:13`；`git log '#49102' → dd900377 sync519` |
| #49117 | `2e6cc4ed8d` | analytics 请求按线程 product SKU 归因 | ⬜ | `X-OpenAI-Product-Sku` 仅在 `mcp/config.go:1009`（非 analytics 客户端） |
| #49294 | `d79a95bdf8` | guardian review/classification 遥测带 context mode | ⬜ | `rg 'context_mode\|GuardianContextMode'`=0 |
| #49295 | `cf12c86dc5` | 配置指纹用 `sort_all_objects` 规范化（行为不变） | ✅(弱证据) | `config/api.go:3743 configVersion` 用 `json.Marshal`（递归排序键，天然规范化）；⚠️ 哈希为 FNV-1a（非 sha256），也可论证为 ➖ |
| #49305 | `c2837d8ece` | 批量读线程元数据解析线程名（999 分块） | ➖ N/A | Go 线程名解析走单次索引文件读 `rollout/session_index.go:102 FindThreadNamesByIDs`；无逐线程 SQLite 元数据查询 ⇒ 无对偶 |
| #49425 | `8ea2c0e0d4` | 后台每 30 分钟按龄+库大小定期裁剪日志 | ✅ 已落地（队长核） — 65f28ce0 | `state/logs.go:234 runLogsStartupMaintenance` 仅启动期；`rg 'time.NewTicker' state/`=0  **[队长核 · 第 84–85 轮]** |
| #49441 | `6ba4bf9e64` | 重试/回退遵守 server retry advice（含 WS→HTTP 前等截止） | ⬜ | `model/responses_agent.go:799-800/1038-1039/1083/1101` `disableWebsockets(); return r.Run(...)` **无 `Retry-After` 截止等待** |
| #49517 | `d42056091a` | 命令中心 fork 快捷键 `agents.fork` | ⬜ | `tui/keymap.go:156-166` 无 `fork`；`rg 'agents.fork'`=0 |
| #49675 | `ed0cc1a4ab` | 路由字段（model/stream/service_tier）排在大 input 前 | ⬜ | `model/responses_agent.go:294-312` 字段序 Input 在 Stream 前；WS payload 为 map（字母序） |
| #49781 | `8953de1f1a` | MCP sandbox 元数据带 `useMxc` | ⬜ | `mcp/sandbox_state.go:7-12` 无 `useMxc` |
| #49874 | `bdfbab9f4f` | usage/credit 链接改 `chatgpt.com/settings/usage` | ⬜ | Go 仍为旧 `chatgpt.com/codex/settings/usage`：`model/usage_limit.go:170,179`、`app/backend_banners.go:31`、`tui/state.go:391` |
| #50209 | `4dd51f4a5f` | transcript 鼠标滚动速度可配置 `tui.mouse_scroll_speed` | ✅ | `app/interactive.go:1932`、`app/mouse_scroll_wiring_test.go:20,27`、`tui/tea/mouse_scroll_like_rust_test.go:43`；`git log '#50209' → 52d5f5f1 sync506` |
| #50454 | `3c3a990da0` | rollout 持久化体积缩减指标 | ⬜ | `rg 'rollout.persistence\|bytes_removed\|turn_bytes'`=0 |
| #50786 | `acf9818fae` | 命令中心分组跨启动记忆（`tui.agents_overview_grouping`） | ⬜ | `tui/agents_overview/overview.go:836 ToggleGrouping` 仅改内存；`config/` 无该键 |
| #51156 | `c9253c4977` | base instructions 移入 input 的 developer 消息（standard Responses） | ⬜ | `model/responses_agent.go:758/982/1441 Instructions:` 仍顶层；仅 **lite** 路径置空并构 developer item ⇒ Go 处于 PR **之前** |
| #51206 | `c19525e55e` | 恢复的 spawned subagent 记录初始化分析（mode=resumed） | ⬜ | `appserver/thread_analytics.go:20/27/35` 仅 thread/start\|resume\|fork 发事件；SpawnAgent/ResumeAgent 不发 |
| #51230 | `80e0b51c9e` | 会话查找分页稳定 + 报告列表失败 | ⬜ | Go 列表为文件偏移游标（`rollout/rollout.go ListThreads`），无 CreatedAt+ThreadID DB-only 游标；`rg 'Could not load more sessions'`=0 |
| #51333 | `5ddd19e8a9` | turn analytics 记录 multi-agent version | ✅ | `telemetry/turn_event.go:130,218`、`telemetry/turn_event_multi_agent_version_test.go:9`、`appserver/turn_analytics_multi_agent_version_test.go:14`；`git log '#51333' → 8807c307 sync518` |
| #49058 | `df3e439c02` | Windows sandbox 长路径 ACL 修复（句柄 SetSecurityInfo） | ⬜ | `sandbox/windowssandbox/acl_windows.go:178 fileDACL` 用 `GetNamedSecurityInfo(path,…)`（原始路径、非句柄）；`rg '\\?\\\|MAX_PATH'`=0 |
| #49325 | `26dd19ef47` | Windows runner logon 遇 1056 用同凭据重试一次 | ✅ 已落地（队长核） — fe88a5a8 | `sandbox/windowssandbox/elevated/runner_client.go:163` 重试集仅 1326/1312（刷新凭据）；`rg '1056'`=0，无「同凭据重试」分支  **[队长核 · 第 84–85 轮]** |
| #49812 | `0f8df3d214` | 影子技能排序移出 turn 准备路径（后台限流 2） | ⬜ | `appserver/turn_runtime.go:9108 r.runSkillShadowSelection(...)` **同步内联**；`skill_shadow_selection.go:59` 无后台 worker/并发上限 2 |
| #51330 | `f5fa209bb0` | Guardian 决策总时长指标 `codex.guardian.decision.duration_ms` | ✅ 已落地（队长核） — af855f66 | `telemetry/metric_names.go:39-42,90` 无 `decision.duration_ms`；`rg 'GuardianDecision'`=0  **[队长核 · 第 84–85 轮]** |
| #51334 | `79cae5f7fb` | Guardian denial-limit 中断计数 | ✅ | `telemetry/metric_names.go:90`、`appserver/guardian_reviewer.go:776-779`、`appserver/guardian_denial_limit_metric_test.go:10`；`git log '#51334' → 11be9802 sync511` |

**表尾：强 ✅ 4（49102 / 50209 / 51333 / 51334）/ ✅(弱证据) 2（49076 / 49295）/ ➖ 1（49305）/ ⬜ 18。**

### 13.E D 分片 app-server（24 条）

> 24 条 PR 号在 Go 提交/源码（除 `update/`）中全部 0 命中；改动均落在 Go **确有对应包**的机制内（skills/systemskills、uds/appserverdaemon、plugin、state、model、guardian、remotecontrol、config），故 **0 条 ➖**。

| #PR | 上游 SHA | 一句话语义 | 判定 | 决定性证据 |
|---|---|---|---|---|
| #48604 | `9db8162d65` | 删除内置 `plugin-creator` skill | ✅ 已落地（队长核） — 34b5faea | Go **仍内嵌**：`systemskills/assets/samples/plugin-creator/SKILL.md` 存在；`systemskills/systemskills_test.go:19`、`appserver/skills_test.go:452` 断言含它 ⇒ 与 upstream **反向**  **[队长核 · 第 84–85 轮]** |
| #48772 | `fdbce2080c` | 长符号链接路径下的 unix socket 连接（canonicalize 重试） | ⬜ | `appserverdaemon/client.go:449 DialContext(…,"unix",dialPath)`；`socket_peer_other.go:9-11 prepareSocketDialPath` 原样返回；无 `InvalidInput`/canonicalize 兜底 |
| #48828 | `81d5405882` | 允许「首轮之前」归档线程（归档前先 persist） | ⬜ | `appserver/router.go:2326` 对未 materialize 线程**直接报错** `"no rollout found for thread id …"`；`runtime_router_test.go:908` 断言该报错 ⇒ **与 PR 相反（强反证）** |
| #49032 | `46fdd5ef39` | 避免建连/stderr span 日志导致的 SQLite 卡顿 | ✅(弱证据) | SQLite 半已落地：`state/sqlite.go:189 initializeDatabaseSettings`（`git log -S → dd900377 / #49102`）；stderr span 半：Go 用 `log/slog`、无 tracing span（`FmtSpan`=0）⇒ 机制性 N/A。⚠️ 归因 PR 为 #49102 |
| #49099 | `1260716393` | 跨插件工作流缓存已解析 manifest | ⬜ | `plugin/manifest.go:118 loadPluginManifest` 每次重读；`rg 'ManifestCache'`=0 |
| #49119 | `8bd5a136ff` | content-filter 重试附加恢复指引 + 独立错误 | ⬜ | `model/responses_stream.go:1255` 仅通用 `"Incomplete response returned…"`；`rg 'content_filter\|ContentFilterGuidance'`=0 |
| #49135 | `458f7046a5` | 显式 provider 模型目录权威化 + slug 校验 | ⬜ | `model/provider.go:306` 标志仅来自 `authHasChatGPTAccount`，与 `model_catalog_url` 无关；`rg 'with_provider_catalog'`=0 |
| #49260 | `af0d68a236` | 收敛企业 MCP 授权 + 刷新失败 fail-closed | ⬜ | 仅 `plugin/mcp_contributions.go:104`（注释引更早的 #44832）；`rg 'disable_mcp_enterprise_auth\|refresh_mcp'`=0 |
| #49269 | `0b1b78a4f1` | 重载时保留线程 override 与云策略有效性 | ⬜ | `rg 'CloudConfigBundlePolicy\|StagedCloudConfigBundleCache\|commit_if_current'`=0 |
| #49339 | `a6e9eaa9bd` | Bedrock 目录加 GPT-6.1 Sol 并设为默认 | ✅ 已落地（队长核） — e721afed | `model/catalog.go:1026 AmazonBedrockModelCatalog()` 无 gpt-6.1-sol；`model/provider_info.go:45-52` 无该 ID  **[队长核 · 第 84–85 轮]** |
| #49432 | `d8f69ea8bc` | 鉴权变更时保留 bootstrap discovery（撤销旧账号访问） | ⬜ | `appserver/application_network.go:61`（引 #47411）有相邻面，但 `invalidateApplicationNetworkPolicy` **0 调用点**；`runtime_router.go:872 noteAuthChanged` 未撤销网络策略 |
| #49600 | `92bc601ad6` | resume 时复用未变的历史快照 | ⬜ | `rg 'historyRevision\|HistorySnapshot\|reuse.*snapshot'`=0 |
| #49714 | `f151a0f5c2` | API-key cyber access program 与模型发现解耦 | ⬜ | `model/access_programs.go:28 AccessProgramsForAuth` 仍**要求 ChatGPT 账号**；`features/features.go:323 api_key_cyber_access_programs` 无消费者 |
| #49785 | `1f77c0cfa6` | 命名时持久化空的 paginated 线程 | ⬜ | `appserver/router.go:2610 handleThreadSetName` 无 persist 步骤；`router_test.go:918 TestRouterThreadSetNameKeepsEmptyThreadUnmaterialized`、`runtime_router_test.go:670` 断言命名**不 materialize** ⇒ **与 PR 相反（强反证）** |
| #49793 | `726f1492db` | Guardian v2 异步分类增加 conversation 模式 | ⬜ | `model/catalog.go:154 GuardianV2ModelConfig` 无 `async_classifier_mode`；`rg 'AsyncClassifierMode'`=0 |
| #49795 | `47a8bd7321` | 避免分类器续写里重复同步 reviews | ⬜ | `rg 'PreviousReviews\|retain_new_reviews'`=0；`state/guardian.go:619` 注释自陈 Go 未建模 |
| #49846 | `408f48ce0c` | 每轮捕获 host 提供的 extension data | ⬜ | `rg 'WithTurnExtensionData\|current_turn_extension_data\|ExtensionData'`=0 |
| #49855 | `bc197b77bc` | 提权 Windows TUI 走 embedded 模式 | ⬜ | 仅有拒绝路径 `appserverdaemon/elevation.go:10 EnsureNonElevated`；`"elevated_windows"` 作为 selection reason `rg`=0 |
| #49993 | `57ac6f5163` | 保留异步 Guardian 历史前缀（retained 段移到 transcript 之后） | ⬜ | `state/guardian.go:635-660 BuildPromptWithOptions` 仍把 retained 段渲染在 transcript **之前**，无 sync/async 分支 |
| #50348 | `9d2b60303e` | 远端控制重连带抖动退避 | ✅ 已落地（队长核） — bbf423f3 | `remotecontrol/websocket_connect.go:73` 初始 200ms/抖动 0.9–1.1/cap 30s（引更早的 #49330）；本 PR 的 initial 5s/jitter 0.5–1.0/60s 重置均无  **[队长核 · 第 84–85 轮]** |
| #50472 | `604061ce51` | Bedrock Astra 启用 Ultrafast service tier | ✅ 已落地（Mantle 半 sync542 `d4922ab5`；Runtime 变体 + 自定义目录归一 = N/A，Go 无对应入口） | `model/catalog.go:1046 normalizeBedrockCatalog` 对每模型 `ServiceTiers=nil`（=本 PR 要改的旧行为）  **[队长核 · 第 84–85 轮]** |
| #50803 | `8f82b8a31c` | 合格的 remote-control 启动走托管 daemon | ⬜ | `app/remote_control.go:33 runRemoteControl` 空子命令**恒定**前台；无 `--no-daemon`（`cli/cli.go:3177 parseRemoteControl`） |
| #51396 | `57d57df608` | 迟到的低风险分数完成 pending Guardian review | ⬜ | `appserver/guardian_reviewer.go:99-104 guardianScoreProgress` 仅 `latestToolCall/latestScoredToolCall`；`rg 'completePending\|pendingReview'`=0 |
| #51400 | `a4ebc509f4` | 阻止后续分数释放更早的 pending review | ⬜ | `rg 'scoreIndex\|wrapperLag\|pendingScore'`=0 |

**表尾：强 ✅ 0 / ✅(弱证据) 1（49032）/ ➖ 0 / ⬜ 23。**
**⚠️ 台账口径提示**：`#48828`、`#49785` 是「Go 明确实现为**相反**行为」的强反证——单靠标识符 grep 会漏（必须读代码/测试断言）。
**⚠️ #49032 口径**：它属「半落地且归因到 #49102」；若口径要求「整 PR 行为齐备才算 ✅」，可降格为 ⬜。

### 13.F 三条最能支撑「假阴性」判定的命令（原文）

```bash
# ① 最强硬锚点之一：#48575（B 分片）——从 Rust 测试名 grep 会漏，靠源码注释 + git log -S 才现形
cd /home/jacks/jacks_dev/codex_go
rg -n '#48575' execserver/ appserver/ ; git log --format='%h %s' --grep '#48575' main
#   输出：execserver/remote_harness.go:34/52/276/309 命中「Rust #48575」
#         f048c5ed sync516: give provisioned executors a longer initial connect window (#48575)

# ② 纯标识符假阴性：#50105 / #50112（E 分片）——Rust 测试名在 Go 必为 0 命中
rg -n 'ComposerFooterModeQuitShortcutReminder|LoadingGlyph' tui/ ; git log --format='%h %s' --grep '#50112' main | head -1
#   输出：tui/bottom_pane/chat_composer/footer_state.go:18 ComposerFooterModeQuitShortcutReminder
#         tui/loading_glyph_like_rust_test.go:8「Rust #50112」
#         ba951807 sync524 … (#50112)

# ③ 反向强反证：#48828 / #49785（D 分片）——Go 测试显式断言「与 PR 相反」的行为
rg -n 'no rollout found for thread id' appserver/router.go ; rg -n 'KeepsEmptyThreadUnmaterialized' appserver/*_test.go
#   输出：appserver/router.go:1343/1973/2118「no rollout found for thread id …」
#         appserver/router_test.go:918 func TestRouterThreadSetNameKeepsEmptyThreadUnmaterialized
```

### 13.G 落盘与边界

- 本节为**只追加**（未改 §0–§12 任一既有行）；参考点 Rust `5a3140176e`；main 复核窗口 `15c54dfa`→`edcfd073`。
- 本轮**未 commit / 未 push / 未改 main**；§13 由队长合并。
- 剩余 111 条 ⬜ 的**唯一权威口径**仍是「派单前以当时 main 复跑 §10.C 重扫」；本节的 `✅`/`➖` 是下界，`⬜` 是上界。
- 队长点名更正：`#51333` 复核 = ✅（`8807c307 sync518`）；`#51331` **不在 §9.D**，其真实提交为 `6b989619 sync522`（队长来信写 sync517，属口径出入）。

> **计数口径注**：§13 的 141「行」源自 §9.D 的 141 条分片槽位，其中 **`#49855`、`#50803` 各有一条跨分片重复**（同时落在 B 分片与 D 分片）⇒ **去重后唯一上游 PR = 139 条**。判定逐行给出（重复行判定一致：两条均为 ⬜），不影响三类计数（强 ✅12 / ✅(弱)9 / ➖9 / ⬜111）。

### 13.H 每条 ✅ 的「不依赖行号」锚点（回应队长方法学提醒）

> `file:line` 会随 main 漂移；下表对 **21 条 ✅（强 12 + 弱 9）** 各给一个**行号无关的锚点**（函数/常量/类型/测试名，或 `git log -S` 落点提交）。复核时以锚点为准。

| #PR | 档 | 行号无关锚点（符号 / 测试名） | 落点提交（`git log --grep`） |
|---|---|---|---|
| #48757 | 强 ✅ | `tui/summary_shimmer.go` 注释 `Rust #48757`（shimmer 起止延迟 const） | `e81682ce sync514` |
| #48776 | 强 ✅ | 测试 `TestCurrentTaskRowOmitsCurrentBadgeLikeRust`（`tui/agents_overview/current_badge_like_rust_test.go`） | `3426c4d9 sync510` |
| #49031 | 强 ✅ | `tui/onboarding/auth_flow.go` 注释 `Rust #49031`；测试 `TestChatGPTSuccessMessageCopyLikeRust` | `9951066a sync520` |
| #49357 | 强 ✅ | `tui/tea/composer_paste.go` 注释 `Rust #49357`；测试 `TestComposerBlockquotePasteContinuesCurrentLinePrefixLikeRust` | `98d4c7cb sync509` |
| #50105 | 强 ✅ | 类型常量 `ComposerFooterModeQuitShortcutReminder`（`tui/bottom_pane/chat_composer/footer_state.go`）；测试 `TestFooterModeTransitionsMatchRust` | （符号锚点，无 PR 号提交） |
| #50112 | 强 ✅ | 常量 `LoadingGlyphFrameDuration` + 函数 `LoadingGlyph`（`tui/motion.go`）；测试 `TestLoadingGlyphLikeRust` | `ba951807 sync524` |
| #50477 | 强 ✅ | `tui/workspace_command.go` 注释 `Rust #50477`；测试 `TestWorkspaceCommandUsesHostDefaultOutputCapLikeRust` | `6866d3ed sync508` |
| #48575 | 强 ✅ | 常量 `provisionedEnvironmentConnectTimeout` + 函数 `ProvisionedEnvironmentConnectTimeout`（`execserver/remote_harness.go`）；测试 `TestProvisionedEnvironmentConnectWindowLikeRust` | `f048c5ed sync516` |
| #49102 | 强 ✅ | 函数 `initializeDatabaseSettings`（`state/sqlite.go`）；测试 `TestOpenReadWritePoolInitializesFreshDatabaseSettingsLikeRust` | `dd900377 sync519` |
| #50209 | 强 ✅ | 函数 `interactiveMouseScrollSpeed`（`app/interactive.go`）；测试 `TestInteractiveMouseScrollSpeedLikeRust` / `TestTranscriptWheelUsesConfiguredMouseScrollSpeedLikeRust` | `52d5f5f1 sync506` |
| #51333 | 强 ✅ | 测试 `TestCodexTurnEventMultiAgentVersionLikeRust`（`telemetry/`）+ `TestRuntimeRouterTurnEventReportsMultiAgentVersionLikeRust`（`appserver/`） | `8807c307 sync518` |
| #51334 | 强 ✅ | 常量 `GuardianDenialLimitReachedMetric`（`telemetry/metric_names.go`）；测试 `TestGuardianDenialLimitReachedMetricLikeRust` | `11be9802 sync511` |
| #48549 | ✅(弱) | `parity/rust_tui_snapshot_manifest_test.go` 快照清单条目命名上游 sha `75a714843b`（**无生产符号**：Go 无拖选复制面） | （清单锚点） |
| #48628 | ✅(弱) | 函数 `setAgentsOverviewBlankSession`（`tui/tea/agents_overview.go`） | （符号锚点） |
| #48761 | ✅(弱) | `tui/exec_cell/render.go` 注释 `Rust #46492`「three-row preview with a hidden-line count」（**归因相邻 PR**） | （符号锚点） |
| #49037 | ✅(弱) | 变量 `FooterModeCycleHint` / `CollaborationModeLabel(…, ShowCycleHint)`（`tui/bottom_pane/footer.go`，注释归 `Rust #49804`） | （符号锚点） |
| #49153 | ✅(弱) | 函数 `blockquoteTargetText`（`tui/chatwidget/copy_target.go`，注释引 Rust `CopyTarget::Quote`） | （符号锚点） |
| #49816 | ✅(弱) | 行为面：`app/app_link_wiring.go` 打开成功返回 nil、失败才 `StatusMsg`；`tui/tea/plugins.go` 仅 Err 加 error（**无符号，无 `git -S` 锚点**） | （无） |
| #49076 | ✅(弱) | 函数 `CollectGitInfoFromDir`（`utils/gitinfo.go`）+ 调用方 `skillInvocationRepo`（`appserver/turn_runtime.go`） | （符号锚点） |
| #49295 | ✅(弱) | 函数 `configVersion`（`config/api.go`，FNV-1a over `json.Marshal`） | （符号锚点） |
| #49032 | ✅(弱) | 函数 `initializeDatabaseSettings`（`state/sqlite.go`，同 #49102） | `dd900377 sync519`（归因 #49102） |

> 复核者请以本表锚点定位（`rg -n -F '<符号>'`）；行号仅作辅助。

### 13.I 关于 §10.C 脚本的 zsh 陷阱核查

- 已核：§10.C 的脚本用的是 `done < <(awk …)` + `while read pr`，**不触发** zsh 的「`for p in $VAR` 不做词分割」陷阱。
- 全库扫描 `rg -n 'for [a-z_]+ in \$[A-Z_]+' update/*.md` = **0 命中**（唯一记录该陷阱的是 `update/plan_2026_10_07.md:2456` 的说明文字）⇒ 无需修正。

### 13.J §9.A 的 23 条 ✅ 的行号无关锚点（Task B；只追加，不改 §9.A 原文）

> 回应队长「§9.A 的 `file:line` 会随 main 漂移」的提醒：对 §9.A 的 23 条 ✅ 各给一个**不依赖行号**的锚点（符号/类型/测试名 + `git log -S` 落点提交）。**§9.A 原文一字未改**。
> 独立核验：23 条上游 SHA 已在 `/home/jacks/jacks_dev/codex` 逐个 `git log --oneline -1 <sha>` 验证，subject 与 PR 一致（本 Agent 自跑，非转述）。

| #PR | 上游 SHA | 一句话语义 | 行号无关锚点（符号/类型/测试名） | 落点提交（`git log -S` 首现 / Go 提交） |
|---|---|---|---|---|
| #49261 | `f35a0fdc5d` | 保留 Windows sandbox runner 启动错误码 | `sandbox/windowssandbox/elevated/runner_client_windows.go` 的 `RunnerLogonError` / `runnerErrorModeFlag`（`SetErrorMode` 前先取 `createProcessWithLogon` err）；测试用例名含 `RunnerLogonError` | 符号随初始导入 `1d2759c3`（2026-07-07）；`-S previousErrorMode` → `eeacecb8`（2026-07-18） |
| #49308 | `50d9c5deac` | piped/常规 legacy sandbox 进程都不开控制台 | `sandbox/windowssandbox/process_windows.go` 两条 `CreateProcessAsUser` 路径的 `windows.CREATE_NO_WINDOW`；旁证 `envutil.SuppressConsoleWindow` | `4291e281`「windows: suppress the console windows … (#48483)」（引号不同，见注 3） |
| #49702 | `3b16b5a5b0` | 统一 exec-server file-handle 术语 + 句柄上限 | `execserver.maxOpenFileReads`(=128) / `maxFileReadHandleIDBytes`(=32) / `validateFileReadHandleID` / `(*Server).openFile`；测试 `TestSessionRegistryIsolatesFileHandlesLikeRust` | `d55dd3a7`「add」（2026-07-12） |
| #49778 | `875bf9209b` | exec-server 流式写协议（`fs/writeBlock`/`fs/open` modes/`FileWriteStreaming`） | `execserver.MethodFSWriteBlock` / `FileWriteStreaming` / `fsOpenModeRead` / `fsOpenModeReplace`；测试 `TestFileWriteStreamingLikeRust` | `1da0383b`「sync352 (#50177)」（引 #50177，见注 3） |
| #49811 | `c51f5bfb82` | 处理不支持/占位的 `fs/writeBlock` | `execserver.(*Server).writeBlock` + `server.go` 分派 `case MethodFSWriteBlock`；测试 `TestFileWriteStreamingLikeRust` | `1da0383b`「sync352 (#50177)」 |
| #49939 | `6b4daafdb4` | 每 turn 的 Cyber access program 选择 | `turn.CyberAccessProgram`（+`CoreValue`）/ `turn.TurnStartParams.CyberAccessProgram` / `model.CyberAccessProgram`；测试 `TestCyberAccessProgramCoreValueLikeRust` | `ad257e72`「sync235 (#44893, #48224)」 |
| #48779 | `21eb35513d` | 父压缩后保留独立 Guardian 历史 | `appserver.(*guardianSessionRunner).ResetAfterParentCompaction` / `features` key `guardian_reuse_parent_compaction` / `retainedctx` 包 | `9fb5d629`（2026-08-10，Guardian compaction reuse 批次） |
| #49280 | `18194bfd35` | capability roots 限定到已捕获 turn 环境选择 | `appserver.restrictCapabilityRootsToSelections`；测试 `TestRestrictCapabilityRootsToSelectionsLikeRust` | `79a13c57`「sync396 (#51493)」（见注 3） |
| #49642 | `67727e7cf1` | managed requirements 可禁用 Windows MXC sandbox | `config.WindowsAllowMXCFromValues` / `WindowsSandboxModeMxc`；测试 `TestWindowsMXCOptOutRejectsExplicitMXCConfigLikeRust` | `4481ffd8`「sync389 (#51547)」（见注 3） |
| #49796 | `5ca55db09c` | 去重 Guardian retained-context 省略通知 | `state.DeduplicateRetainedInstructions`（`state/guardian_retained_context.go`）；测试 `TestDeduplicateRetainedInstructionsUsesTranscriptSourceProof` | `d1088aa3`「sync270」（2026-09-27） |
| #49806 | `a5d56d8120` | app-server 协议接受未知 Codex 错误变体 | `appserver.CodexErrorInfo`（`type CodexErrorInfo any`）；旁证 `rollout.normalizeCodexErrorInfoClassification`；测试 `TestRecordFromPathToleratesUnknownCodexErrorClassifications` | `eeacecb8`「change dirs」（2026-07-18） |
| #49880 | `1f52d40704` | 权限授权绑定到发起 turn | `state.(*TurnState).RecordGrantedPermissions`；测试 `TestTurnStatePermissionsAndCounters` | `eeacecb8`「change dirs」（2026-07-18） |
| #50531 | `7d5f55bdad` | 关闭前持久化 realtime transcript tail | `realtime.FlushTranscriptTailOnEnd` / `(*realtimeTransportSession).takeTranscriptTail` / `(*Manager).flushRealtimeTranscriptTail`；测试 `TestNormalTransportCloseFlushesTranscriptTailBeforeClosed` | `02b7c37b`（2026-08-01） |
| #51221 | `2dbcab90e2` | 分离 environment requests 与 runtime selections（absent vs 显式空） | `mcp.TurnEnvironmentSelection`（+`Ready/Pending/Failed`）/ `appserver.turnEnvironmentSelections`（`runtime_router.go`）；测试 `TestTurnEnvironmentSelectionsDistinguishAbsentFromExplicitlyEmptyLikeRust` | `TurnEnvironmentSelection` → `18a9c908`「sync401 (#51503)」；`turnEnvironmentSelections` → `a388b9be`「sync30 (#42147)」 |
| #49144 | `ff3c82c8a9` | 保留 server reasoning summary/verbosity 设置 | `app.launchReasoningOverrides` / `launchSettingForKey`（键 `model_reasoning_summary`/`model_verbosity`）；测试 `TestInteractiveLaunchReasoningOverridesLikeRust` | `8cc01f75`「sync507 (#50811)」（同源路径，见注 2） |
| #49472 | `b588812e8c` | TUI 使用 server 权威权限（命名 profile） | `tui/tea.(*Model).selectServerPermissionProfile` / `remoteNamedPermissionProfileActive` / `ErrNamedPermissionProfilesUnsupported` | `f5472801`「sync31 (#43340)」（2026-09-12） |
| #49857 | `f58ed54a9d` | 用 model catalog 驱动 TUI cyber refusal 文案 | `tui/history_cell.NewCyberPolicyErrorEvent` / `tui.DaybreakNotice`；测试 `TestCyberPolicyCopyFollowsDaybreakNotice` | 首现 `92515576`「rich tui functions」（2026-07-09） |
| #50140 | `cb6da58876` | 用 server permission catalog 驱动 TUI 权限快捷键 | `tui/tea.(*Model).showPermissionsMenu` / `openPermissionsMenu` / `permissionsRetryOptionID` | `45e288ba`「sync31 (#43340)」（2026-09-12） |
| #50396 | `d61c7a824f` | 分页键遵循 pager 绑定 | `tui.keymapAction("pager", …, "half_page_up"/"half_page_down")` / `tui/chatwidget.PagerHalfPageUp`/`PagerHalfPageDown`；测试 `TestTranscriptOverlayPagerActionsPreserveAndFollowBottom` | 首现 `92515576`（2026-07-09） |
| #50431 | `b6903c0669` | 保留 agents overview 预览里的终端超链接 | `tui/markdown.webFileLink` / `annotateWebLinkLabels` / `osc8FileLink`；测试 `TestRenderWithThemeMarksLinkLabel` | `f808ceca`「tui: mark web link labels …」（2026-08-22） |
| #50503 | `9ce35d337a` | Enter 接受、Escape 取消 transcript Find | `tui/bottom_pane/chat_composer.(*HistorySearchSession).Accept`/`Cancel`/`FooterLine`（文案 `"enter accept | esc cancel"`）；测试 `TestHistorySearchSessionCancelAcceptNoMatchAndQueryEditingMatchRustCore` | 首现 `92515576`（2026-07-09） |
| #50505 | `47379efd52` | 删除任务后保持 Command Center 选中相邻行 | `tui/agents_overview.(*View).PrepareRemoval`；测试 `TestPrepareRemovalSurvivesBatchedAndDuplicateRemovalsLikeRust` | `5df0c760`「sync499 (#50505)」 |
| #50811 | `afb436df8b` | 新 TUI 线程尊重 server reasoning summary 默认 | `app.interactiveLaunchReasoningOverrides` / `launchReasoningOverrides`；测试 `TestInteractiveLaunchReasoningOverridesLikeRust` | `8cc01f75`「sync507 (#50811)」/ `2c0decdb`「sync500 (#50811)」 |

**注：**
1. **8 条无本 PR 号提交**（`git log --grep '#<PR>'`=0，靠 `git log -S` 定位）：`#49261 #49702 #49806 #49880 #50396 #50503 #49857 #49472`（另 `#48779` 为相关批次提交）。
2. `#49144`/`#50811` 落在同一段 Go 代码（`launchReasoningOverrides` 家族）；语义互补（#49144=保留显式设置，#50811=尊重 server 默认）。**`#49144` 的 §9.A 行号 `app/interactive.go:1957` 已漂移**（队长实测 `:2027`）——即本节存在的原因。
3. **落点提交的 PR 号与台账 PR 号不一致**（上游把多条改动合并落地）：`#49308`←`4291e281`(引 #48483)、`#49778`/`#49811`←`1da0383b`(引 #50177)、`#49280`←`79a13c57`(引 #51493)、`#49642`←`4481ffd8`(引 #51547)、`#51221`←`18a9c908`(引 #51503)。这不影响 ✅ 判定（行为已落地），但**复核者不要用这些 PR 号去 `git log --grep`**。
4. **§9.A 一处路径笔误**：`#49261` 写作 `elevated/runner_client_windows.go`，真实路径为 `sandbox/windowssandbox/elevated/runner_client_windows.go`（不改原文，仅在此标注）。
5. `#49702` 的「128 上限」在 Go 有两个语义：`maxOpenFileReads`=128（每连接句柄数）与 `maxFileReadHandleIDBytes`=32（句柄 ID 长度），已分开标注。

> 复核方式：以本表 `符号` 定位（`rg -n -F '<符号>' --glob '*.go' .`），行号仅作辅助。

---

## §14 队长复核更新（第 84–85 轮落地回流，2026-10-07）

**范围**：`c30b56ca..main` 期间本队并入的 sync499–sync544 落地，回填到本台账的状态列。
**方法（可复跑）**：
```bash
cd /home/jacks/jacks_dev/codex_go
git log --format='%h %s' c30b56ca..main | grep -oE '\(#[0-9]+' | tr -d '(#' | sort -u    # 38 个 PR
# 对每行 `| #<PR> |` 且状态列为 ⬜ 的行：改为 ✅ 已落地（队长核）+ 落点 commit
```
**结果**：**48 行**状态更新（31 个 PR）；`⬜` 出现次数 437 → **391**；`✅` 290 → **336**；`git diff --numstat` = 48/48（等量替换，表格列数不变，已校验 1165 行无列数漂移）。

**部分落地（显式标注，不算满 ✅）**
- `#49069` → `⬜ 部分落地`：阶段 A/B 已并入 sync543 `88aa753e` + sync544 `116c4376`；**阶段 C**（`codex.sqlite.reclamation.count/.duration_ms/.pages` 指标 + 打断续跑回归）未落。
- `#50472` → `✅ 已落地（Mantle 半 sync542 `d4922ab5`）`；Runtime 变体 + 自定义目录归一 = **N/A**（Go 无对应入口，见 plan 第 85 轮）。
- `#49325` → ✅（核心已落 sync532 `fe88a5a8`；上游测试半边不可移植，见 `d8160a45`）。
- `#50525` → ✅（strict 校验核心 sync533 `42fbf7f3`）。

**同步登记的新缺口（不在 499 窗口内，另立项）**
- Bedrock **Runtime 专属模型目录**缺失（`rg 'RuntimeModelCatalog|runtime_catalog'` 生产 0 命中；`rg '"global\.|"us\.' model/` = 0）。
- `model/provider.go:352/356/360` preferred-model 漂移（Rust #38470 `d5e256ceb2`）。
- 新窗口（`5a3140176e` 之后）2 项**均已落地**：`#51595` → `497bbe43` sync551（`excludedThreadIds`）、`#51602` → `891a49ba` sync552（listing 失败 vs history 耗尽：显式空 cwd / db-only -32603 / cursor 不复现）。
- ⚠️ `#51595` 触及 vendored `app-server-exports-*.json.zst`：参照点 `5a3140176e` **不含**该 commit，两 .zst 现仍与上游一致（parity 绿）；**Rust pin 一旦推过 `1fbe15c962` 必须重新 vendor 这两个文件**，否则 `TestPrecomputedAppServerExportsMatchRustTarget` 变红。

**计数口径提醒**：本台账的 `⬜` 行数是「表行数」；§13 的 111 是「去重后未落地 PR 数」。两者不可直接互换；以本节更新后的表行数为准，PR 去重数请按 §13 口径重算。

---

## §15 队长下一批派单预备（落点已钉，2026-10-07 第 85 轮）

**本轮账面校正**：`#49096` / `#49074` / `#49028` 由 ⬜ 改判 **➖ N/A**（三项均为「Go 不存在该构建/子命令面」，证据见各行状态列）。

**待派项（队长已做预侦察，落点已钉；均为非 TUI、非 Bedrock、非 thread/list 面，可与当前 5 条车道并行）**

| PR | 上游 sha | 上游规模 | 队长预侦察（Go 落点 / 判据） |
|---|---|---|---|
| `#48772` | `fdbce2080c` | 2 文件 +50/−1（`uds/src/lib.rs` 长 symlink 路径下的 Unix socket 连接） | Go **无 `uds/` 目录**；等价面 = `appserver/unix_socket.go`（+ `unix_socket_test.go`）。真缺口候选：需核对 Go 的 unix socket 路径解析是否同样受长 symlink 影响 |
| `#48819` | `456212ca21` | 6 文件 +140/−28（`otel/src/metrics/names.rs` 显式直方图桶 `context_log_buckets(max_exponent)` = `[f64; 511]`，15.0/17.0 两处） | Go 落点 `telemetry/`（对照既有 metric 常量与 `RecordDuration`/histogram 面）；`rg 'context_log_buckets'` 预期 = 0 |
| `#48983` | `c0d26949be` | 4 文件 +149/−1（thread-store `update_thread_metadata` +16，避免时间戳更新触发整表元数据重写） | Go 落点 = `appserver/thread_attachments_runtime.go` / `router.go` 的线程元数据更新路径 + `state/` 查询层；需确认 Go 是否已有等价「窄更新」 |
| `#49082` | `46d2585ea4` | 2 文件 +202/−5（turn.rs：Guardian `is_basic_session_source` 时**跳过 remote Git discovery** 以显示 diff 路径） | Go 落点 = turn diff 路径推导处（`rg 'CwdRelativeTurnDiffs|is_basic_session_source'`）；真缺口候选 |
| ~~`#49100`~~ | `bfdb157178` | 2 文件 +154/−6（core-plugins manager 复用 HTTP 连接池） | 已派 **`syncnext3`**（`agent-390a7b103148b523e72a2e61`，#49099 已并入 sync550，写集不再冲突） |

**派单优先级建议**：`#49082` → `#48983` → `#48772` → `#48819`（按「落点确定度 × 影响面」排序；`#49100` 随 `#49099` 之后）。

## 队长订正（第 85 轮续 5）

- **#49069 全闭环、无残留**：`parity2` 车道最后留下的阶段 C（`961ccc54`）与 main 的 sync545 **`03d4c28c` patch-id 完全相同**（两侧同为 `6d5c0a70c29eca1fcb72598a4edafcc68c201e0d`），`git diff --stat 961ccc54 03d4c28c` 为空 ⇒ 内容已逐字节在 main，**无待并入项**。（`parity2` / `agent-f98cad967c001d4db7877a1f` 已回收，其「请派下一批」的请求随之作废。）
- **#50525 已在 main**：`42fbf7f3` sync533，`config/config.go:801/809/858` 的 `knownTuiConfigFields` 为准。
- **#49262 三段齐全**：`46d9a9eb`(mailbox_preemption) + `195943ab`(sync553) + `5aedba53`(sync555，车道 `555c0f57` 的纯增量 111 行)。
- **appserver 基线以当前 main 为准**：`4fcf61c6` 实测恰 3 项 FAIL（见 runbook R5）；`bb2dedd1` 上的 4–5 项属历史，勿再引用。

## 第 85 轮续 6 派单（10 条车道，宽度上限 10）

- **已派新项**：`#51350`、`#49798` → `syncnext6`；`#50803`、`#49360` → `syncnext7`；`#51458`、`#50788` → `syncnext8`；`#50804`、`#51063` → `syncnext9`；`#51396`、`#51400` → `syncnext10`。
- **#49100 去重**：已在 `syncnext3`；已发 `message` 令 `syncnext2` 剔除该顶，并对 `syncnext2` 的 `#49099` 穿透加「开工前请示」（`plugin/` 与 syncnext3 写集相邻）。
- **写集相邻的请示约束**：`plugin/`（syncnext2↔syncnext3）、`tui/app/`（syncnext8↔syncnext4）、`appserver/` 线程元数据路径（syncnext9/syncnext10↔syncnext1）。
- **#51350/#49798 的 N/A 口径**：#49798 与既有 sync 里已落的 #49805（`EnvironmentInfo` OnceCell 语义）高度重叠，**必须先在 Go 侧做判据**，已落则给决定性证据判 N/A，禁止重复造。
