# Plugin catalog listing and package resolution audit

Rust upstream: `a7e069dc3a Separate plugin catalog listing from package resolution (#46567)`.

## Verdict

Complete. Go already has separate catalog and provider layers: the plugin
catalog lists cloud/executor/local source locations and metadata, while the
provider resolves an authority-bound package into a concrete
`ResolvedPluginLocation`. Listing does not require package materialization.

## Verification

```text
go test ./plugin -count=1
```
