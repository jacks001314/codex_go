# #61218 Agents overview attached-thread filtering

## Behavior

- Added `agentsOverviewThreadVisible(thread)` in `app/agents_dashboard.go`.
- The overview now excludes both ephemeral threads and side chats whose `AttachedToThreadID` is non-empty.
- The same predicate is used by the local read-receipt snapshot path and the remote overview list path, so attached side chats cannot reappear through either source.

## Regression

`TestAgentsOverviewHidesAttachedAndEphemeralThreadsLikeRust` covers an ordinary root thread, an ephemeral thread, and an attached side chat, asserting only the root thread is returned.

## Evidence

- `go test ./app -run TestAgentsOverviewHidesAttachedAndEphemeralThreadsLikeRust -count=1` passed.
- Log: `update/agents_overview_attached_2026_11_14.log`.
