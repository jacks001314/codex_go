# Codex Go / Rust Parity Matrix

Rust snapshot: `D:\qax\reagent\dev\codex-main\codex-rs`  
Go target: `D:\qax\reagent\dev\codex_go`  
Baseline date: 2026-07-11
Snapshot guard: `internal/parity` pins 123 Rust workspace members, critical source/test file hashes, the Rust CLI/exec/app-server/core/TUI/tools fixture roots, the Rust unified_exec/sandbox platform test matrix, and the Rust TUI `.snap` directory inventory.

This matrix uses the Rust repository as the source of truth. Each row is a Rust top-level directory under `codex-rs`, mapped to the closest Go implementation area. Status values are intentionally conservative:

- `done`: current Go code has a focused parity test or a very small complete surface.
- `partial`: implementation exists, but Rust fixture/golden/schema parity is not yet proven.
- `missing`: no clear Go runtime equivalent exists yet.
- `tooling`: repository, build, fixture, or support material rather than product runtime.
- `intentionally different`: allowed only after a Rust-referenced rationale and test.

## Directory Matrix

| Rust directory | Go target | Status | Priority | Rust-driven next check |
| --- | --- | --- | --- | --- |
| `.cargo` | `go.mod`, local build scripts | tooling | P3 | Decide whether Rust cargo config has Go release/build equivalents. |
| `.config` | None clear | tooling | P3 | Check repo-level config files for required Go packaging behavior. |
| `.github` | None clear | tooling | P3 | Compare CI/release gates if repository-level parity is in scope. |
| `agent-graph-store` | `internal/agent` | partial | P1 | Port Rust graph-store fixtures and persistence semantics. |
| `agent-identity` | `internal/auth`, `internal/model` | partial | P0 | Verify Agent Identity auth headers, fallback, and telemetry tags. |
| `analytics` | `internal/telemetry`, `internal/appserver/*analytics*` | partial | P2 | Compare Rust analytics event names and fields. |
| `ansi-escape` | `internal/utils/ansi_escape.go` | partial | P1 | Add golden tests for ANSI parsing/truncation behavior. |
| `apply-patch` | `internal/applypatch`, `internal/tool` | partial | P1 | Reuse Rust parser fixtures for add/update/delete/move failures. |
| `app-server` | `internal/appserver` | partial | P0 | Run Rust v2 app-server suite as Go protocol tests; `review/start` response now includes Rust synthesized display userMessage, target validation text, `enteredReviewMode` started/completed notifications, detached review `review_model` fork plus `thread/started`, real review runtime completion/interruption/sub-agent-error fallback with `exitedReviewMode` plus final assistant message, and review-only web/image tool restrictions; plugin/app mention runtime now follows Rust linked/structured mention boundaries so `app://...` mentions do not inject explicit plugin instructions. |
| `app-server-client` | `internal/appserver/client*`, `internal/remotecontrol` | partial | P1 | Verify client request/notification framing and errors. |
| `app-server-daemon` | `internal/appserverdaemon` | partial | P1 | Compare daemon bootstrap/start/stop/version lifecycle. |
| `app-server-protocol` | `internal/appserver/protocol.go` | partial | P0 | Generate schema diff against Rust protocol v2. |
| `app-server-test-client` | Go test harnesses | partial | P0 | `TestRustAppServerV2SuiteManifestCoversRustModules` tracks Rust v2 suite modules; next add reusable request/notification fixture runner. |
| `app-server-transport` | `internal/appserver`, `internal/remotecontrol` | partial | P1 | Verify stdio/unix/ws transport auth and framing. |
| `arg0` | `cmd/codex`, `internal/cli`, `internal/app` | partial | P3 | Compare binary alias dispatch for `apply_patch` and helpers. |
| `async-utils` | `internal/runtimeutil` | partial | P2 | Check cancellation/backoff/task-spawn semantics used by runtime. |
| `aws-auth` | `internal/auth`, `internal/model/provider*` | partial | P0 | Match Bedrock/AWS expired-signature handling and credentials lookup. |
| `backend-client` | `internal/chatgptapi`, `internal/codexapi` | partial | P0 | Compare backend request/response models and auth recovery. |
| `bwrap` | `internal/sandbox` | partial | P1 | Add Linux bubblewrap command-line parity tests. |
| `chatgpt` | `internal/chatgptapi`, `internal/auth` | partial | P0 | Verify ChatGPT OAuth/device/login status/apply command behavior. |
| `cli` | `cmd/codex`, `internal/cli`, `internal/app` | partial | P0 | Keep `Subcommand` parse table and help/error golden tests in sync. |
| `cloud-config` | `internal/config` | partial | P1 | Compare cloud bundle cache, residency, originator, and fallback. |
| `cloud-tasks` | `internal/app`, `internal/chatgptapi` | partial | P2 | Port task list/get/apply CLI fixtures. |
| `cloud-tasks-client` | `internal/chatgptapi`, `internal/codexapi` | partial | P2 | Compare backend task API models. |
| `cloud-tasks-mock-client` | Go test fakes | missing | P2 | Add mock client matching Rust fixture behavior. |
| `code-mode` | `internal/codemode` | partial | P1 | Verify code-mode runtime and tool surfaces. |
| `code-mode-host` | `internal/codemode` | partial | P1 | Compare host protocol and process lifecycle. |
| `code-mode-protocol` | `internal/codemode`, `internal/appserver` | partial | P1 | Add JSON schema/event parity tests. |
| `codex-api` | `internal/codexapi`, `internal/model` | partial | P0 | Compare Responses stream event mapping and retry behavior. |
| `codex-backend-openapi-models` | `internal/codexapi`, `internal/chatgptapi` | partial | P0 | Check generated model field names/nullability. |
| `codex-client` | `internal/model`, `internal/codexapi` | partial | P0 | Match client headers, websocket fallback, and sticky turn-state. |
| `codex-experimental-api-macros` | None clear | tooling | P3 | Determine whether generated Rust APIs need Go equivalents. |
| `codex-home` | `internal/config`, `internal/install` | partial | P1 | Compare home discovery, user instructions, and project config paths. |
| `codex-mcp` | `internal/mcp` | partial | P1 | Port MCP client/server runtime fixtures. |
| `collaboration-mode-templates` | `internal/prompt`, `internal/tui` | partial | P1 | Compare rendered collaboration mode instructions. |
| `config` | `internal/config` | partial | P1 | Reuse Rust config fixture suite for layers/strict mode. |
| `connectors` | `internal/apps`, `internal/mcp`, `internal/appserver` | partial | P1 | Verify connector discovery and app/MCP access decisions. |
| `context-fragments` | `internal/context`, `internal/prompt` | partial | P1 | Compare context fragment rendering and ordering. |
| `core` | `internal/turn`, `internal/model`, `internal/tool` | partial | P0 | Drive Go turns with Rust core suite fixtures. |
| `core-api` | `internal/appserver`, `internal/turn` | partial | P0 | Compare public core request/response structures. |
| `core-plugins` | `internal/plugin`, `internal/tool`, `internal/prompt` | partial | P1 | Verify built-in extension/plugin registration. |
| `core-skills` | `internal/prompt`, `internal/appserver` | partial | P1 | Explicit skill mention selection now matches Rust `core-skills/src/injection.rs` boundaries for structured `Skill`, `[$name](skill://path)` linked mentions, direct `$name`, common env-var ignores, non-skill resource exclusion, and no trim/decode/path-cleaning beyond stripping `skill://`; explicit skill fragments now use Rust rendered paths plus byte-boundary truncation for main prompt/name/path with Rust warning text; available-skills rendering now matches Rust token truncation warning wording, render-layer whitespace preservation, fixed default `(file: path)` labels, executor `(environment resource: skill://<selected-root-id>/.../SKILL.md)` display locators, budget-pressure alias selection, `### Skill roots` body/instructions, plugin-cache marketplace-vs-skill-root alias rules, and the model catalog `include_skills_usage_instructions` switch; local loader metadata parsing now rejects Go-only YAML aliases, preserves missing short descriptions as absent, skips invalid local `SKILL.md` with Rust-style errors outside system scope, rejects overlong names, preserves overlong descriptions/short descriptions, filters `policy.products` to Codex, honors Rust scan depth/dir limits, follows directory symlinks outside system roots, and canonicalizes skill identity paths; remote environment skills now use Rust walk limits/symlink following, emit Rust-style scan/load warnings, sort by name/path, skip invalid frontmatter/missing descriptions/overlong names, apply inventory and ancestor-probed plugin namespaces with qualified-name limits, and filter `policy.products` to Codex; `skills.list`/`skills.read` now expose Rust's orchestrator authority contract over `codex_apps` MCP resources; continue host authority unification and deeper progressive disclosure fixtures. |
| `docs` | `docs` | partial | P3 | Align user-facing docs after runtime behavior is proven. |
| `exec` | `internal/exec`, `internal/app` | partial | P0 | JSONL golden, prompt/stdin decode, Responses output-schema request parity, command_execution/web_search/file_change/mcp_tool_call/collab_tool_call/todo_list/error item parity, final-message human output, Rust-style human config summary, `resume --last/--all` cwd selection, resume by-name lookup, resume images, previous response id, basic history reinjection, non-model-visible history filtering, Rust review prompt/no-diff/merge-base behavior, `review/start` display turn, entered lifecycle, detached review `review_model` fork shape, review output JSON parsing/rollout templates/human rendering, review_model selection, review subagent metadata, Rust review rubric instructions, review image/web tool restrictions, and app-server review `exitedReviewMode` completion/interruption/sub-agent-error fallback are covered; continue richer raw Responses history fixtures and stderr progress/warning fixtures. |
| `execpolicy` | `internal/execpolicy` | partial | P1 | Compare rules parsing, host resolution, and pretty output. |
| `execpolicy-legacy` | `internal/execpolicy` | partial | P1 | Verify legacy rule compatibility and warnings. |
| `exec-server` | `internal/execserver`, `internal/app` | partial | P1 | `TestRustUnifiedExecSandboxSuiteManifest` tracks Rust pushed process-event scenarios and sandbox exec-server env context; next compare stdio/ws listen modes and remote registration behavior. |
| `exec-server-protocol` | `internal/execserver`, `internal/appserver` | partial | P1 | Add protocol schema/event parity tests; pushed process-event fixture coverage is now listed from Rust. |
| `ext` | `internal/appserver`, `internal/tool`, `internal/mcp`, `internal/plugin` | partial | P1 | Map every Rust extension provider to Go registration. |
| `external-agent-migration` | `internal/tui`, `internal/appserver` | partial | P2 | Compare external-agent import/migration behavior. |
| `external-agent-sessions` | `internal/session`, `internal/appserver` | partial | P2 | Verify imported session source kinds and metadata. |
| `features` | `internal/features` | done | P0 | Keep feature key/stage/default table synced with Rust. |
| `feedback` | `internal/appserver/feedback*` | partial | P2 | Compare feedback request tags and auth metadata. |
| `file-search` | `internal/filesearch` | partial | P1 | Port file-search scoring and ignore-rule tests. |
| `file-system` | `internal/appserver/fs*`, `internal/utils` | partial | P1 | Compare file read/write/list semantics and errors. |
| `file-watcher` | `internal/appserver/connection_file_watch.go` | partial | P1 | Verify watch subscription notifications and debounce behavior. |
| `git-utils` | `internal/utils/gitinfo.go`, `internal/review`, `internal/tui/get_git_diff.go` | partial | P0 | Review merge-base with HEAD, missing branch fallback, and upstream-ahead preference now mirror Rust; TUI `/diff` now uses Rust-style WorkspaceCommand, fsmonitor safety probe, filter override, colored tracked diff, and untracked `git diff --no-index`; continue repo root and binary edge fixtures. |
| `hooks` | `internal/tool/hooks.go`, `internal/appserver/hooks*` | partial | P1 | Port hook decision/output/error fixtures. |
| `install-context` | `internal/install` | partial | P2 | Compare npm/homebrew/archive/managed install detection. |
| `keyring-store` | `internal/auth` | partial | P1 | Verify keyring/secret storage fallback and error wording. |
| `linux-sandbox` | `internal/sandbox` | partial | P1 | Rust Unix sandbox smoke tests are listed in `TestRustUnifiedExecSandboxSuiteManifest`; next port Linux-only bubblewrap/Landlock behavior and unsupported-path tests. |
| `lmstudio` | `internal/model` | partial | P0 | Compare provider discovery and error mapping. |
| `login` | `internal/auth`, `internal/app` | partial | P1 | Match login/logout/status output and exit codes. |
| `mcp-server` | `internal/mcp` | partial | P1 | Compare Codex-as-MCP server tools/resources framing. |
| `memories` | `internal/memories`, `internal/tui`, `internal/appserver` | partial | P2 | Verify read/write/clear and memory root behavior. |
| `message-history` | `internal/history`, `internal/turn` | partial | P2 | Compare history compaction and item reinjection. |
| `model-provider` | `internal/model` | partial | P0 | Compare provider config, auth, retry, and error mapping. |
| `model-provider-info` | `internal/model` | partial | P0 | Verify provider catalog metadata and defaults. |
| `models-manager` | `internal/model`, `internal/app` | partial | P0 | Compare bundled/remote model catalog refresh and ETag behavior. |
| `network-proxy` | `internal/network`, `internal/sandbox` | partial | P1 | Verify sandbox network proxy rules and routing. |
| `ollama` | `internal/model` | partial | P0 | Compare local provider discovery and request mapping. |
| `otel` | `internal/telemetry` | partial | P2 | Match OpenTelemetry event names, counters, and attributes. |
| `plugin` | `internal/plugin` | partial | P1 | Explicit plugin/app mention collection now matches Rust `core/src/plugins/mentions.rs` and `core-skills/src/injection.rs`: markdown linked mentions and structured paths are honored, raw `$app://...`/`@plugin://...` text is not treated as a path, common env vars are ignored, structured paths are not trimmed or URL-decoded, and plugin selection matches `config_name` only; continue plugin CLI/manifest/marketplace fixture suite. |
| `process-hardening` | `internal/sandbox`, `internal/execserver` | partial | P1 | Verify platform hardening decisions and unsupported errors. |
| `prompts` | `internal/prompt`, `internal/review` | partial | P0 | Review rubric template is embedded byte-for-byte and hash-guarded; continue base/developer/skills/tool instruction rendering. |
| `protocol` | `internal/appserver`, `internal/codexapi`, `internal/turn` | partial | P0 | Match item/event types and JSON field names. |
| `realtime-webrtc` | `internal/realtime` | partial | P2 | Compare realtime session config and sideband auth. |
| `response-debug-context` | `internal/codexapi` | partial | P2 | Verify response debug files/context format. |
| `responses-api-proxy` | `internal/app`, `internal/codexapi` | partial | P0 | Compare hidden proxy CLI and dump/upstream behavior. |
| `rmcp-client` | `internal/mcp` | partial | P1 | Match MCP transport/client edge cases. |
| `rollout` | `internal/rollout`, `internal/session` | partial | P2 | Compare rollout JSONL structure and archive/delete/fork behavior. |
| `rollout-trace` | `internal/rollout`, `internal/app` | partial | P2 | Port trace-reduce fixtures and output names. |
| `sandboxing` | `internal/sandbox`, `internal/execpolicy` | partial | P1 | Rust deny-read entries are now preserved through runtime permission-profile parsing/serialization and block `require_escalated` full-access bypass; manifest tracks remaining sandbox approval, guardian bypass, Unix sandbox, and Windows sandbox suites. |
| `scripts` | `scripts`, build tooling | tooling | P3 | Decide release/build script parity scope. |
| `secrets` | `internal/auth`, `internal/safety` | partial | P1 | Verify secret redaction/storage boundaries. |
| `shell-command` | `internal/shell`, `internal/tool` | partial | P1 | Rust shell argv derivation is now matched for bash/zsh, PowerShell `-NoProfile` login behavior, and `cmd /c`; Rust TUI display helper semantics are now shared for approval UI/history, stripping only bash/zsh/sh and PowerShell while shlex-quoting fallback commands; unified_exec lifecycle/PTY/stdin/timeout/truncation test names are guarded, with runtime behavior fixtures still pending. |
| `shell-escalation` | `internal/shell`, `internal/execpolicy` | partial | P1 | Match approval/escalation request payloads and errors. |
| `skills` | `internal/appserver`, `internal/prompt`, `internal/plugin` | partial | P1 | Explicit skill mention injection now follows Rust linked/structured/direct mention semantics, including requiring the `$` sigil in markdown links, preserving paths without trim/decode/clean, matching executor display locators as well as internal environment paths, and truncating injected main-prompt/name/path bytes like Rust; available skill metadata rendering now follows Rust warning/description/label boundaries, executor `environment resource` display paths, alias-root selection/body behavior under budget pressure, and the model-specific usage-instructions switch; local skill metadata loading now follows Rust field-name, invalid-frontmatter, `policy.products`, scan-limit, symlink, and canonical identity behavior; remote environment loading now follows Rust invalid-skill, warning, walk-limit/symlink, name/path sort, inventory/ancestor plugin namespace, qualified-name, and product-filter boundaries; orchestrator authority `skills.list/read` now routes through `codex_apps` MCP resources with Rust validation/output shape; port remaining progressive-disclosure and host authority fixtures. |
| `state` | `internal/state`, `internal/session` | partial | P2 | Compare SQLite schema, migrations, and recovery. |
| `stdio-to-uds` | `internal/app`, `internal/execserver` | partial | P1 | Verify hidden relay CLI and socket behavior. |
| `terminal-detection` | `internal/tui` | partial | P1 | Compare terminal name/probing behavior and fallbacks. |
| `test-binary-support` | Go test utilities | missing | P3 | Decide whether Rust binary fixture helpers need Go equivalents. |
| `thread-manager-sample` | None clear | tooling | P3 | Treat as sample unless Rust tests require parity. |
| `thread-store` | `internal/session`, `internal/state` | partial | P2 | Compare thread persistence, list/read/delete/archive/fork. |
| `tools` | `internal/tool`, `internal/turn` | partial | P1 | Rust unified_exec handler shell selection, zsh-fork explicit-shell rejection, and deny-read-preserving escalation behavior now have Go behavior tests; remote direct mode, write_stdin handler semantics, schemas, and runtime behavior fixtures remain. |
| `tui` | `internal/tui` | partial | P1 | `/diff` default reader now matches Rust `tui/src/get_git_diff.rs` command flow and no-index untracked handling; slash command frame descriptions now cover Rust wording for review/diff/status/usage/mcp/approve and related commands; Rust TUI snapshot inventory is now guarded across 11 snapshot directories and 539 `.snap` files; `/status` permission/approval labels now match Rust `status_permissions_label`, credit amount formatting now rejects NaN/Inf/negative values like Rust `format_credit_amount`, status-line rate-limit window selection now follows Rust `five_hour_status_window`/`weekly_status_window`, terminal title/global `TruncateText` and `center_truncate_path` now follow Rust grapheme/path truncation semantics, and status/title item-id parsing is exact like Rust `parse::<...>()`; app-server request/notification thread targets, agent navigation, agent picker liveness/backfill, startup stale thread, session summary resume hint, side close errors, and MCP inventory request thread selection now use Rust `ThreadId` UUID parsing/canonicalization without whitespace trimming; session lifecycle metadata fallback, resume hint names/paths, and attach/startup errors now preserve Rust no-trim behavior; config persistence effective enum reads now reject leading/trailing whitespace like Rust typed config fields; side parent request/notification kind matching, side footer separators, and side error display now follow Rust exactness; thread event file-change lookup now uses exact turn/item id matching and preserves FileUpdateChange field whitespace like Rust typed payloads; thread goal usage/confirmation/status summary now follows Rust `goal_display` strings, grapheme truncation, no-trim objective handling, compact tokens, and day-aware elapsed formatting; external editor command splitting now preserves quoted empty args and follows Rust Windows/non-Windows quote parse differences; backtrack and side-return key helpers now treat only exact key identities as Rust `KeyCode` matches; mentions_v2 search candidates now avoid non-Rust normalized fallback, preserve empty plugin display/search-term boundaries, preserve whitespace plugin descriptions, use Rust skill display fallback for `plugin:skill` names, keep new popups unselected until query/candidate synchronization clamps them like Rust `ScrollState`, and split filesystem rows with Rust display-string `rfind(['/', '\\'])` semantics instead of path cleaning; history-cell raw transcript line splitting now matches Rust `str::lines()` behavior, notice cells now preserve Rust whitespace/prefixes plus safety policy wording, MCP inventory keeps Rust server/tool sorting while preserving resource/template order, background terminal process cells follow Rust bullet/prefix/truncation rules, and approval decision cells now use Rust heavy-check symbol plus POSIX/PowerShell command extraction, shlex fallback quoting, multiline ` ...`, and 80-grapheme ASCII `...` truncation; composer history-search case ranges include Rust's dotted-I boundary; request_user_input hidden-options footer now shows Rust `option N/total` progress, display-width wrapping, and non-splitting footer tips; approval overlay network deny shortcut now matches Rust's visible-deny-only boundary, queued approvals advance with Rust LIFO order, queued resolved requests are removed before display, execpolicy-prefix options use Rust's CR/LF-only filter, and approval command display uses the same Rust shell display helper as history; `/agent` status preview now uses Rust item de-duplication, grapheme summary truncation, and display-width wrapping; desktop handoff history messages and URL/error rendering now match Rust; composer/approval/status/history-cell remain prioritized for behavior migration. |
| `uds` | `internal/appserver`, `internal/execserver` | partial | P1 | Compare Unix socket/named pipe path semantics. |
| `utils` | `internal/utils`, `internal/runtimeutil` | partial | P1 | Port utility tests when used by user-visible behavior. |
| `v8-poc` | `internal/codemode` or none | missing | P3 | Decide scope; Rust appears experimental/proof-of-concept. |
| `vendor` | `vendor` or module cache | tooling | P3 | Compare vendored patches only for release parity. |
| `windows-sandbox-rs` | `internal/sandbox` | partial | P1 | Rust restricted-token/elevated deny-read tests are listed in `TestRustUnifiedExecSandboxSuiteManifest`; next add Windows-only WFP/ACL/runtime parity tests. |

## CLI Command Surface

Rust source: `codex-rs/cli/src/main.rs` `Subcommand` enum.

| Rust command | Rust alias/cfg | Go command | Current parity check |
| --- | --- | --- | --- |
| `exec` | visible alias `e` | `CommandExec` | `TestRustSubcommandSurfaceParity`, `TestRustSubcommandAliasParity` |
| `review` | none | `CommandReview` | `TestRustSubcommandSurfaceParity` |
| `login` | none | `CommandLogin` | `TestRustSubcommandSurfaceParity` |
| `logout` | none | `CommandLogout` | `TestRustSubcommandSurfaceParity` |
| `mcp` | none | `CommandMCP` | `TestRustSubcommandSurfaceParity` |
| `plugin` | none | `CommandPlugin` | `TestRustSubcommandSurfaceParity` |
| `mcp-server` | none | `CommandMCPServer` | `TestRustSubcommandSurfaceParity` |
| `app-server` | experimental | `CommandAppServer` | `TestRustSubcommandSurfaceParity` |
| `remote-control` | experimental | `CommandRemoteControl` | `TestRustSubcommandSurfaceParity` |
| `app` | `cfg(macos/windows)` | `CommandApp` | `TestRustSubcommandSurfaceParity`; platform gating still needs runtime/help parity |
| `completion` | none | `CommandCompletion` | `TestRustSubcommandSurfaceParity` |
| `update` | none | `CommandUpdate` | `TestRustSubcommandSurfaceParity` |
| `doctor` | none | `CommandDoctor` | `TestRustSubcommandSurfaceParity` |
| `sandbox` | platform-specific internals | `CommandSandbox` | `TestRustSubcommandSurfaceParity` |
| `debug` | none | `CommandDebug` | `TestRustSubcommandSurfaceParity` |
| `execpolicy` | hidden | `CommandExecpolicy` | `TestRustSubcommandSurfaceParity` |
| `apply` | visible alias `a` | `CommandApply` | `TestRustSubcommandSurfaceParity`, `TestRustSubcommandAliasParity` |
| `resume` | none | `CommandResume` | `TestRustSubcommandSurfaceParity` |
| `archive` | none | `CommandArchive` | `TestRustSubcommandSurfaceParity` |
| `delete` | none | `CommandDelete` | `TestRustSubcommandSurfaceParity` |
| `unarchive` | none | `CommandUnarchive` | `TestRustSubcommandSurfaceParity` |
| `fork` | none | `CommandFork` | `TestRustSubcommandSurfaceParity` |
| `cloud` | alias `cloud-tasks` | `CommandCloud` | `TestRustSubcommandSurfaceParity`, `TestRustSubcommandAliasParity` |
| `responses-api-proxy` | hidden | `CommandResponsesAPIProxy` | `TestRustSubcommandSurfaceParity` |
| `stdio-to-uds` | hidden | `CommandStdioToUDS` | `TestRustSubcommandSurfaceParity` |
| `exec-server` | experimental | `CommandExecServer` | `TestRustSubcommandSurfaceParity` |
| `features` | none | `CommandFeatures` | `TestRustSubcommandSurfaceParity` |

## Open Gaps

- This matrix proves only that a Go ownership area exists; it does not prove behavioral parity except where a test is named.
- Rust snapshot, fixture-root, unified_exec/sandbox matrix, and TUI snapshot inventory drift are guarded by `TestRustWorkspaceMembersSnapshot`, `TestRustCriticalFileHashesSnapshot`, `TestRustGoldenFixtureRootsSnapshot`, `TestRustUnifiedExecSandboxSuiteManifest`, and `TestRustTUISnapshotManifestCoversPrioritySurfaces`.
- App-server protocol schema fixtures, exec JSONL golden tests, output-schema Responses request parity, production-no-stub checks, sticky turn-state parity, Rust `review/start` display response, `enteredReviewMode` and `exitedReviewMode` notification shape including interrupted/error fallback review exit, detached review `review_model` thread fork behavior, and a Rust v2 suite manifest are now in place; next app-server work is scenario-level fixture execution.
- Platform command exposure still needs Rust-style help/availability tests, especially `app`, `sandbox`, and Windows/Linux sandbox helpers.
- Unified exec/sandbox test ownership is now mapped from Rust; shell argv and deny-read escalation behavior are covered, but PTY/stdin/session reuse/process events and Linux/macOS/Windows sandbox enforcement still need behavior fixtures.
- Plugin/app explicit mention parsing and plugin instruction injection now follow Rust linked/structured mention boundaries, including no raw URI mention parsing, no app-to-plugin reverse inference, no structured path trim/decode, and config-name-only plugin matching; remaining plugin work is marketplace/remote/share/install fixture depth.
- Skill explicit mention parsing and appserver injection now follow Rust `core-skills/src/injection.rs` / `ext/skills/src/extension.rs`: structured skill inputs are exact typed inputs, markdown skill links require `[$name](skill://...)`, direct `$name` ignores common env vars, non-skill resource paths are excluded, skill paths are no longer trim/decode/clean normalized beyond `skill://` stripping, executor display locators match internal environment skill entries, and injected skill main prompts/names/paths are byte-truncated like Rust. Rust orchestrator authority tools `skills.list` and `skills.read` are now model-visible namespace tools backed by `codex_apps` MCP resources. Remaining skill work is host authority catalog unification and progressive-disclosure fixture depth.
- Available skills rendering now follows Rust `core-skills/src/render.rs` / `ext/skills/src/render.rs` for token truncation warning wording, render-layer description whitespace, default `(file: path)` labels, executor `(environment resource: skill://<selected-root-id>/.../SKILL.md)` labels, budget-pressure alias-root selection, `### Skill roots` body/instructions, and plugin cache alias-root rules; remaining render work is telemetry side effects and broader progressive-disclosure fixtures.
- Local skill loader parsing now follows Rust `core-skills/src/loader.rs` for accepted YAML field names, absent short-description semantics, invalid `SKILL.md` skip/error behavior, overlong-name rejection, overlong description preservation, Codex product filtering, scan depth/dir limits, user/repo/admin directory symlink following with system roots ignored, and canonical skill identity paths; remote environment loading now also uses Rust scan limits/symlink following, scan/load warning text, name/path sorting, invalid-skill skip behavior, inventory/ancestor plugin namespaces, qualified-name limits, and Codex product filtering. Remaining loader work includes authority/progressive disclosure fixture depth.
- Exec JSONL drift resolved: Rust `codex-rs/exec/src/exec_events.rs` defines only `thread.started`, `turn.started`, `turn.completed`, `turn.failed`, `item.started`, `item.updated`, `item.completed`, and `error`; Go exec JSONL now suppresses streaming `item.delta` and `response.rate_limits`, emits final assistant text as `item.completed`, keeps MCP null fields, maps collab tool calls to Rust `collab_tool_call`, follows Rust todo-list started/updated/completed lifecycle, and matches Rust `exec resume --last/--all`, by-name, image attachment, human config summary, previous-response semantics, review prompt/no-diff/merge-base semantics, review structured-output rendering, review subagent metadata, and app-server review exit/interruption/error fallback lifecycle.
- TUI `/diff` default path now follows Rust WorkspaceCommand/no-index behavior, common slash command descriptions are Rust-aligned, Rust's 539 TUI `.snap` files are mapped to Go owners/focus areas, `/status` permissions/approval labels, credit amount finite-value checks, status-line rate-limit window selection, terminal title grapheme truncation, global `TruncateText` grapheme truncation, Unicode `CapitalizeFirst`, `center_truncate_path`, status/title item-id exact parsing, app-server thread target UUID parsing, agent navigation/liveness/backfill/startup/summary/side `ThreadId` canonicalization, session lifecycle no-trim metadata/error/path behavior, config persistence typed enum reads, side enum/footer/error exact matching, thread-event file-change exact lookup and field preservation, thread-goal usage/confirmation/summary formatting, external editor command splitting, backtrack/side-return key identity boundaries, mentions_v2 candidate normalization/search/display/initial-selection/filesystem-row edge cases, and MCP inventory request thread selection follow Rust snapshot/helper semantics, history-cell raw transcript line splitting now preserves explicit blank lines like Rust, notice cells preserve Rust whitespace/prefixes and safety policy wording, MCP inventory preserves Rust sorting/order boundaries, background terminal process cells follow Rust bullet/prefix/truncation rules, approval decision cells use Rust command snippet extraction/quoting/truncation, execpolicy amendment empty-prefix text, network policy amendment allow/deny text, and guardian helper symbol/whitespace/count formatting, chatwidget unified exec process display follows Rust split-command round-trip and shell display semantics, exec_cell command rendering follows Rust shell display and unified interaction special-case semantics, legacy approval modal command display uses the same Rust shell helper, `/agent` status preview uses Rust de-duplication, grapheme summary truncation, and display-width wrapping, desktop handoff history messages and URL/error rendering match Rust, composer history-search case ranges cover Rust's dotted-I boundary, request_user_input footer progress, display-width wrapping, and non-splitting footer tips follow Rust behavior, and approval overlay network deny shortcuts, queue semantics, execpolicy-prefix filtering, shell command display, per-request header field boundaries, open-thread footer hints, and fullscreen approval shortcuts follow Rust visibility semantics; remaining TUI work is behavior-level snapshot coverage for composer, approval UI, broader status surfaces, history cells, review panes, and unified exec terminal states.
