# Round 116 · #51908 async question availability

Rust source: `82e70121f8`.

## Landed

- Added `tools.experimental_request_user_input.enabled` resolution with Rust's
  default-true semantics.
- The direct `request_user_input` handler is omitted when that setting is
  disabled.
- `request_user_input_async` registration now requires the setting, a root
  agent, and either the current `request_user_input_async` or legacy
  `send_user_message_async` model capability.
- Legacy `send_user_message_async` now maps to the structured
  `request_user_input_async` tool instead of registering the free-form message
  handler.
- `send_message_to_user_async` availability remains independent of the
  request-user-input setting and still applies only to root agents.
- Persistent-mode guidance uses the same availability predicates, so it does
  not advertise async questioning when the tool surface withholds it.

## Verification

```text
go test ./config -run ExperimentalRequestUserInput -count=1  # ok
go test ./turn -run 'RequestUserInput|LegacyAsync|DisabledRequestUserInput' -count=1  # ok
go test ./appserver -run 'RequestUserInputAsyncAvailable|FreeformAsyncToolAvailable' -count=1  # ok
go test ./turn -count=1                                      # ok
go test ./appserver -run 'Async|RequestUserInput' -count=1   # ok
go vet ./config ./turn ./tool ./appserver                    # ok
go build ./...                                               # ok
```
