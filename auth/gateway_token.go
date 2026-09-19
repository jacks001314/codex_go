package auth

// Gateway token lifetimes, endpoint diagnostics, and configuration validation.
//
// Rust parity: codex-rs/login/src/gateway_auth_token.rs plus the config
// validation in gateway_auth.rs.

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
	"time"
)

const gatewayRefreshSkewSeconds int64 = 30

// GatewayAuthConfig is the public-client OAuth configuration of a model
// provider gateway (Rust GatewayAuthConfig).
type GatewayAuthConfig struct {
	AuthorizationURL string
	TokenURL         string
	ClientID         string
	Resource         string
	Scopes           []string
	RedirectPort     *uint16
}

// gatewayStoredToken is the persisted credential shape. A missing expiry means
// the issuer did not report one.
type gatewayStoredToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    *int64 `json:"expires_at,omitempty"`
}

// gatewayTokenResponse is the standard token-endpoint response.
type gatewayTokenResponse struct {
	AccessToken  string  `json:"access_token"`
	TokenType    string  `json:"token_type"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    *uint64 `json:"expires_in"`
}

// intoStored validates one token response and rotates the refresh token, keeping
// the previous one when the issuer does not return a new value.
func (r gatewayTokenResponse) intoStored(previousRefreshToken string) (gatewayStoredToken, error) {
	if strings.TrimSpace(r.AccessToken) == "" {
		return gatewayStoredToken{}, errors.New("provider OAuth token response omitted its access token")
	}
	if r.TokenType != "" && !strings.EqualFold(r.TokenType, "bearer") {
		return gatewayStoredToken{}, errors.New("provider OAuth token response returned an unsupported token type")
	}
	if r.ExpiresIn != nil && *r.ExpiresIn == 0 {
		return gatewayStoredToken{}, errors.New("provider OAuth token response returned a zero lifetime")
	}
	var expiresAt *int64
	if r.ExpiresIn != nil {
		if *r.ExpiresIn > math.MaxInt64 {
			return gatewayStoredToken{}, errors.New("provider OAuth token lifetime is too large")
		}
		lifetime := int64(*r.ExpiresIn)
		now := time.Now().Unix()
		if lifetime > math.MaxInt64-now || now+lifetime < now {
			return gatewayStoredToken{}, errors.New("provider OAuth token expiry overflows")
		}
		value := now + lifetime
		expiresAt = &value
	}
	refreshToken := r.RefreshToken
	if refreshToken == "" {
		refreshToken = previousRefreshToken
	}
	return gatewayStoredToken{AccessToken: r.AccessToken, RefreshToken: refreshToken, ExpiresAt: expiresAt}, nil
}

// gatewayTokenIsUsable mirrors Rust `token_is_usable`: a token without an expiry
// is usable, and one expiring inside the refresh skew is not.
func gatewayTokenIsUsable(token gatewayStoredToken) bool {
	if token.ExpiresAt == nil {
		return true
	}
	return *token.ExpiresAt > time.Now().Unix()+gatewayRefreshSkewSeconds
}

// gatewayEndpointError renders Rust `token::endpoint_error`: a bounded,
// redacted diagnostic that names the request parameters without exposing
// credentials. Query-bearing issuer URLs omit the response detail entirely
// because issuers can echo custom URL credentials the shared redaction does not
// know.
func gatewayEndpointError(oauthErr *oauthError, config GatewayAuthConfig, grantType string, redirectURI string) error {
	endpoint := diagnosticURL(config.TokenURL)
	if oauthErr == nil {
		return fmt.Errorf("provider OAuth token exchange failed for %s", endpoint)
	}
	rejection := oauthErr.rejected()
	if rejection == nil {
		if oauthErr.kind == oauthErrorTransport {
			return fmt.Errorf("provider OAuth token exchange failed for %s", endpoint)
		}
		return errors.New("provider OAuth token response is invalid")
	}
	hasQuery := strings.Contains(config.TokenURL, "?") || strings.Contains(config.Resource, "?")
	detail := "provider response details omitted"
	requestID := ""
	if !hasQuery {
		detail = truncateRunes(redactRequestSecrets(rejection.displayMessage, nil), 512)
		if rejection.requestID != "" {
			requestID = " (request id: " + rejection.requestID + ")"
		}
	}
	resource := "<not sent>"
	if strings.TrimSpace(config.Resource) != "" {
		resource = diagnosticURL(config.Resource)
	}
	if strings.TrimSpace(redirectURI) == "" {
		redirectURI = "<not sent>"
	}
	scopes := "<not sent>"
	if len(config.Scopes) > 0 {
		scopes = strings.Join(config.Scopes, " ")
	}
	pkce := "not applicable"
	if grantType == "authorization_code" {
		pkce = "S256"
	}
	return fmt.Errorf(
		"provider OAuth token endpoint %s returned HTTP %d - %s%s. Request: grant_type=%s, client_id=%s, client_auth=none (public client), pkce=%s, redirect_uri=%s, resource=%s, authorization_scopes=%s",
		endpoint, rejection.statusCode, detail, requestID, grantType, config.ClientID, pkce, redirectURI, resource, scopes,
	)
}

// diagnosticURL mirrors Rust's `diagnostic_url`: sanitized, with the query
// removed so issuer-specific credential keys cannot leak.
func diagnosticURL(value string) string {
	sanitized := sanitizeURLForLogging(value)
	if index := strings.Index(sanitized, "?"); index >= 0 {
		sanitized = sanitized[:index]
	}
	if strings.TrimSpace(sanitized) == "" {
		return "<invalid-url>"
	}
	return sanitized
}

// validateGatewayAuthConfig mirrors Rust `validate_config`: both endpoints must
// be HTTPS (or loopback HTTP) without embedded credentials or fragments, the
// authorization endpoint must not carry OAuth request parameters, and the
// client id and redirect port must be usable.
func validateGatewayAuthConfig(config GatewayAuthConfig) error {
	authorization, err := validateOAuthEndpoint(config.AuthorizationURL, "provider OAuth authorization endpoint")
	if err != nil {
		return err
	}
	for name := range authorization.Query() {
		switch name {
		case "response_type", "client_id", "redirect_uri", "state", "scope", "resource", "code_challenge", "code_challenge_method":
			return errors.New("provider OAuth authorization endpoint cannot include OAuth request parameters")
		}
	}
	if _, err := validateOAuthEndpoint(config.TokenURL, "provider OAuth token endpoint"); err != nil {
		return err
	}
	if strings.TrimSpace(config.ClientID) == "" {
		return errors.New("provider OAuth client ID must not be empty")
	}
	if config.RedirectPort != nil && *config.RedirectPort == 0 {
		return errors.New("provider OAuth redirect port must not be zero")
	}
	return nil
}

func validateOAuthEndpoint(value string, description string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" {
		return nil, fmt.Errorf("invalid %s", description)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("%s cannot include embedded credentials or fragments", description)
	}
	isLoopback := false
	if host := parsed.Hostname(); host != "" {
		if strings.EqualFold(host, "localhost") {
			isLoopback = true
		} else if address := net.ParseIP(host); address != nil && address.IsLoopback() {
			isLoopback = true
		}
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback) {
		return nil, fmt.Errorf("%s must use HTTPS unless it is loopback", description)
	}
	return parsed, nil
}
