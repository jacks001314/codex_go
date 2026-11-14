# Final sync validation (2026-11-14)

## Repository state

- Fast-forwarded `main` from `a1f5e4c0` to `origin/main` at `e212e1c3`, which
  includes the four concurrent commits that were absent from the previous push.
- Working tree changes are limited to the parity snapshots, the isolated plugin
  Git environment, and this validation record.

## Fixes

- Registered `statuslineprovider` in the Go top-level package inventory.
- Recorded `catalog_apps_instructions` as an explicit ahead-of-anchor feature:
  the parity checkout is pinned before Rust #61213, while Go already carries the
  registry key for the implemented Apps-instruction gate.
- Corrected the feature-surface comparison so Rust-only and Go-only values are
  reported under the right names.
- Changed the isolated plugin Git configuration from Windows `NUL` to
  `/dev/null`, matching Rust and avoiding Git for Windows treating `NUL` as a
  relative path (`plugin/NUL`).

## Verification

- `go test ./config ./appserver ./app ./mcp ./rollout ./statuslineprovider ./telemetry ./tui/tea ./turn -count=1` passed.
- `go test ./parity ./plugin ./keyring -count=1` passed.
- `go test ./keyring -count=10` passed after one transient OS-keyring delete
  mismatch in the first whole-repository run.
- `go vet ./...` passed.
- A whole-repository run passed every package except four TUI packages that
  failed to link because the Windows paging file was temporarily exhausted.
  `go test ./tui/footerhint ./tui/history_cell ./tui/ide_context ./tui/imagedecode -count=1`
  then passed, so every Go package has a passing record on the merged tree.
