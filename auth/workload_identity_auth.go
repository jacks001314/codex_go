package auth

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Workload identity environment selection (Rust 96c8be200c, #38188):
// when OPENAI_FEDERATION_RULE_ID / OPENAI_IDENTITY_TOKEN_FILE are set and no
// explicit API key / access token is configured, Codex authenticates through
// a file-backed JWT exchange with ChatGPT's auth endpoints, reusing one
// process-scoped session for resolve and refresh.
const (
	OpenAIFederationRuleIDEnv  = "OPENAI_FEDERATION_RULE_ID"
	OpenAIIdentityTokenFileEnv = "OPENAI_IDENTITY_TOKEN_FILE"

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
	// ChatGPTLoginAllowed mirrors Rust's login-policy check; nil means allowed.
	ChatGPTLoginAllowed *bool
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

func (o *WorkloadIdentityAuthOptions) httpClient() *http.Client {
	if o != nil && o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: workloadRequestTimeout}
}

// IsWorkloadIdentitySelected reports whether the process environment selects
// workload identity authentication (Rust is_workload_identity_selected):
// markers are present and no explicit API key / access token is configured.
func IsWorkloadIdentitySelected() bool {
	return !workloadHasExplicitProcessAuth() && workloadReadProcessEnv().hasMarker()
}

func workloadHasExplicitProcessAuth() bool {
	return readNonEmptyEnv(OpenAIAPIKeyEnv) != "" ||
		readNonEmptyEnv(CodexAPIKeyEnv) != "" ||
		readNonEmptyEnv(CodexAccessTokenEnv) != ""
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
	federationRuleID  string
	identityTokenFile string
	hasFederationRule bool
	hasIdentityToken  bool
}

func workloadReadProcessEnv() workloadIdentityProcessEnv {
	return workloadIdentityProcessEnv{
		federationRuleID:  strings.TrimSpace(readNonEmptyEnv(OpenAIFederationRuleIDEnv)),
		identityTokenFile: readNonEmptyEnv(OpenAIIdentityTokenFileEnv),
		hasFederationRule: osEnvPresent(OpenAIFederationRuleIDEnv),
		hasIdentityToken:  osEnvPresent(OpenAIIdentityTokenFileEnv),
	}
}

func (e workloadIdentityProcessEnv) hasMarker() bool {
	return e.hasFederationRule || e.hasIdentityToken
}

// workloadIdentitySessionConfig is a fully validated, exchange-ready session
// configuration (Rust WorkloadIdentitySessionConfig).
type workloadIdentitySessionConfig struct {
	assertionFile    string
	environment      workloadIdentityEnvironment
	federationRuleID string
	httpClient       *http.Client
	tokenURL         string
}

func (c workloadIdentitySessionConfig) fingerprint() workloadIdentityFingerprint {
	return workloadIdentityFingerprint{
		assertionFile:    c.assertionFile,
		environment:      c.environment,
		federationRuleID: c.federationRuleID,
		tokenURL:         c.tokenURL,
	}
}

func (c workloadIdentitySessionConfig) exchange() (*WorkloadIdentityExchange, error) {
	config, err := NewWorkloadIdentityConfig(c.federationRuleID, c.assertionFile)
	if err != nil {
		return nil, err
	}
	return NewWorkloadIdentityExchange(config, c.tokenURL, c.httpClient)
}

type workloadIdentityFingerprint struct {
	assertionFile    string
	environment      workloadIdentityEnvironment
	federationRuleID string
	tokenURL         string
}

// resolveWorkloadIdentityConfig mirrors Rust resolve_config: selects workload
// identity only when no explicit process auth is configured and the markers
// are present, then validates the rule ID, the absolute assertion path, the
// login policy, and the app routing.
func resolveWorkloadIdentityConfig(baseURL string, env workloadIdentityProcessEnv, hasExplicitProcessAuth bool, chatgptLoginAllowed bool) (*workloadIdentitySessionConfig, error) {
	if hasExplicitProcessAuth || !env.hasMarker() {
		return nil, nil
	}
	if !chatgptLoginAllowed {
		return nil, workloadInvalidConfig("workload identity requires a login policy that permits ChatGPT authentication")
	}
	if !env.hasFederationRule || env.federationRuleID == "" {
		return nil, workloadInvalidConfig("workload identity requires " + OpenAIFederationRuleIDEnv)
	}
	if !env.hasIdentityToken || env.identityTokenFile == "" {
		return nil, workloadInvalidConfig("workload identity requires " + OpenAIIdentityTokenFileEnv)
	}
	if !filepathIsAbsolute(env.identityTokenFile) {
		return nil, workloadInvalidConfig(OpenAIIdentityTokenFileEnv + " must be an absolute path")
	}
	environment, err := classifyWorkloadAuthEnvironment(baseURL)
	if err != nil {
		return nil, err
	}
	return &workloadIdentitySessionConfig{
		assertionFile:    env.identityTokenFile,
		environment:      environment,
		federationRuleID: env.federationRuleID,
		tokenURL:         environment.tokenURL(),
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

type workloadIdentitySubject struct {
	accountID     string
	accountUserID string
	userID        string
}

func workloadSubjectFromToken(token WorkloadIdentityToken) workloadIdentitySubject {
	return workloadIdentitySubject{
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
	if previousAccountID != "" && previousAccountID != token.ChatGPTAccountID {
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
}

// WorkloadIdentityAuthForProcess selects and activates workload identity for
// this process (Rust WorkloadIdentityExternalAuth::from_process_config).
// It returns (nil, nil) when workload identity is not selected.
func WorkloadIdentityAuthForProcess(opts *WorkloadIdentityAuthOptions) (*WorkloadIdentityAuth, error) {
	active := workloadIdentityProcessRegistry.active()
	env := workloadReadProcessEnv()
	config, err := resolveWorkloadIdentityConfig(
		opts.baseURL(),
		env,
		workloadHasExplicitProcessAuth() && !active,
		opts.chatgptLoginAllowed(),
	)
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
	return &WorkloadIdentityAuth{session: session}, nil
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
	return &WorkloadIdentityAuth{session: session}, nil
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
