package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/auth"
)

// These tests mirror Rust #45765's chats coverage: plan eligibility, bounded
// estimate batches with partial outages, title hiding, pagination, and ranking.

func TestAnalyticsThreadUsageSupportedMatchesRust(t *testing.T) {
	for _, plan := range []auth.PlanType{
		auth.PlanBusiness,
		auth.PlanEnterpriseCBPUsageBased,
		auth.PlanEnterpriseCBPAutomation,
	} {
		if !AnalyticsThreadUsageSupported(plan) {
			t.Errorf("plan %q should expose thread usage", plan)
		}
	}
	for _, plan := range []auth.PlanType{
		auth.PlanTeam,
		auth.PlanEnt26,
		auth.PlanEnterprise,
		auth.PlanPlus,
		auth.PlanUnknown,
	} {
		if AnalyticsThreadUsageSupported(plan) {
			t.Errorf("plan %q should not expose thread usage", plan)
		}
	}
}

func TestLoadAnalyticsChatsRetainsAvailableEstimatesLikeRust(t *testing.T) {
	var requests atomic.Int32
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "enterprise_cbp_usage_based") {
			return
		}
		if request.URL.Path != "/backend-api/wham/usage/thread_usage/query" {
			analyticsWriteJSON(w, `{"data": []}`)
			return
		}
		requests.Add(1)
		var body struct {
			ThreadIDs []string `json:"thread_ids"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(body.ThreadIDs) > 1 || body.ThreadIDs[0] == "incomplete" {
			w.WriteHeader(http.StatusServiceUnavailable)
			analyticsWriteJSON(w, `{"detail": "Thread usage is temporarily unavailable."}`)
			return
		}
		thread := map[string]any{"thread_id": body.ThreadIDs[0], "estimated_usage_usd_micros": nil, "groups": nil}
		if body.ThreadIDs[0] != "missing" {
			thread["estimated_usage_credits_micros"] = 1250000
		}
		analyticsWriteJSON(w, `{"threads": [`+mustJSON(t, thread)+`]}`)
	})
	_, live := analyticsLiveForTest(t, server, "enterprise_cbp_usage_based")

	now := time.Now().Unix()
	name := "Named chat"
	parent := "root"
	lister := func(ctx context.Context, cursor *string, limit int) (AnalyticsChatPage, error) {
		if limit != analyticsChatListPageSize {
			t.Errorf("limit = %d", limit)
		}
		return AnalyticsChatPage{Data: []AnalyticsChatThread{
			{ID: "available", Name: &name, UpdatedAt: now},
			{ID: "incomplete", Preview: "Preview chat", UpdatedAt: now},
			{ID: "missing", Preview: "Missing chat", UpdatedAt: now},
			{ID: "ephemeral", Preview: "Ephemeral", UpdatedAt: now, Ephemeral: true},
			{ID: "subagent", Preview: "Subagent", UpdatedAt: now, ParentThreadID: &parent},
		}}, nil
	}
	chats, err := LoadAnalyticsChats(context.Background(), live, lister)
	if err != nil {
		t.Fatalf("LoadAnalyticsChats error = %v", err)
	}
	if chats == nil || len(chats.Rows) != 3 {
		t.Fatalf("chats = %+v", chats)
	}
	if requests.Load() != 5 {
		t.Fatalf("requests = %d, want 5 (3-way batch, two bisections, single retries)", requests.Load())
	}
	first := chats.Rows[0]
	if first.Title != "Named chat" || first.Usage == nil || first.Usage.EstimatedUsageCreditsMicros != 1250000 {
		t.Fatalf("first row = %+v", first)
	}
	for _, row := range chats.Rows[1:] {
		if row.Usage != nil {
			t.Fatalf("row %q kept an unavailable estimate: %+v", row.Title, row.Usage)
		}
		if row.DisplayTitle() != analyticsChatUnavailableTitle {
			t.Fatalf("row title = %q, want %q", row.DisplayTitle(), analyticsChatUnavailableTitle)
		}
	}
}

func TestLoadAnalyticsChatsSkipsUnsupportedPlansLikeRust(t *testing.T) {
	server, recorder := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		analyticsServeAccountsCheck(w, request, "account-a", "plus")
	})
	_, live := analyticsLiveForTest(t, server, "plus")
	called := false
	chats, err := LoadAnalyticsChats(context.Background(), live, func(context.Context, *string, int) (AnalyticsChatPage, error) {
		called = true
		return AnalyticsChatPage{}, nil
	})
	if err != nil || chats != nil {
		t.Fatalf("chats = %+v, err = %v", chats, err)
	}
	if called {
		t.Fatal("unsupported plan listed chats")
	}
	if len(recorder.reportRequests()) != 0 {
		t.Fatalf("reports = %+v", recorder.reportRequests())
	}
}

func TestAnalyticsChatRootsFiltersPagesAndDeduplicatesLikeRust(t *testing.T) {
	now := time.Now().Unix()
	old := now - int64(analyticsChatWindow/time.Second) - 1
	parent := "root"
	pages := []AnalyticsChatPage{
		{
			Data: []AnalyticsChatThread{
				{ID: "recent", UpdatedAt: now},
				{ID: "ephemeral", UpdatedAt: now, Ephemeral: true},
				{ID: "subagent", UpdatedAt: now, ParentThreadID: &parent},
			},
			NextCursor: analyticsChatStringPtr("next"),
		},
		{
			Data: []AnalyticsChatThread{
				{ID: "recent", UpdatedAt: now},
				{ID: "older-root", UpdatedAt: now - 60},
				{ID: "too-old", UpdatedAt: old},
			},
			NextCursor: analyticsChatStringPtr("ignored"),
		},
	}
	index := 0
	threads, err := analyticsChatRoots(context.Background(), func(ctx context.Context, cursor *string, limit int) (AnalyticsChatPage, error) {
		page := pages[index]
		index++
		if cursor != nil && *cursor != "next" {
			t.Errorf("cursor = %q", *cursor)
		}
		return page, nil
	})
	if err != nil {
		t.Fatalf("analyticsChatRoots error = %v", err)
	}
	if index != 2 {
		t.Fatalf("pages = %d, want 2 (stop once the cutoff is reached)", index)
	}
	if len(threads) != 2 || threads[0].ID != "recent" || threads[1].ID != "older-root" {
		t.Fatalf("threads = %+v", threads)
	}
}

func TestAnalyticsChatRootsRejectsRepeatedCursorLikeRust(t *testing.T) {
	_, err := analyticsChatRoots(context.Background(), func(ctx context.Context, cursor *string, limit int) (AnalyticsChatPage, error) {
		return AnalyticsChatPage{
			Data:       []AnalyticsChatThread{{ID: "chat", UpdatedAt: time.Now().Unix()}},
			NextCursor: analyticsChatStringPtr("same"),
		}, nil
	})
	if err == nil || err.Error() != analyticsChatRepeatedCursorMessage {
		t.Fatalf("error = %v, want %q", err, analyticsChatRepeatedCursorMessage)
	}
}

func TestAnalyticsChatEstimatesBoundRequestsDuringOutageLikeRust(t *testing.T) {
	var requests atomic.Int32
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "enterprise_cbp_usage_based") {
			return
		}
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, live := analyticsLiveForTest(t, server, "enterprise_cbp_usage_based")
	session, err := live.Session(context.Background())
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	ids := make([]string, 0, 100)
	for index := 0; index < 100; index++ {
		ids = append(ids, fmt.Sprintf("chat-%03d", index))
	}
	rows, err := analyticsChatEstimates(context.Background(), session, ids)
	if err != nil {
		t.Fatalf("analyticsChatEstimates error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
	if requests.Load() != analyticsChatEstimateRequests {
		t.Fatalf("requests = %d, want %d", requests.Load(), analyticsChatEstimateRequests)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func analyticsChatStringPtr(value string) *string {
	return &value
}
