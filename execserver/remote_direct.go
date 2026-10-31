package execserver

// Rust parity: codex-rs/exec-server/src/remote/direct.rs (#42781) plus the
// `authenticate_websocket_request` / `connect_websocket_request` helpers from
// client_transport.rs. A direct executor registers the `direct_jsonrpc_v1`
// transport and then carries plain exec-server JSON-RPC messages over an
// authenticated WebSocket instead of the Noise rendezvous relay.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	// DirectTransport mirrors Rust DIRECT_TRANSPORT.
	DirectTransport = "direct_jsonrpc_v1"
	// directConnectTimeout mirrors Rust DEFAULT_REMOTE_EXEC_SERVER_CONNECT_TIMEOUT.
	directConnectTimeout = 10 * time.Second
)

// RemoteEnvironmentTransport mirrors Rust ExecServerRemoteTransport.
type RemoteEnvironmentTransport string

const (
	RemoteTransportNoise  RemoteEnvironmentTransport = "noise"
	RemoteTransportDirect RemoteEnvironmentTransport = "direct"
)

// ErrDirectSigningTransient reports handshake or registry authentication that
// may succeed on a later attempt (Rust AuthError::Transient). Every other
// signing failure is terminal, matching AuthError::Build.
var ErrDirectSigningTransient = errors.New("direct exec-server authentication is temporarily unavailable")

// DirectHandshakeSigner mirrors Rust AuthProvider::apply_auth for the direct
// transport: it signs an HTTP projection of the registration request or the
// WebSocket handshake and returns the authenticated URL plus headers. It runs
// again for every reconnect so short-lived signatures stay valid.
type DirectHandshakeSigner interface {
	SignRequest(ctx context.Context, method string, rawURL string, headers http.Header, body []byte) (string, http.Header, error)
}

type directRegistrationRequest struct {
	Transport string `json:"transport"`
}

type directRegistrationResponse struct {
	EnvironmentID  string `json:"environment_id"`
	Transport      string `json:"transport"`
	RegistrationID string `json:"registration_id"`
	URL            string `json:"url"`
}

// registerDirectEnvironment mirrors Rust
// EnvironmentRegistryClient::register_direct_environment. Only the confirmed
// pre-write failures the registry documents are retried; every other failure is
// terminal so an ambiguous registration write is never replayed.
func registerDirectEnvironment(ctx context.Context, cfg RemoteEnvironmentConfig) (*directRegistrationResponse, error) {
	body, err := json.Marshal(directRegistrationRequest{Transport: DirectTransport})
	if err != nil {
		return nil, err
	}
	endpoint := remoteEndpointURL(cfg.BaseURL, "/cloud/environment/"+url.PathEscape(cfg.EnvironmentID)+"/direct/register")
	build := func() (*http.Request, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("environment registry request failed: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		return request, nil
	}
	response, err := sendDirectAuthenticated(ctx, cfg, build, body)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, remoteRegistryStatusError(response)
	}
	var decoded directRegistrationResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("failed to decode environment registry response: %w", err)
	}
	if decoded.EnvironmentID != cfg.EnvironmentID {
		return nil, errors.New("exec-server protocol error: environment registry returned a different environment id")
	}
	if decoded.Transport != DirectTransport {
		return nil, fmt.Errorf("exec-server protocol error: environment registry returned unsupported direct transport `%s`", decoded.Transport)
	}
	if strings.TrimSpace(decoded.RegistrationID) == "" || strings.TrimSpace(decoded.URL) == "" {
		return nil, errors.New("exec-server protocol error: environment registry returned incomplete direct connection data")
	}
	if err := requireSecureOrLoopback(decoded.URL, "wss"); err != nil {
		return nil, err
	}
	return &decoded, nil
}

// sendDirectAuthenticated sends one registry request with the resolved auth
// headers and, when configured, a SigV4 signature over the final body.
func sendDirectAuthenticated(ctx context.Context, cfg RemoteEnvironmentConfig, build registryRequestBuilder, body []byte) (*http.Response, error) {
	headers, executorCredential, err := resolveRemoteEnvironmentAuth(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("environment registry auth resolution failed: %w", err)
	}
	if executorCredential != "" {
		return nil, errors.New("environment registry config error: scoped executor credentials are not supported by the direct exec-server transport")
	}
	request, err := build()
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	method := request.Method
	requestURL := request.URL.String()
	signedHeaders := request.Header.Clone()
	if cfg.DirectSigner != nil {
		signedURL, signerHeaders, signErr := cfg.DirectSigner.SignRequest(ctx, method, requestURL, signedHeaders, body)
		if signErr != nil {
			return nil, fmt.Errorf("environment registry authentication error: failed to resolve environment registry authentication: %w", signErr)
		}
		requestURL = signedURL
		signedHeaders = signerHeaders
	}
	signed, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("environment registry config error: %w", err)
	}
	for key, values := range signedHeaders {
		for _, value := range values {
			signed.Header.Add(key, value)
		}
	}
	client := cfg.HTTPClient
	if client == nil {
		client = remoteHTTPClient()
	}
	response, err := client.Do(signed)
	if err != nil {
		return nil, fmt.Errorf("environment registry request failed: %w", err)
	}
	return response, nil
}

// runDirectEnvironment mirrors Rust run_direct_environment: the registration is
// reused across transient disconnects, a handshake 409 Conflict forces a new
// registration, and non-loopback endpoints must stay on TLS.
func runDirectEnvironment(ctx context.Context, cfg RemoteEnvironmentConfig, server *Server) error {
	if err := requireSecureOrLoopback(cfg.BaseURL, "https"); err != nil {
		return err
	}
	registration, err := registerDirectEnvironment(ctx, cfg)
	if err != nil {
		return err
	}
	server.SetExecutorRegistrationIdentity(cfg.EnvironmentID, registration.RegistrationID)
	var retryAttempt uint32
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		conn, response, connectErr := dialDirectEnvironment(ctx, cfg, registration.URL)
		if connectErr == nil {
			retryAttempt = 0
			serveErr := server.serveWebSocketConnection(ctx, conn)
			if ctx.Err() != nil {
				return nil
			}
			outcome := "disconnected"
			if serveErr != nil {
				outcome = "error"
			}
			slog.Info("direct exec-server connection ended",
				"remote_event", "direct_connection",
				"remote_outcome", outcome,
				"registration_id", registration.RegistrationID,
			)
		} else {
			status := 0
			if response != nil {
				status = response.StatusCode
			}
			if !directConnectRetryable(status) {
				return connectErr
			}
			if status == http.StatusConflict {
				// A handshake conflict rejects this registration; other transient
				// failures reconnect against the existing URL.
				registration, err = registerDirectEnvironment(ctx, cfg)
				if err != nil {
					return err
				}
				server.SetExecutorRegistrationIdentity(cfg.EnvironmentID, registration.RegistrationID)
			}
			slog.Warn("direct exec-server connection failed; retrying",
				"remote_event", "direct_connection",
				"remote_outcome", "error",
				"http_status", status,
			)
		}
		if err := sleepContext(ctx, registrationConflictRetryDelay(cfg.EnvironmentID, retryAttempt)); err != nil {
			return nil
		}
		retryAttempt++
	}
}

func dialDirectEnvironment(ctx context.Context, cfg RemoteEnvironmentConfig, rawURL string) (*websocket.Conn, *http.Response, error) {
	headers := http.Header{}
	authHeaders, err := resolveRemoteEnvironmentAuthHeaders(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("environment registry auth resolution failed: %w", err)
	}
	for key, values := range authHeaders {
		for _, value := range values {
			headers.Add(key, value)
		}
	}
	authenticatedURL, signedHeaders, err := authenticateDirectWebSocketRequest(ctx, cfg.DirectSigner, rawURL, headers)
	if err != nil {
		return nil, nil, err
	}
	for key, values := range signedHeaders {
		headers.Del(key)
		for _, value := range values {
			headers.Add(key, value)
		}
	}
	if err := requireSecureOrLoopback(authenticatedURL, "wss"); err != nil {
		return nil, nil, err
	}
	client := cfg.HTTPClient
	if client == nil {
		client = remoteHTTPClient()
	}
	dialCtx, cancel := context.WithTimeout(ctx, directConnectTimeout)
	defer cancel()
	conn, response, err := cfg.Dial(dialCtx, authenticatedURL, &websocket.DialOptions{HTTPHeader: headers, HTTPClient: client})
	if err != nil {
		return nil, response, fmt.Errorf("failed to connect to exec-server websocket %s: %w", directDiagnosticURL(authenticatedURL), err)
	}
	return conn, response, nil
}

// authenticateDirectWebSocketRequest mirrors Rust
// authenticate_websocket_request: the signer sees an http(s) projection of the
// handshake with only the Host header, may replace the URL (for example with
// presigned query parameters), and must not rewrite WebSocket handshake
// headers.
func authenticateDirectWebSocketRequest(ctx context.Context, signer DirectHandshakeSigner, rawURL string, headers http.Header) (string, http.Header, error) {
	if signer == nil {
		return rawURL, nil, nil
	}
	signingHeaders := http.Header{}
	if host := headers.Get("Host"); host != "" {
		signingHeaders.Set("Host", host)
	}
	signedURL, signedHeaders, err := signer.SignRequest(ctx, http.MethodGet, directSigningURL(rawURL), signingHeaders, nil)
	if err != nil {
		return "", nil, err
	}
	websocketURL, err := directWebSocketURL(signedURL)
	if err != nil {
		return "", nil, err
	}
	out := http.Header{}
	for key, values := range signedHeaders {
		if isWebSocketHandshakeHeader(key) {
			if strings.EqualFold(key, "Host") && len(values) == 1 && values[0] == headers.Get("Host") {
				continue
			}
			return "", nil, fmt.Errorf("direct exec-server handshake authentication changed WebSocket handshake header %s", key)
		}
		for _, value := range values {
			out.Add(key, value)
		}
	}
	if host := headers.Get("Host"); host != "" {
		out.Set("Host", host)
	}
	return websocketURL, out, nil
}

// directConnectRetryable mirrors the retry predicate in run_direct_environment:
// a client error is terminal except for request timeout, conflict, and rate
// limiting.
func directConnectRetryable(status int) bool {
	if status >= 400 && status < 500 {
		switch status {
		case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
			return true
		default:
			return false
		}
	}
	return true
}

func directSigningURL(rawURL string) string {
	if rest, ok := strings.CutPrefix(rawURL, "wss://"); ok {
		return "https://" + rest
	}
	if rest, ok := strings.CutPrefix(rawURL, "ws://"); ok {
		return "http://" + rest
	}
	return rawURL
}

func directWebSocketURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("direct exec-server handshake authentication returned an invalid URL: %w", err)
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", fmt.Errorf("direct exec-server handshake authentication returned unsupported WebSocket URL scheme %q", parsed.Scheme)
	}
	return parsed.String(), nil
}

func isWebSocketHandshakeHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Host", "Connection", "Upgrade", "Content-Length", "Transfer-Encoding":
		return true
	}
	return strings.HasPrefix(strings.ToLower(name), "sec-websocket-")
}

func directDiagnosticURL(rawURL string) string {
	trimmed := rawURL
	if index := strings.IndexAny(trimmed, "?#"); index >= 0 {
		trimmed = trimmed[:index]
	}
	return trimmed
}
