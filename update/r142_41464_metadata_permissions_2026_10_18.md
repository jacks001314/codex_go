# #41464 Preserve permissions when updating session metadata

## Upstream change

Rust defers legacy sandbox-policy projection until a cwd change may need to
rebind project-root writes. Updating only app-server client name/version must
not resolve filesystem paths or alter the existing permission snapshot.

## Go architecture

- App-server client name/version are connection and telemetry context, not
  fields of the session permission configuration.
- `thread/metadata/update` builds a `session.MetadataPatch` from explicit
  fields and leaves `ActivePermissionProfile` plus the stored `permissions`
  extra untouched.
- Go has no cwd-bound legacy `SandboxPolicy` bridge that would need to
  re-project filesystem entries during a metadata-only update.

## Verification

- `go test ./appserver -run 'Metadata|Permissions' -count=1`
