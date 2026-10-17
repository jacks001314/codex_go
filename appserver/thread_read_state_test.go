package appserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
)

// TestThreadReadStateSnapshotsAndRevisionConflictsLikeRust covers the
// experimental snapshots on thread/read and thread/list plus explicit
// read/unread edits with stale-revision conflicts (#52350/#52395).
func TestThreadReadStateSnapshotsAndRevisionConflictsLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateRuntime.Close() })
	const threadID = "0192f0aa-0000-7000-8000-000000000123"
	path := writeMemoryStartupTestRollout(t, home, threadID, time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	if err := stateRuntime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := stateRuntime.StateDB().ExecContext(ctx, `UPDATE threads SET preview = 'read state preview' WHERE id = ?`, threadID); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(session.NewStore(home))
	router.SetStateRuntime(stateRuntime)

	readResponse := router.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{ThreadID: threadID}))
	if readResponse.Error != nil {
		t.Fatalf("thread/read error: %+v", readResponse.Error)
	}
	read := readResponse.Result.(*ThreadReadResponse)
	if read.ReadState == nil || read.ReadState.Revision != "initial:"+threadID || read.ReadState.FirstUnread != nil {
		t.Fatalf("initial read state = %#v", read.ReadState)
	}
	initialRevision := read.ReadState.Revision

	markUnread := func(id RequestID, expected string) *Response {
		return router.Handle(requestWithParams(t, id, MethodThreadReadStateUpdate, ThreadReadStateUpdateParams{
			ThreadID: threadID, ExpectedRevision: expected,
			Operation: ThreadReadStateOperation{Type: "unread"},
		}))
	}
	markedResponse := markUnread(IntID(2), initialRevision)
	if markedResponse.Error != nil {
		t.Fatalf("mark unread error: %+v", markedResponse.Error)
	}
	marked := markedResponse.Result.(*ThreadReadStateUpdateResponse)
	if marked.ReadState.FirstUnread == nil || marked.ReadState.FirstUnread.Type != "threadStart" {
		t.Fatalf("mark unread state = %#v", marked.ReadState)
	}

	stale := router.Handle(requestWithParams(t, IntID(3), MethodThreadReadStateUpdate, ThreadReadStateUpdateParams{
		ThreadID: threadID, ExpectedRevision: initialRevision,
		Operation: ThreadReadStateOperation{Type: "read"},
	}))
	if stale.Error == nil || stale.Error.Code != -32600 || stale.Error.Data["reason"] != "readStateConflict" {
		t.Fatalf("stale edit response = %+v", stale)
	}
	conflictState, ok := stale.Error.Data["readState"].(ThreadReadState)
	if !ok || conflictState.Revision != marked.ReadState.Revision {
		t.Fatalf("conflict read state = %#v", stale.Error.Data["readState"])
	}

	readResponse = router.Handle(requestWithParams(t, IntID(4), MethodThreadRead, ThreadReadParams{ThreadID: threadID}))
	if readResponse.Error != nil {
		t.Fatalf("thread/read after mark error: %+v", readResponse.Error)
	}
	read = readResponse.Result.(*ThreadReadResponse)
	if read.ReadState == nil || read.ReadState.Revision != marked.ReadState.Revision ||
		read.ReadState.FirstUnread == nil || read.ReadState.FirstUnread.Type != "threadStart" {
		t.Fatalf("thread/read state after mark = %#v", read.ReadState)
	}

	listResponse := router.Handle(requestWithParams(t, IntID(5), MethodThreadList, ThreadListParams{}))
	if listResponse.Error != nil {
		t.Fatalf("thread/list error: %+v", listResponse.Error)
	}
	list := listResponse.Result.(*ThreadListResponse)
	listed, ok := list.ReadStates[threadID]
	if !ok || listed.Revision != marked.ReadState.Revision {
		t.Fatalf("thread/list readStates = %#v", list.ReadStates)
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		ReadStates map[string]ThreadReadState `json:"readStates"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ReadStates[threadID].FirstUnread == nil || payload.ReadStates[threadID].FirstUnread.Type != "threadStart" {
		t.Fatalf("serialized readStates = %#v", payload.ReadStates)
	}
}

func TestThreadReadStateEligibilityLikeRust(t *testing.T) {
	user := ThreadSourceUser
	for _, testCase := range []struct {
		name   string
		thread *Thread
		want   bool
	}{
		{name: "user", thread: &Thread{Source: SessionSourceCli, ThreadSource: &user}, want: true},
		{name: "no thread source", thread: &Thread{Source: SessionSourceCli}, want: true},
		{name: "ephemeral", thread: &Thread{Ephemeral: true, Source: SessionSourceCli}, want: false},
		{name: "unknown source", thread: &Thread{Source: SessionSourceUnknown}, want: false},
		{name: "subagent", thread: &Thread{Source: SessionSource(session.GuardianReviewSource)}, want: false},
		{name: "non-user thread source", thread: &Thread{Source: SessionSourceCli, ThreadSource: threadSourcePtr(string(ThreadSourceSubagent))}, want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := threadReadStateEligible(testCase.thread); got != testCase.want {
				t.Fatalf("eligible = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestThreadReadStateChangedNotifiesThreadSubscribersLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	router.subscribeThreadConnection("thread-read", "conn-read")
	state := ThreadReadState{
		FirstUnread: &ThreadUnreadPosition{Type: "turn", TurnID: "turn-1"},
		Revision:    "revision-1",
	}
	router.notifyThreadReadStateChanged("thread-read", state)
	notifications := sink.List()
	if len(notifications) != 1 || notifications[0].Method != NotificationThreadReadStateChanged {
		t.Fatalf("notifications = %#v", notifications)
	}
	params, ok := notifications[0].Params.(*ThreadReadStateChangedNotification)
	if !ok || params.ThreadID != "thread-read" || params.ReadState.Revision != "revision-1" ||
		params.ReadState.FirstUnread == nil || params.ReadState.FirstUnread.TurnID != "turn-1" {
		t.Fatalf("notification params = %#v", notifications[0].Params)
	}
}

func TestTerminalAttentionPublishesBeforeCompletionLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateRuntime.Close() })
	const threadID = "0192f0aa-0000-7000-8000-000000000124"
	path := writeMemoryStartupTestRollout(t, home, threadID, time.Date(2026, 10, 18, 13, 0, 0, 0, time.UTC))
	if err := stateRuntime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	coreRouter := NewRouter(session.NewStore(home))
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: coreRouter, StateRuntime: stateRuntime})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	router.subscribeThreadConnection(threadID, "conn-read")

	completed := &TurnCompletedNotification{
		ThreadID: threadID,
		Turn: Turn{
			ID:     "turn-visible",
			Status: TurnStatusCompleted,
			Items:  []ThreadItem{{Type: "agentMessage", Text: "Done"}},
		},
	}
	router.publishThreadReadAttention(completed)
	read, err := stateRuntime.ThreadReadStates(ctx, []string{threadID})
	if err != nil {
		t.Fatal(err)
	}
	if read[threadID].FirstUnreadTurn == nil || *read[threadID].FirstUnreadTurn != "turn-visible" {
		t.Fatalf("published state = %#v", read)
	}
	notifications := sink.List()
	if len(notifications) != 1 || notifications[0].Method != NotificationThreadReadStateChanged {
		t.Fatalf("notifications = %#v", notifications)
	}
	router.publishThreadReadAttention(completed)
	if got := len(sink.List()); got != 1 {
		t.Fatalf("duplicate terminal publication produced %d notifications", got)
	}

	router.publishThreadReadAttention(&TurnCompletedNotification{
		ThreadID: threadID,
		Turn: Turn{
			ID:     "turn-suppressed",
			Status: TurnStatusCompleted,
			Items:  []ThreadItem{{Type: "agentMessage", Text: "<decision>DONT_NOTIFY</decision>"}},
		},
	})
	if got := mustAPIThreadReadState(t, ctx, stateRuntime, threadID); got.FirstUnread == nil || got.FirstUnread.TurnID != "turn-visible" {
		t.Fatalf("DONT_NOTIFY changed read state: %#v", got)
	}
	if _, err := stateRuntime.ReplaceThreadGoal(ctx, threadID, "finish the task", state.ThreadGoalActive, nil); err != nil {
		t.Fatal(err)
	}
	router.publishThreadReadAttention(&TurnCompletedNotification{
		ThreadID: threadID,
		Turn: Turn{
			ID:     "turn-active-goal",
			Status: TurnStatusCompleted,
			Items:  []ThreadItem{{Type: "agentMessage", Text: "Intermediate"}},
		},
	})
	if got := mustAPIThreadReadState(t, ctx, stateRuntime, threadID); got.FirstUnread == nil || got.FirstUnread.TurnID != "turn-visible" {
		t.Fatalf("active-goal completion changed read state: %#v", got)
	}
}

func mustAPIThreadReadState(t *testing.T, ctx context.Context, runtime *state.StateRuntime, threadID string) ThreadReadState {
	t.Helper()
	states, err := runtime.ThreadReadStates(ctx, []string{threadID})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := states[threadID]
	if !ok {
		t.Fatalf("missing state for %s", threadID)
	}
	return apiThreadReadState(value)
}

func TestRevertRetiresOnlyRemovedUnreadTurnLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateRuntime.Close() })
	const threadID = "0192f0aa-0000-7000-8000-000000000125"
	path := writeMemoryStartupTestRollout(t, home, threadID, time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC))
	if err := stateRuntime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(home)
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		Metadata: session.Metadata{
			Source:       "cli",
			ThreadSource: "user",
			HistoryMode:  "paginated",
			RolloutTurns: []session.TurnSnapshot{{ID: "turn-1"}, {ID: "turn-2"}, {ID: "turn-3"}},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	coreRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: coreRouter, StateRuntime: stateRuntime})

	if changed, err := stateRuntime.PublishThreadAttention(ctx, threadID, "turn-2"); err != nil || !changed {
		t.Fatalf("publish turn-2 = %v, %v", changed, err)
	}
	before := mustThreadReadStateForApp(t, ctx, stateRuntime, threadID)
	if got := router.threadReadStateRevisionToRetire(threadID, "turn-2"); got != before.Revision {
		t.Fatalf("revision to retire = %q, want %q", got, before.Revision)
	}
	if got := router.threadReadStateRevisionToRetire(threadID, "turn-3"); got != "" {
		t.Fatalf("unread turn before revert boundary returned %q", got)
	}
	if _, err := stateRuntime.UpdateThreadReadState(ctx, threadID, before.Revision, state.ReadStateOperationUnread); err != nil {
		t.Fatal(err)
	}
	if got := router.threadReadStateRevisionToRetire(threadID, "turn-2"); got != "" {
		t.Fatalf("explicit thread-start marker returned %q", got)
	}
}

func mustThreadReadStateForApp(t *testing.T, ctx context.Context, runtime *state.StateRuntime, threadID string) state.ThreadReadState {
	t.Helper()
	states, err := runtime.ThreadReadStates(ctx, []string{threadID})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := states[threadID]
	if !ok {
		t.Fatalf("missing state for %s", threadID)
	}
	return value
}
