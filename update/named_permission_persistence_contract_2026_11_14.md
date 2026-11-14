# Named selection persistence contract

Inspection corrected an unsafe assumption: recordPermissionProfileJSON and checkpointThreadSettings treat metadata.permission_profile as the unnamed base profile; the active named selection is persisted separately as ActivePermissionProfile. The initial probe expecting the named canonical snapshot in the base slot failed, but that failure is not evidence of a product bug. No persistence implementation was changed.

Extended the named-snapshot regression to exercise durable writes: first admit/persist a full-access unnamed base, then admit/persist the restricted custom named profile. Re-read the session store and assert that the nonempty base profile remains byte-identical and the named identity is audit. Continue the prior live test by removing the definition: a new admission is rejected without changing the accepted snapshot, and subscribed idle resume uses the accepted snapshot.

Final test command: go test -count=1 ./appserver -run 'TestSubscribedIdleResumeKeepsAdmittedNamedProfileSnapshot|TestColdResume.*Permission' passed. go vet ./appserver passed. Logs named_permission_persistence_contract_2026_11_14.log and named_permission_persistence_vet_2026_11_14.log; named_permission_persist_before_2026_11_14.log is the invalid-assumption probe, not red-before evidence for a production fix.

The named profile remains re-resolved on cold resume by existing behavior. Fully frozen live permissions across subsequent turns and CWD changes, plus no-subscriber reload, still need work. Full appserver session 72221 remains running and predates the named admission snapshot change. Overall synchronization incomplete; no commit/push.
