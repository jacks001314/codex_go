# Inherited named snapshot into next turn

Added internal (json excluded) inherited permission ID and canonical JSON to TurnStartParams. applyThreadSettingsEnvironmentConfigToTurnStartParams supplies these only for an inherited live named selection with unchanged CWD, no explicit environment selection, and omitted or unchanged workspace roots. Explicit permissions/sandbox inputs do not receive the inherited snapshot.

threadDefaultSandboxPermissionProfile consumes it only when its captured ID still matches the selected name and no managed requirements exist. Explicit sandbox and environment profile precedence is preserved. With requirements present, the existing policy-aware named resolver is used instead, so constraints on allowed names and grants are not bypassed.

Regression starts/adopts a custom named profile with explicit CWD, removes its definition, and exercises prepareTurnStartParams followed by production permission resolution. It verifies the next turn retains the name and restricted network grants; explicit full-access selection remains effective. Before the implementation the next-turn resolution failed because the permissions table had been removed. Final related idle/CWD/cold-permission/step-settings tests passed (1.161s); go vet ./appserver ./turn exited 0.

Limitations: changed CWD/roots, selected environments and managed-requirement cases continue to re-resolve instead of retargeting/reconstraining a frozen named definition. When settings lack an explicit CWD but preparation fills it from the record, the guard intentionally does not reuse the snapshot. No broad feature-completion claim. Further lifecycle work and latest-tree full/native regression remain; no commit/push.
