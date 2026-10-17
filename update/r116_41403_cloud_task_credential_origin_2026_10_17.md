# Round 116 · #41403 cloud task credentials restricted to trusted origins

Rust source: `3ae4225b17`.

## Landed

- Added `chatgptapi.ValidateChatGPTBaseURL`, the Go analogue of
  `cloud_tasks::util::validate_chatgpt_base_url`: saved ChatGPT credentials may
  only be sent to `chatgpt.com`, `chat.openai.com`, or `chatgpt-staging.com`
  over HTTPS on port 443, with no user information, query, or fragment. Inputs
  are normalized through `NormalizeCloudBaseURL` and the error message matches
  Rust.
- `newCloudTaskClient` now validates the resolved base URL (environment, cloud
  config, ChatGPT base config, default) before loading saved credentials or
  constructing a client.
- Added `chatgptapi.NewCloudClientWithoutRedirects`, the Go analogue of
  `backend_client::Client::new_without_redirects`. The cloud task client uses
  it, so a 3xx response is reported as an HTTP status error instead of being
  followed with the `Authorization` header. Caller-supplied non-`*http.Client`
  doers keep their own behavior.

## Verification

```text
go test ./chatgptapi -count=1  # ok
go test ./app -run 'TestNewCloudTaskClient' -count=1  # ok
go vet ./chatgptapi ./app  # ok
go build ./...  # ok
```

Coverage includes valid ChatGPT/staging origins with explicit `:443`, scheme,
host, port, userinfo, query, fragment, and look-alike-host rejection, plus an
end-to-end redirect test proving the second origin receives no request.
