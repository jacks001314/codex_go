package auth

// Gateway credential manager.
//
// Rust parity: codex-rs/login/src/gateway_auth.rs (#46318). The manager owns
// gateway credentials, coordinates refresh and browser login through the shared
// OAuth operations, and keeps rotated credentials pending until persistence
// succeeds so a failed save cannot lose a newer external login.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"codex_go/keyring"
)

const (
	gatewayHTTPTimeout        = 20 * time.Second
	gatewayErrorBodyLimitByte = 8 * 1024
)

type gatewayRefreshPolicy struct {
	// rejectedAccessToken makes a stored token reusable only when it differs
	// from the token the failed request used.
	rejectedAccessToken string
	afterRejection      bool
}

func (p gatewayRefreshPolicy) canReuse(token *gatewayStoredToken) bool {
	if token == nil || !gatewayTokenIsUsable(*token) {
		return false
	}
	if !p.afterRejection {
		return true
	}
	return token.AccessToken != p.rejectedAccessToken
}

type gatewayAuthCache struct {
	token   *gatewayStoredToken
	pending *gatewayStoredToken
}

// gatewayAuthManager resolves and persists an OAuth access token for a model
// provider. Callers hold one manager per provider configuration.
type gatewayAuthManager struct {
	config    gatewayAuthConfig
	codexHome string
	storage   *gatewayAuthStorage
	client    *http.Client
	// openBrowser is called with the authorization URL; nil prints the URL and
	// opens the system browser (Rust's authorize path).
	openBrowser func(string) error
	mu          sync.Mutex
	cache       gatewayAuthCache
}

// newGatewayAuthManager builds a manager with a redirect-free HTTP client: token
// grants never follow redirects and never carry request logs.
func newGatewayAuthManager(config gatewayAuthConfig, codexHome string, client *http.Client, store keyring.Store) *gatewayAuthManager {
	if client == nil {
		client = &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &gatewayAuthManager{
		config:    config,
		codexHome: codexHome,
		storage:   newGatewayAuthStorage(codexHome, store),
		client:    client,
	}
}

// resolveAccessToken returns a cached access token or refreshes/authorizes when
// it is no longer usable.
func (m *gatewayAuthManager) resolveAccessToken(ctx context.Context) (string, error) {
	return m.resolve(ctx, gatewayRefreshPolicy{})
}

// refreshAccessToken recovers after a request rejected rejectedAccessToken,
// reusing a usable replacement from storage or refreshing/authorizing.
func (m *gatewayAuthManager) refreshAccessToken(ctx context.Context, rejectedAccessToken string) (string, error) {
	return m.resolve(ctx, gatewayRefreshPolicy{afterRejection: true, rejectedAccessToken: rejectedAccessToken})
}

func (m *gatewayAuthManager) resolve(ctx context.Context, policy gatewayRefreshPolicy) (string, error) {
	if m == nil {
		return "", errors.New("provider OAuth manager is unavailable")
	}
	if err := validateGatewayAuthConfig(m.config); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache.token == nil && m.cache.pending == nil {
		token, err := m.loadToken()
		if err != nil {
			return "", err
		}
		m.cache.token = token
	}
	if m.cache.pending == nil && !policy.afterRejection && policy.canReuse(m.cache.token) {
		return m.cache.token.AccessToken, nil
	}
	token, authorize, err := m.refresh(ctx, policy)
	if err != nil {
		return "", err
	}
	if !authorize {
		return token, nil
	}
	return m.authorize(ctx)
}

func (m *gatewayAuthManager) persistPending() (string, error) {
	token := m.cache.pending
	if token == nil {
		return "", errors.New("provider OAuth credentials are missing")
	}
	if err := m.saveToken(token); err != nil {
		return "", err
	}
	accessToken := token.AccessToken
	m.cache.token = token
	m.cache.pending = nil
	return accessToken, nil
}

// refresh returns either a usable access token or the instruction to authorize.
// It runs under the credential lock so concurrent configurations serialize.
func (m *gatewayAuthManager) refresh(ctx context.Context, policy gatewayRefreshPolicy) (string, bool, error) {
	lock, err := lockGatewayCredentials(m.codexHome)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = lock.release() }()
	// Recovery always rereads under the cross-process lock before choosing a
	// token; even a replacement from storage must differ from the token this
	// request already had rejected.
	for attempt := 0; attempt < 2; attempt++ {
		stored, err := m.loadToken()
		if err != nil {
			return "", false, err
		}
		if !gatewayTokensEqual(stored, m.cache.token) {
			m.cache.token = stored
			m.cache.pending = nil
		}
		if m.cache.pending != nil {
			accessToken, err := m.persistPending()
			if err != nil {
				return "", false, err
			}
			return accessToken, false, nil
		}
		if policy.canReuse(m.cache.token) {
			return m.cache.token.AccessToken, false, nil
		}
		refreshToken := ""
		if m.cache.token != nil {
			refreshToken = strings.TrimSpace(m.cache.token.RefreshToken)
		}
		if refreshToken == "" {
			break
		}
		var response gatewayTokenResponse
		oauthErr := m.oauth().refresh(ctx, refreshTokenGrant{
			refreshToken: refreshToken,
			resource:     m.config.resource,
		}, &response)
		if oauthErr == nil {
			stored, err := response.intoStored(refreshToken)
			if err != nil {
				return "", false, err
			}
			m.cache.pending = &stored
			accessToken, err := m.persistPending()
			if err != nil {
				return "", false, err
			}
			return accessToken, false, nil
		}
		var typed *oauthError
		_ = errors.As(oauthErr, &typed)
		rejection := typed.rejected()
		if rejection != nil && rejection.statusCode == http.StatusBadRequest && refreshGrantRejected(rejection.errorCode) {
			// Some public clients receive refresh tokens despite being unable to
			// use that grant: reauthorize after the explicit rejection, and pick
			// up updates from clients that predate the credential lock.
			stored, err := m.loadToken()
			if err != nil {
				return "", false, err
			}
			if gatewayTokensEqual(stored, m.cache.token) {
				break
			}
			continue
		}
		return "", false, gatewayEndpointError(typed, m.config, "refresh_token", "")
	}
	stored, err := m.loadToken()
	if err != nil {
		return "", false, err
	}
	if !gatewayTokensEqual(stored, m.cache.token) {
		m.cache.token = stored
		m.cache.pending = nil
		if policy.canReuse(m.cache.token) {
			return m.cache.token.AccessToken, false, nil
		}
	}
	return "", true, nil
}

func refreshGrantRejected(errorCode string) bool {
	switch errorCode {
	case "invalid_grant", "unauthorized_client", "unsupported_grant_type":
		return true
	default:
		return false
	}
}

func gatewayTokensEqual(left *gatewayStoredToken, right *gatewayStoredToken) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if left.AccessToken != right.AccessToken || left.RefreshToken != right.RefreshToken {
		return false
	}
	switch {
	case left.ExpiresAt == nil && right.ExpiresAt == nil:
		return true
	case left.ExpiresAt == nil || right.ExpiresAt == nil:
		return false
	default:
		return *left.ExpiresAt == *right.ExpiresAt
	}
}

func (m *gatewayAuthManager) loadToken() (*gatewayStoredToken, error) {
	value, ok, err := m.storage.load(gatewayCredentialID(m.codexHome, m.config))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var token gatewayStoredToken
	if err := json.Unmarshal([]byte(value), &token); err != nil {
		return nil, errors.New("stored provider OAuth credentials are invalid")
	}
	return &token, nil
}

func (m *gatewayAuthManager) saveToken(token *gatewayStoredToken) error {
	encoded, err := json.Marshal(token)
	if err != nil {
		return errors.New("failed to encode provider OAuth credentials")
	}
	return m.storage.save(gatewayCredentialID(m.codexHome, m.config), string(encoded))
}

func (m *gatewayAuthManager) oauth() *oauthClient {
	return newOAuthClient(m.client, tokenEndpoint{
		url:            m.config.tokenURL,
		clientID:       m.config.clientID,
		encoding:       tokenEncodingForm,
		timeout:        gatewayHTTPTimeout,
		errorBodyLimit: errorBodyLimit{bytes: gatewayErrorBodyLimitByte},
	})
}

// authorize runs the authorization-code grant with PKCE and the loopback
// callback listener.
func (m *gatewayAuthManager) authorize(ctx context.Context) (string, error) {
	pkce, err := generatePKCE()
	if err != nil {
		return "", err
	}
	state, err := generateOAuthState()
	if err != nil {
		return "", err
	}
	redirectPort := uint16(0)
	if m.config.redirectPortSet {
		redirectPort = m.config.redirectPort
	}
	listener, err := newGatewayCallbackListener(redirectPort, state)
	if err != nil {
		return "", err
	}
	redirectURI := listener.redirectURL()
	authorizationURL, err := buildAuthorizationURL(authorizationRequest{
		endpoint:    m.config.authorizationURL,
		clientID:    m.config.clientID,
		redirectURI: redirectURI,
		scope:       strings.Join(m.config.scopes, " "),
		resource:    m.config.resource,
		pkce:        pkce,
		state:       state,
	})
	if err != nil {
		listener.close()
		return "", errors.New("invalid provider OAuth authorization endpoint")
	}
	openBrowser := m.openBrowser
	if openBrowser == nil {
		openBrowser = defaultGatewayBrowserOpener
	}
	if err := openBrowser(authorizationURL); err != nil {
		fmt.Fprintln(os.Stderr, "Browser launch failed; open the URL above manually.")
	}
	code, err := listener.wait(ctx)
	if err != nil {
		return "", err
	}
	// Wait for user interaction without holding the store lock, then serialize
	// issuance and persistence with refreshes.
	lock, err := lockGatewayCredentials(m.codexHome)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.release() }()
	// The caller (resolve) holds the cache mutex for the whole operation, so the
	// authorize flow updates the cache without re-locking it.
	stored, err := m.loadToken()
	if err != nil {
		return "", err
	}
	m.cache.token = stored
	var response gatewayTokenResponse
	if oauthErr := m.oauth().exchangeCode(ctx, authorizationCodeGrant{
		code:        code,
		redirectURI: redirectURI,
		pkce:        pkce,
		resource:    m.config.resource,
	}, &response); oauthErr != nil {
		var typed *oauthError
		_ = errors.As(oauthErr, &typed)
		return "", gatewayEndpointError(typed, m.config, "authorization_code", redirectURI)
	}
	pending, err := response.intoStored("")
	if err != nil {
		return "", err
	}
	m.cache.pending = &pending
	return m.persistPending()
}

func defaultGatewayBrowserOpener(authorizationURL string) error {
	fmt.Fprintf(os.Stderr, "Authorize the model provider by opening this URL:\n%s\n\n", authorizationURL)
	return OpenBrowser(authorizationURL)
}
