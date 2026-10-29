package tea

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/tui/analytics"
)

// startSummaryAnalyticsBackend serves the accounts check, the account profile
// and empty analytics reports.
func startSummaryAnalyticsBackend(t *testing.T, account string, plan string, profileBody string, profileStatus int) *analyticsTestBackend {
	t.Helper()
	backend := &analyticsTestBackend{}
	backend.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == analyticsAccountsCheckPath:
			_, _ = fmt.Fprintf(w, `{"accounts":[{"id":%q,"plan_type":%q}],"account_ordering":[%q]}`, account, plan, account)
		case strings.Contains(request.URL.Path, "profiles/me"):
			if profileStatus != 0 && profileStatus != http.StatusOK {
				http.Error(w, "boom", profileStatus)
				return
			}
			_, _ = w.Write([]byte(profileBody))
		default:
			_, _ = w.Write([]byte(`{"data": []}`))
		}
	}))
	t.Cleanup(backend.server.Close)
	return backend
}

const analyticsSummaryTestProfile = `{
	"profile": {"display_name": "Test user", "username": "test.user"},
	"metadata": {"stats_as_of": "2026-09-09"},
	"stats": {
		"lifetime_tokens": 142300000000,
		"peak_daily_tokens": 7000000000,
		"longest_running_turn_sec": 158760,
		"current_streak_days": 158,
		"longest_streak_days": 160,
		"daily_usage_buckets": [{"start_date": "2026-09-09", "tokens": 42}],
		"fast_mode_usage_percentage": 37.5,
		"most_used_reasoning_effort": "high",
		"most_used_reasoning_effort_percentage": 26,
		"unique_skills_used": 147,
		"total_skills_used": 13190,
		"total_threads": 0,
		"top_invocations": [
			{"type": "skill", "skill_name": "review", "usage_count": 12},
			{"type": "plugin", "plugin_name": "github", "usage_count": 34}
		]
	}
}`

func TestAnalyticsSummaryTabLoadsProfileAndScrollsLikeRust(t *testing.T) {
	backend := startSummaryAnalyticsBackend(t, "account-a", "plus", analyticsSummaryTestProfile, http.StatusOK)
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	if dashboard.section != analytics.AnalyticsSectionSummary {
		t.Fatalf("initial section = %v, want summary", dashboard.section)
	}
	if dashboard.profile.State() != analytics.AnalyticsLoadStateReady {
		t.Fatalf("profile state = %v, want ready", dashboard.profile.State())
	}
	view := stripAnalyticsTestANSI(model.View())
	for _, want := range []string{
		"Summary",
		"Test user",
		"@test.user",
		"Plus",
		"Lifetime tokens",
		"142.3B",
		"Token activity",
		"[Daily]  Weekly  Cumulative",
		"tab/1–6",
		"scroll",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("summary view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "r 7/30d") {
		t.Fatalf("summary kept the range hint:\n%s", view)
	}
	if strings.Contains(view, "DAILY · weekly") {
		t.Fatalf("summary kept the standalone chart footer:\n%s", view)
	}

	// r is inert on Summary, while g cycles the token activity view.
	before := dashboard.ranges
	model.Update(runes("r"))
	if dashboard.ranges != before {
		t.Fatalf("r changed the summary range: %v -> %v", before, dashboard.ranges)
	}
	model.Update(runes("g"))
	if dashboard.groupPicker == nil {
		t.Fatal("g did not open the summary view picker")
	}
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if got := dashboard.sections[analytics.AnalyticsSectionSummary].Group; got != 1 {
		t.Fatalf("summary view = %d, want 1", got)
	}
	waitForAnalyticsLoads(t, model)
	if view := stripAnalyticsTestANSI(model.View()); !strings.Contains(view, "Daily  [Weekly]  Cumulative") {
		t.Fatalf("summary did not switch to Weekly:\n%s", view)
	}

	// Scrolling moves the offset without moving a selection.
	model.Update(key(bubbletea.KeyPgDown))
	if dashboard.scrollOffset <= 0 {
		t.Fatalf("page down did not scroll: %d", dashboard.scrollOffset)
	}
	model.Update(key(bubbletea.KeyPgDown))
	scrolled := stripAnalyticsTestANSI(model.View())
	for _, want := range []string{"Activity insights", "Fast Mode", "Most used plugins and skills", "@github", "$review"} {
		if !strings.Contains(scrolled, want) {
			t.Fatalf("scrolled summary missing %q:\n%s", want, scrolled)
		}
	}
	model.Update(key(bubbletea.KeyHome))
	if dashboard.scrollOffset != 0 {
		t.Fatalf("jump top = %d, want 0", dashboard.scrollOffset)
	}
}

func TestAnalyticsSummaryProfileFailureKeepsTabLikeRust(t *testing.T) {
	backend := startSummaryAnalyticsBackend(t, "account-a", "plus", "", http.StatusInternalServerError)
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	if dashboard.section != analytics.AnalyticsSectionSummary {
		t.Fatalf("initial section = %v, want summary", dashboard.section)
	}
	if dashboard.profile.State() != analytics.AnalyticsLoadStateError {
		t.Fatalf("profile state = %v, want error", dashboard.profile.State())
	}
	view := stripAnalyticsTestANSI(model.View())
	if !strings.Contains(view, "Summary") || !strings.Contains(view, "Report request failed") {
		t.Fatalf("summary failure view:\n%s", view)
	}
	// Other reports still loaded independently of the profile.
	if state := dashboard.sections[analytics.AnalyticsSectionUsage].History.State(); state != analytics.AnalyticsLoadStateReady {
		t.Fatalf("usage state = %v, want ready", state)
	}
}
