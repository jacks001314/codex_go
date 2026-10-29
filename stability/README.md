# stability

Deterministic stability scenarios for `codex_go`. The suite is intentionally
separate from `go test ./...`: every scenario file carries `//go:build stability`
and long runs are driven by `scripts/stability.ps1` or `scripts/stability.sh`.

## Layout

- `harness/mockmodel` - scripted Responses API server with per-chunk gates,
  delays, aborted streams, fallbacks, and captured requests. Request capture is
  a bounded ring (256 bodies), and fixtures above 4 MiB stream their frames
  instead of pre-building them, so a large-payload scenario measures the client
  and not the fake server.
- `harness/netproxy` - fault proxy: delay, jitter, throttle, drop-after,
  split writes, seeded behaviour.
- `harness/metrics` - heap, goroutine, RSS, and file-descriptor samples.
- `harness/disk` - directory size, WAL/SHM, temp files, free space.
- `harness/report` - budgets, explicit metrics, soak validation, JSON reports.
- `harness/rolloutgen` - deterministic rollout fixtures of a chosen byte size.
- `scenarios` - the actual stability scenarios (build tag `stability`).
- `cmd/stability-trend` - fits a line through recent report metrics and fails
  on a growing series or on a failed run.
- `budgets.json` - the only source of thresholds.

Scenarios that need private helpers live next to them and carry the same build
tag: `memories/rollout_stability_test.go`, `state/disk_stability_test.go`,
`state/disk_full_fault_test.go`, `turn/stability_tool_fault_test.go`,
`tui/tea/stability_ui_test.go`.

## Running

```powershell
# Fast harness + trend tests (these are part of go test ./...)
go test ./stability/harness/... ./stability/cmd/stability-trend

# Stability smoke
.\scripts\stability.ps1 -Suite smoke

# Everything, short soak, per-test summary, then a trend check
$env:CODEX_STABILITY_LARGE_BYTES = "67108864"
.\scripts\stability.ps1 -Suite all -Duration 5m -Json -Trend

# Split across machines: shards are disjoint and cover every test once
.\scripts\stability.ps1 -Suite all -Shard 1/3 -Json

# One package directly, while developing
$env:CODEX_STABILITY_DURATION = "3m"
$env:CODEX_STABILITY_WARMUP = "30s"
$env:CODEX_STABILITY_SETTLE = "5s"
go test -tags stability ./stability/scenarios -run '^TestSoakTurns$' -count=1 -v

# Trend over the reports collected so far
go run ./stability/cmd/stability-trend -dir stability/reports -last 7
```

```sh
./scripts/stability.sh smoke
CODEX_STABILITY_DURATION=3m ./scripts/stability.sh soak
./scripts/stability.sh all 5m --json --trend
./scripts/stability.sh all 2h --shard 1/2 --json
```

Suites: `smoke`, `soak`, `large`, `faults`, `ui`, `memory`, `disk`, `all`.

| Suite | Scenarios |
| --- | --- |
| `smoke` | `TestSmoke` |
| `soak` | `TestSoakTurns`, `TestSoakIdle`, `TestSoakReconnect` |
| `large` | `TestLargeRolloutRead`, `TestLargeThreadItems`, `TestLargeToolOutput` |
| `faults` | `TestFault*` in `stability/scenarios`, `turn` (tool error/panic/cancel/soak) and `state` (SQLITE_FULL) |
| `ui` | `TestStabilityUI*` in `tui/tea` |
| `memory` | `TestMemoryLongSession`, `TestMemoryGCSteady` |
| `disk` | `TestDiskGrowth1KTurns`, `TestDiskWALCheckpoint`, `TestDiskTempCleanup` |

`large` skips unless `CODEX_STABILITY_LARGE_BYTES >= 67108864`; `soak` skips
unless a duration is set (the scripts always set it).

## Reading a run

- Reports: `stability/reports/<started>-<name>.json` (gitignored).
- `passed` is true only when every non-skipped check passed.
- `metrics` holds the numeric series (heap peak, p99 frame time, bytes per 1k
  turns, ...) that `stability-trend` fits.
- `-Json` prints `pass/fail/skip` counts plus one `FAIL <pkg>/<Test>` line per
  failure and writes the raw events to `.tmp-stability-*.json.log`.

## Conventions

- Run the suite one package at a time (`-p 1`, already set by both
  scripts): scenarios assert process-level heap, handle and throughput numbers,
  and parallel packages turn those into false reds.
- Prefer gates, fake clocks, and injected faults over `time.Sleep`.
- Measure after a warmup and a settle window; in-flight connections are not
  leaks.
- Never auto-retry a stability failure. Record it in
  `update/flake_ledger.jsonl` instead.
- Gate on the resource the scenario really exercises. A Windows process handle
  count includes threads and pooled connections, so connection leaks are judged
  by `mockmodel.Server.ConnStats` (opened vs closed) and the handle delta is
  kept as a trend metric only.
- Soak checks: `soak.goroutines`, `soak.open_fds` (steady-state
  tail only — the second half of the window, so warmup bookkeeping cannot fail
  a run), `soak.conn_accounting` (opened/closed/active identity) and
  `soak.connections_reclaimed` (retained connections must stay under
  `soak.max_active_connections`). `soak.open_fds_delta` and
  `env.fd_delta_series` are trend data, not gates.
- A skipped check is not a pass.
- Budgets live in `budgets.json`; scenarios must not hard-code thresholds, and
  a gate is never made green by widening its budget.
- UI latency budgets are reference-machine numbers; compare relative changes
  when running on a busy host (see risk R3 in the plan).
