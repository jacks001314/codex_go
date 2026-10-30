package tea

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/tui/analytics"
)

// These tests mirror the Rust #45765 dashboard half: the Top chats tab, the
// rendered table, the zero-credit toggle, the ignored range key, and the
// overview card.

func analyticsChatTestPtr(value string) *string {
	return &value
}

// startAnalyticsChatsTestBackend serves the account check and the per-thread
// usage query with the supplied estimates.
func startAnalyticsChatsTestBackend(t *testing.T, plan string, usage map[string]int64) *analyticsTestBackend {
	t.Helper()
	backend := &analyticsTestBackend{}
	backend.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case analyticsAccountsCheckPath:
			_, _ = fmt.Fprintf(w, `{"accounts":[{"id":"account-a","plan_type":%q}],"account_ordering":["account-a"]}`, plan)
		case "/backend-api/wham/usage/thread_usage/query":
			var body struct {
				ThreadIDs []string `json:"thread_ids"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode thread usage request: %v", err)
			}
			threads := make([]string, 0, len(body.ThreadIDs))
			for _, id := range body.ThreadIDs {
				credits, ok := usage[id]
				if !ok {
					threads = append(threads, fmt.Sprintf(`{"thread_id":%q}`, id))
					continue
				}
				threads = append(threads, fmt.Sprintf(
					`{"thread_id":%q,"estimated_usage_credits_micros":%d,"groups":[{"model":"gpt-5-codex","reasoning_effort":"high","speed":"standard","estimated_usage_credits_micros":%d}]}`,
					id, credits, credits,
				))
			}
			_, _ = w.Write([]byte(`{"threads":[` + strings.Join(threads, ",") + `]}`))
		default:
			_, _ = w.Write([]byte(`{"data": []}`))
		}
	}))
	t.Cleanup(backend.server.Close)
	return backend
}

func (d *analyticsDashboard) chatsReadyRows() ([]analytics.AnalyticsChat, bool) {
	chats, ok := d.chats.Ready()
	if !ok {
		return nil, false
	}
	return chats.Rows, true
}

// startAnalyticsConsumerChatsTestBackend serves query_v2 with consumer task
// accounting for the supplied roots.
func startAnalyticsConsumerChatsTestBackend(t *testing.T, credits map[string]string) *analyticsTestBackend {
	t.Helper()
	backend := &analyticsTestBackend{}
	backend.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case analyticsAccountsCheckPath:
			_, _ = fmt.Fprintf(w, `{"accounts":[{"id":"account-a","plan_type":"plus"}],"account_ordering":["account-a"]}`)
		case "/backend-api/wham/usage/thread_usage/query_v2":
			var body struct {
				Threads []struct {
					ThreadID string `json:"thread_id"`
				} `json:"threads"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode task usage request: %v", err)
			}
			rows := make([]string, 0, len(body.Threads))
			for _, thread := range body.Threads {
				if amount, ok := credits[thread.ThreadID]; ok {
					rows = append(rows, fmt.Sprintf(
						`{"thread_id":%q,"data_status":"available","usage_source":"plan_and_credits",`+
							`"weekly_limit_percent":10.5,"five_hour_limit_percent":42.25,"balance_usage_credits":%q,"groups":[]}`,
						thread.ThreadID, amount))
					continue
				}
				rows = append(rows, fmt.Sprintf(
					`{"thread_id":%q,"data_status":"unavailable","usage_source":"none","groups":[]}`, thread.ThreadID))
			}
			_, _ = w.Write([]byte(`{"data_as_of":"2026-10-01T12:30:00Z","threads":[` + strings.Join(rows, ",") + `]}`))
		default:
			_, _ = w.Write([]byte(`{"data": []}`))
		}
	}))
	t.Cleanup(backend.server.Close)
	return backend
}

func TestAnalyticsChatsTabRendersTableLikeRust(t *testing.T) {
	backend := startAnalyticsChatsTestBackend(t, "business", map[string]int64{"chat-a": 2_000_000, "chat-b": 500_000})
	model := newAnalyticsTestModel(t, "business", backend)
	model.analyticsChats = func(ctx context.Context, cursor *string, limit int) (analytics.AnalyticsChatPage, error) {
		now := time.Now().Unix()
		return analytics.AnalyticsChatPage{Data: []analytics.AnalyticsChatThread{
			{ID: "chat-a", Name: analyticsChatTestPtr("Weekly review"), UpdatedAt: now},
			{ID: "chat-b", Preview: "Scratch notes", UpdatedAt: now - 60},
			{ID: "chat-c", Preview: "Other account", UpdatedAt: now - 120},
		}}, nil
	}
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)

	dashboard := model.analyticsDashboard
	model.Update(runes("6"))
	if dashboard.section != analytics.AnalyticsSectionChats {
		t.Fatalf("section after 5 = %v, want chats", dashboard.section)
	}
	waitForAnalyticsLoads(t, model)
	rows, ok := dashboard.chatsReadyRows()
	if !ok || len(rows) != 3 {
		t.Fatalf("chats = %+v (ready %v)", rows, ok)
	}
	if rows[0].Title != "Weekly review" || rows[2].Usage != nil {
		t.Fatalf("rows = %+v", rows)
	}

	view := model.View()
	for _, want := range []string{
		"Top chats",
		"Active in past 30 days · sorted by lifetime credits",
		"Chat usage unavailable",
		"1 chat estimate unavailable",
		"Local chats · excludes archived chats and subagent usage",
		"tab/1–6",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("chats view missing %q:\n%s", want, view)
		}
	}

	// The footer escapes each key hint, so compare the unstyled text.
	plain := stripAnalyticsTestANSI(view)
	if !strings.Contains(plain, "row · ") || !strings.Contains(plain, "enter details") {
		t.Fatalf("chats footer missing the row navigation hint:\n%s", plain)
	}
	if strings.Contains(plain, "day · ") {
		t.Fatalf("chats footer kept the day navigation hint:\n%s", plain)
	}
	if strings.Contains(plain, "r 7/30d") {
		t.Fatalf("chats view kept the range hint:\n%s", view)
	}
}

func TestAnalyticsChatsZeroCreditToggleNeedsDetailLikeRust(t *testing.T) {
	backend := startAnalyticsChatsTestBackend(t, "business", map[string]int64{"chat-a": 2_000_000})
	model := newAnalyticsTestModel(t, "business", backend)
	model.analyticsChats = func(ctx context.Context, cursor *string, limit int) (analytics.AnalyticsChatPage, error) {
		now := time.Now().Unix()
		return analytics.AnalyticsChatPage{Data: []analytics.AnalyticsChatThread{
			{ID: "chat-a", Name: analyticsChatTestPtr("With estimate"), UpdatedAt: now},
			{ID: "chat-b", Preview: "No estimate", UpdatedAt: now - 60},
		}}, nil
	}
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)
	dashboard := model.analyticsDashboard
	model.Update(runes("6"))
	waitForAnalyticsLoads(t, model)

	model.Update(runes("a"))
	if dashboard.showZeroCreditGroups {
		t.Fatal("a toggled zero-credit groups without an open detail")
	}
	model.Update(runes(" "))
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail == nil || *detail != 0 {
		t.Fatalf("detail = %v, want 0", detail)
	}
	model.Update(runes("a"))
	if !dashboard.showZeroCreditGroups {
		t.Fatal("a did not reveal zero-credit groups with an open detail")
	}
	model.Update(runes("a"))
	if dashboard.showZeroCreditGroups {
		t.Fatal("a did not hide zero-credit groups again")
	}

	// Moving the cursor closes the detail, so the toggle stops working.
	model.Update(key(bubbletea.KeyDown))
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail != nil {
		t.Fatalf("detail after cursor move = %v, want nil", detail)
	}
	model.Update(runes("a"))
	if dashboard.showZeroCreditGroups {
		t.Fatal("a toggled zero-credit groups after the detail closed")
	}
	// A row without an estimate cannot open details, and move_right stays a no-op.
	model.Update(runes(" "))
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail != nil {
		t.Fatalf("detail on an unavailable row = %v, want nil", detail)
	}
	model.Update(key(bubbletea.KeyRight))
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail != nil {
		t.Fatalf("move_right opened details on an unavailable row: %v", detail)
	}
	// move_left closes an open detail instead of moving the cursor.
	model.Update(key(bubbletea.KeyUp))
	model.Update(key(bubbletea.KeyRight))
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail == nil || *detail != 0 {
		t.Fatalf("move_right detail = %v, want 0", detail)
	}
	model.Update(key(bubbletea.KeyLeft))
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail != nil {
		t.Fatalf("move_left did not close the detail: %v", detail)
	}
}

func TestAnalyticsChatsIgnoresRangeKeyLikeRust(t *testing.T) {
	backend := startAnalyticsChatsTestBackend(t, "business", map[string]int64{})
	model := newAnalyticsTestModel(t, "business", backend)
	model.analyticsChats = func(ctx context.Context, cursor *string, limit int) (analytics.AnalyticsChatPage, error) {
		return analytics.AnalyticsChatPage{Data: []analytics.AnalyticsChatThread{
			{ID: "chat-a", Preview: "Only chat", UpdatedAt: time.Now().Unix()},
		}}, nil
	}
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)
	dashboard := model.analyticsDashboard
	model.Update(runes("6"))
	waitForAnalyticsLoads(t, model)

	before := dashboard.ranges
	model.Update(runes("r"))
	if dashboard.ranges != before {
		t.Fatalf("r changed the chats range: %v -> %v", before, dashboard.ranges)
	}
	if view := model.View(); strings.Contains(view, "[30d]") {
		t.Fatalf("chats view rendered a range switch:\n%s", view)
	}
}

func TestAnalyticsChatsOverviewCardLikeRust(t *testing.T) {
	usage := map[string]int64{}
	threads := make([]analytics.AnalyticsChatThread, 0, 6)
	for index := 0; index < 6; index++ {
		id := fmt.Sprintf("chat-%d", index)
		usage[id] = int64(6-index) * 100_000
		threads = append(threads, analytics.AnalyticsChatThread{
			ID: id, Name: analyticsChatTestPtr(fmt.Sprintf("Chat %d", index)), UpdatedAt: time.Now().Unix(),
		})
	}
	backend := startAnalyticsChatsTestBackend(t, "business", usage)
	model := newAnalyticsTestModel(t, "business", backend)
	model.analyticsChats = func(ctx context.Context, cursor *string, limit int) (analytics.AnalyticsChatPage, error) {
		return analytics.AnalyticsChatPage{Data: threads}, nil
	}
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)
	dashboard := model.analyticsDashboard
	model.Update(runes("6"))
	waitForAnalyticsLoads(t, model)
	model.Update(runes("z"))
	if dashboard.zoomed {
		t.Fatal("z did not open the overview")
	}

	view := model.View()
	for _, want := range []string{"30d active · lifetime credits", "Local chats · excludes subagents"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overview missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, " Chat 5 ") || strings.Contains(view, "Chat 5") {
		t.Fatalf("overview rendered the sixth chat:\n%s", view)
	}
	if strings.Contains(view, "7d") {
		t.Fatalf("chats card rendered a range header:\n%s", view)
	}
}

func TestAnalyticsConsumerChatsTableAndSortCyclingLikeRust(t *testing.T) {
	backend := startAnalyticsConsumerChatsTestBackend(t, map[string]string{"chat-a": "12.500000000000000001"})
	model := newAnalyticsTestModel(t, "plus", backend)
	now := time.Now().Unix()
	model.analyticsChats = func(ctx context.Context, cursor *string, limit int) (analytics.AnalyticsChatPage, error) {
		return analytics.AnalyticsChatPage{Data: []analytics.AnalyticsChatThread{
			{ID: "chat-a", Name: analyticsChatTestPtr("Weekly review"), UpdatedAt: now},
			{ID: "chat-b", Preview: "Other account", UpdatedAt: now - 60},
		}}, nil
	}
	model.analyticsTaskThreads = func(ctx context.Context, ancestor string, archived bool, cursor *string, limit int) (analytics.AnalyticsTaskThreadPage, error) {
		return analytics.AnalyticsTaskThreadPage{}, nil
	}
	openAnalyticsFromUsageMenu(t, model)
	waitForAnalyticsLoads(t, model)
	dashboard := model.analyticsDashboard
	model.Update(runes("6"))
	if dashboard.section != analytics.AnalyticsSectionChats {
		t.Fatalf("section after 5 = %v, want chats", dashboard.section)
	}
	waitForAnalyticsLoads(t, model)
	tasks, ok := dashboard.tasks.Ready()
	if !ok || len(tasks.Rows) != 2 {
		t.Fatalf("tasks = %+v (ready %v)", tasks, ok)
	}
	if tasks.UpdatedAt == nil {
		t.Fatal("consumer tasks lost their freshness timestamp")
	}

	plain := stripAnalyticsTestANSI(model.View())
	for _, want := range []string{
		"Top chats",
		"Usage estimates · active in past 30 days · sorted by Weekly %",
		"Recorded usage / current full limit · may exceed 100%",
		"Weekly review",
		"Chat usage unavailable",
		"12.50",
		"Local chats · includes discovered descendants · excludes archived roots",
		"s sort · ",
		"row · ",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("consumer chats view missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "day · ") || strings.Contains(plain, "r 7/30d") {
		t.Fatalf("consumer chats kept the day or range hints:\n%s", plain)
	}

	model.Update(runes("s"))
	if dashboard.chatMetric != 1 {
		t.Fatalf("metric after s = %d, want 1", dashboard.chatMetric)
	}
	plain = stripAnalyticsTestANSI(model.View())
	if !strings.Contains(plain, "sorted by 5-hour %") || !strings.Contains(plain, "42.2%") {
		t.Fatalf("five-hour view missing:\n%s", plain)
	}
	model.Update(runes("s"))
	if dashboard.chatMetric != 2 {
		t.Fatalf("metric after second s = %d, want 2", dashboard.chatMetric)
	}
	plain = stripAnalyticsTestANSI(model.View())
	if !strings.Contains(plain, "sorted by Balance credits") ||
		!strings.Contains(plain, "Credits debited from balance · includes adjustments") {
		t.Fatalf("balance view missing:\n%s", plain)
	}
	model.Update(runes("s"))
	if dashboard.chatMetric != 0 {
		t.Fatalf("metric wrapped to %d, want 0", dashboard.chatMetric)
	}

	// The row without accounting cannot open details.
	model.Update(key(bubbletea.KeyDown))
	model.Update(runes(" "))
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail != nil {
		t.Fatalf("detail on an unavailable row = %v, want nil", detail)
	}
	model.Update(key(bubbletea.KeyUp))
	model.Update(runes(" "))
	if detail := dashboard.sections[analytics.AnalyticsSectionChats].Detail; detail == nil || *detail != 0 {
		t.Fatalf("detail = %v, want 0", detail)
	}

	// r never changes the consumer chats range.
	before := dashboard.ranges
	model.Update(runes("r"))
	if dashboard.ranges != before {
		t.Fatalf("r changed the chats range: %v -> %v", before, dashboard.ranges)
	}

	model.Update(runes("z"))
	if dashboard.zoomed {
		t.Fatal("z did not open the overview")
	}
	plain = stripAnalyticsTestANSI(model.View())
	if !strings.Contains(plain, "Showing top 2 of 2 chats") {
		t.Fatalf("consumer card missing its top-five counter:\n%s", plain)
	}
}
