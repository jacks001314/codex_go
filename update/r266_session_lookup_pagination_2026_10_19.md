# Stable session lookup pagination and picker failures

Rust upstream: `80e0b51c9e44853471fae105032fa000c77d3e4a` -
`Make session lookup pagination stable and report listing failures (#51230)`.

## Verdict

Complete.

- Named-session lookup now uses `CreatedAt` ordering with the
  `timestamp|threadID` continuation cursor and `useStateDbOnly=true`, so
  session activity cannot move an unread match across the cursor.
- A timestamp-and-ID cursor is treated as proof of complete pagination.
  Timestamp-only or malformed legacy cursors still produce Rust's
  `Cannot verify a unique session label across server pages` ambiguity error.
- Server listing failures propagate instead of appearing as an exhausted
  listing.
- Local label resolution inspects every filtered session rather than stopping
  at the picker's display limit, so duplicate labels beyond the first page are
  rejected.
- Remote TUI resume/fork discovery follows every continuation page. If a later
  page fails, already loaded sessions remain selectable and the picker shows
  `Could not load more sessions`.

## Verification

```text
go test ./app -run 'InteractiveRemoteSessionPicker|SessionResumeChecksLabelsBeyondPickerPage|LookupRemoteSessionByName|VerifiedNamedSessionLookupCursor' -count=1
go test ./tui/tea -run 'SessionPickerShowsLaterPageFailureNotice|ResumeCommandOpensSessionPicker' -count=1
go test ./session -count=1
go test ./tui/tea -count=1
go vet ./app ./session ./tui/tea
go build ./...
```
