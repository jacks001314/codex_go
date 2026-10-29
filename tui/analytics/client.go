package analytics

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/model"
	"codex_go/remotecontrol"
)

// Account-scoped analytics authentication mirrors Rust #45742: a session stays
// bound to the account and user that were signed in when it opened, reloads
// credentials before and after each request, and gets one bounded recovery
// attempt after a 401.
const (
	analyticsSignInRequiredMessage   = "Sign in locally with ChatGPT to view Analytics."
	analyticsLocalSignInLoadMessage  = "Couldn't load local sign-in. Sign in with ChatGPT and retry."
	analyticsIdentityRequiredMessage = "Analytics requires a ChatGPT account and user identity."
	analyticsAccountChangedMessage   = "Account changed. Press R to refresh Analytics."
	analyticsSignInAgainMessage      = "Sign in again to view Analytics."
)

// AnalyticsAccount is the non-secret account metadata bound to a session.
type AnalyticsAccount struct {
	ID       string
	Email    *string
	PlanType auth.PlanType
}

// AnalyticsSessionConfig supplies the local auth store and backend policy.
type AnalyticsSessionConfig struct {
	// CodexHome locates auth.json. It defaults to auth.DefaultCodexHome when
	// empty.
	CodexHome string
	// BaseURL is the ChatGPT backend base URL; the default ChatGPT backend is
	// used when empty.
	BaseURL string
	// HTTPClient overrides the default HTTP client. Redirects are disabled for
	// the standard client so credentials never follow a redirect.
	HTTPClient chatgptapi.HTTPDoer
	// StoreOptions selects the credential storage backend.
	StoreOptions *auth.StoreOptions
}

// AnalyticsSession is a backend client bound to its initial ChatGPT account
// and user (Rust AnalyticsSession).
type AnalyticsSession struct {
	mu         sync.Mutex
	codexHome  string
	baseURL    string
	httpClient chatgptapi.HTTPDoer
	storeOpts  *auth.StoreOptions
	auth       *auth.AuthDotJSON
	userID     string
	account    AnalyticsAccount
}

// NewAnalyticsSession loads local ChatGPT credentials and binds the session to
// their account and user. Environment API keys are deliberately ignored.
func NewAnalyticsSession(ctx context.Context, config AnalyticsSessionConfig) (*AnalyticsSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	codexHome := strings.TrimSpace(config.CodexHome)
	if codexHome == "" {
		codexHome = auth.DefaultCodexHome()
	}
	snapshot, err := auth.NewStoreWithOptions(codexHome, config.StoreOptions).Load()
	if err != nil {
		return nil, errors.New(analyticsLocalSignInLoadMessage)
	}
	if !isAnalyticsChatGPTAuth(snapshot) {
		return nil, errors.New(analyticsSignInRequiredMessage)
	}
	accountID := auth.AccountIDFromAuthForRestrictions(snapshot)
	userID := auth.ChatGPTUserIDFromAuth(snapshot)
	if accountID == "" || userID == "" {
		return nil, errors.New(analyticsIdentityRequiredMessage)
	}
	account := AnalyticsAccount{ID: accountID, PlanType: auth.PlanUnknown}
	if resolved := auth.AccountFromAuth(snapshot); resolved != nil {
		account.Email = resolved.Email
		account.PlanType = resolved.PlanType
	}
	return &AnalyticsSession{
		codexHome:  codexHome,
		baseURL:    config.BaseURL,
		httpClient: config.HTTPClient,
		storeOpts:  config.StoreOptions,
		auth:       snapshot,
		userID:     userID,
		account:    account,
	}, nil
}

// isAnalyticsChatGPTAuth matches Rust CodexAuth::is_chatgpt_auth: ChatGPT
// tokens, external ChatGPT tokens, and personal access tokens all carry a
// ChatGPT account, while API keys do not.
func isAnalyticsChatGPTAuth(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token":
		return true
	default:
		return false
	}
}

// Account returns the account metadata captured when the session opened.
func (s *AnalyticsSession) Account() AnalyticsAccount {
	if s == nil {
		return AnalyticsAccount{}
	}
	return s.account
}

// EnsureIdentity reloads local credentials and rejects the session once the
// signed-in account or user changes (Rust ensure_identity).
func (s *AnalyticsSession) EnsureIdentity() error {
	if s == nil {
		return errors.New(analyticsAccountChangedMessage)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reloaded, err := auth.NewStoreWithOptions(s.codexHome, s.storeOpts).Load()
	if err != nil || reloaded == nil ||
		auth.AccountIDFromAuthForRestrictions(reloaded) != s.account.ID ||
		auth.ChatGPTUserIDFromAuth(reloaded) != s.userID {
		return errors.New(analyticsAccountChangedMessage)
	}
	s.auth = reloaded
	return nil
}

func (s *AnalyticsSession) snapshot() *auth.AuthDotJSON {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auth
}

// client builds a backend client from the current credentials so a reloaded
// token is used on the next attempt, mirroring Rust's auth provider.
func (s *AnalyticsSession) client() (*chatgptapi.CloudClient, error) {
	headers, err := model.AuthHeadersFromAuth(*s.snapshot())
	if err != nil {
		return nil, err
	}
	return chatgptapi.NewCloudClientWithoutRedirects(&chatgptapi.CloudClientOptions{
		BaseURL:    s.baseURL,
		Headers:    headers.Headers,
		HTTPClient: s.httpClient,
	}), nil
}

func (s *AnalyticsSession) newRecovery() *remotecontrol.UnauthorizedRecovery {
	return remotecontrol.NewUnauthorizedRecoveryForCodexHome(s.codexHome, &remotecontrol.UnauthorizedRecoveryOptions{
		StoreOptions: s.storeOpts,
	})
}

// RequestAnalytics runs one account-scoped request with bounded unauthorized
// recovery (Rust AnalyticsSession::request).
func RequestAnalytics[T any](ctx context.Context, session *AnalyticsSession, request func(context.Context, *chatgptapi.CloudClient) (T, error)) (T, error) {
	var zero T
	if session == nil {
		return zero, errors.New(analyticsSignInRequiredMessage)
	}
	recovery := session.newRecovery()
	previous, _ := remotecontrol.NewRemoteControlConnectionAuthFromSnapshot(session.snapshot())
	for {
		if err := session.EnsureIdentity(); err != nil {
			return zero, err
		}
		client, err := session.client()
		if err != nil {
			return zero, err
		}
		result, requestErr := request(ctx, client)
		if requestErr != nil && isUnauthorizedAnalyticsError(requestErr) && recovery.HasNext() {
			if _, err := recovery.Next(ctx, previous); err != nil {
				return zero, errors.New(analyticsSignInAgainMessage)
			}
			previous, _ = remotecontrol.NewRemoteControlConnectionAuthFromSnapshot(session.snapshot())
			continue
		}
		if err := session.EnsureIdentity(); err != nil {
			return zero, err
		}
		return result, requestErr
	}
}

func isUnauthorizedAnalyticsError(err error) bool {
	var statusErr *chatgptapi.HTTPStatusError
	return errors.As(err, &statusErr) && statusErr.IsStatus(http.StatusUnauthorized)
}

// Live lazily opens the account analytics session, so the dashboard can be
// built before the user signs in (Rust analytics::Live).
type Live struct {
	config  AnalyticsSessionConfig
	mu      sync.Mutex
	session *AnalyticsLiveSession
}

// AnalyticsLiveSession is an initialized Live session with its plan-specific
// credit groupings.
type AnalyticsLiveSession struct {
	Kind         AccountKind
	Backend      *AnalyticsSession
	creditGroups []int
}

// NewLive returns a Live that opens its session on first use.
func NewLive(config AnalyticsSessionConfig) *Live {
	return &Live{config: config}
}

// Session returns the cached session, creating it on first use. Failures are
// not cached, so signing in and retrying initializes the session.
func (l *Live) Session(ctx context.Context) (*AnalyticsLiveSession, error) {
	if l == nil {
		return nil, errors.New(analyticsSignInRequiredMessage)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.session != nil {
		return l.session, nil
	}
	backend, err := NewAnalyticsSession(ctx, l.config)
	if err != nil {
		return nil, err
	}
	account := backend.Account()
	l.session = &AnalyticsLiveSession{
		Kind:         AccountKindFor(account.PlanType),
		Backend:      backend,
		creditGroups: CreditGroupIndices(account.PlanType),
	}
	return l.session, nil
}

// AccountLabel renders "email · id", or just the id when no email is stored.
// It returns nil until the session is initialized.
func (l *Live) AccountLabel() *string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.session == nil {
		return nil
	}
	account := l.session.Backend.Account()
	label := account.ID
	if account.Email != nil && strings.TrimSpace(*account.Email) != "" {
		label = *account.Email + " · " + account.ID
	}
	return &label
}

// CreditGroups returns the dashboard grouping positions for the account's
// plan, defaulting to the surface grouping before initialization.
func (l *Live) CreditGroups() []int {
	if l == nil {
		return []int{0}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.session == nil {
		return []int{0}
	}
	return append([]int(nil), l.session.creditGroups...)
}
