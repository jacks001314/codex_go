# Round 752: built-in permission selections through the app server (#46008)

## Upstream

- Rust commit: `2b59d92dbdd69fc600ed95250867d305942912e1`
- Subject: `Route built-in permission selections through the app server (#46008)`

## Go changes

- `tui/tea` now routes every connected-thread permission selection with a
  profile id through `OnUpdateThreadPermissions`, including the built-in
  presets `:read-only`, `:workspace`, and `:danger-full-access`. The callback
  carries the approval policy and approvals reviewer alongside the profile id.
- `app/remote_tui_permissions.go` sends `permissions`, `approvalPolicy`, and
  `approvalsReviewer` in `thread/settings/update`, so the server's confirmed
  settings remain authoritative.
- Built-in selections are allowed while a turn is running; named server
  profiles keep the existing wait-for-idle guard.
- A built-in selection that already matches the active profile, approval
  policy, and reviewer is adopted locally without waiting for a settings
  notification that may never arrive (Rust's `already_selected` shortcut).
- `ThreadSettingsUpdatedMsg` now also applies the confirmed
  `approvalsReviewer`, alongside the active permission profile and approval
  policy.
- Pending permission updates block `/new`, `/clear`, resume/fork task
  switches, and command-center task creation/selection with the Rust message
  `Wait for permissions to update before switching tasks.`
- An unsupported connected server reports
  `Permission selection requires a newer app server.`

## Structural notes

- The embedded local TUI has no app-server thread session, so its local
  presets remain authoritative and keep the existing local apply path.
- The Go permission surface has no cached permission-specific network proxy,
  so a stale proxy cannot survive a confirmed selection; the server's settings
  are used directly.
- Deferred permission selections carried through Windows sandbox setup are
  absent because Go's setup prompts do not carry a profile selection; that
  flow remains tracked with `#45821`.

## Verification

- `go test ./tui/tea -count=1`
- `go test ./app -run 'RemoteTUIPermission|UpdateThreadPermissions' -count=1`
- `go vet ./tui/tea ./app`
- `go test ./parity -count=1` with
  `CODEX_RUST_ROOT=D:\qax\reagent\dev\git\codex\codex-rs`
