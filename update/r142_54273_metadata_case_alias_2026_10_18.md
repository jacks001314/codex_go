# Rust #54273: protect metadata writes through macOS case aliases

Upstream commit: `3bae578af33e43268e7bc2d6d5d0266f704bb554`

## Result

Equivalent implementation was already present in `sandbox/sandboxpath`.

- Darwin resolves ASCII case aliases for both the protected metadata component
  and writable-root components. It queries `_PC_CASE_SENSITIVE` across existing
  ancestors and fails closed when filesystem sensitivity is unavailable.
- The normalized alias is checked through the same read-only carveout and
  protected-name gates, so `.GIT`, `.CoDeX`, and other case variants cannot
  bypass metadata protection.
- Explicit metadata grants and narrower read/deny restrictions retain their
  existing precedence; mutable symlink targets are not substituted into the
  path.
- Non-Darwin platforms use their native path identity or no alias expansion,
  matching the executor-independent policy semantics.

## Verification

```text
go test ./sandbox/sandboxpath -count=1
go test ./sandbox -run 'Metadata|Protected|Case' -count=1
GOOS=darwin GOARCH=amd64 go test -c -o <temp> ./sandbox/sandboxpath
```

Passed on 2026-10-18.
