package runtimeauth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/auth"
)

func chatgptSnapshot(token string) *auth.AuthDotJSON {
	return &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token": token,
			"account_id":   "acct_1",
		},
	}
}

func apiKeySnapshot(key string) *auth.AuthDotJSON {
	return &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: key}
}

type recordedRequest struct {
	idempotency string
	body        RegistrationRequest
	raw         []byte
}

type fakeRegistrationServer struct {
	mu       sync.Mutex
	requests []recordedRequest
	handle   func(req recordedRequest, attempt int) (int, string)
	server   *httptest.Server
}

func newFakeRegistrationServer(t *testing.T) *fakeRegistrationServer {
	t.Helper()
	fake := &fakeRegistrationServer{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body RegistrationRequest
		_ = json.Unmarshal(raw, &body)
		recorded := recordedRequest{idempotency: r.Header.Get("Idempotency-Key"), body: body, raw: raw}
		fake.mu.Lock()
		fake.requests = append(fake.requests, recorded)
		attempt := len(fake.requests)
		handle := fake.handle
		fake.mu.Unlock()
		status := http.StatusOK
		payload := `{"runtime_agent_instance_id":"rai_1","agent_host_id":"host_1","actor":{"kind":"chatgpt","user_id":"user_1","account_id":"acct_1"}}`
		if handle != nil {
			status, payload = handle(recorded, attempt)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeRegistrationServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeRegistrationServer) at(index int) recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[index]
}

func TestRegistrationRequestWireShapeLikeRust(t *testing.T) {
	registration, err := NewRegistration(chatgptSnapshot("token-1"), Selection{}, "11111111-1111-4111-8111-111111111111", Metadata{Harness: "codex-go", HarnessVersion: "1.2.3"})
	if err != nil {
		t.Fatalf("NewRegistration() error = %v", err)
	}
	raw, err := json.Marshal(registration.request)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	actor := decoded["actor_auth"].(map[string]any)
	if actor["type"] != "oauth" || actor["credential"] != "token-1" {
		t.Fatalf("actor_auth = %#v", actor)
	}
	host := decoded["agent_host_auth"].(map[string]any)
	if host["type"] != "installation_id" || host["installation_id"] != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("agent_host_auth = %#v", host)
	}
	publicKey, _ := decoded["public_key"].(string)
	if !strings.HasPrefix(publicKey, "ssh-ed25519 ") {
		t.Fatalf("public_key = %q", publicKey)
	}
	metadata := decoded["metadata"].(map[string]any)
	if metadata["role"] != "orchestrator_and_executor" || metadata["harness"] != "codex-go" || metadata["harness_version"] != "1.2.3" {
		t.Fatalf("metadata = %#v", metadata)
	}

	personal, err := NewRegistration(&auth.AuthDotJSON{AuthMode: "personal-access-token", PersonalAccessToken: "pat-1"}, Selection{}, "id", Metadata{})
	if err != nil {
		t.Fatalf("personal access token registration: %v", err)
	}
	raw, _ = json.Marshal(personal.request)
	_ = json.Unmarshal(raw, &decoded)
	actor = decoded["actor_auth"].(map[string]any)
	if actor["type"] != "personal_access_token" || actor["credential"] != "pat-1" {
		t.Fatalf("personal access token actor_auth = %#v", actor)
	}

	platform, err := NewRegistration(apiKeySnapshot("sk-key"), Selection{OrganizationID: "org-1", ProjectID: "proj-1"}, "id", Metadata{})
	if err != nil {
		t.Fatalf("api key registration: %v", err)
	}
	raw, _ = json.Marshal(platform.request)
	_ = json.Unmarshal(raw, &decoded)
	actor = decoded["actor_auth"].(map[string]any)
	if actor["type"] != "platform_api_key" || actor["credential"] != "sk-key" || actor["organization_id"] != "org-1" || actor["project_id"] != "proj-1" {
		t.Fatalf("platform actor_auth = %#v", actor)
	}
}

func TestRegistrationRejectsSelectionWithoutAPIKeyLikeRust(t *testing.T) {
	if _, err := NewRegistration(chatgptSnapshot("token"), Selection{ProjectID: "proj-1"}, "id", Metadata{}); !errors.Is(err, ErrUnsupportedResourceSelect) {
		t.Fatalf("selection error = %v, want ErrUnsupportedResourceSelect", err)
	}
	if _, err := NewRegistration(&auth.AuthDotJSON{AuthMode: "agent-identity"}, Selection{}, "id", Metadata{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("agent identity error = %v, want ErrUnsupported", err)
	}
}

func TestAssertionIsSignedV1JWS(t *testing.T) {
	registration, err := NewRegistration(chatgptSnapshot("token"), Selection{}, "id", Metadata{})
	if err != nil {
		t.Fatalf("NewRegistration() error = %v", err)
	}
	instance := registration.finish(&RegistrationResponse{RuntimeAgentInstanceID: "rai_42", AgentHostID: "host", Actor: ActorInfo{Kind: ActorChatGPT, AccountID: "acct"}})
	issued := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	assertion, err := instance.Assertion(issued)
	if err != nil {
		t.Fatalf("Assertion() error = %v", err)
	}
	parts := strings.Split(assertion, ".")
	if len(parts) != 4 || parts[0] != "1" {
		t.Fatalf("assertion = %q", assertion)
	}
	protected, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode protected: %v", err)
	}
	if string(protected) != `{"alg":"EdDSA","typ":"openai-agent-assertion+jws"}` {
		t.Fatalf("protected = %s", protected)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if claims["version"] != float64(1) || claims["runtime_agent_instance"] != "rai_42" || claims["iat"] != float64(issued.Unix()) {
		t.Fatalf("claims = %#v", claims)
	}
	if id, _ := claims["id"].(string); len(id) != 36 {
		t.Fatalf("assertion id = %q", id)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	publicKey := registration.signingKey.Public().(ed25519.PublicKey)
	if !ed25519.Verify(publicKey, []byte(parts[1]+"."+parts[2]), signature) {
		t.Fatal("assertion signature does not verify")
	}
	second, err := instance.Assertion(issued)
	if err != nil {
		t.Fatalf("second Assertion() error = %v", err)
	}
	if second == assertion {
		t.Fatal("each assertion must get a fresh id")
	}
}

func TestRuntimeAuthModesAttachRustHeaders(t *testing.T) {
	registration, err := NewRegistration(chatgptSnapshot("token-abc"), Selection{}, "id", Metadata{})
	if err != nil {
		t.Fatalf("NewRegistration() error = %v", err)
	}
	instance := registration.finish(&RegistrationResponse{RuntimeAgentInstanceID: "rai", AgentHostID: "host", Actor: ActorInfo{Kind: ActorChatGPT, AccountID: "acct_9"}})
	snapshot := chatgptSnapshot("token-abc")
	snapshot.Tokens["is_fedramp_account"] = true
	value := &Auth{instance: instance, snapshot: snapshot}

	dual, err := value.Headers(ModeDualHeader)
	if err != nil {
		t.Fatalf("dual headers: %v", err)
	}
	if got := dual.Get("Authorization"); got != "Bearer token-abc" {
		t.Fatalf("dual Authorization = %q", got)
	}
	if !strings.HasPrefix(dual.Get(AssertionHeader), "1.") {
		t.Fatalf("dual assertion = %q", dual.Get(AssertionHeader))
	}
	if dual.Get("chatgpt-account-id") != "acct_9" || dual.Get("x-openai-fedramp") != "true" {
		t.Fatalf("dual account/fedramp headers = %#v", dual)
	}

	only, err := value.Headers(ModeAssertionOnly)
	if err != nil {
		t.Fatalf("assertion-only headers: %v", err)
	}
	if !strings.HasPrefix(only.Get("Authorization"), "AgentAssertion 1.") {
		t.Fatalf("assertion-only Authorization = %q", only.Get("Authorization"))
	}
	if only.Get(AssertionHeader) != "" {
		t.Fatalf("assertion-only must not send %s", AssertionHeader)
	}
}

func TestValidateRegistrationURLLikeRust(t *testing.T) {
	valid := []string{
		"https://auth.openai.com/agent/register",
		"http://127.0.0.1:8080/agent/register",
		"http://localhost:8080/register",
	}
	for _, input := range valid {
		if _, err := ValidateRegistrationURL(input); err != nil {
			t.Fatalf("ValidateRegistrationURL(%q) error = %v", input, err)
		}
	}
	invalid := []string{
		"http://example.com/register",
		"https://user:pass@auth.openai.com/register",
		"https://auth.openai.com/register?x=1",
		"https://auth.openai.com/register#frag",
		"",
	}
	for _, input := range invalid {
		if _, err := ValidateRegistrationURL(input); !errors.Is(err, ErrInvalidEndpoint) {
			t.Fatalf("ValidateRegistrationURL(%q) error = %v, want ErrInvalidEndpoint", input, err)
		}
	}
}

func TestManagerReusesRuntimePerCredentialSource(t *testing.T) {
	fake := newFakeRegistrationServer(t)
	generation := uint64(1)
	manager, err := NewManager(ManagerOptions{
		Credentials:      func(context.Context) (*auth.AuthDotJSON, error) { return chatgptSnapshot("token-1"), nil },
		SourceGeneration: func() uint64 { return generation },
		InstallationID:   "22222222-2222-4222-8222-222222222222",
		RegistrationURL:  fake.server.URL,
		HTTPClient:       fake.server.Client(),
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	first, err := manager.Auth(context.Background())
	if err != nil {
		t.Fatalf("Auth() error = %v", err)
	}
	second, err := manager.Auth(context.Background())
	if err != nil {
		t.Fatalf("second Auth() error = %v", err)
	}
	if fake.count() != 1 {
		t.Fatalf("registrations = %d, want 1 reused runtime", fake.count())
	}
	if first.InstanceID() != second.InstanceID() || first.InstanceID() != "rai_1" {
		t.Fatalf("instance ids = %q / %q", first.InstanceID(), second.InstanceID())
	}
	if fake.at(0).idempotency == "" {
		t.Fatal("registration must send an Idempotency-Key")
	}

	generation = 2
	if _, err := manager.Auth(context.Background()); err != nil {
		t.Fatalf("Auth() after credential switch error = %v", err)
	}
	if fake.count() != 2 {
		t.Fatalf("registrations after switch = %d, want 2", fake.count())
	}
}

func TestManagerRetriesPendingRegistrationWithSameKeyLikeRust(t *testing.T) {
	fake := newFakeRegistrationServer(t)
	fake.handle = func(recorded recordedRequest, attempt int) (int, string) {
		if attempt == 1 {
			return http.StatusBadGateway, `{"error":{"code":"runtime_verification_unavailable"}}`
		}
		return http.StatusOK, `{"runtime_agent_instance_id":"rai_2","agent_host_id":"host_2","actor":{"kind":"platform","user_id":"u","organization_id":"org"}}`
	}
	manager, err := NewManager(ManagerOptions{
		Credentials:      func(context.Context) (*auth.AuthDotJSON, error) { return apiKeySnapshot("sk-1"), nil },
		SourceGeneration: func() uint64 { return 1 },
		InstallationID:   "33333333-3333-4333-8333-333333333333",
		RegistrationURL:  fake.server.URL,
		HTTPClient:       fake.server.Client(),
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if _, err := manager.Auth(context.Background()); err == nil {
		t.Fatal("first registration should fail")
	}
	if _, err := manager.Auth(context.Background()); err != nil {
		t.Fatalf("retry error = %v", err)
	}
	if fake.count() != 2 {
		t.Fatalf("registrations = %d, want 2", fake.count())
	}
	first, second := fake.at(0), fake.at(1)
	if first.idempotency != second.idempotency {
		t.Fatalf("idempotency keys differ across retries: %q / %q", first.idempotency, second.idempotency)
	}
	if first.body.PublicKey != second.body.PublicKey {
		t.Fatal("a pending registration must reuse its signing key")
	}
}

func TestManagerCachesForbiddenAndRefreshesUnauthorizedLikeRust(t *testing.T) {
	fake := newFakeRegistrationServer(t)
	fake.handle = func(recorded recordedRequest, attempt int) (int, string) {
		switch attempt {
		case 1:
			return http.StatusUnauthorized, `{"error":{"code":"token_expired"}}`
		case 2:
			return http.StatusOK, `{"runtime_agent_instance_id":"rai_3","agent_host_id":"host_3","actor":{"kind":"chatgpt","user_id":"u","account_id":"acct"}}`
		default:
			return http.StatusForbidden, `{"error":{"code":"runtime_instance_revoked"}}`
		}
	}
	token := "token-old"
	refreshed := 0
	manager, err := NewManager(ManagerOptions{
		Credentials:      func(context.Context) (*auth.AuthDotJSON, error) { return chatgptSnapshot(token), nil },
		SourceGeneration: func() uint64 { return 1 },
		RefreshOnUnauthorized: func(context.Context) error {
			refreshed++
			token = "token-new"
			return nil
		},
		InstallationID:  "44444444-4444-4444-8444-444444444444",
		RegistrationURL: fake.server.URL,
		HTTPClient:      fake.server.Client(),
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if _, err := manager.Auth(context.Background()); err != nil {
		t.Fatalf("Auth() error = %v", err)
	}
	if refreshed != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshed)
	}
	actor := fake.at(1).body.ActorAuth.(map[string]any)
	if actor["credential"] != "token-new" {
		t.Fatalf("retry credential = %#v, want the refreshed token", actor)
	}
	if fake.at(0).idempotency != fake.at(1).idempotency {
		t.Fatal("a 401 retry must keep the pending idempotency key")
	}

	denied := newFakeRegistrationServer(t)
	denied.handle = func(recorded recordedRequest, attempt int) (int, string) {
		return http.StatusForbidden, `{"error":{"code":"runtime_instance_revoked"}}`
	}
	deniedManager, err := NewManager(ManagerOptions{
		Credentials:      func(context.Context) (*auth.AuthDotJSON, error) { return chatgptSnapshot("token"), nil },
		SourceGeneration: func() uint64 { return 1 },
		InstallationID:   "55555555-5555-4555-8555-555555555555",
		RegistrationURL:  denied.server.URL,
		HTTPClient:       denied.server.Client(),
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := deniedManager.Auth(context.Background()); err == nil {
			t.Fatal("forbidden registration should fail")
		}
	}
	if denied.count() != 1 {
		t.Fatalf("forbidden registrations = %d, want the denial cached", denied.count())
	}
}

func TestManagerDropsLateRegistrationAfterCredentialSwitchLikeRust(t *testing.T) {
	generation := uint64(1)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		generation = 2
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"runtime_agent_instance_id":"rai_late","agent_host_id":"host","actor":{"kind":"chatgpt","user_id":"u","account_id":"acct"}}`))
	}))
	defer server.Close()
	manager, err := NewManager(ManagerOptions{
		Credentials:      func(context.Context) (*auth.AuthDotJSON, error) { return chatgptSnapshot("token"), nil },
		SourceGeneration: func() uint64 { return generation },
		InstallationID:   "66666666-6666-4666-8666-666666666666",
		RegistrationURL:  server.URL,
		HTTPClient:       server.Client(),
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if _, err := manager.Auth(context.Background()); !errors.Is(err, ErrChanged) {
		t.Fatalf("Auth() error = %v, want ErrChanged", err)
	}
	if manager.cached != nil {
		t.Fatal("a late registration must not stay cached")
	}
}

func TestManagerAuthForSourceRejectsDifferentSourceLikeRust(t *testing.T) {
	fake := newFakeRegistrationServer(t)
	generation := uint64(4)
	manager, err := NewManager(ManagerOptions{
		Credentials:      func(context.Context) (*auth.AuthDotJSON, error) { return chatgptSnapshot("token"), nil },
		SourceGeneration: func() uint64 { return generation },
		InstallationID:   "77777777-7777-4777-8777-777777777777",
		RegistrationURL:  fake.server.URL,
		HTTPClient:       fake.server.Client(),
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if _, err := manager.AuthForSource(context.Background(), 3, Selection{}); !errors.Is(err, ErrChanged) {
		t.Fatalf("AuthForSource() error = %v, want ErrChanged", err)
	}
	if _, err := manager.AuthForSource(context.Background(), 4, Selection{}); err != nil {
		t.Fatalf("AuthForSource() with the current source error = %v", err)
	}
}
