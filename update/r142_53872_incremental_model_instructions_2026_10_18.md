# Rust #53872: refresh model instructions in incremental-tool context windows

Upstream commit: `cb03f28a4034b6ee901885808d27182ac586830a`

## Result

Equivalent implementation was already present.

- Incremental-tool windows record model instructions alongside their catalog.
- Changed instruction text appends an update; identical text replays unchanged.
- Model switches and explicit empty instructions follow Rust semantics.
- Compaction/window replacement installs the latest model instructions in the
  rebuilt declaration prefix.

## Verification

```text
go test ./appserver -run 'IncrementalToolCatalogRefreshesModelInstructions|CompactionInstallsLatestIncrementalInstructions' -count=1
```

Passed on 2026-10-18.
