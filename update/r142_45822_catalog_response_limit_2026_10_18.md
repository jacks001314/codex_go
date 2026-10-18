# Rust #45822: opt-in response body limits

Upstream commit: `ced02c5c3807db59d912733b09722bebceb9b785`

## Result

Equivalent behavior is present at Go's explicit-catalog HTTP carrier.

- `HTTPModelsEndpoint.ListModels` applies a max+1 `LimitReader` whenever the
  provider has an explicit catalog URL.
- The bound is checked before status handling, so oversized diagnostic/error
  bodies also fail closed instead of bypassing the limit.
- Ordinary models-endpoint requests remain unbounded.
- Because the limit is derived from `CatalogURL` on each request, retries and
  auth refreshes preserve it without a separate mutable request field.

Added a regression test proving the limit applies only to explicit catalogs.

## Verification

```text
go test ./model -run 'ModelsCatalogBodyLimitOnlyApplies|ModelsCatalogURLRejectsOversized' -count=1
```

Passed on 2026-10-18.
