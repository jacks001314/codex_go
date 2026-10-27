package app

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/network"
)

// execServerRegistryAuth supplies the credentials remote exec-server registry
// requests send. Rust #55987 (cli/src/exec_server_command.rs
// `load_exec_server_remote_auth_provider`) uses the live AuthManager-backed
// provider for ChatGPT and workload identity auth, so a rotation written by
// another process is picked up on the next request and ChatGPT auth refreshes
// proactively. Go has no long-lived AuthManager, so the account store is the
// source of truth and a mutex stands in for its refresh guard.
type execServerRegistryAuth struct {
	codexHome    string
	storeOptions *auth.StoreOptions
	httpClient   *http.Client

	// refreshMu is Rust's `refresh_lock`: a caller that queued behind a
	// completed refresh reuses its result instead of spending the rotated
	// refresh token a second time.
	refreshMu sync.Mutex
}

func newExecServerRegistryAuth(codexHome string, storeOptions *auth.StoreOptions, httpClient *http.Client) *execServerRegistryAuth {
	return &execServerRegistryAuth{
		codexHome:    codexHome,
		storeOptions: storeOptions,
		httpClient:   httpClient,
	}
}

// ResolveAuthHeaders is Rust's `AuthProvider::resolve_auth_headers`: it reports
// the credentials for one registry attempt and proactively refreshes managed
// ChatGPT auth that is inside the expiry window or overdue on its schedule.
func (a *execServerRegistryAuth) ResolveAuthHeaders(ctx context.Context) (http.Header, error) {
	_, headers, err := a.resolveAuth(ctx)
	return headers, err
}

// RefreshAfterUnauthorized is Rust's `AuthProvider::refresh_after_unauthorized`
// (#55987). It reports whether the registry request may be replayed once with
// replacement credentials: either a different credential is already available,
// or a forced refresh produced one. A rejected credential is never replayed
// unchanged.
func (a *execServerRegistryAuth) RefreshAfterUnauthorized(ctx context.Context, rejected http.Header) bool {
	current, headers, err := a.resolveAuth(ctx)
	if err != nil || current == nil || len(headers) == 0 {
		return false
	}
	if !registryAuthHeadersMatch(headers, rejected) && auth.RefreshFailureForAuth(a.codexHome, current) == nil {
		// The credential source or a concurrent caller already supplied a
		// different credential, so the replay does not refresh again.
		return true
	}
	if !execServerAuthSupportsUnauthorizedRecovery(current) {
		return false
	}
	_, refreshErr := a.refreshIfUnchanged(ctx, current, auth.RefreshReasonForced)
	refreshed, refreshedHeaders, resolveErr := a.loadAuthHeaders()
	if resolveErr != nil || refreshed == nil || len(refreshedHeaders) == 0 {
		return false
	}
	// Even a failed refresh may race with another writer that replaced the
	// stored credential; a late rejection must still not reuse a credential
	// known to have failed.
	if auth.RefreshFailureForAuth(a.codexHome, refreshed) != nil {
		return false
	}
	return !network.IsPolicyError(refreshErr) && !registryAuthHeadersEqual(refreshedHeaders, headers)
}

func (a *execServerRegistryAuth) resolveAuth(ctx context.Context) (*auth.AuthDotJSON, http.Header, error) {
	resolved, err := auth.NewStoreWithOptions(a.codexHome, a.storeOptions).Resolve()
	if err != nil {
		return nil, nil, err
	}
	if resolved == nil {
		return nil, nil, errExecServerRemoteAuthRequired
	}
	snapshot := &resolved.Auth
	if reason, ok := auth.ProactiveRefreshReason(snapshot, time.Now()); ok {
		if refreshed, refreshErr := a.refreshIfUnchanged(ctx, snapshot, reason); refreshErr == nil && refreshed != nil {
			snapshot = refreshed
		}
	}
	headers, err := execServerRemoteAuthHeaders(snapshot)
	if err != nil {
		return nil, nil, err
	}
	return snapshot, headers, nil
}

func (a *execServerRegistryAuth) loadAuthHeaders() (*auth.AuthDotJSON, http.Header, error) {
	resolved, err := auth.NewStoreWithOptions(a.codexHome, a.storeOptions).Resolve()
	if err != nil || resolved == nil {
		return nil, nil, err
	}
	headers, err := execServerRemoteAuthHeaders(&resolved.Auth)
	if err != nil {
		return nil, nil, err
	}
	return &resolved.Auth, headers, nil
}

// refreshIfUnchanged mirrors Rust's `AuthManager::refresh_token_if_unchanged`:
// under the refresh guard the stored credential is reloaded first, and a
// credential that already changed is reused instead of refreshed again.
func (a *execServerRegistryAuth) refreshIfUnchanged(ctx context.Context, expected *auth.AuthDotJSON, reason auth.RefreshReason) (*auth.AuthDotJSON, error) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	current, err := auth.NewStoreWithOptions(a.codexHome, a.storeOptions).Resolve()
	if err != nil {
		return nil, err
	}
	if current != nil && !auth.AuthsEqualForRefresh(expected, &current.Auth) {
		return &current.Auth, nil
	}
	return auth.RefreshChatGPTTokens(ctx, &auth.RefreshChatGPTTokenOptions{
		CodexHome:    a.codexHome,
		HTTPClient:   a.httpClient,
		AuthSnapshot: expected,
		StoreOptions: a.storeOptions,
		Reason:       reason,
	})
}

// execServerAuthSupportsUnauthorizedRecovery mirrors Rust's
// `CodexAuth::supports_unauthorized_recovery`: managed and external ChatGPT
// auth can recover from a 401, API keys cannot.
func execServerAuthSupportsUnauthorizedRecovery(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens":
		return true
	default:
		return false
	}
}

// registryAuthHeadersMatch mirrors Rust's comparison against the rejected
// request headers: every header the provider contributed must still be present
// with the same first value. The rejected request also carries transport
// headers (content type, accept), which are not part of the credential.
func registryAuthHeadersMatch(headers http.Header, rejected http.Header) bool {
	for name, values := range headers {
		other, ok := rejected[name]
		if !ok || len(other) == 0 || len(values) == 0 || other[0] != values[0] {
			return false
		}
	}
	return true
}

func registryAuthHeadersEqual(left http.Header, right http.Header) bool {
	if len(left) != len(right) {
		return false
	}
	for name, values := range left {
		other, ok := right[name]
		if !ok || len(other) != len(values) {
			return false
		}
		for index := range values {
			if values[index] != other[index] {
				return false
			}
		}
	}
	return true
}

var errExecServerRemoteAuthRequired = errors.New(
	"remote exec-server registration requires ChatGPT authentication or API key authentication; run `codex login` or set CODEX_API_KEY")
