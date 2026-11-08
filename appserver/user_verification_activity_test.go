package appserver

import (
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"codex_go/auth"
)

// activityTestJWT builds an unsigned ChatGPT access token carrying the account
// user claim Rust namespaces the platform credential with.
func activityTestJWT(accountUserID string) string {
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString([]byte(
		`{"chatgpt_account_id":"workspace-a","chatgpt_account_user_id":"`+accountUserID+`"}`)) + "."
}

func activityTestSnapshot(accessToken, refreshToken string) *auth.AuthDotJSON {
	return &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"account_id":    "workspace-a",
			"access_token":  accessToken,
			"refresh_token": refreshToken,
		},
	}
}

// activityProbeProvider blocks inside Status until the request's activity
// guard reports cancellation, or until its deadline elapses, and records which
// happened. It implements userVerificationRequestBinder so the runtime hands it
// the same func Rust passes to with_activity_check.
type activityProbeProvider struct {
	started  chan struct{}
	returned chan struct{}
	deadline time.Duration

	mu        sync.Mutex
	cancel    func() bool
	cancelled bool
}

func newActivityProbeProvider(deadline time.Duration) *activityProbeProvider {
	return &activityProbeProvider{
		started:  make(chan struct{}),
		returned: make(chan struct{}),
		deadline: deadline,
	}
}

func (p *activityProbeProvider) BindUserVerificationRequest(_ string, cancelled func() bool) UserVerificationProvider {
	p.mu.Lock()
	p.cancel = cancelled
	p.mu.Unlock()
	return p
}

func (p *activityProbeProvider) sawCancellation() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cancelled
}

func (p *activityProbeProvider) Status() (*UserVerificationStatusResponse, error) {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()
	close(p.started)
	deadline := time.Now().Add(p.deadline)
	for time.Now().Before(deadline) {
		if cancel != nil && cancel() {
			p.mu.Lock()
			p.cancelled = true
			p.mu.Unlock()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	credentialID := "credential-1"
	response := &UserVerificationStatusResponse{CredentialID: &credentialID}
	close(p.returned)
	return response, nil
}

func (p *activityProbeProvider) EnsureKey() (*UserVerificationEnrollResponse, error) {
	return &UserVerificationEnrollResponse{}, nil
}

func (p *activityProbeProvider) Delete() (*UserVerificationDeleteResponse, error) {
	return &UserVerificationDeleteResponse{}, nil
}

func (p *activityProbeProvider) Verify(UserVerificationVerifyParams) (*UserVerificationVerifyResponse, error) {
	return &UserVerificationVerifyResponse{}, nil
}

// newActivityTestRouter builds a stdio router with one cached ChatGPT
// credential and registers the given transports as initialized, the way a real
// initialize handshake would.
func newActivityTestRouter(t *testing.T, provider UserVerificationProvider, accessToken string, connectionIDs ...string) *RuntimeRouter {
	t.Helper()
	manager := auth.NewAccountManager()
	manager.ApplyAuthSnapshot(activityTestSnapshot(accessToken, "refresh-1"))
	router := NewRuntimeRouter(RuntimeServices{Account: manager, UserVerificationProvider: provider})
	router.SetRequestTransport("stdio")
	if !router.publishAuthSnapshot(manager.AuthSnapshot(), nil) {
		t.Fatal("initial auth snapshot was rejected")
	}
	for _, connectionID := range connectionIDs {
		router.rememberConnectionClientInfo(connectionID, ClientInfo{Name: "Codex Desktop"})
	}
	return router
}

// runActivityRequest dispatches one status RPC and calls mutate while the
// native provider is blocked inside Status.
func runActivityRequest(t *testing.T, router *RuntimeRouter, provider *activityProbeProvider, connectionID string, mutate func()) *Response {
	t.Helper()
	request := requestWithParams(t, IntID(1), MethodUserVerificationStatus, UserVerificationStatusParams{})
	request.ConnectionID = connectionID
	done := make(chan *Response, 1)
	go func() { done <- router.Handle(request) }()
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("native provider never started")
	}
	mutate()
	select {
	case response := <-done:
		return response
	case <-time.After(10 * time.Second):
		t.Fatal("user-verification request did not finish")
		return nil
	}
}

func assertUserVerificationCancelled(t *testing.T, response *Response) {
	t.Helper()
	if response == nil || response.Error == nil {
		t.Fatalf("response = %+v, want a cancelled error", response)
	}
	if got := response.Error.Data["type"]; got != "cancelled" {
		t.Fatalf("error data = %+v, want type cancelled", response.Error.Data)
	}
	if got := response.Error.Data["reason"]; got != string(UserVerificationCancellationInterrupted) {
		t.Fatalf("error data = %+v, want reason interrupted", response.Error.Data)
	}
	if response.Error.Message != userVerificationCancelledMessage {
		t.Fatalf("error message = %q, want %q", response.Error.Message, userVerificationCancelledMessage)
	}
}

// Rust's activity check cancels native work when the requesting connection
// closes (app-server/src/user_verification.rs:96-101).
func TestUserVerificationActivityCancelsOnConnectionClosed(t *testing.T) {
	provider := newActivityProbeProvider(5 * time.Second)
	router := newActivityTestRouter(t, provider, activityTestJWT("account-user-a"), "conn-1")

	response := runActivityRequest(t, router, provider, "conn-1", func() {
		router.ConnectionClosed("conn-1")
	})
	assertUserVerificationCancelled(t, response)
	if !provider.sawCancellation() {
		t.Fatal("provider was not cancelled by the connection close")
	}
}

// Any credential revision advances the guard, including an ordinary refresh
// that keeps the same owner.
func TestUserVerificationActivityCancelsOnAuthRevision(t *testing.T) {
	provider := newActivityProbeProvider(5 * time.Second)
	token := activityTestJWT("account-user-a")
	router := newActivityTestRouter(t, provider, token, "conn-1")

	response := runActivityRequest(t, router, provider, "conn-1", func() {
		if !router.publishAuthSnapshot(activityTestSnapshot(token, "refresh-2"), nil) {
			t.Error("refresh snapshot was rejected")
		}
	})
	assertUserVerificationCancelled(t, response)
	if !provider.sawCancellation() {
		t.Fatal("provider was not cancelled by the credential revision change")
	}
}

// A credential swapped underneath the tracker (identity changed without a
// revision) still cancels: the key namespace no longer matches.
func TestUserVerificationActivityCancelsOnIdentityChange(t *testing.T) {
	provider := newActivityProbeProvider(5 * time.Second)
	router := newActivityTestRouter(t, provider, activityTestJWT("account-user-a"), "conn-1")

	response := runActivityRequest(t, router, provider, "conn-1", func() {
		router.services.Account.ApplyAuthSnapshot(
			activityTestSnapshot(activityTestJWT("account-user-b"), "refresh-1"))
	})
	assertUserVerificationCancelled(t, response)
	if !provider.sawCancellation() {
		t.Fatal("provider was not cancelled by the identity change")
	}
}

// Another connection closing must not cancel this request.
func TestUserVerificationActivityIgnoresOtherConnections(t *testing.T) {
	provider := newActivityProbeProvider(150 * time.Millisecond)
	router := newActivityTestRouter(t, provider, activityTestJWT("account-user-a"), "conn-1")

	response := runActivityRequest(t, router, provider, "conn-1", func() {
		router.ConnectionClosed("conn-2")
	})
	if response == nil || response.Error != nil {
		t.Fatalf("response = %+v, want a successful status", response)
	}
	if provider.sawCancellation() {
		t.Fatal("an unrelated connection close cancelled the request")
	}
}

// Router.Close retires the default connection so an embedded TUI that never
// closes a transport still cancels in-flight native work.
func TestUserVerificationActivityCancelsOnRouterClose(t *testing.T) {
	provider := newActivityProbeProvider(5 * time.Second)
	router := newActivityTestRouter(t, provider, activityTestJWT("account-user-a"))

	// The embedded TUI sends requests without a ConnectionID, which the router
	// normalizes to defaultRequestConnectionID.
	response := runActivityRequest(t, router, provider, "", func() {
		if err := router.Close(); err != nil {
			t.Errorf("router close: %v", err)
		}
	})
	assertUserVerificationCancelled(t, response)
	if !provider.sawCancellation() {
		t.Fatal("provider was not cancelled by router retirement")
	}
}
