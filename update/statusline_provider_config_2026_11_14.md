# Statusline provider configuration and persistence (2026-11-14)

## #61211 configuration

- Added `tui.status_line_provider` as a strict two-variant selection: the
  literal `default` or a table containing exactly `server` and `tool`.
- Project configuration and packaged defaults cannot select a provider. Their
  values are removed before effective merging and reported as ignored, but
  malformed values are validated first and still fail the load.
- A higher layer replaces the complete provider identity. A partial
  `{ server = ... }` cannot inherit `tool` from a lower layer.
- The effective selection is exposed by `Config.TUIStatusLineProvider`, carried
  through `SettingsWriteResult`/`Options`, applied on successful settings
  reloads, and passed to both local and remote TUI launch options.

## #61212 persistence

- `StatusLineProviderConfigEdit` writes `tui.status_line_provider` with
  `MergeReplace`.
- `ConfigService.BatchWrite` persists and reads back both provider and
  `default` selections.
- `Model.SaveStatusLineProvider` writes through the existing settings channel
  and stages the reloaded selection only after the host write succeeds; a
  failed write leaves the live value unchanged.

## Evidence

- `go test ./config -count=1` passed.
- `go test ./config -run TestStatusLineProvider -count=1` passed.
- `go test ./app -run 'TestInteractiveStatusLineProviderFromConfig|TestInteractiveStatusLineUseColors' -count=1` passed.
- `go test ./tui/tea -run TestSaveStatusLineProvider -count=1` passed.
- `go vet ./config ./app ./tui/tea` exited 0.
- Logs: `update/config_statusline_provider_final_2026_11_14.log`,
  `update/vet_statusline_provider_config_2026_11_14.log`.
