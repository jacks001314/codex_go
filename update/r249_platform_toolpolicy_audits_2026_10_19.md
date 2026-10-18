# Platform identity and ToolPolicy audits

Audited upstream commits:

- `#46334` Share platform identity across path, network, and sandbox configuration
- `#46999` Decouple tool restrictions from session identity with `ToolPolicy`

## Verdicts

- `#46334` complete: executor `PlatformOS` flows from environment info into
  permissions world state, network socket-path validation, sandbox permission
  prompts, and turn environment metadata.
- `#46999` not applicable: Go has no extension runtime or extension tool-policy
  API. App-server applies app/tool policy and Guardian tool scopes directly, so
  there is no extension session-identity restriction boundary to decouple.

## Verification

```text
go test ./network -run 'ExecutorOS|SocketPath|SocketAllowlist' -count=1
go test ./sandbox -run 'PlatformOS|PermissionPrompt|Seatbelt' -count=1
go test ./appserver -run 'PlatformOS|PermissionsExecutorContext|EnvironmentInfoReportsLocalPlatformOS' -count=1
```
