package execserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	RemoteSecurityProfile                 = "noise_hybrid_ik_v1"
	CodexExecServerExitOnStdinCloseEnvVar = "CODEX_EXEC_SERVER_EXIT_ON_STDIN_CLOSE"

	noiseChannelSuite         = "Noise_hybridIK_X25519+MLKEM768_AESGCM_SHA256"
	mlkem768PublicKeyBytes    = 1184
	errorBodyPreviewMaxBytes  = 4096
	defaultRemoteBackoff      = time.Second
	defaultRemoteMaxBackoff   = 30 * time.Second
	defaultRemoteHTTPTimeout  = 30 * time.Second
	defaultRemoteDialTimeout  = 10 * time.Second
	defaultRemoteCloseTimeout = time.Second
	registryRecoveryInitialMS = 500
	registryRecoveryMax       = 5 * time.Second
)

type RemoteEnvironmentConfig struct {
	BaseURL       string
	EnvironmentID string
	Name          string
	AuthHeaders   http.Header
	// ResolveAuthHeaders resolves fresh auth headers for each environment
	// registry request (Rust AuthProvider::resolve_auth_headers, #38610).
	// Managed credentials (workload identity) are refreshed asynchronously
	// before the request is sent. When nil, the static AuthHeaders are used.
	ResolveAuthHeaders func(ctx context.Context) (http.Header, error)
	HTTPClient         *http.Client
	Dial               func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
	Backoff            time.Duration
	MaxBackoff         time.Duration
}

type RemotePublicKey struct {
	Suite             string `json:"suite"`
	X25519PublicKey   string `json:"x25519_public_key"`
	MLKEM768PublicKey string `json:"mlkem768_public_key"`
}

type remoteRegistrationRequest struct {
	SecurityProfile   string          `json:"security_profile"`
	ExecutorPublicKey RemotePublicKey `json:"executor_public_key"`
}

type remoteRegistrationResponse struct {
	EnvironmentID          string `json:"environment_id"`
	URL                    string `json:"url"`
	SecurityProfile        string `json:"security_profile"`
	ExecutorRegistrationID string `json:"executor_registration_id"`
}

type registryErrorBody struct {
	Error *registryError `json:"error"`
}

type registryError struct {
	Code    *string `json:"code"`
	Message *string `json:"message"`
}

func RunRemoteEnvironment(ctx context.Context, cfg RemoteEnvironmentConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return errors.New("environment registry base URL is required")
	}
	cfg.EnvironmentID = strings.TrimSpace(cfg.EnvironmentID)
	if cfg.EnvironmentID == "" {
		return errors.New("environment id is required for remote exec-server registration")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = remoteHTTPClient()
	}
	if cfg.Dial == nil {
		cfg.Dial = func(ctx context.Context, url string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
			if options == nil {
				options = &websocket.DialOptions{}
			} else {
				cloned := *options
				options = &cloned
			}
			options.HTTPClient = cfg.HTTPClient
			return websocket.Dial(ctx, url, options)
		}
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = defaultRemoteBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = defaultRemoteMaxBackoff
	}

	identity, err := generateRemoteNoiseIdentity()
	if err != nil {
		return fmt.Errorf("failed to generate Noise relay identity: %w", err)
	}
	defer identity.Destroy()

	server := NewServerWithHTTPClient(cfg.HTTPClient)
	defer server.shutdownSessions()
	backoff := cfg.Backoff
	registration, err := registerRemoteEnvironmentWithRetry(ctx, cfg, identity.PublicKey())
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		conn, response, err := cfg.Dial(ctx, registration.URL, &websocket.DialOptions{})
		if err == nil {
			backoff = cfg.Backoff
			serveErr := server.serveNoiseRelayConnection(ctx, conn, cfg, registration, identity)
			if ctx.Err() != nil {
				return nil
			}
			if serveErr != nil {
				err = serveErr
			}
		}
		if response != nil && response.StatusCode >= 400 && response.StatusCode < 500 {
			registration, err = registerRemoteEnvironmentWithRetry(ctx, cfg, identity.PublicKey())
			if err != nil {
				return err
			}
		}
		if err := sleepContext(ctx, backoff); err != nil {
			return nil
		}
		backoff *= 2
		if backoff > cfg.MaxBackoff {
			backoff = cfg.MaxBackoff
		}
	}
}

func registerRemoteEnvironment(ctx context.Context, cfg RemoteEnvironmentConfig, key RemotePublicKey) (*remoteRegistrationResponse, error) {
	body, err := json.Marshal(remoteRegistrationRequest{
		SecurityProfile:   RemoteSecurityProfile,
		ExecutorPublicKey: key,
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, remoteEndpointURL(cfg.BaseURL, "/cloud/environment/"+cfg.EnvironmentID+"/register"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("environment registry request failed: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	headers, err := resolveRemoteEnvironmentAuthHeaders(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("environment registry auth resolution failed: %w", err)
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := cfg.HTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("environment registry request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, remoteRegistryStatusError(response)
	}
	var decoded remoteRegistrationResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("failed to decode environment registry response: %w", err)
	}
	if decoded.EnvironmentID != cfg.EnvironmentID {
		return nil, errors.New("exec-server protocol error: environment registry returned a different environment id")
	}
	if decoded.SecurityProfile != RemoteSecurityProfile {
		return nil, fmt.Errorf("exec-server protocol error: environment registry returned unsupported security profile `%s`", decoded.SecurityProfile)
	}
	return &decoded, nil
}

// registerRemoteEnvironmentWithRetry mirrors Rust #41219
// (EnvironmentRegistryClient::register_environment_with_retry). Retrying
// ambiguous failures is unsafe because a timed-out request may still have
// replaced a newer registration, so only explicit `503 registration_conflict`
// responses are retried with jittered exponential backoff. The enclosing remote
// transport future owns cancellation; retries spawn no background work.
func registerRemoteEnvironmentWithRetry(ctx context.Context, cfg RemoteEnvironmentConfig, key RemotePublicKey) (*remoteRegistrationResponse, error) {
	// Competing executors for the same environment must not retry in lockstep.
	retryKey := uuid.NewString()
	var attempt uint32
	for {
		registration, err := registerRemoteEnvironment(ctx, cfg, key)
		if err == nil {
			return registration, nil
		}
		var httpErr *remoteRegistryHTTPError
		if !errors.As(err, &httpErr) ||
			httpErr.StatusCode != http.StatusServiceUnavailable ||
			httpErr.Code == nil || *httpErr.Code != "registration_conflict" {
			return nil, err
		}
		attempt++
		delay := registrationConflictRetryDelay(retryKey, attempt-1)
		if sleepErr := sleepContext(ctx, delay); sleepErr != nil {
			return nil, sleepErr
		}
	}
}

func resolveRemoteEnvironmentAuthHeaders(ctx context.Context, cfg RemoteEnvironmentConfig) (http.Header, error) {
	if cfg.ResolveAuthHeaders != nil {
		headers, err := cfg.ResolveAuthHeaders(ctx)
		if err != nil {
			return nil, err
		}
		if headers == nil {
			return http.Header{}, nil
		}
		return headers, nil
	}
	return cfg.AuthHeaders, nil
}

func remoteRegistryStatusError(response *http.Response) error {
	status := response.Status
	bodyBytes, _ := io.ReadAll(io.LimitReader(response.Body, errorBodyPreviewMaxBytes+1))
	body := string(bodyBytes)
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("environment registry authentication error: environment registry authentication failed (%s): %s", status, registryErrorMessage(body))
	}
	code, message := registryHTTPErrorMessage(body)
	codeSuffix := ""
	if code != nil {
		codeSuffix = ", " + *code
	}
	return &remoteRegistryHTTPError{
		StatusCode: response.StatusCode,
		Code:       code,
		message:    fmt.Sprintf("environment registry request failed (%s%s): %s", status, codeSuffix, message),
	}
}

// remoteRegistryHTTPError carries the HTTP status and registry error code
// so the initial-connection recovery can classify transient failures (Rust
// EnvironmentRegistryHttp, #39777).
type remoteRegistryHTTPError struct {
	StatusCode int
	Code       *string
	message    string
}

func (e *remoteRegistryHTTPError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func registryHTTPErrorMessage(body string) (*string, string) {
	var decoded registryErrorBody
	if err := json.Unmarshal([]byte(body), &decoded); err == nil && decoded.Error != nil {
		message := ""
		if decoded.Error.Message != nil {
			message = *decoded.Error.Message
		} else {
			message = previewErrorBody(body)
			if message == "" {
				message = "empty error body"
			}
		}
		return decoded.Error.Code, message
	}
	message := previewErrorBody(body)
	if message == "" {
		message = "empty or malformed error body"
	}
	return nil, message
}

func registryErrorMessage(body string) string {
	var decoded registryErrorBody
	if err := json.Unmarshal([]byte(body), &decoded); err == nil && decoded.Error != nil && decoded.Error.Message != nil {
		return *decoded.Error.Message
	}
	if preview := previewErrorBody(body); preview != "" {
		return preview
	}
	return "empty error body"
}

// registrationConflictRetryDelay mirrors Rust
// client_recovery::registry_recovery_retry_delay (#41219): exponential backoff
// capped at registryRecoveryMax, plus a deterministic jitter up to half the base
// delay, so competing executors for the same environment do not retry in
// lockstep. retry_key and attempt seed the jitter.
func registrationConflictRetryDelay(retryKey string, attempt uint32) time.Duration {
	shift := attempt
	if shift > 4 {
		shift = 4
	}
	multiplier := uint32(1) << shift
	base := time.Duration(registryRecoveryInitialMS) * time.Millisecond * time.Duration(multiplier)
	if base > registryRecoveryMax {
		base = registryRecoveryMax
	}
	baseMillis := int64(base / time.Millisecond)
	if baseMillis <= 0 {
		baseMillis = 1
	}
	jitter := stableRegistryHash(retryKey, attempt) % uint64(baseMillis/2+1)
	return time.Duration(baseMillis)*time.Millisecond + time.Duration(jitter)*time.Millisecond
}

// stableRegistryHash is a small FNV-1a style hash used to seed the retry jitter.
func stableRegistryHash(parts ...any) uint64 {
	var h uint64 = 1469598103934665603
	for part := range parts {
		s := fmt.Sprint(parts[part])
		for i := 0; i < len(s); i++ {
			h ^= uint64(s[i])
			h *= 1099511628211
		}
	}
	return h
}

func previewErrorBody(body string) string {
	trimmed := strings.TrimSpace(body)
	runes := []rune(trimmed)
	if len(runes) > errorBodyPreviewMaxBytes {
		trimmed = string(runes[:errorBodyPreviewMaxBytes])
	}
	return trimmed
}

func remoteEndpointURL(baseURL string, path string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func generateRemotePublicKey() (RemotePublicKey, error) {
	identity, err := generateRemoteNoiseIdentity()
	if err != nil {
		return RemotePublicKey{}, err
	}
	defer identity.Destroy()
	return identity.PublicKey(), nil
}

func remoteHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultRemoteHTTPTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
