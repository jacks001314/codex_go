# Round 116 · #52223 daemon update network policy

Rust source: `d570b3632b`.

## Landed

- Daemon installer downloads now load user and managed configuration and bind
  the composed application network policy before creating the HTTP client.
- Managed denials and unlisted destinations stop installer fetches; explicitly
  allowed destinations remain reachable.
- Configuration load failures are propagated and abort the update. The updater
  no longer falls back to an unrestricted `http.DefaultClient`.
- `features.respect_system_proxy` is honored through the shared
  `network.NewHTTPClient` transport before policy binding.
- The PID update loop and foreground legacy migration both use the same
  policy-bound installer fetch. Caller-injected migration fetches are preserved
  for tests and custom hosts.

## Verification

```text
go test ./appserverdaemon -run 'UpdaterHTTPClient|UpdaterNetworkConfiguration' -count=1  # ok
go test ./appserverdaemon -count=1                                                       # ok
go vet ./appserverdaemon ./app                                                           # ok
go test ./app -run '^$'                                                                  # compile ok
go build ./...                                                                           # ok
```

The tests cover allowed, explicitly denied, and unlisted destinations plus
fatal configuration parsing errors for both updater and migration carriers.
