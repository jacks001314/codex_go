# Round 116 · #43903 shared Windows deny-read glob scan planning

Rust source: `4fd2c460dd`.

## Landed

- Added `sandbox.WindowsDenyReadGlobScan` with `Root`, `PatternSuffix`, and
  `MaxDepth`, plus `sandbox.WindowsDenyReadGlobScanForPattern`, the Go analogue
  of `codex_protocol::permissions::windows_deny_read_glob_scan`.
- The planner is lexical only: it finds the deepest literal directory before
  the first `*`/`?`/`[` metacharacter, keeps a leading separator or drive root
  in the scan root, and derives the glob suffix and traversal bound without
  touching the filesystem.
- A recursive `**` component inherits `glob_scan_max_depth` (nil means
  unbounded); otherwise the depth is the suffix component count capped by the
  configured value.
- `windowssandbox.GlobScanPlanForPattern` now delegates to the shared planner,
  removing the duplicated `effectiveGlobScanMaxDepth` and component-splitting
  logic. The resolver still matches with its absolute-pattern matcher, so the
  observable Windows deny-read expansion is unchanged.

## Verification

```text
go test ./sandbox ./sandbox/windowssandbox -run 'GlobScan|DenyRead' -count=1  # ok
go vet ./sandbox ./sandbox/windowssandbox                                    # ok
go build ./...                                                               # ok
```

New table coverage mirrors the Rust cases: recursive and non-recursive globs,
drive-root and plain-literal prefixes, configured depth bounds, and patterns
without a separator.

`go test ./sandbox/...` still reports the pre-existing Windows-only build
failure in `sandbox/linuxsandbox` tests (`probeBwrapHelp` is defined only for
Linux).
