package analytics

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"codex_go/auth"
	"codex_go/chatgptapi"
)

// These tests mirror Rust #45742's analytics/client_tests.rs: sessions stay
// bound to their account and user, reload credentials, and recover once from a
// 401.

func analyticsSignIn(t *testing.T, home string, account string, user string, plan string) {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"exp":   4102444800,
		"email": "analytics@example.test",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": account,
			"chatgpt_user_id":    user,
			"chatgpt_plan_type":  plan,
		},
	})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	token := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".test"
	analyticsSaveAuth(t, home, token, token, account)
}

func analyticsSaveAuth(t *testing.T, home string, accessToken string, idToken string, account string) {
	t.Helper()
	snapshot := auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"id_token":      idToken,
			"access_token":  accessToken,
			"refresh_token": "test-refresh",
			"account_id":    account,
		},
	}
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatalf("save auth: %v", err)
	}
}

func analyticsLiveForTest(t *testing.T, server *httptest.Server, plan string) (string, *Live) {
	t.Helper()
	home := t.TempDir()
	analyticsSignIn(t, home, "account-a", "user-a", plan)
	return home, NewLive(AnalyticsSessionConfig{
		CodexHome: home,
		BaseURL:   server.URL + "/backend-api",
	})
}

func analyticsEmptyUsage(t *testing.T, ctx context.Context, client *chatgptapi.CloudClient) (int, error) {
	t.Helper()
	response, err := client.GetAccountAnalytics(ctx, chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportUsage}, "2026-09-01", "2026-09-07")
	if err != nil {
		return 0, err
	}
	if response.Usage == nil || len(response.Usage.Data) != 0 {
		t.Fatalf("analytics response = %#v, want empty usage", response)
	}
	return 1, nil
}

func TestAnalyticsRejectsIdentityChangesDuringRequestsLikeRust(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	defer server.Close()
	home, live := analyticsLiveForTest(t, server, "business")
	ctx := context.Background()
	session, err := live.Session(ctx)
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	result, err := RequestAnalytics(ctx, session.Backend, func(context.Context, *chatgptapi.CloudClient) (int, error) {
		analyticsSignIn(t, home, "account-b", "user-a", "business")
		return 123, nil
	})
	if err == nil || err.Error() != analyticsAccountChangedMessage {
		t.Fatalf("RequestAnalytics() error = %v, want %q", err, analyticsAccountChangedMessage)
	}
	if result != 0 {
		t.Fatalf("RequestAnalytics() = %d, want zero value", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("server received %d requests, want 0", requests)
	}
}

func TestAnalyticsRejectsAccountAndUserChangesBeforeRequestsLikeRust(t *testing.T) {
	for _, test := range []struct {
		name    string
		account string
		user    string
	}{
		{"account", "account-b", "user-a"},
		{"user", "account-a", "user-b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Errorf("unexpected analytics request after identity change")
			}))
			defer server.Close()
			home, live := analyticsLiveForTest(t, server, "plus")
			ctx := context.Background()
			session, err := live.Session(ctx)
			if err != nil {
				t.Fatalf("Session() error = %v", err)
			}
			analyticsSignIn(t, home, test.account, test.user, "plus")
			requested := false
			_, err = RequestAnalytics(ctx, session.Backend, func(context.Context, *chatgptapi.CloudClient) (int, error) {
				requested = true
				return 0, nil
			})
			if requested {
				t.Fatalf("request ran after the identity changed")
			}
			if err == nil || err.Error() != analyticsAccountChangedMessage {
				t.Fatalf("RequestAnalytics() error = %v, want %q", err, analyticsAccountChangedMessage)
			}
		})
	}
}

func TestAnalyticsRequiresLocalChatGPTAuthenticationLikeRust(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Errorf("unexpected analytics request without local auth")
		}))
		defer server.Close()
		home, live := analyticsLiveForTest(t, server, "plus")
		if err := os.Remove(filepath.Join(home, "auth.json")); err != nil {
			t.Fatalf("remove auth.json: %v", err)
		}
		_, err := live.Session(context.Background())
		if err == nil || err.Error() != analyticsSignInRequiredMessage {
			t.Fatalf("Session() error = %v, want %q", err, analyticsSignInRequiredMessage)
		}
	})
	t.Run("api-key-only", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Errorf("unexpected analytics request with API key auth")
		}))
		defer server.Close()
		home, live := analyticsLiveForTest(t, server, "plus")
		raw, err := json.Marshal(map[string]any{"OPENAI_API_KEY": "sk-test-only"})
		if err != nil {
			t.Fatalf("marshal api key auth: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, "auth.json"), raw, 0o600); err != nil {
			t.Fatalf("write api key auth: %v", err)
		}
		_, err = live.Session(context.Background())
		if err == nil || err.Error() != analyticsSignInRequiredMessage {
			t.Fatalf("Session() error = %v, want %q", err, analyticsSignInRequiredMessage)
		}
	})
}

func TestAnalyticsRequestsUseReloadedCredentialsLikeRust(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var authHeaders []string
	var accountHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		accountHeaders = append(accountHeaders, r.Header.Get("ChatGPT-Account-ID"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	defer server.Close()
	home, live := analyticsLiveForTest(t, server, "plus")
	ctx := context.Background()
	session, err := live.Session(ctx)
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	snapshot, err := auth.NewStore(home).Load()
	if err != nil || snapshot == nil {
		t.Fatalf("Load() = %#v, %v", snapshot, err)
	}
	idToken, _ := snapshot.Tokens["id_token"].(string)
	analyticsSaveAuth(t, home, "refreshed-access-token", idToken, "account-a")
	label := live.AccountLabel()
	if label == nil || *label != "analytics@example.test · account-a" {
		t.Fatalf("AccountLabel() = %v, want %q", label, "analytics@example.test · account-a")
	}
	if _, err := RequestAnalytics(ctx, session.Backend, func(ctx context.Context, client *chatgptapi.CloudClient) (int, error) {
		return analyticsEmptyUsage(t, ctx, client)
	}); err != nil {
		t.Fatalf("RequestAnalytics() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	wantPaths := []string{"/backend-api/wham/usage/daily-token-usage-breakdown"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("request paths = %v, want %v", paths, wantPaths)
	}
	if !reflect.DeepEqual(authHeaders, []string{"Bearer refreshed-access-token"}) {
		t.Fatalf("authorization headers = %v, want reloaded token", authHeaders)
	}
	if !reflect.DeepEqual(accountHeaders, []string{"account-a"}) {
		t.Fatalf("account headers = %v, want account-a", accountHeaders)
	}
}

func TestAnalyticsRetriesUnauthorizedRequestsLikeRust(t *testing.T) {
	var mu sync.Mutex
	var authHeaders []string
	home := t.TempDir()
	analyticsSignIn(t, home, "account-a", "user-a", "plus")
	snapshot, err := auth.NewStore(home).Load()
	if err != nil || snapshot == nil {
		t.Fatalf("Load() = %#v, %v", snapshot, err)
	}
	originalToken, _ := snapshot.Tokens["access_token"].(string)
	idToken, _ := snapshot.Tokens["id_token"].(string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Header.Get("Authorization") == "Bearer "+originalToken {
			analyticsSaveAuth(t, home, "recovered-access-token", idToken, "account-a")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	defer server.Close()
	live := NewLive(AnalyticsSessionConfig{CodexHome: home, BaseURL: server.URL + "/backend-api"})
	ctx := context.Background()
	session, err := live.Session(ctx)
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	if _, err := RequestAnalytics(ctx, session.Backend, func(ctx context.Context, client *chatgptapi.CloudClient) (int, error) {
		return analyticsEmptyUsage(t, ctx, client)
	}); err != nil {
		t.Fatalf("RequestAnalytics() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	wantHeaders := []string{"Bearer " + originalToken, "Bearer recovered-access-token"}
	if !reflect.DeepEqual(authHeaders, wantHeaders) {
		t.Fatalf("authorization headers = %v, want %v", authHeaders, wantHeaders)
	}
}

func TestAnalyticsRetriesSessionInitializationAfterSignInLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	home, live := analyticsLiveForTest(t, server, "plus")
	ctx := context.Background()
	if err := os.Remove(filepath.Join(home, "auth.json")); err != nil {
		t.Fatalf("remove auth.json: %v", err)
	}
	if _, err := live.Session(ctx); err == nil {
		t.Fatalf("Session() succeeded without local auth")
	}
	analyticsSignIn(t, home, "account-a", "user-a", "plus")
	session, err := live.Session(ctx)
	if err != nil {
		t.Fatalf("Session() after sign-in error = %v", err)
	}
	if got := session.Backend.Account().ID; got != "account-a" {
		t.Fatalf("session account = %q, want account-a", got)
	}
}

func TestAnalyticsCreditGroupingsFollowPlanLikeRust(t *testing.T) {
	for _, test := range []struct {
		name string
		plan auth.PlanType
		kind AccountKind
		want []int
	}{
		{"plus", auth.PlanPlus, AccountKindConsumer, []int{0}},
		{"free", auth.PlanFree, AccountKindConsumer, []int{0}},
		{"team", auth.PlanTeam, AccountKindBusiness, []int{0, 2, 4}},
		{"self-serve pro", auth.PlanSelfServeBusinessProlite, AccountKindBusiness, []int{0, 2, 4}},
		{"business", auth.PlanBusiness, AccountKindEnterprise, []int{0, 2, 4, 5}},
		{"enterprise", auth.PlanEnterprise, AccountKindEnterprise, []int{0, 2, 4, 5}},
		{"enterprise usage based", auth.PlanEnterpriseCBPUsageBased, AccountKindEnterprise, []int{0, 2, 4, 5}},
		{"edu", auth.PlanEdu, AccountKindEnterprise, []int{0, 2, 4, 5}},
		{"unknown", auth.PlanUnknown, AccountKindUnknown, []int{0}},
		{"missing", "", AccountKindUnknown, []int{0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := AccountKindFor(test.plan); got != test.kind {
				t.Fatalf("AccountKindFor(%q) = %v, want %v", test.plan, got, test.kind)
			}
			if got := CreditGroupIndices(test.plan); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("CreditGroupIndices(%q) = %v, want %v", test.plan, got, test.want)
			}
		})
	}
}

func TestAnalyticsLiveDefaultsBeforeInitializationLikeRust(t *testing.T) {
	live := NewLive(AnalyticsSessionConfig{CodexHome: t.TempDir()})
	if label := live.AccountLabel(); label != nil {
		t.Fatalf("AccountLabel() = %v, want nil", label)
	}
	if got := live.CreditGroups(); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("CreditGroups() = %v, want [0]", got)
	}
}
