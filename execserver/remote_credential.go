package execserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Rust #54346 (`0003732489`) adds a scoped authentication mode to the remote
// exec-server registry client: the executor presents exactly one
// environment-bound `Authorization: Bearer <credential>` header instead of an
// account credential, and that credential is never echoed back.
//
// registryAuthMode mirrors Rust's `RegistryAuthMode`
// (codex-rs/exec-server/src/remote.rs:60).
type registryAuthMode int

const (
	// registryAuthModeLegacy is the default. The auth provider's headers are
	// sent as-is, exactly like Go's pre-#54346 behaviour.
	registryAuthModeLegacy registryAuthMode = iota
	// registryAuthModeExecutorCredential is Rust's `ExecutorCredential`: one
	// environment-bound bearer with no accompanying account credentials,
	// HTTPS-or-loopback only, and no Direct transport.
	registryAuthModeExecutorCredential
)

// executorCredentialTokenMaxBytes is Rust's scoped-mode bound on the bearer
// token (`token.len() <= 16 * 1024`, remote.rs `resolve_auth`).
const executorCredentialTokenMaxBytes = 16 * 1024

// errInvalidExecutorCredentialAuthorization mirrors Rust's
// `ExecServerError::EnvironmentRegistryAuth("invalid executor credential
// authorization")`. It never carries the rejected value.
var errInvalidExecutorCredentialAuthorization = errors.New("invalid executor credential authorization")

// safeExecutorRegistrationResponseError mirrors Rust's
// `SAFE_ERROR` in `parse_executor_registration_response`: every registry
// diagnostic that could echo the credential is replaced by this fixed text, so
// no path can echo the credential back to the caller's logs.
const safeExecutorRegistrationResponseError = "exec-server protocol error: invalid environment registry executor registration response"

// errScopedRegistryNeedsProxyBypass is returned when a loopback plain-HTTP
// registry needs the proxy-bypassing client Rust builds as `http_direct` and the
// host supplied neither that client nor an unwrappable transport.
var errScopedRegistryNeedsProxyBypass = errors.New(
	"environment registry config error: scoped executor credentials over loopback HTTP require a proxy-bypassing registry client")

// isLoopbackHost mirrors the loopback arm of Rust's
// `remote/direct.rs::require_tls_or_loopback` (direct.rs:308): `localhost`, an
// IPv4 loopback address, or an IPv6 loopback address.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if address := net.ParseIP(host); address != nil {
		return address.IsLoopback()
	}
	return false
}

// requireSecureOrLoopback mirrors Rust's
// `remote/direct.rs::require_tls_or_loopback` (direct.rs:297-325), which #54346
// additionally applies to the scoped registry URL: the URL must use
// `secureScheme`, or be a loopback destination on the scheme without its
// trailing `s` (HTTPS -> HTTP for the registry, WSS -> WS for a socket).
func requireSecureOrLoopback(rawURL string, secureScheme string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("environment registry config error: invalid remote endpoint URL: %v", err)
	}
	if parsed.Scheme == secureScheme {
		return nil
	}
	if insecureScheme := strings.TrimSuffix(secureScheme, "s"); insecureScheme != secureScheme &&
		isLoopbackHost(parsed.Hostname()) && parsed.Scheme == insecureScheme {
		return nil
	}
	return fmt.Errorf("environment registry config error: remote transport requires %s for non-loopback endpoints", secureScheme)
}

// isASCIIGraphicToken mirrors Rust's `token.bytes().all(|byte|
// byte.is_ascii_graphic())` bound in `resolve_auth`: RFC 6750 token characters
// only, so no whitespace or non-ASCII byte can reach the wire.
func isASCIIGraphicToken(token string) bool {
	for i := 0; i < len(token); i++ {
		if token[i] < 0x21 || token[i] > 0x7E {
			return false
		}
	}
	return true
}

// executorCredentialFromHeaders mirrors Rust's scoped-mode gate in
// `EnvironmentRegistryClient::resolve_auth` (remote.rs): the provider must have
// produced exactly one header, `Authorization`, holding exactly one
// `Bearer <token>` value whose token is nonempty, at most 16KiB, and entirely
// printable ASCII. Anything else is rejected before a request is built.
func executorCredentialFromHeaders(headers http.Header) (string, error) {
	if len(headers) != 1 {
		return "", errInvalidExecutorCredentialAuthorization
	}
	var values []string
	// Rust looks the header up by its normalized name in a `HeaderMap`, so the
	// gate cannot accept a companion header under any iteration order. Go's
	// http.Header canonicalizes keys on Set/Add but a literal keeps both
	// spellings, so every key is checked here instead of trusting the last one
	// a map happens to yield.
	for key, headerValues := range headers {
		if !strings.EqualFold(key, "Authorization") {
			return "", errInvalidExecutorCredentialAuthorization
		}
		values = headerValues
	}
	if len(values) != 1 {
		return "", errInvalidExecutorCredentialAuthorization
	}
	token, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok || token == "" || len(token) > executorCredentialTokenMaxBytes || !isASCIIGraphicToken(token) {
		return "", errInvalidExecutorCredentialAuthorization
	}
	return token, nil
}

// withAuthMode selects an auth mode on a config the caller built directly. It
// exists only for tests that need the per-request gate without the constructor's
// URL rule; production code uses WithExecutorCredentialAuth.
func (cfg RemoteEnvironmentConfig) withAuthMode(mode registryAuthMode) RemoteEnvironmentConfig {
	cfg.authMode = mode
	return cfg
}

// resolveRemoteEnvironmentAuth mirrors Rust's
// `EnvironmentRegistryClient::resolve_auth` (remote.rs, #54346): legacy mode
// returns the provider headers unchanged, while scoped mode also validates the
// single environment-bound bearer and requires an HTTPS (or loopback HTTP)
// registry URL. The credential is returned separately so the response parser can
// refuse to echo it.
func resolveRemoteEnvironmentAuth(ctx context.Context, cfg RemoteEnvironmentConfig) (http.Header, string, error) {
	headers, err := resolveRemoteEnvironmentAuthHeaders(ctx, cfg)
	if err != nil {
		return nil, "", err
	}
	if cfg.authMode != registryAuthModeExecutorCredential {
		return headers, "", nil
	}
	token, err := executorCredentialFromHeaders(headers)
	if err != nil {
		return nil, "", err
	}
	if err := requireSecureOrLoopback(cfg.BaseURL, "https"); err != nil {
		return nil, "", err
	}
	return headers, token, nil
}

// remoteRegistryHTTPClientForAuth mirrors Rust's
// `EnvironmentRegistryClient::http_for_auth` (remote.rs): a system proxy can
// otherwise receive a loopback HTTP credential in plaintext, so scoped auth over
// plain HTTP uses the proxy-bypassing pool Rust builds as `http_direct`.
func remoteRegistryHTTPClientForAuth(cfg RemoteEnvironmentConfig) (*http.Client, error) {
	client := cfg.HTTPClient
	if client == nil {
		client = remoteHTTPClient()
	}
	if cfg.authMode != registryAuthModeExecutorCredential || !isPlainHTTPURL(cfg.BaseURL) {
		return client, nil
	}
	if cfg.DirectHTTPClient != nil {
		return cfg.DirectHTTPClient, nil
	}
	return withoutSystemProxy(client)
}

// isPlainHTTPURL mirrors the `http` scheme test in Rust's `http_for_auth`.
func isPlainHTTPURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	return err == nil && parsed.Scheme == "http"
}

// withoutSystemProxy returns a copy of client that never consults the host proxy
// settings. Rust's `http_direct` pool is built that way; Go has no per-request
// proxy override, so the transport is cloned with Proxy unset.
func withoutSystemProxy(client *http.Client) (*http.Client, error) {
	if client == nil {
		client = remoteHTTPClient()
	}
	cloned := *client
	switch transport := client.Transport.(type) {
	case nil:
		direct := http.DefaultTransport.(*http.Transport).Clone()
		direct.Proxy = nil
		cloned.Transport = direct
	case *http.Transport:
		direct := transport.Clone()
		direct.Proxy = nil
		cloned.Transport = direct
	default:
		// A host-injected RoundTripper (for example the application network
		// policy client) has no proxy knob to clear, so this fails loudly rather
		// than silently sending a credential through a proxy.
		return nil, errScopedRegistryNeedsProxyBypass
	}
	return &cloned, nil
}

// parseExecutorRegistrationResponse mirrors Rust's
// `parse_executor_registration_response` (remote.rs, #54346): the credential may
// never be echoed back, so the unauthorized body is not even read, a failed
// registration keeps only the two retry-relevant error codes, and any response
// that contains the credential anywhere is rejected with fixed text.
func parseExecutorRegistrationResponse(response *http.Response, credential string) (*remoteRegistrationResponse, error) {
	status := response.Status
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("environment registry authentication error: environment registry authentication failed (%s): %s",
			status, registryErrorMessage("response body omitted"))
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, errors.New(safeExecutorRegistrationResponseError)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Only the fixed codes that drive retry behavior survive; a registry
		// diagnostic can echo the credential.
		code, _ := registryHTTPErrorMessage(string(body))
		if code != nil && *code != "registration_conflict" && *code != "authentication_service_unavailable" {
			code = nil
		}
		return nil, &remoteRegistryHTTPError{
			StatusCode: response.StatusCode,
			Code:       code,
			message:    fmt.Sprintf("environment registry request failed (%s): environment registry executor registration failed", status),
		}
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, errors.New(safeExecutorRegistrationResponseError)
	}
	if bytes.Contains(body, []byte(credential)) || jsonContainsExecutorCredential(value, credential) {
		return nil, errors.New(safeExecutorRegistrationResponseError)
	}
	var decoded remoteRegistrationResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, errors.New(safeExecutorRegistrationResponseError)
	}
	return &decoded, nil
}

// jsonContainsExecutorCredential mirrors Rust's `json_contains_executor_credential`:
// every string value and every object key is searched, recursively.
func jsonContainsExecutorCredential(value any, credential string) bool {
	if credential == "" {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, credential)
	case []any:
		for _, item := range typed {
			if jsonContainsExecutorCredential(item, credential) {
				return true
			}
		}
		return false
	case map[string]any:
		for key, item := range typed {
			if strings.Contains(key, credential) || jsonContainsExecutorCredential(item, credential) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
