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

// Mirrors Rust #61446 (`thread_list_inclusions_reconcile_old_moved_and_archived_ids`):
// includedThreadIds restricts the listing before pagination, keeps the
// requested sort order, treats an empty list as matching nothing, and lets
// exclusions take precedence.
func TestRouterThreadListIncludedThreadIDsApplyBeforeLimit(t *testing.T) {
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
	included := []string{ids[0], ids[4], ids[0], uuid.NewString()}
	limit := 1
	first := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{
		Limit: &limit, IncludedThreadIDs: &included,
	}))
	if first.Error != nil {
		t.Fatalf("thread/list inclusion error = %+v", first.Error)
	}
	firstPage := first.Result.(*ThreadListResponse)
	if got := threadListIDs(firstPage.Data); !reflect.DeepEqual(got, []string{ids[4]}) {
		t.Fatalf("first included page = %v, want %v", got, []string{ids[4]})
	}
	if firstPage.NextCursor == nil {
		t.Fatal("first included page must carry a next cursor")
	}
	second := router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{
		Limit: &limit, Cursor: firstPage.NextCursor, IncludedThreadIDs: &included,
	}))
	if second.Error != nil {
		t.Fatalf("thread/list inclusion second page error = %+v", second.Error)
	}
	secondPage := second.Result.(*ThreadListResponse)
	if got := threadListIDs(secondPage.Data); !reflect.DeepEqual(got, []string{ids[0]}) {
		t.Fatalf("second included page = %v, want %v", got, []string{ids[0]})
	}
	if secondPage.NextCursor != nil {
		t.Fatalf("second included page nextCursor = %q, want nil", *secondPage.NextCursor)
	}

	// Exclusions take precedence over inclusions.
	excluded := []string{ids[4]}
	filtered := router.Handle(requestWithParams(t, IntID(3), MethodThreadList, ThreadListParams{
		IncludedThreadIDs: &included, ExcludedThreadIDs: &excluded,
	}))
	if filtered.Error != nil {
		t.Fatalf("thread/list inclusion+exclusion error = %+v", filtered.Error)
	}
	if got := threadListIDs(filtered.Result.(*ThreadListResponse).Data); !reflect.DeepEqual(got, []string{ids[0]}) {
		t.Fatalf("included+excluded page = %v, want %v", got, []string{ids[0]})
	}

	// An empty inclusion list matches nothing; omitted and null are unrestricted.
	empty := []string{}
	for name, params := range map[string]ThreadListParams{
		"omitted": {Limit: &limit},
		"null":    {Limit: &limit, IncludedThreadIDs: nil},
		"empty":   {Limit: &limit, IncludedThreadIDs: &empty},
	} {
		response := router.Handle(requestWithParams(t, IntID(4), MethodThreadList, params))
		if response.Error != nil {
			t.Fatalf("thread/list %s error = %+v", name, response.Error)
		}
		got := threadListIDs(response.Result.(*ThreadListResponse).Data)
		want := []string{ids[4]}
		if name == "empty" {
			want = []string{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("thread/list %s page = %v, want %v", name, got, want)
		}
	}

	// Archived threads are selected with the archived listing flag.
	archivedID := uuid.NewString()
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(archivedID),
		SessionID: archivedID,
		Preview:   archivedID,
		Archived:  true,
		CreatedAt: now.Add(time.Hour),
		UpdatedAt: now.Add(time.Hour),
		RecencyAt: now.Add(time.Hour),
		Metadata: session.Metadata{
			CWD:           "D:/repo",
			ModelProvider: "openai",
			Source:        string(SessionSourceCli),
			HistoryMode:   string(ThreadHistoryLegacy),
		},
	}); err != nil {
		t.Fatalf("Create(%s) error = %v", archivedID, err)
	}
	archivedIncluded := []string{archivedID}
	archived := router.Handle(requestWithParams(t, IntID(5), MethodThreadList, ThreadListParams{
		Archived: boolPtr(true), IncludedThreadIDs: &archivedIncluded,
	}))
	if archived.Error != nil {
		t.Fatalf("thread/list archived inclusion error = %+v", archived.Error)
	}
	if got := threadListIDs(archived.Result.(*ThreadListResponse).Data); !reflect.DeepEqual(got, []string{archivedID}) {
		t.Fatalf("archived included page = %v, want %v", got, []string{archivedID})
	}
}

// Mirrors the validation half of Rust #61446: both filters share the 100-entry
// bound, invalid IDs are rejected with the field-specific message, and lists
// are never truncated.
func TestRouterThreadListIncludedThreadIDsValidateBoundAndIDs(t *testing.T) {
	router := NewRouter(session.NewStore(t.TempDir()))

	atBound := make([]string, 0, maxThreadListFilterIDs)
	for i := 0; i < maxThreadListFilterIDs; i++ {
		atBound = append(atBound, uuid.NewString())
	}
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{IncludedThreadIDs: &atBound}))
	if response.Error != nil {
		t.Fatalf("thread/list at the 100-entry inclusion bound error = %+v", response.Error)
	}

	overBound := append(append([]string(nil), atBound...), uuid.NewString())
	response = router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{IncludedThreadIDs: &overBound}))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("thread/list oversized inclusions = %+v, want code %d", response, JSONRPCInvalidParamsErrorCode)
	}
	if want := fmt.Sprintf("includedThreadIds accepts at most %d entries", maxThreadListFilterIDs); !strings.HasPrefix(response.Error.Message, want) {
		t.Fatalf("thread/list oversized inclusions message = %q, want prefix %q", response.Error.Message, want)
	}

	invalid := []string{"not-a-thread"}
	response = router.Handle(requestWithParams(t, IntID(3), MethodThreadList, ThreadListParams{IncludedThreadIDs: &invalid}))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("thread/list invalid inclusions = %+v, want code %d", response, JSONRPCInvalidParamsErrorCode)
	}
	if want := "invalid included thread id"; !strings.HasPrefix(response.Error.Message, want) {
		t.Fatalf("thread/list invalid inclusions message = %q, want prefix %q", response.Error.Message, want)
	}
}

// Mirrors Rust #61446's indexed-metadata rule for the state database path:
// includedThreadIds never triggers a scan-and-repair, so a rollout that has not
// been indexed does not match even when useStateDbOnly is false.
func TestRouterThreadListIncludedThreadIDsUseIndexedStateWithoutRepair(t *testing.T) {
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

	writeRollout := func(id string, indexed bool) {
		t.Helper()
		now := fixedTime()
		recorder, err := rollout.NewRecorder(&rollout.CreateParams{
			CodexHome:     home,
			ThreadID:      id,
			SessionID:     id,
			Source:        "cli",
			CWD:           home,
			ModelProvider: "openai",
			Now:           now,
		})
		if err != nil {
			t.Fatalf("NewRecorder(%s) error = %v", id, err)
		}
		payload, _ := json.Marshal(map[string]any{"type": "user_message", "message": "preview " + id})
		if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(time.Second).Format(time.RFC3339Nano), Payload: payload}); err != nil {
			t.Fatalf("AppendLine(%s) error = %v", id, err)
		}
		if err := recorder.Close(); err != nil {
			t.Fatalf("Close(%s) error = %v", id, err)
		}
		if indexed {
			if err := runtime.ReconcileRollout(ctx, recorder.Path(), false); err != nil {
				t.Fatalf("ReconcileRollout(%s) error = %v", id, err)
			}
		}
	}
	indexedID := uuid.NewString()
	unindexedID := uuid.NewString()
	writeRollout(indexedID, true)
	writeRollout(unindexedID, false)

	included := []string{indexedID, unindexedID}
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{
		IncludedThreadIDs: &included,
	}))
	if response.Error != nil {
		t.Fatalf("thread/list state inclusion error = %+v", response.Error)
	}
	if got := threadListIDs(response.Result.(*ThreadListResponse).Data); !reflect.DeepEqual(got, []string{indexedID}) {
		t.Fatalf("state included page = %v, want only %s", got, indexedID)
	}
}
