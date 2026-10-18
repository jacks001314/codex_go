# Round 759d: managed network, plugin mentions, hooks, and remaining audits

## Complete

- `#40489`: disabled managed-network requirements no longer activate a proxy;
  executor preparation clears proxy fields when the config is disabled.
- `#40490`: credential-broker project-state hardening (enabled/disabled/
  unconfigured) protects provider env and shell startup settings.
- `#40501`: `skills/list` serializes nullable `pluginId`; unified mentions use
  plugin ownership to hide owned skills while retaining standalone skills.
- `#40511`: interrupt hook discovery/execution, bounded timeout, output
  parsing, analytics, and TUI views are implemented.

## Partial / blocked

- `#40492` is partial: provisional titles and title helpers exist, but automatic
  post-turn generation/race-safe application is still missing.
- `#40498` is blocked on a background model-catalog refresh worker (Go refreshes
  on demand/TTL).

## Verification

- `go test ./appserver -run 'Skills|Plugin' -count=1`
- `go test ./tui/app -run PluginMentions -count=1`
- `go test ./appserver -run 'ManagedNetwork|Interrupt|Hook' -count=1`
- `go test ./config -run CredentialBroker -count=1`
- `go test ./parity -count=1`
