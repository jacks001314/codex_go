package execserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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
//
// notSentRetryable mirrors Rust #58355 passing an attempt deadline into
// `send_authenticated` for registration only: a credential deadline reached
// before the first send is RequestNotSent and may be replayed. Connection and
// harness validation wrap the whole operation instead, so their deadline stays
// a transport timeout.
func sendRegistryAuthenticated(ctx context.Context, cfg RemoteEnvironmentConfig, build registryRequestBuilder, notSentRetryable bool) (*http.Response, string, error) {
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
			// Rust #58355: a deadline reached while resolving credentials, before
			// the first transport send, is `RequestNotSent`, not a transport
			// timeout, so registration may safely retry it.
			if notSentRetryable && registryAuthResolutionTimedOut(err) {
				return nil, "", fmt.Errorf("%w: environment registry credential resolution timed out: %v", ErrRegistryCredentialsTransient, err)
			}
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

// registryAuthResolutionTimedOut mirrors Rust #58355
// `ExecServerError::RequestNotSent("environment registry credential resolution
// timed out")`: the credential stage exhausted its deadline before anything was
// written, so the registration replay loop may recover.
func registryAuthResolutionTimedOut(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
