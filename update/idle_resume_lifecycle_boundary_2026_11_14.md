# Idle reload lifecycle boundary audit

The existing Go eviction path is not a shutdown transaction suitable for #61136:

- appserver/thread_idle_lifecycle.go unloadIdleChildThread claims pending under idleLifecycle.mu, then invokes markThreadUnloaded followed by ReleaseLiveThreads. Its residency pin guard prevents timer eviction during requests.
- acquireThreadResidencyPin increments a counter and allows multiple request holders. It does not serialize two resume requests or resume versus turn admission.
- appserver/runtime_router.go markThreadUnloaded removes status/subscriptions and discards session state before closing code-mode, MCP and network resources. Close errors are logged; the function returns no error.
- appserver/thread_manager.go ReleaseLiveThreads removes runtime entries before calling Close and ignores Close errors.

Consequently, directly calling these two functions from resume would not preserve the loaded session on shutdown failure and could race a concurrent input. Required implementation boundary: exclusive per-thread reload admission coordinated with input/subscription, a shutdown outcome before manager removal, and then a fresh rollout read/cold resume. Do not claim no-subscriber idle reload implemented based on timer eviction alone.

No lifecycle production change in this audit. Existing subscribed-idle fixes remain separate. Full regression result is recorded in go_test_post_git_summary_2026_11_14.json; the three appserver failures are being addressed before further lifecycle changes.
