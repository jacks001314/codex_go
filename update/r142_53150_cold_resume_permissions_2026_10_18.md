# Rust #53150: restore thread-owned permissions on cold resume

Upstream commit: `7c9bfb910ef65c8f4a957c53affbd43d5fac2882`

## Result

Go already restored the thread-owned profile when preparing later turns, but
`thread/resume` still reported only the current request's permission fields.
The resume response now restores the same effective permission state.

- A request-scoped sandbox policy remains authoritative and skips persisted
  profile restoration.
- An explicit request permission profile wins over the saved named profile.
  Request config overrides (`permission_profile`, `default_permissions`, and
  sandbox overrides) also win over persisted state.
- Otherwise the saved named profile is reported and used to resolve the
  response sandbox policy.
- If only an unnamed base profile was saved in the owned turn context, it is
  restored as a fallback. Non-portable executor grants remain rejected by the
  existing portability checks, and managed deny-read is retained.
- `allowed_sandbox_modes` now constrains named/default permission selections and
  falls back to an allowed built-in profile, matching the cold-resume managed
  requirements case.

## Verification

```text
go test ./appserver -run 'ColdResumeRestoresThreadOwnedPermissions|ColdResumePermissionOverrides|PersistedPermission|PermissionProfile|ThreadResume|ColdResume' -count=1
go test ./config -count=1
go vet ./appserver ./config
```

Passed on 2026-10-18.
