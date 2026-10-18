# Guardian conversation history retrieval

Rust upstream: `41ed72c32b4980cd7919e1c2a45ecb1f96c5911a` -
`Add opt-in conversation history retrieval to Guardian reviews (#49036)`.

## Verdict

Complete for the Go reviewer architecture.

- `guardian_conversation_history_tools` remains disabled by default and is
  effective only with `features.apps`.
- The reviewer receives the two `user_message` direct-model tools,
  `search_messages` and `read_messages`, backed by the parent thread's live
  host-owned `codex_apps` connection and session identity.
- Each call rechecks app enablement and approval policy. Disabled tools return
  the parent app-configuration rejection; tools requiring approval fail closed
  because an interactive parent prompt is unavailable during review.
- History output is bounded by the configured
  `auto_review.conversation_history_max_output_tokens` default of 4,000 tokens,
  further tightened by the parent tool-output limit.
- `auto_review.experimental_conversation_history_prompt` replaces the bundled
  retrieval instructions, including an explicit empty override.

Go does not expose Rust's extension registry type, so the reviewer owns the
narrow history tool loop directly rather than adding an extension contributor
registry. The observable contract is preserved: only the two host-owned history
tools are exposed, and no private reviewer history is searched.

## Verification

```text
go test ./config -run 'GuardianConversationHistoryConfig|LoadGuardianConversationHistoryConfig' -count=1
go test ./appserver -run 'GuardianReviewExecutesParentHistoryTools|GuardianHistoryToolFailsClosed|GuardianConversationHistoryInstructions|GuardianReviewUsesConversationHistoryTools' -count=1
go test ./appserver -run Guardian -count=1
go test ./config -count=1
go vet ./appserver ./config
go build ./...
```
