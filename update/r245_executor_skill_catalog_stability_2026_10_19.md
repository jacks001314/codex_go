# Stable executor skill catalogs

Rust upstream: `e72da2b538 Stabilize skill catalogs across executor availability changes (#48353)`.

## Verdict

Complete. Go caches the selected executor catalog per thread and reuses it
across turns even when the executor filesystem changes later. Existing skill
descriptions remain stable, newly added skills are not admitted mid-thread, and
the extension byte budget remains authoritative without emitting a core
warning.

## Verification

```text
go test ./appserver -run 'SelectedExecutorCatalogReusesThreadCache|ExecutorCatalogUsesExtensionByteBudget' -count=1
```
