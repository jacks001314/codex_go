# Provider authentication recovery progress

Rust upstream: `41d3dc56a0e1de47e30a9585c1b49253c082f8f7` -
`Surface model provider authentication recovery progress (#41239)`.

## Alignment

Rust emits a turn-scoped `authRecoveryStarted` event before a provider-owned
credential recovery and `authRecoveryCompleted` only after it succeeds.
Amazon Bedrock supplies:

- `AWS session has expired. Reauthenticating...`
- `Signed in with AWS.`

Go now derives the same lifecycle messages for Bedrock auth-refresh and
credential-export configurations. `ResponsesAgentRunner` emits the started
event before Bedrock credential recovery and the completed event only when that
recovery succeeds.

The app-server converts the events into:

- `modelProvider/authRecoveryStarted`
- `modelProvider/authRecoveryCompleted`

Each notification carries the reviewed `threadId`, `turnId`, provider name and
user-facing message. The runner callback is installed on the per-turn clone, so
concurrent turns cannot cross-deliver recovery progress.

`codex exec` writes started and completed messages to stderr, and the TUI adds
the started message as info history and the completed message with a check
mark, matching Rust's human-output processors.

## Verification

```text
go test ./model -run 'ProviderAuthRecovery|EmitsProviderAuthRecovery' -count=1
go test ./appserver -run 'AuthRecoveryNotificationsAreTurnScoped' -count=1
go test ./exec -run 'AuthRecovery' -count=1
go test ./tui/tea -run 'AuthRecovery' -count=1
go test ./parity -count=1
```
