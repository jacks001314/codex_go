package tea

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/auth"
	codextui "codex_go/tui"
	"codex_go/tui/analytics"
)

// These tests mirror Rust #45764's analytics view coverage: the /usage menu
// entry, account-specific sections, range and grouping changes, model
// filtering, independent load failures, cancellation on close, small
// terminals, and draft preservation.

const analyticsAccountsCheckPath = "/backend-api/wham/accounts/check"

func analyticsTestToken(t *testing.T, account string, user string, plan string) string {
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
	return "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".test"
}

func writeAnalyticsTestAuth(t *testing.T, home string, account string, user string, plan string) {
	t.Helper()
	token := analyticsTestToken(t, account, user, plan)
	snapshot := auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"id_token":      token,
			"access_token":  token,
			"refresh_token": "test-refresh",
			"account_id":    account,
		},
	}
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatalf("save auth: %v", err)
	}
}

// analyticsTestBackend serves the account check and one report payload for
// every report route.
type analyticsTestBackend struct {
	server  *httptest.Server
	release chan struct{}
}

func startAnalyticsTestBackend(t *testing.T, account string, plan string, body string) *analyticsTestBackend {
	t.Helper()
	backend := &analyticsTestBackend{}
	backend.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == analyticsAccountsCheckPath {
			_, _ = fmt.Fprintf(w, `{"accounts":[{"id":%q,"plan_type":%q}],"account_ordering":[%q]}`, account, plan, account)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(backend.server.Close)
	return backend
}

// startBlockingAnalyticsTestBackend keeps every report request pending until
// the returned backend is released.
func startBlockingAnalyticsTestBackend(t *testing.T, account string, plan string) *analyticsTestBackend {
	t.Helper()
	backend := &analyticsTestBackend{release: make(chan struct{})}
	backend.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == analyticsAccountsCheckPath {
			_, _ = fmt.Fprintf(w, `{"accounts":[{"id":%q,"plan_type":%q}],"account_ordering":[%q]}`, account, plan, account)
			return
		}
		<-backend.release
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	t.Cleanup(func() {
		close(backend.release)
		backend.server.Close()
	})
	return backend
}

func newAnalyticsTestModel(t *testing.T, plan string, backend *analyticsTestBackend) *Model {
	t.Helper()
	home := t.TempDir()
	writeAnalyticsTestAuth(t, home, "account-a", "user-a", plan)
	model := NewModel(codextui.NewState(nil), Options{
		Width:             100,
		Height:            30,
		HasChatGPTAccount: true,
		CodexHome:         home,
	})
	model.modelCatalogOpts = []codextui.ModelPickerOption{
		{ID: "alpha", Label: "Alpha"},
		{ID: "beta", Label: "Beta"},
	}
	baseURL := backend.server.URL + "/backend-api"
	model.analyticsLive = func(endDate time.Time) *analytics.Live {
		return analytics.NewLive(analytics.AnalyticsSessionConfig{CodexHome: home, BaseURL: baseURL}, endDate)
	}
	return model
}

// waitForAnalyticsLoads polls pending loads until they settle.
func waitForAnalyticsLoads(t *testing.T, model *Model) {
	t.Helper()
	dashboard := model.analyticsDashboard
	if dashboard == nil {
		t.Fatal("analytics dashboard is not open")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		dashboard.pollReports()
		if !dashboard.anyLoading() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("analytics loads did not settle")
}

func openAnalyticsFromUsageMenu(t *testing.T, model *Model) {
	t.Helper()
	typeText(t, model, "/usage")
	model.Update(key(bubbletea.KeyEnter))
	// Rust #45769 makes "View analytics" the first usage-menu entry.
	model.Update(key(bubbletea.KeyEnter))
	if model.analyticsDashboard == nil || !model.analyticsDashboard.active {
		t.Fatalf("analytics dashboard did not open; notice=%q", model.notice)
	}
}

func analyticsSectionList(sections []analytics.AnalyticsSection) []analytics.AnalyticsSection {
	return append([]analytics.AnalyticsSection(nil), sections...)
}

func TestUsageMenuOpensAnalyticsLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsTestModel(t, "plus", backend)

	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	want := []analytics.AnalyticsSection{
		analytics.AnalyticsSectionSummary,
		analytics.AnalyticsSectionUsage,
		analytics.AnalyticsSectionActivity,
		analytics.AnalyticsSectionPlugins,
		analytics.AnalyticsSectionSkills,
		analytics.AnalyticsSectionChats,
	}
	if got := analyticsSectionList(model.analyticsDashboard.visibleSections()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("consumer sections = %v, want %v", got, want)
	}
	view := model.View()
	for _, want := range []string{"Analytics", "Live account", "tab/1–6", "Total usage history", "Summary"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard view missing %q:\n%s", want, view)
		}
	}
}

func TestAnalyticsBusinessSectionsLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "business", `{"data": []}`)
	model := newAnalyticsTestModel(t, "business", backend)

	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	want := []analytics.AnalyticsSection{
		analytics.AnalyticsSectionSummary,
		analytics.AnalyticsSectionCredits,
		analytics.AnalyticsSectionUsage,
		analytics.AnalyticsSectionPlugins,
		analytics.AnalyticsSectionSkills,
		analytics.AnalyticsSectionChats,
	}
	if got := analyticsSectionList(model.analyticsDashboard.visibleSections()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("business sections = %v, want %v", got, want)
	}
	// Summary is the initial tab for every account that resolves a plan.
	if model.analyticsDashboard.section != analytics.AnalyticsSectionSummary {
		t.Fatalf("initial business section = %v, want summary", model.analyticsDashboard.section)
	}
}

func TestAnalyticsRangeChangeKeepsSelectionLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	if got := dashboard.rowCount(); got != 7 {
		t.Fatalf("row count = %d, want 7", got)
	}
	model.Update(runes("r"))
	if got := dashboard.ranges[analytics.AnalyticsRangeGroupUsage]; got != 1 {
		t.Fatalf("usage range = %d, want 1", got)
	}
	if got := dashboard.rowCount(); got != 30 {
		t.Fatalf("row count after r = %d, want 30", got)
	}
	if got := dashboard.sections[analytics.AnalyticsSectionUsage].Cursor; got != 29 {
		t.Fatalf("cursor after r = %d, want 29", got)
	}
	waitForAnalyticsLoads(t, model)
	if !strings.Contains(model.View(), "30d") {
		t.Fatalf("view missing 30d range:\n%s", model.View())
	}
	model.Update(runes("r"))
	if got := dashboard.sections[analytics.AnalyticsSectionUsage].Cursor; got != 6 {
		t.Fatalf("cursor after second r = %d, want 6", got)
	}
}

func TestAnalyticsGroupPickerAppliesGroupingLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	options := dashboard.groupOptions()
	position := 0
	for index, group := range options {
		if group == dashboard.sections[analytics.AnalyticsSectionUsage].Group {
			position = index
			break
		}
	}
	model.Update(runes("g"))
	if dashboard.groupPicker == nil {
		t.Fatalf("group picker did not open; options=%v", options)
	}
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if dashboard.groupPicker != nil {
		t.Fatal("group picker stayed open after selection")
	}
	if want := options[(position+1)%len(options)]; dashboard.sections[analytics.AnalyticsSectionUsage].Group != want {
		t.Fatalf("usage group = %d, want %d", dashboard.sections[analytics.AnalyticsSectionUsage].Group, want)
	}
	waitForAnalyticsLoads(t, model)
}

func TestAnalyticsBusinessModelFilterLikeRust(t *testing.T) {
	body := fmt.Sprintf(`{"data": [{"date": %q, "product_surface_usage_values": {}, "groups": [{"dimensions": {"model": "alpha"}, "credits": 0, "uncached_text_input_tokens": 10, "cached_text_input_tokens": 20, "text_output_tokens": 30}, {"dimensions": {"model": "beta"}, "credits": 0, "uncached_text_input_tokens": 100, "cached_text_input_tokens": 200, "text_output_tokens": 300}]}]}`, time.Now().UTC().Format("2006-01-02"))
	backend := startAnalyticsTestBackend(t, "account-a", "business", body)
	model := newAnalyticsTestModel(t, "business", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	dashboard.selectSection(analytics.AnalyticsSectionUsage)
	waitForAnalyticsLoads(t, model)
	if got := dashboard.live.TokenModels(); len(got) != 2 {
		t.Fatalf("token models = %v, want two", got)
	}
	model.Update(runes("m"))
	if dashboard.tokenModel == nil || *dashboard.tokenModel != "beta" {
		t.Fatalf("first model filter = %v, want beta", dashboard.tokenModel)
	}
	waitForAnalyticsLoads(t, model)
	model.Update(runes("m"))
	if dashboard.tokenModel == nil || *dashboard.tokenModel != "alpha" {
		t.Fatalf("second model filter = %v, want alpha", dashboard.tokenModel)
	}
	waitForAnalyticsLoads(t, model)
	model.Update(runes("m"))
	if dashboard.tokenModel != nil {
		t.Fatalf("third model filter = %v, want all models", *dashboard.tokenModel)
	}
	if !strings.Contains(model.View(), "All models") {
		t.Fatalf("view missing all-models label:\n%s", model.View())
	}
}

func TestAnalyticsCloseCancelsPendingLoadsLikeRust(t *testing.T) {
	backend := startBlockingAnalyticsTestBackend(t, "account-a", "plus")
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)

	dashboard := model.analyticsDashboard
	deadline := time.Now().Add(2 * time.Second)
	for !dashboard.anyLoading() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !dashboard.anyLoading() {
		t.Fatal("reports never started loading")
	}
	model.Update(runes("q"))
	if dashboard.active {
		t.Fatal("dashboard stayed active after q")
	}
	for index := range dashboard.sections {
		if dashboard.sections[index].History.State() == analytics.AnalyticsLoadStateLoading {
			t.Fatalf("section %d still loading after close", index)
		}
	}
	if dashboard.live != nil {
		t.Fatal("live client retained after close")
	}
	if strings.Contains(model.View(), "Live account") {
		t.Fatalf("dashboard still rendered after close:\n%s", model.View())
	}
}

func TestAnalyticsPreservesComposerDraftLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsTestModel(t, "plus", backend)
	model.composer.SetValue("keep me")
	model.openAnalyticsDashboard()
	if model.analyticsDashboard == nil || !model.analyticsDashboard.active {
		t.Fatal("analytics dashboard did not open")
	}
	waitForAnalyticsLoads(t, model)

	// Keys typed while the dashboard is open belong to the dashboard, never to
	// the retained composer draft.
	model.Update(runes("z"))
	model.Update(runes("1"))
	model.Update(key(bubbletea.KeyTab))
	model.Update(runes("q"))
	if got := model.composer.Value(); got != "keep me" {
		t.Fatalf("composer draft = %q, want %q", got, "keep me")
	}
}

func TestAnalyticsRetainsNavigationAcrossReopenLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	model.Update(key(bubbletea.KeyTab))
	model.Update(runes("r"))
	section := model.analyticsDashboard.section
	model.Update(runes("q"))
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	if dashboard.section != section {
		t.Fatalf("reopened section = %v, want %v", dashboard.section, section)
	}
	if got := dashboard.ranges[section.RangeGroup(dashboard.business())]; got != 1 {
		t.Fatalf("reopened range = %d, want 1", got)
	}
}

func TestAnalyticsSmallTerminalAndZoomKeysLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsTestModel(t, "plus", backend)
	model.width, model.height = 32, 10
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	view := model.View()
	if lines := strings.Split(view, "\n"); len(lines) > model.height {
		t.Fatalf("dashboard rendered %d lines, want at most %d:\n%s", len(lines), model.height, view)
	}
	model.Update(runes("z"))
	if model.analyticsDashboard.zoomed {
		t.Fatal("z did not switch the dashboard to the overview")
	}
	model.Update(runes("z"))
	if !model.analyticsDashboard.zoomed {
		t.Fatal("z did not restore the maximized section")
	}
}

func TestAnalyticsSectionsSwitchAndDetailsLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	model.Update(runes("3"))
	if dashboard.section != analytics.AnalyticsSectionActivity {
		t.Fatalf("section after 3 = %v, want activity", dashboard.section)
	}
	model.Update(runes("2"))
	if dashboard.section != analytics.AnalyticsSectionUsage {
		t.Fatalf("section after 2 = %v, want usage", dashboard.section)
	}
	model.Update(key(bubbletea.KeyLeft))
	if got := dashboard.sections[analytics.AnalyticsSectionUsage].Cursor; got != 5 {
		t.Fatalf("cursor after left = %d, want 5", got)
	}
	model.Update(key(bubbletea.KeyRight))
	if got := dashboard.sections[analytics.AnalyticsSectionUsage].Cursor; got != 6 {
		t.Fatalf("cursor after right = %d, want 6", got)
	}
	model.Update(runes(" "))
	if detail := dashboard.sections[analytics.AnalyticsSectionUsage].Detail; detail == nil || *detail != 6 {
		t.Fatalf("detail = %v, want 6", detail)
	}
	model.Update(runes(" "))
	if detail := dashboard.sections[analytics.AnalyticsSectionUsage].Detail; detail != nil {
		t.Fatalf("detail after second space = %v, want nil", detail)
	}
	model.Update(key(bubbletea.KeyEsc))
	if dashboard.active {
		t.Fatal("esc did not close the maximized dashboard")
	}
}

func TestAnalyticsSectionLoadFailuresAreIndependentLikeRust(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == analyticsAccountsCheckPath {
			_, _ = fmt.Fprintf(w, `{"accounts":[{"id":"account-a","plan_type":"plus"}],"account_ordering":["account-a"]}`)
			return
		}
		if strings.Contains(request.URL.Path, "plugin") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	t.Cleanup(failing.Close)
	backend := &analyticsTestBackend{server: failing}
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	if state := dashboard.sections[analytics.AnalyticsSectionUsage].History.State(); state != analytics.AnalyticsLoadStateReady {
		t.Fatalf("usage state = %v, want ready", state)
	}
	if state := dashboard.sections[analytics.AnalyticsSectionPlugins].History.State(); state != analytics.AnalyticsLoadStateError {
		t.Fatalf("plugins state = %v, want error", state)
	}
	model.Update(runes("4"))
	if view := model.View(); !strings.Contains(view, "Report request failed") {
		t.Fatalf("plugin panel missing the failure message:\n%s", view)
	}
}

func TestAnalyticsUsesTerminalColorsLikeRust(t *testing.T) {
	backend := startAnalyticsTestBackend(t, "account-a", "plus", `{"data": []}`)
	model := newAnalyticsTestModel(t, "plus", backend)
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	view := model.View()
	if !strings.Contains(view, "\x1b[") {
		t.Fatalf("dashboard rendered without terminal styling:\n%s", view)
	}
	// The focused tab keeps its label in the accent style, so the bracketed
	// label must still be present after stripping the escape sequences.
	if plain := stripAnalyticsTestANSI(view); !strings.Contains(plain, "[1 ") {
		t.Fatalf("focused tab missing from styled view:\n%s", plain)
	}
}

func stripAnalyticsTestANSI(value string) string {
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != 0x1b || index+1 >= len(value) || value[index+1] != '[' {
			builder.WriteByte(value[index])
			continue
		}
		index += 2
		for index < len(value) && value[index] != 'm' {
			index++
		}
	}
	return builder.String()
}
