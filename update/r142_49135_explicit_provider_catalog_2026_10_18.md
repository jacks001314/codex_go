# Rust #49135: explicit provider model catalogs are authoritative

Upstream commit: `458f7046a52232f5c01dd8bc65dfbce8d5e79fa8`

## Result

Implemented.

- `RemoteModelsManagerOptions.ExplicitProviderCatalog` is set whenever the
  provider has `model_catalog_url`.
- An explicit catalog starts empty instead of seeding bundled models and does
  not fall back to bundled metadata when fetching is gated, fails, or returns an
  empty catalog.
- Fetched and cached provider entries replace bundled metadata without requiring
  a visible remote model.
- Explicit catalogs resolve model metadata only by exact slug; prefix and
  namespaced-suffix matching remain limited to the default catalog path.
- Explicit catalog responses reject blank or duplicate slugs with Rust's
  `model catalog must contain unique, non-empty model slugs` error.
- Thread start resolves a default from the explicit catalog. If no model is
  available, it fails with Rust's
  ``No models are available. Set `model` explicitly or check your model catalog configuration.``
  error.

## Verification

```text
go test ./model -count=1
go test ./appserver -run 'ThreadStartRequiresModelFromEmptyExplicitCatalog|ThreadStart|ModelList|Models|Provider' -count=1
```

The model suite passed. The appserver selection had only the two known
project-trust baseline failures.
