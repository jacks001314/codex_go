# Rust #52742: opt-in OpenAI output token replay

Upstream commit: `5ef96ab2785f3ecddf279639eff7b1b42c906fa4`

## Result

Implemented the disabled-by-default `output_token_replay` request path.

- OpenAI requests add `output.encrypted_content` alongside
  `reasoning.encrypted_content` only when the feature is enabled. Other
  providers keep the reasoning include list unchanged.
- Sampled message and call items retain top-level `encrypted_content` and
  `status`. Message `output_text` blocks also retain `annotations` and
  `logprobs`, including explicitly empty arrays.
- Stored ciphertext remains in session/rollout history. Non-OpenAI request
  copies clear top-level output ciphertext except for reasoning and compaction
  items, matching Rust's provider filter without weakening replay back to
  OpenAI.
- The app-server turn runner resolves the feature from per-turn config and
  passes it into the Responses client.

## Verification

```text
go test ./model -count=1
go test -race ./model -run 'OutputTokenReplay|ResponsesIncludeOutputTokenReplay' -count=1
go test ./appserver -run 'ResponsesAgent|ContentItemKinds|Provider' -count=1
```

`go vet ./model ./appserver` still reports the pre-existing
`ResponsesAgentRunner` lock-copy warning at `model/responses_agent.go:1541`.

Passed on 2026-10-18.
