# Resume queue dispatch (#61216)

Resume and successful agent-switch restoration now record the restored thread ID. At the end of the current Model.Update, after history/settings/draft restoration, consumeResumedQueue checks that this thread is still displayed and calls the existing eligibility-gated submitNextQueued once. Stale restoration IDs cannot submit on a different conversation.

The first new regression showed an idle writable destination never dispatched its saved queue. After the change, the actual AgentSwitchResultMsg Update path dispatches one destination input, while running/read-only/missing-steer-receipt cases preserve the queue. A separate restore test checks stale-target suppression and one-shot consumption.

Validation:
- Focused switch/resume/dashboard/locked-fork tests: exit 0, 0.474s.
- go test ./tui/tea -count=1 -timeout 5m: exit 0, 47.798s.
- Subsequent go vet ./tui/tea: exit 0.
- Logs: resume_dispatch_red_2026_11_14.log, resume_dispatch_green_2026_11_14.log, resume_dispatch_full_2026_11_14.log.

Limitations: this is the immediate post-restore dispatch path, not full #61216 certification. Late-receipt wakeup, outgoing-event draining, buffered lifecycle state reconciliation, backend-qualified input identity, full draft attachment ownership and Cloud shell rejection remain open. Whole upstream synchronization and commit/push remain incomplete.
