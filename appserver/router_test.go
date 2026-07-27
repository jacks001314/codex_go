package appserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"codex_go/compact"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/session"

	"github.com/google/uuid"
)

func TestRouterStartPromptDoesNotMaterializeThreadLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })

	startRequest := requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:           "D:/repo",
		Prompt:        "hello",
		ModelProvider: "openai",
		HistoryMode:   ThreadHistoryLegacy,
	})
	startResponse := router.Handle(startRequest)
	if startResponse.Error != nil {
		t.Fatalf("start error: %+v", startResponse.Error)
	}

	start, ok := startResponse.Result.(*ThreadStartResponse)
	if !ok {
		t.Fatalf("unexpected start result type: %T", startResponse.Result)
	}
	if _, err := uuid.Parse(start.Thread.ID); err != nil {
		t.Fatalf("started thread id = %q, want UUID: %v", start.Thread.ID, err)
	}
	if start.Thread.CWD != "D:/repo" {
		t.Fatalf("unexpected started thread: %+v", start.Thread)
	}
	if start.Thread.Status.Type != "idle" {
		t.Fatalf("start thread status = %#v", start.Thread.Status)
	}
	if start.Thread.Path == nil || !strings.HasSuffix(*start.Thread.Path, ".jsonl") {
		t.Fatalf("start thread Path = %+v, want rollout jsonl path", start.Thread.Path)
	}
	startData, err := json.Marshal(startResponse.Result)
	if err != nil {
		t.Fatalf("Marshal(thread/start result) error = %v", err)
	}
	var startPayload map[string]any
	if err := json.Unmarshal(startData, &startPayload); err != nil {
		t.Fatalf("Unmarshal(thread/start result) error = %v", err)
	}
	if _, ok := startPayload["sessionId"]; ok {
		t.Fatalf("thread/start result has top-level sessionId: %v", startPayload["sessionId"])
	}
	startThreadPayload, ok := startPayload["thread"].(map[string]any)
	if !ok {
		t.Fatalf("thread/start payload thread = %T, want object", startPayload["thread"])
	}
	if value, ok := startThreadPayload["name"]; !ok || value != nil {
		t.Fatalf("thread/start payload thread.name = %v, present=%v; want explicit null", value, ok)
	}
	if value, ok := startThreadPayload["ephemeral"].(bool); !ok || value {
		t.Fatalf("thread/start payload thread.ephemeral = %v, present=%v; want false", startThreadPayload["ephemeral"], ok)
	}

	readRequest := requestWithParams(t, StringID("read"), MethodThreadRead, ThreadReadParams{ThreadID: start.Thread.ID})
	readResponse := router.Handle(readRequest)
	if readResponse.Error != nil {
		t.Fatalf("read error: %+v", readResponse.Error)
	}
	read := readResponse.Result.(*ThreadReadResponse)
	if len(read.Thread.Turns) != 0 {
		t.Fatalf("thread/start prompt unexpectedly created turns: %+v", read.Thread.Turns)
	}
	if read.Thread.Path == nil || !strings.HasSuffix(*read.Thread.Path, ".jsonl") {
		t.Fatalf("read thread Path = %+v, want rollout jsonl path", read.Thread.Path)
	}

	listRequest := requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{})
	listResponse := router.Handle(listRequest)
	if listResponse.Error != nil {
		t.Fatalf("list error: %+v", listResponse.Error)
	}
	list := listResponse.Result.(*ThreadListResponse)
	if len(list.Data) != 0 {
		t.Fatalf("thread/list exposed unmaterialized thread: %+v", list.Data)
	}

	itemsRequest := requestWithParams(t, IntID(3), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: start.Thread.ID,
	})
	itemsResponse := router.Handle(itemsRequest)
	if itemsResponse.Error == nil || !strings.Contains(itemsResponse.Error.Message, "unavailable before first user message") {
		t.Fatalf("items response = %+v, want unmaterialized-thread error", itemsResponse)
	}
	if _, err := os.Stat(*start.Thread.Path); !os.IsNotExist(err) {
		t.Fatalf("thread/start prompt unexpectedly materialized rollout: %v", err)
	}
}

func TestRouterThreadStartAllowsOmittedCWD(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		Prompt: "hello default cwd",
	}))
	if response.Error != nil {
		t.Fatalf("start error: %+v", response.Error)
	}
	start := response.Result.(*ThreadStartResponse)
	if strings.TrimSpace(start.CWD) == "" {
		t.Fatalf("start cwd is empty: %+v", start)
	}
	if start.Thread.CWD != start.CWD {
		t.Fatalf("thread cwd = %q, want response cwd %q", start.Thread.CWD, start.CWD)
	}
	if len(start.RuntimeWorkspaceRoots) != 1 || !sameAppPath(start.RuntimeWorkspaceRoots[0], start.CWD) {
		t.Fatalf("runtimeWorkspaceRoots = %#v, want cwd %q", start.RuntimeWorkspaceRoots, start.CWD)
	}

	record, err := store.Read(session.ThreadID(start.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read start record error = %v", err)
	}
	if record.Metadata.CWD != start.CWD {
		t.Fatalf("record cwd = %q, want %q", record.Metadata.CWD, start.CWD)
	}
	recordRoots := stringSliceFromAny(record.Metadata.Extra["runtime_workspace_roots"])
	if len(recordRoots) != 1 || !sameAppPath(recordRoots[0], start.CWD) {
		t.Fatalf("record runtime roots = %#v, want cwd %q", recordRoots, start.CWD)
	}
}

func TestRouterThreadStartAcceptsMetricsServiceName(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         "D:/repo",
		ServiceName: stringPtr("my_app_server_client"),
	}))
	if response.Error != nil {
		t.Fatalf("thread/start serviceName error: %+v", response.Error)
	}
	start := response.Result.(*ThreadStartResponse)
	if start.Thread == nil || start.Thread.ID == "" {
		t.Fatalf("thread/start serviceName result = %+v", start)
	}
}

func TestRouterThreadStartSessionStartSourceValidationAndPersistence(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)

	invalidSource := string(SessionStartSourceResume)
	invalid := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                t.TempDir(),
		SessionStartSource: &invalidSource,
	}))
	if invalid.Error == nil || invalid.Error.Code != -32600 || invalid.Error.Message != `sessionStartSource must be "startup" or "clear"` {
		t.Fatalf("invalid sessionStartSource response = %+v", invalid)
	}

	clearSource := string(SessionStartSourceClear)
	start := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD:                t.TempDir(),
		SessionStartSource: &clearSource,
	}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read start record error = %v", err)
	}
	if got := stringFromMap(record.Metadata.Extra, pendingSessionStartSourceExtraKey); got != string(SessionStartSourceClear) {
		t.Fatalf("pending session start source = %q, want clear; extra = %#v", got, record.Metadata.Extra)
	}
}

func TestRouterThreadListEmptySourceKindsDefaultsToInteractiveSources(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	create := func(id session.ThreadID, source string, createdAt time.Time) {
		t.Helper()
		if err := store.Create(&session.Record{
			ID:        id,
			SessionID: string(id),
			Preview:   string(id),
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
			RecencyAt: createdAt,
			Metadata: session.Metadata{
				CWD:           "D:/repo",
				ModelProvider: "openai",
				Source:        source,
				HistoryMode:   string(ThreadHistoryLegacy),
			},
		}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	create("thread-cli", string(SessionSourceCli), now)
	create("thread-exec", string(SessionSourceExec), now.Add(time.Minute))
	create("thread-app", string(SessionSourceAppServer), now.Add(2*time.Minute))

	limit := 10
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{
		Limit:       &limit,
		SourceKinds: []ThreadSourceKind{},
	}))
	if response.Error != nil {
		t.Fatalf("thread/list error: %+v", response.Error)
	}
	list := response.Result.(*ThreadListResponse)
	if len(list.Data) != 2 || list.Data[0].ID != "thread-app" || list.Data[1].ID != "thread-cli" {
		t.Fatalf("thread/list data = %+v", list.Data)
	}
	if list.Data[0].Source != SessionSourceAppServer || list.Data[1].Source != SessionSourceCli {
		t.Fatalf("thread/list sources = %+v", list.Data)
	}
}

func TestRouterThreadListSourceKindsPostFiltersSubAgents(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	create := func(id session.ThreadID, source string, agentPath string, createdAt time.Time) {
		t.Helper()
		if err := store.Create(&session.Record{
			ID:        id,
			SessionID: string(id),
			Preview:   string(id),
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
			RecencyAt: createdAt,
			Metadata: session.Metadata{
				CWD:           "D:/repo",
				ModelProvider: "openai",
				Source:        source,
				AgentPath:     agentPath,
				HistoryMode:   string(ThreadHistoryLegacy),
			},
		}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	create("thread-cli", string(SessionSourceCli), "", now)
	create("thread-review", string(ThreadSourceKindSubAgentReview), "", now.Add(time.Minute))
	create("thread-spawn", "subagent", "agents/researcher", now.Add(2*time.Minute))
	create("thread-generic", "subagent", "", now.Add(3*time.Minute))

	review := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{
		SourceKinds: []ThreadSourceKind{ThreadSourceKindSubAgentReview},
	}))
	if review.Error != nil {
		t.Fatalf("thread/list review error: %+v", review.Error)
	}
	if data := review.Result.(*ThreadListResponse).Data; len(data) != 1 || data[0].ID != "thread-review" {
		t.Fatalf("review sourceKinds data = %+v", data)
	}

	spawn := router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{
		SourceKinds: []ThreadSourceKind{ThreadSourceKindSubAgentThreadSpawn},
	}))
	if spawn.Error != nil {
		t.Fatalf("thread/list spawn error: %+v", spawn.Error)
	}
	if data := spawn.Result.(*ThreadListResponse).Data; len(data) != 1 || data[0].ID != "thread-spawn" {
		t.Fatalf("spawn sourceKinds data = %+v", data)
	}

	allSubagents := router.Handle(requestWithParams(t, IntID(3), MethodThreadList, ThreadListParams{
		SourceKinds: []ThreadSourceKind{ThreadSourceKindSubAgent},
	}))
	if allSubagents.Error != nil {
		t.Fatalf("thread/list subagent error: %+v", allSubagents.Error)
	}
	if data := allSubagents.Result.(*ThreadListResponse).Data; len(data) != 3 || data[0].ID != "thread-generic" || data[1].ID != "thread-spawn" || data[2].ID != "thread-review" {
		t.Fatalf("subagent sourceKinds data = %+v", data)
	}
}

func TestRouterThreadListUseStateDBOnlySkipsRolloutScan(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     store.Root(),
		ThreadID:      "thread-rollout-only",
		SessionID:     "thread-rollout-only",
		CWD:           "D:/repo",
		Model:         "gpt-test",
		ModelProvider: "openai",
		Source:        string(SessionSourceCli),
		Now:           fixedTime(),
	})
	if err != nil {
		t.Fatalf("create rollout error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("close rollout error = %v", err)
	}

	stateOnly := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{UseStateDBOnly: true}))
	if stateOnly.Error != nil {
		t.Fatalf("thread/list state-only error = %+v", stateOnly.Error)
	}
	if data := stateOnly.Result.(*ThreadListResponse).Data; len(data) != 0 {
		t.Fatalf("state-db-only data = %#v, want empty", data)
	}

	withRollouts := router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{}))
	if withRollouts.Error != nil {
		t.Fatalf("thread/list rollout scan error = %+v", withRollouts.Error)
	}
	data := withRollouts.Result.(*ThreadListResponse).Data
	if len(data) != 1 || data[0].ID != "thread-rollout-only" {
		t.Fatalf("rollout scan data = %#v", data)
	}
}

func TestRouterThreadListEnforcesMaxLimit(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	for i := 0; i < 105; i++ {
		id := session.ThreadID("thread-limit-" + strconv.Itoa(i))
		createdAt := now.Add(time.Duration(i) * time.Minute)
		if err := store.Create(&session.Record{
			ID:        id,
			SessionID: string(id),
			Preview:   string(id),
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
			RecencyAt: createdAt,
			Metadata: session.Metadata{
				CWD:           "D:/repo",
				ModelProvider: "openai",
				Source:        string(SessionSourceCli),
				HistoryMode:   string(ThreadHistoryLegacy),
			},
		}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}

	limit := 200
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{Limit: &limit}))
	if response.Error != nil {
		t.Fatalf("thread/list error: %+v", response.Error)
	}
	list := response.Result.(*ThreadListResponse)
	if len(list.Data) != maxThreadListPageSize {
		t.Fatalf("thread/list len = %d, want %d", len(list.Data), maxThreadListPageSize)
	}
	if list.NextCursor == nil || strings.TrimSpace(*list.NextCursor) == "" {
		t.Fatalf("thread/list nextCursor = %+v", list.NextCursor)
	}
	zeroLimit := 0
	zeroResponse := router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{Limit: &zeroLimit}))
	if zeroResponse.Error != nil {
		t.Fatalf("thread/list zero limit error: %+v", zeroResponse.Error)
	}
	zeroList := zeroResponse.Result.(*ThreadListResponse)
	if len(zeroList.Data) != 1 {
		t.Fatalf("thread/list zero limit len = %d, want 1", len(zeroList.Data))
	}
}

func TestRouterThreadListUsesTimeCursors(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	base := fixedTime()
	create := func(id string, updatedAt time.Time) {
		t.Helper()
		if err := store.Create(&session.Record{
			ID:        session.ThreadID(id),
			SessionID: id,
			Preview:   id,
			CreatedAt: updatedAt,
			UpdatedAt: updatedAt,
			RecencyAt: updatedAt,
			Metadata: session.Metadata{
				CWD:           "D:/repo",
				ModelProvider: "openai",
				Source:        string(SessionSourceCli),
				HistoryMode:   string(ThreadHistoryLegacy),
			},
		}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	create("thread-old", base)
	create("thread-watermark", base.Add(2*time.Hour))

	one := 1
	first := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{
		Limit:   &one,
		SortKey: SortUpdatedAt,
	}))
	if first.Error != nil {
		t.Fatalf("thread/list first error: %+v", first.Error)
	}
	firstPage := first.Result.(*ThreadListResponse)
	if len(firstPage.Data) != 1 || firstPage.Data[0].ID != "thread-watermark" {
		t.Fatalf("first page = %+v", firstPage)
	}
	wantNext := base.Add(2 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	wantBackwards := base.Add(2*time.Hour - time.Millisecond).UTC().Format("2006-01-02T15:04:05.000Z")
	if firstPage.NextCursor == nil || *firstPage.NextCursor != wantNext {
		t.Fatalf("nextCursor = %+v, want %q", firstPage.NextCursor, wantNext)
	}
	if firstPage.BackwardsCursor == nil || *firstPage.BackwardsCursor != wantBackwards {
		t.Fatalf("backwardsCursor = %+v, want %q", firstPage.BackwardsCursor, wantBackwards)
	}

	create("thread-newer", base.Add(3*time.Hour))
	asc := SortAsc
	ten := 10
	delta := router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{
		Cursor:        firstPage.BackwardsCursor,
		Limit:         &ten,
		SortKey:       SortUpdatedAt,
		SortDirection: asc,
	}))
	if delta.Error != nil {
		t.Fatalf("thread/list delta error: %+v", delta.Error)
	}
	deltaPage := delta.Result.(*ThreadListResponse)
	got := make([]string, 0, len(deltaPage.Data))
	for _, thread := range deltaPage.Data {
		got = append(got, thread.ID)
	}
	if strings.Join(got, ",") != "thread-watermark,thread-newer" {
		t.Fatalf("delta ids = %v", got)
	}
}

func TestRouterThreadListInvalidCursorReturnsInvalidRequest(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	cursor := "not-a-cursor"
	limit := 2
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{
		Cursor: &cursor,
		Limit:  &limit,
	}))
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "invalid cursor: not-a-cursor" {
		t.Fatalf("thread/list invalid cursor response = %+v", response)
	}

	blankCursor := " 1 "
	search := router.Handle(requestWithParams(t, IntID(2), MethodThreadSearch, ThreadSearchParams{
		Cursor:     &blankCursor,
		SearchTerm: "needle",
	}))
	if search.Error == nil || search.Error.Code != -32600 || search.Error.Message != "invalid cursor:  1 " {
		t.Fatalf("thread/search blank cursor response = %+v", search)
	}

	parent := "00000000-0000-0000-0000-000000000001"
	ancestor := "00000000-0000-0000-0000-000000000002"
	conflict := router.Handle(requestWithParams(t, IntID(3), MethodThreadList, ThreadListParams{
		ParentThreadID:   &parent,
		AncestorThreadID: &ancestor,
	}))
	if conflict.Error == nil || conflict.Error.Code != -32600 || conflict.Error.Message != "parentThreadId and ancestorThreadId are mutually exclusive" {
		t.Fatalf("thread/list relation conflict response = %+v", conflict)
	}

	badSortKey := SortKey("mystery")
	sortKeyResponse := router.Handle(requestWithParams(t, IntID(4), MethodThreadList, ThreadListParams{
		SortKey: badSortKey,
	}))
	if sortKeyResponse.Error == nil || sortKeyResponse.Error.Code != -32600 || sortKeyResponse.Error.Message != `unsupported sortKey "mystery"` {
		t.Fatalf("thread/list bad sortKey response = %+v", sortKeyResponse.Error)
	}

	badSortDirection := SortDirection("sideways")
	sortDirectionResponse := router.Handle(requestWithParams(t, IntID(5), MethodThreadSearch, ThreadSearchParams{
		SearchTerm:    "needle",
		SortDirection: badSortDirection,
	}))
	if sortDirectionResponse.Error == nil || sortDirectionResponse.Error.Code != -32600 || sortDirectionResponse.Error.Message != `unsupported sortDirection "sideways"` {
		t.Fatalf("thread/search bad sortDirection response = %+v", sortDirectionResponse.Error)
	}
}

func TestRouterThreadSearchRejectsEmptySearchTerm(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadSearch, ThreadSearchParams{SearchTerm: "  "}))
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "thread/search requires a non-empty searchTerm" {
		t.Fatalf("thread/search empty searchTerm response = %+v", response)
	}
}

func TestRouterThreadLoadedListPaginatesLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-z", fixedTime())
	createRecord(t, store, "thread-a", fixedTime().Add(time.Minute))

	zero := 0
	first := router.Handle(requestWithParams(t, IntID(1), MethodThreadLoadedList, ThreadLoadedListParams{Limit: &zero}))
	if first.Error != nil {
		t.Fatalf("thread/loaded/list first page error: %+v", first.Error)
	}
	firstPage := first.Result.(*ThreadLoadedListResponse)
	if len(firstPage.Data) != 1 || firstPage.Data[0] != "thread-a" || firstPage.NextCursor == nil || *firstPage.NextCursor != "thread-a" {
		t.Fatalf("first loaded page = %+v, want thread-a with next cursor", firstPage)
	}

	one := 1
	second := router.Handle(requestWithParams(t, IntID(2), MethodThreadLoadedList, ThreadLoadedListParams{Cursor: firstPage.NextCursor, Limit: &one}))
	if second.Error != nil {
		t.Fatalf("thread/loaded/list second page error: %+v", second.Error)
	}
	secondPage := second.Result.(*ThreadLoadedListResponse)
	if len(secondPage.Data) != 1 || secondPage.Data[0] != "thread-z" || secondPage.NextCursor != nil {
		t.Fatalf("second loaded page = %+v, want final thread-z page", secondPage)
	}

	missingCursor := "thread-b"
	insert := router.Handle(requestWithParams(t, IntID(3), MethodThreadLoadedList, ThreadLoadedListParams{Cursor: &missingCursor, Limit: &one}))
	if insert.Error != nil {
		t.Fatalf("thread/loaded/list insertion cursor error: %+v", insert.Error)
	}
	insertPage := insert.Result.(*ThreadLoadedListResponse)
	if len(insertPage.Data) != 1 || insertPage.Data[0] != "thread-z" {
		t.Fatalf("insertion cursor page = %+v, want thread-z", insertPage)
	}

	badCursor := "not-a-cursor"
	bad := router.Handle(requestWithParams(t, IntID(4), MethodThreadLoadedList, ThreadLoadedListParams{Cursor: &badCursor}))
	if bad.Error == nil || bad.Error.Code != -32600 || bad.Error.Message != "invalid cursor: not-a-cursor" {
		t.Fatalf("thread/loaded/list invalid cursor response = %+v", bad)
	}

	malformed := router.Handle(&Request{ID: IntID(5), Method: MethodThreadLoadedList, Params: []byte(`{"limit":"bad"}`)})
	if malformed.Error == nil || malformed.Error.Code != -32600 || !strings.HasPrefix(malformed.Error.Message, "Invalid request: json: cannot unmarshal string") {
		t.Fatalf("thread/loaded/list malformed params response = %+v", malformed)
	}
}

func TestRouterThreadItemTurnListMissingThreadUsesRustErrors(t *testing.T) {
	router := NewRouter(session.NewStore(t.TempDir()))

	read := router.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{ThreadID: "thread-missing"}))
	if read.Error == nil || read.Error.Code != -32600 || read.Error.Message != "thread not loaded: thread-missing" {
		t.Fatalf("read missing thread response = %+v", read)
	}

	items := router.Handle(requestWithParams(t, IntID(2), MethodThreadItemsList, ThreadItemsListParams{ThreadID: "thread-missing"}))
	if items.Error == nil || items.Error.Code != -32600 || items.Error.Message != "no rollout found for thread id thread-missing" {
		t.Fatalf("items missing thread response = %+v", items)
	}

	turns := router.Handle(requestWithParams(t, IntID(3), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: "thread-missing"}))
	if turns.Error == nil || turns.Error.Code != -32600 || turns.Error.Message != "thread not loaded: thread-missing" {
		t.Fatalf("turns missing thread response = %+v", turns)
	}
}

func TestRouterApproveGuardianDeniedActionRequiresEvent(t *testing.T) {
	router := NewRouter(session.NewStore(t.TempDir()))

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadApproveGuardianDeniedAction, ThreadApproveGuardianDeniedActionParams{
		ThreadID: "thread-a",
	}))
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "event is required" {
		t.Fatalf("approve guardian missing event response = %+v", response)
	}
}

func TestRouterThreadStartPersistsPaginatedHistoryMode(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         "D:/repo",
		HistoryMode: ThreadHistoryPaginated,
	}))
	if response.Error != nil {
		t.Fatalf("paginated start error = %+v", response.Error)
	}
	thread := response.Result.(*ThreadStartResponse).Thread
	if thread.HistoryMode != ThreadHistoryPaginated {
		t.Fatalf("history mode = %q", thread.HistoryMode)
	}
	record, err := store.Load(session.ThreadID(thread.ID))
	if err != nil || record.Metadata.HistoryMode != string(ThreadHistoryPaginated) {
		t.Fatalf("stored history mode = %q, error = %v", record.Metadata.HistoryMode, err)
	}

	unknown := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD:         "D:/repo",
		HistoryMode: ThreadHistoryMode("future"),
	}))
	if unknown.Error == nil || unknown.Error.Code != -32600 || unknown.Error.Message != `unsupported historyMode "future"` {
		t.Fatalf("unknown historyMode response = %+v", unknown)
	}
}

func TestRouterStartWithoutPromptReturnsUnmaterializedPath(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: "D:/repo"}))
	if start.Error != nil {
		t.Fatalf("start error: %+v", start.Error)
	}
	thread := start.Result.(*ThreadStartResponse).Thread
	if thread.Path == nil || !strings.HasSuffix(*thread.Path, ".jsonl") {
		t.Fatalf("start thread path = %+v", thread.Path)
	}
	if thread.Status.Type != "idle" {
		t.Fatalf("start thread status = %#v", thread.Status)
	}
	if _, err := os.Stat(*thread.Path); !os.IsNotExist(err) {
		t.Fatalf("start path should be unmaterialized, stat err = %v", err)
	}

	read := router.Handle(requestWithParams(t, IntID(2), MethodThreadRead, ThreadReadParams{ThreadID: thread.ID}))
	if read.Error != nil {
		t.Fatalf("read error: %+v", read.Error)
	}
	readThread := read.Result.(*ThreadReadResponse).Thread
	if readThread.Path == nil || *readThread.Path != *thread.Path || len(readThread.Turns) != 0 {
		t.Fatalf("read thread = %+v, want path %q and no turns", readThread, *thread.Path)
	}

	readTurns := router.Handle(requestWithParams(t, IntID(3), MethodThreadRead, ThreadReadParams{ThreadID: thread.ID, IncludeTurns: true}))
	if readTurns.Error == nil || readTurns.Error.Code != -32600 || !strings.Contains(readTurns.Error.Message, "includeTurns is unavailable before first user message") {
		t.Fatalf("read includeTurns error = %+v", readTurns.Error)
	}
	negativeLimit := -1
	badTurnsList := router.Handle(requestWithParams(t, IntID(31), MethodThreadTurnsList, ThreadTurnsListParams{
		ThreadID: thread.ID,
		Limit:    &negativeLimit,
	}))
	if badTurnsList.Error == nil || badTurnsList.Error.Code != -32600 || badTurnsList.Error.Message != "limit must be non-negative" {
		t.Fatalf("bad turns list error = %+v", badTurnsList.Error)
	}
	badItemsList := router.Handle(requestWithParams(t, IntID(32), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: thread.ID,
		Limit:    &negativeLimit,
	}))
	if badItemsList.Error == nil || badItemsList.Error.Code != -32600 || badItemsList.Error.Message != "limit must be non-negative" {
		t.Fatalf("bad items list error = %+v", badItemsList.Error)
	}
	badSortDirection := SortDirection("sideways")
	badItemsSort := router.Handle(requestWithParams(t, IntID(33), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID:      thread.ID,
		SortDirection: badSortDirection,
	}))
	if badItemsSort.Error == nil || badItemsSort.Error.Code != -32600 || badItemsSort.Error.Message != `unsupported sortDirection "sideways"` {
		t.Fatalf("bad items sortDirection error = %+v", badItemsSort.Error)
	}
	badItemsView := TurnItemsView("tiny")
	badTurnsItemsView := router.Handle(requestWithParams(t, IntID(34), MethodThreadTurnsList, ThreadTurnsListParams{
		ThreadID:  thread.ID,
		ItemsView: badItemsView,
	}))
	if badTurnsItemsView.Error == nil || badTurnsItemsView.Error.Code != -32600 || badTurnsItemsView.Error.Message != `unsupported itemsView "tiny"` {
		t.Fatalf("bad turns itemsView error = %+v", badTurnsItemsView.Error)
	}
	badResumeInitialTurnsPage := router.Handle(requestWithParams(t, IntID(35), MethodThreadResume, ThreadResumeParams{
		ThreadID: thread.ID,
		InitialTurnsPage: &ThreadInitialPageParams{
			SortDirection: badSortDirection,
		},
	}))
	if badResumeInitialTurnsPage.Error == nil || badResumeInitialTurnsPage.Error.Code != -32600 || badResumeInitialTurnsPage.Error.Message != `unsupported sortDirection "sideways"` {
		t.Fatalf("bad resume initialTurnsPage error = %+v", badResumeInitialTurnsPage.Error)
	}
	turnsList := router.Handle(requestWithParams(t, IntID(4), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: thread.ID}))
	if turnsList.Error == nil || turnsList.Error.Code != -32600 || !strings.Contains(turnsList.Error.Message, "thread/turns/list is unavailable before first user message") {
		t.Fatalf("turns list error = %+v", turnsList.Error)
	}
	itemsList := router.Handle(requestWithParams(t, IntID(40), MethodThreadItemsList, ThreadItemsListParams{ThreadID: thread.ID}))
	if itemsList.Error == nil || itemsList.Error.Code != -32600 || !strings.Contains(itemsList.Error.Message, "thread/items/list is unavailable before first user message") {
		t.Fatalf("items list error = %+v", itemsList.Error)
	}
	resume := router.Handle(requestWithParams(t, IntID(41), MethodThreadResume, ThreadResumeParams{ThreadID: thread.ID}))
	if resume.Error == nil || resume.Error.Code != -32600 || !strings.Contains(resume.Error.Message, "no rollout found for thread id") {
		t.Fatalf("resume error = %+v", resume.Error)
	}
	resumeMetadataOnly := router.Handle(requestWithParams(t, IntID(42), MethodThreadResume, ThreadResumeParams{ThreadID: thread.ID, ExcludeTurns: true}))
	if resumeMetadataOnly.Error == nil || resumeMetadataOnly.Error.Code != -32600 || !strings.Contains(resumeMetadataOnly.Error.Message, "no rollout found for thread id") {
		t.Fatalf("resume excludeTurns error = %+v", resumeMetadataOnly.Error)
	}
	fork := router.Handle(requestWithParams(t, IntID(5), MethodThreadFork, ThreadForkParams{ThreadID: thread.ID}))
	if fork.Error == nil || fork.Error.Code != -32600 || !strings.Contains(fork.Error.Message, "no rollout found for thread id") {
		t.Fatalf("fork error = %+v", fork.Error)
	}
}

func TestRouterThreadSetNameKeepsEmptyThreadUnmaterialized(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: "D:/repo",
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	named := router.Handle(requestWithParams(t, IntID(2), MethodThreadNameSet, ThreadSetNameParams{
		ThreadID: threadID,
		Name:     "SDK lifecycle example",
	}))
	if named.Error != nil {
		t.Fatalf("thread/name/set error: %+v", named.Error)
	}
	if _, err := rollout.FindThreadPath(store.Root(), threadID, false); err == nil {
		t.Fatal("thread/name/set unexpectedly materialized a rollout")
	}
	read := router.Handle(requestWithParams(t, IntID(3), MethodThreadRead, ThreadReadParams{ThreadID: threadID}))
	if read.Error != nil {
		t.Fatalf("thread/read error: %+v", read.Error)
	}
	readThread := read.Result.(*ThreadReadResponse).Thread
	if readThread.Name == nil || *readThread.Name != "SDK lifecycle example" {
		t.Fatalf("thread/read name = %+v", readThread.Name)
	}
	list := router.Handle(requestWithParams(t, IntID(4), MethodThreadList, ThreadListParams{}))
	if list.Error != nil {
		t.Fatalf("thread/list error: %+v", list.Error)
	}
	for _, listed := range list.Result.(*ThreadListResponse).Data {
		if listed.ID == threadID {
			t.Fatalf("unmaterialized named thread appeared in thread/list: %+v", listed)
		}
	}

	fork := router.Handle(requestWithParams(t, IntID(5), MethodThreadFork, ThreadForkParams{ThreadID: threadID}))
	if fork.Error == nil || fork.Error.Code != -32600 || !strings.Contains(fork.Error.Message, "no rollout found for thread id") {
		t.Fatalf("thread/fork response = %+v", fork)
	}
}

func TestRouterThreadStartRetainsDynamicToolsWhenRolloutMaterializes(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    "D:/repo",
		Prompt: "materialize rollout",
		DynamicTools: []json.RawMessage{
			json.RawMessage(`{"type":"namespace","name":"codex_app","description":"Demo namespace tools","tools":[{"type":"function","name":"demo_tool","description":"Demo dynamic tool","inputSchema":{"type":"object"}}]}`),
		},
		SelectedCapabilityRoots: []SelectedCapabilityRoot{{
			ID: "cap-root",
			Location: CapabilityRootLocation{
				Type:          CapabilityRootLocationEnvironment,
				EnvironmentID: "env-1",
				Path:          "/skills",
			},
		}},
	}))
	if start.Error != nil {
		t.Fatalf("start error: %+v", start.Error)
	}
	startedThread := start.Result.(*ThreadStartResponse).Thread
	record, err := store.Read(session.ThreadID(startedThread.ID), true, false)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	tools, ok := record.Metadata.Extra["dynamic_tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("dynamic_tools = %#v", record.Metadata.Extra)
	}
	if len(record.Metadata.DynamicTools) != 1 || len(record.Metadata.SelectedCapabilityRoots) != 1 {
		t.Fatalf("metadata dynamic/capability roots = %#v", record.Metadata)
	}
	if _, err := os.Stat(*startedThread.Path); !os.IsNotExist(err) {
		t.Fatalf("thread/start unexpectedly materialized rollout: %v", err)
	}
	materializeThreadRolloutForTest(t, router, store, startedThread.ID)
	rolloutPath, err := rollout.FindThreadPath(store.Root(), startedThread.ID, false)
	if err != nil {
		t.Fatalf("rollout path error: %v", err)
	}
	fromRollout, err := rollout.RecordFromPath(rolloutPath, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(fromRollout.Metadata.DynamicTools) != 1 || len(fromRollout.Metadata.SelectedCapabilityRoots) != 1 {
		t.Fatalf("rollout metadata dynamic/capability roots = %#v", fromRollout.Metadata)
	}
}

func TestRouterThreadStartRejectsInvalidDynamicTools(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		DynamicTools: []json.RawMessage{
			json.RawMessage(`{"type":"namespace","name":"empty_namespace","description":"Contains no tools","tools":[]}`),
		},
	}))
	if response.Error == nil || response.Error.Code != -32600 || !strings.Contains(response.Error.Message, "must contain at least one tool") {
		t.Fatalf("start invalid dynamic tools error = %+v", response.Error)
	}
	if _, err := store.Read("thread-1", true, true); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("thread should not be created, Read error = %v", err)
	}

	hidden := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		DynamicTools: []json.RawMessage{
			json.RawMessage(`{"type":"function","name":"hidden_tool","description":"Hidden","deferLoading":true,"inputSchema":{"type":"object"}}`),
		},
	}))
	if hidden.Error == nil || hidden.Error.Code != -32600 || !strings.Contains(hidden.Error.Message, "hidden_tool") || !strings.Contains(hidden.Error.Message, "namespace") {
		t.Fatalf("hidden dynamic tool error = %+v", hidden.Error)
	}

	mixed := router.Handle(requestWithParams(t, IntID(3), MethodThreadStart, ThreadStartParams{
		DynamicTools: []json.RawMessage{
			json.RawMessage(`{"type":"function","name":"canonical_tool","description":"Canonical","inputSchema":{"type":"object"}}`),
			json.RawMessage(`{"namespace":"legacy_app","name":"legacy_tool","description":"Legacy","inputSchema":{"type":"object"}}`),
		},
	}))
	if mixed.Error == nil || mixed.Error.Code != -32600 || !strings.Contains(mixed.Error.Message, "either canonical or legacy format") {
		t.Fatalf("mixed dynamic tools error = %+v", mixed.Error)
	}
}

func TestRouterThreadStartNormalizesLegacyDynamicTools(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    "D:/repo",
		Prompt: "legacy dynamic tools",
		DynamicTools: []json.RawMessage{
			json.RawMessage(`{"namespace":"legacy_app","name":"legacy_tool","description":"Legacy tool","inputSchema":{"type":"object"},"exposeToContext":false}`),
		},
	}))
	if start.Error != nil {
		t.Fatalf("start error: %+v", start.Error)
	}
	startedThread := start.Result.(*ThreadStartResponse).Thread
	record, err := store.Read(session.ThreadID(startedThread.ID), true, false)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(record.Metadata.DynamicTools) != 1 {
		t.Fatalf("metadata dynamic tools = %#v", record.Metadata.DynamicTools)
	}
	var tool map[string]any
	if err := json.Unmarshal(record.Metadata.DynamicTools[0], &tool); err != nil {
		t.Fatalf("Unmarshal dynamic tool error = %v", err)
	}
	if tool["type"] != "namespace" || tool["name"] != "legacy_app" {
		t.Fatalf("normalized dynamic tool = %#v", tool)
	}
	tools := tool["tools"].([]any)
	nested := tools[0].(map[string]any)
	if nested["type"] != "function" || nested["name"] != "legacy_tool" || nested["deferLoading"] != true {
		t.Fatalf("normalized namespace tool = %#v", nested)
	}
}

func TestRouterArchiveUnarchiveAndDelete(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())
	record, err := store.Read("thread-a", true, true)
	if err != nil {
		t.Fatalf("read created: %v", err)
	}
	if err := router.createThreadRollout(record, fixedTime()); err != nil {
		t.Fatalf("create rollout: %v", err)
	}

	archive := router.Handle(requestWithParams(t, IntID(1), MethodThreadArchive, ThreadArchiveParams{ThreadID: "thread-a"}))
	if archive.Error != nil {
		t.Fatalf("archive error: %+v", archive.Error)
	}
	assertJSONPayload(t, archive.Result, "thread/archive result", map[string]any{})
	record, err = store.Read("thread-a", true, false)
	if err != nil {
		t.Fatalf("read archived: %v", err)
	}
	if !record.Archived {
		t.Fatal("record not archived")
	}
	if _, err := rollout.FindThreadPath(store.Root(), "thread-a", true); err != nil {
		t.Fatalf("archived rollout path error: %v", err)
	}

	unarchive := router.Handle(requestWithParams(t, IntID(2), MethodThreadUnarchive, ThreadUnarchiveParams{ThreadID: "thread-a"}))
	if unarchive.Error != nil {
		t.Fatalf("unarchive error: %+v", unarchive.Error)
	}
	record, err = store.Read("thread-a", true, false)
	if err != nil {
		t.Fatalf("read unarchived: %v", err)
	}
	if record.Archived {
		t.Fatal("record still archived")
	}
	if _, err := rollout.FindThreadPath(store.Root(), "thread-a", false); err != nil {
		t.Fatalf("active rollout path error: %v", err)
	}

	deleted := router.Handle(requestWithParams(t, IntID(3), MethodThreadDelete, ThreadDeleteParams{ThreadID: "thread-a"}))
	if deleted.Error != nil {
		t.Fatalf("delete error: %+v", deleted.Error)
	}
	assertJSONPayload(t, deleted.Result, "thread/delete result", map[string]any{})
	missing := router.Handle(requestWithParams(t, IntID(4), MethodThreadRead, ThreadReadParams{ThreadID: "thread-a"}))
	if missing.Error == nil || missing.Error.Code != -32600 || missing.Error.Message != "thread not loaded: thread-a" {
		t.Fatalf("expected thread not loaded after delete, got %+v", missing.Error)
	}
	missingDelete := router.Handle(requestWithParams(t, IntID(5), MethodThreadDelete, ThreadDeleteParams{ThreadID: "thread-a"}))
	if missingDelete.Error == nil || missingDelete.Error.Code != -32600 || missingDelete.Error.Message != "thread not found: thread-a" {
		t.Fatalf("expected thread not found on repeat delete, got %+v", missingDelete.Error)
	}
	if _, err := rollout.FindThreadPath(store.Root(), "thread-a", false); err == nil {
		t.Fatal("active rollout still exists after delete")
	}
	if _, err := rollout.FindThreadPath(store.Root(), "thread-a", true); err == nil {
		t.Fatal("archived rollout still exists after delete")
	}
}

func TestRouterArchiveUnarchiveAndDeleteRolloutOnlyThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: store.Root(),
		ThreadID:  "thread-rollout",
		Source:    "cli",
		CWD:       "/repo",
		Now:       fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "message", Role: "user", Text: "from rollout"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	archive := router.Handle(requestWithParams(t, IntID(1), MethodThreadArchive, ThreadArchiveParams{ThreadID: "thread-rollout"}))
	if archive.Error != nil {
		t.Fatalf("archive error: %+v", archive.Error)
	}
	archivedPath, err := rollout.FindThreadPath(store.Root(), "thread-rollout", true)
	if err != nil {
		t.Fatalf("archived rollout path error: %v", err)
	}
	old := time.Unix(1, 0).UTC()
	if err := os.Chtimes(archivedPath, old, old); err != nil {
		t.Fatalf("Chtimes(archived rollout) error = %v", err)
	}

	unarchive := router.Handle(requestWithParams(t, IntID(2), MethodThreadUnarchive, ThreadUnarchiveParams{ThreadID: "thread-rollout"}))
	if unarchive.Error != nil {
		t.Fatalf("unarchive error: %+v", unarchive.Error)
	}
	unarchived := unarchive.Result.(*ThreadUnarchiveResponse).Thread
	if unarchived.UpdatedAt <= old.Unix() {
		t.Fatalf("unarchived.UpdatedAt = %d, want after %d", unarchived.UpdatedAt, old.Unix())
	}
	if unarchived.Status.Type != NotLoadedStatus().Type {
		t.Fatalf("unarchived.Status = %+v, want notLoaded", unarchived.Status)
	}
	data, err := json.Marshal(unarchive.Result)
	if err != nil {
		t.Fatalf("Marshal(unarchive.Result) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal(unarchive.Result) error = %v", err)
	}
	threadPayload, ok := payload["thread"].(map[string]any)
	if !ok {
		t.Fatalf("unarchive payload thread = %T, want object", payload["thread"])
	}
	if value, ok := threadPayload["name"]; !ok || value != nil {
		t.Fatalf("unarchive payload thread.name = %v, present=%v; want explicit null", value, ok)
	}
	if _, err := rollout.FindThreadPath(store.Root(), "thread-rollout", false); err != nil {
		t.Fatalf("active rollout path error: %v", err)
	}

	deleted := router.Handle(requestWithParams(t, IntID(3), MethodThreadDelete, ThreadDeleteParams{ThreadID: "thread-rollout"}))
	if deleted.Error != nil {
		t.Fatalf("delete error: %+v", deleted.Error)
	}
	if _, err := rollout.FindThreadPath(store.Root(), "thread-rollout", false); err == nil {
		t.Fatal("rollout still exists after delete")
	}
}

func TestRouterThreadUnarchivePreservesPathlessStoreMetadata(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	if err := store.Create(&session.Record{
		ID:             "pathless-child",
		SessionID:      "pathless-child",
		ForkedFromID:   "pathless-parent",
		Title:          "named pathless thread",
		Preview:        "",
		Archived:       true,
		CreatedAt:      now,
		UpdatedAt:      now,
		RecencyAt:      now,
		ParentThreadID: "",
		Metadata: session.Metadata{
			CWD:           "",
			ModelProvider: "test-provider",
			Source:        "cli",
			MemoryMode:    "disabled",
		},
	}); err != nil {
		t.Fatalf("Create(pathless archived record) error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadUnarchive, ThreadUnarchiveParams{
		ThreadID: "pathless-child",
	}))
	if response.Error != nil {
		t.Fatalf("thread/unarchive pathless error: %+v", response.Error)
	}
	thread := response.Result.(*ThreadUnarchiveResponse).Thread
	if thread.ID != "pathless-child" {
		t.Fatalf("thread id = %q, want pathless-child", thread.ID)
	}
	if thread.Path != nil {
		t.Fatalf("thread path = %+v, want nil for pathless store thread", thread.Path)
	}
	if thread.ForkedFromID == nil || *thread.ForkedFromID != "pathless-parent" {
		t.Fatalf("thread forkedFromId = %+v, want pathless-parent", thread.ForkedFromID)
	}
	if thread.Name == nil || *thread.Name != "named pathless thread" {
		t.Fatalf("thread name = %+v, want named pathless thread", thread.Name)
	}
	if thread.Preview != "" {
		t.Fatalf("thread preview = %q, want empty", thread.Preview)
	}
	data, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("Marshal(thread/unarchive pathless result) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal(thread/unarchive pathless result) error = %v", err)
	}
	threadPayload, ok := payload["thread"].(map[string]any)
	if !ok {
		t.Fatalf("thread/unarchive pathless payload thread = %T, want object", payload["thread"])
	}
	if value, ok := threadPayload["path"]; !ok || value != nil {
		t.Fatalf("thread/unarchive pathless thread.path = %v, present=%v; want explicit null", value, ok)
	}
	if value, ok := threadPayload["name"].(string); !ok || value != "named pathless thread" {
		t.Fatalf("thread/unarchive pathless thread.name = %v, present=%v; want named pathless thread", threadPayload["name"], ok)
	}
	if value, ok := threadPayload["forkedFromId"].(string); !ok || value != "pathless-parent" {
		t.Fatalf("thread/unarchive pathless thread.forkedFromId = %v, present=%v; want pathless-parent", threadPayload["forkedFromId"], ok)
	}
}

func TestRouterThreadElicitationAcceptsRustAndLegacyWireNames(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())

	increment := router.Handle(requestWithParams(t, IntID(1), MethodThreadIncrementElicitation, ThreadIncrementElicitationParams{ThreadID: "thread-a"}))
	if increment.Error != nil {
		t.Fatalf("increment error: %+v", increment.Error)
	}
	if got := increment.Result.(*ThreadIncrementElicitationResponse); got.Count != 1 || !got.Paused {
		t.Fatalf("increment result = %+v", got)
	}

	legacyIncrement := router.Handle(requestWithParams(t, IntID(2), MethodThreadIncrementElicitationLegacy, ThreadIncrementElicitationParams{ThreadID: "thread-a"}))
	if legacyIncrement.Error != nil {
		t.Fatalf("legacy increment error: %+v", legacyIncrement.Error)
	}
	if got := legacyIncrement.Result.(*ThreadIncrementElicitationResponse); got.Count != 2 || !got.Paused {
		t.Fatalf("legacy increment result = %+v", got)
	}

	decrement := router.Handle(requestWithParams(t, IntID(3), MethodThreadDecrementElicitation, ThreadDecrementElicitationParams{ThreadID: "thread-a"}))
	if decrement.Error != nil {
		t.Fatalf("decrement error: %+v", decrement.Error)
	}
	if got := decrement.Result.(*ThreadDecrementElicitationResponse); got.Count != 1 || !got.Paused {
		t.Fatalf("decrement result = %+v", got)
	}

	legacyDecrement := router.Handle(requestWithParams(t, IntID(4), MethodThreadDecrementElicitationLegacy, ThreadDecrementElicitationParams{ThreadID: "thread-a"}))
	if legacyDecrement.Error != nil {
		t.Fatalf("legacy decrement error: %+v", legacyDecrement.Error)
	}
	if got := legacyDecrement.Result.(*ThreadDecrementElicitationResponse); got.Count != 0 || got.Paused {
		t.Fatalf("legacy decrement result = %+v", got)
	}
}

func TestRouterThreadCompactStartPersistsSummary(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	if err := store.Save(&session.Record{
		ID:        "thread-a",
		SessionID: "thread-a",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first request", CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "first answer", CreatedAt: now},
			{ID: "u2", Type: "message", Role: "user", Text: "second request", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := router.createThreadRollout(&session.Record{ID: "thread-a", SessionID: "thread-a"}, now); err != nil {
		t.Fatalf("create rollout error: %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: "thread-a"}))
	if response.Error != nil {
		t.Fatalf("compact error: %+v", response.Error)
	}
	record, err := store.Read("thread-a", true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(record.Items) == 0 || !strings.Contains(record.Items[len(record.Items)-1].Text, compact.SummaryPrefix) {
		t.Fatalf("compacted items = %#v", record.Items)
	}
	if record.Metadata.Extra["compaction_summary"] == "" {
		t.Fatalf("compact metadata = %#v", record.Metadata.Extra)
	}
	path, err := rollout.FindThreadPath(store.Root(), "thread-a", false)
	if err != nil {
		t.Fatalf("rollout path error: %v", err)
	}
	fromRollout, err := rollout.RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(fromRollout.Items) != len(record.Items) {
		t.Fatalf("rollout items len = %d, want %d", len(fromRollout.Items), len(record.Items))
	}
}

func TestRouterThreadLifecycleRepairRolloutOnlyThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime().Add(5 * time.Second) })
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: store.Root(),
		SessionID: "session-rollout",
		ThreadID:  "thread-rollout",
		Source:    "cli",
		CWD:       "/repo",
		Now:       fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	for _, item := range []rollout.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first request", Metadata: map[string]any{"turnId": "turn-1"}},
		{ID: "a1", Type: "message", Role: "assistant", Text: "first answer", Metadata: map[string]any{"turnId": "turn-1"}},
	} {
		if err := recorder.AppendItem(item); err != nil {
			t.Fatalf("AppendItem() error = %v", err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	increment := router.Handle(requestWithParams(t, IntID(1), MethodThreadIncrementElicitation, ThreadIncrementElicitationParams{ThreadID: "thread-rollout"}))
	if increment.Error != nil {
		t.Fatalf("increment error: %+v", increment.Error)
	}
	if got := increment.Result.(*ThreadIncrementElicitationResponse); got.Count != 1 || !got.Paused {
		t.Fatalf("increment result = %+v", got)
	}

	decrement := router.Handle(requestWithParams(t, IntID(2), MethodThreadDecrementElicitation, ThreadDecrementElicitationParams{ThreadID: "thread-rollout"}))
	if decrement.Error != nil {
		t.Fatalf("decrement error: %+v", decrement.Error)
	}

	mode := router.Handle(requestWithParams(t, IntID(3), MethodThreadMemoryModeSet, ThreadMemoryModeSetParams{
		ThreadID: "thread-rollout",
		Mode:     ThreadMemoryModeDisabled,
	}))
	if mode.Error != nil {
		t.Fatalf("memory mode error: %+v", mode.Error)
	}

	compactResponse := router.Handle(requestWithParams(t, IntID(4), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: "thread-rollout"}))
	if compactResponse.Error != nil {
		t.Fatalf("compact error: %+v", compactResponse.Error)
	}
	record, err := store.Read("thread-rollout", true, true)
	if err != nil {
		t.Fatalf("Read(repaired) error = %v", err)
	}
	if record.Metadata.ElicitationCount != 0 || record.Metadata.MemoryMode != string(ThreadMemoryModeDisabled) {
		t.Fatalf("metadata = %+v", record.Metadata)
	}
	if len(record.Items) == 0 || !strings.Contains(record.Items[len(record.Items)-1].Text, compact.SummaryPrefix) {
		t.Fatalf("compacted items = %+v", record.Items)
	}
}

func TestRouterMemoryResetClearsMemoriesAndPreservesThreads(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	createRecord(t, store, "thread-memory-reset", fixedTime())
	memoryRoot := filepath.Join(root, "memories")
	if err := os.MkdirAll(filepath.Join(memoryRoot, "rollout_summaries"), 0o755); err != nil {
		t.Fatalf("MkdirAll memories error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "MEMORY.md"), []byte("stale memory\n"), 0o600); err != nil {
		t.Fatalf("WriteFile MEMORY.md error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "rollout_summaries", "stale.md"), []byte("stale summary\n"), 0o600); err != nil {
		t.Fatalf("WriteFile stale summary error = %v", err)
	}

	response := NewRouter(store).Handle(&Request{JSONRPC: "2.0", ID: IntID(1), Method: MethodMemoryReset})
	if response.Error != nil {
		t.Fatalf("memory/reset error = %+v", response.Error)
	}
	entries, err := os.ReadDir(memoryRoot)
	if err != nil {
		t.Fatalf("ReadDir memories error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("memory root entries after reset = %+v", entries)
	}
	if _, err := store.Read("thread-memory-reset", true, true); err != nil {
		t.Fatalf("thread was not preserved: %v", err)
	}
}

func TestRouterMemoryResetClearsRustMemoriesSQLiteRowsLikeRust(t *testing.T) {
	root := t.TempDir()
	sqliteHome := t.TempDir()
	t.Setenv("CODEX_SQLITE_HOME", sqliteHome)
	store := session.NewStore(root)
	threadID := session.ThreadID("thread-memory-reset-sqlite")
	createRecord(t, store, threadID, fixedTime())
	memoryRoot := filepath.Join(root, "memories")
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll memories error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "MEMORY.md"), []byte("stale memory\n"), 0o600); err != nil {
		t.Fatalf("WriteFile MEMORY.md error = %v", err)
	}

	memoriesDB := openRouterTestSQLite(t, filepath.Join(sqliteHome, rustMemoriesSQLiteFilename))
	routerTestExecSQL(t, memoriesDB, `CREATE TABLE stage1_outputs (thread_id TEXT PRIMARY KEY, source_updated_at INTEGER NOT NULL, raw_memory TEXT NOT NULL, rollout_summary TEXT NOT NULL, generated_at INTEGER NOT NULL)`)
	routerTestExecSQL(t, memoriesDB, `CREATE TABLE jobs (kind TEXT NOT NULL, job_key TEXT NOT NULL, status TEXT NOT NULL, PRIMARY KEY (kind, job_key))`)
	routerTestExecSQL(t, memoriesDB, `INSERT INTO stage1_outputs (thread_id, source_updated_at, raw_memory, rollout_summary, generated_at) VALUES (?, 1, 'raw', 'summary', 2)`, string(threadID))
	routerTestExecSQL(t, memoriesDB, `INSERT INTO jobs (kind, job_key, status) VALUES ('memory_stage1', ?, 'queued')`, string(threadID))
	routerTestExecSQL(t, memoriesDB, `INSERT INTO jobs (kind, job_key, status) VALUES ('memory_consolidate_global', 'global', 'queued')`)
	routerTestExecSQL(t, memoriesDB, `INSERT INTO jobs (kind, job_key, status) VALUES ('other_pipeline', 'keep', 'queued')`)

	stateDB := openRouterTestSQLite(t, filepath.Join(sqliteHome, rustStateSQLiteFilename))
	routerTestExecSQL(t, stateDB, `CREATE TABLE threads (id TEXT PRIMARY KEY, memory_mode TEXT NOT NULL DEFAULT 'enabled')`)
	routerTestExecSQL(t, stateDB, `INSERT INTO threads (id, memory_mode) VALUES (?, 'enabled')`, string(threadID))

	response := NewRouter(store).Handle(&Request{JSONRPC: "2.0", ID: IntID(1), Method: MethodMemoryReset})
	if response.Error != nil {
		t.Fatalf("memory/reset error = %+v", response.Error)
	}
	if got := routerTestScalarInt(t, memoriesDB, `SELECT COUNT(*) FROM stage1_outputs`); got != 0 {
		t.Fatalf("stage1_outputs count = %d, want 0", got)
	}
	if got := routerTestScalarInt(t, memoriesDB, `SELECT COUNT(*) FROM jobs WHERE kind IN ('memory_stage1', 'memory_consolidate_global')`); got != 0 {
		t.Fatalf("memory jobs count = %d, want 0", got)
	}
	if got := routerTestScalarInt(t, memoriesDB, `SELECT COUNT(*) FROM jobs WHERE kind = 'other_pipeline'`); got != 1 {
		t.Fatalf("other jobs count = %d, want 1", got)
	}
	if got := routerTestScalarString(t, stateDB, `SELECT memory_mode FROM threads WHERE id = ?`, string(threadID)); got != "enabled" {
		t.Fatalf("thread memory_mode after reset = %q, want enabled", got)
	}
}

func TestRouterThreadMemoryModeSetUpdatesRustStateSQLiteLikeRust(t *testing.T) {
	root := t.TempDir()
	sqliteHome := t.TempDir()
	t.Setenv("CODEX_SQLITE_HOME", sqliteHome)
	store := session.NewStore(root)
	threadID := session.ThreadID("thread-memory-mode-sqlite")
	createRecord(t, store, threadID, fixedTime())
	stateDB := openRouterTestSQLite(t, filepath.Join(sqliteHome, rustStateSQLiteFilename))
	routerTestExecSQL(t, stateDB, `CREATE TABLE threads (id TEXT PRIMARY KEY, memory_mode TEXT NOT NULL DEFAULT 'enabled')`)
	routerTestExecSQL(t, stateDB, `INSERT INTO threads (id, memory_mode) VALUES (?, 'enabled')`, string(threadID))

	response := NewRouter(store).Handle(requestWithParams(t, IntID(1), MethodThreadMemoryModeSet, ThreadMemoryModeSetParams{
		ThreadID: string(threadID),
		Mode:     ThreadMemoryModeDisabled,
	}))
	if response.Error != nil {
		t.Fatalf("thread/memoryMode/set error = %+v", response.Error)
	}
	if got := routerTestScalarString(t, stateDB, `SELECT memory_mode FROM threads WHERE id = ?`, string(threadID)); got != "disabled" {
		t.Fatalf("sqlite memory_mode = %q, want disabled", got)
	}
	record, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("Read thread error = %v", err)
	}
	if record.Metadata.MemoryMode != string(ThreadMemoryModeDisabled) {
		t.Fatalf("store memory mode = %q, want disabled", record.Metadata.MemoryMode)
	}
}

func TestRouterSetNameAndMetadata(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())

	named := router.Handle(requestWithParams(t, IntID(1), MethodThreadSetName, ThreadSetNameParams{
		ThreadID: "thread-a",
		Name:     "Named",
	}))
	if named.Error != nil {
		t.Fatalf("set name error: %+v", named.Error)
	}
	emptyName := router.Handle(requestWithParams(t, IntID(11), MethodThreadSetName, ThreadSetNameParams{
		ThreadID: "thread-a",
		Name:     "  ",
	}))
	if emptyName.Error == nil || emptyName.Error.Code != -32600 || emptyName.Error.Message != "thread name must not be empty" {
		t.Fatalf("empty set name response = %+v", emptyName)
	}

	metadataJSON := []byte(`{
		"threadId":"thread-a",
		"gitInfo":{"sha":"abc","branch":null,"originUrl":"git@example"}
	}`)
	request := &Request{ID: IntID(2), Method: MethodThreadMetadataUpdate, Params: metadataJSON}
	updated := router.Handle(request)
	if updated.Error != nil {
		t.Fatalf("metadata error: %+v", updated.Error)
	}
	record, err := store.Read("thread-a", true, false)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if record.Title != "Named" {
		t.Fatalf("title = %q", record.Title)
	}
	if record.Metadata.Extra[explicitThreadNameExtraKey] != true {
		t.Fatalf("explicit name marker = %#v", record.Metadata.Extra)
	}
	if record.Metadata.Git["sha"] != "abc" || record.Metadata.Git["branch"] != "" || record.Metadata.Git["origin_url"] != "git@example" {
		t.Fatalf("git = %#v", record.Metadata.Git)
	}

	readNamed := router.Handle(requestWithParams(t, IntID(12), MethodThreadRead, ThreadReadParams{ThreadID: "thread-a"}))
	if readNamed.Error != nil {
		t.Fatalf("read named thread error: %+v", readNamed.Error)
	}
	if got := readNamed.Result.(*ThreadReadResponse).Thread.Name; got == nil || *got != "Named" {
		t.Fatalf("read named thread name = %+v, want Named", got)
	}
	assertThreadPayloadField(t, readNamed.Result, "thread/read", "name", "Named")

	listNamed := router.Handle(requestWithParams(t, IntID(13), MethodThreadList, ThreadListParams{}))
	if listNamed.Error != nil {
		t.Fatalf("list named thread error: %+v", listNamed.Error)
	}
	listData, err := json.Marshal(listNamed.Result)
	if err != nil {
		t.Fatalf("Marshal(thread/list result) error = %v", err)
	}
	var listPayload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(listData, &listPayload); err != nil {
		t.Fatalf("Unmarshal(thread/list result) error = %v", err)
	}
	if len(listPayload.Data) != 1 {
		t.Fatalf("thread/list data length = %d, want 1", len(listPayload.Data))
	}
	if value, ok := listPayload.Data[0]["name"].(string); !ok || value != "Named" {
		t.Fatalf("thread/list thread.name = %v, present=%v; want Named", listPayload.Data[0]["name"], ok)
	}
	if value, ok := listPayload.Data[0]["ephemeral"].(bool); !ok || value {
		t.Fatalf("thread/list thread.ephemeral = %v, present=%v; want false", listPayload.Data[0]["ephemeral"], ok)
	}

	resumeNamed := router.Handle(requestWithParams(t, IntID(14), MethodThreadResume, ThreadResumeParams{ThreadID: "thread-a"}))
	if resumeNamed.Error != nil {
		t.Fatalf("resume named thread error: %+v", resumeNamed.Error)
	}
	if got := resumeNamed.Result.(*ThreadResumeResponse).Thread.Name; got == nil || *got != "Named" {
		t.Fatalf("resume named thread name = %+v, want Named", got)
	}
	assertThreadPayloadField(t, resumeNamed.Result, "thread/resume", "name", "Named")

	clearJSON := []byte(`{
		"threadId":"thread-a",
		"gitInfo":{"sha":null,"branch":null,"originUrl":null}
	}`)
	cleared := router.Handle(&Request{ID: IntID(3), Method: MethodThreadMetadataUpdate, Params: clearJSON})
	if cleared.Error != nil {
		t.Fatalf("metadata clear error: %+v", cleared.Error)
	}
	if got := cleared.Result.(*ThreadMetadataUpdateResponse).Thread.GitInfo; got != nil {
		t.Fatalf("cleared response gitInfo = %+v, want nil", got)
	}
	read := router.Handle(requestWithParams(t, IntID(4), MethodThreadRead, ThreadReadParams{ThreadID: "thread-a"}))
	if read.Error != nil {
		t.Fatalf("read after clear error: %+v", read.Error)
	}
	if got := read.Result.(*ThreadReadResponse).Thread.GitInfo; got != nil {
		t.Fatalf("cleared read gitInfo = %+v, want nil", got)
	}
}

func TestRouterThreadMetadataUpdateUpdatesRustStateSQLiteLikeRust(t *testing.T) {
	root := t.TempDir()
	sqliteHome := t.TempDir()
	t.Setenv("CODEX_SQLITE_HOME", sqliteHome)
	store := session.NewStore(root)
	threadID := session.ThreadID("thread-git-sqlite")
	createRecord(t, store, threadID, fixedTime())
	stateDB := openRouterTestSQLite(t, filepath.Join(sqliteHome, rustStateSQLiteFilename))
	routerTestExecSQL(t, stateDB, `CREATE TABLE threads (id TEXT PRIMARY KEY, git_sha TEXT, git_branch TEXT, git_origin_url TEXT)`)
	routerTestExecSQL(t, stateDB, `INSERT INTO threads (id) VALUES (?)`, string(threadID))

	sha := "abc123"
	branch := "feature/sqlite"
	origin := "git@example.com:openai/codex.git"
	update := NewRouter(store).Handle(requestWithParams(t, IntID(1), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID: string(threadID),
		GitInfo: &ThreadMetadataGitInfoPatch{
			SHA:       OptionalString{Set: true, Value: &sha},
			Branch:    OptionalString{Set: true, Value: &branch},
			OriginURL: OptionalString{Set: true, Value: &origin},
		},
	}))
	if update.Error != nil {
		t.Fatalf("thread/metadata/update error = %+v", update.Error)
	}
	if got := routerTestScalarString(t, stateDB, `SELECT git_sha FROM threads WHERE id = ?`, string(threadID)); got != sha {
		t.Fatalf("sqlite git_sha = %q, want %q", got, sha)
	}
	if got := routerTestScalarString(t, stateDB, `SELECT git_branch FROM threads WHERE id = ?`, string(threadID)); got != branch {
		t.Fatalf("sqlite git_branch = %q, want %q", got, branch)
	}
	if got := routerTestScalarString(t, stateDB, `SELECT git_origin_url FROM threads WHERE id = ?`, string(threadID)); got != origin {
		t.Fatalf("sqlite git_origin_url = %q, want %q", got, origin)
	}

	clear := NewRouter(store).Handle(requestWithParams(t, IntID(2), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID: string(threadID),
		GitInfo: &ThreadMetadataGitInfoPatch{
			SHA:       OptionalString{Set: true},
			Branch:    OptionalString{Set: true},
			OriginURL: OptionalString{Set: true},
		},
	}))
	if clear.Error != nil {
		t.Fatalf("thread/metadata/update clear error = %+v", clear.Error)
	}
	if got := routerTestScalarNullString(t, stateDB, `SELECT git_sha FROM threads WHERE id = ?`, string(threadID)); got.Valid {
		t.Fatalf("sqlite git_sha after clear = %+v, want NULL", got)
	}
	if got := routerTestScalarNullString(t, stateDB, `SELECT git_branch FROM threads WHERE id = ?`, string(threadID)); got.Valid {
		t.Fatalf("sqlite git_branch after clear = %+v, want NULL", got)
	}
	if got := routerTestScalarNullString(t, stateDB, `SELECT git_origin_url FROM threads WHERE id = ?`, string(threadID)); got.Valid {
		t.Fatalf("sqlite git_origin_url after clear = %+v, want NULL", got)
	}
}

func TestRouterMetadataWritesMissingThreadUseRustErrors(t *testing.T) {
	router := NewRouter(session.NewStore(t.TempDir()))
	const missingThreadID = "missing-thread"
	const want = "thread not found: missing-thread"

	named := router.Handle(requestWithParams(t, IntID(1), MethodThreadSetName, ThreadSetNameParams{
		ThreadID: missingThreadID,
		Name:     "Named",
	}))
	if named.Error == nil || named.Error.Code != -32600 || named.Error.Message != want {
		t.Fatalf("set name missing thread response = %+v", named)
	}

	mode := router.Handle(requestWithParams(t, IntID(2), MethodThreadMemoryModeSet, ThreadMemoryModeSetParams{
		ThreadID: missingThreadID,
		Mode:     ThreadMemoryModeDisabled,
	}))
	if mode.Error == nil || mode.Error.Code != -32600 || mode.Error.Message != want {
		t.Fatalf("memory mode missing thread response = %+v", mode)
	}

	sha := "abc"
	metadata := router.Handle(requestWithParams(t, IntID(3), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID: missingThreadID,
		GitInfo:  &ThreadMetadataGitInfoPatch{SHA: OptionalString{Set: true, Value: &sha}},
	}))
	if metadata.Error == nil || metadata.Error.Code != -32600 || metadata.Error.Message != want {
		t.Fatalf("metadata missing thread response = %+v", metadata)
	}
}

func TestRouterMemoryModeRejectsUnknownModeAsInvalidRequest(t *testing.T) {
	router := NewRouter(session.NewStore(t.TempDir()))

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadMemoryModeSet, ThreadMemoryModeSetParams{
		ThreadID: "thread-a",
		Mode:     ThreadMemoryMode("turbo"),
	}))
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != `unsupported memory mode "turbo"` {
		t.Fatalf("memory mode unknown mode response = %+v", response)
	}
}

func TestRouterSetNameAndMemoryModeRejectArchivedThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "archive me",
	}))
	if start.Error != nil {
		t.Fatalf("start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router, store, threadID)
	archive := router.Handle(requestWithParams(t, IntID(2), MethodThreadArchive, ThreadArchiveParams{ThreadID: threadID}))
	if archive.Error != nil {
		t.Fatalf("archive error: %+v", archive.Error)
	}
	want := "session " + threadID + " is archived. Run `codex unarchive " + threadID + "` to unarchive it first."

	named := router.Handle(requestWithParams(t, IntID(3), MethodThreadSetName, ThreadSetNameParams{
		ThreadID: threadID,
		Name:     "Archived Name",
	}))
	if named.Error == nil || named.Error.Code != -32600 {
		t.Fatalf("set name response = %+v", named)
	}
	if named.Error.Message != want {
		t.Fatalf("set name error = %q", named.Error.Message)
	}

	mode := router.Handle(requestWithParams(t, IntID(4), MethodThreadMemoryModeSet, ThreadMemoryModeSetParams{
		ThreadID: threadID,
		Mode:     ThreadMemoryModeDisabled,
	}))
	if mode.Error == nil || mode.Error.Code != -32600 {
		t.Fatalf("memory mode response = %+v", mode)
	}
	if mode.Error.Message != want {
		t.Fatalf("memory mode error = %q", mode.Error.Message)
	}

	record, err := store.Read(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatalf("Read archived record error = %v", err)
	}
	if record.Title != "" || record.Metadata.MemoryMode != "" {
		t.Fatalf("archived metadata changed = %+v", record)
	}
}

func TestRouterThreadMetadataUpdateRejectsEmptyGitInfoPatch(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID: "thread-a",
		GitInfo:  &ThreadMetadataGitInfoPatch{},
	}))
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "gitInfo must include at least one field" {
		t.Fatalf("metadata empty gitInfo response = %+v", response)
	}

	blankSHA := "  "
	blank := router.Handle(requestWithParams(t, IntID(2), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID: "thread-a",
		GitInfo:  &ThreadMetadataGitInfoPatch{SHA: OptionalString{Set: true, Value: &blankSHA}},
	}))
	if blank.Error == nil || blank.Error.Code != -32600 || blank.Error.Message != "gitInfo.sha must not be empty" {
		t.Fatalf("metadata blank gitInfo response = %+v", blank)
	}
}

func TestRouterSetNameAndMetadataRepairRolloutOnlyThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: store.Root(),
		SessionID: "session-rollout",
		ThreadID:  "thread-rollout",
		Source:    "cli",
		CWD:       "/repo",
		Now:       fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "message", Role: "user", Text: "from rollout"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	named := router.Handle(requestWithParams(t, IntID(1), MethodThreadSetName, ThreadSetNameParams{
		ThreadID: "thread-rollout",
		Name:     "Recovered",
	}))
	if named.Error != nil {
		t.Fatalf("set name error: %+v", named.Error)
	}

	metadataJSON := []byte(`{
		"threadId":"thread-rollout",
		"gitInfo":{"sha":"abc","branch":null,"originUrl":"git@example"}
	}`)
	updated := router.Handle(&Request{ID: IntID(2), Method: MethodThreadMetadataUpdate, Params: metadataJSON})
	if updated.Error != nil {
		t.Fatalf("metadata error: %+v", updated.Error)
	}
	thread := updated.Result.(*ThreadMetadataUpdateResponse).Thread
	if thread.Name == nil || *thread.Name != "Recovered" || thread.Path == nil || *thread.Path != recorder.Path() {
		t.Fatalf("updated thread = %+v", thread)
	}
	record, err := store.Load("thread-rollout")
	if err != nil {
		t.Fatalf("Load(repaired) error = %v", err)
	}
	if record.Title != "Recovered" || record.Preview != "from rollout" || len(record.Items) != 1 {
		t.Fatalf("record = %+v", record)
	}
	if record.Metadata.Git["sha"] != "abc" || record.Metadata.Git["branch"] != "" || record.Metadata.Git["origin_url"] != "git@example" {
		t.Fatalf("git = %#v", record.Metadata.Git)
	}
	fromRollout, err := rollout.RecordFromPath(recorder.Path(), false)
	if err != nil {
		t.Fatalf("RecordFromPath(updated rollout) error = %v", err)
	}
	if fromRollout.Metadata.Git["sha"] != "abc" || fromRollout.Metadata.Git["branch"] != "" || fromRollout.Metadata.Git["origin_url"] != "git@example" {
		t.Fatalf("rollout git = %#v", fromRollout.Metadata.Git)
	}
}

func TestRouterThreadMetadataUpdateRepairsArchivedRolloutOnlyThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     store.Root(),
		SessionID:     "session-archived-rollout",
		ThreadID:      "thread-archived-rollout",
		Source:        "cli",
		CWD:           "/repo",
		ModelProvider: "openai",
		Now:           fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "message", Role: "user", Text: "archived preview"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	archivedPath, err := rollout.Archive(recorder.Path(), store.Root())
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}

	branch := "feature/archived-thread"
	updated := router.Handle(requestWithParams(t, IntID(1), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID: "thread-archived-rollout",
		GitInfo:  &ThreadMetadataGitInfoPatch{Branch: OptionalString{Set: true, Value: &branch}},
	}))
	if updated.Error != nil {
		t.Fatalf("metadata error: %+v", updated.Error)
	}
	thread := updated.Result.(*ThreadMetadataUpdateResponse).Thread
	if thread.ID != "thread-archived-rollout" || thread.Preview != "archived preview" || thread.Path == nil || *thread.Path != archivedPath {
		t.Fatalf("updated thread = %+v, archivedPath = %q", thread, archivedPath)
	}
	if thread.GitInfo == nil || thread.GitInfo.Branch == nil || *thread.GitInfo.Branch != branch {
		t.Fatalf("updated gitInfo = %+v", thread.GitInfo)
	}

	record, err := store.Read("thread-archived-rollout", true, false)
	if err != nil {
		t.Fatalf("Read(repaired archived record) error = %v", err)
	}
	if !record.Archived || record.Preview != "archived preview" || record.Metadata.Git["branch"] != branch {
		t.Fatalf("record = %+v", record)
	}
	fromRollout, err := rollout.RecordFromPath(archivedPath, true)
	if err != nil {
		t.Fatalf("RecordFromPath(archived rollout) error = %v", err)
	}
	if !fromRollout.Archived || fromRollout.Metadata.Git["branch"] != branch {
		t.Fatalf("archived rollout record = %+v", fromRollout)
	}
}

func TestRouterMemoryModeSetAppendsRolloutMetadata(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     store.Root(),
		SessionID:     "session-rollout-memory",
		ThreadID:      "thread-rollout-memory",
		Source:        "cli",
		CWD:           "/repo",
		ModelProvider: "openai",
		Now:           fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadMemoryModeSet, ThreadMemoryModeSetParams{
		ThreadID: "thread-rollout-memory",
		Mode:     ThreadMemoryModeDisabled,
	}))
	if response.Error != nil {
		t.Fatalf("memory mode error: %+v", response.Error)
	}

	record, err := store.Read("thread-rollout-memory", true, true)
	if err != nil {
		t.Fatalf("Read(repaired) error = %v", err)
	}
	if record.Metadata.MemoryMode != string(ThreadMemoryModeDisabled) {
		t.Fatalf("store memory mode = %q", record.Metadata.MemoryMode)
	}
	fromRollout, err := rollout.RecordFromPath(recorder.Path(), false)
	if err != nil {
		t.Fatalf("RecordFromPath(updated rollout) error = %v", err)
	}
	if fromRollout.Metadata.MemoryMode != string(ThreadMemoryModeDisabled) {
		t.Fatalf("rollout memory mode = %q", fromRollout.Metadata.MemoryMode)
	}
	lines, _, err := rollout.Load(recorder.Path())
	if err != nil {
		t.Fatalf("Load(updated rollout) error = %v", err)
	}
	var sessionMetaCount int
	for _, line := range lines {
		if line.Type == "session_meta" {
			sessionMetaCount++
		}
	}
	if sessionMetaCount != 2 {
		t.Fatalf("session_meta count = %d, want 2", sessionMetaCount)
	}
}

func TestRouterResumeInitialTurnsPageWithExcludeTurns(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())

	limit := 1
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID:     "thread-a",
		ExcludeTurns: true,
		InitialTurnsPage: &ThreadInitialPageParams{
			Limit:     &limit,
			ItemsView: TurnItemsSummary,
		},
	}))
	if response.Error != nil {
		t.Fatalf("resume error: %+v", response.Error)
	}
	resume := response.Result.(*ThreadResumeResponse)
	if len(resume.Thread.Turns) != 0 {
		t.Fatalf("thread turns should be excluded: %+v", resume.Thread.Turns)
	}
	if resume.InitialTurnsPage == nil || len(resume.InitialTurnsPage.Data) != 1 {
		t.Fatalf("initial turns page = %+v", resume.InitialTurnsPage)
	}
	if resume.InitialTurnsPage.Data[0].ItemsView != TurnItemsSummary {
		t.Fatalf("items view = %+v", resume.InitialTurnsPage.Data[0])
	}
}

func TestRouterThreadResumeRedactsRemoteClientInitialTurnsPage(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	if err := store.Create(&session.Record{
		ID:        "thread-redact",
		SessionID: "thread-redact",
		Title:     "redact",
		Preview:   "redact",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           "D:/repo",
			ModelProvider: "openai",
			Source:        "cli",
		},
		Items: []session.Item{
			{
				ID:        "user-redact",
				Type:      "message",
				Role:      "user",
				Text:      "Saved user message",
				CreatedAt: now,
				Metadata:  map[string]any{"turnId": "turn-redact"},
			},
			{
				ID:        "mcp-redact",
				Type:      "mcpToolCall",
				CreatedAt: now.Add(time.Second),
				Metadata:  map[string]any{"turnId": "turn-redact"},
				Data: map[string]any{
					"turnId":    "turn-redact",
					"server":    "docs",
					"tool":      "lookup",
					"status":    "completed",
					"arguments": map[string]any{"secret": "argument"},
					"result": map[string]any{
						"content":           []any{map[string]any{"type": "text", "text": "secret result"}},
						"structuredContent": map[string]any{"secret": "structured"},
						"_meta":             map[string]any{"secret": "meta"},
					},
				},
			},
			{
				ID:        "image-redact",
				Type:      "imageGeneration",
				Status:    "completed",
				CreatedAt: now.Add(2 * time.Second),
				Metadata:  map[string]any{"turnId": "turn-redact"},
				Data: map[string]any{
					"turnId":        "turn-redact",
					"revisedPrompt": "secret revised prompt",
					"result":        "base64-image-result",
				},
			},
		},
	}); err != nil {
		t.Fatalf("Create(redaction record) error = %v", err)
	}

	limit := 10
	remoteClient := "codex_chatgpt_android_remote"
	remote := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID:         "thread-redact",
		ClientName:       &remoteClient,
		InitialTurnsPage: &ThreadInitialPageParams{Limit: &limit, ItemsView: TurnItemsFull},
	}))
	if remote.Error != nil {
		t.Fatalf("remote thread/resume error: %+v", remote.Error)
	}
	remoteResume := remote.Result.(*ThreadResumeResponse)
	assertRedactedResumeTurn(t, resumeTurnWithItemTypeForTest(t, remoteResume.Thread.Turns, "mcpToolCall"))
	if remoteResume.InitialTurnsPage == nil || len(remoteResume.InitialTurnsPage.Data) == 0 {
		t.Fatalf("remote initialTurnsPage = %+v", remoteResume.InitialTurnsPage)
	}
	assertRedactedResumeTurn(t, resumeTurnWithItemTypeForTest(t, remoteResume.InitialTurnsPage.Data, "mcpToolCall"))

	normalClient := "some_other_client"
	normal := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID:         "thread-redact",
		ClientName:       &normalClient,
		InitialTurnsPage: &ThreadInitialPageParams{Limit: &limit, ItemsView: TurnItemsFull},
	}))
	if normal.Error != nil {
		t.Fatalf("normal thread/resume error: %+v", normal.Error)
	}
	normalResume := normal.Result.(*ThreadResumeResponse)
	assertUnredactedResumeTurn(t, resumeTurnWithItemTypeForTest(t, normalResume.Thread.Turns, "mcpToolCall"))
	if normalResume.InitialTurnsPage == nil || len(normalResume.InitialTurnsPage.Data) == 0 {
		t.Fatalf("normal initialTurnsPage = %+v", normalResume.InitialTurnsPage)
	}
	assertUnredactedResumeTurn(t, resumeTurnWithItemTypeForTest(t, normalResume.InitialTurnsPage.Data, "mcpToolCall"))
}

func TestRouterResumeResponseIncludesRuntimeSettings(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	if err := store.Save(&session.Record{
		ID:        "thread-a",
		SessionID: "thread-a",
		CreatedAt: fixedTime(),
		UpdatedAt: fixedTime(),
		RecencyAt: fixedTime(),
		Metadata: session.Metadata{
			CWD:           "/repo",
			Model:         "gpt-default",
			ModelProvider: "openai",
			ServiceTier:   "auto",
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	model := "mock-model"
	provider := "mock_provider"

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID:      "thread-a",
		Model:         &model,
		ModelProvider: &provider,
	}))
	if response.Error != nil {
		t.Fatalf("resume error: %+v", response.Error)
	}
	resume := response.Result.(*ThreadResumeResponse)
	if resume.CWD != "/repo" || resume.Model != model || resume.ModelProvider != provider {
		t.Fatalf("resume settings = cwd:%q model:%q provider:%q", resume.CWD, resume.Model, resume.ModelProvider)
	}
	if resume.ServiceTier == nil || *resume.ServiceTier != "auto" {
		t.Fatalf("service tier = %+v", resume.ServiceTier)
	}
}

func TestRouterLifecycleNullServiceTierUsesDefault(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })

	startResponse := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, map[string]any{
		"cwd":         "D:/repo",
		"prompt":      "hello",
		"serviceTier": nil,
	}))
	if startResponse.Error != nil {
		t.Fatalf("start error: %+v", startResponse.Error)
	}
	start := startResponse.Result.(*ThreadStartResponse)
	if start.ServiceTier == nil || *start.ServiceTier != model.ServiceTierDefaultRequestValue {
		t.Fatalf("start service tier = %+v", start.ServiceTier)
	}
	record, err := store.Read(session.ThreadID(start.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read start record error = %v", err)
	}
	if record.Metadata.ServiceTier != model.ServiceTierDefaultRequestValue {
		t.Fatalf("start metadata service tier = %q", record.Metadata.ServiceTier)
	}

	record.Metadata.ServiceTier = "priority"
	if err := store.Save(record); err != nil {
		t.Fatalf("Save record error = %v", err)
	}
	materializeThreadRolloutForTest(t, router, store, start.Thread.ID)
	resumeResponse := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, map[string]any{
		"threadId":    start.Thread.ID,
		"serviceTier": nil,
	}))
	if resumeResponse.Error != nil {
		t.Fatalf("resume error: %+v", resumeResponse.Error)
	}
	resume := resumeResponse.Result.(*ThreadResumeResponse)
	if resume.ServiceTier == nil || *resume.ServiceTier != model.ServiceTierDefaultRequestValue {
		t.Fatalf("resume service tier = %+v", resume.ServiceTier)
	}

	forkResponse := router.Handle(requestWithParams(t, IntID(3), MethodThreadFork, map[string]any{
		"threadId":    start.Thread.ID,
		"serviceTier": nil,
	}))
	if forkResponse.Error != nil {
		t.Fatalf("fork error: %+v", forkResponse.Error)
	}
	fork := forkResponse.Result.(*ThreadForkResponse)
	if fork.ServiceTier == nil || *fork.ServiceTier != model.ServiceTierDefaultRequestValue {
		t.Fatalf("fork service tier = %+v", fork.ServiceTier)
	}
	forkRecord, err := store.Read(session.ThreadID(fork.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read fork record error = %v", err)
	}
	if forkRecord.Metadata.ServiceTier != model.ServiceTierDefaultRequestValue {
		t.Fatalf("fork metadata service tier = %q", forkRecord.Metadata.ServiceTier)
	}
}

func TestRouterThreadStartDropsUnsupportedServiceTier(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })

	startResponse := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         "D:/repo",
		ServiceTier: stringPtr("experimental-tier-id"),
	}))
	if startResponse.Error != nil {
		t.Fatalf("start error: %+v", startResponse.Error)
	}
	start := startResponse.Result.(*ThreadStartResponse)
	if start.ServiceTier != nil {
		t.Fatalf("start service tier = %+v", start.ServiceTier)
	}
	record, err := store.Read(session.ThreadID(start.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read start record error = %v", err)
	}
	if record.Metadata.ServiceTier != "" {
		t.Fatalf("start metadata service tier = %q", record.Metadata.ServiceTier)
	}
}

func TestRouterThreadLifecycleRejectsPermissionsWithSandbox(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	permissions := "dev"
	sandboxPolicy := map[string]any{"mode": "read-only"}

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         "D:/repo",
		Permissions: &permissions,
		Sandbox:     sandboxPolicy,
	}))
	assertPermissionsSandboxConflict(t, start)

	resume := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID:    "thread-missing",
		Permissions: &permissions,
		Sandbox:     sandboxPolicy,
	}))
	assertPermissionsSandboxConflict(t, resume)

	fork := router.Handle(requestWithParams(t, IntID(3), MethodThreadFork, ThreadForkParams{
		ThreadID:    "thread-missing",
		Permissions: &permissions,
		Sandbox:     sandboxPolicy,
	}))
	assertPermissionsSandboxConflict(t, fork)
}

func TestRouterThreadResumeRejectsArchivedSessionByID(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "archive me",
	}))
	if start.Error != nil {
		t.Fatalf("start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router, store, threadID)
	archive := router.Handle(requestWithParams(t, IntID(2), MethodThreadArchive, ThreadArchiveParams{ThreadID: threadID}))
	if archive.Error != nil {
		t.Fatalf("archive error: %+v", archive.Error)
	}
	archivedPath, err := rollout.FindThreadPath(store.Root(), threadID, true)
	if err != nil {
		t.Fatalf("archived rollout path error: %v", err)
	}

	resume := router.Handle(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{ThreadID: threadID}))
	if resume.Error == nil || resume.Error.Code != -32600 {
		t.Fatalf("resume response = %+v", resume)
	}
	want := "session " + threadID + " is archived. Run `codex unarchive " + threadID + "` to unarchive it first."
	if resume.Error.Message != want {
		t.Fatalf("resume error = %q", resume.Error.Message)
	}

	resumeByPath := router.Handle(requestWithParams(t, IntID(4), MethodThreadResume, ThreadResumeParams{
		ThreadID: "ignored-thread-id",
		Path:     &archivedPath,
	}))
	if resumeByPath.Error == nil || resumeByPath.Error.Code != -32600 {
		t.Fatalf("resume by path response = %+v", resumeByPath)
	}
	if resumeByPath.Error.Message != want {
		t.Fatalf("resume by path error = %q", resumeByPath.Error.Message)
	}
}

func TestRouterThreadForkRejectsArchivedSessionByIDAndPath(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "archive me",
	}))
	if start.Error != nil {
		t.Fatalf("start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router, store, threadID)
	archive := router.Handle(requestWithParams(t, IntID(2), MethodThreadArchive, ThreadArchiveParams{ThreadID: threadID}))
	if archive.Error != nil {
		t.Fatalf("archive error: %+v", archive.Error)
	}
	archivedPath, err := rollout.FindThreadPath(store.Root(), threadID, true)
	if err != nil {
		t.Fatalf("archived rollout path error: %v", err)
	}
	want := "session " + threadID + " is archived. Run `codex unarchive " + threadID + "` to unarchive it first."

	forkByID := router.Handle(requestWithParams(t, IntID(3), MethodThreadFork, ThreadForkParams{ThreadID: threadID}))
	if forkByID.Error == nil || forkByID.Error.Code != -32600 {
		t.Fatalf("fork by ID response = %+v", forkByID)
	}
	if forkByID.Error.Message != want {
		t.Fatalf("fork by ID error = %q", forkByID.Error.Message)
	}

	forkByPath := router.Handle(requestWithParams(t, IntID(4), MethodThreadFork, ThreadForkParams{
		ThreadID: "ignored-thread-id",
		Path:     &archivedPath,
	}))
	if forkByPath.Error == nil || forkByPath.Error.Code != -32600 {
		t.Fatalf("fork by path response = %+v", forkByPath)
	}
	if forkByPath.Error.Message != want {
		t.Fatalf("fork by path error = %q", forkByPath.Error.Message)
	}
}

func TestRouterResumeAndForkRuntimeWorkspaceRootsUseEffectiveRoots(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })
	cwd := t.TempDir()
	storedRoot := t.TempDir()
	explicitRoot := t.TempDir()

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    cwd,
		Prompt: "hello roots",
	}))
	if start.Error != nil {
		t.Fatalf("start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read start record error = %v", err)
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra["runtime_workspace_roots"] = []string{storedRoot, filepath.Join(storedRoot, ".")}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save record error = %v", err)
	}
	materializeThreadRolloutForTest(t, router, store, threadID)

	resume := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID:     threadID,
		ExcludeTurns: true,
	}))
	if resume.Error != nil {
		t.Fatalf("resume error: %+v", resume.Error)
	}
	resumeResult := resume.Result.(*ThreadResumeResponse)
	if len(resumeResult.RuntimeWorkspaceRoots) != 1 || !sameAppPath(resumeResult.RuntimeWorkspaceRoots[0], storedRoot) {
		t.Fatalf("resume runtimeWorkspaceRoots = %#v", resumeResult.RuntimeWorkspaceRoots)
	}

	fork := router.Handle(requestWithParams(t, IntID(3), MethodThreadFork, ThreadForkParams{
		ThreadID:              threadID,
		RuntimeWorkspaceRoots: []string{explicitRoot, filepath.Join(explicitRoot, ".")},
	}))
	if fork.Error != nil {
		t.Fatalf("fork error: %+v", fork.Error)
	}
	forkResult := fork.Result.(*ThreadForkResponse)
	if len(forkResult.RuntimeWorkspaceRoots) != 1 || !sameAppPath(forkResult.RuntimeWorkspaceRoots[0], explicitRoot) {
		t.Fatalf("fork runtimeWorkspaceRoots = %#v", forkResult.RuntimeWorkspaceRoots)
	}
	forkRecord, err := store.Read(session.ThreadID(forkResult.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read fork record error = %v", err)
	}
	forkRoots := stringSliceFromAny(forkRecord.Metadata.Extra["runtime_workspace_roots"])
	if len(forkRoots) != 1 || !sameAppPath(forkRoots[0], explicitRoot) {
		t.Fatalf("fork record runtime roots = %#v", forkRoots)
	}
}

func TestRouterResumeFromPath(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())
	if err := router.createThreadRollout(&session.Record{
		ID:        "thread-a",
		SessionID: "thread-a",
		Metadata:  session.Metadata{ModelProvider: "openai"},
		Items: []session.Item{{
			ID:        "path-item-1",
			Type:      "message",
			Role:      "user",
			Text:      "from explicit path",
			CreatedAt: fixedTime(),
			Metadata:  map[string]any{"turnId": "turn-path"},
		}},
	}, fixedTime()); err != nil {
		t.Fatalf("create rollout error: %v", err)
	}
	path, err := rollout.FindThreadPath(store.Root(), "thread-a", false)
	if err != nil {
		t.Fatalf("source rollout path error: %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: "ignored-thread-id",
		Path:     &path,
	}))
	if response.Error != nil {
		t.Fatalf("resume error: %+v", response.Error)
	}
	resume := response.Result.(*ThreadResumeResponse)
	if resume.Thread.ID != "thread-a" {
		t.Fatalf("resume thread ID = %q, want thread-a", resume.Thread.ID)
	}
	if resume.Thread.Path == nil || *resume.Thread.Path != path {
		t.Fatalf("resume thread Path = %+v, want %q", resume.Thread.Path, path)
	}
	if len(resume.Thread.Turns) != 1 || len(resume.Thread.Turns[0].Items) != 1 || resume.Thread.Turns[0].Items[0].Text != "from explicit path" {
		t.Fatalf("resume turns = %+v", resume.Thread.Turns)
	}
}

func TestRouterResumeFromExternalPath(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	externalHome := t.TempDir()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     externalHome,
		SessionID:     "external-session",
		ThreadID:      "external-thread",
		Source:        "cli",
		CWD:           "/external/repo",
		ModelProvider: "openai",
		Now:           fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "message", Role: "user", Text: "external path history"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	path := recorder.Path()

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: "not-the-rollout-thread",
		Path:     &path,
	}))
	if response.Error != nil {
		t.Fatalf("resume error: %+v", response.Error)
	}
	resume := response.Result.(*ThreadResumeResponse)
	if resume.Thread.ID != "external-thread" || resume.Thread.Preview != "external path history" || resume.Thread.Status.Type != "idle" {
		t.Fatalf("resume thread = %+v", resume.Thread)
	}
	if resume.Thread.Path == nil || *resume.Thread.Path != path {
		t.Fatalf("resume thread Path = %+v, want %q", resume.Thread.Path, path)
	}
	if len(resume.Thread.Turns) != 1 || len(resume.Thread.Turns[0].Items) != 1 {
		t.Fatalf("resume turns = %+v", resume.Thread.Turns)
	}
}

func TestRouterResumeForkRejectDirectoryPath(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	path := t.TempDir()

	resume := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: "thread-a",
		Path:     &path,
	}))
	if resume.Error == nil || !strings.Contains(resume.Error.Message, "path is a directory") || strings.Contains(resume.Error.Message, "Is a directory") {
		t.Fatalf("resume error = %+v", resume.Error)
	}

	fork := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID: "thread-a",
		Path:     &path,
	}))
	if fork.Error == nil || !strings.Contains(fork.Error.Message, "path is a directory") || strings.Contains(fork.Error.Message, "Is a directory") {
		t.Fatalf("fork error = %+v", fork.Error)
	}
}

func TestRouterReadAndResumeFallbackToRollout(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     store.Root(),
		SessionID:     "session-rollout",
		ThreadID:      "thread-rollout",
		Source:        "cli",
		ThreadSource:  "user",
		CWD:           "/repo",
		ModelProvider: "openai",
		HistoryMode:   "legacy",
		CLIVersion:    "0.0.0",
		Now:           fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "message", Role: "user", Text: "from rollout"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	read := router.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{
		ThreadID:     "thread-rollout",
		IncludeTurns: true,
	}))
	if read.Error != nil {
		t.Fatalf("read error: %+v", read.Error)
	}
	readThread := read.Result.(*ThreadReadResponse).Thread
	if readThread.Preview != "from rollout" || len(readThread.Turns) != 1 {
		t.Fatalf("read thread = %+v", readThread)
	}
	if readThread.ThreadSource == nil || *readThread.ThreadSource != ThreadSourceUser {
		t.Fatalf("read threadSource = %+v", readThread.ThreadSource)
	}
	if readThread.Path == nil || *readThread.Path != recorder.Path() {
		t.Fatalf("read path = %+v, want %q", readThread.Path, recorder.Path())
	}

	resume := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID: "thread-rollout",
	}))
	if resume.Error != nil {
		t.Fatalf("resume error: %+v", resume.Error)
	}
	resumed := resume.Result.(*ThreadResumeResponse).Thread
	if resumed.ID != "thread-rollout" || len(resumed.Turns) != 1 {
		t.Fatalf("resumed thread = %+v", resumed)
	}
	if resumed.ThreadSource == nil || *resumed.ThreadSource != ThreadSourceUser {
		t.Fatalf("resumed threadSource = %+v", resumed.ThreadSource)
	}

	list := router.Handle(requestWithParams(t, IntID(3), MethodThreadList, ThreadListParams{}))
	if list.Error != nil {
		t.Fatalf("list error: %+v", list.Error)
	}
	listResult := list.Result.(*ThreadListResponse)
	if len(listResult.Data) != 1 || listResult.Data[0].ID != "thread-rollout" {
		t.Fatalf("list result = %+v", listResult)
	}

	search := router.Handle(requestWithParams(t, IntID(4), MethodThreadSearch, ThreadSearchParams{SearchTerm: " rollout "}))
	if search.Error != nil {
		t.Fatalf("search error: %+v", search.Error)
	}
	searchResult := search.Result.(*ThreadSearchResponse)
	if len(searchResult.Data) != 1 || searchResult.Data[0].Thread.ID != "thread-rollout" {
		t.Fatalf("search result = %+v", searchResult)
	}

	items := router.Handle(requestWithParams(t, IntID(5), MethodThreadItemsList, ThreadItemsListParams{ThreadID: "thread-rollout"}))
	if items.Error != nil {
		t.Fatalf("items error = %+v", items.Error)
	}
	itemsResult := items.Result.(*ThreadItemsListResponse)
	if len(itemsResult.Data) != 1 || itemsResult.Data[0].ID != "user-1" || itemsResult.Data[0].Text != "from rollout" {
		t.Fatalf("items result = %+v", itemsResult)
	}

	turns := router.Handle(requestWithParams(t, IntID(6), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: "thread-rollout"}))
	if turns.Error != nil {
		t.Fatalf("turns error: %+v", turns.Error)
	}
	turnsResult := turns.Result.(*TurnsPage)
	if len(turnsResult.Data) != 1 || len(turnsResult.Data[0].Items) != 1 {
		t.Fatalf("turns result = %+v", turnsResult)
	}
}

func TestRouterThreadReadAndListPreservePathlessStoreMetadata(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	if err := store.Create(&session.Record{
		ID:        "pathless-thread",
		SessionID: "pathless-thread",
		Title:     "named pathless thread",
		Preview:   "",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           "",
			ModelProvider: "test-provider",
			Source:        "cli",
			MemoryMode:    "disabled",
		},
	}); err != nil {
		t.Fatalf("Create(pathless record) error = %v", err)
	}

	read := router.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{ThreadID: "pathless-thread"}))
	if read.Error != nil {
		t.Fatalf("thread/read pathless error: %+v", read.Error)
	}
	readThread := read.Result.(*ThreadReadResponse).Thread
	if readThread.Path != nil || readThread.Preview != "" || readThread.Name == nil || *readThread.Name != "named pathless thread" {
		t.Fatalf("thread/read pathless thread = %+v", readThread)
	}
	assertThreadPayloadField(t, read.Result, "thread/read pathless", "path", nil)
	assertThreadPayloadField(t, read.Result, "thread/read pathless", "preview", "")
	assertThreadPayloadField(t, read.Result, "thread/read pathless", "name", "named pathless thread")

	limit := 10
	list := router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{
		Limit:          &limit,
		ModelProviders: []string{},
	}))
	if list.Error != nil {
		t.Fatalf("thread/list pathless error: %+v", list.Error)
	}
	listData, err := json.Marshal(list.Result)
	if err != nil {
		t.Fatalf("Marshal(thread/list pathless result) error = %v", err)
	}
	var listPayload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(listData, &listPayload); err != nil {
		t.Fatalf("Unmarshal(thread/list pathless result) error = %v", err)
	}
	if len(listPayload.Data) != 1 {
		t.Fatalf("thread/list pathless data length = %d, want 1", len(listPayload.Data))
	}
	threadPayload := listPayload.Data[0]
	if value, ok := threadPayload["path"]; !ok || value != nil {
		t.Fatalf("thread/list pathless thread.path = %v, present=%v; want explicit null", value, ok)
	}
	if value, ok := threadPayload["preview"].(string); !ok || value != "" {
		t.Fatalf("thread/list pathless thread.preview = %v, present=%v; want empty", threadPayload["preview"], ok)
	}
	if value, ok := threadPayload["name"].(string); !ok || value != "named pathless thread" {
		t.Fatalf("thread/list pathless thread.name = %v, present=%v; want named pathless thread", threadPayload["name"], ok)
	}
}

func TestRouterPaginatedRolloutSupportsPagedHistoryReads(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     store.Root(),
		SessionID:     "session-paginated",
		ThreadID:      "thread-paginated",
		Source:        "cli",
		ModelProvider: "openai",
		HistoryMode:   string(ThreadHistoryPaginated),
		Now:           fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "message", Role: "user", Text: "from paginated rollout"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	readSummary := router.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{ThreadID: "thread-paginated"}))
	if readSummary.Error != nil {
		t.Fatalf("read summary error = %+v", readSummary.Error)
	}
	if readSummary.Result.(*ThreadReadResponse).Thread.HistoryMode != ThreadHistoryPaginated {
		t.Fatalf("read summary thread = %+v", readSummary.Result.(*ThreadReadResponse).Thread)
	}

	readTurns := router.Handle(requestWithParams(t, IntID(2), MethodThreadRead, ThreadReadParams{ThreadID: "thread-paginated", IncludeTurns: true}))
	if readTurns.Error == nil || readTurns.Error.Code != -32600 || readTurns.Error.Message != "paginated threads do not support thread/read(includeTurns=true)" {
		t.Fatalf("read turns response = %+v", readTurns)
	}
	itemsList := router.Handle(requestWithParams(t, IntID(21), MethodThreadItemsList, ThreadItemsListParams{ThreadID: "thread-paginated"}))
	if itemsList.Error != nil || len(itemsList.Result.(*ThreadItemsListResponse).Data) != 1 {
		t.Fatalf("items list = %+v", itemsList)
	}
	turnsList := router.Handle(requestWithParams(t, IntID(3), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: "thread-paginated"}))
	if turnsList.Error != nil {
		t.Fatalf("turns list = %+v", turnsList)
	}
	resume := router.Handle(requestWithParams(t, IntID(4), MethodThreadResume, ThreadResumeParams{ThreadID: "thread-paginated"}))
	if resume.Error != nil || resume.Result.(*ThreadResumeResponse).Thread.HistoryMode != ThreadHistoryPaginated {
		t.Fatalf("resume = %+v", resume)
	}
	fork := router.Handle(requestWithParams(t, IntID(5), MethodThreadFork, ThreadForkParams{ThreadID: "thread-paginated"}))
	if fork.Error != nil || fork.Result.(*ThreadForkResponse).Thread.HistoryMode != ThreadHistoryPaginated {
		t.Fatalf("fork = %+v", fork)
	}
	path := recorder.Path()
	forkPath := router.Handle(requestWithParams(t, IntID(6), MethodThreadFork, ThreadForkParams{Path: &path}))
	if forkPath.Error != nil || forkPath.Result.(*ThreadForkResponse).Thread.HistoryMode != ThreadHistoryPaginated {
		t.Fatalf("path fork = %+v", forkPath)
	}

	runtimeRouter := NewRuntimeRouter(RuntimeServices{ThreadRouter: router, ThreadStatus: NewThreadStatusManager()})
	runtimeTurnsList := runtimeRouter.Handle(requestWithParams(t, IntID(7), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: "thread-paginated"}))
	if runtimeTurnsList.Error != nil {
		t.Fatalf("runtime turns list = %+v", runtimeTurnsList)
	}
	runtimeFork := runtimeRouter.Handle(requestWithParams(t, IntID(8), MethodThreadFork, ThreadForkParams{
		ThreadID:  "thread-paginated",
		Ephemeral: true,
	}))
	if runtimeFork.Error == nil || runtimeFork.Error.Code != -32600 || runtimeFork.Error.Message != "ephemeral paginated thread/fork requires `excludeTurns: true`" {
		t.Fatalf("runtime fork = %+v", runtimeFork)
	}
	runtimeFork = runtimeRouter.Handle(requestWithParams(t, IntID(9), MethodThreadFork, ThreadForkParams{
		ThreadID:     "thread-paginated",
		Ephemeral:    true,
		ExcludeTurns: true,
	}))
	if runtimeFork.Error != nil || runtimeFork.Result.(*ThreadForkResponse).Thread.HistoryMode != ThreadHistoryPaginated || len(runtimeFork.Result.(*ThreadForkResponse).Thread.Turns) != 0 {
		t.Fatalf("runtime fork with excludeTurns = %+v", runtimeFork)
	}
}

func TestRouterThreadResumeReturnsStableHistoryHeadCursorsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-head-cursors", fixedTime())

	resume := func(id int64) *ThreadResumeResponse {
		response := router.Handle(requestWithParams(t, IntID(id), MethodThreadResume, ThreadResumeParams{ThreadID: "thread-head-cursors", ExcludeTurns: true}))
		if response.Error != nil {
			t.Fatalf("resume error = %+v", response.Error)
		}
		return response.Result.(*ThreadResumeResponse)
	}
	first := resume(1)
	second := resume(2)
	if first.TurnsBackwardsCursor == nil || first.ItemsBackwardsCursor == nil {
		t.Fatalf("resume cursors = turns %v items %v", first.TurnsBackwardsCursor, first.ItemsBackwardsCursor)
	}
	if stringPtrValue(first.TurnsBackwardsCursor) != stringPtrValue(second.TurnsBackwardsCursor) || stringPtrValue(first.ItemsBackwardsCursor) != stringPtrValue(second.ItemsBackwardsCursor) {
		t.Fatalf("unstable cursors: first=%#v second=%#v", first, second)
	}
	turnsResponse := router.Handle(requestWithParams(t, IntID(3), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: "thread-head-cursors", Cursor: first.TurnsBackwardsCursor, SortDirection: SortDesc}))
	if turnsResponse.Error != nil || len(turnsResponse.Result.(*TurnsPage).Data) != 2 {
		t.Fatalf("turns page = %+v", turnsResponse)
	}
	itemsResponse := router.Handle(requestWithParams(t, IntID(4), MethodThreadItemsList, ThreadItemsListParams{ThreadID: "thread-head-cursors", Cursor: first.ItemsBackwardsCursor, SortDirection: SortDesc}))
	if itemsResponse.Error != nil || len(itemsResponse.Result.(*ThreadItemsListResponse).Data) != 2 {
		t.Fatalf("items page = %+v", itemsResponse)
	}
}

func TestRouterResumeEmptyPathUsesThreadID(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())
	emptyPath := ""

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: "thread-a",
		Path:     &emptyPath,
	}))
	if response.Error != nil {
		t.Fatalf("resume error: %+v", response.Error)
	}
	resume := response.Result.(*ThreadResumeResponse)
	if resume.Thread.ID != "thread-a" {
		t.Fatalf("resume thread ID = %q, want thread-a", resume.Thread.ID)
	}
}

func TestRouterResumeHistoryCreatesPersistentThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })
	model := "mock-model"
	provider := "mock_provider"
	history := []ThreadResumeHistoryItem{
		ThreadResumeHistoryItem(json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello from history"}]}`)),
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID:      "ignored-thread-id",
		History:       history,
		Model:         &model,
		ModelProvider: &provider,
	}))
	if response.Error != nil {
		t.Fatalf("resume error: %+v", response.Error)
	}
	resume := response.Result.(*ThreadResumeResponse)
	if resume.Thread == nil || !strings.HasPrefix(resume.Thread.ID, "thread-history-") {
		t.Fatalf("resume thread = %+v", resume.Thread)
	}
	if resume.Thread.Preview != "Hello from history" {
		t.Fatalf("preview = %q", resume.Thread.Preview)
	}
	if resume.Thread.Ephemeral || resume.Thread.Path == nil || *resume.Thread.Path == "" {
		t.Fatalf("ephemeral/path = %v/%+v", resume.Thread.Ephemeral, resume.Thread.Path)
	}
	if len(resume.Thread.Turns) != 1 || len(resume.Thread.Turns[0].Items) != 1 {
		t.Fatalf("turns = %+v", resume.Thread.Turns)
	}
	if resume.ModelProvider != provider {
		t.Fatalf("modelProvider = %q, want %q", resume.ModelProvider, provider)
	}
	record, err := store.Load(session.ThreadID(resume.Thread.ID))
	if err != nil {
		t.Fatalf("Load(history resume) error = %v", err)
	}
	if record.Metadata.Extra["history_resume"] != true || record.Metadata.Extra["ephemeral"] == true {
		t.Fatalf("history resume metadata = %#v", record.Metadata.Extra)
	}
}

func TestRouterResumeRejectsEmptyHistory(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, map[string]any{
		"threadId": "thread-a",
		"history":  []any{},
	}))
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "history must not be empty" {
		t.Fatalf("empty history response = %+v", response)
	}
}

func TestRouterResumeHistoryInitialTurnsPageWithExcludeTurns(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })
	history := []ThreadResumeHistoryItem{
		ThreadResumeHistoryItem(json.RawMessage(`{"id":"first","type":"message","role":"user","content":[{"type":"input_text","text":"First from history"}]}`)),
		ThreadResumeHistoryItem(json.RawMessage(`{"id":"second","type":"message","role":"user","content":[{"type":"input_text","text":"Second from history"}]}`)),
	}
	limit := 1

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID:     "ignored-thread-id",
		History:      history,
		ExcludeTurns: true,
		InitialTurnsPage: &ThreadInitialPageParams{
			Limit:     &limit,
			ItemsView: TurnItemsFull,
		},
	}))
	if response.Error != nil {
		t.Fatalf("resume error: %+v", response.Error)
	}
	resume := response.Result.(*ThreadResumeResponse)
	if len(resume.Thread.Turns) != 0 {
		t.Fatalf("thread turns should be excluded: %+v", resume.Thread.Turns)
	}
	if resume.InitialTurnsPage == nil || len(resume.InitialTurnsPage.Data) != 1 {
		t.Fatalf("initial turns page = %+v", resume.InitialTurnsPage)
	}
	turn := resume.InitialTurnsPage.Data[0]
	if turn.ID != "turn-second" || turn.ItemsView != TurnItemsFull || len(turn.Items) != 1 || turn.Items[0].Text != "Second from history" {
		t.Fatalf("initial turn = %+v", turn)
	}
}

func TestRouterThreadResumeInitialTurnsPageMatchesRequestedTurnsListPage(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	threadID := "thread-initial-page"
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		Preview:   "first",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           "D:/repo",
			ModelProvider: "openai",
			Source:        string(SessionSourceAppServer),
			HistoryMode:   string(ThreadHistoryLegacy),
		},
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "first", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "user-2", Type: "message", Role: "user", Text: "second", CreatedAt: now.Add(time.Minute), Metadata: map[string]any{"turnId": "turn-2"}},
			{ID: "user-3", Type: "message", Role: "user", Text: "third", CreatedAt: now.Add(2 * time.Minute), Metadata: map[string]any{"turnId": "turn-3"}},
		},
	}); err != nil {
		t.Fatalf("Create record error: %v", err)
	}
	limit := 2
	expectedResponse := router.Handle(requestWithParams(t, IntID(1), MethodThreadTurnsList, ThreadTurnsListParams{
		ThreadID:      threadID,
		Limit:         &limit,
		SortDirection: SortAsc,
		ItemsView:     TurnItemsNotLoaded,
	}))
	if expectedResponse.Error != nil {
		t.Fatalf("thread/turns/list error: %+v", expectedResponse.Error)
	}
	expected := expectedResponse.Result.(*TurnsPage)

	resumeResponse := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID:     threadID,
		ExcludeTurns: true,
		InitialTurnsPage: &ThreadInitialPageParams{
			Limit:         &limit,
			SortDirection: SortAsc,
			ItemsView:     TurnItemsNotLoaded,
		},
	}))
	if resumeResponse.Error != nil {
		t.Fatalf("thread/resume error: %+v", resumeResponse.Error)
	}
	resume := resumeResponse.Result.(*ThreadResumeResponse)
	if len(resume.Thread.Turns) != 0 {
		t.Fatalf("resume turns = %+v, want excluded", resume.Thread.Turns)
	}
	if !reflect.DeepEqual(resume.InitialTurnsPage, expected) {
		t.Fatalf("initialTurnsPage = %+v, want %+v", resume.InitialTurnsPage, expected)
	}
}

func TestRouterThreadTurnsListSupportsRequestedItemsView(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime() })
	threadID := "thread-items-view"
	now := fixedTime()
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		Title:     "items view",
		Preview:   "items view",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           "D:/repo",
			ModelProvider: "openai",
			Source:        "cli",
		},
		Items: []session.Item{
			{ID: "first", Type: "message", Role: "user", Text: "First from history", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-items"}},
			{ID: "final", Type: "message", Role: "assistant", Text: "Final answer", CreatedAt: now.Add(time.Second), Metadata: map[string]any{"turnId": "turn-items", "phase": "final_answer"}},
			{ID: "commentary", Type: "message", Role: "assistant", Text: "Late commentary", CreatedAt: now.Add(2 * time.Second), Metadata: map[string]any{"turnId": "turn-items", "phase": "commentary"}},
		},
	}); err != nil {
		t.Fatalf("Create record error: %v", err)
	}
	limit := 1
	readTurn := func(id int64, view TurnItemsView) Turn {
		t.Helper()
		response := router.Handle(requestWithParams(t, IntID(id), MethodThreadTurnsList, ThreadTurnsListParams{
			ThreadID:  threadID,
			Limit:     &limit,
			ItemsView: view,
		}))
		if response.Error != nil {
			t.Fatalf("thread/turns/list %s error: %+v", view, response.Error)
		}
		page := response.Result.(*TurnsPage)
		if len(page.Data) != 1 {
			t.Fatalf("thread/turns/list %s page = %+v", view, page)
		}
		return page.Data[0]
	}

	full := readTurn(2, TurnItemsFull)
	if full.ItemsView != TurnItemsFull || len(full.Items) != 3 {
		t.Fatalf("full turn = %+v", full)
	}
	summary := readTurn(3, TurnItemsSummary)
	if summary.ItemsView != TurnItemsSummary || len(summary.Items) != 2 || summary.Items[0].Text != "First from history" || summary.Items[1].Text != "Final answer" {
		t.Fatalf("summary turn = %+v", summary)
	}
	notLoaded := readTurn(4, TurnItemsNotLoaded)
	if notLoaded.ItemsView != TurnItemsNotLoaded || len(notLoaded.Items) != 0 || notLoaded.ID != full.ID || notLoaded.Status != full.Status || !sameInt64Ptr(notLoaded.StartedAt, full.StartedAt) || !sameInt64Ptr(notLoaded.CompletedAt, full.CompletedAt) {
		t.Fatalf("notLoaded turn = %+v full=%+v", notLoaded, full)
	}
}

func TestSummarizeTurnItemsHonorsFinalAnswerBoundaryLikeRust(t *testing.T) {
	items := []ThreadItem{
		{ID: "user", Type: "userMessage", Text: "question"},
		{ID: "final", Type: "agentMessage", Text: "answer", Data: map[string]any{"phase": "final_answer"}},
		{ID: "commentary", Type: "agentMessage", Text: "late note", Data: map[string]any{"phase": "commentary"}},
	}
	summary := summarizeTurnItems(items, TurnStatusCompleted)
	if len(summary) != 2 || summary[1].ID != "final" {
		t.Fatalf("summary = %+v", summary)
	}
	commentaryOnly := summarizeTurnItems(items[2:], TurnStatusCompleted)
	if len(commentaryOnly) != 0 {
		t.Fatalf("commentary-only summary = %+v", commentaryOnly)
	}
	legacyRunning := summarizeTurnItems([]ThreadItem{{ID: "legacy", Type: "agentMessage", Text: "unphased"}}, TurnStatusInProgress)
	if len(legacyRunning) != 0 {
		t.Fatalf("running legacy summary crossed final boundary = %+v", legacyRunning)
	}
	legacyCompleted := summarizeTurnItems([]ThreadItem{{ID: "legacy", Type: "agentMessage", Text: "unphased"}}, TurnStatusCompleted)
	if len(legacyCompleted) != 1 || legacyCompleted[0].ID != "legacy" {
		t.Fatalf("completed legacy summary = %+v", legacyCompleted)
	}
}

func TestRuntimeRouterThreadResumeAndReadInterruptIncompleteRolloutTurnWhenIdleLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	runtimeRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: router,
		ThreadStatus: NewThreadStatusManager(),
	})
	now := fixedTime()
	threadID := "thread-incomplete-rollout"
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     store.Root(),
		SessionID:     threadID,
		ThreadID:      threadID,
		Source:        string(SessionSourceCli),
		CWD:           "/repo",
		ModelProvider: "openai",
		HistoryMode:   string(ThreadHistoryLegacy),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "message", Role: "user", Text: "Saved user message"}); err != nil {
		t.Fatalf("AppendItem(user) error = %v", err)
	}
	const incompleteTurnID = "incomplete-turn"
	if err := recorder.AppendTurnStarted(incompleteTurnID, now.Add(time.Minute)); err != nil {
		t.Fatalf("AppendTurnStarted() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "assistant-incomplete", Type: "agent_message", Role: "assistant", Text: "Still running"}); err != nil {
		t.Fatalf("AppendItem(agent) error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	assertInterruptedTail := func(label string, thread *Thread) {
		t.Helper()
		if thread == nil || thread.Status.Type != "idle" || len(thread.Turns) != 2 {
			t.Fatalf("%s thread = %+v, want idle with two turns", label, thread)
		}
		if thread.Turns[0].Status != TurnStatusCompleted {
			t.Fatalf("%s first turn = %+v, want completed", label, thread.Turns[0])
		}
		if tail := thread.Turns[1]; tail.ID != incompleteTurnID || tail.Status != TurnStatusInterrupted {
			t.Fatalf("%s tail turn = %+v, want interrupted %s", label, tail, incompleteTurnID)
		}
	}

	resume := runtimeRouter.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{ThreadID: threadID}))
	if resume.Error != nil {
		t.Fatalf("thread/resume error: %+v", resume.Error)
	}
	assertInterruptedTail("resume", resume.Result.(*ThreadResumeResponse).Thread)

	resumeAgain := runtimeRouter.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: threadID}))
	if resumeAgain.Error != nil {
		t.Fatalf("second thread/resume error: %+v", resumeAgain.Error)
	}
	assertInterruptedTail("second resume", resumeAgain.Result.(*ThreadResumeResponse).Thread)

	read := runtimeRouter.Handle(requestWithParams(t, IntID(3), MethodThreadRead, ThreadReadParams{
		ThreadID:     threadID,
		IncludeTurns: true,
	}))
	if read.Error != nil {
		t.Fatalf("thread/read error: %+v", read.Error)
	}
	assertInterruptedTail("read", read.Result.(*ThreadReadResponse).Thread)
}

func TestRouterFork(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime().Add(time.Hour) })
	createRecord(t, store, "thread-a", fixedTime())
	sourceRecord, err := store.Read("thread-a", true, true)
	if err != nil {
		t.Fatalf("Read source error: %v", err)
	}
	sourceRecord.Metadata.Source = string(SessionSourceVsCode)
	if err := store.Save(sourceRecord); err != nil {
		t.Fatalf("Save source error: %v", err)
	}

	lastN := 1
	cwd := "D:/forked"
	model := "mock-model"
	provider := "mock_provider"
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID:      "thread-a",
		HistoryMode:   session.ForkLastN,
		LastN:         lastN,
		CWD:           &cwd,
		Model:         &model,
		ModelProvider: &provider,
	}))
	if response.Error != nil {
		t.Fatalf("fork error: %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse)
	if forked.Thread.ForkedFromID == nil || *forked.Thread.ForkedFromID != "thread-a" {
		t.Fatalf("fork metadata = %+v", forked.Thread)
	}
	if forked.Thread.SessionID != forked.Thread.ID {
		t.Fatalf("fork SessionID = %q, want new thread id %q", forked.Thread.SessionID, forked.Thread.ID)
	}
	if forked.Thread.Name != nil {
		t.Fatalf("fork name = %+v, want nil", forked.Thread.Name)
	}
	if forked.Thread.Source != SessionSourceVsCode {
		t.Fatalf("fork source = %q", forked.Thread.Source)
	}
	if forked.Thread.Status.Type != "idle" {
		t.Fatalf("fork status = %#v", forked.Thread.Status)
	}
	if len(forked.Thread.Turns) != 1 {
		t.Fatalf("fork turns = %+v", forked.Thread.Turns)
	}
	if forked.CWD != cwd || forked.Model != model || forked.ModelProvider != provider {
		t.Fatalf("fork response overrides = cwd:%q model:%q provider:%q", forked.CWD, forked.Model, forked.ModelProvider)
	}
	if forked.Thread.CWD != cwd || forked.Thread.ModelProvider != provider {
		t.Fatalf("fork thread overrides = %+v", forked.Thread)
	}
	if _, err := rollout.FindThreadPath(store.Root(), forked.Thread.ID, false); err != nil {
		t.Fatalf("fork rollout path error: %v", err)
	}
	forkRecord, err := store.Read(session.ThreadID(forked.Thread.ID), false, false)
	if err != nil {
		t.Fatalf("read fork record error: %v", err)
	}
	if forkRecord.Title != "" {
		t.Fatalf("fork record title = %q, want empty", forkRecord.Title)
	}
}

func TestRouterForkInheritsExplicitThreadName(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())

	named := router.Handle(requestWithParams(t, IntID(1), MethodThreadSetName, ThreadSetNameParams{
		ThreadID: "thread-a",
		Name:     "Renamed parent thread",
	}))
	if named.Error != nil {
		t.Fatalf("set name error: %+v", named.Error)
	}
	fork := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{ThreadID: "thread-a"}))
	if fork.Error != nil {
		t.Fatalf("fork error: %+v", fork.Error)
	}
	thread := fork.Result.(*ThreadForkResponse).Thread
	if thread.Name == nil || *thread.Name != "Renamed parent thread" {
		t.Fatalf("fork name = %+v", thread.Name)
	}
	list := router.Handle(requestWithParams(t, IntID(3), MethodThreadList, ThreadListParams{}))
	if list.Error != nil {
		t.Fatalf("list error: %+v", list.Error)
	}
	var listed *Thread
	for i := range list.Result.(*ThreadListResponse).Data {
		if list.Result.(*ThreadListResponse).Data[i].ID == thread.ID {
			listed = &list.Result.(*ThreadListResponse).Data[i]
			break
		}
	}
	if listed == nil || listed.Name == nil || *listed.Name != "Renamed parent thread" {
		t.Fatalf("listed fork = %+v", listed)
	}
}

func TestRouterForkPreservesRolloutTurnSnapshots(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime().Add(time.Hour) })
	startedAt := fixedTime().Unix()
	completedAt := fixedTime().Add(5 * time.Second).Unix()
	durationMS := int64(5000)
	source := &session.Record{
		ID:        "thread-a",
		SessionID: "thread-a",
		Preview:   "interrupted",
		CreatedAt: fixedTime(),
		UpdatedAt: fixedTime(),
		RecencyAt: fixedTime(),
		Metadata: session.Metadata{
			Source:      string(SessionSourceAppServer),
			HistoryMode: string(ThreadHistoryLegacy),
			RolloutTurns: []session.TurnSnapshot{
				{ID: "turn-1", Status: string(TurnStatusInterrupted), StartedAt: &startedAt, CompletedAt: &completedAt, DurationMS: &durationMS},
			},
		},
		Items: []session.Item{
			{ID: "item-1", Type: "message", Role: "user", Text: "interrupted", CreatedAt: fixedTime(), Metadata: map[string]any{"turnId": "turn-1"}},
		},
	}
	if err := store.Save(source); err != nil {
		t.Fatalf("Save(source) error = %v", err)
	}
	if err := router.createThreadRollout(source, fixedTime()); err != nil {
		t.Fatalf("create source rollout error: %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{ThreadID: "thread-a"}))
	if response.Error != nil {
		t.Fatalf("fork error: %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse)
	if len(forked.Thread.Turns) != 1 || forked.Thread.Turns[0].Status != TurnStatusInterrupted {
		t.Fatalf("fork turns = %+v", forked.Thread.Turns)
	}
	if forked.Thread.Path == nil {
		t.Fatalf("fork path is nil")
	}
	fromRollout, err := rollout.RecordFromPath(*forked.Thread.Path, false)
	if err != nil {
		t.Fatalf("RecordFromPath(fork) error = %v", err)
	}
	if len(fromRollout.Metadata.RolloutTurns) != 1 || fromRollout.Metadata.RolloutTurns[0].Status != string(TurnStatusInterrupted) {
		t.Fatalf("fork rollout turns = %#v", fromRollout.Metadata.RolloutTurns)
	}
}

func TestRouterForkFromPath(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime().Add(time.Hour) })
	createRecord(t, store, "thread-a", fixedTime())
	if err := router.createThreadRollout(&session.Record{
		ID:        "thread-a",
		SessionID: "thread-a",
		Metadata:  session.Metadata{ModelProvider: "openai"},
		Items: []session.Item{{
			ID:        "path-item-1",
			Type:      "message",
			Role:      "user",
			Text:      "from explicit path",
			CreatedAt: fixedTime(),
			Metadata:  map[string]any{"turnId": "turn-path"},
		}},
	}, fixedTime()); err != nil {
		t.Fatalf("create rollout error: %v", err)
	}
	path, err := rollout.FindThreadPath(store.Root(), "thread-a", false)
	if err != nil {
		t.Fatalf("source rollout path error: %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		Path: &path,
	}))
	if response.Error != nil {
		t.Fatalf("fork error: %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse)
	if forked.Thread.ForkedFromID == nil || *forked.Thread.ForkedFromID != "thread-a" {
		t.Fatalf("fork metadata = %+v", forked.Thread)
	}
	if forked.Thread.SessionID != forked.Thread.ID {
		t.Fatalf("fork SessionID = %q, want new thread id %q", forked.Thread.SessionID, forked.Thread.ID)
	}
	if len(forked.Thread.Turns) != 1 || len(forked.Thread.Turns[0].Items) != 1 || forked.Thread.Turns[0].Items[0].Text != "from explicit path" {
		t.Fatalf("fork turns = %+v", forked.Thread.Turns)
	}
	if _, err := rollout.FindThreadPath(store.Root(), forked.Thread.ID, false); err != nil {
		t.Fatalf("fork rollout path error: %v", err)
	}
}

func TestRouterForkFallsBackToRolloutSource(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime().Add(time.Hour) })
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     store.Root(),
		SessionID:     "session-rollout",
		ThreadID:      "thread-rollout",
		Source:        "cli",
		CWD:           "/repo",
		ModelProvider: "openai",
		HistoryMode:   "legacy",
		Now:           fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "user-1", Type: "message", Role: "user", Text: "from rollout"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID: "thread-rollout",
	}))
	if response.Error != nil {
		t.Fatalf("fork error: %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse).Thread
	if forked.ForkedFromID == nil || *forked.ForkedFromID != "thread-rollout" {
		t.Fatalf("fork lineage = %+v", forked)
	}
	if forked.Source != SessionSourceCli {
		t.Fatalf("fork source = %q", forked.Source)
	}
	if len(forked.Turns) != 1 || forked.Turns[0].Items[0].Text != "from rollout" || forked.Turns[0].Status != TurnStatusInterrupted {
		t.Fatalf("fork turns = %+v", forked.Turns)
	}
	if _, err := store.Load(session.ThreadID(forked.ID)); err != nil {
		t.Fatalf("Load(fork) error = %v", err)
	}
	if forked.Path == nil {
		t.Fatalf("fork path is nil")
	}
	fromRollout, err := rollout.RecordFromPath(*forked.Path, false)
	if err != nil {
		t.Fatalf("RecordFromPath(fork) error = %v", err)
	}
	if len(fromRollout.Metadata.RolloutTurns) != 1 || fromRollout.Metadata.RolloutTurns[0].Status != string(TurnStatusInterrupted) {
		t.Fatalf("fork rollout turns = %#v", fromRollout.Metadata.RolloutTurns)
	}
}

func TestRouterForkAtLastTurnIDKeepsTerminalPrefix(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	if err := store.Save(&session.Record{
		ID:        "thread-a",
		SessionID: "thread-a",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{RolloutTurns: []session.TurnSnapshot{
			{ID: "turn-1", Status: string(TurnStatusCompleted)},
			{ID: "turn-2", Status: string(TurnStatusCompleted)},
			{ID: "turn-3", Status: string(TurnStatusInProgress)},
		}},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer 1", CreatedAt: now.Add(time.Second), Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "u2", Type: "message", Role: "user", Text: "second", CreatedAt: now.Add(2 * time.Second), Metadata: map[string]any{"turnId": "turn-2"}},
			{ID: "a2", Type: "agent_message", Role: "assistant", Text: "answer 2", CreatedAt: now.Add(3 * time.Second), Metadata: map[string]any{"turnId": "turn-2"}},
			{ID: "u3", Type: "message", Role: "user", Text: "third", CreatedAt: now.Add(4 * time.Second), Metadata: map[string]any{"turnId": "turn-3"}},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID:   "thread-a",
		LastTurnID: "turn-2",
	}))
	if response.Error != nil {
		t.Fatalf("fork error: %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse).Thread
	if len(forked.Turns) != 2 {
		t.Fatalf("fork turns = %+v", forked.Turns)
	}
	if forked.Turns[0].ID != "turn-1" || forked.Turns[1].ID != "turn-2" {
		t.Fatalf("fork turn IDs = %+v", forked.Turns)
	}
	if forked.Turns[0].Status != TurnStatusCompleted || forked.Turns[1].Status != TurnStatusCompleted {
		t.Fatalf("fork turn statuses = %+v", forked.Turns)
	}
	record, err := store.Read(session.ThreadID(forked.ID), true, true)
	if err != nil {
		t.Fatalf("Read forked error: %v", err)
	}
	if len(record.Items) != 4 || record.Items[len(record.Items)-1].Text != "answer 2" {
		t.Fatalf("forked items = %+v", record.Items)
	}

	missing := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:   "thread-a",
		LastTurnID: "turn-missing",
	}))
	if missing.Error == nil || missing.Error.Code != -32600 || missing.Error.Message != "lastTurnId 'turn-missing' was not found in the source thread" {
		t.Fatalf("missing lastTurnId response = %+v", missing)
	}

	inProgress := router.Handle(requestWithParams(t, IntID(3), MethodThreadFork, ThreadForkParams{
		ThreadID:   "thread-a",
		LastTurnID: "turn-3",
	}))
	if inProgress.Error == nil || inProgress.Error.Code != -32600 || inProgress.Error.Message != "lastTurnId 'turn-3' identifies an in-progress turn" {
		t.Fatalf("in-progress lastTurnId response = %+v", inProgress)
	}

	if err := store.Save(&session.Record{
		ID:        "thread-legacy",
		SessionID: "thread-legacy",
		Items:     []session.Item{{ID: "legacy-item", Type: "message", Role: "user", Text: "legacy"}},
	}); err != nil {
		t.Fatalf("Save legacy thread error: %v", err)
	}
	synthetic := router.Handle(requestWithParams(t, IntID(4), MethodThreadFork, ThreadForkParams{
		ThreadID:   "thread-legacy",
		LastTurnID: "turn-1",
	}))
	if synthetic.Error == nil || synthetic.Error.Code != -32600 || synthetic.Error.Message != "lastTurnId 'turn-1' is not a persisted canonical turn in the source thread" {
		t.Fatalf("synthetic lastTurnId response = %+v", synthetic)
	}
}

func TestRouterForkBeforeTurnIDKeepsSourcePreservingPrefixLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	source := &session.Record{ID: "thread-before", SessionID: "thread-before", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{RolloutTurns: []session.TurnSnapshot{{ID: "turn-1", Status: string(TurnStatusCompleted)}, {ID: "turn-2", Status: string(TurnStatusCompleted)}}}, Items: []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "retained prompt", Metadata: map[string]any{"turnId": "turn-1"}},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "retained answer", Metadata: map[string]any{"turnId": "turn-1"}},
		{ID: "u2", Type: "message", Role: "user", Text: "selected prompt", Metadata: map[string]any{"turnId": "turn-2"}},
	}}
	if err := store.Save(source); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{ThreadID: "thread-before", BeforeTurnID: "turn-2"}))
	if response.Error != nil {
		t.Fatalf("fork error = %+v", response.Error)
	}
	forked := response.Result.(*ThreadForkResponse).Thread
	if len(forked.Turns) != 1 || forked.Turns[0].ID != "turn-1" {
		t.Fatalf("fork turns = %+v", forked.Turns)
	}
	loaded, err := store.Read("thread-before", true, true)
	if err != nil {
		t.Fatalf("Read(source) = %v", err)
	}
	if len(loaded.Items) != 3 {
		t.Fatalf("source items = %+v", loaded.Items)
	}
	both := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{ThreadID: "thread-before", LastTurnID: "turn-1", BeforeTurnID: "turn-2"}))
	if both.Error == nil || both.Error.Code != -32600 || both.Error.Message != "`beforeTurnId` cannot be combined with `lastTurnId`" {
		t.Fatalf("both response = %+v", both)
	}
}

func TestRouterForkEphemeralDoesNotPersistOrCreateRollout(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createRecord(t, store, "thread-a", fixedTime())

	source := ThreadSourceUser
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID:     "thread-a",
		ThreadSource: &source,
		Ephemeral:    true,
	}))
	if response.Error != nil {
		t.Fatalf("fork error: %+v", response.Error)
	}
	thread := response.Result.(*ThreadForkResponse).Thread
	if !thread.Ephemeral {
		t.Fatalf("fork thread Ephemeral = false")
	}
	if thread.Path != nil {
		t.Fatalf("fork thread Path = %v, want nil", *thread.Path)
	}
	if len(thread.Turns) == 0 {
		t.Fatalf("fork turns empty")
	}
	if thread.ThreadSource == nil || *thread.ThreadSource != ThreadSourceUser {
		t.Fatalf("fork thread source = %+v", thread.ThreadSource)
	}
	if _, err := store.Load(session.ThreadID(thread.ID)); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("Load(ephemeral fork) error = %v, want ErrThreadNotFound", err)
	}
	if _, err := rollout.FindThreadPath(store.Root(), thread.ID, false); err == nil {
		t.Fatalf("ephemeral fork unexpectedly created rollout")
	}
}

func TestRouterEphemeralPaginatedForkRequiresExcludeTurns(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	source := &session.Record{
		ID:        "thread-paginated-ephemeral",
		SessionID: "thread-paginated-ephemeral",
		CreatedAt: fixedTime(),
		UpdatedAt: fixedTime(),
		RecencyAt: fixedTime(),
		Metadata: session.Metadata{
			HistoryMode:  string(ThreadHistoryPaginated),
			RolloutTurns: []session.TurnSnapshot{{ID: "turn-1", Status: string(TurnStatusCompleted)}},
		},
		Items: []session.Item{{ID: "item-1", Type: "message", Role: "user", Text: "source", Metadata: map[string]any{"turnId": "turn-1"}}},
	}
	if err := store.Create(source); err != nil {
		t.Fatalf("Create(source) error = %v", err)
	}

	invalid := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID:  string(source.ID),
		Ephemeral: true,
	}))
	if invalid.Error == nil || invalid.Error.Code != -32600 || invalid.Error.Message != "ephemeral paginated thread/fork requires `excludeTurns: true`" {
		t.Fatalf("invalid fork response = %+v", invalid)
	}
	valid := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:     string(source.ID),
		Ephemeral:    true,
		ExcludeTurns: true,
	}))
	if valid.Error != nil {
		t.Fatalf("valid fork error = %+v", valid.Error)
	}
	forked := valid.Result.(*ThreadForkResponse).Thread
	if !forked.Ephemeral || forked.Path != nil || len(forked.Turns) != 0 {
		t.Fatalf("valid paginated ephemeral fork = %+v", forked)
	}
}

func TestRouterFreshPaginatedThreadAllowsExcludedEphemeralFork(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         t.TempDir(),
		HistoryMode: ThreadHistoryPaginated,
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error = %+v", start.Error)
	}
	source := start.Result.(*ThreadStartResponse).Thread
	if source.Path == nil {
		t.Fatal("thread/start omitted the planned rollout path")
	}
	if _, err := os.Stat(*source.Path); !os.IsNotExist(err) {
		t.Fatalf("fresh paginated source unexpectedly materialized: %v", err)
	}

	fork := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:     source.ID,
		Ephemeral:    true,
		ExcludeTurns: true,
	}))
	if fork.Error != nil {
		t.Fatalf("thread/fork error = %+v", fork.Error)
	}
	forked := fork.Result.(*ThreadForkResponse).Thread
	if !forked.Ephemeral || forked.Path != nil || forked.HistoryMode != ThreadHistoryPaginated || len(forked.Turns) != 0 {
		t.Fatalf("fresh paginated fork = %+v", forked)
	}
}

func TestRouterForkReservesSourceWriterBeforeCreatingTarget(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	source := &session.Record{
		ID:        "thread-fork-writer-conflict",
		SessionID: "thread-fork-writer-conflict",
		CreatedAt: fixedTime(),
		UpdatedAt: fixedTime(),
		RecencyAt: fixedTime(),
		Items:     []session.Item{{ID: "item-1", Type: "message", Role: "user", Text: "source"}},
	}
	if err := store.Create(source); err != nil {
		t.Fatalf("Create(source) error = %v", err)
	}
	competingWriter, err := session.NewStore(root).AcquireWriter(source.ID)
	if err != nil {
		t.Fatalf("AcquireWriter(source) error = %v", err)
	}
	defer func() { _ = competingWriter.Close() }()

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{ThreadID: string(source.ID)}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "already has an active writer") {
		t.Fatalf("fork response = %+v", response)
	}
	records, err := store.AllRecords()
	if err != nil {
		t.Fatalf("AllRecords() error = %v", err)
	}
	if len(records) != 1 || records[0].ID != source.ID {
		t.Fatalf("fork conflict left target side effects: %#v", records)
	}
}

func TestRouterSearchLoadedTurnsRollbackAndInjectItems(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime().Add(10 * time.Second) })
	createRecord(t, store, "thread-a", fixedTime())

	search := router.Handle(requestWithParams(t, IntID(1), MethodThreadSearch, ThreadSearchParams{SearchTerm: "world"}))
	if search.Error != nil {
		t.Fatalf("search error: %+v", search.Error)
	}
	searchResult := search.Result.(*ThreadSearchResponse)
	if len(searchResult.Data) != 1 || searchResult.Data[0].Thread.ID != "thread-a" {
		t.Fatalf("search result = %+v", searchResult)
	}

	loaded := router.Handle(requestWithParams(t, IntID(2), MethodThreadLoadedList, ThreadLoadedListParams{}))
	if loaded.Error != nil {
		t.Fatalf("loaded list error: %+v", loaded.Error)
	}
	loadedResult := loaded.Result.(*ThreadLoadedListResponse)
	if len(loadedResult.Data) != 1 || loadedResult.Data[0] != "thread-a" {
		t.Fatalf("loaded result = %+v", loadedResult)
	}

	turns := router.Handle(requestWithParams(t, IntID(3), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: "thread-a"}))
	if turns.Error != nil {
		t.Fatalf("turns error: %+v", turns.Error)
	}
	if len(turns.Result.(*TurnsPage).Data) != 2 {
		t.Fatalf("turns page = %+v", turns.Result)
	}

	zeroLimit := 0
	items := router.Handle(requestWithParams(t, IntID(30), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "thread-a",
		Limit:    &zeroLimit,
	}))
	if items.Error != nil {
		t.Fatalf("items error: %+v", items.Error)
	}
	itemsPage := items.Result.(*ThreadItemsListResponse)
	if len(itemsPage.Data) != 1 || itemsPage.Data[0].ID != "item-1" || itemsPage.NextCursor == nil || *itemsPage.NextCursor != "1" {
		t.Fatalf("items page = %+v", itemsPage)
	}
	oneLimit := 1
	nextItems := router.Handle(requestWithParams(t, IntID(31), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "thread-a",
		Cursor:   itemsPage.NextCursor,
		Limit:    &oneLimit,
	}))
	if nextItems.Error != nil {
		t.Fatalf("next items error: %+v", nextItems.Error)
	}
	nextItemsPage := nextItems.Result.(*ThreadItemsListResponse)
	if len(nextItemsPage.Data) != 1 || nextItemsPage.Data[0].ID != "item-2" || nextItemsPage.NextCursor != nil {
		t.Fatalf("next items page = %+v", nextItemsPage)
	}

	badItemsCursor := "not-a-number"
	badItemsCursorResponse := router.Handle(requestWithParams(t, IntID(32), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "thread-a",
		Cursor:   &badItemsCursor,
	}))
	if badItemsCursorResponse.Error == nil || badItemsCursorResponse.Error.Code != -32600 || badItemsCursorResponse.Error.Message != "invalid cursor" {
		t.Fatalf("bad items cursor response = %+v", badItemsCursorResponse.Error)
	}
	badTurnsCursor := "not-json"
	badTurnsCursorResponse := router.Handle(requestWithParams(t, IntID(33), MethodThreadTurnsList, ThreadTurnsListParams{
		ThreadID: "thread-a",
		Cursor:   &badTurnsCursor,
	}))
	if badTurnsCursorResponse.Error == nil || badTurnsCursorResponse.Error.Code != -32600 || badTurnsCursorResponse.Error.Message != "invalid cursor: not-json" {
		t.Fatalf("bad turns cursor response = %+v", badTurnsCursorResponse.Error)
	}
	blankTurnsCursor := "  "
	blankTurnsCursorResponse := router.Handle(requestWithParams(t, IntID(35), MethodThreadTurnsList, ThreadTurnsListParams{
		ThreadID: "thread-a",
		Cursor:   &blankTurnsCursor,
	}))
	if blankTurnsCursorResponse.Error == nil || blankTurnsCursorResponse.Error.Code != -32600 || blankTurnsCursorResponse.Error.Message != "invalid cursor: "+blankTurnsCursor {
		t.Fatalf("blank turns cursor response = %+v", blankTurnsCursorResponse.Error)
	}
	missingAnchorCursor := `{"turnId":"turn-missing"}`
	missingAnchorResponse := router.Handle(requestWithParams(t, IntID(36), MethodThreadTurnsList, ThreadTurnsListParams{
		ThreadID: "thread-a",
		Cursor:   &missingAnchorCursor,
	}))
	if missingAnchorResponse.Error == nil || missingAnchorResponse.Error.Code != -32600 || missingAnchorResponse.Error.Message != "invalid cursor: anchor turn is no longer present" {
		t.Fatalf("missing anchor cursor response = %+v", missingAnchorResponse.Error)
	}

	emptyInject := router.Handle(requestWithParams(t, IntID(40), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: "thread-a",
		Items:    []json.RawMessage{},
	}))
	if emptyInject.Error != nil {
		t.Fatalf("empty inject error: %+v", emptyInject.Error)
	}
	beforeInject, err := store.Read("thread-a", true, true)
	if err != nil {
		t.Fatalf("read before inject: %v", err)
	}

	inject := router.Handle(requestWithParams(t, IntID(4), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: "thread-a",
		Items: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"injected"}]}`),
		},
	}))
	if inject.Error != nil {
		t.Fatalf("inject error: %+v", inject.Error)
	}
	record, err := store.Read("thread-a", true, true)
	if err != nil {
		t.Fatalf("read after inject: %v", err)
	}
	if len(record.Items) != len(beforeInject.Items)+1 {
		t.Fatalf("items after inject = %d, want %d", len(record.Items), len(beforeInject.Items)+1)
	}
	if got := record.Items[len(record.Items)-1].Text; got != "injected" {
		t.Fatalf("injected text = %q", got)
	}

	invalidRollback := router.Handle(requestWithParams(t, IntID(50), MethodThreadRollback, ThreadRollbackParams{ThreadID: "thread-a"}))
	if invalidRollback.Error == nil || invalidRollback.Error.Code != -32600 || invalidRollback.Error.Message != "numTurns must be >= 1" {
		t.Fatalf("invalid rollback error = %+v", invalidRollback.Error)
	}

	rollback := router.Handle(requestWithParams(t, IntID(5), MethodThreadRollback, ThreadRollbackParams{ThreadID: "thread-a", NumTurns: 1}))
	if rollback.Error != nil {
		t.Fatalf("rollback error: %+v", rollback.Error)
	}
	rollbackResult := rollback.Result.(*ThreadRollbackResponse)
	if len(rollbackResult.Thread.Turns) != 2 {
		t.Fatalf("rollback turns = %+v", rollbackResult.Thread.Turns)
	}
}

func TestRouterInjectItemsAndRollbackRepairRolloutOnlyThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetClock(func() time.Time { return fixedTime().Add(10 * time.Second) })
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: store.Root(),
		SessionID: "session-rollout",
		ThreadID:  "thread-rollout",
		Source:    "cli",
		CWD:       "/repo",
		Now:       fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	for _, item := range []rollout.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "from rollout", Metadata: map[string]any{"turnId": "turn-1"}},
		{ID: "a1", Type: "message", Role: "assistant", Text: "answer", Metadata: map[string]any{"turnId": "turn-1"}},
	} {
		if err := recorder.AppendItem(item); err != nil {
			t.Fatalf("AppendItem() error = %v", err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	inject := router.Handle(requestWithParams(t, IntID(1), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: "thread-rollout",
		Items: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"injected"}],"turnId":"turn-2"}`),
		},
	}))
	if inject.Error != nil {
		t.Fatalf("inject error: %+v", inject.Error)
	}
	record, err := store.Load("thread-rollout")
	if err != nil {
		t.Fatalf("Load(repaired) error = %v", err)
	}
	if len(record.Items) != 3 || record.Items[2].Text != "injected" {
		t.Fatalf("record items after inject = %+v", record.Items)
	}

	rollback := router.Handle(requestWithParams(t, IntID(2), MethodThreadRollback, ThreadRollbackParams{
		ThreadID: "thread-rollout",
		NumTurns: 1,
	}))
	if rollback.Error != nil {
		t.Fatalf("rollback error: %+v", rollback.Error)
	}
	rollbackResult := rollback.Result.(*ThreadRollbackResponse)
	if len(rollbackResult.Thread.Turns) != 1 {
		t.Fatalf("rollback turns = %+v", rollbackResult.Thread.Turns)
	}
	if rollbackResult.Thread.SessionID != "session-rollout" {
		t.Fatalf("rollback thread SessionID = %q, want session-rollout", rollbackResult.Thread.SessionID)
	}
	rollbackData, err := json.Marshal(rollback.Result)
	if err != nil {
		t.Fatalf("Marshal(thread/rollback result) error = %v", err)
	}
	var rollbackPayload map[string]any
	if err := json.Unmarshal(rollbackData, &rollbackPayload); err != nil {
		t.Fatalf("Unmarshal(thread/rollback result) error = %v", err)
	}
	rollbackThreadPayload, ok := rollbackPayload["thread"].(map[string]any)
	if !ok {
		t.Fatalf("thread/rollback payload thread = %T, want object", rollbackPayload["thread"])
	}
	if value, ok := rollbackThreadPayload["name"]; !ok || value != nil {
		t.Fatalf("thread/rollback payload thread.name = %v, present=%v; want explicit null", value, ok)
	}
	if value, ok := rollbackThreadPayload["sessionId"].(string); !ok || value != "session-rollout" {
		t.Fatalf("thread/rollback payload thread.sessionId = %v, present=%v; want session-rollout", rollbackThreadPayload["sessionId"], ok)
	}
	fromRollout, err := rollout.RecordFromPath(recorder.Path(), false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if len(fromRollout.Items) != 2 || fromRollout.Items[0].Text != "from rollout" {
		t.Fatalf("rollout items after rollback = %+v", fromRollout.Items)
	}
}

func TestRouterUnknownMethod(t *testing.T) {
	router := NewRouter(session.NewStore(t.TempDir()))
	response := router.Handle(&Request{ID: IntID(1), Method: Method("missing/method"), Params: []byte(`{}`)})
	if response.Error == nil || response.Error.Code != -32601 {
		t.Fatalf("expected unknown method, got %+v", response.Error)
	}
}

func TestRouterInjectItemsRejectsRemoteImageURLs(t *testing.T) {
	router := NewRouter(session.NewStore(t.TempDir()))
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: "thread-1",
		Items: []json.RawMessage{
			json.RawMessage(`{"type":"function_call_output","call_id":"call-1","output":{"content":[{"type":"input_image","image_url":"https://example.com/tool.png"}]}}`),
		},
	}))
	if response.Error == nil {
		t.Fatalf("expected remote image error, got %+v", response)
	}
	if response.Error.Code != -32600 || response.Error.Message != remoteImageURLError {
		t.Fatalf("error = %+v", response.Error)
	}
}

func requestWithParams(t *testing.T, id RequestID, method Method, params any) *Request {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return &Request{JSONRPC: "2.0", ID: id, Method: method, Params: data}
}

func assertPermissionsSandboxConflict(t *testing.T, response *Response) {
	t.Helper()
	if response == nil || response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "`permissions` cannot be combined with `sandbox`" {
		t.Fatalf("permissions/sandbox response = %+v", response)
	}
}

func sameInt64Ptr(left *int64, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func assertThreadPayloadField(t *testing.T, result any, method string, field string, want any) {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal(%s result) error = %v", method, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal(%s result) error = %v", method, err)
	}
	threadPayload, ok := payload["thread"].(map[string]any)
	if !ok {
		t.Fatalf("%s payload thread = %T, want object", method, payload["thread"])
	}
	if got := threadPayload[field]; got != want {
		t.Fatalf("%s payload thread.%s = %v, want %v", method, field, got, want)
	}
}

func assertJSONPayload(t *testing.T, value any, label string, want map[string]any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(%s) error = %v", label, err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", label, err)
	}
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Fatalf("%s[%s] = %v, want %v", label, key, got[key], wantValue)
		}
	}
}

func assertRedactedResumeTurn(t *testing.T, turn Turn) {
	t.Helper()
	mcpItem := threadItemByTypeForTest(turn.Items, "mcpToolCall")
	if mcpItem == nil {
		t.Fatalf("redacted turn missing mcpToolCall: %+v", turn.Items)
	}
	if got := mcpItem.Data["arguments"]; got != RedactedThreadResumePayload {
		t.Fatalf("redacted mcp arguments = %v, want %q", got, RedactedThreadResumePayload)
	}
	result, ok := mcpItem.Data["result"].(map[string]any)
	if !ok {
		t.Fatalf("redacted mcp result = %T, want object", mcpItem.Data["result"])
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("redacted mcp result content = %#v", result["content"])
	}
	text, _ := content[0].(map[string]any)
	if text["text"] != RedactedThreadResumePayload {
		t.Fatalf("redacted mcp result text = %#v, want %q", text, RedactedThreadResumePayload)
	}
	if _, ok := result["structuredContent"]; ok {
		t.Fatalf("redacted mcp structuredContent leaked: %#v", result)
	}
	if _, ok := result["_meta"]; ok {
		t.Fatalf("redacted mcp _meta leaked: %#v", result)
	}
	if imageItem := threadItemByTypeForTest(turn.Items, "imageGeneration"); imageItem != nil {
		t.Fatalf("redacted turn leaked imageGeneration item: %+v", imageItem)
	}
}

func assertUnredactedResumeTurn(t *testing.T, turn Turn) {
	t.Helper()
	mcpItem := threadItemByTypeForTest(turn.Items, "mcpToolCall")
	if mcpItem == nil {
		t.Fatalf("normal turn missing mcpToolCall: %+v", turn.Items)
	}
	arguments, ok := mcpItem.Data["arguments"].(map[string]any)
	if !ok || arguments["secret"] != "argument" {
		t.Fatalf("normal mcp arguments = %#v", mcpItem.Data["arguments"])
	}
	result, ok := mcpItem.Data["result"].(map[string]any)
	if !ok {
		t.Fatalf("normal mcp result = %T, want object", mcpItem.Data["result"])
	}
	if structured, ok := result["structuredContent"].(map[string]any); !ok || structured["secret"] != "structured" {
		t.Fatalf("normal mcp structuredContent = %#v", result["structuredContent"])
	}
	if meta, ok := result["_meta"].(map[string]any); !ok || meta["secret"] != "meta" {
		t.Fatalf("normal mcp _meta = %#v", result["_meta"])
	}
	if imageItem := threadItemByTypeForTest(turn.Items, "imageGeneration"); imageItem == nil {
		t.Fatalf("normal turn missing imageGeneration: %+v", turn.Items)
	}
}

func threadItemByTypeForTest(items []ThreadItem, itemType string) *ThreadItem {
	for i := range items {
		if items[i].Type == itemType {
			return &items[i]
		}
	}
	return nil
}

func resumeTurnWithItemTypeForTest(t *testing.T, turns []Turn, itemType string) Turn {
	t.Helper()
	for _, turn := range turns {
		if threadItemByTypeForTest(turn.Items, itemType) != nil {
			return turn
		}
	}
	t.Fatalf("turn with item type %q missing in %+v", itemType, turns)
	return Turn{}
}

func openRouterTestSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll sqlite dir error = %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("Open sqlite error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func routerTestExecSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("Exec %q error = %v", query, err)
	}
}

func routerTestScalarInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var value int
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("QueryRow %q error = %v", query, err)
	}
	return value
}

func routerTestScalarString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("QueryRow %q error = %v", query, err)
	}
	return value
}

func routerTestScalarNullString(t *testing.T, db *sql.DB, query string, args ...any) sql.NullString {
	t.Helper()
	var value sql.NullString
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("QueryRow %q error = %v", query, err)
	}
	return value
}

func createRecord(t *testing.T, store *session.Store, id session.ThreadID, now time.Time) {
	createRecordWithHistoryMode(t, store, id, now, string(ThreadHistoryLegacy))
}

func createPaginatedRecord(t *testing.T, store *session.Store, id session.ThreadID, now time.Time) {
	createRecordWithHistoryMode(t, store, id, now, string(ThreadHistoryPaginated))
}

func createRecordWithHistoryMode(t *testing.T, store *session.Store, id session.ThreadID, now time.Time, historyMode string) {
	t.Helper()
	err := store.Create(&session.Record{
		ID:        id,
		SessionID: string(id),
		Title:     "title",
		Preview:   "preview",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           "D:/repo",
			ModelProvider: "openai",
			Source:        "cli",
			HistoryMode:   historyMode,
		},
		Items: []session.Item{
			{
				ID:        "item-1",
				Type:      "message",
				Role:      "user",
				Text:      "hello",
				CreatedAt: now,
				Metadata:  map[string]any{"turnId": "turn-1"},
			},
			{
				ID:        "item-2",
				Type:      "message",
				Role:      "assistant",
				Text:      "world",
				CreatedAt: now.Add(time.Second),
				Metadata:  map[string]any{"turnId": "turn-2"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create record: %v", err)
	}
}

// materializeThreadRolloutForTest mirrors Rust's ensure_rollout_materialized:
// it persists the live session metadata without inventing a user message or turn.
func materializeThreadRolloutForTest(t *testing.T, router *Router, store *session.Store, threadID string) {
	t.Helper()
	if router == nil || store == nil {
		t.Fatal("materialize thread rollout: router and store are required")
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("materialize thread rollout read error: %v", err)
	}
	path := router.threadRolloutPath(record)
	if strings.TrimSpace(path) == "" {
		t.Fatalf("materialize thread rollout path is empty for %s", threadID)
	}
	if _, err := os.Stat(path); err == nil {
		return
	} else if !os.IsNotExist(err) {
		t.Fatalf("materialize thread rollout stat error: %v", err)
	}
	now := record.CreatedAt
	if now.IsZero() {
		now = fixedTime()
	}
	if err := router.createThreadRollout(record, now); err != nil {
		t.Fatalf("materialize thread rollout create error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("materialized thread rollout missing at %q: %v", path, err)
	}
}

func TestRouterPaginatedWriterOwnershipBlocksCompetingResumeArchiveAndDelete(t *testing.T) {
	root := t.TempDir()
	owner := NewRouter(session.NewStore(root))
	contender := NewRouter(session.NewStore(root))
	t.Cleanup(func() { _ = contender.Close() })
	t.Cleanup(func() { _ = owner.Close() })

	start := owner.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         "D:/repo",
		HistoryMode: ThreadHistoryPaginated,
		Prompt:      "owned",
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error = %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, owner, owner.store, threadID)

	for _, request := range []*Request{
		requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: threadID}),
		requestWithParams(t, IntID(3), MethodThreadArchive, ThreadArchiveParams{ThreadID: threadID}),
		requestWithParams(t, IntID(4), MethodThreadDelete, ThreadDeleteParams{ThreadID: threadID}),
	} {
		response := contender.Handle(request)
		if response.Error == nil || response.Error.Code != -32600 || !strings.Contains(response.Error.Message, "already has an active writer") {
			t.Fatalf("%s response = %+v, want ownership conflict", request.Method, response)
		}
		if _, err := owner.store.Read(session.ThreadID(threadID), true, true); err != nil {
			t.Fatalf("owned thread changed after %s conflict: %v", request.Method, err)
		}
	}

	if err := owner.Close(); err != nil {
		t.Fatalf("owner.Close() error = %v", err)
	}
	resume := contender.Handle(requestWithParams(t, IntID(5), MethodThreadResume, ThreadResumeParams{ThreadID: threadID}))
	if resume.Error != nil {
		t.Fatalf("resume after ownership transfer error = %+v", resume.Error)
	}
}
