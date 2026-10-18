# Generated Responses input item IDs

Rust upstream: `868c9edb0da913a5fc699a71664e65f44f6058b0` -
`Assign stable IDs to generated Responses input items (#41349)`.

## Alignment

Rust assigns type-prefixed IDs to generated Guardian classifier inputs before
sampling, so WebSocket retries reuse the same item identities. It also assigns
a prefixed ID to the generated memory phase-one user message.

Go's memory phase-one extractor builds its user message with a `msg_`-prefixed
ID and enables `ItemIDsEnabled`. The input slice is created once before the
agent call, so the runner's internal HTTP and WebSocket retries replay the same
IDs instead of generating replacements.

Go has no guardian-v2 async scorer or classifier request builder, so that half
of the upstream change has no carrier. Its absence does not create an
unidentified generated classifier request in this architecture.

## Verification

```text
go test ./appserver -run 'MemoryStageOneUsesDetachedResponsesRequestLikeRust|Memory.*StageOne|MemoryStartup' -count=1
go test ./parity -count=1
```
