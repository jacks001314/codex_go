# #54257 Preserve recent speech when trimming realtime transcripts

## Upstream change

The realtime transcript is bounded to 8 KiB while preserving the newest
speech. Trimming drops or shrinks older entries and keeps the active tail
available to the model and session history.

## Go status

Complete by equivalent implementation.

`realtime/transport.go` applies the transcript budget by keeping the tail of
the accumulated speech, and `appserver/realtime_runtime.go` re-applies the
bound when runtime state changes so an unbounded producer cannot exceed it.

## Verification

- `go test ./realtime -run 'TranscriptBudget|Transcript' -count=1`
- `go test ./appserver -run 'TestRealtimeTranscriptTailIsBoundedLikeRust' -count=1`
