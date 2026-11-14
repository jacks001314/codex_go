# Thread-owned queued input (#61216)

Added per-thread parking/restoration for unsent follow-ups and rejected-steer retries. Thread reset parks input before removing transient UI state; switching restores only the destination thread's queue. Existing client-ID receipt reconciliation now operates on that restored queue. submitNextQueued holds input while submitted steers remain unconfirmed, while a turn-start receipt is pending, or while the destination is read-only.

Regression first reproduced cross-thread queue leakage and dequeue despite an unconfirmed steer. New tests cover A/B round trips, retry retention, destination receipt isolation, missing-receipt holds, and release only for a confirmed writable thread.

First full run found an existing locked-view fork contract: creating an editable fork intentionally moves the source input. Added explicit transfer on this successful fork path, consuming the parked source queue rather than leaving a duplicate. Existing locked-fork draft/image/steer/queue regression passes in the final full run.

Validation: focused resume/pending-steer/dashboard/reconnect sweep passed 0.522s. Final `go test ./tui/tea -count=1 -timeout 5m` exit 0, 60.006s; `go vet ./tui/tea` exit 0. Logs: resume_queue_red_2026_11_14.log, resume_queue_green_2026_11_14.log, resume_queue_full_2026_11_14.log (intermediate failure), resume_queue_full_final_2026_11_14.log.

Still incomplete: outgoing event draining and automatic idle queue dispatch after resume, buffered completion/interruption reconciliation, full draft attachments/paste ownership, backend-qualified identities, Cloud shell rejection and queue lifecycle cleanup need further work. Agent-switch response history does not yet call the same reconciliation as resume. Overall synchronization remains incomplete; no commit/push.
