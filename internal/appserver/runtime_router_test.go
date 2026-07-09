package appserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/internal/agent"
	"codex_go/internal/apps"
	"codex_go/internal/auth"
	"codex_go/internal/compact"
	"codex_go/internal/config"
	"codex_go/internal/features"
	"codex_go/internal/mcp"
	"codex_go/internal/model"
	"codex_go/internal/plugin"
	"codex_go/internal/remotecontrol"
	"codex_go/internal/review"
	"codex_go/internal/rollout"
	"codex_go/internal/sandbox"
	"codex_go/internal/session"
	"codex_go/internal/tool"
	"codex_go/internal/turn"
)

func TestRuntimeRouterDispatchesThreadAndFS(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		FS:           NewFSService(),
	})
	router.SetNotificationSink(sink)
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: "D:/repo", Prompt: "hello"}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	if !sinkHasMethod(sink, NotificationThreadStarted) {
		t.Fatalf("thread started notification missing: %+v", sink.List())
	}
	path := filepath.Join(t.TempDir(), "note.txt")
	write := router.Handle(requestWithParams(t, IntID(2), MethodFSWriteFile, WriteFileParams{
		Path:       path,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("hello")),
	}))
	if write.Error != nil {
		t.Fatalf("fs write error: %+v", write.Error)
	}
	read := router.Handle(requestWithParams(t, IntID(3), MethodFSReadFile, ReadFileParams{Path: path}))
	if read.Error != nil {
		t.Fatalf("fs read error: %+v", read.Error)
	}
	response := read.Result.(*ReadFileResponse)
	decoded, err := base64.StdEncoding.DecodeString(response.DataBase64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if string(decoded) != "hello" {
		t.Fatalf("decoded = %q, want hello", decoded)
	}
}

func TestRuntimeRouterThreadListDefaultsToConfiguredModelProvider(t *testing.T) {
	store := session.NewStore(t.TempDir())
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = \"azure\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	now := fixedTime()
	openaiID := session.ThreadID("00000000-0000-0000-0000-000000000101")
	azureID := session.ThreadID("00000000-0000-0000-0000-000000000102")
	childID := session.ThreadID("00000000-0000-0000-0000-000000000103")
	create := func(id session.ThreadID, provider string, parent session.ThreadID, createdAt time.Time) {
		t.Helper()
		if err := store.Create(&session.Record{
			ID:             id,
			SessionID:      string(id),
			ParentThreadID: parent,
			Preview:        string(id),
			CreatedAt:      createdAt,
			UpdatedAt:      createdAt,
			RecencyAt:      createdAt,
			Metadata: session.Metadata{
				CWD:           "D:/repo",
				ModelProvider: provider,
				Source:        string(SessionSourceCli),
				HistoryMode:   string(ThreadHistoryLegacy),
			},
		}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	create(openaiID, "openai", "", now)
	create(azureID, "azure", "", now.Add(time.Minute))
	create(childID, "openai", azureID, now.Add(2*time.Minute))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})

	defaultList := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{}))
	if defaultList.Error != nil {
		t.Fatalf("thread/list default error: %+v", defaultList.Error)
	}
	if data := defaultList.Result.(*ThreadListResponse).Data; len(data) != 1 || data[0].ID != string(azureID) {
		t.Fatalf("default provider list = %+v", data)
	}

	allProviders := router.Handle(&Request{
		JSONRPC: "2.0",
		ID:      IntID(2),
		Method:  MethodThreadList,
		Params:  json.RawMessage(`{"modelProviders":[]}`),
	})
	if allProviders.Error != nil {
		t.Fatalf("thread/list all providers error: %+v", allProviders.Error)
	}
	if data := allProviders.Result.(*ThreadListResponse).Data; len(data) != 3 || data[0].ID != string(childID) || data[1].ID != string(azureID) || data[2].ID != string(openaiID) {
		t.Fatalf("explicit empty provider list = %+v", data)
	}

	parent := string(azureID)
	relation := router.Handle(requestWithParams(t, IntID(3), MethodThreadList, ThreadListParams{ParentThreadID: &parent}))
	if relation.Error != nil {
		t.Fatalf("thread/list relation error: %+v", relation.Error)
	}
	if data := relation.Result.(*ThreadListResponse).Data; len(data) != 1 || data[0].ID != string(childID) {
		t.Fatalf("relation provider list = %+v", data)
	}
}

func TestRuntimeRouterThreadListAndSearchOverlayRuntimeStatus(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	threadID := session.ThreadID("thread-system-error")
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Preview:   "needle preview",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			Source:        string(SessionSourceCli),
			ModelProvider: "openai",
			HistoryMode:   string(ThreadHistoryLegacy),
		},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.requireThreadStatus().UpsertThread(string(threadID), false)
	router.requireThreadStatus().NoteSystemError(string(threadID))

	list := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{
		ModelProviders: []string{"openai"},
	}))
	if list.Error != nil {
		t.Fatalf("thread/list error: %+v", list.Error)
	}
	listData := list.Result.(*ThreadListResponse).Data
	if len(listData) != 1 || listData[0].ID != string(threadID) {
		t.Fatalf("thread/list data = %+v", listData)
	}
	if got := listData[0].Status.Type; got != SystemErrorStatus().Type {
		t.Fatalf("thread/list status = %q, want %q", got, SystemErrorStatus().Type)
	}

	search := router.Handle(requestWithParams(t, IntID(2), MethodThreadSearch, ThreadSearchParams{
		SearchTerm: "needle",
	}))
	if search.Error != nil {
		t.Fatalf("thread/search error: %+v", search.Error)
	}
	searchData := search.Result.(*ThreadSearchResponse).Data
	if len(searchData) != 1 || searchData[0].Thread.ID != string(threadID) {
		t.Fatalf("thread/search data = %+v", searchData)
	}
	if got := searchData[0].Thread.Status.Type; got != SystemErrorStatus().Type {
		t.Fatalf("thread/search status = %q, want %q", got, SystemErrorStatus().Type)
	}
}

func TestRuntimeRouterThreadStartRejectsPaginatedHistoryMode(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	persistent := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         t.TempDir(),
		HistoryMode: ThreadHistoryPaginated,
	}))
	if persistent.Error == nil || persistent.Error.Code != -32601 || persistent.Error.Message != "paginated_threads is not supported yet" {
		t.Fatalf("persistent start error = %+v", persistent.Error)
	}
	ephemeral := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD:         t.TempDir(),
		HistoryMode: ThreadHistoryPaginated,
		Ephemeral:   true,
	}))
	if ephemeral.Error == nil || ephemeral.Error.Code != -32601 || ephemeral.Error.Message != "paginated_threads is not supported yet" {
		t.Fatalf("ephemeral start error = %+v", ephemeral.Error)
	}
}

func TestRuntimeRouterThreadResumeRejectsUnmaterializedThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadStatus: NewThreadStatusManager()})

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if start.Error != nil {
		t.Fatalf("start error: %+v", start.Error)
	}
	thread := start.Result.(*ThreadStartResponse).Thread
	if thread.Path == nil {
		t.Fatalf("start path = nil")
	}
	if _, err := os.Stat(*thread.Path); !os.IsNotExist(err) {
		t.Fatalf("start path should be unmaterialized, stat err = %v", err)
	}

	resume := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: thread.ID}))
	if resume.Error == nil || resume.Error.Code != -32600 || !strings.Contains(resume.Error.Message, "no rollout found for thread id") {
		t.Fatalf("resume error = %+v", resume.Error)
	}
	resumeMetadataOnly := router.Handle(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{ThreadID: thread.ID, ExcludeTurns: true}))
	if resumeMetadataOnly.Error == nil || resumeMetadataOnly.Error.Code != -32600 || !strings.Contains(resumeMetadataOnly.Error.Message, "no rollout found for thread id") {
		t.Fatalf("resume excludeTurns error = %+v", resumeMetadataOnly.Error)
	}
}

func TestRuntimeRouterThreadLifecycleRejectsPermissionsWithSandboxBeforeStarting(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	permissions := "dev"
	sandboxPolicy := map[string]any{"mode": "read-only"}

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         t.TempDir(),
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
	if notifications := sink.List(); len(notifications) != 0 {
		t.Fatalf("notifications = %#v", notifications)
	}
}

func TestRuntimeRouterThreadStartInstructionSourcesFeedTurns(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("global instructions"), 0o600); err != nil {
		t.Fatalf("write global AGENTS.md: %v", err)
	}
	workspace := t.TempDir()
	projectAgents := filepath.Join(workspace, "AGENTS.md")
	if err := os.WriteFile(projectAgents, []byte("project instructions"), 0o600); err != nil {
		t.Fatalf("write project AGENTS.md: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Config:       config.NewConfigService(home),
	})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: workspace}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	startResult := start.Result.(*ThreadStartResponse)
	globalSource := filepath.Join(home, "AGENTS.md")
	if len(startResult.InstructionSources) != 2 || !sameAppPath(startResult.InstructionSources[0], globalSource) || !sameAppPath(startResult.InstructionSources[1], projectAgents) {
		t.Fatalf("instruction sources = %#v", startResult.InstructionSources)
	}
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: startResult.Thread.ID,
		Prompt:   "inspect",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	request := waitForRuntimeAgentRequest(t, agent)
	if !strings.Contains(request.Instructions, "global instructions") || !strings.Contains(request.Instructions, "project instructions") {
		t.Fatalf("instructions = %q", request.Instructions)
	}
	if err := os.Remove(projectAgents); err != nil {
		t.Fatalf("remove project AGENTS.md: %v", err)
	}
	resume := router.Handle(requestWithParams(t, IntID(5), MethodThreadResume, ThreadResumeParams{
		ThreadID:     startResult.Thread.ID,
		ExcludeTurns: true,
	}))
	if resume.Error != nil {
		t.Fatalf("resume error: %+v", resume.Error)
	}
	resumeResult := resume.Result.(*ThreadResumeResponse)
	if len(resumeResult.InstructionSources) != 2 || !sameAppPath(resumeResult.InstructionSources[0], globalSource) || !sameAppPath(resumeResult.InstructionSources[1], projectAgents) {
		t.Fatalf("resume instruction sources = %#v", resumeResult.InstructionSources)
	}

	startNoEnvironment := router.Handle(requestWithParams(t, IntID(3), MethodThreadStart, map[string]any{
		"cwd":          workspace,
		"environments": []any{},
	}))
	if startNoEnvironment.Error != nil {
		t.Fatalf("thread start with empty environments error: %+v", startNoEnvironment.Error)
	}
	startNoEnvironmentResult := startNoEnvironment.Result.(*ThreadStartResponse)
	if len(startNoEnvironmentResult.InstructionSources) != 1 || !sameAppPath(startNoEnvironmentResult.InstructionSources[0], globalSource) {
		t.Fatalf("empty environment instruction sources = %#v", startNoEnvironmentResult.InstructionSources)
	}

	emptyWorkspace := t.TempDir()
	emptyProjectAgents := filepath.Join(emptyWorkspace, "AGENTS.md")
	if err := os.WriteFile(emptyProjectAgents, []byte(""), 0o600); err != nil {
		t.Fatalf("write empty project AGENTS.md: %v", err)
	}
	startEmptyProject := router.Handle(requestWithParams(t, IntID(6), MethodThreadStart, ThreadStartParams{CWD: emptyWorkspace}))
	if startEmptyProject.Error != nil {
		t.Fatalf("thread start with empty project AGENTS.md error: %+v", startEmptyProject.Error)
	}
	startEmptyProjectResult := startEmptyProject.Result.(*ThreadStartResponse)
	if len(startEmptyProjectResult.InstructionSources) != 1 || !sameAppPath(startEmptyProjectResult.InstructionSources[0], globalSource) {
		t.Fatalf("empty project AGENTS.md instruction sources = %#v", startEmptyProjectResult.InstructionSources)
	}

	turnStart = router.Handle(requestWithParams(t, IntID(4), MethodTurnStart, turn.TurnStartParams{
		ThreadID: startNoEnvironmentResult.Thread.ID,
		Prompt:   "inspect global only",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start with empty environments error: %+v", turnStart.Error)
	}
	turnID = turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	request = waitForRuntimeAgentRequest(t, agent)
	if !strings.Contains(request.Instructions, "global instructions") || strings.Contains(request.Instructions, "project instructions") {
		t.Fatalf("empty environment instructions = %q", request.Instructions)
	}
}

func TestRuntimeRouterMaterializesUnpromptedThreadRolloutOnFirstTurn(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	thread := start.Result.(*ThreadStartResponse).Thread
	if thread.Path == nil || !strings.HasSuffix(*thread.Path, ".jsonl") {
		t.Fatalf("thread path = %+v", thread.Path)
	}
	if _, err := os.Stat(*thread.Path); !os.IsNotExist(err) {
		t.Fatalf("thread path should not exist before first turn, stat err = %v", err)
	}
	read := router.Handle(requestWithParams(t, IntID(2), MethodThreadRead, ThreadReadParams{ThreadID: thread.ID}))
	if read.Error != nil {
		t.Fatalf("thread read error: %+v", read.Error)
	}
	readThread := read.Result.(*ThreadReadResponse).Thread
	if readThread.Status.Type != "idle" || readThread.Path == nil || *readThread.Path != *thread.Path {
		t.Fatalf("read thread = %+v, want idle path %q", readThread, *thread.Path)
	}

	archiveBeforeMaterialized := router.Handle(requestWithParams(t, IntID(3), MethodThreadArchive, ThreadArchiveParams{ThreadID: thread.ID}))
	if archiveBeforeMaterialized.Error == nil || archiveBeforeMaterialized.Error.Code != -32600 || !strings.Contains(archiveBeforeMaterialized.Error.Message, "no rollout found for thread id "+thread.ID) {
		t.Fatalf("archive before materialized error = %+v", archiveBeforeMaterialized.Error)
	}

	turnStart := router.Handle(requestWithParams(t, IntID(4), MethodTurnStart, turn.TurnStartParams{
		ThreadID: thread.ID,
		Prompt:   "materialize",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if _, err := os.Stat(*thread.Path); err != nil {
		t.Fatalf("thread path should exist after first turn: %v", err)
	}
	found, err := rollout.FindThreadPath(store.Root(), thread.ID, false)
	if err != nil {
		t.Fatalf("FindThreadPath() error = %v", err)
	}
	if found != *thread.Path {
		t.Fatalf("materialized path = %q, want %q", found, *thread.Path)
	}
	archive := router.Handle(requestWithParams(t, IntID(5), MethodThreadArchive, ThreadArchiveParams{ThreadID: thread.ID}))
	if archive.Error != nil {
		t.Fatalf("archive after materialized error: %+v", archive.Error)
	}
}

func TestRuntimeRouterThreadItemTurnListMissingThreadUsesRustErrors(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(session.NewStore(t.TempDir()))})

	items := router.Handle(requestWithParams(t, IntID(1), MethodThreadItemsList, ThreadItemsListParams{ThreadID: "thread-missing"}))
	if items.Error == nil || items.Error.Code != -32600 || items.Error.Message != "no rollout found for thread id thread-missing" {
		t.Fatalf("items missing thread response = %+v", items)
	}

	turns := router.Handle(requestWithParams(t, IntID(2), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: "thread-missing"}))
	if turns.Error == nil || turns.Error.Code != -32600 || turns.Error.Message != "thread not loaded: thread-missing" {
		t.Fatalf("turns missing thread response = %+v", turns)
	}
}

func TestRuntimeRouterEphemeralThreadStartStaysInMemory(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ephemeral ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:       t.TempDir(),
		Prompt:    "seed",
		Ephemeral: true,
	}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	thread := start.Result.(*ThreadStartResponse).Thread
	if !thread.Ephemeral || thread.Path != nil || thread.Status.Type != "idle" {
		t.Fatalf("ephemeral start thread = %+v", thread)
	}
	storePath, err := store.Path(session.ThreadID(thread.ID))
	if err != nil {
		t.Fatalf("store.Path() error = %v", err)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("ephemeral thread should not be persisted, stat err = %v", err)
	}
	list := router.Handle(requestWithParams(t, IntID(2), MethodThreadList, ThreadListParams{}))
	if list.Error != nil {
		t.Fatalf("thread list error: %+v", list.Error)
	}
	for _, listed := range list.Result.(*ThreadListResponse).Data {
		if listed.ID == thread.ID {
			t.Fatalf("ephemeral thread appeared in thread/list: %+v", listed)
		}
	}
	loaded := router.Handle(requestWithParams(t, IntID(3), MethodThreadLoadedList, ThreadLoadedListParams{}))
	if loaded.Error != nil {
		t.Fatalf("loaded list error: %+v", loaded.Error)
	}
	if got := loaded.Result.(*ThreadLoadedListResponse).Data; len(got) != 1 || got[0] != thread.ID {
		t.Fatalf("loaded ids = %v, want [%s]", got, thread.ID)
	}
	read := router.Handle(requestWithParams(t, IntID(4), MethodThreadRead, ThreadReadParams{ThreadID: thread.ID}))
	if read.Error != nil {
		t.Fatalf("thread read error: %+v", read.Error)
	}
	readThread := read.Result.(*ThreadReadResponse).Thread
	if !readThread.Ephemeral || readThread.Path != nil || readThread.Status.Type != "idle" {
		t.Fatalf("read thread = %+v", readThread)
	}
	includeTurns := router.Handle(requestWithParams(t, IntID(5), MethodThreadRead, ThreadReadParams{ThreadID: thread.ID, IncludeTurns: true}))
	if includeTurns.Error == nil || includeTurns.Error.Code != -32600 || includeTurns.Error.Message != "ephemeral threads do not support includeTurns" {
		t.Fatalf("includeTurns error = %+v", includeTurns.Error)
	}

	turnStart := router.Handle(requestWithParams(t, IntID(6), MethodTurnStart, turn.TurnStartParams{
		ThreadID: thread.ID,
		Prompt:   "continue",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("ephemeral thread should remain unpersisted after turn, stat err = %v", err)
	}
	record, ok := router.ephemeralThreadRecord(session.ThreadID(thread.ID), true)
	if !ok {
		t.Fatalf("ephemeral record missing after turn")
	}
	if len(record.Items) < 3 || record.Metadata.LastResponseID != "resp-"+turnID {
		t.Fatalf("ephemeral record after turn = %+v", record)
	}
	turnsList := router.Handle(requestWithParams(t, IntID(7), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: thread.ID}))
	if turnsList.Error == nil || turnsList.Error.Code != -32600 || turnsList.Error.Message != "ephemeral threads do not support thread/turns/list" {
		t.Fatalf("turns/list error = %+v", turnsList.Error)
	}
	itemsList := router.Handle(requestWithParams(t, IntID(71), MethodThreadItemsList, ThreadItemsListParams{ThreadID: thread.ID}))
	if itemsList.Error == nil || itemsList.Error.Code != -32600 || itemsList.Error.Message != "ephemeral threads do not support thread/items/list" {
		t.Fatalf("items/list error = %+v", itemsList.Error)
	}
	deleteResp := router.Handle(requestWithParams(t, IntID(8), MethodThreadDelete, ThreadDeleteParams{ThreadID: thread.ID}))
	wantDelete := "thread is not persisted and cannot be deleted: " + thread.ID
	if deleteResp.Error == nil || deleteResp.Error.Code != -32600 || deleteResp.Error.Message != wantDelete {
		t.Fatalf("delete error = %+v, want %q", deleteResp.Error, wantDelete)
	}
	loadedAfterDelete := router.Handle(requestWithParams(t, IntID(9), MethodThreadLoadedList, ThreadLoadedListParams{}))
	if got := loadedAfterDelete.Result.(*ThreadLoadedListResponse).Data; len(got) != 1 || got[0] != thread.ID {
		t.Fatalf("loaded ids after failed delete = %v, want [%s]", got, thread.ID)
	}
	objective := "finish"
	goal := router.Handle(requestWithParams(t, IntID(10), MethodThreadGoalSet, GoalSetParams{
		ThreadID:  thread.ID,
		Objective: &objective,
	}))
	wantGoal := "ephemeral thread does not support goals: " + thread.ID
	if goal.Error == nil || goal.Error.Code != -32600 || goal.Error.Message != wantGoal {
		t.Fatalf("goal error = %+v, want %q", goal.Error, wantGoal)
	}
	branch := "main"
	metadata := router.Handle(requestWithParams(t, IntID(11), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID: thread.ID,
		GitInfo:  &ThreadMetadataGitInfoPatch{Branch: OptionalString{Set: true, Value: &branch}},
	}))
	wantMetadata := "ephemeral thread does not support metadata updates: " + thread.ID
	if metadata.Error == nil || metadata.Error.Code != -32600 || metadata.Error.Message != wantMetadata {
		t.Fatalf("metadata error = %+v, want %q", metadata.Error, wantMetadata)
	}
}

func TestRuntimeRouterEphemeralForkStaysReadableAndUnlisted(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("fork ok"),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "source"}))
	if start.Error != nil {
		t.Fatalf("source start error: %+v", start.Error)
	}
	source := start.Result.(*ThreadStartResponse).Thread
	fork := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:  source.ID,
		Ephemeral: true,
	}))
	if fork.Error != nil {
		t.Fatalf("fork error: %+v", fork.Error)
	}
	forkThread := fork.Result.(*ThreadForkResponse).Thread
	if !forkThread.Ephemeral || forkThread.Path != nil || len(forkThread.Turns) == 0 {
		t.Fatalf("ephemeral fork thread = %+v", forkThread)
	}
	storePath, err := store.Path(session.ThreadID(forkThread.ID))
	if err != nil {
		t.Fatalf("store.Path() error = %v", err)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("ephemeral fork should not be persisted, stat err = %v", err)
	}
	read := router.Handle(requestWithParams(t, IntID(3), MethodThreadRead, ThreadReadParams{ThreadID: forkThread.ID}))
	if read.Error != nil {
		t.Fatalf("fork read error: %+v", read.Error)
	}
	if got := read.Result.(*ThreadReadResponse).Thread; !got.Ephemeral || got.Path != nil || got.Status.Type != "idle" {
		t.Fatalf("read fork thread = %+v", got)
	}
	list := router.Handle(requestWithParams(t, IntID(4), MethodThreadList, ThreadListParams{}))
	if list.Error != nil {
		t.Fatalf("thread list error: %+v", list.Error)
	}
	seenSource := false
	for _, listed := range list.Result.(*ThreadListResponse).Data {
		if listed.ID == source.ID {
			seenSource = true
		}
		if listed.ID == forkThread.ID {
			t.Fatalf("ephemeral fork appeared in thread/list: %+v", listed)
		}
	}
	if !seenSource {
		t.Fatalf("persistent source missing from thread/list")
	}
	if err := store.Save(&session.Record{
		ID:        "thread-legacy-ephemeral",
		SessionID: "thread-legacy-ephemeral",
		Items:     []session.Item{{ID: "legacy-item", Type: "message", Role: "user", Text: "legacy"}},
	}); err != nil {
		t.Fatalf("Save legacy thread error: %v", err)
	}
	synthetic := router.Handle(requestWithParams(t, IntID(41), MethodThreadFork, ThreadForkParams{
		ThreadID:   "thread-legacy-ephemeral",
		LastTurnID: "turn-1",
		Ephemeral:  true,
	}))
	if synthetic.Error == nil || synthetic.Error.Code != -32600 || synthetic.Error.Message != "lastTurnId 'turn-1' is not a persisted canonical turn in the source thread" {
		t.Fatalf("synthetic ephemeral fork response = %+v", synthetic)
	}
	turnStart := router.Handle(requestWithParams(t, IntID(5), MethodTurnStart, turn.TurnStartParams{
		ThreadID: forkThread.ID,
		Prompt:   "continue",
	}))
	if turnStart.Error != nil {
		t.Fatalf("fork turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("ephemeral fork should remain unpersisted after turn, stat err = %v", err)
	}
}

func TestRuntimeRouterTurnStartEmptyInputRunsAgent(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Prompt != "" || len(request.InputItems) != 0 {
		t.Fatalf("agent request = %#v", request)
	}
}

func TestRuntimeRouterTurnStartRejectsOversizedInputWithRustErrorData(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Turns: turn.NewTurnService()})
	response := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{
		ThreadID: "thread-1",
		Input: []turn.TurnUserInput{
			{Type: "text", Text: strings.Repeat("x", turn.MaxUserInputTextChars+1)},
			{Type: "mention", Name: "README", Path: "README.md"},
		},
	}))
	if response.Error == nil {
		t.Fatalf("expected oversized input error, got %+v", response)
	}
	if response.Error.Code != -32602 {
		t.Fatalf("error code = %d, want -32602", response.Error.Code)
	}
	wantMessage := "Input exceeds the maximum length of 1048576 characters."
	if response.Error.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", response.Error.Message, wantMessage)
	}
	if response.Error.Data["input_error_code"] != turn.InputTooLargeCode {
		t.Fatalf("error data = %#v", response.Error.Data)
	}
	if response.Error.Data["max_chars"] != turn.MaxUserInputTextChars || response.Error.Data["actual_chars"] != turn.MaxUserInputTextChars+1 {
		t.Fatalf("error data = %#v", response.Error.Data)
	}
}

func TestRuntimeRouterTurnStartRejectsPermissionsWithSandboxPolicyBeforeStarting(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Turns: turn.NewTurnService()})
	router.SetNotificationSink(sink)
	permissions := "danger-full-access"
	response := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{
		ThreadID:      "thread-1",
		Input:         []turn.TurnUserInput{{Text: "hello"}},
		Permissions:   &permissions,
		SandboxPolicy: sandbox.NewDangerFullAccessPolicy(),
	}))
	if response.Error == nil {
		t.Fatalf("expected permissions/sandboxPolicy conflict error, got %+v", response)
	}
	if response.Error.Code != -32600 {
		t.Fatalf("error code = %d, want -32600", response.Error.Code)
	}
	wantMessage := "`permissions` cannot be combined with `sandboxPolicy`"
	if response.Error.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", response.Error.Message, wantMessage)
	}
	if len(sink.List()) != 0 {
		t.Fatalf("unexpected notifications on rejected turn/start: %+v", sink.List())
	}
}

func TestRuntimeRouterTurnStartRejectsUnknownEnvironmentBeforeStarting(t *testing.T) {
	sink := NewNotificationBuffer()
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Environment:  NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "sh"}, ""),
	})
	router.SetNotificationSink(sink)
	response := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{
		ThreadID: "thread-1",
		Input:    []turn.TurnUserInput{{Text: "hello"}},
		Environments: []map[string]any{{
			"environmentId": "missing",
			"cwd":           "D:/repo",
		}},
	}))
	if response.Error == nil {
		t.Fatalf("expected unknown environment error, got %+v", response)
	}
	if response.Error.Code != -32600 || response.Error.Message != "unknown turn environment id `missing`" {
		t.Fatalf("error = %+v", response.Error)
	}
	if len(sink.List()) != 0 {
		t.Fatalf("unexpected notifications on rejected turn/start: %+v", sink.List())
	}
	threadStart := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD: "D:/repo",
		Environments: []map[string]any{{
			"environmentId": "missing",
			"cwd":           "D:/repo",
		}},
	}))
	if threadStart.Error == nil || threadStart.Error.Code != -32600 || threadStart.Error.Message != "unknown turn environment id `missing`" {
		t.Fatalf("thread/start error = %+v", threadStart.Error)
	}
	if len(sink.List()) != 0 {
		t.Fatalf("unexpected notifications on rejected thread/start: %+v", sink.List())
	}
}

func TestRuntimeRouterTurnStartRejectsRelativeEnvironmentCWD(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Environment:  NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "sh"}, ""),
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{
		ThreadID: "thread-1",
		Input:    []turn.TurnUserInput{{Text: "hello"}},
		Environments: []map[string]any{{
			"environmentId": "missing",
			"cwd":           "relative/path",
		}},
	}))
	if response.Error == nil {
		t.Fatalf("expected invalid cwd error, got %+v", response)
	}
	want := "invalid cwd for environment `missing`: path `relative/path` does not use absolute POSIX or Windows path syntax"
	if response.Error.Code != -32600 || response.Error.Message != want {
		t.Fatalf("error = %+v", response.Error)
	}
	threadStart := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD: "D:/repo",
		Environments: []map[string]any{{
			"environmentId": "missing",
			"cwd":           "relative/path",
		}},
	}))
	if threadStart.Error == nil || threadStart.Error.Code != -32600 || threadStart.Error.Message != want {
		t.Fatalf("thread/start error = %+v", threadStart.Error)
	}
}

func TestRuntimeRouterRejectsRemoteImageTurnInputs(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Turns: turn.NewTurnService()})
	cases := []struct {
		name   string
		method Method
		params any
	}{
		{
			name:   "turn start",
			method: MethodTurnStart,
			params: turn.TurnStartParams{
				ThreadID: "thread-1",
				Input:    []turn.TurnUserInput{{Type: "image", URL: "HTTP://example.com/start.png"}},
			},
		},
		{
			name:   "turn steer",
			method: MethodTurnSteer,
			params: turn.TurnSteerParams{
				ThreadID:       "thread-1",
				ExpectedTurnID: "turn-1",
				Input:          []turn.TurnUserInput{{Type: "image", URL: "https://example.com/steer.png"}},
			},
		},
	}
	for i, tc := range cases {
		response := router.Handle(requestWithParams(t, IntID(int64(i+1)), tc.method, tc.params))
		if response.Error == nil {
			t.Fatalf("%s: expected remote image error, got %+v", tc.name, response)
		}
		if response.Error.Code != -32600 || response.Error.Message != remoteImageURLError {
			t.Fatalf("%s: error = %+v", tc.name, response.Error)
		}
	}
}

func TestRuntimeRouterTurnStartSendsStructuredImageInputWithDefaultDetail(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	explicitDetail := "original"
	localImageBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatalf("DecodeString local image error = %v", err)
	}
	localImagePath := filepath.Join(t.TempDir(), "local.png")
	if err := os.WriteFile(localImagePath, localImageBytes, 0o600); err != nil {
		t.Fatalf("WriteFile local image error = %v", err)
	}
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Input: []turn.TurnUserInput{
			{Type: "text", Text: "describe these"},
			{Type: "image", URL: "data:image/png;base64,AAAA"},
			{Type: "localImage", Path: localImagePath, Detail: &explicitDetail},
		},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Prompt != "" {
		t.Fatalf("prompt = %q, want structured input item only", request.Prompt)
	}
	if !agentRequestInputItemsContain(request, "describe these") {
		t.Fatalf("agent request input items missing text: %#v", request.InputItems)
	}
	details := inputImageDetails(request.InputItems)
	if len(details) != 2 || details[0] != "high" || details[1] != "original" {
		t.Fatalf("input image details = %#v, input items = %#v", details, request.InputItems)
	}
	imageURLs := inputImageURLs(request.InputItems)
	if len(imageURLs) != 2 || imageURLs[0] != "data:image/png;base64,AAAA" || !strings.HasPrefix(imageURLs[1], "data:image/png;base64,") {
		t.Fatalf("input image URLs = %#v, input items = %#v", imageURLs, request.InputItems)
	}
}

func TestRuntimeRouterTurnStartEmitsUserMessageStartedWithTextElements(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	clientID := "client-message-1"
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID:            threadID,
		ClientUserMessageID: clientID,
		Input: []turn.TurnUserInput{{
			Text: "Hello",
			TextElements: []turn.TextElement{{
				ByteRange:   turn.ByteRange{Start: 0, End: 5},
				Placeholder: stringPtrIfNotEmpty("<note>"),
			}},
		}},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	_ = waitForRuntimeAgentRequest(t, agent)
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	for _, notification := range sink.List() {
		if notification.Method != NotificationItemStarted {
			continue
		}
		started, ok := notification.Params.(*ItemStartedNotification)
		if !ok || started.Item["type"] != "userMessage" {
			continue
		}
		if started.Item["clientId"] != clientID {
			t.Fatalf("clientId = %#v", started.Item["clientId"])
		}
		content, ok := started.Item["content"].([]any)
		if !ok || len(content) != 1 {
			t.Fatalf("content = %#v", started.Item["content"])
		}
		text, ok := content[0].(map[string]any)
		if !ok || text["type"] != "text" || text["text"] != "Hello" {
			t.Fatalf("text content = %#v", content[0])
		}
		elements, ok := text["text_elements"].([]any)
		if !ok || len(elements) != 1 {
			t.Fatalf("text_elements = %#v", text["text_elements"])
		}
		element, ok := elements[0].(map[string]any)
		byteRange, rangeOK := element["byteRange"].(map[string]any)
		if !ok || !rangeOK || byteRange["start"] != float64(0) || byteRange["end"] != float64(5) || element["placeholder"] != "<note>" {
			t.Fatalf("text element = %#v", elements[0])
		}
		return
	}
	t.Fatalf("userMessage item/started missing: %+v", sink.List())
}

func TestRuntimeRouterTurnStartLocalImageReadFailureBecomesInputText(t *testing.T) {
	store := session.NewStore(t.TempDir())
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	missingPath := filepath.Join(t.TempDir(), "missing.png")
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Input:    []turn.TurnUserInput{{Type: "localImage", Path: missingPath}},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	text := inputItemText(request.InputItems)
	if !strings.Contains(text, "Codex could not read the local image at `"+missingPath+"`") {
		t.Fatalf("input items = %#v", request.InputItems)
	}
	if urls := inputImageURLs(request.InputItems); len(urls) != 0 {
		t.Fatalf("input image URLs = %#v, want none", urls)
	}
}

func TestRuntimeRouterFSWatchUsesConnectionScope(t *testing.T) {
	fs := NewFSService()
	router := NewRuntimeRouter(RuntimeServices{FS: fs})
	root := t.TempDir()

	request := requestWithParams(t, IntID(1), MethodFSWatch, WatchParams{WatchID: "watch-1", Path: root})
	request.ConnectionID = "conn-a"
	response := router.Handle(request)
	if response.Error != nil {
		t.Fatalf("fs/watch conn-a error: %+v", response.Error)
	}

	request = requestWithParams(t, IntID(2), MethodFSWatch, WatchParams{WatchID: "watch-1", Path: root})
	request.ConnectionID = "conn-b"
	response = router.Handle(request)
	if response.Error != nil {
		t.Fatalf("fs/watch conn-b error: %+v", response.Error)
	}

	request = requestWithParams(t, IntID(3), MethodFSWatch, WatchParams{WatchID: "watch-1", Path: root})
	request.ConnectionID = "conn-a"
	response = router.Handle(request)
	if response.Error == nil || !strings.Contains(response.Error.Message, "watchId already exists") {
		t.Fatalf("duplicate fs/watch conn-a = %+v, want duplicate watch error", response)
	}

	request = requestWithParams(t, IntID(4), MethodFSUnwatch, UnwatchParams{WatchID: "watch-1"})
	request.ConnectionID = "conn-b"
	response = router.Handle(request)
	if response.Error != nil {
		t.Fatalf("fs/unwatch conn-b error: %+v", response.Error)
	}

	if _, ok := fs.ChangedForConnection("conn-a", "watch-1", filepath.Join(root, "a.txt")); !ok {
		t.Fatalf("ChangedForConnection(conn-a) ok = false, want true")
	}
	if _, ok := fs.ChangedForConnection("conn-b", "watch-1", filepath.Join(root, "b.txt")); ok {
		t.Fatalf("ChangedForConnection(conn-b) ok = true after unwatch, want false")
	}

	router.ConnectionClosed("conn-a")
	if fs.WatchCount() != 0 {
		t.Fatalf("WatchCount() = %d after ConnectionClosed, want 0", fs.WatchCount())
	}
}

func TestRuntimeRouterFSWriteFileEmitsTargetedChangedNotifications(t *testing.T) {
	fs := NewFSService()
	router := NewRuntimeRouter(RuntimeServices{FS: fs})
	sink := &targetedNotificationTestSink{}
	router.SetNotificationSink(sink)
	root := t.TempDir()
	file := filepath.Join(root, "FETCH_HEAD")

	request := requestWithParams(t, IntID(1), MethodFSWatch, WatchParams{WatchID: "watch-dir", Path: root})
	request.ConnectionID = "conn-a"
	if response := router.Handle(request); response.Error != nil {
		t.Fatalf("fs/watch dir error: %+v", response.Error)
	}
	defer router.ConnectionClosed("conn-a")
	request = requestWithParams(t, IntID(2), MethodFSWatch, WatchParams{WatchID: "watch-file", Path: file})
	request.ConnectionID = "conn-b"
	if response := router.Handle(request); response.Error != nil {
		t.Fatalf("fs/watch file error: %+v", response.Error)
	}
	defer router.ConnectionClosed("conn-b")

	write := requestWithParams(t, IntID(3), MethodFSWriteFile, WriteFileParams{
		Path:       file,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("updated\n")),
	})
	if response := router.Handle(write); response.Error != nil {
		t.Fatalf("fs/writeFile error: %+v", response.Error)
	}

	dirPayload := waitForTargetedFSChanged(t, sink, "conn-a", "watch-dir", file)
	if len(dirPayload.ChangedPaths) != 1 || dirPayload.ChangedPaths[0] != file {
		t.Fatalf("dir changed paths = %+v, want %q", dirPayload.ChangedPaths, file)
	}
	filePayload := waitForTargetedFSChanged(t, sink, "conn-b", "watch-file", file)
	if len(filePayload.ChangedPaths) != 1 || filePayload.ChangedPaths[0] != file {
		t.Fatalf("file changed paths = %+v, want %q", filePayload.ChangedPaths, file)
	}
}

func TestRuntimeRouterFSWatchReportsExternalFileChanges(t *testing.T) {
	fs := NewFSService()
	router := NewRuntimeRouter(RuntimeServices{FS: fs})
	sink := &targetedNotificationTestSink{}
	router.SetNotificationSink(sink)
	root := t.TempDir()
	file := filepath.Join(root, "external.txt")
	if err := os.WriteFile(file, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile fixture error = %v", err)
	}

	request := requestWithParams(t, IntID(1), MethodFSWatch, WatchParams{WatchID: "watch-file", Path: file})
	request.ConnectionID = "conn-a"
	if response := router.Handle(request); response.Error != nil {
		t.Fatalf("fs/watch file error: %+v", response.Error)
	}
	defer router.ConnectionClosed("conn-a")
	time.Sleep(2 * defaultFSWatchPollInterval)
	if err := os.WriteFile(file, []byte("updated outside appserver"), 0o600); err != nil {
		t.Fatalf("WriteFile external change error = %v", err)
	}

	payload := waitForTargetedFSChanged(t, sink, "conn-a", "watch-file", file)
	if len(payload.ChangedPaths) != 1 || payload.ChangedPaths[0] != file {
		t.Fatalf("changed paths = %+v, want %q", payload.ChangedPaths, file)
	}
}

type targetedNotificationTestSink struct {
	mu            sync.Mutex
	notifications []targetedNotificationTestRecord
}

type targetedNotificationTestRecord struct {
	connectionID string
	notification *Notification
}

func (s *targetedNotificationTestSink) Notify(notification *Notification) {
	s.append("", notification)
}

func (s *targetedNotificationTestSink) NotifyToConnection(connectionID string, notification *Notification) {
	s.append(connectionID, notification)
}

func (s *targetedNotificationTestSink) append(connectionID string, notification *Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifications = append(s.notifications, targetedNotificationTestRecord{connectionID: connectionID, notification: notification})
}

func (s *targetedNotificationTestSink) List() []targetedNotificationTestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]targetedNotificationTestRecord(nil), s.notifications...)
}

func waitForTargetedFSChanged(t *testing.T, sink *targetedNotificationTestSink, connectionID string, watchID string, changedPath string) *ChangedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []targetedNotificationTestRecord
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, sent := range last {
			if sent.connectionID != connectionID || sent.notification == nil || sent.notification.Method != NotificationFSChanged {
				continue
			}
			payload, ok := sent.notification.Params.(*ChangedNotification)
			if !ok || payload == nil || payload.WatchID != watchID {
				continue
			}
			for _, path := range payload.ChangedPaths {
				if path == changedPath {
					return payload
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for fs/changed target=%s watch=%s path=%s in %+v", connectionID, watchID, changedPath, last)
	return nil
}

func TestRuntimeRouterThreadUnsubscribeTracksConnectionSubscriptions(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	assertUnsubscribe := func(t *testing.T, router *RuntimeRouter, id int64, connectionID string, threadID string, want ThreadUnsubscribeStatus) {
		t.Helper()
		request := requestWithParams(t, IntID(id), MethodThreadUnsubscribe, ThreadUnsubscribeParams{ThreadID: threadID})
		request.ConnectionID = connectionID
		response := router.Handle(request)
		if response.Error != nil {
			t.Fatalf("thread/unsubscribe error: %+v", response.Error)
		}
		result := response.Result.(*ThreadUnsubscribeResponse)
		if result.Status != want {
			t.Fatalf("thread/unsubscribe connection=%s thread=%s status = %s, want %s", connectionID, threadID, result.Status, want)
		}
	}

	startRequest := requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "hello"})
	startRequest.ConnectionID = "conn-a"
	start := router.Handle(startRequest)
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	assertUnsubscribe(t, router, 2, "conn-b", threadID, ThreadUnsubscribeStatusNotSubscribed)
	assertUnsubscribe(t, router, 3, "conn-a", threadID, ThreadUnsubscribeStatusUnsubscribed)
	assertUnsubscribe(t, router, 4, "conn-a", threadID, ThreadUnsubscribeStatusNotSubscribed)

	resumeRequest := requestWithParams(t, IntID(5), MethodThreadResume, ThreadResumeParams{ThreadID: threadID, ExcludeTurns: true})
	resumeRequest.ConnectionID = "conn-b"
	resume := router.Handle(resumeRequest)
	if resume.Error != nil {
		t.Fatalf("thread/resume error: %+v", resume.Error)
	}
	assertUnsubscribe(t, router, 6, "conn-b", threadID, ThreadUnsubscribeStatusUnsubscribed)

	forkRequest := requestWithParams(t, IntID(7), MethodThreadFork, ThreadForkParams{ThreadID: threadID})
	forkRequest.ConnectionID = "conn-c"
	fork := router.Handle(forkRequest)
	if fork.Error != nil {
		t.Fatalf("thread/fork error: %+v", fork.Error)
	}
	forkThreadID := fork.Result.(*ThreadForkResponse).Thread.ID
	assertUnsubscribe(t, router, 8, "conn-c", forkThreadID, ThreadUnsubscribeStatusUnsubscribed)

	rejoinRequest := requestWithParams(t, IntID(9), MethodThreadResume, ThreadResumeParams{ThreadID: threadID, ExcludeTurns: true})
	rejoinRequest.ConnectionID = "conn-a"
	rejoin := router.Handle(rejoinRequest)
	if rejoin.Error != nil {
		t.Fatalf("thread/resume rejoin error: %+v", rejoin.Error)
	}
	router.ConnectionClosed("conn-a")
	assertUnsubscribe(t, router, 10, "conn-a", threadID, ThreadUnsubscribeStatusNotSubscribed)

	coldStore := session.NewStore(t.TempDir())
	coldStart := NewRouter(coldStore).Handle(requestWithParams(t, IntID(11), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "cold"}))
	if coldStart.Error != nil {
		t.Fatalf("cold thread/start error: %+v", coldStart.Error)
	}
	coldThreadID := coldStart.Result.(*ThreadStartResponse).Thread.ID
	coldRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(coldStore),
		ThreadStatus: NewThreadStatusManager(),
	})
	assertUnsubscribe(t, coldRouter, 12, "conn-a", coldThreadID, ThreadUnsubscribeStatusNotLoaded)
}

func TestRuntimeRouterThreadResumeRejectsHistoryWhileRunning(t *testing.T) {
	store := session.NewStore(t.TempDir())
	createRecord(t, store, "thread-running-history", fixedTime())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	if err := router.registerActiveRuntimeTurn("thread-running-history", "turn-running", func() {}, fixedTime().UnixMilli(), &turn.TurnStartParams{
		ThreadID: "thread-running-history",
		Prompt:   "still running",
	}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: "thread-running-history",
		History: []ThreadResumeHistoryItem{
			ThreadResumeHistoryItem(json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"history override"}]}`)),
		},
	}))
	if response.Error == nil || response.Error.Code != -32600 {
		t.Fatalf("resume response = %+v", response)
	}
	wantMessage := "cannot resume thread thread-running-history with history while it is already running"
	if response.Error.Message != wantMessage {
		t.Fatalf("resume error = %q, want %q", response.Error.Message, wantMessage)
	}
}

func TestRuntimeRouterThreadResumeRejectsStalePathWhileRunning(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "materialized",
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	thread := start.Result.(*ThreadStartResponse).Thread
	if thread.Path == nil || strings.TrimSpace(*thread.Path) == "" {
		t.Fatalf("thread path = %+v", thread.Path)
	}
	if err := router.registerActiveRuntimeTurn(thread.ID, "turn-running", func() {}, fixedTime().UnixMilli(), &turn.TurnStartParams{
		ThreadID: thread.ID,
		Prompt:   "still running",
	}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}

	stalePath := filepath.Join(t.TempDir(), "stale-rollout.jsonl")
	stale := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID:     thread.ID,
		Path:         &stalePath,
		ExcludeTurns: true,
	}))
	activePath := canonicalThreadLifecyclePath(*thread.Path, codexHomeFromSessionStore(store))
	wantStaleMessage := "cannot resume running thread " + thread.ID + " with stale path: requested `" + stalePath + "`, active `" + activePath + "`"
	if stale.Error == nil || stale.Error.Code != -32600 || stale.Error.Message != wantStaleMessage {
		t.Fatalf("stale resume response = %+v", stale)
	}

	currentPath := *thread.Path
	current := router.Handle(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{
		ThreadID:     thread.ID,
		Path:         &currentPath,
		ExcludeTurns: true,
	}))
	if current.Error != nil {
		t.Fatalf("current path resume error: %+v", current.Error)
	}
}

func TestRuntimeRouterThreadResumeRunningIgnoresOverrideMismatch(t *testing.T) {
	store := session.NewStore(t.TempDir())
	routerStore := NewRouter(store)
	routerStore.SetClock(func() time.Time { return fixedTime() })
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: routerStore,
		ThreadStatus: NewThreadStatusManager(),
	})
	cwd := t.TempDir()
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    cwd,
		Model:  "gpt-running",
		Prompt: "seed",
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	if err := router.registerActiveRuntimeTurn(threadID, "turn-running", func() {}, fixedTime().Add(time.Minute).UnixMilli(), &turn.TurnStartParams{
		ThreadID: threadID,
		CWD:      cwd,
		Model:    "gpt-running",
		Prompt:   "still running",
	}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}
	overrideModel := "not-the-running-model"
	overrideCWD := t.TempDir()
	limit := 1
	resume := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID: threadID,
		Model:    &overrideModel,
		CWD:      &overrideCWD,
		InitialTurnsPage: &ThreadInitialPageParams{
			Limit: &limit,
		},
	}))
	if resume.Error != nil {
		t.Fatalf("thread/resume error: %+v", resume.Error)
	}
	result := resume.Result.(*ThreadResumeResponse)
	if result.Model != "gpt-running" {
		t.Fatalf("resume model = %q", result.Model)
	}
	if result.CWD != cwd {
		t.Fatalf("resume cwd = %q, want %q", result.CWD, cwd)
	}
	if result.InitialTurnsPage == nil || len(result.InitialTurnsPage.Data) != 1 {
		t.Fatalf("initialTurnsPage = %+v", result.InitialTurnsPage)
	}
	runningTurn := result.InitialTurnsPage.Data[0]
	if runningTurn.ID != "turn-running" || runningTurn.Status != TurnStatusInProgress {
		t.Fatalf("running turn = %+v", runningTurn)
	}
}

func TestRuntimeRouterThreadLoadedListUsesRuntimeLoadedStatus(t *testing.T) {
	store := session.NewStore(t.TempDir())
	coldStart := NewRouter(store).Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "cold"}))
	if coldStart.Error != nil {
		t.Fatalf("cold thread/start error: %+v", coldStart.Error)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	listLoaded := func(t *testing.T, id int64, params ThreadLoadedListParams) *ThreadLoadedListResponse {
		t.Helper()
		response := router.Handle(requestWithParams(t, IntID(id), MethodThreadLoadedList, params))
		if response.Error != nil {
			t.Fatalf("thread/loaded/list error: %+v", response.Error)
		}
		return response.Result.(*ThreadLoadedListResponse)
	}
	empty := listLoaded(t, 2, ThreadLoadedListParams{})
	if len(empty.Data) != 0 || empty.NextCursor != nil {
		t.Fatalf("cold loaded list = %+v, want empty", empty)
	}

	startLoaded := func(id int64) string {
		start := router.Handle(requestWithParams(t, IntID(id), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "loaded"}))
		if start.Error != nil {
			t.Fatalf("thread/start error: %+v", start.Error)
		}
		return start.Result.(*ThreadStartResponse).Thread.ID
	}
	first := startLoaded(3)
	second := startLoaded(4)
	expected := []string{first, second}
	sort.Strings(expected)

	zero := 0
	firstPage := listLoaded(t, 5, ThreadLoadedListParams{Limit: &zero})
	if len(firstPage.Data) != 1 || firstPage.Data[0] != expected[0] || firstPage.NextCursor == nil || *firstPage.NextCursor != expected[0] {
		t.Fatalf("first page = %+v, expected first id %s with next cursor", firstPage, expected[0])
	}
	one := 1
	secondPage := listLoaded(t, 6, ThreadLoadedListParams{Cursor: firstPage.NextCursor, Limit: &one})
	if len(secondPage.Data) != 1 || secondPage.Data[0] != expected[1] || secondPage.NextCursor != nil {
		t.Fatalf("second page = %+v, expected second id %s with no next cursor", secondPage, expected[1])
	}

	missingCursor := expected[0] + "z"
	insertPage := listLoaded(t, 7, ThreadLoadedListParams{Cursor: &missingCursor, Limit: &one})
	if len(insertPage.Data) != 1 || insertPage.Data[0] != expected[1] {
		t.Fatalf("insert cursor page = %+v, expected %s", insertPage, expected[1])
	}

	badCursor := "not-a-cursor"
	bad := router.Handle(requestWithParams(t, IntID(8), MethodThreadLoadedList, ThreadLoadedListParams{Cursor: &badCursor}))
	if bad.Error == nil || bad.Error.Code != -32600 || bad.Error.Message != "invalid cursor: not-a-cursor" {
		t.Fatalf("thread/loaded/list invalid cursor response = %+v", bad)
	}
}

func TestRuntimeRouterThreadArchiveDeleteUnloadRuntimeStatus(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	startLoaded := func(id int64, connectionID string) string {
		request := requestWithParams(t, IntID(id), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "loaded"})
		request.ConnectionID = connectionID
		response := router.Handle(request)
		if response.Error != nil {
			t.Fatalf("thread/start error: %+v", response.Error)
		}
		return response.Result.(*ThreadStartResponse).Thread.ID
	}
	loadedIDs := func(id int64) []string {
		response := router.Handle(requestWithParams(t, IntID(id), MethodThreadLoadedList, ThreadLoadedListParams{}))
		if response.Error != nil {
			t.Fatalf("thread/loaded/list error: %+v", response.Error)
		}
		ids := append([]string(nil), response.Result.(*ThreadLoadedListResponse).Data...)
		sort.Strings(ids)
		return ids
	}
	hasString := func(values []string, target string) bool {
		for _, value := range values {
			if value == target {
				return true
			}
		}
		return false
	}
	unsubscribeStatus := func(id int64, connectionID string, threadID string) ThreadUnsubscribeStatus {
		request := requestWithParams(t, IntID(id), MethodThreadUnsubscribe, ThreadUnsubscribeParams{ThreadID: threadID})
		request.ConnectionID = connectionID
		response := router.Handle(request)
		if response.Error != nil {
			t.Fatalf("thread/unsubscribe error: %+v", response.Error)
		}
		return response.Result.(*ThreadUnsubscribeResponse).Status
	}

	archiveID := startLoaded(1, "conn-archive")
	deleteID := startLoaded(2, "conn-delete")
	if ids := loadedIDs(3); !hasString(ids, archiveID) || !hasString(ids, deleteID) {
		t.Fatalf("loaded before archive/delete = %#v, want %s and %s", ids, archiveID, deleteID)
	}

	archive := router.Handle(requestWithParams(t, IntID(4), MethodThreadArchive, ThreadArchiveParams{ThreadID: archiveID}))
	if archive.Error != nil {
		t.Fatalf("thread/archive error: %+v", archive.Error)
	}
	if ids := loadedIDs(5); hasString(ids, archiveID) || !hasString(ids, deleteID) {
		t.Fatalf("loaded after archive = %#v, want only %s among archived/deleted pair", ids, deleteID)
	}
	if status := unsubscribeStatus(6, "conn-archive", archiveID); status != ThreadUnsubscribeStatusNotLoaded {
		t.Fatalf("archived unsubscribe status = %s, want notLoaded", status)
	}

	deleted := router.Handle(requestWithParams(t, IntID(7), MethodThreadDelete, ThreadDeleteParams{ThreadID: deleteID}))
	if deleted.Error != nil {
		t.Fatalf("thread/delete error: %+v", deleted.Error)
	}
	if ids := loadedIDs(8); hasString(ids, deleteID) {
		t.Fatalf("loaded after delete = %#v, want delete thread absent", ids)
	}
	if status := unsubscribeStatus(9, "conn-delete", deleteID); status != ThreadUnsubscribeStatusNotLoaded {
		t.Fatalf("deleted unsubscribe status = %s, want notLoaded", status)
	}
}

func TestRuntimeRouterDispatchesRemoteEnvironmentAndWindows(t *testing.T) {
	execServerURL, done := newEnvironmentInfoExecServerForTest(t, map[string]any{
		"shell": map[string]any{"name": "bash", "path": "/bin/bash"},
		"cwd":   "file:///workspace",
	})
	envManager := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		Remote:      remotecontrol.NewManager("codex", "install"),
		Environment: envManager,
		Windows:     sandbox.NewWindowsManager(sandbox.WindowsReadinessNotConfigured),
	})
	enable := router.Handle(requestWithParams(t, IntID(1), MethodRemoteControlEnable, remotecontrol.EnableParams{}))
	if enable.Error != nil || enable.Result.(*remotecontrol.EnableResponse).Status != remotecontrol.StatusConnected {
		t.Fatalf("remote enable = %+v", enable)
	}
	add := router.Handle(requestWithParams(t, IntID(2), MethodEnvironmentAdd, EnvironmentAddParams{
		EnvironmentID: "env-1",
		ExecServerURL: execServerURL,
	}))
	if add.Error != nil {
		t.Fatalf("environment add error: %+v", add.Error)
	}
	info := router.Handle(requestWithParams(t, IntID(3), MethodEnvironmentInfo, EnvironmentInfoParams{EnvironmentID: "env-1"}))
	if info.Error != nil || info.Result.(*EnvironmentInfoResponse).Shell.Name != "bash" {
		t.Fatalf("environment info = %+v", info)
	}
	waitEnvironmentInfoExecServerForTest(t, done)
	cwd := t.TempDir()
	setup := router.Handle(requestWithParams(t, IntID(4), MethodWindowsSandboxSetupStart, sandbox.WindowsSetupStartParams{
		Mode: sandbox.WindowsSetupElevated,
		CWD:  &cwd,
	}))
	if setup.Error != nil || !setup.Result.(*sandbox.WindowsSetupStartResponse).Started {
		t.Fatalf("windows setup = %+v", setup)
	}
}

func TestRuntimeRouterRemoteControlStatusChangedNotifications(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Remote: remotecontrol.NewManager("codex", "install-1")})
	router.SetNotificationSink(sink)

	enable := router.Handle(requestWithParams(t, IntID(1), MethodRemoteControlEnable, remotecontrol.EnableParams{}))
	if enable.Error != nil {
		t.Fatalf("remote enable error: %+v", enable.Error)
	}
	enableResult := enable.Result.(*remotecontrol.EnableResponse)
	if enableResult.Status != remotecontrol.StatusConnected || enableResult.EnvironmentID == nil || *enableResult.EnvironmentID != "default" {
		t.Fatalf("remote enable result = %+v", enableResult)
	}
	statuses := remoteControlStatusChangedNotifications(sink)
	if len(statuses) != 1 || statuses[0].Status != remotecontrol.StatusConnected || statuses[0].EnvironmentID == nil || *statuses[0].EnvironmentID != "default" {
		t.Fatalf("remote status notifications after enable = %+v", statuses)
	}

	secondEnable := router.Handle(requestWithParams(t, IntID(2), MethodRemoteControlEnable, remotecontrol.EnableParams{}))
	if secondEnable.Error != nil {
		t.Fatalf("second remote enable error: %+v", secondEnable.Error)
	}
	if statuses := remoteControlStatusChangedNotifications(sink); len(statuses) != 1 {
		t.Fatalf("second enable notifications = %+v, want unchanged", statuses)
	}

	disable := router.Handle(requestWithParams(t, IntID(3), MethodRemoteControlDisable, remotecontrol.DisableParams{}))
	if disable.Error != nil {
		t.Fatalf("remote disable error: %+v", disable.Error)
	}
	disableResult := disable.Result.(*remotecontrol.DisableResponse)
	if disableResult.Status != remotecontrol.StatusDisabled || disableResult.EnvironmentID != nil {
		t.Fatalf("remote disable result = %+v", disableResult)
	}
	statuses = remoteControlStatusChangedNotifications(sink)
	if len(statuses) != 2 || statuses[1].Status != remotecontrol.StatusDisabled || statuses[1].EnvironmentID != nil {
		t.Fatalf("remote status notifications after disable = %+v", statuses)
	}

	secondDisable := router.Handle(requestWithParams(t, IntID(4), MethodRemoteControlDisable, remotecontrol.DisableParams{}))
	if secondDisable.Error != nil {
		t.Fatalf("second remote disable error: %+v", secondDisable.Error)
	}
	if statuses := remoteControlStatusChangedNotifications(sink); len(statuses) != 2 {
		t.Fatalf("second disable notifications = %+v, want unchanged", statuses)
	}
}

func TestRuntimeRouterRemoteControlStatusChangedHonorsOptOut(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Remote: remotecontrol.NewManager("codex", "install-1")})
	router.SetNotificationSink(sink)

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-test", Version: "0.1.0"},
		Capabilities: &InitializeCapabilities{
			ExperimentalAPI:           true,
			OptOutNotificationMethods: []string{string(NotificationRemoteControlStatusChanged)},
		},
	})
	initialize.ConnectionID = "conn-remote-opt-out"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	enable := requestWithParams(t, IntID(2), MethodRemoteControlEnable, remotecontrol.EnableParams{})
	enable.ConnectionID = "conn-remote-opt-out"
	if response := router.Handle(enable); response.Error != nil {
		t.Fatalf("remote enable error: %+v", response.Error)
	}
	if statuses := remoteControlStatusChangedNotifications(sink); len(statuses) != 0 {
		t.Fatalf("remote status notifications = %+v, want opt-out filtered", statuses)
	}
}

func TestRuntimeRouterEnvironmentErrorsUseInvalidRequest(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		Environment: NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, t.TempDir()),
	})
	add := router.Handle(requestWithParams(t, IntID(1), MethodEnvironmentAdd, EnvironmentAddParams{
		EnvironmentID: "env-1",
		ExecServerURL: "https://example.test/exec",
	}))
	if add.Error == nil || add.Error.Code != -32600 {
		t.Fatalf("environment add error = %+v, want invalid_request", add.Error)
	}
	info := router.Handle(requestWithParams(t, IntID(2), MethodEnvironmentInfo, EnvironmentInfoParams{EnvironmentID: "missing"}))
	if info.Error == nil || info.Error.Code != -32600 {
		t.Fatalf("environment info error = %+v, want invalid_request", info.Error)
	}
}

func TestRuntimeRouterDispatchesConfigAccountHooksAndFeedback(t *testing.T) {
	home := t.TempDir()
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		Config:     config.NewConfigService(home),
		Account:    auth.NewAccountManager(),
		Hooks:      NewHookRegistry(),
		DefaultCWD: "/repo",
	})
	router.SetNotificationSink(sink)
	if err := router.services.Hooks.Add("/repo", HookMetadata{
		Key:         "hook-1",
		EventName:   HookEventPostToolUse,
		HandlerType: HookHandlerCommand,
		SourcePath:  "/repo/hooks.json",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("Hooks.Add() error = %v", err)
	}
	configWrite := router.Handle(requestWithParams(t, IntID(1), MethodConfigValueWrite, config.ConfigValueWriteParams{
		KeyPath:       "model",
		Value:         "gpt-5",
		MergeStrategy: config.MergeReplace,
	}))
	if configWrite.Error != nil {
		t.Fatalf("config write error: %+v", configWrite.Error)
	}
	configRead := router.Handle(requestWithParams(t, IntID(2), MethodConfigRead, config.ConfigReadParams{}))
	if configRead.Error != nil || configRead.Result.(*config.ConfigReadResponse).Config["model"] != "gpt-5" {
		t.Fatalf("config read = %+v", configRead)
	}
	login := router.Handle(requestWithParams(t, IntID(3), MethodLoginAccount, auth.LoginAccountParams{
		Type:   auth.AccountAPIKey,
		APIKey: "sk-test",
	}))
	if login.Error != nil || login.Result.(*auth.LoginAccountResponse).Type != auth.AccountAPIKey {
		t.Fatalf("login = %+v", login)
	}
	loaded, err := auth.NewStore(home).Load()
	if err != nil {
		t.Fatalf("auth load error: %v", err)
	}
	if loaded == nil || loaded.OpenAIAPIKey != "sk-test" {
		t.Fatalf("persisted auth = %+v", loaded)
	}
	if !sinkHasMethod(sink, NotificationAccountLoginCompleted) || !sinkHasMethod(sink, NotificationAccountUpdated) {
		t.Fatalf("account login notifications = %+v", sink.List())
	}
	hookList := router.Handle(requestWithParams(t, IntID(4), MethodHooksList, HookListParams{}))
	if hookList.Error != nil || len(hookList.Result.(*HookListResponse).Data) != 1 {
		t.Fatalf("hooks list = %+v", hookList)
	}
	threadID := "thread-1"
	feedbackResponse := router.Handle(requestWithParams(t, IntID(5), MethodFeedbackUpload, map[string]any{"threadId": threadID, "classification": "bug"}))
	if feedbackResponse.Error != nil || feedbackResponse.Result.(*FeedbackUploadResponse).ThreadID != "thread-1" {
		t.Fatalf("feedback upload = %+v", feedbackResponse)
	}
	badFeedback := router.Handle(requestWithParams(t, IntID(6), MethodFeedbackUpload, map[string]any{"threadId": threadID}))
	if badFeedback.Error == nil || badFeedback.Error.Code != -32600 {
		t.Fatalf("feedback upload validation error = %+v, want invalid_request", badFeedback.Error)
	}
}

func TestRuntimeRouterInitializeEmitsConfigWarnings(t *testing.T) {
	home := t.TempDir()
	configService := config.NewConfigService(home)
	details := "bad config value"
	configService.SetWarnings([]config.ConfigWarningNotification{{
		Summary: "Invalid configuration; using defaults.",
		Details: &details,
	}})
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Config: configService})
	router.SetNotificationSink(sink)

	response := router.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-test", Version: "1.0.0"},
	}))
	if response.Error != nil {
		t.Fatalf("initialize = %+v", response)
	}
	notifications := sink.List()
	if len(notifications) != 1 || notifications[0].Method != NotificationConfigWarning {
		t.Fatalf("config warning notifications = %+v", notifications)
	}
	warning, ok := notifications[0].Params.(*config.ConfigWarningNotification)
	if !ok || warning.Summary != "Invalid configuration; using defaults." || warning.Details == nil || *warning.Details != details {
		t.Fatalf("config warning payload = %#v", notifications[0].Params)
	}
}

func TestRuntimeRouterInitializeRejectsInvalidClientName(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	response := router.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "bad\rname", Version: "0.1.0"},
	}))
	want := "Invalid clientInfo.name: 'bad\rname'. Must be a valid HTTP header value."
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != want {
		t.Fatalf("initialize error = %+v, want %q", response.Error, want)
	}
}

func TestRuntimeRouterInitializeUserAgentOriginator(t *testing.T) {
	cases := []struct {
		name        string
		clientName  string
		envOverride *string
		wantPrefix  string
	}{
		{
			name:       "client name originator",
			clientName: "codex_vscode",
			wantPrefix: "codex_vscode/",
		},
		{
			name:       "probe keeps default",
			clientName: "codex_app_server_daemon",
			wantPrefix: "codex_cli_rs/",
		},
		{
			name:       "backend keeps default",
			clientName: "codex-backend",
			wantPrefix: "codex_cli_rs/",
		},
		{
			name:        "env override wins",
			clientName:  "codex_vscode",
			envOverride: stringPtr("codex_originator_via_env_var"),
			wantPrefix:  "codex_originator_via_env_var/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setOriginatorOverrideForTest(t, tc.envOverride)
			router := NewRuntimeRouter(RuntimeServices{})
			response := router.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
				ClientInfo: ClientInfo{Name: tc.clientName, Version: "0.1.0"},
			}))
			if response.Error != nil {
				t.Fatalf("initialize = %+v", response)
			}
			result := response.Result.(*InitializeResponse)
			if !strings.HasPrefix(result.UserAgent, tc.wantPrefix) {
				t.Fatalf("user agent = %q, want prefix %q", result.UserAgent, tc.wantPrefix)
			}
			if _, err := ParseAppServerVersionFromUserAgent(result.UserAgent); err != nil {
				t.Fatalf("ParseAppServerVersionFromUserAgent(%q) error = %v", result.UserAgent, err)
			}
		})
	}
}

func TestRuntimeRouterInitializeRejectsAlreadyInitializedConnection(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	first := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex_vscode", Version: "0.1.0"},
	})
	first.ConnectionID = "conn-a"
	if response := router.Handle(first); response.Error != nil {
		t.Fatalf("first initialize = %+v", response)
	}

	second := requestWithParams(t, IntID(2), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex_vscode", Version: "0.1.1"},
	})
	second.ConnectionID = "conn-a"
	if response := router.Handle(second); response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "Already initialized" {
		t.Fatalf("second initialize = %+v, want Already initialized", response)
	}

	otherConnection := requestWithParams(t, IntID(3), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex_vscode", Version: "0.1.0"},
	})
	otherConnection.ConnectionID = "conn-b"
	if response := router.Handle(otherConnection); response.Error != nil {
		t.Fatalf("other connection initialize = %+v", response)
	}

	router.ConnectionClosed("conn-a")
	reopened := requestWithParams(t, IntID(4), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex_vscode", Version: "0.1.2"},
	})
	reopened.ConnectionID = "conn-a"
	if response := router.Handle(reopened); response.Error != nil {
		t.Fatalf("reopened connection initialize = %+v", response)
	}
}

func TestRuntimeRouterRejectsExplicitConnectionBeforeInitialize(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	request := requestWithParams(t, IntID(1), MethodConfigRead, config.ConfigReadParams{})
	request.ConnectionID = "conn-a"
	if response := router.Handle(request); response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "Not initialized" {
		t.Fatalf("pre-initialize response = %+v, want Not initialized", response)
	}

	direct := router.Handle(requestWithParams(t, IntID(2), MethodConfigRead, config.ConfigReadParams{}))
	if direct.Error != nil {
		t.Fatalf("direct no-connection config/read = %+v", direct)
	}

	initialize := requestWithParams(t, IntID(3), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex_vscode", Version: "0.1.0"},
	})
	initialize.ConnectionID = "conn-a"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize = %+v", response)
	}
	afterInitialize := requestWithParams(t, IntID(4), MethodConfigRead, config.ConfigReadParams{})
	afterInitialize.ConnectionID = "conn-a"
	if response := router.Handle(afterInitialize); response.Error != nil {
		t.Fatalf("config/read after initialize = %+v", response)
	}
}

func setOriginatorOverrideForTest(t *testing.T, value *string) {
	t.Helper()
	previous, hadPrevious := os.LookupEnv(originatorOverrideEnv)
	if value == nil {
		if err := os.Unsetenv(originatorOverrideEnv); err != nil {
			t.Fatalf("Unsetenv(%s) error = %v", originatorOverrideEnv, err)
		}
	} else if err := os.Setenv(originatorOverrideEnv, *value); err != nil {
		t.Fatalf("Setenv(%s) error = %v", originatorOverrideEnv, err)
	}
	t.Cleanup(func() {
		if hadPrevious {
			_ = os.Setenv(originatorOverrideEnv, previous)
		} else {
			_ = os.Unsetenv(originatorOverrideEnv)
		}
	})
}

func TestRuntimeRouterInitializeOptOutNotificationMethodsFiltersStatusChanged(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadStatus: NewThreadStatusManager()})
	router.SetNotificationSink(sink)

	response := router.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex_vscode", Version: "0.1.0"},
		Capabilities: &InitializeCapabilities{
			OptOutNotificationMethods: []string{string(NotificationThreadStatusChanged)},
		},
	}))
	if response.Error != nil {
		t.Fatalf("initialize = %+v", response)
	}

	router.notifyThreadStatus(router.requireThreadStatus().NoteTurnStarted("thread-opt-out"))
	if sinkHasMethod(sink, NotificationThreadStatusChanged) {
		t.Fatalf("status changed notification should be filtered: %+v", sink.List())
	}

	router.notify(NotificationThreadStarted, &ThreadStartedNotification{Thread: &Thread{ID: "thread-opt-out"}})
	if !sinkHasMethod(sink, NotificationThreadStarted) {
		t.Fatalf("non-opted notification missing: %+v", sink.List())
	}
}

func TestRuntimeRouterExperimentalAPICapabilityGate(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-test", Version: "0.1.0"},
		Capabilities: &InitializeCapabilities{
			ExperimentalAPI: false,
		},
	})
	initialize.ConnectionID = "conn-no-experimental"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}

	assertExperimentalErrorForConnection := func(connectionID string, id int64, method Method, params any, reason string) {
		t.Helper()
		request := requestWithParams(t, IntID(id), method, params)
		request.ConnectionID = connectionID
		response := router.Handle(request)
		want := reason + " requires experimentalApi capability"
		if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != want {
			t.Fatalf("%s response = %+v, want %q", method, response, want)
		}
	}
	assertExperimentalError := func(id int64, method Method, params any, reason string) {
		t.Helper()
		assertExperimentalErrorForConnection("conn-no-experimental", id, method, params, reason)
	}

	assertExperimentalError(2, MethodMockExperimentalMethod, MockExperimentalMethodParams{Value: "x"}, "mock/experimentalMethod")
	assertExperimentalError(3, MethodThreadMemoryModeSet, ThreadMemoryModeSetParams{ThreadID: "thread-a", Mode: ThreadMemoryModeDisabled}, "thread/memoryMode/set")
	assertExperimentalError(4, MethodThreadSettingsUpdate, SettingsUpdateParams{ThreadID: "thread-a"}, "thread/settings/update")
	assertExperimentalError(5, MethodThreadRealtimeStart, map[string]any{"threadId": "thread-a"}, "thread/realtime/start")
	assertExperimentalError(6, MethodThreadStart, map[string]any{"cwd": t.TempDir(), "mockExperimentalField": "mock"}, "thread/start.mockExperimentalField")
	assertExperimentalError(7, MethodThreadStart, map[string]any{"cwd": t.TempDir(), "dynamicTools": []any{map[string]any{"namespace": "tools", "tools": []any{map[string]any{"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object"}}}}}}, "thread/start.dynamicTools")
	assertExperimentalError(8, MethodThreadStart, map[string]any{"cwd": t.TempDir(), "approvalPolicy": map[string]any{"granular": map[string]any{"sandbox_approval": true}}}, "askForApproval.granular")

	start := requestWithParams(t, IntID(9), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()})
	start.ConnectionID = "conn-no-experimental"
	if response := router.Handle(start); response.Error != nil {
		t.Fatalf("stable thread/start should be allowed: %+v", response.Error)
	}

	omittedCapabilities := requestWithParams(t, IntID(10), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-default-capabilities", Version: "0.1.0"},
	})
	omittedCapabilities.ConnectionID = "conn-default-capabilities"
	if response := router.Handle(omittedCapabilities); response.Error != nil {
		t.Fatalf("initialize with omitted capabilities error: %+v", response.Error)
	}
	assertExperimentalErrorForConnection("conn-default-capabilities", 11, MethodMockExperimentalMethod, MockExperimentalMethodParams{Value: "x"}, "mock/experimentalMethod")

	nullCapabilities := &Request{
		JSONRPC:      "2.0",
		ID:           IntID(12),
		Method:       MethodInitialize,
		Params:       json.RawMessage(`{"clientInfo":{"name":"codex-null-capabilities","version":"0.1.0"},"capabilities":null}`),
		ConnectionID: "conn-null-capabilities",
	}
	if response := router.Handle(nullCapabilities); response.Error != nil {
		t.Fatalf("initialize with null capabilities error: %+v", response.Error)
	}
	assertExperimentalErrorForConnection("conn-null-capabilities", 13, MethodMockExperimentalMethod, MockExperimentalMethodParams{Value: "x"}, "mock/experimentalMethod")
}

func TestRuntimeRouterInitializeMCPServerOpenAIFormCapabilityAdvertisesExtension(t *testing.T) {
	initializeParams := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			initializeParams <- request.Params
			w.Header().Set("Mcp-Session-Id", "session-1")
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "docs", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		case "resources/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"resources": []any{}})
		case "resources/templates/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []any{}})
		default:
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{})
		}
	}))
	defer server.Close()

	router := NewRuntimeRouter(RuntimeServices{
		MCP: mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
			"docs": {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
		}}),
	})

	initializeDefault := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-default-mcp-form", Version: "0.1.0"},
	})
	initializeDefault.ConnectionID = "conn-default-mcp-form"
	if response := router.Handle(initializeDefault); response.Error != nil {
		t.Fatalf("initialize default error: %+v", response.Error)
	}
	statusDefault := requestWithParams(t, IntID(2), MethodMCPServerStatusList, mcp.MCPListServerStatusParams{
		Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailFull},
	})
	statusDefault.ConnectionID = "conn-default-mcp-form"
	if response := router.Handle(statusDefault); response.Error != nil {
		t.Fatalf("status default error: %+v", response.Error)
	}
	assertRuntimeRouterMCPInitializeOpenAIFormExtension(t, <-initializeParams, false)

	initializeOpenAIForm := requestWithParams(t, IntID(3), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-openai-form", Version: "0.1.0"},
		Capabilities: &InitializeCapabilities{
			MCPServerOpenAIFormElicitation: true,
		},
	})
	initializeOpenAIForm.ConnectionID = "conn-openai-form"
	if response := router.Handle(initializeOpenAIForm); response.Error != nil {
		t.Fatalf("initialize openai/form error: %+v", response.Error)
	}
	statusOpenAIForm := requestWithParams(t, IntID(4), MethodMCPServerStatusList, mcp.MCPListServerStatusParams{
		Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailFull},
	})
	statusOpenAIForm.ConnectionID = "conn-openai-form"
	if response := router.Handle(statusOpenAIForm); response.Error != nil {
		t.Fatalf("status openai/form error: %+v", response.Error)
	}
	assertRuntimeRouterMCPInitializeOpenAIFormExtension(t, <-initializeParams, true)
}

func writeRuntimeRouterMCPResponse(t *testing.T, w http.ResponseWriter, id int64, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}

func assertRuntimeRouterMCPInitializeOpenAIFormExtension(t *testing.T, params map[string]any, want bool) {
	t.Helper()
	capabilities, ok := params["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %#v", params["capabilities"])
	}
	extensions, _ := capabilities["extensions"].(map[string]any)
	openAIForm, ok := extensions["openai/form"].(map[string]any)
	if want {
		if !ok || len(openAIForm) != 0 {
			t.Fatalf("openai/form extension = %#v", extensions["openai/form"])
		}
		return
	}
	if ok {
		t.Fatalf("openai/form extension should be absent: %#v", extensions["openai/form"])
	}
}

func TestRuntimeRouterAccountCancelAndLogoutNotify(t *testing.T) {
	sink := NewNotificationBuffer()
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{Account: auth.NewAccountManager(), Config: config.NewConfigService(home)})
	router.SetNotificationSink(sink)

	login := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: auth.AccountChatGPT}))
	if login.Error != nil {
		t.Fatalf("login = %+v", login)
	}
	loginID := login.Result.(*auth.LoginAccountResponse).LoginID
	cancel := router.Handle(requestWithParams(t, IntID(2), MethodCancelLoginAccount, auth.CancelLoginAccountParams{LoginID: loginID}))
	if cancel.Error != nil || cancel.Result.(*auth.CancelLoginAccountResponse).Status != auth.CancelLoginCanceled {
		t.Fatalf("cancel = %+v", cancel)
	}
	if !sinkHasMethod(sink, NotificationAccountLoginCompleted) {
		t.Fatalf("cancel notification missing: %+v", sink.List())
	}

	apiLogin := router.Handle(requestWithParams(t, IntID(3), MethodLoginAccount, auth.LoginAccountParams{Type: auth.AccountAPIKey, APIKey: "sk-test"}))
	if apiLogin.Error != nil {
		t.Fatalf("api login = %+v", apiLogin)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); err != nil {
		t.Fatalf("auth.json after login stat error: %v", err)
	}
	logout := router.Handle(requestWithParams(t, IntID(4), MethodLogoutAccount, map[string]any{}))
	if logout.Error != nil {
		t.Fatalf("logout = %+v", logout)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json after logout stat error = %v, want not exist", err)
	}
	if got := router.services.Account.GetAccount(&auth.GetAccountParams{}); got.Account != nil || !got.RequiresOpenAIAuth {
		t.Fatalf("account after logout = %+v", got)
	}
	if !sinkHasMethod(sink, NotificationAccountUpdated) {
		t.Fatalf("logout account/updated missing: %+v", sink.List())
	}
}

func TestRuntimeRouterAccountSessionsRPC(t *testing.T) {
	sink := NewNotificationBuffer()
	manager := auth.NewAccountManager()
	active := "session-1"
	manager.SetSessions(&active, []auth.Session{
		{
			SessionID: "session-1",
			IsActive:  true,
			Workspaces: []auth.SessionWorkspace{{
				AccountID: "account-1",
			}},
		},
		{
			SessionID: "session-2",
			Workspaces: []auth.SessionWorkspace{{
				AccountID: "account-2",
			}},
		},
	})
	router := NewRuntimeRouter(RuntimeServices{Account: manager})
	router.SetNotificationSink(sink)

	list := router.Handle(requestWithParams(t, IntID(1), MethodAccountSessionsList, auth.AccountSessionsListParams{RefreshWorkspaceMetadata: true}))
	if list.Error != nil {
		t.Fatalf("sessions list = %+v", list)
	}
	if got := list.Result.(*auth.AccountSessionsResponse); got.ActiveSessionID == nil || *got.ActiveSessionID != "session-1" || len(got.Sessions) != 2 {
		t.Fatalf("sessions list result = %+v", got)
	}

	add := router.Handle(requestWithParams(t, IntID(2), MethodAccountSessionsAdd, auth.AccountSessionsAddParams{SwitchToAddedAccount: true}))
	if add.Error != nil || len(add.Result.(*auth.AccountSessionsResponse).Sessions) != 2 {
		t.Fatalf("sessions add = %+v", add)
	}

	switched := router.Handle(requestWithParams(t, IntID(3), MethodAccountSessionsSwitch, auth.AccountSessionsSwitchParams{
		SessionID: "session-2",
		AccountID: "account-2",
	}))
	if switched.Error != nil {
		t.Fatalf("sessions switch = %+v", switched)
	}
	switchResult := switched.Result.(*auth.AccountSessionsResponse)
	if switchResult.ActiveSessionID == nil || *switchResult.ActiveSessionID != "session-2" || !switchResult.Sessions[1].IsActive {
		t.Fatalf("sessions switch result = %+v", switchResult)
	}
	if !sinkHasMethod(sink, NotificationAccountUpdated) {
		t.Fatalf("sessions switch did not notify account/updated: %+v", sink.List())
	}

	missing := router.Handle(requestWithParams(t, IntID(4), MethodAccountSessionsSwitch, auth.AccountSessionsSwitchParams{
		SessionID: "session-2",
		AccountID: "missing",
	}))
	if missing.Error == nil || missing.Error.Code != -32602 {
		t.Fatalf("missing workspace switch = %+v", missing)
	}

	loggedOut := router.Handle(requestWithParams(t, IntID(5), MethodAccountSessionsLogout, auth.AccountSessionsLogoutParams{SessionID: "session-2"}))
	if loggedOut.Error != nil {
		t.Fatalf("sessions logout = %+v", loggedOut)
	}
	logoutResult := loggedOut.Result.(*auth.AccountSessionsResponse)
	if logoutResult.ActiveSessionID == nil || *logoutResult.ActiveSessionID != "session-1" || len(logoutResult.Sessions) != 1 {
		t.Fatalf("sessions logout result = %+v", logoutResult)
	}
}

func TestRuntimeRouterLogoutRevokesStoredChatGPTToken(t *testing.T) {
	home := t.TempDir()
	if err := auth.PersistChatGPTTokens(home, &auth.ExchangedTokens{
		IDToken:      fakeJWTAppserver(map[string]any{"chatgpt_account_id": "account-1"}),
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/revoke" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		writeJSON(t, w, map[string]string{"message": "success"})
	}))
	defer server.Close()
	t.Setenv(auth.RevokeTokenURLEnvOverride, server.URL+"/oauth/revoke")
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})

	logout := router.Handle(requestWithParams(t, IntID(1), MethodLogoutAccount, map[string]any{}))
	if logout.Error != nil {
		t.Fatalf("logout = %+v", logout)
	}
	if requestBody["token"] != "refresh-token" || requestBody["token_type_hint"] != "refresh_token" {
		t.Fatalf("revoke request = %#v", requestBody)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json after logout stat error = %v, want not exist", err)
	}
}

func TestRuntimeRouterSendAddCreditsNudgeEmailUsesChatGPTBackend(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("chatgpt-token", "account-123", nil)); err != nil {
		t.Fatalf("Save auth error = %v", err)
	}
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/accounts/send_add_credits_nudge_email" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer chatgpt-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "account-123" {
			t.Fatalf("ChatGPT-Account-ID = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
		t.Fatalf("Write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodSendAddCreditsNudgeEmail, auth.SendAddCreditsNudgeEmailParams{
		CreditType: auth.AddCreditsNudgeUsageLimit,
	}))
	if response.Error != nil {
		t.Fatalf("send nudge error = %+v", response.Error)
	}
	if response.Result.(*auth.SendAddCreditsNudgeEmailResponse).Status != auth.AddCreditsNudgeEmailSent {
		t.Fatalf("send nudge response = %+v", response.Result)
	}
	if requestBody["credit_type"] != "usage_limit" {
		t.Fatalf("request body = %#v", requestBody)
	}
}

func TestRuntimeRouterSendAddCreditsNudgeEmailMapsCooldown(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("chatgpt-token", "account-123", nil)); err != nil {
		t.Fatalf("Save auth error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
		t.Fatalf("Write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodSendAddCreditsNudgeEmail, auth.SendAddCreditsNudgeEmailParams{
		CreditType: auth.AddCreditsNudgeCredits,
	}))
	if response.Error != nil {
		t.Fatalf("send nudge cooldown error = %+v", response.Error)
	}
	if response.Result.(*auth.SendAddCreditsNudgeEmailResponse).Status != auth.AddCreditsNudgeEmailCooldownActive {
		t.Fatalf("send nudge cooldown response = %+v", response.Result)
	}
}

func TestRuntimeRouterSendAddCreditsNudgeEmailHydratesPersonalAccessTokenRouting(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAccessToken("at-test-token")); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/user-auth-credential/whoami" {
			t.Fatalf("auth path = %q", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"chatgpt_user_id":            "user-pat",
			"chatgpt_account_id":         "account-pat",
			"chatgpt_plan_type":          "team",
			"chatgpt_account_is_fedramp": true,
		})
	}))
	defer authServer.Close()
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/accounts/send_add_credits_nudge_email" {
			t.Fatalf("backend path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer at-test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "account-pat" {
			t.Fatalf("ChatGPT-Account-ID = %q", got)
		}
		if got := r.Header.Get("X-OpenAI-Fedramp"); got != "true" {
			t.Fatalf("X-OpenAI-Fedramp = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backendServer.Close()
	t.Setenv(auth.AuthAPIBaseURLEnv, authServer.URL)
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+backendServer.URL+`"`), 0o600); err != nil {
		t.Fatalf("Write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Account: auth.NewAccountManager(), Config: config.NewConfigService(home)})

	response := router.Handle(requestWithParams(t, IntID(1), MethodSendAddCreditsNudgeEmail, auth.SendAddCreditsNudgeEmailParams{
		CreditType: auth.AddCreditsNudgeCredits,
	}))
	if response.Error != nil {
		t.Fatalf("send nudge response = %+v", response)
	}
}

func TestRuntimeRouterSendAddCreditsNudgeEmailRequiresChatGPTAuth(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})

	missing := router.Handle(requestWithParams(t, IntID(1), MethodSendAddCreditsNudgeEmail, auth.SendAddCreditsNudgeEmailParams{
		CreditType: auth.AddCreditsNudgeCredits,
	}))
	if missing.Error == nil || missing.Error.Code != -32600 || missing.Error.Message != "codex account authentication required to notify workspace owner" {
		t.Fatalf("missing auth response = %+v", missing.Error)
	}

	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save api key auth error = %v", err)
	}
	apiKey := router.Handle(requestWithParams(t, IntID(2), MethodSendAddCreditsNudgeEmail, auth.SendAddCreditsNudgeEmailParams{
		CreditType: auth.AddCreditsNudgeUsageLimit,
	}))
	if apiKey.Error == nil || apiKey.Error.Code != -32600 || apiKey.Error.Message != "chatgpt authentication required to notify workspace owner" {
		t.Fatalf("api key auth response = %+v", apiKey.Error)
	}
}

func TestRuntimeRouterConsumeRateLimitResetCreditUsesChatGPTBackend(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("chatgpt-token", "account-123", nil)); err != nil {
		t.Fatalf("Save auth error = %v", err)
	}
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/rate-limit-reset-credits/consume" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer chatgpt-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "account-123" {
			t.Fatalf("ChatGPT-Account-ID = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		writeJSON(t, w, map[string]any{"code": "reset", "windows_reset": 2})
	}))
	defer server.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
		t.Fatalf("Write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodConsumeAccountRateLimitResetCredit, auth.ConsumeRateLimitResetCreditParams{
		IdempotencyKey: "request-1",
	}))
	if response.Error != nil {
		t.Fatalf("consume reset credit error = %+v", response.Error)
	}
	if response.Result.(*auth.ConsumeRateLimitResetCreditResponse).Outcome != auth.ResetCreditOutcomeReset {
		t.Fatalf("consume reset credit response = %+v", response.Result)
	}
	if requestBody["redeem_request_id"] != "request-1" {
		t.Fatalf("request body = %#v", requestBody)
	}
}

func TestRuntimeRouterConsumeRateLimitResetCreditHydratesPersonalAccessTokenRouting(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAccessToken("at-test-token")); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"chatgpt_user_id":            "user-pat",
			"chatgpt_account_id":         "account-pat",
			"chatgpt_plan_type":          "team",
			"chatgpt_account_is_fedramp": true,
		})
	}))
	defer authServer.Close()
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/rate-limit-reset-credits/consume" {
			t.Fatalf("backend path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer at-test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "account-pat" {
			t.Fatalf("ChatGPT-Account-ID = %q", got)
		}
		if got := r.Header.Get("X-OpenAI-Fedramp"); got != "true" {
			t.Fatalf("X-OpenAI-Fedramp = %q", got)
		}
		writeJSON(t, w, map[string]any{"code": "reset"})
	}))
	defer backendServer.Close()
	t.Setenv(auth.AuthAPIBaseURLEnv, authServer.URL)
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+backendServer.URL+`"`), 0o600); err != nil {
		t.Fatalf("Write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Account: auth.NewAccountManager(), Config: config.NewConfigService(home)})

	response := router.Handle(requestWithParams(t, IntID(1), MethodConsumeAccountRateLimitResetCredit, auth.ConsumeRateLimitResetCreditParams{
		IdempotencyKey: "request-1",
	}))
	if response.Error != nil {
		t.Fatalf("consume reset credit response = %+v", response)
	}
	if response.Result.(*auth.ConsumeRateLimitResetCreditResponse).Outcome != auth.ResetCreditOutcomeReset {
		t.Fatalf("consume reset credit response = %+v", response.Result)
	}
}

func TestRuntimeRouterConsumeRateLimitResetCreditMapsOutcomes(t *testing.T) {
	cases := []struct {
		code    string
		outcome auth.ConsumeRateLimitResetCreditOutcome
	}{
		{code: "reset", outcome: auth.ResetCreditOutcomeReset},
		{code: "nothing_to_reset", outcome: auth.ResetCreditOutcomeNothingToReset},
		{code: "no_credit", outcome: auth.ResetCreditOutcomeNoCredit},
		{code: "already_redeemed", outcome: auth.ResetCreditOutcomeAlreadyRedeemed},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			home := t.TempDir()
			if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("chatgpt-token", "account-123", nil)); err != nil {
				t.Fatalf("Save auth error = %v", err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, map[string]any{"code": tc.code, "windows_reset": 0})
			}))
			defer server.Close()
			if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
				t.Fatalf("Write config error = %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{
				Account: auth.NewAccountManager(),
				Config:  config.NewConfigService(home),
			})

			response := router.Handle(requestWithParams(t, IntID(1), MethodConsumeAccountRateLimitResetCredit, auth.ConsumeRateLimitResetCreditParams{
				IdempotencyKey: "request-1",
			}))
			if response.Error != nil {
				t.Fatalf("consume reset credit error = %+v", response.Error)
			}
			if response.Result.(*auth.ConsumeRateLimitResetCreditResponse).Outcome != tc.outcome {
				t.Fatalf("consume reset credit response = %+v", response.Result)
			}
		})
	}
}

func TestRuntimeRouterConsumeRateLimitResetCreditRequiresChatGPTAuth(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})

	empty := router.Handle(requestWithParams(t, IntID(1), MethodConsumeAccountRateLimitResetCredit, auth.ConsumeRateLimitResetCreditParams{}))
	if empty.Error == nil || empty.Error.Code != -32600 || empty.Error.Message != "idempotencyKey must not be empty" {
		t.Fatalf("empty key response = %+v", empty.Error)
	}
	missing := router.Handle(requestWithParams(t, IntID(2), MethodConsumeAccountRateLimitResetCredit, auth.ConsumeRateLimitResetCreditParams{
		IdempotencyKey: "request-1",
	}))
	if missing.Error == nil || missing.Error.Code != -32600 || missing.Error.Message != "codex account authentication required for rate limit reset credits" {
		t.Fatalf("missing auth response = %+v", missing.Error)
	}

	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save api key auth error = %v", err)
	}
	apiKey := router.Handle(requestWithParams(t, IntID(3), MethodConsumeAccountRateLimitResetCredit, auth.ConsumeRateLimitResetCreditParams{
		IdempotencyKey: "request-2",
	}))
	if apiKey.Error == nil || apiKey.Error.Code != -32600 || apiKey.Error.Message != "chatgpt authentication required for rate limit reset credits" {
		t.Fatalf("api key auth response = %+v", apiKey.Error)
	}
}

func TestRuntimeRouterAccountBackendReadsRequireChatGPTAuth(t *testing.T) {
	clearAuthEnvAppserver(t)
	cases := []struct {
		method   Method
		resource string
		result   func(*Response) bool
	}{
		{
			method:   MethodGetAccountRateLimits,
			resource: "rate limits",
			result: func(response *Response) bool {
				_, ok := response.Result.(*auth.GetAccountRateLimitsResponse)
				return ok
			},
		},
		{
			method:   MethodGetAccountTokenUsage,
			resource: "token usage",
			result: func(response *Response) bool {
				_, ok := response.Result.(*auth.GetAccountTokenUsageResponse)
				return ok
			},
		},
		{
			method:   MethodGetWorkspaceMessages,
			resource: "workspace messages",
			result: func(response *Response) bool {
				_, ok := response.Result.(*auth.GetWorkspaceMessagesResponse)
				return ok
			},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.method)+" without auth", func(t *testing.T) {
			home := t.TempDir()
			router := NewRuntimeRouter(RuntimeServices{
				Account: auth.NewAccountManager(),
				Config:  config.NewConfigService(home),
			})
			response := router.Handle(requestWithParams(t, IntID(1), tc.method, map[string]any{}))
			expected := "codex account authentication required to read " + tc.resource
			if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != expected {
				t.Fatalf("response = %+v, want %q", response, expected)
			}
		})

		t.Run(string(tc.method)+" with api key", func(t *testing.T) {
			home := t.TempDir()
			if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
				t.Fatalf("auth save error: %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{
				Account: auth.NewAccountManager(),
				Config:  config.NewConfigService(home),
			})
			response := router.Handle(requestWithParams(t, IntID(1), tc.method, map[string]any{}))
			expected := "chatgpt authentication required to read " + tc.resource
			if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != expected {
				t.Fatalf("response = %+v, want %q", response, expected)
			}
		})

		t.Run(string(tc.method)+" with chatgpt tokens", func(t *testing.T) {
			home := t.TempDir()
			plan := "pro"
			if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens(
				"chatgpt-token",
				"account-1",
				&plan,
			)); err != nil {
				t.Fatalf("auth save error: %v", err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer chatgpt-token" {
					t.Fatalf("Authorization = %q", got)
				}
				if got := r.Header.Get("ChatGPT-Account-ID"); got != "account-1" {
					t.Fatalf("ChatGPT-Account-ID = %q", got)
				}
				switch r.URL.Path {
				case "/api/codex/usage":
					writeJSON(t, w, map[string]any{
						"plan_type": "pro",
						"rate_limit": map[string]any{
							"primary_window": map[string]any{
								"used_percent":         42.5,
								"limit_window_seconds": 3600,
								"reset_at":             1735693200,
							},
						},
						"rate_limit_reset_credits": map[string]any{"available_count": 3},
					})
				case "/api/codex/profiles/me":
					writeJSON(t, w, map[string]any{
						"stats": map[string]any{
							"lifetime_tokens":          123,
							"peak_daily_tokens":        45,
							"longest_running_turn_sec": 67,
							"current_streak_days":      8,
							"longest_streak_days":      9,
							"daily_usage_buckets": []map[string]any{{
								"start_date": "2026-05-29",
								"tokens":     10,
							}},
						},
					})
				case "/api/codex/workspace-messages":
					writeJSON(t, w, map[string]any{
						"messages": []map[string]any{{
							"message_id":   "headline-id",
							"message_type": "headline",
							"message_body": "Headline body",
							"created_at":   "2026-06-14T00:00:00Z",
							"archived_at":  "2026-06-15T00:00:00Z",
						}},
					})
				default:
					t.Fatalf("path = %q", r.URL.Path)
				}
			}))
			defer server.Close()
			if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
				t.Fatalf("Write config error = %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{
				Account: auth.NewAccountManager(),
				Config:  config.NewConfigService(home),
			})
			response := router.Handle(requestWithParams(t, IntID(1), tc.method, map[string]any{}))
			if response.Error != nil || !tc.result(response) {
				t.Fatalf("response = %+v", response)
			}
			if tc.method == MethodGetAccountRateLimits {
				limits := response.Result.(*auth.GetAccountRateLimitsResponse)
				if limits.RateLimits.Primary == nil || limits.RateLimits.Primary.UsedPercent != 43 || limits.RateLimitResetCredits.AvailableCount != 3 {
					t.Fatalf("rate limits = %+v", limits)
				}
				data, err := json.Marshal(limits)
				if err != nil {
					t.Fatalf("Marshal rate limits error: %v", err)
				}
				if !strings.Contains(string(data), `"usedPercent":43`) || strings.Contains(string(data), `"usedPercent":42.5`) {
					t.Fatalf("rate limits JSON = %s", data)
				}
			}
			if tc.method == MethodGetAccountTokenUsage {
				usage := response.Result.(*auth.GetAccountTokenUsageResponse)
				if usage.Summary.LifetimeTokens == nil || *usage.Summary.LifetimeTokens != 123 || len(usage.DailyUsageBuckets) != 1 || usage.DailyUsageBuckets[0].Tokens != 10 {
					t.Fatalf("usage = %+v", usage)
				}
			}
			if tc.method == MethodGetWorkspaceMessages {
				messages := response.Result.(*auth.GetWorkspaceMessagesResponse)
				if !messages.FeatureEnabled || len(messages.Messages) != 1 || messages.Messages[0].CreatedAt == nil || *messages.Messages[0].CreatedAt != 1781395200 {
					t.Fatalf("messages = %+v", messages)
				}
			}
		})
	}
}

func TestRuntimeRouterPersonalAccessTokenBackendReadsHydrateAccountRouting(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAccessToken("at-test-token")); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	authRequests := 0
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authRequests++
		if r.URL.Path != "/v1/user-auth-credential/whoami" {
			t.Fatalf("auth path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer at-test-token" {
			t.Fatalf("auth Authorization = %q", got)
		}
		writeJSON(t, w, map[string]any{
			"email":                      "pat@example.com",
			"chatgpt_user_id":            "user-pat",
			"chatgpt_account_id":         "account-pat",
			"chatgpt_plan_type":          "team",
			"chatgpt_account_is_fedramp": true,
		})
	}))
	defer authServer.Close()
	backendRequests := 0
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendRequests++
		if r.URL.Path != "/api/codex/usage" {
			t.Fatalf("backend path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer at-test-token" {
			t.Fatalf("backend Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "account-pat" {
			t.Fatalf("backend ChatGPT-Account-ID = %q", got)
		}
		if got := r.Header.Get("X-OpenAI-Fedramp"); got != "true" {
			t.Fatalf("backend X-OpenAI-Fedramp = %q", got)
		}
		writeJSON(t, w, map[string]any{
			"plan_type": "team",
			"rate_limit": map[string]any{
				"primary_window": map[string]any{
					"used_percent":         11,
					"limit_window_seconds": 3600,
					"reset_at":             1735693200,
				},
			},
		})
	}))
	defer backendServer.Close()
	t.Setenv(auth.AuthAPIBaseURLEnv, authServer.URL)
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+backendServer.URL+`"`), 0o600); err != nil {
		t.Fatalf("Write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccountRateLimits, map[string]any{}))
	if response.Error != nil {
		t.Fatalf("rate limits response = %+v", response)
	}
	if authRequests != 1 || backendRequests != 1 {
		t.Fatalf("requests auth=%d backend=%d, want 1/1", authRequests, backendRequests)
	}
}

func TestRuntimeRouterAccountBackendClientConstructionErrorIsWrapped(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	keyMaterial, err := agent.GenerateAgentKeyMaterial()
	if err != nil {
		t.Fatalf("GenerateAgentKeyMaterial() error = %v", err)
	}
	if err := auth.NewStore(home).Save(auth.AuthDotJSON{
		AuthMode: "agent-identity",
		AgentIdentity: &auth.AgentIdentityAuthRecord{
			AgentRuntimeID:  "agent-runtime",
			AgentPrivateKey: keyMaterial.PrivateKeyPKCS8Base64,
			AccountID:       "account-1",
			ChatGPTUserID:   "user-1",
		},
	}); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccountRateLimits, map[string]any{}))
	if response.Error == nil || response.Error.Code != -32603 || response.Error.Message != "failed to construct backend client: agent identity auth is missing task id" {
		t.Fatalf("response = %+v", response)
	}
}

func TestRuntimeRouterAccountBackendTimeoutsMatchRust(t *testing.T) {
	if accountTokenUsageFetchTimeout() != 10*time.Second {
		t.Fatalf("token usage timeout = %s, want 10s", accountTokenUsageFetchTimeout())
	}
	if accountWorkspaceMessagesFetchTimeout() != time.Second {
		t.Fatalf("workspace messages timeout = %s, want 1s", accountWorkspaceMessagesFetchTimeout())
	}
}

func TestRuntimeRouterWorkspaceMessagesNotFoundDisablesFeature(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("chatgpt-token", "account-1", nil)); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/workspace-messages" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
		t.Fatalf("Write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetWorkspaceMessages, map[string]any{}))
	if response.Error != nil {
		t.Fatalf("workspace messages response = %+v", response)
	}
	messages := response.Result.(*auth.GetWorkspaceMessagesResponse)
	if messages.FeatureEnabled || len(messages.Messages) != 0 {
		t.Fatalf("messages = %+v, want disabled empty", messages)
	}
}

func TestRuntimeRouterExternalChatGPTTokensPersistAndNotify(t *testing.T) {
	home := t.TempDir()
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
	router.SetNotificationSink(sink)
	plan := "pro"
	login := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{
		Type:             "chatgptAuthTokens",
		AccessToken:      fakeJWTAppserver(map[string]any{"email": "external@example.com", "plan_type": "plus"}),
		ChatGPTAccountID: "account-1",
		ChatGPTPlanType:  &plan,
	}))
	if login.Error != nil || login.Result.(*auth.LoginAccountResponse).Type != "chatgptAuthTokens" {
		t.Fatalf("login = %+v", login)
	}
	read := router.Handle(requestWithParams(t, IntID(2), MethodGetAccount, auth.GetAccountParams{RefreshToken: true}))
	if read.Error != nil {
		t.Fatalf("account read = %+v", read)
	}
	account := read.Result.(*auth.GetAccountResponse).Account
	if account == nil || account.Email == nil || *account.Email != "external@example.com" || account.PlanType != auth.PlanPro {
		t.Fatalf("account = %+v", read.Result)
	}
	loaded, err := auth.NewStore(home).Load()
	if err != nil {
		t.Fatalf("auth load error: %v", err)
	}
	if loaded == nil || loaded.Mode() != "chatgptAuthTokens" || loaded.Tokens["account_id"] != "account-1" || loaded.Tokens["plan_type"] != "pro" {
		t.Fatalf("persisted auth = %+v", loaded)
	}
	if !sinkHasMethod(sink, NotificationAccountUpdated) {
		t.Fatalf("account updated notification missing: %+v", sink.List())
	}
}

func TestRuntimeRouterExternalChatGPTTokensCancelActiveLogin(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
	login := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: auth.AccountChatGPT}))
	if login.Error != nil {
		t.Fatalf("login = %+v", login)
	}
	loginID := login.Result.(*auth.LoginAccountResponse).LoginID
	plan := "pro"
	external := router.Handle(requestWithParams(t, IntID(2), MethodLoginAccount, auth.LoginAccountParams{
		Type:             "chatgptAuthTokens",
		AccessToken:      fakeJWTAppserver(map[string]any{"email": "external@example.com", "plan_type": "pro"}),
		ChatGPTAccountID: "account-1",
		ChatGPTPlanType:  &plan,
	}))
	if external.Error != nil {
		t.Fatalf("external auth = %+v", external)
	}
	cancel := router.Handle(requestWithParams(t, IntID(3), MethodCancelLoginAccount, auth.CancelLoginAccountParams{LoginID: loginID}))
	if cancel.Error != nil {
		t.Fatalf("cancel = %+v", cancel)
	}
	if cancel.Result.(*auth.CancelLoginAccountResponse).Status != auth.CancelLoginNotFound {
		t.Fatalf("cancel after external auth = %+v", cancel.Result)
	}
}

func TestRuntimeRouterExternalChatGPTTokensRejectsForcedWorkspaceMismatch(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`forced_chatgpt_workspace_id = ["workspace-allowed"]`), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
	login := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{
		Type:             "chatgptAuthTokens",
		AccessToken:      fakeJWTAppserver(map[string]any{"email": "external@example.com"}),
		ChatGPTAccountID: "workspace-denied",
	}))
	if login.Error == nil || !strings.Contains(login.Error.Message, "External auth must use one of workspace(s)") {
		t.Fatalf("login should reject forced workspace mismatch: %+v", login)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json should not exist after rejected login: %v", err)
	}
}

func TestRuntimeRouterChatGPTLoginIncludesForcedWorkspaceQuery(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "single",
			body: `forced_chatgpt_workspace_id = "workspace-allowed"`,
			want: "workspace-allowed",
		},
		{
			name: "allowlist",
			body: `forced_chatgpt_workspace_id = ["workspace-allowed", "workspace-second"]`,
			want: "workspace-allowed,workspace-second",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(config.ConfigPath(home), []byte(tc.body+"\n"), 0o600); err != nil {
				t.Fatalf("WriteFile config returned error: %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{
				Account: auth.NewAccountManager(),
				Config:  config.NewConfigService(home),
			})
			response := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: auth.AccountChatGPT}))
			if response.Error != nil {
				t.Fatalf("login response = %+v", response)
			}
			login := response.Result.(*auth.LoginAccountResponse)
			parsed, err := url.Parse(login.AuthURL)
			if err != nil {
				t.Fatalf("Parse auth URL %q: %v", login.AuthURL, err)
			}
			if got := parsed.Query().Get("allowed_workspace_id"); got != tc.want {
				t.Fatalf("allowed_workspace_id = %q, want %q; url=%s", got, tc.want, login.AuthURL)
			}
		})
	}
}

func TestRuntimeRouterRejectsLoginAgainstForcedMethod(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		params  auth.LoginAccountParams
		message string
	}{
		{
			name:    "api key disabled",
			config:  `forced_login_method = "chatgpt"`,
			params:  auth.LoginAccountParams{Type: auth.AccountAPIKey, APIKey: "sk-test"},
			message: "API key login is disabled. Use ChatGPT login instead.",
		},
		{
			name:    "chatgpt disabled",
			config:  `forced_login_method = "api"`,
			params:  auth.LoginAccountParams{Type: auth.AccountChatGPT},
			message: "ChatGPT login is disabled. Use API key login instead.",
		},
		{
			name:   "external chatgpt disabled",
			config: `forced_login_method = "api"`,
			params: auth.LoginAccountParams{
				Type:             "chatgptAuthTokens",
				AccessToken:      fakeJWTAppserver(map[string]any{"email": "external@example.com"}),
				ChatGPTAccountID: "account-1",
			},
			message: "External ChatGPT auth is disabled. Use API key login instead.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(config.ConfigPath(home), []byte(tc.config+"\n"), 0o600); err != nil {
				t.Fatalf("WriteFile config returned error: %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{
				Account: auth.NewAccountManager(),
				Config:  config.NewConfigService(home),
			})
			response := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, tc.params))
			if response.Error == nil || response.Error.Message != tc.message {
				t.Fatalf("login response = %+v, want error %q", response, tc.message)
			}
		})
	}
}

func TestRuntimeRouterRejectsReplacingActiveExternalChatGPTAuth(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	plan := "pro"
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens(
		fakeJWTAppserver(map[string]any{"email": "external@example.com", "plan_type": "pro"}),
		"account-1",
		&plan,
	)); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Account: auth.NewAccountManager()})
	for _, params := range []auth.LoginAccountParams{
		{Type: auth.AccountAPIKey, APIKey: "sk-test"},
		{Type: auth.AccountChatGPT},
		{Type: "chatgptDeviceCode"},
	} {
		response := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, params))
		if response.Error == nil || response.Error.Message != externalChatGPTAuthActiveMessage {
			t.Fatalf("login %s response = %+v", params.Type, response)
		}
	}
	update := router.Handle(requestWithParams(t, IntID(2), MethodLoginAccount, auth.LoginAccountParams{
		Type:             "chatgptAuthTokens",
		AccessToken:      fakeJWTAppserver(map[string]any{"email": "new@example.com", "plan_type": "pro"}),
		ChatGPTAccountID: "account-2",
		ChatGPTPlanType:  &plan,
	}))
	if update.Error != nil {
		t.Fatalf("external auth update = %+v", update)
	}
}

func TestRuntimeRouterEnforcesStoredAuthForcedLoginMethod(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`forced_login_method = "chatgpt"`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Account: auth.NewAccountManager()})
	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAuthStatus, AuthStatusParams{}))
	if response.Error != nil {
		t.Fatalf("get auth status = %+v", response)
	}
	status := response.Result.(*AuthStatusResponse)
	if status.Authenticated || status.AuthMethod != nil {
		t.Fatalf("status = %+v, want logged out", status)
	}
	if loaded, err := auth.NewStore(home).Load(); err != nil || loaded != nil {
		t.Fatalf("auth store after restriction = %+v, err=%v", loaded, err)
	}
}

func TestRuntimeRouterEnforcesStoredAuthForcedWorkspace(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`forced_chatgpt_workspace_id = ["workspace-allowed"]`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	snapshot := auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token": fakeJWTAppserver(map[string]any{"chatgpt_account_id": "workspace-denied"}),
			"account_id":   "workspace-denied",
		},
	}
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Account: auth.NewAccountManager()})
	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error != nil {
		t.Fatalf("get account = %+v", response)
	}
	account := response.Result.(*auth.GetAccountResponse)
	if account.Account != nil || !account.RequiresOpenAIAuth {
		t.Fatalf("account = %+v, want logged out requiring auth", account)
	}
	if loaded, err := auth.NewStore(home).Load(); err != nil || loaded != nil {
		t.Fatalf("auth store after restriction = %+v, err=%v", loaded, err)
	}
}

func TestRuntimeRouterForcedLoginMethodRejectsEnvAuth(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`forced_login_method = "chatgpt"`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	t.Setenv(auth.OpenAIAPIKeyEnv, "sk-env")
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Account: auth.NewAccountManager()})
	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAuthStatus, AuthStatusParams{}))
	if response.Error == nil || !strings.Contains(response.Error.Message, "ChatGPT login is required, but an API key is currently being used") {
		t.Fatalf("response = %+v", response)
	}
}

func TestRuntimeRouterGetAuthStatusReadsAuthStore(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-status")); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})

	withoutToken := router.Handle(requestWithParams(t, IntID(1), MethodGetAuthStatus, AuthStatusParams{}))
	if withoutToken.Error != nil {
		t.Fatalf("get auth status = %+v", withoutToken)
	}
	status := withoutToken.Result.(*AuthStatusResponse)
	if !status.Authenticated || status.AuthMethod == nil || *status.AuthMethod != string(AuthModeAPIKey) || status.Mode != string(AuthModeAPIKey) {
		t.Fatalf("status = %+v", status)
	}
	if status.AuthToken != nil {
		t.Fatalf("auth token leaked without includeToken: %+v", status)
	}
	if status.RequiresOpenAIAuth == nil || !*status.RequiresOpenAIAuth {
		t.Fatalf("requires openai auth = %+v", status.RequiresOpenAIAuth)
	}

	includeToken := true
	withToken := router.Handle(requestWithParams(t, IntID(2), MethodGetAuthStatus, AuthStatusParams{IncludeToken: &includeToken}))
	if withToken.Error != nil {
		t.Fatalf("get auth status include token = %+v", withToken)
	}
	tokenStatus := withToken.Result.(*AuthStatusResponse)
	if tokenStatus.AuthToken == nil || *tokenStatus.AuthToken != "sk-status" {
		t.Fatalf("token status = %+v", tokenStatus)
	}
}

func TestDefaultRuntimeRouterReadsConfiguredKeyringAuthStore(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`cli_auth_credentials_store = "keyring"`), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := auth.NewStoreWithOptions(home, auth.StoreOptionsFromConfig("keyring", false))
	if err := store.Save(auth.FromAPIKey("sk-keyring")); err != nil {
		t.Fatalf("Save keyring auth error: %v", err)
	}
	router := NewDefaultRuntimeRouter(session.NewStore(filepath.Join(home, "sessions")), home)

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAuthStatus, AuthStatusParams{}))
	if response.Error != nil {
		t.Fatalf("get auth status = %+v", response)
	}
	status := response.Result.(*AuthStatusResponse)
	if !status.Authenticated || status.AuthMethod == nil || *status.AuthMethod != string(AuthModeAPIKey) {
		t.Fatalf("status = %+v", status)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json should not exist for keyring auth: %v", err)
	}
}

func TestRuntimeRouterGetAuthStatusReadsExternalChatGPTTokens(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	plan := "pro"
	snapshot := auth.FromChatGPTAuthTokens(
		fakeJWTAppserver(map[string]any{"email": "external@example.com", "chatgpt_account_id": "account-jwt", "plan_type": "pro"}),
		"account-explicit",
		&plan,
	)
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	includeToken := true

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAuthStatus, AuthStatusParams{IncludeToken: &includeToken}))
	if response.Error != nil {
		t.Fatalf("get auth status = %+v", response)
	}
	status := response.Result.(*AuthStatusResponse)
	if !status.Authenticated || status.AuthMethod == nil || *status.AuthMethod != string(AuthModeChatGPTAuthTokens) {
		t.Fatalf("status = %+v", status)
	}
	if status.AuthToken == nil || !strings.Contains(*status.AuthToken, ".") {
		t.Fatalf("auth token = %+v", status.AuthToken)
	}
	if status.AccountID != "account-explicit" {
		t.Fatalf("account id = %q, want account-explicit", status.AccountID)
	}
}

func TestRuntimeRouterPersonalAccessTokenAuthStatusAndAccountRead(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/user-auth-credential/whoami" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer at-test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"email":                      nil,
			"chatgpt_user_id":            "user-123",
			"chatgpt_account_id":         "account-123",
			"chatgpt_plan_type":          "pro",
			"chatgpt_account_is_fedramp": false,
		}); err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
	}))
	defer server.Close()
	t.Setenv(auth.CodexAccessTokenEnv, "at-test-token")
	t.Setenv(auth.AuthAPIBaseURLEnv, server.URL)
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Account: auth.NewAccountManager()})
	includeToken := true

	statusResponse := router.Handle(requestWithParams(t, IntID(1), MethodGetAuthStatus, AuthStatusParams{IncludeToken: &includeToken}))
	if statusResponse.Error != nil {
		t.Fatalf("get auth status = %+v", statusResponse)
	}
	status := statusResponse.Result.(*AuthStatusResponse)
	if !status.Authenticated || status.AuthMethod == nil || *status.AuthMethod != string(AuthModePersonalAccessToken) || status.AuthToken != nil {
		t.Fatalf("status = %+v", status)
	}

	accountResponse := router.Handle(requestWithParams(t, IntID(2), MethodGetAccount, auth.GetAccountParams{}))
	if accountResponse.Error != nil {
		t.Fatalf("get account = %+v", accountResponse)
	}
	account := accountResponse.Result.(*auth.GetAccountResponse)
	if account.Account == nil || account.Account.Type != auth.AccountChatGPT || account.Account.Email != nil || account.Account.PlanType != auth.PlanPro || !account.RequiresOpenAIAuth {
		t.Fatalf("account = %+v", account)
	}
	if requests != 1 {
		t.Fatalf("whoami requests = %d, want 1", requests)
	}
}

func TestRuntimeRouterGetAuthStatusUsesProviderRequirement(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-local")); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = \"lmstudio\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAuthStatus, AuthStatusParams{}))
	if response.Error != nil {
		t.Fatalf("get auth status = %+v", response)
	}
	status := response.Result.(*AuthStatusResponse)
	if status.RequiresOpenAIAuth == nil || *status.RequiresOpenAIAuth {
		t.Fatalf("requires openai auth = %+v, want false", status.RequiresOpenAIAuth)
	}
}

func TestRuntimeRouterGetAccountWithAmazonBedrockProvider(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = \"amazon-bedrock\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Account: auth.NewAccountManager()})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error != nil {
		t.Fatalf("get account = %+v", response)
	}
	account := response.Result.(*auth.GetAccountResponse)
	if account.RequiresOpenAIAuth || account.Account == nil || account.Account.Type != auth.AccountAmazonBedrock || account.Account.CredentialSource != auth.BedrockCredentialSourceAWSManaged {
		t.Fatalf("account = %+v", account)
	}
}

func TestRuntimeRouterGetAccountWithManagedBedrockAPIKey(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = \"amazon-bedrock\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := auth.NewStore(home).Save(auth.FromBedrockAPIKey("managed-bedrock-api-key", "us-west-2")); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), Account: auth.NewAccountManager()})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error != nil {
		t.Fatalf("get account = %+v", response)
	}
	account := response.Result.(*auth.GetAccountResponse)
	if account.RequiresOpenAIAuth || account.Account == nil || account.Account.Type != auth.AccountAmazonBedrock || account.Account.CredentialSource != auth.BedrockCredentialSourceCodexManaged {
		t.Fatalf("account = %+v", account)
	}
}

func TestRuntimeRouterAuthStatusAndAccountOmitChatGPTAfterPermanentRefreshFailure(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	snapshot := auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"id_token":      fakeJWTAppserver(map[string]any{"chatgpt_account_id": "account-1"}),
			"access_token":  "stale-access-token",
			"refresh_token": "stale-refresh-token",
			"account_id":    "account-1",
			"email":         "user@example.com",
			"plan_type":     "pro",
		},
	}
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "refresh_token_reused"},
		}); err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
	}))
	defer server.Close()
	t.Setenv(auth.RefreshTokenURLEnvOverride, server.URL)
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
	includeToken := true
	refreshToken := true

	statusResponse := router.Handle(requestWithParams(t, IntID(1), MethodGetAuthStatus, AuthStatusParams{
		IncludeToken: &includeToken,
		RefreshToken: &refreshToken,
	}))
	if statusResponse.Error != nil {
		t.Fatalf("get auth status = %+v", statusResponse)
	}
	status := statusResponse.Result.(*AuthStatusResponse)
	if !status.Authenticated || status.AuthMethod == nil || *status.AuthMethod != string(AuthModeChatGPT) {
		t.Fatalf("status = %+v", status)
	}
	if status.AuthToken != nil {
		t.Fatalf("auth token after permanent failure = %+v, want nil", status.AuthToken)
	}

	accountResponse := router.Handle(requestWithParams(t, IntID(2), MethodGetAccount, auth.GetAccountParams{}))
	if accountResponse.Error != nil {
		t.Fatalf("get account = %+v", accountResponse)
	}
	account := accountResponse.Result.(*auth.GetAccountResponse)
	if account.Account != nil || !account.RequiresOpenAIAuth {
		t.Fatalf("account after permanent failure = %+v", account)
	}
	if requests != 1 {
		t.Fatalf("refresh endpoint requests = %d, want 1", requests)
	}
}

func TestRuntimeRouterExternalAuthRefreshBridgeUpdatesAccount(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	plan := "pro"
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestChatGPTAuthTokensRefresh {
			t.Errorf("server request method = %s", request.Method)
		}
		params, ok := request.Params.(*auth.ChatGPTAuthTokensRefreshParams)
		if !ok || params.PreviousAccountID == nil || *params.PreviousAccountID != "account-old" || params.Reason != auth.RefreshUnauthorized {
			t.Errorf("server request params = %#v", request.Params)
		}
		go func() {
			_, _ = router.requireServerRequests().Resolve(OK(request.ID, auth.ChatGPTAuthTokensRefreshResponse{
				AccessToken:      fakeJWTAppserver(map[string]any{"email": "refresh@example.com", "plan_type": "pro"}),
				ChatGPTAccountID: "account-new",
				ChatGPTPlanType:  &plan,
			}))
		}()
	}))
	beforeRevision, err := router.authRevisionSnapshot(context.Background())
	if err != nil {
		t.Fatalf("auth revision before refresh error: %v", err)
	}
	response, err := router.externalAuthRefresh(context.Background(), &model.ExternalAuthRefreshRequest{
		Reason:            model.ExternalAuthRefreshUnauthorized,
		PreviousAccountID: "account-old",
	})
	if err != nil {
		t.Fatalf("externalAuthRefresh() error = %v", err)
	}
	if response.AccessToken == "" || response.ChatGPTAccountID != "account-new" {
		t.Fatalf("response = %+v", response)
	}
	read := router.services.Account.GetAccount(&auth.GetAccountParams{})
	if read.Account == nil || read.Account.Email == nil || *read.Account.Email != "refresh@example.com" || read.Account.PlanType != auth.PlanPro {
		t.Fatalf("account = %+v", read)
	}
	loaded, err := auth.NewStore(home).Load()
	if err != nil {
		t.Fatalf("auth load error: %v", err)
	}
	if loaded == nil || loaded.Mode() != "chatgptAuthTokens" || loaded.Tokens["account_id"] != "account-new" {
		t.Fatalf("loaded auth = %+v", loaded)
	}
	afterRevision, err := router.authRevisionSnapshot(context.Background())
	if err != nil {
		t.Fatalf("auth revision after refresh error: %v", err)
	}
	if afterRevision != beforeRevision+1 {
		t.Fatalf("auth revision = %d, want %d", afterRevision, beforeRevision+1)
	}
	if !sinkHasMethod(sink, NotificationAccountUpdated) {
		t.Fatalf("account updated notification missing: %+v", sink.List())
	}
}

func TestRemoteControlAuthRecoveryLogObserverEmitsRustMetadata(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(previous)

	changed := true
	remoteControlAuthRecoveryLogObserver(remotecontrol.RemoteControlAuthRecoveryEvent{
		Mode:              "managed",
		Step:              "reload",
		UnavailableReason: "ready",
		AuthStateChanged:  &changed,
	})
	output := buf.String()
	for _, want := range []string{
		`msg="remote control unauthorized recovery step"`,
		"mode=managed",
		"step=reload",
		"unavailable_reason=ready",
		"auth_state_changed=true",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("success log missing %q in %q", want, output)
		}
	}

	buf.Reset()
	remoteControlAuthRecoveryLogObserver(remotecontrol.RemoteControlAuthRecoveryEvent{
		Mode:              "external",
		Step:              "external_refresh",
		UnavailableReason: "ready",
		Err:               errors.New("boom"),
	})
	output = buf.String()
	for _, want := range []string{
		"level=WARN",
		`msg="remote control unauthorized recovery failed"`,
		"mode=external",
		"step=external_refresh",
		"error=boom",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("failure log missing %q in %q", want, output)
		}
	}
}

func TestRuntimeRouterRemoteControlAuthRecoveryUsesExternalRefreshBridge(t *testing.T) {
	home := t.TempDir()
	oldSnapshot := auth.FromChatGPTAuthTokens("old-token", "account-old", nil)
	if err := auth.NewStore(home).Save(oldSnapshot); err != nil {
		t.Fatalf("save auth: %v", err)
	}

	var authorizations []string
	var accountIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/remote/control/server/enroll" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		accountIDs = append(accountIDs, r.Header.Get(remotecontrol.RemoteControlAccountIDHeader))
		if len(authorizations) == 1 {
			http.Error(w, `{"error":"expired"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(remotecontrol.EnrollRemoteServerResponse{
			ServerID:           "srv_e_recovered",
			EnvironmentID:      "env_recovered",
			RemoteControlToken: "server-token",
			ExpiresAt:          time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		})
	}))
	defer server.Close()

	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(filepath.Join(home, "sessions")), home, &RuntimeRouterOptions{
		RemoteControlBackendEnabled: true,
		RemoteControlURL:            server.URL + "/backend-api",
		RemoteControlServerAPIOptions: &remotecontrol.ServerAPIOptions{
			HTTPClient: server.Client(),
		},
	})
	defer router.Close()

	refreshRequests := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		refreshRequests <- request
		go func() {
			_, _ = router.requireServerRequests().Resolve(OK(request.ID, auth.ChatGPTAuthTokensRefreshResponse{
				AccessToken:      "new-token",
				ChatGPTAccountID: "account-old",
			}))
		}()
	}))

	response := router.Handle(requestWithParams(t, IntID(1), MethodRemoteControlEnable, remotecontrol.EnableParams{}))
	if response.Error != nil {
		t.Fatalf("remote control enable error: %+v", response.Error)
	}
	result := response.Result.(*remotecontrol.EnableResponse)
	if result.Status != remotecontrol.StatusConnecting || result.EnvironmentID == nil || *result.EnvironmentID != "env_recovered" {
		t.Fatalf("remote control enable result = %+v", result)
	}
	select {
	case request := <-refreshRequests:
		if request.Method != ServerRequestChatGPTAuthTokensRefresh {
			t.Fatalf("server request method = %s", request.Method)
		}
		params, ok := request.Params.(*auth.ChatGPTAuthTokensRefreshParams)
		if !ok || params.Reason != auth.RefreshUnauthorized || params.PreviousAccountID == nil || *params.PreviousAccountID != "account-old" {
			t.Fatalf("server request params = %#v", request.Params)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for external auth refresh server request")
	}
	if strings.Join(authorizations, ",") != "Bearer old-token,Bearer new-token" {
		t.Fatalf("authorizations = %#v", authorizations)
	}
	if strings.Join(accountIDs, ",") != "account-old,account-old" {
		t.Fatalf("account ids = %#v", accountIDs)
	}
	loaded, err := auth.NewStore(home).Load()
	if err != nil {
		t.Fatalf("load auth: %v", err)
	}
	if loaded == nil || loaded.Mode() != "chatgptAuthTokens" || loaded.Tokens["access_token"] != "new-token" || loaded.Tokens["account_id"] != "account-old" {
		t.Fatalf("loaded auth after recovery = %+v", loaded)
	}
}

func TestRuntimeRouterRequestCurrentTimeBridge(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	router.subscribeThreadConnection("thread-time", "conn-time")
	sink := &currentTimeTargetSink{
		router:   router,
		requests: make(chan targetedCurrentTimeRequest, 1),
	}
	router.SetServerRequestSink(sink)
	current, err := router.requestCurrentTime(context.Background(), "thread-time")
	if err != nil {
		t.Fatalf("requestCurrentTime() error = %v", err)
	}
	if current.Unix() != 1781717655 || current.Location() != time.UTC {
		t.Fatalf("current time = %s", current)
	}
	sent := <-sink.requests
	if sent.connectionID != "conn-time" {
		t.Fatalf("target connection = %q", sent.connectionID)
	}
	if sent.request.Method != ServerRequestCurrentTimeRead {
		t.Fatalf("server request method = %s", sent.request.Method)
	}
	params, ok := sent.request.Params.(*CurrentTimeReadParams)
	if !ok || params.ThreadID != "thread-time" {
		t.Fatalf("server request params = %#v", sent.request.Params)
	}
	if _, err := router.requestCurrentTime(context.Background(), ""); err == nil {
		t.Fatal("empty threadID requestCurrentTime() error = nil")
	}
}

func TestRuntimeRouterRequestCurrentTimeRequiresSingleSubscriber(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	router.subscribeThreadConnection("thread-time", "conn-a")
	router.subscribeThreadConnection("thread-time", "conn-b")

	_, err := router.requestCurrentTime(context.Background(), "thread-time")
	if err == nil || err.Error() != "expected exactly one client subscribed to the thread, found 2" {
		t.Fatalf("requestCurrentTime() error = %v", err)
	}
}

func TestRuntimeRouterRequestCurrentTimeWaitsForSubscriber(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := router.requestCurrentTime(ctx, "thread-time")
	if err == nil || err.Error() != "timed out waiting for a client to subscribe to the thread after 10s" {
		t.Fatalf("requestCurrentTime() error = %v", err)
	}
}

type targetedCurrentTimeRequest struct {
	connectionID string
	request      *ServerRequest
}

type currentTimeTargetSink struct {
	router   *RuntimeRouter
	requests chan targetedCurrentTimeRequest
}

func (s *currentTimeTargetSink) SendServerRequest(request *ServerRequest) {
	s.send("", request)
}

func (s *currentTimeTargetSink) SendServerRequestToConnection(connectionID string, request *ServerRequest) {
	s.send(connectionID, request)
}

func (s *currentTimeTargetSink) send(connectionID string, request *ServerRequest) {
	s.requests <- targetedCurrentTimeRequest{connectionID: connectionID, request: request}
	go func() {
		_, _ = s.router.requireServerRequests().Resolve(OK(request.ID, &CurrentTimeReadResponse{CurrentTimeAt: 1781717655}))
	}()
}

func TestRuntimeRouterConfigProfileService(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-main\"\n"), 0o600); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	if err := os.WriteFile(config.ProfileConfigPath(home, "work"), []byte("model = \"gpt-work\"\n"), 0o600); err != nil {
		t.Fatalf("write profile config: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewProfileConfigService(home, "work"),
	})

	read := router.Handle(requestWithParams(t, IntID(1), MethodConfigRead, config.ConfigReadParams{IncludeLayers: true}))
	if read.Error != nil {
		t.Fatalf("config read error: %+v", read.Error)
	}
	readResult := read.Result.(*config.ConfigReadResponse)
	if readResult.Config["model"] != "gpt-work" {
		t.Fatalf("profile config read = %+v", readResult.Config)
	}
	if len(readResult.Layers) != 1 || readResult.Layers[0].Name.Profile == nil || *readResult.Layers[0].Name.Profile != "work" {
		t.Fatalf("profile layer metadata = %+v", readResult.Layers)
	}

	write := router.Handle(requestWithParams(t, IntID(2), MethodConfigValueWrite, config.ConfigValueWriteParams{
		KeyPath:       "model",
		Value:         "gpt-updated",
		MergeStrategy: config.MergeReplace,
	}))
	if write.Error != nil {
		t.Fatalf("config write error: %+v", write.Error)
	}
	if write.Result.(*config.ConfigWriteResponse).FilePath != config.ProfileConfigPath(home, "work") {
		t.Fatalf("write response = %+v", write.Result)
	}
	mainData, err := os.ReadFile(config.ConfigPath(home))
	if err != nil {
		t.Fatalf("read main config: %v", err)
	}
	if string(mainData) != "model = \"gpt-main\"\n" {
		t.Fatalf("main config changed: %q", string(mainData))
	}
}

func TestRuntimeRouterConfigRejectsLegacyProfileWrite(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(t.TempDir()),
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodConfigValueWrite, config.ConfigValueWriteParams{
		KeyPath:       "profiles.work.model",
		Value:         "gpt-5",
		MergeStrategy: config.MergeReplace,
	}))
	if response.Error == nil || response.Error.Code != -32602 {
		t.Fatalf("expected invalid request error, got %+v", response.Error)
	}
	if response.Error == nil || !strings.Contains(response.Error.Message, "legacy config profile tables") {
		t.Fatalf("unexpected error = %+v", response.Error)
	}
	if got := response.Error.Data["config_write_error_code"]; got != string(config.ConfigWriteValidation) {
		t.Fatalf("error data = %+v, want config_write_error_code=%s", response.Error.Data, config.ConfigWriteValidation)
	}
}

func TestRuntimeRouterConfigWriteErrorDataMatchesRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-old\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(home),
	})

	staleVersion := "sha256:stale"
	conflict := router.Handle(requestWithParams(t, IntID(1), MethodConfigValueWrite, config.ConfigValueWriteParams{
		KeyPath:         "model",
		Value:           "gpt-new",
		MergeStrategy:   config.MergeReplace,
		ExpectedVersion: &staleVersion,
	}))
	if conflict.Error == nil || conflict.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("version conflict error = %+v", conflict.Error)
	}
	if got := conflict.Error.Data["config_write_error_code"]; got != string(config.ConfigWriteVersionConflict) {
		t.Fatalf("version conflict data = %+v, want %s", conflict.Error.Data, config.ConfigWriteVersionConflict)
	}

	otherPath := filepath.Join(t.TempDir(), "config.toml")
	readOnly := router.Handle(requestWithParams(t, IntID(2), MethodConfigValueWrite, config.ConfigValueWriteParams{
		KeyPath:       "model",
		Value:         "gpt-new",
		MergeStrategy: config.MergeReplace,
		FilePath:      &otherPath,
	}))
	if readOnly.Error == nil || readOnly.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("readonly path error = %+v", readOnly.Error)
	}
	if got := readOnly.Error.Data["config_write_error_code"]; got != string(config.ConfigWriteLayerReadonly) {
		t.Fatalf("readonly path data = %+v, want %s", readOnly.Error.Data, config.ConfigWriteLayerReadonly)
	}
	if _, err := os.Stat(otherPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("readonly path stat error = %v, want not written", err)
	}
}

func TestRuntimeRouterDispatchesCatalogAPIs(t *testing.T) {
	skillsRoot := t.TempDir()
	skillDir := filepath.Join(skillsRoot, "skill-a")
	if _, err := NewFSService().CreateDirectory(&CreateDirectoryParams{Path: skillDir}); err != nil {
		t.Fatalf("CreateDirectory() error = %v", err)
	}
	if _, err := NewFSService().WriteFile(&WriteFileParams{
		Path:       filepath.Join(skillDir, "SKILL.md"),
		DataBase64: base64.StdEncoding.EncodeToString([]byte("# Skill A")),
	}); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	plugins := plugin.NewPluginService()
	plugins.SetMarketplaceMaterializer(plugin.MarketplaceMaterializerFunc(func(source *plugin.ParsedMarketplaceSource, sparsePaths []string, destination string) error {
		if source != nil && source.Kind == plugin.MarketplaceSourceGit {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(destination, "materialized.txt"), []byte(source.Display()), 0o600)
		}
		return nil
	}))
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{Name: "sample", MarketplaceName: "local", DisplayName: "Sample"}})
	router := NewRuntimeRouter(RuntimeServices{
		Skills:      NewSkillsService([]string{skillsRoot}),
		Plugins:     plugins,
		Models:      model.NewModelService(nil),
		Permissions: sandbox.NewPermissionProfileService(nil),
		MCP:         mcp.NewMCPService(nil),
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	skills := router.Handle(requestWithParams(t, IntID(1), MethodSkillsList, SkillsListParams{}))
	if skills.Error != nil || len(skills.Result.(*SkillsListResponse).Skills) != 1 {
		t.Fatalf("skills = %+v", skills)
	}
	rootsSet := router.Handle(requestWithParams(t, IntID(20), MethodSkillsExtraRootsSet, SkillsExtraRootsSetParams{ExtraRoots: []string{t.TempDir()}}))
	if rootsSet.Error != nil {
		t.Fatalf("skills extra roots set = %+v", rootsSet)
	}
	if !sinkHasMethod(sink, NotificationSkillsChanged) {
		t.Fatalf("skills changed notification missing after extra roots set: %+v", sink.List())
	}
	notificationCount := len(sink.List())
	configWrite := router.Handle(requestWithParams(t, IntID(21), MethodSkillsConfigWrite, SkillsConfigWriteParams{Name: "skill-a", Enabled: false}))
	if configWrite.Error != nil || configWrite.Result.(*SkillsConfigWriteResponse).EffectiveEnabled {
		t.Fatalf("skills config write = %+v", configWrite)
	}
	if len(sink.List()) <= notificationCount {
		t.Fatalf("skills changed notification missing after config write: %+v", sink.List())
	}
	marketplace := router.Handle(requestWithParams(t, IntID(2), MethodMarketplaceAdd, plugin.MarketplaceAddParams{URL: "https://github.com/acme/plugins.git"}))
	if marketplace.Error != nil || marketplace.Result.(*plugin.MarketplaceAddResponse).Marketplace.Name != "plugins" {
		t.Fatalf("marketplace = %+v", marketplace)
	}
	pluginList := router.Handle(requestWithParams(t, IntID(3), MethodPluginList, plugin.PluginListParams{}))
	if pluginList.Error != nil || len(pluginList.Result.(*plugin.PluginListResponse).Plugins) != 1 {
		t.Fatalf("plugin list = %+v", pluginList)
	}
	invalidPluginInstall := router.Handle(requestWithParams(t, IntID(30), MethodPluginInstall, plugin.PluginInstallParams{}))
	if invalidPluginInstall.Error == nil || invalidPluginInstall.Error.Code != -32600 || !strings.Contains(invalidPluginInstall.Error.Message, "plugin id or plugin name is required") {
		t.Fatalf("invalid plugin install = %+v", invalidPluginInstall)
	}
	models := router.Handle(requestWithParams(t, IntID(4), MethodModelList, model.ModelListParams{}))
	if models.Error != nil || len(models.Result.(*model.ModelListResponse).Models) == 0 {
		t.Fatalf("models = %+v", models)
	}
	badCursor := "bad"
	invalidModels := router.Handle(requestWithParams(t, IntID(40), MethodModelList, model.ModelListParams{Cursor: &badCursor}))
	if invalidModels.Error == nil || invalidModels.Error.Code != -32600 || invalidModels.Error.Message != "invalid cursor: bad" {
		t.Fatalf("invalid models = %+v", invalidModels)
	}
	permissions := router.Handle(requestWithParams(t, IntID(5), MethodPermissionProfileList, sandbox.PermissionProfileListParams{}))
	if permissions.Error != nil || len(permissions.Result.(*sandbox.PermissionProfileListResponse).Data) == 0 {
		t.Fatalf("permissions = %+v", permissions)
	}
	badPermissionCursor := "bad"
	badPermissions := router.Handle(requestWithParams(t, IntID(50), MethodPermissionProfileList, sandbox.PermissionProfileListParams{Cursor: &badPermissionCursor}))
	if badPermissions.Error == nil || badPermissions.Error.Code != -32600 {
		t.Fatalf("permission cursor error = %+v, want invalid_request", badPermissions.Error)
	}
	mcpStatus := router.Handle(requestWithParams(t, IntID(6), MethodMCPServerStatusList, mcp.MCPListServerStatusParams{}))
	if mcpStatus.Error != nil {
		t.Fatalf("mcp status = %+v", mcpStatus)
	}
	invalidMCPStatus := router.Handle(requestWithParams(t, IntID(60), MethodMCPServerStatusList, mcp.MCPListServerStatusParams{Cursor: &badCursor}))
	if invalidMCPStatus.Error == nil || invalidMCPStatus.Error.Code != -32600 || invalidMCPStatus.Error.Message != "invalid cursor: bad" {
		t.Fatalf("invalid mcp status = %+v", invalidMCPStatus)
	}
	invalidMCPToolCall := router.Handle(requestWithParams(t, IntID(61), MethodMCPServerToolCall, mcp.MCPToolCallParams{ServerName: "custom"}))
	if invalidMCPToolCall.Error == nil || invalidMCPToolCall.Error.Code != -32600 || invalidMCPToolCall.Error.Message != "server and tool are required" {
		t.Fatalf("invalid mcp tool call = %+v", invalidMCPToolCall)
	}
}

func TestTurnSandboxPermissionProfilePreservesModePolicyFields(t *testing.T) {
	extra := filepath.Join(t.TempDir(), "extra")
	resolved, err := turnSandboxPermissionProfile(&config.Config{Values: map[string]any{}}, t.TempDir(), &turn.TurnStartParams{
		SandboxPolicy: map[string]any{
			"mode":          "workspace-write",
			"writableRoots": []string{extra},
			"networkAccess": true,
		},
	})
	if err != nil {
		t.Fatalf("turnSandboxPermissionProfile() error = %v", err)
	}
	if resolved == nil || resolved.Profile == nil {
		t.Fatalf("resolved = %#v", resolved)
	}
	policy := resolved.Profile.LegacySandboxPolicy()
	if resolved.ID != sandbox.BuiltInPermissionProfileWorkspace || policy.Kind != sandbox.SandboxWorkspaceWrite || !resolved.Profile.AllowsNetwork() {
		t.Fatalf("resolved = %#v policy = %#v", resolved, policy)
	}
	if len(policy.WritableRoots) != 1 || policy.WritableRoots[0] != extra {
		t.Fatalf("WritableRoots = %#v, want %q", policy.WritableRoots, extra)
	}
}

func TestRuntimeRouterMCPThreadScopedRequestsRejectUnknownThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		MCP:          mcp.NewMCPService(nil),
	})

	threadID := "missing-thread"
	statusList := router.Handle(requestWithParams(t, IntID(1), MethodMCPServerStatusList, mcp.MCPListServerStatusParams{
		ThreadID: &threadID,
	}))
	if statusList.Error == nil || statusList.Error.Code != -32004 || !strings.Contains(statusList.Error.Message, "thread not found") {
		t.Fatalf("status list error = %+v", statusList.Error)
	}

	oauthLogin := router.Handle(requestWithParams(t, IntID(2), MethodMCPServerOauthLogin, mcp.MCPServerOauthLoginParams{
		Name:     "custom",
		ThreadID: &threadID,
	}))
	if oauthLogin.Error == nil || oauthLogin.Error.Code != -32004 || !strings.Contains(oauthLogin.Error.Message, "thread not found") {
		t.Fatalf("oauth login error = %+v", oauthLogin.Error)
	}

	toolCall := router.Handle(requestWithParams(t, IntID(3), MethodMCPServerToolCall, mcp.MCPToolCallParams{
		ThreadID: "missing-thread",
		Server:   "custom",
		Tool:     "read",
	}))
	if toolCall.Error == nil || toolCall.Error.Code != -32004 || !strings.Contains(toolCall.Error.Message, "thread not found") {
		t.Fatalf("tool call error = %+v", toolCall.Error)
	}

	resourceRead := router.Handle(requestWithParams(t, IntID(4), MethodMCPServerResourceRead, mcp.MCPResourceReadParams{
		ThreadID: &threadID,
		Server:   "custom",
		URI:      "file://demo",
	}))
	if resourceRead.Error == nil || resourceRead.Error.Code != -32004 || !strings.Contains(resourceRead.Error.Message, "thread not found") {
		t.Fatalf("resource read error = %+v", resourceRead.Error)
	}
}

func TestRuntimeRouterMCPConfigReloadAppliesConfig(t *testing.T) {
	home := t.TempDir()
	body := `[features]
apps = false

[mcp_servers.docs]
command = "mcp-docs"
args = ["--repo", "codex"]
required = true
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(home),
		MCP:    mcp.NewMCPService(nil),
	})

	reload := router.Handle(requestWithParams(t, IntID(1), MethodConfigMCPServerReload, map[string]any{}))
	if reload.Error != nil {
		t.Fatalf("reload = %+v", reload)
	}
	status := router.Handle(requestWithParams(t, IntID(2), MethodMCPServerStatusList, mcp.MCPListServerStatusParams{}))
	if status.Error != nil {
		t.Fatalf("status = %+v", status)
	}
	response := status.Result.(*mcp.MCPListServerStatusResponse)
	if len(response.Data) != 1 || response.Data[0].Name != "docs" || response.Data[0].Server.Command != "mcp-docs" {
		t.Fatalf("status response = %+v", response.Data)
	}
}

func TestAppserverMCPElicitationHandlerRequestsServer(t *testing.T) {
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestMCPElicitation {
			t.Fatalf("method = %s", request.Method)
		}
		params, ok := request.Params.(*MCPElicitationRequestParams)
		if !ok {
			t.Fatalf("params = %#v", request.Params)
		}
		if params.ThreadID != "thread-1" || params.TurnID == nil || *params.TurnID != "turn-1" || params.ServerName != "docs" || params.Message != "Approve?" {
			t.Fatalf("params = %#v", params)
		}
		_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{
			Action:  MCPElicitationActionAccept,
			Content: map[string]any{"approved": true},
			Meta:    map[string]any{"handled": true},
		}))
	}))
	handler := &appserverMCPElicitationHandler{broker: broker}
	response, err := handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
		ServerName:      "docs",
		ThreadID:        "thread-1",
		TurnID:          "turn-1",
		Method:          "elicitation/create",
		Message:         "Approve?",
		RequestedSchema: map[string]any{"type": "object"},
		ElicitationID:   "el-1",
		Meta: map[string]any{
			"threadId": "stale-thread",
			"turnId":   "stale-turn",
		},
	})
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("response = %#v", response)
	}
	content, ok := response.Content.(map[string]any)
	if !ok || content["approved"] != true {
		t.Fatalf("content = %#v", response.Content)
	}
}

func TestAppserverMCPElicitationParamsURLModeAndIDFallback(t *testing.T) {
	params := appserverMCPElicitationParams(&mcp.MCPElicitationRequest{
		ServerName: "codex_apps",
		ID:         json.RawMessage(`"codex_apps_auth_call_123"`),
		Message:    "Reconnect Google Calendar on ChatGPT.",
		URL:        "https://chatgpt.com/apps/google-calendar/connector_calendar",
		Meta: map[string]any{
			"_codex_apps": map[string]any{
				"connector_auth_failure": map[string]any{
					"is_auth_failure": true,
					"connector_id":    "connector_calendar",
					"connector_name":  "Google Calendar",
				},
			},
		},
	})
	if params.Mode != "url" || params.ElicitationID != "codex_apps_auth_call_123" || params.URL == "" {
		t.Fatalf("params = %#v", params)
	}
	meta, ok := params.Meta.(map[string]any)
	if !ok {
		t.Fatalf("meta = %#v", params.Meta)
	}
	if _, ok := meta["_codex_apps"]; !ok {
		t.Fatalf("meta missing _codex_apps: %#v", meta)
	}
}

func TestAppserverMCPElicitationParamsOpenAIFormMode(t *testing.T) {
	params := appserverMCPElicitationParams(&mcp.MCPElicitationRequest{
		ServerName:      "docs",
		ThreadID:        "thread-1",
		Method:          "openai/form",
		Message:         "Approve?",
		RequestedSchema: map[string]any{"type": "object"},
	})
	if params.Mode != "openai/form" || params.ThreadID != "thread-1" || params.TurnID != nil {
		t.Fatalf("params = %#v", params)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal params returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal params returned error: %v", err)
	}
	if payload["mode"] != "openai/form" || payload["threadId"] != "thread-1" || payload["turnId"] != nil || payload["serverName"] != "docs" {
		t.Fatalf("payload = %#v", payload)
	}
	if _, ok := payload["requestedSchema"].(map[string]any); !ok {
		t.Fatalf("requestedSchema missing: %#v", payload)
	}
}

func TestRuntimeRouterDispatchesExperienceAPIs(t *testing.T) {
	searchRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(searchRoot, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write search fixture: %v", err)
	}
	appService := apps.NewAppService([]apps.AppEntry{{ID: "chat", Name: "Chat", Enabled: true, IsAccessible: true}})
	featureService := features.NewFeatureService([]features.FeatureEntry{{Key: "alpha", Stage: features.FeatureStageBeta}})
	misc := NewMiscService()
	misc.SetAuthStatus(AuthStatusResponse{Authenticated: true, Mode: "api-key"})
	agent := newBlockingAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Apps:         appService,
		Features:     featureService,
		Turns:        turn.NewTurnService(),
		Reviews:      review.NewService(),
		Misc:         misc,
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	featureSet := router.Handle(requestWithParams(t, IntID(1), MethodExperimentalFeatureSet, features.FeatureEnablementSetParams{Enabled: []string{"alpha"}}))
	if featureSet.Error != nil {
		t.Fatalf("feature set = %+v", featureSet)
	}
	featureList := router.Handle(requestWithParams(t, IntID(2), MethodExperimentalFeatureList, features.FeatureListParams{}))
	if featureList.Error != nil || !featureList.Result.(*features.FeatureListResponse).Data[0].Enabled {
		t.Fatalf("feature list = %+v", featureList)
	}
	appList := router.Handle(requestWithParams(t, IntID(3), MethodAppList, apps.AppListParams{}))
	if appList.Error != nil || len(appList.Result.(*apps.AppListResponse).Apps) != 1 {
		t.Fatalf("app list = %+v", appList)
	}
	appListJSON, err := json.Marshal(appList.Result)
	if err != nil {
		t.Fatalf("marshal app list: %v", err)
	}
	if strings.Contains(string(appListJSON), `"apps"`) {
		t.Fatalf("app list JSON contains legacy apps field: %s", string(appListJSON))
	}
	badCursor := "bad"
	badFeatureList := router.Handle(requestWithParams(t, IntID(31), MethodExperimentalFeatureList, features.FeatureListParams{Cursor: &badCursor}))
	if badFeatureList.Error == nil || badFeatureList.Error.Code != -32600 || !strings.Contains(badFeatureList.Error.Message, "invalid cursor: bad") {
		t.Fatalf("bad feature cursor = %+v", badFeatureList.Error)
	}
	badAppList := router.Handle(requestWithParams(t, IntID(32), MethodAppList, apps.AppListParams{Cursor: &badCursor}))
	if badAppList.Error == nil || badAppList.Error.Code != -32600 || !strings.Contains(badAppList.Error.Message, "invalid cursor: bad") {
		t.Fatalf("bad app cursor = %+v", badAppList.Error)
	}
	blankCursor := "  "
	blankAppList := router.Handle(requestWithParams(t, IntID(33), MethodAppList, apps.AppListParams{Cursor: &blankCursor}))
	if blankAppList.Error == nil || blankAppList.Error.Code != -32600 || !strings.Contains(blankAppList.Error.Message, "invalid cursor:   ") {
		t.Fatalf("blank app cursor = %+v", blankAppList.Error)
	}
	authStatus := router.Handle(requestWithParams(t, IntID(4), MethodGetAuthStatus, AuthStatusParams{}))
	if authStatus.Error != nil || !authStatus.Result.(*AuthStatusResponse).Authenticated {
		t.Fatalf("auth status = %+v", authStatus)
	}
	threadStart := router.Handle(requestWithParams(t, IntID(50), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start = %+v", threadStart)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(5), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID, Prompt: "hi"}))
	if turnStart.Error != nil {
		t.Fatalf("turn start = %+v", turnStart)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForBlockingAgentStart(t, agent)
	clientUserMessageID := "client-steer-1"
	turnSteer := router.Handle(requestWithParams(t, IntID(6), MethodTurnSteer, turn.TurnSteerParams{
		ThreadID:            threadID,
		ExpectedTurnID:      turnID,
		Prompt:              "more",
		ClientUserMessageID: clientUserMessageID,
		AdditionalContext: map[string]turn.AdditionalContextEntry{
			"ide": {Value: "selection", Kind: turn.AdditionalContextApplication},
		},
	}))
	if turnSteer.Error != nil || turnSteer.Result.(*turn.TurnSteerResponse).TurnID != turnID {
		t.Fatalf("turn steer = %+v", turnSteer)
	}
	emptySteer := router.Handle(requestWithParams(t, IntID(61), MethodTurnSteer, turn.TurnSteerParams{ThreadID: threadID, ExpectedTurnID: turnID}))
	if emptySteer.Error == nil || emptySteer.Error.Code != -32600 || emptySteer.Error.Message != "input must not be empty" {
		t.Fatalf("empty steer = %+v", emptySteer)
	}
	missingExpected := router.Handle(requestWithParams(t, IntID(62), MethodTurnSteer, turn.TurnSteerParams{ThreadID: threadID, Prompt: "more"}))
	if missingExpected.Error == nil || missingExpected.Error.Code != -32600 || missingExpected.Error.Message != "expectedTurnId must not be empty" {
		t.Fatalf("missing expected steer = %+v", missingExpected)
	}
	reviewStart := router.Handle(requestWithParams(t, IntID(7), MethodReviewStart, review.StartParams{ThreadID: threadID}))
	if reviewStart.Error != nil || reviewStart.Result.(*review.StartResponse).Turn.ID != "review-"+threadID {
		t.Fatalf("review start = %+v", reviewStart)
	}
	search := router.Handle(requestWithParams(t, IntID(8), MethodFuzzyFileSearch, FuzzyFileSearchParams{Query: "readme", Roots: []string{searchRoot}}))
	if search.Error != nil || len(search.Result.(*FuzzyFileSearchResponse).Files) != 1 {
		t.Fatalf("search = %+v", search)
	}
	summary := router.Handle(requestWithParams(t, IntID(9), MethodGetConversationSummary, ConversationSummaryParams{ThreadID: threadID}))
	if summary.Error != nil {
		t.Fatalf("summary = %+v", summary)
	}
	interrupt := router.Handle(requestWithParams(t, IntID(10), MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: threadID, TurnID: turnID}))
	if interrupt.Error != nil {
		t.Fatalf("interrupt = %+v", interrupt)
	}
}

func TestRuntimeRouterFuzzyFileSearchSessionUpdateNotifiesRustShape(t *testing.T) {
	searchRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(searchRoot, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write search fixture: %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Misc: NewMiscService()})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodFuzzyFileSearchStart, FuzzyFileSearchSessionStartParams{
		SessionID: "session-1",
		Roots:     []string{searchRoot},
	}))
	if start.Error != nil {
		t.Fatalf("session start = %+v", start)
	}

	update := router.Handle(requestWithParams(t, IntID(2), MethodFuzzyFileSearchUpdate, FuzzyFileSearchSessionUpdateParams{
		SessionID: "session-1",
		Query:     "read",
		Limit:     1,
	}))
	if update.Error != nil {
		t.Fatalf("session update = %+v", update)
	}
	responseJSON, err := json.Marshal(update.Result)
	if err != nil {
		t.Fatalf("Marshal update result error = %v", err)
	}
	if string(responseJSON) != `{}` {
		t.Fatalf("session update response JSON = %s", responseJSON)
	}

	notifications := sink.List()
	if len(notifications) != 2 {
		t.Fatalf("notifications = %#v, want updated and completed", notifications)
	}
	updated, ok := notifications[0].Params.(*FuzzyFileSearchSessionUpdatedNotification)
	if notifications[0].Method != NotificationFuzzyFileSearchSessionUpdated || !ok || updated.SessionID != "session-1" || updated.Query != "read" || len(updated.Files) != 1 {
		t.Fatalf("updated notification = %#v", notifications[0])
	}
	completed, ok := notifications[1].Params.(*FuzzyFileSearchSessionCompletedNotification)
	if notifications[1].Method != NotificationFuzzyFileSearchSessionCompleted || !ok || completed.SessionID != "session-1" {
		t.Fatalf("completed notification = %#v", notifications[1])
	}
	updatedJSON, err := json.Marshal(updated)
	if err != nil {
		t.Fatalf("Marshal updated notification error = %v", err)
	}
	if !strings.Contains(string(updatedJSON), `"files":[`) || strings.Contains(string(updatedJSON), `"limit"`) {
		t.Fatalf("updated notification JSON = %s", updatedJSON)
	}
}

func TestRuntimeRouterFuzzyFileSearchSessionStopRejectsFurtherUpdates(t *testing.T) {
	searchRoot := t.TempDir()
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Misc: NewMiscService()})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodFuzzyFileSearchStart, FuzzyFileSearchSessionStartParams{
		SessionID: "session-1",
		Roots:     []string{searchRoot},
	}))
	if start.Error != nil {
		t.Fatalf("session start = %+v", start)
	}
	stop := router.Handle(requestWithParams(t, IntID(2), MethodFuzzyFileSearchStop, FuzzyFileSearchSessionStopParams{
		SessionID: "session-1",
	}))
	if stop.Error != nil {
		t.Fatalf("session stop = %+v", stop)
	}
	update := router.Handle(requestWithParams(t, IntID(3), MethodFuzzyFileSearchUpdate, FuzzyFileSearchSessionUpdateParams{
		SessionID: "session-1",
		Query:     "read",
	}))
	if update.Error == nil || !strings.Contains(update.Error.Message, "fuzzy file search session not found") {
		t.Fatalf("session update after stop = %+v, want not found error", update)
	}
	if notifications := sink.List(); len(notifications) != 0 {
		t.Fatalf("notifications after stopped session update = %#v, want none", notifications)
	}
}

func TestRuntimeRouterAppListLoadsChatGPTDirectory(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("chatgpt-token", "account-1", nil)); err != nil {
		t.Fatalf("auth save error = %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/connectors/directory/list":
			if got := r.URL.Query().Get("external_logos"); got != "true" {
				t.Fatalf("external_logos = %q", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer chatgpt-token" {
				t.Fatalf("Authorization = %q", got)
			}
			writeJSON(t, w, map[string]any{
				"apps": []map[string]any{{
					"id":   "drive",
					"name": "Google Drive",
				}},
			})
		case "/connectors/directory/list_workspace":
			writeJSON(t, w, map[string]any{"apps": []map[string]any{}})
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(home),
		Apps:   apps.NewAppService(nil),
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodAppList, apps.AppListParams{}))
	if response.Error != nil {
		t.Fatalf("app list response = %+v", response)
	}
	appList := response.Result.(*apps.AppListResponse)
	if len(appList.Apps) != 1 || appList.Apps[0].ID != "drive" || appList.Apps[0].InstallURL == nil {
		t.Fatalf("apps = %+v", appList.Apps)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRuntimeRouterAppListEmitsUpdatedNotificationWithFullList(t *testing.T) {
	limit := uint32(1)
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		Apps: apps.NewAppService([]apps.AppEntry{{
			ID:   "b",
			Name: "Beta",
		}, {
			ID:   "a",
			Name: "Alpha",
		}}),
	})
	router.SetNotificationSink(sink)

	response := router.Handle(requestWithParams(t, IntID(1), MethodAppList, apps.AppListParams{Limit: &limit}))
	if response.Error != nil {
		t.Fatalf("app list response = %+v", response)
	}
	appList := response.Result.(*apps.AppListResponse)
	if len(appList.Data) != 1 || appList.Data[0].ID != "a" {
		t.Fatalf("paged apps = %+v", appList.Data)
	}
	notification := realtimeNotification[*apps.AppListUpdatedNotification](t, sink, NotificationAppListUpdated)
	if notification == nil || len(notification.Data) != 2 {
		t.Fatalf("app list updated notification = %#v", notification)
	}
	if notification.Data[0].ID != "a" || notification.Data[1].ID != "b" {
		t.Fatalf("notification apps = %+v", notification.Data)
	}
}

func TestRuntimeRouterAppListForceRefetchEmitsCachedThenFreshNotification(t *testing.T) {
	alphaV1 := "Alpha v1"
	betaV1 := "Beta v1"
	alphaV2 := "Alpha v2"
	directory := &runtimeTestDirectoryProvider{apps: []apps.AppEntry{{
		ID:          "alpha",
		Name:        "Alpha",
		Description: &alphaV1,
	}, {
		ID:          "beta",
		Name:        "Beta",
		Description: &betaV1,
	}}}
	accessible := &runtimeTestAccessibleProvider{apps: []apps.AppEntry{{
		ID:           "beta",
		Name:         "Beta",
		IsAccessible: true,
	}}}
	service := apps.NewAppService(nil)
	service.SetDirectoryProvider(directory)
	service.SetAccessibleProvider(accessible)
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Apps: service})
	router.SetNotificationSink(sink)

	warm := router.Handle(requestWithParams(t, IntID(1), MethodAppList, apps.AppListParams{}))
	if warm.Error != nil {
		t.Fatalf("warm app list response = %+v", warm)
	}
	if directory.calls != 1 || accessible.calls != 1 {
		t.Fatalf("warm provider calls = %d/%d, want 1/1", directory.calls, accessible.calls)
	}
	updates := appListUpdatedNotificationsForTest(t, sink)
	if len(updates) != 1 {
		t.Fatalf("warm updates = %#v, want one", updates)
	}

	directory.apps = []apps.AppEntry{{
		ID:          "alpha",
		Name:        "Alpha",
		Description: &alphaV2,
	}}
	accessible.apps = nil
	force := router.Handle(requestWithParams(t, IntID(2), MethodAppList, apps.AppListParams{ForceRefetch: true}))
	if force.Error != nil {
		t.Fatalf("force app list response = %+v", force)
	}
	if directory.calls != 2 || accessible.calls != 2 {
		t.Fatalf("force provider calls = %d/%d, want 2/2", directory.calls, accessible.calls)
	}
	updates = appListUpdatedNotificationsForTest(t, sink)
	if len(updates) != 3 {
		t.Fatalf("force updates = %#v, want warm + cached + fresh", updates)
	}
	cached := updates[1].Data
	if len(cached) != 2 || cached[0].ID != "beta" || cached[0].Description == nil || *cached[0].Description != betaV1 || cached[1].ID != "alpha" {
		t.Fatalf("cached update = %+v", cached)
	}
	fresh := updates[2].Data
	if len(fresh) != 1 || fresh[0].ID != "alpha" || fresh[0].Description == nil || *fresh[0].Description != alphaV2 {
		t.Fatalf("fresh update = %+v", fresh)
	}
}

type runtimeTestDirectoryProvider struct {
	apps  []apps.AppEntry
	calls int
}

func (p *runtimeTestDirectoryProvider) ListDirectoryApps(params *apps.AppDirectoryListParams) (*apps.AppDirectoryListResponse, error) {
	p.calls++
	allLoaded := true
	return &apps.AppDirectoryListResponse{Apps: append([]apps.AppEntry(nil), p.apps...), AllConnectorsLoaded: &allLoaded}, nil
}

type runtimeTestAccessibleProvider struct {
	apps  []apps.AppEntry
	calls int
}

func (p *runtimeTestAccessibleProvider) ListAccessibleApps(params *apps.AppAccessibleListParams) (*apps.AppAccessibleListResponse, error) {
	p.calls++
	return &apps.AppAccessibleListResponse{Apps: append([]apps.AppEntry(nil), p.apps...), CodexAppsReady: true}, nil
}

func appListUpdatedNotificationsForTest(t *testing.T, sink *NotificationBuffer) []*apps.AppListUpdatedNotification {
	t.Helper()
	var out []*apps.AppListUpdatedNotification
	for _, notification := range sink.List() {
		if notification.Method != NotificationAppListUpdated {
			continue
		}
		payload, ok := notification.Params.(*apps.AppListUpdatedNotification)
		if !ok {
			t.Fatalf("app/list updated params = %#v", notification.Params)
		}
		out = append(out, payload)
	}
	return out
}

func TestRuntimeRouterAppListUsesPluginAppMetadata(t *testing.T) {
	description := "Track work"
	installURL := "https://chatgpt.com/connectors/linear"
	logoURL := "https://example.com/slack.png"
	slackID := "slack"
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{
		Summary: plugin.PluginSummary{
			ID:          "linear-plugin@debug",
			Name:        "linear-plugin",
			DisplayName: "Linear Plugin",
			Installed:   true,
			Enabled:     true,
		},
		Apps: []plugin.AppSummary{{
			ID:          "linear",
			DisplayName: "Linear",
			Description: &description,
			InstallURL:  &installURL,
		}},
		AppTemplates: []plugin.AppTemplateSummary{{
			TemplateID:           "slack-template",
			DisplayName:          "Slack Template",
			CanonicalConnectorID: &slackID,
			LogoURL:              &logoURL,
		}},
	})
	router := NewRuntimeRouter(RuntimeServices{
		Apps:    apps.NewAppService(nil),
		Plugins: plugins,
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodAppList, apps.AppListParams{}))
	if response.Error != nil {
		t.Fatalf("app list response = %+v", response)
	}
	appList := response.Result.(*apps.AppListResponse)
	linear := appEntryByIDForTest(appList.Apps, "linear")
	if linear == nil || linear.Name != "Linear" || linear.Description == nil || *linear.Description != "Track work" {
		t.Fatalf("linear app = %#v", linear)
	}
	if linear.InstallURL == nil || *linear.InstallURL != installURL || strings.Join(linear.PluginDisplayNames, ",") != "Linear Plugin" {
		t.Fatalf("linear metadata = %#v", linear)
	}
	slack := appEntryByIDForTest(appList.Apps, "slack")
	if slack == nil || slack.Name != "Slack Template" || slack.LogoURL == nil || *slack.LogoURL != logoURL {
		t.Fatalf("slack template app = %#v", slack)
	}
}

func appEntryByIDForTest(entries []apps.AppEntry, id string) *apps.AppEntry {
	for i := range entries {
		if entries[i].ID == id {
			return &entries[i]
		}
	}
	return nil
}

func TestRuntimeRouterAppListMergesMCPAccessibleConnectors(t *testing.T) {
	mcpService := mcp.NewMCPService(nil)
	mcpService.SetServer(mcp.MCPServerStatus{
		Name:  mcp.CodexAppsServerName,
		State: mcp.MCPServerReady,
		Server: mcp.MCPServerInfo{
			Name: mcp.CodexAppsServerName,
		},
		Tools: []mcp.MCPToolInfo{{
			Name: "search",
			Meta: map[string]any{
				"codex_apps": map[string]any{
					"connector_id":           "drive",
					"connector_name":         "Google Drive",
					"connector_description":  "Drive files",
					"connector_icon_url":     "https://example.com/drive.png",
					"connector_color":        "#4285f4",
					"homepage_url":           "https://drive.example.com",
					"docs_url":               "https://docs.example.com/drive",
					"is_enabled":             false,
					"plugin_display_names":   []any{"Docs Plugin"},
					"namespace_description":  "Files",
					"synthetic_link_ignored": false,
				},
			},
		}},
	})
	router := NewRuntimeRouter(RuntimeServices{
		Apps: apps.NewAppService([]apps.AppEntry{{
			ID:        "drive",
			Name:      "drive",
			IsEnabled: true,
			Enabled:   true,
		}}),
		MCP: mcpService,
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodAppList, apps.AppListParams{}))
	if response.Error != nil {
		t.Fatalf("app list response = %+v", response)
	}
	appList := response.Result.(*apps.AppListResponse)
	if len(appList.Apps) != 1 {
		t.Fatalf("apps = %+v, want one", appList.Apps)
	}
	app := appList.Apps[0]
	if app.ID != "drive" || app.Name != "Google Drive" || !app.IsAccessible || app.Description == nil || *app.Description != "Drive files" {
		t.Fatalf("app = %+v", app)
	}
	if app.IsEnabled {
		t.Fatalf("app IsEnabled = true, want false from MCP connector metadata")
	}
	if len(app.PluginDisplayNames) != 1 || app.PluginDisplayNames[0] != "Docs Plugin" {
		t.Fatalf("plugin names = %+v", app.PluginDisplayNames)
	}
	if app.InstallURL == nil || !strings.Contains(*app.InstallURL, "/google-drive/drive") {
		t.Fatalf("install URL = %+v", app.InstallURL)
	}
	if app.LogoURL == nil || *app.LogoURL != "https://example.com/drive.png" {
		t.Fatalf("logo URL = %+v", app.LogoURL)
	}
	branding, ok := app.Branding.(map[string]any)
	if !ok || branding["iconUrl"] != "https://example.com/drive.png" || branding["color"] != "#4285f4" || branding["website"] != "https://drive.example.com" || branding["docsUrl"] != "https://docs.example.com/drive" {
		t.Fatalf("branding = %#v", app.Branding)
	}
}

func TestRuntimeRouterToolRouterForTurnExposesMCPStatusTools(t *testing.T) {
	hidden := false
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[apps.mail]\nenabled = false\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	mcpService := mcp.NewMCPService(nil)
	mcpService.SetServer(mcp.MCPServerStatus{
		Name:  "drive",
		State: mcp.MCPServerReady,
		Server: mcp.MCPServerInfo{
			Name: "drive",
		},
		Tools: []mcp.MCPToolInfo{
			{
				Name:        "read",
				Description: "Read Drive files",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
				},
				Annotations: map[string]any{"readOnlyHint": true},
			},
			{
				Name:         "secret",
				Description:  "Hidden Drive tool",
				Meta:         map[string]any{"modelVisible": hidden},
				Annotations:  map[string]any{"readOnlyHint": true},
				InputSchema:  map[string]any{"type": "object"},
				OutputSchema: map[string]any{"type": "object"},
			},
		},
	})
	mcpService.SetServer(mcp.MCPServerStatus{
		Name:  mcp.CodexAppsServerName,
		State: mcp.MCPServerReady,
		Server: mcp.MCPServerInfo{
			Name: mcp.CodexAppsServerName,
		},
		Tools: []mcp.MCPToolInfo{
			{
				Name:        "create_event",
				Description: "Create calendar events",
				Meta: map[string]any{
					"codex_apps": map[string]any{
						"connector_id":         "calendar",
						"connector_name":       "Calendar",
						"plugin_display_names": []any{"Calendar Plugin"},
					},
				},
			},
			{
				Name:        "send_mail",
				Description: "Send mail",
				Meta: map[string]any{
					"codex_apps": map[string]any{
						"connector_id":   "mail",
						"connector_name": "Mail",
					},
				},
			},
			{
				Name:        "synthetic_link",
				Description: "Synthetic connector link",
				Meta: map[string]any{
					"codex_apps": map[string]any{
						"connector_id":   "calendar",
						"connector_name": "Calendar",
						"synthetic_link": true,
					},
				},
			},
		},
	})
	router := NewRuntimeRouter(RuntimeServices{
		Apps:   apps.NewAppService(nil),
		Config: config.NewConfigService(home),
		MCP:    mcpService,
	})

	toolRouter, err := router.toolRouterForTurn(t.TempDir(), &turn.TurnStartParams{ThreadID: "thread-1"}, "turn-1")
	if err != nil {
		t.Fatalf("toolRouterForTurn() error = %v", err)
	}
	visible := toolSpecKeySetForTest(toolRouter.ModelVisibleSpecs())
	if !visible[tool.ToolSearchName] {
		t.Fatalf("model-visible specs = %#v, missing tool_search", visible)
	}
	for _, name := range []string{"drive.read", "codex_apps.create_event"} {
		if visible[name] {
			t.Fatalf("model-visible specs = %#v, %s should be deferred", visible, name)
		}
	}

	driveSpecs := runtimeRouterToolSearchSpecsForTest(t, toolRouter, "drive files")
	driveRead, ok := driveSpecs["drive.read"]
	if !ok {
		t.Fatalf("drive search specs = %#v, missing drive.read", sortedToolSpecKeysForTest(driveSpecs))
	}
	if !driveRead.Parallel {
		t.Fatalf("drive.read Parallel = false, want readOnlyHint to carry through")
	}
	if properties, ok := driveRead.InputSchema["properties"].(map[string]any); !ok || properties["path"] == nil {
		t.Fatalf("drive.read input schema = %#v", driveRead.InputSchema)
	}
	if _, ok := driveSpecs["drive.secret"]; ok {
		t.Fatalf("drive search specs = %#v, should not include hidden tool", sortedToolSpecKeysForTest(driveSpecs))
	}

	calendarSpecs := runtimeRouterToolSearchSpecsForTest(t, toolRouter, "calendar event")
	calendarCreate, ok := calendarSpecs["codex_apps.create_event"]
	if !ok {
		t.Fatalf("calendar search specs = %#v, missing codex_apps.create_event", sortedToolSpecKeysForTest(calendarSpecs))
	}
	if !strings.Contains(calendarCreate.Description, "Calendar Plugin") {
		t.Fatalf("calendar description = %q, missing plugin provenance", calendarCreate.Description)
	}
	for _, name := range []string{"codex_apps.send_mail", "codex_apps.synthetic_link"} {
		if _, ok := calendarSpecs[name]; ok {
			t.Fatalf("calendar search specs = %#v, should not include %s", sortedToolSpecKeysForTest(calendarSpecs), name)
		}
	}
	mailSpecs := runtimeRouterToolSearchSpecsForTest(t, toolRouter, "mail")
	if _, ok := mailSpecs["codex_apps.send_mail"]; ok {
		t.Fatalf("mail search specs = %#v, disabled connector should not be exposed", sortedToolSpecKeysForTest(mailSpecs))
	}
}

func runtimeRouterToolSearchSpecsForTest(t *testing.T, router *tool.Router, query string) map[string]tool.Spec {
	t.Helper()
	output, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "search-" + strings.ReplaceAll(query, " ", "-"),
		ToolName: tool.PlainName(tool.ToolSearchName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"query":` + strconv.Quote(query) + `}`},
	})
	if err != nil {
		t.Fatalf("Dispatch(tool_search %q) error = %v", query, err)
	}
	rawTools, ok := output.Data["tools"].([]any)
	if !ok {
		t.Fatalf("tool_search output data = %#v", output.Data)
	}
	specs := make(map[string]tool.Spec, len(rawTools))
	for _, raw := range rawTools {
		spec, ok := raw.(tool.Spec)
		if !ok {
			t.Fatalf("tool_search tool = %#v, want tool.Spec", raw)
		}
		specs[spec.Name.Key()] = spec
	}
	return specs
}

func toolSpecKeySetForTest(specs []tool.Spec) map[string]bool {
	out := make(map[string]bool, len(specs))
	for _, spec := range specs {
		if key := spec.Name.Key(); key != "" {
			out[key] = true
		}
	}
	return out
}

func sortedToolSpecKeysForTest(specs map[string]tool.Spec) []string {
	keys := make([]string, 0, len(specs))
	for key := range specs {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func TestRuntimeRouterDispatchesThreadExtrasAndRealtime(t *testing.T) {
	extras := NewThreadExtraService()
	extras.SetBackgroundTerminals("thread-1", []BackgroundTerminal{
		{ItemID: "item-1", ProcessID: "proc-1", Command: "sleep", CWD: "D:/repo"},
	})
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadExtras: extras})
	router.SetNotificationSink(sink)
	router.requireThreadStatus().UpsertThread("thread-1", false)

	budget := int64(100)
	objective := "finish"
	setGoal := router.Handle(requestWithParams(t, IntID(1), MethodThreadGoalSet, GoalSetParams{
		ThreadID: "thread-1", Objective: &objective, TokenBudget: &budget,
	}))
	if setGoal.Error != nil || setGoal.Result.(*GoalSetResponse).Goal.Objective != "finish" {
		t.Fatalf("goal set = %+v", setGoal)
	}
	getGoal := router.Handle(requestWithParams(t, IntID(2), MethodThreadGoalGet, GoalGetParams{ThreadID: "thread-1"}))
	if getGoal.Error != nil || getGoal.Result.(*GoalGetResponse).Goal == nil {
		t.Fatalf("goal get = %+v", getGoal)
	}
	shell := router.Handle(requestWithParams(t, IntID(3), MethodThreadShellCommand, ShellCommandParams{
		ThreadID: "thread-1", Command: "pwd",
	}))
	if shell.Error != nil {
		t.Fatalf("shell command = %+v", shell)
	}
	terminals := router.Handle(requestWithParams(t, IntID(4), MethodThreadBackgroundTerminalsList, BackgroundTerminalsListParams{ThreadID: "thread-1"}))
	if terminals.Error != nil {
		t.Fatalf("background terminals = %+v", terminals)
	}
	terminalData := terminals.Result.(*BackgroundTerminalsListResponse).Data
	foundPreexistingTerminal := false
	for _, terminal := range terminalData {
		if terminal.ProcessID == "proc-1" {
			foundPreexistingTerminal = true
			break
		}
	}
	if !foundPreexistingTerminal {
		t.Fatalf("background terminals missing preexisting terminal: %+v", terminalData)
	}
	clearGoal := router.Handle(requestWithParams(t, IntID(5), MethodThreadGoalClear, GoalClearParams{ThreadID: "thread-1"}))
	if clearGoal.Error != nil || !clearGoal.Result.(*GoalClearResponse).Cleared {
		t.Fatalf("goal clear = %+v", clearGoal)
	}
	modelName := "gpt-settings"
	permissions := "trusted"
	settingsUpdate := router.Handle(requestWithParams(t, IntID(6), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:    "thread-1",
		Model:       &modelName,
		Permissions: &permissions,
	}))
	if settingsUpdate.Error != nil {
		t.Fatalf("settings update = %+v", settingsUpdate)
	}
	settingsNotification := threadSettingsNotification(t, sink, "thread-1")
	if settingsNotification.ThreadSettings.Model != "gpt-settings" ||
		settingsNotification.ThreadSettings.ActivePermissionProfile == nil ||
		*settingsNotification.ThreadSettings.ActivePermissionProfile != "trusted" {
		t.Fatalf("thread/settings/updated = %+v", settingsNotification)
	}
	serviceTier := "priority"
	setServiceTier := router.Handle(requestWithParams(t, IntID(7), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:    "thread-1",
		ServiceTier: &ThreadExtraOptionalString{Set: true, Value: &serviceTier},
	}))
	if setServiceTier.Error != nil {
		t.Fatalf("settings service tier set = %+v", setServiceTier)
	}
	clearServiceTier := router.Handle(requestWithParams(t, IntID(8), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:    "thread-1",
		ServiceTier: &ThreadExtraOptionalString{Set: true},
	}))
	if clearServiceTier.Error != nil {
		t.Fatalf("settings service tier clear = %+v", clearServiceTier)
	}
	clearNotification := lastThreadSettingsNotification(t, sink, "thread-1")
	if clearNotification.ThreadSettings.Model != "gpt-settings" ||
		clearNotification.ThreadSettings.ServiceTier == nil ||
		*clearNotification.ThreadSettings.ServiceTier != model.ServiceTierDefaultRequestValue {
		t.Fatalf("thread/settings/updated clear = %+v", clearNotification)
	}
	sandboxPolicy := "read-only"
	conflict := router.Handle(requestWithParams(t, IntID(9), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:      "thread-1",
		Permissions:   &permissions,
		SandboxPolicy: &sandboxPolicy,
	}))
	if conflict.Error == nil || conflict.Error.Code != -32600 || conflict.Error.Message != "`permissions` cannot be combined with `sandboxPolicy`" {
		t.Fatalf("settings conflict error = %+v", conflict.Error)
	}

	realtimeSink := NewNotificationBuffer()
	realtimeRouter := NewRuntimeRouter(RuntimeServices{})
	realtimeRouter.SetNotificationSink(realtimeSink)
	start := realtimeRouter.Handle(requestWithParams(t, IntID(10), MethodThreadRealtimeStart, map[string]any{
		"threadId": "thread-1", "outputModality": "text", "realtimeSessionId": "rt-1",
	}))
	if start.Error != nil {
		t.Fatalf("realtime start = %+v", start)
	}
	started := realtimeNotification[*ThreadRealtimeStartedNotification](t, realtimeSink, NotificationThreadRealtimeStarted)
	if started.ThreadID != "thread-1" || started.RealtimeSessionID == nil || *started.RealtimeSessionID != "rt-1" || started.Version != "v2" {
		t.Fatalf("realtime started notification = %+v", started)
	}
	appendText := realtimeRouter.Handle(requestWithParams(t, IntID(11), MethodThreadRealtimeAppendText, map[string]any{
		"threadId": "thread-1", "text": "hello",
	}))
	if appendText.Error != nil {
		t.Fatalf("realtime append text = %+v", appendText)
	}
	voices := realtimeRouter.Handle(requestWithParams(t, IntID(12), MethodThreadRealtimeListVoices, map[string]any{}))
	if voices.Error != nil {
		t.Fatalf("realtime voices = %+v", voices)
	}
	stop := realtimeRouter.Handle(requestWithParams(t, IntID(11), MethodThreadRealtimeStop, map[string]any{"threadId": "thread-1"}))
	if stop.Error != nil {
		t.Fatalf("realtime stop = %+v", stop)
	}
	closed := realtimeNotification[*ThreadRealtimeClosedNotification](t, realtimeSink, NotificationThreadRealtimeClosed)
	if closed.ThreadID != "thread-1" || closed.Reason == nil || *closed.Reason != "client" {
		t.Fatalf("realtime closed notification = %+v", closed)
	}
	webrtc := realtimeRouter.Handle(requestWithParams(t, IntID(11), MethodThreadRealtimeStart, map[string]any{
		"threadId": "thread-1", "outputModality": "audio", "transport": map[string]any{"type": "webrtc", "sdp": "offer"},
	}))
	if webrtc.Error != nil {
		t.Fatalf("realtime webrtc start = %+v", webrtc)
	}
	sdp := realtimeNotification[*ThreadRealtimeSDPNotification](t, realtimeSink, NotificationThreadRealtimeSDP)
	if sdp.ThreadID != "thread-1" || sdp.SDP != "answer:offer" {
		t.Fatalf("realtime sdp notification = %+v", sdp)
	}
}

func TestRuntimeRouterLiveThreadOnlyMethodsRejectNotLoaded(t *testing.T) {
	store := session.NewStore(t.TempDir())
	createRecord(t, store, "thread-cold", fixedTime())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:   NewRouter(store),
		ThreadExtras:   NewThreadExtraService(),
		ThreadStatus:   NewThreadStatusManager(),
		ServerRequests: NewServerRequestBroker(),
	})
	modelName := "gpt-cold"

	cases := []struct {
		name   string
		method Method
		params any
	}{
		{"increment", MethodThreadIncrementElicitation, ThreadIncrementElicitationParams{ThreadID: "thread-cold"}},
		{"legacy increment", MethodThreadIncrementElicitationLegacy, ThreadIncrementElicitationParams{ThreadID: "thread-cold"}},
		{"decrement", MethodThreadDecrementElicitation, ThreadDecrementElicitationParams{ThreadID: "thread-cold"}},
		{"legacy decrement", MethodThreadDecrementElicitationLegacy, ThreadDecrementElicitationParams{ThreadID: "thread-cold"}},
		{"guardian", MethodThreadApproveGuardianDeniedAction, ThreadApproveGuardianDeniedActionParams{ThreadID: "thread-cold", Event: json.RawMessage(`{"id":"denied"}`)}},
		{"settings", MethodThreadSettingsUpdate, SettingsUpdateParams{ThreadID: "thread-cold", Model: &modelName}},
		{"settings conflict", MethodThreadSettingsUpdate, SettingsUpdateParams{ThreadID: "thread-cold", Permissions: stringPtr("trusted"), SandboxPolicy: stringPtr("workspace-write")}},
		{"inject items", MethodThreadInjectItems, ThreadInjectItemsParams{ThreadID: "thread-cold", Items: []json.RawMessage{json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"injected"}]}`)}}},
		{"shell", MethodThreadShellCommand, ShellCommandParams{ThreadID: "thread-cold", Command: "pwd"}},
		{"terminals clean", MethodThreadBackgroundTerminalsClean, BackgroundTerminalsCleanParams{ThreadID: "thread-cold"}},
		{"terminals list", MethodThreadBackgroundTerminalsList, BackgroundTerminalsListParams{ThreadID: "thread-cold"}},
		{"terminals terminate", MethodThreadBackgroundTerminalsTerminate, BackgroundTerminalsTerminateParams{ThreadID: "thread-cold", ProcessID: "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := router.Handle(requestWithParams(t, IntID(1), tc.method, tc.params))
			if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "thread not found: thread-cold" {
				t.Fatalf("%s response = %+v", tc.method, response)
			}
		})
	}
}

func TestRuntimeRouterThreadShellCommandEmitsUserShellNotifications(t *testing.T) {
	sink := NewNotificationBuffer()
	extras := NewThreadExtraService()
	router := NewRuntimeRouter(RuntimeServices{ThreadExtras: extras, DefaultCWD: t.TempDir()})
	router.SetNotificationSink(sink)
	router.requireThreadStatus().UpsertThread("thread-shell-1", false)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadShellCommand, ShellCommandParams{
		ThreadID: "thread-shell-1",
		Command:  "  echo shell-ok  ",
	}))
	if response.Error != nil {
		t.Fatalf("thread/shellCommand response = %+v", response)
	}
	if history := extras.shellHistory["thread-shell-1"]; len(history) != 1 || history[0] != "echo shell-ok" {
		t.Fatalf("shell history = %#v", history)
	}
	started := waitForCommandExecutionStartedBySource(t, sink, CommandExecutionSourceUserShell)
	itemID, _ := started.Item["id"].(string)
	if itemID == "" {
		t.Fatalf("started item missing id: %+v", started.Item)
	}
	waitForCommandExecutionOutputDeltaContaining(t, sink, started.TurnID, itemID, "shell-ok")
	completed := waitForItemCompleted(t, sink, itemID)
	completedItem := notificationItemMap(t, completed.Item)
	if completedItem["type"] != "commandExecution" || completedItem["source"] != string(CommandExecutionSourceUserShell) {
		t.Fatalf("completed item = %+v", completedItem)
	}
	if !strings.Contains(stringFromAny(completedItem["aggregatedOutput"]), "shell-ok") {
		t.Fatalf("completed aggregatedOutput = %+v", completedItem["aggregatedOutput"])
	}
	waitForTurnCompletedStatus(t, sink, started.TurnID, TurnStatusCompleted)
}

func TestRuntimeRouterThreadShellCommandEnqueuesActiveTurnContext(t *testing.T) {
	sink := NewNotificationBuffer()
	mailbox := turn.NewSteerMailbox()
	router := NewRuntimeRouter(RuntimeServices{ThreadExtras: NewThreadExtraService(), SteerMailbox: mailbox, DefaultCWD: t.TempDir()})
	router.SetNotificationSink(sink)
	threadID := "thread-shell-active"
	turnID := "turn-shell-active"
	router.requireThreadStatus().UpsertThread(threadID, false)
	router.turnsMu.Lock()
	router.active[threadID] = &activeRuntimeTurn{ThreadID: threadID, TurnID: turnID, StartedAtMS: time.Now().UTC().UnixMilli()}
	router.turnsMu.Unlock()

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadShellCommand, ShellCommandParams{
		ThreadID: threadID,
		Command:  "echo active-shell",
	}))
	if response.Error != nil {
		t.Fatalf("thread/shellCommand response = %+v", response)
	}
	started := waitForCommandExecutionStartedBySource(t, sink, CommandExecutionSourceUserShell)
	if started.TurnID != turnID {
		t.Fatalf("started turn id = %q, want %q", started.TurnID, turnID)
	}
	itemID, _ := started.Item["id"].(string)
	waitForCommandExecutionOutputDeltaContaining(t, sink, turnID, itemID, "active-shell")
	waitForItemCompleted(t, sink, itemID)

	items := mailbox.Drain(&turn.SteerDrainParams{ThreadID: threadID, TurnID: turnID})
	text := inputItemText(items)
	if !strings.Contains(text, "<user_shell_command>") || !strings.Contains(text, "active-shell") {
		t.Fatalf("mailbox items = %s", text)
	}
}

func TestRuntimeRouterThreadShellCommandPersistsUserShellRecord(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-shell-store")
	if err := store.Save(&session.Record{ID: threadID, Metadata: session.Metadata{CWD: t.TempDir()}}); err != nil {
		t.Fatalf("Save thread error = %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: NewThreadExtraService()})
	router.SetNotificationSink(sink)
	router.requireThreadStatus().UpsertThread(string(threadID), false)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadShellCommand, ShellCommandParams{
		ThreadID: string(threadID),
		Command:  "echo persisted-shell",
	}))
	if response.Error != nil {
		t.Fatalf("thread/shellCommand response = %+v", response)
	}
	started := waitForCommandExecutionStartedBySource(t, sink, CommandExecutionSourceUserShell)
	itemID, _ := started.Item["id"].(string)
	waitForItemCompleted(t, sink, itemID)
	record, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("Read thread error = %v", err)
	}
	if len(record.Items) != 1 {
		t.Fatalf("record items = %#v", record.Items)
	}
	item := record.Items[0]
	if item.Type == "commandExecution" || item.Metadata["kind"] != "user_shell_command" {
		t.Fatalf("persisted item = %#v", item)
	}
	if !strings.Contains(item.Text, "<user_shell_command>") || !strings.Contains(item.Text, "persisted-shell") {
		t.Fatalf("persisted shell text = %q", item.Text)
	}
}

func TestRuntimeRouterRealtimeRejectsUnknownThreadWhenStoreBacked(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadRealtimeStart, map[string]any{
		"threadId": "missing-thread", "outputModality": "text",
	}))
	if start.Error == nil || start.Error.Code != -32004 || !strings.Contains(start.Error.Message, "thread not found") {
		t.Fatalf("realtime missing thread = %+v", start.Error)
	}
}

func TestRuntimeRouterTurnStartRunsRuntimeAndPersistsItems(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        model.NewLocalAgentRunner(),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello runtime",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello runtime",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID

	record := waitForSessionItem(t, store, threadID, "agent_message")
	if len(record.Items) < 2 {
		t.Fatalf("record items = %#v", record.Items)
	}
	if record.Metadata.SessionPrefix == "" {
		t.Fatalf("session prefix missing: %#v", record.Metadata)
	}
	if got := record.Items[len(record.Items)-1].Metadata["turnId"]; got != turnID {
		t.Fatalf("assistant turn id = %#v, want %s", got, turnID)
	}
	if record.Items[len(record.Items)-1].Metadata["timingProfile"] == nil || record.Items[len(record.Items)-1].Metadata["timing_profile"] == nil {
		t.Fatalf("assistant timing profile metadata = %#v", record.Items[len(record.Items)-1].Metadata)
	}
	if record.Metadata.LastResponseID != "resp-"+turnID {
		t.Fatalf("last response id = %q", record.Metadata.LastResponseID)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if got := router.services.ThreadStatus.LoadedStatusForThread(threadID); got.Type != "idle" {
		t.Fatalf("thread status = %#v", got)
	}
	if !sinkHasMethod(sink, NotificationTurnStarted) || !sinkHasMethod(sink, NotificationTurnCompleted) || !sinkHasMethod(sink, NotificationItemCompleted) {
		t.Fatalf("notifications = %#v", sink.List())
	}
	var startedNotification *TurnStartedNotification
	for _, notification := range sink.List() {
		if notification.Method == NotificationTurnStarted {
			startedNotification, _ = notification.Params.(*TurnStartedNotification)
			break
		}
	}
	if startedNotification == nil || startedNotification.Turn.ItemsView != TurnItemsNotLoaded || len(startedNotification.Turn.Items) != 0 {
		t.Fatalf("turn started notification = %#v, want notLoaded empty items", startedNotification)
	}
	staleSteer := router.Handle(requestWithParams(t, IntID(3), MethodTurnSteer, turn.TurnSteerParams{
		ThreadID:       threadID,
		ExpectedTurnID: turnID,
		Prompt:         "too late",
	}))
	if staleSteer.Error == nil {
		t.Fatalf("expected steer after completed runtime to fail")
	}
	rolloutPath, err := rollout.FindThreadPath(store.Root(), threadID, false)
	if err != nil {
		t.Fatalf("rollout path error: %v", err)
	}
	lines, _, err := rollout.Load(rolloutPath)
	if err != nil {
		t.Fatalf("rollout load error: %v", err)
	}
	if len(lines) < len(record.Items)+1 {
		t.Fatalf("rollout lines = %d, items = %d", len(lines), len(record.Items))
	}
}

func TestRuntimeRouterTurnStartWritesRolloutTurnLifecycle(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        model.NewLocalAgentRunner(),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello lifecycle",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "complete lifecycle",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	snapshots := rolloutTurnSnapshotsForThread(t, store, threadID)
	if len(snapshots) != 1 {
		t.Fatalf("rollout turn snapshots = %#v", snapshots)
	}
	snapshot := snapshots[0]
	if snapshot.ID != turnID || snapshot.Status != string(TurnStatusCompleted) {
		t.Fatalf("rollout turn snapshot = %#v", snapshot)
	}
	if snapshot.StartedAt == nil || snapshot.CompletedAt == nil || snapshot.DurationMS == nil {
		t.Fatalf("rollout turn timing = %#v", snapshot)
	}
}

func TestRuntimeRouterTurnInterruptWritesRolloutTurnLifecycle(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newBlockingAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello interrupt lifecycle",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "interrupt lifecycle",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForBlockingAgentStart(t, agent)

	interrupt := router.Handle(requestWithParams(t, IntID(3), MethodTurnInterrupt, turn.TurnInterruptParams{
		ThreadID: threadID,
		TurnID:   turnID,
	}))
	if interrupt.Error != nil {
		t.Fatalf("interrupt error: %+v", interrupt.Error)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusInterrupted)

	record := rolloutRecordForThread(t, store, threadID)
	if len(record.Items) != 2 || record.Items[0].Text != "interrupt lifecycle" {
		t.Fatalf("interrupted record items = %#v", record.Items)
	}
	marker := record.Items[1]
	if marker.Metadata["kind"] != "turn_aborted" || marker.Metadata["turnId"] != turnID || !strings.Contains(marker.Text, "<turn_aborted>") {
		t.Fatalf("interrupted marker item = %#v", record.Items)
	}
	snapshots := record.Metadata.RolloutTurns
	if len(snapshots) != 1 {
		t.Fatalf("rollout turn snapshots = %#v", snapshots)
	}
	snapshot := snapshots[0]
	if snapshot.ID != turnID || snapshot.Status != string(TurnStatusInterrupted) {
		t.Fatalf("rollout turn snapshot = %#v", snapshot)
	}
	if snapshot.StartedAt == nil || snapshot.CompletedAt == nil || snapshot.DurationMS == nil {
		t.Fatalf("rollout turn timing = %#v", snapshot)
	}
}

func TestRuntimeRouterTurnFailureWritesRolloutTurnLifecycle(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        &failThenOKRuntimeAgent{},
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello failed lifecycle",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "fail lifecycle",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusFailed)

	snapshots := rolloutTurnSnapshotsForThread(t, store, threadID)
	if len(snapshots) != 1 {
		t.Fatalf("rollout turn snapshots = %#v", snapshots)
	}
	snapshot := snapshots[0]
	if snapshot.ID != turnID || snapshot.Status != string(TurnStatusFailed) || !strings.Contains(snapshot.ErrorMessage, "context deadline exceeded") {
		t.Fatalf("rollout turn snapshot = %#v", snapshot)
	}
	if snapshot.StartedAt == nil || snapshot.CompletedAt == nil || snapshot.DurationMS == nil {
		t.Fatalf("rollout turn timing = %#v", snapshot)
	}
}

func TestRuntimeRouterSessionItemsForTurnPersistsAllModelResponses(t *testing.T) {
	router := &RuntimeRouter{}
	createdAt := time.Unix(1781717000, 0).UTC()
	result := &turn.AgentLoopResult{
		Responses: []*model.AgentResponse{{
			ResponseID:  "resp-first",
			RequestID:   "req-first",
			ServerModel: "gpt-server-first",
			Model:       "gpt-test",
			ProviderID:  model.OpenAIProviderID,
			Items: []model.AgentItem{{
				ID:   "reasoning-1",
				Type: "reasoning",
				Text: "thinking",
			}, {
				ID:     "call-1",
				Type:   "function_call",
				Name:   "echo",
				CallID: "call-1",
			}, {
				ID:     "call-2",
				Type:   "function_call",
				Name:   "echo",
				CallID: "call-2",
			}},
		}, {
			ResponseID: "resp-final",
			Model:      "gpt-test",
			ProviderID: model.OpenAIProviderID,
			Items: []model.AgentItem{{
				ID:   "msg-final",
				Type: "agent_message",
				Text: "done",
			}},
		}},
		ToolExecutions: []turn.ToolExecutionResult{{
			Invocation: &tool.Invocation{
				CallID:   "call-1",
				ToolName: tool.PlainName("echo"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
			},
			Output: &tool.Output{CallID: "call-1", Success: true, Body: "tool ok"},
		}, {
			Invocation: &tool.Invocation{
				CallID:   "call-2",
				ToolName: tool.PlainName("echo"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
			},
			Output: &tool.Output{CallID: "call-2", Success: true, Body: "tool ok 2"},
		}},
	}
	result.Response = result.Responses[1]
	items := router.sessionItemsForTurn("turn-app", &turn.TurnStartParams{Prompt: "hello"}, result, createdAt)

	reasoning := sessionItemByID(items, "reasoning-1")
	if reasoning == nil || reasoning.ResponseID != "resp-first" || reasoning.Metadata["responseId"] != "resp-first" {
		t.Fatalf("reasoning item = %#v", reasoning)
	}
	if reasoning.Metadata["requestId"] != "req-first" || reasoning.Metadata["server_model"] != "gpt-server-first" {
		t.Fatalf("reasoning metadata = %#v", reasoning.Metadata)
	}
	final := sessionItemByID(items, "msg-final")
	if final == nil || final.ResponseID != "resp-final" || final.Metadata["response_id"] != "resp-final" {
		t.Fatalf("final item = %#v", final)
	}
	if sessionItemByID(items, "call-1") != nil {
		t.Fatalf("tool call response item should be represented by tool execution, items = %#v", items)
	}
	reasoningIndex := sessionItemIndexByID(items, "reasoning-1")
	call1Index := sessionItemIndexByTypeAndCallID(items, "function_call", "call-1")
	call2Index := sessionItemIndexByTypeAndCallID(items, "function_call", "call-2")
	output1Index := sessionItemIndexByTypeAndCallID(items, "tool_output", "call-1")
	output2Index := sessionItemIndexByTypeAndCallID(items, "tool_output", "call-2")
	finalIndex := sessionItemIndexByID(items, "msg-final")
	if reasoningIndex < 0 || call1Index < 0 || call2Index < 0 || output1Index < 0 || output2Index < 0 || finalIndex < 0 ||
		!(reasoningIndex < call1Index && call1Index < call2Index && call2Index < output1Index && output1Index < output2Index && output2Index < finalIndex) {
		t.Fatalf("item order = %#v", items)
	}
	if items[call1Index].Metadata["request_id"] != "req-first" || items[output1Index].Metadata["requestId"] != "req-first" {
		t.Fatalf("tool response metadata call=%#v output=%#v", items[call1Index].Metadata, items[output1Index].Metadata)
	}
}

func TestRuntimeRouterFinishTurnClearsDiffTracker(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{ThreadStatus: NewThreadStatusManager()})
	startedAtMS := time.Now().UTC().UnixMilli()
	cases := []struct {
		name     string
		threadID string
		turnID   string
		finish   func()
	}{{
		name:     "failed",
		threadID: "thread-failed",
		turnID:   "turn-failed",
		finish: func() {
			router.finishTurnWithError("thread-failed", "turn-failed", startedAtMS, context.Canceled)
		},
	}, {
		name:     "interrupted",
		threadID: "thread-interrupted",
		turnID:   "turn-interrupted",
		finish: func() {
			router.finishTurnInterrupted("thread-interrupted", "turn-interrupted", startedAtMS)
		},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router.turnsMu.Lock()
			router.ensureActiveDiffTrackerLocked(tc.threadID, tc.turnID)
			router.turnsMu.Unlock()

			tc.finish()

			router.turnsMu.Lock()
			_, ok := router.diffs[activeTurnDiffKey(tc.threadID, tc.turnID)]
			router.turnsMu.Unlock()
			if ok {
				t.Fatalf("diff tracker for %s/%s was not cleared", tc.threadID, tc.turnID)
			}
		})
	}
}

func sessionItemByID(items []session.Item, id string) *session.Item {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func sessionItemIndexByID(items []session.Item, id string) int {
	for i := range items {
		if items[i].ID == id {
			return i
		}
	}
	return -1
}

func sessionItemIndexByType(items []session.Item, itemType string) int {
	for i := range items {
		if items[i].Type == itemType {
			return i
		}
	}
	return -1
}

func sessionItemIndexByTypeAndCallID(items []session.Item, itemType string, callID string) int {
	for i := range items {
		if items[i].Type == itemType && items[i].CallID == callID {
			return i
		}
	}
	return -1
}

func turnByID(turns []Turn, turnID string) *Turn {
	for i := range turns {
		if turns[i].ID == turnID {
			return &turns[i]
		}
	}
	return nil
}

func turnHasItemText(turn *Turn, text string) bool {
	if turn == nil {
		return false
	}
	for i := range turn.Items {
		if strings.Contains(turn.Items[i].Text, text) {
			return true
		}
	}
	return false
}

func TestRuntimeRouterTurnSteerPersistsUserInput(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newBlockingAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello steer",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hold",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForBlockingAgentStart(t, agent)

	clientID := "client-steer-1"
	steer := router.Handle(requestWithParams(t, IntID(3), MethodTurnSteer, turn.TurnSteerParams{
		ThreadID:            threadID,
		ExpectedTurnID:      turnID,
		ClientUserMessageID: clientID,
		Input: []turn.TurnUserInput{{
			Text: "extra user input",
			TextElements: []turn.TextElement{{
				ByteRange:   turn.ByteRange{Start: 0, End: 5},
				Placeholder: stringPtrIfNotEmpty("<steer>"),
			}},
		}},
		AdditionalContext: map[string]turn.AdditionalContextEntry{
			"ide": {Value: "selection", Kind: turn.AdditionalContextApplication},
		},
	}))
	if steer.Error != nil {
		t.Fatalf("turn steer error: %+v", steer.Error)
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read thread error = %v", err)
	}
	var steered *session.Item
	for i := range record.Items {
		if record.Items[i].ID == clientID {
			steered = &record.Items[i]
			break
		}
	}
	if steered == nil || steered.Text != "extra user input" || steered.Metadata["steered"] != true {
		t.Fatalf("steered item = %#v", steered)
	}
	additional, ok := steered.Metadata["additionalContext"].(map[string]any)
	if !ok || additional["ide"] == nil {
		t.Fatalf("additional context = %#v", steered.Metadata["additionalContext"])
	}
	started := waitForItemStarted(t, sink, clientID)
	if started.ThreadID != threadID || started.TurnID != turnID || started.Item["type"] != "userMessage" {
		t.Fatalf("item started = %#v", started)
	}
	if started.Item["clientId"] != clientID {
		t.Fatalf("started clientId = %#v", started.Item["clientId"])
	}
	startedContent, ok := started.Item["content"].([]any)
	if !ok || len(startedContent) != 1 {
		t.Fatalf("started content = %#v", started.Item["content"])
	}
	startedText, ok := startedContent[0].(map[string]any)
	if !ok || startedText["type"] != "text" || startedText["text"] != "extra user input" {
		t.Fatalf("started text content = %#v", startedContent[0])
	}
	startedElements, ok := startedText["text_elements"].([]any)
	if !ok || len(startedElements) != 1 {
		t.Fatalf("started text_elements = %#v", startedText["text_elements"])
	}
	startedElement, ok := startedElements[0].(map[string]any)
	startedRange, rangeOK := startedElement["byteRange"].(map[string]any)
	if !ok || !rangeOK || startedRange["start"] != float64(0) || startedRange["end"] != float64(5) || startedElement["placeholder"] != "<steer>" {
		t.Fatalf("started text element = %#v", startedElements[0])
	}
	completed := waitForItemCompleted(t, sink, clientID)
	if completed.ThreadID != threadID || completed.TurnID != turnID {
		t.Fatalf("item completed = %#v", completed)
	}

	interrupt := router.Handle(requestWithParams(t, IntID(4), MethodTurnInterrupt, turn.TurnInterruptParams{
		ThreadID: threadID,
		TurnID:   turnID,
	}))
	if interrupt.Error != nil {
		t.Fatalf("interrupt error: %+v", interrupt.Error)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusInterrupted)
}

func TestRuntimeRouterTurnSteerDeliveredToNextAgentSampling(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newSteerAwareToolAgent()
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ToolRouter:   tool.NewRouter(registry),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello steer delivery",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "start tools",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForSteerAwareFirstRequest(t, agent)

	steer := router.Handle(requestWithParams(t, IntID(3), MethodTurnSteer, turn.TurnSteerParams{
		ThreadID:       threadID,
		ExpectedTurnID: turnID,
		Input: []turn.TurnUserInput{{
			Text: "live steer input",
		}},
	}))
	if steer.Error != nil {
		t.Fatalf("turn steer error: %+v", steer.Error)
	}
	agent.releaseFirstResponse()
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if !agent.sawSteerText("live steer input") {
		t.Fatalf("agent requests did not include steer input: %#v", agent.snapshotRequests())
	}
}

func TestRuntimeRouterTurnStartUsesProjectConfigFromThreadCWD(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	projectTrust := strings.ReplaceAll(filepath.Clean(project), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-user\"\nmodel_provider = \"openai\"\n\n[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user config returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .codex returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".codex", "instructions.md"), []byte("project instructions"), 0o600); err != nil {
		t.Fatalf("WriteFile instructions returned error: %v", err)
	}
	if err := os.WriteFile(config.ProjectConfigPath(project), []byte("model = \"gpt-project\"\nmodel_instructions_file = \"instructions.md\"\nmodel_reasoning_effort = \"high\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile project config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    project,
		Prompt: "hello project",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	startResult := threadStart.Result.(*ThreadStartResponse)
	if startResult.Model != "gpt-project" || startResult.ModelProvider != "openai" || startResult.ReasoningEffort == nil || *startResult.ReasoningEffort != "high" {
		t.Fatalf("thread start result = %+v", startResult)
	}
	threadID := startResult.Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello project",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Model != "gpt-project" || request.ProviderID != "openai" || request.ReasoningEffort != "high" || request.Instructions != "project instructions" {
		t.Fatalf("agent request = %#v", request)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	usage := waitForTokenUsageUpdated(t, sink, turnID)
	if usage.TokenUsage.InputTokens != 2 || usage.TokenUsage.CachedInputTokens != 1 || usage.TokenUsage.OutputTokens != 3 || usage.TokenUsage.ReasoningOutputTokens != 1 {
		t.Fatalf("usage = %+v", usage.TokenUsage)
	}
	if usage.TokenUsage.ModelContextWindow == nil || *usage.TokenUsage.ModelContextWindow <= 0 {
		t.Fatalf("modelContextWindow = %#v", usage.TokenUsage.ModelContextWindow)
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read thread error = %v", err)
	}
	tokenStatus, ok := record.Metadata.Extra["token_status"].(map[string]any)
	if !ok || tokenStatus["activeContextTokens"] == nil {
		t.Fatalf("token_status = %#v", record.Metadata.Extra["token_status"])
	}
	lastUsage, ok := record.Metadata.Extra["last_token_usage"].(map[string]any)
	if !ok || lastUsage["totalTokens"] == nil {
		t.Fatalf("last_token_usage = %#v", record.Metadata.Extra["last_token_usage"])
	}
}

func TestRuntimeRouterThreadStartServiceTierFiltersByModelCatalog(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"mock-model\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Models:       model.NewModelService(nil),
		ThreadStatus: NewThreadStatusManager(),
	})

	unsupported := "experimental-tier-id"
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:         t.TempDir(),
		ServiceTier: &unsupported,
	}))
	if threadStart.Error != nil {
		t.Fatalf("unsupported tier start error: %+v", threadStart.Error)
	}
	startResult := threadStart.Result.(*ThreadStartResponse)
	if startResult.ServiceTier != nil {
		t.Fatalf("unsupported tier response = %+v", startResult.ServiceTier)
	}
	record, err := store.Read(session.ThreadID(startResult.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read unsupported tier record error = %v", err)
	}
	if record.Metadata.ServiceTier != "" {
		t.Fatalf("unsupported tier metadata = %q", record.Metadata.ServiceTier)
	}

	modelID, serviceTierID := modelWithServiceTierForRuntimeTest(t)
	supported := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD:         t.TempDir(),
		Model:       modelID,
		ServiceTier: &serviceTierID,
	}))
	if supported.Error != nil {
		t.Fatalf("supported tier start error: %+v", supported.Error)
	}
	supportedResult := supported.Result.(*ThreadStartResponse)
	if supportedResult.ServiceTier == nil || *supportedResult.ServiceTier != serviceTierID {
		t.Fatalf("supported tier response = %+v", supportedResult.ServiceTier)
	}

	ephemeral := router.Handle(requestWithParams(t, IntID(3), MethodThreadStart, ThreadStartParams{
		CWD:         t.TempDir(),
		Ephemeral:   true,
		ServiceTier: &unsupported,
	}))
	if ephemeral.Error != nil {
		t.Fatalf("ephemeral unsupported tier start error: %+v", ephemeral.Error)
	}
	ephemeralResult := ephemeral.Result.(*ThreadStartResponse)
	if ephemeralResult.ServiceTier != nil {
		t.Fatalf("ephemeral unsupported tier response = %+v", ephemeralResult.ServiceTier)
	}
	ephemeralRecord, err := router.threadRecord(session.ThreadID(ephemeralResult.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("ephemeral threadRecord error = %v", err)
	}
	if ephemeralRecord.Metadata.ServiceTier != "" {
		t.Fatalf("ephemeral unsupported tier metadata = %q", ephemeralRecord.Metadata.ServiceTier)
	}

	defaultStart := router.Handle(requestWithParams(t, IntID(4), MethodThreadStart, map[string]any{
		"cwd":         t.TempDir(),
		"serviceTier": model.ServiceTierDefaultRequestValue,
	}))
	if defaultStart.Error != nil {
		t.Fatalf("default tier start error: %+v", defaultStart.Error)
	}
	defaultResult := defaultStart.Result.(*ThreadStartResponse)
	if defaultResult.ServiceTier == nil || *defaultResult.ServiceTier != model.ServiceTierDefaultRequestValue {
		t.Fatalf("default tier response = %+v", defaultResult.ServiceTier)
	}
}

func TestRuntimeRouterThreadStartProviderModelFallbackUsesBedrockStaticCatalog(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = \"amazon-bedrock\"\nmodel = \"gpt-5.4-mini\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		ThreadStatus: NewThreadStatusManager(),
	})

	configuredFallback := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                        t.TempDir(),
		AllowProviderModelFallback: true,
	}))
	if configuredFallback.Error != nil {
		t.Fatalf("configured fallback start error: %+v", configuredFallback.Error)
	}
	configuredResult := configuredFallback.Result.(*ThreadStartResponse)
	if configuredResult.Model != model.AmazonBedrockGPT55ModelID || configuredResult.ModelProvider != model.AmazonBedrockProviderID {
		t.Fatalf("configured fallback response = model:%q provider:%q", configuredResult.Model, configuredResult.ModelProvider)
	}
	record, err := store.Read(session.ThreadID(configuredResult.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read configured fallback record error = %v", err)
	}
	if record.Metadata.Model != model.AmazonBedrockGPT55ModelID {
		t.Fatalf("configured fallback metadata model = %q", record.Metadata.Model)
	}

	supported := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD:                        t.TempDir(),
		Model:                      model.AmazonBedrockGPT54ModelID,
		AllowProviderModelFallback: true,
	}))
	if supported.Error != nil {
		t.Fatalf("supported fallback start error: %+v", supported.Error)
	}
	supportedResult := supported.Result.(*ThreadStartResponse)
	if supportedResult.Model != model.AmazonBedrockGPT54ModelID {
		t.Fatalf("supported fallback model = %q", supportedResult.Model)
	}

	unsupportedNoFallback := router.Handle(requestWithParams(t, IntID(3), MethodThreadStart, ThreadStartParams{
		CWD:                        t.TempDir(),
		Model:                      "gpt-5.4-mini",
		AllowProviderModelFallback: false,
	}))
	if unsupportedNoFallback.Error != nil {
		t.Fatalf("unsupported no fallback start error: %+v", unsupportedNoFallback.Error)
	}
	unsupportedNoFallbackResult := unsupportedNoFallback.Result.(*ThreadStartResponse)
	if unsupportedNoFallbackResult.Model != "gpt-5.4-mini" {
		t.Fatalf("unsupported no fallback model = %q", unsupportedNoFallbackResult.Model)
	}

	dynamicProvider := router.Handle(requestWithParams(t, IntID(4), MethodThreadStart, ThreadStartParams{
		CWD:                        t.TempDir(),
		Model:                      "unlisted-dynamic-model",
		ModelProvider:              "mock_provider",
		AllowProviderModelFallback: true,
	}))
	if dynamicProvider.Error != nil {
		t.Fatalf("dynamic provider start error: %+v", dynamicProvider.Error)
	}
	dynamicProviderResult := dynamicProvider.Result.(*ThreadStartResponse)
	if dynamicProviderResult.Model != "unlisted-dynamic-model" {
		t.Fatalf("dynamic provider model = %q", dynamicProviderResult.Model)
	}
}

func TestRuntimeRouterThreadStartRuntimeWorkspaceRootsUseEffectiveCWD(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	extraRoot := t.TempDir()
	profileRoot := t.TempDir()
	profileRootKey := strings.ReplaceAll(filepath.Clean(profileRoot), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"mock-model\"\ndefault_permissions = \"dev\"\n\n[permissions.dev.workspace_roots]\n\""+profileRootKey+"\" = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		ThreadStatus: NewThreadStatusManager(),
	})

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	startResult := start.Result.(*ThreadStartResponse)
	if startResult.CWD != cwd {
		t.Fatalf("cwd = %q, want %q", startResult.CWD, cwd)
	}
	if len(startResult.RuntimeWorkspaceRoots) != 1 || !sameAppPath(startResult.RuntimeWorkspaceRoots[0], cwd) {
		t.Fatalf("runtimeWorkspaceRoots = %#v, want cwd %q", startResult.RuntimeWorkspaceRoots, cwd)
	}
	if sameAppPath(startResult.RuntimeWorkspaceRoots[0], profileRoot) {
		t.Fatalf("profile root leaked into runtimeWorkspaceRoots: %#v", startResult.RuntimeWorkspaceRoots)
	}
	record, err := store.Read(session.ThreadID(startResult.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read thread record error = %v", err)
	}
	recordRoots := stringSliceFromAny(record.Metadata.Extra["runtime_workspace_roots"])
	if len(recordRoots) != 1 || !sameAppPath(recordRoots[0], cwd) {
		t.Fatalf("record runtime roots = %#v", recordRoots)
	}

	explicit := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD:                   cwd,
		RuntimeWorkspaceRoots: []string{extraRoot, extraRoot},
	}))
	if explicit.Error != nil {
		t.Fatalf("explicit roots start error: %+v", explicit.Error)
	}
	explicitResult := explicit.Result.(*ThreadStartResponse)
	if len(explicitResult.RuntimeWorkspaceRoots) != 1 || !sameAppPath(explicitResult.RuntimeWorkspaceRoots[0], extraRoot) {
		t.Fatalf("explicit runtimeWorkspaceRoots = %#v", explicitResult.RuntimeWorkspaceRoots)
	}
	explicitRecord, err := store.Read(session.ThreadID(explicitResult.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read explicit roots thread record error = %v", err)
	}
	explicitRecordRoots := stringSliceFromAny(explicitRecord.Metadata.Extra["runtime_workspace_roots"])
	if len(explicitRecordRoots) != 1 || !sameAppPath(explicitRecordRoots[0], extraRoot) {
		t.Fatalf("explicit record runtime roots = %#v", explicitRecordRoots)
	}
}

func TestRuntimeRouterThreadStartOmittedCWDUsesDefaultCWD(t *testing.T) {
	store := session.NewStore(t.TempDir())
	defaultCWD := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		DefaultCWD:   defaultCWD,
		ThreadStatus: NewThreadStatusManager(),
	})

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	startResult := start.Result.(*ThreadStartResponse)
	if startResult.CWD != defaultCWD || startResult.Thread.CWD != defaultCWD {
		t.Fatalf("start cwd = response:%q thread:%q, want %q", startResult.CWD, startResult.Thread.CWD, defaultCWD)
	}
	if len(startResult.RuntimeWorkspaceRoots) != 1 || !sameAppPath(startResult.RuntimeWorkspaceRoots[0], defaultCWD) {
		t.Fatalf("runtimeWorkspaceRoots = %#v, want default cwd %q", startResult.RuntimeWorkspaceRoots, defaultCWD)
	}
	record, err := store.Read(session.ThreadID(startResult.Thread.ID), true, true)
	if err != nil {
		t.Fatalf("Read thread record error = %v", err)
	}
	if record.Metadata.CWD != defaultCWD {
		t.Fatalf("record cwd = %q, want %q", record.Metadata.CWD, defaultCWD)
	}
	recordRoots := stringSliceFromAny(record.Metadata.Extra["runtime_workspace_roots"])
	if len(recordRoots) != 1 || !sameAppPath(recordRoots[0], defaultCWD) {
		t.Fatalf("record runtime roots = %#v, want default cwd %q", recordRoots, defaultCWD)
	}

	ephemeral := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{Ephemeral: true}))
	if ephemeral.Error != nil {
		t.Fatalf("ephemeral thread start error: %+v", ephemeral.Error)
	}
	ephemeralResult := ephemeral.Result.(*ThreadStartResponse)
	if ephemeralResult.CWD != defaultCWD || ephemeralResult.Thread.CWD != defaultCWD {
		t.Fatalf("ephemeral cwd = response:%q thread:%q, want %q", ephemeralResult.CWD, ephemeralResult.Thread.CWD, defaultCWD)
	}
	if len(ephemeralResult.RuntimeWorkspaceRoots) != 1 || !sameAppPath(ephemeralResult.RuntimeWorkspaceRoots[0], defaultCWD) {
		t.Fatalf("ephemeral runtimeWorkspaceRoots = %#v, want default cwd %q", ephemeralResult.RuntimeWorkspaceRoots, defaultCWD)
	}
}

func TestRuntimeRouterTurnStartRuntimeWorkspaceRootsResumeFromLoadedThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("roots ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	cwd := t.TempDir()
	extraRoot := t.TempDir()

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID:              threadID,
		Prompt:                "hello roots",
		RuntimeWorkspaceRoots: []string{extraRoot, filepath.Join(extraRoot, ".")},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read thread record error = %v", err)
	}
	recordRoots := stringSliceFromAny(record.Metadata.Extra["runtime_workspace_roots"])
	if len(recordRoots) != 1 || !sameAppPath(recordRoots[0], extraRoot) {
		t.Fatalf("record runtime roots = %#v", recordRoots)
	}

	resume := router.Handle(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{
		ThreadID:     threadID,
		ExcludeTurns: true,
	}))
	if resume.Error != nil {
		t.Fatalf("resume error: %+v", resume.Error)
	}
	resumeResult := resume.Result.(*ThreadResumeResponse)
	if len(resumeResult.RuntimeWorkspaceRoots) != 1 || !sameAppPath(resumeResult.RuntimeWorkspaceRoots[0], extraRoot) {
		t.Fatalf("resume runtimeWorkspaceRoots = %#v", resumeResult.RuntimeWorkspaceRoots)
	}

	resumeRoot := t.TempDir()
	resumeExplicit := router.Handle(requestWithParams(t, IntID(4), MethodThreadResume, ThreadResumeParams{
		ThreadID:              threadID,
		ExcludeTurns:          true,
		RuntimeWorkspaceRoots: []string{resumeRoot, filepath.Join(resumeRoot, ".")},
	}))
	if resumeExplicit.Error != nil {
		t.Fatalf("explicit resume error: %+v", resumeExplicit.Error)
	}
	resumeExplicitResult := resumeExplicit.Result.(*ThreadResumeResponse)
	if len(resumeExplicitResult.RuntimeWorkspaceRoots) != 1 || !sameAppPath(resumeExplicitResult.RuntimeWorkspaceRoots[0], resumeRoot) {
		t.Fatalf("explicit resume runtimeWorkspaceRoots = %#v", resumeExplicitResult.RuntimeWorkspaceRoots)
	}
	record, err = store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read explicit resume record error = %v", err)
	}
	recordRoots = stringSliceFromAny(record.Metadata.Extra["runtime_workspace_roots"])
	if len(recordRoots) != 1 || !sameAppPath(recordRoots[0], resumeRoot) {
		t.Fatalf("explicit resume record runtime roots = %#v", recordRoots)
	}
}

func TestRuntimeRouterThreadStartElevatedSandboxPersistsProjectTrust(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll project config dir returned error: %v", err)
	}
	if err := os.WriteFile(config.ProjectConfigPath(workspace), []byte("model_reasoning_effort = \"high\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile project config returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-user\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		ThreadStatus: NewThreadStatusManager(),
	})

	first := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:     workspace,
		Sandbox: "workspace-write",
	}))
	if first.Error != nil {
		t.Fatalf("first start error: %+v", first.Error)
	}
	body, err := os.ReadFile(config.ConfigPath(home))
	if err != nil {
		t.Fatalf("ReadFile config returned error: %v", err)
	}
	if !strings.Contains(string(body), "[projects."+strconv.Quote(filepath.Clean(workspace))+"]") || !strings.Contains(string(body), "trust_level = \"trusted\"") {
		t.Fatalf("trusted project config = %s", string(body))
	}

	second := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{CWD: workspace}))
	if second.Error != nil {
		t.Fatalf("second start error: %+v", second.Error)
	}
	secondResult := second.Result.(*ThreadStartResponse)
	if secondResult.ReasoningEffort == nil || *secondResult.ReasoningEffort != "high" {
		t.Fatalf("second reasoning effort = %+v", secondResult.ReasoningEffort)
	}
}

func TestRuntimeRouterThreadStartProjectTrustWriteGuards(t *testing.T) {
	readOnlyHome := t.TempDir()
	readOnlyWorkspace := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(readOnlyHome), []byte("model = \"gpt-user\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile read-only config returned error: %v", err)
	}
	readOnlyRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(readOnlyHome, "sessions"))),
		Config:       config.NewConfigService(readOnlyHome),
		ThreadStatus: NewThreadStatusManager(),
	})
	readOnly := readOnlyRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:     readOnlyWorkspace,
		Sandbox: "read-only",
	}))
	if readOnly.Error != nil {
		t.Fatalf("read-only start error: %+v", readOnly.Error)
	}
	readOnlyBody, err := os.ReadFile(config.ConfigPath(readOnlyHome))
	if err != nil {
		t.Fatalf("ReadFile read-only config returned error: %v", err)
	}
	if strings.Contains(string(readOnlyBody), "trust_level = \"trusted\"") {
		t.Fatalf("read-only config unexpectedly trusted project: %s", string(readOnlyBody))
	}

	untrustedHome := t.TempDir()
	untrustedWorkspace := t.TempDir()
	untrustedConfig := "model = \"gpt-user\"\n\n[projects." + strconv.Quote(filepath.Clean(untrustedWorkspace)) + "]\ntrust_level = \"untrusted\"\n"
	if err := os.WriteFile(config.ConfigPath(untrustedHome), []byte(untrustedConfig), 0o600); err != nil {
		t.Fatalf("WriteFile untrusted config returned error: %v", err)
	}
	untrustedRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(untrustedHome, "sessions"))),
		Config:       config.NewConfigService(untrustedHome),
		ThreadStatus: NewThreadStatusManager(),
	})
	untrusted := untrustedRouter.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD:     untrustedWorkspace,
		Sandbox: "workspace-write",
	}))
	if untrusted.Error != nil {
		t.Fatalf("untrusted start error: %+v", untrusted.Error)
	}
	untrustedBody, err := os.ReadFile(config.ConfigPath(untrustedHome))
	if err != nil {
		t.Fatalf("ReadFile untrusted config returned error: %v", err)
	}
	if string(untrustedBody) != untrustedConfig {
		t.Fatalf("untrusted config changed:\n%s", string(untrustedBody))
	}

	gitHome := t.TempDir()
	repoRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir .git returned error: %v", err)
	}
	nested := filepath.Join(repoRoot, "nested", "project")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(gitHome), []byte("model = \"gpt-user\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile git config returned error: %v", err)
	}
	gitRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(gitHome, "sessions"))),
		Config:       config.NewConfigService(gitHome),
		ThreadStatus: NewThreadStatusManager(),
	})
	gitStart := gitRouter.Handle(requestWithParams(t, IntID(3), MethodThreadStart, ThreadStartParams{
		CWD:     nested,
		Sandbox: "workspace-write",
	}))
	if gitStart.Error != nil {
		t.Fatalf("git start error: %+v", gitStart.Error)
	}
	gitBody, err := os.ReadFile(config.ConfigPath(gitHome))
	if err != nil {
		t.Fatalf("ReadFile git config returned error: %v", err)
	}
	if !strings.Contains(string(gitBody), "[projects."+strconv.Quote(filepath.Clean(repoRoot))+"]") {
		t.Fatalf("git config did not trust repo root: %s", string(gitBody))
	}
	if strings.Contains(string(gitBody), "[projects."+strconv.Quote(filepath.Clean(nested))+"]") {
		t.Fatalf("git config trusted nested cwd: %s", string(gitBody))
	}
}

func TestRuntimeRouterThreadSettingsUpdateAffectsFutureTurn(t *testing.T) {
	home := t.TempDir()
	initialProject := t.TempDir()
	updatedProject := t.TempDir()
	if err := os.MkdirAll(filepath.Join(updatedProject, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .codex returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(updatedProject, ".codex", "instructions.md"), []byte("updated project instructions"), 0o600); err != nil {
		t.Fatalf("WriteFile instructions returned error: %v", err)
	}
	if err := os.WriteFile(config.ProjectConfigPath(updatedProject), []byte("model_instructions_file = \"instructions.md\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile project config returned error: %v", err)
	}
	projectTrust := strings.ReplaceAll(filepath.Clean(updatedProject), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user config returned error: %v", err)
	}
	modelID, serviceTierID := modelWithServiceTierForRuntimeTest(t)
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadExtras: NewThreadExtraService(),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    initialProject,
		Prompt: "initial",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	effort := "high"
	summary := "auto"
	personality := "friendly"
	settingsUpdate := router.Handle(requestWithParams(t, IntID(2), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:    threadID,
		CWD:         &updatedProject,
		Model:       &modelID,
		ServiceTier: &ThreadExtraOptionalString{Set: true, Value: &serviceTierID},
		Effort:      &effort,
		Summary:     &summary,
		Personality: &personality,
	}))
	if settingsUpdate.Error != nil {
		t.Fatalf("settings update error: %+v", settingsUpdate.Error)
	}

	turnStart := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "uses updated settings",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Model != modelID ||
		request.Instructions != "updated project instructions" ||
		request.ServiceTier != serviceTierID ||
		request.ReasoningEffort != effort ||
		request.ReasoningSummary != summary {
		t.Fatalf("agent request = %#v", request)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestRuntimeRouterTurnStartSettingsOverrideEmitsThreadSettingsUpdated(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadExtras: NewThreadExtraService(),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	modelID, _ := modelWithServiceTierForRuntimeTest(t)
	effort := "low"
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello",
		Model:    modelID,
		Effort:   &effort,
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	updated := lastThreadSettingsNotification(t, sink, threadID)
	if updated.ThreadSettings.Model != modelID || updated.ThreadSettings.Effort == nil || *updated.ThreadSettings.Effort != effort {
		t.Fatalf("thread/settings/updated = %+v", updated)
	}
	firstRequest := waitForRuntimeAgentRequest(t, agent)
	if firstRequest.Model != modelID || firstRequest.ReasoningEffort != effort {
		t.Fatalf("first agent request = %#v", firstRequest)
	}
	firstTurnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, firstTurnID, TurnStatusCompleted)

	secondTurnStart := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "again",
	}))
	if secondTurnStart.Error != nil {
		t.Fatalf("second turn start error: %+v", secondTurnStart.Error)
	}
	secondRequest := waitForRuntimeAgentRequest(t, agent)
	if secondRequest.Model != modelID || secondRequest.ReasoningEffort != effort {
		t.Fatalf("second agent request = %#v", secondRequest)
	}
}

func TestRuntimeRouterTurnStartIgnoresDeprecatedMultiAgentMode(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	extras := NewThreadExtraService()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadExtras: extras,
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
	})

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	router.SetNotificationSink(sink)

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, map[string]any{
		"threadId":       threadID,
		"prompt":         "hello",
		"multiAgentMode": "proactive",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Prompt != "hello" {
		t.Fatalf("agent prompt = %q, want hello", request.Prompt)
	}
	if strings.Contains(request.Instructions, "Proactive multi-agent delegation is active.") {
		t.Fatalf("deprecated multiAgentMode leaked into instructions: %q", request.Instructions)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if sinkHasMethod(sink, NotificationThreadSettingsUpdated) {
		t.Fatalf("deprecated multiAgentMode should not emit settings update: %+v", sink.List())
	}
	if settings := extras.Settings(threadID); settings != nil && settings.MultiAgentMode != "" {
		t.Fatalf("deprecated multiAgentMode persisted to settings: %+v", settings)
	}
}

func TestRuntimeRouterThreadStartIgnoresDeprecatedMultiAgentMode(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
	})

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, map[string]any{
		"multiAgentMode": "proactive",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	startResponse := threadStart.Result.(*ThreadStartResponse)
	data, err := json.Marshal(startResponse)
	if err != nil {
		t.Fatalf("Marshal thread start response: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal thread start response: %v", err)
	}
	if payload["multiAgentMode"] != string(MultiAgentModeExplicitRequestOnly) {
		t.Fatalf("thread/start multiAgentMode = %#v", payload["multiAgentMode"])
	}

	threadID := startResponse.Thread.ID
	router.SetNotificationSink(sink)
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	request := waitForRuntimeAgentRequest(t, agent)
	if strings.Contains(request.Instructions, "Proactive multi-agent delegation is active.") {
		t.Fatalf("deprecated thread/start multiAgentMode leaked into instructions: %q", request.Instructions)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestRuntimeRouterTurnStartAppliesExplicitPersonality(t *testing.T) {
	store := session.NewStore(t.TempDir())
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models:       personalityModelServiceForRuntimeTest(),
	})

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		Model: "personality-model",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, map[string]any{
		"threadId":    threadID,
		"prompt":      "hello",
		"personality": "friendly",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if !strings.Contains(request.Instructions, "Base friendly personality") || !strings.Contains(request.Instructions, "<personality_spec>") {
		t.Fatalf("instructions = %q", request.Instructions)
	}
}

func TestRuntimeRouterTurnStartUsesConfigPersonalityTemplate(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"personality-model\"\npersonality = \"pragmatic\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models:       personalityModelServiceForRuntimeTest(),
	})

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Model != "personality-model" || !strings.Contains(request.Instructions, "Base pragmatic personality") {
		t.Fatalf("agent request = %#v", request)
	}
	if strings.Contains(request.Instructions, "<personality_spec>") {
		t.Fatalf("config personality should be baked, not emitted as update: %q", request.Instructions)
	}
}

func TestRuntimeRouterThreadStartEmptyInstructionOverrideSuppressesModelInstructions(t *testing.T) {
	store := session.NewStore(t.TempDir())
	agent := newRecordingRuntimeAgent("ok")
	empty := ""
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models:       personalityModelServiceForRuntimeTest(),
	})

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		Model:                 "personality-model",
		BaseInstructions:      &empty,
		DeveloperInstructions: &empty,
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Instructions != "" {
		t.Fatalf("instructions = %q, want empty", request.Instructions)
	}
}

func TestRuntimeRouterTurnStartNullServiceTierClearsConfigDefault(t *testing.T) {
	home := t.TempDir()
	modelID, serviceTierID := modelWithServiceTierForRuntimeTest(t)
	if err := os.WriteFile(config.ConfigPath(home), []byte("service_tier = "+strconv.Quote(serviceTierID)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadExtras: NewThreadExtraService(),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, map[string]any{
		"threadId":    threadID,
		"prompt":      "clear service tier",
		"model":       modelID,
		"serviceTier": nil,
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	updated := lastThreadSettingsNotification(t, sink, threadID)
	if updated.ThreadSettings.ServiceTier == nil || *updated.ThreadSettings.ServiceTier != model.ServiceTierDefaultRequestValue {
		t.Fatalf("thread/settings/updated = %+v", updated)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Model != modelID || request.ServiceTier != "" {
		t.Fatalf("agent request = %#v", request)
	}
}

func TestRuntimeRouterTurnStartInjectsEnabledPluginInstructions(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{
		Summary: plugin.PluginSummary{
			Name:            "docs",
			MarketplaceName: "local",
			DisplayName:     "Docs",
			Installed:       true,
			Enabled:         true,
			HasSkills:       true,
			MCPServers:      []string{"docs-mcp"},
			AppConnectors:   []string{"docs-app"},
		},
	})
	plugins.AddPlugin(plugin.PluginDetail{
		Summary: plugin.PluginSummary{
			Name:            "drive",
			MarketplaceName: "market",
			DisplayName:     "Drive",
			Description:     "Search Drive files",
			InstallPolicy:   plugin.InstallAllowed,
		},
	})
	mcpService := mcp.NewMCPService(nil)
	mcpService.SetServer(mcp.MCPServerStatus{
		Name:  "docs-mcp",
		State: mcp.MCPServerReady,
		Server: mcp.MCPServerInfo{
			Name: "docs-mcp",
		},
		Tools: []mcp.MCPToolInfo{{
			Name:        "search",
			Description: "Search docs",
		}},
	})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		Plugins:      plugins,
		MCP:          mcpService,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello plugin",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Input: []turn.TurnUserInput{{
			Type: "mention",
			Path: "plugin://docs@local",
		}, {
			Type: "text",
			Text: "use this plugin",
		}},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	for _, want := range []string{"## Plugins", "Capabilities from the `Docs` plugin:", "`docs-mcp`", "`docs-app`"} {
		if !strings.Contains(request.Instructions, want) {
			t.Fatalf("instructions missing %q:\n%s", want, request.Instructions)
		}
	}
	if strings.Contains(request.Instructions, "<plugin_instructions>") {
		t.Fatalf("instructions should not contain plugin wrapper tag:\n%s", request.Instructions)
	}
	inputText := inputItemText(request.InputItems)
	if !strings.Contains(inputText, "<recommended_plugins>") || !strings.Contains(inputText, "Drive (drive@market)") {
		t.Fatalf("recommended plugin input item missing: %s", inputText)
	}
	toolsJSON, err := json.Marshal(request.Tools)
	if err != nil {
		t.Fatalf("Marshal tools error = %v", err)
	}
	if !strings.Contains(string(toolsJSON), "request_plugin_install") {
		t.Fatalf("request_plugin_install tool missing: %s", toolsJSON)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestRuntimeRouterTurnStartDoesNotRecommendConnectorOnlyCandidates(t *testing.T) {
	store := session.NewStore(t.TempDir())
	agent := newRecordingRuntimeAgent("ok")
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{
		Summary: plugin.PluginSummary{
			ID:              "installed@market",
			Name:            "installed",
			DisplayName:     "Installed",
			MarketplaceName: "market",
			Installed:       true,
			Enabled:         true,
		},
		Apps: []plugin.AppSummary{{ID: "connector_docs", DisplayName: "Docs Connector"}},
	})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		Plugins:      plugins,
		ThreadStatus: NewThreadStatusManager(),
	})

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{Prompt: "hello connector"}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	inputItemsJSON, err := json.Marshal(request.InputItems)
	if err != nil {
		t.Fatalf("Marshal input items error = %v", err)
	}
	if strings.Contains(string(inputItemsJSON), "<recommended_plugins>") {
		t.Fatalf("connector-only candidate should not be in recommended plugins: %s", inputItemsJSON)
	}
	toolsJSON, err := json.Marshal(request.Tools)
	if err != nil {
		t.Fatalf("Marshal tools error = %v", err)
	}
	if strings.Contains(string(toolsJSON), "request_plugin_install") {
		t.Fatalf("request_plugin_install should not be registered for connector-only recommendations: %s", toolsJSON)
	}
}

func TestRuntimeRouterTurnStartInjectsAvailableSkills(t *testing.T) {
	skillsRoot := t.TempDir()
	visibleSkill := filepath.Join(skillsRoot, "build-stuff")
	if err := os.MkdirAll(visibleSkill, 0o755); err != nil {
		t.Fatalf("MkdirAll(visible skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(visibleSkill, SkillFilename), []byte("---\nname: build-stuff\ndescription: Build code quickly\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(visible skill) error = %v", err)
	}
	hiddenSkill := filepath.Join(skillsRoot, "hidden-skill")
	if err := os.MkdirAll(filepath.Join(hiddenSkill, SkillMetadataDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(hidden skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hiddenSkill, SkillFilename), []byte("---\nname: hidden-skill\ndescription: Do not expose\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(hidden skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hiddenSkill, SkillMetadataDir, SkillMetadataFilename), []byte("policy:\n  allow_implicit_invocation: false\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(hidden metadata) error = %v", err)
	}
	pluginRoot := t.TempDir()
	pluginSkill := filepath.Join(pluginRoot, "skills", "review")
	if err := os.MkdirAll(pluginSkill, 0o755); err != nil {
		t.Fatalf("MkdirAll(plugin skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginSkill, SkillFilename), []byte("---\nname: review\ndescription: Review with plugin context\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(plugin skill) error = %v", err)
	}
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{
		MarketplaceRoot: pluginRoot,
		Summary: plugin.PluginSummary{
			Name:            "docs",
			MarketplaceName: "local",
			DisplayName:     "Docs",
			Installed:       true,
			Enabled:         true,
			HasSkills:       true,
		},
	})
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		Skills:       NewSkillsService([]string{skillsRoot}),
		Plugins:      plugins,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello skills",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "use skills",
		Input: []turn.TurnUserInput{{
			Type: "skill",
			Name: "build-stuff",
			Path: filepath.Join(visibleSkill, SkillFilename),
		}},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	for _, want := range []string{"## Skills", "build-stuff", "Build code quickly", "Docs:review", "Review with plugin context"} {
		if !strings.Contains(request.Instructions, want) {
			t.Fatalf("instructions missing %q:\n%s", want, request.Instructions)
		}
	}
	if strings.Contains(request.Instructions, "hidden-skill") {
		t.Fatalf("instructions included disabled implicit skill:\n%s", request.Instructions)
	}
	for _, want := range []string{"<skill>", "<name>build-stuff</name>", filepath.Join(visibleSkill, SkillFilename)} {
		if !agentRequestInputItemsContain(request, want) {
			t.Fatalf("input items missing explicit skill fragment %q: %#v", want, request.InputItems)
		}
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestPromptSkillMetadataPreservesPluginID(t *testing.T) {
	entries := []SkillsListEntry{{
		Name:             "Docs:review",
		Path:             filepath.Join("plugins", "docs", "skills", "review", SkillFilename),
		Scope:            "plugin",
		Description:      "Long plugin review guidance",
		ShortDescription: "Short review guidance",
		Enabled:          true,
		PluginID:         "docs@market",
	}}
	metadata := promptSkillMetadataFromEntries(entries)
	if len(metadata) != 1 || metadata[0].PluginID != "docs@market" {
		t.Fatalf("prompt skill metadata = %#v", metadata)
	}
	if metadata[0].Description != "Short review guidance" {
		t.Fatalf("prompt skill description = %q, want short description", metadata[0].Description)
	}
}

func TestRuntimeRouterAppMentionInjectsAppContext(t *testing.T) {
	description := "Drive files"
	installURL := "https://chatgpt.com/apps/google-drive/drive"
	appService := apps.NewAppService([]apps.AppEntry{{
		ID:                 "drive",
		Name:               "Google Drive",
		Description:        &description,
		InstallURL:         &installURL,
		IsAccessible:       true,
		IsEnabled:          true,
		PluginDisplayNames: []string{"Docs"},
	}})
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		Apps:         appService,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "Use $app://drive and $app://missing",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	for _, want := range []string{
		"<app>",
		"<id>drive</id>",
		"<name>Google Drive</name>",
		"<description>Drive files</description>",
		"<is_accessible>true</is_accessible>",
		"<plugin_display_names>Docs</plugin_display_names>",
		"<id>missing</id>",
		"https://chatgpt.com/apps/missing/missing",
	} {
		if !agentRequestInputItemsContain(request, want) {
			t.Fatalf("input items missing app fragment %q: %#v", want, request.InputItems)
		}
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestAppInstructionsDataHonorsExplicitDisabledState(t *testing.T) {
	data := appInstructionsData(&apps.AppEntry{
		ID:              "drive",
		Name:            "Drive",
		IsEnabled:       false,
		Enabled:         true,
		EnabledExplicit: true,
	})
	if data.IsEnabled {
		t.Fatalf("explicit disabled app rendered enabled: %+v", data)
	}

	legacy := appInstructionsData(&apps.AppEntry{
		ID:      "legacy",
		Name:    "Legacy",
		Enabled: true,
	})
	if !legacy.IsEnabled {
		t.Fatalf("legacy enabled app rendered disabled: %+v", legacy)
	}
}

func TestRuntimeRouterSkillsContextEmitsBudgetWarning(t *testing.T) {
	skillsRoot := t.TempDir()
	description := strings.Repeat("long ", 100)
	for i := 0; i < 30; i++ {
		skillDir := filepath.Join(skillsRoot, fmt.Sprintf("skill-%02d", i))
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(skill) error = %v", err)
		}
		body := fmt.Sprintf("---\nname: skill-%02d\ndescription: %s\n---\n", i, description)
		if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(skill) error = %v", err)
		}
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		Skills: NewSkillsService([]string{skillsRoot}),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
			Slug:          "tiny-skills",
			ContextWindow: 100,
		}}})),
	})
	router.SetNotificationSink(sink)
	instructions, _, _, err := router.instructionsWithSkillsContext("thread-skills", &config.Config{Values: map[string]any{"model": "tiny-skills"}}, &turn.TurnStartParams{}, "base")
	if err != nil {
		t.Fatalf("instructionsWithSkillsContext() error = %v", err)
	}
	if !strings.Contains(instructions, "## Skills") {
		t.Fatalf("instructions missing skills:\n%s", instructions)
	}
	if !sinkHasMethod(sink, NotificationWarning) {
		t.Fatalf("warning notification missing: %+v", sink.List())
	}
}

func TestRuntimeRouterSkillsContextWarnsForMissingMCPDependencies(t *testing.T) {
	skillsRoot := t.TempDir()
	skillDir := filepath.Join(skillsRoot, "calendar-skill")
	if err := os.MkdirAll(filepath.Join(skillDir, SkillMetadataDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(skill metadata) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: calendar-skill\ndescription: Calendar helper\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillMetadataDir, SkillMetadataFilename), []byte(`
dependencies:
  tools:
    - type: mcp
      value: calendar
      url: https://mcp.example.test
`), 0o600); err != nil {
		t.Fatalf("WriteFile(skill metadata) error = %v", err)
	}

	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Skills: NewSkillsService([]string{skillsRoot})})
	router.SetNotificationSink(sink)
	if _, _, _, err := router.instructionsWithSkillsContext("thread-skills", &config.Config{Values: map[string]any{}}, &turn.TurnStartParams{}, "base"); err != nil {
		t.Fatalf("instructionsWithSkillsContext() error = %v", err)
	}
	warnings := warningNotificationsForTest(sink)
	if len(warnings) != 1 || warnings[0].ThreadID == nil || *warnings[0].ThreadID != "thread-skills" || !strings.Contains(warnings[0].Message, "calendar") {
		t.Fatalf("warnings = %+v", warnings)
	}

	configuredSink := NewNotificationBuffer()
	configured := NewRuntimeRouter(RuntimeServices{Skills: NewSkillsService([]string{skillsRoot})})
	configured.SetNotificationSink(configuredSink)
	cfg := &config.Config{Values: map[string]any{
		"mcp_servers": map[string]any{
			"calendar": map[string]any{"url": "https://mcp.example.test"},
		},
	}}
	if _, _, _, err := configured.instructionsWithSkillsContext("thread-skills", cfg, &turn.TurnStartParams{}, "base"); err != nil {
		t.Fatalf("instructionsWithSkillsContext(configured) error = %v", err)
	}
	if warnings := warningNotificationsForTest(configuredSink); len(warnings) != 0 {
		t.Fatalf("configured warnings = %+v, want none", warnings)
	}
}

func warningNotificationsForTest(sink *NotificationBuffer) []*WarningNotification {
	if sink == nil {
		return nil
	}
	var out []*WarningNotification
	for _, notification := range sink.List() {
		if notification.Method != NotificationWarning {
			continue
		}
		warning, ok := notification.Params.(*WarningNotification)
		if ok {
			out = append(out, warning)
		}
	}
	return out
}

func TestRuntimeRouterImplicitSkillInvocationFromShellCommand(t *testing.T) {
	skillsRoot := t.TempDir()
	skillDir := filepath.Join(skillsRoot, "build-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	skillPath := filepath.Join(skillDir, SkillFilename)
	if err := os.WriteFile(skillPath, []byte("---\nname: build-skill\ndescription: Build helper\n---\nUse this build helper.\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newImplicitSkillRuntimeAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		Skills:       NewSkillsService([]string{skillsRoot}),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: skillDir}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		CWD:      skillDir,
		Prompt:   "run build",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	second := waitForImplicitSkillAgentRequest(t, agent, 2)
	for _, want := range []string{"<skill>", "<name>build-skill</name>", skillPath, "Use this build helper."} {
		if !agentRequestInputItemsContain(second, want) {
			t.Fatalf("second request missing implicit skill fragment %q: %#v", want, second.InputItems)
		}
	}
	if !sinkHasMethod(sink, NotificationWarning) {
		t.Fatalf("implicit skill warning notification missing: %+v", sink.List())
	}
}

func TestRuntimeRouterTurnStartUsesSelectedCapabilitySkillRoots(t *testing.T) {
	capabilityRoot := t.TempDir()
	skillDir := filepath.Join(capabilityRoot, "cap-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: cap-skill\ndescription: Capability skill\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		Skills:       NewSkillsService(nil),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: t.TempDir(),
		SelectedCapabilityRoots: []SelectedCapabilityRoot{{
			ID: "cap-root",
			Location: CapabilityRootLocation{
				Type:          CapabilityRootLocationEnvironment,
				EnvironmentID: "local",
				Path:          capabilityRoot,
			},
		}},
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "use capability skill",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if !strings.Contains(request.Instructions, "cap-skill") || !strings.Contains(request.Instructions, "Capability skill") {
		t.Fatalf("instructions missing selected capability skill:\n%s", request.Instructions)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestRuntimeRouterTurnStartUsesRemoteEnvironmentSkillRoot(t *testing.T) {
	rootURI := "file:///remote/skills"
	skillURI := "file:///remote/skills/remote-skill/SKILL.md"
	metadataURI := "file:///remote/skills/remote-skill/agents/openai.yaml"
	execServerURL, done := newRemoteSkillsExecServerForTest(t, rootURI, map[string]string{
		skillURI: "---\nname: remote-skill\ndescription: Remote capability skill\n---\n",
		metadataURI: `
interface:
  display_name: Remote Skill
  icon_small: assets/icon.png
policy:
  allow_implicit_invocation: true
`,
	})
	environment := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, "")
	if _, err := environment.Add(&EnvironmentAddParams{EnvironmentID: "remote", ExecServerURL: execServerURL}); err != nil {
		t.Fatalf("environment Add() error = %v", err)
	}
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		Skills:       NewSkillsService(nil),
		Environment:  environment,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: t.TempDir(),
		SelectedCapabilityRoots: []SelectedCapabilityRoot{{
			ID: "remote-cap-root",
			Location: CapabilityRootLocation{
				Type:          CapabilityRootLocationEnvironment,
				EnvironmentID: "remote",
				Path:          rootURI,
			},
		}},
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	remoteSkillPath := "environment://remote/remote/skills/remote-skill/SKILL.md"
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "use [remote-skill](skill://" + remoteSkillPath + ")",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if !strings.Contains(request.Instructions, "remote-skill") || !strings.Contains(request.Instructions, "Remote capability skill") || !strings.Contains(request.Instructions, "(environment: "+remoteSkillPath+")") {
		t.Fatalf("instructions missing remote selected capability skill:\n%s", request.Instructions)
	}
	for _, want := range []string{"<skill>", "<name>remote-skill</name>", remoteSkillPath, "Remote capability skill"} {
		if !agentRequestInputItemsContain(request, want) {
			t.Fatalf("input items missing remote explicit skill fragment %q: %#v", want, request.InputItems)
		}
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	waitEnvironmentInfoExecServerForTest(t, done)
}

func TestRuntimeRouterAutoCompactsWhenTokenStatusRequiresIt(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "compact soon",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	if _, err := store.UpdateMetadata(session.ThreadID(threadID), &session.MetadataPatch{
		Extra: map[string]any{"auto_compact_token_limit": 1},
	}, true); err != nil {
		t.Fatalf("UpdateMetadata() error = %v", err)
	}

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "trigger auto compact",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	notification := waitForNotificationMethod(t, sink, NotificationContextCompacted)
	compacted, ok := notification.Params.(*ContextCompactedNotification)
	if !ok || compacted.ThreadID != threadID || compacted.TurnID != turnID || compacted.Summary == "" {
		t.Fatalf("compacted notification = %#v", notification.Params)
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read thread error = %v", err)
	}
	if record.Metadata.Extra["compaction_trigger"] != string(compact.TriggerAuto) || record.Metadata.Extra["compaction_summary"] == "" {
		t.Fatalf("compaction metadata = %#v", record.Metadata.Extra)
	}
	if len(record.Items) == 0 || record.Items[len(record.Items)-1].Metadata["compact"] != true {
		t.Fatalf("compacted items = %#v", record.Items)
	}
}

func TestRuntimeRouterTurnStartRestoresThreadDynamicTools(t *testing.T) {
	store := session.NewStore(t.TempDir())
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: t.TempDir(),
		DynamicTools: []json.RawMessage{
			json.RawMessage(`{"type":"namespace","name":"codex_app","description":"Demo namespace tools","tools":[{"type":"function","name":"demo_tool","description":"Demo dynamic tool","inputSchema":{"type":"object"}}]}`),
		},
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "use dynamic tool",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if len(request.Tools) == 0 {
		t.Fatalf("agent tools empty")
	}
	found := false
	for _, rawTool := range request.Tools {
		toolMap, ok := rawTool.(map[string]any)
		if ok && toolMap["type"] == "namespace" && toolMap["name"] == "codex_app" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dynamic namespace missing from tools: %#v", request.Tools)
	}
}

func TestRuntimeRouterTurnStartRestoresSessionHistory(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	if err := store.Save(&session.Record{
		ID:        "thread-history",
		SessionID: "thread-history",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:            t.TempDir(),
			LastResponseID: "resp-prev",
		},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "older user message", CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "older assistant message", CreatedAt: now.Add(time.Second)},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})

	turnStart := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{
		ThreadID: "thread-history",
		Prompt:   "new prompt",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.PreviousResponseID != "resp-prev" || request.Store {
		t.Fatalf("agent request previous/store = %q/%v", request.PreviousResponseID, request.Store)
	}
	if len(request.InputItems) < 2 {
		t.Fatalf("input items = %#v", request.InputItems)
	}
	data, _ := json.Marshal(request.InputItems)
	if !strings.Contains(string(data), "older user message") || !strings.Contains(string(data), "older assistant message") {
		t.Fatalf("input items json = %s", data)
	}
}

func TestRuntimeRouterTurnStartRepairsRolloutOnlyThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     store.Root(),
		SessionID:     "session-rollout-runtime",
		ThreadID:      "thread-rollout-runtime",
		Source:        "cli",
		CWD:           "D:/rollout-runtime",
		Model:         "gpt-rollout",
		ModelProvider: "openai",
		HistoryMode:   "paginated",
		Extra:         map[string]any{"last_response_id": "resp-rollout-prev"},
		Now:           fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	for _, item := range []rollout.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "rollout older user", Metadata: map[string]any{"turnId": "turn-old"}},
		{ID: "a1", Type: "message", Role: "assistant", Text: "rollout older assistant", Metadata: map[string]any{"turnId": "turn-old"}},
	} {
		if err := recorder.AppendItem(item); err != nil {
			t.Fatalf("AppendItem() error = %v", err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})

	turnStart := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{
		ThreadID: "thread-rollout-runtime",
		Prompt:   "new prompt",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Model != "gpt-rollout" || request.ProviderID != "openai" {
		t.Fatalf("agent request model/provider = %q/%q", request.Model, request.ProviderID)
	}
	if request.PreviousResponseID != "resp-rollout-prev" || request.Store {
		t.Fatalf("agent request previous/store = %q/%v", request.PreviousResponseID, request.Store)
	}
	data, _ := json.Marshal(request.InputItems)
	if !strings.Contains(string(data), "rollout older user") || !strings.Contains(string(data), "rollout older assistant") {
		t.Fatalf("input items json = %s", data)
	}
	record := waitForSessionItem(t, store, "thread-rollout-runtime", "agent_message")
	if len(record.Items) < 3 {
		t.Fatalf("record items = %#v", record.Items)
	}
}

func TestRuntimeRouterAppTurnStoreOnlyForAzureResponsesProvider(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	runConfig, err := router.appTurnModelProviderConfig(&config.Config{Values: map[string]any{
		"model_provider": "azure",
		"model_providers": map[string]any{
			"azure": map[string]any{
				"name":     "Azure",
				"base_url": "https://example.openai.azure.com/openai",
				"wire_api": "responses",
			},
		},
	}}, &turn.TurnStartParams{})
	if err != nil {
		t.Fatalf("appTurnModelProviderConfig() error = %v", err)
	}
	if !runConfig.Store {
		t.Fatalf("Azure responses provider Store = false, want true")
	}

	runConfig, err = router.appTurnModelProviderConfig(&config.Config{Values: map[string]any{
		"model_provider": "openai",
	}}, &turn.TurnStartParams{})
	if err != nil {
		t.Fatalf("appTurnModelProviderConfig(openai) error = %v", err)
	}
	if runConfig.Store {
		t.Fatalf("OpenAI provider Store = true, want false")
	}
}

func TestRuntimeRouterTurnStartInjectsAdditionalContext(t *testing.T) {
	store := session.NewStore(t.TempDir())
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello context",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "use context",
		AdditionalContext: map[string]turn.AdditionalContextEntry{
			"app":  {Value: "developer-only context", Kind: turn.AdditionalContextApplication},
			"user": {Value: "untrusted context", Kind: turn.AdditionalContextUntrusted},
		},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if !strings.Contains(request.Instructions, "developer-only context") || !strings.Contains(request.Instructions, "<additional_context>") {
		t.Fatalf("instructions = %q", request.Instructions)
	}
	inputText := inputItemText(request.InputItems)
	if !strings.Contains(inputText, "untrusted context") || !strings.Contains(inputText, "<additional_context>") {
		t.Fatalf("input items = %#v", request.InputItems)
	}
}

func TestRuntimeRouterSessionStartHookInjectsAdditionalContextOnce(t *testing.T) {
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	hooks := NewHookRegistry()
	hook := hookRunnerMetadata("session-start-clear", HookEventSessionStart, "clear", 0)
	command := hookRunnerOutputCommand(`{"hookSpecificOutput":{"additionalContext":"clear hook context"}}`, "")
	hook.Command = &command
	if err := hooks.Add(cwd, hook); err != nil {
		t.Fatalf("Hooks.Add() error = %v", err)
	}
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Hooks:        hooks,
		HookRunner:   NewHookRunner(),
	})
	router.SetNotificationSink(sink)

	clearSource := string(SessionStartSourceClear)
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                cwd,
		SessionStartSource: &clearSource,
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read start record error = %v", err)
	}
	if got := stringFromMap(record.Metadata.Extra, pendingSessionStartSourceExtraKey); got != string(SessionStartSourceClear) {
		t.Fatalf("pending session start source = %q, want clear", got)
	}

	firstTurn := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "first",
	}))
	if firstTurn.Error != nil {
		t.Fatalf("first turn start error: %+v", firstTurn.Error)
	}
	firstTurnID := firstTurn.Result.(*turn.TurnStartResponse).Turn.ID
	firstRequest := waitForRuntimeAgentRequest(t, agent)
	if !strings.Contains(firstRequest.Instructions, "clear hook context") {
		t.Fatalf("first request instructions = %q", firstRequest.Instructions)
	}
	record, err = store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read consumed record error = %v", err)
	}
	if got := stringFromMap(record.Metadata.Extra, pendingSessionStartSourceExtraKey); got != "" {
		t.Fatalf("pending session start source after first turn = %q", got)
	}
	waitForTurnCompletedStatus(t, sink, firstTurnID, TurnStatusCompleted)
	hookCompletedCount := func() int {
		count := 0
		for _, notification := range sink.List() {
			if notification.Method == NotificationHookCompleted {
				count++
			}
		}
		return count
	}
	if count := hookCompletedCount(); count != 1 {
		t.Fatalf("hook completed count after first turn = %d, want 1", count)
	}

	secondTurn := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "second",
	}))
	if secondTurn.Error != nil {
		t.Fatalf("second turn start error: %+v", secondTurn.Error)
	}
	secondTurnID := secondTurn.Result.(*turn.TurnStartResponse).Turn.ID
	secondRequest := waitForRuntimeAgentRequest(t, agent)
	if strings.Contains(secondRequest.Instructions, "clear hook context") {
		t.Fatalf("second request should not include session hook context: %q", secondRequest.Instructions)
	}
	waitForTurnCompletedStatus(t, sink, secondTurnID, TurnStatusCompleted)
	if count := hookCompletedCount(); count != 1 {
		t.Fatalf("hook completed count after second turn = %d, want 1", count)
	}
}

func TestRuntimeRouterRequestUserInputToolUsesServerRequest(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newUserInputToolAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestToolUserInput {
			return
		}
		params, ok := request.Params.(*ToolRequestUserInputParams)
		if !ok || params == nil {
			t.Errorf("request params = %#v", request.Params)
			return
		}
		if params.ThreadID == "" || params.TurnID == "" || len(params.Questions) != 1 || params.Questions[0].ID != "choice" {
			t.Errorf("user input params = %#v", params)
			return
		}
		if !params.Questions[0].IsOther || !params.Questions[0].IsSecret {
			t.Errorf("user input flags = %#v", params.Questions[0])
			return
		}
		response := &ToolRequestUserInputResponse{
			Answers: map[string]ToolRequestUserInputAnswer{
				"choice": {Answers: []string{"Blue"}},
			},
		}
		if _, err := router.requireServerRequests().Resolve(OK(request.ID, response)); err != nil {
			t.Errorf("Resolve user input response error = %v", err)
		}
	}))

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "ask",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "ask user",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID

	completed := waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if completed.ThreadID != threadID {
		t.Fatalf("completed thread id = %q, want %q", completed.ThreadID, threadID)
	}
	if agent.answer != "Blue" {
		t.Fatalf("agent answer = %q", agent.answer)
	}
	resolved := waitForNotificationMethod(t, sink, NotificationServerRequestResolved)
	resolvedParams, ok := resolved.Params.(*ServerRequestResolvedNotification)
	if !ok || resolvedParams.ThreadID != threadID || resolvedParams.RequestID.IsZero() {
		t.Fatalf("serverRequest/resolved params = %#v", resolved.Params)
	}
	if !sinkHasThreadStatusFlag(sink, threadID, ThreadActiveFlagWaitingOnUserInput) {
		t.Fatalf("missing waitingOnUserInput status in %#v", sink.List())
	}
	if got := router.services.ThreadStatus.LoadedStatusForThread(threadID); got.Type != "idle" {
		t.Fatalf("thread status = %#v", got)
	}
}

func TestRuntimeRouterTurnStartInjectsExternalCurrentTimeReminder(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	projectTrust := strings.ReplaceAll(filepath.Clean(project), `\`, `\\`)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[projects.\""+projectTrust+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user config returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .codex returned error: %v", err)
	}
	projectConfig := `instructions = "project instructions"

[features.current_time_reminder]
enabled = true
clock_source = "external"
`
	if err := os.WriteFile(config.ProjectConfigPath(project), []byte(projectConfig), 0o600); err != nil {
		t.Fatalf("WriteFile project config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	agent := newRecordingRuntimeAgent("ok")
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestCurrentTimeRead {
			t.Errorf("server request method = %s", request.Method)
		}
		go func() {
			_, _ = router.requireServerRequests().Resolve(OK(request.ID, &CurrentTimeReadResponse{CurrentTimeAt: 1781717655}))
		}()
	}))

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    project,
		Prompt: "hello time",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello time",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	request := waitForRuntimeAgentRequest(t, agent)
	if !strings.Contains(request.Instructions, "<current_time>") ||
		!strings.Contains(request.Instructions, "It is 2026-06-17 17:34:15 UTC.") ||
		!strings.Contains(request.Instructions, "project instructions") {
		t.Fatalf("instructions = %q", request.Instructions)
	}
	toolsJSON, err := json.Marshal(request.Tools)
	if err != nil {
		t.Fatalf("Marshal tools error = %v", err)
	}
	if !strings.Contains(string(toolsJSON), "clock__curr_time") {
		t.Fatalf("clock current-time tool missing: %s", toolsJSON)
	}
	if strings.Contains(string(toolsJSON), "clock__sleep") {
		t.Fatalf("clock sleep tool should follow sleep_tool=false: %s", toolsJSON)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestRuntimeRouterTurnStartPassesResponsesAPIClientMetadata(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
[responsesapi_client_metadata]
workspace_kind = "git"
`), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	agent := newRecordingRuntimeAgent("ok")
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello metadata",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello metadata",
		ResponsesAPIMetadata: map[string]string{
			"workspace_kind":           "projectless",
			"x-codex-installation-id":  "bad",
			"x-codex-parent-thread-id": "bad",
		},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	request := waitForRuntimeAgentRequest(t, agent)
	if request.PromptCacheKey != threadID {
		t.Fatalf("PromptCacheKey = %q, want %q", request.PromptCacheKey, threadID)
	}
	if request.ClientMetadata["workspace_kind"] != "" {
		t.Fatalf("workspace_kind should not be top-level client metadata: %#v", request.ClientMetadata)
	}
	var turnMetadata map[string]any
	if err := json.Unmarshal([]byte(request.ClientMetadata["x-codex-turn-metadata"]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v metadata=%#v", err, request.ClientMetadata)
	}
	if turnMetadata["workspace_kind"] != "projectless" || turnMetadata["thread_id"] != threadID || turnMetadata["turn_id"] == "" {
		t.Fatalf("turn metadata = %#v client=%#v", turnMetadata, request.ClientMetadata)
	}
	if turnMetadata["x-codex-installation-id"] != nil || turnMetadata["x-codex-parent-thread-id"] != nil {
		t.Fatalf("reserved metadata leaked: %#v", turnMetadata)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestCompactTokenStatusFromMetadata(t *testing.T) {
	left := 12
	status := compactTokenStatusFromMetadata(map[string]any{"token_status": map[string]any{
		"activeContextTokens":      20,
		"autoCompactScopeTokens":   20,
		"autoCompactScopeLimit":    32,
		"tokensUntilCompaction":    left,
		"shouldCompact":            false,
		"reason":                   "",
		"newContextWindowRequired": false,
	}})
	if status.ActiveContextTokens != 20 || status.TokensUntilCompaction == nil || *status.TokensUntilCompaction != 12 {
		t.Fatalf("status = %#v", status)
	}
}

func TestRuntimeRouterTurnStartPreservesThreadOriginator(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	if err := store.Create(&session.Record{
		ID:        "thread-originator",
		SessionID: "thread-originator",
		Preview:   "hello",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:        t.TempDir(),
			Originator: "codex_vscode",
		},
	}); err != nil {
		t.Fatalf("Create record error = %v", err)
	}
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})

	turnStart := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{
		ThreadID: "thread-originator",
		Prompt:   "hello",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Originator != "codex_vscode" {
		t.Fatalf("originator = %q", request.Originator)
	}
}

func TestRuntimeRouterThreadStartUsesConnectionClientInfoOriginator(t *testing.T) {
	store := session.NewStore(t.TempDir())
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex_vscode", Version: "0.1.0"},
	})
	initialize.ConnectionID = "conn-originator"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}

	startRequest := requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{})
	startRequest.ConnectionID = "conn-originator"
	threadStart := router.Handle(startRequest)
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello originator",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.Originator != "codex_vscode" {
		t.Fatalf("originator = %q", request.Originator)
	}
}

func TestRuntimeRouterThreadGoalPersistsInThreadStoreAndNotifies(t *testing.T) {
	store := session.NewStore(t.TempDir())
	routerStore := NewRouter(store)
	routerStore.SetClock(func() time.Time { return fixedTime() })
	createRecord(t, store, "thread-goal", fixedTime())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: routerStore})
	router.SetNotificationSink(sink)

	budget := int64(10)
	objective := "persist the goal"
	setGoal := router.Handle(requestWithParams(t, IntID(1), MethodThreadGoalSet, GoalSetParams{
		ThreadID: "thread-goal", Objective: &objective, TokenBudget: &budget,
	}))
	if setGoal.Error != nil {
		t.Fatalf("goal set error: %+v", setGoal.Error)
	}
	if setGoal.Result.(*GoalSetResponse).Goal.CreatedAt != fixedTime().Unix() {
		t.Fatalf("goal = %#v", setGoal.Result.(*GoalSetResponse).Goal)
	}
	if !sinkHasMethod(sink, NotificationThreadGoalUpdated) {
		t.Fatalf("goal update notification missing: %#v", sink.List())
	}

	reloaded := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	getGoal := reloaded.Handle(requestWithParams(t, IntID(2), MethodThreadGoalGet, GoalGetParams{ThreadID: "thread-goal"}))
	if getGoal.Error != nil || getGoal.Result.(*GoalGetResponse).Goal == nil || getGoal.Result.(*GoalGetResponse).Goal.Objective != objective {
		t.Fatalf("goal get = %+v", getGoal)
	}

	blocked := GoalBlocked
	updateStatus := reloaded.Handle(requestWithParams(t, IntID(3), MethodThreadGoalSet, GoalSetParams{
		ThreadID: "thread-goal", Status: &blocked,
	}))
	if updateStatus.Error != nil || updateStatus.Result.(*GoalSetResponse).Goal.Status != GoalBlocked {
		t.Fatalf("goal status update = %+v", updateStatus)
	}

	clearGoal := reloaded.Handle(requestWithParams(t, IntID(4), MethodThreadGoalClear, GoalClearParams{ThreadID: "thread-goal"}))
	if clearGoal.Error != nil || !clearGoal.Result.(*GoalClearResponse).Cleared {
		t.Fatalf("goal clear = %+v", clearGoal)
	}
	record, err := store.Read("thread-goal", true, true)
	if err != nil {
		t.Fatalf("read thread after clear: %v", err)
	}
	if len(record.Items) == 0 {
		t.Fatalf("thread items were lost after clearing goal")
	}
	missing := reloaded.Handle(requestWithParams(t, IntID(5), MethodThreadGoalGet, GoalGetParams{ThreadID: "thread-goal"}))
	if missing.Error != nil || missing.Result.(*GoalGetResponse).Goal != nil {
		t.Fatalf("goal after clear = %+v", missing)
	}
}

func TestRuntimeRouterThreadGoalRepairsRolloutOnlyThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	routerStore := NewRouter(store)
	routerStore.SetClock(func() time.Time { return fixedTime() })
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: store.Root(),
		SessionID: "session-rollout-goal",
		ThreadID:  "thread-rollout-goal",
		Source:    "cli",
		CWD:       "D:/repo",
		Now:       fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "u1", Type: "message", Role: "user", Text: "from rollout goal"}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: routerStore})
	router.SetNotificationSink(sink)

	objective := "persist goal on rollout-only thread"
	setGoal := router.Handle(requestWithParams(t, IntID(1), MethodThreadGoalSet, GoalSetParams{
		ThreadID: "thread-rollout-goal", Objective: &objective,
	}))
	if setGoal.Error != nil {
		t.Fatalf("goal set error: %+v", setGoal.Error)
	}
	if !sinkHasMethod(sink, NotificationThreadGoalUpdated) {
		t.Fatalf("goal update notification missing: %#v", sink.List())
	}
	record, err := store.Read("thread-rollout-goal", true, true)
	if err != nil {
		t.Fatalf("read repaired thread: %v", err)
	}
	if len(record.Items) != 1 || record.Items[0].Text != "from rollout goal" {
		t.Fatalf("repaired items = %#v", record.Items)
	}

	reloaded := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	getGoal := reloaded.Handle(requestWithParams(t, IntID(2), MethodThreadGoalGet, GoalGetParams{ThreadID: "thread-rollout-goal"}))
	if getGoal.Error != nil || getGoal.Result.(*GoalGetResponse).Goal == nil || getGoal.Result.(*GoalGetResponse).Goal.Objective != objective {
		t.Fatalf("goal get = %+v", getGoal)
	}
	clearGoal := reloaded.Handle(requestWithParams(t, IntID(3), MethodThreadGoalClear, GoalClearParams{ThreadID: "thread-rollout-goal"}))
	if clearGoal.Error != nil || !clearGoal.Result.(*GoalClearResponse).Cleared {
		t.Fatalf("goal clear = %+v", clearGoal)
	}
}

func TestRuntimeRouterThreadCompactStartNotifies(t *testing.T) {
	store := session.NewStore(t.TempDir())
	if err := store.Save(&session.Record{
		ID: "thread-compact",
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "please compact"},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "ok"},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)
	router.requireThreadStatus().UpsertThread("thread-compact", false)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: "thread-compact"}))
	if response.Error != nil {
		t.Fatalf("compact error: %+v", response.Error)
	}
	notification := waitForNotificationMethod(t, sink, NotificationContextCompacted)
	compacted, ok := notification.Params.(*ContextCompactedNotification)
	if !ok || compacted.Summary == "" || compacted.ItemCount == 0 {
		t.Fatalf("notification = %#v", notification.Params)
	}
}

func TestRuntimeRouterThreadCompactStartRejectsNotLoadedThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	if err := store.Save(&session.Record{
		ID: "thread-cold-compact",
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "please compact"},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "ok"},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: "thread-cold-compact"}))
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "thread not found: thread-cold-compact" {
		t.Fatalf("compact response = %+v", response)
	}
	record, err := store.Read("thread-cold-compact", true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(record.Items) != 2 || record.Metadata.Extra["compaction_summary"] != nil {
		t.Fatalf("record was mutated by rejected compact: %+v", record)
	}
	if notifications := sink.List(); len(notifications) != 0 {
		t.Fatalf("notifications = %+v", notifications)
	}
}

func TestRuntimeRouterThreadCompactStartUsesRemoteRunner(t *testing.T) {
	store := session.NewStore(t.TempDir())
	if err := store.Save(&session.Record{
		ID: "thread-remote-compact",
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "please compact remotely", Metadata: map[string]any{"kind": "user_message"}},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "remote context"},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	runner := &recordingCompactRunner{summary: "remote compact summary"}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:  NewRouter(store),
		CompactRunner: runner,
	})
	router.SetNotificationSink(sink)
	router.requireThreadStatus().UpsertThread("thread-remote-compact", false)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: "thread-remote-compact"}))
	if response.Error != nil {
		t.Fatalf("compact error: %+v", response.Error)
	}
	if runner.request == nil || runner.request.ThreadID != "thread-remote-compact" {
		t.Fatalf("remote request = %#v", runner.request)
	}
	notification := waitForNotificationMethod(t, sink, NotificationContextCompacted)
	compacted := notification.Params.(*ContextCompactedNotification)
	if compacted.Summary != "remote compact summary" {
		t.Fatalf("notification summary = %q", compacted.Summary)
	}
	if compacted.Source != string(compact.SourceRemote) || compacted.ResponseID != "resp-compact" || compacted.Model != "gpt-compact" {
		t.Fatalf("notification metadata = %#v", compacted)
	}
	record, err := store.Read("thread-remote-compact", true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if record.Metadata.Extra["compaction_summary"] != "remote compact summary" {
		t.Fatalf("metadata = %#v", record.Metadata.Extra)
	}
	if record.Metadata.Extra["compaction_response_id"] != "resp-compact" || record.Metadata.Extra["compaction_source"] != string(compact.SourceRemote) {
		t.Fatalf("metadata = %#v", record.Metadata.Extra)
	}
}

func TestRuntimeRouterThreadCompactStartRunsHooks(t *testing.T) {
	store := session.NewStore(t.TempDir())
	cwd := t.TempDir()
	if err := store.Save(&session.Record{
		ID: "thread-hook-compact",
		Metadata: session.Metadata{
			CWD:   cwd,
			Model: "gpt-hook",
		},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "please compact with hooks", Metadata: map[string]any{"kind": "user_message"}},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "ok"},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	hooks := NewHookRegistry()
	pre := hookRunnerMetadata("pre-compact", HookEventPreCompact, "manual", 0)
	preCommand := hookRunnerOutputCommand(`{"hookSpecificOutput":{"additionalContext":"pre compact context"}}`, "")
	pre.Command = &preCommand
	if err := hooks.Add(cwd, pre); err != nil {
		t.Fatalf("Add pre hook error = %v", err)
	}
	post := hookRunnerMetadata("post-compact", HookEventPostCompact, "manual", 1)
	postCommand := hookRunnerOutputCommand(`{"systemMessage":"post compact ran"}`, "")
	post.Command = &postCommand
	if err := hooks.Add(cwd, post); err != nil {
		t.Fatalf("Add post hook error = %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Hooks:        hooks,
		HookRunner:   NewHookRunner(),
		DefaultCWD:   cwd,
	})
	router.SetNotificationSink(sink)
	router.requireThreadStatus().UpsertThread("thread-hook-compact", false)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: "thread-hook-compact"}))
	if response.Error != nil {
		t.Fatalf("compact error: %+v", response.Error)
	}
	record, err := store.Read("thread-hook-compact", true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(record.Items) < 2 || record.Items[0].Text != "pre compact context" {
		t.Fatalf("compacted items = %#v", record.Items)
	}
	notifications := sink.List()
	if !notificationsContainHook(notifications, HookEventPreCompact) || !notificationsContainHook(notifications, HookEventPostCompact) {
		t.Fatalf("notifications = %#v", notifications)
	}
}

func TestRuntimeRouterConversationSummaryUsesThreadStore(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	if err := store.Save(&session.Record{
		ID:        "thread-summary",
		SessionID: "thread-summary",
		Preview:   "preview fallback",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           "/repo",
			ModelProvider: "openai",
			Source:        "cli",
			CLIVersion:    "0.1.0",
			Git:           map[string]string{"sha": "abc123", "branch": "main", "origin_url": "https://example.test/repo.git"},
		},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "user asks for a migration plan", CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "assistant proposes stage six", CreatedAt: now.Add(time.Second)},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetConversationSummary, ConversationSummaryParams{ConversationID: "thread-summary"}))
	if response.Error != nil {
		t.Fatalf("summary error: %+v", response.Error)
	}
	summary := response.Result.(*ConversationSummaryResponse).Summary
	if !strings.Contains(summary, "user asks for a migration plan") || !strings.Contains(summary, "assistant proposes stage six") {
		t.Fatalf("summary = %q", summary)
	}
	data, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("Marshal(summary response) error = %v", err)
	}
	timestamp := now.UTC().Format("2006-01-02T15:04:05.000Z")
	for _, want := range []string{
		`"conversationId":"thread-summary"`,
		`"preview":"preview fallback"`,
		`"timestamp":"` + timestamp + `"`,
		`"updatedAt":"` + timestamp + `"`,
		`"modelProvider":"openai"`,
		`"cwd":"/repo"`,
		`"cliVersion":"0.1.0"`,
		`"source":"cli"`,
		`"origin_url":"https://example.test/repo.git"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("summary response JSON missing %s: %s", want, data)
		}
	}
}

func TestRuntimeRouterConversationSummaryRepairsRolloutOnlyThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: store.Root(),
		SessionID: "session-rollout-summary",
		ThreadID:  "thread-rollout-summary",
		Source:    "cli",
		CWD:       "D:/repo",
		Now:       fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	for _, item := range []rollout.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "rollout asks for summary"},
		{ID: "a1", Type: "message", Role: "assistant", Text: "rollout assistant summary answer"},
	} {
		if err := recorder.AppendItem(item); err != nil {
			t.Fatalf("AppendItem() error = %v", err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetConversationSummary, ConversationSummaryParams{ThreadID: "thread-rollout-summary"}))
	if response.Error != nil {
		t.Fatalf("summary error: %+v", response.Error)
	}
	summary := response.Result.(*ConversationSummaryResponse).Summary
	if !strings.Contains(summary, "rollout asks for summary") || !strings.Contains(summary, "rollout assistant summary answer") {
		t.Fatalf("summary = %q", summary)
	}
	if _, err := store.Read("thread-rollout-summary", true, true); err != nil {
		t.Fatalf("read repaired thread: %v", err)
	}
}

func TestRuntimeRouterConversationSummaryReadsRelativeRolloutPath(t *testing.T) {
	store := session.NewStore(t.TempDir())
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     store.Root(),
		SessionID:     "session-relative-summary",
		ThreadID:      "thread-relative-summary",
		Source:        "cli",
		CWD:           "/repo",
		ModelProvider: "openai",
		CLIVersion:    "0.1.0",
		Git:           map[string]string{"branch": "main"},
		Now:           fixedTime(),
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	for _, item := range []rollout.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "relative rollout asks for summary"},
		{ID: "a1", Type: "message", Role: "assistant", Text: "relative rollout answer"},
	} {
		if err := recorder.AppendItem(item); err != nil {
			t.Fatalf("AppendItem() error = %v", err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	relativePath, err := filepath.Rel(store.Root(), recorder.Path())
	if err != nil {
		t.Fatalf("Rel() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetConversationSummary, ConversationSummaryParams{
		ConversationID: "wrong-thread",
		RolloutPath:    relativePath,
	}))
	if response.Error != nil {
		t.Fatalf("summary error: %+v", response.Error)
	}
	result := response.Result.(*ConversationSummaryResponse)
	if !strings.Contains(result.Summary, "relative rollout asks for summary") || !strings.Contains(result.Summary, "relative rollout answer") {
		t.Fatalf("summary = %q", result.Summary)
	}
	if result.SummaryData == nil || result.SummaryData.ConversationID != "thread-relative-summary" || result.SummaryData.Path == "" || !filepath.IsAbs(result.SummaryData.Path) {
		t.Fatalf("summary data = %#v", result.SummaryData)
	}
	if result.SummaryData.ModelProvider != "openai" || result.SummaryData.CWD != "/repo" || result.SummaryData.Source != SessionSourceCli || result.SummaryData.GitInfo == nil || result.SummaryData.GitInfo.Branch == nil || *result.SummaryData.GitInfo.Branch != "main" {
		t.Fatalf("summary data metadata = %#v", result.SummaryData)
	}
	if _, err := store.Read("thread-relative-summary", true, true); err != nil {
		t.Fatalf("read repaired relative rollout thread: %v", err)
	}
}

func TestRuntimeRouterConversationSummaryPrefersCompactionSummary(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	if err := store.Save(&session.Record{
		ID:        "thread-compacted-summary",
		SessionID: "thread-compacted-summary",
		Preview:   "preview fallback",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{Extra: map[string]any{
			"compaction_summary": "persisted compact summary",
		}},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "older raw history", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})

	response := router.Handle(requestWithParams(t, IntID(1), MethodGetConversationSummary, ConversationSummaryParams{ThreadID: "thread-compacted-summary"}))
	if response.Error != nil {
		t.Fatalf("summary error: %+v", response.Error)
	}
	if got := response.Result.(*ConversationSummaryResponse).Summary; got != "persisted compact summary" {
		t.Fatalf("summary = %q", got)
	}
}

func TestRuntimeRouterThreadLifecycleNotifications(t *testing.T) {
	store := session.NewStore(t.TempDir())
	createRecord(t, store, "thread-a", fixedTime())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)

	setName := router.Handle(requestWithParams(t, IntID(1), MethodThreadSetName, ThreadSetNameParams{ThreadID: "thread-a", Name: "Renamed"}))
	if setName.Error != nil {
		t.Fatalf("set name error: %+v", setName.Error)
	}
	archive := router.Handle(requestWithParams(t, IntID(2), MethodThreadArchive, ThreadArchiveParams{ThreadID: "thread-a"}))
	if archive.Error != nil {
		t.Fatalf("archive error: %+v", archive.Error)
	}
	deleted := router.Handle(requestWithParams(t, IntID(3), MethodThreadDelete, ThreadDeleteParams{ThreadID: "thread-a"}))
	if deleted.Error != nil {
		t.Fatalf("delete error: %+v", deleted.Error)
	}
	if !sinkHasMethod(sink, NotificationThreadNameUpdated) || !sinkHasMethod(sink, NotificationThreadArchived) || !sinkHasMethod(sink, NotificationThreadDeleted) {
		t.Fatalf("notifications = %+v", sink.List())
	}
}

func TestRuntimeRouterThreadRollbackEmitsDeprecationNotice(t *testing.T) {
	store := session.NewStore(t.TempDir())
	createRecord(t, store, "thread-a", fixedTime())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)
	router.requireThreadStatus().UpsertThread("thread-a", false)

	rollback := router.Handle(requestWithParams(t, IntID(1), MethodThreadRollback, ThreadRollbackParams{
		ThreadID: "thread-a",
		NumTurns: 1,
	}))
	if rollback.Error != nil {
		t.Fatalf("rollback error: %+v", rollback.Error)
	}
	notifications := sink.List()
	if len(notifications) == 0 || notifications[0].Method != NotificationDeprecationNotice {
		t.Fatalf("notifications = %+v", notifications)
	}
	notice, ok := notifications[0].Params.(*DeprecationNoticeNotification)
	if !ok || notice.Summary != "thread/rollback is deprecated and will be removed soon" {
		t.Fatalf("notice = %#v", notifications[0].Params)
	}
}

func TestRuntimeRouterThreadRollbackRejectsNotLoadedThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	createRecord(t, store, "thread-a", fixedTime())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)

	rollback := router.Handle(requestWithParams(t, IntID(1), MethodThreadRollback, ThreadRollbackParams{
		ThreadID: "thread-a",
		NumTurns: 1,
	}))
	if rollback.Error == nil || rollback.Error.Code != -32600 || rollback.Error.Message != "thread not found: thread-a" {
		t.Fatalf("rollback response = %+v", rollback)
	}
	record, err := store.Read("thread-a", true, true)
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if len(record.Items) != 2 {
		t.Fatalf("record items were mutated by rejected rollback: %+v", record.Items)
	}
	notifications := sink.List()
	if len(notifications) == 0 || notifications[0].Method != NotificationDeprecationNotice {
		t.Fatalf("notifications = %+v", notifications)
	}
}

func TestRuntimeRouterThreadRollbackSuppressesDeprecationNoticeForCodexTUI(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-tui", Version: "0.1.0"},
	})
	initialize.ConnectionID = "conn-tui"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}

	rollback := requestWithParams(t, IntID(2), MethodThreadRollback, ThreadRollbackParams{
		ThreadID: "missing-thread",
		NumTurns: 1,
	})
	rollback.ConnectionID = "conn-tui"
	response := router.Handle(rollback)
	if response.Error == nil {
		t.Fatalf("rollback response = %+v, want error", response)
	}
	if sinkHasMethod(sink, NotificationDeprecationNotice) {
		t.Fatalf("notifications = %+v", sink.List())
	}
}

func TestRuntimeRouterThreadRollbackDeprecationNoticeUsesExactCodexTUIName(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: " codex-tui ", Version: "0.1.0"},
	})
	initialize.ConnectionID = "conn-padded-tui"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}

	rollback := requestWithParams(t, IntID(2), MethodThreadRollback, ThreadRollbackParams{
		ThreadID: "missing-thread",
		NumTurns: 1,
	})
	rollback.ConnectionID = "conn-padded-tui"
	response := router.Handle(rollback)
	if response.Error == nil {
		t.Fatalf("rollback response = %+v, want error", response)
	}
	if !sinkHasMethod(sink, NotificationDeprecationNotice) {
		t.Fatalf("notifications = %+v", sink.List())
	}
}

func TestRuntimeRouterThreadLifecycleSubtreeNotifications(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	createRecord(t, store, "parent", now)
	createRecord(t, store, "child", now)
	createRecord(t, store, "grandchild", now)
	child, err := store.Read("child", true, true)
	if err != nil {
		t.Fatalf("read child: %v", err)
	}
	child.ParentThreadID = "parent"
	if err := store.Save(child); err != nil {
		t.Fatalf("save child: %v", err)
	}
	grandchild, err := store.Read("grandchild", true, true)
	if err != nil {
		t.Fatalf("read grandchild: %v", err)
	}
	grandchild.ParentThreadID = "child"
	if err := store.Save(grandchild); err != nil {
		t.Fatalf("save grandchild: %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)

	archive := router.Handle(requestWithParams(t, IntID(1), MethodThreadArchive, ThreadArchiveParams{ThreadID: "parent"}))
	if archive.Error != nil {
		t.Fatalf("archive error: %+v", archive.Error)
	}
	if got := notificationThreadIDs(sink, NotificationThreadArchived); !equalStrings(got, []string{"parent", "grandchild", "child"}) {
		t.Fatalf("archived ids = %#v", got)
	}
	for _, id := range []session.ThreadID{"parent", "child", "grandchild"} {
		record, err := store.Read(id, true, false)
		if err != nil {
			t.Fatalf("read archived %s: %v", id, err)
		}
		if !record.Archived {
			t.Fatalf("%s was not archived", id)
		}
	}

	delete := router.Handle(requestWithParams(t, IntID(2), MethodThreadDelete, ThreadDeleteParams{ThreadID: "parent"}))
	if delete.Error != nil {
		t.Fatalf("delete error: %+v", delete.Error)
	}
	if got := notificationThreadIDs(sink, NotificationThreadDeleted); !equalStrings(got, []string{"grandchild", "child", "parent"}) {
		t.Fatalf("deleted ids = %#v", got)
	}
}

func TestRuntimeRouterThreadLifecycleUsesSpawnGraphDescendants(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	createRecord(t, store, "parent", now)
	createRecord(t, store, "child", now)
	createRecord(t, store, "grandchild", now)
	graph := agent.NewMemoryStore()
	if err := graph.UpsertThreadSpawnEdge("parent", "child", agent.ThreadSpawnEdgeClosed); err != nil {
		t.Fatalf("upsert child edge: %v", err)
	}
	if err := graph.UpsertThreadSpawnEdge("child", "grandchild", agent.ThreadSpawnEdgeOpen); err != nil {
		t.Fatalf("upsert grandchild edge: %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), SpawnGraph: graph})
	router.SetNotificationSink(sink)

	archive := router.Handle(requestWithParams(t, IntID(1), MethodThreadArchive, ThreadArchiveParams{ThreadID: "parent"}))
	if archive.Error != nil {
		t.Fatalf("archive error: %+v", archive.Error)
	}
	if got := notificationThreadIDs(sink, NotificationThreadArchived); !equalStrings(got, []string{"parent", "grandchild", "child"}) {
		t.Fatalf("archived ids = %#v", got)
	}
	for _, id := range []session.ThreadID{"parent", "child", "grandchild"} {
		record, err := store.Read(id, true, false)
		if err != nil {
			t.Fatalf("read archived %s: %v", id, err)
		}
		if !record.Archived {
			t.Fatalf("%s was not archived", id)
		}
	}

	delete := router.Handle(requestWithParams(t, IntID(2), MethodThreadDelete, ThreadDeleteParams{ThreadID: "parent"}))
	if delete.Error != nil {
		t.Fatalf("delete error: %+v", delete.Error)
	}
	if got := notificationThreadIDs(sink, NotificationThreadDeleted); !equalStrings(got, []string{"grandchild", "child", "parent"}) {
		t.Fatalf("deleted ids = %#v", got)
	}
}

func TestRuntimeRouterThreadArchiveSkipsSpawnedDescendantWhenRolloutArchiveFails(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	now := fixedTime()
	for _, id := range []session.ThreadID{"parent", "child", "grandchild"} {
		createRecord(t, store, id, now)
		record, err := store.Read(id, true, true)
		if err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if err := threadRouter.createThreadRollout(record, now); err != nil {
			t.Fatalf("create rollout %s: %v", id, err)
		}
	}
	graph := agent.NewMemoryStore()
	if err := graph.UpsertThreadSpawnEdge("parent", "child", agent.ThreadSpawnEdgeClosed); err != nil {
		t.Fatalf("upsert child edge: %v", err)
	}
	if err := graph.UpsertThreadSpawnEdge("child", "grandchild", agent.ThreadSpawnEdgeOpen); err != nil {
		t.Fatalf("upsert grandchild edge: %v", err)
	}
	childPath, err := rollout.FindThreadPath(store.Root(), "child", false)
	if err != nil {
		t.Fatalf("child rollout path: %v", err)
	}
	conflictPath := filepath.Join(store.Root(), rollout.ArchivedSessionsSubdir, filepath.Base(childPath))
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		t.Fatalf("create archive conflict: %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, SpawnGraph: graph})
	router.SetNotificationSink(sink)

	archive := router.Handle(requestWithParams(t, IntID(1), MethodThreadArchive, ThreadArchiveParams{ThreadID: "parent"}))
	if archive.Error != nil {
		t.Fatalf("archive error: %+v", archive.Error)
	}
	if got := notificationThreadIDs(sink, NotificationThreadArchived); !equalStrings(got, []string{"parent", "grandchild"}) {
		t.Fatalf("archived ids = %#v", got)
	}
	for _, id := range []session.ThreadID{"parent", "grandchild"} {
		record, err := store.Read(id, true, false)
		if err != nil {
			t.Fatalf("read archived %s: %v", id, err)
		}
		if !record.Archived {
			t.Fatalf("%s was not archived", id)
		}
	}
	child, err := store.Read("child", true, false)
	if err != nil {
		t.Fatalf("read child: %v", err)
	}
	if child.Archived {
		t.Fatalf("child should remain active after rollout archive failure")
	}
	if _, err := os.Stat(childPath); err != nil {
		t.Fatalf("child rollout should remain active: %v", err)
	}
	if info, err := os.Stat(conflictPath); err != nil || !info.IsDir() {
		t.Fatalf("archive conflict path = info:%+v err:%v, want directory", info, err)
	}
}

func TestRuntimeRouterThreadDeleteAllowsMissingRootWithSpawnGraphDescendants(t *testing.T) {
	store := session.NewStore(t.TempDir())
	createRecord(t, store, "child", fixedTime())
	graph := agent.NewMemoryStore()
	if err := graph.UpsertThreadSpawnEdge("missing-parent", "child", agent.ThreadSpawnEdgeOpen); err != nil {
		t.Fatalf("upsert edge: %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), SpawnGraph: graph})
	router.SetNotificationSink(sink)

	deleted := router.Handle(requestWithParams(t, IntID(1), MethodThreadDelete, ThreadDeleteParams{ThreadID: "missing-parent"}))
	if deleted.Error != nil {
		t.Fatalf("delete error: %+v", deleted.Error)
	}
	if got := notificationThreadIDs(sink, NotificationThreadDeleted); !equalStrings(got, []string{"child", "missing-parent"}) {
		t.Fatalf("deleted ids = %#v", got)
	}
	if _, err := store.Read("child", true, false); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("child read after delete error = %v, want ErrThreadNotFound", err)
	}
}

func TestRuntimeRouterThreadDeleteMissingRootWithoutDescendantsReturnsInvalidRequest(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(session.NewStore(t.TempDir()))})

	deleted := router.Handle(requestWithParams(t, IntID(1), MethodThreadDelete, ThreadDeleteParams{ThreadID: "missing-parent"}))
	if deleted.Error == nil || deleted.Error.Code != -32600 || deleted.Error.Message != "thread not found: missing-parent" {
		t.Fatalf("delete missing root error = %+v", deleted.Error)
	}
}

func TestRuntimeRouterThreadForkStartedNotificationOmitsCopiedTurns(t *testing.T) {
	store := session.NewStore(t.TempDir())
	createRecord(t, store, "thread-a", fixedTime())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)

	fork := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{ThreadID: "thread-a"}))
	if fork.Error != nil {
		t.Fatalf("fork error: %+v", fork.Error)
	}
	forked := fork.Result.(*ThreadForkResponse).Thread
	notifications := sink.List()
	if len(notifications) == 0 {
		t.Fatal("missing notifications")
	}
	var started *ThreadStartedNotification
	for _, notification := range notifications {
		if notification.Method == NotificationThreadStarted {
			if payload, ok := notification.Params.(*ThreadStartedNotification); ok {
				started = payload
			}
		}
	}
	if started == nil || started.Thread == nil || started.Thread.ID != forked.ID {
		t.Fatalf("thread/started notification = %+v", notifications)
	}
	if forked.Name != nil || started.Thread.Name != nil {
		t.Fatalf("fork names = response:%+v started:%+v, want nil", forked.Name, started.Thread.Name)
	}
	if len(forked.Turns) == 0 {
		t.Fatalf("fork response should include copied turns: %+v", forked)
	}
	if len(started.Thread.Turns) != 0 {
		t.Fatalf("thread/started copied turns = %+v", started.Thread.Turns)
	}
	if started.Thread.Path == nil || !strings.HasSuffix(*started.Thread.Path, ".jsonl") {
		t.Fatalf("thread/started path = %+v, want rollout jsonl path", started.Thread.Path)
	}
}

func TestRuntimeRouterThreadForkRestoredTokenUsagePrecedesStarted(t *testing.T) {
	store := session.NewStore(t.TempDir())
	createRecord(t, store, "thread-a", fixedTime())
	record, err := store.Read("thread-a", true, true)
	if err != nil {
		t.Fatalf("Read source error: %v", err)
	}
	record.Metadata.Extra = map[string]any{
		"token_usage_turn_id": "turn-2",
		"token_usage_info": map[string]any{
			"total_token_usage": map[string]any{
				"input_tokens":            float64(180),
				"cached_input_tokens":     float64(40),
				"output_tokens":           float64(50),
				"reasoning_output_tokens": float64(15),
				"total_tokens":            float64(230),
			},
			"last_token_usage": map[string]any{
				"input_tokens":            float64(90),
				"cached_input_tokens":     float64(30),
				"output_tokens":           float64(40),
				"reasoning_output_tokens": float64(12),
				"total_tokens":            float64(130),
			},
			"model_context_window": float64(200000),
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save source error: %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)

	fork := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{ThreadID: "thread-a"}))
	if fork.Error != nil {
		t.Fatalf("fork error: %+v", fork.Error)
	}
	forked := fork.Result.(*ThreadForkResponse).Thread
	usageIndex, startedIndex := -1, -1
	for i, notification := range sink.List() {
		switch notification.Method {
		case NotificationThreadTokenUsageUpdated:
			payload, _ := notification.Params.(*ThreadTokenUsageUpdatedNotification)
			if payload != nil && payload.ThreadID == forked.ID && payload.TurnID == "turn-2" {
				usageIndex = i
			}
		case NotificationThreadStarted:
			payload, _ := notification.Params.(*ThreadStartedNotification)
			if payload != nil && payload.Thread != nil && payload.Thread.ID == forked.ID {
				startedIndex = i
			}
		}
	}
	if usageIndex < 0 || startedIndex < 0 || usageIndex > startedIndex {
		t.Fatalf("notification order usage=%d started=%d notifications=%+v", usageIndex, startedIndex, sink.List())
	}
}

func TestRuntimeRouterThreadForkActiveTurnAddsInterruptedMarker(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime().Add(10 * time.Second)
	startedAt := now.Add(-2 * time.Second)
	createRecord(t, store, "thread-active-fork", fixedTime())
	routerStore := NewRouter(store)
	routerStore.SetClock(func() time.Time { return now })
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: routerStore})
	if err := router.registerActiveRuntimeTurn("thread-active-fork", "turn-active", func() {}, startedAt.UnixMilli(), &turn.TurnStartParams{
		ThreadID: "thread-active-fork",
		Prompt:   "active prompt",
	}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}

	fork := router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{ThreadID: "thread-active-fork"}))
	if fork.Error != nil {
		t.Fatalf("fork error: %+v", fork.Error)
	}
	forked := fork.Result.(*ThreadForkResponse).Thread
	if forked == nil {
		t.Fatal("forked thread is nil")
	}
	if forked.Source != SessionSourceCli {
		t.Fatalf("fork source = %q", forked.Source)
	}
	if forked.Name != nil {
		t.Fatalf("active fork name = %+v, want nil", forked.Name)
	}
	if forked.Status.Type != "idle" {
		t.Fatalf("fork status = %#v", forked.Status)
	}
	activeTurn := turnByID(forked.Turns, "turn-active")
	if activeTurn == nil || activeTurn.Status != TurnStatusInterrupted {
		t.Fatalf("active turn = %+v in %+v", activeTurn, forked.Turns)
	}
	if !turnHasItemText(activeTurn, "active prompt") || !turnHasItemText(activeTurn, "<turn_aborted>") {
		t.Fatalf("active turn items = %+v", activeTurn.Items)
	}

	source, err := store.Read("thread-active-fork", true, true)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if sessionItemByID(source.Items, runtimeUserPromptSessionItemID("turn-active")) != nil || sessionItemByID(source.Items, "turn-aborted-turn-active") != nil {
		t.Fatalf("source was mutated with active fork items: %+v", source.Items)
	}
	forkRecord, err := store.Read(session.ThreadID(forked.ID), true, true)
	if err != nil {
		t.Fatalf("read fork: %v", err)
	}
	if forkRecord.Title != "" {
		t.Fatalf("active fork record title = %q, want empty", forkRecord.Title)
	}
	if sessionItemByID(forkRecord.Items, runtimeUserPromptSessionItemID("turn-active")) == nil || sessionItemByID(forkRecord.Items, "turn-aborted-turn-active") == nil {
		t.Fatalf("fork record items = %+v", forkRecord.Items)
	}
	rolloutRecord := rolloutRecordForThread(t, store, forked.ID)
	rolloutActive := turnByID(BuildThread(rolloutRecord, "", true).Turns, "turn-active")
	if rolloutActive == nil || rolloutActive.Status != TurnStatusInterrupted || !turnHasItemText(rolloutActive, "<turn_aborted>") {
		t.Fatalf("rollout active turn = %+v", rolloutActive)
	}
}

func TestRuntimeRouterEphemeralForkRejectsArchivedSourceByIDAndPath(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
	})

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
	assertArchived := func(t *testing.T, response *Response) {
		t.Helper()
		if response.Error == nil || response.Error.Code != -32600 {
			t.Fatalf("response = %+v", response)
		}
		if response.Error.Message != want {
			t.Fatalf("error = %q, want %q", response.Error.Message, want)
		}
	}

	forkByID := router.Handle(requestWithParams(t, IntID(3), MethodThreadFork, ThreadForkParams{
		ThreadID:  threadID,
		Ephemeral: true,
	}))
	assertArchived(t, forkByID)

	forkByPath := router.Handle(requestWithParams(t, IntID(4), MethodThreadFork, ThreadForkParams{
		ThreadID:  "ignored-thread-id",
		Path:      &archivedPath,
		Ephemeral: true,
	}))
	assertArchived(t, forkByPath)
}

func TestRuntimeRouterNotifyRestoredTokenUsageFromRecord(t *testing.T) {
	store := session.NewStore(t.TempDir())
	record := &session.Record{
		ID:        "thread-a",
		SessionID: "thread-a",
		CreatedAt: fixedTime(),
		UpdatedAt: fixedTime(),
		RecencyAt: fixedTime(),
		Metadata: session.Metadata{
			Extra: map[string]any{
				"token_usage_turn_id": "turn-2",
				"token_usage_info": map[string]any{
					"total_token_usage": map[string]any{
						"input_tokens":            float64(180),
						"cached_input_tokens":     float64(40),
						"output_tokens":           float64(50),
						"reasoning_output_tokens": float64(15),
						"total_tokens":            float64(230),
					},
					"last_token_usage": map[string]any{
						"input_tokens":            float64(90),
						"cached_input_tokens":     float64(30),
						"output_tokens":           float64(40),
						"reasoning_output_tokens": float64(12),
						"total_tokens":            float64(130),
					},
					"model_context_window": float64(200000),
				},
			},
			RolloutTurns: []session.TurnSnapshot{
				{ID: "turn-1", Status: string(TurnStatusCompleted)},
				{ID: "turn-2", Status: string(TurnStatusInterrupted)},
			},
		},
		Items: []session.Item{
			{ID: "item-1", Type: "message", Role: "user", Text: "first", Metadata: map[string]any{"turnId": "turn-1"}, CreatedAt: fixedTime()},
			{ID: "item-2", Type: "agent_message", Role: "assistant", Text: "second", Metadata: map[string]any{"turnId": "turn-2"}, CreatedAt: fixedTime().Add(time.Second)},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(sink)

	router.notifyRestoredTokenUsage(&ThreadResumeResponse{Thread: BuildThread(record, "", true)})

	usage := waitForTokenUsageUpdated(t, sink, "turn-2")
	if usage.TokenUsage.Total == nil || usage.TokenUsage.Total.TotalTokens != 230 || usage.TokenUsage.Last == nil || usage.TokenUsage.Last.TotalTokens != 130 {
		t.Fatalf("usage = %+v", usage.TokenUsage)
	}
	if usage.TokenUsage.ModelContextWindow == nil || *usage.TokenUsage.ModelContextWindow != 200000 {
		t.Fatalf("modelContextWindow = %#v", usage.TokenUsage.ModelContextWindow)
	}
}

func TestRuntimeRouterThreadTurnsListMergesActiveTurn(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	threadID := "thread-active-turns"
	if err := store.Save(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		Title:     "active",
		Preview:   "active",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           t.TempDir(),
			ModelProvider: "openai",
			Source:        "cli",
			HistoryMode:   "paginated",
		},
		Items: []session.Item{},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	sink := NewNotificationBuffer()
	agent := newBlockingAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	turnStart := router.Handle(requestWithParams(t, IntID(1), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "live prompt",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForBlockingAgentStart(t, agent)

	list := router.Handle(requestWithParams(t, IntID(2), MethodThreadTurnsList, ThreadTurnsListParams{
		ThreadID:      threadID,
		ItemsView:     TurnItemsFull,
		SortDirection: SortAsc,
	}))
	if list.Error != nil {
		t.Fatalf("turns list error: %+v", list.Error)
	}
	page := list.Result.(*TurnsPage)
	if len(page.Data) != 1 {
		t.Fatalf("turns page = %+v", page.Data)
	}
	activeTurn := page.Data[0]
	if activeTurn.ID != turnID || activeTurn.Status != TurnStatusInProgress || activeTurn.ItemsView != TurnItemsFull {
		t.Fatalf("active turn = %+v", activeTurn)
	}
	if len(activeTurn.Items) != 1 || normalizeThreadItemType(activeTurn.Items[0].Type) != "userMessage" || activeTurn.Items[0].Text != "live prompt" {
		t.Fatalf("active turn items = %+v", activeTurn.Items)
	}

	interrupt := router.Handle(requestWithParams(t, IntID(3), MethodTurnInterrupt, turn.TurnInterruptParams{
		ThreadID: threadID,
		TurnID:   turnID,
	}))
	if interrupt.Error != nil {
		t.Fatalf("interrupt error: %+v", interrupt.Error)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusInterrupted)
}

func TestRuntimeRouterThreadTurnsListPreservesRolloutSnapshotsAfterRepair(t *testing.T) {
	store := session.NewStore(t.TempDir())
	path := filepath.Join(store.Root(), rollout.SessionsSubdir, "2026", "06", "29", "rollout-2026-06-29T01-02-03-thread-rollout-events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := strings.Join([]string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-rollout-events","timestamp":"2026-06-29T01:02:03Z","source":"cli","model_provider":"openai","cwd":"/repo"}}`,
		`{"timestamp":"2026-06-29T01:02:04Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1","started_at":1700000000}}`,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"user_message","message":"hello repaired rollout"}}`,
		`{"timestamp":"2026-06-29T01:02:06Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-1","reason":"interrupted","completed_at":1700000005,"duration_ms":5000}}`,
		``,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})

	for attempt := 1; attempt <= 2; attempt++ {
		response := router.Handle(requestWithParams(t, IntID(int64(attempt)), MethodThreadTurnsList, ThreadTurnsListParams{
			ThreadID:      "thread-rollout-events",
			SortDirection: SortAsc,
			ItemsView:     TurnItemsFull,
		}))
		if response.Error != nil {
			t.Fatalf("turns attempt %d error: %+v", attempt, response.Error)
		}
		page := response.Result.(*TurnsPage)
		if len(page.Data) != 1 {
			t.Fatalf("turns attempt %d = %+v", attempt, page.Data)
		}
		turn := page.Data[0]
		if turn.ID != "turn-1" || turn.Status != TurnStatusInterrupted || len(turn.Items) != 1 || turn.Items[0].Text != "hello repaired rollout" {
			t.Fatalf("turn attempt %d = %+v", attempt, turn)
		}
		if turn.StartedAt == nil || *turn.StartedAt != 1700000000 || turn.CompletedAt == nil || *turn.CompletedAt != 1700000005 || turn.DurationMS == nil || *turn.DurationMS != 5000 {
			t.Fatalf("turn attempt %d timing = %+v", attempt, turn)
		}
	}
}

func TestRuntimeRouterTurnInterruptCancelsActiveRuntimeAndRejectsConcurrentStart(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newBlockingAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello blocking runtime",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hold",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForBlockingAgentStart(t, agent)

	concurrentStart := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "second",
	}))
	if concurrentStart.Error == nil || concurrentStart.Error.Code != -32009 {
		t.Fatalf("expected active thread conflict, got %+v", concurrentStart.Error)
	}

	interrupt := router.Handle(requestWithParams(t, IntID(4), MethodTurnInterrupt, turn.TurnInterruptParams{
		ThreadID: threadID,
		TurnID:   turnID,
	}))
	if interrupt.Error != nil {
		t.Fatalf("interrupt error: %+v", interrupt.Error)
	}
	completed := waitForTurnCompletedStatus(t, sink, turnID, TurnStatusInterrupted)
	if completed.ThreadID != threadID {
		t.Fatalf("completed thread id = %q, want %q", completed.ThreadID, threadID)
	}
	if got := router.services.ThreadStatus.LoadedStatusForThread(threadID); got.Type != "idle" {
		t.Fatalf("thread status = %#v", got)
	}
}

func TestRuntimeRouterTurnFailureClearsActiveStateAndAllowsNextTurn(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := &failThenOKRuntimeAgent{}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello failing runtime",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	failedStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "fail once",
	}))
	if failedStart.Error != nil {
		t.Fatalf("failed turn start error: %+v", failedStart.Error)
	}
	failedTurnID := failedStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, failedTurnID, TurnStatusFailed)
	if got := router.services.ThreadStatus.LoadedStatusForThread(threadID); got.Type != "systemError" {
		t.Fatalf("thread status after failure = %#v", got)
	}
	failedRead := router.Handle(requestWithParams(t, IntID(3), MethodThreadRead, ThreadReadParams{ThreadID: threadID}))
	if failedRead.Error != nil {
		t.Fatalf("failed thread read error: %+v", failedRead.Error)
	}
	if got := failedRead.Result.(*ThreadReadResponse).Thread.Status.Type; got != "systemError" {
		t.Fatalf("thread/read status after failure = %q", got)
	}

	nextStart := router.Handle(requestWithParams(t, IntID(4), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "recover",
	}))
	if nextStart.Error != nil {
		t.Fatalf("next turn start error: %+v", nextStart.Error)
	}
	nextTurnID := nextStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, nextTurnID, TurnStatusCompleted)
	if got := router.services.ThreadStatus.LoadedStatusForThread(threadID); got.Type != "idle" {
		t.Fatalf("thread status after recovery = %#v", got)
	}
	recoveredRead := router.Handle(requestWithParams(t, IntID(5), MethodThreadRead, ThreadReadParams{ThreadID: threadID}))
	if recoveredRead.Error != nil {
		t.Fatalf("recovered thread read error: %+v", recoveredRead.Error)
	}
	if got := recoveredRead.Result.(*ThreadReadResponse).Thread.Status.Type; got != "idle" {
		t.Fatalf("thread/read status after recovery = %q", got)
	}
}

func TestRuntimeRouterResponsesStreamingEmitsDeltaNotifications(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		if body["stream"] != true {
			t.Fatalf("stream body = %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(modelResponsesSSE(
			`{"type":"response.metadata","metadata":{"model_reroute":{"from_model":"gpt-test","to_model":"gpt-safe","reason":"high_risk_cyber_activity"},"model_verification":{"verifications":["trusted_access_for_cyber"]},"turn_moderation_metadata":{"category":"cyber"},"safety_buffering":{"model":"gpt-safe","use_cases":["cyber"],"reasons":["policy"],"show_buffering_ui":true,"faster_model":"gpt-fast"}}}`,
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.output_item.added","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`,
			`{"type":"response.output_text.delta","item_id":"msg-1","delta":"hello "}`,
			`{"type":"response.output_text.delta","item_id":"msg-1","delta":"stream"}`,
			`{"type":"response.output_item.added","item":{"id":"call-1","type":"custom_tool_call","call_id":"call-1","name":"apply_patch","input":""}}`,
			`{"type":"response.custom_tool_call_input.delta","item_id":"call-1","call_id":"call-1","delta":"*** Begin Patch\n*** Add File: streamed.txt\n+streamed\n*** End Patch"}`,
			`{"type":"response.output_item.added","item":{"id":"reasoning-1","type":"reasoning","summary":[],"content":[],"encrypted_content":null}}`,
			`{"type":"response.reasoning_summary_text.delta","summary_index":0,"delta":"thinking "}`,
			`{"type":"response.reasoning_summary_part.added","summary_index":1}`,
			`{"type":"response.reasoning_text.delta","content_index":0,"delta":"raw thought"}`,
			`{"type":"response.output_item.done","item":{"id":"reasoning-1","type":"reasoning","summary":[{"type":"summary_text","text":"thinking done"}],"content":[{"type":"reasoning_text","text":"raw thought"}],"encrypted_content":null}}`,
			`{"type":"response.plan.delta","item_id":"plan-1","delta":"- inspect stream notifications\n"}`,
			`{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello stream"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
		)))
	}))
	defer server.Close()

	agent := model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{BaseURL: server.URL + "/v1"},
		Stream:   true,
	})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                   t.TempDir(),
		Prompt:                "hello streaming runtime",
		ExperimentalRawEvents: true,
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello streaming runtime",
		Model:    "gpt-test",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID

	messageStarted := waitForItemStarted(t, sink, "msg-1")
	if messageStarted.Item["responseId"] != "resp-1" {
		t.Fatalf("message started response id = %#v", messageStarted.Item)
	}
	waitForAgentDelta(t, sink, turnID, "hello ")
	waitForAgentDelta(t, sink, turnID, "stream")
	waitForFileChangePatchUpdated(t, sink, turnID, "streamed.txt")
	reasoningSummary := waitForReasoningSummaryDelta(t, sink, turnID, "thinking ")
	if reasoningSummary.ItemID != "reasoning-1" || reasoningSummary.SummaryIndex != 0 {
		t.Fatalf("reasoning summary delta = %+v", reasoningSummary)
	}
	reasoningPart := waitForReasoningPartAdded(t, sink, turnID, "reasoning-1")
	if reasoningPart.SummaryIndex != 1 {
		t.Fatalf("reasoning summary part = %+v", reasoningPart)
	}
	reasoningText := waitForReasoningTextDelta(t, sink, turnID, "raw thought")
	if reasoningText.ItemID != "reasoning-1" || reasoningText.ContentIndex != 0 {
		t.Fatalf("reasoning text delta = %+v", reasoningText)
	}
	planDelta := waitForPlanDelta(t, sink, turnID, "- inspect stream notifications\n")
	if planDelta.ItemID != "plan-1" {
		t.Fatalf("plan delta = %+v", planDelta)
	}
	rawReasoning := waitForRawResponseItemCompleted(t, sink, turnID, "reasoning-1")
	if rawReasoning["type"] != "reasoning" {
		t.Fatalf("raw reasoning item = %#v", rawReasoning)
	}
	rawMessage := waitForRawResponseItemCompleted(t, sink, turnID, "msg-1")
	if rawMessage["type"] != "message" {
		t.Fatalf("raw message item = %#v", rawMessage)
	}
	rerouted := waitForModelRerouted(t, sink, turnID)
	if rerouted.FromModel != "gpt-test" || rerouted.ToModel != "gpt-safe" || rerouted.Reason != string(ModelRerouteReasonHighRiskCyberActivity) {
		t.Fatalf("model/rerouted = %+v", rerouted)
	}
	verification := waitForModelVerification(t, sink, turnID)
	if len(verification.Verifications) != 1 || verification.Verifications[0] != ModelVerificationTrustedAccessForCyber {
		t.Fatalf("model/verification = %+v", verification)
	}
	if moderation := waitForTurnModerationMetadata(t, sink, turnID); moderation.Metadata == nil {
		t.Fatalf("turn/moderationMetadata = %+v", moderation)
	}
	usage := waitForTokenUsageUpdated(t, sink, turnID)
	if usage.TokenUsage.InputTokens != 1 || usage.TokenUsage.OutputTokens != 2 || usage.TokenUsage.TotalTokens != 3 {
		t.Fatalf("usage = %+v", usage.TokenUsage)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	assertNoModelSafetyBufferingNotification(t, sink, turnID)
}

func TestRuntimeRouterResponsesStreamingSuppressesRawResponseItemsByDefault(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(modelResponsesSSE(
			`{"type":"response.created","response":{"id":"resp-default-raw"}}`,
			`{"type":"response.output_item.added","item":{"id":"msg-default-raw","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`,
			`{"type":"response.output_text.delta","item_id":"msg-default-raw","delta":"hidden raw"}`,
			`{"type":"response.output_item.done","item":{"id":"msg-default-raw","type":"message","role":"assistant","content":[{"type":"output_text","text":"hidden raw"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-default-raw","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)))
	}))
	defer server.Close()

	agent := model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{BaseURL: server.URL + "/v1"},
		Stream:   true,
	})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello default raw suppression",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello default raw suppression",
		Model:    "gpt-test",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	for _, notification := range sink.List() {
		if notification.Method == NotificationRawResponseItemCompleted {
			t.Fatalf("unexpected rawResponseItem/completed notification by default: %+v", notification.Params)
		}
	}
}

func TestRuntimeRouterPlanModeStreamsProposedPlanItem(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	fullMessage := "Intro\n<proposed_plan>\n- Step 1\n</proposed_plan>\nOutro"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(modelResponsesSSE(
			`{"type":"response.created","response":{"id":"resp-plan"}}`,
			`{"type":"response.output_item.added","item":{"id":"msg-plan","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`,
			`{"type":"response.output_text.delta","item_id":"msg-plan","delta":"Intro\n<proposed"}`,
			`{"type":"response.output_text.delta","item_id":"msg-plan","delta":"_plan>\n- Step 1\n"}`,
			`{"type":"response.output_text.delta","item_id":"msg-plan","delta":"</proposed_plan>\nOutro"}`,
			`{"type":"response.output_item.done","item":{"id":"msg-plan","type":"message","role":"assistant","content":[{"type":"output_text","text":`+strconv.Quote(fullMessage)+`}]}}`,
			`{"type":"response.completed","response":{"id":"resp-plan","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)))
	}))
	defer server.Close()

	agent := model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{BaseURL: server.URL + "/v1"},
		Stream:   true,
	})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "plan",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID:          threadID,
		Prompt:            "plan",
		Model:             "gpt-test",
		CollaborationMode: map[string]any{"mode": "plan"},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	planItemID := safeIdentifier(turnID) + "-plan"

	waitForAgentDelta(t, sink, turnID, "Intro\n")
	waitForAgentDelta(t, sink, turnID, "Outro")
	planStarted := waitForItemStarted(t, sink, planItemID)
	if planStarted.Item["type"] != "plan" || planStarted.Item["responseId"] != "resp-plan" {
		t.Fatalf("plan started item = %#v", planStarted.Item)
	}
	planDelta := waitForPlanDelta(t, sink, turnID, "- Step 1\n")
	if planDelta.ItemID != planItemID {
		t.Fatalf("plan delta = %+v", planDelta)
	}
	planCompleted := waitForItemCompleted(t, sink, planItemID)
	if planCompleted.Item["type"] != "plan" || planCompleted.Item["text"] != "- Step 1\n" {
		t.Fatalf("plan completed item = %#v", planCompleted.Item)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if sinkHasAgentDeltaContaining(sink, "proposed_plan") {
		t.Fatalf("agent deltas leaked proposed_plan tags: %#v", sink.List())
	}
}

func TestRuntimeRouterApplyPatchEmitsTurnDiffUpdated(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	cwd := t.TempDir()
	agent := &applyPatchRuntimeAgent{
		patch: `*** Begin Patch
*** Add File: diff.txt
+hello diff
*** End Patch`,
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    cwd,
		Prompt: "patch turn",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "apply patch",
		CWD:      cwd,
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID

	diff := waitForTurnDiffUpdated(t, sink, turnID)
	if diff.ThreadID != threadID || !strings.Contains(diff.Diff, "diff.txt") || !strings.Contains(diff.Diff, "+hello diff") {
		t.Fatalf("turn diff notification = %+v", diff)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestRuntimeRouterUpdatePlanEmitsTurnPlanUpdated(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        &updatePlanRuntimeAgent{},
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "plan turn",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "make a plan",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID

	updated := waitForTurnPlanUpdated(t, sink, turnID)
	if updated.ThreadID != threadID || updated.Explanation == nil || *updated.Explanation != "next steps" {
		t.Fatalf("turn/plan/updated = %+v", updated)
	}
	if len(updated.Plan) != 2 || updated.Plan[0].Status != TurnPlanStepInProgress || updated.Plan[1].Status != TurnPlanStepPending {
		t.Fatalf("turn plan = %+v", updated.Plan)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestRuntimeRouterBuildsResponsesAgentFromConfig(t *testing.T) {
	store := session.NewStore(t.TempDir())
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-appserver")); err != nil {
		t.Fatalf("Save auth error = %v", err)
	}
	var recordedAuth string
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(modelResponsesSSE(
			`{"type":"response.created","response":{"id":"resp-runtime"}}`,
			`{"type":"response.output_item.added","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`,
			`{"type":"response.output_text.delta","item_id":"msg-1","delta":"from configured responses"}`,
			`{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"from configured responses"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-runtime","model":"gpt-app","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
		)))
	}))
	defer server.Close()
	configBody := "model = \"gpt-app\"\nmodel_provider = \"openai\"\nopenai_base_url = \"" + server.URL + "/v1\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("Write config error = %v", err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Config:       config.NewConfigService(home),
		ThreadStatus: NewThreadStatusManager(),
		HTTPClient:   server.Client(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "configured runtime",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "call configured responses",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if recordedAuth != "Bearer sk-appserver" {
		t.Fatalf("Authorization = %q", recordedAuth)
	}
	if recordedBody["model"] != "gpt-app" {
		t.Fatalf("body = %#v", recordedBody)
	}
	if recordedBody["stream"] != true {
		t.Fatalf("body stream = %#v", recordedBody)
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if record.Metadata.LastResponseID != "resp-runtime" {
		t.Fatalf("last response id = %q", record.Metadata.LastResponseID)
	}
}

func TestRuntimeRouterValidationErrors(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	response := router.Handle(requestWithParams(t, IntID(1), MethodProcessSpawn, ProcessSpawnParams{}))
	if response.Error == nil || response.Error.Code != -32602 {
		t.Fatalf("expected invalid process spawn, got %+v", response.Error)
	}
	response = router.Handle(&Request{ID: IntID(2), Method: Method("missing/method"), Params: []byte(`{}`)})
	if response.Error == nil || response.Error.Code != -32601 {
		t.Fatalf("expected unknown method, got %+v", response.Error)
	}
}

func waitForSessionItem(t *testing.T, store *session.Store, threadID string, itemType string) *session.Record {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last *session.Record
	for time.Now().Before(deadline) {
		record, err := store.Read(session.ThreadID(threadID), true, true)
		if err == nil {
			last = record
			for i := range record.Items {
				if record.Items[i].Type == itemType {
					return record
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s in record %#v", itemType, last)
	return nil
}

func waitForNotificationMethod(t *testing.T, sink *NotificationBuffer, method NotificationMethod) *Notification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method == method {
				return notification
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for notification %s in %#v", method, last)
	return nil
}

func waitForItemStarted(t *testing.T, sink *NotificationBuffer, itemID string) *ItemStartedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationItemStarted {
				continue
			}
			payload, ok := notification.Params.(*ItemStartedNotification)
			if !ok || payload == nil {
				continue
			}
			if payload.Item["id"] == itemID {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for started item %s in %#v", itemID, last)
	return nil
}

func waitForCommandExecutionStartedBySource(t *testing.T, sink *NotificationBuffer, source CommandExecutionSource) *ItemStartedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationItemStarted {
				continue
			}
			payload, ok := notification.Params.(*ItemStartedNotification)
			if !ok || payload == nil {
				continue
			}
			item := notificationItemMap(t, payload.Item)
			if item["type"] == "commandExecution" && item["source"] == string(source) {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for commandExecution started from %s in %#v", source, last)
	return nil
}

func notificationsContainHook(notifications []*Notification, event HookEventName) bool {
	for _, notification := range notifications {
		if notification == nil || notification.Method != NotificationHookCompleted {
			continue
		}
		completed, ok := notification.Params.(*HookRunCompletedNotification)
		if ok && completed.Run.EventName == event {
			return true
		}
	}
	return false
}

func waitForItemCompleted(t *testing.T, sink *NotificationBuffer, itemID string) *ItemCompletedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationItemCompleted {
				continue
			}
			payload, ok := notification.Params.(*ItemCompletedNotification)
			if !ok || payload == nil {
				continue
			}
			if payload.Item["id"] == itemID {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for completed item %s in %#v", itemID, last)
	return nil
}

func waitForAgentDelta(t *testing.T, sink *NotificationBuffer, turnID string, delta string) *AgentMessageDeltaNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationAgentMessageDelta {
				continue
			}
			payload, ok := notification.Params.(*AgentMessageDeltaNotification)
			if !ok || payload == nil {
				continue
			}
			if payload.TurnID == turnID && payload.Delta == delta {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn %s delta %q in %#v", turnID, delta, last)
	return nil
}

func waitForCommandExecutionOutputDeltaContaining(t *testing.T, sink *NotificationBuffer, turnID string, itemID string, text string) *CommandExecutionOutputDeltaNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationCommandExecutionOutputDelta {
				continue
			}
			payload, ok := notification.Params.(*CommandExecutionOutputDeltaNotification)
			if !ok || payload == nil {
				continue
			}
			if payload.TurnID == turnID && payload.ItemID == itemID && strings.Contains(payload.Delta, text) {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for commandExecution output delta containing %q in %#v", text, last)
	return nil
}

func waitForPlanDelta(t *testing.T, sink *NotificationBuffer, turnID string, delta string) *PlanDeltaNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationPlanDelta {
				continue
			}
			payload, ok := notification.Params.(*PlanDeltaNotification)
			if !ok || payload == nil {
				continue
			}
			if payload.TurnID == turnID && payload.Delta == delta {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn %s plan delta %q in %#v", turnID, delta, last)
	return nil
}

func waitForReasoningSummaryDelta(t *testing.T, sink *NotificationBuffer, turnID string, delta string) *ReasoningSummaryTextDeltaNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationReasoningSummaryTextDelta {
				continue
			}
			payload, ok := notification.Params.(*ReasoningSummaryTextDeltaNotification)
			if !ok || payload == nil {
				continue
			}
			if payload.TurnID == turnID && payload.Delta == delta {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn %s reasoning summary delta %q in %#v", turnID, delta, last)
	return nil
}

func waitForReasoningTextDelta(t *testing.T, sink *NotificationBuffer, turnID string, delta string) *ReasoningTextDeltaNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationReasoningTextDelta {
				continue
			}
			payload, ok := notification.Params.(*ReasoningTextDeltaNotification)
			if !ok || payload == nil {
				continue
			}
			if payload.TurnID == turnID && payload.Delta == delta {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn %s reasoning text delta %q in %#v", turnID, delta, last)
	return nil
}

func waitForReasoningPartAdded(t *testing.T, sink *NotificationBuffer, turnID string, itemID string) *ReasoningSummaryPartAddedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationReasoningSummaryPartAdded {
				continue
			}
			payload, ok := notification.Params.(*ReasoningSummaryPartAddedNotification)
			if !ok || payload == nil {
				continue
			}
			if payload.TurnID == turnID && payload.ItemID == itemID {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn %s reasoning part item %s in %#v", turnID, itemID, last)
	return nil
}

func waitForRawResponseItemCompleted(t *testing.T, sink *NotificationBuffer, turnID string, itemID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationRawResponseItemCompleted {
				continue
			}
			payload, ok := notification.Params.(*RawResponseItemCompletedNotification)
			if !ok || payload == nil || payload.TurnID != turnID {
				continue
			}
			item := notificationItemMap(t, payload.Item)
			if item["id"] == itemID {
				return item
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn %s raw response item %s in %#v", turnID, itemID, last)
	return nil
}

func notificationItemMap(t *testing.T, item any) map[string]any {
	t.Helper()
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal notification item error = %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal notification item error = %v", err)
	}
	return out
}

func sinkHasAgentDeltaContaining(sink *NotificationBuffer, needle string) bool {
	for _, notification := range sink.List() {
		if notification == nil || notification.Method != NotificationAgentMessageDelta {
			continue
		}
		payload, ok := notification.Params.(*AgentMessageDeltaNotification)
		if ok && payload != nil && strings.Contains(payload.Delta, needle) {
			return true
		}
	}
	return false
}

func waitForFileChangePatchUpdated(t *testing.T, sink *NotificationBuffer, turnID string, path string) *FileChangePatchUpdatedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationFileChangePatchUpdated {
				continue
			}
			payload, ok := notification.Params.(*FileChangePatchUpdatedNotification)
			if !ok || payload == nil || payload.TurnID != turnID {
				continue
			}
			for _, change := range payload.Changes {
				data, ok := change.(map[string]any)
				if ok && data["path"] == path {
					return payload
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for fileChange patchUpdated %q in %#v", path, last)
	return nil
}

func waitForTurnDiffUpdated(t *testing.T, sink *NotificationBuffer, turnID string) *TurnDiffUpdatedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationTurnDiffUpdated {
				continue
			}
			payload, ok := notification.Params.(*TurnDiffUpdatedNotification)
			if ok && payload != nil && payload.TurnID == turnID {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn diff update for turn %s in %#v", turnID, last)
	return nil
}

func waitForTurnPlanUpdated(t *testing.T, sink *NotificationBuffer, turnID string) *TurnPlanUpdatedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationTurnPlanUpdated {
				continue
			}
			payload, ok := notification.Params.(*TurnPlanUpdatedNotification)
			if ok && payload != nil && payload.TurnID == turnID {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn plan update for turn %s in %#v", turnID, last)
	return nil
}

func waitForModelRerouted(t *testing.T, sink *NotificationBuffer, turnID string) *ModelReroutedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationModelRerouted {
				continue
			}
			payload, ok := notification.Params.(*ModelReroutedNotification)
			if ok && payload != nil && payload.TurnID == turnID {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for model/rerouted for turn %s in %#v", turnID, last)
	return nil
}

func waitForModelVerification(t *testing.T, sink *NotificationBuffer, turnID string) *ModelVerificationNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationModelVerification {
				continue
			}
			payload, ok := notification.Params.(*ModelVerificationNotification)
			if ok && payload != nil && payload.TurnID == turnID {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for model/verification for turn %s in %#v", turnID, last)
	return nil
}

func waitForTurnModerationMetadata(t *testing.T, sink *NotificationBuffer, turnID string) *TurnModerationMetadataNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationTurnModerationMetadata {
				continue
			}
			payload, ok := notification.Params.(*TurnModerationMetadataNotification)
			if ok && payload != nil && payload.TurnID == turnID {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn/moderationMetadata for turn %s in %#v", turnID, last)
	return nil
}

func assertNoModelSafetyBufferingNotification(t *testing.T, sink *NotificationBuffer, turnID string) {
	t.Helper()
	for _, notification := range sink.List() {
		if notification.Method != NotificationMethod("model/safetyBuffering/updated") {
			continue
		}
		if params, ok := notification.Params.(map[string]any); ok && params["turnId"] == turnID {
			t.Fatalf("non-Rust model/safetyBuffering notification emitted: %+v", params)
		}
		t.Fatalf("non-Rust model/safetyBuffering notification emitted: %+v", notification.Params)
	}
}

func waitForTokenUsageUpdated(t *testing.T, sink *NotificationBuffer, turnID string) *ThreadTokenUsageUpdatedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationThreadTokenUsageUpdated {
				continue
			}
			payload, ok := notification.Params.(*ThreadTokenUsageUpdatedNotification)
			if ok && payload != nil && payload.TurnID == turnID {
				return payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for token usage update for turn %s in %#v", turnID, last)
	return nil
}

type blockingAgent struct {
	started chan struct{}
}

func newBlockingAgent() *blockingAgent {
	return &blockingAgent{started: make(chan struct{})}
}

func (a *blockingAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	select {
	case <-a.started:
	default:
		close(a.started)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

type applyPatchRuntimeAgent struct {
	patch string
	calls int
}

func (a *applyPatchRuntimeAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.calls++
	if a.calls == 1 {
		return &model.AgentResponse{
			Items: []model.AgentItem{{
				ID:     "patch-call-1",
				Type:   "custom_tool_call",
				Name:   "apply_patch",
				CallID: "patch-call-1",
				Input:  a.patch,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-" + request.TurnID,
		Message:    "patched",
		Items: []model.AgentItem{{
			ID:   "msg-after-patch",
			Type: "agent_message",
			Text: "patched",
		}},
	}, nil
}

type updatePlanRuntimeAgent struct {
	calls int
}

func (a *updatePlanRuntimeAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.calls++
	if a.calls == 1 {
		return &model.AgentResponse{
			Items: []model.AgentItem{{
				ID:        "plan-call-1",
				Type:      "function_call",
				Name:      "update_plan",
				CallID:    "plan-call-1",
				Arguments: `{"explanation":"next steps","plan":[{"step":"write code","status":"in_progress"},{"step":"verify","status":"pending"}]}`,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-" + request.TurnID,
		Message:    "planned",
		Items: []model.AgentItem{{
			ID:   "msg-after-plan",
			Type: "agent_message",
			Text: "planned",
		}},
	}, nil
}

type steerAwareToolAgent struct {
	firstRequest           chan struct{}
	releaseFirstResponseCh chan struct{}
	releaseOnce            sync.Once
	mu                     sync.Mutex
	requests               []model.AgentRequest
}

func newSteerAwareToolAgent() *steerAwareToolAgent {
	return &steerAwareToolAgent{
		firstRequest:           make(chan struct{}),
		releaseFirstResponseCh: make(chan struct{}),
	}
}

func (a *steerAwareToolAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.requests = append(a.requests, *request)
	callCount := len(a.requests)
	a.mu.Unlock()
	if callCount == 1 {
		close(a.firstRequest)
		select {
		case <-a.releaseFirstResponseCh:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &model.AgentResponse{
			Items: []model.AgentItem{{
				ID:        "echo-call-1",
				Type:      "function_call",
				Name:      "echo",
				CallID:    "echo-call-1",
				Arguments: `{"text":"hello"}`,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-" + request.TurnID,
		Message:    "done",
		Items: []model.AgentItem{{
			ID:   "msg-after-steer",
			Type: "agent_message",
			Text: "done",
		}},
	}, nil
}

func (a *steerAwareToolAgent) releaseFirstResponse() {
	a.releaseOnce.Do(func() {
		close(a.releaseFirstResponseCh)
	})
}

func (a *steerAwareToolAgent) sawSteerText(text string) bool {
	for _, request := range a.snapshotRequests() {
		for _, raw := range request.InputItems {
			item, ok := raw.(map[string]any)
			if !ok || item["role"] != "user" {
				continue
			}
			if appserverContentHasInputText(item["content"], text) {
				return true
			}
		}
	}
	return false
}

func (a *steerAwareToolAgent) snapshotRequests() []model.AgentRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]model.AgentRequest(nil), a.requests...)
}

func waitForSteerAwareFirstRequest(t *testing.T, agent *steerAwareToolAgent) {
	t.Helper()
	select {
	case <-agent.firstRequest:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first steer-aware request")
	}
}

func appserverContentHasInputText(raw any, text string) bool {
	content, ok := raw.([]map[string]any)
	if !ok {
		return false
	}
	for i := range content {
		if content[i]["type"] == "input_text" && content[i]["text"] == text {
			return true
		}
	}
	return false
}

type recordingRuntimeAgent struct {
	message  string
	requests chan model.AgentRequest
}

type failThenOKRuntimeAgent struct {
	mu    sync.Mutex
	calls int
}

func (a *failThenOKRuntimeAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.calls++
	call := a.calls
	a.mu.Unlock()
	if call == 1 {
		return nil, context.DeadlineExceeded
	}
	return &model.AgentResponse{
		ResponseID: "resp-recovered",
		Message:    "recovered",
		Items: []model.AgentItem{{
			ID:   "msg-recovered",
			Type: "agent_message",
			Text: "recovered",
		}},
		Usage: model.AgentUsage{InputTokens: 1, OutputTokens: 1},
	}, nil
}

func newRecordingRuntimeAgent(message string) *recordingRuntimeAgent {
	return &recordingRuntimeAgent{message: message, requests: make(chan model.AgentRequest, 1)}
}

func (a *recordingRuntimeAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests <- *request
	message := firstNonEmpty(a.message, "ok")
	return &model.AgentResponse{
		ResponseID: "resp-" + request.TurnID,
		Message:    message,
		Usage:      model.AgentUsage{InputTokens: 2, CachedInputTokens: 1, OutputTokens: 3, ReasoningOutputTokens: 1},
		Items: []model.AgentItem{{
			ID:   "msg-1",
			Type: "agent_message",
			Text: message,
		}},
		Model:      request.Model,
		ProviderID: request.ProviderID,
	}, nil
}

type implicitSkillRuntimeAgent struct {
	mu       sync.Mutex
	requests []model.AgentRequest
}

func newImplicitSkillRuntimeAgent() *implicitSkillRuntimeAgent {
	return &implicitSkillRuntimeAgent{}
}

func (a *implicitSkillRuntimeAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.requests = append(a.requests, *request)
	callCount := len(a.requests)
	a.mu.Unlock()
	if callCount == 1 {
		return &model.AgentResponse{
			ResponseID: "resp-shell-call",
			Items: []model.AgentItem{{
				ID:        "shell-call-1",
				Type:      "function_call",
				Name:      tool.DefaultExecCommandToolName,
				CallID:    "shell-call-1",
				Arguments: `{"cmd":"echo build-skill"}`,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-after-shell",
		Message:    "done",
		Items: []model.AgentItem{{
			ID:   "msg-after-shell",
			Type: "agent_message",
			Text: "done",
		}},
	}, nil
}

func waitForImplicitSkillAgentRequest(t *testing.T, agent *implicitSkillRuntimeAgent, count int) model.AgentRequest {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		agent.mu.Lock()
		if len(agent.requests) >= count {
			request := agent.requests[count-1]
			agent.mu.Unlock()
			return request
		}
		agent.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	t.Fatalf("timed out waiting for agent request %d in %#v", count, agent.requests)
	return model.AgentRequest{}
}

func waitForRuntimeAgentRequest(t *testing.T, agent *recordingRuntimeAgent) model.AgentRequest {
	t.Helper()
	select {
	case request := <-agent.requests:
		return request
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime agent request")
	}
	return model.AgentRequest{}
}

func modelWithServiceTierForRuntimeTest(t *testing.T) (string, string) {
	t.Helper()
	manager := model.NewStaticModelsManager(model.BundledModelsResponse())
	for _, info := range manager.RawModelCatalog(model.RefreshOffline).Models {
		modelID := strings.TrimSpace(info.Slug)
		if modelID == "" {
			continue
		}
		for _, tier := range info.ServiceTiers {
			tier = strings.TrimSpace(tier)
			if tier != "" && tier != model.ServiceTierDefaultRequestValue {
				return modelID, tier
			}
		}
	}
	t.Fatal("bundled model catalog does not include a non-default service tier")
	return "", ""
}

func personalityModelServiceForRuntimeTest() *model.ModelService {
	return model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug:             "personality-model",
		DisplayName:      "personality-model",
		Visibility:       model.VisibilityVisible,
		SupportedInAPI:   true,
		BaseInstructions: "Base default instructions",
		ModelMessages: &model.ModelMessages{
			InstructionsTemplate: "Base {{ personality }}",
			PersonalityDefault:   "default personality",
			PersonalityFriendly:  "friendly personality",
			PersonalityPragmatic: "pragmatic personality",
		},
	}}}))
}

func agentRequestInputItemsContain(request model.AgentRequest, want string) bool {
	return strings.Contains(inputItemText(request.InputItems), want)
}

func inputImageDetails(value any) []string {
	return inputImageFieldValues(value, "detail")
}

func inputImageURLs(value any) []string {
	return inputImageFieldValues(value, "image_url")
}

func inputImageFieldValues(value any, field string) []string {
	switch v := value.(type) {
	case []any:
		out := []string{}
		for _, item := range v {
			out = append(out, inputImageFieldValues(item, field)...)
		}
		return out
	case []map[string]any:
		out := []string{}
		for _, item := range v {
			out = append(out, inputImageFieldValues(item, field)...)
		}
		return out
	case map[string]any:
		out := []string{}
		if v["type"] == "input_image" {
			out = append(out, stringValue(v[field]))
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out = append(out, inputImageFieldValues(v[key], field)...)
		}
		return out
	default:
		return nil
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func inputItemText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case model.AgentItem:
		return v.Text
	case *model.AgentItem:
		if v == nil {
			return ""
		}
		return v.Text
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, inputItemText(item))
		}
		return strings.Join(parts, "\n")
	case []map[string]any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, inputItemText(item))
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, inputItemText(v[key]))
		}
		return strings.Join(parts, "\n")
	default:
		return inputItemTextReflect(reflect.ValueOf(value))
	}
}

func inputItemTextReflect(value reflect.Value) string {
	if !value.IsValid() {
		return ""
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return ""
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String:
		return value.String()
	case reflect.Slice, reflect.Array:
		parts := make([]string, 0, value.Len())
		for i := 0; i < value.Len(); i++ {
			parts = append(parts, inputItemTextReflect(value.Index(i)))
		}
		return strings.Join(parts, "\n")
	case reflect.Map:
		parts := make([]string, 0, value.Len())
		for _, key := range value.MapKeys() {
			parts = append(parts, inputItemTextReflect(value.MapIndex(key)))
		}
		return strings.Join(parts, "\n")
	case reflect.Struct:
		if field := value.FieldByName("Text"); field.IsValid() && field.Kind() == reflect.String {
			if text := field.String(); text != "" {
				return text
			}
		}
		if field := value.FieldByName("Content"); field.IsValid() {
			return inputItemTextReflect(field)
		}
	}
	return ""
}

type recordingCompactRunner struct {
	summary string
	request *compact.Request
}

func (r *recordingCompactRunner) Compact(ctx context.Context, request *compact.Request) (*compact.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	copied := *request
	r.request = &copied
	summary := firstNonEmpty(r.summary, "remote compact summary")
	return &compact.Result{
		Status:  compact.StatusCompleted,
		Request: copied,
		Summary: summary,
		NewHistory: compact.BuildCompactedHistory(nil, []compact.Item{{
			ID:   "u-remote",
			Type: "message",
			Role: "user",
			Kind: "user_message",
			Text: "latest user",
		}}, summary),
		CompletedAt: time.Now().UTC(),
		Source:      compact.SourceRemote,
		ResponseID:  "resp-compact",
		Model:       "gpt-compact",
		ProviderID:  "openai",
		Usage:       &compact.Usage{InputTokens: 3, CachedInputTokens: 1, OutputTokens: 2, ReasoningOutputTokens: 1},
	}, nil
}

type userInputToolAgent struct {
	calls  int
	answer string
}

func newUserInputToolAgent() *userInputToolAgent {
	return &userInputToolAgent{}
}

func (a *userInputToolAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.calls++
	if a.calls == 1 {
		return &model.AgentResponse{
			Items: []model.AgentItem{{
				ID:        "input-call-1",
				Type:      "function_call",
				Name:      "request_user_input",
				CallID:    "input-call-1",
				Arguments: `{"questions":[{"header":"Choice","id":"choice","question":"Pick one?","isOther":true,"isSecret":true,"options":[{"label":"Blue","description":"Use blue"},{"label":"Green","description":"Use green"}]}],"autoResolutionMs":60000}`,
			}},
		}, nil
	}
	a.answer = userInputAnswerFromAgentRequest(request, "choice")
	return &model.AgentResponse{
		Message: "answer: " + a.answer,
		Items: []model.AgentItem{{
			ID:   "msg-after-input",
			Type: "agent_message",
			Text: "answer: " + a.answer,
		}},
		Usage: model.AgentUsage{InputTokens: 1, OutputTokens: 1},
	}, nil
}

func userInputAnswerFromAgentRequest(request *model.AgentRequest, questionID string) string {
	if request == nil {
		return ""
	}
	for _, raw := range request.InputItems {
		item, ok := raw.(*turn.ToolResponseItem)
		if !ok || item == nil || item.Output == nil {
			continue
		}
		var response tool.UserInputResponse
		if err := json.Unmarshal([]byte(item.Output.Text()), &response); err != nil {
			continue
		}
		if answer := strings.TrimSpace(response.Answers[questionID]); answer != "" {
			return answer
		}
	}
	return ""
}

func waitForBlockingAgentStart(t *testing.T, agent *blockingAgent) {
	t.Helper()
	select {
	case <-agent.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocking agent start")
	}
}

func rolloutTurnSnapshotsForThread(t *testing.T, store *session.Store, threadID string) []session.TurnSnapshot {
	t.Helper()
	return rolloutRecordForThread(t, store, threadID).Metadata.RolloutTurns
}

func rolloutRecordForThread(t *testing.T, store *session.Store, threadID string) *session.Record {
	t.Helper()
	path, err := rollout.FindThreadPath(store.Root(), threadID, false)
	if err != nil {
		t.Fatalf("rollout path error: %v", err)
	}
	record, err := rollout.RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("rollout record error: %v", err)
	}
	return record
}

func waitForTurnCompletedStatus(t *testing.T, sink *NotificationBuffer, turnID string, status TurnStatus) *TurnCompletedNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []*Notification
	for time.Now().Before(deadline) {
		last = sink.List()
		for _, notification := range last {
			if notification.Method != NotificationTurnCompleted {
				continue
			}
			completed, ok := notification.Params.(*TurnCompletedNotification)
			if !ok || completed == nil {
				continue
			}
			if completed.Turn.ID == turnID && completed.Turn.Status == status {
				return completed
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn %s status %s in notifications %#v", turnID, status, last)
	return nil
}

func sinkHasMethod(sink *NotificationBuffer, method NotificationMethod) bool {
	for _, notification := range sink.List() {
		if notification.Method == method {
			return true
		}
	}
	return false
}

func remoteControlStatusChangedNotifications(sink *NotificationBuffer) []*RemoteControlStatusChangedNotification {
	var out []*RemoteControlStatusChangedNotification
	for _, notification := range sink.List() {
		if notification.Method != NotificationRemoteControlStatusChanged {
			continue
		}
		if payload, ok := notification.Params.(*RemoteControlStatusChangedNotification); ok && payload != nil {
			out = append(out, payload)
		}
	}
	return out
}

func realtimeNotification[T any](t *testing.T, sink *NotificationBuffer, method NotificationMethod) T {
	t.Helper()
	var zero T
	for _, notification := range sink.List() {
		if notification.Method != method {
			continue
		}
		payload, ok := notification.Params.(T)
		if !ok {
			t.Fatalf("notification %s params = %#v", method, notification.Params)
		}
		return payload
	}
	t.Fatalf("notification %s missing in %#v", method, sink.List())
	return zero
}

func threadSettingsNotification(t *testing.T, sink *NotificationBuffer, threadID string) *SettingsUpdatedNotification {
	t.Helper()
	for _, notification := range sink.List() {
		if notification.Method != NotificationThreadSettingsUpdated {
			continue
		}
		payload, ok := notification.Params.(*SettingsUpdatedNotification)
		if ok && payload != nil && payload.ThreadID == threadID {
			return payload
		}
	}
	t.Fatalf("thread/settings/updated missing in %#v", sink.List())
	return nil
}

func lastThreadSettingsNotification(t *testing.T, sink *NotificationBuffer, threadID string) *SettingsUpdatedNotification {
	t.Helper()
	notifications := sink.List()
	for i := len(notifications) - 1; i >= 0; i-- {
		notification := notifications[i]
		if notification.Method != NotificationThreadSettingsUpdated {
			continue
		}
		payload, ok := notification.Params.(*SettingsUpdatedNotification)
		if ok && payload != nil && payload.ThreadID == threadID {
			return payload
		}
	}
	t.Fatalf("thread/settings/updated missing in %#v", notifications)
	return nil
}

func notificationThreadIDs(sink *NotificationBuffer, method NotificationMethod) []string {
	out := []string{}
	for _, notification := range sink.List() {
		if notification.Method != method {
			continue
		}
		payload, ok := notification.Params.(*ThreadIDNotification)
		if !ok || payload == nil {
			continue
		}
		out = append(out, payload.ThreadID)
	}
	return out
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sinkHasThreadStatusFlag(sink *NotificationBuffer, threadID string, flag ThreadActiveFlag) bool {
	for _, notification := range sink.List() {
		if notification.Method != NotificationThreadStatusChanged {
			continue
		}
		payload, ok := notification.Params.(*ThreadStatusChangedNotification)
		if !ok || payload == nil || payload.ThreadID != threadID {
			continue
		}
		for _, activeFlag := range payload.Status.ActiveFlags {
			if activeFlag == flag {
				return true
			}
		}
	}
	return false
}

func modelResponsesSSE(payloads ...string) string {
	var builder strings.Builder
	for _, payload := range payloads {
		eventType := modelResponseSSEType(payload)
		if eventType != "" {
			builder.WriteString("event: ")
			builder.WriteString(eventType)
			builder.WriteByte('\n')
		}
		builder.WriteString("data: ")
		builder.WriteString(payload)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func modelResponseSSEType(payload string) string {
	var value map[string]any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return ""
	}
	eventType, _ := value["type"].(string)
	return eventType
}

func fakeJWTAppserver(claims map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
}

func clearAuthEnvAppserver(t *testing.T) {
	t.Helper()
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
}
