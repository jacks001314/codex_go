package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func unsetTestEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		previous, had := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(key, previous)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

func setWorkloadIdentityEnv(t *testing.T, rule string, tokenFile string) {
	t.Helper()
	if rule != "" {
		t.Setenv(OpenAIFederationRuleIDEnv, rule)
	} else {
		unsetTestEnv(t, OpenAIFederationRuleIDEnv)
	}
	if tokenFile != "" {
		t.Setenv(OpenAIIdentityTokenFileEnv, tokenFile)
	} else {
		unsetTestEnv(t, OpenAIIdentityTokenFileEnv)
	}
}

func clearExplicitAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv(OpenAIAPIKeyEnv, "")
	t.Setenv(CodexAPIKeyEnv, "")
	t.Setenv(CodexAccessTokenEnv, "")
}

func workloadTestJWT(t *testing.T, userID string) string {
	t.Helper()
	encode := func(value map[string]any) string {
		bytes, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(bytes)
	}
	header := encode(map[string]any{"alg": "none", "typ": "JWT"})
	payload := encode(map[string]any{
		"jti": "workload-test",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "account-one",
			"chatgpt_plan_type":  "enterprise",
			"chatgpt_user_id":    userID,
			"user_id":            userID,
		},
	})
	return header + "." + payload + ".sig"
}

func workloadTestSuccessResponse(t *testing.T, accessToken string, accountUserID string, userID string) string {
	t.Helper()
	return fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","expires_in":600,"scope":"model.request","chatgpt_account_id":"account-one","chatgpt_account_user_id":%q,"chatgpt_plan_type":"enterprise","user_id":%q}`, accessToken, accountUserID, userID)
}

func workloadTestSessionConfig(t *testing.T, tokenURL string) workloadIdentitySessionConfig {
	t.Helper()
	assertionFile := filepath.Join(t.TempDir(), "identity-token")
	if err := os.WriteFile(assertionFile, []byte("assertion-one"), 0o600); err != nil {
		t.Fatal(err)
	}
	return workloadIdentitySessionConfig{
		assertionFile:    assertionFile,
		environment:      workloadEnvironmentStaging,
		federationRuleID: "rule-one",
		httpClient:       &http.Client{Timeout: time.Second},
		tokenURL:         tokenURL,
	}
}

func workloadTestAuth(t *testing.T, registry *workloadIdentitySessionRegistry, tokenURL string) (*WorkloadIdentityAuth, error) {
	t.Helper()
	return newWorkloadIdentityAuthForRegistry(workloadTestSessionConfig(t, tokenURL), registry)
}

func TestWorkloadIdentityMarkersSelectAndPartialConfigFailsClosed(t *testing.T) {
	env := workloadIdentityProcessEnv{}
	if config, err := resolveWorkloadIdentityConfig("https://chatgpt.com/backend-api", env, true); err != nil || config != nil {
		t.Fatalf("no markers: config = %+v, err = %v", config, err)
	}
	partial := workloadIdentityProcessEnv{hasFederationRule: true, federationRuleID: "partial"}
	if _, err := resolveWorkloadIdentityConfig("https://chatgpt.com/backend-api", partial, true); err == nil || !strings.Contains(err.Error(), OpenAIIdentityTokenFileEnv) {
		t.Fatalf("partial workload identity marker should fail closed: err = %v", err)
	}

	for _, test := range []struct {
		env     workloadIdentityProcessEnv
		missing string
	}{
		{env: workloadIdentityProcessEnv{hasIdentityToken: true, identityTokenFile: "/tmp/token"}, missing: OpenAIFederationRuleIDEnv},
		{env: workloadIdentityProcessEnv{hasFederationRule: true, federationRuleID: "rule-one"}, missing: OpenAIIdentityTokenFileEnv},
	} {
		_, err := resolveWorkloadIdentityConfig("https://chatgpt.com/backend-api", test.env, true)
		if err == nil || !strings.Contains(err.Error(), test.missing) {
			t.Fatalf("partial config err = %v, want mention of %s", err, test.missing)
		}
	}

	relative := workloadIdentityProcessEnv{
		hasFederationRule: true, federationRuleID: "rule-one",
		hasIdentityToken: true, identityTokenFile: "relative.jwt",
	}
	_, err := resolveWorkloadIdentityConfig("https://chatgpt.com/backend-api", relative, true)
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative assertion path err = %v, want absolute-path failure", err)
	}
}

func TestWorkloadIdentityAuthPolicyAndAppEnvironmentEnforced(t *testing.T) {
	complete := workloadIdentityProcessEnv{
		hasFederationRule: true, federationRuleID: "rule-one",
		hasIdentityToken: true, identityTokenFile: filepath.Join(t.TempDir(), "identity-token"),
	}
	_, err := resolveWorkloadIdentityConfig("https://chatgpt.com/backend-api", complete, false)
	if err == nil || !strings.Contains(err.Error(), "login policy") {
		t.Fatalf("policy error = %v, want login-policy failure", err)
	}

	for _, test := range []struct {
		baseURL  string
		env      workloadIdentityEnvironment
		tokenURL string
	}{
		{"https://chatgpt.com/backend-api/", workloadEnvironmentProduction, workloadProdTokenURL},
		{"https://chatgpt.com", workloadEnvironmentProduction, workloadProdTokenURL},
		{"https://chat.openai.com/backend-api/codex", workloadEnvironmentProduction, workloadProdTokenURL},
		{"https://chatgpt-staging.com/backend-api", workloadEnvironmentStaging, workloadStagingTokenURL},
	} {
		config, err := resolveWorkloadIdentityConfig(test.baseURL, complete, true)
		if err != nil || config == nil {
			t.Fatalf("baseURL %q: config = %+v, err = %v", test.baseURL, config, err)
		}
		if config.environment != test.env || config.tokenURL != test.tokenURL {
			t.Fatalf("baseURL %q: environment = %v tokenURL = %q", test.baseURL, config.environment, config.tokenURL)
		}
	}

	_, err = resolveWorkloadIdentityConfig("https://example.invalid/backend-api", complete, true)
	if err == nil || !strings.Contains(err.Error(), "app routing") {
		t.Fatalf("untrusted environment err = %v, want app-routing failure", err)
	}
}

func TestWorkloadIdentityCompatibleAdaptersShareExchange(t *testing.T) {
	var exchanges atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		fmt.Fprint(w, workloadTestSuccessResponse(t, workloadTestJWT(t, "user-one"), "account-user-one", "user-one"))
	}))
	defer server.Close()

	registry := &workloadIdentitySessionRegistry{}
	config := workloadTestSessionConfig(t, server.URL)
	first, err := newWorkloadIdentityAuthForRegistry(config, registry)
	if err != nil {
		t.Fatalf("first adapter: %v", err)
	}
	second, err := newWorkloadIdentityAuthForRegistry(config, registry)
	if err != nil {
		t.Fatalf("second adapter: %v", err)
	}
	if first.session != second.session {
		t.Fatal("compatible adapters must share the process session")
	}
	firstAuth, err := first.ResolveAuth(context.Background())
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	secondAuth, err := second.ResolveAuth(context.Background())
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if firstAuth == nil || secondAuth == nil || ChatGPTUserIDFromAuth(firstAuth) != ChatGPTUserIDFromAuth(secondAuth) {
		t.Fatalf("shared tokens: first = %+v second = %+v", firstAuth, secondAuth)
	}
	if exchanges.Load() != 1 {
		t.Fatalf("exchanges = %d, want 1 (shared session)", exchanges.Load())
	}
}

func TestWorkloadIdentityIncompatibleSessionSettingsRejected(t *testing.T) {
	var exchanges atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		fmt.Fprint(w, workloadTestSuccessResponse(t, workloadTestJWT(t, "user-one"), "account-user-one", "user-one"))
	}))
	defer server.Close()

	registry := &workloadIdentitySessionRegistry{}
	base := workloadTestSessionConfig(t, server.URL)
	if _, err := newWorkloadIdentityAuthForRegistry(base, registry); err != nil {
		t.Fatalf("active adapter: %v", err)
	}

	differentRule := base
	differentRule.federationRuleID = "rule-two"
	differentFile := base
	differentFile.assertionFile = filepath.Join(t.TempDir(), "identity-token-two")
	if err := os.WriteFile(differentFile.assertionFile, []byte("assertion-two"), 0o600); err != nil {
		t.Fatal(err)
	}
	differentEnvironment := base
	differentEnvironment.environment = workloadEnvironmentProduction
	differentRoute := base
	differentRoute.httpClient = &http.Client{Timeout: time.Second}

	routeAdapter, err := newWorkloadIdentityAuthForRegistry(differentRoute, registry)
	if err != nil {
		t.Fatalf("route changes reuse the process-owned session: %v", err)
	}
	if routeAdapter.session != registry.entry.session {
		t.Fatal("route-only change must reuse the session")
	}
	for name, config := range map[string]workloadIdentitySessionConfig{
		"different rule": differentRule,
		"different file": differentFile,
		"different env":  differentEnvironment,
	} {
		if _, err := newWorkloadIdentityAuthForRegistry(config, registry); err != workloadErrConflictingConfiguration {
			t.Fatalf("%s: err = %v, want ConflictingConfiguration", name, err)
		}
	}
}

func TestWorkloadIdentityRefreshPreservesIdentityAndInvalidTokensReexchanged(t *testing.T) {
	var exchanges atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch exchanges.Add(1) {
		case 1:
			fmt.Fprint(w, workloadTestSuccessResponse(t, workloadTestJWT(t, "user-one"), "account-user-one", "user-one"))
		case 2:
			fmt.Fprint(w, workloadTestSuccessResponse(t, workloadTestJWT(t, "user-one"), "account-user-two", "user-one"))
		default:
			fmt.Fprint(w, workloadTestSuccessResponse(t, workloadTestJWT(t, "user-one"), "account-user-one", "user-one"))
		}
	}))
	defer server.Close()

	registry := &workloadIdentitySessionRegistry{}
	adapter, err := workloadTestAuth(t, registry, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ResolveAuth(context.Background()); err != nil {
		t.Fatalf("initial resolve: %v", err)
	}
	if _, err := adapter.RefreshAuth(context.Background(), "account-one"); err == nil {
		t.Fatal("identity change must be rejected")
	} else if !workloadIdentityErrorIsPermanent(err) {
		t.Fatalf("identity-change error must be permanent, got %v", err)
	}
	corrected, err := adapter.ResolveAuth(context.Background())
	if err != nil {
		t.Fatalf("corrected token is re-exchanged: %v", err)
	}
	if ChatGPTUserIDFromAuth(corrected) != "user-one" {
		t.Fatalf("corrected auth = %+v", corrected)
	}
	if exchanges.Load() != 3 {
		t.Fatalf("exchanges = %d, want 3", exchanges.Load())
	}
}

func TestWorkloadIdentityConcurrentRefreshesShareOneExchange(t *testing.T) {
	var exchanges atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exchanges.Add(1) == 1 {
			fmt.Fprint(w, workloadTestSuccessResponse(t, workloadTestJWT(t, "user-one"), "account-user-one", "user-one"))
			return
		}
		time.Sleep(30 * time.Millisecond)
		fmt.Fprint(w, workloadTestSuccessResponse(t, workloadTestJWT(t, "user-one"), "account-user-one", "user-one"))
	}))
	defer server.Close()

	registry := &workloadIdentitySessionRegistry{}
	config := workloadTestSessionConfig(t, server.URL)
	first, err := newWorkloadIdentityAuthForRegistry(config, registry)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newWorkloadIdentityAuthForRegistry(config, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.ResolveAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ResolveAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	var firstToken, secondToken string
	var firstErr, secondErr error
	go func() {
		defer wg.Done()
		auth, err := first.RefreshAuth(context.Background(), "account-one")
		if err == nil {
			firstToken = ChatGPTUserIDFromAuth(auth)
		}
		firstErr = err
	}()
	go func() {
		defer wg.Done()
		auth, err := second.RefreshAuth(context.Background(), "account-one")
		if err == nil {
			secondToken = ChatGPTUserIDFromAuth(auth)
		}
		secondErr = err
	}()
	wg.Wait()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("refresh errors: first = %v second = %v", firstErr, secondErr)
	}
	if firstToken != secondToken {
		t.Fatalf("tokens differ: %q vs %q", firstToken, secondToken)
	}
	if exchanges.Load() != 2 {
		t.Fatalf("exchanges = %d, want 2 (one shared refresh)", exchanges.Load())
	}
}

func TestWorkloadIdentityExchangeErrorsMapToRetryPolicy(t *testing.T) {
	cases := []struct {
		err       error
		transient bool
	}{
		{err: &workloadExchangeRejected{Status: 400}, transient: false},
		{err: &workloadExchangeRejected{Status: 408}, transient: true},
		{err: &workloadExchangeRejected{Status: 429}, transient: true},
		{err: &workloadExchangeRejected{Status: 503}, transient: true},
		{err: &workloadAssertionFileError{path: "/missing", err: os.ErrNotExist}, transient: true},
		{err: workloadErrInvalidExchangeResponse, transient: false},
		{err: workloadErrExchangeUnavailable, transient: true},
	}
	for _, test := range cases {
		if got := !workloadIdentityErrorIsPermanent(test.err); got != test.transient {
			t.Fatalf("error %v: transient = %v, want %v", test.err, got, test.transient)
		}
		if permanent := WorkloadIdentityPermanentError(test.err); permanent != nil != !test.transient {
			t.Fatalf("error %v: permanent error = %+v, transient = %v", test.err, permanent, test.transient)
		}
	}
}

func TestStoreResolveRejectsPartialWorkloadIdentityConfiguration(t *testing.T) {
	clearExplicitAuthEnv(t)
	setWorkloadIdentityEnv(t, "rule-one", "")
	store := NewStore(t.TempDir())
	_, err := store.Resolve()
	if err == nil || !strings.Contains(err.Error(), OpenAIIdentityTokenFileEnv) {
		t.Fatalf("Resolve() err = %v, want mention of %s", err, OpenAIIdentityTokenFileEnv)
	}
}

func TestStoreResolveFailsClosedWhenWorkloadIdentityMarkersOverrideExplicitEnv(t *testing.T) {
	clearExplicitAuthEnv(t)
	setWorkloadIdentityEnv(t, "rule-one", filepath.Join(t.TempDir(), "identity-token"))
	t.Setenv(OpenAIAPIKeyEnv, "sk-env-key")
	resolved, err := NewStore(t.TempDir()).Resolve()
	if resolved != nil || err == nil || !strings.Contains(err.Error(), "assertion file") {
		t.Fatalf("Resolve() = %+v, %v; want workload identity to fail closed over the API key", resolved, err)
	}
}

func TestIsWorkloadIdentitySelected(t *testing.T) {
	clearExplicitAuthEnv(t)
	unsetTestEnv(t, OpenAIFederationRuleIDEnv, OpenAIIdentityTokenFileEnv)
	if IsWorkloadIdentitySelected() {
		t.Fatal("selected without markers")
	}
	setWorkloadIdentityEnv(t, "rule-one", filepath.Join(t.TempDir(), "identity-token"))
	if !IsWorkloadIdentitySelected() {
		t.Fatal("not selected with complete markers")
	}
	t.Setenv(OpenAIAPIKeyEnv, "sk-env-key")
	if !IsWorkloadIdentitySelected() {
		t.Fatal("markers should keep workload identity selected over an explicit API key")
	}
}
