package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	managedconfig "codex_go/config"
)

// EMAAuthScope identifies the Codex account owner of an enterprise grant.
type EMAAuthScope struct {
	UserID      string
	WorkspaceID string
}

// EMAAuthCredentialName derives the keyring entry name for one account's
// enterprise IdP grant (Rust McpServerIdpOAuthConfig::credential_name).
func EMAAuthCredentialName(idp managedconfig.MCPServerIdPOAuthConfig, scope EMAAuthScope) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("codex-ema-idp-credential-v1"))
	_, _ = digest.Write([]byte{0})
	for _, part := range []string{scope.UserID, scope.WorkspaceID, idp.Issuer, idp.ClientID} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(part))
	}
	return "ema-idp:" + base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
}

// EMAAuthOptions configures one server's enterprise authorization lifetime.
type EMAAuthOptions struct {
	CodexHome string
	StoreMode OAuthCredentialsStoreMode
	Profile   managedconfig.MCPEnterpriseManagedAuthConfig
	Scope     EMAAuthScope
	// CurrentScope rechecks the account owner at every HTTP dispatch. A missing
	// callback keeps the captured scope for the manager's lifetime.
	CurrentScope func() (EMAAuthScope, bool)
	// CurrentLoginGeneration reports the account login revision. A newer login
	// revokes this manager even when the account owner is unchanged, including
	// the loss of login metadata (Rust #55546 login_generation).
	CurrentLoginGeneration func() uint64
	HTTPClient             *http.Client
}

// EMAAuthManager exchanges the account-scoped enterprise IdP grant for a
// resource-bound MCP bearer and applies it to each HTTP request.
type EMAAuthManager struct {
	serverName                string
	serverURL                 string
	resource                  string
	clientID                  string
	clientSecret              string
	tokenEndpointAuthMethod   managedconfig.MCPEMATokenEndpointAuthMethod
	authorizationServerIssuer string
	scopes                    []string
	profile                   managedconfig.MCPEnterpriseManagedAuthConfig
	scope                     EMAAuthScope
	currentScope              func() (EMAAuthScope, bool)
	loginGeneration           uint64
	currentLoginGeneration    func() uint64
	codexHome                 string
	storeMode                 OAuthCredentialsStoreMode
	httpClient                *http.Client
	mu                        sync.Mutex
	token                     string
	tokenExpiresAt            time.Time
}

func NewEMAAuthManager(serverName string, config *ServerConfig, options EMAAuthOptions) (*EMAAuthManager, error) {
	if config == nil {
		return nil, errors.New("enterprise MCP server is unavailable")
	}
	if err := config.ValidateEMAAuthTransport(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(options.Profile.IDP.Issuer) == "" || strings.TrimSpace(options.Profile.IDP.ClientID) == "" {
		return nil, errors.New("enterprise MCP registration is missing")
	}
	if strings.TrimSpace(options.Scope.UserID) == "" || strings.TrimSpace(options.Scope.WorkspaceID) == "" {
		return nil, errors.New("enterprise MCP requires an account owner")
	}
	manager := &EMAAuthManager{
		serverName:                strings.TrimSpace(serverName),
		serverURL:                 strings.TrimSpace(config.URL),
		resource:                  strings.TrimSpace(config.OAuthResource),
		clientID:                  strings.TrimSpace(config.OAuthClientID),
		clientSecret:              config.OAuthClientSecret,
		tokenEndpointAuthMethod:   managedconfig.MCPEMATokenEndpointAuthMethod(strings.TrimSpace(config.OAuthTokenEndpointAuthMethod)),
		authorizationServerIssuer: strings.TrimSpace(config.OAuthAuthorizationServerIssuer),
		scopes:                    append([]string(nil), config.Scopes...),
		profile:                   options.Profile,
		scope:                     options.Scope,
		currentScope:              options.CurrentScope,
		currentLoginGeneration:    options.CurrentLoginGeneration,
		codexHome:                 strings.TrimSpace(options.CodexHome),
		storeMode:                 options.StoreMode,
		httpClient:                options.HTTPClient,
	}
	if manager.currentLoginGeneration != nil {
		manager.loginGeneration = manager.currentLoginGeneration()
	}
	return manager, nil
}

// CredentialsAvailable reports whether the account has a stored enterprise IdP
// grant without performing a network exchange or waiting for token refresh.
// Rust #56127 inspects an owner-checked grant instead of leasing it, because a
// status check must not block on refresh coordination; the scope check keeps a
// stale manager from reporting another account's grant.
func (m *EMAAuthManager) CredentialsAvailable() bool {
	if m == nil || m.codexHome == "" || !m.scopeIsCurrent() {
		return false
	}
	tokens, err := NewOAuthStoreWithMode(m.codexHome, m.storeMode).Load(EMAAuthCredentialName(m.profile.IDP, m.scope), m.profile.IDP.Issuer)
	return err == nil && tokens != nil && strings.TrimSpace(tokens.RefreshToken) != ""
}

// emaCredentialKey identifies one account-scoped enterprise IdP grant lookup.
type emaCredentialKey struct {
	storeMode      OAuthCredentialsStoreMode
	credentialName string
	issuer         string
}

// emaCredentialStatus shares one lazy credential read across every admitted EMA
// server (Rust #56127). Success, a missing grant and a read error are all
// reported identically for servers that resolve the same account grant, so a
// status pass performs the storage work once instead of once per server.
type emaCredentialStatus struct {
	mu     sync.Mutex
	values map[emaCredentialKey]bool
}

func (s *emaCredentialStatus) available(key emaCredentialKey, load func() bool) bool {
	if s == nil || load == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[emaCredentialKey]bool{}
	}
	if value, ok := s.values[key]; ok {
		return value
	}
	value := load()
	s.values[key] = value
	return value
}

func (m *EMAAuthManager) ApplyHTTPRequest(request *http.Request, _ []byte) error {
	if m == nil || request == nil {
		return errors.New("enterprise MCP authorization is unavailable")
	}
	if !m.scopeIsCurrent() {
		return errors.New("enterprise MCP authorization was revoked")
	}
	token, err := m.accessToken(request.Context())
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (m *EMAAuthManager) scopeIsCurrent() bool {
	if m == nil {
		return false
	}
	if m.currentLoginGeneration != nil && m.currentLoginGeneration() != m.loginGeneration {
		return false
	}
	if m.currentScope == nil {
		return true
	}
	current, ok := m.currentScope()
	if !ok {
		return false
	}
	return strings.TrimSpace(current.UserID) == strings.TrimSpace(m.scope.UserID) &&
		strings.TrimSpace(current.WorkspaceID) == strings.TrimSpace(m.scope.WorkspaceID)
}

func (m *EMAAuthManager) accessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" && (m.tokenExpiresAt.IsZero() || time.Now().Before(m.tokenExpiresAt.Add(-30*time.Second))) {
		return m.token, nil
	}
	if !m.scopeIsCurrent() {
		return "", errors.New("enterprise MCP authorization was revoked")
	}
	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	credentialName := EMAAuthCredentialName(m.profile.IDP, m.scope)
	tokens, err := NewOAuthStoreWithMode(m.codexHome, m.storeMode).Load(credentialName, m.profile.IDP.Issuer)
	if err != nil || tokens == nil || strings.TrimSpace(tokens.RefreshToken) == "" {
		return "", errors.New("enterprise sign-in is required")
	}
	// Rust ema_identity: a stored enterprise grant must belong to the
	// configured issuer and IdP client. The credential name already derives
	// from both; this guard rejects a tampered or legacy record before any
	// credential reaches the token endpoints.
	if storedClientID := strings.TrimSpace(tokens.ClientID); storedClientID != strings.TrimSpace(m.profile.IDP.ClientID) {
		return "", errors.New("stored enterprise IdP credentials do not match the configured issuer and client")
	}
	discovery, err := DiscoverStreamableHTTPOAuth(ctx, m.serverURL, client)
	if err != nil || discovery == nil {
		return "", errors.New("enterprise MCP authorization discovery failed")
	}
	if m.authorizationServerIssuer != "" && !enterpriseIssuerIdentifiersMatch(discovery.Issuer, m.authorizationServerIssuer) {
		return "", errors.New("MCP authorization server issuer does not match configuration")
	}
	resource := m.resource
	if resource == "" {
		resource = strings.TrimSpace(discovery.Resource)
	}
	if err := ValidateMCPEMAAuthResource(m.serverURL, &resource); err != nil {
		return "", err
	}
	resourceAuth, err := resolveMCPEMAClientAuth(discovery.TokenEndpointAuthMethodsSupported, m.clientID, m.clientSecret, m.tokenEndpointAuthMethod, "MCP authorization server")
	if err != nil {
		return "", err
	}
	idp, err := resolveEnterpriseAuthorizationMetadata(ctx, m.profile.IDP.Issuer, m.profile.IDP.ClientID, m.profile.IDP.ClientSecret, m.profile.IDP.TokenEndpointAuthMethod, client)
	if err != nil {
		return "", err
	}
	scopes := append([]string(nil), m.scopes...)
	token, err := ExchangeMCPEMAIDJAG(ctx, MCPEMAIDJAGExchangeRequest{
		Resource:                         resource,
		Scopes:                           scopes,
		MCPClientID:                      m.clientID,
		AuthorizationServerIssuer:        discovery.Issuer,
		AuthorizationServerTokenEndpoint: discovery.TokenEndpoint,
		IDPTokenEndpoint:                 idp.TokenEndpoint,
		IDPIssuer:                        m.profile.IDP.Issuer,
		IDPClientID:                      m.profile.IDP.ClientID,
		IDPClientAuthentication:          idp.ClientAuthentication,
		ResourceClientAuthentication:     resourceAuth,
		RefreshToken:                     tokens.RefreshToken,
		IDPHTTPClient:                    client,
		ResourceHTTPClient:               client,
	})
	if err != nil {
		return "", err
	}
	m.token = token.AccessToken
	m.tokenExpiresAt = time.Time{}
	if token.ExpiresIn != nil && *token.ExpiresIn > 0 {
		m.tokenExpiresAt = time.Now().Add(*token.ExpiresIn)
	}
	if !m.scopeIsCurrent() {
		return "", errors.New("enterprise MCP authorization was revoked")
	}
	return m.token, nil
}

// AttachEnterpriseMCPAuthorization configures every admitted EMA server with
// its per-request bearer exchange and credential status.
func AttachEnterpriseMCPAuthorization(runtime *RuntimeConfig, codexHome string, auth *RuntimeAuth, currentScope func() (EMAAuthScope, bool), currentLoginGeneration func() uint64) {
	if runtime == nil || !runtime.XAAEnabled || runtime.EnterpriseManagedAuth == nil {
		return
	}
	grantStatus := &emaCredentialStatus{}
	for name, registration := range runtime.Servers {
		config := registration.Config
		if config.EffectiveAuth() != ServerAuthEMAAuth || !config.Enabled {
			continue
		}
		scope := EMAAuthScope{}
		if auth != nil {
			scope = EMAAuthScope{UserID: auth.ChatGPTUserID, WorkspaceID: auth.AccountID}
		}
		manager, err := NewEMAAuthManager(name, &config, EMAAuthOptions{
			CodexHome:              codexHome,
			StoreMode:              config.OAuthCredentialsStoreMode,
			Profile:                *runtime.EnterpriseManagedAuth,
			Scope:                  scope,
			CurrentScope:           currentScope,
			CurrentLoginGeneration: currentLoginGeneration,
			HTTPClient:             httpClientForEMAAuth(runtime),
		})
		if err != nil {
			// Admission stays intact: the server is configured, but its first
			// request fails closed until an account owner and grant exist.
			config.EMAConfigured = true
			config.EMAAuthExtension = true
			config.EMACredentialsAvailable = false
			connectionErr := err
			config.ApplyHTTPRequest = func(*http.Request, []byte) error { return connectionErr }
			registration.Config = config
			runtime.Servers[name] = registration
			continue
		}
		config.EMAConfigured = true
		config.EMAAuthExtension = true
		config.EMACredentialsAvailable = grantStatus.available(emaCredentialKey{
			storeMode:      manager.storeMode,
			credentialName: EMAAuthCredentialName(manager.profile.IDP, manager.scope),
			issuer:         manager.profile.IDP.Issuer,
		}, manager.CredentialsAvailable)
		config.ApplyHTTPRequest = manager.ApplyHTTPRequest
		if runtime.HTTPClient != nil {
			config.HTTPClient = emaScopeCheckingDoer{next: runtime.HTTPClient, manager: manager}
		}
		modern := MCPProtocol20260728
		config.ProtocolModeOverride = &modern
		registration.Config = config
		runtime.Servers[name] = registration
	}
}

// emaScopeCheckingDoer rechecks the account owner at the HTTP client boundary,
// so a stale manager cannot reuse a revoked enterprise grant after a config
// reload or account switch (Rust #55287/#55288).
type emaScopeCheckingDoer struct {
	next    HTTPDoer
	manager *EMAAuthManager
}

func (d emaScopeCheckingDoer) Do(request *http.Request) (*http.Response, error) {
	if d.manager != nil && !d.manager.scopeIsCurrent() {
		return nil, errors.New("enterprise MCP authorization was revoked")
	}
	return d.next.Do(request)
}

func httpClientForEMAAuth(runtime *RuntimeConfig) *http.Client {
	if runtime == nil || runtime.HTTPClient == nil {
		return nil
	}
	if client, ok := runtime.HTTPClient.(*http.Client); ok {
		return client
	}
	return &http.Client{Transport: httpDoerTransport{doer: runtime.HTTPClient}}
}

type httpDoerTransport struct {
	doer HTTPDoer
}

func (t httpDoerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.doer == nil {
		return http.DefaultTransport.RoundTrip(request)
	}
	return t.doer.Do(request)
}

func (m *EMAAuthManager) String() string {
	if m == nil {
		return "EMAAuthManager<nil>"
	}
	return fmt.Sprintf("EMAAuthManager{server:%s}", m.serverName)
}
