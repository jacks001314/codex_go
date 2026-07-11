# Codex Go / Rust 100% 对齐开发计划

生成时间：2026-07-11  
Go 版本目录：`D:\qax\reagent\dev\codex_go`  
Rust 版本目录：`D:\qax\reagent\dev\codex-main`

## 目标

将 Go 版本 Codex 的命令行行为、配置语义、认证、模型请求、agent/turn runtime、app-server 协议、MCP、插件、skills、TUI、sandbox、session 存储、遥测、发布物和测试结果对齐本地 Rust 版本源码。

“100% 对齐”的验收定义：

- 同一输入下，Go/Rust 的 stdout、stderr、退出码、JSON/JSONL 事件、错误文本和持久化文件结构一致。
- Go app-server 的 JSON-RPC schema、通知、请求、错误码和 TypeScript/Python SDK 期望的协议一致。
- Go 的默认生产路径不能落到本地 stub；stub 只能用于显式测试或离线 harness。
- 每个 Rust integration/golden fixture 都有对应 Go parity test，或有明确记录的语言/平台不可移植差异。
- Windows、Linux、macOS 行为按 Rust 平台语义分别验收，不能只在当前 Windows 主机上通过。

## 当前比对结论

本次是静态源码比对，未运行完整测试。Rust 目录不是 git 仓库快照；Go 仓库当前有大量未提交改动，且 `plan.md` 原本处于删除状态。本文件只重建计划，不回滚任何现有改动。后续所有对齐判断均以 `D:\qax\reagent\dev\codex-main\codex-rs` 的 Rust 源码为准。

规模概览：

| 项目 | Rust 版本 | Go 版本 |
| --- | --- | --- |
| 顶层实现目录 | `codex-rs` 下 101 个顶层目录 | `internal` 下 45 个包目录 |
| 源码文件 | 约 2446 个 `.rs` | 约 1338 个 `.go` |
| 入口 | `codex-rs/cli/src/main.rs` + workspace crates | `cmd/codex/main.go` + `internal/app` |
| CLI 命令面 | `exec/review/login/logout/mcp/plugin/app-server/remote-control/app/completion/update/doctor/sandbox/debug/execpolicy/apply/resume/archive/delete/unarchive/fork/cloud/responses-api-proxy/stdio-to-uds/exec-server/features` | 命令名和多数 flag 已覆盖 |
| Feature registry | `codex-rs/features/src/lib.rs` | `internal/features/features.go` 基本已同步当前 key/stage/default |
| 核心 runtime | Rust 以 `core` + in-process app-server client 驱动 thread/turn | Go 有 `internal/exec` + `internal/turn` + Responses runner，但需逐项证明协议/事件等价 |

当前 Go 版已有较多迁移成果：

- CLI 命令树、根级 `-c/--config`、`--enable/--disable`、`--remote`、`--strict-config` 解析已大体贴近 Rust。
- `internal/features` 已覆盖当前 Rust feature flags，包括 removed/stable/experimental/under-development 分类。
- `internal/model` 已有 Responses API runner、provider、OSS、Agent Identity、request compression、image generation 等路径。
- `internal/appserver`、`internal/mcp`、`internal/plugin`、`internal/sandbox`、`internal/tui` 都已不是空壳，具备大量协议和运行时代码。
- `internal/exec` 默认 `UseResponsesAPI=true`，本地 `LocalAgentRunner` 主要应作为测试/离线路径存在。

最高风险区域：

- Rust `exec` 现在强依赖 in-process app-server 协议、thread/turn 生命周期和 server request 处理；Go 需要证明直接 runtime 路径与 Rust 事件/持久化完全等价，或收敛到同样的 app-server 驱动路径。
- Rust `exec` JSONL 事件枚举不包含 `item.delta` / `response.rate_limits`；Go 已按 Rust 真源移除 exec JSONL 对这两类流式事件的输出，保留最终 `item.completed` / `turn.completed`。
- app-server protocol v2 是 IDE、SDK、TUI、exec 的共同契约，必须以 Rust schema/fixture 为单一真源做差异测试。
- 模型客户端的 WebSocket v2、sticky turn-state、401 恢复、debug headers、attestation、request compression、Responses Lite、output schema 等细节容易出现隐性差异。
- Sandbox/unified exec/PTY 在 Windows、Linux、macOS 上有大量平台细节，仅静态移植不够。
- TUI 行为由 Rust ratatui 迁移到 Go Bubble Tea，必须用 golden/snapshot 驱动，不应只靠人工观察。

## 模块对照

| Rust 模块 | Go 对应模块 | 状态判断 | 主要风险 |
| --- | --- | --- | --- |
| `cli`, `utils/cli`, `arg0` | `cmd/codex`, `internal/cli`, `internal/app` | 命令面大体覆盖 | help 文本、错误文本、flag 冲突、隐藏命令、completion |
| `features` | `internal/features` | 基本同步 | 自定义 feature config、legacy usage warning、model beta header |
| `config`, `cloud-config`, `codex-home`, `install-context` | `internal/config`, `internal/install` | 大量已实现 | config layer、managed requirements、cloud bundle、profile v2、strict unknown fields |
| `login`, `keyring-store`, `aws-auth` | `internal/auth`, `internal/model/provider` | 部分已实现 | OAuth/device code、Agent Identity、keyring/secret storage、forced workspace |
| `core`, `codex-api`, `model-provider`, `ollama`, `lmstudio` | `internal/model`, `internal/codexapi`, `internal/chatgptapi`, `internal/turn` | 已有 Go runner | WebSocket/fallback、retry、headers、event mapping、compaction、memory/realtime |
| `exec` | `internal/exec` | 功能较完整但需验收 | JSONL/human output、resume、review、server requests、session files |
| `app-server`, `app-server-protocol`, `app-server-transport`, `app-server-daemon` | `internal/appserver`, `internal/appserverdaemon`, `internal/remotecontrol` | 大量已实现 | schema 完全一致、transport auth、daemon lifecycle、remote control |
| `tools`, `core-plugins`, `core-skills`, `ext/*` | `internal/tool`, `internal/turn`, `internal/prompt`, `internal/appserver`, `internal/mcp`, `internal/plugin` | 分散实现 | dynamic tool、tool_search、request_user_input、image_generation extension、guardian、goal |
| `codex-mcp`, `mcp-server`, `rmcp-client`, `ext/mcp` | `internal/mcp` | 已有 client/server/runtime | OAuth scopes fallback、resources/templates、elicitation、progress、roots、OpenAI form |
| `plugin`, `core-plugins`, `skills`, `ext/skills` | `internal/plugin`, `internal/appserver/skills*`, `internal/prompt/skills_render*` | 已有主体 | marketplace install/upgrade/share、remote plugin、skill authorities、orchestrator provider |
| `tui` | `internal/tui` | 文件数充足，需行为验收 | slash commands、composer、history cells、status、approvals、diff（默认 `/diff` 已走 Rust WorkspaceCommand/no-index 路径）、onboarding、pets、snapshots |
| `sandboxing`, `linux-sandbox`, `windows-sandbox-rs`, `bwrap`, `exec-server` | `internal/sandbox`, `internal/execserver`, `internal/execpolicy`, `internal/shell` | 有实现和平台 stub | Windows elevated/restricted token、Linux bwrap/Landlock、macOS seatbelt、PTY/unified exec |
| `thread-store`, `state`, `rollout`, `rollout-trace`, `message-history` | `internal/session`, `internal/state`, `internal/rollout`, `internal/history` | 部分实现 | SQLite schema、archive/delete/fork、trace reduce、compression |
| `memories`, `realtime-webrtc`, `feedback`, `otel` | `internal/memories`, `internal/realtime`, `internal/appserver/feedback*`, `internal/telemetry` | 部分实现 | app-server endpoints、TUI surfaces、telemetry facts/events |
| `sdk/python`, `sdk/typescript`, root packaging/docs | 暂无直接 Go 对应或未明确 | 待定 | 若要求仓库级 100%，需补 SDK/发布/文档生成链 |

## 优先级路线

### P0 - 建立硬性对齐基线

- [x] 固化本地 Rust 快照清单：新增 `internal/parity` 基准测试，记录 `codex-rs/Cargo.toml` 123 个 workspace members 与关键 Rust 文件 hash，避免后续比较基准漂移。覆盖测试：`TestRustWorkspaceMembersSnapshot`、`TestRustCriticalFileHashesSnapshot`。
- [ ] 建立 Go/Rust parity matrix：以 Rust crate、CLI command、app-server method、feature flag、tool name、session item type 为主键，标记 Go 状态：`done / partial / missing / intentionally different`。
- [x] 建立 golden test 入口：新增 `internal/parity` manifest，固定 Rust `cli/tests`、`exec/tests`、`app-server/tests/suite/v2`、`core/tests/suite`、`tui/src/chatwidget/tests`、`tools/src` 的 fixture 根目录、文件数量和关键文件。具体输入输出 fixture 继续按模块逐项迁移。
- [ ] 增加 Go parity test 标签：将复制过来的 Rust fixture 单独放在 `internal/parity` 或各模块 `_test.go` 中，允许按模块运行。
- [ ] 定义差异报告格式：每个未对齐项必须包含 Rust source、Go source、现象、优先级、验收命令。
- [x] 禁止生产路径隐式 stub：增加测试确保 `codex exec/review` 默认不输出 `Go Codex exec stub received`，除非显式使用 `NewLocalRunner` 或测试注入 agent。现有覆盖：`TestNewRunnerDefaultsToResponsesAPI`、`TestExecDefaultUsesResponsesAPIInsteadOfStub`。

验收：

- `go test ./internal/features ./internal/cli ./internal/exec` 至少跑通新增 parity tests。
- `plan.md` 后续每次推进都能更新 TODO 状态和剩余差异数。

### P0 - CLI 行为与错误文本对齐

- [x] 逐条比对 Rust `Subcommand` 与 Go `knownCommands`，覆盖 visible alias、hidden command、cfg platform command。新增 `TestRustSubcommandSurfaceParity` / `TestRustSubcommandAliasParity`。
- [ ] 对齐 `codex --help`、各子命令 `--help`、usage、flag 默认值、冲突规则、unknown flag 文案。
- [ ] 对齐 root flags：`-c/--config`、`--enable/--disable`、`--remote`、`--remote-auth-token-env`、`--strict-config` 的作用范围和错误。
- [ ] 对齐 session commands：`resume/archive/delete/unarchive/fork` 的 `--last/--all/--include-non-interactive/--force` 解析、cwd 过滤和名字/UUID 优先级。
- [ ] 对齐 hidden/internal commands：`responses-api-proxy`、`stdio-to-uds`、`execpolicy`、`debug trace-reduce`、`debug clear-memories`。
- [ ] 对齐 platform commands：`sandbox` 在 macOS/Linux/Windows 的可用参数，`app` 仅桌面平台语义。

验收：

- 为每个 CLI command 建立 parse table test。
- Go 的错误文本不能出现旧迁移措辞，例如 `not implemented`、`Go port`、非 Rust 风格 placeholder。

### P0 - app-server protocol v2 绝对对齐

- [ ] 将 Rust `app-server-protocol/src/protocol/v2/*.rs` 的 request/notification/response/error schema 映射到 Go `internal/appserver/protocol.go`。
- [ ] 对齐 JSON-RPC request id：整数/字符串/null/invalid 类型处理。
- [ ] 对齐所有 method 名称、params 字段名、omitempty/null 语义和 camelCase/snake_case 兼容策略。
- [ ] 对齐 thread lifecycle：initialize、thread/start、turn/start、turn/interrupt、thread/read/list/delete/archive/unarchive/fork、subscribe/unsubscribe。
- [ ] 对齐 server requests：command/file/apply_patch/permissions approvals、MCP elicitation、dynamic tool call、request_user_input、auth refresh、attestation、current time。
- [ ] 对齐 schema 生成：Go 生成的 JSON Schema 必须与 Rust `generate-json-schema` / fixtures 结构一致，差异只能是排序。
- [ ] 对齐 TS bindings 生成或提供 Go 生成器的等价输出。

验收：

- Rust schema fixtures 可直接作为 Go test input。
- Go app-server 对 Rust SDK/Python SDK 测试请求能返回同构响应。

### P0 - exec/review/turn runtime 对齐

- [ ] 确认 Go `internal/exec` 的默认运行路径和 Rust `codex_exec::run_main` 等价：config bootstrap、auth restriction、telemetry init、state db、in-process server、initial operation。
- [x] 对齐 prompt/stdin 读取：UTF-8 BOM、UTF-16LE/BE BOM、UTF-32 BOM 错误、非法 UTF-8、`-` sentinel、piped stdin、positional prompt + stdin append。覆盖测试：`TestResolveRustPromptStdinParity`、`TestResolveRustPromptDecodeParity`、`TestRunJSONRustPromptStdinGolden`。
- [ ] 对齐 `exec --json` 事件顺序和 payload：thread.started、turn.started、item.started/updated/completed、reasoning、tool call、command_execution、web_search、file_change、mcp_tool_call、collab_tool_call、todo_list、error item、usage、turn.completed/failed。已补 Rust `CommandExecutionItem` 对齐：真实 `exec_command` 输出 `command_execution` start/completed，未执行的 approval required 保留 tool_output，覆盖测试：`TestCommandExecutionItemJSONShape`、`TestEmitFinalEventsMapsExecCommandToCommandExecutionLikeRust`、`TestEmitFinalEventsKeepsApprovalRequiredExecCommandAsToolOutput`。已补 Rust `WebSearchItem` 对齐：`tool_search_call` 不再退化为 generic `tool_call`，覆盖测试：`TestWebSearchItemJSONShape`、`TestEmitFinalEventsMapsToolSearchCallToWebSearchLikeRust`。已补 Rust `FileChangeItem` 对齐：apply_patch `fileChange=true` 输出 `file_change`，add/delete/update 和 declined->failed 映射按 Rust，覆盖测试：`TestFileChangeItemJSONShape`、`TestEmitFinalEventsMapsApplyPatchToFileChangeLikeRust`、`TestEmitFinalEventsMapsDeclinedFileChangeToFailedLikeRust`。已补 Rust `McpToolCallItem` 对齐：namespaced MCP 工具输出 `mcp_tool_call` start/completed，arguments/result/error/status/null 字段按 Rust，覆盖测试：`TestMCPToolCallItemJSONShape`、`TestEmitFinalEventsMapsMCPToolCallLikeRust`、`TestEmitFinalEventsMapsFailedMCPToolCallLikeRust`。已补 Rust `CollabToolCallItem` 对齐：`agent.spawn_agent` 输出 `collab_tool_call`，Go `wait_agent` 归一为 Rust `wait`，sender/receiver/prompt/agents_states/status 按 Rust，覆盖测试：`TestCollabToolCallItemJSONShape`、`TestEmitFinalEventsMapsCollabToolCallLikeRust`、`TestEmitFinalEventsMapsCollabWaitAgentToRustWait`、`TestToolDispatcherAddsThreadAndTurnContextToInvocations`。已补 Rust `TodoListItem` 生命周期：首次 `update_plan` 输出 `item.started`，后续输出 `item.updated`，turn 完成前输出 `item.completed`，覆盖测试：`TestTodoListItemJSONShape`、`TestItemUpdatedJSONShape`、`TestEmitFinalEventsMapsUpdatePlanToTodoList`、`TestEmitFinalEventsMapsMultipleUpdatePlansToTodoListLifecycleLikeRust`。已补 Rust `ErrorItem` 对齐：model reroute 作为 `item.completed` error，exec JSONL 关闭 HTML escaping，覆盖测试：`TestErrorItemJSONShape`、`TestExecStreamEventCollectorMapsModelRerouteToErrorItemLikeRust`。剩余：review/resume 边缘 fixture、human output 的进度/警告细节。
- [ ] 对齐 human output：stdout 只能是最终回答；进度、token、warning、config/auth 错误走 stderr。已补 final message 恢复规则和空 final message 行为：优先 response message，再从最后的 agent message/plan item 恢复；缺失时不打印空 stdout，last-message-file 写空内容并输出 Rust 风格 warning。已补 Rust 风格启动摘要：version/workdir/model/provider/approval/sandbox/reasoning/session/user prompt 全部走 stderr，并覆盖 resume CLI overrides。覆盖测试：`TestRunHumanRecoversFinalMessageFromAgentItemsLikeRust`、`TestRunHumanMissingFinalMessageWritesEmptyLastMessageLikeRust`、`TestRunExecResumeHumanConfigSummaryUsesCLIOverridesLikeRust`。
- [x] 对齐 `--output-schema`：schema 文件错误文案按 Rust 大小写输出，并通过 Responses `text.format` 发送 `{name:"codex_output_schema", type:"json_schema", strict:true, schema}`。覆盖测试：`TestRunRejectsInvalidOutputSchema`、`TestRunPassesOutputSchemaToAgent`、`TestRunResponsesRequestIncludesOutputSchemaLikeRust`。
- [ ] 对齐 `exec resume`：session id/name/last/all、cwd 匹配、previous response id、history input items。已补 `resume --last` cwd 过滤与 `--all` 禁用 cwd 过滤且不包含 archived 的 Rust 语义、`resume --image/-i` 附图、resume 显式 prompt 不追加 stdin、by-name 精确标题解析（UUID 优先、cwd 过滤、`--all` 禁用 cwd 过滤）、resume config override stderr 摘要、`LastResponseID` 优先于旧 `PreviousResponseID`、基础历史 input items 回灌、非模型可见 thread item（command_execution/file_change/mcp/collab/todo/error）不回灌且无 raw reasoning 不降级为 user message。覆盖测试：`TestRunExecResumeLastFiltersCWDUnlessAllLikeRust`、`TestRunExecResumeLastAllIgnoresArchivedLikeRust`、`TestRunExecResumeAcceptsImagesAfterSubcommandLikeRust`、`TestRunExecResumeByExactNameFiltersCWDUnlessAllLikeRust`、`TestExecResumeTargetUUIDTakesPrecedenceOverNameLikeRust`、`TestRunExecResumeHumanConfigSummaryUsesCLIOverridesLikeRust`、`TestRunExecResumeAppendsToExistingSession`、`TestInputItemsFromRecordOmitsNonModelVisibleThreadItemsLikeRust`。剩余：resume 更复杂 raw Responses history fixture。
- [ ] 对齐 `review`：已补 Rust review prompt 构造：uncommitted/base/commit/custom 文案逐字对齐，custom prompt 原样透传且空 prompt 错误为 `Review prompt cannot be empty`，`BuildPromptFromOptions` 不再把 diff 嵌入 prompt，`--base` 按 Rust `merge_base_with_head` 解析 merge-base SHA 并在远端 ahead 时优先 upstream，缺失分支退回 Rust backup prompt。已补 `review/start` response 基础 shape：target 校验文案按 Rust `branch/sha/instructions must not be empty`，Turn `items` 包含 Rust 合成 `userMessage` display hint，commit title trim 后进入 `commit abcdef1: title`。已补 Rust `review_format` 输出：finding 行使用 `—`，body 按 Rust `str::lines()` 处理空 body/结尾换行；已补 reviewer JSON 输出解析：先整段 JSON，再首尾 `{...}` 子串，失败时把原文放入 `overall_explanation`，并用 Rust `exit_success.xml`/`exit_interrupted.xml` 生成固定 `review_rollout_user`/`review_rollout_assistant` 文本；`exec review` 的 human stdout 与 last-message-file 使用该渲染。已补 review model 选择：`review_model` 优先于 session/CLI model，未设置时回落 session model。已补 review subagent metadata：`exec review` 请求携带 `x-openai-subagent=review`，turn metadata 携带 `subagent_kind=review`。已补 review rubric instructions：Go 嵌入的 `internal/review/rubric.md` 与 Rust `prompts/templates/review/rubric.md` 逐字一致，`exec review` 使用该 rubric 且忽略普通 model instructions file；rubric SHA 已纳入 `TestRustCriticalFileHashesSnapshot`。已补 review-only 工具限制：review 请求禁用 hosted/standalone image generation，防止 Responses runner 自动补工具；app-server review runtime 同时禁用 standalone web search 与 standalone/hosted image generation 工具入口。已补 `review/start` app-server lifecycle：成功启动 review 后按 Rust 同步发送 `enteredReviewMode` 的 `item/started` 与 `item/completed`；detached delivery 会先 materialize 父线程 rollout、按 `review_model` 覆盖 fork 模型、fork 出真实 review thread、发送 `thread/started`，并把 `reviewThreadId` 指向可读取的新线程；有 runtime/agent 时以 Rust review task 参数运行 reviewer（`TaskKind=review`、review rubric、`approvalPolicy=never`、`review_model` 覆盖、禁用 hosted image generation），抑制 reviewer 原始 stream delta，完成后解析 JSON、写入 Rust rollout user/assistant、发送 `exitedReviewMode` started/completed 与最终 `agentMessage`；review 被 `turn/interrupt` 取消时按 Rust interrupted rollout 发送 fallback `exitedReviewMode` 和 `Review was interrupted...` assistant message；reviewer runtime 返回非取消错误时按 Rust `start_review_conversation(...).await.ok()` 语义吞掉子会话错误，发送 fallback `exitedReviewMode` 和 interrupted assistant message，但主 review turn 仍为 completed 且不发普通 `error`。覆盖测试：`TestPromptForBaseBranchBackupMatchesRustTemplate`、`TestBuildPromptFromOptionsUsesRustReviewPromptWithoutEmbeddingDiff`、`TestBuildPromptFromOptionsBaseUsesMergeBaseLikeRust`、`TestGitDiffProviderMergeBaseWithHeadReturnsSharedCommit`、`TestGitDiffProviderMergeBaseWithHeadPrefersUpstreamWhenRemoteAhead`、`TestGitDiffProviderMergeBaseWithHeadReturnsEmptyWhenBranchMissing`、`TestReviewStartDisplayHintsMatchRust`、`TestReviewStartResponseMarshalRustTurnShape`、`TestRuntimeRouterReviewStartEmitsEnteredReviewModeLikeRust`、`TestRuntimeRouterReviewStartDetachedForksThreadLikeRust`、`TestRuntimeRouterReviewStartRunsReviewTurnAndEmitsExitLikeRust`、`TestRuntimeRouterReviewStartInterruptEmitsInterruptedExitLikeRust`、`TestRuntimeRouterReviewStartRuntimeErrorEmitsFallbackExitAndCompletesLikeRust`、`TestRuntimeRouterReviewRuntimeDisablesStandaloneWebAndImageToolsLikeRust`、`TestFormatFindingsBlockMatchesRustPlainAndBodyLines`、`TestParseOutputEventMatchesRustJSONAndSubstringFallback`、`TestReviewRolloutMessagesMatchRustTemplates`、`TestRenderOutputText`、`TestRunExecReview`、`TestRunExecReviewRendersStructuredOutputLikeRust`、`TestRunExecReviewAddsReviewSubagentMetadataLikeRust`、`TestRunExecReviewUsesReviewModelFromConfigLikeRust`、`TestRunExecReviewFallsBackToSessionModelWhenReviewModelUnsetLikeRust`、`TestRunExecReviewUsesRustReviewRubricInstructions`、`TestRunResponsesReviewRequestDisablesImageGenerationToolsLikeRust`、`TestReviewEndToEnd`、`TestExecReviewEndToEnd`、`TestReviewCustomPromptEndToEnd`。剩余：真实模型通过工具执行 git diff 时的 binary/untracked 边缘 fixture。
- [ ] 对齐 exec 模式下 server request 的 auto-reject/auto-cancel 文案。
- [ ] 对齐 last-message-file、ephemeral、ignore-user-config、ignore-rules、skip-git-repo-check、removed `--full-auto` warning。已补 last-message-file 无尾随换行、无 final message 时写空文件并告警；其它 CLI 开关仍需逐项对齐。

验收：

- Rust `codex-rs/exec` fixture 对应 Go tests 通过。
- 同一 fake Responses stream 下，Go/Rust JSONL 完全一致或有明确排序白名单。

### P0 - 模型客户端、Responses/WebSocket 与认证恢复

- [ ] 对齐 Rust `core/src/client.rs` 的 Responses HTTP/SSE/WebSocket v2 行为：prewarm、incremental reuse、fallback、disable websocket session state。
- [ ] 对齐 sticky routing：`x-codex-turn-state` 只在同一 turn 内复用，不跨 turn。
- [ ] 对齐 request headers：installation id、turn metadata、parent thread id、window id、subagent、beta features、timing metrics、Responses Lite、attestation。
- [ ] 对齐 request compression：zstd 开关、阈值、Content-Encoding、失败回退。
- [ ] 对齐 401 unauthorized recovery：ChatGPT refresh、Agent Identity fallback、provider auth refresh、telemetry tag。
- [ ] 对齐 provider error mapping：OpenAI、Codex backend、Bedrock expired signature、OSS provider、LM Studio、Ollama。
- [ ] 对齐 model catalog：bundled catalog、remote refresh、ETag、service tier、fast mode、reasoning effort `ultra -> max`、verbosity。
- [ ] 对齐 image input/output：local images、remote images、detail、invalid image placeholder、generated image save path。

验收：

- 通过 fake HTTP/SSE/WebSocket server 驱动 Go tests，覆盖 Rust `core/tests/suite/model_*`、`view_image`、`search_tool`、`realtime` 的同类场景。

### P1 - 配置、认证、要求文件和云配置

- [ ] 对齐 `config.toml` 全字段：model/provider、sandbox、approval、MCP、plugins、tools、features、analytics、auth、TUI、profiles、projects。
- [ ] 对齐 config layer 顺序：defaults、user、profile v2、project `.codex`、managed config、requirements、CLI overrides。
- [ ] 对齐 strict config：未知字段、legacy key、removed feature、project 层禁止字段。
- [ ] 对齐 `requirements.toml`：forced login method、workspace restrictions、feature requirements、remote control allowed、auth routes。
- [ ] 对齐 cloud config bundle：读取、缓存、失败回退、residency、originator、auth route。
- [ ] 对齐 auth 存储：API key、personal access token、ChatGPT OAuth、device auth、access token、agent identity、AWS Bedrock、keyring/secret storage。
- [ ] 对齐 login/logout/status 文案、退出码、环境变量优先级、forced workspace 校验。

验收：

- Go config/auth tests 复用 Rust config fixtures。
- `codex login status` 在各 auth mode 的输出与 Rust 一致。

### P1 - 工具系统与 agent loop

- [ ] 对齐 tool registry：shell、unified_exec、apply_patch、web_search、image_generation、view_image、tool_search、MCP、request_user_input、current_time、multi-agent。
- [ ] 对齐 tool spec JSON schema：strictness、unsupported schema pruning、tool names/namespaces、non-prefixed MCP tools。
- [ ] 对齐 shell/unified exec：PTY、stdin、interrupt、timeout、output truncation、exit code、streamed stdout/stderr、approval request payload。已用 `TestRustUnifiedExecSandboxSuiteManifest` 固定 Rust `unified_exec`/sandbox 测试平台矩阵和关键测试函数清单；已按 Rust `core/src/shell.rs`/`core/src/tools/handlers/unified_exec.rs` 对齐 handler 层 shell argv 派生、PowerShell profile 参数、`cmd /c`、zsh-fork 显式 shell 拒绝入口；已按 Rust sandboxing tests 保留 deny-read entries 并阻止 `require_escalated` 绕过 deny-read。后续按清单继续迁移 PTY/stdin/session/runtime fixture。
- [ ] 对齐 apply_patch：parser、streaming events、approval、error wording、move/delete/update edge cases。
- [ ] 对齐 dynamic tool runtime：deferred discovery、tool_search BM25、plugin install request、MCP dynamic tools。
- [ ] 对齐 hooks：PreToolUse、PostToolUse、PermissionRequest、SessionStart、universal output、unsupported decision 文案。
- [ ] 对齐 multi-agent：agent graph、identity、registry、spawn/attach/import、analytics、subagent source kinds。
- [ ] 对齐 compaction：local/remote compaction v2、context overflow、mid-turn compaction、history reinjection。

验收：

- Rust `core/tests/suite/tools.rs`、`unified_exec.rs`、`apply_patch`、`compact*.rs` 的关键 fixture 有 Go 等价测试；`unified_exec.rs`、`unified_exec_process_events.rs`、`unified_exec_zsh_fork_approvals.rs`、`core/src/tools/handlers/unified_exec_tests.rs` 已有 Go 侧 manifest 漂移检测。

### P1 - app-server runtime、daemon 与 remote control

- [ ] 对齐 transports：stdio、unix socket、websocket、off；Windows named pipe/Unix socket 兼容。
- [ ] 对齐 websocket auth：capability token、signed bearer token、issuer/audience/max skew、token file/hash。
- [ ] 对齐 daemon lifecycle：bootstrap/start/restart/stop/version、pid update loop、remote-control enable/disable、socket readiness。
- [ ] 对齐 request broker、connection notification sink、file watch、thread status manager、environment manager。
- [ ] 对齐 app-server command execution、PTY、process exec、file system APIs、feedback、review、goal、memory、config manager。
- [ ] 对齐 remote-control pairing、client tracker、server API、persistence、websocket reconnect/refresh。

验收：

- Go 通过 Rust `app-server/tests/suite/v2` 同名场景。
- `codex app-server --listen stdio://|unix://|ws://...|off` 行为与 Rust 一致。

### P1 - MCP、插件、skills 和 extensions

- [ ] 对齐 `codex mcp` CLI：list/get/add/remove/login/logout、JSON 输出、OAuth scopes discovery、provider rejected scopes fallback。
- [ ] 对齐 MCP runtime：stdio、streamable HTTP、bearer token env、OAuth token store、resources/templates、roots、progress、elicitation、required server failure。
- [ ] 对齐 `mcp-server`：Codex 作为 MCP server 的 tools/resources、runner、stdin/stdout framing。
- [ ] 对齐 `codex plugin` CLI：add/list/remove/marketplace add/list/upgrade/remove、JSON 输出、selector `plugin@marketplace`。
- [ ] 对齐 marketplace：local/git/git-subdir/npm/remote source、install cache、manifest validation、default/system marketplaces、remote plugin sharing。
- [ ] 对齐 plugin capabilities：skills、hooks、apps、app templates、MCP servers、auth/install policy。
- [ ] 对齐 skills：filesystem/environment/orchestrator/custom authority、SKILL.md progressive disclosure、assets、sample skills、dependency prompts。
- [ ] 对齐 extensions：web-search、image-generation、goal、guardian、memories、connectors、mcp executor provider、extension-api。

验收：

- Rust `cli/tests/mcp_*`、`plugin_cli.rs`、`marketplace_*.rs`、`ext/skills/tests` 的 Go 对应测试通过。

### P1 - Sandbox、exec-server 与平台安全

- [ ] 对齐 execpolicy：rules parsing、prefix rules、network rules、pretty output、host executable resolution、error source formatting。
- [ ] 对齐 Linux sandbox：bubblewrap 默认、legacy Landlock、network proxy、read/write roots、allow unix sockets、denial logs。
- [ ] 对齐 macOS sandbox：seatbelt profile、read/write/network policy、temporary files。
- [ ] 对齐 Windows sandbox：restricted token、elevated setup/runner、deny-read ACL、WFP filters、desktop/DPAPI/conpty、world-writable scan。
- [ ] 对齐 exec-server：stdio/ws listen、remote registration、environment id/name、agent identity auth、protocol events、process signals。
- [ ] 对齐 path normalization：Windows paths、WSL paths、path URI、symlink/canonicalization、cwd junction。

验收：

- Rust sandbox smoke tests 中可移植部分迁移为 Go；`exec/tests/suite/sandbox.rs`、`core/tests/suite/windows_sandbox.rs`、`core/src/tools/sandboxing_tests.rs` 已有 Go 侧 manifest 漂移检测。
- 平台专属测试用 build tags 分开，stub 必须返回 Rust 风格 unsupported error，不能静默降级。

### P1 - TUI 用户体验与快照

- [ ] 对齐 TUI startup：auth/onboarding、trust directory、model selection、managed config、remote mode、terminal probing。
- [ ] 对齐 composer：multi-line input、history search、paste burst、images、mentions v2、slash popup、keyboard modes。
- [ ] 对齐 slash commands：`/diff` 默认读取路径已按 Rust `tui/src/get_git_diff.rs` 改为 WorkspaceCommand 执行 `git diff --no-textconv --no-ext-diff --submodule=short --ignore-submodules=dirty --color`，未跟踪文件用 `git diff --no-index -- NUL|/dev/null <file>`，并禁用 hooks、可执行 filter 与不安全 fsmonitor helper；剩余继续覆盖 `/help`、`/model`、`/approval`、`/permissions`、`/status`、`/review`、`/goal`、`/memories`、`/plugins`、`/mcp`、`/pets` 等。
- [ ] 对齐 history cells：assistant/user/reasoning/tool/MCP/approval/patch/hook/plan/notice/session cells。
- [ ] 对齐 approval UI：exec、apply_patch、file change、permissions、MCP elicitation、request_user_input。
- [ ] 对齐 status surfaces：account、rate limits、remote connection、token budget、rollout budget、goals、fast mode。
- [ ] 对齐 diff/review side pane、theme picker、model picker、settings commands、external agent import/migration。
- [ ] 对齐 terminal rendering：markdown tables、wrapping、line truncation、ANSI、clipboard、notifications、title、terminal visualization instructions。

验收：

- 从 Rust TUI snapshot/golden 迁移关键场景到 Go。
- 在 Windows Terminal、PowerShell、cmd、Linux/macOS terminal 下分别做 smoke。

### P2 - Session、state、rollout、memory、cloud

- [ ] 对齐 thread-store/state SQLite schema、migration、recovery、WAL、安全写入。
- [ ] 对齐 session JSONL/rollout 文件结构、metadata、item ids、compression、archive/delete/fork/unarchive。
- [ ] 对齐 trace reduce、rollout truncation、response debug context、session source kinds。
- [ ] 对齐 memories read/write、clear memories、memory extraction/consolidation、Chronicle sidecar gating。
- [ ] 对齐 Codex cloud tasks：list/get/apply attempts、backend models、workspace settings、connectors、apply command。
- [ ] 对齐 realtime voice/WebRTC：session config、sideband auth、TUI entry、app-server APIs。

验收：

- 旧 Rust session/rollout 样本可被 Go 读取；Go 生成文件能被 Rust tools 读取。

### P2 - 遥测、反馈、诊断和更新

- [ ] 对齐 otel events/counters/logs：feature state、turn/tool/app-server/plugin/hook/review/accepted lines 等。
- [ ] 对齐 feedback request tags、auth recovery tags、response debug context。
- [ ] 对齐 `codex doctor`：环境、auth、config、sandbox、updates、app-server、thread inventory、provider reachability。
- [ ] 对齐 update/install context：npm/homebrew/release archive/managed updater/offline status。
- [ ] 对齐 panic/error recovery：Rust 风格 user-facing error、silent exit、exit code。

验收：

- doctor snapshot tests 对齐 Rust 输出。
- telemetry unit tests 验证字段名和值域。

### P3 - 发布物、SDK、文档和仓库级对齐

- [ ] 明确范围：如果“100% 对齐”包含整个 Rust repo，则 Go 版也需要覆盖 `sdk/python`、`sdk/typescript`、root docs、installer、release packaging、Bazel/CI 等；如果只对齐 CLI/runtime，则将 SDK/packaging 标为外部范围。
- [ ] 对齐 README、docs/config/authentication/sandbox/execpolicy/skills/slash_commands/install。
- [ ] 对齐 shell completion、man/help 文档生成。
- [ ] 对齐 binary aliases：`apply_patch`、`linux_sandbox`、`execve_wrapper`、Windows helper dispatch。
- [ ] 对齐 release archive layout、version injection、license/notice、third-party patches。
- [ ] 建立持续同步流程：Rust 更新后自动生成 Go parity issue 和 TODO diff。

验收：

- 用户安装、运行、升级、卸载路径与 Rust release 文档一致。

## 推荐执行顺序

1. 先做 P0 基线与协议测试，不再凭肉眼判断“差不多”。
2. 然后锁定 `exec/review`，因为它是 CLI、TUI、app-server、agent loop 的最小可见闭环。
3. 接着推进 app-server protocol/runtime，保证 IDE/SDK/TUI 都有同一后端契约。
4. 再补工具、MCP、插件、skills、sandbox，这些是 agent 真正可用的能力面。
5. 最后补 TUI snapshot、state/memory/cloud、发布/SDK/文档。

## 短期 TODO

- [x] P0-1：新增 `docs/parity_matrix.md` 和 `internal/parity` 基准测试，列出 Rust 123 个 workspace members/关键路径到 Go 包的映射与 snapshot hash。
- [x] P0-2：新增 CLI parse parity tests，覆盖 Rust `Subcommand` 全量命令和 alias；主要 flag 冲突仍按各命令专项测试继续补齐。
- [x] P0-3：新增 app-server protocol schema parity tests，先覆盖 request id、initialize、thread/start、turn/start、thread/list/read。
- [x] P0-4：新增 exec JSONL golden tests，覆盖普通 prompt、stdin prompt、`--output-schema`、tool call、command_execution、web_search、file_change、mcp_tool_call、collab_tool_call、todo_list lifecycle、error item、turn failure。覆盖测试：`TestRunJSONAndLastMessage`、`TestRunJSONRustPromptStdinGolden`、`TestRunPassesOutputSchemaToAgent`、`TestRunResponsesRequestIncludesOutputSchemaLikeRust`、`TestRunJSONRustToolCallGolden`、`TestCommandExecutionItemJSONShape`、`TestEmitFinalEventsMapsExecCommandToCommandExecutionLikeRust`、`TestEmitFinalEventsKeepsApprovalRequiredExecCommandAsToolOutput`、`TestWebSearchItemJSONShape`、`TestEmitFinalEventsMapsToolSearchCallToWebSearchLikeRust`、`TestFileChangeItemJSONShape`、`TestEmitFinalEventsMapsApplyPatchToFileChangeLikeRust`、`TestEmitFinalEventsMapsDeclinedFileChangeToFailedLikeRust`、`TestMCPToolCallItemJSONShape`、`TestEmitFinalEventsMapsMCPToolCallLikeRust`、`TestEmitFinalEventsMapsFailedMCPToolCallLikeRust`、`TestCollabToolCallItemJSONShape`、`TestEmitFinalEventsMapsCollabToolCallLikeRust`、`TestEmitFinalEventsMapsCollabWaitAgentToRustWait`、`TestToolDispatcherAddsThreadAndTurnContextToInvocations`、`TestTodoListItemJSONShape`、`TestItemUpdatedJSONShape`、`TestEmitFinalEventsMapsUpdatePlanToTodoList`、`TestEmitFinalEventsMapsMultipleUpdatePlansToTodoListLifecycleLikeRust`、`TestErrorItemJSONShape`、`TestExecStreamEventCollectorMapsModelRerouteToErrorItemLikeRust`、`TestRunJSONEmitsErrorEventWhenTurnFails`。已按 Rust `exec/src/exec_events.rs` 收敛事件面，exec JSONL 不再输出 `item.delta` / `response.rate_limits`，流式 assistant 文本在完成时输出 `item.completed`，并关闭 HTML escaping、补齐 MCP null 字段以匹配 Rust serde JSON。
- [x] P0-5：新增 production-no-stub test，确保默认 `codex exec` 不返回本地 stub 文案。
- [x] P0-6：新增 Responses fake server tests，覆盖 headers、reasoning、usage、rate limit、401 refresh、stream events。补齐 Rust 基准 `core/tests/suite/turn_state.rs` 的 sticky turn-state 语义：同一 turn 内复用首次 `x-codex-turn-state`，同 turn 后续新值不覆盖，跨 turn 清空；覆盖 HTTP JSON 与 SSE 两条 Go Responses 路径。
  - 最新：已按 Rust `core/src/session/turn.rs` 与 `core/src/responses_retry.rs` 补齐 Responses SSE 正文阶段重试：HTTP 成功建立后若在 `response.completed` 前断流、读取失败或 idle timeout，按 `stream_max_retries` 以 200ms 指数退避重新发起 sampling；调用方取消、deadline、明确 `response.failed` 与非 408 的 4xx 不重试。覆盖测试：`TestResponsesAgentRunnerRetriesDroppedSSEStreamLikeRust`、`TestResponsesAgentRunnerDoesNotRetryResponseFailed`、`TestResponsesAgentRunnerStreamsRetryAndIdleTimeout`。
- [x] P0-7：新增 `internal/parity` Rust snapshot/fixture manifest，固定 123 个 workspace members、关键 Rust 文件 hash、CLI/exec/app-server/core/TUI/tools fixture 根目录和文件数量，并进一步固定 Rust TUI 539 个 `.snap` 快照的目录清单与优先级 surface。覆盖测试：`TestRustWorkspaceMembersSnapshot`、`TestRustCriticalFileHashesSnapshot`、`TestRustGoldenFixtureRootsSnapshot`、`TestRustTUISnapshotManifestCoversPrioritySurfaces`。
- [x] P0-8：按 Rust `prompts/src/review_request.rs` 与 `git-utils/src/branch.rs` 对齐 review prompt：不在 prompt 中嵌入 diff，custom prompt 原样透传，commit/title/base backup 文案逐字对齐，base merge-base/upstream 优先级补齐。覆盖测试：`go test ./internal/review ./internal/exec ./internal/app`。
- [x] P0-9：按 Rust `app-server/src/request_processors/turn_processor.rs` 与 `bespoke_event_handling.rs` 对齐 `review/start` response 基础 shape、entered lifecycle 与 detached fork：合成 display `userMessage`、review hint、空 target 字段错误文案，发送 `enteredReviewMode` 的 `item/started`/`item/completed` 通知，detached review 先按 `review_model` 覆盖模型并 fork 可读取 review thread，随后发送 `thread/started`。覆盖测试：`go test ./internal/review ./internal/appserver ./internal/app`。
- [x] P0-10：按 Rust `core/src/review_format.rs` 对齐 review output 渲染：finding separator、checkbox、body line 处理与 fallback。覆盖测试：`go test ./internal/review`。
- [x] P0-11：按 Rust `core/src/session/review.rs` / `tasks/review.rs` 对齐 review model 选择：`review_model` 优先，未配置回落 session model。覆盖测试：`go test ./internal/exec`。
- [x] P0-12：按 Rust `prompts/templates/review/rubric.md` 对齐 review rubric instructions，并把模板 SHA 加入 critical snapshot。覆盖测试：`go test ./internal/parity ./internal/exec ./internal/review`。
- [x] P0-13：按 Rust review-only feature restrictions 禁用 review 请求的 hosted/standalone image generation 工具。覆盖测试：`go test ./internal/exec ./internal/model ./internal/turn`。
- [x] P0-14：按 Rust `core/src/tasks/review.rs` 与 `prompts/templates/review/exit_*.xml` 对齐 reviewer 输出解析和 review rollout 文本：整段 JSON/子串 JSON/fallback 规则、固定 rollout message ID、`exec review` human/last-message 渲染。覆盖测试：`go test ./internal/review ./internal/exec`。
- [x] P0-15：按 Rust `SubAgentSource::Review` 对齐 `exec review` Responses metadata：请求携带 `x-openai-subagent=review`，turn metadata 携带 `subagent_kind=review`。覆盖测试：`go test ./internal/exec`。
- [x] P0-16：按 Rust `core/src/tasks/review.rs::exit_review_mode` 与 app-server `ExitedReviewMode` bespoke handling 对齐 `review/start` 真实 runtime 完成/中断/子会话错误路径：review turn 使用 review rubric、`TaskKind=review`、`approvalPolicy=never`、`review_model` 覆盖，并禁用 hosted image generation、standalone image generation、standalone web search；streaming reviewer 原始 assistant delta 被抑制；完成后发 `exitedReviewMode` item started/completed 和最终 `review_rollout_assistant` agentMessage；中断时发 fallback review item 和 interrupted assistant message；reviewer runtime 返回非取消错误时按 Rust 吞掉子会话错误，主 turn completed 且不发普通 `error`。覆盖测试：`go test ./internal/appserver -run "TestRuntimeRouterReviewStart(RunsReviewTurn|Interrupt|RuntimeError)|TestRuntimeRouterReviewRuntimeDisablesStandaloneWebAndImageToolsLikeRust"`。
- [x] P1-1：整理 Rust `app-server/tests/suite/v2` 到 Go 可复用 fixture。已新增 `TestRustAppServerV2SuiteManifestCoversRustModules`，把 Rust v2 suite 的每个模块映射到 Go owner/focus，并在 Rust suite 增删文件时报告清单漂移；后续按该清单逐场景补齐 runtime fixture。
- [x] P1-2a：按 Rust `tui/src/get_git_diff.rs` 对齐 TUI `/diff` 默认读取路径：新增本地 `WorkspaceCommandRunner`、`ReadGitDiffWithRunner`、fsmonitor 安全探测、filter override、真实 untracked `git diff --no-index` 组合；Bubble Tea 默认 `/diff` 不再走 `review.GitDiffProvider` pseudo diff。同步 Rust slash command 描述到 Go `SlashCommandFrames`，覆盖 `/diff`、`/review`、`/approve`、`/status`、`/usage`、`/mcp` 等用户可见文案。覆盖测试：`go test ./internal/tui -run "Test(ReadGitDiff|GitDiff|DiffFilter|ParseUntracked|BuildGitDiff|SlashCommandFrameDescriptionsMatchRust)"`、`go test ./internal/tui/tea -run "TestModelDiffCommand|SlashPopup|Slash"`。
- [x] P1-2：整理 Rust `core/tests/suite/unified_exec.rs` 和 sandbox tests 的平台矩阵。已新增 `TestRustUnifiedExecSandboxSuiteManifest`，覆盖 Rust `core/tests/suite/unified_exec.rs` 34 个测试、`unified_exec_process_events.rs` 4 个参数化场景、`unified_exec_zsh_fork_approvals.rs` 3 个测试、`exec/tests/suite/sandbox.rs` 5 个 Unix sandbox 测试、`core/tests/suite/windows_sandbox.rs` 2 个 Windows sandbox 测试，以及 `core/src/tools/sandboxing_tests.rs`/`core/src/tools/handlers/unified_exec_tests.rs` 的工具层单测清单。覆盖测试：`go test ./internal/parity -run TestRustUnifiedExecSandboxSuiteManifest`。
- [x] P1-2b：按 Rust `core/src/shell.rs::derive_exec_args` 与 `core/src/tools/handlers/unified_exec.rs::get_command` 对齐 Go `exec_command` handler 层 shell 选择：PowerShell login 模式不再强制 `-NoLogo`，non-login 插入 `-NoProfile`，`cmd` 使用 Rust 小写 `/c`，新增 `UnifiedExecShellModeZshFork` 解析入口并按 Rust 拒绝模型显式 `shell`。覆盖测试：`go test ./internal/tool -run "Test(ShellDeriveExecArgs|ResolveCommand|BuildShellRequest|ShellExecutor)"`、`go test ./internal/tool`。
- [x] P1-2c：按 Rust `core/src/tools/sandboxing_tests.rs::deny_read_blocks_explicit_escalation_and_policy_bypass` 对齐 deny-read 保护：Go `PermissionProfile` 保留 Rust wire `access:"deny"` entries，`RuntimePermissionProfileJSON` 可写回 deny entry，`SandboxPermissionsPreservingDeniedReads` 将带 deny-read 的 `require_escalated` 降回 `use_default`，`BuildShellRequest` 不再升级到 danger-full-access。覆盖测试：`go test ./internal/sandbox -run "Test(RuntimePermissionProfilePreservesDenyReadEntriesLikeRust|SandboxPermissionsPreserveDeniedReadsLikeRust|BuildCommandRunPlan)"`、`go test ./internal/tool -run "Test(BuildShellRequestPreservesDeniedReadsForEscalationLikeRust|BuildShellRequestRequireEscalatedPreapprovalUsesFullAccessProfile|ResolveCommand|ShellDeriveExecArgs)"`。
- [x] P1-3a：整理 Rust TUI snapshot 的 Go 等价策略。已新增 `TestRustTUISnapshotManifestCoversPrioritySurfaces`，固定 Rust `tui` 下 11 个 snapshot 目录、539 个 `.snap` 文件、每个目录的 Go owner/focus，并强制 composer、approval、status、history-cell 四个优先 surface 都有 Rust 源快照索引。覆盖测试：`go test ./internal/parity -run TestRustTUISnapshotManifestCoversPrioritySurfaces`。
- [ ] P1-3b：按 P1-3a 清单继续迁移 Rust TUI 行为快照到 Go，优先覆盖 composer、approval overlay/request_user_input、`/status` surface、history cell、review pane 与 unified exec terminal snapshot。已补 Rust `tui/src/status/card.rs` 的 permissions/approval formatter 语义：AutoReview 显示 `Approve for me`，User 显示 `Ask for approval`，内置 read-only/workspace/full-access、自定义 profile、workspace extra roots 与 unrestricted managed sandbox 文案按 Rust `status_permissions_label` 对齐。已补 Rust `tui/src/status/rate_limits.rs` 的 credit amount formatter 边界：月度 credit limit 的 amount 解析必须拒绝 NaN/Inf/负数，仅接受 finite 且非负值。已补 Rust `tui/src/chatwidget/status_surfaces.rs` 的 status-line rate-limit 窗口选择顺序：5h/weekly/monthly primary/secondary 组合按 Rust `five_hour_status_window` 与 `weekly_status_window` 语义显示或省略；terminal title 截断按 Rust `graphemes(true)` 以 grapheme cluster 为单位，不再切坏组合字符；status-line/terminal-title item id 解析改为 Rust `parse::<...>()` 风格的精确匹配，不再接受前后空格。已补 Rust `tui/src/history_cell/tests.rs` 的 raw transcript 行语义：显式中间空行保留，末尾只去掉一个行终止符，不再吞掉 `alpha\n\n` 的最后一个空行。已补 Rust `tui/src/history_cell/mcp.rs` 的 MCP inventory 顺序语义：server/tool 名按 Rust 排序，但 resources/resource templates 保持 app-server 返回顺序。已补 Rust `tui/src/history_cell/exec.rs` 的 background terminal process cell：列表 bullet、chunk prefix、display-width 截断、首行 80 grapheme 限制与多行命令 ` [...]` suffix 按 Rust 语义。已补 Rust `tui/src/history_cell/approvals.rs` 的 approval decision cell：approved symbol 使用 U+2714 heavy check；命令 snippet 按 Rust `strip_bash_lc_and_escape` 等价规则，仅剥离 bash/zsh/sh 与 PowerShell，不剥离 fish/cmd，fallback 使用 shlex quoting，多行首行追加 ` ...`，80 grapheme 截断使用 ASCII `...`。已补 Rust `bottom_pane/chat_composer/history_search.rs` 的 case-insensitive match 边界：`\u0130` 按 Rust 范围映射为原始 UTF-8 字节区间。已补 Rust `bottom_pane/request_user_input/render.rs` 的隐藏 options footer 进度提示：options 区域不足时在 footer 前缀 `option N/total`，并按 Rust 选中索引 clamp；问题文本/notes wrapping 改用终端 display width，footer tip wrapping 按 Rust 保持单个 tip 不拆分。已补 Rust `bottom_pane/approval_overlay.rs` 的 network policy deny 快捷键边界：仅提供 deny amendment 时 `d` 提交永久 block；deny 选项隐藏时 `d` 不误触发；queued approval 推进改为 Rust `Vec::pop()` 的 LIFO 顺序，app-server resolved request 会从等待队列中移除；execpolicy-prefix option 过滤只按 Rust 拒绝 CR/LF，不再因 network/additional permissions 或空 prefix 额外隐藏；approval command display 复用同一 Rust shell 展示 helper。覆盖测试：`go test ./internal/tui/status -run "Test(StatusPermissionsLabelMatchesRustStatusSnapshots|RateLimitRenderingAndCreditFormatting)"`、`go test ./internal/tui/chatwidget -run "Test(StatusLineRateLimitWindowSelectionMatchesRust|TerminalTitleFrameAndTruncate|StatusSurfaceSelectionsDefaultsAliasesAndInvalids)"`、`go test ./internal/tui/history_cell -run "Test(RawLinesFromSourceMatchesRustTranscriptSemantics|MCPHistoryCells|ExecHistoryCells|ApprovalHistoryCells)"`、`go test ./internal/tui/bottom_pane/chat_composer -run TestHistorySearchFooterCursorCompatibilityAndCaseInsensitiveRangesMatchRustCore`、`go test ./internal/tui/bottom_pane/request_user_input`、`go test ./internal/tui/bottom_pane -run "Test(StripBashLCAndEscape|ExecApprovalOptions|ApprovalOverlay)"`、`go test ./internal/shell -run TestStripShellCommandAndEscapeMatchesRustDisplay`。
  - 最新：已补 Rust `tui/src/history_cell/approvals.rs` 的 network policy amendment allow/deny 历史文案：allow 保持 `persisted Codex network access to ...`，deny 使用 `✗` 前缀并输出 `denied codex network access to ... and saved that rule`。覆盖测试：`go test ./internal/tui/history_cell -run TestApprovalHistoryCells`。
  - 最新：已补 Rust `tui/src/history_cell/approvals.rs` 的 execpolicy amendment 历史文案：空 prefix 不再退化成 `matching commands`，而是按 Rust 保留 `commands that start with `。覆盖测试：`go test ./internal/tui/history_cell -run TestApprovalHistoryCells`。
  - 最新：已补 Rust `tui/src/history_cell/approvals.rs` 的 guardian action/patch/status helper：approved action 使用 U+2714 heavy check，summary/message/target/file 名不再 trim，patch 文件数量按 Rust `len().to_string()` 不加千分位。覆盖测试：`go test ./internal/tui/history_cell -run TestApprovalHistoryCells`。
  - 最新：已补 Rust `bottom_pane/approval_overlay.rs::build_header` 的 per-request header 字段边界：exec/permissions 保留 Thread/Environment/Reason common header，apply_patch 只显示 Thread/Reason 且不显示 Changes，MCP elicitation 只显示 Thread/Server/Message。覆盖测试：`go test ./internal/tui/bottom_pane -run TestApprovalOverlay`。
  - 最新：已补 Rust `approval_footer_hint` 的 open-thread footer：带 `ThreadLabel` 的 approval overlay footer 追加 `or o to open thread`，与现有 `o` 快捷键行为一致。覆盖测试：`go test ./internal/tui/bottom_pane -run TestApprovalOverlay`。
  - 最新：已补 Rust approval `open_fullscreen` 快捷键：`ctrl+a`/`ctrl+shift+a` 发送 fullscreen approval request 事件且不完成当前审批。覆盖测试：`go test ./internal/tui/bottom_pane -run TestApprovalOverlay`。
  - 最新：已补 Rust `tui/src/exec_command.rs::split_command_string` 与 chatwidget unified exec process display：命令字符串必须 round-trip 才拆分；Windows `C:\...` 非 round-trip 命令保留为单 token 后按 shlex quote；display 复用 Rust shell helper，支持 PowerShell，且不剥离 fish。覆盖测试：`go test ./internal/tui/chatwidget -run TestCommandLifecycle`、`go test ./internal/tui/...`。
  - 最新：已补 Rust `tui/src/exec_cell/render.rs` 的 exec command display：普通 exec transcript/display 使用共享 Rust shell display helper；unified exec interaction 保持 Rust 特例，只剥 bash/zsh/sh，其他命令按 `command.join(" ")`。覆盖测试：`go test ./internal/tui/exec_cell`、`go test ./internal/tui/...`。
  - 最新：已补 legacy tool_output approval modal 的 command metadata display：`approvalCommandText` 复用 Rust shell display helper，hook command 仍原样优先。覆盖测试：`go test ./internal/tui/tea -run TestApprovalCommandText`、`go test ./internal/tui/...`。
  - 最新：已补 Rust `tui/src/text_formatting.rs::truncate_text`：Go `tui.TruncateText` 改为 grapheme cluster 边界截断，`max >= 3` 时保留 ASCII `...` 预算，避免切坏组合字符。覆盖测试：`go test ./internal/tui/...`。
  - 最新：已补 Rust `tui/src/text_formatting.rs::capitalize_first`：Go `CapitalizeFirst` 改为 Unicode 首 rune uppercase，不再只处理 ASCII a-z。覆盖测试：`go test ./internal/tui/...`。
  - 最新：已补 Rust `tui/src/text_formatting.rs::center_truncate_path`：Go `CenterTruncatePath` 改为 Rust 同款左右 segment 优先、suffix segment 保留、必要时 front-truncate 单段并插入单字符 `…`。覆盖测试：`go test ./internal/tui -run TestTextFormattingHelpers`、`go test ./internal/tui/...`。
  - 最新：已补 Rust `tui/src/app/agent_status_feed.rs` 的 `/agent` status preview：最近 item 去重包含空 item id，activity/command 摘要使用共享 grapheme `TruncateText`，预览 wrapping 按 display width 处理宽字符并保留最近 3 行。覆盖测试：`go test ./internal/tui/app -run "Test(AgentActivity|AgentStatus|BoundedAgent|FindLoaded)"`、`go test ./internal/tui/...`。
  - 最新：已补 Rust `tui/src/history_cell/notices.rs` 与 `tui/src/app/history_ui.rs` 的 notice/desktop handoff 细节：warning/info/error/deprecation 不再 trim 输入，error 前缀保持 Rust 的 `■ `，safety/cyber policy 文案使用 Rust curly apostrophe 和 URL，deprecation summary 不强制换行，desktop thread URL/error message 原样保留输入，并补 Desktop opened/error history snapshot 断言。覆盖测试：`go test ./internal/tui/history_cell ./internal/tui/app -run "Test(NoticeHistoryCells|DesktopThread|HistoryUIState)"`、`go test ./internal/tui/...`。
  - 最新：已补 Rust `tui/src/app/app_server_event_targets.rs` 的 thread id 解析：app-server request/notification target 不再 trim 或接受 `thread-1` 这类宽松 slug，改为 Rust `ThreadId::from_string` 等价的 UUID parse，含 whitespace 的 id 进入 invalid/threadless 路径，合法 id 规范化为 canonical UUID。覆盖测试：`go test ./internal/tui/app -run "Test(ServerRequestThreadID|ServerNotificationThreadTarget|EventTargetFromServerEvent|RouteServer)"`、`go test ./internal/tui/...`。
  - 最新：已补 Rust `tui/src/app/agent_navigation.rs` 与相关 session lifecycle/background request 的 `ThreadId` typed 语义：Go agent navigation、agent picker liveness/backfill、startup stale thread、session summary resume hint、MCP inventory request thread 选择现在都拒绝带空白或非 UUID 的 thread id，合法 UUID 统一 canonical lower-hyphenated 形式；closed agent thread 不再带入 MCP inventory 请求；agent nickname/role 只在 Rust `None` 等价时回退，空字符串原样保留；resume hint 的 thread name、rollout path、attach/startup error display 也不再 trim。覆盖测试：`go test ./internal/tui/app -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust `tui/src/app/side.rs` 的 enum/ThreadId/文案边界：side parent request/notification kind 改为精确匹配，不再 trim 后接受；side close error message 的 thread id 改为 Rust `ThreadId` 等价 UUID parse/canonical，invalid/spaced id 回到 generic `side conversation` 文案；side footer context label 使用 Rust ` · ` 分隔并保留 parent label 原样；start/close error message 不再 trim error display。覆盖测试：`go test ./internal/tui/app -run TestSide -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust `tui/src/app/thread_events.rs` 的 file-change lookup 语义：turn id/item id 改为 Rust 精确匹配，空 request turn id 仍匹配任意 turn，空 item id 可匹配真实空 item id；FileUpdateChange 的 path/kind/diff/movePath 解析不再 trim，`movePath` 按字段存在性保留 Some，包括空字符串。覆盖测试：`go test ./internal/tui/app -run "Test(ThreadEvent|FileUpdateChanges)" -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust `tui/src/app/thread_goal_actions.rs`、`tui/src/goal_display.rs`、`tui/src/chatwidget/goal_status.rs` 与 `tui/src/chatwidget/slash_dispatch.rs` 的 goal 展示语义：`/goal` usage 文案改为 Rust 方括号形式，slash dispatcher 与 app action 共用同款文案；replace confirmation objective 不再 trim，并复用 grapheme `TruncateText`；goal usage summary 固定输出 `Objective: ...`，time/tokens 按 Rust 句式、compact token 与跨天 elapsed formatter；无 token budget 时不再额外显示 tokens used。覆盖测试：`go test ./internal/tui/app -run "Test(ThreadGoal|GoalUsage|ReplaceThreadGoal)" -count=1`、`go test ./internal/tui/chatwidget -run "Test(FormatGoalElapsed|ActiveGoal|CompletedGoal|GoalSummary|PreparedSlashArgs.*Goal)" -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust `tui/src/external_editor.rs` 的 editor command split 平台语义：Go 外部编辑器命令解析保留 quoted 空参数；Windows 路径仅按双引号分组且未闭合 quote 不返回 parse error，单引号按普通字符处理；非 Windows 路径保持 shlex 风格未闭合 quote parse error。覆盖测试：`go test ./internal/tui/tea -run Test.*ExternalEditor -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust `tui/src/app/input.rs` 的 backtrack primed reset 边界：非 Esc key press 才取消 primed backtrack，Go helper 不再 trim 后把 `" esc "` 当作 Esc。覆盖测试：`go test ./internal/tui/app -run "Test(ExternalEditor|InputShortcut|Backtrack)" -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust side conversation return shortcut 的 key 精确匹配语义：Ctrl+C/Ctrl+D 只接受真实 key identity，Go helper 不再 trim 后把带空白的 key 字符串当作有效快捷键。覆盖测试：`go test ./internal/tui/app -run "Test(SideReturnShortcut|WindowsSandboxState)" -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust `tui/src/app/config_persistence.rs` 的 effective config typed enum 语义：approval policy、approvals reviewer、sandbox mode、windows sandbox mode 不再 trim 后接受带前后空白的字符串，Go helper 改为精确字符串并显式拒绝 leading/trailing whitespace。覆盖测试：`go test ./internal/tui/app -run TestEffectiveConfig -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust `tui/src/bottom_pane/mentions_v2/search_catalog.rs`、`candidate.rs` 与 `filter.rs` 的 mention candidate 边界：Go 不再在 `Candidate.ToResult`、filter、render 途中执行非 Rust 的 normalized fallback；plugin candidate 保留空 display name、空 display search term 与原始 whitespace description；skill display fallback 增加 Rust `plugin:skill -> skill (plugin)` 语义。覆盖测试：`go test ./internal/tui/bottom_pane/mentions_v2 -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust `tui/src/bottom_pane/mentions_v2/popup.rs` 的初始 selection 语义：`MentionV2Popup::new` 本身保持未选中状态，只有 `set_query`/`set_candidates` 等同步动作才 clamp 到第一项，避免 Go 构造即选中造成初始渲染差异。覆盖测试：`go test ./internal/tui/bottom_pane/mentions_v2 -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust `tui/src/bottom_pane/mentions_v2/render.rs` 的 filesystem row 文件名拆分：Go 不再使用 `filepath.Base` 清理路径，而是按 Rust 对 display string 执行 `rfind(['/', '\\'])`，保留 `src/` 这类尾随分隔符路径的空文件名/路径前缀表现。覆盖测试：`go test ./internal/tui/bottom_pane/mentions_v2 -count=1`、`go test ./internal/tui/... -count=1`。
  - 最新：已补 Rust `tui/src/chatwidget/mcp_startup.rs` 的 MCP 启动过程：本地 TUI 不再在界面启动前同步阻塞 MCP inventory 探测，改为进入 TUI 后逐 server 异步发送 `starting/ready/failed`；底部显示 `Booting MCP server: name` 或 `Starting MCP servers (completed/total): names` 及耗时/中断提示；启动期间输入进入队列，全部 settle 后释放；失败即时输出 warning 并在结束时汇总 `MCP startup incomplete`；Esc 中断使用 lag finish，迟到 terminal update 不会重新打开进度。MCP service 新增 observer 入口，完成后更新 runner tools 与 `/mcp` inventory。覆盖测试：`go test ./internal/tui/tea -run "TestModel.*MCPStartup|TestModelQueuesInputUntilMCPStartupFinishes" -count=1`、`go test ./internal/mcp -run TestListStatusCheckedObserverReportsStartupLifecycle -count=1`、`go test ./internal/app -run TestInteractiveMCPRuntimeLoadsConfiguredServersForSlashCommand -count=1`、`go test ./...`。
  - 最新：已按 Rust `rmcp-client/src/streamable_http_retry.rs` 与 `rmcp_client.rs::run_service_operation_with_transient_retries` 对齐 MCP 网络重试：streamable HTTP 初始化及 `notifications/initialized`、`tools/list` 对网络错误和 408/429/500/502/503/504 最多尝试 3 次，退避固定为 250ms、1s；JSON-RPC `-32603` 且消息以 `http/request failed:` 开头时同样重试；403、401、404 session expiry、普通协议/反序列化错误不做瞬时重试。任意 HTTP operation 保留一次 404/410 session 重建，重建初始化继续使用同一重试规则；`tools/call` 的 5xx 不重试。stdio 删除 Go 原有的任意传输错误二次尝试，缺失可执行文件等永久错误按 Rust 立即失败。网络路径审计确认模型 HTTP/SSE 已有请求级重试与 401 恢复，remote-control WebSocket 已有指数退避、认证恢复和 `Retry-After`，MCP OAuth 与 Rust 一样无通用自动重试。覆盖测试：`go test ./internal/mcp -count=1`、`go test ./...`。
- [ ] P1-4：整理 MCP/plugin/skills 端到端 fixture，优先覆盖 JSON 输出和 OAuth/marketplace 错误路径。
  - 最新：已补 Rust `core/src/plugins/mentions.rs` 与 `core-skills/src/injection.rs` 的显式 plugin/app mention 语义：Go mention parser 改为 Rust markdown linked mention/裸 mention 规则，裸 `$app://...` / `@plugin://...` 不再当 path；common env var 不再作为工具 mention；结构化 mention path 不再 trim/URL decode；显式 plugin mention 只按 `config_name` 精确匹配，移除 Go-only app mention 反推插件与 name/remote/displayName 兜底匹配。appserver runtime 增加 `app://...` 不触发 explicit plugin instructions 的端到端护栏，app context 测试改为 Rust 支持的 `[$name](app://id)` linked mention。覆盖测试：`go test ./internal/plugin -count=1`、`go test ./internal/appserver -run "TestRuntimeRouter(TurnStartInjectsEnabledPluginInstructions|ThreadSettingsUpdateAffectsFutureTurn|AppMentionInjectsAppContext)" -count=1`、`go test ./internal/plugin ./internal/appserver -count=1`。
  - 最新：已补 Rust `core-skills/src/injection.rs` 的显式 skill mention 语义：Go `CollectExplicitSkillMentions` 现在只接受结构化 `type == "skill"` 和 Rust 形式的 `[$name](skill://path)`/直接 `$name`；linked mention 必须带 `$` sigil；common env var 不作为 plain skill；skill path 只去掉 `skill://` 前缀，不再 trim、URL decode、远程 URI 重写、绝对路径化或 clean；非 skill `app://`/`mcp://`/`plugin://` path 按 Rust 排除。appserver remote environment skill 测试同步改为 `[$remote-skill](skill://...)`。覆盖测试：`go test ./internal/prompt -run TestCollectExplicitSkillMentions -count=1`、`go test ./internal/appserver -run "TestRuntimeRouterTurnStartUsesRemoteEnvironmentSkillRoot|TestRuntimeRouterTurnStartUsesSelectedCapabilitySkillRoots|TestRuntimeRouterExplicitSkillInstructionsPersistForNextTurnLikeRust" -count=1`、`go test ./internal/prompt ./internal/appserver -count=1`。
  - 最新：已补 Rust `core-skills/src/render.rs` 的 available skills render 边界：token budget 下 description 截断 warning 使用 Rust `2% skills context budget` 文案；render 层不再额外折叠 description whitespace，只做默认 1024 字符截断；skill 行路径 label 固定为 Rust `file`，即使 path 是 `environment://...` 也不再输出 Go-only `(environment: ...)`。覆盖测试：`go test ./internal/prompt -count=1`、`go test ./internal/appserver -run "TestRuntimeRouter(SkillsContext|TurnStartUsesRemoteEnvironmentSkillRoot|TurnStartUsesSelectedCapabilitySkillRoots|ExplicitSkillInstructionsPersistForNextTurnLikeRust)" -count=1`、`go test ./internal/prompt ./internal/appserver -count=1`。
  - 最新：继续补齐 Rust `core-skills/src/render.rs` 的 available skills alias 渲染：Go 现在在绝对路径方案发生预算压力时构建 Rust 风格 `### Skill roots` / `r0` 短路径表，只有 alias 方案包含更多 skill、截断更少或总体 cost 更低时才切换；正文切换到 Rust alias intro/how-to；插件 cache 下单技能版本使用 marketplace root，多技能同版本使用 skill root。覆盖测试：`go test ./internal/prompt -count=1`、`go test ./internal/appserver -run "TestRuntimeRouter(SkillsContext|TurnStartUsesRemoteEnvironmentSkillRoot|TurnStartUsesSelectedCapabilitySkillRoots|ExplicitSkillInstructionsPersistForNextTurnLikeRust)" -count=1`、`go test ./internal/prompt ./internal/appserver -count=1`。
  - 最新：补齐 Rust `ext/skills/src/tools` 的 orchestrator authority 工具契约：Go registry 在存在 MCP service 时注册 model-visible `skills.list` / `skills.read` namespace tools；`skills.list` 从 `codex_apps` MCP `mcp/skill` resources 生成 `{authority, package, name, description, main_resource}`，按 Rust 校验 `skill://` URI、`skill_name`/`plugin_name` metadata、description `&<>` escape、warning 数量/byte 截断；`skills.read` 校验 authority/package/resource handle 和 package/resource 前缀关系，复用 MCP `resources/read` 读取完整资源。覆盖测试：`go test ./internal/turn -run "TestSkillsTools|TestBuildToolRegistryIncludesCoreAndRuntimeTools" -count=1`、`go test ./internal/turn -count=1`、`go test ./internal/appserver -run "TestRuntimeRouter(TurnStart|MCP|Tool|Dynamic|Image|Web|SkillsContext)" -count=1`。
  - 最新：补齐 Rust `AvailableSkillsInstructions::from_available_skills(..., include_skills_usage_instructions)` 开关：Go prompt 新增 `RenderAvailableSkillsWithOptions`，appserver runtime 从模型 catalog 的 `IncludeSkillsUsageInstructions` 决定是否注入 `### How to use skills`；旧 `RenderAvailableSkills` 默认保持包含 usage instructions，避免非 runtime 调用退化。覆盖测试：`go test ./internal/prompt -count=1`、`go test ./internal/appserver -run "TestRuntimeRouterSkillsContextFollowsModelUsageInstructionsFlagLikeRust|TestRuntimeRouterTurnStartInjectsAvailableSkills|TestRuntimeRouterExplicitSkillInstructionsPersistForNextTurnLikeRust|TestRuntimeRouterSkillsContextEmitsBudgetWarning" -count=1`、`go test ./internal/prompt ./internal/turn ./internal/appserver -count=1`。
  - 最新：补齐 Rust `ext/skills/src/provider/executor.rs` / `ext/skills/src/render.rs` 的 executor locator 展示语义：Go skill metadata 新增 `LocatorKind` / `LocatorPath`，selected capability remote skills 在 model-visible catalog 中渲染为 Rust 风格 `(environment resource: skill://<selected-root-id>/.../SKILL.md)`；内部 `environment://...` 路径继续用于远端内容读取，同时显式 skill mention 匹配可同时识别内部路径与 display locator，explicit skill fragment 的 `<path>` 使用 Rust `rendered_path()` 等价 display locator。覆盖测试：`go test ./internal/prompt -count=1`、`go test ./internal/appserver -run "TestRuntimeRouterTurnStartUsesRemoteEnvironmentSkillRoot|TestRuntimeRouterTurnStartWarnsAndSkipsInvalidRemoteEnvironmentSkillLikeRust|TestRuntimeRouterTurnStartUsesSelectedCapabilitySkillRoots|TestRuntimeRouterExplicitSkillInstructionsPersistForNextTurnLikeRust" -count=1`、`go test ./internal/prompt ./internal/turn ./internal/appserver -count=1`。
  - 最新：补齐 Rust `ext/skills/src/render.rs` / `ext/skills/src/extension.rs` 的 explicit skill main-prompt 截断：Go appserver 注入 `<skill>` fragment 前按 Rust 上限截断正文 8000 bytes、name 256 bytes、path 1024 bytes，并保证 UTF-8 字符边界；正文被截断时通过现有 warning 通道发出 Rust 文案 `Skill ... exceeded the main prompt context limit and was truncated.`。覆盖测试：`go test ./internal/appserver -run "TestRuntimeRouterSkillInstructionsInputItemTruncatesMainPromptLikeRust|TestRuntimeRouterTurnStartUsesRemoteEnvironmentSkillRoot|TestRuntimeRouterExplicitSkillInstructionsPersistForNextTurnLikeRust|TestRuntimeRouterImplicitSkillInvocationFromShellCommand" -count=1`。
  - 最新：已补 Rust `core-skills/src/loader.rs` 的本地 skill metadata 解析边界：Go 本地 loader 不再接受 Go-only YAML 别名（frontmatter `metadata.shortDescription`、openai.yaml `displayName`/`short-description`/`iconSmall`/`brand-color`/`defaultPrompt`、dependency `kind`/`name`/`mcpServer`、policy `allowImplicitInvocation` 等），只按 Rust snake_case / `metadata.short-description` 解析；合法 frontmatter 缺少 short description 时不再回退 description；缺失/损坏 frontmatter 或缺失 description 的本地 `SKILL.md` 不再按第一行 fallback 加载，而是按 Rust 跳过并在非 system scope 返回 `SkillErrorInfo`，system scope 静默忽略；frontmatter name 超长返回 Rust `invalid name` 错误，description 和 `metadata.short-description` 超过 1024 字符仍按 Rust 保留，交由 render 阶段预算截断。覆盖测试：`go test ./internal/appserver -run "Test(ListSkills|SkillsService|WriteConfig|SetExtraRoots|RemoteSkill|RuntimeRouterTurnStartUsesRemoteEnvironmentSkillRoot|RuntimeRouterTurnStartUsesSelectedCapabilitySkillRoots)" -count=1`、`go test ./internal/appserver -count=1`、`go test ./internal/prompt ./internal/appserver ./internal/app -run "Test.*Skill|Test.*Skills|TestRuntimeRouterDispatchesCatalogAPIs|TestListSkills|TestSkillsService|TestSetExtraRoots|TestWriteConfig" -count=1`。
  - 最新：已补 Rust `core-skills/src/loader/environment.rs` 的 remote environment invalid-skill 边界：Go remote environment skill 不再对缺失/损坏 frontmatter、缺少 description、超长 name 做第一行 fallback，而是像 Rust 一样跳过该 skill；metadata 解析仍只在合法 frontmatter 后执行。覆盖测试：`go test ./internal/appserver -run "TestRemoteSkill|TestRuntimeRouterTurnStartUsesRemoteEnvironmentSkillRoot|TestRuntimeRouterTurnStartUsesSelectedCapabilitySkillRoots|TestRuntimeRouterExplicitSkillInstructionsPersistForNextTurnLikeRust" -count=1`、`go test ./internal/appserver -count=1`。
  - 最新：继续补齐 Rust remote environment loader：`fs/walk` 参数改为 Rust `maxDepth=6`、`maxDirectories=2000`、`maxEntries=20000`、`followDirectorySymlinks=true`；接收 walk `errors/truncated` 并按 Rust 文案发 `warning` notification；invalid remote skill parse warning 使用 `Failed to load environment skill at ...`；remote entries 最终按 name/path 排序。覆盖测试：`go test ./internal/appserver -run "TestRemoteSkill|TestRemoteEnvironmentSkill|TestRuntimeRouterTurnStart(UsesRemoteEnvironmentSkillRoot|WarnsAndSkipsInvalidRemoteEnvironmentSkillLikeRust|UsesSelectedCapabilitySkillRoots)|TestRuntimeRouterExplicitSkillInstructionsPersistForNextTurnLikeRust" -count=1`、`go test ./internal/appserver -count=1`。
  - 最新：继续补齐 Rust remote environment plugin namespace：Go remote loader 现在从 walk inventory 中的 `.codex-plugin/plugin.json` / `.claude-plugin/plugin.json` 读取顶层 `name`，给其下 skill 加 `namespace:skill`，并按 Rust `qualified name` 128 字符限制跳过超长项；manifest name 为空时回退插件 root basename；namespaced skill 仍按 Rust name/path 排序。覆盖测试：`go test ./internal/appserver -run "Test(RemoteSkill|RemoteEnvironmentSkill|DiscoverRemoteEnvironmentSkills)" -count=1`、`go test ./internal/appserver -run "TestRuntimeRouterTurnStart(UsesRemoteEnvironmentSkillRoot|WarnsAndSkipsInvalidRemoteEnvironmentSkillLikeRust|UsesSelectedCapabilitySkillRoots)|TestRuntimeRouterExplicitSkillInstructionsPersistForNextTurnLikeRust" -count=1`、`go test ./internal/appserver -count=1`（首次完整包跑到无关 `TestCommandExecTTYStreamsAndResizes` PTY 输出等待失败，重跑通过）。
  - 最新：已补 Rust `filter_skill_load_outcome_for_product(..., Product::Codex)` 的产品过滤：Go 本地/插件/selected local discovery 和 remote environment discovery 现在只保留 `policy.products` 为空或包含 `codex` 的 skill，过滤 `chatgpt`/`atlas` 限定项。覆盖测试：`go test ./internal/appserver -run "Test(ListSkills|RemoteSkill|RemoteEnvironmentSkill|DiscoverRemoteEnvironmentSkills)" -count=1`、`go test ./internal/appserver -count=1`。
  - 最新：继续补齐 Rust `plugin_namespace_for_skill_uri` 的 ancestor probe：Go remote loader 现在会通过 `fs/getMetadata` 探测 selected root 及祖先的 `.codex-plugin/plugin.json` / `.claude-plugin/plugin.json`，读取 manifest name 后为子 skill 施加 namespace；remote exec-server 测试 helper 已支持 Rust-style `fs/getMetadata` probe。覆盖测试：`go test ./internal/appserver -run "Test(RemoteSkill|RemoteEnvironmentSkill|DiscoverRemoteEnvironmentSkills|RuntimeRouterTurnStartUsesRemoteEnvironmentSkillRoot|RuntimeRouterTurnStartWarnsAndSkipsInvalidRemoteEnvironmentSkillLikeRust)" -count=1`、`go test ./internal/appserver -count=1`。
  - 最新：继续补齐 Rust 本地 skill scan 边界：Go `discover` 现在按 Rust `MAX_SCAN_DEPTH=6` / `MAX_SKILLS_DIRS_PER_ROOT=2000` 限制本地 skill 遍历，depth 6 的 `SKILL.md` 保留、depth 7 跳过；remote walk 常量复用同一 depth/dir limit。覆盖测试：`go test ./internal/appserver -run "TestListSkills|TestSkillsService|TestSetExtraRoots|TestWriteConfig|TestRemoteSkill|TestDiscoverRemoteEnvironmentSkills" -count=1`、`go test ./internal/appserver -count=1`。
  - 最新：继续补齐 Rust 本地 symlink policy：Go 本地 loader 改为 skill 专用 walker，user/repo/admin/local root 跟随目录 symlink 并用 resolved identity 避免循环，system root 忽略 symlink 目录；保留点目录跳过、Rust depth/dir limit 和 invalid skill error 收集。覆盖测试：`go test ./internal/appserver -run "TestListSkills|TestSkillsService|TestSetExtraRoots|TestWriteConfig" -count=1`、`go test ./internal/appserver -count=1`。
  - 最新：继续补齐 Rust 本地 skill identity canonicalization：Go `entryFromPath` 现在对本地 skill path 执行 `EvalSymlinks`，发现 symlink 目录下的 skill 时暴露 target canonical path，失败时才 fallback clean path；symlink 测试同步校验 canonical path。覆盖测试：`go test ./internal/appserver -run "TestListSkills|TestSkillsService|TestSetExtraRoots|TestWriteConfig" -count=1`、`go test ./internal/appserver -count=1`。

## 工作纪律

- 每完成一个 TODO，更新本文件状态并补充验收命令。
- Go 代码改动必须优先复用现有 `internal/*` 包，不为绕过差异新增平行实现。
- 所有 user-visible 文案、JSON 字段、错误码和退出码都以 Rust 源码/fixture 为准。
- 平台不可实现项必须显式返回 Rust 风格 unsupported error，并有测试覆盖。
- 不因为 Go 实现“功能更强”而偏离 Rust 行为；增强只能放在 feature gate 或明确的后续提案中。
