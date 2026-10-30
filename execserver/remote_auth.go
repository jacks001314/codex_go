package execserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Rust #55987 (codex-rs/exec-server/src/remote/auth.rs,
// `EnvironmentRegistryClient::send_authenticated`): every registry request
// resolves its credentials per attempt and is replayed at most once after an
// HTTP 401 when the auth provider reports replacement credentials. Transport
// failures and every other status are never replayed here.

// ErrRegistryCredentialsTransient reports registry credential resolution that
// failed before a request was sent. Registration may safely retry it, while
// every other credential failure is terminal (Rust #58251
// ExecServerError::RequestNotSent).
var ErrRegistryCredentialsTransient = errors.New("environment registry credential resolution is temporarily unavailable")

// registryRequestBuilder builds a fresh registry request for one attempt. Rust
// clones the prepared request before replaying it, so the body has to be
// re-creatable instead of consumed by the first send.
type registryRequestBuilder func() (*http.Request, error)

// sendRegistryAuthenticated sends one registry request with current
// credentials and returns the final response plus the scoped executor
// credential, if any. Only a 401 answered by RefreshAfterUnauthorized is
// replayed, and only once, so a repeatedly rejected credential cannot loop.
func sendRegistryAuthenticated(ctx context.Context, cfg RemoteEnvironmentConfig, build registryRequestBuilder) (*http.Response, string, error) {
	client, err := remoteRegistryHTTPClientForAuth(cfg)
	if err != nil {
		return nil, "", err
	}
	retried := false
	for {
		request, err := build()
		if err != nil {
			return nil, "", err
		}
		headers, executorCredential, err := resolveRemoteEnvironmentAuth(ctx, cfg)
		if err != nil {
			return nil, "", fmt.Errorf("environment registry auth resolution failed: %w", err)
		}
		for key, values := range headers {
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}
		rejectedHeaders := request.Header.Clone()
		response, err := client.Do(request)
		if err != nil {
			return nil, "", fmt.Errorf("environment registry request failed: %w", err)
		}
		if retried || response.StatusCode != http.StatusUnauthorized || cfg.RefreshAfterUnauthorized == nil ||
			!cfg.RefreshAfterUnauthorized(ctx, rejectedHeaders) {
			// Rust wraps each attempt in the caller's deadline, so a context that
			// expired during refresh is a timeout, not the rejected response.
			if ctxErr := ctx.Err(); ctxErr != nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, errorBodyPreviewMaxBytes))
				_ = response.Body.Close()
				return nil, "", fmt.Errorf("environment registry request failed: %w", ctxErr)
			}
			return response, executorCredential, nil
		}
		// The rejected response is never used, so its connection is released
		// before the single replay runs.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, errorBodyPreviewMaxBytes))
		_ = response.Body.Close()
		retried = true
	}
}
