# Full appserver regression findings

Session39822 completed exit 1. Appserver package failed after 471.725s; exact failures in mcp_context_full_2026_11_14.log:
- TestRuntimeRouterTurnStartAcceptsCollaborationModeOverrideLikeRust: TempDir RemoveAll cleanup directory not empty.
- TestEphemeralThreadStartFinishingAfterConnectionCloseStillUnloadsLikeRust: thread/closed notification missing after waitForIdleUnload.

Neither failure is dismissed as harmless. Next inspect asynchronous shutdown completion and the unload/notification ordering. The run began before latest empty-selection and originator-refresh changes, so further validation is also required for those changes.

Latest runtime/coordinator/refresh/fingerprint and explicit/host context regressions pass 1.472s (mcp_refresh_regression_2026_11_14.log); vet passes. Extended originator refresh regression confirms whitespace normalization does not refresh and clearing identity does refresh. Synced final test and evidence.

Overall synchronization, final full checks and commit/push remain incomplete.
