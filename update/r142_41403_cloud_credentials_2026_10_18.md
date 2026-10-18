# #41403 Restrict cloud task credentials to trusted origins

## Upstream change

Rust validates the configured ChatGPT cloud-task backend before loading saved
credentials and refuses to follow redirects that could forward those
credentials to another origin.

## Go carriers

- `chatgptapi.ValidateChatGPTBaseURL` requires an HTTPS origin on port 443 with
  no user information, query, or fragment.
- `chatgptapi.NewCloudClientWithoutRedirects` clones the standard HTTP client
  with `CheckRedirect` returning `http.ErrUseLastResponse`.
- `app/cloud.go` validates `CODEX_CLOUD_TASKS_BASE_URL` and constructs cloud
  task clients through the no-redirect path.

## Verification

- `go test ./chatgptapi ./app -run 'CredentialOrigin|ValidateChatGPTBaseURL|CloudTask' -count=1`
