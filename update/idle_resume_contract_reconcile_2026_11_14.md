# Live rejoin versus cold resume regression reconciliation

Full appserver session 72221 finished exit 1 (514.318s). Its three failures were TestRuntimeRouterTurnStartRuntimeWorkspaceRootsResumeFromLoadedThread, TestRuntimeRouterRuntimeWorkspaceRootsEmptyVsOmittedLikeRust, and TestThreadResumeUsesSavedProviderWhenDefaultChangesLikeRust. Complete JSON and summary are synced. This run predates named snapshot admission changes.

These failures exposed old tests assuming a second subscribed idle resume accepts overrides. The #61136 contract audited in idle_resume_audit_2026_11_14.md retains live configuration when subscribers exist. Tests now distinguish that case explicitly:

- Loaded subscribed workspace-root resume expects both response and persisted roots to remain unchanged after override attempts.
- Empty-versus-omitted roots test waits for turn workers and explicitly evicts the runtime in the fixture before each cold resume. Existing root retargeting and clearing expectations are retained.
- Saved-provider/model override test closes the first router and creates a fresh router before asserting a cold model override while preserving the saved provider.

These fixture transitions do not implement automatic no-subscriber reload. Targeted reconciliation plus subscribed-idle tests pass (1.122s); go vet ./appserver exits 0. No production behavior changed this turn. No full latest-tree pass is claimed, and no duplicate full test started. Overall synchronization incomplete; no commit/push.
