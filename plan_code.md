# Codex Go 下一步 Rust 对齐开发计划

更新日期：2026-07-09

本文档基于 `plan_new.md`、`plan.md`、当前 Go 代码，以及本机 Rust 参考目录 `D:\qax\reagent\dev\codex-main\codex-rs` 制定。后续开发优先读本文件，再回看 `plan_new.md` 的工作日志。

## 结论

当前 Go 版已经不再是早期骨架：核心 turn/runtime、Responses streaming、app-server V2、TUI 大量 view model、session/rollout、MCP/plugin/sandbox/doctor 都已有主路径实现。`plan_new.md` 的最新判断约为 93% 综合对齐，更接近当前代码；`plan.md` 的 65-70% 是较早阶段估算，但其中“阶段 1 核心 Turn Runtime、阶段 6 App-Server V2 RPC 必须 100% 高保真”的约束仍然有效。

下一步不应再大面积铺新目录，而应按 Rust 测试面和协议 fixture 收口。优先级从高到低：

1. P0：App-server V2 schema/result/notification/error 全字段 fixture。
2. P0：CLI/exec/review Rust 测试翻译，尤其 parser/help/error/JSONL/human output/exit code。
3. P0：exec-server remote 严格对齐 Rust Noise relay、多路复用和 `streamResponse`。
4. P0：sandbox/network proxy 真实平台矩阵，避免 unsupported host 静默降级。
5. P1：provider/auth live matrix，补 ChatGPT OAuth、OS keyring、AWS role/web identity/IMDS。
6. P1：MCP/plugin/skills/apps/connectors live 与 error mapping fixture。
7. P1：TUI 真终端 smoke、remote client 长尾和 golden/snapshot 收口。
8. P2：doctor/install/update/telemetry/code-mode/external-agent migration 的 snapshot 与 live-gated 测试。

## 当前代码观察

- Go 工作树已有未提交改动：`internal/tui/tea/model.go`、`internal/tui/tea/model_test.go`、`internal/turn/agent_loop.go`、`internal/turn/agent_loop_test.go`、`plan_new.md`。后续开发不要回滚这些改动，继续在其基础上工作。
- `plan_code.md` 是新增计划文档，不替代 `plan_new.md` 的历史账本。
- Rust 参考目录包含完整 workspace，关键测试面包括：
  - `cli/tests/*.rs`：16 个文件。
  - `exec/tests/suite/*.rs`：15 个文件。
  - `app-server/tests/suite/v2/*.rs`：84 个文件。
  - `exec-server/tests/*.rs`：13 个文件。
  - `tui/tests/suite/*.rs`：5 个文件。
  - `app-server-protocol/tests/schema_fixtures.rs`。
  - `network-proxy/src/*_tests.rs`。
- Go 代码仍有显式差异信号：
  - `internal/app/app.go` 保留通用 `notImplemented` helper。
  - `internal/exec/exec.go` 对 `exec` 非 `review/resume` 子命令的旧 `not implemented` 文案已改为 Rust-style unknown defensive error。
  - `internal/exec/exec.go` 和 `internal/appserver/agent_runtime.go` 仍保留 `LocalAgentRunner` fallback，默认 exec 已按计划走真实 runner，但 app-server 无凭据 fallback 仍要按 Rust 行为重新审计。
  - `internal/app/remote_tui.go` 对未知 server request 与 Rust `PendingAppServerRequests` unsupported 长尾已统一为 `-32000` reject；`currentTime/read`、legacy `applyPatchApproval`/`execCommandApproval` 不再伪装为 TUI 可用。
  - `internal/sandbox/windowssandbox/types.go` 的旧 `Go port yet` backend 文案已清理为 neutral unavailable；后续仍需继续补 Windows/Linux gated 真机矩阵，确保 unsupported host 是显式 skip/reason，而不是静默降级。

## Rust/Go 覆盖矩阵

| 优先级 | Rust 参考 | Go 位置 | 当前判断 | 下一步 |
| --- | --- | --- | --- | --- |
| P0 | `app-server`、`app-server-protocol`、`app-server-transport` | `internal/appserver`、`internal/app` | 主体深度对齐，仍缺 result/notification/error 全字段 diff | 以 Rust schema 和 `tests/suite/v2` 建 Go fixture |
| P0 | `cli`、`exec`、`utils/cli`、`arg0` | `cmd/codex`、`internal/cli`、`internal/app`、`internal/exec` | 命令树主路径可用，fixture 仍不足 | 翻译 Rust CLI/exec 测试，锁 stdout/stderr/exit code |
| P0 | `core`、`core-api`、`protocol`、`tools` | `internal/turn`、`internal/tool`、`internal/protocol` | 主链路强，缺 Rust tool suite 全量回归 | 对 apply_patch/approval/hooks/sandbox/MCP required 建 fixture |
| P0 | `exec-server`、`exec-server-protocol`、`network-proxy` | `internal/execserver`、`internal/network`、`internal/app` | remote registration 最小闭环已接通 | 对齐 Noise relay、multiplex、streamResponse、proxy policy |
| P0 | `sandboxing`、`linux-sandbox`、`windows-sandbox-rs` | `internal/sandbox` | unit/smoke 有基础 | 建 Windows/Linux gated 真机矩阵 |
| P1 | `model-provider`、`codex-api`、`chatgpt`、`aws-auth` | `internal/model`、`internal/codexapi`、`internal/chatgptapi`、`internal/auth` | Responses runner 与 provider 基础可用 | request body golden + live provider/auth matrix |
| P1 | `mcp-server`、`codex-mcp`、`rmcp-client` | `internal/mcp`、`internal/appserver` | stdio/HTTP/OAuth 已有大量路径 | live stdio/HTTP/SSE/OAuth + error mapping |
| P1 | `plugin`、`skills`、`connectors`、`ext/*` | `internal/plugin`、`internal/prompt`、`internal/apps`、`internal/tool` | 主路径已有 | remote marketplace/materialization/telemetry/connector accept |
| P1 | `tui`、`collaboration-mode-templates` | `internal/tui`、`internal/app/interactive.go` | 主路径和大量纯逻辑已对齐 | 真终端 smoke、remote client 长尾、golden |
| P2 | `cli/src/doctor`、`install-context`、`otel`、`analytics` | `internal/doctor`、`internal/install`、`internal/telemetry` | doctor 主体已深 | snapshot fixture、OTEL/export/install live |
| P2 | `external-agent-*`、`realtime-webrtc`、`code-mode`、`file-*` | `internal/agent`、`internal/realtime`、`internal/codemode`、`internal/filesearch` | 基础服务存在 | migration ledger、IDE integration、大仓库性能 |

## P0 开发顺序

### 1. App-server V2 schema 与协议收口

Rust source：
`app-server-protocol/tests/schema_fixtures.rs`、`app-server-protocol/schema/typescript/v2/*`、`app-server/tests/suite/v2/*`、`app-server/src/request_processors/*`、`app-server/src/error_code.rs`。

Go target：
`internal/appserver/schema.go`、`protocol.go`、`router.go`、`runtime_router.go`、各 service/processor 测试。

任务：

- 扩展现有 schema diff，从 method/params 扩到 params/result/server request/notification/thread item/error envelope 全字段。
- 对 `ThreadItem` union 建样本：message、reasoning、commandExecution、fileChange、mcpToolCall、dynamicToolCall、webSearch、image/imageGeneration、sleep、contextCompaction、entered/exitedReviewMode、subAgentActivity。
- 锁定 JSON-RPC 错误码和业务 error `data/code`：thread、turn、command/process、fs、config、MCP、plugin、account、feedback、realtime。
- 把 Rust `app-server/tests/suite/v2` 按服务分批翻译：先 thread/turn/process/fs/config/mcp，再 plugin/skills/apps/realtime/windows_sandbox。
- 建 SDK contract smoke：Python/TypeScript 至少覆盖 initialize、thread start/read/list、turn start/steer/cancel、command exec、MCP call、shutdown。

验收：

- `go test ./internal/appserver -run "Test(BuildProtocolSchema|BuildTypeScriptProtocolSchema|ProtocolPayloadsValidateAgainstRustSchemas)" -count=1`
- `go test ./internal/appserver -count=1`
- 新增 Rust v2 suite 翻译测试默认可跑；平台相关用明确 gated env skip。

### 2. CLI/exec/review fixture 收口

Rust source：
`cli/src/main.rs`、`cli/tests/*.rs`、`exec/src/cli.rs`、`exec/src/event_processor_with_jsonl_output.rs`、`exec/src/event_processor_with_human_output.rs`、`exec/tests/suite/*.rs`。

Go target：
`internal/cli`、`internal/app`、`internal/exec`、`internal/review`、`cmd/codex`。

任务：

- 翻译 Rust `cli/tests`：login、features、debug_models、debug_clear_memories、exec_server、execpolicy、delete、plugin_cli、mcp、marketplace、app_server、update、sandbox_network_proxy。
- 翻译 Rust `exec/tests/suite`：agents_md、prompt_stdin、output_schema、auth_env、ephemeral、resume、server_error_exit、approval_policy、apply_patch、hooks、sandbox、mcp_required_exit、add_dir、originator。
- 锁定 human output、JSONL events、final JSON、stderr warning、exit code。
- 复核 `internal/exec/exec.go` 的子命令 fallback：已确认 Rust 仅支持 `review/resume`，Go 直接 runner 防御错误已改为 unknown；继续补 CLI/exec output fixture。
- `review` 独立补 diff/base/commit/uncommitted/title/output fixture。

验收：

- `go test ./internal/cli ./internal/app ./internal/exec ./internal/review -count=1`
- `go test ./cmd/codex -count=1`
- 默认 `codex exec` 不出现 `Go Codex exec stub received`，除显式测试/离线注入。

### 3. Core tool/turn/runtime 高保真 fixture

Rust source：
`core/src/tools/**`、`core/src/context/**`、`core/src/tools/runtimes/**`、`core/src/tools/handlers/**`。

Go target：
`internal/turn`、`internal/tool`、`internal/shell`、`internal/applypatch`、`internal/context`、`internal/prompt`。

任务：

- 翻译 Rust tool suite：shell、unified_exec、apply_patch、tool_search、MCP resource/tool、agent jobs、multi_agents_v2、request_user_input、request_permissions、plan、sleep/current_time、new_context_window、get_context_remaining。
- 锁定 tool output/error JSON shape：fatal/non-fatal、model-visible、retry context、approval request、hook additional context。
- 补 network approval、approval amendment、非 shell tool approval、session-level policy persistence。
- 审计 app-server `LocalAgentRunner` fallback：无凭据、provider 不需要 auth、测试注入、remote compact 等场景要和 Rust 一致，不能把真实路径静默降级成本地 stub。

验收：

- `go test ./internal/turn ./internal/tool ./internal/shell ./internal/applypatch ./internal/context ./internal/prompt -count=1`
- 新增 fixture 明确覆盖 fatal/non-fatal/error/retry/approval/hook 分支。

### 4. Exec-server、sandbox、network proxy 系统能力

Rust source：
`exec-server/src/*`、`exec-server/tests/*`、`network-proxy/src/*`、`sandboxing`、`linux-sandbox`、`windows-sandbox-rs`、`cli/src/debug_sandbox.rs`。

Go target：
`internal/execserver`、`internal/network`、`internal/sandbox`、`internal/sandbox/windowssandbox`、`internal/app`。

任务：

- Exec-server：按 Rust `tests/relay.rs`、`relay_noise_tests.rs`、`websocket.rs`、`process.rs`、`exec_process.rs`、`file_system_*`、`http_request.rs` 建 Go 对应测试。
- 对齐 remote registration 后的 Noise relay、多路复用、streamResponse、client recovery、trace context、selected capability roots。
- Network proxy：实现/锁定 credential broker、MITM、native certs、policy reload、upstream/http/socks5、sandbox no-network env。
- Windows sandbox：restricted token、elevated setup/provision、WFP/firewall、ACL deny/read/workspace、ConPTY resize/interrupt/output drain。
- Linux sandbox：bwrap、Landlock/seccomp、execve wrapper FD passing、unsupported/fallback 文案。

验收：

- 默认 unit 测试不依赖特权或外部网络。
- 真机测试全部 gated，例如 `CODEX_GO_SANDBOX_SMOKE=1`、`CODEX_GO_TUI_PTY_SMOKE=1`。
- unsupported host 必须 skip 或返回 Rust 同款错误，不能静默全权限执行。

## P1 开发顺序

### 5. Provider/auth/account live matrix

Rust source：
`model-provider`、`codex-api`、`chatgpt`、`login`、`keyring-store`、`aws-auth`、`ollama`、`lmstudio`、`cloud-config`。

Go target：
`internal/model`、`internal/codexapi`、`internal/chatgptapi`、`internal/auth`、`internal/config`、`internal/doctor`。

任务：

- 建 request body golden：OpenAI Responses、ChatGPT、OSS、Ollama、LM Studio、Azure Responses endpoint、Bedrock。
- Auth matrix：API key、ChatGPT OAuth/device-code、external `chatgptAuthTokens`、PAT、agent identity、auth.command、forced workspace/login method。
- Keyring：从进程内/文件 fallback 推进到 OS keychain/secrets 或明确兼容层，并补失败/回退 fixture。
- AWS：role、web identity、SSO、IMDS、SigV4、Bedrock bearer live-gated。
- Rate limit、retry、stream idle timeout、debug context、prompt cache key、service tier golden。

验收：

- `go test ./internal/model ./internal/codexapi ./internal/chatgptapi ./internal/auth ./internal/config -count=1`
- live-gated 测试无凭据清晰 skip，有凭据覆盖真实请求。

### 6. MCP/plugin/skills/apps/connectors

Rust source：
`mcp-server`、`codex-mcp`、`rmcp-client`、`plugin`、`core-plugins`、`skills`、`core-skills`、`connectors`、`ext/*`。

Go target：
`internal/mcp`、`internal/plugin`、`internal/prompt`、`internal/apps`、`internal/tool`、`internal/appserver`。

任务：

- MCP live：stdio、streamable HTTP、SSE、roots/list、elicitation、progress、resources/resourceTemplates/tools。
- MCP OAuth：discovery、dynamic registration、PKCE、callback server、refresh、invalid_grant cleanup、cancel。
- MCP session cache：HTTP DELETE、stale 404/410 rebuild、stdio process reuse、config reload cleanup。
- Plugin marketplace：local/git/remote add/read/install/uninstall/share/upgrade/materialize/cache rebuild。
- Skills：frontmatter、`openai.yaml` aliases、remote skill root、dependencies、implicit invocation、budget、telemetry。
- Apps/connectors：ChatGPT directory live、accessibility cache、connector accept force refresh、synthetic/disabled filtering。

验收：

- `go test ./internal/mcp ./internal/plugin ./internal/apps ./internal/prompt ./internal/tool -count=1`
- 每个 live 类测试都有 mock、fixture、live-gated 三层之一，不能只靠手测。

### 7. TUI 与 remote interactive 长尾

Rust source：
`tui/src/lib.rs`、`tui/src/app/*`、`tui/src/chatwidget/*`、`tui/tests/suite/*`、`tui/tests/fixtures/*`。

Go target：
`internal/tui`、`internal/tui/tea`、`internal/tui/chatwidget`、`internal/tui/bottom_pane`、`internal/tui/history_cell`、`internal/app/interactive.go`、`internal/app/remote_tui.go`。

任务：

- 真实终端 smoke：Windows ConPTY host 输出完整通过记录、Unix PTY alt-screen restore、interrupt/resize/focus/output drain。
- remote TUI：Rust `PendingAppServerRequests` unsupported 长尾已落地；后续重点转向真实终端 smoke、snapshot fixture 和剩余 remote client 交互细节。
- remote/local 共用 app-server/turn/runtime 语义，补 slash session actions、background terminal panel、diff/file change display、compact/rollback/continue/steer。
- 把已存在的轻量接口壳逐步替换为 Rust 完整业务实现，并补 golden/snapshot。
- 复核 `/import` remote unavailable、IDE context、external editor handoff、clipboard、pets/image/sixel 等小面。

验收：

- `go test ./internal/tui ./internal/tui/chatwidget ./internal/tui/tea ./internal/tui/bottom_pane ./internal/tui/streaming ./internal/tui/history_cell ./internal/tui/exec_cell -count=1`
- gated terminal smoke 在支持宿主上有完整通过记录；不支持宿主明确 skip reason。

## P2 开发顺序

### 8. Session/rollout/state/migration

Rust source：
`rollout`、`rollout-trace`、`thread-store`、`message-history`、`state`、`memories`、`external-agent-*`。

Go target：
`internal/rollout`、`internal/session`、`internal/history`、`internal/state`、`internal/memories`、`internal/agent`。

任务：

- 导入 Rust rollout JSONL fixtures：message、tool、reasoning、diff、compact、review、agent/subagent。
- 对 Go 生成 rollout 做 Rust-compatible snapshot。
- 补 thread list/search cursor、sourceKinds、relation filter、sortKey/sortDirection、limit clamp、historyMode。
- 补 state/memories/history 旧文件迁移、Windows handle cleanup、path canonical、并发读写。
- 补 external-agent sessions ledger/migration fixture。

验收：

- `go test ./internal/rollout ./internal/session ./internal/history ./internal/state ./internal/memories ./internal/agent -count=1`

### 9. Doctor/install/update/telemetry/code-mode

Rust source：
`cli/src/doctor/**`、`install-context`、`otel`、`analytics`、`realtime-webrtc`、`code-mode`、`file-search`、`file-system`、`file-watcher`。

Go target：
`internal/doctor`、`internal/install`、`internal/telemetry`、`internal/realtime`、`internal/codemode`、`internal/filesearch`、`internal/appserver`。

任务：

- Doctor snapshot：auth、config、git、terminal、state、rollout DB parity、sandbox、MCP、network、provider、installation、updates。
- Install/update：npm/bun/cargo/managed package root proof、update target、mismatch、download/reexec。
- Telemetry/eventmap：Rust event map diff、privacy boundary、accepted lines、memory usage、OTEL exporter mock/live。
- Realtime：WebRTC/auth/app-server RPC integration。
- Code mode：protocol fixture、host lifecycle、IDE integration。
- File search/fs/watch：ignore rules、大仓库性能、remote environment edge。

验收：

- `go test ./internal/doctor ./internal/install ./internal/telemetry ./internal/realtime ./internal/codemode ./internal/filesearch -count=1`

## 验证基线

每轮最小验证：

```powershell
go list -buildvcs=false ./...
```

P0 改动建议验证：

```powershell
go test ./internal/appserver -count=1
go test ./internal/cli ./internal/app ./internal/exec ./internal/review -count=1
go test ./internal/turn ./internal/tool ./internal/shell ./internal/applypatch -count=1
go test ./internal/execserver ./internal/network ./internal/sandbox -count=1
```

大范围收口验证：

```powershell
go test ./... -count=1
```

已知 Windows 抖动：

- `internal/appserver/TestProcessServiceSpawnTTYStreamsAndResizes` 曾出现 ConPTY 时序抖动，单包重跑通过。
- TUI ConPTY smoke 默认 gated skip，只有宿主可读输出时才能完整断言 alt-screen enter/leave。

## 下一轮建议任务

建议下一轮直接做 P0.1，不要再扩散到 TUI 长尾：

1. 读取 Rust `app-server-protocol/tests/schema_fixtures.rs` 和 `schema/typescript/v2/ThreadItem.ts` 相关 union。
2. 扩展 Go `TestProtocolPayloadsValidateAgainstRustSchemas`，新增 ThreadItem 全 union 样本。
3. 补 Go `ThreadItem` marshal/build/replay 缺字段，直到 fixture 通过。
4. 跑 `go test ./internal/appserver -run "Test(ProtocolPayloadsValidateAgainstRustSchemas|BuildTypeScriptProtocolSchema)" -count=1 -v`。
5. 更新 `plan_new.md` 工作日志，把新样本、通过命令和剩余 union 差异写回。

这样做收益最高：它同时服务 app-server、remote TUI、SDK、session replay 和后续 CLI/exec 输出稳定性，是当前剩余 7% 的主承重点。

## 2026-07-09 执行进度与下一步修订

已完成：

- P0.1 ThreadItem union fixture 已落地：Go `TestProtocolPayloadsValidateAgainstRustSchemas` 已覆盖 Rust `ThreadItem` 全 union 样本、`ItemStartedNotification`、`ItemCompletedNotification`，并修复 JSON Schema boolean node 校验。
- P0.1 result/notification fixture 已扩展：JSON-RPC envelope、thread read/list/resume/fork/rollback/metadata、command exec 控制面、fs params/results/changed notification 已加入 Rust schema 校验。
- P0.1 MCP schema fixture 已扩展：`ListMcpServerStatusParams/Response`、`McpResourceReadParams/Response`、`McpServerToolCallParams/Response`、`McpServerElicitationRequestParams/Response` form/url 分支已加入 Rust schema 校验。
- Rust MCP 行为补充回归已落地：status list 保留 raw server/tool name，HTTP tool call 透传 `structuredContent`、`isError:false`、`_meta`，同时 request `_meta.threadId` 覆盖为 live thread id。
- 当前验证：`go test ./internal/appserver -count=1` 与 `go test ./internal/mcp -count=1` 均通过。

新的下一步建议：

1. 继续 P0 App-server V2，优先补业务错误 `data/code` fixture：thread/turn not found、command/process invalid params、fs errors、MCP required/unavailable、plugin/account/feedback/realtime errors。
2. 翻译 Rust `app-server/tests/suite/v2/config_rpc.rs`、`thread.rs`、`turn.rs`、`mcp_*` 中剩余 router-level 行为，按服务分批进入 Go `internal/appserver` 测试。
3. 建 SDK contract smoke：initialize、thread start/read/list、turn start/steer/cancel、command exec、MCP call、shutdown，先 mock app-server 再接真实 router。
4. P0.2 并行启动 CLI/exec/review fixture：从 Rust `cli/tests` parser/help/error/exit code 开始，避免继续只靠真实运行 smoke。

## 2026-07-09 App-server Rust Parity Update

Completed:

- MCP remote JSON-RPC errors are now locked at router level: Go preserves the remote `error.code`, message, decoded `data`, and Rust-compatible `error.data.type=mcp_remote_error` fields for tool calls and resource reads.
- `command/exec` control errors now match Rust's two-path behavior: missing/finished client process ids return `command/exec "id" is no longer running`, while cross-connection control attempts return `no active command/exec for process id "id"` and leave the owning connection's process alive.
- Verification passed: `go test ./internal/appserver -run "TestCommandExecSessionsAreConnectionScoped|TestCommandExecConnectionClosedCancelsOnlyThatConnection|TestRuntimeRouterCommandExec|TestRuntimeRouterCommandExecInvalidRequestAndParamsCodes|TestCommandExecStreamingSessionOperations|TestRuntimeRouterMCPRemoteErrorsIncludeRustErrorData" -count=1 -v`; `go test ./internal/appserver -count=1`.

Next priority:

1. Continue translating Rust `app-server/tests/suite/v2` router behavior, starting with `thread.rs`, `turn.rs`, `mcp_*.rs`, and remaining config error data cases.
2. Add SDK contract smoke over the real Go router for initialize, thread start/read/list, turn start/cancel/steer, command exec, MCP call, and shutdown.
3. Backfill fs/plugin/account/feedback/realtime error `data/code` fixtures after the high-traffic app-server paths are locked.
4. Then resume P0.2 CLI/exec/review parser and output fixtures from Rust `cli/tests` and `exec/tests`.

## 2026-07-09 Rust parity progress - thread unarchive response shape

Completed:

- Reviewed Rust `app-server/tests/suite/v2/thread_unarchive.rs`.
- Go already matched the core rollout behavior for archive -> unarchive -> delete; added router-level regression coverage for the Rust response wire details.
- `TestRouterArchiveUnarchiveAndDeleteRolloutOnlyThread` now asserts the unarchived thread returns `status.type = "notLoaded"` and serializes an explicit `thread.name: null` when no title is present.
- Verification passed: `go test ./internal/appserver -run "TestRouterArchiveUnarchiveAndDeleteRolloutOnlyThread|TestRuntimeRouterThreadArchiveDeleteUnloadRuntimeStatus" -count=1 -v`.

Next priority:

1. Continue scanning Rust `app-server/tests/suite/v2/thread_*.rs` for small response-shape deltas before moving to broader turn/config error data.
2. Keep prioritizing executable fixtures over manual checklist updates when Go behavior is already mostly aligned.

## 2026-07-09 Rust parity progress - thread start wire shape

Completed:

- Reviewed Rust `app-server/tests/suite/v2/thread_start.rs` for the persistent thread response and `thread/started` notification contract.
- Go router coverage now locks the Rust response details: `thread.name: null`, `thread.ephemeral: false`, and no top-level `sessionId` on `thread/start`.
- Go runtime-router coverage now locks the Rust notification details: `thread.name: null`, `thread.ephemeral: false`, `thread.threadSource = "user"`, and no top-level `sessionId` inside notification params.
- Verification passed: `go test ./internal/appserver -run "TestRouterStartReadListAndItems|TestRuntimeRouterThreadStartStartedNotificationMatchesRustWireShape|TestRuntimeRouterInitializeOptOutNotificationMethodsFiltersThreadStarted" -count=1 -v`.

Next priority:

1. Continue with Rust `thread_rollback.rs` and `thread_read.rs` name/sessionId wire-shape fixtures.
2. Then scan remaining thread lifecycle tests for error `data/code` gaps and loaded/not-loaded status deltas.

## 2026-07-09 Rust parity progress - thread rollback response shape

Completed:

- Reviewed Rust `app-server/tests/suite/v2/thread_rollback.rs`.
- Go rollout-only rollback coverage now locks the Rust response details: `thread.name: null` for an unset title and `thread.sessionId` preserved from the rollout metadata.
- Verification passed: `go test ./internal/appserver -run "TestRouterInjectItemsAndRollbackRepairRolloutOnlyThread|TestRouterSearchLoadedTurnsRollbackAndInjectItems|TestRuntimeRouterThreadRollback" -count=1 -v`.

Next priority:

1. Continue with Rust `thread_read.rs` and `thread_resume.rs` title/name serialization cases.
2. Then move into lifecycle error data and pathless store metadata coverage.

## 2026-07-09 Rust parity progress - thread name read/list/resume shape

Completed:

- Reviewed Rust `app-server/tests/suite/v2/thread_read.rs::thread_name_set_is_reflected_in_read_list_and_resume`.
- Go `thread/name/set` coverage now locks the Rust-visible title propagation through `thread/read`, `thread/list`, and `thread/resume` JSON payloads.
- Go `thread/list` coverage also asserts `thread.ephemeral: false` remains serialized for the named persistent thread.
- Verification passed: `go test ./internal/appserver -run "TestRouterSetNameAndMetadata|TestRuntimeRouterSetNameLifecycleNotifications|TestRouterMetadataWritesMissingThreadUseRustErrors" -count=1 -v`.

Next priority:

1. Continue with Rust pathless thread store metadata cases in `thread_read.rs` and `thread_unarchive.rs`.
2. Then move to remaining lifecycle error `data/code` fixtures.

## 2026-07-09 Rust parity progress - pathless thread metadata

Completed:

- Reviewed Rust `app-server/tests/suite/v2/thread_read.rs` and `thread_unarchive.rs` pathless store metadata cases.
- Go now has router regressions for store-only threads with no rollout JSONL: `thread/read` and `thread/list` serialize explicit `path: null`, preserve empty `preview`, and preserve `thread.name`.
- Go now has router regression coverage for archived pathless store threads: `thread/unarchive` preserves `path: null`, `forkedFromId`, `name`, and empty preview.
- Verification passed: `go test ./internal/appserver -run "TestRouterThread(ReadAndListPreservePathlessStoreMetadata|UnarchivePreservesPathlessStoreMetadata)|TestRouterReadAndResumeFallbackToRollout" -count=1 -v`.

Next priority:

1. Continue with Rust `thread_archive.rs` and `thread_delete.rs` lifecycle error/wire cases.
2. Then sweep `thread_resume.rs` for redaction and archived/path-specific deltas.

## 2026-07-09 Rust parity progress - archive/delete empty results

Completed:

- Reviewed Rust `app-server/tests/suite/v2/thread_archive.rs` and `thread_delete.rs` response handling.
- Go router coverage now asserts `thread/archive` and `thread/delete` serialize empty result objects `{}` while retaining Go-only unexported lifecycle IDs for runtime notification ordering.
- Verification passed: `go test ./internal/appserver -run "TestRouterArchiveUnarchiveAndDelete$|TestRouterThread(ReadAndListPreservePathlessStoreMetadata|UnarchivePreservesPathlessStoreMetadata)|TestRuntimeRouterThread(ArchiveDeleteUnloadRuntimeStatus|ArchiveDeleteSpawnedDescendants)" -count=1 -v`.

Next priority:

1. Sweep `thread_resume.rs` redaction/path/archive cases.
2. Then move to config/error data fixtures if no smaller thread lifecycle gaps remain.

## 2026-07-09 Rust parity progress - thread resume remote redaction

Completed:

- Reviewed Rust `app-server/tests/suite/v2/thread_resume.rs::thread_resume_redacts_payloads_for_chatgpt_remote_clients`.
- Fixed Go `thread/resume` redaction so ChatGPT remote client payload redaction applies to `initialTurnsPage.data` as well as `thread.turns`.
- Added router regression coverage for remote clients redacting MCP arguments/results, dropping structured/meta payloads, and filtering `imageGeneration` items across both response surfaces; non-remote clients keep the original payloads.
- Verification passed: `go test ./internal/appserver -run "TestRouterThreadResumeRedactsRemoteClientInitialTurnsPage|TestRouterResumeInitialTurnsPageWithExcludeTurns|TestRouterResumeHistoryInitialTurnsPageWithExcludeTurns" -count=1 -v`.

Next priority:

1. Continue sweeping `thread_resume.rs` path mismatch/stale path and running-thread resume cases.
2. Then move to config/error data fixtures.

## 2026-07-09 Rust parity progress - config batch write legacy profile rejection

Completed:

- Reviewed Rust `app-server/tests/suite/v2/config_rpc.rs::config_batch_write_rejects_legacy_profile_tables`.
- Added runtime-router regression coverage that `config/batchWrite` rejects legacy `profiles.*` writes with `config_write_error_code = configValidationError`.
- The same regression asserts batch writes are atomic for this failure class: earlier valid edits are not persisted when a later legacy profile edit fails validation.
- Verification passed: `go test ./internal/appserver ./internal/config -run "TestRuntimeRouterConfig(WriteErrorDataMatchesRust|RejectsLegacyProfileWrite|BatchWriteRejectsLegacyProfilesAtomicallyLikeRust)|TestServiceWriteValueValidation" -count=1 -v`.

Next priority:

1. Continue config RPC origin/layer parity for tools/apps/model defaults.
2. Then move to remaining app-server error data fixtures.

## 2026-07-09 Rust parity progress - config origins for arrays/tools/apps

Completed:

- Reviewed Rust `app-server/tests/suite/v2/config_rpc.rs::config_read_includes_tools` and `config_read_includes_apps`.
- Fixed Go config origin generation so array elements get indexed origin paths, for example `tools.web_search.allowed_domains.0`, matching Rust.
- Added config service regression coverage for web search tool config origins and apps approval/destructive/default-tool approval origins.
- Verification passed: `go test ./internal/config -run "TestServiceRead(ConfigWithLayersAndOrigins|ToolsAndAppsOriginsMatchRustConfigRPC)|TestConfigReadResponseMarshalRustShape" -count=1 -v`.

Next priority:

1. Continue config RPC nested web-search/forced workspace ID cases.
2. Then return to app-server v2 error data fixtures.

## 2026-07-09 Experimental Feature API Update

Completed:

- `experimentalFeature/enablement/set` now matches Rust's map contract: primary params are `enablement: {name: bool}`, invalid feature names are ignored, and the response returns `enablement` with only the effective changes from the current request.
- Legacy Go `enabled`/`disabled` input remains accepted for compatibility, but outgoing JSON is Rust-shaped.
- Verification passed: `go test ./internal/features ./internal/appserver -run "Test(SetEnablementIgnoresUnknownKeys|FeatureWireShapeMatchesRust|RuntimeRouterDispatchesExperienceAPIs)" -count=1 -v`; `go test ./internal/features ./internal/appserver -count=1`.

Next priority remains app-server v2 router/API contract: continue with model/provider capabilities, config feature read/write interactions, and SDK smoke over initialize -> thread -> turn -> MCP -> shutdown.

## 2026-07-09 Model Provider Capabilities Contract

Completed:

- Added app-server router contract coverage for `modelProvider/capabilities/read` matching Rust fixtures: default provider returns `namespaceTools=true`, `imageGeneration=true`, `webSearch=true`; Bedrock-style provider returns `namespaceTools=true`, `imageGeneration=false`, `webSearch=false`.
- Verification passed: `go test ./internal/model ./internal/appserver -run "TestProviderCapabilities|TestRuntimeRouterModelProviderCapabilitiesReadMatchesRust" -count=1 -v`.
- Broader `go test ./internal/model ./internal/features ./internal/appserver -count=1` first hit the known Windows PTY timing flake in `TestCommandExecTTYStreamsAndResizes`; immediate `go test ./internal/appserver -count=1` rerun passed.

Next SDK/contract step:

- No standalone Go SDK package is present; build the smoke at app-server transport/router level or on top of `internal/appserverdaemon/client.go`, covering initialize, thread start/read/list, turn start/steer/cancel, command exec, MCP call, and shutdown/close semantics.

## 2026-07-09 SDK Contract Smoke Update

Completed:

- Added `TestRuntimeRouterSDKContractSmoke` as the first transport-agnostic SDK/IDE contract smoke over the real Go `RuntimeRouter`.
- Covered one initialized connection through `initialize`, `thread/start`, `thread/read`, `thread/list`, `turn/start`, buffered `command/exec`, and HTTP MCP `mcpServer/tool/call`.
- The smoke locks MCP's initialize + initialized notification + tools/resources/resourceTemplates inventory + tool call sequence.
- Verification passed: `go test ./internal/appserver -run TestRuntimeRouterSDKContractSmoke -count=1 -v`; `go test ./internal/appserver -count=1` passed on immediate rerun after a known Windows TempDir cleanup flake.

Remaining SDK contract work:

- Add transport/daemon-level smoke for websocket or daemon client behavior.
- Cover `turn/steer` and `turn/interrupt` while a turn is active.
- Cover shutdown/close semantics at transport or daemon level, because Go app-server currently has no `shutdown` JSON-RPC method.

## 2026-07-09 SDK Smoke Expansion

Completed:

- Expanded `TestRuntimeRouterSDKContractSmoke` to include `turn/steer` and `turn/interrupt` while a turn is active, using the existing blocking runtime agent fixture.
- Verification passed: `go test ./internal/appserver -run TestRuntimeRouterSDKContractSmoke -count=1 -v`; `go test ./internal/appserver -count=1`.
- Reviewed Rust `current_time`, `rate_limits`, and `rate_limit_reset_credits` fixtures against Go coverage; Go already has matching current-time request routing and account rate-limit/reset-credit auth/error/wire-shape coverage, so no duplicate fixture was added in this pass.

## Turn Steer Metadata Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/client_metadata.rs::turn_steer_updates_client_metadata_on_follow_up_responses_request_v2`.
- Go changes:
  - `internal/turn/SteerMailbox` now stores optional `ClientMetadata` alongside steer input items and exposes `DrainWithMetadata` while preserving the old `Drain` API.
  - `internal/turn/Runtime` and `AgentLoop` replace subsequent sampling metadata when drained steer metadata is present.
  - `internal/appserver` builds full Responses API client metadata for `turn/steer` using config metadata plus steer-provided `responsesapiClientMetadata`, active turn id, thread id, started timestamp, installation id, and responses-lite flag.
- Verification:
  - `go test ./internal/turn -count=1`
  - `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartPassesResponsesAPIClientMetadata|TurnSteerDeliveredToNextAgentSampling|DispatchesExperienceAPIs|SDKContractSmoke)" -count=1 -v`

## Account Wire Shape Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/account.rs` get_account and account/login/completed cases.
- Go changes:
  - Added full `GetAccountResponse` JSON wire-shape coverage for `requiresOpenaiAuth` and account union variants.
  - Strengthened appserver account login/cancel notification payload assertions.
- Verification:
  - `go test ./internal/auth -run "TestGetAccountResponseMarshalRustUnionShape" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterAccountCancelAndLogoutNotify" -count=1 -v`

## Thread Status Notification Rust Parity Update
- Status: implemented / verified.
- Rust reference: `app-server/tests/suite/v2/thread_status.rs`.
- Go changes:
  - Added runtime-router coverage proving `thread/status/changed` emits `active` when a turn starts and later emits `idle` after turn completion.
  - Reused existing opt-out coverage for `optOutNotificationMethods=["thread/status/changed"]`.
- Adjacent review:
  - `thread_unsubscribe.rs` behavior is already covered by Go tests for connection-scoped subscriptions, repeat unsubscribe -> `notSubscribed`, cold thread -> `notLoaded`, and runtime loaded-list behavior.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouter(ThreadStatusChangedEmitsActiveThenIdle|InitializeOptOutNotificationMethodsFiltersStatusChanged|TurnFailureClearsActiveStateAndAllowsNextTurn)" -count=1 -v`

## Thread Turns ItemsView Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/thread_read.rs::thread_turns_list_supports_requested_items_view`.
- Go changes:
  - Added router regression coverage for `thread/turns/list` with `itemsView=full|summary|notLoaded` on a multi-item turn.
  - Confirms `summary` reduces items to the user input plus latest assistant output, and `notLoaded` clears items while preserving metadata.
- Verification:
  - `go test ./internal/appserver -run "TestRouterThreadTurnsListSupportsRequestedItemsView" -count=1 -v`
  - Adjacent router/runtime turns-list tests passed.

## Permission Profile List Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/permission_profile_list.rs`.
- Go changes:
  - `permissionProfile/list` now builds its response from the effective app-server config for the request `cwd`, so trusted project `.codex/config.toml` permission profiles are discoverable.
  - Built-in profile ordering now matches Rust: `:read-only`, `:workspace`, `:danger-full-access`, followed by configured profile ids sorted lexicographically.
  - Built-in `PermissionProfileSummary` JSON keeps Rust's null description shape and continues to hide legacy `sandboxMode`/`network` fields.
  - Added config-level summary extraction and router-level coverage for configured profiles, trusted project profiles, pagination, and project discovery without `default_permissions`.
- Verification:
  - `go test ./internal/sandbox ./internal/config ./internal/appserver -run "Test(RuntimeRouterPermissionProfileList|RuntimeRouterDispatchesCatalogAPIs|ListProfiles|PermissionProfileSummary|LoadWithOptionsAppliesProjectConfigLayers|ProjectConfigRequiresTrustedProject|ProjectConfigTrustUsesActiveProjectRoot)" -count=1`

## Experimental Feature Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/experimental_feature_list.rs`.
- Go changes:
  - `experimentalFeature/list` now honors `threadId` by loading effective config for the thread CWD, including trusted project `.codex/config.toml` feature settings.
  - Unknown `threadId` now returns Rust-compatible invalid request `-32600` with `thread not found: ...`.
  - `experimentalFeature/enablement/set` now returns the Rust `enablement` map, filters unsupported/invalid feature names like Rust, and records applied enablement as config-read defaults without overriding explicit user/project config.
  - `config/read` now includes these app-server feature defaults only when a real config layer has not already set the same feature.
- Verification:
  - `go test ./internal/features ./internal/config ./internal/appserver -run "Test(RuntimeRouterExperimentalFeature|RuntimeRouterDispatchesExperienceAPIs|SetEnablementIgnoresUnknownKeys|FeatureWireShapeMatchesRust|ListPaginatesFeatures|ListRejectsInvalidCursor|ServiceReadIncludesProjectConfigForCWD)" -count=1 -v`
  - `go test ./internal/features ./internal/config ./internal/appserver -count=1`

## Collaboration Mode List Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/collaboration_mode_list.rs` and `models-manager/src/collaboration_mode_presets.rs`.
- Go changes:
  - Default collaboration mode presets now match Rust order and contents: `Plan` first with `reasoning_effort="medium"`, then `Default`.
  - Removed the Go-only `Agentic` preset from the app-server list response.
  - Added service and runtime-router tests locking the Rust preset list.
- Verification:
  - `go test ./internal/appserver -run "Test(CollaborationModeList|RuntimeRouterCollaborationModeList|RuntimeRouterDispatchesCatalogAPIs)" -count=1 -v`
  - `go test ./internal/appserver -count=1`

## App List Accessible Readiness Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/app_list.rs` readiness and force-refetch notification cases.
- Go changes:
  - `AppService` now tracks `CodexAppsReady` when reading accessible connector cache snapshots.
  - When accessible data is not ready, `app/list` and cached force-refetch notifications suppress remote directory-only connector entries instead of surfacing an interim inaccessible directory list.
  - Static apps and plugin-provided app connectors remain visible while accessible data is still warming, so local plugin capabilities continue to be injected into turn instructions.
  - Added service regressions for unready accessible merging and cached notification data.
- Verification:
  - `go test ./internal/apps ./internal/appserver -run "Test(ListWaitsForAccessibleReadyBeforeMergingDirectoryLikeRust|CachedListForNotificationSkipsDirectoryWhenAccessibleNotReadyLikeRust)|TestRuntimeRouterTurnStartInjectsEnabledPluginInstructions|TestRuntimeRouterAppList" -count=1 -v`
  - `go test ./internal/apps ./internal/appserver -count=1`

## App List Thread Feature Config Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/app_list.rs::list_apps_uses_thread_feature_flag_when_thread_id_is_provided`.
- Go changes:
  - `app/list` now resolves effective config from the supplied `threadId`'s stored CWD before configuring app providers.
  - Legacy `features.connectors=false` is honored through the canonical `apps` feature and returns an empty app/list without hitting directory providers.
  - Trusted project config can re-enable apps for a thread even when the current global config disables connectors.
  - Added runtime-router coverage for global-disabled/thread-project-enabled behavior.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterAppList(UsesThreadProjectFeatureConfigLikeRust|LoadsChatGPTDirectory|EmitsUpdatedNotificationWithFullList|ForceRefetchEmitsCachedThenFreshNotification|UsesPluginAppMetadata|MergesMCPAccessibleConnectors)|TestRuntimeRouterExperimentalFeatureListResolvesThreadProjectConfig" -count=1 -v`
  - `go test ./internal/apps ./internal/appserver -count=1`

## App List Force-Refetch Failure Cache Rust Parity Update
- Status: verified with regression coverage.
- Rust reference: `app-server/tests/suite/v2/app_list.rs::list_apps_force_refetch_preserves_previous_cache_on_failure`.
- Go changes:
  - Added a service-level regression proving a failed directory `forceRefetch` returns an error but leaves the previous successful app-list cache available for subsequent non-force requests.
  - Existing implementation already updated directory and accessible caches only after successful provider responses.
- Verification:
  - `go test ./internal/apps -run "TestForceRefetchPreservesPreviousCacheOnDirectoryFailureLikeRust|TestListMergesProvidersPluginConnectorsAndCache|TestListWaitsForAccessibleReadyBeforeMergingDirectoryLikeRust" -count=1 -v`
  - `go test ./internal/apps ./internal/appserver -count=1`

## Hooks List Per-CWD Feature Enablement Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/hooks_list.rs::hooks_list_uses_each_cwds_effective_feature_enablement`.
- Go changes:
  - Hook discovery now reads each requested CWD's effective config and honors the canonical `hooks` feature before loading user, project, or plugin hooks.
  - A global `features.hooks=false` can suppress hook discovery for one CWD while a trusted project `.codex/config.toml` can re-enable hooks for another CWD in the same request.
  - Added focused discovery coverage for the per-CWD enablement behavior.
- Verification:
  - `go test ./internal/appserver -run "TestHookDiscovery(UsesEachCWDEffectiveFeatureEnablementLikeRust|UsesTrustedProjectConfigLayers|SkipsUntrustedProjectHooksWhenConfigServicePresent|LinkedWorktreeUsesRootCheckoutHooks)|TestRuntimeRouterHooksList" -count=1 -v`
  - `go test ./internal/appserver -count=1`

## Skills List CWD Roots And Cache Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/skills_list.rs` cwd `.codex/skills`, relative cwd/order, and force-reload cache cases.
- Go changes:
  - `skills/list` now includes `cwd/.codex/skills` as a repo skill root, matching Rust's cwd-local skill discovery behavior.
  - Added regressions that preserve requested CWD order and relative CWD values in the response.
  - Added a cache regression proving newly-created cwd skills remain hidden until `forceReload=true`.
- Verification:
  - `go test ./internal/appserver -run "TestSkillsList(IncludesCWDCodeXSkillsRootLikeRust|PreservesRequestedCWDOrderAndRelativeCWDLikeRust|UsesCachedResultUntilForceReloadLikeRust)|TestSetExtraRoots|TestListSkillsAndConfig" -count=1 -v`
  - `go test ./internal/appserver -count=1`
- Note: `TestProcessServiceSpawnTTYStreamsAndResizes` showed the known Windows ConPTY timing flake twice, then skipped when isolated and the final full appserver run passed.

## Thread Shell Command History Filtering Rust Parity Update
- Status: verified with regression coverage.
- Rust reference: `app-server/tests/suite/v2/thread_shell_command.rs::thread_shell_command_history_responses_exclude_persisted_command_executions`.
- Go changes:
  - Strengthened runtime-router shell command coverage so `thread/read` and `thread/turns/list` are asserted not to serialize `commandExecution` items after a user shell command.
  - Existing Go behavior persists the shell result as a user message with `kind=user_shell_command`, while realtime notifications still use the `commandExecution` item shape.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterThreadShellCommand(PersistsUserShellRecord|EmitsUserShellNotifications|EnqueuesActiveTurnContext)|TestRuntimeRouterColdThreadOperationsReturnThreadNotFound" -count=1 -v`
  - `go test ./internal/appserver -count=1`

## Selected Capability Unavailable Environment Skills Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/selected_capability_stack.rs`.
- Go changes:
  - Selected capability skill discovery no longer falls back to the local filesystem path when an `EnvironmentManager` is available and the selected non-local environment is not connected.
  - The local/no-environment-service fallback remains for existing local selected capability tests, while connected remote environments still use `discoverRemoteEnvironmentSkills`.
  - Added runtime-router coverage matching the Rust unavailable executor behavior: selected skill description/body must not be present in model instructions or input items until the environment exists.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(UsesSelectedCapabilitySkillRoots|SkipsUnavailableSelectedEnvironmentSkillRootsLikeRust|UsesRemoteEnvironmentSkillRoot)" -count=1 -v`
  - `go test ./internal/appserver -count=1`

## Memory Reset Runtime RPC Rust Parity Update
- Status: verified with regression coverage.
- Rust reference: `app-server/tests/suite/v2/memory_reset.rs::memory_reset_clears_memory_files_and_rows_preserves_threads`.
- Go changes:
  - Added runtime-router coverage for `memory/reset`, matching the initialized app-server path used by Rust.
  - The regression asserts an explicit empty result object, clears `memories/MEMORY.md` and `memories/rollout_summaries/*`, and preserves existing session thread records.
  - Go has no Rust sqlite stage1 memory table yet, so the test intentionally covers the shared filesystem/thread contract only.
- Verification:
  - `go test ./internal/appserver -run "Test(RuntimeRouterMemoryResetClearsMemoriesAndPreservesThreadsLikeRust|RouterMemoryResetClearsMemoriesAndPreservesThreads|RuntimeRouterModelProviderCapabilitiesReadMatchesRust)" -count=1 -v`
  - `go test ./internal/appserver -count=1`

## Turn Output Schema Per-Turn Rust Parity Update
- Status: verified with regression coverage.
- Rust reference: `app-server/tests/suite/v2/output_schema.rs`.
- Go changes:
  - Added app-server runtime coverage that `turn/start.outputSchema` is forwarded to the agent request for that turn only.
  - Existing Responses runner coverage already verifies the Rust wire shape: `text.format = { name: "codex_output_schema", type: "json_schema", strict: true, schema: ... }`.
  - The new two-turn regression prevents a schema from leaking into later turns that omit `outputSchema`.
- Verification:
  - `go test ./internal/appserver ./internal/model -run "Test(RuntimeRouterTurnStartOutputSchemaIsPerTurnLikeRust|ResponsesAgentRunnerSendsOutputSchemaTextFormat)" -count=1 -v`
  - `go test ./internal/appserver ./internal/model -count=1`

## External Clock Sleep Items Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/sleep.rs::external_sleep_polls_current_time_and_emits_items`.
- Go changes:
  - `turn.ToolDispatcher` now supports an `OnToolStarted` callback, threaded through `AgentLoopRequest`, so app-server can surface long-running tool items at execution start.
  - `RuntimeRouter` maps `clock.sleep` to Rust-style `sleep` thread items: `item/started` is emitted immediately, and completion persists a single `sleep` item rather than generic function_call/tool_output records.
  - Sleep item `durationMs` now preserves the requested duration from tool arguments instead of being overwritten by measured timing metadata.
  - Added external-clock runtime coverage with sequenced `currentTime/read` responses.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouter(ExternalClockSleepEmitsSleepItemsLikeRust|TurnStartInjectsExternalCurrentTimeReminder|RequestCurrentTimeBridge|RequestCurrentTimeRequiresSingleSubscriber|RequestCurrentTimeWaitsForSubscriber)|TestRegisterCoreHandlersWithOptionsClockTools" -count=1 -v`
  - `go test ./internal/appserver ./internal/turn ./internal/tool -count=1`

## FS Runtime RPC Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/fs.rs` and `app-server/src/request_processors/fs_processor.rs`.
- Go changes:
  - FS JSON params now reject relative paths during `DecodeParams`, matching Rust `AbsolutePathBuf` deserialization behavior and error text.
  - `RuntimeRouter` now gates `fs/*` RPCs behind local filesystem availability; `CODEX_EXEC_SERVER_URL=none` returns `local filesystem is not configured`.
  - Added runtime regressions for exact `fs/getMetadata` field set, invalid `dataBase64`, relative paths across path-bearing FS methods, and disabled local filesystem behavior.
  - Direct `FSService` calls keep their existing service-level `ErrInvalidFSRequest` validation for non-RPC usage.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterFS(GetMetadataReturnsOnlyUsedFieldsLikeRust|MethodsReturnErrorWhenLocalEnvironmentDisabledLikeRust|WriteFileRejectsInvalidBase64LikeRust|MethodsRejectRelativePathsLikeRust)|TestRuntimeRouterDispatchesThreadAndFS|TestRuntimeRouterFSWatch|TestRuntimeRouterFSWriteFile|TestService(ReadWriteFile|RejectsRelativePath|DirectoryMetadataCopyAndRemove|CopyDirectoryRequiresRecursive|CopyDirectoryRejectsDescendant|WatchChangedAndUnwatch|ChangedForPathMatchesFileAndDirectDirectoryWatch)" -count=1 -v`
  - `go test ./internal/appserver -count=1`

## Process And Command Exec Local Environment Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/process_exec.rs::process_spawn_returns_error_when_local_environment_is_disabled` and `app-server/tests/suite/v2/command_exec.rs::command_exec_returns_error_when_local_environment_is_disabled`.
- Go changes:
  - Runtime local availability is now represented as `LocalEnvironmentEnabled`, so FS, `process/spawn`, and `command/exec` share the same disabled-environment decision.
  - `process/spawn` and `command/exec` now stop before launching anything when `CODEX_EXEC_SERVER_URL=none` and return `local environment is not configured`.
  - Added runtime-router regressions for disabled process spawn and disabled command exec.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouter(ProcessSpawnNotifications|ProcessSpawnReturnsErrorWhenLocalEnvironmentDisabledLikeRust|ProcessControlInvalidRequestAndParamsCodes|CommandExec|CommandExecReturnsErrorWhenLocalEnvironmentDisabledLikeRust|CommandExecInvalidRequestAndParamsCodes|FSMethodsReturnErrorWhenLocalEnvironmentDisabledLikeRust)|Test(CommandExecExecuteBuffered|ProcessServiceSpawnEmitsExitNotification)" -count=1 -v`
  - `go test ./internal/appserver -count=1`

## Process And Command Exec Validation Rust Parity Update
- Status: verified with regression coverage.
- Rust reference: `app-server/tests/suite/v2/process_exec.rs::process_spawn_reports_buffered_output_cap_reached` and `app-server/tests/suite/v2/command_exec.rs::command_exec_rejects_sandbox_policy_with_permission_profile`.
- Go changes:
  - Added process service coverage that buffered stdout/stderr are capped independently and report `stdoutCapReached` / `stderrCapReached` when `outputBytesCap` is hit.
  - Added runtime-router coverage that `command/exec` rejects `permissionProfile` combined with `sandboxPolicy` using Rust's exact error message and invalid-request code.
- Verification:
  - `go test ./internal/appserver -run "Test(ProcessServiceSpawnReportsBufferedOutputCapReachedLikeRust|RuntimeRouterCommandExecRejectsSandboxPolicyWithPermissionProfileLikeRust|RuntimeRouterCommandExecInvalidRequestAndParamsCodes|RuntimeRouterCommandExecReturnsErrorWhenLocalEnvironmentDisabledLikeRust|RuntimeRouterProcessSpawnReturnsErrorWhenLocalEnvironmentDisabledLikeRust)" -count=1 -v`

## Command Exec Non-Streaming Termination Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/command_exec.rs::command_exec_without_streams_can_be_terminated`.
- Go changes:
  - `CommandExecService` now registers non-streaming commands as active when the caller supplies `processId`, so `command/exec/terminate` can cancel them while the original `command/exec` is still waiting for completion.
  - Commands without `processId` retain the legacy synchronous buffered compatibility path.
  - Added coverage for terminating a sleeping non-streaming command and receiving a non-zero response with empty buffered output.
- Verification:
  - `go test ./internal/appserver -run "TestCommandExec(WithoutStreamsCanBeTerminatedLikeRust|StreamingSessionOperations|StreamingStdin|ExecuteBuffered|SessionsAreConnectionScoped|ConnectionClosedCancelsOnlyThatConnection)|TestRuntimeRouterCommandExec" -count=1 -v`
  - `go test ./internal/appserver -count=1`

## Command Exec Caps Env And Streaming Rust Parity Update
- Status: verified with regression coverage.
- Rust reference: `app-server/tests/suite/v2/command_exec.rs::command_exec_env_overrides_merge_with_server_environment_and_support_unset`, `command_exec_non_streaming_respects_output_cap`, and `command_exec_streaming_does_not_buffer_output`.
- Go changes:
  - Added env merge coverage for overriding, adding, and unsetting request env values while preserving base env entries.
  - Added non-streaming `command/exec` coverage that stdout and stderr are capped independently when a `processId` is present.
  - Added streaming coverage that capped stdout emits a `command/exec/outputDelta` with `capReached=true` and final streaming responses keep stdout/stderr empty.
- Verification:
  - `go test ./internal/appserver -run "TestCommandExec(StreamingDoesNotBufferOutputLikeRust|StreamingSessionOperations|WithoutStreamsCanBeTerminatedLikeRust|NonStreamingWithProcessIDRespectsOutputCapLikeRust|EnvOverridesMergeAndUnsetLikeRust)" -count=1 -v`

## Command Exec Custom Permission Profile Rust Parity Update
- Status: implemented.
- Rust reference: app-server `command/exec` permission profile resolution through effective config, including custom `[permissions.<id>]` profiles.
- Go changes:
  - `CommandExecService` now accepts an injectable permission profile resolver while preserving the built-in profile resolver for direct service usage.
  - `RuntimeRouter` resolves `command/exec.permissionProfile` from the effective config for the command cwd, so custom profiles such as `networked` are compiled by the existing config profile machinery and passed to the sandbox runner.
  - Added service-level and runtime-router regressions proving custom profile ID, cwd, and network-enabled sandbox profile reach `tool.ShellRequest`.
- Verification:
  - `go test ./internal/appserver -run "TestCommandExec(CustomPermissionProfileResolverLikeRust|SandboxPolicyRequiringRunnerUsesSandboxRunner|FullAccessPermissionProfileRuns|SandboxDangerFullAccessRunsAndInjectsProfile|EnvOverridesMergeAndUnsetLikeRust)|TestRuntimeRouterCommandExec(ResolvesCustomPermissionProfileFromConfigLikeRust|RejectsSandboxPolicyWithPermissionProfileLikeRust|ReturnsErrorWhenLocalEnvironmentDisabledLikeRust|InvalidRequestAndParamsCodes|$)" -count=1 -v`

## Command Exec Network Proxy Marker Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/command_exec.rs::command_exec_permission_profile_starts_selected_network_proxy` and `command_exec_permission_profile_does_not_reuse_default_network_proxy`.
- Go changes:
  - `command/exec` now clears inherited `CODEX_NETWORK_PROXY_ACTIVE` before each launch and sets it only for the current resolved permission profile when that profile is sandboxed and allows network access.
  - This keeps a default `networked` config profile from leaking proxy-active state into an explicit read-only `permissionProfile`.
  - Added regressions for selected custom network profile activation and explicit read-only isolation.
- Verification:
  - `go test ./internal/appserver -run "TestCommandExecCustomPermissionProfileResolverLikeRust|TestRuntimeRouterCommandExec(ResolvesCustomPermissionProfileFromConfigLikeRust|PermissionProfileDoesNotReuseDefaultNetworkProxyLikeRust)$" -count=1 -v`

## Command Exec Project Roots CWD Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/command_exec.rs::command_exec_permission_profile_project_roots_use_command_cwd`.
- Go changes:
  - `command/exec.cwd` now supports relative paths by resolving them against `RuntimeServices.DefaultCWD`, matching Rust app-server request handling.
  - Custom permission profile runtime JSON is preserved from config resolution through `CommandExecService`, `tool.ShellRequest`, and `sandbox.CommandRunRequest`, so `:workspace_roots` entries are materialized from the command cwd rather than losing path rules.
  - Added runtime-router regression that `:workspace_roots = "write"` grants the relative command cwd and does not grant the server default cwd.
- Verification:
  - `go test ./internal/appserver ./internal/tool -run "TestRuntimeRouterCommandExec(ResolvesCustomPermissionProfileFromConfigLikeRust|PermissionProfileDoesNotReuseDefaultNetworkProxyLikeRust|PermissionProfileProjectRootsUseCommandCWDLikeRust)|TestCommandExecCustomPermissionProfileResolverLikeRust|TestLocalShellRunnerUsesWindowsSandboxForSandboxedProfile|TestBuildShellRequestBuildsSandboxProfileFromPermissionProfile" -count=1 -v`

## Command Exec Validation And Pipe Streaming Rust Parity Update
- Status: verified with regression coverage.
- Rust reference: `command_exec_rejects_negative_timeout_ms` and `command_exec_pipe_streams_output_and_accepts_write`.
- Go changes:
  - Added runtime-router regression for negative `timeoutMs`, preserving Rust's exact error message and invalid-params code.
  - Added streaming pipe regression that stdout and stderr deltas are emitted before stdin write, stdin write returns `{}`, post-write stdout/stderr deltas are emitted, and the final streaming response keeps stdout/stderr empty.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterCommandExecInvalidRequestAndParamsCodes|TestCommandExec(PipeStreamsOutputAndAcceptsWriteLikeRust|StreamingStdin|StreamStdinBuffersFinalOutputWhenNotStreamingStdout)" -count=1 -v`

## Process Spawn Return Timing Rust Parity Update
- Status: verified with regression coverage.
- Rust reference: `app-server/tests/suite/v2/process_exec.rs::process_spawn_returns_before_exit_and_emits_exit_notification`.
- Go changes:
  - Added a probe/release regression proving `process/spawn` returns `{}` while the child is still running, then emits `process/exited` with buffered stdout/stderr after the release file is created.
  - The test uses cross-platform shell fixtures and guards against premature `process/exited` before release.
- Verification:
  - `go test ./internal/appserver -run "TestProcessService(SpawnReturnsBeforeExitAndEmitsExitNotificationLikeRust|SpawnEmitsExitNotification|SpawnReportsBufferedOutputCapReachedLikeRust|DuplicateKillAndResize|ControlErrorsMatchRust)|TestRuntimeRouterProcessSpawnReturnsErrorWhenLocalEnvironmentDisabledLikeRust" -count=1 -v`

## Responses Client Metadata Lineage Rust Parity Update
- Status: implemented.
- Rust reference: `app-server/tests/suite/v2/client_metadata.rs::turn_start_forwards_client_metadata_to_responses_request_v2`, `turn_start_sends_fork_lineage_in_turn_metadata_for_thread_fork_v2`, `turn_start_sends_nested_subagent_lineage_after_cold_thread_resume_v2`, and `turn_steer_updates_client_metadata_on_follow_up_responses_request_v2`.
- Go changes:
  - `turn.BuildResponsesClientMetadata` now accepts and emits Rust-style lineage fields: `forked_from_thread_id`, `parent_thread_id`, `subagent_kind`, `thread_source`, and subagent/parent compatibility client metadata keys.
  - Runtime turn metadata now derives session lineage from the stored thread record, preserving cold-resumed subagent `session_id` while keeping `thread_id` as the active child thread.
  - Ordinary thread forks send `forked_from_thread_id` without leaking Go's tree `ParentThreadID` as Responses `parent_thread_id`; subagent records send parent/subagent metadata and top-level compatibility headers.
  - Subagent source parsing now supports Rust display forms such as `subagent_guardian`, `subagent_review`, `subagent_thread_spawn_*_dN`, plus the existing Go colon forms.
- Verification:
  - `go test ./internal/codexapi ./internal/turn -run "TestClientSubagent|TestBuildResponsesClientMetadata" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(PassesResponsesAPIClientMetadata|SendsForkLineageInClientMetadataLikeRust|SendsSubagentLineageAfterColdResumeLikeRust)$" -count=1 -v`
  - `go test ./internal/codexapi ./internal/turn -count=1`
  - `go test ./internal/appserver -count=1`

## Turn Steer Validation Rust Parity Update
- Status: verified with regression coverage.
- Rust reference: `app-server/tests/suite/v2/turn_steer.rs::turn_steer_rejects_oversized_text_input` and `turn_steer_rejects_context_only_input_without_merging_context`.
- Go changes:
  - Added runtime-router coverage that oversized `turn/steer` input returns Rust's invalid-params code, stable max/actual character data, and `input_error_code`.
  - Added active-turn coverage that a context-only steer request is rejected with `input must not be empty` and does not append a user message or merge `additionalContext` into persisted items.
  - Existing active-turn and steer delivery tests cover accepted steer item notifications and follow-up sampling.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnSteer(PersistsUserInput|RejectsOversizedInputWithRustErrorData|RejectsContextOnlyInputWithoutMergingContextLikeRust|DeliveredToNextAgentSampling)$" -count=1 -v`

## Thread Read And Resume Rust Parity Update
- Status: implemented and covered with focused regressions.
- Rust reference: `app-server/tests/suite/v2/thread_read.rs::thread_resume_initial_turns_page_matches_requested_turns_list_page`, `thread_resume.rs::thread_resume_skips_restored_token_usage_when_turns_are_excluded`, `thread_resume_token_usage_replay_ignores_stale_interrupted_tail_turn`, `thread_resume_and_read_interrupt_incomplete_rollout_turn_when_thread_is_idle`, `thread_resume_defers_updated_at_until_turn_start`, and pending approval replay coverage.
- Go changes:
  - Added coverage that `thread/resume.initialTurnsPage` exactly matches a same-parameter `thread/turns/list` page while `excludeTurns=true` keeps `thread.turns` empty.
  - Added token usage replay regressions so metadata-only resumes do not emit restored usage, and stale usage turn IDs fall back to the latest completed turn rather than an interrupted tail.
  - Added runtime coverage that rollout-only threads with an unclosed turn are returned as idle with an interrupted tail from both `thread/resume` and `thread/read`.
  - Added runtime coverage that `thread/resume` does not update `updatedAt`/`recencyAt`; the next `turn/start` prompt persistence performs that refresh.
  - `ServerRequestBroker` can now replay pending server requests for a thread, and runtime `thread/resume` re-sends pending approval requests for the resumed thread, matching Rust's running-turn resume behavior.
- Verification:
  - `go test ./internal/appserver -run "Test(RuntimeRouterThreadResumeAndReadInterruptIncompleteRolloutTurnWhenIdleLikeRust|RouterThreadResumeInitialTurnsPageMatchesRequestedTurnsListPage|RuntimeRouterNotifyRestoredTokenUsageFromRecord|RuntimeRouterThreadResumeDefersUpdatedAtUntilTurnStartLikeRust|RuntimeRouterThreadResumeReplaysPendingServerRequestApprovalLikeRust)$" -count=1 -v`

## Thread Resume Personality Override Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `app-server/tests/suite/v2/thread_resume.rs::thread_resume_accepts_personality_override`.
- Go changes:
  - Runtime `thread/resume` now persists idle-thread resume overrides into `ThreadExtras` settings for the resumed thread, including `cwd`, `model`, `serviceTier`, and `personality`.
  - Running-thread resume still ignores override mismatches for persistent settings, matching Rust's rejoin semantics for an already active turn.
  - Resume-origin personality settings carry an internal explicit marker into a later `turn/start`, so a resume-supplied personality produces the explicit `<personality_spec>` developer instruction while ordinary settings updates and config defaults retain their prior baked-in behavior.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouter(ThreadResumeAppliesPersonalityOverrideLikeRust|ThreadResumeRunningIgnoresOverrideMismatch|TurnStartAppliesExplicitPersonality|TurnStartUsesConfigPersonalityTemplate|ThreadResumeDefersUpdatedAtUntilTurnStartLikeRust)$" -count=1 -v`

## Turn Interrupt Pending Approval Rust Parity Update
- Status: verified with focused regression coverage.
- Rust reference: `app-server/tests/suite/v2/turn_interrupt.rs::turn_interrupt_resolves_pending_command_approval_request`.
- Go changes:
  - Added runtime-router coverage proving a pending command approval server request is resolved when `turn/interrupt` cancels the active turn context.
  - The regression asserts the pending `commandExecutionRequestApproval` carries the active thread/turn IDs, `turn/interrupt` succeeds, `serverRequest/resolved` is emitted with the same request ID, and the turn completes as `interrupted`.
  - No production code change was required; this locks the existing broker context-cancel behavior against Rust's app-server contract.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnInterrupt(ResolvesPendingCommandApprovalLikeRust|WritesRolloutTurnLifecycle|CancelsActiveRuntimeAndRejectsConcurrentStart)$|TestRuntimeRouterThreadResumeReplaysPendingServerRequestApprovalLikeRust|TestServerRequestBrokerResolvedCallbackOnContextCancel" -count=1 -v`

## Turn Start Personality Mid-Thread Rust Parity Update
- Status: verified with focused regression coverage.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs::turn_start_change_personality_mid_thread_v2`.
- Go changes:
  - Added a two-turn runtime-router regression where the first turn uses the default personality template without emitting `<personality_spec>`, and the second turn explicitly switches to `friendly` and emits the Rust-style personality update.
  - This locks the interaction between per-turn explicit personality, persisted thread settings, and config-default personality behavior after the resume personality work.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartAppliesExplicitPersonality|TurnStartChangesPersonalityMidThreadLikeRust|ThreadResumeAppliesPersonalityOverrideLikeRust|TurnStartUsesConfigPersonalityTemplate)$" -count=1 -v`

## Turn Start CWD Rebinding Rust Parity Update
- Status: verified with focused regression coverage.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs::turn_start_updates_sandbox_and_cwd_between_turns_v2` for the CWD rebinding behavior.
- Go changes:
  - Added runtime-router coverage that two explicit turn CWD overrides load different trusted project instruction files in the same thread.
  - Added a third turn without `cwd` to prove the latest turn CWD is persisted into thread settings and used for future turns.
  - This locks the settings/update order around `turn/start`: explicit CWD affects the current turn and becomes the sticky CWD for later turns.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartUpdatesCWDBetweenTurnsLikeRust|ThreadSettingsUpdateAffectsFutureTurn|TurnStartSettingsOverrideEmitsThreadSettingsUpdated)$" -count=1 -v`

## Turn Steer And Interrupt Error Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `app-server/tests/suite/v2/turn_steer.rs::turn_steer_requires_active_turn` and `turn_interrupt.rs::turn_interrupt_rejects_completed_turn`.
- Go changes:
  - Added runtime-router coverage for `turn/steer` without an active turn, locking Rust's `-32600` invalid request response and `no active turn to steer` message.
  - Mapped inactive/completed `turn/interrupt` errors to JSON-RPC invalid request while preserving the existing message, matching Rust's completed-turn rejection code.
  - Added coverage for completed-turn interrupt alongside the existing oversized/context-only steer and interrupt lifecycle regressions.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurn(SteerRequiresActiveTurnLikeRust|InterruptRejectsCompletedTurnLikeRust|InterruptResolvesPendingCommandApprovalLikeRust|InterruptWritesRolloutTurnLifecycle|SteerRejectsOversizedInputWithRustErrorData|SteerRejectsContextOnlyInputWithoutMergingContextLikeRust)$" -count=1 -v`

## Exec Headless Approval Policy Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `exec/tests/suite/approval_policy.rs`, `exec/src/lib.rs::build_exec_config`, and `exec/src/event_processor_with_human_output.rs::config_summary_entries`.
- Go changes:
  - `codex exec` now computes a Rust-style headless approval policy: default human/headless exec uses `never`, `--dangerously-bypass-approvals-and-sandbox` and removed `--full-auto` force `never`, while `approvals_reviewer = "auto_review"` preserves the configured approval policy such as `on-request`.
  - The computed approval policy is passed into the exec tool router, so `exec_command` requests for `require_escalated` are rejected model-visibly under default headless `never` and become approval requests under auto-review/on-request.
  - Human `codex exec` stderr now includes an `approval: ...` summary line, matching the Rust fixture's observable approval-mode check; JSON mode remains event-only.
  - The defensive direct-run error for unknown internal exec subcommands no longer exposes stale `Go port yet` / `not implemented` wording and now returns `unknown exec subcommand ...`.
- Verification:
  - `go test ./internal/exec -run "Test(EffectiveExecApprovalPolicyMatchesRustHeadless|ToolRouterUsesExecHeadlessApprovalPolicyLikeRust|RunRejectsUnknownExecSubcommandWithoutGoPortMessage|RunJSONAndLastMessage|NewRunnerDefaultsToResponsesAPI)$" -count=1 -v`
  - `go test ./internal/exec -count=1`
  - `go test ./internal/app -run "Test(AppExecJSONEndToEnd|RunExecPromptFromStdin|RunReview|RunExecReview|RunRootReview|RunExecServer|Exec)" -count=1 -v`

## Debug CLI Unknown Subcommand Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `cli/src/main.rs::DebugSubcommand`, where clap only accepts `models`, `app-server`, `prompt-input`, hidden `trace-reduce`, and hidden `clear-memories`.
- Go changes:
  - `cli.Parse` now rejects unknown `debug` subcommands with `unknown debug subcommand ...` instead of passing them through to an app-layer `not implemented` fallback.
  - `runDebug` keeps a matching defensive unknown-subcommand fallback, and the generic app fallback no longer exposes `is not implemented in the Go port yet`.
  - Added CLI and app entry regressions proving stale `not implemented` / `Go port` wording is not exposed for unknown debug subcommands.
- Verification:
  - `go test ./internal/cli ./internal/app -run "Test(ParseDebugTooling|ParseDebugRejectsUnknownSubcommandLikeRust|DebugPromptInput|DebugUnknownSubcommandDoesNotExposeGoPortMessage)$" -count=1 -v`

## Remote TUI Server Request Long Tail Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `tui/src/app/app_server_requests.rs::PendingAppServerRequests::note_server_request` and `App::reject_app_server_request`.
- Go changes:
  - `remoteServerRequestResult` now rejects Rust-unsupported TUI server requests with JSON-RPC `-32000`: dynamic tool calls, attestation generation, external current time, legacy patch approval, and legacy command approval.
  - `currentTime/read` no longer returns a local Go timestamp in remote TUI; it now returns Rust's `External current time is not available in TUI.` message.
  - Legacy `applyPatchApproval` and `execCommandApproval` no longer open Go approval modals; the Rust-supported replacements remain `item/fileChange/requestApproval` and `item/commandExecution/requestApproval`.
  - Unknown remote TUI server request methods now return `Unsupported app-server request: ...` with `-32000`, removing the stale `Go TUI remote client` / `not implemented` wording.
- Verification:
  - `go test ./internal/app -run "TestRemoteServerRequestLongTailResponses|TestInteractiveRemoteTurnHandlesCommandApprovalServerRequest|TestInteractiveRemoteTurnHandlesUserInputServerRequest" -count=1 -v`

## Windows Sandbox Setup Backend Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `app-server/src/request_processors/windows_sandbox_processor.rs`, `app-server/tests/suite/v2/windows_sandbox_setup.rs`, and `core/src/windows_sandbox.rs::run_windows_sandbox_setup`.
- Go changes:
  - `windowsSandbox/setupStart` now mirrors Rust's two-phase behavior: validate mode/cwd, return `{started:true}` immediately, run setup asynchronously, then emit `windowsSandbox/setupCompleted` back to the originating connection.
  - Added an injectable `WindowsSandboxSetupRunner` plus a default runner that dispatches elevated setup and unelevated legacy preflight using the resolved permission profile, workspace roots, codex home, cwd, and environment.
  - Successful setup now persists Rust's new `windows.sandbox` config value and clears legacy `features.experimental_windows_sandbox`, `features.elevated_windows_sandbox`, and `features.enable_experimental_windows_sandbox`; persist failures are reported as setup completion failures like Rust core.
  - Relative `cwd` for `windowsSandbox/setupStart` now returns Rust-style JSON-RPC `-32600` with `Invalid request: AbsolutePathBuf deserialized without a base path`.
  - Removed stale Windows sandbox backend `Go port yet` wording while keeping explicit unsupported/unavailable sentinel errors.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterWindowsSandboxSetupStart|TestRuntimeRouterDispatchesRemoteEnvironmentAndWindows" -count=1 -v`
  - `go test ./internal/sandbox ./internal/sandbox/windowssandbox -count=1`
  - `go test ./internal/config -run "TestResolveSandboxPermissionProfile|TestConfig|Test.*Write" -count=1`
  - `go test ./internal/appserver -count=1`

## Windows Sandbox Readiness Config Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `app-server/src/request_processors/windows_sandbox_processor.rs::determine_windows_sandbox_readiness` and `core/src/windows_sandbox.rs::WindowsSandboxLevelExt`.
- Go changes:
  - `windowsSandbox/readiness` now computes status from effective config instead of only returning the in-memory manager state.
  - Config parsing follows Rust precedence: `[windows].sandbox` first, then legacy `windows_sandbox` compatibility, then legacy feature flags `elevated_windows_sandbox` and `experimental_windows_sandbox` / `enable_experimental_windows_sandbox`.
  - Non-Windows hosts always report `notConfigured`; Windows `unelevated` reports `ready`; Windows `elevated` reports `ready` only when the elevated setup marker is complete, otherwise `updateRequired`.
  - The in-memory manager still allows immediate `ready` for a just-completed successful setup when no persisted config is present, preserving the Go runtime's active-session behavior.
- Verification:
  - `go test ./internal/appserver -run "Test(RuntimeRouterWindowsSandbox|WindowsSandboxLevelFromConfigValues)" -count=1 -v`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/config ./internal/sandbox ./internal/sandbox/windowssandbox -count=1`

## Windows Sandbox Setup Workspace Roots Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `app-server/src/request_processors/windows_sandbox_processor.rs`, which sends `config.effective_workspace_roots()` into `WindowsSandboxSetupRequest`.
- Go changes:
  - `config.SandboxPermissionProfileResolution` now exposes the materialized workspace roots used to compile a custom permission profile.
  - `windowsSandbox/setupStart` now passes the effective runtime roots into the setup runner, combining cwd/default runtime roots with profile `workspace_roots` and de-duplicating like existing thread runtime roots.
  - Added coverage proving a custom `default_permissions` profile with `[permissions.<name>.workspace_roots]` reaches `WindowsSandboxSetupRuntimeRequest.WorkspaceRoots`.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterWindowsSandboxSetupStart|TestWindowsSandboxLevelFromConfigValues" -count=1 -v`
  - `go test ./internal/config -run "TestResolveSandboxPermissionProfile|Test.*Permission|TestConfig" -count=1`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/config ./internal/sandbox ./internal/sandbox/windowssandbox -count=1`

## Memory SQLite Reset And Thread Memory Mode Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `app-server/tests/suite/v2/memory_reset.rs`, `app-server/tests/suite/v2/thread_memory_mode_set.rs`, `app-server/src/request_processors/thread_processor.rs`, and `state/src/runtime/memories.rs`.
- Go changes:
  - Added `internal/appserver/memory_sqlite.go` as a narrow compatibility layer for Rust runtime sqlite files.
  - `thread/memoryMode/set` now updates `state_5.sqlite.threads.memory_mode` when the Rust state DB exists, while still updating the Go session store and rollout metadata.
  - `memory/reset` now clears `memories_1.sqlite.stage1_outputs` plus `jobs` rows for `memory_stage1` and `memory_consolidate_global`, preserving unrelated jobs and thread memory modes.
  - Missing sqlite files or older missing memory tables are treated as no-op to preserve existing Go-only local sessions.
- Verification:
  - `go test ./internal/appserver -run "TestRouter(MemoryResetClearsRustMemoriesSQLiteRowsLikeRust|ThreadMemoryModeSetUpdatesRustStateSQLiteLikeRust|MemoryResetClearsMemoriesAndPreservesThreads)|TestRuntimeRouterMemoryResetClearsMemoriesAndPreservesThreadsLikeRust" -count=1 -v`
  - `go test ./internal/appserver -count=1`

## Thread Metadata SQLite Git Fields Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `app-server/tests/suite/v2/thread_metadata_update.rs`, `app-server/src/request_processors/thread_processor.rs::thread_metadata_update_response_inner`, and `state/src/runtime/threads.rs`.
- Go changes:
  - `thread/metadata/update` now synchronizes final git metadata into existing `state_5.sqlite.threads` rows when a Rust state DB exists.
  - The synchronized columns are `git_sha`, `git_branch`, and `git_origin_url`; null git patches clear them to NULL.
  - This keeps sqlite-backed thread inventory consistent with Go session-store and rollout metadata for loaded, stored, and repaired rollout threads.
- Verification:
  - `go test ./internal/appserver -run "TestRouter(ThreadMetadataUpdateUpdatesRustStateSQLiteLikeRust|SetNameAndMetadata|ThreadMetadataUpdateRejectsEmptyGitInfoPatch|MetadataWritesMissingThreadUseRustErrors|MemoryResetClearsRustMemoriesSQLiteRowsLikeRust|ThreadMemoryModeSetUpdatesRustStateSQLiteLikeRust)" -count=1 -v`
  - `go test ./internal/appserver -count=1` (first run hit a transient Windows TempDir cleanup error in an unrelated test; immediate rerun passed)

## Model List Default Remote Refresh Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `app-server/tests/suite/v2/model_list.rs`, `app-server/src/request_processors/catalog_processor.rs`, `app-server/src/models.rs`, and `models-manager/src/manager.rs`.
- Go changes:
  - `model/list` now defaults omitted internal refresh strategy to `RefreshOnlineIfUncached`, matching Rust app-server behavior for list requests.
  - ChatGPT-style remote catalog managers now fetch `/models` on the first default list request and can return a remote-only source-of-truth catalog when the remote response contains picker-visible models.
  - Explicit `RefreshOffline` remains supported for internal code paths that should not refresh from the network.
  - Added service-level regressions for default online-if-uncached behavior, cached second list behavior, and explicit offline no-refresh behavior.
- Verification:
  - `go test ./internal/model -run "TestListModels(Default|Explicit|Filters|Pagination)|TestRemoteModelsManager(CanUseRemoteCatalogAsSourceOfTruth|KeepsMerging)|TestConfiguredProviderModelsManagerUsesChatGPTRemoteCatalogAsSourceOfTruth" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterDispatchesCatalogAPIs|TestRuntimeRouterModelProviderCapabilitiesReadMatchesRust" -count=1 -v`
  - `go test ./internal/model -count=1`

## Recommended Plugins After External Login Rust Parity Update
- Status: implemented with focused regression coverage.
- Rust reference: `app-server/tests/suite/v2/recommended_plugins.rs`, `core-plugins/src/manager.rs::recommended_plugin_candidates_for_config`, and `core-plugins/src/remote.rs::fetch_recommended_plugins`.
- Go changes:
  - Added `internal/plugin/suggested.go` with a small ChatGPT suggested-plugin provider for `/ps/plugins/suggested?scope=GLOBAL`.
  - Remote suggestions are mapped to Rust-style install candidates under `openai-curated-remote`, for example `github@openai-curated-remote`, using `release.display_name` for the model-visible name.
  - `RuntimeRouter` configures this provider from effective config and ChatGPT auth when `plugins`, `remote_plugin`, and `tool_suggest` are enabled.
  - The first `turn/start` after external `chatgptAuthTokens` login now blocks on the suggested-plugin response before creating the model request, so the request contains `<recommended_plugins>` and the recommendation-mode `request_plugin_install` tool.
  - Login, account-session switch/logout, and logout clear the recommended-plugin cache to avoid stale auth-scoped suggestions.
  - Suggested endpoint errors or `enabled != true` fall back to the existing local discovery behavior.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouter(FirstTurnAfterExternalLoginWaitsForRecommendedPluginsLikeRust|TurnStartInjectsEnabledPluginInstructions|TurnStartDoesNotRecommendConnectorOnlyCandidates)" -count=1 -v`
  - `go test ./internal/plugin -count=1`
  - `go test ./internal/appserver -run "Test(RuntimeRouterTurnStart.*Plugin|PluginInstallRuntime|PluginInstallCandidatesForTurnApplyDisabledAndLoadedConnectorConfig|RuntimeRouterDispatchesCatalogAPIs)" -count=1 -v`
  - `go test ./internal/appserver -count=1` (first run hit an unrelated Windows TempDir cleanup error; immediate rerun passed)

## Account Rate Limits Rust Fixture Coverage Update
- Status: covered; no production changes needed in this pass.
- Rust reference: `app-server/tests/suite/v2/rate_limits.rs`, `app-server/tests/suite/v2/rate_limit_reset_credits.rs`, account processor rate-limit handlers, and backend-client reset-credit helpers.
- Go findings:
  - Existing account backend routing already matches Rust for ChatGPT-auth-only backend reads, reset-credit consumption, add-credits nudge email, request timeouts, idempotency-key validation, and reset-credit authorization errors.
  - Added `TestRuntimeRouterGetAccountRateLimitsReturnsSnapshotLikeRust` to lock the full `/api/codex/usage` fixture shape: primary `codex` snapshot, primary/secondary windows, spend-control limit, plan type, rate-limit reached type, reset credits, and `rateLimitsByLimitId`.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouter(GetAccountRateLimitsReturnsSnapshotLikeRust|SendAddCreditsNudgeEmail|ConsumeRateLimitResetCredit|AccountBackendReadsRequireChatGPTAuth|AccountBackendTimeoutsMatchRust|PersonalAccessTokenBackendReadsHydrateAccountRouting|AccountBackendClientConstructionErrorIsWrapped)" -count=1 -v`

## Standalone Web Search Rust Parity Update
- Status: implemented for the Rust `web_search.rs` main path with focused regression coverage.
- Rust reference: `app-server/tests/suite/v2/web_search.rs`, `ext/web-search/src/tool.rs`, `ext/web-search/src/extension.rs`, `ext/web-search/src/history.rs`, and `ext/web-search/src/output.rs`.
- Go changes:
  - Added `internal/turn/web_search.go` with a `web.run` executor that parses `codexapi.SearchCommands`, posts to provider `/alpha/search`, applies provider/auth headers and request signing, and returns `function_call_output` content items.
  - `turn.ToolRegistryOptions` can now register standalone web search per turn; `RuntimeRouter.toolRouterForTurn` enables it only when `features.standalone_web_search` is active and provider/auth resolution succeeds.
  - `model.ResponsesToolsFromSpecs` now serializes `web.run` as a Responses namespace tool named `web` with inner function `run`, preserving Rust's schema expectation for `parameters.properties.time.description`.
  - `turn_runtime` now emits `webSearch` started/completed items and persists exactly one completed `webSearch` session item for the call, preventing duplicate ordinary function-call/tool-output history entries.
  - Search request settings currently map Rust-critical `allowed_callers: ["direct"]` plus Go config values from `[tools.web_search]`: `context_size`, `allowed_domains`, and approximate `location`.
  - Search request input now mirrors Rust `recent_input`: keep the previous visible user turn plus current user text, preserve assistant text between those user messages, remove user image content, ignore contextual `<environment_context>` user messages, and strip message IDs.
  - Command-action mapping now has Rust-shaped coverage for multi-query image searches, literal URL open, URL/non-URL find-in-page, and non-literal open fallback.
- Verification:
  - `go test ./internal/model ./internal/turn -count=1`
  - `go test ./internal/appserver -run "TestRuntimeRouter(StandaloneWebSearchMatchesRustFixture|TurnStartRunsRuntimeAndPersistsItems|TurnStartInjectsExternalCurrentTimeReminder|TurnStartNullServiceTierClearsConfigDefault)" -count=1 -v`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Add `external_web_access` mode parity once Go exposes Rust's full web-search mode selection (`disabled`, `cached`, `indexed`, `live`) for standalone search settings.
  - Add full app-server coverage for `[tools.web_search]` domains/location/context-size once Rust fixtures cover those fields on standalone search.

## Output Schema App-Server HTTP Fixture Update
- Status: covered; no production changes needed in this pass.
- Rust reference: `app-server/tests/suite/v2/output_schema.rs`.
- Go findings:
  - Existing runtime plumbing already keeps `TurnStartParams.OutputSchema` per turn and does not leak it into later turns.
  - Existing Responses runner serialization already emits `text.format` as Rust expects: `{"name":"codex_output_schema","type":"json_schema","strict":true,"schema":...}`.
- Go changes:
  - Added `TestRuntimeRouterTurnStartSendsOutputSchemaTextFormatLikeRust`, an app-server-level HTTP/SSE regression that uses the real `ResponsesAgentRunner` and configured mock provider.
  - The regression asserts the first turn's outbound `/v1/responses` body contains Rust's exact `text.format` object and the second turn omits `text.format` when `outputSchema` is not provided.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(OutputSchemaIsPerTurnLikeRust|SendsOutputSchemaTextFormatLikeRust)" -count=1 -v`
  - `go test ./internal/appserver -count=1`

## Current Time Reminder And Clock Namespace Rust Parity Update
- Status: implemented for the app-server `current_time.rs` fixture and the main core reminder/tool paths.
- Rust reference: `app-server/tests/suite/v2/current_time.rs`, `core/tests/suite/current_time_reminder.rs`, `core/src/context/current_time_reminder.rs`, `core/src/session/time_reminder.rs`, and `core/src/tools/handlers/current_time.rs`.
- Go changes:
  - `CurrentTimeReminder` now renders as a bare developer message (`It is ... UTC.`) instead of wrapping the text in `<current_time>` tags.
  - `turn/start` now sends the reminder as a Responses `input` developer message, matching Rust's `message_input_texts("developer")`, instead of embedding it in the top-level `instructions` string.
  - Delivered reminders are persisted as developer session messages tagged with `Data.kind = "current_time_reminder"`, so later model requests carry prior reminders in history.
  - Reminder delivery now follows Rust's interval behavior for normal turns: first request delivers, requests inside the interval keep only the historical reminder, and requests at/after the interval append a fresh reminder.
  - `reminder_interval_seconds = 0` now delivers a fresh reminder even if time moves backward, matching Rust's zero-interval edge case.
  - `delivery_mode = "after_user_or_tool_output"` now injects a fresh reminder after a tool output on the next model request for the single-tool path.
  - Responses tool serialization now treats the `clock` namespace like Rust, exposing `clock.curr_time` / `clock.sleep` as namespace tools instead of flattened `clock__...` functions.
  - Added app-server HTTP/SSE coverage proving `clock.curr_time` returns the latest provider time in the follow-up `function_call_output`.
- Verification:
  - `go test ./internal/model ./internal/appserver -run "TestResponses(ToolsFromSpecs|ToolNames)|TestRuntimeRouter(CurrentTime(ReadAddsDeveloperInputLikeRust|RemindersFollowIntervalAndPersistInHistoryLikeRust|ToolReturnsLatestTimeLikeRust|ReminderFollowsToolOutputDeliveryModeLikeRust)|ZeroCurrentTimeReminderIntervalDeliversWhenTimeMovesBackwardLikeRust|TurnStartInjectsExternalCurrentTimeReminder|ExternalClockSleepEmitsSleepItemsLikeRust)$" -count=1 -v`
  - `go test ./internal/... -count=1`
- Next follow-up candidates:
  - Finish `delivery_mode = "after_user_or_tool_output"` for multi-tool batches and assistant-only `end_turn=false` continuations. Rust injects at most once before the next inference after user/tool-output boundaries and does not inject after assistant-only continuations.
  - Align compaction/window semantics so a new context window forces a fresh reminder even when the time interval has not elapsed.

## Config Requirements New-Thread Defaults Rust Parity Update
- Status: implemented for the app-server `config_rpc.rs::config_requirements_read_includes_new_thread_model_defaults` fixture.
- Rust reference: `app-server/tests/suite/v2/config_rpc.rs` and `app-server/README.md` `configRequirements/read`.
- Go changes:
  - `config.NewConfigService` now loads `${CODEX_HOME}/requirements.toml` during construction, so managed requirements are available to app-server runtime services without test-only `SetRequirements` injection.
  - Load failures are surfaced through the existing config warning channel while preserving the constructor signature.
  - Added `TestNewConfigServiceLoadsRequirementsFileLikeRust` for `[models.new_thread]` parsing through the service boundary.
  - Added `TestRuntimeRouterConfigRequirementsReadIncludesNewThreadModelDefaultsLikeRust` to lock the RPC response for `models.newThread.model`, `modelReasoningEffort`, and `serviceTier`.
- Verification:
  - `go test ./internal/config ./internal/appserver -run "Test(NewConfigServiceLoadsRequirementsFileLikeRust|RuntimeRouterConfigRequirementsReadIncludesNewThreadModelDefaultsLikeRust)" -count=1 -v`
  - `go test ./internal/config ./internal/appserver -count=1`
- Next follow-up candidates:
  - Continue `config_rpc.rs` fixture translation for `config/read` layers/origins, nested web-search tool config, apps, desktop settings, and project/system layer precedence.
  - Add strict error-data coverage for remaining config write/batch conflict and validation paths.

## Config Read Web Search Tool Rust Parity Update
- Status: implemented for `config_rpc.rs::config_read_includes_nested_web_search_tool_config` and `config_read_ignores_bool_web_search_tool_config`.
- Rust reference: `app-server/tests/suite/v2/config_rpc.rs`.
- Go changes:
  - Added `TestRuntimeRouterConfigReadWebSearchToolConfigMatchesRust`, which marshals the real router `config/read` result and verifies the client-visible JSON shape.
  - Locked nested `[tools.web_search]` fields: `context_size`, `allowed_domains`, and `location` with Rust's explicit nullable `region`.
  - Fixed `configValuesForJSON` so invalid legacy `[tools] web_search = true` serializes as `tools.web_search: null` instead of leaking a boolean.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterConfigReadWebSearchToolConfigMatchesRust" -count=1 -v`
  - `go test ./internal/config ./internal/appserver -count=1` initially hit known Windows ConPTY flake `TestProcessServiceSpawnTTYStreamsAndResizes`; `go test ./internal/appserver -count=1` immediately passed on rerun, and `internal/config` passed.
- Next follow-up candidates:
  - Continue `config_rpc.rs` app-server fixture translation for apps, desktop settings, project layers, and managed/system layer overrides.

## Config Read Apps And Desktop Settings Rust Parity Update
- Status: implemented for `config_rpc.rs::config_read_includes_apps` and `config_read_includes_desktop_settings`.
- Rust reference: `app-server/tests/suite/v2/config_rpc.rs`.
- Go changes:
  - Added `TestRuntimeRouterConfigReadAppsAndDesktopSettingsMatchRust`.
  - The router-level fixture verifies app origins/layers and Rust's client-visible app defaults: `_default.enabled=true`, `_default.destructive_enabled=true`, `_default.open_world_enabled=true`, app-level nullable `open_world_enabled`, `default_tools_enabled`, and `tools`.
  - The same fixture verifies opaque `[desktop]` settings, including hyphenated keys and nested `desktop.workspace` values, are preserved through `config/read`.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterConfigReadAppsAndDesktopSettingsMatchRust" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterConfig(Read(WebSearchToolConfig|AppsAndDesktopSettings)|RequirementsReadIncludesNewThreadModelDefaults)" -count=1 -v`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Continue `config_rpc.rs` with project layer origin metadata and managed/system layer override precedence.

## Config Read Managed Layer Override Rust Parity Update
- Status: implemented for `config_rpc.rs::config_read_includes_system_layer_and_overrides`.
- Rust reference: `app-server/tests/suite/v2/config_rpc.rs` and `app-server-protocol/src/protocol/v2/config.rs` layer precedence comments.
- Go changes:
  - `config.NewConfigService` now honors explicit `CODEX_APP_SERVER_MANAGED_CONFIG_PATH` by loading that TOML as a `legacyManagedConfigTomlFromFile` layer.
  - Config warnings are emitted through the existing warning channel if the explicit managed config cannot be parsed.
  - Config layer merge ordering now follows Rust protocol precedence semantics: layers with larger precedence values are applied later and override lower-precedence layers.
  - Existing managed override tests now use Rust's legacy managed-file layer instead of `enterpriseManaged`, which has lower precedence than user config in the protocol ordering.
  - Added `TestNewConfigServiceLoadsManagedConfigFromAppServerEnvLikeRust`.
  - Added `TestRuntimeRouterConfigReadIncludesManagedLayerOverridesLikeRust`, covering managed overrides for `model`, `approval_policy`, and nested `sandbox_workspace_write.writable_roots`, while preserving user `sandbox_mode` and `network_access`.
- Verification:
  - `go test ./internal/config -run "Test(NewConfigServiceLoadsManagedConfigFromAppServerEnvLikeRust|ServiceManagedLayersOverride|ServiceWriteReportsOverriddenByManagedLayer|ServiceReadIncludesProjectConfigForCWD|LayerSourcePrecedence)" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterConfigReadIncludesManagedLayerOverridesLikeRust" -count=1 -v`
  - `go test ./internal/config ./internal/appserver -count=1`
- Next follow-up candidates:
  - Finish `config_rpc.rs::config_read_includes_project_layers_for_cwd` at router level, then continue remaining config write/desktop batch/reload fixtures.

## Config Read Project Layer Rust Parity Update
- Status: implemented for `config_rpc.rs::config_read_includes_project_layers_for_cwd`.
- Rust reference: `app-server/tests/suite/v2/config_rpc.rs`.
- Go changes:
  - Added `TestRuntimeRouterConfigReadIncludesProjectLayerForCWDLikeRust`.
  - The router-level fixture writes a trusted project entry, creates `.codex/config.toml`, calls `config/read` with `cwd`, and verifies `model_reasoning_effort = "high"` comes from the project layer.
  - The test also locks project origin metadata (`type=project`, `dotCodexFolder`) and layer ordering (`user` then `project`) at the app-server boundary.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterConfigReadIncludesProjectLayerForCWDLikeRust" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterConfig(Read(WebSearchToolConfig|AppsAndDesktopSettings|IncludesManagedLayerOverrides|IncludesProjectLayerForCWD)|RequirementsReadIncludesNewThreadModelDefaults)" -count=1 -v`
  - `go test ./internal/config ./internal/appserver -count=1` hit a known Windows TempDir cleanup race once in the recommended-plugins test; `go test ./internal/appserver -count=1` passed on rerun, and `internal/config` passed.
- Next follow-up candidates:
  - Continue `config_rpc.rs` write fixtures: value write replacement, desktop write, batch write, version conflicts, and hot reload.

## Config Write Success Paths Rust Parity Update
- Status: implemented for `config_value_write_replaces_value`, `config_value_write_updates_desktop_settings`, `config_batch_write_applies_multiple_edits`, and `config_batch_write_updates_multiple_desktop_settings`.
- Rust reference: `app-server/tests/suite/v2/config_rpc.rs`.
- Go changes:
  - Added `TestRuntimeRouterConfigWriteSuccessPathsMatchRust`.
  - The router fixture covers value write with `expectedVersion`, verifies `status=ok`, `filePath=config.toml`, no `overriddenMetadata`, and confirms the next `config/read` sees the new value.
  - The fixture also covers `desktop.appearanceTheme`, multi-edit sandbox batch writes, and multi-edit desktop batch writes.
  - Fixed `valuesEqual` normalization for nested map values and integer-like JSON floats, preventing false `okOverridden` results when TOML reload returns `int64` but JSON request params carry `float64`/`int`.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterConfigWriteSuccessPathsMatchRust" -count=1 -v`
  - `go test ./internal/config ./internal/appserver -run "Test(ConfigReadResponseMarshalRustShape|ServiceWriteValueAndBatchWrite|ServiceWriteReportsOverriddenByManagedLayer|RuntimeRouterConfig(WriteSuccessPathsMatchRust|WriteErrorDataMatchesRust|BatchWriteRejectsLegacyProfilesAtomicallyLikeRust))" -count=1 -v`
  - `go test ./internal/config ./internal/appserver -count=1` hit known Windows ConPTY output flake once; `go test ./internal/appserver -count=1` passed on rerun, and `internal/config` passed.
- Next follow-up candidates:
  - Continue config write fixtures for pipelined write/read ordering and hot reload of loaded threads after `reloadUserConfig=true`.

## Config Read Forced Workspace IDs Rust Parity Update
- Status: implemented for `config_read_accepts_legacy_forced_chatgpt_workspace_id` and `config_read_accepts_forced_chatgpt_workspace_id_list`.
- Rust reference: `app-server/tests/suite/v2/config_rpc.rs` and `app-server-protocol/src/protocol/v2/config.rs` `ForcedChatgptWorkspaceIds`.
- Go changes:
  - Added `TestRuntimeRouterConfigReadForcedWorkspaceIDsMatchRust`.
  - The router fixture verifies the client-visible JSON preserves Rust's untagged shape: a single string for legacy single workspace id and an array for multiple workspace ids.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterConfigReadForcedWorkspaceIDsMatchRust" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterConfig(Read(WebSearchToolConfig|ForcedWorkspaceIDs|AppsAndDesktopSettings|IncludesManagedLayerOverrides|IncludesProjectLayerForCWD)|RequirementsReadIncludesNewThreadModelDefaults|WriteSuccessPathsMatchRust|WriteErrorDataMatchesRust|BatchWriteRejectsLegacyProfilesAtomicallyLikeRust)" -count=1 -v`
  - `go test ./internal/config ./internal/appserver -count=1`
- Next follow-up candidates:
  - Finish any remaining `config_rpc.rs` edge cases, then move back to thread/turn/app-server error-data fixtures.

## Config Read Effective Layers Rust Parity Update
- Status: implemented for `config_read_returns_effective_and_layers`.
- Rust reference: `app-server/tests/suite/v2/config_rpc.rs`.
- Go changes:
  - Added `TestRuntimeRouterConfigReadReturnsEffectiveAndLayersLikeRust`.
  - The router fixture verifies effective user config values, `origins.model` user file metadata, and the app-server `layers` array when `includeLayers=true`.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterConfig(Read(WebSearchToolConfig|ForcedWorkspaceIDs|ReturnsEffectiveAndLayers|AppsAndDesktopSettings|IncludesManagedLayerOverrides|IncludesProjectLayerForCWD)|RequirementsReadIncludesNewThreadModelDefaults|WriteSuccessPathsMatchRust|WriteErrorDataMatchesRust|BatchWriteRejectsLegacyProfilesAtomicallyLikeRust)" -count=1 -v`
- Next follow-up candidates:
  - Treat `config_rpc.rs` as largely covered at router level; continue with Rust `thread.rs`, `turn.rs`, and remaining business error-data fixtures.

## Requirements Clone Empty Slice Rust Parity Fix
- Status: fixed after full `internal/...` regression exposed a debug-config requirement rendering gap.
- Rust reference: `requirements.toml` semantics from `config_rpc.rs` and TUI debug-config rendering expectations.
- Go changes:
  - Fixed `cloneRequirements` so explicit empty slices remain non-nil instead of being collapsed to nil.
  - This preserves `allowed_web_search_modes = []` as an explicit managed requirement, allowing debug-config to render `allowed_web_search_modes: disabled` like Rust.
- Verification:
  - `go test ./internal/app ./internal/config -run "TestInteractiveDebugConfigReaderUsesRustStyleRenderer|TestNewConfigServiceLoadsRequirementsFileLikeRust|TestRequirementsClone|TestLoadRequirementsFileParsesRustStyleTOML" -count=1 -v`
  - `go test ./internal/... -count=1`
- Next follow-up candidates:
  - Move on from `config_rpc.rs` to Rust app-server `thread.rs`, `turn.rs`, and remaining business error-data fixtures.

## Request Validation Remote Image URL Rust Parity Update
- Status: implemented for `request_validation.rs::request_handlers_reject_remote_image_urls`.
- Rust reference: `app-server/tests/suite/v2/request_validation.rs`.
- Go changes:
  - Upgraded the runtime-router remote-image validation fixture to cover all three Rust request handlers in one app-server-level flow: `turn/start`, `turn/steer`, and `thread/inject_items`.
  - The test starts a real thread first so `thread/inject_items` passes runtime loaded-thread gating before hitting the payload validator, matching the Rust fixture shape.
  - The fixture sends raw JSON-shaped params with `HTTP://` and `https://` remote image URLs plus a nested `function_call_output.output.content[].image_url`.
  - Locked the JSON-RPC error shape to Rust parity: code `-32600`, exact message `remote image URLs are not supported; use an inline data URL instead`, nil `error.data`, and no serialized `data` field.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterRequestHandlersRejectRemoteImageURLsLikeRust" -count=1 -v`
  - `go test ./internal/appserver -run "RequestHandlersRejectRemoteImageURLsLikeRust|InjectItemsRejectsRemoteImageURLs" -count=1 -v`
- Next follow-up candidates:
  - Continue Rust app-server suite parity with `turn.rs` request/error semantics and any remaining thread lifecycle fixtures that still differ at the JSON-RPC boundary.

## Turn Start Skills Budget Warning Rust Parity Update
- Status: implemented for the warning payload behavior covered by `turn_start.rs::turn_start_emits_thread_scoped_warning_notification_for_trimmed_skills`.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs` and `core-skills/src/render.rs`.
- Go changes:
  - `prompt.RenderAvailableSkills` now mirrors Rust's token-budget warning prefix: token budgets say `Exceeded skills context budget of 2%.`, while character budgets keep the legacy `Exceeded skills context budget.` prefix.
  - Added prompt-level regression coverage for the token-budget warning text.
  - Strengthened `TestRuntimeRouterSkillsContextEmitsBudgetWarning` so the app-server warning notification must include the originating `threadId` and the Rust token-budget warning prefix.
- Verification:
  - `go test ./internal/prompt ./internal/appserver -run "TestRenderAvailableSkillsTokenBudgetWarningMentionsPercentLikeRust|TestRuntimeRouterSkillsContextEmitsBudgetWarning" -count=1 -v`
  - `go test ./internal/prompt ./internal/appserver -run "Test(RenderAvailableSkills|DefaultSkillMetadataBudget|RuntimeRouterSkillsContext|RuntimeRouterTurnStartInjectsAvailableSkills|RuntimeRouterImplicitSkillInvocationFromShellCommand)" -count=1 -v`
- Next follow-up candidates:
  - Continue `turn_start.rs` parity around service-tier forwarding, originator HTTP headers, and command/file-change approval notification details.

## Turn Start Service Tier Forwarding Rust Parity Update
- Status: implemented for `turn_start.rs::turn_start_sends_service_tier_id_to_model_request` at the app runtime boundary.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs`.
- Go changes:
  - Added `TestRuntimeRouterTurnStartSendsServiceTierIDToModelRequestLikeRust`.
  - The test starts a thread with a model that supports a non-default service tier, sends `turn/start` with explicit `serviceTier`, and verifies the runtime `model.AgentRequest` carries the same service tier.
  - This complements the existing lower-level Responses-agent test that serializes `AgentRequest.ServiceTier` as the HTTP `service_tier` field and existing settings/null-service-tier coverage.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStartSendsServiceTierIDToModelRequestLikeRust|TestRuntimeRouterTurnStartNullServiceTierClearsConfigDefault|TestRuntimeRouterThreadSettingsUpdateAffectsFutureTurn" -count=1 -v`
- Next follow-up candidates:
  - Continue `turn_start.rs` parity around originator HTTP headers, analytics fields, and command/file-change approval notification details.

## Turn Start Notifications and Model Override Rust Parity Update
- Status: implemented for `turn_start.rs::turn_start_emits_notifications_and_accepts_model_override`.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs`, `app-server/src/bespoke_event_handling.rs`, and `app-server/src/request_processors/turn_processor.rs`.
- Go changes:
  - Runtime `turn/completed` notifications now match Rust's lightweight payload shape: `itemsView:"notLoaded"` with an empty `items` array, while preserving turn id, status, error, and timing fields.
  - Shared the completed-turn notification constructor across normal completion, failure, interruption, and standalone `thread/shellCommand` completion.
  - Added `TestRuntimeRouterTurnStartEmitsNotificationsAndAcceptsModelOverrideLikeRust`, covering two turns on one thread: started/completed notifications use `notLoaded` empty items, the second turn gets a distinct id, and `turn/start.model` reaches `model.AgentRequest.Model`.
  - Added `waitForTurnStartedStatus` so tests can wait by turn id instead of accidentally matching an earlier `turn/started` notification.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStartEmitsNotificationsAndAcceptsModelOverrideLikeRust|TestRuntimeRouterTurnStartRunsRuntimeAndPersistsItems" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartEmitsNotificationsAndAcceptsModelOverrideLikeRust|TurnStartRunsRuntimeAndPersistsItems|TurnFailureClearsActiveStateAndAllowsNextTurn|TurnInterruptCancelsActiveRuntime|ThreadShellCommandStandaloneCompletes|ThreadShellCommand)" -count=1 -v`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Continue `turn_start.rs` with collaboration-mode override, analytics/client metadata fields, and command/file-change approval notification parity.

## Turn Start Collaboration Mode Override Rust Parity Update
- Status: implemented for `turn_start.rs::turn_start_accepts_collaboration_mode_override_v2`.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs`, `app-server/src/request_processors/turn_processor.rs`, `protocol/src/config_types.rs`, and `collaboration-mode-templates/templates/default.md`.
- Go changes:
  - `turn/start.collaborationMode` now participates in `thread/settings/updated` and can be inherited by later turn starts.
  - Runtime turn preparation now applies `collaborationMode.settings.model` and `collaborationMode.settings.reasoning_effort` as the effective per-turn model and reasoning effort, matching Rust's `CollaborationMode::model()` / `reasoning_effort()` source of truth.
  - Default collaboration mode with `developer_instructions: null` now injects Rust's built-in Default mode developer block as a `<collaboration_mode>` developer input item, including the `request_user_input` availability warning.
  - Added `TestRuntimeRouterTurnStartAcceptsCollaborationModeOverrideLikeRust`.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStartAcceptsCollaborationModeOverrideLikeRust" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartAcceptsCollaborationModeOverrideLikeRust|TurnStartEmitsNotificationsAndAcceptsModelOverrideLikeRust|TurnStartSendsServiceTierIDToModelRequestLikeRust|ThreadSettingsUpdateAffectsFutureTurn|TurnStartChangesPersonalityMidThreadLikeRust)" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterPlanModeStreamsProposedPlanItem" -count=1 -v`
  - `go test ./internal/appserver -count=1` passed on rerun after one Windows TempDir cleanup race.
- Next follow-up candidates:
  - Continue `turn_start.rs` around feature-overridden `request_user_input` descriptions, analytics fields, and command/file-change approval notification details.

## Turn Start Request User Input Feature Override Rust Parity Update
- Status: implemented for `turn_start.rs::turn_start_uses_thread_feature_overrides_for_request_user_input_tool_description_v2`.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs` and `core/src/tools/handlers/request_user_input_spec.rs`.
- Go changes:
  - `thread/start.config` is now persisted into thread metadata and merged into later `turn/start` params unless the turn supplies its own override.
  - Runtime config loading applies per-turn config overrides with dotted-key expansion via `config.ApplyOverrides`, so Rust-style keys such as `features.default_mode_request_user_input` affect `Config.FeatureSettings()`.
  - `request_user_input` tool schema now uses the Rust description, including `autoResolutionMs` guidance and mode availability text.
  - Tool registry options now carry `RequestUserInputAvailableModes`; app-server sets it to `["Default","Plan"]` when `default_mode_request_user_input` is enabled and avoids reusing the cached default tool router for that feature-specific schema.
  - Added `TestRequestUserInputHandlerSpecDescriptionMatchesRustModes` and `TestRuntimeRouterTurnStartUsesThreadFeatureOverridesForRequestUserInputToolDescriptionLikeRust`.
- Verification:
  - `go test ./internal/tool -run "TestRequestUserInput" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(UsesThreadFeatureOverridesForRequestUserInputToolDescriptionLikeRust|AcceptsCollaborationModeOverrideLikeRust)" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartUsesThreadFeatureOverridesForRequestUserInputToolDescriptionLikeRust|TurnStartAcceptsCollaborationModeOverrideLikeRust|TurnStartEmitsNotificationsAndAcceptsModelOverrideLikeRust|TurnStartSendsServiceTierIDToModelRequestLikeRust|ThreadSettingsUpdateAffectsFutureTurn|TurnStartChangesPersonalityMidThreadLikeRust)" -count=1 -v`
  - `go test ./internal/tool ./internal/turn -count=1`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Continue `turn_start.rs` analytics/client metadata fields and command/file-change approval notification details.

## Turn Start Analytics Event Shape Rust Parity Update
- Status: partially implemented as the local `codex_turn_event` payload contract for `turn_start.rs::turn_start_tracks_thread_originator_in_analytics`; full analytics transport/reducer wiring remains pending.
- Rust reference: `analytics/src/events.rs::CodexTurnEventParams`, `analytics/src/reducer.rs::codex_turn_event_params`, and `app-server/tests/suite/v2/turn_start.rs::turn_start_tracks_thread_originator_in_analytics`.
- Go changes:
  - Added `internal/telemetry` turn event payload types and `NewCodexTurnEvent`.
  - The builder preserves Rust's nested `app_server_client` shape, includes null-valued optional fields instead of omitting them, defaults empty `service_tier` to `default`, and applies thread-originator override to `app_server_client.product_client_id`.
  - Added tests for the exact Rust serialization shape and the app-server thread-originator override behavior used by the Rust fixture.
- Verification:
  - `go test ./internal/telemetry -run "TestCodexTurnEvent" -count=1 -v`
  - `go test ./internal/telemetry -count=1`
  - `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartUsesThreadFeatureOverridesForRequestUserInputToolDescriptionLikeRust|TurnStartPassesResponsesAPIClientMetadata|TurnStartPreservesThreadOriginator|ThreadStartUsesConnectionClientInfoOriginator)" -count=1 -v`
- Next follow-up candidates:
  - Wire app-server runtime facts into the telemetry turn-event builder and analytics delivery, then continue command/file-change approval notification parity.

## Turn Start Command/File Change Approval Rust Parity Update
- Status: implemented for `turn_start.rs::turn_start_exec_approval_toggle_v2`, `turn_start_exec_approval_decline_v2`, and `turn_start_file_change_approval_v2`.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs`, `core/src/tools/sandboxing.rs`, `core/src/tools/handlers/shell.rs`, and `core/src/tools/handlers/apply_patch.rs`.
- Go changes:
  - `approvalPolicy:"untrusted"` now makes shell executions request approval even when the model did not explicitly ask for escalated sandbox permissions; explicit escalated permissions remain restricted to `on-request`, matching Rust's guard.
  - App-server turn tool routers now inject broker-backed approval callbacks for `item/commandExecution/requestApproval` and `item/fileChange/requestApproval`.
  - `exec_command` and `apply_patch` now emit Rust-shaped `item/started` notifications before approval, with `commandExecution.status:"inProgress"` / `fileChange.status:"inProgress"` and call-id based item ids.
  - Declined command approvals now complete as `commandExecution.status:"declined"` with `exitCode:null` and `aggregatedOutput:null`.
  - `apply_patch` requests file-change approval before writing, emits `serverRequest/resolved` before the completed file-change item, and uses absolute file paths in `changes`, matching the Rust fixture.
  - Runtime completion notifications no longer emit duplicate completed items for the in-progress tool-call half of command/fileChange executions; completion is emitted for the final tool output item.
- Verification:
  - `go test ./internal/tool -run "Test(ShellExecutor|BuildShellRequest|ApplyPatchExecutor)" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(ExecApprovalToggleLikeRust|ExecApprovalDeclineLikeRust|FileChangeApprovalLikeRust)$" -count=1 -v`
  - `go test ./internal/tool -count=1`
  - `go test ./internal/appserver -run "Test(RuntimeRouterTurnStart|RuntimeRouterApplyPatch|RuntimeRouterResponsesStreaming|ThreadItem|ServerRequest|Notification|Schema)" -count=1`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/turn ./internal/tool -count=1`
- Next follow-up candidates:
  - Wire actual app-server analytics delivery using the turn-event payload builder.
  - Continue Rust `turn_start.rs` and tool approval parity for approval-for-session caching, granular policy details, and approval amendment/network approval paths.

## Turn Start Analytics Delivery Rust Parity Update
- Status: implemented for app-server runtime `codex_turn_event` emission on completed turns; full Rust HTTP analytics queue/reducer delivery remains a later transport task.
- Rust reference: `analytics/src/reducer.rs::codex_turn_event_params`, `analytics/src/events.rs::CodexTurnEventParams`, `analytics/src/client.rs`, and `app-server/tests/suite/v2/turn_start.rs::turn_start_tracks_thread_originator_in_analytics`.
- Go changes:
  - Added `telemetry.TurnEventSink` and runtime metadata helpers so app-server can emit typed `codex_turn_event` payloads without hard-wiring an HTTP exporter.
  - `RuntimeServices` now accepts an analytics sink and RPC transport metadata; `turn/start` carries the normalized connection id into the runtime goroutine.
  - Runtime completion now builds the Rust-shaped analytics event from thread lineage, session metadata, app-server client info, thread originator override, effective model/provider, service tier, approval reviewer/policy, sandbox policy/network, collaboration mode, personality, workspace kind, image count, token usage, timing profile, and tool counts.
  - Added `TestRuntimeRouterTurnStartEmitsCodexTurnAnalyticsLikeRust` to lock the Rust fixture behavior, including `app_server_client.product_client_id` coming from thread originator instead of the connected client name.
- Verification:
  - `go test ./internal/telemetry -count=1`
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStartEmitsCodexTurnAnalyticsLikeRust|TestRuntimeRouterTurnStartPreservesThreadOriginator|TestRuntimeRouterThreadStartUsesConnectionClientInfoOriginator" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStart" -count=1`
  - `go test ./internal/appserver ./internal/telemetry -count=1`
- Next follow-up candidates:
  - Add the Rust-style analytics queue/export transport to `/codex/analytics-events/events` instead of only the in-process sink.
  - Emit failed/interrupted turn analytics, `turn_initialized`/thread initialized events, accepted-line fingerprints, and steer-count/tool-item analytics.
  - Continue approval-for-session cache, granular approval policy, network approval, and amendment parity from Rust `turn_start.rs` and tool suites.

## File Change Approval Session/Decline Rust Parity Update
- Status: implemented for `turn_start_file_change_approval_accept_for_session_persists_v2` and `turn_start_file_change_approval_decline_v2`.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs`.
- Go changes:
  - Added app-server session approval caches for command and file-change approvals, keyed by thread id.
  - `FileChangeApprovalDecision.acceptForSession` now skips later `item/fileChange/requestApproval` prompts on the same thread, including later turns.
  - File-change declined outputs now preserve Rust's `status:"declined"` instead of being normalized to `failed`.
  - Added Rust-shaped app-server regressions for file-change accept-for-session persistence and decline/no-write behavior.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStartFileChangeApproval(LikeRust|AcceptForSessionPersistsLikeRust|DeclineLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStart(ExecApprovalToggleLikeRust|ExecApprovalDeclineLikeRust|FileChangeApprovalLikeRust|FileChangeApprovalAcceptForSessionPersistsLikeRust|FileChangeApprovalDeclineLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Continue Rust `turn_start.rs` after approval fixtures: command execution process id, working directory, return-code/output shape, and network/amendment approval paths.
  - Tighten command `acceptForSession` with a Rust fixture once the corresponding Rust app-server test is selected.

## Turn Sandbox/CWD And Personality Migration Rust Parity Update
- Status: implemented for stable Windows-compatible coverage of `turn_start_updates_sandbox_and_cwd_between_turns_v2`, `turn_start_with_elevated_override_does_not_persist_project_trust`, and the startup personality migration path used by `turn_start_uses_migrated_pragmatic_personality_without_override_v2`.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs`, `core/src/personality_migration.rs`, and `core/tests/suite/personality_migration.rs`.
- Go changes:
  - Added `TestRuntimeRouterTurnStartUpdatesSandboxAndCWDBetweenTurnsLikeRust`; first turn applies workspace-write/cwd settings without running a Windows workspace sandbox command, second turn runs a shell command under danger-full-access and verifies the command item uses the second cwd.
  - Added `TestRuntimeRouterTurnStartElevatedSandboxDoesNotPersistProjectTrustLikeRust`, proving `turn/start.sandboxPolicy=danger-full-access` does not write `[projects.*].trust_level = "trusted"`.
  - Added `config.MaybeMigratePersonality` and `.personality_migration` marker handling: marker short-circuit, explicit global personality skip, no-session marker-only skip, active/archived rollout user-session detection, and global `personality = "pragmatic"` persistence.
  - `NewRuntimeRouter` now invokes the migration during app-server startup, matching Rust's app-server startup behavior.
  - Added app-server coverage proving startup-migrated pragmatic personality is baked into model instructions and does not emit a `<personality_spec>` update.
- Verification:
  - `go test ./internal/config -run "TestMaybeMigratePersonality" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(StartupMigratesPragmaticPersonalityLikeRust|TurnStartUsesConfigPersonalityTemplate|TurnStartAppliesExplicitPersonality|TurnStartChangesPersonalityMidThreadLikeRust)" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(TurnStartIgnoresDeprecatedMultiAgentMode|ThreadStartIgnoresDeprecatedMultiAgentMode)$" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStartUpdates(CWDBetweenTurnsLikeRust|SandboxAndCWDBetweenTurnsLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStartElevatedSandboxDoesNotPersistProjectTrustLikeRust|TestRuntimeRouterThreadStartElevatedSandboxPersistsProjectTrust|TestRuntimeRouterThreadStartProjectTrustWriteGuards" -count=1 -v`
  - `go test ./internal/config ./internal/appserver -count=1`
- Next follow-up candidates:
  - Continue command execution notification parity where platform-stable; Rust's process-id fixture is ignored on Windows and should be gated similarly.
  - Continue network/amendment approval parity and the analytics HTTP queue/export path.

## Apply Patch Streaming Events Feature Gate Rust Parity Update
- Status: implemented for `turn_start_does_not_stream_apply_patch_change_updates_without_feature_v2` and the enabled streaming behavior covered by `turn_start_streams_apply_patch_change_updates_v2`.
- Rust reference: `app-server/tests/suite/v2/turn_start.rs` and the `apply_patch_streaming_events` feature gate.
- Go changes:
  - `responsesStreamHandler` now resolves the effective turn config, including per-turn `Config` overrides, and stores `features.apply_patch_streaming_events` in the Responses streaming notification state.
  - Apply-patch custom tool input deltas are still consumed as tool input, but `item/fileChange/patchUpdated` is only emitted when the feature is enabled.
  - The positive streaming notification regression now explicitly enables `apply_patch_streaming_events`; a new negative regression proves default/off behavior produces no `NotificationFileChangePatchUpdated`.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterResponsesStreaming(EmitsDeltaNotifications|SkipsApplyPatchPatchUpdatedWithoutFeatureLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterResponsesStreaming" -count=1 -v`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Continue Rust `turn_start.rs` command execution notification parity, especially process-id/output-shape cases with Windows gating where Rust marks the fixture ignored.
  - Continue approval network/amendment parity and the remaining analytics transport/export work.

## Command Approval Amendment/Session Rust Parity Update
- Status: implemented for the app-server command approval payload path that corresponds to Rust `ExecPolicyAmendment` and session-level command approval caching.
- Rust reference: `protocol/src/approvals.rs::ExecPolicyAmendment`, `core/tests/suite/approvals.rs`, and `app-server/tests/suite/v2/turn_start.rs` command approval fixtures.
- Go changes:
  - `item/commandExecution/requestApproval` now includes `proposedExecpolicyAmendment` as Rust's transparent `[]string` shape when the model supplies a valid `exec_command.prefix_rule`.
  - Added a router-level regression proving the broker sees the amendment on the real turn runtime approval path.
  - Added command `acceptForSession` regression coverage proving the first accepted command approval suppresses later command approval requests on the same thread.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStartExecApproval(ToggleLikeRust|DeclineLikeRust|IncludesPrefixRuleAmendmentLikeRust|AcceptForSessionPersistsLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Continue network approval and network policy amendment parity from Rust `core/tests/suite/network_approval.rs` / `approvals.rs`.
  - Keep the unified exec process-id notification fixture gated until Go has a Rust-equivalent unified exec process manager.

## Network Approval Protocol Wire Shape Rust Parity Update
- Status: implemented for the shared app-server wire enum shape used by network approval payloads.
- Rust reference: `protocol/src/approvals.rs::NetworkApprovalProtocol` and network approval assertions in `core/tests/suite/network_approval.rs` / `core/tests/suite/approvals.rs`.
- Go changes:
  - Updated `NetworkApprovalSocks5TCP` / `NetworkApprovalSocks5UDP` JSON values from Go-only camelCase to Rust's `socks5_tcp` / `socks5_udp`.
  - Extended `TestServerRequestMarshalShape` so `networkApprovalContext.protocol` is locked to the Rust snake_case shape in command approval server requests.
- Verification:
  - `go test ./internal/appserver -run "TestServerRequestMarshalShape|TestRuntimeRouterTurnStartExecApproval" -count=1 -v`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Implement the actual managed-network approval request path and network policy amendment persistence once the Go network proxy runtime reaches Rust parity.
  - Continue analytics HTTP queue/export transport.

## Analytics HTTP Queue/Export Rust Parity Update
- Status: implemented for the Rust `AnalyticsEventsClient` HTTP delivery spine used by app-server turn analytics.
- Rust reference: `analytics/src/client.rs`, `analytics/src/events.rs::TrackEventsRequest`, and `app-server/src/analytics_utils.rs`.
- Go changes:
  - Added a queue-backed `telemetry.AnalyticsEventsClient` with Rust's non-blocking buffered delivery semantics, disabled behavior for `analytics.enabled=false`, 10s request timeout, graceful close, and a lower-level HTTP exporter.
  - Added Rust-shaped `TrackEventsRequest { events: [...] }` POST delivery to `{chatgpt_base_url}/codex/analytics-events/events`, including `Content-Type: application/json` and ChatGPT account auth headers.
  - Added `config.AnalyticsEnabled(default)` / `AnalyticsEnabledValue()` to preserve Rust's `Option<bool>` behavior: explicit config wins, unset uses the app-server default flag.
  - `NewDefaultRuntimeRouterWithOptions` now configures analytics from app-server config when enabled, resolves current auth before each send, skips non-Codex-backend credentials, and closes the analytics queue with the router.
  - CLI `--analytics-default-enabled` now flows into `RuntimeRouterOptions.AnalyticsDefaultEnabled`, so app-server can match Rust's default-enabled flag path.
  - Added HTTP exporter/unit tests and a router-level regression proving a completed turn posts the Rust `{"events":[codex_turn_event]}` envelope to a local backend with auth headers.
- Verification:
  - `go test ./internal/telemetry -count=1`
  - `go test ./internal/config -count=1`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/app -count=1`
  - `go test ./internal/cli -run "TestParseAppServer|TestParse" -count=1`
  - `git diff --check` only reported existing CRLF normalization warnings.
- Next follow-up candidates:
  - Add failed/interrupted turn analytics and Rust's thread/turn initialized analytics events.
  - Continue accepted-line fingerprints, tool-item analytics, and steer-count parity from Rust `analytics/src/client.rs` / `reducer.rs`.
  - Resume the managed-network approval request path and network policy amendment persistence once the Go network proxy runtime is ready.

## Failed/Interrupted Turn Analytics Rust Parity Update
- Status: implemented for the completed-turn analytics lifecycle statuses beyond the previous success-only path.
- Rust reference: `analytics/src/reducer.rs::analytics_turn_status` and `analytics/src/analytics_client_tests.rs::turn_lifecycle_emits_failed_turn_event_with_error` / `turn_lifecycle_emits_interrupted_turn_event_without_error`.
- Go changes:
  - Active runtime turns now retain the app-server connection id and resolved run config so non-success completion paths can build the same Rust-shaped `codex_turn_event`.
  - Runtime errors after config resolution now emit `status:"failed"` analytics with timing fields and null generic error fields unless a future CodexErrorInfo mapping is available.
  - `turn/interrupt` now emits `status:"interrupted"` analytics from the active turn context, with null `turn_error` / `codex_error_*` fields like Rust.
  - Added lifecycle regressions for completed, failed, and interrupted analytics events.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurn(StartEmitsCodexTurnAnalyticsLikeRust|StartFailedEmitsCodexTurnAnalyticsLikeRust|InterruptedEmitsCodexTurnAnalyticsLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Add CodexErrorInfo / CodexErrKind mapping for failed turn analytics when Go has equivalent structured errors.
  - Implement Rust's `codex_thread_initialized` / turn initialized analytics events.
  - Continue accepted-line fingerprints and tool-item analytics.

## Thread Initialized Analytics Rust Parity Update
- Status: implemented for Rust's `codex_thread_initialized` event across app-server `thread/start`, `thread/resume`, and `thread/fork`.
- Rust reference: `analytics/src/events.rs::ThreadInitializedEventParams`, `analytics/src/reducer.rs::emit_thread_initialized`, and app-server suite fixtures `thread_start_tracks_thread_initialized_analytics`, `thread_resume_tracks_thread_initialized_analytics`, and `thread_fork_tracks_thread_initialized_analytics`.
- Go changes:
  - Added `telemetry.CodexThreadInitializedEventRequest` and a Rust-shaped builder with nested `app_server_client`, runtime metadata, model, ephemeral flag, thread source, initialization mode, subagent/parent/fork lineage, and `created_at`.
  - Converted the analytics HTTP envelope to a raw-message event union so `{"events":[...]}` can carry both `codex_turn_event` and `codex_thread_initialized` without changing the transport again.
  - `AnalyticsEventsClient` and `HTTPAnalyticsExporter` now implement both turn-event and thread-initialized sinks.
  - Runtime router emits thread initialized analytics after successful start/resume/fork lifecycle handling, using initialized connection metadata and record/request originator overrides.
  - Fork analytics follows Rust semantics by sending `forked_from_thread_id` while keeping `parent_thread_id` null for fork initialization.
  - Added telemetry serialization/HTTP regressions and app-server regressions for `new`, `resumed`, and `forked` initialization modes.
- Verification:
  - `go test ./internal/telemetry -count=1`
  - `go test ./internal/appserver -run "TestRuntimeRouterThread(Start|Resume|Fork)EmitsThreadInitializedAnalyticsLikeRust|TestRuntimeRouterConfiguredAnalyticsPostsRustTrackEventsRequest" -count=1 -v`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/telemetry ./internal/config ./internal/app ./internal/cli -count=1`
- Next follow-up candidates:
  - Add structured CodexErrorInfo / CodexErrKind mapping for failed turn analytics.
  - Continue Rust analytics reducer parity for accepted-line fingerprints, tool-item analytics, steer counts, and subagent/thread originator edge cases.
  - Resume managed-network approval request and policy amendment parity once the Go network proxy runtime is ready.

## Turn Analytics Steer Count Rust Parity Update
- Status: implemented for Rust's accepted-steer count on final `codex_turn_event` payloads.
- Rust reference: `analytics/src/reducer.rs` `TurnState::steer_count` handling and `analytics_client_tests.rs::accepted_steers_increment_turn_steer_count`.
- Go changes:
  - Active runtime turns now track accepted `turn/steer` calls by thread/turn id after app-server steer handling succeeds.
  - Successful, failed, and interrupted analytics completion paths carry the active steer count into `telemetry.NewCodexTurnEvent` instead of hard-coding `steer_count: 0`.
  - Added `TestRuntimeRouterTurnAnalyticsCountsAcceptedSteersLikeRust`, which sends two accepted steers while a turn is active and verifies the completed `codex_turn_event.steer_count` is `2`.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurn(StartEmitsCodexTurnAnalyticsLikeRust|StartFailedEmitsCodexTurnAnalyticsLikeRust|InterruptedEmitsCodexTurnAnalyticsLikeRust|AnalyticsCountsAcceptedSteersLikeRust)$" -count=1 -v`
  - `go test ./internal/turn -run "TestAgentLoopDrainsSteerMailboxBeforeNextSampling|TestSteer" -count=1 -v`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`
- Next follow-up candidates:
  - Implement Rust's separate `codex_turn_steer_event` accepted/rejected analytics event.
  - Continue accepted-line fingerprints, tool-item analytics, and structured CodexErrorInfo/CodexErrKind mapping.

## Turn Steer Analytics Event Rust Parity Update
- Status: implemented for Rust's separate `codex_turn_steer_event` analytics event on accepted and rejected app-server `turn/steer` requests.
- Rust reference: `analytics/src/events.rs::CodexTurnSteerEventParams`, `analytics/src/facts.rs::TurnSteerResult` / `TurnSteerRejectionReason`, and analytics reducer tests for accepted/rejected steer events.
- Go changes:
  - Added `telemetry.CodexTurnSteerEventRequest` with Rust's event union shape: expected/accepted turn ids, nested `app_server_client`, runtime metadata, thread/subagent/parent lineage, input-image count, result, rejection reason, and `created_at`.
  - `AnalyticsEventsClient` and `HTTPAnalyticsExporter` now implement the steer-event sink so the same Rust-shaped `{"events":[...]}` HTTP envelope carries `codex_turn_event`, `codex_thread_initialized`, and `codex_turn_steer_event`.
  - Runtime router emits accepted steer analytics after `turn/steer` is fully accepted and queued, and emits rejected steer analytics for `TurnService.Steer` failures before returning the JSON-RPC error.
  - Rejection reasons now map Go errors to Rust names for `no_active_turn`, `expected_turn_mismatch`, `empty_input`, and `input_too_large`.
  - Added telemetry serialization/HTTP regressions plus app-server accepted/rejected steer regressions.
- Verification:
  - `go test ./internal/telemetry -count=1`
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnSteer(EmitsAcceptedAnalyticsLikeRust|RejectedEmitsAnalyticsLikeRust|RejectsOversizedInputWithRustErrorData)|TestRuntimeRouterTurnAnalyticsCountsAcceptedSteersLikeRust|TestTurnSteerAnalyticsRejectionReasonMatchesRust" -count=1 -v`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`
- Next follow-up candidates:
  - Add Rust's non-steerable review/compact rejection paths once Go exposes equivalent active-turn states.
  - Continue accepted-line fingerprints, tool-item analytics, and structured CodexErrorInfo/CodexErrKind mapping.

## Accepted-Line Fingerprints Analytics Transport Rust Parity Update
- Status: implemented for the accepted-line event wire shape and Rust's isolated HTTP batching behavior.
- Rust reference: `analytics/src/accepted_lines.rs`, `analytics/src/events.rs::CodexAcceptedLineFingerprintsEventRequest`, and `analytics/src/client.rs::track_event_request_batches`.
- Go changes:
  - Corrected the accepted-line event request from Go-only `type` to Rust's outer `event_type:"codex_accepted_line_fingerprints"` with inner `event_params.event_type:"codex.accepted_line_fingerprints"`.
  - Matched Rust's explicit `null` serialization for optional `product_surface`, `model_slug`, and `repo_hash`.
  - Preserved Rust's privacy behavior: local line fingerprints are computed for counts/tests but the uploaded `line_fingerprints` payload is always empty.
  - `HTTPAnalyticsExporter.SendTrackEvents` now splits accepted-line fingerprint events into isolated single-event HTTP requests while keeping adjacent regular events batched together.
- Verification:
  - `go test ./internal/telemetry -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterConfiguredAnalyticsPostsRustTrackEventsRequest|TestRuntimeRouterTurnSteer(EmitsAcceptedAnalyticsLikeRust|RejectedEmitsAnalyticsLikeRust)|TestRuntimeRouterTurnAnalyticsCountsAcceptedSteersLikeRust" -count=1 -v`
  - `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`
- Next follow-up candidates:
  - Wire accepted-line event emission from completed runtime turns once Go has the latest-diff and repo-hash context available at completion time.
  - Continue tool-item analytics events and structured CodexErrorInfo/CodexErrKind mapping.

## Accepted-Line Fingerprints Runtime Emission Rust Parity Update
- Status: implemented for successful app-server runtime turns that produce a tracked unified diff.
- Rust reference: `analytics/src/reducer.rs::accepted_line_event_input` and `analytics/src/accepted_lines.rs::accepted_line_fingerprint_event_requests`.
- Go changes:
  - Runtime completion now snapshots the active diff tracker before it is cleared and emits `codex_accepted_line_fingerprints` when the diff has accepted added/deleted lines.
  - The emitted payload sets Rust's `product_surface:"codex"`, effective model slug, completion timestamp, aggregate line counts, and an empty `line_fingerprints` array.
  - Added an app-server regression that applies a patch through the real tool runtime, waits for turn completion, and verifies the accepted-line analytics aggregate.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust|TestRuntimeRouterApplyPatchEmitsTurnDiffUpdated" -count=1 -v`
  - `go test ./internal/telemetry -count=1`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Continue tool-item analytics and structured CodexErrorInfo/CodexErrKind mapping.

## Accepted-Line Repo Hash Rust Parity Update
- Status: implemented for completed runtime turns with a git remote available from the thread CWD.
- Rust reference: `analytics/src/accepted_lines.rs::accepted_line_repo_hash_for_cwd` and `git-utils/src/info.rs::canonicalize_git_remote_url`.
- Go changes:
  - Accepted-line runtime emission now probes `git remote -v` from the thread CWD with a short timeout and sets `repo_hash` when a remote URL is available.
  - Remote canonicalization matches Rust's common cases: `origin` is preferred, `.git` suffixes and default ports are removed, scp-style remotes are supported, and GitHub owner/repo paths are lowercased before hashing.
  - Upload behavior still matches Rust's privacy shape: local line fingerprints are computed for counts/tests, but uploaded `line_fingerprints` remains an empty array.
- Verification:
  - `go test ./internal/appserver -run "Test(CanonicalizeAcceptedLineGitRemoteURLMatchesRust|AcceptedLineRepoHashFromRemoteURLUsesCanonicalRemote|AcceptedLineParseGitRemoteURLs|RuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -count=1` passed on rerun after one Windows TempDir cleanup race.
  - `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`
- Next follow-up candidates:
  - Continue detailed guardian subagent telemetry and remaining Rust-specific CodexErrorInfo variants when equivalent Go runtime state exists.

## Command Execution Tool-Item Analytics Rust Parity Update
- Status: implemented for completed app-server `commandExecution` tool items on ordinary agent shell commands.
- Rust reference: `analytics/src/events.rs::CodexCommandExecutionEventRequest`, `CodexToolItemEventBase`, and `analytics/src/reducer.rs::tool_item_event`.
- Go changes:
  - Added Rust-shaped `codex_command_execution_event` telemetry types with flattened tool-item base fields, command source, exit code, and command action counts.
  - `AnalyticsEventsClient` and `HTTPAnalyticsExporter` now implement a command-execution event sink on the same Rust `{"events":[...]}` transport.
  - Runtime completion now emits command execution analytics for completed commandExecution items using connection client metadata, runtime metadata, thread lineage, observed timing, execution duration, terminal status, failure kind, and action counts.
  - No-review runtime events now follow Rust reducer semantics with review counts at zero and final approval outcome `unknown`.
  - Added telemetry serialization coverage and an app-server regression that executes a real `exec_command` turn and verifies the analytics event.
- Verification:
  - `go test ./internal/telemetry -run "TestCodexCommandExecutionEventSerializesExpectedRustShape|TestAnalyticsEventsClientPosts" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(CommandExecutionEmitsAnalyticsLikeRust|TurnCompletedEmitsAcceptedLineFingerprintsLikeRust)" -count=1 -v`
  - `go test ./internal/telemetry -count=1`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`
- Next follow-up candidates:
  - Extend commandExecution analytics to guardian-reviewed commands and network/additional-permission amendment edge cases.
  - Add guardian/permissions/network review paths and structured CodexErrorInfo/CodexErrKind mapping.

## File Change Tool-Item Analytics Rust Parity Update
- Status: implemented for completed app-server `fileChange` tool items produced by `apply_patch`.
- Rust reference: `analytics/src/events.rs::CodexFileChangeEventRequest`, `CodexToolItemEventBase`, and `analytics/src/reducer.rs::tool_item_event` / `patch_apply_outcome`.
- Go changes:
  - Added Rust-shaped `codex_file_change_event` telemetry types with flattened tool-item base fields and file add/update/delete/move counters.
  - `AnalyticsEventsClient` and `HTTPAnalyticsExporter` now implement a file-change event sink on the existing Rust `{"events":[...]}` analytics transport.
  - Runtime completion now emits file-change analytics for completed `apply_patch` items using connection client metadata, runtime metadata, thread lineage, observed timing, terminal status, failure kind, and per-change kind counts.
  - Outcome mapping follows Rust's patch apply mapping: completed -> `completed`, failed -> `failed/tool_error`, declined -> `rejected/approval_denied`, while in-progress items are skipped.
  - No-review runtime events now follow Rust reducer semantics with review counts at zero and final approval outcome `unknown`.
  - Added telemetry serialization coverage and an app-server regression that applies a real patch and verifies the analytics event.
- Verification:
  - `go test ./internal/telemetry -run "TestCodex(CommandExecution|FileChange)EventSerializesExpectedRustShape|TestAnalyticsEventsClientPosts" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(FileChangeEmitsAnalyticsLikeRust|CommandExecutionEmitsAnalyticsLikeRust|TurnCompletedEmitsAcceptedLineFingerprintsLikeRust)" -count=1 -v`
- Next follow-up candidates:
  - Extend command/file-change analytics to guardian-reviewed and network/additional-permission amendment edge cases.
  - Add guardian/permissions/network review paths and structured CodexErrorInfo/CodexErrKind mapping.

## Tool-Item Approval Review Summary Rust Parity Update
- Status: implemented for user-reviewed command execution and file-change approval paths.
- Rust reference: `analytics/src/reducer.rs::record_item_review_summary`, `tool_item_base`, `command_execution_review_result`, `file_change_review_result`, and `final_approval_outcome`.
- Go changes:
  - Runtime router now records per `(thread_id, turn_id, item_id)` review summaries when command execution and file-change approval server responses are resolved.
  - `codex_command_execution_event` and `codex_file_change_event` now denormalize Rust-style review counts, final approval outcomes, and requested permission flags onto completed tool-item analytics.
  - No-review tool-item analytics now uses Rust reducer default `final_approval_outcome:"unknown"` instead of the earlier placeholder `not_needed`.
  - Command approval decisions map to `user_approved`, `user_approved_for_session`, `user_denied`, or `user_aborted`; file-change decisions use the same user outcome mapping.
  - Added app-server regressions for command `acceptForSession` and file-change `decline` approval summaries flowing into analytics events.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouter(CommandExecutionEmitsAnalyticsLikeRust|FileChangeEmitsAnalyticsLikeRust|CommandExecutionAnalyticsIncludesUserReviewSummaryLikeRust|FileChangeAnalyticsIncludesUserReviewSummaryLikeRust)" -count=1 -v`
  - `go test ./internal/telemetry -run "TestCodex(CommandExecution|FileChange)EventSerializesExpectedRustShape" -count=1 -v`
- Next follow-up candidates:
  - Extend review analytics to guardian reviews and permissions/network approval response paths.
  - Add structured CodexErrorInfo/CodexErrKind mapping.

## MCP/Dynamic Tool-Call Analytics Rust Parity Update
- Status: implemented for telemetry shape and runtime emission, with app-server runtime coverage for both MCP and dynamic tools.
- Rust reference: `analytics/src/events.rs::CodexMcpToolCallEventRequest`, `CodexDynamicToolCallEventRequest`, and `analytics/src/reducer.rs::tool_item_event`, `mcp_tool_call_outcome`, `dynamic_tool_call_outcome`, `dynamic_content_counts`.
- Go changes:
  - Added Rust-shaped `codex_mcp_tool_call_event` and `codex_dynamic_tool_call_event` telemetry types reusing the shared flattened tool-item base.
  - `AnalyticsEventsClient` and `HTTPAnalyticsExporter` now implement MCP and dynamic tool-call event sinks on the existing Rust `{"events":[...]}` transport.
  - Runtime completion now emits MCP and dynamic analytics for completed app-server `mcpToolCall` / `dynamicToolCall` items, including terminal status/failure kind, timing, review summary fields, and event-specific metadata.
  - Dynamic tool analytics counts output content items by total/text/image and carries the Rust `success` optional boolean.
  - Added telemetry serialization coverage for both event types, an app-server regression using the real `item/tool/call` server request path for dynamic tools, and a full MCP HTTP runtime fixture that executes `tools/list` / `tools/call` through a model-visible MCP tool.
- Verification:
  - `go test ./internal/telemetry -run "TestCodex(MCPToolCall|DynamicToolCall|Review|CommandExecution|FileChange)EventSerializesExpectedRustShape|TestAnalyticsEventsClientPostsReviewUnionEventLikeRust" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(MCPToolCallEmitsAnalyticsLikeRust|DynamicToolCallEmitsAnalyticsLikeRust|CommandExecutionEmitsAnalyticsLikeRust|FileChangeEmitsAnalyticsLikeRust|CommandExecutionAnalyticsIncludesUserReviewSummaryLikeRust|FileChangeAnalyticsIncludesUserReviewSummaryLikeRust)" -count=1 -v`
- Next follow-up candidates:
  - Continue guardian/permissions/network review paths and structured CodexErrorInfo/CodexErrKind mapping.

## Review Analytics Event Rust Parity Update
- Status: implemented for user command-execution and file-change approval responses.
- Rust reference: `analytics/src/events.rs::CodexReviewEventRequest`, `CodexReviewEventParams`, and `analytics/src/reducer.rs::emit_review_event`.
- Go changes:
  - Added Rust-shaped `codex_review_event` telemetry types with nested app-server client/runtime metadata, thread lineage, review subject, reviewer, trigger, status, resolution, and timing fields.
  - `AnalyticsEventsClient` and `HTTPAnalyticsExporter` now implement a review-event sink on the existing Rust `{"events":[...]}` transport.
  - `ServerRequestBroker` now exposes a response-aware resolved callback so app-server can derive Rust review ids as `user:<request_id>` while preserving the existing `serverRequest/resolved` notification callback.
  - Runtime router emits review analytics when command execution and file-change approval responses resolve, using the active turn connection metadata and the same user review result mapping used by tool-item review summaries.
  - Added telemetry serialization/HTTP union coverage and app-server regressions verifying command `acceptForSession` and file-change `decline` produce both review events and denormalized tool-item summaries.
- Verification:
  - `go test ./internal/telemetry -run "TestCodexReviewEventSerializesExpectedRustShape|TestAnalyticsEventsClientPostsReviewUnionEventLikeRust|TestCodex(CommandExecution|FileChange)EventSerializesExpectedRustShape" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(CommandExecutionAnalyticsIncludesUserReviewSummaryLikeRust|FileChangeAnalyticsIncludesUserReviewSummaryLikeRust|CommandExecutionEmitsAnalyticsLikeRust|FileChangeEmitsAnalyticsLikeRust)" -count=1 -v`
- Next follow-up candidates:
  - Extend review analytics to guardian reviews and permissions/network approval response paths.
  - Add structured CodexErrorInfo/CodexErrKind mapping.

## Collab/Web/Image Tool-Item Analytics Rust Parity Update
- Status: implemented for Rust-shaped telemetry and runtime-router emission; web-search is covered through the real standalone `web.run` runtime path, while collab/image generation use router-level ThreadItem emission coverage until Go exposes full equivalent product runtime paths.
- Rust reference: `analytics/src/events.rs::CodexCollabAgentToolCallEventRequest`, `CodexWebSearchEventRequest`, `CodexImageGenerationEventRequest`, and `analytics/src/reducer.rs::tool_item_event`, `collab_tool_call_outcome`, `web_search_action_kind`, `web_search_query_count`, and `image_generation_outcome`.
- Go changes:
  - Added Rust-shaped `codex_collab_agent_tool_call_event`, `codex_web_search_event`, and `codex_image_generation_event` telemetry types flattening the shared tool-item base.
  - `AnalyticsEventsClient` and `HTTPAnalyticsExporter` now implement collab-agent, web-search, and image-generation event sinks on the existing Rust `{"events":[...]}` transport.
  - Runtime completion now emits the three remaining tool-item analytics events alongside command/file/MCP/dynamic events.
  - Web-search analytics maps `search`, `open_page`, `find_in_page`, and `other`, tracks query presence/count, and is verified through the existing standalone web-search runtime fixture.
  - Collab analytics maps Rust tool names (`spawn_agent`, `send_input`, `resume_agent`, `wait_agent`, `close_agent`) and counts receiver threads plus completed/failed agent states; image generation maps failed/error status to `failed/tool_error`.
- Verification:
  - `go test ./internal/telemetry -run "TestCodex(CollabAgentToolCall|WebSearch|ImageGeneration|MCPToolCall|DynamicToolCall)EventSerializesExpectedRustShape" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(StandaloneWebSearchMatchesRustFixture|CollabAndImageToolAnalyticsLikeRust|DynamicToolCallEmitsAnalyticsLikeRust)$" -count=1 -v`
- Next follow-up candidates:
  - Extend review analytics to guardian reviews and permissions/network approval response paths.
  - Add structured CodexErrorInfo/CodexErrKind mapping for failed turn analytics.

## Turn Failure Error Analytics Rust Parity Update
- Status: implemented for app-server runtime failures that have Go equivalents to Rust `CodexErrorInfo` / `CodexErrKind`.
- Rust reference: `analytics/src/events.rs::CodexTurnEventParams`, `analytics/src/reducer.rs::maybe_emit_turn_event`, `analytics/src/facts.rs::CodexErrKind`, and the app-server protocol `CodexErrorInfo` mapping.
- Go changes:
  - Runtime failure completion now classifies errors before emitting `turn/completed` and analytics, so classified failed turns carry `turn.error.codexErrorInfo` plus `codex_turn_event` fields `turn_error`, `codex_error_kind`, and `codex_error_http_status_code`.
  - Added mappings for `codexapi.APIError` variants, HTTP status fallbacks, deadline/cancel errors, invalid request errors, and sandbox request errors.
  - API invalid request failures now emit Rust-shaped `turn_error:"badRequest"`, `codex_error_kind:"invalid_request"`, and an HTTP status when Go has one; deadline failures emit `turn_error:"other"` and `codex_error_kind:"timeout"`.
  - Unknown generic Go failures continue to keep the structured error analytics fields null until a richer Rust-equivalent error type exists.
  - Interrupted turn analytics remains Rust-compatible with null structured error fields because the app-server interruption path is a user outcome, not an agent/runtime failure.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouterTurnStartFailed(EmitsCodexTurnAnalyticsLikeRust|AnalyticsClassifiesCodexAPIErrorLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`
- Next follow-up candidates:
  - Extend review analytics to guardian reviews and permissions/network approval response paths.
  - Add any remaining Rust-specific `CodexErrorInfo` variants once Go exposes matching runtime states, especially active-turn non-steerable and managed-network policy/amendment failures.

## Permissions/Network/Guardian Review Analytics Rust Parity Update
- Status: implemented for the remaining app-server review paths that are represented in Go today.
- Rust reference: `analytics/src/reducer.rs::ingest_server_request`, `ingest_server_response`, `ingest_effective_permissions_approval_response`, `ingest_guardian_review_completed`, `command_execution_review_result`, `effective_permissions_review_result`, `guardian_review_subject_metadata`, and `item_review_summary_key`.
- Go changes:
  - `item/permissions/requestApproval` resolved responses now emit `codex_review_event` with Rust subject `permissions`, network/sandbox/initial trigger classification, and `turn` vs `session` approval resolution.
  - Object-shaped command approval decisions now parse Rust variants `acceptWithExecpolicyAmendment` and `applyNetworkPolicyAmendment`; exec-policy amendment approvals run the command and emit `exec_policy_amendment`, while network-policy amendments emit approved/denied `network_policy_amendment` based on rule action.
  - `network_access` reviews no longer denormalize into command tool-item review summaries, matching Rust's `item_review_summary_key` exclusions for `Permissions` and `NetworkAccess`.
  - Guardian completed-review notifications now emit guardian `codex_review_event` records and denormalize only command/file/MCP guardian reviews into tool-item summaries with `guardian_approved`, `guardian_denied`, or `guardian_aborted` final outcomes.
  - Notification-driven guardian analytics runs before client notification opt-out filtering so telemetry is not lost when a client suppresses display notifications.
- Verification:
  - `go test ./internal/appserver -run "TestRuntimeRouter(CommandApprovalExecPolicyAmendmentRunsAndEmitsReviewAnalyticsLikeRust|CommandNetworkPolicyAmendmentReviewAnalyticsLikeRust|PermissionsApprovalEmitsReviewAnalyticsLikeRust|GuardianReviewCompletedEmitsReviewAnalyticsLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(CommandExecutionAnalyticsIncludesUserReviewSummaryLikeRust|FileChangeAnalyticsIncludesUserReviewSummaryLikeRust|CommandExecutionEmitsAnalyticsLikeRust|FileChangeEmitsAnalyticsLikeRust|TurnStartExecApprovalIncludesPrefixRuleAmendmentLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`
- Next follow-up candidates:
  - Add Rust-shaped `codex_guardian_review` telemetry if/when Go surfaces the detailed guardian subagent analytics payload rather than only app-server completed-review notifications.
  - Continue remaining Rust-specific `CodexErrorInfo` variants and managed-network policy persistence once equivalent Go runtime state exists.

## Compaction Analytics Event Rust Parity Update
- Status: implemented for Go app-server manual and auto compaction attempts that have app-server connection metadata.
- Rust reference: `analytics/src/events.rs::CodexCompactionEventRequest`, `analytics/src/facts.rs::CodexCompactionEvent`, `analytics/src/reducer.rs::ingest_compaction`, and `core/src/compact.rs::CompactionAnalyticsAttempt`.
- Go changes:
  - Added Rust-shaped `codex_compaction_event` telemetry types with app-server client metadata, runtime metadata, thread lineage, trigger/reason/implementation/phase/strategy/status, structured error fields, context token before/after counts, optional retained-image/summary-token/cache-token fields, and timing.
  - `AnalyticsEventsClient` and `HTTPAnalyticsExporter` now accept compaction events through the same Rust `{"events":[...]}` track-events envelope.
  - Runtime manual `thread/compact/start` now carries the JSON-RPC connection id into compaction so analytics can use the initialized client info; auto compaction carries the originating turn connection id and token-status before snapshot.
  - Compaction success now emits a completed event after the compacted record is persisted. Full compact errors emit failed events with the same `CodexErrKind` / HTTP status mapping used by failed turn analytics when Go has an equivalent error type.
  - Go maps internal compact values to Rust snake_case enums, for example `tokenLimit` / `contextWindowExceeded` -> `context_limit`, local compact -> `responses`, and remote compact runner -> `responses_compact`.
  - Added runtime regressions for both manual `thread/compact/start` and auto token-limit compaction so trigger/reason/turn id/client metadata are locked.
  - PreCompact hook stop now remains an `ErrInvalidHook` response for callers but also carries an internal `ErrCompactHookStopped` sentinel so analytics emits Rust-style `status:"interrupted"` with `codex_error_kind:"turn_aborted"`.
  - Active context tokens after compaction use Go's current compacted-history estimate because Go does not yet maintain Rust's exact cumulative session token accounting for compacted history.
- Verification:
  - `go test ./internal/telemetry -run "Test(CodexCompactionEventSerializesExpectedRustShape|AnalyticsEventsClientPostsCompactionUnionEventLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouter(ThreadCompactStartEmitsCompactionAnalyticsLikeRust|AutoCompactionEmitsCompactionAnalyticsLikeRust|PreCompactStoppedEmitsInterruptedCompactionAnalyticsLikeRust|ThreadCompactStartRunsHooks)$" -count=1 -v`
  - `go test ./internal/telemetry -count=1`
  - `go test ./internal/appserver -run "TestRuntimeRouterThreadCompactStart|TestRuntimeRouterThreadCompactStartEmitsCompactionAnalyticsLikeRust|TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust|TestRuntimeRouterConfiguredAnalyticsPostsRustTrackEventsRequest" -count=1 -v`
  - `go test ./internal/appserver -count=1`
  - `go test ./internal/telemetry ./internal/turn ./internal/config ./internal/app ./internal/cli -count=1`
- Next follow-up candidates:
  - Tighten compaction token accounting when Go gains Rust-equivalent cumulative session token usage after compacted history replacement.
  - Audit PostCompact hook stopped timing against Rust's post-compact completed-event behavior before changing Go's existing post-hook-before-save semantics.
  - Continue detailed `codex_guardian_review`, remaining Rust-specific `CodexErrorInfo`, and managed-network policy persistence when matching Go runtime state exists.

## Goal Analytics Event Rust Parity Update
- Status: implemented for app-server goal create/status-change/clear events backed by thread records.
- Rust reference: `analytics/src/events.rs::CodexGoalEventRequest`, `analytics/src/facts.rs::GoalEventKind`, `analytics/src/reducer.rs::ingest_goal`, and `ext/goal/src/analytics.rs`.
- Go changes:
  - Added Rust-shaped `codex_goal_event` telemetry with app-server client/runtime metadata, thread lineage, stable `goal_id`, event kind, snake_case goal status, token-budget presence, and nullable cumulative accounting fields.
  - `AnalyticsEventsClient` and `HTTPAnalyticsExporter` now accept goal events through the same Rust `{"events":[...]}` track-events envelope.
  - Go goals now carry an internal UUID `goalId` that is persisted in thread metadata extra but intentionally omitted from `thread/goal/set/get` response JSON, matching Rust's public v2 schema while preserving analytics identity.
  - `thread/goal/set` emits `created` for new goals and `status_changed` only when the status actually changes; objective-only edits do not emit a goal event, matching Rust `GoalAnalytics::status_changed`.
  - `thread/goal/clear` emits `cleared` using the removed goal snapshot so the event keeps the prior goal id and status.
  - Goal telemetry maps Go API camelCase statuses (`usageLimited`, `budgetLimited`) to Rust state snake_case (`usage_limited`, `budget_limited`) without changing the public API.
- Verification:
  - `go test ./internal/telemetry -run "Test(CodexGoalEventSerializesExpectedRustShape|CodexGoalEventSerializesNullOptionalsLikeRust|AnalyticsEventsClientPostsGoalUnionEventLikeRust)$" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterThreadGoalSetAndClearEmitGoalAnalyticsLikeRust$" -count=1 -v`
  - `go test ./internal/appserver -run "TestRuntimeRouterThreadGoal(PersistsInThreadStoreAndNotifies|RepairsRolloutOnlyThread|SetAndClearEmitGoalAnalyticsLikeRust)$" -count=1 -v`
  - `go test ./internal/telemetry -count=1`
  - `go test ./internal/appserver -count=1`
- Next follow-up candidates:
  - Implement Rust goal runtime accounting parity for `usage_accounted` events when Go has turn-linked goal accounting state.
  - Continue exact post-compaction token accounting and remaining Rust-specific error/guardian/policy gaps.
