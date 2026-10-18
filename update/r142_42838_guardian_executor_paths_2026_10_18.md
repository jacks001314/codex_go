# Rust #42838: preserve executor paths in Guardian approval reviews

Upstream commit: `773f0b081de689b0d54f2809e7b17bfdb4c9f341`

## Result

Equivalent behavior.

- Guardian command actions copy `CWD` and argv/command text as strings.
- Patch actions copy `CWD` and change paths as the executor reported them.
- Neither path is passed through `filepath.Abs` or converted to a host-native
  absolute path, so a foreign executor convention (for example Windows paths
  reviewed on POSIX) remains reviewable and serializable.
- The shared Guardian action byte limit rejects oversized action payloads.

## Verification

```text
go test ./state ./appserver -run 'GuardianActionLimit|GuardianAction|GuardianReview' -count=1
```

Passed on 2026-10-18.
