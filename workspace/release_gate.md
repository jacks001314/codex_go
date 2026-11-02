# Release gate — align/gate @ 777c003f

- Date: 2026-11-02
- Worktree: `D:\qax\reagent\dev\codex_go_lane_gate` (branch `align/gate`, clean checkout of
  `777c003f Merge branch 'align/cloud2' into align/round1308`)
- Toolchain: `go version go1.26.5 windows/amd64`; host Windows x64; `GOFLAGS`/`GOCACHE` unset
- All results below come from commands run in this worktree; raw logs kept outside the repo in
  `D:\qax\reagent\dev\_gate_logs\`.

## 1. Builds

| # | command | result |
|---|---------|--------|
| 1 | `go build ./...` | exit 0 |
| 2 | `GOOS=linux GOARCH=amd64 go build ./...` | exit 0 |
| 3 | `GOOS=darwin GOARCH=arm64 go build ./...` | exit 0 |
| 4 | `GOOS=windows GOARCH=amd64 go build ./...` | exit 0 |

No output on any of them (nothing to report). Cross-compiles are pure compile checks, no linking
targets are installed or run.

## 2. Vet

`go vet ./...` → exit 0, zero diagnostics. This repository's vet baseline on Windows is clean, so
any future vet line would be a new finding rather than inherited noise.

## 3. Full test suite

`go test ./tui/... ./app/... ./appserver/... -count=1` → exit 1.

- 30 packages `ok`, 1 package `FAIL`.
- `ok codex_go/app 103.807s`, `ok codex_go/appserver 499.804s`, `ok codex_go/tui 20.199s`.
- Failing package: `codex_go/tui/tea 73.256s` with exactly two failing tests:
  - `--- FAIL: TestModelTerminalSnapshots` (`TestModelTerminalSnapshots/main_view`, snapshot_test.go:75)
  - `--- FAIL: TestModelVT100TerminalSnapshotMainView` (vt100_snapshot_test.go:39)

Failure content (both tests, one line of diff each):

```
--- got ---
  gpt-5 high · D:\qax\reagent\dev\codex_go_lane_gate\tui\tea
--- want ---
  gpt-5 high · D:\qax\reagent\dev\codex_go\tui\tea
```

**Attribution: 环境夹具 (environment fixture).** The golden snapshots embed the absolute checkout
path of the main repo (`codex_go`), and the gate runs in a sibling worktree
(`codex_go_lane_gate`). That is the only difference; every other line of the snapshot matches.
These are precisely the two known golden worktree-path snapshots. No test failed for any other
reason, and no package outside `tui/tea` failed.

New failures: **none**.

## 4. gofmt

`gofmt -l tui app appserver` reports **2699 files — every tracked .go file in those trees**.

**Attribution: 环境夹具 (environment fixture), not formatting drift.** This checkout stores every
file with CRLF line endings (Windows `core.autocrlf`), so gofmt wants to rewrite all of them.

Proof:

- `tui/activity_group.go` contains 90 CRLF pairs and 90 LFs total (i.e. 100% CRLF).
- Copying that same file to `%TEMP%` with CRLF→LF and running `gofmt -l` on the copy returns
  nothing (clean).

Per-commit practice used here: only the files a commit touches are checked, by LF-normalising a
copy to `%TEMP%` and running `gofmt -l` on it. For every file changed on this branch the result was
clean (see the commit notes below).

## 5. Race detector (Windows)

The race detector works on this host (cgo/gcc present); no "not supported" fallback was needed.

| command | result |
|---------|--------|
| `go test -race ./tui/app ./tui/chatwidget -count=1` | `ok tui/app 2.395s`, `ok tui/chatwidget 1.901s` |
| `go test -race ./app -count=1 -run "Cloud|ModelSelection|Recap|TurnParams|TaskTools|ThreadSettings|Remote"` | `ok codex_go/app 25.070s` |

The `./app` subset covers the cloud/settings/turn-start paths changed by recent lanes.

## 6. Verdict

**门禁结论：通过（含 2 个已知环境夹具快照失败，0 个新失败）。**

| tier | count | detail |
|------|-------|--------|
| 通过 | all builds, vet, 30/31 test packages, race subsets | — |
| 已知失败（环境夹具） | 2 tests in `tui/tea` | golden snapshots embed the sibling worktree path |
| 新失败 | 0 | — |

Platform build matrix: windows/amd64, linux/amd64, darwin/arm64 all compile.

## 7. Changes landed on this branch after the gate

The gate above was measured on the clean `777c003f` checkout, before any edit. Two follow-up
commits then landed; both were re-verified with `go build ./...` plus their package tests and are
gofmt-clean (LF-normalised check):

- `dc5e5488` cloud model-selection notice (app, tui/tea)
- `ce539feb` cloud turn/start permissions assertion (app, test-only)

No files outside `app/`, `tui/tea/` and `workspace/` are touched by this branch.