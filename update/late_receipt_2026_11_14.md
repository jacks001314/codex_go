# Late receipt queue wakeup (#61216)

The last committed pending steer now marks the current thread for a queue check after Model.Update finishes. This reuses resume's target-thread and writable/idle/pending gates. Unknown, wrong-thread, partial and duplicate receipts do not cause extra submissions.

New regression first failed with submissions=0 queued=1 after the final receipt. Focused tests pass 0.243s. Full go test ./tui/tea -count=1 -timeout 5m passes 47.638s; subsequent go vet ./tui/tea exits 0 (completed session 47809 before interruption). Additional tests verify running and read-only destinations still hold the queue after confirming delivery.

Logs: late_receipt_red_2026_11_14.log, late_receipt_green_2026_11_14.log, late_receipt_full_2026_11_14.log. Overall synchronization remains incomplete; no commit/push yet. User explicitly reaffirmed that final code must be pushed to main.
