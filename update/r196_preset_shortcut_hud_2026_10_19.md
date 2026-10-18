# Round 753: preset keyboard shortcut HUD (#54381)

## Upstream

- Rust commit: `111362a6a0a92d08885b1ae62d3760714907ad94`
- Subject: `Add a keyboard shortcut menu for presets (#54381)`

## Existing Go coverage

The `/presets` HUD, numbered `0`-`9` recall/create slots, `n`/`s`/`e`/`d`
actions, responsive one/two-column layout, draft preservation, the
`chat.open_presets` keymap action, the `leader p` default, and the shortcut
tooltip were already implemented.

## Remaining gaps closed

- Enter is consumed by the HUD without dismissing it or submitting the draft;
  Escape closes it.
- Paste dismisses the HUD before normal composer handling, including a
  single-character paste that would otherwise look like a slot action.
- An active slash or skill popup owns input, so the preset HUD stays closed
  instead of replacing the popup.
- Menu actions arm a bounded key guard equivalent to Rust's
  `ActivationKeyGuard`: held auto-repeats of the action key are consumed
  instead of typing into the composer or re-triggering the menu. The guard
  expires after one second, matching Rust's legacy terminals that report no
  key releases.

## Verification

- `go test ./tui/tea -run 'QuickDial|PresetsLeader|ClearCommandRejectsPending' -count=1`
- `go test ./tui/tea -count=1`
- `go vet ./tui/tea`
