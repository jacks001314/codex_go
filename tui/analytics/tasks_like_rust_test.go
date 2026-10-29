package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests mirror Rust #45768's consumer task coverage: consumer-only
// loading, archived descendant discovery, truncation and freshness, overlap
// rejection, and the descendant chain checks.

func taskTestRoots(threads ...AnalyticsChatThread) AnalyticsChatLister {
	return func(ctx context.Context, cursor *string, limit int) (AnalyticsChatPage, error) {
		if cursor != nil {
			return AnalyticsChatPage{}, nil
		}
		return AnalyticsChatPage{Data: threads}, nil
	}
}

func taskTestDescendants(pages map[string]map[bool][]AnalyticsTaskThreadPage) AnalyticsTaskDescendantLister {
	var mutex sync.Mutex
	positions := map[string]int{}
	return func(ctx context.Context, ancestor string, archived bool, cursor *string, limit int) (AnalyticsTaskThreadPage, error) {
		mutex.Lock()
		defer mutex.Unlock()
		key := fmt.Sprintf("%s/%t", ancestor, archived)
		index := positions[key]
		positions[key] = index + 1
		list := pages[ancestor][archived]
		if index >= len(list) {
			return AnalyticsTaskThreadPage{}, nil
		}
		return list[index], nil
	}
}

func taskTestStringPtr(value string) *string {
	return &value
}

func TestLoadAnalyticsTasksConsumerOnlyLikeRust(t *testing.T) {
	server, recorder := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		analyticsServeAccountsCheck(w, request, "account-a", "business")
	})
	_, live := analyticsLiveForTest(t, server, "business")
	called := false
	tasks, err := LoadAnalyticsTasks(context.Background(), live,
		func(context.Context, *string, int) (AnalyticsChatPage, error) {
			called = true
			return AnalyticsChatPage{}, nil
		},
		func(context.Context, string, bool, *string, int) (AnalyticsTaskThreadPage, error) {
			called = true
			return AnalyticsTaskThreadPage{}, nil
		})
	if err != nil || tasks != nil {
		t.Fatalf("tasks = %+v, err = %v", tasks, err)
	}
	if called {
		t.Fatal("a non-consumer account listed tasks")
	}
	if len(recorder.reportRequests()) != 0 {
		t.Fatalf("reports = %+v", recorder.reportRequests())
	}
}

func TestLoadAnalyticsTasksDiscoversArchivedDescendantsLikeRust(t *testing.T) {
	var requestBody struct {
		Threads []struct {
			ThreadID            string   `json:"thread_id"`
			CreatedAt           *string  `json:"created_at"`
			DescendantThreadIDs []string `json:"descendant_thread_ids"`
		} `json:"threads"`
	}
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
			return
		}
		if request.URL.Path != "/backend-api/wham/usage/thread_usage/query_v2" {
			t.Errorf("path = %q", request.URL.Path)
			analyticsWriteJSON(w, `{"data": []}`)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		threads := make([]string, 0, len(requestBody.Threads))
		for _, thread := range requestBody.Threads {
			if thread.ThreadID == "root" {
				threads = append(threads, `{"thread_id":"root","data_status":"available","usage_source":"plan_and_credits",`+
					`"five_hour_limit_percent":150.5,"weekly_limit_percent":12.25,"balance_usage_credits":"12.500000000000000001",`+
					`"groups":[{"product_experience":"codex","model":"gpt-5.5","reasoning_effort":"high","speed":"standard",`+
					`"five_hour_limit_percent":75.25,"weekly_limit_percent":6.5,"balance_usage_credits":"0"}]}`)
				continue
			}
			threads = append(threads, fmt.Sprintf(`{"thread_id":%q,"data_status":"unavailable","usage_source":"none","groups":[]}`, thread.ThreadID))
		}
		analyticsWriteJSON(w, `{"data_as_of":"2026-10-01T12:30:00Z","threads":[`+strings.Join(threads, ",")+`]}`)
	})
	_, live := analyticsLiveForTest(t, server, "plus")

	now := time.Now().Unix()
	roots := []AnalyticsChatThread{
		{ID: "missing", Preview: "Other account chat", UpdatedAt: now - 600},
		{ID: "root", Name: taskTestStringPtr("Weekly review"), UpdatedAt: now, CreatedAt: now - 86_400},
	}
	descendants := taskTestDescendants(map[string]map[bool][]AnalyticsTaskThreadPage{
		"root": {
			false: {
				{Data: []AnalyticsChatThread{{ID: "child-b", ParentThreadID: taskTestStringPtr("root"), UpdatedAt: now}}, NextCursor: taskTestStringPtr("page-2")},
				{Data: []AnalyticsChatThread{{ID: "child-a", ParentThreadID: taskTestStringPtr("root"), UpdatedAt: now}}},
			},
			true: {
				{Data: []AnalyticsChatThread{{ID: "grandchild", ParentThreadID: taskTestStringPtr("child-a"), UpdatedAt: now}}},
			},
		},
	})
	tasks, err := LoadAnalyticsTasks(context.Background(), live, taskTestRoots(roots...), descendants)
	if err != nil {
		t.Fatalf("LoadAnalyticsTasks error = %v", err)
	}
	if tasks == nil || len(tasks.Rows) != 2 {
		t.Fatalf("tasks = %+v", tasks)
	}
	if tasks.Truncated {
		t.Fatal("two chats were reported as truncated")
	}
	if tasks.Rows[0].Title != "Weekly review" || tasks.Rows[1].Title != "Other account chat" {
		t.Fatalf("root order = %+v", tasks.Rows)
	}
	if tasks.Rows[1].Task == nil || tasks.Rows[1].Task.DataStatus != "unavailable" ||
		tasks.Rows[1].DisplayTitle() != analyticsChatUnavailableTitle {
		t.Fatalf("unavailable row = %+v", tasks.Rows[1])
	}
	row := tasks.Rows[0]
	if row.Task == nil || row.Task.DataStatus != "available" {
		t.Fatalf("available row = %+v", row)
	}
	if row.Task.Amounts.WeeklyLimitPercent == nil || *row.Task.Amounts.WeeklyLimitPercent != 12.25 {
		t.Fatalf("weekly percent = %+v", row.Task.Amounts.WeeklyLimitPercent)
	}
	if row.Task.Amounts.BalanceUsageCredits == nil || row.Task.Amounts.BalanceUsageCredits.String() != "12.500000000000000001" {
		t.Fatalf("balance credits = %+v", row.Task.Amounts.BalanceUsageCredits)
	}
	if len(requestBody.Threads) != 2 {
		t.Fatalf("requested threads = %+v", requestBody.Threads)
	}
	var rootQuery *struct {
		ThreadID            string   `json:"thread_id"`
		CreatedAt           *string  `json:"created_at"`
		DescendantThreadIDs []string `json:"descendant_thread_ids"`
	}
	for index := range requestBody.Threads {
		if requestBody.Threads[index].ThreadID == "root" {
			rootQuery = &requestBody.Threads[index]
		}
	}
	if rootQuery == nil || strings.Join(rootQuery.DescendantThreadIDs, ",") != "child-a,child-b,grandchild" {
		t.Fatalf("root query = %+v", requestBody.Threads)
	}
	if rootQuery.CreatedAt == nil ||
		*rootQuery.CreatedAt != time.Unix(now-86_400, 0).UTC().Format(time.RFC3339) {
		t.Fatalf("created_at = %+v", rootQuery.CreatedAt)
	}
	if tasks.UpdatedAt == nil || !tasks.UpdatedAt.Equal(time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)) {
		t.Fatalf("updated_at = %v", tasks.UpdatedAt)
	}
}

func TestLoadAnalyticsTasksTruncatesAndClearsFreshnessLikeRust(t *testing.T) {
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		if analyticsServeAccountsCheck(w, request, "account-a", "plus") {
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		analyticsWriteJSON(w, `{"detail": "temporarily unavailable"}`)
	})
	_, live := analyticsLiveForTest(t, server, "plus")
	now := time.Now().Unix()
	roots := make([]AnalyticsChatThread, 0, 101)
	for index := 0; index < 101; index++ {
		roots = append(roots, AnalyticsChatThread{
			ID:        fmt.Sprintf("chat-%03d", index),
			Preview:   fmt.Sprintf("Chat %d", index),
			UpdatedAt: now - int64(index),
		})
	}
	descendants := taskTestDescendants(nil)
	tasks, err := LoadAnalyticsTasks(context.Background(), live, taskTestRoots(roots...), descendants)
	if err != nil {
		t.Fatalf("LoadAnalyticsTasks error = %v", err)
	}
	if tasks == nil || !tasks.Truncated || len(tasks.Rows) != analyticsTaskChatLimit {
		t.Fatalf("tasks = %+v", tasks)
	}
	if tasks.UpdatedAt != nil {
		t.Fatalf("updated_at = %v, want nil after an outage", tasks.UpdatedAt)
	}
	for _, row := range tasks.Rows {
		if row.Task != nil {
			t.Fatalf("row %q kept usage from a failed batch", row.Title)
		}
	}
	if tasks.Rows[0].Title != "Chat 0" || tasks.Rows[99].Title != "Chat 99" {
		t.Fatalf("truncated ranking = %q .. %q", tasks.Rows[0].Title, tasks.Rows[99].Title)
	}
}

func TestLoadAnalyticsTasksRejectsOverlappingDescendantsLikeRust(t *testing.T) {
	server, _ := analyticsTestServer(t, func(w http.ResponseWriter, request *http.Request) {
		analyticsServeAccountsCheck(w, request, "account-a", "plus")
	})
	_, live := analyticsLiveForTest(t, server, "plus")
	now := time.Now().Unix()
	roots := []AnalyticsChatThread{
		{ID: "root-a", UpdatedAt: now},
		{ID: "root-b", UpdatedAt: now - 1},
	}
	descendants := func(ctx context.Context, ancestor string, archived bool, cursor *string, limit int) (AnalyticsTaskThreadPage, error) {
		if archived {
			return AnalyticsTaskThreadPage{}, nil
		}
		parent := ancestor
		return AnalyticsTaskThreadPage{Data: []AnalyticsChatThread{
			{ID: "shared", ParentThreadID: &parent, UpdatedAt: now},
		}}, nil
	}
	tasks, err := LoadAnalyticsTasks(context.Background(), live, taskTestRoots(roots...), descendants)
	if err == nil || err.Error() != analyticsTaskOverlapMessage {
		t.Fatalf("tasks = %+v, err = %v, want %q", tasks, err, analyticsTaskOverlapMessage)
	}
}

func TestAnalyticsTaskDescendantsValidateChainsLikeRust(t *testing.T) {
	root := AnalyticsChatThread{ID: "root", UpdatedAt: time.Now().Unix()}
	parent := func(id string) *string { return &id }
	cases := []struct {
		name  string
		pages map[bool]AnalyticsTaskThreadPage
		want  string
	}{
		{
			name: "repeated cursor",
			pages: map[bool]AnalyticsTaskThreadPage{
				false: {Data: []AnalyticsChatThread{{ID: "child", ParentThreadID: parent("root")}}, NextCursor: taskTestStringPtr("same")},
			},
			want: analyticsTaskCursorMessage,
		},
		{
			name: "missing parent",
			pages: map[bool]AnalyticsTaskThreadPage{
				false: {Data: []AnalyticsChatThread{{ID: "child"}}},
			},
			want: analyticsTaskUnsupportedMessage,
		},
		{
			name: "cycle",
			pages: map[bool]AnalyticsTaskThreadPage{
				false: {Data: []AnalyticsChatThread{
					{ID: "a", ParentThreadID: parent("b")},
					{ID: "b", ParentThreadID: parent("a")},
				}},
			},
			want: analyticsTaskCycleMessage,
		},
		{
			name: "incomplete",
			pages: map[bool]AnalyticsTaskThreadPage{
				false: {Data: []AnalyticsChatThread{{ID: "child", ParentThreadID: parent("unknown")}}},
			},
			want: analyticsTaskIncompleteMessage,
		},
	}
	for _, testCase := range cases {
		lister := func(ctx context.Context, ancestor string, archived bool, cursor *string, limit int) (AnalyticsTaskThreadPage, error) {
			if archived {
				return AnalyticsTaskThreadPage{}, nil
			}
			return testCase.pages[false], nil
		}
		_, err := analyticsTaskDescendants(context.Background(), lister, root)
		if err == nil || err.Error() != testCase.want {
			t.Fatalf("%s error = %v, want %q", testCase.name, err, testCase.want)
		}
	}

	many := make([]AnalyticsChatThread, 0, 1000)
	for index := 0; index < 1000; index++ {
		many = append(many, AnalyticsChatThread{ID: fmt.Sprintf("child-%d", index), ParentThreadID: parent("root")})
	}
	_, err := analyticsTaskDescendants(context.Background(), func(ctx context.Context, ancestor string, archived bool, cursor *string, limit int) (AnalyticsTaskThreadPage, error) {
		if archived || cursor != nil {
			return AnalyticsTaskThreadPage{}, nil
		}
		return AnalyticsTaskThreadPage{Data: many}, nil
	}, root)
	if err == nil || err.Error() != analyticsTaskLimitMessage {
		t.Fatalf("limit error = %v, want %q", err, analyticsTaskLimitMessage)
	}
}
