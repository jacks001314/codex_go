# Guardian policy contextual fragment

Rust upstream: `60821c50431c600074a2af6d943de76164c51638` -
`Represent Guardian policy as a contextual fragment (#40420)`.

## Alignment

Go now renders the resolved Guardian policy as a dedicated contextual fragment:

- role is `developer`;
- markers are empty;
- body is the fully rendered policy template plus output contract;
- content kind is `guardian.policy`;
- the fragment is appended as its own input message;
- top-level `AgentRequest.Instructions` is empty, avoiding duplicate policy
  delivery.

This preserves the policy as a distinct, auditable developer message through
compaction and history handling, matching Rust's GuardianPolicy fragment.

## Verification

```text
go test ./appserver -run 'GuardianReviewRequestCarriesThePolicyInstructionsLikeRust|GuardianReviewInstructionsPrecedence|Guardian' -count=1
go test ./context -count=1
go test ./parity -count=1
```
