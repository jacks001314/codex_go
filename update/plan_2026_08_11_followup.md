# Codex Go update alignment plan - 2026-08-11 (follow-up batch completion)

## Baseline

- Rust upstream checkout: `D:\qax\reagent\dev\git\codex` at `0ca439900e`
  (origin/main HEAD; no newer upstream commits).
- Go baseline: `aa9b13a` (2026-08-11), two alignment batches already delivered
  (`73606e6` for `41ece455b7`, `6d67641` for `0ca439900e`).
- This batch completes the 14-item follow-up register tracked in
  `update/plan_2026_08_11.md`.

## Item-by-item status

### Implemented in this batch (new code + tests)

1. **MCP standard form input in full-access user threads** (`4b0e2a0bff`) —
   commit `61d8a7e`:
   - `InitializeCapabilities.MCPServerStandardFormInput`
     (`mcpServerStandardFormInput`) recognized per connection; client-only, not
     advertised to MCP servers (`mcpClientCapabilities` unchanged).
   - `appserverMCPElicitationHandler` surfaces non-approval forms in full-access
     threads after session startup (`EnableFullAccessFormInput` set at MCP
     service configuration, which runs per turn start after session startup);
     empty forms still auto-accept, tool-suggestion and approval-kind
     elicitations stay on the decline path.
   - Tests: `mcp_elicitation_form_input_test.go` (surfaced/declined/safeguard/
     empty-form) + capability wire-shape test.
2. **Cloud config bundle refresh for later sessions** (`070a26a1f0`) — commit
   `61d8a7e`: `CloudConfigLoader.Get()` now retrieves the latest bundle on each
   load (no permanent `sync.Once` snapshot) and preserves the last successful
   bundle when a refresh fails; tests cover latest-retrieval, failure
   preservation, and never-succeeded error.
3. **Windows sandbox managed networking level-only** (`a603d7ca5c`, completion)
   — commits `9018434` + `61d8a7e`: the elevated backend is selected solely from
   `WindowsSandboxLevel`; the `profile.HasDenyReadEntries()` fallback was
   removed in `tool/runner.go` and `execserver/sandbox_process_windows.go` so
   managed networking with a restricted-token sandbox is always rejected before
   spawn; tests updated to the level-only expectation.
4. **Thread section appearance** (`1549756b78`) — commit `61d8a7e`: protocol
   `ThreadSectionAppearance` (icon/color, 64-byte limit), double-option update
   semantics (`AppearanceSet`), session store `CreateSection`/`UpdateSection`/
   `DeleteSection` with persistence, SQLite migration
   `0047_thread_section_appearance.sql` (mirrors Rust `0048`), state thread-list
   query exposes `thread_sections.appearance`.
5. **Goal token budget limits** (`a9dee37f9c`) — commit `61d8a7e`:
   `goals.max_goal_token_budget` config (`config/goals.go`), default budget for
   new goals and null-reset, and oversized-budget rejection across
   `thread/goal/set` persistence paths (`buildGoalFromSetParams`,
   `thread_goal_state.go`).

### Implemented in parallel batch commits (already on origin/main)

- `093f4b8` — packaged defaults config layer (`34ecac1f2b`) + imagegen native
  transparency docs (`8cabf5a6cf`).
- `d9eccf7` — bundled package discovery + manifest version (`cc2f262033`) and
  running unified-exec process metric at turn completion (`d109393270`).

### Already equivalent in the Go architecture (no code change needed)

- **Turn-start thread persistence** (`722784e936`): Go thread-store persistence
  is synchronous on all paths; the Rust change is a background-enqueueable
  persist optimization with no Go behavioral gap.
- **Skillprovider batch** (`1c042dd4d8`/`09f47c8785`/`3c60d4da64`/
  `680934adc4`): Go `skills/list` resolves plugin roots per CWD with
  `ApplicableCWDs`/dedupe (multi-workspace consistency), and `skills/read`
  supports authority+package+resource with cursor pagination (package-based
  reads). The remaining Rust commits are internal host-service refactors with
  no Go-facing behavior change.
- **Preserve environments when reloading V2 agents** (`4996cf05af`): Go
  persists `runtime_environments` in the thread record and restores them via
  `inheritTurnEnvironmentSelections` on follow-up turns (covered by
  `TestRuntimeAgentControllerChildInheritsTurnEnvironmentSelectionsLikeRust`).

### Rust-specific / N/A for Go (documented)

- **Intercepted exec approvals through shared review** (`d06dc73290`): Unix
  zsh-fork `execve` interception; Go does not intercept `execve`.
- **gRPC code-mode notifications fire-and-forget** (`9be95745fb`): applies only
  to the Rust native gRPC delegate; the Go code-mode host runs stdio
  (`RunStdioHost`) where Rust also waits for `notification/delivered`, and Go's
  gRPC transport adapter is not yet wired into the host (tracked with the
  code-mode lifecycle batch).

## Verification

- `go build ./...` clean; `go vet` clean on touched packages; `gofmt -l`
  empty; `git diff --check` clean (only normal Windows LF/CRLF notices).
- `go test` passes for `install`, `telemetry`, `config`, `mcp`, `session`,
  `state`, `tool`, `execserver`; appserver targeted suites pass
  (elicitation/form-input/goal/skills/thread-sections/environment-inheritance).
- App-server full suite baseline: only the three pre-existing rollout
  item-shape failures in the plain `Router` remain (unrelated to this batch).

## Delivered commits

- `9018434` — Windows sandbox level-only backend selection (managed networking)
- `093f4b8` — packaged defaults config layer + imagegen native transparency docs
- `d9eccf7` — bundled package manifest + running unified-exec processes metric
- `61d8a7e` — MCP standard form input, cloud config bundle refresh, Windows
  sandbox level-only completion, thread section appearance, goal token budget

All pushed to origin/main.

## Remaining follow-ups

- gRPC code-mode transport adoption (fire-and-forget notification delivery when
  the Go gRPC host mode is wired).
- No other open items from the 14-item register.
