package remotecontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"codex_go/auth"
	"codex_go/model"
	"codex_go/runtimeauth"
)

const RemoteControlAccountIDHeader = "chatgpt-account-id"

var (
	ErrRemoteControlAuthRequired        = errors.New("remote control requires ChatGPT authentication")
	ErrRemoteControlAPIKeyUnsupported   = errors.New("remote control requires ChatGPT authentication; API key auth is not supported")
	ErrRemoteControlWaitingForAccountID = errors.New("remote control enrollment is waiting for a ChatGPT account id")
	ErrRemoteControlPermissionDenied    = errors.New("remote control permission denied")
	ErrRemoteControlNotFound            = errors.New("remote control not found")
	ErrRemoteControlTimedOut            = errors.New("remote control request timed out")
	ErrRemoteControlRequestFailed       = errors.New("remote control request failed")
	ErrRemoteControlRefreshDeferred     = errors.New("remote control server token refresh deferred")
	ErrRemoteControlPairingUnavailable  = errors.New("remote control pairing is unavailable until the server token is refreshed")
	// Rust request_context: a runtime-bound login fails instead of falling back
	// to the actor credential before the runtime is initialized (WouldBlock).
	ErrRemoteControlRuntimeAuthNotInitialized = errors.New("runtime authentication is not initialized")
	// Rust validate_runtime_destination: an unrecognized OpenAI origin is a hard
	// error, never a bearer fallback.
	ErrRemoteControlRuntimeAuthUnsupportedEndpoint = errors.New("runtime auth does not support this OpenAI endpoint")
)

type RemoteControlConnectionAuth struct {
	AuthHeaders *model.AuthHeaders
	AccountID   string
	// RuntimeAuth binds this login lifetime to a runtime slot. Nil keeps the
	// actor credential, which is the behavior every non-runtime caller relies on.
	RuntimeAuth *RuntimeAuthSelection
}

// RuntimeAuthSelection mirrors the (RuntimeAuthSlot, RuntimeAuthMode) pair Rust
// captures with one remote-control login lifetime.
type RuntimeAuthSelection struct {
	Slot *RuntimeAuthSlot
	Mode runtimeauth.Mode
}

// RuntimeAuthSlot mirrors Rust RuntimeAuthSlot: it starts empty and receives the
// runtime manager once runtime auth is enabled. A request that arrives first
// fails instead of silently dropping the runtime credential.
type RuntimeAuthSlot struct {
	mu      sync.RWMutex
	manager *runtimeauth.Manager
}

// Set fills the slot once. Rust RuntimeAuthSlot::set rejects a second manager so
// a connection cannot adopt a replacement authentication owner.
func (s *RuntimeAuthSlot) Set(manager *runtimeauth.Manager) error {
	if s == nil || manager == nil {
		return fmt.Errorf("%w: runtime auth slot requires a manager", ErrInvalidRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manager != nil {
		return fmt.Errorf("%w: runtime auth slot is already filled", ErrInvalidRequest)
	}
	s.manager = manager
	return nil
}

// Get returns the manager bound to this login lifetime.
func (s *RuntimeAuthSlot) Get() *runtimeauth.Manager {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.manager
}

func NewRemoteControlConnectionAuthFromSnapshot(snapshot *auth.AuthDotJSON) (*RemoteControlConnectionAuth, error) {
	if snapshot == nil {
		return nil, ErrRemoteControlAuthRequired
	}
	if !AuthUsesCodexBackend(snapshot) {
		return nil, ErrRemoteControlAPIKeyUnsupported
	}
	headers, err := model.AuthHeadersFromAuth(*snapshot)
	if err != nil {
		return nil, err
	}
	accountID := strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(snapshot))
	if accountID == "" {
		return nil, ErrRemoteControlWaitingForAccountID
	}
	return &RemoteControlConnectionAuth{
		AuthHeaders: &headers,
		AccountID:   accountID,
	}, nil
}

// WithRuntimeAuth binds every connection auth a loader produces to one runtime
// slot, mirroring Rust RemoteControlAuth::capture, which stores the runtime pair
// with the captured login. The slot is read per request, so rotating the runtime
// takes effect without reloading the connection.
func WithRuntimeAuth(loader RemoteControlAuthLoader, selection *RuntimeAuthSelection) RemoteControlAuthLoader {
	if loader == nil || selection == nil {
		return loader
	}
	return func(ctx context.Context) (*RemoteControlConnectionAuth, error) {
		connectionAuth, err := loader(ctx)
		if err != nil || connectionAuth == nil {
			return connectionAuth, err
		}
		bound := *connectionAuth
		bound.RuntimeAuth = selection
		return &bound, nil
	}
}

func NewRemoteControlAuthLoaderForCodexHome(codexHome string) RemoteControlAuthLoader {
	return func(ctx context.Context) (*RemoteControlConnectionAuth, error) {
		resolved, err := auth.NewStore(codexHome).Resolve()
		if err != nil {
			return nil, err
		}
		if resolved == nil {
			return nil, ErrRemoteControlAuthRequired
		}
		return NewRemoteControlConnectionAuthFromSnapshot(&resolved.Auth)
	}
}

func NewRemoteControlAuthRecoveryForCodexHome(codexHome string) RemoteControlAuthRecovery {
	return NewRemoteControlAuthRecoveryForCodexHomeWithOptions(codexHome, nil)
}

func NewRemoteControlAuthRecoveryForCodexHomeWithOptions(codexHome string, options *UnauthorizedRecoveryOptions) RemoteControlAuthRecovery {
	return NewUnauthorizedRecoveryControllerForCodexHome(codexHome, options).Recover
}

// The recovery plan's vocabulary is shared with the model client's 401 handling
// (codex-login's manager.rs owns both in Rust).
type UnauthorizedRecoveryMode = auth.UnauthorizedRecoveryMode

type UnauthorizedRecoveryStep = auth.UnauthorizedRecoveryStep

const (
	UnauthorizedRecoveryModeManaged  = auth.UnauthorizedRecoveryModeManaged
	UnauthorizedRecoveryModeExternal = auth.UnauthorizedRecoveryModeExternal
)

const (
	UnauthorizedRecoveryStepReload          = auth.UnauthorizedRecoveryStepReload
	UnauthorizedRecoveryStepRefreshToken    = auth.UnauthorizedRecoveryStepRefreshToken
	UnauthorizedRecoveryStepExternalRefresh = auth.UnauthorizedRecoveryStepExternalRefresh
	UnauthorizedRecoveryStepDone            = auth.UnauthorizedRecoveryStepDone
)

type UnauthorizedRecoveryOptions struct {
	StoreOptions    *auth.StoreOptions
	ExternalRefresh RemoteControlAuthRecovery
	Observer        RemoteControlAuthRecoveryObserver
}

type UnauthorizedRecovery struct {
	codexHome         string
	storeOptions      *auth.StoreOptions
	externalRefresh   RemoteControlAuthRecovery
	step              UnauthorizedRecoveryStep
	mode              UnauthorizedRecoveryMode
	expectedAccountID string
	authSnapshot      *auth.AuthDotJSON
}

type UnauthorizedRecoveryController struct {
	mu               sync.Mutex
	codexHome        string
	options          *UnauthorizedRecoveryOptions
	recovery         *UnauthorizedRecovery
	authStateChanged bool
}

type RemoteControlAuthRecoveryEvent struct {
	Mode              string
	Step              string
	UnavailableReason string
	AuthStateChanged  *bool
	Err               error
}

type RemoteControlAuthRecoveryObserver func(RemoteControlAuthRecoveryEvent)

type UnauthorizedRecoveryStepResult struct {
	auth             *RemoteControlConnectionAuth
	authStateChanged *bool
}

// refreshTokenAccountMismatchMessage mirrors codex-login's
// REFRESH_TOKEN_ACCOUNT_MISMATCH_MESSAGE, owned by the auth package.
const refreshTokenAccountMismatchMessage = auth.RefreshTokenAccountMismatchMessage

func NewUnauthorizedRecoveryControllerForCodexHome(codexHome string, options *UnauthorizedRecoveryOptions) *UnauthorizedRecoveryController {
	return &UnauthorizedRecoveryController{
		codexHome: strings.TrimSpace(codexHome),
		options:   cloneUnauthorizedRecoveryOptions(options),
	}
}

func (c *UnauthorizedRecoveryController) Recover(ctx context.Context, previous *RemoteControlConnectionAuth) (*RemoteControlConnectionAuth, bool, error) {
	if c == nil {
		return nil, false, nil
	}
	c.mu.Lock()
	if c.recovery == nil || !c.recovery.HasNext() {
		c.recovery = NewUnauthorizedRecoveryForCodexHome(c.codexHome, c.options)
	}
	current := c.recovery
	if !current.HasNext() {
		event := current.authRecoveryEvent(nil, nil)
		c.mu.Unlock()
		c.notify(event)
		return nil, false, nil
	}
	mode := current.ModeName()
	step := current.StepName()
	result, err := current.Next(ctx, previous)
	event := current.authRecoveryEvent(result, err)
	event.Mode = mode
	event.Step = step
	if event.AuthStateChanged != nil && *event.AuthStateChanged {
		c.authStateChanged = true
	}
	c.mu.Unlock()
	c.notify(event)
	if err != nil {
		return nil, false, err
	}
	if result == nil || result.auth == nil {
		return nil, false, nil
	}
	return result.auth, true, nil
}

func (c *UnauthorizedRecoveryController) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.recovery = nil
	c.authStateChanged = false
	c.mu.Unlock()
}

func (c *UnauthorizedRecoveryController) ConsumeAuthStateChanged() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	changed := c.authStateChanged
	c.authStateChanged = false
	c.mu.Unlock()
	return changed
}

func (c *UnauthorizedRecoveryController) notify(event RemoteControlAuthRecoveryEvent) {
	if c == nil || c.options == nil || c.options.Observer == nil {
		return
	}
	c.options.Observer(event)
}

func NewUnauthorizedRecoveryForCodexHome(codexHome string, options *UnauthorizedRecoveryOptions) *UnauthorizedRecovery {
	codexHome = strings.TrimSpace(codexHome)
	var storeOptions *auth.StoreOptions
	var externalRefresh RemoteControlAuthRecovery
	if options != nil {
		storeOptions = options.StoreOptions
		externalRefresh = options.ExternalRefresh
	}
	snapshot, _ := loadUnauthorizedRecoverySnapshot(codexHome, storeOptions)
	mode := UnauthorizedRecoveryModeManaged
	step := UnauthorizedRecoveryStepReload
	if snapshot != nil && snapshot.Mode() == "chatgptAuthTokens" {
		mode = UnauthorizedRecoveryModeExternal
		step = UnauthorizedRecoveryStepExternalRefresh
	}
	return &UnauthorizedRecovery{
		codexHome:         codexHome,
		storeOptions:      storeOptions,
		externalRefresh:   externalRefresh,
		step:              step,
		mode:              mode,
		expectedAccountID: strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(snapshot)),
		authSnapshot:      cloneAuthSnapshotForRemoteControl(snapshot),
	}
}

func (r *UnauthorizedRecovery) HasNext() bool {
	if r == nil {
		return false
	}
	if !supportsUnauthorizedRecovery(r.authSnapshot) {
		return false
	}
	if r.mode == UnauthorizedRecoveryModeExternal && r.externalRefresh == nil {
		return false
	}
	return r.step != UnauthorizedRecoveryStepDone
}

func (r *UnauthorizedRecovery) UnavailableReason() string {
	if r == nil || r.authSnapshot == nil {
		return "not_chatgpt_auth"
	}
	if r.authSnapshot.Mode() == "personal-access-token" {
		return "not_refreshable_auth"
	}
	if !supportsUnauthorizedRecovery(r.authSnapshot) {
		return "not_chatgpt_auth"
	}
	if r.mode == UnauthorizedRecoveryModeExternal && r.externalRefresh == nil {
		return "no_external_auth"
	}
	if r.step == UnauthorizedRecoveryStepDone {
		return "recovery_exhausted"
	}
	return "ready"
}

func (r *UnauthorizedRecovery) ModeName() string {
	if r == nil {
		return ""
	}
	if r.mode == "" {
		return string(UnauthorizedRecoveryModeManaged)
	}
	return string(r.mode)
}

func (r *UnauthorizedRecovery) StepName() string {
	if r == nil {
		return ""
	}
	if r.step == "" {
		return string(UnauthorizedRecoveryStepReload)
	}
	return string(r.step)
}

func (r *UnauthorizedRecoveryStepResult) Auth() *RemoteControlConnectionAuth {
	if r == nil {
		return nil
	}
	return r.auth
}

func (r *UnauthorizedRecoveryStepResult) AuthStateChanged() *bool {
	if r == nil {
		return nil
	}
	return r.authStateChanged
}

func (r *UnauthorizedRecovery) Next(ctx context.Context, previous *RemoteControlConnectionAuth) (*UnauthorizedRecoveryStepResult, error) {
	if r == nil || !r.HasNext() {
		return nil, fmt.Errorf("No more recovery steps available.")
	}
	switch r.step {
	case UnauthorizedRecoveryStepReload:
		return r.nextReload()
	case UnauthorizedRecoveryStepRefreshToken:
		return r.nextRefreshToken(ctx)
	case UnauthorizedRecoveryStepExternalRefresh:
		return r.nextExternalRefresh(ctx, previous)
	case UnauthorizedRecoveryStepDone:
		return nil, fmt.Errorf("No more recovery steps available.")
	default:
		return nil, fmt.Errorf("unsupported unauthorized recovery step %q", r.step)
	}
}

func (r *UnauthorizedRecovery) authRecoveryEvent(result *UnauthorizedRecoveryStepResult, err error) RemoteControlAuthRecoveryEvent {
	event := RemoteControlAuthRecoveryEvent{
		Mode:              r.ModeName(),
		Step:              r.StepName(),
		UnavailableReason: r.UnavailableReason(),
		Err:               err,
	}
	if result != nil {
		event.AuthStateChanged = result.AuthStateChanged()
	}
	return event
}

func (r *UnauthorizedRecovery) nextReload() (*UnauthorizedRecoveryStepResult, error) {
	loaded, err := loadUnauthorizedRecoverySnapshot(r.codexHome, r.storeOptions)
	if err != nil {
		return nil, err
	}
	if loaded == nil || !supportsUnauthorizedRecovery(loaded) {
		r.step = UnauthorizedRecoveryStepDone
		return nil, fmt.Errorf(refreshTokenAccountMismatchMessage)
	}
	accountID := strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(loaded))
	if r.expectedAccountID != "" && accountID != r.expectedAccountID {
		r.step = UnauthorizedRecoveryStepDone
		return nil, fmt.Errorf(refreshTokenAccountMismatchMessage)
	}
	changed := !auth.AuthsEqualForRefresh(r.authSnapshot, loaded)
	r.authSnapshot = cloneAuthSnapshotForRemoteControl(loaded)
	r.step = UnauthorizedRecoveryStepRefreshToken
	recovered, err := NewRemoteControlConnectionAuthFromSnapshot(loaded)
	if err != nil {
		return nil, err
	}
	return &UnauthorizedRecoveryStepResult{
		auth:             recovered,
		authStateChanged: remoteControlBoolPtr(changed),
	}, nil
}

func (r *UnauthorizedRecovery) nextRefreshToken(ctx context.Context) (*UnauthorizedRecoveryStepResult, error) {
	// Rust #53991: UnauthorizedRecovery's refresh step reports
	// `unauthorized_recovery` rather than the `forced` default.
	refreshed, err := auth.RefreshChatGPTTokens(ctx, &auth.RefreshChatGPTTokenOptions{
		CodexHome:    r.codexHome,
		AuthSnapshot: r.authSnapshot,
		StoreOptions: r.storeOptions,
		Reason:       auth.RefreshReasonUnauthorizedRecovery,
	})
	if err != nil {
		return nil, err
	}
	r.authSnapshot = cloneAuthSnapshotForRemoteControl(refreshed)
	r.step = UnauthorizedRecoveryStepDone
	recovered, err := NewRemoteControlConnectionAuthFromSnapshot(refreshed)
	if err != nil {
		return nil, err
	}
	return &UnauthorizedRecoveryStepResult{
		auth:             recovered,
		authStateChanged: remoteControlBoolPtr(true),
	}, nil
}

func (r *UnauthorizedRecovery) nextExternalRefresh(ctx context.Context, previous *RemoteControlConnectionAuth) (*UnauthorizedRecoveryStepResult, error) {
	if r.externalRefresh == nil {
		return nil, fmt.Errorf("external auth is not configured")
	}
	recovered, ok, err := r.externalRefresh(ctx, previous)
	if err != nil {
		return nil, err
	}
	if !ok || recovered == nil {
		return nil, fmt.Errorf("external auth recovery did not return auth")
	}
	r.step = UnauthorizedRecoveryStepDone
	return &UnauthorizedRecoveryStepResult{
		auth:             recovered,
		authStateChanged: remoteControlBoolPtr(true),
	}, nil
}

func loadUnauthorizedRecoverySnapshot(codexHome string, storeOptions *auth.StoreOptions) (*auth.AuthDotJSON, error) {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil, nil
	}
	return auth.NewStoreWithOptions(codexHome, storeOptions).Load()
}

func supportsUnauthorizedRecovery(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens":
		return true
	default:
		return false
	}
}

func cloneAuthSnapshotForRemoteControl(snapshot *auth.AuthDotJSON) *auth.AuthDotJSON {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	if snapshot.Tokens != nil {
		clone.Tokens = make(map[string]any, len(snapshot.Tokens))
		for key, value := range snapshot.Tokens {
			clone.Tokens[key] = value
		}
	}
	return &clone
}

func remoteControlBoolPtr(value bool) *bool {
	return &value
}

func cloneUnauthorizedRecoveryOptions(options *UnauthorizedRecoveryOptions) *UnauthorizedRecoveryOptions {
	if options == nil {
		return nil
	}
	clone := *options
	return &clone
}

func AuthUsesCodexBackend(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "agent-identity", "agentIdentity", "personal-access-token", "personalAccessToken":
		return true
	default:
		return false
	}
}

func (a *RemoteControlConnectionAuth) RequestHeaders() (http.Header, error) {
	if a == nil {
		return nil, ErrRemoteControlAuthRequired
	}
	accountID := strings.TrimSpace(a.AccountID)
	if accountID == "" {
		return nil, ErrRemoteControlWaitingForAccountID
	}
	if !validHTTPHeaderValue(accountID) {
		return nil, fmt.Errorf("%w: invalid remote control account id header", ErrInvalidRequest)
	}
	headers := http.Header{}
	if a.AuthHeaders != nil {
		for key, values := range a.AuthHeaders.Headers {
			for _, value := range values {
				headers.Add(key, value)
			}
		}
	}
	headers.Set(RemoteControlAccountIDHeader, accountID)
	return headers, nil
}

// RequestHeadersForEndpoint resolves the credential for one request destination
// (Rust RemoteControlConnectionAuth::request_context). A login lifetime bound to
// runtime auth sends the runtime assertion instead of the actor credential for
// first-party destinations, and fails rather than falling back when the runtime
// is not initialized or the destination is an unsupported OpenAI origin.
func (a *RemoteControlConnectionAuth) RequestHeadersForEndpoint(ctx context.Context, endpoint string) (http.Header, error) {
	headers, _, err := a.headersForEndpoint(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return headers, nil
}

// headersForEndpoint builds the request headers and reports the runtime
// selection used for them, so the caller can skip the actor credential that the
// runtime just replaced.
func (a *RemoteControlConnectionAuth) headersForEndpoint(ctx context.Context, endpoint string) (http.Header, *runtimeauth.Auth, error) {
	headers, err := a.RequestHeaders()
	if err != nil {
		return nil, nil, err
	}
	selected, err := a.runtimeAuthForEndpoint(ctx, endpoint)
	if err != nil {
		return nil, nil, err
	}
	if selected == nil {
		return headers, nil, nil
	}
	runtimeHeaders, err := selected.Headers(a.RuntimeAuth.Mode)
	if err != nil {
		return nil, nil, err
	}
	// Runtime headers replace the actor credential, so switching modes or
	// rotating a runtime never leaves the previous credential behind
	// (Rust replace_auth_headers).
	runtimeauth.ReplaceAuthHeaders(headers, runtimeHeaders)
	// The login lifetime's account id stays authoritative; the runtime has to
	// agree with it (Rust request_headers followed by validate_headers).
	headers.Set(RemoteControlAccountIDHeader, strings.TrimSpace(a.AccountID))
	if err := validateRuntimeSelectors(headers, selected.Instance().Actor()); err != nil {
		return nil, nil, err
	}
	return headers, selected, nil
}

// runtimeAuthForEndpoint selects the runtime for one destination. Rust rejects
// unsupported OpenAI origins, skips runtime auth for other providers, and reads
// the slot on every request so a rotation takes effect immediately.
func (a *RemoteControlConnectionAuth) runtimeAuthForEndpoint(ctx context.Context, endpoint string) (*runtimeauth.Auth, error) {
	if a == nil || a.RuntimeAuth == nil || a.RuntimeAuth.Slot == nil {
		return nil, nil
	}
	// An unrecognized OpenAI origin must not silently fall back to legacy auth.
	if runtimeauth.ClassifyOpenAIDestination(endpoint) == runtimeauth.DestinationUnsupported {
		return nil, ErrRemoteControlRuntimeAuthUnsupportedEndpoint
	}
	// Custom backends keep their own authentication.
	if !runtimeauth.IsOpenAIAPIURL(endpoint) {
		return nil, nil
	}
	manager := a.RuntimeAuth.Slot.Get()
	if manager == nil {
		return nil, ErrRemoteControlRuntimeAuthNotInitialized
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// The runtime is selected against the manager's current credential source,
	// so a replaced login re-registers or fails instead of continuing with
	// another authentication owner.
	return manager.AuthWithSelection(ctx, runtimeauth.Selection{})
}

// validateRuntimeSelectors mirrors Rust validate_runtime_selectors: selectors
// that disagree with the registered runtime actor are rejected instead of being
// sent to the backend.
func validateRuntimeSelectors(headers http.Header, actor runtimeauth.ActorInfo) error {
	validate := func(name string, expected *string) error {
		values := runtimeSelectorValues(headers, name)
		if len(values) == 0 {
			return nil
		}
		if expected == nil || values[0] != *expected {
			return fmt.Errorf("configured %s does not match the registered runtime", name)
		}
		return nil
	}
	switch actor.Kind {
	case runtimeauth.ActorChatGPT:
		accountID := actor.AccountID
		if err := validate(RemoteControlAccountIDHeader, &accountID); err != nil {
			return err
		}
		if err := validate("openai-organization", nil); err != nil {
			return err
		}
		return validate("openai-project", nil)
	case runtimeauth.ActorPlatform:
		organizationID := actor.OrganizationID
		if err := validate(RemoteControlAccountIDHeader, nil); err != nil {
			return err
		}
		return validate("openai-organization", &organizationID)
	default:
		return nil
	}
}

func runtimeSelectorValues(headers http.Header, name string) []string {
	for key, values := range headers {
		if strings.EqualFold(key, name) {
			return values
		}
	}
	return nil
}

func (a *RemoteControlConnectionAuth) ApplyRequest(ctx context.Context, request *http.Request, body []byte) ([]byte, error) {
	if request == nil || request.URL == nil {
		return nil, fmt.Errorf("%w: remote control request is nil", ErrInvalidRequest)
	}
	headers, selected, err := a.headersForEndpoint(ctx, request.URL.String())
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		request.Header.Del(key)
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	// A runtime selection replaced the actor credential outright; re-applying
	// AuthHeaders here would append the bearer the runtime just removed.
	if selected == nil && a.AuthHeaders != nil {
		signed, err := a.AuthHeaders.ApplyRequest(ctx, request, body)
		if err != nil {
			return nil, err
		}
		if signed != nil && signed.Body != nil {
			body = signed.Body
		}
	}
	request.Header.Set(RemoteControlAccountIDHeader, strings.TrimSpace(a.AccountID))
	return body, nil
}

func validHTTPHeaderValue(value string) bool {
	for _, r := range value {
		if r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
