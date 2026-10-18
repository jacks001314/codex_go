# Guardian prompt/model-message refactor audits

Rust upstream refactors:

- `#46026` Centralize model-message resolution and rendering in `codex-prompts`
- `#45417` Extract guardian transcript selection into `guardian-context`

## Verdict

Both are not applicable as code-organization changes. Go resolves catalog
messages in `model/catalog.go` and renders Guardian/mode/permission text in the
context/state/appserver packages; Guardian transcript selection is already owned
by `state/guardian_retained_context.go` and
`appserver/guardian_review_history.go`. There is no duplicated Rust prompt-module
boundary to extract or centralize.
