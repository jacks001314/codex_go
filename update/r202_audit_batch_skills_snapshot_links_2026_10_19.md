# Round 759: audit batch for skills, shell snapshots, and terminal links

## Implemented

- `#40413`: `skills.list` uses the invocation response-byte budget, skips
  entries that cannot fit, keeps pagination across retained entries, emits the
  oversized-metadata warning once per budget, and writes the budget into new
  cursors while accepting legacy cursors.
- `#40447`: shell snapshot failures are cached with attempts/retry time,
  retried after one second up to three attempts, and single-flighted across
  concurrent requests. The final failure stays cached.
- `#40471`: Markdown web links render label-only with OSC 8 in recognized
  hyperlink-capable terminals; unknown terminals, multiplexers, Apple Terminal,
  Warp, non-terminal stdout, and empty labels keep the destination visible.

## Re-audited complete

- `#40068` MCP runtime connection status (all seven states, compact `/mcp`).
- `#40200` Plan-mode composer nudge absent.
- `#40221` Guardian review source distinguished from subagents.
- `#40301` Business Pro Lite displayed as Business Premium.
- `#40368` MCP projection uses immutable turn snapshot source.
- `#40381` Go preserves rollout/sandbox initialization errors without an
  InvalidInput-to-corruption branch.
- `#40422` Windows updater resolves only absolute PATH entries and runs in a
  temporary directory.
- `#40423` Windows file URI conversion recognizes encoded drive colons and
  rejects encoded separators.
- `#40436` in-app local automation feature gate registered.
- `#40413` and `#40471` implementations above.

## Blocked

- `#40376`: shellSnapshotV2 capability remains blocked on the missing
  executor-managed in-memory snapshot subsystem (`#39957`); advertising true
  would be incorrect.

## Verification

- `go test ./turn -run 'SkillsList|SkillsTools|ExecutorSkills' -count=1`
- `go test ./execserver -run ShellSnapshot -count=1`
- `go test ./tui/markdown ./mcp ./tui/history_cell ./install ./utils -count=1`
- `go vet ./turn ./execserver ./tui/markdown ./mcp ./tui/history_cell ./install ./utils`
- `go test ./parity -count=1`

Full `./turn` and `./execserver` runs currently hit unrelated parallel-worktree
failures in request_permissions decoding and Windows PowerShell fallback; the
affected focused suites above pass.
