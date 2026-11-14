# Appserver integration failure: incomplete settings fixture

Full appserver run 64036 failed only TestRuntimeRouterDispatchesThreadExtrasAndRealtime. The fixture inserted a loaded-status entry for thread-1 but supplied neither a ThreadRouter/store record nor a definition for the selected custom profile trusted. Before admission normalization this passed an arbitrary name straight to settings; resolving permissions now requires actual thread/config context.

Updated the fixture to create thread-1 in a real temporary session store and define permissions.trusted.filesystem in its temporary config. Preserved the existing goal, shell/background-terminal, model/profile notification, service-tier clearing, conflicting-input and realtime assertions. Added router teardown and more useful settings-error diagnostics. No production checks relaxed.

Single regression passed 0.200s. Combined dispatch/admitted-snapshot/resolver-boundary/real-Git tests passed; see thread_extra_admission_regression_2026_11_14.log. go vet ./appserver exited 0. A fresh full appserver run was started after the correction (appserver_after_admission_fixture_2026_11_14.jsonl); it is still running and is not a pass claim.

Overall synchronization incomplete; no commit/push.
