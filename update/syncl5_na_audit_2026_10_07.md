# syncl5 · N/A 裁决对抗性复核（adversarial re-audit）· round87

- 车道：**syncl5**（Linux 节点）；任务：队长 `msg-1791374929830421600-5012`
- 基线：Go **`185d03c933086ddd2b0a1f498fdcf7f9d2683d70`**（sync632）；Rust 对照打 **`b17c74cfd5ebb39fe70ffaff78de198120278636`**（对象只读；parity 检出 `C:\rw\codex-rs` 仍在 `5b0b253035`，**未碰**）
- 纪律：**只读** —— 0 补丁 / 0 commit / 0 push / 0 ref 移动；本报告是唯一产物
- 范围：从本车道 round86/87 worklist 与台账中取 **12 条**（优先「无载体/已等价」且此前只给 grep-0 弱证据的条目）

## 0. 复核判据（与派单一一对应）

| 原标签 | 本次必须给出的证据 |
|---|---|
| 已等价型 | Rust 原 hunk ↔ Go 载体 **逐点对照**（不是 grep 计数） |
| 无载体型 | 证明**机制**不存在：给出 Rust 依赖的**类型/函数**在 Go 的缺失（不是「某字符串不存在」） |
| 已取代型 | 取代者 sha + **覆盖面交集证明**（含祖先关系） |
| 纯测试型 | 证明改动全部落在 `#[cfg(test)]` / `mod tests`，无生产语义 |
| 需架构决策 | 证明 Go 侧**承载该行为的运行路径**不存在（stub/无消费者） |

**结论：12/12 `HOLD（原判成立）`，0 `REOPEN`。** 但其中 3 条的**标签需要细化**（`#51642` 从「已取代」改为「机制性 N/A」；`#50781` 从「需架构决策」改为「无载体型」；`#49032` 的 sqlite 半片证据从「窗口内覆盖」升级为「**后代提交** #49102 覆盖」）。详见下表与 §2。

## 1. 逐条裁决（一行一条）

| # | PR | Rust sha | 原标签 | 本次裁决 | 一行依据 |
|---|---|---|---|---|---|
| 1 | `#51642` | `a513012869` | 已取代 | **HOLD**（标签更正为**机制性 N/A**） | Rust #51642 是 `#51627` 的**直接子提交**（parent = `5b0b253035`），只补 `SectionContent` 类型迁移；Go 无 `SectionContent`/`TranscriptFormat`（0 命中），flat 模型的可观察行为已由 sync631 承载 |
| 2 | `#51651` | `30bdfec59d` | 无载体 | **HOLD** | Go 无 `guardian_feedback` 表/模块、无 `auto-review-failures` 上传合并；迁移止于 `0050`；`appserver/feedback.go` 是用户反馈上传，非 Guardian 失败持久化 |
| 3 | `#51133` | `4c9f42f4` | 无载体 | **HOLD** | Go 无 `async_scorer` 包/`DecisionsSampler`；`CODEX_GUARDIAN_DECISIONS_API_KEY` 仅在 `envutil` 的**不可继承环境变量黑名单**（`:30`），非采样器凭据 |
| 4 | `#51140` | `3f1ccb7c` | 无载体 | **HOLD** | Go 0 命中 `PreparedGuardianContext`/`GuardianReviewSessionReuseKey`/`ReviewerRequest`/`requires_fresh_session`/`ReviewerPool`：Rust 的 reviewer 会话池/checkpoint 恢复机制整体缺失 |
| 5 | `#49032` | `46fdd5ef39` | 半落地 | **HOLD** | sqlite 半片被**后代提交** `#49102`(`c2d2f422e6`) 取代，Go 已对齐 `#49102` 终态；app-server 半片 `FmtSpan`：Go 无 tracing span 机制（0 命中），slog 架构 |
| 6 | `#49467` | `76a6e55d5a` | 已落地等价 | **HOLD** | Go 全量载体：`features/features.go:303 login_shell_package_path` + `tool/shell.go:414 DeriveExecArgsWithPathPrepends` + `tool/unified_exec.go:661` + 专项测试 |
| 7 | `#49280` | `18194bfd35` | 已落地等价 | **HOLD** | Go `appserver/environment_capability_roots.go:70 restrictCapabilityRootsToSelections`（唯一调用点 `runtime_router.go:13520`）；Go 无 `captured_environments`/readiness 重连路径，故 Rust 的「filter 前置」理由不适用 |
| 8 | `#49147` | `3a16c0b707` | 已等价 | **HOLD** | Go `chatgptapi/cloud_tasks.go:175` 已用 `strings.TrimRight(..., "/")`（无循环）+ 回归测试 `cloud_tasks_normalize_test.go:23` |
| 9 | `#49144` | `ff3c82c8a9` | 已落地等价 | **HOLD** | `ff3c82c8a9` 是 `afb436df8b`(#50811) 的**祖先**；Go `app/interactive.go:2256 launchSettingForKey` 实现 Rust `is_launch`，且 `:2209` 同时覆盖 `model_reasoning_summary`+`model_verbosity` |
| 10 | `#50445` | `d4eed6dca5` | 纯测试型 | **HOLD** | Rust 改动全部在 `core/src/tools/parallel.rs` 的 `mod tests`；Go 无 `codex.tool_call` timing 事件（0 命中），无生产语义 |
| 11 | `#49489` | `bcd6d9ab6b` | 纯测试型 | **HOLD** | Rust = `#[cfg(debug_assertions)]` 分析测试 + 删 2 个未用 import/1 个 1 行空文件；0 生产语义 |
| 12 | `#50781` | `f365d5754b` | 需架构决策 | **HOLD**（标签更正为**无载体型**） | Go `tui/app_server_session.go` 为 **10 行 stub**；`tui/app/app_server_events.go:75` 决策函数**无生产消费者**；live app-server TUI 事件循环未移植 ⇒ 运营该 gate 的路径不存在 |

## 2. 逐条全文证据

### 2.1 `#51642` —— HOLD（机制性 N/A）

**Rust 原文**（`git show a5130128697b10022a88e8f5eae6dca77393b4b0`）：
- `git log --format=%H%n%s -1 a513012869^` ⇒ `5b0b253035…` = **#51627** ⇒ #51642 是 #51627 的**直接子提交**。
- 三处 hunk 全部是**类型迁移收尾**（`SectionContent::Other(ContentItem::InputText{..})` 取代 `ContentItem::InputText{..}`，以及 framing marker 用 `assistant_start.to_owned().into()`）：
  ```
  -|| matches!(&item.content, ContentItem::InputText { text }
  +|| matches!(&item.content, SectionContent::Other(ContentItem::InputText { text })
  ```
- 类型来源（`git show 5b0b253035:codex-rs/guardian-context/src/composition.rs`）：
  ```rust
  pub(crate) enum SectionContent { Transcript(TranscriptRecord), Other(ContentItem) }
  ```

**机制缺失证明（非字符串计数）**：
```zsh
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'SectionContent\|sectionContent' 185d03c9 -- '*.go'
（无输出）
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'TranscriptFormat\|TranscriptRecord' 185d03c9 -- '*.go'
185d03c9:tui/chatwidget/voice.go:70:// VoiceTranscriptRecord is one retained caption.   ← 仅语音字幕，与 guardian-context 无关
```
⇒ Go **不存在** `SectionContent` 这个类型，也不存在 Line/Json 的 `TranscriptFormat` 概念；guardian 转录在 Go 里是 `[]string`（`appserver/guardian_reviewer.go:820 guardianReviewTranscript`）。

**Go 已承载的可观察行为**（`git show 185d03c9:state/guardian_retained_context.go`）：
| Rust #51627/#51642 语义 | Go 载体 |
|---|---|
| partition：assistant originals + omission notice 移出 instruction 前缀 | `:227 SplitRetainedInstructionFragments`（`!Required \|\| Content == notice`） |
| `retained_assistant_context` 段的 START/END 标记 | `:51-52` 常量 + `:243 retainedAssistantContextSectionItems` |
| 只有 marker/空家族 ⇒ 不产生该段 | `:243`（`len(fragments)==0` ⇒ `nil`） |
| async/sync 两套 framing | `:312 RenderRetainedInstructionSectionsForPresentation` |
| split omission ⇒ 不下发/不复用 omission 元数据 | `:330 HasSplitAssistantOmission`、`:408 RetainNewRetainedInstructions` |

RC/回归证据：`state/guardian_retained_context_test.go:791 TestHasSplitAssistantOmissionLikeRust`、`:830 TestRetainNewRetainedInstructionsKeepsSplitAssistantOmissionLikeRust`、`appserver/retained_context_test.go:166`、`appserver/retained_instruction_test.go:48`。

**越界提示（非本单范围）**：Go 没有 `transcript_mode=json`（Rust `TranscriptFormat::Json` 是生产配置，见 `ext/guardian-v2/src/async_scorer/config_tests.rs`）。这是 **Guardian-v2 异步评分器族**（队长已判 N/A / 用户决策清单）的既存缺口，**不属于 #51642**（#51642 只补类型匹配，不新增 Json 模式）。见 §3(a)。

### 2.2 `#51651` —— HOLD（无载体）

**Rust 原文**：`git show --stat 30bdfec59d` ⇒ 17 文件，含 `state/migrations/0060_guardian_review_feedback.sql`、`state/src/runtime/guardian_feedback.rs`（新增）、`feedback/src/guardian.rs`（进程内 buffer → SQLite 持久化）、`auto-review-failures.jsonl` 上传合并去重。

**机制缺失证明**：
```zsh
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'guardian_review_feedback\|guardian_feedback\|GuardianReviewFeedback' 185d03c9   # 0 命中
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'auto-review-failures' 185d03c9                                                     # 0 命中
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'GuardianFeedback\|record_failed_review\|RecordFailedReview' 185d03c9              # 0 命中
$ git -C /home/jacks/jacks_dev/codex_go ls-tree -r --name-only 185d03c9 -- state/migrations/state | tail -1
state/migrations/state/0050_threads_section_empty_preview_indexes.sql     ← 无 0060
```
Go 的 `appserver/feedback.go`（543 行）是**用户发起的反馈上传**（附件/诊断包），不含 Guardian 自动失败记录的持久化存储；`FailedReview` 的 2 处命中是无关测试名 `TestFailedReviewTurnPreservesLifecycleOrderLikeRust`。⇒ 机制（SQLite 持久化 + 跨重启 + 上传合并）不存在。

### 2.3 `#51133` —— HOLD（无载体）

**Rust 原文**：`git show 4c9f42f4` 改 `ext/guardian-v2/src/async_scorer/startup.rs::decisions_api_key`（`CODEX_GUARDIAN_DECISIONS_API_KEY` 缺失时按 provider=`openai` 且无 custom base_url 回退 `OPENAI_API_KEY`）。

**对抗点（本次特别注意）**：`CODEX_GUARDIAN_DECISIONS_API_KEY` **在 Go 里有命中**，但**不是机制**：
```zsh
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'CODEX_GUARDIAN_DECISIONS_API_KEY' 185d03c9 -- '*.go'
185d03c9:envutil/envutil.go:30:	"CODEX_GUARDIAN_DECISIONS_API_KEY",     ← 不可继承环境变量黑名单（清洗）
185d03c9:envutil/envutil_test.go:23: ...
185d03c9:envutil/envutil_test.go:118: ...
```
`envutil.go:24-35` 的上下文是 `nonInheritableEnvVars`（Rust #50019 的清洗语义），**不是采样器凭据解析**。
```zsh
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'AsyncScorer\|DecisionsSampler\|async_scorer' 185d03c9 -- '*.go'
（仅 parity/contracts/manifest.json + update/*.md，无 Go 源码）
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'DecisionsKey\|fallback_to_api_key\|FallbackToAPIKey' 185d03c9 -- '*.go'
（0 命中）
```
⇒ Go 无 `ext/guardian-v2` 异步评分器 / Decisions 传输，`decisions_api_key` 的**回退机制**无处可落。**HOLD**。

### 2.4 `#51140` —— HOLD（无载体）

**Rust 原文**：`git show 3f1ccb7c` 改 `core/src/guardian/review_session_setup.rs` + `input_budget.rs`，引入 `ReviewerSelection{ReuseIfAvailable,FreshParentCheckpoint}`、把 `CheckpointRecovery` 共享标志改为每次尝试独立的 `recovery_requested: Arc<AtomicBool>`、`requires_fresh_session`。

**机制缺失证明（类型/函数级）**：
```zsh
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'PreparedGuardianContext\|GuardianReviewSessionReuseKey\|ReviewerRequest\|requires_fresh_session\|RequiresFreshSession\|fresh_parent_checkpoint\|FreshParentCheckpoint' 185d03c9 -- '*.go'
（0 命中，exit=1）
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'ReviewerPool\|reviewerPool' 185d03c9 -- '*.go'
（0 命中，exit=1）
```
Go 的 Guardian 是**同步** `Review`（`appserver/guardian_reviewer.go`），没有 `codex-guardian-reviewer` 会话池、没有「同一 review deadline 下的 checkpoint 重试」，因此「每次尝试独立恢复标志」这一改动无对应运行路径。**HOLD**。

### 2.5 `#49032` —— HOLD（半落地：sqlite 半由后代 #49102 覆盖；app-server 半机制性 N/A）

**祖先关系证明（比「窗口内覆盖」更强的证据）**：
```zsh
$ git -C /home/jacks/jacks_dev/codex merge-base --is-ancestor 46fdd5ef39 c2d2f422e6 && echo YES
YES     ← #49032(46fdd5ef39) 是 #49102(c2d2f422e6) 的祖先
```
`git show 46fdd5ef39` 在 `state/src/sqlite.rs` **首次**引入 `after_connect`，其条件是 `if mode == 1 || empty { PRAGMA auto_vacuum = INCREMENTAL }`；随后 **#49102**（`git show c2d2f422e6 -- codex-rs/state/src/sqlite.rs`）把它改成 `if empty { … }`（**保留既有模式含 FULL，不再写锁转换**）并加上「首个初始化错误直达调用方」的 channel。

**Go 命中 #49102 终态**（`git show 185d03c9:state/sqlite.go:207-242 initializeDatabaseSettings`）：
```go
var mode int64
conn.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode)
if mode == 0 {
    var empty int64
    conn.QueryRowContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM sqlite_schema)`).Scan(&empty)
    if empty == 1 { conn.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`) }
}
conn.ExecContext(ctx, `PRAGMA journal_mode = WAL`)
```
注释亦自陈「(#49102, upstream c2d2f422e6)」。⇒ sqlite 半片**已是终态**，且比 #49032 的中间态**更新**。

**app-server 半片（`FmtSpan::FULL → NEW|CLOSE`）机制缺失证明**：
```zsh
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'FmtSpan\|span_events\|SpanEvents\|tracing_subscriber' 185d03c9 -- '*.go' '*.rs'
（0 命中，exit=1）
```
Go 日志是 `log/slog`（`state/log_handler.go:451 NewTextHandler`），**不存在 span enter/exit 记录**，也就没有「span 记录阻塞 SQLx worker」的场景可改。⇒ app-server 半片 HOLD。

### 2.6 `#49467` —— HOLD（已落地）

**Rust 原文**：`76a6e55d5a` 5 文件，新增 `ShellInvocation::derive_exec_args_with_path_prepends`（POSIX login shell 内在启动后把 executor 报告的目录前插回 `PATH`），并在 `tools/runtimes/unified_exec.rs` 以 `Feature::LoginShellPackagePath` + `is_posix_login` + 非显式 `PATH` 覆盖门控。

**Go 载体（逐点对照）**：
| Rust 元素 | Go 载体 |
|---|---|
| `Feature::LoginShellPackagePath` | `features/features.go:303`（key `login_shell_package_path`）；`appserver/runtime_router.go:14280`、`exec/exec.go:1304` 读取 |
| `derive_exec_args_with_path_prepends` | `tool/shell.go:414`（`:393 IsPosixLogin`）、`tool/shell_executor.go:775` |
| `Environment::info().prepend_path_dirs` | `execserver/server.go:427 PrependPathDirs` / `:3544 LocalPrependPathDirs`（`#49360`） |
| unified_exec 门控 | `tool/unified_exec.go:604-661`（注释逐条引 Rust #49467） |
| 回归 | `tool/login_shell_path_prepends_like_rust_test.go`（`:117` 门控、`:225` `$LINENO`/换行路径） |

### 2.7 `#49280` —— HOLD（已落地等价）

**Rust 原文**：`18194bfd35` 在 `core/src/session/mcp.rs::selected_capability_roots` 加 `captured_environments.contains_key(environment_id)` 过滤（**在 dedup/readiness 之前**，以避免重连历史 executor）；并删掉一段「roots 可独立于 turn environments」的旧注释。

**Go 载体**：`appserver/environment_capability_roots.go:70 restrictCapabilityRootsToSelections`（唯一生产调用点 `appserver/runtime_router.go:13520`），按**当前环境选择**保留 root 并恢复原序。

**对抗点（诚实登记）**：
```zsh
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'CapturedEnvironments\|resolveSelectedCapabilityRoots' 185d03c9 -- '*.go'
（0 命中）
```
Go **没有** `captured_environments` 概念，也**没有** `resolve_selected_capability_roots` 这条「会重连历史 executor 的 readiness 解析」路径；Go 用「线程环境选择」（`threadEnvironmentSelections`，turn 开始时由 `persistTurnEnvironmentSelections` 更新）作为等价输入。Rust 的「filter 必须前置」的**理由**在 Go 不成立，故**行为等价**。⇒ HOLD。
（提醒：唯一调用点 `runtime_router.go` 是 ⛔ syncw3 避让面；**仅登记，未动**。）

### 2.8 `#49147` —— HOLD（已等价）

**Rust 原文**（`git show 3a16c0b707`）：`normalize_base_url` 的 `while base_url.ends_with('/') { pop }` → `input.trim_end_matches('/')`，并加表驱动回归（含 `""`、`"///"`、`https://chatgpt.com///` 等）。

**Go 载体**：`chatgptapi/cloud_tasks.go:175 NormalizeCloudBaseURL` = `strings.TrimRight(strings.TrimSpace(input), "/")`；回归 `chatgptapi/cloud_tasks_normalize_test.go:23 TestNormalizeCloudBaseURLNormalizesURLsLikeRust`。

**逐点对照**：
| 输入 | Rust `trim_end_matches('/')` | Go `TrimRight(...,"/")` | 一致 |
|---|---|---|---|
| `https://example.com/path` | 同 | 同 | ✅ |
| `https://example.com/path///` | `…/path` | `…/path` | ✅ |
| `""` / `"///"` | `""` | `""`（Go 再回落默认值，见下） | ✅（尾斜杠语义） |
| `https://chatgpt.com///` | `…/backend-api` | `…/backend-api` | ✅ |

**两处已声明的 Go 超集**（**既存**、非 #49147 引入，且 Go 测试注释已显式登记）：① `""`/`"///"` → `DefaultCloudTasksBaseURL`（Rust 的调用方另行回落）；② 额外 `TrimSpace`（Rust 的 `trim_end_matches` 不裁空白）。二者均**不在 #49147 的 diff 语义内**（#49147 是 behavior-preserving 重写）。⇒ HOLD。

### 2.9 `#49144` —— HOLD（已落地等价）

**取代/覆盖证明**：
```zsh
$ git -C /home/jacks/jacks_dev/codex merge-base --is-ancestor ff3c82c8a9 afb436df8b && echo YES
YES     ← #49144 是 #50811 的祖先（"Honor server reasoning summary defaults in new TUI threads"）
```
Rust #49144 的语义（仅当 `model_reasoning_summary`/`model_verbosity` 的**胜出配置层**是 `SessionFlags` 或 `User{profile:Some}` 才作为 request override 转发）被 **#50811** 吸收。

**Go 载体（逐点对照）**：
- `app/interactive.go:2256 launchSettingForKey(layers, cliKeys, key)` —— 实现 Rust `is_launch`：
  ```go
  return source.Type == config.LayerSourceSessionFlags ||
      (source.Type == config.LayerSourceUser && source.Profile != nil)
  ```
- `app/interactive.go:2209 launchReasoningOverrides` —— **同时**遍历 `{"model_reasoning_summary", "model_verbosity"}`（与 Rust 的 `for key in [..]` 一致），`:2185 interactiveLaunchReasoningOverrides` 消费之。
- 回归：`app/embedded_reasoning_test.go:55 TestLaunchSettingForKeyMatchesRustIsLaunch`、`:16 TestInteractiveLaunchReasoningOverridesLikeRust`。

### 2.10 `#50445` —— HOLD（纯测试型）

**Rust 原文**（`git show d4eed6dca5`）：单文件 `codex-rs/core/src/tools/parallel.rs`，且 hunk 全部位于 `mod tests {` 内（加强 `tool_call_timing_guard_ignores_code_mode_source`：捕获 tracing 输出，断言 `event.name="codex.tool_call"` 恰 1 次）。

**Go 侧**：
```zsh
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'codex\.tool_call\|toolCallTiming\|ToolCallTiming' 185d03c9 -- '*.go'
（0 命中，exit=1）
```
Rust 改动**零生产语义**（只在测试模块内）。Go 无同名 timing 事件 ⇒ 无可移植的生产接线。⇒ HOLD。

### 2.11 `#49489` —— HOLD（纯测试型）

**Rust 原文**（`git show bcd6d9ab6b -- <paths>`）：
- `codex-rs/analytics/src/client_tests.rs`：新增 `#[cfg(debug_assertions)]` 测试 `account_switch_between_batches_does_not_send_old_credentials`；
- `core/src/tools/registry.rs`：删 1 行未用 import `ToolCallSource`；
- `login/src/internal_identity.rs`：删除（内容仅 1 行文档注释 `//! Authentication identity helpers...`）。

全部为测试/清理，无生产行为变更。⇒ HOLD。

### 2.12 `#50781` —— HOLD（标签更正为「无载体型」）

**Rust 原文**（`git show f365d5754b`）：
- 新增 `tui/src/app/app_server_thread_ownership.rs`：`owns_thread_for_routing`（`primary_thread_id == t || thread_event_channels.contains_key(t) || side_threads.contains_key(t) || agent_navigation.get(t).is_some()`）与 `owns_untracked_notification`（对未跟踪线程：`ThreadStarted` 看 subagent parent；**`McpServerStatusUpdated` 用 `ThreadRead` 有界重试取 parent**；parent 不被 TUI 拥有则忽略）。
- 关键：删除旧代码里对 `ServerNotification::McpServerStatusUpdated` 的**豁免**（旧逻辑显式 `&& !matches!(&notification, ServerNotification::McpServerStatusUpdated(_))`，即未跟踪线程的 MCP 状态更新会被接受）。

**Go 侧无运营路径（非字符串计数）**：
```zsh
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'owns_thread_for_routing\|owns_untracked_notification\|read_mcp_subagent_parent_thread_id' 185d03c9 -- '*.go'
（0 命中）
$ git -C /home/jacks/jacks_dev/codex_go grep -nI 'HandleServerNotificationEventDecision' 185d03c9 -- '*.go' | grep -v _test.go
185d03c9:tui/app/app_server_events.go:75:func HandleServerNotificationEventDecision(...)   ← 仅定义，无生产消费者
$ git -C /home/jacks/jacks_dev/codex_go show 185d03c9:tui/app_server_session.go | wc -l
10        ← AppServerSessionState 结构 + 2 个 getter 的 stub
```
- Go 的 `tui/app/app_server_events.go:84` 对 `ServerNotificationMcpServerStatusUpdated` **无条件** `RefreshMCPStartupExpectedServers = true`（无 ownership gate）——但该决策函数**没有生产消费者**；
- Go 的 `tui/app/thread_events.go:28 ThreadEventChannel`、`session_lifecycle.go:219 ShouldAttachLiveThreadForSelection`、`chatwidget/protocol.go:74 DecideProtocolNotification` 同样**无生产消费者**；
- live TUI（`tui/tea`）不含 `primaryThreadID`/`threadEventChannel`/`agentNavigation`/`ownedThread`（均 0 命中）。

⇒ Go **没有**「live app-server 支撑的 TUI 事件循环」，#50781 所改的 ownership gate 无运行路径可承载。**HOLD**（并从「需架构决策」细化为「无载体型」）。

**潜在分歧（若将来接线）**：`tui/app/app_server_events.go:84` 的 MCP 分支缺 ownership gate，且 `chatwidget/protocol.go:77` 只做 `currentThreadID != notificationThreadID` 的粗判（不查 parent）。这两处**当前无消费者**，故不构成行为分歧；接线时需一并补 #50781 语义。

## 3. 新发现的既存 divergence（非本单范围，需另立）

> 依队长 `msg-1791365664093310800-3313` §裁定 2 的口径：**不夹带进本单**，仅登记。

**(a) Go 缺 Guardian `transcript_mode=json`（Line/Json 转录格式）**
- 证据：`git -C codex grep -nI 'TranscriptFormat' b17c74cfd5 -- codex-rs` 显示 `TranscriptFormat::Json` 是**生产配置**（`ext/guardian-v2/src/async_scorer/config_tests.rs`、`guardian-context/src/composition.rs:86-87`、`prompts/src/guardian_instructions.rs:75-76`）；Go 侧 `TranscriptFormat`/`SectionContent` 均 0 命中，guardian 转录恒为 `[]string`。
- 归属：**Guardian-v2 异步评分器族**（队长已判 N/A / 用户决策清单）——**不是** #51642（#51642 只补类型迁移，不新增 Json 模式）。若单独立项，级别 **L**。
- 可复跑判据：`git -C <go> grep -nI 'TranscriptFormat' 185d03c9 -- '*.go'` ⇒ 0；`git -C <rust> grep -nI 'TranscriptFormat::Json' b17c74cfd5 -- 'codex-rs' | grep -v tests` ⇒ 有命中。

**(b) Go `NormalizeCloudBaseURL` 是 Rust 行为的超集（TrimSpace + 空值回落）**
- 已由 Go 测试 `chatgptapi/cloud_tasks_normalize_test.go:16-22` **显式登记**，且 #49147 属 behavior-preserving ⇒ 无需动作，仅留档。

**(c) Go `tui/app` + `tui/chatwidget` 决策库无生产消费者（app-server TUI 未接线）**
- 与 (a)/(b) 不同，这是**架构现状**（见 §2.12）。登记为「如需 app-server-backed TUI，需整体立项」。级别 **L / 需架构决策**，与 #50781 的处置一致。

## 4. 只读声明

- Rust 仓：仅 `git fetch origin main`（remote-tracking 更新）+ 只读 `show/log/grep/merge-base`；**工作树 HEAD 仍 `5b0b253035`**，未 checkout、未动 ref、未改文件。
- Go 仓：仅 `git -C … show/grep/ls-tree` 打 `185d03c9` 只读对象；**未创建/修改任何文件**（`git status` 无本车道产生的改动）。
- 本报告是唯一产物：`update/syncl5_na_audit_2026_10_07.md`。**0 补丁 / 0 commit / 0 push / 0 ref 移动。**
- 无 REOPEN ⇒ **无自行开工项**；`#51642` 关联的 Json 转录格式（§3(a)）与 `#50781`（§3(c)）均需队长裁决，我不自启。

---
*报告生成：syncl5 · Linux 节点 · 2026-10-07 · 基线 Go `185d03c9` / Rust `b17c74cfd5`*
