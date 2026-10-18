# Project list recency sorting

Rust upstream: `f6726e898612e27c66a25f7c6b294f6269d9ed4f` -
`Add recency sorting to project/list (#41223)`.

## Alignment

Go's project listing now exposes and sorts by `recencyAt`:

- recency is the newest recency timestamp among non-archived member threads;
- projects without a member thread sort after populated projects;
- ascending and descending recency orders are supported;
- cursor pagination uses the same recency/null ordering;
- manual `position` sorting remains available and independent of recency.

State and app-server tests cover populated/empty projects, both directions,
cursor behavior and compatibility with position sorting.

## Verification

```text
go test ./state -run 'ProjectListRecency|Project' -count=1
go test ./appserver -run 'Project' -count=1
go test ./parity -count=1
```
