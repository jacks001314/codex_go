# Post-Git full regression and appserver fixture fixes

Full test session 88859 finished with exit 1. JSON parsed without invalid lines: 140 package passes, 1 failure (appserver), 25 skips. Three failing tests: TestRuntimeRouterCommandExecutionEmitsAnalyticsLikeRust, TestRuntimeRouterFileChangeEmitsAnalyticsLikeRust, TestRuntimeRouterAPIKeyOpenAIProviderIncludesHostedImageGeneration. Full 19,234,580-byte log and summary have been synced. This run started before the idle-resume changes and is not a latest-tree certification.

The two analytics tests timed out at the hard-coded two-second item wait; notifications show the turn was still running. File-change and hosted-image tests also reported TempDir cleanup errors with directories not empty. All three passed in an unmodified isolated recheck (1.364s), so these are not claimed deterministic product regressions.

Changes: both analytics tests now await terminal turn status using the established bounded asynchronous wait before examining the retained item notification. All three tests use existing closeNotificationTestRouter to await tail workers then close the router before test directory cleanup. Event content assertions are unchanged.

Final targeted verification: five repetitions of all three tests passed; see post_git_failures_fixed_2026_11_14.log. go vet ./appserver exited 0. A fresh full appserver test was started after these fixes and the subscribed-idle changes; appserver_post_idle_2026_11_14.jsonl is still being written, so no full-package pass is claimed yet.

Overall upstream synchronization remains incomplete. No commit/push.
