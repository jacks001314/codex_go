# Round 755: conversation-based `/rename` title suggestions (#40495)

## Upstream

- Rust commit: `5a51caf04d05d6afb15db2b01e477ceb2fd48882`
- Subject: `Suggest conversation-based thread titles in /rename (#40495)`

## Go changes

- `/rename` opens the editable prompt with the label
  `Generating a title suggestion…`.
- Shared title helpers in `tui/app`:
  - select the latest eight substantive user and assistant messages;
  - omit commentary and strip IDE context from user prompts;
  - escape `&`, `<`, and `>` before embedding messages;
  - bound the conversation and overall prompt to 960 bytes without splitting a
    Unicode character;
  - expose the Rust title output schema and normalized parser.
- The app-server TUI runs the existing temporary structured turn with
  `threadSource = "thread_title"`. When ChatGPT auth and the model catalog make
  `gpt-5.6-luna` available, the suggestion uses that model with `low`
  reasoning effort; otherwise it uses the current model.
- Responses are correlated by thread id and request id. A matching prompt is
  pre-filled only when the user has not typed, pasted, deleted, or otherwise
  edited the text; manual edits always win. Successful application swaps the
  loading label for `Suggested from this conversation`.
- Completed commentary messages now retain their phase in transcript state so
  title suggestions can omit them.

## Structural note

The embedded local TUI has no temporary structured app-server session hook, so
it keeps the plain editable rename prompt without a generated suggestion. The
app-server TUI path is fully wired through the existing recap/structured
request runtime.

## Verification

- `go test ./tui/app ./tui/bottom_pane ./tui/tea -count=1`
- `go test ./app -run 'Recap|ThreadTitle' -count=1`
- `go vet ./app ./tui/app ./tui/bottom_pane ./tui/tea ./tui`
