package analytics

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
)

// These tests mirror Rust #45742/#45762's analytics client coverage: sessions
// stay bound to their account and user, use the server-reported plan, reuse
// account-scoped payloads, and recover once from a 401.

const analyticsAccountsCheckPath = "/backend-api/wham/accounts/check"

var analyticsTestEndDate = time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

func analyticsTestStartDate() time.Time {
	return analyticsTestEndDate.AddDate(0, 0, -6)
}

type analyticsRecordedRequest struct {
	path          string
	query         url.Values
	authorization string
	accountID     string
}

type analyticsRequestRecorder struct {
	mu       sync.Mutex
	requests []analyticsRecordedRequest
}

func (r *analyticsRequestRecorder) record(request *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, analyticsRecordedRequest{
		path:          request.URL.Path,
		query:         request.URL.Query(),
		authorization: request.Header.Get("Authorization"),
		accountID:     request.Header.Get("ChatGPT-Account-ID"),
	})
}

func (r *analyticsRequestRecorder) reportRequests() []analyticsRecordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	reports := []analyticsRecordedRequest{}
	for _, request := range r.requests {
		if request.path != analyticsAccountsCheckPath {
			reports = append(reports, request)
		}
	}
	return reports
}

func analyticsWriteJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func analyticsAccountsCheckBody(accountID string, plan string) string {
	return fmt.Sprintf(`{"accounts":[{"id":%q,"plan_type":%q}],"account_ordering":[%q]}`, accountID, plan, accountID)
}

// analyticsServeAccountsCheck answers the plan-discovery request and reports
// whether the request was handled.
func analyticsServeAccountsCheck(w http.ResponseWriter, request *http.Request, accountID string, plan string) bool {
	if request.URL.Path != analyticsAccountsCheckPath {
		return false
	}
	analyticsWriteJSON(w, analyticsAccountsCheckBody(accountID, plan))
	return true
}

func analyticsTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *analyticsRequestRecorder) {
	t.Helper()
	recorder := &analyticsRequestRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		recorder.record(request)
		handler(w, request)
	}))
	t.Cleanup(server.Close)
	return server, recorder
}

func analyticsSaveAuthFile(home string, accessToken string, idToken string, account string) error {
	snapshot := auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"id_token":      idToken,
			"access_token":  accessToken,
			"refresh_token": "test-refresh",
			"account_id":    account,
		},
	}
	return auth.NewStore(home).Save(snapshot)
}

func analyticsWriteSignIn(home string, account string, user string, plan string) error {
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
		return err
	}
	token := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".test"
	return analyticsSaveAuthFile(home, token, token, account)
}

func analyticsSignIn(t *testing.T, home string, account string, user string, plan string) {
	t.Helper()
	if err := analyticsWriteSignIn(home, account, user, plan); err != nil {
		t.Fatalf("sign in: %v", err)
	}
}

func analyticsLiveForTest(t *testing.T, server *httptest.Server, plan string) (string, *Live) {
	t.Helper()
	home := t.TempDir()
	analyticsSignIn(t, home, "account-a", "user-a", plan)
	return home, NewLive(AnalyticsSessionConfig{
		CodexHome: home,
		BaseURL:   server.URL + "/backend-api",
	}, analyticsTestEndDate)
}

func TestAnalyticsRejectsIdentityChangesDuringRequestsLikeRust(t *testing.T) {
	home := t.TempDir()
	analyticsSignIn(t, home, "account-a", "user-a", "business")
	server, recorder := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "business") {
			return
		}
		analyticsWriteJSON(w, `{"data": []}`)
	})
	ctx := context.Background()
	live := NewLive(AnalyticsSessionConfig{CodexHome: home, BaseURL: server.URL + "/backend-api"}, analyticsTestEndDate)
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
	if reports := recorder.reportRequests(); len(reports) != 0 {
		t.Fatalf("server received %d report requests, want 0", len(reports))
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
			server, recorder := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
				if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
					return
				}
				t.Errorf("unexpected analytics request after identity change")
			})
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
			if reports := recorder.reportRequests(); len(reports) != 0 {
				t.Fatalf("server received %d report requests, want 0", len(reports))
			}
		})
	}
}

func TestAnalyticsRequiresLocalChatGPTAuthenticationLikeRust(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
			if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
				return
			}
			t.Errorf("unexpected analytics request without local auth")
		})
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
		server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
			if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
				return
			}
			t.Errorf("unexpected analytics request with API key auth")
		})
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
	server, recorder := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
			return
		}
		analyticsWriteJSON(w, `{"data": []}`)
	})
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
	if err := analyticsSaveAuthFile(home, "refreshed-access-token", idToken, "account-a"); err != nil {
		t.Fatalf("save reloaded auth: %v", err)
	}
	label := live.AccountLabel()
	if label == nil || *label != "analytics@example.test" {
		t.Fatalf("AccountLabel() = %v, want %q", label, "analytics@example.test")
	}
	if _, err := RequestAnalytics(ctx, session.Backend, func(ctx context.Context, client *chatgptapi.CloudClient) (int, error) {
		if _, err := client.GetAccountAnalytics(ctx, chatgptapi.AnalyticsReport{Kind: chatgptapi.AnalyticsReportUsage}, analyticsTestStartDate().Format("2006-01-02"), analyticsTestEndDate.Format("2006-01-02")); err != nil {
			return 0, err
		}
		return 1, nil
	}); err != nil {
		t.Fatalf("RequestAnalytics() error = %v", err)
	}
	reports := recorder.reportRequests()
	if len(reports) != 1 {
		t.Fatalf("report requests = %d, want 1", len(reports))
	}
	if reports[0].path != "/backend-api/wham/usage/daily-token-usage-breakdown" {
		t.Fatalf("report path = %q", reports[0].path)
	}
	if reports[0].authorization != "Bearer refreshed-access-token" || reports[0].accountID != "account-a" {
		t.Fatalf("report headers = %q/%q", reports[0].authorization, reports[0].accountID)
	}
}

func TestAnalyticsRetriesUnauthorizedRequestsLikeRust(t *testing.T) {
	home := t.TempDir()
	analyticsSignIn(t, home, "account-a", "user-a", "plus")
	snapshot, err := auth.NewStore(home).Load()
	if err != nil || snapshot == nil {
		t.Fatalf("Load() = %#v, %v", snapshot, err)
	}
	originalToken, _ := snapshot.Tokens["access_token"].(string)
	idToken, _ := snapshot.Tokens["id_token"].(string)
	var mu sync.Mutex
	var authHeaders []string
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
			return
		}
		mu.Lock()
		authHeaders = append(authHeaders, request.Header.Get("Authorization"))
		mu.Unlock()
		if request.Header.Get("Authorization") == "Bearer "+originalToken {
			if err := analyticsSaveAuthFile(home, "recovered-access-token", idToken, "account-a"); err != nil {
				t.Errorf("recovered save: %v", err)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		analyticsWriteJSON(w, `{"data": []}`)
	})
	live := NewLive(AnalyticsSessionConfig{CodexHome: home, BaseURL: server.URL + "/backend-api"}, analyticsTestEndDate)
	if _, err := live.History(context.Background(), AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingSurface); err != nil {
		t.Fatalf("History() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	wantHeaders := []string{"Bearer " + originalToken, "Bearer recovered-access-token"}
	if !reflect.DeepEqual(authHeaders, wantHeaders) {
		t.Fatalf("authorization headers = %v, want %v", authHeaders, wantHeaders)
	}
}

func TestAnalyticsRetriesSessionInitializationAfterSignInLikeRust(t *testing.T) {
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
			return
		}
		analyticsWriteJSON(w, `{"data": []}`)
	})
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
func TestAnalyticsUsesServerPlanAndRoutesLikeRust(t *testing.T) {
	for _, test := range []struct {
		name     string
		plan     string
		report   AccountAnalyticsReport
		grouping AccountAnalyticsGrouping
		endpoint string
		query    url.Values
		hasRange bool
	}{
		{"consumer usage", "plus", AccountAnalyticsReportUsage, AccountAnalyticsGroupingModel, "usage/daily-token-usage-breakdown", url.Values{"group_by": {"day"}}, true},
		{"enterprise tokens", "business", AccountAnalyticsReportUsage, AccountAnalyticsGroupingTokenType, "usage/daily-workspace-user-token-usage-breakdown", url.Values{"group_by": {"day"}, "breakdown_by": {"model"}, "modes": {"codex", "work"}}, true},
		{"messages", "business", AccountAnalyticsReportMessages, AccountAnalyticsGroupingModel, "analytics/daily-workspace-usage-counts", url.Values{"group_by": {"day"}, "workspace_user": {"true"}}, true},
		{"unknown usage", "unknown", AccountAnalyticsReportUsage, AccountAnalyticsGroupingSurface, "usage/daily-token-usage-breakdown", url.Values{"group_by": {"day"}}, true},
		{"unknown plugins", "unknown", AccountAnalyticsReportPlugins, AccountAnalyticsGroupingFeature, "analytics/daily-plugin-usage-metrics", url.Values{"group_by": {"day"}, "workspace_user": {"true"}, "top_plugin_limit": {"10"}}, true},
		{"edu enterprise credits", "edu", AccountAnalyticsReportCredits, AccountAnalyticsGroupingModel, "usage/daily-workspace-user-credit-usage", url.Values{"breakdown": {"model"}}, true},
		{"consumer credits", "plus", AccountAnalyticsReportCredits, AccountAnalyticsGroupingSurface, "usage/credit-usage-events", url.Values{}, false},
		{"business usage", "team", AccountAnalyticsReportUsage, AccountAnalyticsGroupingModel, "usage/daily-workspace-user-token-usage-breakdown", url.Values{"group_by": {"day"}}, true},
		{"business credits", "team", AccountAnalyticsReportCredits, AccountAnalyticsGroupingSpeed, "usage/daily-workspace-user-token-usage-breakdown", url.Values{"group_by": {"day"}}, true},
		{"enterprise reasoning credits", "business", AccountAnalyticsReportCredits, AccountAnalyticsGroupingReasoning, "usage/daily-workspace-user-credit-usage", url.Values{"breakdown": {"reasoning_effort"}}, true},
		{"consumer messages", "plus", AccountAnalyticsReportMessages, AccountAnalyticsGroupingModel, "analytics/daily-workspace-usage-counts", url.Values{"group_by": {"day"}, "workspace_user": {"true"}}, true},
		{"enterprise plugins", "business", AccountAnalyticsReportPlugins, AccountAnalyticsGroupingFeature, "analytics/daily-plugin-usage-metrics", url.Values{"group_by": {"day"}, "workspace_user": {"true"}, "top_plugin_limit": {"8"}}, true},
		{"enterprise skills", "business", AccountAnalyticsReportSkills, AccountAnalyticsGroupingFeature, "analytics/daily-skill-usage-metrics", url.Values{"group_by": {"day"}, "workspace_user": {"true"}, "top_skill_limit": {"6"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, recorder := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
				if analyticsServeAccountsCheck(w, request, "account-a", test.plan) {
					return
				}
				if request.URL.Path == "/backend-api/wham/usage/daily-workspace-user-credit-usage" {
					analyticsWriteJSON(w, `{"breakdown": "product", "series": [], "data": []}`)
					return
				}
				analyticsWriteJSON(w, `{"data": []}`)
			})
			_, live := analyticsLiveForTest(t, server, test.plan)
			history, err := live.History(context.Background(), test.report, 7, test.grouping)
			if err != nil {
				t.Fatalf("History() error = %v", err)
			}
			if history == nil {
				t.Fatalf("History() = nil, want an empty history")
			}
			reports := recorder.reportRequests()
			if len(reports) != 1 {
				t.Fatalf("report requests = %d, want 1", len(reports))
			}
			wantPath := "/backend-api/wham/" + test.endpoint
			if reports[0].path != wantPath {
				t.Fatalf("report path = %q, want %q", reports[0].path, wantPath)
			}
			wantQuery := url.Values{}
			for key, values := range test.query {
				wantQuery[key] = append([]string(nil), values...)
			}
			if test.hasRange {
				wantQuery.Set("start_date", analyticsTestStartDate().Format("2006-01-02"))
				wantQuery.Set("end_date", analyticsTestEndDate.Format("2006-01-02"))
			}
			if !reflect.DeepEqual(reports[0].query, wantQuery) {
				t.Fatalf("report query = %v, want %v", reports[0].query, wantQuery)
			}
			if reports[0].authorization == "" || reports[0].accountID != "account-a" {
				t.Fatalf("report headers = %q/%q", reports[0].authorization, reports[0].accountID)
			}
		})
	}
}

func TestAnalyticsReusesPayloadForGroupingAndRejectsAccountChangesLikeRust(t *testing.T) {
	server, recorder := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "team") {
			return
		}
		analyticsWriteJSON(w, fmt.Sprintf(`{"data": [{"date": %q, "product_surface_usage_values": {}, "premium_usage_values": {"credit_usage_credits": {"cli": 12.5}, "total_usage_credits": {}, "uncached_text_input_tokens_by_surface": {}, "cached_text_input_tokens_by_surface": {}, "text_output_tokens_by_surface": {}, "text_total_tokens_by_surface": {}}, "models": [{"model": "model-a", "credits": 12.5, "speed": "fast", "on_demand_credits": 12.5}]}]}`, analyticsTestEndDate.Format("2006-01-02")))
	})
	home, live := analyticsLiveForTest(t, server, "team")
	ctx := context.Background()
	surface, err := live.History(ctx, AccountAnalyticsReportCredits, 7, AccountAnalyticsGroupingSurface)
	if err != nil || surface == nil {
		t.Fatalf("surface history = %#v, %v", surface, err)
	}
	model, err := live.History(ctx, AccountAnalyticsReportCredits, 7, AccountAnalyticsGroupingModel)
	if err != nil || model == nil {
		t.Fatalf("model history = %#v, %v", model, err)
	}
	surfaceTotal := surface.Data[len(surface.Data)-1].Total
	modelTotal := model.Data[len(model.Data)-1].Total
	if surfaceTotal != 12.5 || modelTotal != 12.5 {
		t.Fatalf("surface/model totals = %v/%v", surfaceTotal, modelTotal)
	}
	if reports := recorder.reportRequests(); len(reports) != 1 {
		t.Fatalf("report requests = %d, want 1 cached payload", len(reports))
	}
	analyticsSignIn(t, home, "account-a", "user-b", "team")
	_, err = live.History(ctx, AccountAnalyticsReportCredits, 7, AccountAnalyticsGroupingModel)
	if err == nil || err.Error() != analyticsAccountChangedMessage {
		t.Fatalf("History() after switch error = %v, want %q", err, analyticsAccountChangedMessage)
	}
}

func TestTokenModelFilterReusesAccountScopedResponseLikeRust(t *testing.T) {
	server, recorder := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "business") {
			return
		}
		analyticsWriteJSON(w, fmt.Sprintf(`{"data": [{"date": %q, "product_surface_usage_values": {}, "groups": [{"dimensions": {"model": "alpha"}, "credits": 0, "uncached_text_input_tokens": 10, "cached_text_input_tokens": 20, "text_output_tokens": 30}, {"dimensions": {"model": "beta"}, "credits": 0, "uncached_text_input_tokens": 100, "cached_text_input_tokens": 200, "text_output_tokens": 300}]}]}`, analyticsTestEndDate.Format("2006-01-02")))
	})
	_, live := analyticsLiveForTest(t, server, "business")
	ctx := context.Background()
	all, err := live.History(ctx, AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingTokenType)
	if err != nil || all == nil {
		t.Fatalf("all history = %#v, %v", all, err)
	}
	alphaFilter := "alpha"
	alpha, err := live.FilteredHistory(ctx, AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingTokenType, &alphaFilter)
	if err != nil || alpha == nil {
		t.Fatalf("alpha history = %#v, %v", alpha, err)
	}
	betaFilter := "beta"
	beta, err := live.FilteredHistory(ctx, AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingTokenType, &betaFilter)
	if err != nil || beta == nil {
		t.Fatalf("beta history = %#v, %v", beta, err)
	}
	if all.Data[0].Total != 660 || alpha.Data[0].Total != 60 || beta.Data[0].Total != 600 {
		t.Fatalf("totals = %v/%v/%v", all.Data[0].Total, alpha.Data[0].Total, beta.Data[0].Total)
	}
	if got := live.TokenModels(); !reflect.DeepEqual(got, []string{"beta", "alpha"}) {
		t.Fatalf("TokenModels() = %v, want [beta alpha]", got)
	}
	if reports := recorder.reportRequests(); len(reports) != 1 {
		t.Fatalf("report requests = %d, want 1", len(reports))
	}
}

func TestLegacyUsageFallsBackToSurfaceLikeRust(t *testing.T) {
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
			return
		}
		analyticsWriteJSON(w, fmt.Sprintf(`{"data": [{"date": %q, "product_surface_usage_values": {"cli": 12.5}}]}`, analyticsTestEndDate.Format("2006-01-02")))
	})
	_, live := analyticsLiveForTest(t, server, "plus")
	ctx := context.Background()
	surface, err := live.History(ctx, AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingSurface)
	if err != nil || surface == nil {
		t.Fatalf("surface history = %#v, %v", surface, err)
	}
	for _, grouping := range []AccountAnalyticsGrouping{AccountAnalyticsGroupingFeature, AccountAnalyticsGroupingTaskStart} {
		fallback, err := live.History(ctx, AccountAnalyticsReportUsage, 7, grouping)
		if err != nil {
			t.Fatalf("grouping %v history error = %v", grouping, err)
		}
		if live.AttributedUsage() {
			t.Fatalf("AttributedUsage() = true for legacy rows")
		}
		if !reflect.DeepEqual(fallback, surface) {
			t.Fatalf("grouping %v history = %#v, want surface %#v", grouping, fallback, surface)
		}
	}
}
func TestInvalidRangeFailsWithoutRequestsLikeRust(t *testing.T) {
	server, recorder := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
			return
		}
		analyticsWriteJSON(w, `{"data": []}`)
	})
	_, live := analyticsLiveForTest(t, server, "plus")
	for _, days := range []uint32{0, ^uint32(0)} {
		_, err := live.History(context.Background(), AccountAnalyticsReportUsage, days, AccountAnalyticsGroupingSurface)
		if err == nil || err.Error() != analyticsInvalidRangeMessage {
			t.Fatalf("days %d error = %v, want %q", days, err, analyticsInvalidRangeMessage)
		}
	}
	if reports := recorder.reportRequests(); len(reports) != 0 {
		t.Fatalf("report requests = %d, want 0", len(reports))
	}
}

func TestUnknownPlanExposesNoCreditBreakdownsLikeRust(t *testing.T) {
	server, recorder := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "unknown") {
			return
		}
		analyticsWriteJSON(w, `{"data": []}`)
	})
	_, live := analyticsLiveForTest(t, server, "unknown")
	ctx := context.Background()
	if _, err := live.Session(ctx); err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	if groups := live.CreditGroups(); len(groups) != 0 {
		t.Fatalf("CreditGroups() = %v, want none", groups)
	}
	if _, err := live.History(ctx, AccountAnalyticsReportCredits, 7, AccountAnalyticsGroupingSurface); err == nil {
		t.Fatalf("credits history succeeded for an unknown plan")
	}
	if reports := recorder.reportRequests(); len(reports) != 0 {
		t.Fatalf("report requests = %d, want 0", len(reports))
	}
}

func TestInFlightAccountSwitchRetainsRefreshGuidanceLikeRust(t *testing.T) {
	home := t.TempDir()
	analyticsSignIn(t, home, "account-a", "user-a", "plus")
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
			return
		}
		if err := analyticsWriteSignIn(home, "account-b", "user-a", "plus"); err != nil {
			t.Errorf("switch account: %v", err)
		}
		analyticsWriteJSON(w, `{"data": []}`)
	})
	live := NewLive(AnalyticsSessionConfig{CodexHome: home, BaseURL: server.URL + "/backend-api"}, analyticsTestEndDate)
	_, err := live.History(context.Background(), AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingSurface)
	if err == nil || err.Error() != analyticsAccountChangedMessage {
		t.Fatalf("History() error = %v, want %q", err, analyticsAccountChangedMessage)
	}
}

func TestReportErrorsMapToGuidanceLikeRust(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
			if analyticsServeAccountsCheck(w, request, "account-a", "business") {
				return
			}
			w.WriteHeader(status)
		})
		_, live := analyticsLiveForTest(t, server, "business")
		_, err := live.History(context.Background(), AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingSurface)
		want := analyticsReportDeniedMessage
		if status == http.StatusNotFound {
			want = analyticsReportUnavailableMessage
		}
		if err == nil || err.Error() != want {
			t.Fatalf("status %d error = %v, want %q", status, err, want)
		}
	}
}

func TestInvalidReportsRetryWithoutRecreatingSessionLikeRust(t *testing.T) {
	for _, plan := range []string{"plus", "business"} {
		var mu sync.Mutex
		attempts := 0
		server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
			if analyticsServeAccountsCheck(w, request, "account-a", plan) {
				return
			}
			mu.Lock()
			attempts++
			first := attempts == 1
			mu.Unlock()
			if first {
				analyticsWriteJSON(w, `{"data": [{"date": "invalid"}]}`)
				return
			}
			analyticsWriteJSON(w, `{"data": []}`)
		})
		_, live := analyticsLiveForTest(t, server, plan)
		ctx := context.Background()
		if _, err := live.History(ctx, AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingSurface); err == nil {
			t.Fatalf("plan %q first history succeeded", plan)
		}
		if _, err := live.History(ctx, AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingSurface); err != nil {
			t.Fatalf("plan %q retry history error = %v", plan, err)
		}
		mu.Lock()
		if attempts != 2 {
			t.Fatalf("plan %q attempts = %d, want 2", plan, attempts)
		}
		mu.Unlock()
	}
}

func TestInvalidCachedBreakdownsAreEvictedBeforeRetryLikeRust(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
			return
		}
		mu.Lock()
		attempts++
		first := attempts == 1
		mu.Unlock()
		credits := "12"
		if first {
			credits = "-1"
		}
		analyticsWriteJSON(w, fmt.Sprintf(`{"data": [{"date": %q, "product_surface_usage_values": {"cli": 12}, "models": [{"model": "alpha", "credits": %s}]}]}`, analyticsTestEndDate.Format("2006-01-02"), credits))
	})
	_, live := analyticsLiveForTest(t, server, "plus")
	ctx := context.Background()
	if _, err := live.History(ctx, AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingSurface); err != nil {
		t.Fatalf("surface history error = %v", err)
	}
	if _, err := live.History(ctx, AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingModel); err == nil {
		t.Fatalf("model history accepted a negative credited model")
	}
	if _, err := live.History(ctx, AccountAnalyticsReportUsage, 7, AccountAnalyticsGroupingModel); err != nil {
		t.Fatalf("model retry error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
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
		{"unknown", auth.PlanUnknown, AccountKindUnknown, []int{}},
		{"missing", "", AccountKindUnknown, []int{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := AccountKindFor(test.plan); got != test.kind {
				t.Fatalf("AccountKindFor(%q) = %v, want %v", test.plan, got, test.kind)
			}
			got := CreditGroupIndices(test.plan)
			if len(got) != len(test.want) || (len(got) > 0 && !reflect.DeepEqual(got, test.want)) {
				t.Fatalf("CreditGroupIndices(%q) = %v, want %v", test.plan, got, test.want)
			}
		})
	}
	if label := AnalyticsGroupLabel(3); label != "Turn start" {
		t.Fatalf("AnalyticsGroupLabel(3) = %q, want Turn start", label)
	}
}

func TestServerPlanDiscoveryIgnoresStaleTokenPlanLikeRust(t *testing.T) {
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "enterprise") {
			return
		}
		analyticsWriteJSON(w, `{"data": []}`)
	})
	_, live := analyticsLiveForTest(t, server, "plus")
	session, err := live.Session(context.Background())
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	if session.Kind != AccountKindEnterprise {
		t.Fatalf("session kind = %v, want enterprise from the server plan", session.Kind)
	}
}

func TestPlanDiscoveryFailureRetriesWithoutTokenPlanLikeRust(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusServiceUnavailable, ""},
		{"invalid response", http.StatusOK, "invalid account response"},
		{"missing plan", http.StatusOK, `{"accounts": [{"id": "account-a"}]}`},
		{"other account", http.StatusOK, `{"accounts": [{"id": "other-account", "plan_type": "enterprise"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			attempts := 0
			server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != analyticsAccountsCheckPath {
					analyticsWriteJSON(w, `{"data": []}`)
					return
				}
				mu.Lock()
				attempts++
				first := attempts == 1
				mu.Unlock()
				if !first {
					analyticsWriteJSON(w, analyticsAccountsCheckBody("account-a", "plus"))
					return
				}
				if test.status != http.StatusOK {
					w.WriteHeader(test.status)
					return
				}
				analyticsWriteJSON(w, test.body)
			})
			_, live := analyticsLiveForTest(t, server, "plus")
			ctx := context.Background()
			if _, err := live.Session(ctx); err == nil || err.Error() != analyticsPlanLoadMessage {
				t.Fatalf("Session() error = %v, want %q", err, analyticsPlanLoadMessage)
			}
			session, err := live.Session(ctx)
			if err != nil {
				t.Fatalf("Session() retry error = %v", err)
			}
			if session.Kind != AccountKindConsumer {
				t.Fatalf("retry kind = %v, want consumer", session.Kind)
			}
		})
	}
}

func TestAccountPlanLookupRecoversFromUnauthorizedLikeRust(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != analyticsAccountsCheckPath {
			analyticsWriteJSON(w, `{"data": []}`)
			return
		}
		mu.Lock()
		attempts++
		first := attempts == 1
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		analyticsWriteJSON(w, `{"accounts": {"account-a": {"account": {"account_id": "account-a", "plan_type": "enterprise"}}}, "account_ordering": ["account-a"]}`)
	})
	_, live := analyticsLiveForTest(t, server, "plus")
	session, err := live.Session(context.Background())
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	if session.Kind != AccountKindEnterprise {
		t.Fatalf("session kind = %v, want enterprise", session.Kind)
	}
}

func TestAccountPlanLookupRejectsIdentitySwitchesLikeRust(t *testing.T) {
	for _, test := range []struct {
		name    string
		account string
		user    string
	}{
		{"account", "account-b", "user-a"},
		{"user", "account-a", "user-b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			analyticsSignIn(t, home, "account-a", "user-a", "plus")
			server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != analyticsAccountsCheckPath {
					analyticsWriteJSON(w, `{"data": []}`)
					return
				}
				if err := analyticsWriteSignIn(home, test.account, test.user, "plus"); err != nil {
					t.Errorf("switch account: %v", err)
				}
				analyticsWriteJSON(w, `{"accounts": [{"id": "account-a", "plan_type": "enterprise"}]}`)
			})
			live := NewLive(AnalyticsSessionConfig{CodexHome: home, BaseURL: server.URL + "/backend-api"}, analyticsTestEndDate)
			if _, err := live.Session(context.Background()); err == nil || err.Error() != analyticsAccountChangedMessage {
				t.Fatalf("Session() error = %v, want %q", err, analyticsAccountChangedMessage)
			}
		})
	}
}

func TestAnalyticsLiveDefaultsBeforeInitializationLikeRust(t *testing.T) {
	live := NewLive(AnalyticsSessionConfig{CodexHome: t.TempDir()}, analyticsTestEndDate)
	if label := live.AccountLabel(); label != nil {
		t.Fatalf("AccountLabel() = %v, want nil", label)
	}
	if got := live.CreditGroups(); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("CreditGroups() = %v, want [0]", got)
	}
}
func TestAnalyticsLoadPublishesStatesLikeRust(t *testing.T) {
	for _, test := range []struct {
		name        string
		value       *int
		err         error
		wantReady   bool
		wantMessage string
	}{
		{"ready", func() *int { value := 7; return &value }(), nil, true, ""},
		{"unavailable", nil, nil, false, analyticsUnavailableMessage},
		{"error", nil, errors.New("temporary failure"), false, "temporary failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames := make(chan struct{}, 1)
			load := StartAnalyticsLoad(context.Background(), func(context.Context) (*int, error) {
				return test.value, test.err
			}, func() { frames <- struct{}{} })
			select {
			case <-frames:
			case <-time.After(5 * time.Second):
				t.Fatalf("load did not schedule a frame")
			}
			load.Poll()
			value, ready := load.Ready()
			message, hasMessage := load.Message()
			if ready != test.wantReady {
				t.Fatalf("Ready() = %v, want %v", ready, test.wantReady)
			}
			if ready && (test.value == nil || value != *test.value) {
				t.Fatalf("Ready() value = %d", value)
			}
			if hasMessage != (test.wantMessage != "") || message != test.wantMessage {
				t.Fatalf("Message() = %q/%v, want %q", message, hasMessage, test.wantMessage)
			}
		})
	}
}

func TestReplacingPendingLoadCancelsRequestLikeRust(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	load := StartAnalyticsLoad(context.Background(), func(ctx context.Context) (*int, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}, nil)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("load did not start")
	}
	load.Cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatalf("cancelling a pending load did not abort its request")
	}
}

func TestPanickingLoadReportsInterruptionLikeRust(t *testing.T) {
	frames := make(chan struct{}, 1)
	load := StartAnalyticsLoad(context.Background(), func(context.Context) (*int, error) {
		panic("report task failed")
	}, func() { frames <- struct{}{} })
	select {
	case <-frames:
	case <-time.After(5 * time.Second):
		t.Fatalf("panicking load did not schedule a frame")
	}
	load.Poll()
	message, ok := load.Message()
	if !ok || message != analyticsInterruptedMessage {
		t.Fatalf("Message() = %q/%v, want %q", message, ok, analyticsInterruptedMessage)
	}
}
