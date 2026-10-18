package chatgptapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCloudClientGetAccountAnalyticsPluginQueryAndDecodeLikeRust(t *testing.T) {
	const payload = `{"data":[{"date":"2026-01-03","plugin_usage_overviews":[{"plugin_id":null,"plugin_name":"example","marketplace":null,"display_name":"Example plugin","invocation_counts":12}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/codex/analytics/daily-plugin-usage-metrics" {
			t.Errorf("request = %s %s, want GET /api/codex/analytics/daily-plugin-usage-metrics", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		assertAnalyticsQuery(t, r.URL.Query(), map[string][]string{
			"start_date":       {"2026-01-03"},
			"end_date":         {"2026-01-09"},
			"group_by":         {"day"},
			"workspace_user":   {"true"},
			"top_plugin_limit": {"10"},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	response, err := client.GetAccountAnalytics(context.Background(), AnalyticsReport{
		Kind:  AnalyticsReportPlugins,
		Limit: 10,
	}, "2026-01-03", "2026-01-09")
	if err != nil {
		t.Fatalf("GetAccountAnalytics() error = %v", err)
	}
	if response.Kind != AnalyticsReportPlugins || response.Plugins == nil {
		t.Fatalf("GetAccountAnalytics() = %#v, want plugins response", response)
	}
	if len(response.Plugins.Data) != 1 || len(response.Plugins.Data[0].PluginUsageOverviews) != 1 {
		t.Fatalf("plugin data = %#v", response.Plugins.Data)
	}
	plugin := response.Plugins.Data[0].PluginUsageOverviews[0]
	if plugin.PluginName != "example" || plugin.DisplayName != "Example plugin" || plugin.InvocationCounts != 12 {
		t.Fatalf("plugin = %#v", plugin)
	}
	if plugin.PluginID != nil || plugin.Marketplace != nil {
		t.Fatalf("optional plugin attribution = %#v, %#v; want nil", plugin.PluginID, plugin.Marketplace)
	}
}

func TestCloudClientGetAccountAnalyticsCreditsPreservesSignedValuesLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/backend-api/wham/usage/credit-usage-events" {
			t.Errorf("request = %s %s, want GET /backend-api/wham/usage/credit-usage-events", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.URL.RawQuery; got != "" {
			t.Errorf("query = %q, want no query for credit events", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"date":"2026-01-03","product_surface":"cli","credit_amount":-0.004}]}`))
	}))
	defer server.Close()

	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL + "/backend-api", HTTPClient: server.Client()})
	response, err := client.GetAccountAnalytics(context.Background(), AnalyticsReport{Kind: AnalyticsReportCredits}, "2026-01-03", "2026-01-09")
	if err != nil {
		t.Fatalf("GetAccountAnalytics() error = %v", err)
	}
	if response.Credits == nil || len(response.Credits.Data) != 1 {
		t.Fatalf("credits = %#v", response.Credits)
	}
	event := response.Credits.Data[0]
	if event.CreditAmount != -0.004 || event.ProductSurface != "cli" || event.UsageID != nil {
		t.Fatalf("credit event = %#v", event)
	}
}

func TestCloudClientGetAccountAnalyticsRejectsMismatchedShapeWithoutBodyLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"date":"2026-01-03","product_surface":"sensitive-value","credit_amount":10}]}`))
	}))
	defer server.Close()

	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	_, err := client.GetAccountAnalytics(context.Background(), AnalyticsReport{Kind: AnalyticsReportMessages}, "2026-01-03", "2026-01-09")
	if err == nil {
		t.Fatal("GetAccountAnalytics() error = nil, want decode error")
	}
	if err.Error() != "Invalid analytics response." {
		t.Fatalf("error = %q, want generic analytics decode error", err)
	}
	if strings.Contains(err.Error(), "sensitive-value") {
		t.Fatalf("error leaked response body: %q", err)
	}
}

func TestCloudClientGetAccountAnalyticsRoutesReportsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name      string
		report    AnalyticsReport
		wantRoute string
		wantQuery map[string][]string
		payload   string
		check     func(*testing.T, *AnalyticsResponse)
	}{
		{
			name:      "usage",
			report:    AnalyticsReport{Kind: AnalyticsReportUsage},
			wantRoute: "/api/codex/usage/daily-token-usage-breakdown",
			wantQuery: map[string][]string{
				"start_date": {"2026-01-03"},
				"end_date":   {"2026-01-09"},
				"group_by":   {"day"},
			},
			payload: `{"data":[{"date":"2026-01-03","product_surface_usage_values":{"future_surface":1.0}}],"units":"future_unit"}`,
			check: func(t *testing.T, response *AnalyticsResponse) {
				t.Helper()
				if response.Usage == nil || response.Usage.Units == nil || *response.Usage.Units != "future_unit" {
					t.Fatalf("usage = %#v", response.Usage)
				}
				if got := response.Usage.Data[0].ProductSurfaceUsageValues["future_surface"]; got != 1 {
					t.Fatalf("future_surface usage = %v, want 1", got)
				}
			},
		},
		{
			name:      "enterprise credits",
			report:    AnalyticsReport{Kind: AnalyticsReportEnterpriseCredits, Breakdown: "model"},
			wantRoute: "/api/codex/usage/daily-workspace-user-credit-usage",
			wantQuery: map[string][]string{
				"start_date": {"2026-01-03"},
				"end_date":   {"2026-01-09"},
				"breakdown":  {"model"},
			},
			payload: `{"data":[],"series":[],"breakdown":"model"}`,
			check: func(t *testing.T, response *AnalyticsResponse) {
				t.Helper()
				if response.EnterpriseCredits == nil || response.EnterpriseCredits.Breakdown != "model" {
					t.Fatalf("enterprise credits = %#v", response.EnterpriseCredits)
				}
			},
		},
		{
			name:      "messages",
			report:    AnalyticsReport{Kind: AnalyticsReportMessages},
			wantRoute: "/api/codex/analytics/daily-workspace-usage-counts",
			wantQuery: map[string][]string{
				"start_date":     {"2026-01-03"},
				"end_date":       {"2026-01-09"},
				"group_by":       {"day"},
				"workspace_user": {"true"},
			},
			payload: `{"data":[]}`,
			check: func(t *testing.T, response *AnalyticsResponse) {
				t.Helper()
				if response.Messages == nil {
					t.Fatalf("messages = %#v", response.Messages)
				}
			},
		},
		{
			name:      "skills",
			report:    AnalyticsReport{Kind: AnalyticsReportSkills, Limit: 10},
			wantRoute: "/api/codex/analytics/daily-skill-usage-metrics",
			wantQuery: map[string][]string{
				"start_date":      {"2026-01-03"},
				"end_date":        {"2026-01-09"},
				"group_by":        {"day"},
				"workspace_user":  {"true"},
				"top_skill_limit": {"10"},
			},
			payload: `{"data":[]}`,
			check: func(t *testing.T, response *AnalyticsResponse) {
				t.Helper()
				if response.Skills == nil {
					t.Fatalf("skills = %#v", response.Skills)
				}
			},
		},
		{
			name:      "workspace credits",
			report:    AnalyticsReport{Kind: AnalyticsReportWorkspaceCredits},
			wantRoute: "/api/codex/usage/daily-workspace-user-token-usage-breakdown",
			wantQuery: map[string][]string{
				"start_date": {"2026-01-03"},
				"end_date":   {"2026-01-09"},
				"group_by":   {"day"},
			},
			payload: `{"data":[]}`,
			check: func(t *testing.T, response *AnalyticsResponse) {
				t.Helper()
				if response.Usage == nil {
					t.Fatalf("workspace credits = %#v", response.Usage)
				}
			},
		},
		{
			name:      "enterprise tokens",
			report:    AnalyticsReport{Kind: AnalyticsReportEnterpriseTokens},
			wantRoute: "/api/codex/usage/daily-workspace-user-token-usage-breakdown",
			wantQuery: map[string][]string{
				"start_date":   {"2026-01-03"},
				"end_date":     {"2026-01-09"},
				"group_by":     {"day"},
				"breakdown_by": {"model"},
				"modes":        {"codex", "work"},
			},
			payload: `{"data":[]}`,
			check: func(t *testing.T, response *AnalyticsResponse) {
				t.Helper()
				if response.Usage == nil {
					t.Fatalf("enterprise tokens = %#v", response.Usage)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != tc.wantRoute {
					t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, tc.wantRoute)
					http.NotFound(w, r)
					return
				}
				assertAnalyticsQuery(t, r.URL.Query(), tc.wantQuery)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.payload))
			}))
			defer server.Close()

			client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL, HTTPClient: server.Client()})
			response, err := client.GetAccountAnalytics(context.Background(), tc.report, "2026-01-03", "2026-01-09")
			if err != nil {
				t.Fatalf("GetAccountAnalytics() error = %v", err)
			}
			if response.Kind != tc.report.Kind {
				t.Fatalf("response kind = %q, want %q", response.Kind, tc.report.Kind)
			}
			tc.check(t, response)
		})
	}
}

func TestCloudClientGetAccountAnalyticsRejectsUnknownReport(t *testing.T) {
	client := NewCloudClient(nil)
	_, err := client.GetAccountAnalytics(context.Background(), AnalyticsReport{Kind: "future"}, "2026-01-03", "2026-01-09")
	if err == nil || !strings.Contains(err.Error(), "unsupported analytics report") {
		t.Fatalf("error = %v, want unsupported report", err)
	}
}

func assertAnalyticsQuery(t *testing.T, got url.Values, want map[string][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("query keys = %#v, want %#v", got, want)
	}
	for key, values := range want {
		gotValues := got[key]
		if len(gotValues) != len(values) {
			t.Fatalf("query %q = %#v, want %#v", key, gotValues, values)
		}
		for i := range values {
			if gotValues[i] != values[i] {
				t.Fatalf("query %q = %#v, want %#v", key, gotValues, values)
			}
		}
	}
}
