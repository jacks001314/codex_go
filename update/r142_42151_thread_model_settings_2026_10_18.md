# Rust #42151: thread model settings metadata

Upstream commit: `986ff1cc7ced0081ec5014b700a376333d87f869`

## Result

Implemented across read, list, and resume paths.

- The shared `Thread` object already had nullable `model` and
  `reasoningEffort` fields; `BuildThread` already reported persisted record
  values for unloaded threads.
- Loaded threads now overlay their live settings in `thread/read`,
  `thread/list`, `thread/search`, and `thread/resume` responses. Missing legacy
  values stay null.
- `thread/settings/update` now writes model and reasoning effort into the thread
  record (including the config override used by `reasoningEffortFromRecord`), so
  a restarted process reports the latest persisted snapshot for unloaded
  threads.

## Verification

```text
go test ./appserver -run 'ThreadReadReportsLiveModelSettings|ThreadListReportsLiveAndPersistedModelSettings|ThreadSettingsUpdatePersistsModelSettings' -count=1
go test ./appserver -run 'ThreadRead|ThreadList|ThreadSettingsUpdate|ThreadResume|ModelSettings' -count=1
```

Passed on 2026-10-18.
