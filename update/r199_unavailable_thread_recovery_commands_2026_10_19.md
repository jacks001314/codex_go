# Round 756: recovery commands on unavailable threads (#46566)

## Upstream

- Rust commit: `ef9f3d022aff6bd087c640392dc8608743aa411d`
- Subject: `Allow recovery commands when the current thread is unavailable (#46566)`

## Go changes

- Added `threadUnavailable`, a restricted-input mode for a replay-only
  conversation whose app server is still connected. A closed agent-thread
  switch enters this mode and a live switch leaves it.
- Entering the mode clears stale review and MCP-startup activity and forces an
  idle status, so recovery commands are not blocked by old activity. Composer
  drafts, attachments, and queued messages are retained.
- Configured composer submit keys, including custom bindings, dispatch only
  single-line recovery/local commands:
  `/new`, `/clear`, `/resume`, `/agents`, `/subagents`, `/exit`/`/quit`,
  `/status`, `/debug-config`, `/pwd`, `/rollout`, `/copy`, and `/raw`.
- Ordinary prompts and other slash commands keep the draft and are not
  submitted. Slash, skill, and mention popups are suppressed in this mode so a
  completion key cannot bypass the restricted path.
- `/new` and `/clear` start a fresh writable session and clear the mode.
- A read-only thread owned by another app still takes precedence.

## Structural note

Go does not have a general connected-channel replay-only runtime outside the
closed-agent-thread carrier, and it has no postponed-turn input queue. Those
Rust internals map to the retained composer and queue state in Go.

## Verification

- `go test ./tui/tea -run 'UnavailableThread|ClosedAgentSwitch|ReadOnlyThreadOutranks' -count=1`
- `go test ./tui/tea -count=1`
