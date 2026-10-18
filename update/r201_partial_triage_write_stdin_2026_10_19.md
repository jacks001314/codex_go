# Round 758: partial triage, Guardian write-stdin UI, and Linux PRIMARY tests

## Partial re-audit

Closed as complete after inspecting current code and tests:

- `#47647` Browser connector: live invocation registry and strict fail-closed
  review are implemented by `a2f22896`.
- `#41928` executor path permissions: `99ef6839` applies session/turn grants to
  shell and apply_patch, through which extension tools execute.
- `#50504` centered dialogs: every `ModalKind` routes through the retained
  backdrop overlay; only the field-level session picker is full-screen.
- `#51840` bubblewrap integrity: command `PATH`/`ENV`, bundled/system bwrap,
  glob scanner, and `TMPDIR`/`HOME` resolution are implemented.
- `#44243` originating model: step settings refresh before each request, step
  model drives tool truncation and step input, and image preparation runs over
  the complete request at sampling time.
- `#41879` / `#42065` Guardian history: bounded review history, compaction
  capture, checkpoint persistence/restore, rollback, fork, and subagent
  isolation are implemented.
- `#47695` Windows sandbox credentials: completed-setup logon probing,
  credential mismatch classification, ownership validation, and repair are
  implemented.
- `#49112` / `#53753` PRIMARY selection: added real Linux helper integration
  tests for availability, fallback, stdin delivery, ownership clear, and
  unavailable-helper errors.

Moved to `blocked_precondition`:

- `#45779`: macOS `proc_pidinfo` flavor 17 is unavailable through the current
  non-cgo `x/sys` surface; the kinfo_proc identity fallback remains.
- `#53754`: Bubble Tea v1.3.10 (latest published v1) has no Super modifier and
  drops unknown CSI-u events; live Command/Super chords require an upstream
  fork or parser replacement.

## Implementation

- Guardian `write_stdin` review actions now carry process id, stdin, and CWD
  into the TUI, render the Rust action summary with quoted/truncated input, and
  produce Rust-compatible denied/timed-out history messages.
- The auto-review denial table header is `Action`.
- Added a complete centered-overlay kind-matrix regression and a Windows
  sandbox specialized-prompt backdrop test.
- Added Linux build-tagged PRIMARY helper tests using real temporary scripts.

## Verification

- `go test ./tui/tea -run 'CenteredOverlay|WindowsSandboxDialogRetains' -count=1`
- `go test ./tui/chatwidget -run 'Guardian|PermissionPopup' -count=1`
- `go test ./app -run 'GuardianWriteStdin' -count=1`
- `go test ./sandbox/integrity -count=1`; `GOOS=linux go vet ./sandbox/integrity`
- `go test ./tui -count=1`; `GOOS=linux go vet ./tui`
- Focused re-audit suites for Browser, permissions, Guardian history,
  originating model, and Windows sandbox credentials
- `go test ./parity -count=1`
