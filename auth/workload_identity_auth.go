package auth

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// Workload identity environment selection (Rust 96c8be200c, #38188):
// when OPENAI_FEDERATION_RULE_ID / OPENAI_IDENTITY_TOKEN_FILE are set and no
// explicit API key / access token is configured, Codex authenticates through
// a file-backed JWT exchange with ChatGPT's auth endpoints, reusing one
// process-scoped session for resolve and refresh.
const (
	OpenAIFederationRuleIDEnv        = "OPENAI_FEDERATION_RULE_ID"
	OpenAIIdentityTokenFileEnv       = "OPENAI_IDENTITY_TOKEN_FILE"
	OpenAIIdentityTokenSourceEnv     = "OPENAI_IDENTITY_TOKEN_SOURCE"
	OpenAISPIFFEAudienceEnv          = "OPENAI_SPIFFE_AUDIENCE"
	OpenAISPIFFEIDEnv                = "OPENAI_SPIFFE_ID"
	SPIFFEEndpointSocketEnv          = "SPIFFE_ENDPOINT_SOCKET"
	OpenAIWorkloadIdentityContextEnv = "OPENAI_WORKLOAD_IDENTITY_CONTEXT"

	// WorkloadIdentitySource labels ResolvedAuth from the workload identity
	// process session (Rust's configured external auth source).
	WorkloadIdentitySource = "workload-identity"

	workloadProdTokenURL    = "https://auth.openai.com/oauth/token"
	workloadStagingTokenURL = "https://auth.api.openai.org/oauth/token"

	defaultWorkloadChatGPTBaseURL = "https://chatgpt.com/backend-api/"
)

// WorkloadIdentityAuthOptions customizes workload identity selection. Zero
// values select the production app routing, allow ChatGPT login, and use the
// default HTTP client.
type WorkloadIdentityAuthOptions struct {
	// ChatGPTBaseURL classifies the auth environment (production vs staging).
	// Empty means the production default "https://chatgpt.com/backend-api/".
	ChatGPTBaseURL string
	// ChatGPTLoginAllowed mirrors Rust's login-policy check for the ChatGPT
	// product; nil means allowed.
	ChatGPTLoginAllowed *bool
	// APILoginAllowed is the login-policy check for the API product (#59275);
	// nil means allowed.
	APILoginAllowed *bool
	// ForcedChatGPTWorkspaces restricts the ChatGPT product to the listed
	// workspace account IDs; empty means unrestricted. API products have no
	// ChatGPT workspace.
	ForcedChatGPTWorkspaces []string
	// HTTPClient performs the token exchange; nil means the default client.
	HTTPClient *http.Client
}

func (o *WorkloadIdentityAuthOptions) baseURL() string {
	if o == nil {
		return defaultWorkloadChatGPTBaseURL
	}
	if value := strings.TrimSpace(o.ChatGPTBaseURL); value != "" {
		return value
	}
	return defaultWorkloadChatGPTBaseURL
}

func (o *WorkloadIdentityAuthOptions) chatgptLoginAllowed() bool {
	if o == nil || o.ChatGPTLoginAllowed == nil {
		return true
	}
	return *o.ChatGPTLoginAllowed
}

func (o *WorkloadIdentityAuthOptions) forcedChatGPTWorkspaces() []string {
	if o == nil {
		return nil
	}
	return o.ForcedChatGPTWorkspaces
}

func (o *WorkloadIdentityAuthOptions) apiLoginAllowed() bool {
	if o == nil || o.APILoginAllowed == nil {
		return true
	}
	return *o.APILoginAllowed
}

func (o *WorkloadIdentityAuthOptions) httpClient() *http.Client {
	if o != nil && o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: workloadRequestTimeout}
}

// IsWorkloadIdentitySelected reports whether the process environment selects
// workload identity authentication (Rust is_workload_identity_selected):
// either environment marker selects workload identity and partial
// configuration fails validation rather than falling back to another
// credential source.
func IsWorkloadIdentitySelected() bool {
	return workloadReadProcessEnv().hasMarker()
}

type workloadIdentityEnvironment int

const (
	workloadEnvironmentProduction workloadIdentityEnvironment = iota
	workloadEnvironmentStaging
)

func (e workloadIdentityEnvironment) tokenURL() string {
	if e == workloadEnvironmentStaging {
		return workloadStagingTokenURL
	}
	return workloadProdTokenURL
}

// workloadIdentitySessionError mirrors Rust WorkloadIdentitySessionError.
type workloadIdentitySessionError string

func (e workloadIdentitySessionError) Error() string { return string(e) }

const (
	workloadErrConflictingConfiguration workloadIdentitySessionError = "a different workload identity configuration is already active in this process"
	workloadErrRegistryUnavailable      workloadIdentitySessionError = "the workload identity process-session registry is unavailable"
)

func workloadInvalidConfig(message string) error {
	return workloadIdentitySessionError(message)
}

type workloadIdentityProcessEnv struct {
	federationRuleID        string
	identityTokenFile       string
	identityTokenSource     string
	spiffeEndpoint          string
	spiffeAudience          string
	spiffeID                string
	workloadIdentityContext string
	hasFederationRule       bool
	hasIdentityToken        bool
	hasIdentityTokenSource  bool
	hasSPIFFEEndpoint       bool
	hasSPIFFEAudience       bool
	hasSPIFFEID             bool
}

func workloadReadProcessEnv() workloadIdentityProcessEnv {
	return workloadIdentityProcessEnv{
		federationRuleID:        strings.TrimSpace(readNonEmptyEnv(OpenAIFederationRuleIDEnv)),
		identityTokenFile:       readNonEmptyEnv(OpenAIIdentityTokenFileEnv),
		identityTokenSource:     os.Getenv(OpenAIIdentityTokenSourceEnv),
		workloadIdentityContext: readNonEmptyEnv(OpenAIWorkloadIdentityContextEnv),
		hasFederationRule:       osEnvPresent(OpenAIFederationRuleIDEnv),
		hasIdentityToken:        osEnvPresent(OpenAIIdentityTokenFileEnv),
		hasIdentityTokenSource:  osEnvPresent(OpenAIIdentityTokenSourceEnv),
		hasSPIFFEEndpoint:       osEnvPresent(SPIFFEEndpointSocketEnv),
		hasSPIFFEAudience:       osEnvPresent(OpenAISPIFFEAudienceEnv),
		hasSPIFFEID:             osEnvPresent(OpenAISPIFFEIDEnv),
		spiffeEndpoint:          os.Getenv(SPIFFEEndpointSocketEnv),
		spiffeAudience:          os.Getenv(OpenAISPIFFEAudienceEnv),
		spiffeID:                os.Getenv(OpenAISPIFFEIDEnv),
	}
}

func (e workloadIdentityProcessEnv) hasMarker() bool {
	return e.hasFederationRule || e.hasIdentityToken || e.hasIdentityTokenSource || e.hasSPIFFEAudience || e.hasSPIFFEID
}

// workloadIdentitySessionConfig is a fully validated, exchange-ready session
// configuration (Rust WorkloadIdentitySessionConfig).
type workloadIdentitySessionConfig struct {
	spiffe                  *spiffeAssertionSource
	assertionFile           string
	environment             workloadIdentityEnvironment
	federationRuleID        string
	httpClient              *http.Client
	tokenURL                string
	workloadIdentityContext string
}

func (c workloadIdentitySessionConfig) fingerprint() workloadIdentityFingerprint {
	fingerprint := workloadIdentityFingerprint{
		assertionFile:           c.assertionFile,
		environment:             c.environment,
		federationRuleID:        c.federationRuleID,
		tokenURL:                c.tokenURL,
		workloadIdentityContext: c.workloadIdentityContext,
	}
	if c.spiffe != nil {
		fingerprint.spiffe = *c.spiffe
		fingerprint.hasSPIFFE = true
	}
	return fingerprint
}

func (c workloadIdentitySessionConfig) exchange() (*WorkloadIdentityExchange, error) {
	if c.spiffe != nil {
		source := *c.spiffe
		return NewWorkloadIdentityExchange(WorkloadIdentityConfig{spiffe: &source, FederationRuleID: c.federationRuleID, WorkloadIdentityContext: c.workloadIdentityContext}, c.tokenURL, c.httpClient)
	}
	config, err := NewWorkloadIdentityConfig(c.federationRuleID, c.assertionFile, c.workloadIdentityContext)
	if err != nil {
		return nil, err
	}
	return NewWorkloadIdentityExchange(config, c.tokenURL, c.httpClient)
}

type workloadIdentityFingerprint struct {
	spiffe                  spiffeAssertionSource
	hasSPIFFE               bool
	assertionFile           string
	environment             workloadIdentityEnvironment
	federationRuleID        string
	tokenURL                string
	workloadIdentityContext string
}

// resolveWorkloadIdentityConfig mirrors Rust resolve_config: any workload
// identity marker selects workload identity, and the configuration is then
// validated (rule ID, absolute assertion path, login policy, and app routing)
// without falling back to explicit environment credentials.
func resolveWorkloadIdentityConfig(baseURL string, env workloadIdentityProcessEnv) (*workloadIdentitySessionConfig, error) {
	if !env.hasMarker() {
		return nil, nil
	}
	if !env.hasFederationRule || env.federationRuleID == "" {
		return nil, workloadInvalidConfig("workload identity requires " + OpenAIFederationRuleIDEnv)
	}
	if !utf8.ValidString(env.identityTokenSource) {
		return nil, workloadInvalidConfig("workload identity variable " + OpenAIIdentityTokenSourceEnv + " is invalid")
	}
	if env.hasIdentityTokenSource && env.identityTokenSource == "" {
		return nil, workloadInvalidConfig("OPENAI_IDENTITY_TOKEN_SOURCE must be file or spiffe")
	}
	var source *spiffeAssertionSource
	switch env.identityTokenSource {
	case "", "file":
		if env.hasSPIFFEAudience || env.hasSPIFFEID {
			return nil, workloadInvalidConfig("SPIFFE settings require OPENAI_IDENTITY_TOKEN_SOURCE=spiffe")
		}
	case "spiffe":
		if env.hasIdentityToken {
			return nil, workloadInvalidConfig("SPIFFE authentication cannot also set OPENAI_IDENTITY_TOKEN_FILE")
		}
		endpoint, err := workloadRequiredUnicode(env.spiffeEndpoint, env.hasSPIFFEEndpoint, SPIFFEEndpointSocketEnv)
		if err != nil {
			return nil, err
		}
		audience, err := workloadRequiredUnicode(env.spiffeAudience, env.hasSPIFFEAudience, OpenAISPIFFEAudienceEnv)
		if err != nil {
			return nil, err
		}
		if !utf8.ValidString(env.spiffeID) {
			return nil, workloadInvalidConfig("workload identity variable " + OpenAISPIFFEIDEnv + " is invalid")
		}
		if !spiffePlatformSupported {
			return nil, workloadInvalidConfig("SPIFFE Workload API authentication requires a Unix platform")
		}
		if env.hasSPIFFEID && env.spiffeID == "" {
			return nil, workloadInvalidConfig("the SPIFFE ID is invalid")
		}
		source, err = newSPIFFEAssertionSource(endpoint, audience, env.spiffeID)
		if err != nil {
			return nil, err
		}
	default:
		return nil, workloadInvalidConfig("OPENAI_IDENTITY_TOKEN_SOURCE must be file or spiffe")
	}
	if source == nil && (!env.hasIdentityToken || env.identityTokenFile == "") {
		return nil, workloadInvalidConfig("workload identity requires " + OpenAIIdentityTokenFileEnv)
	}
	if source == nil && !filepathIsAbsolute(env.identityTokenFile) {
		return nil, workloadInvalidConfig(OpenAIIdentityTokenFileEnv + " must be an absolute path")
	}
	environment, err := classifyWorkloadAuthEnvironment(baseURL)
	if err != nil {
		return nil, err
	}
	return &workloadIdentitySessionConfig{
		spiffe:                  source,
		assertionFile:           env.identityTokenFile,
		environment:             environment,
		federationRuleID:        env.federationRuleID,
		tokenURL:                environment.tokenURL(),
		workloadIdentityContext: env.workloadIdentityContext,
	}, nil
}

// classifyWorkloadAuthEnvironment mirrors Rust classify_auth_environment: only
// the trusted production and staging app routing prefixes are supported.
func classifyWorkloadAuthEnvironment(baseURL string) (workloadIdentityEnvironment, error) {
	switch strings.TrimRight(strings.TrimSpace(baseURL), "/") {
	case "https://chatgpt.com",
		"https://chatgpt.com/backend-api",
		"https://chatgpt.com/codex",
		"https://chatgpt.com/backend-api/codex",
		"https://chat.openai.com",
		"https://chat.openai.com/backend-api",
		"https://chat.openai.com/codex",
		"https://chat.openai.com/backend-api/codex":
		return workloadEnvironmentProduction, nil
	case "https://chatgpt-staging.com",
		"https://chatgpt-staging.com/backend-api",
		"https://chatgpt-staging.com/codex",
		"https://chatgpt-staging.com/backend-api/codex":
		return workloadEnvironmentStaging, nil
	default:
		return 0, workloadInvalidConfig("workload identity auth supports only trusted production and staging app routing")
	}
}

// workloadIdentitySubject identifies the authenticated identity so a token
// that changes product or identity is rejected (Rust WorkloadIdentitySubject,
// #59275).
type workloadIdentitySubject struct {
	product        WorkloadIdentityProduct
	accountID      string
	accountUserID  string
	userID         string
	organizationID string
	projectID      string
}

func workloadSubjectFromToken(token WorkloadIdentityToken) workloadIdentitySubject {
	if token.Product == WorkloadIdentityProductAPI {
		subject := workloadIdentitySubject{product: token.Product}
		if token.API != nil {
			subject.userID = token.API.UserID
			subject.organizationID = token.API.APIOrganizationID
			subject.projectID = token.API.APIProjectID
		}
		return subject
	}
	return workloadIdentitySubject{
		product:       WorkloadIdentityProductChatGPT,
		accountID:     token.ChatGPTAccountID,
		accountUserID: token.ChatGPTAccountUserID,
		userID:        token.UserID,
	}
}

// workloadIdentitySession owns one exchange plus the authenticated subject it
// has accepted, so a token that changes the subject is rejected
// (Rust WorkloadIdentitySession).
type workloadIdentitySession struct {
	exchange *WorkloadIdentityExchange
	mu       sync.Mutex
	subject  *workloadIdentitySubject
}

func (s *workloadIdentitySession) acceptSubject(token WorkloadIdentityToken, previousAccountID string) error {
	accountID := ""
	if token.Product != WorkloadIdentityProductAPI {
		accountID = token.ChatGPTAccountID
	}
	// An API workload identity has no ChatGPT account, so a previous ChatGPT
	// account can never carry over to it (Rust accept_subject).
	if previousAccountID != "" && previousAccountID != accountID {
		return workloadErrInvalidExchangeResponse
	}
	subject := workloadSubjectFromToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.subject != nil && *s.subject != subject:
		return workloadErrInvalidExchangeResponse
	case s.subject == nil:
		s.subject = &subject
	}
	return nil
}

// workloadIdentitySessionRegistry reuses a compatible process-scoped session
// and rejects a conflicting configuration while one is active (Rust
// WorkloadIdentitySessionRegistry). The entry keeps the session for the
// process lifetime once workload identity is activated.
type workloadIdentitySessionRegistry struct {
	mu    sync.Mutex
	entry *workloadIdentitySessionEntry
}

type workloadIdentitySessionEntry struct {
	fingerprint workloadIdentityFingerprint
	session     *workloadIdentitySession
}

func (r *workloadIdentitySessionRegistry) active() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entry != nil
}

func (r *workloadIdentitySessionRegistry) session(config workloadIdentitySessionConfig) (*workloadIdentitySession, error) {
	fingerprint := config.fingerprint()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entry != nil {
		if r.entry.fingerprint == fingerprint {
			return r.entry.session, nil
		}
		return nil, workloadErrConflictingConfiguration
	}
	exchange, err := config.exchange()
	if err != nil {
		return nil, err
	}
	session := &workloadIdentitySession{exchange: exchange}
	r.entry = &workloadIdentitySessionEntry{
		fingerprint: fingerprint,
		session:     session,
	}
	return session, nil
}

// workloadIdentityProcessRegistry is the process-scoped registry shared by
// every auth resolution and refresh path (Rust process_registry OnceLock).
var workloadIdentityProcessRegistry = &workloadIdentitySessionRegistry{}

func workloadIdentitySessionActive() bool {
	return workloadIdentityProcessRegistry.active()
}

// WorkloadIdentityAuth is the Go counterpart of Rust
// WorkloadIdentityExternalAuth: it resolves and refreshes tokens through the
// process-scoped session, preserving the authenticated subject and
// invalidating rejected tokens without discarding a newer concurrent exchange.
type WorkloadIdentityAuth struct {
	observedTokenVersion atomic.Uint64
	session              *workloadIdentitySession
	// Login policy is applied to the product the exchange resolved, not to the
	// configuration before the exchange (Rust #59275).
	chatgptLoginAllowed     bool
	apiLoginAllowed         bool
	forcedChatGPTWorkspaces []string
}

// WorkloadIdentityAuthForProcess selects and activates workload identity for
// this process (Rust WorkloadIdentityExternalAuth::from_process_config).
// It returns (nil, nil) when workload identity is not selected.
func WorkloadIdentityAuthForProcess(opts *WorkloadIdentityAuthOptions) (*WorkloadIdentityAuth, error) {
	env := workloadReadProcessEnv()
	config, err := resolveWorkloadIdentityConfig(opts.baseURL(), env)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, nil
	}
	config.httpClient = opts.httpClient()
	session, err := workloadIdentityProcessRegistry.session(*config)
	if err != nil {
		return nil, err
	}
	return &WorkloadIdentityAuth{
		session:                 session,
		chatgptLoginAllowed:     opts.chatgptLoginAllowed(),
		apiLoginAllowed:         opts.apiLoginAllowed(),
		forcedChatGPTWorkspaces: append([]string(nil), opts.forcedChatGPTWorkspaces()...),
	}, nil
}

// newWorkloadIdentityAuthForRegistry builds an adapter bound to a specific
// session registry and pre-resolved config (Rust
// WorkloadIdentityExternalAuth::from_config_with_registry), used by tests and
// the refresh path.
func newWorkloadIdentityAuthForRegistry(config workloadIdentitySessionConfig, registry *workloadIdentitySessionRegistry) (*WorkloadIdentityAuth, error) {
	session, err := registry.session(config)
	if err != nil {
		return nil, err
	}
	return &WorkloadIdentityAuth{session: session, chatgptLoginAllowed: true, apiLoginAllowed: true}, nil
}

// ResolveAuth resolves a validated workload identity token (Rust ExternalAuth
// resolve): the exchanged token must preserve the session subject.
func (a *WorkloadIdentityAuth) ResolveAuth(ctx context.Context) (*AuthDotJSON, error) {
	token, err := a.session.exchange.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	return a.buildValidatedAuth(token, "")
}

// RefreshAuth re-exchanges after a downstream rejection (Rust ExternalAuth
// refresh), preserving the previously authenticated account.
func (a *WorkloadIdentityAuth) RefreshAuth(ctx context.Context, previousAccountID string) (*AuthDotJSON, error) {
	observed := a.observedTokenVersion.Load()
	token, err := a.session.exchange.Refresh(ctx, observed)
	if err != nil {
		return nil, err
	}
	return a.buildValidatedAuth(token, previousAccountID)
}

func (a *WorkloadIdentityAuth) buildValidatedAuth(token WorkloadIdentityToken, previousAccountID string) (*AuthDotJSON, error) {
	auth, err := a.validateToken(token, previousAccountID)
	if err != nil {
		// Invalidate the rejected cached token without discarding a newer
		// concurrent exchange (Rust build_validated_auth).
		a.session.exchange.InvalidateIfCurrent(token.Version)
		return nil, err
	}
	a.observedTokenVersion.Store(token.Version)
	return auth, nil
}

func (a *WorkloadIdentityAuth) validateToken(token WorkloadIdentityToken, previousAccountID string) (*AuthDotJSON, error) {
	if token.Product == WorkloadIdentityProductAPI {
		if !a.apiLoginAllowed {
			return nil, workloadInvalidConfig("workload identity requires a login policy that permits API key authentication")
		}
		if token.API == nil {
			return nil, workloadErrInvalidExchangeResponse
		}
		// Rust #60433: an API exchange token must carry OAuth source claims so
		// the credential can be bound to a session family.
		if !hasOAuthSessionClaim(token.AccessToken) {
			return nil, workloadErrInvalidExchangeResponse
		}
		// Rust #59275: an API product exchange resolves as API key auth and
		// keeps its service account metadata for ownership checks.
		auth := FromAPIKey(token.AccessToken)
		metadata := *token.API
		auth.WorkloadIdentity = &metadata
		if err := a.session.acceptSubject(token, previousAccountID); err != nil {
			return nil, err
		}
		return &auth, nil
	}
	if !a.chatgptLoginAllowed {
		return nil, workloadInvalidConfig("workload identity requires a login policy that permits ChatGPT authentication")
	}
	// Rust #59275 applies the forced-workspace restriction to the resolved
	// ChatGPT product; API products have no ChatGPT workspace.
	if err := EnsureWorkspaceAccountAllowed(a.forcedChatGPTWorkspaces, token.ChatGPTAccountID); err != nil {
		return nil, err
	}
	auth := FromChatGPTAuthTokens(token.AccessToken, token.ChatGPTAccountID, token.ChatGPTPLanType)
	claims := ChatGPTClaimsFromJWT(token.AccessToken)
	// The exchanged token must carry the same subject as the assertion: the
	// access-token JWT's user id must equal the exchange response's user id
	// (Rust validate_auth).
	if claims.UserID == "" || claims.UserID != token.UserID {
		return nil, workloadErrInvalidExchangeResponse
	}
	if err := a.session.acceptSubject(token, previousAccountID); err != nil {
		return nil, err
	}
	return &auth, nil
}

// workloadIdentityErrorIsPermanent mirrors Rust WorkloadIdentityExternalAuth
// classify_error: transient exchange failures map to retryable errors, while
// everything else is permanent.
func workloadIdentityErrorIsPermanent(err error) bool {
	if err == nil {
		return false
	}
	return !workloadErrorIsTransient(err)
}

// WorkloadIdentityTransientError reports whether err is a temporary external
// auth failure that may succeed on retry (Rust RefreshTokenError::Transient).
func WorkloadIdentityTransientError(err error) bool {
	return workloadErrorIsTransient(err)
}

// RetainAuthAfterTransientExternalFailure returns a copy of cached when a
// temporarily failing external auth provider must not be reported as a logout
// (Rust #59491 load_auth). validate reports whether the cached credential still
// satisfies the active login and workspace restrictions.
func RetainAuthAfterTransientExternalFailure(err error, cached *AuthDotJSON, validate func(*AuthDotJSON) bool) (*AuthDotJSON, bool) {
	if cached == nil || !WorkloadIdentityTransientError(err) {
		return nil, false
	}
	if validate != nil && !validate(cached) {
		return nil, false
	}
	return cloneAuthDotJSON(cached), true
}

// WorkloadIdentityPermanentError mirrors Rust RefreshTokenError::Permanent with
// reason Other for non-transient workload identity failures.
func WorkloadIdentityPermanentError(err error) *RefreshTokenFailedError {
	if err == nil || workloadErrorIsTransient(err) {
		return nil
	}
	return &RefreshTokenFailedError{
		Reason:  RefreshTokenFailedOther,
		Message: err.Error(),
	}
}

// workloadRequiredUnicode preserves the missing/invalid distinction in Rust's
// required_unicode while keeping environment values out of errors.
func workloadRequiredUnicode(value string, present bool, variable string) (string, error) {
	if !present {
		return "", workloadInvalidConfig("workload identity requires " + variable)
	}
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return "", workloadInvalidConfig("workload identity variable " + variable + " is invalid")
	}
	return strings.TrimSpace(value), nil
}
