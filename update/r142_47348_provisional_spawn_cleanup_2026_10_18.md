# Rust #47348: provisional subagent spawns and cancellation cleanup

Upstream commit: `5121aeff13aa6615081f9467c8051289201893fc`

## Result

Implemented the ownership and cleanup semantics of Rust's `PendingSpawn`.

- The child record and open spawn edge may exist while initial turn admission
  runs, but the registry reservation is not committed, the agent is not
  registered, and `thread/started` is not emitted yet.
- Admission success commits the reservation, registers the child, disarms the
  provisional state, and only then emits `thread/started`.
- Admission failure or caller cancellation cancels any turn started during the
  race, closes the spawn edge, releases registry state, and deletes the child
  record before returning.
- Go's record/store and spawn-graph writes are synchronous, so there is no
  separate background durability checkpoint to overlap. The durability barrier
  remains completed before spawn acknowledgement.

## Verification

```text
go test ./appserver -run 'ProvisionalSpawn|Spawn|AgentController|Delegate|Cancelled' -count=1
go vet ./appserver
```

Passed on 2026-10-18.
