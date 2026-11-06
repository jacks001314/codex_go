package auth

// Gateway device-code authorization (Rust #59822 login/src/oauth/device.rs):
// terminals that cannot open a browser present a verification URL and short
// code, then poll the token endpoint until the issuer approves the request.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const gatewayDeviceResponseLimitBytes = 64 * 1024

// GatewayDeviceCodePrompt is the URL and short code a user must approve.
type GatewayDeviceCodePrompt struct {
	VerificationURL         string
	VerificationURLComplete string
	UserCode                string
}

var (
	// ErrGatewayDeviceCodeDenied reports that the user rejected the request.
	ErrGatewayDeviceCodeDenied = errors.New("Device authorization was denied; start sign-in again when ready")
	// ErrGatewayDeviceCodeExpired reports that the short code ran out.
	ErrGatewayDeviceCodeExpired = errors.New("Device authorization expired; start sign-in again")
	// ErrGatewayDeviceVerificationURL reports an unsafe verification target.
	ErrGatewayDeviceVerificationURL = errors.New("The gateway returned an invalid device verification URL")
)

type gatewayDeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// LoginWithDeviceCode runs the caller-initiated device sign-in. onPrompt
// receives the verification URL and code; completion means the credential was
// saved and is visible to existing request consumers.
func (m *GatewayAuthManager) LoginWithDeviceCode(ctx context.Context, onPrompt func(GatewayDeviceCodePrompt)) error {
	if m == nil {
		return errors.New("provider OAuth manager is unavailable")
	}
	if err := validateGatewayAuthConfig(m.config); err != nil {
		return err
	}
	if strings.TrimSpace(m.config.DeviceAuthorizationURL) == "" {
		return errors.New("The provider does not configure device authorization")
	}
	if !m.beginLoginAttempt() {
		return ErrGatewayLoginInProgress
	}
	defer m.endLoginAttempt()
	if ctx == nil {
		ctx = context.Background()
	}
	m.publishStatus(GatewayAuthStatus{Kind: GatewayAuthStatusStarted}, "")
	m.mu.Lock()
	defer m.mu.Unlock()
	accessToken, err := m.authorizeDevice(ctx, onPrompt)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			err = ErrGatewayLoginCanceled
		}
		m.publishStatus(GatewayAuthStatus{Kind: GatewayAuthStatusFailed, Message: err.Error()}, "")
		return err
	}
	m.publishStatus(GatewayAuthStatus{Kind: GatewayAuthStatusSucceeded}, accessToken)
	return nil
}

// authorizeDevice requests a device code, hands the prompt to the caller, polls
// for approval and persists the resulting grant.
func (m *GatewayAuthManager) authorizeDevice(ctx context.Context, onPrompt func(GatewayDeviceCodePrompt)) (string, error) {
	authorization, err := m.requestDeviceAuthorization(ctx)
	if err != nil {
		return "", err
	}
	verification, err := validateGatewayDeviceVerificationURL(authorization.VerificationURI)
	if err != nil {
		return "", err
	}
	complete := ""
	if strings.TrimSpace(authorization.VerificationURIComplete) != "" {
		if candidate, completeErr := validateGatewayDeviceVerificationURL(authorization.VerificationURIComplete); completeErr == nil {
			complete = candidate
		}
	}
	if onPrompt != nil {
		onPrompt(GatewayDeviceCodePrompt{
			VerificationURL:         verification,
			VerificationURLComplete: complete,
			UserCode:                strings.TrimSpace(authorization.UserCode),
		})
	}
	response, err := m.pollDeviceToken(ctx, authorization)
	if err != nil {
		return "", err
	}
	lock, err := lockGatewayCredentials(m.codexHome)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.release() }()
	stored, err := m.loadToken()
	if err != nil {
		return "", err
	}
	m.cache.token = stored
	pending, err := response.intoStored(nil)
	if err != nil {
		return "", err
	}
	m.cache.pending = &pending
	return m.persistPending()
}

// requestDeviceAuthorization posts the device authorization request.
func (m *GatewayAuthManager) requestDeviceAuthorization(ctx context.Context) (gatewayDeviceAuthorization, error) {
	endpoint := strings.TrimSpace(m.config.DeviceAuthorizationURL)
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return gatewayDeviceAuthorization{}, errors.New("Invalid device authorization URL")
	}
	form := url.Values{}
	form.Set("client_id", m.config.ClientID)
	if scope := strings.Join(m.config.Scopes, " "); strings.TrimSpace(scope) != "" {
		form.Set("scope", scope)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return gatewayDeviceAuthorization{}, errors.New("Device authorization request failed")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return gatewayDeviceAuthorization{}, errors.New("Device authorization request failed")
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, gatewayDeviceResponseLimitBytes))
	if err != nil {
		return gatewayDeviceAuthorization{}, errors.New("Device authorization request failed")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return gatewayDeviceAuthorization{}, errors.New("Device authorization request failed")
	}
	var authorization gatewayDeviceAuthorization
	if err := json.Unmarshal(body, &authorization); err != nil {
		return gatewayDeviceAuthorization{}, errors.New("Device authorization request failed")
	}
	if strings.TrimSpace(authorization.DeviceCode) == "" || strings.TrimSpace(authorization.UserCode) == "" || strings.TrimSpace(authorization.VerificationURI) == "" {
		return gatewayDeviceAuthorization{}, errors.New("Device authorization request failed")
	}
	return authorization, nil
}

// pollDeviceToken polls the token endpoint until approval, denial or expiry.
func (m *GatewayAuthManager) pollDeviceToken(ctx context.Context, authorization gatewayDeviceAuthorization) (gatewayTokenResponse, error) {
	interval := time.Duration(authorization.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(authorization.ExpiresIn) * time.Second)
	if authorization.ExpiresIn <= 0 {
		deadline = time.Now().Add(10 * time.Minute)
	}
	for {
		if time.Now().After(deadline) {
			return gatewayTokenResponse{}, ErrGatewayDeviceCodeExpired
		}
		var response gatewayTokenResponse
		err := m.oauth().exchange(ctx, [][2]string{
			{"grant_type", "urn:ietf:params:oauth:grant-type:device_code"},
			{"client_id", m.config.ClientID},
			{"device_code", authorization.DeviceCode},
		}, []string{authorization.DeviceCode}, &response)
		if err == nil {
			return response, nil
		}
		if ctx.Err() != nil {
			return gatewayTokenResponse{}, ctx.Err()
		}
		switch gatewayDeviceErrorCode(err) {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return gatewayTokenResponse{}, ErrGatewayDeviceCodeDenied
		case "expired_token":
			return gatewayTokenResponse{}, ErrGatewayDeviceCodeExpired
		default:
			return gatewayTokenResponse{}, errors.New("Device authorization failed")
		}
		select {
		case <-ctx.Done():
			return gatewayTokenResponse{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// gatewayDeviceErrorCode returns the issuer's error code without exposing the
// rest of the response.
func gatewayDeviceErrorCode(err error) string {
	var typed *oauthError
	if !errors.As(err, &typed) || typed.rejection == nil {
		return ""
	}
	return strings.TrimSpace(typed.rejection.errorCode)
}

// validateGatewayDeviceVerificationURL accepts https targets and http targets
// on the loopback interface only.
func validateGatewayDeviceVerificationURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if _, err := validateOAuthEndpoint(trimmed, "device verification URL"); err != nil {
		return "", ErrGatewayDeviceVerificationURL
	}
	return trimmed, nil
}
