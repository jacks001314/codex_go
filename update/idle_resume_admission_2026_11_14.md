# Subscribed idle resume: reject invalid live permissions before dispatch

The response-stage validation added previously could reject a resume after ThreadRouter.dispatch and clearActiveDisabledPluginIDs had already changed runtime state. This turn adds admission validation in prepareSubscribedIdleResume, before dispatch. The response-stage validation remains to detect later invalid state.

Regression: TestSubscribedIdleResumeKeepsExistingConfiguration seeds an active disabled-plugin selection, corrupts the live permission snapshot, calls the public resume handler, and verifies both rejection and preservation of the selection. Before the fix it failed with `ids=[] active=false`; after the fix the subscribed-idle and running-override tests passed. go vet ./appserver exited 0.

Evidence: idle_resume_admission_before_2026_11_14.log, idle_resume_admission_after_2026_11_14.log, idle_resume_admission_vet_2026_11_14.log.

Scope: this is admission validation, not atomic lifecycle locking or rollback for every resume failure. Named-profile sandbox projection and unsubscribed idle shutdown/rebuild remain incomplete. Full test session 88859 was still running at the last check and started before the idle-resume changes; it cannot certify these changes. Overall synchronization is incomplete; no commit/push performed.
