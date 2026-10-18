# Rust #40647: validate step settings against proposed permissions

Upstream commit: `1d64085e67adec1506665e6861c03c1a78b947bb`

## Result

Implemented the settings constraint transaction in Go.

- `appserver/step_settings_constraints.go` validates the candidate model and
  approvals reviewer against the permission profile and primary environment
  proposed by the same write, rather than the permissions currently committed.
- A model listed in `auto_review.required_on_models` requires the `auto_review`
  reviewer, a profile without full-disk write access, and the Guardian approval
  feature. An invalid candidate fails with Rust's
  ``To use model `<model>`, you need to use auto review.`` error.
- A protected model selected without an explicit reviewer automatically chooses
  `auto_review` when managed requirements allow it.
- `thread/settings/update`, turn start, and active `turn/settings/update` share
  the validator. Failed writes leave the committed settings and active turn
  unchanged.
- Turn-start permission selections now flow into the thread settings commit, so
  a model plus read-only permission update remains valid for later turns.

## Verification

```text
go test ./appserver -run 'StepSettings|ProtectedModel|ThreadSettings|TurnSettings' -count=1
go vet ./appserver
```

Passed on 2026-10-18.
