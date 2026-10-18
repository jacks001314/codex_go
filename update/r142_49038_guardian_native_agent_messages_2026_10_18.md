# Rust #49038: preserve encrypted agent messages in Guardian reviews

Upstream commit: `368e5eae2f006a70a91dddfdc96e6b2d11498f81`

## Result

Equivalent implementation was already present.

- Guardian transcript entries can carry a complete native response item for
  encrypted agent messages.
- Native items are omitted from text rendering and token truncation, then
  emitted in conversation order between the surrounding text runs.
- Ciphertext is never copied into a rendered transcript text section, and the
  original native item stays intact for the reviewer request.

## Verification

```text
go test ./appserver ./state -run 'NativeAgentMessage|EncryptedAgent|GuardianTranscript' -count=1
```

Passed on 2026-10-18.
