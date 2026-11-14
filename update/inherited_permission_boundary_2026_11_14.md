# Recheck inherited snapshot paths at permission resolution

The admission guard alone was insufficient: after prepareThreadSettingsEnvironmentConfigToTurnStartParams captured a profile, later mutation of turn CWD or runtime roots still reused it. New table-driven resolver tests reproduced both failures. The same test covers unchanged inheritance, managed requirements, and changed named selection; those latter guard cases already passed.

The internal inherited selection now stores its CWD and a detached root slice at capture time. threadDefaultSandboxPermissionProfile checks both against current resolution inputs and rejects reuse when environment selections exist. Identity match and absence of managed requirements are still required; otherwise the established resolver runs. Captured paths are not rebound on repeated preparation.

Final targeted tests passed (0.562s), including prior idle/cold/CWD regressions. go vet ./appserver ./turn exited 0. Evidence: inherited_boundary_before_2026_11_14.log, inherited_boundary_after_2026_11_14.log, inherited_boundary_vet_2026_11_14.log. Changes synced to Master.

This prevents stale path grants; it does not implement frozen-definition retargeting or managed re-constraining. Those still re-resolve configuration. No-subscriber reload, broader upstream work, and full/native validation remain incomplete. No commit/push.
