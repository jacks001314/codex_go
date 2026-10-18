# Rust #40271: preserve annotations when rolling back model switches

Upstream commit: `bd0d4a23e3d276d6c4addcf12da40042b9c0b4df`

## Result

Fixed.

When rolling back every user turn, Go previously retained first-turn developer
messages verbatim. A mixed message could therefore keep a stale
`<model_switch>` fragment, and removing it without realigning annotations would
make the surviving content inherit the wrong positional kind.

`DropLastUserTurns` now filters model-switch fragments from retained first-turn
developer messages and rebuilds `ContentItemKinds` from the surviving content.
The reference-context baseline is retained because this is the prefix path, not
the mixed pre-turn-bundle trim path.

`TestDropLastUserTurnsFiltersModelSwitchAndPreservesAnnotationsLikeRust` keeps
persistent developer instructions around a removed model-switch fragment and
verifies the surviving kind remains `developer.instructions`.

## Verification

```text
go test ./context -count=1
go vet ./context
```

Passed on 2026-10-18.
