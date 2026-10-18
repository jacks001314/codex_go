# #47867 Render remote permission paths using executor context

## Upstream change

Permission instructions resolve paths against the selected executor
environment's native cwd, home, and platform facts so remote Windows or Linux
paths render correctly. Entries that exceed the display budget are omitted
whole with a warning.

## Go status

Complete by equivalent implementation.

`sandbox/permission_prompt_executor.go` resolves permission entries with the
supplied executor facts, including native separators and HOME expansion.
`appserver/permissions_world_state.go` passes the executor environment's
filesystem facts into that renderer. The display budget omits complete entries
and reports the omission.

## Verification

- `go test ./sandbox -run 'PermissionPromptRemoteWindowsPaths|PermissionPromptRemote|PermissionPrompt' -count=1`
- `go test ./appserver -run 'PermissionsInstructionsUseRemoteExecutorPaths' -count=1`
