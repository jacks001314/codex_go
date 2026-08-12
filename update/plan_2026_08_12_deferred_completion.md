# Codex Go deferred-register completion batch - 2026-08-12

## Goal

Complete the deferred follow-up register items tracked across
`update/plan_2026_08_12.md`, `plan_2026_08_12_followup.md`, and
`plan_2026_08_12_next.md`, plus the newest upstream commit.

## Completed and pushed to origin/main

- `125ad1d` **#38179** (0e82c62a44) embed packaged config defaults: embedded
  `config/defaults.toml` installed as the lowest-precedence layer when no
  packaged-defaults path is supplied; packaged defaults filtered from config
  RPC layers/origins; override metadata reported only when the effective layer
  strictly outranks the user layer; strict-config schema accepts the embedded
  keys (file_opener, project_root_markers, history, ...).
- `346ef1a` TUI batch + skills:
  - **#38032** (8f4a2c99dd) approved Guardian assessments complete silently
    (no history entry; removed NewGuardianApprovedActionRequest).
  - **#38044** (be751dd1df) node_repl.js MCP calls compact in history (title +
    meaningful output) with full untruncated TranscriptLines/raw output.
  - **#38075** (33aaf91366) diff-summary content width saturated for narrow
    terminals; width-aware insertion already equivalent (live model width).
  - **#38036** (dad1db87bb) N/A: Go TUI streaming has no per-chunk slog traces.
  - **#37979** (7d486ffa94) skills/list resolves [skills.bundled] enabled per
    CWD from the effective config layer stack (regression test mirrors Rust).
  - **#38086** (f4936d7aba) N/A: Go lacks remote_sandbox_config/hostname
    requirements routing and `~` expansion (pre-existing tracked gap).
- `78d1071` analytics:
  - **#38066** (c8f673fddc) resource-backed skill invocation events: skills.read
    resolves executor/orchestrator entries, sha1 fallback skill_id, provider
    scope, per-turn resource-id dedupe, success-gated implicit reads.
  - **#38057** (44d992c14e) codex_artifact_operation events:
    RecognizeArtifactOperation for trusted openai-primary-runtime markers +
    started-event emission from the command-item attribution path. Tracked
    deltas: primary-runtime plugins not yet in TrustedPluginRoots (curated
    remote only); attribution happens at persist time, not UnifiedExecStartup.
- `2d730ce` **#38024** (edcec13372) image generation usage-limit failures:
  ImageGenerationFailure wire type (usageLimitExceeded/limitId/resetsAt),
  detection of image_gen limit responses, failure carried on the failed item.
- `8e3e752` **#38089** (4c89139da9) native CIMD MCP OAuth registration: auto
  prefers advertised CIMD with public-client token auth + native loopback
  callback; forced cimd/dcr validated; client_metadata_url + metadata-document
  client id; metadata CIMD flags captured once during discovery.
- `f415e3d` leftover session work: in-process app-server initialize handshake
  for local TUI interactive flows (compact/goal/review/status).

## Still open (dedicated batches recommended)

1. **#38108 tracked deltas** (from 2230d64464; Go verified equivalent + hook
   precedence in 0e35687):
   - per-server/per-connector `approvals_reviewer` resolution from MCP config
     layers (Go resolves the thread-level effective config only);
   - persistent MCP policy-amendment approvals ("Allow and don't ask me again"
     → `approved_mcp_policy_amendment` wire action + policy store; the enum
     value is currently unused);
   - unified approval resolution-source telemetry (single
     Hook/Guardian/User source; Go records hook runs + auto_review meta).
2. **#38067** (b43de77679) scope environment readiness config to thread
   attachments: large environment-selection change (18 Rust files).
3. **#38058** (3a6f747d77) preserve harness metadata across conversation
   history: large compact/history payload change (67 Rust files).

## Verification

- `go build ./...` clean; `go vet` clean on touched packages; `gofmt -l`
  empty (LF-normalized); `git diff --check` clean.
- `go test ./app ./appserver ./mcp ./turn ./exec ./protocol ./plugin
  ./telemetry ./tui/... ./config ./prompt ./session ./rollout -count=1`
  passes.

## Deliverable

- 6 alignment commits + 1 leftover-work commit, pushed to origin/main
  (`b6dd43a..8e3e752`).
- This plan document (`update/plan_2026_08_12_deferred_completion.md`).