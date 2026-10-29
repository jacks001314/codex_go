package appserver

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

func newInternalSessionTestRouter(t *testing.T) *RuntimeRouter {
	t.Helper()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		DefaultCWD:   t.TempDir(),
	})
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func startInternalSessionParent(t *testing.T, router *RuntimeRouter, requestID int64, modelID string) *Thread {
	t.Helper()
	response := router.Handle(requestWithParams(t, IntID(requestID), MethodThreadStart, ThreadStartParams{
		CWD:         t.TempDir(),
		Model:       modelID,
		ServiceName: stringPtr("codex_work_desktop"),
	}))
	if response.Error != nil {
		t.Fatalf("thread start error: %+v", response.Error)
	}
	thread := response.Result.(*ThreadStartResponse).Thread
	if thread == nil || strings.TrimSpace(thread.ID) == "" {
		t.Fatalf("thread start returned no thread")
	}
	return thread
}

// TestSpawnInternalSessionPreservesParentLineageWithoutForkingHistoryLikeRust
// mirrors Rust's spawn_internal_session_preserves_parent_lineage_without_forking_history.
func TestSpawnInternalSessionPreservesParentLineageWithoutForkingHistoryLikeRust(t *testing.T) {
	router := newInternalSessionTestRouter(t)
	parent := startInternalSessionParent(t, router, 1, "gpt-test")

	parentRecord, err := router.threadRecord(session.ThreadID(parent.ID), true, true)
	if err != nil || parentRecord == nil {
		t.Fatalf("parent record: %v", err)
	}
	parentRecord.Items = []session.Item{{
		ID:        "parent-item",
		Type:      "message",
		Role:      "user",
		Text:      "parent history must not be inherited",
		CreatedAt: time.Now().UTC(),
	}}
	if err := router.runtimeSaveThreadRecord(parentRecord); err != nil {
		t.Fatalf("seed parent history: %v", err)
	}

	internal, err := router.SpawnInternalSession(parent.ID, InternalSessionOptions{
		Source: InternalSessionSourceGuardian,
		Model:  "gpt-test",
		History: []session.Item{{
			ID:        "caller-item",
			Type:      "message",
			Role:      "user",
			Text:      "caller history must not be inherited",
			CreatedAt: time.Now().UTC(),
		}},
	})
	if err != nil {
		t.Fatalf("spawn internal session: %v", err)
	}
	if internal.SessionID != parent.SessionID {
		t.Fatalf("internal session id = %q, want parent session id %q", internal.SessionID, parent.SessionID)
	}
	record, err := router.threadRecord(session.ThreadID(internal.ThreadID), true, true)
	if err != nil || record == nil {
		t.Fatalf("internal record: %v", err)
	}
	if record.SessionID != parent.SessionID {
		t.Fatalf("record session id = %q, want %q", record.SessionID, parent.SessionID)
	}
	if record.ParentThreadID != session.ThreadID(parent.ID) {
		t.Fatalf("record parent thread id = %q, want %q", record.ParentThreadID, parent.ID)
	}
	if record.ForkedFromID != "" {
		t.Fatalf("record forked_from = %q, want empty", record.ForkedFromID)
	}
	if record.Metadata.Source != string(internalGuardianSessionSource) {
		t.Fatalf("record source = %q, want %q", record.Metadata.Source, internalGuardianSessionSource)
	}
	if record.Metadata.ThreadSource != string(ThreadSourceGuardianReview) {
		t.Fatalf("record thread source = %q, want %q", record.Metadata.ThreadSource, ThreadSourceGuardianReview)
	}
	if record.Metadata.Originator != "codex_work_desktop" {
		t.Fatalf("record originator = %q, want codex_work_desktop", record.Metadata.Originator)
	}
	if record.Metadata.MultiAgentVersion != internalSessionMultiAgentVersionDisabled {
		t.Fatalf("record multi-agent version = %q, want disabled", record.Metadata.MultiAgentVersion)
	}
	if len(record.Items) != 0 {
		t.Fatalf("internal history = %#v, want empty", record.Items)
	}
	if internal.Response == nil || internal.Response.Thread == nil || internal.Response.Thread.ID != internal.ThreadID {
		t.Fatalf("internal response thread = %+v", internal.Response)
	}
	if internal.Response.Thread.SessionID != parent.SessionID || internal.Response.Thread.ParentThreadID == nil || *internal.Response.Thread.ParentThreadID != parent.ID {
		t.Fatalf("response thread lineage = %+v", internal.Response.Thread)
	}
	if internal.Response.Thread.ForkedFromID != nil {
		t.Fatalf("response thread forked_from = %v, want nil", internal.Response.Thread.ForkedFromID)
	}

	loaded := router.Handle(requestWithParams(t, IntID(2), MethodThreadLoadedList, ThreadLoadedListParams{}))
	if loaded.Error != nil {
		t.Fatalf("loaded list error: %+v", loaded.Error)
	}
	loadedIDs := loaded.Result.(*ThreadLoadedListResponse).Data
	if !containsString(loadedIDs, parent.ID) {
		t.Fatalf("loaded list %v missing parent %s", loadedIDs, parent.ID)
	}
	if containsString(loadedIDs, internal.ThreadID) {
		t.Fatalf("loaded list %v exposes internal thread %s", loadedIDs, internal.ThreadID)
	}
	read := router.Handle(requestWithParams(t, IntID(3), MethodThreadRead, ThreadReadParams{ThreadID: internal.ThreadID}))
	if read.Error == nil || read.Error.Message != "thread not found: "+internal.ThreadID {
		t.Fatalf("thread/read internal error = %+v", read.Error)
	}
	for offset, method := range []Method{
		MethodThreadTurnsList,
		MethodThreadItemsList,
		MethodThreadItemsRead,
		MethodThreadRevert,
		MethodThreadMetadataUpdate,
		MethodThreadSettingsUpdate,
		MethodTurnStart,
	} {
		response := router.Handle(requestWithParams(t, IntID(int64(10+offset)), method, map[string]string{"threadId": internal.ThreadID}))
		if response.Error == nil || response.Error.Message != "thread not found: "+internal.ThreadID {
			t.Fatalf("%s internal error = %+v", method, response.Error)
		}
	}

	hostReadRequest := requestWithParams(t, IntID(20), MethodThreadRead, ThreadReadParams{ThreadID: internal.ThreadID})
	hostReadRequest.Internal = true
	hostRead := router.Handle(hostReadRequest)
	if hostRead.Error != nil {
		t.Fatalf("host thread/read internal error = %+v", hostRead.Error)
	}
	if thread := hostRead.Result.(*ThreadReadResponse).Thread; thread == nil || thread.ID != internal.ThreadID {
		t.Fatalf("host thread/read internal thread = %+v", thread)
	}

	list := router.Handle(requestWithParams(t, IntID(21), MethodThreadList, ThreadListParams{}))
	if list.Error != nil {
		t.Fatalf("thread/list error = %+v", list.Error)
	}
	for _, thread := range list.Result.(*ThreadListResponse).Data {
		if thread.ID == internal.ThreadID {
			t.Fatalf("thread/list exposes internal thread %s", internal.ThreadID)
		}
	}

	if got := router.responsesPromptCacheKey(internal.ThreadID, responsesMetadataLineage{}, true); got != "guardian:"+parent.ID {
		t.Fatalf("internal prompt cache key = %q, want guardian:%s", got, parent.ID)
	}
	if got := router.runtimeMultiAgentVersionForTurn(internal.ThreadID, &config.Config{Values: map[string]any{
		"features": map[string]any{"multi_agent_v2": true},
	}}, nil); got != "" {
		t.Fatalf("internal multi-agent version = %q, want disabled", got)
	}
	if err := router.CloseInternalSession(internal.ThreadID); err != nil {
		t.Fatalf("close internal session: %v", err)
	}
	afterClose := router.Handle(requestWithParams(t, IntID(22), MethodThreadRead, ThreadReadParams{ThreadID: internal.ThreadID}))
	if afterClose.Error == nil {
		t.Fatalf("thread/read closed internal error = %+v", afterClose.Error)
	}
}

// TestInternalSessionThreadReadyLifecycleLikeRust covers the host-owned
// ThreadReadyInput payload and the Guardian parent context recorded for regular
// threads.
func TestInternalSessionThreadReadyLifecycleLikeRust(t *testing.T) {
	router := newInternalSessionTestRouter(t)
	var mu sync.Mutex
	var inputs []ThreadReadyInput
	router.RegisterThreadReadyHandler(func(input ThreadReadyInput) {
		mu.Lock()
		inputs = append(inputs, input)
		mu.Unlock()
	})

	parent := startInternalSessionParent(t, router, 1, "gpt-ready")
	mu.Lock()
	parentInputs := append([]ThreadReadyInput(nil), inputs...)
	mu.Unlock()
	if len(parentInputs) != 1 {
		t.Fatalf("thread-ready calls = %d, want 1", len(parentInputs))
	}
	parentInput := parentInputs[0]
	if parentInput.ThreadID != parent.ID || parentInput.ParentThreadID != "" {
		t.Fatalf("parent thread-ready input = %+v", parentInput)
	}
	if parentInput.Config == nil || threadReadyModel(nil, parentInput.Config) != "gpt-ready" {
		t.Fatalf("parent thread-ready config = %+v", parentInput.Config)
	}
	if parentInput.SessionStore == nil || parentInput.ThreadStore == nil {
		t.Fatalf("thread-ready stores = %+v", parentInput)
	}
	context, ok := router.GuardianThreadContext(parent.ID)
	if !ok || context.ParentThreadID != parent.ID || context.ParentModel != "gpt-ready" {
		t.Fatalf("guardian thread context = %+v, ok=%v", context, ok)
	}

	if _, err := router.SpawnInternalSession(parent.ID, InternalSessionOptions{
		Source: InternalSessionSourceGuardian,
		Model:  "gpt-ready",
	}); err != nil {
		t.Fatalf("spawn internal session: %v", err)
	}
	mu.Lock()
	allInputs := append([]ThreadReadyInput(nil), inputs...)
	mu.Unlock()
	if len(allInputs) != 2 {
		t.Fatalf("thread-ready calls = %d, want 2", len(allInputs))
	}
	internalInput := allInputs[1]
	if internalInput.SessionSource != string(internalGuardianSessionSource) || internalInput.ParentThreadID != parent.ID {
		t.Fatalf("internal thread-ready input = %+v", internalInput)
	}
	if internalInput.SessionStore != parentInput.SessionStore {
		t.Fatal("internal session did not reuse the parent session store")
	}
	if _, ok := router.GuardianThreadContext(internalInput.ThreadID); ok {
		t.Fatal("internal thread received a Guardian thread context")
	}
}

func TestSpawnInternalSessionRejectsInvalidRequestsLikeRust(t *testing.T) {
	router := newInternalSessionTestRouter(t)
	parent := startInternalSessionParent(t, router, 1, "gpt-test")

	if _, err := router.SpawnInternalSession("", InternalSessionOptions{Source: InternalSessionSourceGuardian}); err == nil {
		t.Fatal("spawn without a parent thread succeeded")
	}
	if _, err := router.SpawnInternalSession(parent.ID, InternalSessionOptions{Source: "subagent_guardian"}); err == nil || !strings.Contains(err.Error(), "internal session source") {
		t.Fatalf("non-internal source error = %v", err)
	}
	if _, err := router.SpawnInternalSession("thread-missing", InternalSessionOptions{Source: InternalSessionSourceGuardian}); err == nil || err.Error() != "thread not found: thread-missing" {
		t.Fatalf("missing parent error = %v", err)
	}
	internal, err := router.SpawnInternalSession(parent.ID, InternalSessionOptions{Source: InternalSessionSourceGuardian})
	if err != nil {
		t.Fatalf("spawn internal session: %v", err)
	}
	if _, err := router.SpawnInternalSession(internal.ThreadID, InternalSessionOptions{Source: InternalSessionSourceGuardian}); err == nil || !strings.Contains(err.Error(), "thread not found") {
		t.Fatalf("internal parent error = %v", err)
	}
}

func TestForkInternalSessionKeepsExplicitHistoryLikeRust(t *testing.T) {
	router := newInternalSessionTestRouter(t)
	parent := startInternalSessionParent(t, router, 1, "gpt-test")
	internal, err := router.ForkInternalSession(parent.ID, InternalSessionOptions{
		Source: InternalSessionSourceGuardian,
		History: []session.Item{{
			ID:        "committed-item",
			Type:      "message",
			Role:      "user",
			Text:      "explicit committed history",
			CreatedAt: time.Now().UTC(),
		}},
	})
	if err != nil {
		t.Fatalf("fork internal session: %v", err)
	}
	record, err := router.threadRecord(session.ThreadID(internal.ThreadID), true, true)
	if err != nil || record == nil {
		t.Fatalf("forked record: %v", err)
	}
	if len(record.Items) != 1 || record.Items[0].Text != "explicit committed history" {
		t.Fatalf("forked history = %#v", record.Items)
	}
	if record.ForkedFromID != "" || record.ParentThreadID != session.ThreadID(parent.ID) {
		t.Fatalf("forked lineage parent=%q forked_from=%q", record.ParentThreadID, record.ForkedFromID)
	}
}

// TestInternalSessionSourceWireLikeRust keeps the app-server schema form for
// the guardian internal source aligned with Rust's externally tagged enum.
func TestInternalSessionSourceWireLikeRust(t *testing.T) {
	encoded, err := json.Marshal(SessionSource(internalGuardianSessionSource))
	if err != nil {
		t.Fatalf("marshal session source: %v", err)
	}
	if string(encoded) != `{"internal":"guardian"}` {
		t.Fatalf("session source wire = %s", encoded)
	}
	var decoded SessionSource
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal session source: %v", err)
	}
	if string(decoded) != internalGuardianSessionSource {
		t.Fatalf("decoded session source = %q, want %q", decoded, internalGuardianSessionSource)
	}
}

// TestInternalSessionTurnUsesParentSessionIdentityLikeRust pins the Responses
// identity split for host-owned internal sessions: the session-id header comes
// from the reused parent session, thread-id stays the internal thread, and the
// prompt cache key is scoped to `guardian:<parent thread>` (Rust #39994).
func TestInternalSessionTurnUsesParentSessionIdentityLikeRust(t *testing.T) {
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		DefaultCWD:   t.TempDir(),
	})
	t.Cleanup(func() { _ = router.Close() })

	parent := startInternalSessionParent(t, router, 1, "gpt-test")
	internal, err := router.SpawnInternalSession(parent.ID, InternalSessionOptions{
		Source: InternalSessionSourceGuardian,
		Model:  "gpt-test",
	})
	if err != nil {
		t.Fatalf("spawn internal session: %v", err)
	}

	start := requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: internal.ThreadID,
		Prompt:   "review the parent turn",
	})
	start.Internal = true
	response := router.Handle(start)
	if response.Error != nil {
		t.Fatalf("internal turn start error: %+v", response.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if got := request.SessionID; got != parent.SessionID {
		t.Fatalf("model request session id = %q, want parent session id %q", got, parent.SessionID)
	}
	if got := request.ThreadID; got != internal.ThreadID {
		t.Fatalf("model request thread id = %q, want internal thread id %q", got, internal.ThreadID)
	}
	if got, want := request.PromptCacheKey, "guardian:"+parent.ID; got != want {
		t.Fatalf("model request prompt cache key = %q, want %q", got, want)
	}
}
