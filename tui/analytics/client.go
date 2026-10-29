package analytics

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/model"
	"codex_go/remotecontrol"
)

// Account-scoped analytics authentication mirrors Rust #45742/#45762: a session
// stays bound to the account and user signed in when it opened, loads its plan
// from the server instead of stale token claims, reloads credentials around each
// request, and gets one bounded recovery attempt after a 401.
const (
	analyticsSignInRequiredMessage       = "Sign in locally with ChatGPT to view Analytics."
	analyticsLocalSignInLoadMessage      = "Couldn't load local sign-in. Sign in with ChatGPT and retry."
	analyticsIdentityRequiredMessage     = "Analytics requires a ChatGPT account and user identity."
	analyticsAccountChangedMessage       = "Account changed. Press R to refresh Analytics."
	analyticsSignInAgainMessage          = "Sign in again to load Analytics."
	analyticsPlanLoadMessage             = "Couldn't load account plan. Press R to retry Analytics."
	analyticsReportSignInMessage         = "Sign in again to load this report."
	analyticsReportDeniedMessage         = "Access denied for this report."
	analyticsReportUnavailableMessage    = "This report endpoint is unavailable. Press R to retry."
	analyticsReportFailedMessage         = "Report request failed. Press R to retry."
	analyticsInvalidRangeMessage         = "Invalid analytics date range."
	analyticsUnsupportedBreakdownMessage = "This credit breakdown is not supported for this account type."
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
// and user, with a server plan snapshot (Rust AnalyticsSession).
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

// NewAnalyticsSession loads local ChatGPT credentials, binds the session to
// their account and user, and reads the active account's plan from the server.
// Environment API keys are deliberately ignored.
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
	}
	session := &AnalyticsSession{
		codexHome:  codexHome,
		baseURL:    config.BaseURL,
		httpClient: config.HTTPClient,
		storeOpts:  config.StoreOptions,
		auth:       snapshot,
		userID:     userID,
		account:    account,
	}
	accounts, planErr := RequestAnalytics(ctx, session, func(ctx context.Context, client *chatgptapi.CloudClient) (*chatgptapi.AccountsCheckResponse, error) {
		return client.GetAccountsCheck(ctx)
	})
	// Preserve account-switch guidance even when it interrupts plan discovery.
	if err := session.EnsureIdentity(); err != nil {
		return nil, err
	}
	if planErr != nil {
		if isUnauthorizedAnalyticsError(planErr) {
			return nil, errors.New(analyticsSignInAgainMessage)
		}
		return nil, errors.New(analyticsPlanLoadMessage)
	}
	plan, ok := analyticsPlanForAccount(accounts, accountID)
	if !ok {
		return nil, errors.New(analyticsPlanLoadMessage)
	}
	session.account.PlanType = plan
	return session, nil
}

// analyticsPlanForAccount finds the active account's server plan. Token plan
// claims are not a fallback because they can be stale.
func analyticsPlanForAccount(accounts *chatgptapi.AccountsCheckResponse, accountID string) (auth.PlanType, bool) {
	if accounts == nil {
		return "", false
	}
	for _, entry := range accounts.Accounts {
		if entry.ID != accountID || entry.PlanType == nil {
			continue
		}
		return auth.PlanTypeFromString(*entry.PlanType), true
	}
	return "", false
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
// recovery (Rust AnalyticsSession::request). A failed recovery keeps the
// original 401 so callers retain sign-in guidance.
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
			if _, recoveryErr := recovery.Next(ctx, previous); recoveryErr == nil {
				previous, _ = remotecontrol.NewRemoteControlConnectionAuthFromSnapshot(session.snapshot())
				continue
			}
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

// analyticsRequestErrorMessage maps a failed report request to the dashboard's
// guidance (Rust analytics/client.rs request_error).
func analyticsRequestErrorMessage(err error) string {
	var statusErr *chatgptapi.HTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusUnauthorized:
			return analyticsReportSignInMessage
		case http.StatusForbidden:
			return analyticsReportDeniedMessage
		case http.StatusNotFound:
			return analyticsReportUnavailableMessage
		}
	}
	return analyticsReportFailedMessage
}

type analyticsCacheKey struct {
	kind      chatgptapi.AnalyticsReportKind
	breakdown string
	limit     uint8
	start     string
	end       string
}

func analyticsCacheKeyFor(route chatgptapi.AnalyticsReport, start time.Time, end time.Time) analyticsCacheKey {
	if route.Kind == chatgptapi.AnalyticsReportCredits {
		return analyticsCacheKey{kind: route.Kind}
	}
	return analyticsCacheKey{
		kind:      route.Kind,
		breakdown: route.Breakdown,
		limit:     route.Limit,
		start:     analyticsDateString(start),
		end:       analyticsDateString(end),
	}
}

// analyticsRoute selects the backend endpoint for a report, grouping, and
// server plan. Account type selects billing semantics, never activity-report
// eligibility (Rust analytics/client.rs route).
func analyticsRoute(report AccountAnalyticsReport, grouping AccountAnalyticsGrouping, plan auth.PlanType) (chatgptapi.AnalyticsReport, bool) {
	kind := AccountKindFor(plan)
	enterprise := kind == AccountKindEnterprise
	switch report {
	case AccountAnalyticsReportUsage:
		switch {
		case enterprise:
			return chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportEnterpriseTokens}, true
		case kind == AccountKindBusiness:
			return chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportWorkspaceCredits}, true
		default:
			return chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportUsage}, true
		}
	case AccountAnalyticsReportMessages:
		return chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportMessages}, true
	case AccountAnalyticsReportCredits:
		if !analyticsCreditGroupingSupported(grouping, plan) {
			return chatgptapi.AnalyticsReport{}, false
		}
		if enterprise {
			breakdown, ok := map[AccountAnalyticsGrouping]string{
				AccountAnalyticsGroupingSurface:   "product",
				AccountAnalyticsGroupingModel:     "model",
				AccountAnalyticsGroupingSpeed:     "speed",
				AccountAnalyticsGroupingReasoning: "reasoning_effort",
			}[grouping]
			if !ok {
				return chatgptapi.AnalyticsReport{}, false
			}
			return chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportEnterpriseCredits, Breakdown: breakdown}, true
		}
		if kind == AccountKindBusiness {
			return chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportWorkspaceCredits}, true
		}
		return chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportCredits}, true
	case AccountAnalyticsReportPlugins:
		return chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportPlugins, Limit: analyticsToolLimit(enterprise, 8, 10)}, true
	case AccountAnalyticsReportSkills:
		return chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportSkills, Limit: analyticsToolLimit(enterprise, 6, 10)}, true
	}
	return chatgptapi.AnalyticsReport{}, false
}

func analyticsCreditGroupingSupported(grouping AccountAnalyticsGrouping, plan auth.PlanType) bool {
	for _, candidate := range CreditGroupings(plan) {
		if candidate == grouping {
			return true
		}
	}
	return false
}

func analyticsToolLimit(enterprise bool, enterpriseLimit uint8, consumerLimit uint8) uint8 {
	if enterprise {
		return enterpriseLimit
	}
	return consumerLimit
}

func analyticsDateString(date time.Time) string {
	return date.UTC().Format("2006-01-02")
}

// analyticsStartDate computes the inclusive start of a days-long range. The
// bounds mirror chrono's NaiveDate so an unrepresentable range is rejected
// instead of silently wrapping (Rust checked_sub_days).
func analyticsStartDate(end time.Time, days uint32) (time.Time, bool) {
	if days == 0 {
		return time.Time{}, false
	}
	start := end.AddDate(0, 0, -int(int64(days)-1))
	if start.Year() < -262144 || start.Year() > 262143 {
		return time.Time{}, false
	}
	return start, true
}

// Live lazily opens the account analytics session, so the dashboard can be
// built before the user signs in (Rust analytics::Live).
type Live struct {
	config          AnalyticsSessionConfig
	endDate         time.Time
	mu              sync.Mutex
	session         *AnalyticsLiveSession
	tokenModelsMu   sync.RWMutex
	tokenModels     []string
	attributedUsage atomic.Bool
}

// AnalyticsLiveSession is an initialized Live session with its plan-specific
// credit groupings and account-scoped response cache.
type AnalyticsLiveSession struct {
	Kind         AccountKind
	Backend      *AnalyticsSession
	creditGroups []int
	cacheMu      sync.Mutex
	cache        map[analyticsCacheKey]analyticsData
}

// NewLive returns a Live that opens its session on first use. Every report uses
// endDate until a new Live replaces it.
func NewLive(config AnalyticsSessionConfig, endDate time.Time) *Live {
	return &Live{
		config:  config,
		endDate: analyticsDateOnly(endDate),
	}
}

func analyticsDateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// EndDate is the captured end date used by every report in this view.
func (l *Live) EndDate() time.Time {
	if l == nil {
		return time.Time{}
	}
	return l.endDate
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
		cache:        map[analyticsCacheKey]analyticsData{},
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

// AttributedUsage reports whether the last usage report carried complete
// attribution.
func (l *Live) AttributedUsage() bool {
	if l == nil {
		return false
	}
	return l.attributedUsage.Load()
}

// TokenModels returns the models seen in the last enterprise token report.
func (l *Live) TokenModels() []string {
	if l == nil {
		return nil
	}
	l.tokenModelsMu.RLock()
	defer l.tokenModelsMu.RUnlock()
	return append([]string(nil), l.tokenModels...)
}

// History loads one report for the view's end date (Rust Live::history).
func (l *Live) History(ctx context.Context, report AccountAnalyticsReport, days uint32, grouping AccountAnalyticsGrouping) (*AccountAnalyticsHistory, error) {
	return l.FilteredHistory(ctx, report, days, grouping, nil)
}

// FilteredHistory loads one report with an optional token model filter. Cached
// payloads are reused across grouping changes and belong to one local account.
func (l *Live) FilteredHistory(ctx context.Context, report AccountAnalyticsReport, days uint32, grouping AccountAnalyticsGrouping, modelFilter *string) (*AccountAnalyticsHistory, error) {
	if l == nil {
		return nil, errors.New(analyticsSignInRequiredMessage)
	}
	end := l.endDate
	start, ok := analyticsStartDate(end, days)
	if !ok {
		return nil, errors.New(analyticsInvalidRangeMessage)
	}
	session, err := l.Session(ctx)
	if err != nil {
		return nil, err
	}
	if err := session.Backend.EnsureIdentity(); err != nil {
		return nil, err
	}
	enterpriseTokens := report == AccountAnalyticsReportUsage &&
		(session.Kind == AccountKindEnterprise || session.Kind == AccountKindBusiness)
	if enterpriseTokens && grouping != AccountAnalyticsGroupingModel && grouping != AccountAnalyticsGroupingTokenType {
		grouping = AccountAnalyticsGroupingTokenType
	}
	plan := session.Backend.Account().PlanType
	route, ok := analyticsRoute(report, grouping, plan)
	if !ok {
		return nil, errors.New(analyticsUnsupportedBreakdownMessage)
	}
	// Credit events have no range parameters. Other grouping changes reuse the
	// same payload.
	key := analyticsCacheKeyFor(route, start, end)
	data, cached := session.cached(key)
	var requestErr error
	if !cached {
		data, requestErr = RequestAnalytics(ctx, session.Backend, func(ctx context.Context, client *chatgptapi.CloudClient) (analyticsData, error) {
			response, err := client.GetAccountAnalytics(ctx, route, analyticsDateString(start), analyticsDateString(end))
			if err != nil {
				return analyticsData{}, err
			}
			return projectAnalyticsResponse(*response), nil
		})
	}
	if err := session.Backend.EnsureIdentity(); err != nil {
		return nil, err
	}
	if requestErr != nil {
		return nil, errors.New(analyticsRequestErrorMessage(requestErr))
	}
	history, historyErr := l.reportHistory(report, grouping, enterpriseTokens, data, start, end, modelFilter)
	if historyErr == nil {
		session.store(key, data)
	} else {
		session.evict(key)
	}
	return history, historyErr
}

func (l *Live) reportHistory(
	report AccountAnalyticsReport,
	grouping AccountAnalyticsGrouping,
	enterpriseTokens bool,
	data analyticsData,
	start time.Time,
	end time.Time,
	modelFilter *string,
) (*AccountAnalyticsHistory, error) {
	if enterpriseTokens {
		models, err := tokenHistory(data, AccountAnalyticsGroupingModel, start, end)
		if err != nil {
			return nil, err
		}
		categories := AnalyticsCategories(&models)
		keys := make([]string, 0, len(categories))
		for _, model := range categories {
			keys = append(keys, model.Key)
		}
		l.tokenModelsMu.Lock()
		l.tokenModels = keys
		l.tokenModelsMu.Unlock()
		filtered, err := tokenFilteredHistory(data, grouping, start, end, modelFilter)
		if err != nil {
			return nil, err
		}
		return &filtered, nil
	}
	attributed := false
	if report == AccountAnalyticsReportUsage {
		var err error
		attributed, err = hasCompleteAttribution(data, start, end)
		if err != nil {
			return nil, err
		}
		l.attributedUsage.Store(attributed)
		if !attributed && (grouping == AccountAnalyticsGroupingFeature || grouping == AccountAnalyticsGroupingTaskStart) {
			grouping = AccountAnalyticsGroupingSurface
		}
	}
	return history(data, report, grouping, start, end)
}

func (s *AnalyticsLiveSession) cached(key analyticsCacheKey) (analyticsData, bool) {
	if s == nil {
		return analyticsData{}, false
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	data, ok := s.cache[key]
	return data, ok
}

func (s *AnalyticsLiveSession) store(key analyticsCacheKey, data analyticsData) {
	if s == nil {
		return
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cache == nil {
		s.cache = map[analyticsCacheKey]analyticsData{}
	}
	s.cache[key] = data
}

func (s *AnalyticsLiveSession) evict(key analyticsCacheKey) {
	if s == nil {
		return
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	delete(s.cache, key)
}
