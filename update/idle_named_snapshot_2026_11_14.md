# Named permission admission snapshot

commitThreadSettingsUpdateWithConnection now resolves incoming named permissions before publishing settings and captures the canonical permission JSON alongside the identity. ThreadExtraService copies that JSON instead of unconditionally clearing it. Resolution includes incoming CWD/roots, with persisted CWD fallback. Caller settings are copied. Invalid new named selections fail before settings publication.

Subscribed idle resume already prefers the captured JSON; thus removing a named definition from the config file does not invalidate a previously accepted selection. The new regression admits a real custom profile from temporary TOML, removes the definition, verifies a new admission fails without changing the accepted snapshot, then resumes successfully with the retained identity and restricted network profile.

Before: regression failed `named admission did not capture a permission snapshot`. After final edits: subscribed idle, CWD, cold permission, ThreadExtra, running resume and step-settings tests passed (1.119s). go vet ./appserver exited 0. Logs: idle_named_snapshot_before_2026_11_14.log, idle_named_snapshot_after_2026_11_14.log, idle_named_snapshot_vet_2026_11_14.log.

Limits: this closes live resume freezing for named settings accepted through the runtime commit entry. Initial session admission, subsequent-turn named resolution, persisted/cold restoration and CWD retargeting of already frozen profiles still require a coherent follow-up. The running full appserver session 72221 predates this change. No full latest-tree pass or overall completion is claimed. No commit/push.
