# Guardian required-model review reuse

Rust upstream: `f98649cde9402d8b055154d1121248cc72163947` -
`Honor required reviews when reusing Guardian scores (#41309)`.

## Alignment

Rust now defers models listed in `auto_review.required_on_models` from the
Guardian V2 cached-score fast path, so a low-risk score cached before a model
switch cannot approve an action that skipped classification.

Go has no cached-score auto-approval path: `modelGuardianReviewer.Review`
normally samples a full review and fails closed when scoring metadata is stale.
The remaining equivalent fast path auto-approves `node_repl` JavaScript in user
approval mode. That path now checks the reviewed turn's required-model
requirements and defers protected models to the full reviewer.

The precedence matches Rust's routing exactly:

1. Full Access is approved before approval contributors run.
2. Required models bypass the user-mode fast approval.
3. All other approvals follow the existing complete review path.

`turnApprovalsReviewerForTurn` also continues to force `auto_review` for a
protected model, including when a request explicitly asks for `user`.

## Verification

```text
go test ./appserver -run 'RequiresFullReviewForProtectedModel|ProtectedModelForcesAutoReviewerForTurn|GuardianReviewerShortCircuitsInFullAccess|AutoAcceptsUserModeNodeReplJS' -count=1
go test ./appserver -count=1
go vet ./appserver
go test ./parity -count=1
```
