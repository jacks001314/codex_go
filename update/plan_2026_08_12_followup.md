# Codex Go update alignment plan - 2026-08-12 follow-up (next step)

## Baseline

- Rust upstream checkout: `D:\qax\reagent\dev\git\codex`
- Previous aligned Rust commit: `ca4d532b2a` (Go commit `10e89fa`, 2026-08-12,
  covered by `update/plan_2026_08_12.md`)
- Audited Rust commit: `4ef836f883` (HEAD after `git pull origin main` via
  system proxy `127.0.0.1:7897`; direct connect and `7890` proxy timed out)
- Range: 4 commits (`ca4d532b2a..4ef836f883`)
- Go baseline: `b9cc7e8` (clean worktree on `main`; `b9cc7e8` adds the #38020
  exec-server startup-retry regression test on top of the alignment commit)

## Upstream audit (4 commits)

### Implement in this batch (Go code + tests)

1. **Attach hosted app context to file uploads** (`c909d1bc04`, #38101)
   - Rust: new `HostedFileUploadContext { connector_id, action_name, model }`
     passed from the MCP tool call metadata (`item_metadata.connector_id` +
     `action_name`, model slug from `turn_context.model_info.slug`). The file
     create request body adds `codex_connector_id`/`codex_action_name`/
     `codex_model` when hosted context is present; the upload finalization
     response gains an optional `file_size_bytes` (used when present, falling
     back to the local size for older servers); finalization request body stays
     empty `{}` for older-server compatibility.
   - Go: `mcp/openai_file.go` — `LocalOpenAIFileUploader` create body and the
     finalize response struct gain the optional `file_size_bytes` (fallback to
     request size); a hosted-context struct is threaded through
     `OpenAIFileUploadRequest` → create body. Plumbing: `OpenAIFileRewriter`
     gains the hosted context (populated from the MCP tool metadata
     connector_id/action_name and the turn model slug), passed through
     `mcp/tool_executor.go` `Execute` (where `RewriteArgumentsWithOptionalFields`
     is invoked) and wired from `appserver/runtime_router.go` turn params.
2. **Route MCP tool calls through shared approval handling** (`2230d64464`,
   #38108)
   - Rust: MCP tool calls become `ApprovalAction::McpToolCall` and go through
     the session-level approval flow: permission hooks run before user or
     automatic review; reviewer selection uses the captured
     `approval_policy` + `approvals_reviewer` (guardian vs user) instead of the
     turn-level default; MCP-specific user prompts, session/persistent approval
     choices, and the captured policy/reviewer are preserved;
     `ReviewDecision::ApprovedMcpPolicyAmendment` is returned directly from the
     shared flow; resolution telemetry is recorded.
   - Go: align the `appserver` MCP tool approval path
     (`appserver/mcp_elicitation.go`, `appserver/runtime_router.go`,
     `appserver/guardian_reviewer.go`, `appserver/server_request.go`,
     `mcp/tool_runtime.go` approval templates) to: run permission hooks before
     user/automatic review for MCP tool calls, select the reviewer from the
     captured policy, preserve MCP-specific prompts and session/persistent
     choices, return `ApprovedMcpPolicyAmendment` directly, and record
     resolution telemetry. Add regression tests mirroring Rust's `hooks_mcp.rs`
     and `mcp_turn_metadata.rs` coverage.

### N/A for Go (documented)

- **Avoid cloning MCP invocations in TUI history** (`eb9dceba1a`, #38103): Rust
  internal borrow optimization when rendering TUI history cells; Go is
  garbage-collected, no protocol surface or behavior difference.
- **Distinguish rollout IDs from thread IDs** (`4ef836f883`, #38127): Rust
  reworks rollout filenames (`rollout-{ts}-{thread_id}[_{rollout_id}].jsonl`)
  to support `thread/revert`, which preserves the thread ID while creating a
  new immutable rollout, and re-keys reference indexing/compression by rollout
  ID. Go has no `thread/revert`; the session store persists one
  `<threadID>.json` per thread. Nothing to change now; tracked for when Go
  gains revert semantics.

### Deferred follow-up register continuation (from `update/plan_2026_08_12.md`)

- `4c5fc230a9` (#38020) retry transient exec-server startup failures: Go
  regression test added in `b9cc7e8`
  (`TestFetchRemoteEnvironmentStatusRetriesAfterTransientStartupFailureLikeRust`,
  passes; Go status fetches dial per call so the failed startup is not
  memoized). Retained as verification; no production code change required.
- `7d486ffa94` (#37979) per-directory bundled skill settings in `skills/list`
  (tracked).
- `3a6f747d77` (#38058) preserve harness metadata across conversation history
  (tracked).
- `b43de77679` (#38067) scope environment readiness config to thread
  attachments (tracked).
- `edcec13372` (#38024) expose image generation usage-limit failures (tracked).
- `a817d9424d`/`c8f673fddc` (#38074/#38066) skill invocation analytics
  (tracked).
- `44d992c14e` (#38057) artifact operations from trusted plugin markers
  (tracked).
- `33aaf91366`/`be751dd1df`/`dad1db87bb`/`8f4a2c99dd` TUI batch
  (#38075/#38044/#38036/#38032) (tracked).
- `f4936d7aba` (#38086) execution-host context for cloud config resolution
  (tracked).
- `4c89139da9` (#38089) CIMD full client-metadata-document flow remains partial
  (Go rejects `cimd` explicitly).

## Implementation plan

1. **#38101 hosted upload context** (small, self-contained):
   - `mcp/openai_file.go`: add hosted-context type; extend
     `OpenAIFileUploadRequest`; add `codex_connector_id`/`codex_action_name`/
     `codex_model` to the create body; parse optional `file_size_bytes` from the
     finalize response with fallback to the local size; keep the finalize body
     empty.
   - Thread the context through `OpenAIFileRewriter` →
     `RewriteArgumentsWithOptionalFields` → `buildUploadedValue` →
     `UploadOpenAIFile`; populate it in `mcp/tool_executor.go` `Execute` from
     connector metadata + turn model; wire at `appserver/runtime_router.go`
     (turn params carry the model; tool info carries connector_id/action_name).
   - Tests: hosted create-body metadata, legacy finalize response fallback,
     empty finalize body preserved, rewriter plumbing.
2. **#38108 MCP shared approval routing** (main item):
   - Inventory result: the Go architecture already routes MCP tool calls
     through the shared gate — `Router.DispatchWithHooks` runs permission hooks
     (PreToolUsePayload) before the MCP call executes, and Codex-initiated MCP
     approval requests (`codex_request_type=approval_request`) route through
     `appserverMCPElicitationHandler` with the same reviewer split as Rust
     `routes_approval_policy_to_guardian` (on_request/granular + auto_review →
     guardian, else user). `ReviewDecisionApprovedMcpPolicyAmendment` was added
     in the previous batch (wire format).
   - Delivered: regression test mirroring Rust `hooks_mcp.rs` — a permission
     hook Allow lets an MCP tool call execute and a Deny blocks it, in both
     cases without reaching any user/guardian elicitation review
     (`TestDispatchWithHooksPermissionHookResolvesMcpToolCallBeforeReviewLikeRust`).
   - Tracked deltas (documented, not structural ports): per-server/per-connector
     `approvals_reviewer` resolution from MCP config layers (Go resolves the
     thread-level effective config); persistent MCP approvals ("Allow and don't
     ask me again" → `approved_mcp_policy_amendment` wire action + policy
     store, the enum value is unused so far); unified approval resolution-source
     telemetry (Go records hook runs and `approvals_reviewer=auto_review` meta
     but not a single Hook/Guardian/User source).
3. **Verification**: `go build ./...` clean; `go vet` clean on touched
   packages; `gofmt -l` empty; `git diff --check` clean;
   `go test ./mcp ./turn -count=1` passes; `go test ./appserver -count=1`
   passes except for a pre-existing thread fork/resume/rollback cluster
   (`TestRouterResumeFromPath`, `TestRouterForkFromPath`,
   `TestRouterInjectItemsAndRollbackRepairRolloutOnlyThread`) and the
   pre-existing hang in `TestRouterThreadSectionsListUpdateFilterAndClearLikeRust`
   — all confirmed failing on the baseline without this batch.
4. **Commit and push** the aligned Go changes to `origin/main`.

## Deliverable

- Commit aligned to Rust `4ef836f883`: #38101 hosted upload context fully
  implemented with tests; #38108 verified equivalent + hooks-precedence
  regression test, with per-server reviewer resolution / persistent MCP
  approval / resolution-source telemetry tracked. Pushed to `origin/main`.
- This plan document (`update/plan_2026_08_12_followup.md`).

## Delivered

- Implemented (Go commit `0e35687`, 2026-08-12): **#38101 hosted upload
  context** fully implemented with tests. `mcp/openai_file.go`
  `LocalOpenAIFileUploader` create body gains
  `codex_connector_id`/`codex_action_name`/`codex_model` when hosted context is
  present; the finalize response parses the optional `file_size_bytes` with a
  fallback to the local request size; the finalize body stays empty for
  older-server compatibility. The hosted context is threaded through
  `OpenAIFileRewriter` → `RewriteArgumentsWithOptionalFields` →
  `buildUploadedValue` → `UploadOpenAIFile`, populated in `mcp/tool_executor.go`
  `Execute` from the MCP tool metadata (connector id + action name) and the
  turn model, and wired from `appserver/runtime_router.go` turn params.
- **#38108 MCP shared approval routing** verified equivalent: Go already routes
  MCP tool calls through the shared approval gate —
  `Router.DispatchWithHooks` runs permission hooks (`PreToolUsePayload`)
  before the MCP call executes, and Codex-initiated MCP approval requests
  (`codex_request_type=approval_request`) route through
  `appserverMCPElicitationHandler` with the same guardian-vs-user reviewer
  split as Rust `routes_approval_policy_to_guardian`.
  `ReviewDecisionApprovedMcpPolicyAmendment` was added in the previous batch
  (wire format). Added regression test
  `TestDispatchWithHooksPermissionHookResolvesMcpToolCallBeforeReviewLikeRust`
  mirroring Rust `hooks_mcp.rs` (permission-hook Allow executes the MCP tool
  call, Deny blocks it; neither reaches user/guardian review). Tracked deltas
  (documented, not structural ports): per-server/per-connector
  `approvals_reviewer` resolution from MCP config layers; persistent MCP
  policy-amendment approvals ("Allow and don't ask me again" wire action +
  policy store, enum value unused so far); unified approval resolution-source
  telemetry (hook runs + `approvals_reviewer=auto_review` meta recorded, no
  single Hook/Guardian/User source).
- Verification: `go build ./...` clean; `go vet ./mcp ./turn ./appserver`
  clean; `gofmt -l` empty (LF-normalized); `git diff --check` clean;
  `go test ./mcp ./turn -count=1` passes; `go test ./appserver -count=1`
  passes. The pre-existing appserver fork/resume/rollback cluster and the
  thread-section deadlock hang noted in the plan were repaired afterwards in
  `f01d404` (see `update/plan_2026_08_12_repair.md`); appserver now passes with
  no skips.
- Commit `0e35687` "Align Go Codex with Rust 4ef836f883: hosted app upload
  context (#38101) + MCP permission-hook approval precedence (#38108)" pushed
  to `origin/main`.
