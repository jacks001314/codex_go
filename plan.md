# Codex Go / Rust 100% 功能对齐计划

生成日期：2026-07-12  
统计口径：仅根据源码树静态比对，不引用既有迁移文档。  
Go 目录：`D:\qax\reagent\dev\codex_go`  
Rust 目录：`D:\qax\reagent\dev\codex-main\codex-rs`

## 目标与验收定义

目标是让 Go 版本 Codex 在功能、协议、命令行行为、持久化、运行时副作用和平台语义上对齐 Rust 版本。

100% 对齐的验收标准：

- 同一 CLI 输入下，Go/Rust 的 stdout、stderr、退出码、错误文本、help/usage、JSON/JSONL payload 一致。
- app-server JSON-RPC method、request/response/notification、错误码、字段名、null/omitempty 语义与 Rust protocol v2 一致。
- exec/review/TUI/session/sandbox/MCP/plugin/skills/network/unified exec 的用户可见行为由 Rust 源码或 Rust 测试 fixture 驱动验证。
- 生产路径不得落入 Go-only stub；stub 只能用于显式测试注入或离线 harness。
- Windows、Linux、macOS 分别按 Rust 平台语义验收；当前 Windows 主机通过不等于跨平台完成。

## 源码规模统计

| 指标 | Rust | Go |
| --- | ---: | ---: |
| 顶层实现目录 | `codex-rs` 下 101 个目录 | `internal` 下 48 个目录 |
| 源码文件 | 2446 个 `.rs` | 1413 个 `.go` |
| 测试源码 | 约 364 个 Rust test 源文件 | 522 个 Go `_test.go` |
| crate/manifest | 123 个 `Cargo.toml` | 1 个 `go.mod` |
| 主入口 | `codex-rs/cli/src/main.rs` | `cmd/codex/main.go` |

## 当前源码对齐进度统计

该统计以 Rust 顶层功能域为分母，按 Go 源码中是否存在对应实现、入口接线和测试覆盖做保守判断。它不是文档复述，也不是运行测试后的通过率。

| 分类 | 数量 | 占比 | 含义 |
| --- | ---: | ---: | --- |
| 基本对齐 | 6 | 5.9% | Go 源码中有清晰对应实现，且源码显示有专门测试或表驱动覆盖；仍需防 Rust 漂移。 |
| 部分对齐 | 72 | 71.3% | Go 有主体实现，但协议、fixture、平台边界或错误文本尚未逐项证明。 |
| 缺失/不明确 | 11 | 10.9% | Go 未见明确运行时等价物，或只存在间接/散落实现。 |
| 工具/仓库级 | 12 | 11.9% | CI、vendor、sample、构建脚本、实验 POC 等，不一定需要 Go 运行时等价物。 |

当前总进度估算：约 66%。  
解释：CLI 命令面、TUI、app-server、sandbox、network、MCP、model、turn、execserver 等 Go 源码规模已经很大，说明主体功能已迁移；但 Rust 共有 101 个顶层目录和 123 个 crate，Go 侧大多数仍属于“实现存在但未被 Rust fixture 逐项证明”的状态，因此不能按文件数量估算为接近完成。

## 源码模块对照与状态

| Rust 功能域 | Rust 源码量 | Go 对应源码 | 状态 | 下一步 |
| --- | ---: | --- | --- | --- |
| CLI/arg0/app dispatch：`cli`, `arg0` | 48 | `cmd/codex`, `internal/cli`, `internal/app` | 基本对齐 | 继续补 help/usage/error golden。 |
| feature flags：`features` | 4 | `internal/features` | 基本对齐 | 建立 Rust feature table 漂移测试。 |
| config/home/install/cloud-config | 69 | `internal/config`, `internal/install` | 部分对齐 | 对齐 profile v2、strict config、managed/cloud bundle。 |
| auth/login/keyring/aws/backend/chatgpt | 52 | `internal/auth`, `internal/chatgptapi`, `internal/model`, `internal/codexapi` | 部分对齐 | 对齐 OAuth、device code、Agent Identity、keyring fallback、backend models。 |
| model/codex-api/provider/ollama/lmstudio/models | 91 | `internal/model`, `internal/codexapi` | 部分对齐 | 对齐 Responses/WebSocket/SSE、retry、headers、debug context、catalog refresh。 |
| core/turn/tools/protocol/prompts/context | 653 | `internal/turn`, `internal/tool`, `internal/protocol`, `internal/prompt`, `internal/context` | 部分对齐 | 以 Rust core tests 驱动 turn lifecycle、tool schemas、prompt rendering。 |
| exec/review/resume | 29 | `internal/exec`, `internal/review`, `internal/app` | 部分对齐 | 完成 JSONL/human output/review/resume fixture。 |
| app-server/protocol/client/daemon/transport | 276 | `internal/appserver`, `internal/appserverdaemon`, `internal/remotecontrol` | 部分对齐 | schema diff、SDK fixture、transport auth、daemon lifecycle。 |
| MCP/RMCP/mcp-server | 65 | `internal/mcp`, `internal/app`, `internal/tool` | 部分对齐 | OAuth、resources/templates、elicitation/progress/roots。 |
| plugin/core-plugins/skills/core-skills/ext | 168 | `internal/plugin`, `internal/skillprovider`, `internal/systemskills`, `internal/appserver`, `internal/turn` | 部分对齐 | marketplace、remote bundle、skill authorities、progressive disclosure。 |
| TUI | 365 | `internal/tui`, `internal/app` | 部分对齐 | 用 Rust snapshots/golden 场景覆盖 composer/history/status/approval/diff。 |
| sandbox/linux/windows/bwrap/process-hardening | 89 | `internal/sandbox`, `internal/execpolicy`, `internal/shell`, `internal/execserver` | 部分对齐 | Linux bwrap/Landlock、Windows sandbox、macOS seatbelt、permission profiles。 |
| exec-server/protocol/stdio-to-uds/uds | 95 | `internal/execserver`, `internal/appserver` | 部分对齐 | process/HTTP/FS/PTY/recovery/remote relay fixture。 |
| network-proxy | 24 | `internal/network`, `internal/turn`, `internal/appserver` | 部分对齐 | HTTP/2 MITM、DNS rebinding、per-environment isolation、approval/audit。 |
| session/thread/state/rollout/history | 106 | `internal/session`, `internal/state`, `internal/rollout`, `internal/history` | 部分对齐 | SQLite schema、archive/delete/fork、trace reduce、history reinjection。 |
| telemetry/analytics/otel/feedback | 71 | `internal/telemetry`, `internal/appserver` | 部分对齐 | event names、fields、rate-limit/usage facts、feedback tags。 |
| file-search/git-utils/utils/ansi/apply-patch/hooks | 164 | `internal/filesearch`, `internal/utils`, `internal/applypatch`, `internal/appserver`, `internal/tool` | 部分对齐 | Port parser/scoring/diff/hook golden fixtures。 |
| realtime/memories/code-mode/external-agent | 70 | `internal/realtime`, `internal/memories`, `internal/codemode`, `internal/agent` | 部分对齐 | runtime protocol、memory roots、external agent import/session metadata。 |
| cloud-tasks/cloud-task-client/mock | 14 | `internal/app`, `internal/chatgptapi` | 缺失/不明确 | 明确 task browse/apply API 与 mock client。 |
| SDK/root packaging/vendor/scripts/docs/v8-poc/sample | 多目录 | 无直接 Go runtime | 工具/仓库级 | 只在发布和仓库级 100% 要求下处理。 |

## P0 开发计划：先把硬契约钉住

- [x] 建立源码派生的 parity inventory：扫描 Rust crate、CLI enum、app-server method、feature key、tool name、session item type，生成 Go 测试中的固定清单。
- [x] CLI golden：覆盖 `codex --help`、全部子命令 help、unknown flag、flag conflict、hidden command、alias、platform command。
- [x] app-server schema diff：从 Rust protocol v2 源码/生成物抽取 JSON Schema，与 Go `internal/appserver` 生成结果逐字段比较。
- [x] exec JSONL golden：覆盖 `thread.started`、`turn.started/completed/failed`、`item.started/updated/completed`、tool/error/reasoning/usage、review/resume 边界。
- [x] Responses/model client parity：对齐 request body、headers、sticky turn-state、retry、401/auth refresh、output schema、image/file/search/debug metadata。
- [x] turn/tool schema parity：`exec_command`、`write_stdin`、`apply_patch`、MCP、web/image search、skills、request_user_input、goal、plan、dynamic tools 的输入输出 schema 与错误语义。
- [x] 禁止 stub 漏出：默认 app-server 不再注入 `LocalAgentRunner`，真实 Responses agent 不可用时显式失败；默认 MCP stdio server 不再注入内存 runner；测试 harness 仍可显式注入这些实现。

P0 验收命令：

```powershell
go test ./internal/cli ./internal/features ./internal/model ./internal/turn ./internal/tool ./internal/exec ./internal/appserver ./internal/parity -count=1
go list -buildvcs=false ./...
```

## P1 开发计划：补齐主体运行时差异

- [ ] config/auth：profile v2、cloud config、managed requirements、user instructions、keyring/secret store、OAuth/device login、Agent Identity headers。
- [ ] app-server lifecycle：initialize、thread start/list/read/archive/delete/fork、turn start/interrupt、server requests、remote control、daemon bootstrap/start/stop/version。
- [ ] TUI：composer、slash commands、history cells、approval overlays、status/rate limit、diff/review panes、session resume、onboarding、clipboard、pets。
- [ ] MCP/plugin/skills：OAuth、roots/resources/templates、elicitation/progress、marketplace install/upgrade/remove/share、remote plugin cache、skill authority locator、progressive disclosure。
- [ ] sandbox/unified exec：Windows restricted/elevated、Linux bwrap/Landlock、macOS seatbelt、PTY, stdin/write, process replay, permission escalation, deny-read preservation。
- [ ] network proxy：HTTP CONNECT/forward、SOCKS5 TCP/UDP、MITM CA/trust, HTTP/2 inner policy、credential broker、approval/audit、per-thread/per-environment isolation。
- [ ] session/state/rollout：SQLite schema/migrations、thread metadata、archive/delete/fork, rollout JSONL, trace reduce, history reinjection and compaction。

P1 验收命令：

```powershell
go test ./internal/appserver ./internal/mcp ./internal/plugin ./internal/tui ./internal/sandbox/... ./internal/execserver ./internal/network ./internal/session ./internal/state ./internal/rollout -count=1
```

## P2 开发计划：仓库级和平台级收口

- [ ] 跨平台编译与平台测试：Windows 当前主机、Linux 交叉编译和 Linux sandbox 测试、macOS seatbelt 语义测试。
- [ ] telemetry/analytics/feedback：事件名、字段、采样、OpenTelemetry attributes、review/goal/plugin/skill/command/network events。
- [ ] cloud tasks：task list/get/apply、PR/diff handling、backend client models、mock client。
- [ ] release/install/update：npm/homebrew/archive/managed install context、update command、completion、doctor。
- [ ] 仓库级差异决策：Rust SDK、vendor、Bazel/Nix/scripts/docs/sample/POC 是否进入 Go 100% 范围；若不进入，写明“非 Go runtime 目标”。

P2 验收命令：

```powershell
go test ./... -count=1
go vet ./...
```

## 近期执行顺序

1. 先做 `internal/parity` 源码清单测试，确保 Rust 基准不漂移。
2. 做 CLI help/error golden，因为命令面是用户最先触达且 Rust/Go 都集中。
3. 做 app-server schema diff，避免 IDE/SDK/TUI/exec 共用协议继续分叉。
4. 做 exec JSONL 和 Responses request golden，锁住非交互主路径。
5. 按模块迁移 Rust fixture：每迁移一个 fixture，更新本计划的状态统计。

## 当前风险

- Rust 源码目录不是 Go 仓库内的一部分，后续若 Rust 源码更新，必须先刷新 parity inventory。
- Go 工作区当前已有非本次改动：`codex_go.md`、`desgn.md`、`docs/parity_matrix.md`、`docs/tui_tech_selection.md`、旧 `plan.md` 均处于删除状态，`internal/model/responses_agent_test.go`、`internal/model/responses_stream.go` 已修改。本计划不回滚这些改动。
- 本次统计没有运行完整测试，也没有执行 Rust 测试；“进度”代表源码静态覆盖和可验证性，不代表功能通过率。

## 2026-07-12 source parity implementation update

- Completed Rust-source parity for the `view_image` tool specification and runtime handler.
- Added exact input fields for `path`, optional `detail`, and optional `environment_id`, plus the Rust output schema for `image_url` and `detail`.
- Added relative-path resolution, regular-file validation, binary data URL output, Rust-compatible detail validation, and production registry wiring behind an explicit image-capability option.
- Verified `go test ./internal/tool ./internal/turn -count=1`; both packages pass. The turn package required access to the host Go build cache.
- Reconfirmed from source that `tool_search`, `list_available_plugins_to_install`, and `request_plugin_install` already have Go runtime implementations and registry wiring.

Current source-derived alignment estimate: approximately 67%. The estimate increased by one point because the previously missing `view_image` tool now has schema, runtime behavior, production reachability, and tests. MCP/plugin/skills remains partially aligned pending resource/template and approval-flow fixture coverage.

### MCP resource tool implementation update

- Added production handlers for Rust's `list_mcp_resources`, `list_mcp_resource_templates`, and `read_mcp_resource` tool surface.
- Reused the existing Go MCP inventory and resource read service instead of introducing a parallel client path.
- Preserved Rust schema descriptions, optional `server`/`cursor` fields, required `server`/`uri` fields, and unsupported-payload error semantics.
- Registered these tools when MCP is enabled and deferred `tool_search` discovery is disabled, matching the Rust tool exposure intent.
- Added a Rust-source-derived surface test and verified `internal/mcp`, `internal/tool`, and `internal/turn` packages.

Current source-derived alignment estimate: approximately 68%. MCP resource APIs are now reachable through model-callable tools; remaining MCP work is concentrated in elicitation approval events, progress notifications, roots refresh semantics, and cross-client fixtures.

### MCP elicitation, progress, and roots update

- Verified the production app-server wiring for MCP elicitation requests, tool-call progress notifications, OAuth completion, and thread-scoped roots providers.
- Aligned elicitation failure fallback with Rust app-server behavior: cancelled or timed-out client requests return `cancel`; malformed, failed, or rejected server responses return `decline`; broker errors no longer leak into the MCP transport response.
- Verified progress notifications retain thread, turn, item, server, token, numeric progress, total, message, and raw params.
- Verified roots are resolved from active turn runtime workspace roots and persisted thread metadata, and MCP resource cache keys include the normalized roots set.
- Fixed MCP resource aggregation across multiple servers so every returned resource/template retains its real `server` value required by `read_mcp_resource`.
- Verified full regression with `go test ./internal/mcp ./internal/turn ./internal/appserver -count=1`.

Current source-derived alignment estimate: approximately 69%. Remaining MCP gaps include Guardian-specific elicitation review, selected-capability-root refresh behavior, and broader Rust app-server fixture coverage.

### Selected capability roots to MCP runtime update

- Connected persisted `selectedCapabilityRoots` to the thread-scoped MCP roots provider, matching Rust's step/runtime use of ready selected capability roots.
- Local and `local` environment capability paths are merged with runtime workspace roots and deduplicated.
- Remote environment capability paths are intentionally excluded from the primary local MCP client because they are not native filesystem paths for that environment.
- Added a source-derived integration test through the real `thread/start` persistence path, including duplicate local roots and an unavailable remote environment root.
- Verified full regression with `go test ./internal/appserver ./internal/mcp ./internal/turn -count=1`.

Current source-derived alignment estimate: approximately 70%. Remaining MCP approval work is Guardian metadata validation/review and additional Rust app-server elicitation fixtures.

### Guardian and MCP request metadata update

- Ported Rust's Guardian MCP elicitation opt-in validation for `codex_request_type=approval_request` and `codex_approval_kind=mcp_tool_call`.
- Unsafe opted-in shapes now auto-decline: URL elicitations, non-empty form schemas, missing `tool_name`, non-object `tool_params`, and incorrect approval kinds.
- Requests without explicit Guardian opt-in continue through the normal app-server elicitation flow.
- Added MCP request `_meta.thread_id` propagation and Codex Apps `_codex_apps.call_id` plus connector provenance without mutating shared metadata maps.
- Fixed resource tools to request full MCP inventory, reject cursor-without-server like Rust, retain thread context for roots, reject unsupported read payloads, and wrap resource read failures as model-facing `resources/read failed` errors.
- Verified full regression repeatedly with `go test ./internal/turn ./internal/mcp ./internal/appserver -count=1`.

Current source-derived alignment estimate: approximately 72%. Guardian request validation is aligned; the remaining Guardian gap is executing the dedicated Guardian reviewer rather than forwarding valid reviewed requests to the app-server client approval flow.

### P0 stub prevention and paginated thread lifecycle update

- Removed the default production `LocalAgentRunner` from app-server construction. The router now attempts a real Responses agent and returns an explicit unavailable error when it cannot build one.
- Removed the default in-memory Codex runner from the MCP stdio server. Tests and offline harnesses may still inject memory/local runners explicitly.
- Added source-level regression coverage that prevents these production defaults from returning.
- Completed `historyMode=paginated` support across thread start, persistence, read with turns, resume, initial turns page, item list, turn list, persistent/path fork, and runtime ephemeral/active fork paths.
- Removed every production `paginated_threads is not supported yet` branch while retaining invalid future history-mode validation.
- Verified `go test ./internal/appserver ./internal/parity ./internal/model ./internal/mcp ./internal/turn -count=1`.

Current source-derived alignment estimate: approximately 75%. All P0 plan items are complete. Remaining work is concentrated in dedicated Guardian reviewer execution, config/auth edge semantics, sandbox/platform behavior, TUI fixtures, and repository-level cross-platform verification.

### Config and lifecycle parity update

- Completed paginated history lifecycle support through thread start, read, resume, items/turns listing, initial turns pages, persistent/path fork, and runtime fork paths.
- Removed all production `paginated_threads is not supported yet` branches; unknown future history modes remain rejected.
- Added strict-config nested field validation for `features.<key>` and `mcp_servers.<server>.<key>`, matching Rust's source-derived error paths while preserving non-strict compatibility.
- Confirmed invalid managed requirements remain warning-and-ignore behavior because Rust source tests explicitly require that behavior.
- Verified `internal/config`, `internal/appserver`, `internal/parity`, `internal/model`, `internal/mcp`, and `internal/turn` full package tests.

Current source-derived alignment estimate: approximately 78%. The next major gap is sandbox/platform behavior: Windows restricted/elevated execution, Linux Landlock/bwrap semantics, macOS seatbelt parity, and cross-platform fixture execution.

### Sandbox and cross-platform verification update

- Verified Go Windows ACL/elevated/unified sandbox implementations are selected by build tags and non-Windows ACL stubs return explicit unsupported errors rather than silently succeeding.
- Verified Linux permission-profile JSON accepts the Rust-shaped restricted/unrestricted/network forms and preserves deny-read entries.
- Ran full sandbox, exec-server, and tool tests successfully.
- Cross-compiled the sandbox package successfully for `linux/amd64`, `darwin/amd64`, and `windows/amd64` using `go test -c`. Direct execution of Linux/macOS test binaries is unavailable on this Windows host and was not counted as a test failure.

Current source-derived alignment estimate: approximately 80%. Sandbox behavior is covered on the current host and cross-compiled for all three target OS families; remaining platform risk is runtime-only validation on native Linux/macOS hosts.

### Source inventory correction

- Rechecked the Rust `cloud-tasks` crate against Go source: Go already contains the Cloud Tasks client, pagination models, list/status/diff/apply handlers, CLI parsing, JSON output, and HTTP authentication path under `internal/chatgptapi/cloud_tasks.go` and `internal/app/cloud.go`.
- The earlier inventory label "missing/unclear" was stale and has been corrected conceptually; no duplicate cloud-task implementation was added.
- Verified network proxy package tests and sandbox/exec-server/tool tests remain passing.

Current source-derived alignment estimate: approximately 81%. Remaining work is primarily TUI golden coverage, native Linux/macOS runtime validation, and dedicated Guardian reviewer execution.

### Network, TUI, and cloud-task source recheck

- Rust and Go network proxy mode semantics match for limited/full HTTP methods: limited allows GET/HEAD/OPTIONS, full allows CONNECT and all methods; Go proxy tests pass.
- Rust TUI source explicitly marks legacy patch/command approval requests unavailable, matching Go's existing unsupported handling; no false parity change was made.
- Go TUI package tree passes in full, including composer, approvals, history, status, onboarding, pets, streaming, and app-server request/event handling.
- Cloud Tasks was rechecked against Rust's dedicated crate and Go's `internal/chatgptapi` plus `internal/app` implementation; list/status/diff/apply/pagination and CLI surfaces are already present.

Current source-derived alignment estimate: approximately 82%. Remaining concrete runtime gap is dedicated Guardian reviewer execution; secondary gaps are dynamic tool call client flows and native non-Windows runtime validation.

### Dynamic tool call parity update

- Compared Rust `app-server/src/dynamic_tools.rs` and protocol types against Go's dynamic tool registry, app-server request broker, thread persistence, turn item conversion, and analytics path.
- Confirmed Rust wire fields are emitted exactly: `threadId`, `turnId`, `callId`, `namespace`, `tool`, `arguments`; responses emit `contentItems` and `success`.
- Confirmed Go's compatibility-only legacy inputs (`toolName`/`input`) are normalized by `MarshalJSON` and never leak onto the Rust wire format.
- Confirmed image outputs reject remote HTTP(S) URLs and become the Rust-compatible model-visible fallback; client/request failures become `dynamic tool request failed` with `success=false`.
- Added a source-derived regression test for client-error fallback and retained existing tests for server request dispatch, response resolution, remote-image rejection, persistence, and analytics.
- Verification: `go test ./internal/turn ./internal/appserver -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 83%. Dynamic tool call client flow is complete. Remaining concrete gaps are dedicated Guardian reviewer execution and native Linux/macOS runtime validation.

### Guardian MCP reviewer source audit

- Rust source (`core/src/codex_delegate.rs`, `core/src/mcp_tool_call.rs`) routes eligible MCP approvals into a dedicated Guardian agent session via `GuardianApprovalRequest::McpToolCall`, carrying call id, server, tool, arguments, connector metadata, tool metadata, and annotations.
- Rust then maps the Guardian decision: Approved -> MCP accept; ApprovedForSession -> accept-for-session; Denied -> decline with Guardian rationale; TimedOut/Abort -> synthetic decline/abort handling.
- Go currently implements the request-shape preflight, Guardian state/event store, notifications, analytics, and normal app-server elicitation broker, but has no production Guardian agent spawner or MCP-specific Guardian decision executor equivalent to the Rust path.
- This remains a genuine runtime gap. No fake approval was added. The existing safe behavior is invalid-shape auto-decline and valid-shape forwarding to the user/client approval broker.

Current source-derived alignment estimate remains approximately 83%; Guardian MCP reviewer execution is explicitly outstanding, alongside native Linux/macOS runtime validation.

### Guardian MCP decision runtime update

- Added a model-backed Guardian reviewer using the existing production `model.AgentRunner`, `state.BuildPrompt`, strict assessment output schema, and `state.ParseAssessment`.
- Guardian requests now use review task kind, `originator=guardian`, `x-openai-subagent=guardian`, thread/turn/target context, and a 90-second review timeout.
- MCP review actions carry tool arguments plus connector id/name and tool title from request metadata, matching the Rust Guardian request payload's core provenance fields.
- Structured Guardian assessments are accepted from either the response message or the final agent-message item, covering both production Responses result shapes.
- Wired valid Guardian MCP approval elicitations to the dedicated reviewer path when a production reviewer/agent is available; approved reviews return MCP `accept`, while denied, timed-out, aborted, malformed, or failed reviews return a safe MCP `decline`.
- Preserved the existing user/client approval broker as the compatibility fallback when no Guardian reviewer runtime is available.
- Added unit coverage for allow, deny, timeout, malformed assessment, request metadata/schema, MCP action construction, and handler-level decision mapping.
- Avoided constructing or caching an unavailable model agent during MCP service initialization; reviewer wiring only uses an explicitly injected or already-established production agent.
- Verification: `go test ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 84%. Guardian MCP decision execution is now present. Remaining Guardian differences are Rust's prewarmed/reused dedicated Guardian session, full review started/completed notification lifecycle for MCP elicitations, transcript/context enrichment, and circuit-breaker integration. Native Linux/macOS runtime validation also remains outstanding.

### Guardian review lifecycle update

- Extended the Guardian review store with atomic timeout and abort terminal transitions so model failures do not bypass persisted review state.
- Model-backed MCP reviews now create an in-progress review event, persist approved/denied/timed-out/aborted completion, and emit Rust-shaped `item/autoApprovalReview/started` and `item/autoApprovalReview/completed` notifications.
- Notification payloads include review id, target item id, thread/turn ids, timestamps, MCP server/tool/connector metadata, risk level, user authorization, rationale, decision source, and protocol-compatible status casing.
- Connected Guardian decisions to the existing denial circuit breaker: approvals reset consecutive denial state; denied, timed-out, and aborted reviews increment denial accounting.
- Added state-store timeout/abort tests plus reviewer lifecycle, shared review-id, terminal status, target-item, and breaker-accounting coverage.
- Verification: `go test ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 85%. Guardian MCP decision and notification lifecycles are implemented. Remaining Guardian differences are prewarming/reusing a distinct long-lived Guardian model session, richer parent-turn transcript/context input, and acting on the circuit breaker's interrupt signal. Native Linux/macOS sandbox runtime validation remains outstanding.

### Guardian circuit-breaker execution update

- Connected the Guardian denial circuit breaker's first `InterruptTurn` signal to the real app-server turn interruption path.
- Active runtime turns are cancelled and finalized through the existing interrupted lifecycle and analytics path; non-runtime turns fall back to `TurnService.Interrupt`.
- Consecutive Guardian denials now interrupt on the third denial, and the existing breaker latch prevents duplicate interrupts on subsequent denials for the same turn.
- Added reviewer-level coverage for threshold triggering, correct thread/turn routing, and single-interrupt behavior.
- Verification: `go test ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 86%. Guardian MCP decision, notification, and denial-interrupt behavior are implemented. Remaining Guardian differences are a distinct prewarmed/reused Guardian model session and richer parent-turn transcript/context input. Native Linux/macOS sandbox runtime validation remains outstanding.

### Guardian transcript context update

- Guardian review prompts now include a bounded transcript from the parent thread's persisted or ephemeral session record.
- The provider selects the latest 12 non-empty text items, preserves role/type prefixes, reverses them into chronological order, and caps each item at 4000 characters.
- This reuses the existing thread history path and avoids a parallel transcript store or unbounded prompt expansion.
- Verification: `go test ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 87%. Remaining Guardian difference is primarily the Rust-specific prewarmed/reused dedicated Guardian model session. Native Linux/macOS sandbox runtime validation remains outstanding.

### Guardian session reuse update

- Added a serialized Guardian session runner around the production `AgentRunner`.
- The first review starts the Guardian session; later reviews reuse the previous response id through `PreviousResponseID`, with `Store=false` and cloned metadata to avoid state leakage.
- Failed or response-id-less requests do not advance the reusable session state.
- Added coverage proving response-id reuse, sequential request behavior, and non-persistent Guardian requests.
- Verification: `go test ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 88%. Guardian review execution, lifecycle notifications, transcript context, denial interruption, and session reuse are implemented. Remaining differences are Guardian prewarm timing/configuration parity and native Linux/macOS sandbox runtime validation.

### Guardian prewarm and platform verification update

- Audited app-server initialization and Responses agent construction for a safe Guardian prewarm insertion point.
- Go's current `AgentRunner` has no non-generating prewarm/session-create API equivalent to Rust's dedicated Guardian spawner. Triggering a normal review request during `initialize` would change model behavior and can incorrectly cache an unavailable agent, so no fake prewarm request was added.
- The reusable Guardian session remains lazy: it is established by the first real review and reused through response ids thereafter.
- Re-ran sandbox package cross-compilation successfully for `linux/amd64`, `darwin/amd64`, and `windows/amd64` after the Guardian changes.
- Native Linux/macOS execution is still unavailable on this Windows host and is not counted as runtime verification.
- Temporary cross-compile outputs remain as `.tmp-sandbox-*.test` because the environment rejected their cleanup when its automatic approval reviewer model was unavailable.

Current source-derived alignment estimate remains approximately 88%. The remaining Guardian prewarm difference requires a real non-generating session initialization API rather than a synthetic model request. Native Linux/macOS sandbox execution remains the principal platform validation gap.

### Guardian denied-action override update

- Replaced Go's previous no-op `thread/approveGuardianDeniedAction` runtime behavior with the Rust source flow.
- The handler strictly decodes the full Guardian assessment event, returns `invalid Guardian denial event` for malformed payloads, and ignores non-denied events while succeeding.
- Denied events generate the Rust-equivalent developer message using the shared approval prefix, explicitly authorize only the exact original action, and serialize `{action, outcome:"allowed"}` into the context.
- The developer item is persisted to normal or ephemeral thread history and rollout; when the original turn is still active it is also injected through the steer mailbox without starting a new turn.
- Removed a dead test helper that still referenced the obsolete `paginated_threads is not supported yet` behavior.
- Verification: `go test ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 89%. Guardian approval override is now functional. Rust Guardian prewarm remains tied to its Responses WebSocket v2 `generate=false` transport; Go currently has HTTP/SSE only. Native Linux/macOS sandbox execution remains outstanding.

### Live session injection update

- Fixed a runtime dispatch bug where `thread/approveGuardianDeniedAction` was classified as a thread method and returned through the base router before the new live-session handler could run.
- Added a full dispatch integration test proving Guardian denied-action approvals reach the active turn mailbox through the real loaded-thread guard and runtime router.
- Aligned `thread/inject_items` with Rust's live `inject_response_items` behavior. Go now keeps the existing strict validation, session persistence, and rollout append, then also injects the original response items into the active turn's steer mailbox.
- Threads without an active turn retain persistence-only behavior; active turns receive the items before the next model sampling without starting a new turn.
- Added integration coverage proving both mailbox delivery and persisted session history through the real runtime dispatch path.
- Verification: `go test ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 90%. Remaining known structural gaps are Responses WebSocket v2/Guardian `generate=false` prewarm and native Linux/macOS sandbox execution. Further work continues through source audits of registered handlers and runtime side effects.

### Out-of-band elicitation pause update

- Rust source showed that `thread/increment_elicitation` and `thread/decrement_elicitation` do more than persist a counter: transitions between zero and non-zero pause and resume unified-exec collection for the live session.
- Added thread-scoped elicitation pause gates to `UnifiedExecManager`. Process execution continues, but result collection waits until the elicitation count returns to zero or the request context is cancelled.
- Added runtime wrappers around both canonical and legacy increment/decrement methods. The base router remains authoritative for validation, count persistence, underflow errors, and response fields; successful responses now drive the unified-exec pause state.
- Added a real helper-process test proving an exited command remains blocked while paused and returns after resume, plus app-server dispatch coverage proving count transitions drive the manager gate.
- Verification: `go test ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/tool ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 91%. Known structural gaps remain Responses WebSocket v2/Guardian prewarm and native Linux/macOS sandbox execution; active-session side-effect auditing continues.

### Active rollback safety update

- Rust core rejects thread rollback while a turn is active with `Cannot rollback while a turn is in progress.`
- Go previously passed directly to the persistence router and could truncate history underneath a running model loop.
- Added the same active-turn guard to the runtime rollback path before any session or rollout mutation.
- Added full runtime dispatch coverage proving an active thread receives the Rust-compatible rejection.
- Reconfirmed memory mode is persistence-only in Rust and Go already updates session metadata, rollout metadata, and Rust state SQLite. Reconfirmed memory reset deletes the same `stage1_outputs` and memory job rows while preserving thread memory modes and unrelated jobs.
- Verification: `go test ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/tool ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 92%. Active-session rollback safety is aligned. Structural gaps remain Responses WebSocket v2/Guardian prewarm and native Linux/macOS sandbox execution; lifecycle auditing continues.

### App-server ChatGPT login runtime update

- Replaced the app-server's placeholder ChatGPT browser URL and synthetic device code flow with the existing real OAuth implementations.
- Browser login now starts the local callback server and returns its actual authorization URL. Device login requests a real device code and asynchronously polls/exchanges it.
- Successful login reloads the persisted auth snapshot, updates account state, invalidates auth-dependent plugin caches, increments the auth revision, and emits login-completed plus account-updated notifications.
- Added per-login cancellation ownership in `RuntimeRouter`. `account/login/cancel` now cancels the browser/device context, while the completion goroutine suppresses duplicate failure notifications after explicit cancellation.
- Added injectable OAuth options for deterministic app-server tests without production stubs.
- Added local HTTP fixtures covering device-code success, persisted account activation, device cancellation, real browser authorization URL construction, and browser cancellation.
- Verification: `go test ./internal/auth ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/tool ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 94%. ChatGPT browser/device login and cancellation are now production-capable. Structural gaps remain Responses WebSocket v2/Guardian prewarm and native Linux/macOS sandbox execution; auth and lifecycle edge auditing continues.

### Pending-login lifecycle update

- Aligned Rust's active-login drop semantics across logout, external `chatgptAuthTokens` replacement, explicit login cancellation, and app-server shutdown.
- `account/logout` now cancels every browser/device OAuth context before clearing account state and revoking/removing persisted credentials, preventing a late OAuth completion from restoring auth after logout.
- External token login cancels pending interactive login attempts before installing managed auth.
- `RuntimeRouter.Close` now closes callback listeners/device polling and clears AccountManager pending login state.
- Completion ownership prevents cancelled or shutdown tasks from emitting duplicate login-failed notifications.
- Added pending-device fixtures covering logout cancellation and router-close cancellation.
- Verification: `go test ./internal/auth ./internal/appserver ./internal/state ./internal/mcp ./internal/turn ./internal/tool ./internal/parity -count=1` passed with a workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 95%. Auth login/logout/cancel lifecycles are aligned. Remaining structural gaps are Responses WebSocket v2/Guardian prewarm and native Linux/macOS sandbox execution, plus any issues exposed by repository-wide regression testing.

### Repository-wide verification update

- Ran `go test ./... -count=1`; every Go package passed on the current Windows host.
- Ran `go vet ./...`; static analysis passed without findings.
- Re-audited Responses WebSocket support. Go contains provider capability/config parsing and a real WebSocket reachability probe in `internal/doctor`, but `ResponsesAgentRunner` still samples through HTTP/SSE only.
- Rust Guardian startup prewarm is specifically a WebSocket v2 `response.create` request with `generate=false`; implementing only an HTTP field or a handshake would not provide transport parity.

## Authoritative Current Status (2026-07-13)

- Current source-derived runtime alignment: approximately 95%.
- Completed domains include CLI/protocol inventories, app-server lifecycle and live injection side effects, Responses HTTP/SSE behavior, tools/dynamic tools, MCP resources/roots/progress/elicitation, Guardian review lifecycle and overrides, real ChatGPT OAuth login/cancel/logout, paginated history, config strictness, cloud tasks, TUI regression, Windows sandbox runtime, and cross-platform sandbox compilation.
- Remaining implementation gap: full Responses WebSocket v2 transport, including `generate=false` Guardian prewarm and production event/reconnect/auth semantics.
- Remaining validation gap: native Linux Landlock/bwrap runtime tests and native macOS seatbelt runtime tests. Cross-compilation alone is already passing and is not counted as native execution.
- Current verification baseline: `go test ./... -count=1` and `go vet ./...` both pass.
- Earlier percentages and unchecked lists above are historical snapshots; this section and the latest chronological update are authoritative.

### Responses WebSocket Guardian transport update

- Added a real Responses WebSocket v2 prewarm path to `ResponsesAgentRunner` for providers that declare WebSocket support.
- Prewarm sends the Rust wire shape `type=response.create` with `generate=false`, normal model/input/instructions/tools/reasoning/text/service-tier metadata, provider/auth headers, and waits for `response.completed`.
- Added `RunWebSocket` for Guardian review sampling. It sends `previous_response_id`, omits `generate` for inference, aggregates output-text delta/done events, returns the completed response id, and maps failed/error events.
- Guardian session reuse now selects WebSocket sampling after a successful prewarm, so the warm response id is actually consumed by the first review rather than being ignored by the HTTP/SSE fallback.
- The reviewer is cached in runtime services, prewarmed once asynchronously after the real Responses agent is created, and remains lazily usable if prewarm fails.
- Added local WebSocket fixtures covering authorization headers, `generate=false`, client metadata, completion waiting, output text aggregation, normal omission of `generate`, and `previous_response_id` reuse across prewarm and multiple reviews.
- Verification: targeted model/app-server WebSocket tests, related package tests, `go test ./... -count=1`, and `go vet ./...` all passed.
- Race testing was attempted with `CGO_ENABLED=1`, but the current Windows environment has no `gcc` in `PATH`; `go test -race` cannot build `runtime/cgo` until a C compiler is installed.

Current source-derived alignment estimate: approximately 97%. Guardian WebSocket prewarm and review-session reuse are implemented. Remaining work is broader WebSocket transport parity for regular model turns (reconnect/401/426 fallback/full event mapping) and native Linux/macOS sandbox execution.

### Guardian WebSocket fallback update

- Added WebSocket review failure fallback in the Guardian session runner. A failed warm-session request clears the stale previous response id and retries through the normal HTTP/SSE agent instead of converting a transport outage into a denied approval.
- Added Rust-compatible HTTP 426 handling: prewarm treats Upgrade Required as WebSocket unavailable and remains lazy; normal WebSocket inference falls back to the existing HTTP/SSE request path.
- Added fixtures covering warm-session WebSocket selection, fallback without leaking `previous_response_id` into HTTP, session recovery after fallback, and separate 426 behavior for prewarm versus inference.
- Re-ran targeted WebSocket/model tests, related packages, `go test ./... -count=1`, and `go vet ./...`; all passed.
- `go test -race` still cannot run on this host because enabling CGO succeeds but no `gcc` executable is available in `PATH`.

Current source-derived alignment estimate: approximately 98%. Guardian prewarm/review transport and fallback behavior are aligned. Remaining implementation work is full regular-turn WebSocket event/tool/reconnect/401 parity; remaining platform validation is native Linux/macOS sandbox execution.

### Regular-turn WebSocket event and auth update

- Replaced the regular-turn WebSocket text-only parser with the shared Responses stream accumulator already used by HTTP/SSE.
- WebSocket turns now preserve completed output items, function/custom tool input deltas, reasoning/plan stream events, image/search/tool items, response metadata, timing metrics, usage, model metadata, and normal stream callbacks through the same source path as SSE.
- Retained compatibility with servers that emit output-text deltas but omit completed message output by materializing the accumulated text as an agent message at completion.
- Added WebSocket handshake 401 handling: refresh the configured external/managed/provider auth through the existing production refresh chain, rebuild headers, and retry exactly once.
- Added local WebSocket fixtures proving function-call argument delta reconstruction, completed item mapping, token usage, previous-response reuse, and 401 token/account refresh before successful reconnect.
- Rust source audit confirms connection caching belongs to a turn-scoped `ModelClientSession`; Go's runner is currently shared, so connection reuse must be added with explicit turn isolation rather than caching one connection globally.
- Verification: `go test ./internal/model -count=1`, `go test ./... -count=1`, and `go vet ./...` all passed with workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 98.5%. Full regular-turn WebSocket event and 401 auth semantics are aligned. Remaining implementation work is turn-isolated connection reuse/reconnect and session-scoped HTTP fallback; remaining platform validation is native Linux/macOS sandbox execution.

### Turn-isolated WebSocket session update

- Added WebSocket session storage keyed by thread and turn, matching Rust's requirement that connection and sticky transport state must not cross turn boundaries.
- Guardian requests without thread/turn identifiers use their subagent identity as the dedicated session key, so `generate=false` prewarm and subsequent Guardian inference now use the same physical WebSocket connection.
- Requests within one turn serialize access to their connection and reuse it across continuation/model calls; requests from different turns receive separate connections and cannot consume each other's frames.
- A stale reused connection now reconnects once when write fails or when read closes before any event for the new request. The request is not replayed after partial response events, preventing duplicate inference/tool side effects.
- Connection failures clear only the affected turn session. 401 refresh still rebuilds auth and reconnects once, while 426 retains HTTP/SSE fallback.
- Added source-level local fixtures proving one-handshake reuse within a turn, separate connections across turns, and successful reconnect after a server-closed reused connection.
- Verification: `go test ./internal/model -count=1`, `go test ./... -count=1`, and `go vet ./...` all passed with workspace-local `GOCACHE`.

Current source-derived alignment estimate: approximately 99%. Regular-turn WebSocket event mapping, auth refresh, connection reuse, turn isolation, stale-connection reconnect, Guardian prewarm, and 426 fallback are implemented. Remaining implementation gap is Rust's session-scoped permanent WebSocket disable/fallback policy after retry exhaustion; remaining platform validation is native Linux Landlock/bwrap and native macOS seatbelt execution.

### Session-scoped WebSocket fallback update

- Added runner-session WebSocket disable state matching Rust's `ModelClient::force_http_fallback` lifetime: once the retry budget is exhausted, all later turns use HTTP/SSE and prewarm becomes a no-op.
- A request with no received WebSocket events retries once on a fresh connection. If the fresh connection also fails before any event, the current request transparently runs through the existing HTTP Responses path and permanently activates fallback for that runner session.
- Requests are never replayed after any response event has arrived, preserving the earlier duplicate-side-effect protection.
- HTTP 426 now also activates permanent session fallback rather than probing WebSocket again on every later request.
- Activating fallback clears the indexed turn-session map so disabled transport state cannot be reused by later turns.
- Added a local fixture proving two failed WebSocket attempts, current-request HTTP recovery, later-turn direct HTTP use, and disabled Guardian prewarm without further WebSocket handshakes.
- Verification: `go test ./internal/model -count=1`, `go test ./... -count=1`, and `go vet ./...` all passed with workspace-local `GOCACHE`.

Current source-derived implementation alignment estimate: approximately 99.5%. All currently identified Responses WebSocket v2 transport gaps are implemented from Rust source: Guardian `generate=false` prewarm, full event mapping, previous-response continuation, turn-isolated reuse, reconnect, 401 refresh, 426 fallback, and session-scoped permanent HTTP fallback. Remaining gaps are validation/environmental: native Linux Landlock/bwrap execution, native macOS seatbelt execution, and race testing on Windows once a GCC-compatible C toolchain is available.

### Encrypted agent task identity update

- A renewed production-source scan found one concrete gap outside the previous WebSocket audit: Go rejected Agent Identity registration responses containing `encrypted_task_id` or `encryptedTaskId`, while Rust decrypts them.
- Implemented the Rust cryptographic flow using existing Go dependencies: parse the PKCS#8 Ed25519 private key, hash its seed with SHA-512, clamp the first 32 bytes into the Curve25519 secret key, derive the X25519 public key, base64-decode the response, and open the anonymous sealed box.
- Registration now accepts both snake_case and camelCase encrypted task-id fields and returns the decrypted UTF-8 task id. Plain task-id fields retain precedence.
- Error behavior distinguishes invalid base64, sealed-box authentication/decryption failure, invalid private key material, nil identity keys, and invalid UTF-8 plaintext.
- Added generated sealed-box fixtures proving successful decryption and response integration, plus malformed base64 and invalid ciphertext rejection.
- Extended the production stub leakage guard so the exec runner's default constructor cannot silently switch back to the local synthetic agent. The explicit local runner remains available only through its dedicated constructor.
- Verification: `go test ./internal/agent -count=1`, `go test ./... -count=1`, and `go vet ./...` all passed with workspace-local `GOCACHE`.

Current source-derived implementation alignment estimate: approximately 99.7%. The newly discovered encrypted Agent Identity task-id path is aligned. Remaining known gaps are native Linux/macOS sandbox runtime verification and Windows race testing without a GCC-compatible compiler; continued source scans retain a small unknown-gap allowance rather than claiming unverified 100% parity.

### Windows race verification update

- Installed MSYS2 UCRT64 GCC and verified `C:\msys64\ucrt64\bin\gcc.exe` is available.
- Verified the active compiler is MSYS2 GCC 16.1.0 with `GOOS=windows`, `GOARCH=amd64`, `CGO_ENABLED=1`, and `CC=gcc`.
- Ran the complete repository under Go's race detector with a dedicated workspace cache: `go test -race ./... -count=1`.
- The command completed successfully with no race detector findings and no package failures.

Current source-derived implementation alignment estimate remains approximately 99.7%. Windows race verification is complete. Remaining known validation gaps are native Linux Landlock/bwrap execution and native macOS seatbelt execution; the remaining 0.3% is reserved for continued source-semantic auditing rather than a known unimplemented feature.

## 维护规则

- 后续每完成一个对齐项，必须同时更新本文件的状态、验收命令和剩余风险。
- 任何“故意不同”必须写明 Rust source、Go source、原因、用户可见影响和测试。
- 新增功能优先落在现有 Go 包内，不通过平行实现绕过差异。
- 用户可见文案、JSON 字段、退出码、平台 unsupported error 均以 Rust 源码为准。
