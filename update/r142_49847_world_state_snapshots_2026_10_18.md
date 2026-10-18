# Rust #49847: persist world-state snapshots alongside rendered context

Upstream commit: `b44ca872b1059c65f0eb6a7534e9e8ae55df54a8`

## Result

Equivalent implementation was already present through Go's section-specific
world-state writers.

- Collaboration mode, environment, permissions, context-window guidance, and
  incremental tool catalog renderers produce their context update and persisted
  snapshot from the same live state.
- The incremental catalog stores top-level-tool and base-instruction snapshots
  in the same `Record.Metadata.WorldState` write as its generated items.
- Resume and history-diff paths use stored snapshots, retain skipped stored
  sections, and preserve explicit empty-but-known section states.

## Verification

```text
go test ./appserver ./session -run 'WorldState|IncrementalCatalog|ContextWindowGuidance|Environment.*World' -count=1
```

Passed on 2026-10-18.
