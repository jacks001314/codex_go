package appserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/compact"
	"codex_go/session"
	"codex_go/turn"
)

func newEnvironmentSubagentTestRouter(t *testing.T, multiAgentVersion string) (*RuntimeRouter, *session.Store) {
	t.Helper()
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	records := []*session.Record{
		{ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root", MultiAgentVersion: multiAgentVersion,
		}},
		{ID: "child-a", SessionID: "child-a", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root/alpha", AgentNickname: "Alpha",
		}},
		{ID: "child-b", SessionID: "child-b", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root/beta",
		}},
		{ID: "grand", SessionID: "grand", ParentThreadID: "child-a", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root/alpha/grand",
		}},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatal(err)
		}
	}
	graph := agent.NewMemoryStore()
	for _, edge := range [][2]string{{"root", "child-a"}, {"root", "child-b"}, {"child-a", "grand"}} {
		if err := graph.UpsertThreadSpawnEdge(edge[0], edge[1], agent.ThreadSpawnEdgeOpen); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		SpawnGraph:   graph,
		Environment:  NewEnvironmentManager(EnvironmentShellInfo{Name: "sh"}, cwd),
		DefaultCWD:   cwd,
	})
	return router, store
}

// Mirrors Rust #43491: the multi-agent v2 roster lists registered direct
// children (including unloaded ones), excludes grandchildren, and renders the
// full agent path.
func TestTurnEnvironmentContextRendersMultiAgentV2SubagentsLikeRust(t *testing.T) {
	router, _ := newEnvironmentSubagentTestRouter(t, string(agent.VersionV2))
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	text := router.turnEnvironmentContextTextAt(&turn.TurnStartParams{ThreadID: "root"}, now, "UTC", "")
	want := "  <subagents>\n    <agent name=\"/root/alpha\" />\n    <agent name=\"/root/beta\" />\n  </subagents>\n"
	if !strings.Contains(text, want) {
		t.Fatalf("environment context missing v2 subagents roster: %q", text)
	}
	if strings.Contains(text, "grand") {
		t.Fatalf("environment context must exclude grandchildren: %q", text)
	}

	// A loaded child is listed before an alphabetically earlier unloaded sibling.
	router.threads.liveThreads[session.ThreadID("child-b")] = &managedLiveThread{}
	text = router.turnEnvironmentContextTextAt(&turn.TurnStartParams{ThreadID: "root"}, now, "UTC", "")
	want = "  <subagents>\n    <agent name=\"/root/beta\" />\n    <agent name=\"/root/alpha\" />\n  </subagents>\n"
	if !strings.Contains(text, want) {
		t.Fatalf("environment context did not prioritize loaded child: %q", text)
	}
}

// Mirrors Rust's non-v2 behavior: only loaded children are listed, using the
// short agent path name and nickname.
func TestTurnEnvironmentContextRendersMultiAgentV1SubagentsLikeRust(t *testing.T) {
	router, _ := newEnvironmentSubagentTestRouter(t, "")
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	router.threads.liveThreads[session.ThreadID("child-a")] = &managedLiveThread{}
	text := router.turnEnvironmentContextTextAt(&turn.TurnStartParams{ThreadID: "root"}, now, "UTC", "")
	want := "  <subagents>\n    - alpha: Alpha\n  </subagents>\n"
	if !strings.Contains(text, want) {
		t.Fatalf("environment context missing v1 subagents roster: %q", text)
	}
	if strings.Contains(text, "beta") {
		t.Fatalf("environment context must omit unloaded children for v1: %q", text)
	}
}

type environmentTestAgentReader struct {
	children []AgentRef
	err      error
	reads    int
}

func (r *environmentTestAgentReader) Root(context.Context) (AgentRef, error) {
	return AgentRef{ThreadID: "root", Path: "/root"}, nil
}

func (r *environmentTestAgentReader) Parent(context.Context) (*AgentRef, error) {
	return nil, nil
}

func (r *environmentTestAgentReader) Children(context.Context) ([]AgentRef, error) {
	r.reads++
	if r.err != nil {
		return nil, r.err
	}
	return append([]AgentRef(nil), r.children...), nil
}

// Mirrors Rust #59531: host-owned V2 membership is read through the injected
// reader, preserving its loaded-first order, and a failed read omits the roster
// without falling back to controller-owned spawn edges.
func TestTurnEnvironmentContextUsesHostOwnedSubagentReaderLikeRust(t *testing.T) {
	router, store := newEnvironmentSubagentTestRouter(t, string(agent.VersionV2))
	reader := &environmentTestAgentReader{children: []AgentRef{
		{ThreadID: "child-b", Path: "/root/beta"},
		{ThreadID: "child-a", Path: "/root/alpha"},
	}}
	router.services.AgentSessionReader = reader
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	text := router.turnEnvironmentContextTextAt(&turn.TurnStartParams{ThreadID: "root"}, now, "UTC", "")
	want := "  <subagents>\n    <agent name=\"/root/beta\" />\n    <agent name=\"/root/alpha\" />\n  </subagents>\n"
	if !strings.Contains(text, want) {
		t.Fatalf("host-owned roster = %q, want %q", text, want)
	}

	record, err := store.Read("root", true, true)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	reader.children = []AgentRef{
		{ThreadID: "child-a", Path: "/root/alpha"},
		{ThreadID: "child-c", Path: "/root/gamma"},
	}
	items, err := router.compactEnvironmentContext(context.Background(), record, &compact.Request{ThreadID: "root", TurnID: "compact-roster"})
	if err != nil {
		t.Fatalf("compact environment context: %v", err)
	}
	compacted := ""
	for _, item := range items {
		if item.Kind == "environment_context" {
			compacted = item.Text
		}
	}
	if !strings.Contains(compacted, "/root/gamma") || strings.Contains(compacted, "/root/beta") {
		t.Fatalf("compaction did not refresh host roster: %q", compacted)
	}

	reader.err = errors.New("roster unavailable")
	failed := router.turnEnvironmentContextTextAt(&turn.TurnStartParams{ThreadID: "root"}, now, "UTC", "")
	if strings.Contains(failed, "<subagents>") || strings.Contains(failed, "/root/alpha") {
		t.Fatalf("failed host roster read should omit the roster: %q", failed)
	}
}

// blockingEnvironmentReader suspends the host-owned roster read until the test
// releases it, so ordering around the compaction window can be observed.
type blockingEnvironmentReader struct {
	started chan struct{}
	release chan struct{}
	err     error
}

func newBlockingEnvironmentReader() *blockingEnvironmentReader {
	return &blockingEnvironmentReader{started: make(chan struct{}), release: make(chan struct{})}
}

func (r *blockingEnvironmentReader) Root(context.Context) (AgentRef, error) {
	return AgentRef{ThreadID: "root", Path: "/root"}, nil
}

func (r *blockingEnvironmentReader) Parent(context.Context) (*AgentRef, error) {
	return nil, nil
}

func (r *blockingEnvironmentReader) Children(ctx context.Context) ([]AgentRef, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if r.err != nil {
		return nil, r.err
	}
	return nil, nil
}

func newHostRosterCompactionRouter(t *testing.T, threadID string, items []session.Item) *RuntimeRouter {
	t.Helper()
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	created := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	record := &session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID, CreatedAt: created, UpdatedAt: created, RecencyAt: created,
		Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root", Model: "gpt-5.4", MultiAgentVersion: string(agent.VersionV2),
			Extra: map[string]any{},
		},
		Items: items,
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Environment:  NewEnvironmentManager(EnvironmentShellInfo{Name: "sh"}, cwd),
		DefaultCWD:   cwd,
	})
	router.SetNotificationSink(NewNotificationBuffer())
	router.requireThreadStatus().UpsertThread(threadID, false)
	return router
}

// Mirrors Rust #59531: the replacement context's host-owned roster read
// resolves before the compaction window advances, so a roster provider that
// blocks keeps the old window in place until it returns.
func TestCompactThreadReadsHostRosterBeforeAdvancingWindowLikeRust(t *testing.T) {
	const threadID = "thread-roster-window-order"
	created := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	router := newHostRosterCompactionRouter(t, threadID, []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first request", CreatedAt: created},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "first answer", CreatedAt: created},
	})
	reader := newBlockingEnvironmentReader()
	reader.err = errors.New("roster unavailable")
	router.services.AgentSessionReader = reader

	done := make(chan *Response, 1)
	go func() {
		done <- router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: threadID}))
	}()
	select {
	case <-reader.started:
	case <-time.After(10 * time.Second):
		t.Fatal("compaction never read the host roster")
	}
	if got := router.windowNumberForThread(threadID); got != 0 {
		t.Fatalf("window advanced to %d while the roster read was blocked", got)
	}
	close(reader.release)
	select {
	case response := <-done:
		if response.Error != nil {
			t.Fatalf("compact error: %+v", response.Error)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("compaction did not finish after the roster read was released")
	}
	if got := router.windowNumberForThread(threadID); got != 1 {
		t.Fatalf("window number after compaction = %d, want 1", got)
	}
}

// Mirrors Rust #59531: a prepared compaction candidate captured before the
// roster read is rejected when the history was replaced while that read was
// outstanding; the compaction falls back instead of installing the stale
// prefix over the newer history.
func TestCompactThreadRejectsStaleCandidateAfterHistoryReplacementLikeRust(t *testing.T) {
	const threadID = "thread-roster-stale-candidate"
	created := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	router := newHostRosterCompactionRouter(t, threadID, []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first request", CreatedAt: created},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "first answer", CreatedAt: created},
	})
	record, err := router.services.ThreadRouter.store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	originalHistory := compactItemsFromSessionItems(record.Items)
	generation := compact.HistoryVersion(originalHistory)
	state := router.threads.asyncCompactionState(threadID)
	authOwner := router.authOwnerRevisionSnapshot()
	state.mu.Lock()
	state.lastAttempt = &compactionAttemptKey{
		HistoryVersion:      generation,
		ModelSlug:           "gpt-5.4",
		CompactionHash:      router.modelCompHash("gpt-5.4"),
		AuthOwnerGeneration: authOwner,
	}
	state.resultCh = make(chan *backgroundCompactionCandidate, 1)
	state.resultCh <- &backgroundCompactionCandidate{
		Prefix: compact.CompactionPrefix{Generation: generation, Len: len(originalHistory), AuthOwnerGeneration: &authOwner},
		Result: &compact.Result{
			Status:  compact.StatusCompleted,
			Summary: "stale prepared candidate",
			NewHistory: []compact.Item{{
				ID: "stale-summary", Type: "message", Role: "user", Kind: "compaction_summary",
				Text: "STALE PREPARED CANDIDATE", Created: created,
			}},
		},
	}
	state.mu.Unlock()

	reader := newBlockingEnvironmentReader()
	router.services.AgentSessionReader = reader
	done := make(chan *Response, 1)
	go func() {
		done <- router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: threadID}))
	}()
	select {
	case <-reader.started:
	case <-time.After(10 * time.Second):
		t.Fatal("compaction never read the host roster")
	}
	// Replace the history while the roster read is outstanding.
	replaced := *record
	replaced.Items = []session.Item{
		{ID: "u2", Type: "message", Role: "user", Text: "replacement request", CreatedAt: created},
		{ID: "a2", Type: "agent_message", Role: "assistant", Text: "replacement answer", CreatedAt: created},
	}
	if err := router.services.ThreadRouter.store.Save(&replaced); err != nil {
		t.Fatalf("Save(replacement) error = %v", err)
	}
	close(reader.release)
	select {
	case response := <-done:
		if response.Error != nil {
			t.Fatalf("compact error: %+v", response.Error)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("compaction did not finish after the roster read was released")
	}
	latest, err := router.services.ThreadRouter.store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("read compacted record: %v", err)
	}
	for _, item := range latest.Items {
		if strings.Contains(item.Text, "STALE PREPARED CANDIDATE") {
			t.Fatalf("stale candidate installed over the replaced history: %#v", latest.Items)
		}
	}
	if got := router.windowNumberForThread(threadID); got != 1 {
		t.Fatalf("window number after compaction = %d, want 1", got)
	}
}
