# Round 116 · #49894 combined world-state transitions (not applicable)

Rust source: `08a7031b6a`.

## Why the refactor has no Go carrier

Rust replaced the separate `WorldStateSection::snapshot` method with a typed
`SectionTransition` returned by `render_diff`, so a section can return a
snapshot update and/or a model-context fragment independently (skipping the
snapshot preserves comparison state).

Go's world-state implementation does not have a `WorldStateSection` trait or a
separate `snapshot()` step. Each section is a bespoke
`RuntimeRouter.<section>WorldStateInputItem` function that, in one operation:

- decodes the persisted snapshot (`session.DecodeWorldState`),
- compares it with the current configuration,
- returns `nil` when unchanged, and
- writes the new snapshot and returns the rendered fragment together when it
  changes (for example `collaborationModeWorldStateInputItem` and
  `contextWindowGuidanceWorldStateInputItem`).

"Unchanged sections return neither a snapshot nor a fragment" is therefore the
native Go behavior, and there is no separate snapshot path that could drift.
The multi-agent usage hint is likewise rendered and compared against the
persisted JSON each turn; caching its fingerprint would be a local optimization
without a behavioral contract.

## Verification

```text
go test ./appserver -run 'PermissionsWorldState|CollaborationModeWorldState|ContextWindowGuidance|MultiAgentUsageHint' -count=1  # ok
rg -n 'WorldStateSection|SectionTransition|render_diff' appserver context -g '*.go'  # no carrier
```
