# Rust #44332: persist disabled plugin IDs in thread settings

Upstream commit: `b5544d5732f40431c57bf6ca3bed5b127cdf27a0`

## Result

Equivalent implementation was already present.

- `SettingsUpdateParams.DisabledPluginIDs` distinguishes an omitted or JSON
  null value from an explicit empty list. Supplied lists replace the saved
  selection, omission preserves it, and `[]` clears it.
- `UpdateSettingsWithCommit` applies the plugin selection inside the settings
  critical section and returns the committed snapshot used by notifications.
- `prepareTurnStartParams` carries the saved selection into turn start, and
  `activateThreadDisabledPluginIDs` freezes it at task admission so a pending
  settings update applies only to the next task.
- Thread resume and fork expose the persisted `disabled_plugin_ids` from thread
  metadata, including a cleared selection. Records without the field fall back
  to an empty selection.
- Subagent spawning keeps the parent plugin selection independently of forked
  model history, with explicit child overrides taking precedence.

## Verification

```text
go test ./appserver -run 'DisabledPluginIDs|ThreadSettings' -count=1
```

Passed on 2026-10-18.
