package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewWorkloadIdentityConfigValidates(t *testing.T) {
	if _, err := NewWorkloadIdentityConfig("  ", "/tmp/assertion"); err == nil {
		t.Fatal("empty federation rule id accepted")
	}
	if _, err := NewWorkloadIdentityConfig("rule-1", "relative/path"); err == nil {
		t.Fatal("relative assertion path accepted")
	}
	config, err := NewWorkloadIdentityConfig("rule-1", "/tmp/assertion")
	if err != nil {
		t.Fatalf("NewWorkloadIdentityConfig() error = %v", err)
	}
	if config.FederationRuleID != "rule-1" || config.AssertionFile != "/tmp/assertion" {
		t.Fatalf("config = %+v", config)
	}
}

func TestWorkloadTokenURLValidation(t *testing.T) {
	for _, invalid := range []string{"http://example.com/token", "ftp://example.com", "https://", "https://user@example.com/token", "https://example.com/token?q=1"} {
		if err := validateWorkloadTokenURL(mustParseURL(t, invalid)); err == nil {
			t.Fatalf("token URL %q accepted", invalid)
		}
	}
	for _, valid := range []string{"https://example.com/token", "http://localhost:8080/token", "https://127.0.0.1/token"} {
		if err := validateWorkloadTokenURL(mustParseURL(t, valid)); err != nil {
			t.Fatalf("token URL %q rejected: %v", valid, err)
		}
	}
}

func TestReadWorkloadAssertion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assertion")
	if err := os.WriteFile(path, []byte("  eyJhbGciOiJSUzI1NiJ9.rotated  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertion, err := readWorkloadAssertion(path)
	if err != nil {
		t.Fatalf("readWorkloadAssertion() error = %v", err)
	}
	if !strings.HasPrefix(assertion, "eyJ") {
		t.Fatalf("assertion = %q, want trimmed JWT", assertion)
	}
	big := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", workloadMaxAssertionBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkloadAssertion(big); err == nil {
		t.Fatal("oversized assertion accepted")
	}
}

func TestWorkloadIdentityExchangeCachesAndRefreshes(t *testing.T) {
	var exchanges atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != workloadJWTBearerGrant || !strings.Contains(r.Form.Get("assertion"), "eyJ") || r.Form.Get("federation_rule_id") != "rule-1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `{"access_token":"tok-1","chatgpt_account_id":"acct-1","chatgpt_account_user_id":"u-1","expires_in":300,"issued_token_type":"urn:ietf:params:oauth:token-type:access_token","scope":"codex","token_type":"Bearer","user_id":"user-1","chatgpt_plan_type":"plus"}`)
	}))
	defer server.Close()

	config, _ := NewWorkloadIdentityConfig("rule-1", writeAssertion(t))
	exchange, err := NewWorkloadIdentityExchange(config, server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewWorkloadIdentityExchange() error = %v", err)
	}
	first, err := exchange.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if first.AccessToken != "tok-1" || first.ChatGPTAccountID != "acct-1" || first.Version != 1 {
		t.Fatalf("token = %+v", first)
	}
	// Cached token: no second exchange.
	second, err := exchange.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() cached error = %v", err)
	}
	if exchanges.Load() != 1 {
		t.Fatalf("exchanges = %d, want 1 (cached)", exchanges.Load())
	}
	if second.AccessToken != "tok-1" {
		t.Fatalf("cached token = %+v", second)
	}
	// Refresh after rejection: version bumps.
	refreshed, err := exchange.Refresh(context.Background(), first.Version)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if refreshed.Version != 2 || exchanges.Load() != 2 {
		t.Fatalf("refreshed = %+v exchanges=%d", refreshed, exchanges.Load())
	}
}

func TestWorkloadIdentityExchangeCoalescesConcurrentResolves(t *testing.T) {
	var exchanges atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(w, `{"access_token":"tok-1","chatgpt_account_id":"acct-1","chatgpt_account_user_id":"u-1","expires_in":300,"issued_token_type":"urn:ietf:params:oauth:token-type:access_token","scope":"codex","token_type":"Bearer","user_id":"user-1"}`)
	}))
	defer server.Close()
	config, _ := NewWorkloadIdentityConfig("rule-1", writeAssertion(t))
	exchange, err := NewWorkloadIdentityExchange(config, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = exchange.Resolve(context.Background())
		}()
	}
	wg.Wait()
	if exchanges.Load() > 2 {
		t.Fatalf("exchanges = %d, want coalesced (<=2)", exchanges.Load())
	}
}

func TestWorkloadIdentityExchangeValidatesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"access_token":"","chatgpt_account_id":"acct-1"}`)
	}))
	defer server.Close()
	config, _ := NewWorkloadIdentityConfig("rule-1", writeAssertion(t))
	exchange, err := NewWorkloadIdentityExchange(config, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.Resolve(context.Background()); err == nil {
		t.Fatal("invalid exchange response accepted")
	}
}

func writeAssertion(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "assertion")
	if err := os.WriteFile(path, []byte("eyJhbGciOiJSUzI1NiJ9.assertion"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
