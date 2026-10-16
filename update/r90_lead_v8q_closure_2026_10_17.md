# round 90 - closure of the three open questions raised in lane V's V8 evidence (2026-10-17, lead)

Source: `update/r90_verify_v8_2026_10_17.md` (lane V, section 7). Verified against main
`60bc5512`; all three are already covered by lane pin's P-b7 commit `18fbba74`, so no new
batch was dispatched. Evidence below is reproducible with the listed commands.

## Q1 (V section 4) - "Go's experimentalApi gate does not cover project/*" -> ALREADY FIXED

`18fbba74` extended the runtime half of the gate: `appserver/runtime_router.go:2712`
(`func experimentalAPIMethod`) now lists `MethodProjectCreate, MethodProjectDelete,
MethodProjectImport, MethodProjectList, MethodProjectMove, MethodProjectRead,
MethodProjectUpdate` at `:2736-2742`, together with 13 further Rust-marked methods
(`MethodPluginSearch`, `MethodThreadItemsRead`, `MethodThreadPredictionRequest`,
`MethodThreadQueue*`, `MethodThreadReadStateUpdate`, `MethodThreadSearchOccurrences`,
`MethodThreadTimelineList`, `MethodTurnSettingsUpdate`).

The missing piece V asked for - a guard that keeps the gate in step with Rust - also
landed: `TestRustExperimentalClientSurfaceIsGatedLikeRust` (`appserver/schema_test.go:932`,
failing assertion at `:946` `"Rust experimental client requests missing from
experimentalAPIMethod"`) derives the set from the Rust `#[experimental("...")]` attribute
and compares both directions, so dropping a project method from the list turns it red.

Reproduce: `git -C <repo> show 18fbba74 -- appserver/runtime_router.go`,
`git grep -n 'MethodProject' -- appserver/runtime_router.go`.

## Q2 (V section 5) - "the two log-only protocol-surface guards" -> ALREADY HARDENED

Same commit made both fail closed:

- method surface: `appserver/rust_protocol_surface_test.go:201` now uses an empty
  `knownMissing := map[string]bool{}` and `:205` fails for any Rust method without a Go
  constant, while `:217` fails the other way (`"Rust method %q is implemented in Go;
  remove it from knownMissing"`);
- notification surface: the same file aborts when its parser extracts nothing
  (`"parsed 0 NotificationMethod constants from %s; this guard would silently pass"`,
  `"parsed 0 server notifications from %s; the Rust macro shape changed and this guard
  would silently pass"`), and `appserver/schema_test.go:906` fails closed the same way
  for the experimental client set.

Reproduce: `git -C <repo> show 18fbba74 -- appserver/rust_protocol_surface_test.go`.

## Q3 - "correct the schema_test.go regex line 831 -> 834" -> MOOT

The regex V cited was rewritten by `18fbba74` in the same file; the current anchors are
`appserver/schema_test.go:298` (the coverage assertion) and `:906` (the fail-closed
parsed-0 guard). A line-number correction against the pre-P-b7 revision would be
misleading, so none is applied.

## Verdict

No dispatch: Q1/Q2 are implemented and test-pinned, Q3 is obsolete. Lane V's V8 section 7
open items are therefore closed; the only remaining protocol-surface follow-up is the
ledger entry for the pin-face items reassigned to A (plugin/**#52220, config/**#52302).