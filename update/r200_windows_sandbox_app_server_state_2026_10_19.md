# Round 757: app-server state for Windows sandbox decisions (#45821)

## Upstream

- Rust commit: `73db60e71f7b29de2cdcbf66cb32dd5e83e155c2`
- Subject: `Use app-server state for TUI Windows sandbox decisions (#45821)`

## Go changes

- The TUI owns a Windows sandbox state carrying the app-server level and
  permission requirements.
- The remote startup prompt reads `config/read` and `configRequirements/read`
  from a local app server; the embedded TUI reads the same values through its
  config service. It no longer derives a remote decision from local files.
- Shared prompt derivation uses the server's requirements-source presence to
  decide whether elevated setup is required and computes both allowed setup
  modes.
- Setup prompt and fallback prompt hide the elevated item when requirements
  disallow elevated setup.
- Before every setup request the model re-reads the app-server state, rejects a
  disallowed mode, and only then starts setup. After setup completes it
  refreshes again.
- A thread settings update that changes the working directory schedules a
  directory-scoped refresh; adopted state also drives the non-admin degraded
  permission-menu copy.
- Read failures keep the last known state and report
  `Could not read Windows sandbox configuration and requirements from the app server.`

## Structural note

Go's TUI has no `EnvironmentManager` thread-host classifier and no
initial-user-message deferral pipeline. Mixed/unknown executor host
classification and deferred initial prompts therefore have no carrier: remote
workspaces never prompt, local daemon decisions use app-server state, and a
draft remains in the composer if a refresh fails.

## Verification

- `go test ./app -run 'WindowsSandbox' -count=1`
- `go test ./tui/tea -run 'WindowsSandbox' -count=1`
- `go test ./tui/chatwidget -count=1`
- `go test ./tui/tea -count=1`
- `go vet ./app ./tui/tea ./tui/chatwidget`
