# Live named permissions on subscribed idle resume

Fixed a response mismatch: a loaded subscribed thread with live :read-only settings and older :danger-full-access rollout metadata returned the read-only identity but the full-access sandbox. The new public-handler regression failed before the fix with `live read-only profile returned sandbox danger-full-access`.

The subscribed-idle path now resolves the current named selection with the same permission resolver used for subsequent turns, passing current CWD/workspace roots and stored thread config overrides. Both response identity and sandbox reflect this selection. Admission checks also resolve the name before dispatch; a missing profile returns an error without clearing the active plugin selection. Normalized unnamed permissions retain their existing parsing behavior.

Validation: go test -count=1 ./appserver -run 'TestSubscribedIdleResume|TestColdResume.*Permission|TestRuntimeRouterThreadResumeRunningIgnoresOverrideMismatch' exited 0, 0.864s, after the final test edit. go vet ./appserver exited 0. Logs: idle_resume_named_before_2026_11_14.log, idle_resume_named_after_2026_11_14.log, idle_resume_named_vet_2026_11_14.log.

Limitations: named selection resolution still reads effective configuration instead of a fully frozen live runtime profile, leaving config-file drift semantics unresolved. No-subscriber shutdown/rebuild and atomic lifecycle coordination remain incomplete. Full regression session 88859 was still running at last check and predates idle-resume work; it does not certify this change. Overall synchronization remains incomplete; no commit/push.
