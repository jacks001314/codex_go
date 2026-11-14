# Named permission next-turn boundary

Verified current production call chain:

1. prepareTurnStartParams calls applyThreadSettingsEnvironmentConfigToTurnStartParams. When settings.ActivePermissionProfile is present, that helper assigns params.Permissions and does not consume settings.normalizedPermissionProfile.
2. threadDefaultSandboxPermissionProfile prioritizes an explicit sandbox, then resolves params.Permissions through Config.ResolveSandboxPermissionProfileWithWorkspaceRoots.
3. restoredPermissionProfileResolution is only considered when params.Permissions is nil; its input is explicitly the unnamed rollout base profile. It rechecks managed requirements and merges current managed deny-read entries.
4. prepareTurnStartParams can also restore the named identity from record.Metadata.ActivePermissionProfile, so merely clearing Permissions in the settings helper would not be a complete fix.

Therefore the accepted named snapshot currently stabilizes subscribed idle resume only, not subsequent turns. A correct extension must distinguish inherited live identity+profile from explicit request selection; preserve explicit permissions/sandbox precedence, selected-environment precedence and requirements revalidation; and retarget workspace roots when CWD changes. Do not repurpose the unnamed persisted profile slot or bypass managed requirements to obtain a passing test.

This turn started a focused appserver test sweep matching Settings|Permission|Resume|CWD|WorkspaceRoots, covering more interactions than prior short runs. Results are recorded separately in permission_resume_sweep_2026_11_14.jsonl and its summary once the process exits. No next-turn production fix or overall completion claimed.
