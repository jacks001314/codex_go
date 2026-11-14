# Guardian direct review #61163

Upstream e61a08f9ecb93a29d5379b123bee047fff0c7a24 routes direct review into reusable review sessions; the existing Go ReviewAssessment path already uses the same per-parent modelGuardianReviewer as live approval. Synced protocol documentation and completed regressions rather than adding a duplicate reviewer implementation.

Verified with deterministic model runners:
- Direct RPC A,A,B,A continues empty,review-1,empty,review-2; live approval then direct RPC continue review-4 and review-5.
- Both parent item histories remain unchanged; the recorded command never creates its output file.
- Request-permissions retry changes on-request -> never -> on-request: middle call returns null without inference; final call runs inference using existing review history.
- With the production on-demand reviewer initialization, both persistent and ephemeral parent metadata propagate the correct Ephemeral and SessionID into the model request.
- Existing parent reset regression remains passing.

`go test -count=1 -timeout 3m ./appserver -run 'TestThreadGuardianReview|TestGuardianSessionRunnerSummaryFreeResetInvalidatesSession'` exited 0. `go vet ./appserver` exited 0. Logs: direct_review_complete_2026_11_14.log and direct_review_complete_vet_2026_11_14.log.

Scope: Go carries reviewer persistence intent on AgentRequest instead of spawning Rust's internal reviewer thread. Tests verify this request boundary, not remote server retention or real model output. #61163 is complete at the Go equivalent boundary. Overall upstream synchronization, final global validation, Linux validation and commit/push are not complete. Full uncached test session 34226 remains running, last observed TUI subpackages with no fail events.
