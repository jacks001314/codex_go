package appserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"codex_go/internal/compact"
	"codex_go/internal/model"
	"codex_go/internal/rollout"
	"codex_go/internal/session"
)

func TestRouterStartReadListAndItems(t *testing.T) {
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
	if start.Thread.ID != "thread-1" || start.Thread.CWD != "D:/repo" {
		t.Fatalf("unexpected started thread: %+v", start.Thread)
	}
	if start.Thread.Status.Type != "idle" {
		t.Fatalf("start thread status = %#v", start.Thread.Status)
	}
	if start.Thread.Path == nil || !strings.HasSuffix(*start.Thread.Path, ".jsonl") {
		t.Fatalf("start thread Path = %+v, want rollout jsonl path", start.Thread.Path)
	}

	readRequest := requestWithParams(t, StringID("read"), MethodThreadRead, ThreadReadParams{
		ThreadID:     "thread-1",
		IncludeTurns: true,
	})
	readResponse := router.Handle(readRequest)
	if readResponse.Error != nil {
		t.Fatalf("read error: %+v", readResponse.Error)
	}
	read := readResponse.Result.(*ThreadReadResponse)
	if len(read.Thread.Turns) != 1 {
		t.Fatalf("turns not included: %+v", read.Thread.Turns)
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
	if len(list.Data) != 1 || list.Data[0].ID != "thread-1" {
		t.Fatalf("unexpected list: %+v", list.Data)
	}

	itemsRequest := requestWithParams(t, IntID(3), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "thread-1",
	})
	itemsResponse := router.Handle(itemsRequest)
	if itemsResponse.Error != nil {
		t.Fatalf("items error: %+v", itemsResponse.Error)
	}
	items := itemsResponse.Result.(*ThreadItemsListResponse)
	if len(items.Data) != 1 || items.Data[0].ID != "item-1" || items.Data[0].Text != "hello" {
		t.Fatalf("items response = %+v", items)
	}
	rolloutPath, err := rollout.FindThreadPath(store.Root(), "thread-1", false)
	if err != nil {
		t.Fatalf("rollout path error: %v", err)
	}
	lines, _, err := rollout.Load(rolloutPath)
	if err != nil {
		t.Fatalf("rollout load error: %v", err)
	}
	if len(lines) < 2 || lines[1].ItemID == "" {
		t.Fatalf("rollout lines = %+v", lines)
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

func TestRouterThreadStartRejectsPaginatedHistoryMode(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         "D:/repo",
		HistoryMode: ThreadHistoryPaginated,
	}))
	assertPaginatedUnsupported(t, response)

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

func TestRouterThreadStartPersistsDynamicTools(t *testing.T) {
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
	record, err := store.Read("thread-1", true, false)
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
	rolloutPath, err := rollout.FindThreadPath(store.Root(), "thread-1", false)
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
	record, err := store.Read("thread-1", true, false)
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

func TestRouterPaginatedRolloutRejectsLegacyHistoryPaths(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
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
	assertPaginatedUnsupported(t, readTurns)
	itemsList := router.Handle(requestWithParams(t, IntID(21), MethodThreadItemsList, ThreadItemsListParams{ThreadID: "thread-paginated"}))
	assertPaginatedUnsupported(t, itemsList)
	turnsList := router.Handle(requestWithParams(t, IntID(3), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: "thread-paginated"}))
	assertPaginatedUnsupported(t, turnsList)
	resume := router.Handle(requestWithParams(t, IntID(4), MethodThreadResume, ThreadResumeParams{ThreadID: "thread-paginated"}))
	assertPaginatedUnsupported(t, resume)
	fork := router.Handle(requestWithParams(t, IntID(5), MethodThreadFork, ThreadForkParams{ThreadID: "thread-paginated"}))
	assertPaginatedUnsupported(t, fork)
	path := recorder.Path()
	forkPath := router.Handle(requestWithParams(t, IntID(6), MethodThreadFork, ThreadForkParams{Path: &path}))
	assertPaginatedUnsupported(t, forkPath)

	runtimeRouter := NewRuntimeRouter(RuntimeServices{ThreadRouter: router, ThreadStatus: NewThreadStatusManager()})
	runtimeTurnsList := runtimeRouter.Handle(requestWithParams(t, IntID(7), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: "thread-paginated"}))
	assertPaginatedUnsupported(t, runtimeTurnsList)
	runtimeFork := runtimeRouter.Handle(requestWithParams(t, IntID(8), MethodThreadFork, ThreadForkParams{
		ThreadID:  "thread-paginated",
		Ephemeral: true,
	}))
	assertPaginatedUnsupported(t, runtimeFork)
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

func assertPaginatedUnsupported(t *testing.T, response *Response) {
	t.Helper()
	if response == nil || response.Error == nil || response.Error.Code != -32601 || response.Error.Message != "paginated_threads is not supported yet" {
		t.Fatalf("response error = %+v", response)
	}
}

func assertPermissionsSandboxConflict(t *testing.T, response *Response) {
	t.Helper()
	if response == nil || response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "`permissions` cannot be combined with `sandbox`" {
		t.Fatalf("permissions/sandbox response = %+v", response)
	}
}

func createRecord(t *testing.T, store *session.Store, id session.ThreadID, now time.Time) {
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
			HistoryMode:   "paginated",
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
