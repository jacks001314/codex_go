package remotecontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"codex_go/internal/auth"
	"codex_go/internal/model"
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
)

type RemoteControlConnectionAuth struct {
	AuthHeaders *model.AuthHeaders
	AccountID   string
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

type UnauthorizedRecoveryMode string

const (
	UnauthorizedRecoveryModeManaged  UnauthorizedRecoveryMode = "managed"
	UnauthorizedRecoveryModeExternal UnauthorizedRecoveryMode = "external"
)

type UnauthorizedRecoveryStep string

const (
	UnauthorizedRecoveryStepReload          UnauthorizedRecoveryStep = "reload"
	UnauthorizedRecoveryStepRefreshToken    UnauthorizedRecoveryStep = "refresh_token"
	UnauthorizedRecoveryStepExternalRefresh UnauthorizedRecoveryStep = "external_refresh"
	UnauthorizedRecoveryStepDone            UnauthorizedRecoveryStep = "done"
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

const refreshTokenAccountMismatchMessage = "Your access token could not be refreshed because you have since logged out or signed in to another account. Please sign in again."

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
	refreshed, err := auth.RefreshChatGPTTokens(ctx, &auth.RefreshChatGPTTokenOptions{
		CodexHome:    r.codexHome,
		AuthSnapshot: r.authSnapshot,
		StoreOptions: r.storeOptions,
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

func (a *RemoteControlConnectionAuth) ApplyRequest(ctx context.Context, request *http.Request, body []byte) ([]byte, error) {
	headers, err := a.RequestHeaders()
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		request.Header.Del(key)
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if a != nil && a.AuthHeaders != nil {
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
