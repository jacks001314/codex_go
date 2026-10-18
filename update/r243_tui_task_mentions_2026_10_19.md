# TUI task mentions

Rust upstream: `76d98a771e6cd44a79a3ab895a9f7c49d27d6deb` -
`Add task mentions to the TUI composer (#40315)`.

## Alignment

Go already implements the task-mention end-to-end contract:

- task tools are capability-gated and remembered per thread;
- task search runs only when the active thread exposes task tools, with
  debouncing and stale-result rejection;
- task candidates participate in mentions-v2 ordering, filtering and rendering;
- task links use the multi-word and escaped-title codec;
- composer draft state retains mention bindings through editing and history;
- referenced tasks inject one bounded task-reference context block and preserve
  the visible task links;
- submitted user messages and transcript cells decode task links back into
  displayable task titles.

## Verification

```text
go test ./tui/bottom_pane ./tui/chatwidget ./tui/tea -run 'TaskMention|TaskReference|TaskLink|TaskSearch|TaskCandidates|TaskTools' -count=1
go test ./parity -count=1
```
