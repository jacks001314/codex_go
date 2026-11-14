# Invalidate path-bound named snapshots on settings moves

Regression found that a CWD-only or roots-only settings update kept the old canonical named permission JSON. Next-turn preparation compared against the newly updated settings, so its same-path guard incorrectly reused the old snapshot. Both cases failed before the fix.

ThreadExtraService now drops only the canonical named snapshot on a changed CWD/root selection, retaining the name for policy-aware re-resolution. An update that also selects permissions supplies a freshly admitted snapshot. For that combined CWD+permissions update, admission now retargets inherited roots before profile resolution, matching settings commit behavior.

Final related tests passed (0.567s); go vet ./appserver exited 0. Logs named_snapshot_paths_before_2026_11_14.log, named_snapshot_paths_after_2026_11_14.log, named_snapshot_paths_vet_2026_11_14.log. Both regression cases exercise settings publication followed by next-turn preparation.

Limitations: re-resolution after a path move still depends on the named definition remaining available; materializing a frozen definition at new paths remains incomplete. Managed requirements, selected-environment rebinding, no-subscriber lifecycle reload and whole/native regression remain outstanding. No full synchronization completion or commit/push claimed.
