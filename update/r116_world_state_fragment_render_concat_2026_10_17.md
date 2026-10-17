# Round 116 · fragment rendering concatenates markers without separators

Rust contract: `codex-rs/context-fragments/src/fragment.rs`
`ContextualUserFragment::render` renders `start_marker + InputText body +
end_marker`, with no inserted separators; bodies own their whitespace.

## Problem

Go's `context.Render` and `RenderItems` trimmed the body's newlines and joined
markers with `"\n"`. This produced different model-visible text for fragments
whose bodies do not carry leading/trailing newlines, and for empty bodies:

- collaboration mode with empty instructions rendered
  `<collaboration_mode>\n</collaboration_mode>` instead of
  `<collaboration_mode></collaboration_mode>`;
- context-window guidance rendered `<tag>\noriginal guidance\n</tag>` instead of
  `<tag>original guidance</tag>`;
- current-time reminder/unavailable fragments gained newlines Rust does not
  emit.

It also caused five app-server world-state tests to be treated as platform
baseline failures.

## Fix

- `Render`/`RenderItems` now concatenate markers and the body verbatim, matching
  `RenderStandalone` and Rust. Fragments that need surrounding whitespace keep
  it in `Body()` (tools, skills, permissions do).
- Updated stale Go expectations that had encoded the separator behavior:
  current-time reminder/unavailable rendering, guardian tool descriptions, and
  the typed-content render test.
- The reminder parser helper now strips either marker form so historical
  records remain readable.

## Verification

```text
go test ./context -count=1  # ok
go test ./appserver -run 'TestCollaborationModeWorldState|TestRuntimeRouterContextWindowGuidance' -count=1  # ok (previously failing)
go test ./appserver -run 'CurrentTimeReminder|CurrentTimeRead|NonfatalClock' -count=1  # ok
go test ./appserver -count=1
# only TestAppInstalledForceRefreshReplacesThreadToolsLikeRust and the two
# project-trust baseline failures remain
go vet ./context ./appserver  # ok
go build ./...  # ok
```
