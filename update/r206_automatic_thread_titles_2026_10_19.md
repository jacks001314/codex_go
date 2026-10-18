# Round 760: automatic descriptive thread titles (#40492)

## Changes

- The first completed `user_message` on an unnamed thread immediately sets a
  provisional title (normalized whitespace, no slash commands, 36-rune bound)
  and persists it through the thread rename hook.
- One bounded structured title request starts in the background. It reuses the
  /rename title runtime with the current model/provider/cwd/permission profile
  and the low-effort `gpt-5.6-luna` preference when available.
- A generated title is applied only when:
  - the same thread is still active;
  - the provisional name is still current;
  - the provisional persistence succeeded.
- Manual renames clear the pending automatic state, so a later generated title
  cannot overwrite them.
- Automatic updates mutate thread state silently; no rename confirmation cell is
  added to the transcript.
- Shared provisional-title logic now lives in `tui/app`, used by both the TUI and
  the legacy app helper.

## Verification

- `go test ./tui/tea -run 'AutomaticThreadTitle|ProvisionalThreadTitle' -count=1`
- `go test ./tui/tea ./tui/app ./app -run 'ThreadTitle|Automatic|AutoThreadTitle' -count=1`
- `go test ./tui/tea -count=1`
- `go vet ./tui/tea ./tui/app ./app`
- `go test ./parity -count=1`
