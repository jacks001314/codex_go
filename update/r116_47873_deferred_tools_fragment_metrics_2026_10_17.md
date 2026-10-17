# Round 116 · #47873 deferred tool namespace fragment metrics

Rust source: `4021746a15` (later family buckets from `456212ca21`).

## Landed

- `context.DeferredToolsStateFragmentWithReport` now returns the rendered block
  sizes measured before and after the 4 KiB cap: namespace counts, byte totals
  including the `<tools>` wrapper, and whether the fragment is a full snapshot
  or a delta. Descriptions are measured after normalization, matching the text
  that is rendered. Unchanged state returns no fragment and no report.
- Added `codex.thread.tools.namespaces_total` and
  `codex.thread.tools.fragment_bytes` metric names plus the shared logarithmic
  boundaries through 2^15 (`telemetry.ThreadToolsMetricBoundaries`, Rust
  `THREAD_TOOLS_METRIC_BUCKETS`).
- `RuntimeRouter.recordDeferredToolsFragmentMetrics` emits four histogram
  samples per rendered fragment (namespaces/bytes × before/after), tagged by
  `stage` (`before`/`after`) and `kind` (`snapshot`/`delta`), through the same
  session metric sink the skill-catalog metrics use. Delta counts include only
  changed entries; unchanged state emits nothing.

The ledger also named `CONTEXT_FRAGMENT_BYTES_BUCKETS`. That constant was
introduced by this commit and superseded by Rust #48819's family-specific
buckets; the Go port carries the current `ThreadToolsMetricBoundaries` family.

## Verification

```text
go test ./context -run 'TestDeferredTools' -count=1  # ok
go test ./appserver -run 'TestDeferredToolsFragmentMetricsLikeRust' -count=1  # ok
go test ./telemetry ./context -count=1  # ok
go vet ./telemetry ./context ./appserver  # ok
go build ./...  # ok
```

Coverage: explicit before/after byte formulas, snapshot/delta classification,
no report for unchanged state, four histogram samples with stage/kind tags and
the shared boundaries, and the boundary-family zero/overflow test.
