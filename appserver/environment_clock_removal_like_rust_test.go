package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/compact"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

type scriptedClockRead struct {
	value int64
	err   string
}

// scriptedClockSink answers current-time/read server requests from a script so
// a test can interleave successful and failing external clock reads.
type scriptedClockSink struct {
	mu     sync.Mutex
	router *RuntimeRouter
	reads  []scriptedClockRead
	seen   int
}

func (s *scriptedClockSink) SendServerRequest(request *ServerRequest) {
	s.send(request)
}

func (s *scriptedClockSink) SendServerRequestToConnection(_ string, request *ServerRequest) {
	s.send(request)
}

func (s *scriptedClockSink) send(request *ServerRequest) {
	if request.Method != ServerRequestCurrentTimeRead {
		return
	}
	s.mu.Lock()
	next := scriptedClockRead{value: 1781717655}
	if len(s.reads) > 0 {
		next = s.reads[0]
		s.reads = s.reads[1:]
	}
	s.seen++
	s.mu.Unlock()
	go func() {
		if next.err != "" {
			_, _ = s.router.requireServerRequests().Resolve(ErrorResponse(request.ID, -32603, next.err, nil))
			return
		}
		_, _ = s.router.requireServerRequests().Resolve(OK(request.ID, &CurrentTimeReadResponse{CurrentTimeAt: next.value}))
	}()
}

func (s *scriptedClockSink) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen
}

func nonfatalExternalClockConfig() *config.Config {
	return &config.Config{Values: map[string]any{
		"features": map[string]any{
			"nonfatal_clock_read_errors": true,
			"current_time_reminder": map[string]any{
				"enabled":      true,
				"clock_source": "external",
			},
		},
	}}
}

func newEnvironmentRemovalTestRouter(t *testing.T, threadID string) (*RuntimeRouter, *scriptedClockSink, *session.Store) {
	t.Helper()
	return newEnvironmentRemovalTestRouterWithConfig(t, threadID, "")
}

// newEnvironmentRemovalTestRouterWithConfig seeds a thread record and a router
// whose config service reads toml (when non-empty) from the temporary home.
func newEnvironmentRemovalTestRouterWithConfig(t *testing.T, threadID string, toml string) (*RuntimeRouter, *scriptedClockSink, *session.Store) {
	t.Helper()
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	now := time.Now().UTC()
	record := &session.Record{ID: session.ThreadID(threadID), SessionID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	home := t.TempDir()
	if strings.TrimSpace(toml) != "" {
		if err := os.WriteFile(config.ConfigPath(home), []byte(toml), 0o600); err != nil {
			t.Fatalf("WriteFile config error = %v", err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		DefaultCWD:   t.TempDir(),
	})
	sink := &scriptedClockSink{router: router}
	router.SetServerRequestSink(sink)
	router.subscribeThreadConnection(threadID, "conn-clock")
	return router, sink, store
}

// Rust #45825 (core/src/context/world_state/environment.rs): an environment
// date the model already saw is cleared with
// `<current_date status="unavailable" />` exactly once, and renders normally
// again once the clock recovers.
func TestEnvironmentContextDateRemovalRendersUnavailableOnceLikeRust(t *testing.T) {
	const threadID = "thread-clock-removal"
	router, sink, _ := newEnvironmentRemovalTestRouter(t, threadID)
	sink.reads = []scriptedClockRead{
		{value: 1781717655},
		{err: "PRIVATE_CLOCK_ERROR"},
		{err: "PRIVATE_CLOCK_ERROR"},
		{value: 1781717655},
	}
	cfg := nonfatalExternalClockConfig()
	params := &turn.TurnStartParams{ThreadID: threadID, CWD: t.TempDir()}
	wantDate := time.Unix(1781717655, 0).In(time.Local).Format("2006-01-02")

	read := func(turnID string) (string, []session.Item) {
		t.Helper()
		items, sessionItems, err := router.turnEnvironmentContextInputItemForTurn(context.Background(), threadID, turnID, params, cfg)
		if err != nil {
			t.Fatalf("turn %s environment context error = %v", turnID, err)
		}
		texts := messageInputTextsForRole(items, "user")
		if len(texts) != 1 {
			t.Fatalf("turn %s environment texts = %#v, want exactly one", turnID, texts)
		}
		return texts[0], sessionItems
	}

	first, _ := read("turn-1")
	if !strings.Contains(first, "<current_date>"+wantDate+"</current_date>") {
		t.Fatalf("first environment context = %q, want the visible date", first)
	}

	removed, sessionItems := read("turn-2")
	if !strings.Contains(removed, "  <current_date status=\"unavailable\" />\n") {
		t.Fatalf("removed environment context = %q, want the unavailable date marker", removed)
	}
	if strings.Contains(removed, "<current_date>") {
		t.Fatalf("removed environment context = %q, must not still carry a date", removed)
	}
	if !strings.Contains(removed, "<timezone>") {
		t.Fatalf("removed environment context = %q, want the timezone retained", removed)
	}
	if len(sessionItems) != 1 || stringValueFromMap(sessionItems[0].Data, "kind") != "current_time_unavailable" {
		t.Fatalf("turn-2 session items = %#v, want one current_time_unavailable notice", sessionItems)
	}

	stillMissing, _ := read("turn-3")
	if strings.Contains(stillMissing, "<current_date") {
		t.Fatalf("turn-3 environment context = %q, the cleared date must not be reported twice", stillMissing)
	}

	recovered, _ := read("turn-4")
	if !strings.Contains(recovered, "<current_date>"+wantDate+"</current_date>") {
		t.Fatalf("recovered environment context = %q, want the date restored", recovered)
	}
	if sink.readCount() != 4 {
		t.Fatalf("clock reads = %d, want one per turn", sink.readCount())
	}
}

// Rust renders the whole world state for a compaction window (render_full), so
// the compaction context omits a missing date without clearing it a second time.
func TestCompactEnvironmentContextSkipsDateRemovalNoticeLikeRust(t *testing.T) {
	const threadID = "thread-compact-clock-removal"
	router, sink, store := newEnvironmentRemovalTestRouterWithConfig(t, threadID, `
include_environment_context = true

[features]
nonfatal_clock_read_errors = true

[features.current_time_reminder]
enabled = true
clock_source = "external"
`)
	sink.reads = []scriptedClockRead{{err: "PRIVATE_CLOCK_ERROR"}}
	record, err := router.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("threadRecord() error = %v", err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatalf("DecodeWorldState() error = %v", err)
	}
	state.Environments = []byte(`{"currentDate":"2026-06-17"}`)
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		t.Fatalf("EncodeWorldState() error = %v", err)
	}

	request := &compact.Request{ThreadID: threadID, TurnID: "turn-compact"}
	items, err := router.compactEnvironmentContext(context.Background(), record, request)
	if err != nil {
		t.Fatalf("compactEnvironmentContext() error = %v", err)
	}
	text := ""
	for _, item := range items {
		if item.Kind == "environment_context" {
			text = item.Text
		}
	}
	if strings.Contains(text, "<current_date") || strings.Contains(text, "status=\"unavailable\"") {
		t.Fatalf("compaction environment context = %q, want no removal marker", text)
	}
	state, err = session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatalf("DecodeWorldState() error = %v", err)
	}
	snapshot, known := decodeEnvironmentWorldStateSnapshot(state.Environments)
	if !known || snapshot.CurrentDate != "" {
		t.Fatalf("compaction snapshot = %#v known=%t, want the empty date recorded", snapshot, known)
	}
	// The compaction caller stores the record once it has rewritten the window.
	if err := router.runtimeSaveThreadRecord(record); err != nil {
		t.Fatalf("runtimeSaveThreadRecord() error = %v", err)
	}
	persisted, err := store.Read(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatalf("store.Read() error = %v", err)
	}
	persistedState, err := session.DecodeWorldState(persisted.Metadata.WorldState)
	if err != nil {
		t.Fatalf("DecodeWorldState(persisted) error = %v", err)
	}
	persistedSnapshot, persistedKnown := decodeEnvironmentWorldStateSnapshot(persistedState.Environments)
	if !persistedKnown || persistedSnapshot.CurrentDate != "" {
		t.Fatalf("persisted snapshot = %#v known=%t, want the cleared date stored", persistedSnapshot, persistedKnown)
	}

	params := &turn.TurnStartParams{ThreadID: threadID, CWD: t.TempDir()}
	sink.reads = []scriptedClockRead{{err: "PRIVATE_CLOCK_ERROR"}}
	nextItems, _, err := router.turnEnvironmentContextInputItemForTurn(context.Background(), threadID, "turn-after-compact", params, nonfatalExternalClockConfig())
	if err != nil {
		t.Fatalf("post-compaction environment context error = %v", err)
	}
	for _, text := range messageInputTextsForRole(nextItems, "user") {
		if strings.Contains(text, "<current_date") {
			t.Fatalf("post-compaction environment context = %q, the cleared date must not be reported again", text)
		}
	}
}

// Rust's shell_version_removed arm (#41232) reports a previously visible
// PowerShell version the same way once it disappears.
func TestEnvironmentWorldStateRemovalSnapshotLikeRust(t *testing.T) {
	const threadID = "thread-environment-snapshot"
	router, _, _ := newEnvironmentRemovalTestRouter(t, threadID)
	record, err := router.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("threadRecord() error = %v", err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatalf("DecodeWorldState() error = %v", err)
	}
	state.Environments = []byte(`{"currentDate":"2026-06-17","shellVersion":"7.4.1"}`)
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		t.Fatalf("EncodeWorldState() error = %v", err)
	}

	removals, err := router.recordEnvironmentWorldState(record, "", "", true, false)
	if err != nil {
		t.Fatalf("recordEnvironmentWorldState() error = %v", err)
	}
	if !removals.CurrentDate || !removals.ShellVersion {
		t.Fatalf("removals = %#v, want both values reported once", removals)
	}
	text := router.turnEnvironmentContextText(&turn.TurnStartParams{CWD: t.TempDir()}, nil, "UTC", "", true, removals)
	if !strings.Contains(text, "  <shell_version status=\"unavailable\" />\n") ||
		!strings.Contains(text, "  <current_date status=\"unavailable\" />\n") {
		t.Fatalf("removed environment context = %q", text)
	}

	again, err := router.recordEnvironmentWorldState(record, "", "", true, false)
	if err != nil {
		t.Fatalf("second recordEnvironmentWorldState() error = %v", err)
	}
	if again.CurrentDate || again.ShellVersion {
		t.Fatalf("second removals = %#v, want no repeat notice", again)
	}

	restored, err := router.recordEnvironmentWorldState(record, "2026-06-18", "7.4.1", true, false)
	if err != nil {
		t.Fatalf("restoring recordEnvironmentWorldState() error = %v", err)
	}
	if restored.CurrentDate || restored.ShellVersion {
		t.Fatalf("restored removals = %#v, want no removal on recovery", restored)
	}
}

type clockSleepFailureAgent struct {
	mu       sync.Mutex
	requests []*model.AgentRequest
}

func (a *clockSleepFailureAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	call := len(a.requests) + 1
	copied := *request
	copied.InputItems = append([]any(nil), request.InputItems...)
	a.requests = append(a.requests, &copied)
	a.mu.Unlock()
	if call == 1 {
		return &model.AgentResponse{
			ResponseID: "resp-sleep-call",
			Items: []model.AgentItem{{
				ID:        "sleep-1",
				Type:      "function_call",
				Name:      "sleep",
				Namespace: "clock",
				CallID:    "sleep-1",
				Arguments: `{"duration_ms":60000}`,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-after-sleep",
		Message:    "done",
		Items: []model.AgentItem{{
			ID:   "msg-after-sleep",
			Type: "agent_message",
			Text: "done",
		}},
	}, nil
}

// Rust #45825 (app-server tests/suite/v2/sleep.rs): a clock failure while the
// sleep tool polls returns the generic notice to the model, keeps the sleep
// item completion notification, and lets the turn finish normally.
func TestRuntimeRouterSleepClockFailureReturnsNoticeAndCompletesTurnLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
include_environment_context = false

[features]
nonfatal_clock_read_errors = true

[features.current_time_reminder]
enabled = true
clock_source = "external"
sleep_tool = true
`), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	agent := &clockSleepFailureAgent{}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		DefaultCWD:   t.TempDir(),
	})
	router.SetNotificationSink(sink)
	clock := &scriptedClockSink{router: router, reads: []scriptedClockRead{
		{value: 1781717655},
		{value: 1781717655},
		{err: "PRIVATE_CLOCK_ERROR"},
	}}
	router.SetServerRequestSink(clock)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello sleep",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "sleep through a stalled clock",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForItemStarted(t, sink, "sleep-1")
	completed := waitForItemCompleted(t, sink, "sleep-1")
	if completed.Item["type"] != "sleep" {
		t.Fatalf("sleep completed item = %#v", completed.Item)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	agent.mu.Lock()
	requests := append([]*model.AgentRequest(nil), agent.requests...)
	agent.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want the failed tool result to reach a second inference", len(requests))
	}
	outputs := additionalContextToolOutputs(t, requests[1].InputItems)
	found := false
	for _, output := range outputs {
		if output["call_id"] != "sleep-1" {
			continue
		}
		found = true
		if output["output"] != "failed to read current time" {
			t.Fatalf("sleep tool output = %#v, want the generic clock notice", output["output"])
		}
	}
	if !found {
		t.Fatalf("second request has no output for sleep-1: %#v", outputs)
	}
	if clock.readCount() < 3 {
		t.Fatalf("clock reads = %d, want the sleep tool to poll the external clock", clock.readCount())
	}
}
