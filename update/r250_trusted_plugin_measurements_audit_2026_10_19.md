# Trusted executor plugin measurements audit

Rust upstream: `6cf2ff11b3 Bind executor plugin measurements to the trusted plugin version (#46528)`.

## Verdict

Complete. Go resolves executor plugin metrics operations and measurement
declarations from the trusted plugin root/version captured for the thread, and
rejects declarations from a different plugin version. The sidecar validates
measurement rows before they reach app-server attribution analytics.

## Verification

```text
go test ./plugin -run 'PluginMetrics|Measurement|Trusted|Attribution' -count=1
```
