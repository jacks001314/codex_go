# Shared Apps tool catalog audit

Rust upstream: `f8bed26f7b Share Apps tool catalogs without retaining unused snapshots (#45440)`.

## Verdict

Complete. Go shares the Codex Apps catalog through its connection provider,
preserves intentionally empty cached catalogs, bounds entries to the Apps
catalog limit, and rebuilds tool bindings from that shared provider without
retaining per-turn unused snapshots.

## Verification

```text
go test ./mcp -run 'Catalog|Binding|Apps' -count=1
```
