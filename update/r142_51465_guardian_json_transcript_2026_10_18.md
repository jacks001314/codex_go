# Rust #51465: optional JSON transcript format for Guardian

Upstream commit: `61969074a7c49fbd31467dc2e8c5ede0edfd3962`

## Result

Equivalent implementation was already present.

- `config.Config.GuardianV2TranscriptFormat` reads
  `[features.guardianv2].transcript_mode`; unknown values preserve the default
  line format.
- `state.GuardianTranscriptEntry.Render` emits JSON records with host-owned
  `author`, `index`, optional `label` and `retained_source_order`, plus the
  message in `text`. HTML characters remain literal.
- `ApplyGuardianTranscriptInstructions` prepends the fixed provenance grammar
  ahead of configured policy instructions in JSON mode.
- Guardian's per-entry budget truncates the message body before JSON encoding,
  so truncation cannot rewrite host metadata. Native encrypted agent messages
  remain complete response items and are never flattened into text.

## Verification

```text
go test ./state ./config ./appserver -run 'GuardianTranscript|GuardianV2Transcript|NativeAgentMessage' -count=1
```

Passed on 2026-10-18.
