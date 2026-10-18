# #41400 Refresh MCP HTTP helper headers after authorization failures

## Upstream change

Rust reruns the HTTP headers helper after a same-origin `POST` receives 401 or
403 and retries once when effective headers change. Concurrent rejections share
one refresh, failed/unchanged refreshes keep the previous headers, retries stay
within the original timeout, and helper-supplied Authorization never overrides
an explicit OAuth/bearer credential.

## Go implementation

- `mcpHTTPHeadersHelperTransport.RoundTrip` loads the initial helper headers,
  then refreshes after a same-origin POST 401/403.
- `headersEqual` gates the single retry; unchanged output returns the original
  response.
- `refreshHeaders` serializes one helper run per rejection cohort with a refresh
  epoch and a completion channel. Failure retains the last successful headers.
- `roundTrip` skips a helper `Authorization` value whenever the request already
  carries an explicit Authorization header.
- Retry requests clone the original context, preserving the caller's timeout and
  existing redirect policy.

## Verification

- `go test ./mcp -run 'HTTPHeadersHelper' -count=1`
- `go test ./mcp -count=1`
