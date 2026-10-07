package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"

	"github.com/google/uuid"
)

// Mirrors Rust #51595 (`thread_list_exclusions_refill_across_storage_paths_and_scopes`):
// thread/list drops the excluded thread IDs before the page limit is applied, so
// a page that would otherwise be short refills with the next eligible threads.
func TestRouterThreadListExcludedThreadIDsApplyBeforeLimit(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = uuid.NewString()
		created := now.Add(time.Duration(i) * time.Minute)
		if err := store.Create(&session.Record{
			ID:        session.ThreadID(ids[i]),
			SessionID: ids[i],
			Preview:   ids[i],
			CreatedAt: created,
			UpdatedAt: created,
			RecencyAt: created,
			Metadata: session.Metadata{
				CWD:           "D:/repo",
				ModelProvider: "openai",
				Source:        string(SessionSourceCli),
				HistoryMode:   string(ThreadHistoryLegacy),
			},
		}); err != nil {
			t.Fatalf("Create(%s) error = %v", ids[i], err)
		}
	}
	// Created-at descending order is ids[4], ids[3], ids[2], ids[1], ids[0].
	limit := 2
	control := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{Limit: &limit}))
	if control.Error != nil {
		t.Fatalf("thread/list control error = %+v", control.Error)
	}
	controlPage := control.Result.(*ThreadListResponse)
	if got := threadListIDs(controlPage.Data); !reflect.DeepEqual(got, []string{ids[4], ids[3]}) {
		t.Fatalf("control page = %v, want %v", got, []string{ids[4], ids[3]})
	}
	if controlPage.NextCursor == nil {
		t.Fatal("control page must carry a next cursor")
	}

	// A dense excluded prefix plus a duplicate entry and an unknown ID: the
	// duplicates count toward the bound, and unknown IDs simply match nothing.
	excluded := []string{ids[4], ids[3], ids[2], ids[4], uuid.NewString()}
	filtered := router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{Limit: &limit, ExcludedThreadIDs: &excluded}))
	if filtered.Error != nil {
		t.Fatalf("thread/list exclusion error = %+v", filtered.Error)
	}
	filteredPage := filtered.Result.(*ThreadListResponse)
	if got := threadListIDs(filteredPage.Data); !reflect.DeepEqual(got, []string{ids[1], ids[0]}) {
		t.Fatalf("excluded page = %v, want %v", got, []string{ids[1], ids[0]})
	}
	if filteredPage.NextCursor != nil {
		t.Fatalf("excluded page nextCursor = %q, want nil", *filteredPage.NextCursor)
	}

	// Omitted, null and empty lists all preserve existing behavior.
	empty := []string{}
	for name, params := range map[string]ThreadListParams{
		"omitted": {Limit: &limit},
		"null":    {Limit: &limit, ExcludedThreadIDs: nil},
		"empty":   {Limit: &limit, ExcludedThreadIDs: &empty},
	} {
		response := router.Handle(requestWithParams(t, IntID(3), MethodThreadList, params))
		if response.Error != nil {
			t.Fatalf("thread/list %s error = %+v", name, response.Error)
		}
		if got := threadListIDs(response.Result.(*ThreadListResponse).Data); !reflect.DeepEqual(got, []string{ids[4], ids[3]}) {
			t.Fatalf("thread/list %s page = %v, want %v", name, got, []string{ids[4], ids[3]})
		}
	}
}

// Mirrors the database-only half of Rust #51595: exclusions also apply to the
// rows read from the state database.
func TestRouterThreadListExcludedThreadIDsApplyOnStatePath(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	stateConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, stateConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	router := NewRouter(session.NewStore(home))
	router.SetStateRuntime(runtime)

	ids := make([]string, 3)
	for i := range ids {
		ids[i] = uuid.NewString()
		now := fixedTime().Add(time.Duration(i) * time.Minute)
		recorder, err := rollout.NewRecorder(&rollout.CreateParams{
			CodexHome:     home,
			ThreadID:      ids[i],
			SessionID:     ids[i],
			Source:        "cli",
			CWD:           home,
			ModelProvider: "openai",
			Now:           now,
		})
		if err != nil {
			t.Fatalf("NewRecorder(%s) error = %v", ids[i], err)
		}
		payload, _ := json.Marshal(map[string]any{"type": "user_message", "message": "preview " + ids[i]})
		if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(time.Second).Format(time.RFC3339Nano), Payload: payload}); err != nil {
			t.Fatalf("AppendLine(%s) error = %v", ids[i], err)
		}
		if err := recorder.Close(); err != nil {
			t.Fatalf("Close(%s) error = %v", ids[i], err)
		}
		if err := runtime.ReconcileRollout(ctx, recorder.Path(), false); err != nil {
			t.Fatalf("ReconcileRollout(%s) error = %v", ids[i], err)
		}
	}

	limit := 10
	all := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{UseStateDBOnly: true, Limit: &limit}))
	if all.Error != nil {
		t.Fatalf("thread/list state error = %+v", all.Error)
	}
	if got := threadListIDs(all.Result.(*ThreadListResponse).Data); !reflect.DeepEqual(got, []string{ids[2], ids[1], ids[0]}) {
		t.Fatalf("state page = %v, want %v", got, []string{ids[2], ids[1], ids[0]})
	}

	excluded := []string{ids[2], ids[0]}
	filtered := router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{UseStateDBOnly: true, Limit: &limit, ExcludedThreadIDs: &excluded}))
	if filtered.Error != nil {
		t.Fatalf("thread/list state exclusion error = %+v", filtered.Error)
	}
	if got := threadListIDs(filtered.Result.(*ThreadListResponse).Data); !reflect.DeepEqual(got, []string{ids[1]}) {
		t.Fatalf("state excluded page = %v, want %v", got, []string{ids[1]})
	}
}

// Mirrors Rust #51595 (`thread_list_exclusions_validate_bound_without_truncating...`):
// oversized and invalid exclusion lists are rejected with -32602 rather than
// truncated, and the last entry still takes effect at the 100-entry bound.
func TestRouterThreadListExcludedThreadIDsValidateBoundAndIDs(t *testing.T) {
	router := NewRouter(session.NewStore(t.TempDir()))

	atBound := make([]string, 0, maxThreadListExcludedIDs)
	for i := 0; i < maxThreadListExcludedIDs; i++ {
		atBound = append(atBound, uuid.NewString())
	}
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{ExcludedThreadIDs: &atBound}))
	if response.Error != nil {
		t.Fatalf("thread/list at the 100-entry bound error = %+v", response.Error)
	}

	overBound := append(append([]string(nil), atBound...), uuid.NewString())
	response = router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{ExcludedThreadIDs: &overBound}))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("thread/list oversized exclusions = %+v, want code %d", response, JSONRPCInvalidParamsErrorCode)
	}
	if want := fmt.Sprintf("excludedThreadIds accepts at most %d entries", maxThreadListExcludedIDs); !strings.HasPrefix(response.Error.Message, want) {
		t.Fatalf("thread/list oversized exclusions message = %q, want prefix %q", response.Error.Message, want)
	}

	// Duplicates count toward the bound: 100 copies plus one more is oversized.
	duplicates := make([]string, 0, maxThreadListExcludedIDs+1)
	id := uuid.NewString()
	for i := 0; i <= maxThreadListExcludedIDs; i++ {
		duplicates = append(duplicates, id)
	}
	response = router.Handle(requestWithParams(t, IntID(3), MethodThreadList, ThreadListParams{ExcludedThreadIDs: &duplicates}))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("thread/list duplicate-heavy exclusions = %+v, want code %d", response, JSONRPCInvalidParamsErrorCode)
	}

	invalid := []string{"not-a-thread"}
	response = router.Handle(requestWithParams(t, IntID(4), MethodThreadList, ThreadListParams{ExcludedThreadIDs: &invalid}))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("thread/list invalid exclusions = %+v, want code %d", response, JSONRPCInvalidParamsErrorCode)
	}
	if want := "invalid excluded thread id"; !strings.HasPrefix(response.Error.Message, want) {
		t.Fatalf("thread/list invalid exclusions message = %q, want prefix %q", response.Error.Message, want)
	}
}
