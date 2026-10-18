# #41928 Use executor path context for permission preapproval

## Upstream change

Rust normalizes, materializes, and compares additional filesystem permissions
using the selected executor's URI sandbox context, and fails closed when
symbolic temporary-directory or project-root grants cannot be resolved.

## Go status

Partial.

Implemented:

- `appserver.permissionPromptExecutorContextForTurn` captures the selected
  environment's cwd, home, and platform.
- `sandbox/permission_prompt_executor.go` resolves relative paths, `~` entries,
  deny-read globs, writable roots, and special paths in the executor's path
  convention.
- `TestPermissionsInstructionsUseRemoteExecutorPathsLikeRust` covers a Windows
  executor selected from a non-Windows host.

Remaining:

- The grant/preapproval path still uses
  `sandbox.IntersectPermissionProfiles(requested, granted, request.CWD)`.
- `PermissionProfileWithAdditionalPermissions` and its `cleanRunPath` /
  `materializePermissionGlob` helpers remain host-native and do not accept the
  executor policy context, temporary directories, or project roots.
- The selected-executor context is not yet threaded through exec_command,
  apply_patch, and extension-tool grant materialization, and unresolved symbolic
  temp/project-root grants are not explicitly failed closed at this layer.

## Verification

- `go test ./sandbox ./appserver -run 'Permission|ExecutorPaths|Policy' -count=1`
