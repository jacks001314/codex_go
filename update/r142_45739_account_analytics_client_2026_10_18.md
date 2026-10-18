# #45739 Typed account analytics reports in the backend client

## Upstream contract

Rust adds `Client::get_account_analytics` with report-specific routes for
`api/codex` and `wham`, typed responses for usage, credits, workspace messages,
plugins, and skills, and query parameters that preserve the Analytics views'
scope. Backend attribution and optional amounts remain visible, signed credit
events keep their sign, string values remain open for forward compatibility,
and a mismatched response shape returns a generic decode error without exposing
the response body.

## Go implementation

- `chatgptapi/analytics.go` adds all 28 upstream model/report symbols, including
  `AnalyticsReport`, `AnalyticsResponse`, and the daily usage, credit, plugin,
  and skill response models.
- `CloudClient.GetAccountAnalytics` selects the same report routes and query
  parameters for both Codex API and ChatGPT API base URLs. Credit events omit
  the date range and grouping parameters, enterprise credits send `breakdown`,
  enterprise tokens send `breakdown_by=model` with both `modes`, and plugin and
  skill reports send their top-N limit.
- Required fields are validated before typed decoding. A response carrying a
  different report's shape returns `Invalid analytics response.`; the payload
  is not included in the returned error or any diagnostics.
- `cloudResponseDecodeError` preserves the existing generic decode error text
  for other cloud endpoints while giving analytics a typed failure to redact.

## Verification

- `go test ./chatgptapi -run 'Analytics' -count=1`
- `go test ./chatgptapi -count=1`
- `go vet ./chatgptapi`

The downstream `#45740` TUI normalization and rendering remain owned by the
TUI synchronization lane.
