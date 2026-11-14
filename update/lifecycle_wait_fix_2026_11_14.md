# Appserver lifecycle test synchronization

Investigated both full-package failures. Idle eviction calls ReleaseLiveThreads before notify(thread/closed), so waitForIdleUnload does not prove notification delivery. Both lifecycle notification tests now wait for the actual thread/closed event before inspecting its payload. Collaboration-mode test now registers cleanup that waits for turn workers and closes live thread writers before TempDir deletion; turn/completed alone does not guarantee post-turn writes finished.

No production lifecycle behavior changed. The collaboration-mode test and both persisted/ephemeral delayed-start lifecycle tests passed 10 repetitions (18.521s). go vet ./appserver exited 0. Started a fresh complete appserver test with -count=1 -timeout 12m, log appserver_full_lifecycle_fixed_2026_11_14.log; result pending. This new run includes latest MCP context and originator refresh changes.

Overall upstream synchronization remains incomplete; no commit/push.
