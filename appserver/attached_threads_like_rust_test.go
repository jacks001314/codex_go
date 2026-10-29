package appserver

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
)

type attachedThreadHarness struct {
	home         string
	store        *session.Store
	threadRouter *Router
	state        *state.StateRuntime
	router       *RuntimeRouter
	sink         *NotificationBuffer
}

func newAttachedThreadHarness(t *testing.T) *attachedThreadHarness {
	t.Helper()
	h := &attachedThreadHarness{home: t.TempDir()}
	h.open(t)
	t.Cleanup(func() { h.close() })
	return h
}

func (h *attachedThreadHarness) open(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	sqliteConfig, err := state.NewSqliteConfig(h.home)
	if err != nil {
		t.Fatalf("NewSqliteConfig() error = %v", err)
	}
	// Start the way the app server does: a damaged state database is preserved
	// and rebuilt from rollouts instead of aborting startup.
	runtime, _, err := initStateRuntimeWithFreshStartOnCorruption(ctx, sqliteConfig, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v", err)
	}
	h.state = runtime
	h.store = session.NewStore(h.home)
	h.threadRouter = NewRouter(h.store)
	h.threadRouter.SetStateRuntime(runtime)
	h.router = NewRuntimeRouter(RuntimeServices{ThreadRouter: h.threadRouter, StateRuntime: runtime})
	h.sink = NewNotificationBuffer()
	h.router.SetNotificationSink(h.sink)
}

func (h *attachedThreadHarness) close() {
	if h == nil {
		return
	}
	if h.router != nil {
		_ = h.router.Close()
		h.router = nil
	}
	if h.state != nil {
		_ = h.state.Close()
		h.state = nil
	}
}

func (h *attachedThreadHarness) createPaginatedThread(t *testing.T, id string, cwd string) *session.Record {
	t.Helper()
	now := time.Now().UTC()
	path := rollout.PathForThread(h.home, id, now)
	record := &session.Record{
		ID:        session.ThreadID(id),
		SessionID: id,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           cwd,
			HistoryMode:   string(ThreadHistoryPaginated),
			ModelProvider: "openai",
			Source:        string(SessionSourceVsCode),
			Extra:         map[string]any{"rollout_path": path},
		},
	}
	if err := h.store.Create(record); err != nil {
		t.Fatalf("store.Create(%s) error = %v", id, err)
	}
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     h.home,
		ThreadID:      id,
		Source:        string(SessionSourceVsCode),
		CWD:           cwd,
		ModelProvider: "openai",
		HistoryMode:   string(ThreadHistoryPaginated),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("rollout.NewRecorder(%s) error = %v", id, err)
	}
	payload, err := json.Marshal(map[string]any{"type": "user_message", "message": "preview-" + id})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(time.Second).Format(time.RFC3339Nano), Payload: payload}); err != nil {
		t.Fatalf("AppendLine(%s) error = %v", id, err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close(%s) error = %v", id, err)
	}
	if recorder.Path() != path {
		t.Fatalf("rollout path = %s, want %s", recorder.Path(), path)
	}
	if err := h.state.ReconcileRollout(context.Background(), path, false); err != nil {
		t.Fatalf("ReconcileRollout(%s) error = %v", id, err)
	}
	return record
}

func (h *attachedThreadHarness) listAttached(t *testing.T, parentID string, limit int) []Thread {
	t.Helper()
	params := ThreadListParams{AttachedToThreadID: &parentID, UseStateDBOnly: true}
	if limit > 0 {
		params.Limit = &limit
	}
	var found []Thread
	for page := 0; page < 20; page++ {
		response := h.router.Handle(requestWithParams(t, IntID(int64(1000+page)), MethodThreadList, params))
		if response.Error != nil {
			t.Fatalf("thread/list(attachedToThreadId=%s) error = %+v", parentID, response.Error)
		}
		list := response.Result.(*ThreadListResponse)
		found = append(found, list.Data...)
		if list.NextCursor == nil {
			return found
		}
		params.Cursor = list.NextCursor
	}
	t.Fatalf("attached thread pagination did not terminate")
	return nil
}

func attachedThreadIDs(threads []Thread) []string {
	ids := make([]string, 0, len(threads))
	for _, thread := range threads {
		ids = append(ids, thread.ID)
	}
	return ids
}

func sortedAttachedThreadIDs(threads []Thread) string {
	ids := attachedThreadIDs(threads)
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func sortedIDs(ids []string) string {
	copied := append([]string(nil), ids...)
	sort.Strings(copied)
	return strings.Join(copied, ",")
}

// TestAttachedThreadForkDiscoveryRestartRecoveryAndDeleteLikeRust mirrors Rust
// #56351: an attached fork is persisted without thread/started, hidden from
// ordinary listings, discoverable per parent (including empty threads and
// pagination), survives restart and a missing state database, and keeps its
// source undeletable until the attached threads are deleted.
func TestAttachedThreadForkDiscoveryRestartRecoveryAndDeleteLikeRust(t *testing.T) {
	h := newAttachedThreadHarness(t)
	parent := h.createPaginatedThread(t, "0198f001-0000-7000-8000-000000000001", t.TempDir())
	other := h.createPaginatedThread(t, "0198f001-0000-7000-8000-000000000002", t.TempDir())

	children := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		response := h.router.Handle(requestWithParams(t, IntID(int64(i+1)), MethodThreadFork, ThreadForkParams{
			ThreadID:       string(parent.ID),
			AttachToSource: true,
			ExcludeTurns:   true,
		}))
		if response.Error != nil {
			t.Fatalf("attached thread/fork error = %+v", response.Error)
		}
		thread := response.Result.(*ThreadForkResponse).Thread
		if thread == nil || thread.Ephemeral {
			t.Fatalf("attached fork thread = %+v, want persistent thread", thread)
		}
		if thread.ForkedFromID == nil || *thread.ForkedFromID != string(parent.ID) {
			t.Fatalf("attached fork forkedFromId = %v, want %s", thread.ForkedFromID, parent.ID)
		}
		children = append(children, thread.ID)
	}
	if sinkHasMethod(h.sink, NotificationThreadStarted) {
		t.Fatalf("attached forks must not emit thread/started: %+v", h.sink.List())
	}

	ordinary := h.router.Handle(requestWithParams(t, IntID(10), MethodThreadList, ThreadListParams{
		UseStateDBOnly: true,
		Limit:          intPointer(2),
	}))
	if ordinary.Error != nil {
		t.Fatalf("thread/list error = %+v", ordinary.Error)
	}
	if got := attachedThreadIDs(ordinary.Result.(*ThreadListResponse).Data); sortedIDs(got) != sortedIDs([]string{string(other.ID), string(parent.ID)}) {
		t.Fatalf("ordinary listing = %v, want [%s %s]", got, other.ID, parent.ID)
	}
	// Raw-scan and database listings must refill pages while newer attached
	// threads are filtered out.
	for _, stateOnly := range []bool{false, true} {
		searched := h.router.Handle(requestWithParams(t, IntID(12), MethodThreadList, ThreadListParams{
			SearchTerm:     stringPointer("preview-" + string(parent.ID)),
			UseStateDBOnly: stateOnly,
			Limit:          intPointer(1),
		}))
		if searched.Error != nil {
			t.Fatalf("search listing (stateOnly=%v) error = %+v", stateOnly, searched.Error)
		}
		if got := attachedThreadIDs(searched.Result.(*ThreadListResponse).Data); sortedIDs(got) != sortedIDs([]string{string(parent.ID)}) {
			t.Fatalf("search listing (stateOnly=%v) = %v, want [%s]", stateOnly, got, parent.ID)
		}
	}
	ascending := h.router.Handle(requestWithParams(t, IntID(13), MethodThreadList, ThreadListParams{
		UseStateDBOnly: true,
		SortDirection:  SortAsc,
		Limit:          intPointer(2),
	}))
	if ascending.Error != nil {
		t.Fatalf("ascending listing error = %+v", ascending.Error)
	}
	if got := attachedThreadIDs(ascending.Result.(*ThreadListResponse).Data); sortedIDs(got) != sortedIDs([]string{string(other.ID), string(parent.ID)}) {
		t.Fatalf("ascending listing = %v, want [%s %s]", got, parent.ID, other.ID)
	}
	// The attached fork records its attachment and inherits the parent history
	// through the paginated history base, which is what a later resume reads.
	childMeta, err := rollout.FirstSessionMeta(h.threadRouter.threadRolloutPath(&session.Record{ID: session.ThreadID(children[0])}))
	if err != nil {
		t.Fatalf("FirstSessionMeta(%s) error = %v", children[0], err)
	}
	if !childMeta.AttachedThread || childMeta.ForkedFromID != string(parent.ID) {
		t.Fatalf("attached fork meta = %+v, want attached to %s", childMeta, parent.ID)
	}
	if childMeta.HistoryBase == nil || childMeta.HistoryBase.ThreadID != string(parent.ID) || childMeta.HistoryBase.EndOrdinalExclusive == 0 {
		t.Fatalf("attached fork history base = %+v, want parent history", childMeta.HistoryBase)
	}
	resolvedChild, err := rollout.RecordFromPathResolved(h.home, h.threadRouter.threadRolloutPath(&session.Record{ID: session.ThreadID(children[0])}), false)
	if err != nil {
		t.Fatalf("RecordFromPathResolved(%s) error = %v", children[0], err)
	}
	if resolvedChild.InheritedItems == 0 {
		t.Fatalf("attached fork inherited no items from its parent history")
	}
	encodedItems, err := json.Marshal(resolvedChild.Items)
	if err != nil {
		t.Fatalf("json.Marshal(items) error = %v", err)
	}
	if !strings.Contains(string(encodedItems), "preview-"+string(parent.ID)) {
		t.Fatalf("attached fork history = %s, want the parent user message", encodedItems)
	}
	spawnChildren := h.router.Handle(requestWithParams(t, IntID(11), MethodThreadList, ThreadListParams{
		ParentThreadID: stringPointer(string(parent.ID)),
		UseStateDBOnly: true,
	}))
	if spawnChildren.Error != nil {
		t.Fatalf("parentThreadId listing error = %+v", spawnChildren.Error)
	}
	if got := spawnChildren.Result.(*ThreadListResponse).Data; len(got) != 0 {
		t.Fatalf("attached forks leaked into spawn-children listing: %v", attachedThreadIDs(got))
	}
	if got := attachedThreadIDs(h.listAttached(t, string(parent.ID), 1)); sortedIDs(got) != sortedIDs(children) {
		t.Fatalf("attached listing = %v, want %v", got, children)
	}
	if got := h.listAttached(t, string(other.ID), 0); len(got) != 0 {
		t.Fatalf("unrelated attached listing = %v, want empty", attachedThreadIDs(got))
	}

	// Restart keeps the persisted attachment even though no child took a turn.
	h.close()
	h.open(t)
	if got := attachedThreadIDs(h.listAttached(t, string(parent.ID), 0)); sortedIDs(got) != sortedIDs(children) {
		t.Fatalf("attached listing after restart = %v, want %v", got, children)
	}

	// A missing state database is rebuilt from rollouts, including attachment.
	h.close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(h.stateDBPath(t) + suffix)
	}
	h.open(t)
	if err := state.WaitForRolloutBackfill(context.Background(), h.state, h.home, state.RolloutBackfillOptions{}); err != nil {
		t.Fatalf("WaitForRolloutBackfill() error = %v", err)
	}
	if got := attachedThreadIDs(h.listAttached(t, string(parent.ID), 0)); sortedIDs(got) != sortedIDs(children) {
		t.Fatalf("attached listing after database recovery = %v, want %v", got, children)
	}
	h.close()
	if err := os.WriteFile(h.stateDBPath(t), []byte("damaged state database"), 0o600); err != nil {
		t.Fatalf("corrupt state database error = %v", err)
	}
	h.open(t)
	if err := state.WaitForRolloutBackfill(context.Background(), h.state, h.home, state.RolloutBackfillOptions{}); err != nil {
		t.Fatalf("WaitForRolloutBackfill() after corruption error = %v", err)
	}
	if got := attachedThreadIDs(h.listAttached(t, string(parent.ID), 0)); sortedIDs(got) != sortedIDs(children) {
		t.Fatalf("attached listing after corrupt database recovery = %v, want %v", got, children)
	}

	// Resume preserves the attachment, and an ordinary fork of an attached
	// thread never inherits it.
	resume := h.router.Handle(requestWithParams(t, IntID(20), MethodThreadResume, ThreadResumeParams{ThreadID: children[0]}))
	if resume.Error != nil {
		t.Fatalf("thread/resume error = %+v", resume.Error)
	}
	if resumed := resume.Result.(*ThreadResumeResponse).Thread; len(resumed.Turns) != 0 {
		t.Fatalf("resumed attached thread turns = %d, want none before a turn runs", len(resumed.Turns))
	}
	if got := attachedThreadIDs(h.listAttached(t, string(parent.ID), 0)); sortedIDs(got) != sortedIDs(children) {
		t.Fatalf("attached listing after resume = %v, want %v", got, children)
	}
	independent := h.router.Handle(requestWithParams(t, IntID(21), MethodThreadFork, ThreadForkParams{ThreadID: children[0]}))
	if independent.Error != nil {
		t.Fatalf("ordinary thread/fork error = %+v", independent.Error)
	}
	independentID := independent.Result.(*ThreadForkResponse).Thread.ID
	if got := h.listAttached(t, children[0], 0); len(got) != 0 {
		t.Fatalf("ordinary fork inherited attachment: %v", attachedThreadIDs(got))
	}
	if deleted := h.router.Handle(requestWithParams(t, IntID(41), MethodThreadDelete, ThreadDeleteParams{ThreadID: independentID})); deleted.Error != nil {
		t.Fatalf("independent fork delete error = %+v", deleted.Error)
	}

	deleteParent := h.router.Handle(requestWithParams(t, IntID(30), MethodThreadDelete, ThreadDeleteParams{ThreadID: string(parent.ID)}))
	if deleteParent.Error == nil || !strings.Contains(deleteParent.Error.Message, "delete the attached thread first") {
		t.Fatalf("parent delete error = %+v, want attached-thread guard", deleteParent.Error)
	}
	if _, err := h.store.Read(parent.ID, true, false); err != nil {
		t.Fatalf("rejected delete removed parent: %v", err)
	}
	for i, child := range children {
		deleted := h.router.Handle(requestWithParams(t, IntID(int64(31+i)), MethodThreadDelete, ThreadDeleteParams{ThreadID: child}))
		if deleted.Error != nil {
			t.Fatalf("delete attached thread %s error = %+v", child, deleted.Error)
		}
	}
	if deleted := h.router.Handle(requestWithParams(t, IntID(40), MethodThreadDelete, ThreadDeleteParams{ThreadID: string(parent.ID)})); deleted.Error != nil {
		t.Fatalf("parent delete after children error = %+v", deleted.Error)
	}
}

func (h *attachedThreadHarness) stateDBPath(t *testing.T) string {
	t.Helper()
	sqliteConfig, err := state.NewSqliteConfig(h.home)
	if err != nil {
		t.Fatalf("NewSqliteConfig() error = %v", err)
	}
	return sqliteConfig.StateDBPath()
}

func TestAttachedThreadForkRejectsConflictingOptionsLikeRust(t *testing.T) {
	h := newAttachedThreadHarness(t)
	paginated := h.createPaginatedThread(t, "0198f001-0000-7000-8000-000000000004", t.TempDir())
	legacy := &session.Record{
		ID:        "0198f001-0000-7000-8000-000000000003",
		SessionID: "0198f001-0000-7000-8000-000000000003",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Metadata: session.Metadata{
			CWD:           h.home,
			HistoryMode:   string(ThreadHistoryLegacy),
			ModelProvider: "openai",
			Source:        string(SessionSourceVsCode),
		},
	}
	if err := h.store.Create(legacy); err != nil {
		t.Fatalf("store.Create(legacy) error = %v", err)
	}
	if err := h.threadRouter.createThreadRollout(legacy, legacy.CreatedAt); err != nil {
		t.Fatalf("createThreadRollout(legacy) error = %v", err)
	}

	cases := []struct {
		name    string
		method  Method
		params  any
		message string
	}{
		{
			name:    "legacy parent",
			method:  MethodThreadFork,
			params:  ThreadForkParams{ThreadID: string(legacy.ID), AttachToSource: true},
			message: "attachToSource requires a paginated parent",
		},
		{
			name:    "ephemeral",
			method:  MethodThreadFork,
			params:  ThreadForkParams{ThreadID: string(paginated.ID), AttachToSource: true, Ephemeral: true},
			message: "attachToSource cannot be combined",
		},
		{
			name:    "defer goal",
			method:  MethodThreadFork,
			params:  ThreadForkParams{ThreadID: string(paginated.ID), AttachToSource: true, DeferGoalContinuation: true},
			message: "attachToSource cannot be combined",
		},
		{
			name:    "parent filter",
			method:  MethodThreadList,
			params:  ThreadListParams{AttachedToThreadID: stringPointer(string(paginated.ID)), ParentThreadID: stringPointer(string(paginated.ID))},
			message: "attachedToThreadId cannot be combined",
		},
		{
			name:    "ancestor filter",
			method:  MethodThreadList,
			params:  ThreadListParams{AttachedToThreadID: stringPointer(string(paginated.ID)), AncestorThreadID: stringPointer(string(paginated.ID))},
			message: "attachedToThreadId cannot be combined",
		},
	}
	for i, testCase := range cases {
		response := h.router.Handle(requestWithParams(t, IntID(int64(100+i)), testCase.method, testCase.params))
		if response.Error == nil || !strings.Contains(response.Error.Message, testCase.message) {
			t.Fatalf("%s error = %+v, want %q", testCase.name, response.Error, testCase.message)
		}
	}
	if got := h.listAttached(t, string(paginated.ID), 0); len(got) != 0 {
		t.Fatalf("failed forks left attached threads behind: %v", attachedThreadIDs(got))
	}
}

func TestAttachedThreadDiscoveryRequiresStateDatabaseLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter})
	t.Cleanup(func() { _ = router.Close() })
	parent := "0198f001-0000-7000-8000-000000000005"
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{AttachedToThreadID: &parent}))
	if response.Error == nil || response.Error.Code != JSONRPCMethodNotFoundErrorCode || !strings.Contains(response.Error.Message, "attached threads is not supported yet") {
		t.Fatalf("attached listing without state database error = %+v", response.Error)
	}
}

func intPointer(value int) *int { return &value }
