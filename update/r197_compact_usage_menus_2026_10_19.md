# Round 754: compact usage menus (#46862)

## Upstream

- Rust commit: `5e5cadad6b90f90a999cbc49745a91c61bc0cdbf`
- Subject: `Refresh analytics on identity changes and compact usage menus (#46862)`

## Go changes

- `/usage` uses Rust's shortened copy:
  - subtitle `Account usage and resets.`
  - `Show usage` / `Usage history.`
  - `Redeem reset` with `N available.`, `Check availability.`, or
    `None available.`
- Usage menus use the shared picker row columns and
  `HideWhenNarrowDescriptionLayout`, so descriptions collapse on narrow
  terminals instead of crowding the action names.
- Footer hints come from the active list keymap and use action-specific labels:
  `open` for the usage menu, `confirm` for the reset confirmation, and `back`
  for cancel. The usage modal now forwards its footer hint and picker layout.

## Structural note

Go has `chatgptapi` analytics report types but no TUI analytics dashboard,
chat/task report loader, or cached report state. Rust's
`identity_invalidated` polling and report refresh therefore have no Go
carrier; there is no stale analytics report to discard or reload.

## Verification

- `go test ./tui/tea -run 'Usage|RateLimit' -count=1`
- `go test ./tui/chatwidget -run 'Usage|RateLimit' -count=1`
- `go test ./tui/tea ./tui/chatwidget -count=1`
- `go vet ./tui/tea ./tui/chatwidget`
