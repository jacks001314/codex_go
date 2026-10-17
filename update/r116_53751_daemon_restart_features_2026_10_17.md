# Round 116 · #53751 daemon restart feature overrides

Rust source: `e762c05bd6`.

## Landed

- Explicit `app-server daemon restart` now parses `--enable`, `--disable`, and
  boolean `features.*` / `features.<name>.enabled` overrides.
- Feature aliases are canonicalized, with the canonical spelling taking
  precedence when an alias and canonical key are both supplied.
- Invalid or unknown feature toggles are rejected before the daemon operation
  lock is acquired, so the running daemon and saved settings are untouched.
- `LifecycleRunner.RestartApplyingFeatures` merges toggles into saved settings,
  removes conflicting saved alias spellings, preserves unrelated settings, and
  always restarts the managed backend. Restarts with no feature flags keep the
  existing explicit restart path.
- Nested feature tables without `enabled` are ignored like Rust's extraction
  from the CLI config layer; non-boolean `enabled` values fail validation.

## Verification

```text
go test ./app -run DaemonRestartFeatureOverrides -count=1       # ok
go test ./appserverdaemon -run RestartApplyingFeatures -count=1 # ok
go test ./appserverdaemon -count=1                              # ok
go vet ./app ./appserverdaemon                                  # ok
go build ./...                                                  # ok
```

The app daemon test subset still has host-environment failures around an
existing control socket and non-interactive terminal detection; the new parser
test passes independently. The lifecycle test covers stopped-daemon start,
cross-alias replacement, unrelated-setting preservation, and an always-restart
unchanged toggle.
