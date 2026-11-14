# Transcript preview clearing before reflow (#61196)

## Behavior

- The Go transcript-image runtime compares the new placement key with the drawn image before every frame.
- When new history moves a retained Sixel or Kitty preview, `beforeWrite` clears the previous pixels first; the replacement text frame is written afterwards and the next placement is repainted.
- The composer draft is held by the composer state and is not touched by transcript reflow.

## Regression

`TestTranscriptImageClearsBeforeReflowLikeRust` moves a Sixel preview from content row 0 to row 1 and asserts the clear sequence appears before the replacement text.

## Evidence

- `go test ./tui/tea -run TestTranscriptImageClearsBeforeReflowLikeRust -count=1` passed.
- Full `go test ./tui/tea -count=1` passed in 47.601s.
