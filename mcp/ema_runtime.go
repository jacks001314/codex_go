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
	"sync/atomic"
	"time"

	"codex_go/auth"
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
	// AccountLoginCurrent rechecks the cached and persisted account login the
	// manager was built with. A persisted replacement can leave the auth cache
	// holding the previous login even for the same account (Rust #55680).
	AccountLoginCurrent func() (bool, error)
	// PolicyBinding is the enterprise policy revision this manager was built
	// from. A suspended or stale binding holds new requests before dispatch
	// while already admitted responses finish (Rust #55691).
	PolicyBinding *managedconfig.CloudConfigBundleBinding
	// AccountGrantOptions makes the account's Codex OAuth grant available as an
	// enterprise credential source when no enterprise sign-in is stored (Rust
	// #55698). The manager fills CodexHome and LoginGeneration from its own
	// configuration when they are unset.
	AccountGrantOptions *auth.AccountOAuthGrantLeaseOptions
	HTTPClient          *http.Client
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
	accountLoginCurrent       func() (bool, error)
	policyBinding             *managedconfig.CloudConfigBundleBinding
	accountGrantOptions       *auth.AccountOAuthGrantLeaseOptions
	credentialSource          emaCredentialSource
	accountClientID           string
	revoked                   atomic.Bool
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
		accountLoginCurrent:       options.AccountLoginCurrent,
		policyBinding:             options.PolicyBinding,
		codexHome:                 strings.TrimSpace(options.CodexHome),
		storeMode:                 options.StoreMode,
		httpClient:                options.HTTPClient,
	}
	if manager.currentLoginGeneration != nil {
		manager.loginGeneration = manager.currentLoginGeneration()
	}
	if options.AccountGrantOptions != nil {
		grantOptions := *options.AccountGrantOptions
		if strings.TrimSpace(grantOptions.CodexHome) == "" {
			grantOptions.CodexHome = manager.codexHome
		}
		if grantOptions.LoginGeneration == nil {
			grantOptions.LoginGeneration = manager.currentLoginGeneration
		}
		manager.accountGrantOptions = &grantOptions
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
	if err := m.checkNewRequest(); err != nil {
		return err
	}
	token, err := m.accessToken(request.Context())
	if err != nil {
		// A policy change during renewal supersedes the credential failure: the
		// request was never admitted, so callers retry once policy is applied.
		if authorityErr := m.checkNewRequest(); authorityErr != nil {
			return authorityErr
		}
		return err
	}
	if err := m.checkNewRequest(); err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// checkNewRequest mirrors Rust EmaRequestAuthority::check_new_request: the
// synchronous authority and policy checks run at each HTTP boundary, cheaply
// and without credential or network I/O.
func (m *EMAAuthManager) checkNewRequest() error {
	if !m.scopeIsCurrent() {
		return errors.New("enterprise MCP authorization was revoked")
	}
	return m.checkPolicyCurrent()
}

// checkPolicyCurrent holds new dispatches while the bound enterprise policy is
// updating or unavailable. Already admitted responses are not cancelled.
func (m *EMAAuthManager) checkPolicyCurrent() error {
	if m == nil || m.policyBinding == nil {
		return nil
	}
	if m.policyBinding.Status() == managedconfig.CloudConfigBundleBindingCurrent {
		return nil
	}
	return &MCPEMARequestNotSentError{
		Reason: "enterprise MCP policy is updating or unavailable; retry after it is applied",
	}
}

func (m *EMAAuthManager) scopeIsCurrent() bool {
	if m == nil || m.revoked.Load() {
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

// checkAccount mirrors EmaAuth::check_account: a failed ownership check
// revokes this manager, while a storage failure is reported without revoking
// (Rust #55680).
func (m *EMAAuthManager) checkAccount() error {
	if m == nil {
		return errors.New("enterprise MCP authorization is unavailable")
	}
	if m.revoked.Load() {
		return &EMAAuthFailure{Code: EMAAuthFailureReauthenticationRequired}
	}
	if m.accountLoginCurrent == nil {
		return nil
	}
	current, err := m.accountLoginCurrent()
	if err != nil {
		return err
	}
	if !current {
		m.revoked.Store(true)
		return &EMAAuthFailure{Code: EMAAuthFailureReauthenticationRequired}
	}
	return nil
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
	client := m.policyCheckedHTTPClient(m.httpClient)
	// Rust #55680: check the account login before an exchange and again before
	// accepting its result, so a persisted login replacement cannot keep using
	// enterprise authority through a stale auth cache.
	if err := m.checkAccount(); err != nil {
		return "", emaAccountCheckFailure(m.credentialKind(), err)
	}
	source, err := m.pinCredentialSource(ctx)
	if err != nil {
		return "", err
	}
	var token *MCPEMAAccessToken
	if source == emaCredentialAccount {
		grant, err := m.accountGrant(ctx)
		if err != nil {
			return "", emaAccountCheckFailure(m.credentialKind(), err)
		}
		token, err = m.exchangeAccountToken(ctx, client, grant)
	} else {
		token, err = m.exchangeKeyringToken(ctx, client)
	}
	if err != nil {
		return "", err
	}
	if err := m.checkAccount(); err != nil {
		return "", emaAccountCheckFailure(m.credentialKind(), err)
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

// emaCredentialSource is the credential an enterprise connection is pinned to.
// Rust EnterpriseTokenSource chooses one source and OAuth client when the
// connection is established (#55698), so a live connection never silently
// switches credentials when the other source appears or disappears.
type emaCredentialSource int

const (
	emaCredentialUnresolved emaCredentialSource = iota
	emaCredentialAccount
	emaCredentialKeyring
)

func (s emaCredentialSource) kind() EMACredentialKind {
	if s == emaCredentialAccount {
		return EMACredentialKindAccount
	}
	return EMACredentialKindKeyring
}

// credentialKind classifies failures before a source is pinned as an account
// login problem, because only the account can supply a credential then.
func (m *EMAAuthManager) credentialKind() EMACredentialKind {
	if m == nil || m.credentialSource == emaCredentialUnresolved {
		return EMACredentialKindAccount
	}
	return m.credentialSource.kind()
}

// pinCredentialSource selects the credential and OAuth client this manager uses
// (Rust EnterpriseTokenSource::new, #55698). A stored enterprise grant wins
// while it exists; otherwise a validated Codex account grant whose provenance
// names the configured issuer supplies the IdP client. Only a successful
// selection is cached, so a later sign-in is still picked up.
func (m *EMAAuthManager) pinCredentialSource(ctx context.Context) (emaCredentialSource, error) {
	if m.credentialSource != emaCredentialUnresolved {
		return m.credentialSource, nil
	}
	tokens, err := m.storedEnterpriseCredential()
	if err != nil {
		return emaCredentialUnresolved, err
	}
	if tokens != nil {
		m.credentialSource = emaCredentialKeyring
		return m.credentialSource, nil
	}
	lease, err := m.leaseAccountGrant(ctx)
	if err != nil {
		return emaCredentialUnresolved, err
	}
	var clientID string
	if lease != nil {
		granted := lease.Tokens()
		lease.Release()
		if MCPEMAAccountGrantIsUsable(granted, m.profile.IDP) {
			clientID = auth.AccountTokenFactsFromTokens(granted).ClientID
		}
	}
	if clientID == "" {
		return emaCredentialUnresolved, m.accountSetupError()
	}
	m.credentialSource = emaCredentialAccount
	m.accountClientID = clientID
	return m.credentialSource, nil
}

// storedEnterpriseCredential loads the account-scoped enterprise grant. A
// missing record returns nil so selection can consider the Codex account grant;
// a record for another IdP client is a configuration error that must not
// silently fall back to a different credential.
func (m *EMAAuthManager) storedEnterpriseCredential() (*OAuthTokenSet, error) {
	tokens, err := NewOAuthStoreWithMode(m.codexHome, m.storeMode).Load(EMAAuthCredentialName(m.profile.IDP, m.scope), m.profile.IDP.Issuer)
	if err != nil {
		return nil, err
	}
	if tokens == nil || strings.TrimSpace(tokens.RefreshToken) == "" {
		return nil, nil
	}
	if storedClientID := strings.TrimSpace(tokens.ClientID); storedClientID != strings.TrimSpace(m.profile.IDP.ClientID) {
		return nil, errors.New("stored enterprise IdP credentials do not match the configured issuer and client")
	}
	return tokens, nil
}

// accountSetupError reports why no enterprise credential can be selected,
// preferring an actionable sign-in or issuer diagnosis over a generic message.
func (m *EMAAuthManager) accountSetupError() error {
	message := "Complete enterprise sign-in to use this MCP server"
	if m.accountGrantOptions != nil && m.accountGrantOptions.Cached != nil {
		if cached := m.accountGrantOptions.Cached(); cached != nil {
			if issue := MCPEMAAccountConfigurationIssue(cached.Tokens, m.profile.IDP); issue != "" {
				message = issue
			}
		}
	}
	return errors.New(message)
}

// leaseAccountGrant takes the account refresh exclusion for one exchange. A
// manager without an account credential view never leases.
func (m *EMAAuthManager) leaseAccountGrant(ctx context.Context) (*auth.AccountOAuthGrantLease, error) {
	if m == nil || m.accountGrantOptions == nil {
		return nil, nil
	}
	return auth.LeaseAccountOAuthGrant(ctx, m.accountGrantOptions)
}

// accountGrant leases the pinned Codex account grant and rechecks that it is
// still usable for the pinned OAuth client. A stale grant fails the call
// instead of being exchanged (Rust EnterpriseTokenSource::account_grant).
func (m *EMAAuthManager) accountGrant(ctx context.Context) (*auth.AccountOAuthGrant, error) {
	if err := m.checkAccount(); err != nil {
		return nil, err
	}
	lease, err := m.leaseAccountGrant(ctx)
	if err != nil {
		return nil, err
	}
	if lease == nil {
		return nil, &EMAAuthFailure{Code: EMAAuthFailureReauthenticationRequired}
	}
	tokens := lease.Tokens()
	if !MCPEMAAccountGrantIsUsable(tokens, m.profile.IDP) ||
		auth.AccountTokenFactsFromTokens(tokens).ClientID != m.accountClientID {
		lease.Release()
		return nil, &EMAAuthFailure{Code: EMAAuthFailureReauthenticationRequired}
	}
	return lease.Release(), nil
}

// resolveMCPEMAExchange performs the resource-side discovery every EMA exchange
// needs and returns a request with the validated resource, authorization-server
// metadata, and resource client authentication.
func (m *EMAAuthManager) resolveMCPEMAExchange(ctx context.Context, client *http.Client) (MCPEMAIDJAGExchangeRequest, error) {
	request := MCPEMAIDJAGExchangeRequest{
		Scopes:             append([]string(nil), m.scopes...),
		MCPClientID:        m.clientID,
		IDPIssuer:          m.profile.IDP.Issuer,
		IDPHTTPClient:      client,
		ResourceHTTPClient: client,
	}
	discovery, err := DiscoverStreamableHTTPOAuth(ctx, m.serverURL, client)
	if err != nil || discovery == nil {
		return request, errors.New("enterprise MCP authorization discovery failed")
	}
	if m.authorizationServerIssuer != "" && !enterpriseIssuerIdentifiersMatch(discovery.Issuer, m.authorizationServerIssuer) {
		return request, errors.New("MCP authorization server issuer does not match configuration")
	}
	request.Resource = m.resource
	if request.Resource == "" {
		request.Resource = strings.TrimSpace(discovery.Resource)
	}
	request.AuthorizationServerIssuer = discovery.Issuer
	request.AuthorizationServerTokenEndpoint = discovery.TokenEndpoint
	if err := ValidateMCPEMAAuthResource(m.serverURL, &request.Resource); err != nil {
		return request, err
	}
	resourceAuth, err := resolveMCPEMAClientAuth(discovery.TokenEndpointAuthMethodsSupported, m.clientID, m.clientSecret, m.tokenEndpointAuthMethod, "MCP authorization server")
	if err != nil {
		return request, err
	}
	request.ResourceClientAuthentication = resourceAuth
	return request, nil
}

// exchangeKeyringToken exchanges the dedicated enterprise sign-in grant.
func (m *EMAAuthManager) exchangeKeyringToken(ctx context.Context, client *http.Client) (*MCPEMAAccessToken, error) {
	tokens, err := m.storedEnterpriseCredential()
	if err != nil {
		return nil, err
	}
	if tokens == nil {
		return nil, NewEMATokenSourceError(EMACredentialKindKeyring, &EMAAuthFailure{
			Code:    EMAAuthFailureReauthenticationRequired,
			Context: "enterprise sign-in is required",
		})
	}
	request, err := m.resolveMCPEMAExchange(ctx, client)
	if err != nil {
		return nil, err
	}
	idp, err := resolveEnterpriseAuthorizationMetadata(ctx, m.profile.IDP.Issuer, m.profile.IDP.ClientID, m.profile.IDP.ClientSecret, m.profile.IDP.TokenEndpointAuthMethod, client)
	if err != nil {
		return nil, err
	}
	// Rust #55685: a published advertisement that omits ID-JAG identity
	// chaining or the token-exchange grant type rejects the exchange before any
	// credentialed request is sent.
	if err := validateMCPEMAIDPTokenExchangeCapability(idp); err != nil {
		return nil, err
	}
	request.IDPTokenEndpoint = idp.TokenEndpoint
	request.IDPClientAuthentication = idp.ClientAuthentication
	request.IDPClientID = m.profile.IDP.ClientID
	request.RefreshToken = tokens.RefreshToken
	token, err := ExchangeMCPEMAIDJAG(ctx, request)
	if err != nil {
		// Rust #55691: a rejected enterprise grant is retired only when the
		// exchange itself rejected it; a resource authorization rejection leaves
		// the login intact.
		var failure *EMAAuthFailure
		if errors.As(err, &failure) && emaFailureInvalidatesCredential(failure) {
			m.invalidateRejectedCredential(ctx, tokens)
		}
		return nil, emaExchangeFailure(EMACredentialKindKeyring, err)
	}
	return token, nil
}

// exchangeAccountToken exchanges one pinned Codex account grant. Discovery runs
// before the grant is leased so metadata refresh never blocks ordinary account
// refresh; the lease covers only the IdP exchange and is released before
// resource-server I/O (Rust #55698).
func (m *EMAAuthManager) exchangeAccountToken(ctx context.Context, client *http.Client, grant *auth.AccountOAuthGrant) (*MCPEMAAccessToken, error) {
	request, err := m.resolveMCPEMAExchange(ctx, client)
	if err != nil {
		return nil, err
	}
	facts := auth.AccountTokenFactsFromTokens(grant.Tokens())
	identity, err := ResolveMCPEMAAccountIDPIdentity(ctx, MCPEMAAccountIDPIdentityRequest[struct{}]{
		IdP:          m.profile.IDP,
		ClientID:     m.accountClientID,
		IDToken:      facts.IDToken,
		RefreshToken: facts.RefreshToken,
		HTTPClient:   client,
	})
	if err != nil {
		return nil, err
	}
	if err := m.checkAccount(); err != nil {
		return nil, err
	}
	lease, err := m.leaseAccountGrant(ctx)
	if err != nil {
		return nil, err
	}
	if lease == nil {
		return nil, &EMAAuthFailure{Code: EMAAuthFailureReauthenticationRequired}
	}
	// The lease is released before the resource exchange; releasing again is a
	// no-op, so an early validation failure cannot strand the refresh gate.
	defer lease.Release()
	if !auth.SameAccountGrantCredential(lease.Tokens(), grant.Tokens()) {
		return nil, &EMAAuthFailure{Code: EMAAuthFailureReauthenticationRequired}
	}
	request.IDPTokenEndpoint = identity.TokenEndpoint
	request.IDPClientAuthentication = identity.ClientAuthentication
	request.IDPClientID = m.accountClientID
	request.RefreshToken = identity.RefreshToken
	token, receipt, err := ExchangeMCPEMAIDJAGWithCredentials(ctx, request, lease, func(held *auth.AccountOAuthGrantLease) *auth.AccountOAuthGrant {
		return held.Release()
	})
	if err != nil {
		return nil, emaExchangeFailure(EMACredentialKindAccount, err)
	}
	if receipt != nil {
		current, err := receipt.CheckCurrent()
		if err != nil {
			return nil, err
		}
		if !current {
			// A successful exchange with a stale receipt must fail this call.
			return nil, NewEMATokenSourceError(EMACredentialKindAccount, &EMAAuthFailure{Code: EMAAuthFailureReauthenticationRequired})
		}
	}
	return token, nil
}

// policyCheckedHTTPClient bounds every discovery, IdP, and resource-exchange
// request to the enterprise policy revision this manager was built from. Rust
// #55696 re-checks the bound policy in EnterpriseExchangeHttpClient before each
// exchange request, so a revision delivered mid-exchange aborts it instead of
// completing credential work for a policy that no longer authorizes it.
func (m *EMAAuthManager) policyCheckedHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	if m == nil || m.policyBinding == nil {
		return client
	}
	cloned := *client
	next := cloned.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	cloned.Transport = emaPolicyCheckingTransport{next: next, manager: m}
	return &cloned
}

// emaPolicyCheckingTransport refuses a request before it leaves the process
// while the bound enterprise policy is updating or unavailable.
type emaPolicyCheckingTransport struct {
	next    http.RoundTripper
	manager *EMAAuthManager
}

func (t emaPolicyCheckingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := t.manager.checkNewRequest(); err != nil {
		return nil, err
	}
	if t.next == nil {
		return http.DefaultTransport.RoundTrip(request)
	}
	return t.next.RoundTrip(request)
}

// emaExchangeFailure converts an exchange error for transport: an existing
// classification is preserved, a sanitized auth failure keeps its credential
// classification, and anything else becomes an unexpected token-source failure
// with no provider or transport text (Rust #55691). Only the token exchange
// itself is wrapped this way; credential storage, discovery, and metadata
// failures are connection-time errors that keep their diagnostic text, exactly
// as Rust's EnterpriseTokenSource reports them before any token request is
// made.
func emaExchangeFailure(credential EMACredentialKind, err error) error {
	if err == nil {
		return nil
	}
	var sourceErr *EMATokenSourceError
	if errors.As(err, &sourceErr) {
		return sourceErr
	}
	var failure *EMAAuthFailure
	if errors.As(err, &failure) {
		return NewEMATokenSourceError(credential, failure)
	}
	return UnexpectedEMATokenSourceError(credential)
}

// emaAccountCheckFailure wraps only a sanitized account-ownership failure; a
// credential-store error keeps its own text, as in Rust EmaAuth::check_account.
func emaAccountCheckFailure(credential EMACredentialKind, err error) error {
	if err == nil {
		return nil
	}
	var failure *EMAAuthFailure
	if errors.As(err, &failure) {
		return NewEMATokenSourceError(credential, failure)
	}
	return err
}

// emaFailureInvalidatesCredential reports whether a rejected grant must be
// retired so a later attempt cannot reuse it (Rust #55691).
func emaFailureInvalidatesCredential(failure *EMAAuthFailure) bool {
	if failure == nil {
		return false
	}
	// Rust only retires the stored grant when the enterprise IdP itself rejected
	// it with invalid_grant. A client-registration or step-up rejection can be
	// repaired without discarding the user's login, so it leaves the grant alone.
	return failure.Code == EMAAuthFailureInvalidGrant &&
		failure.GrantSource == EMAInvalidGrantSourceEnterpriseIdentity
}

// invalidateRejectedCredential deletes the stored enterprise grant the exchange
// used, but only while it is still current, so a concurrent replacement
// survives (Rust #55691). The account is rechecked under the credential lock
// and a cancelled request deletes nothing.
func (m *EMAAuthManager) invalidateRejectedCredential(ctx context.Context, used *OAuthTokenSet) {
	if m == nil || used == nil || m.codexHome == "" {
		return
	}
	guard, err := AcquireEnterpriseOAuthCredentialGuard(
		m.codexHome,
		EMAAuthCredentialName(m.profile.IDP, m.scope),
		m.profile.IDP.Issuer,
		m.storeMode,
	)
	if err != nil {
		return
	}
	defer guard.Close()
	if err := m.checkAccount(); err != nil {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	current, err := guard.store.Load(guard.credentialName, guard.issuer)
	if err != nil || !sameStoredMCPOAuthCredential(current, used) {
		return
	}
	_, _ = guard.DeleteTokens()
}

// AttachEnterpriseMCPAuthorization configures every admitted EMA server with
// its per-request bearer exchange and credential status.
func AttachEnterpriseMCPAuthorization(runtime *RuntimeConfig, codexHome string, auth *RuntimeAuth, currentScope func() (EMAAuthScope, bool), currentLoginGeneration func() uint64, accountLoginCurrent func() (bool, error), accountGrantOptions *auth.AccountOAuthGrantLeaseOptions) {
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
			AccountLoginCurrent:    accountLoginCurrent,
			AccountGrantOptions:    accountGrantOptions,
			PolicyBinding:          runtime.EMAPolicyBinding,
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

// emaScopeCheckingDoer rechecks the account owner and the bound policy at the
// HTTP client boundary, so a stale manager cannot reuse a revoked enterprise
// grant after a config reload or account switch (Rust #55287/#55288/#55696).
type emaScopeCheckingDoer struct {
	next    HTTPDoer
	manager *EMAAuthManager
}

func (d emaScopeCheckingDoer) Do(request *http.Request) (*http.Response, error) {
	// Rust #55696 checks the retained connection authority at the HTTP boundary,
	// so a revoked account or a superseded policy revision stops the dispatch
	// even when it happens after ApplyHTTPRequest returned.
	if d.manager != nil {
		if err := d.manager.checkNewRequest(); err != nil {
			return nil, err
		}
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
