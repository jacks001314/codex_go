# Direct Guardian review history verification

Upstream: e61a08f9ecb93a29d5379b123bee047fff0c7a24 (#61163).

The existing direct RPC calls ReviewAssessment on the same modelGuardianReviewer used for live approval; sessionFor keys reusable runners by parent thread. Added TestThreadGuardianReviewReusesHistoryWithinParent exercising four actual router RPCs in parent order A,A,B,A. Observed continuation IDs are empty,review-1,empty,review-2. All responses contain the model assessment; both actors retain their original recorded items, and the recorded command produces no file.

Validation: go test -count=1 -timeout 3m ./appserver -run 'TestThreadGuardianReview|TestGuardianSessionRunnerSummaryFreeResetInvalidatesSession' exited 0; go vet ./appserver exited 0. Logs: direct_review_history_2026_11_14.log and direct_review_history_vet_2026_11_14.log.

This uses an injected deterministic model runner, not a remote model. Parent persistence settings and policy changes between retries are still unaudited. #61163 remains partial. #61185 status also consolidated in the plan to reflect already delivered changes rather than remaining pending.

Full uncached serial test session 34226 remains active; latest observed package state. No global pass claimed. Full synchronization and commit/push remain incomplete.
