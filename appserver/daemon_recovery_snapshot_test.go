package appserver

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"codex_go/daemonrecovery"
	"codex_go/session"
	"codex_go/turn"
)

// daemonRecoveryTestRouter builds a router whose store holds record and that
// reports the thread as loaded, matching how a client-loaded thread reaches the
// daemon snapshot (Rust thread_manager.list_thread_ids).
func daemonRecoveryTestRouter(t *testing.T, record *session.Record) *RuntimeRouter {
	t.Helper()
	store := session.NewStore(t.TempDir())
	if record != nil {
		if err := store.Create(record); err != nil {
			t.Fatalf("store.Create() error = %v", err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	if record != nil {
		router.requireThreadStatus().UpsertThread(string(record.ID), false)
	}
	return router
}

func daemonRecoveryTestRootRecord(threadID string, turnID string, withInput bool) *session.Record {
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		Metadata:  session.Metadata{CWD: "/workspace"},
	}
	if withInput {
		record.Items = []session.Item{{
			ID:   runtimeUserPromptSessionItemID(turnID),
			Type: "message",
			Role: "user",
			Text: "finish the task",
		}}
	}
	return record
}

// Rust app-server/src/request_processors/daemon_snapshot.rs selects a persistent
// root thread whose newest regular turn may continue, and
// core/src/session/daemon_recovery.rs captures the interrupted turn. The
// recorded shape of an interrupted turn is
// app-server-transport/src/daemon_recovery.rs::InterruptedTurn.
func TestDaemonRecoverySnapshotSelectsInterruptedRegularTurnsLikeRust(t *testing.T) {
	record := daemonRecoveryTestRootRecord("thread-root", "turn-1", true)
	router := daemonRecoveryTestRouter(t, record)

	tier := "priority"
	params := &turn.TurnStartParams{
		ThreadID:              "thread-root",
		Prompt:                "finish the task",
		ServiceTier:           &tier,
		RuntimeWorkspaceRoots: []string{"/workspace"},
	}
	if err := router.threads.RegisterTurn("thread-root", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}

	snapshot := router.DaemonRecoverySnapshot()
	if len(snapshot.Loaded) != 1 || snapshot.Loaded[0] != "thread-root" {
		t.Fatalf("Loaded = %#v, want the loaded root thread", snapshot.Loaded)
	}
	interrupted, ok := snapshot.Interrupted["thread-root"]
	if !ok {
		t.Fatalf("Interrupted = %#v, want the interrupted turn", snapshot.Interrupted)
	}
	if interrupted.TurnID != "turn-1" {
		t.Fatalf("turn id = %q, want turn-1", interrupted.TurnID)
	}
	if interrupted.ServiceTier == nil || *interrupted.ServiceTier != "priority" {
		t.Fatalf("service tier = %#v, want priority", interrupted.ServiceTier)
	}

	// Rust ThreadEnvironment is camelCase on the wire (protocol/v2/environment.rs).
	var environment struct {
		EnvironmentID         string   `json:"environmentId"`
		CWD                   string   `json:"cwd"`
		RuntimeWorkspaceRoots []string `json:"runtimeWorkspaceRoots"`
	}
	if err := json.Unmarshal(interrupted.LocalEnvironment, &environment); err != nil {
		t.Fatalf("local environment is not the ThreadEnvironment shape: %v (%s)", err, interrupted.LocalEnvironment)
	}
	if environment.EnvironmentID != "local" || environment.CWD != "/workspace" {
		t.Fatalf("local environment = %#v", environment)
	}
	if len(environment.RuntimeWorkspaceRoots) != 1 || environment.RuntimeWorkspaceRoots[0] != "/workspace" {
		t.Fatalf("workspace roots = %#v", environment.RuntimeWorkspaceRoots)
	}

	// Rust InterruptedTurn has no skip_serializing_if, so the absent optionals
	// stay on disk as null rather than being dropped.
	raw, err := json.Marshal(interrupted)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, want := range []string{`"output_schema":null`, `"cyber_access_program":null`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("interrupted turn JSON = %s, want %s", raw, want)
		}
	}
}

// The continuation gate: a turn whose accepted input is not yet recorded can
// never resume (Rust core/src/session/turn.rs:450 RecordedTurnInput), so the
// thread is a candidate but carries no interrupted turn.
func TestDaemonRecoverySnapshotRequiresRecordedInputLikeRust(t *testing.T) {
	record := daemonRecoveryTestRootRecord("thread-root", "turn-1", false)
	router := daemonRecoveryTestRouter(t, record)
	if err := router.threads.RegisterTurn("thread-root", "turn-1", nil, 0, &turn.TurnStartParams{ThreadID: "thread-root", Prompt: "finish the task"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}

	snapshot := router.DaemonRecoverySnapshot()
	if len(snapshot.Loaded) != 1 || snapshot.Loaded[0] != "thread-root" {
		t.Fatalf("Loaded = %#v, want the loaded root thread", snapshot.Loaded)
	}
	if len(snapshot.Interrupted) != 0 {
		t.Fatalf("Interrupted = %#v, want no resume before the input is recorded", snapshot.Interrupted)
	}
}

// TaskKind::Regular: a review turn is not a recovery candidate.
func TestDaemonRecoverySnapshotSkipsReviewTurnsLikeRust(t *testing.T) {
	record := daemonRecoveryTestRootRecord("thread-root", "turn-1", true)
	router := daemonRecoveryTestRouter(t, record)
	if err := router.threads.RegisterTurn("thread-root", "turn-1", nil, 0, &turn.TurnStartParams{
		ThreadID:   "thread-root",
		Prompt:     "review it",
		Originator: "review",
	}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}

	if snapshot := router.DaemonRecoverySnapshot(); len(snapshot.Interrupted) != 0 {
		t.Fatalf("Interrupted = %#v, want no resume for a review turn", snapshot.Interrupted)
	}
}

// The root filter in daemon_snapshot.rs: ephemeral threads, child threads, and
// non-root agents are never daemon recovery candidates.
func TestDaemonRecoverySnapshotSkipsNonRootThreadsLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		record *session.Record
	}{
		{
			name: "ephemeral",
			record: &session.Record{
				ID:       "thread-ephemeral",
				Metadata: session.Metadata{Extra: map[string]any{"ephemeral": true}},
			},
		},
		{
			name: "child",
			record: &session.Record{
				ID:             "thread-child",
				ParentThreadID: "thread-root",
			},
		},
		{
			name: "subagent",
			record: &session.Record{
				ID:       "thread-subagent",
				Metadata: session.Metadata{ThreadSource: string(ThreadSourceSubagent)},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if daemonRecoveryRootThread(tc.record) {
				t.Fatalf("%s record = %#v, want it excluded from daemon recovery", tc.name, tc.record)
			}
			router := daemonRecoveryTestRouter(t, tc.record)
			turnID := "turn-" + tc.name
			if err := router.threads.RegisterTurn(string(tc.record.ID), turnID, nil, 0, &turn.TurnStartParams{ThreadID: string(tc.record.ID)}); err != nil {
				t.Fatalf("RegisterTurn() error = %v", err)
			}
			if snapshot := router.DaemonRecoverySnapshot(); len(snapshot.Loaded) != 0 {
				t.Fatalf("Loaded = %#v, want no candidates", snapshot.Loaded)
			}
		})
	}
}

// A managed daemon's snapshot is the union of every connection's candidates,
// taken once after the server loop returns (Rust app-server/src/lib.rs:1037-1042
// keeps the snapshot at daemon scope, not per connection).
func TestDaemonRecoverySinkMergesConnectionSnapshotsLikeRust(t *testing.T) {
	sink := NewDaemonRecoverySink(t.TempDir())

	routerA := daemonRecoveryTestRouter(t, daemonRecoveryTestRootRecord("thread-a", "turn-a", true))
	if err := routerA.threads.RegisterTurn("thread-a", "turn-a", nil, 0, &turn.TurnStartParams{ThreadID: "thread-a", Prompt: "finish the task"}); err != nil {
		t.Fatalf("RegisterTurn(A) error = %v", err)
	}
	routerA.SetDaemonRecoverySink(sink)
	_ = routerA.Close()

	routerB := daemonRecoveryTestRouter(t, daemonRecoveryTestRootRecord("thread-b", "turn-b", true))
	if err := routerB.threads.RegisterTurn("thread-b", "turn-b", nil, 0, &turn.TurnStartParams{ThreadID: "thread-b", Prompt: "finish the task"}); err != nil {
		t.Fatalf("RegisterTurn(B) error = %v", err)
	}
	routerB.SetDaemonRecoverySink(sink)
	_ = routerB.Close()

	if err := sink.WriteSnapshot(); err != nil {
		t.Fatalf("WriteSnapshot() error = %v", err)
	}
	read, err := daemonrecovery.ReadSnapshot(sink.Path())
	if err != nil {
		t.Fatalf("ReadSnapshot() error = %v", err)
	}
	if len(read.Loaded) != 2 || read.Loaded[0] != "thread-a" || read.Loaded[1] != "thread-b" {
		t.Fatalf("Loaded = %#v, want the union of both connections", read.Loaded)
	}
	if len(read.Interrupted) != 2 || read.Interrupted["thread-a"].TurnID != "turn-a" || read.Interrupted["thread-b"].TurnID != "turn-b" {
		t.Fatalf("Interrupted = %#v, want both interrupted turns", read.Interrupted)
	}
}

// The sink exists only for a managed daemon: an unmanaged server builds none and
// writes no file.
func TestDaemonRecoverySinkForAccessOnlyManagedLikeRust(t *testing.T) {
	home := t.TempDir()
	if sink := daemonRecoverySinkForAccess(DaemonShutdownDisabled, home); sink != nil {
		t.Fatalf("unmanaged sink = %#v, want nil", sink)
	}
	sink := daemonRecoverySinkForAccess(DaemonShutdownManaged, home)
	if sink == nil {
		t.Fatal("managed sink = nil, want a sink")
	}
	if sink.Path() != daemonrecovery.FilePath(home) {
		t.Fatalf("sink path = %q, want %q", sink.Path(), daemonrecovery.FilePath(home))
	}

	// With no sink, closing a router must not leave a recovery file behind.
	router := daemonRecoveryTestRouter(t, daemonRecoveryTestRootRecord("thread-root", "turn-1", true))
	if err := router.threads.RegisterTurn("thread-root", "turn-1", nil, 0, &turn.TurnStartParams{ThreadID: "thread-root"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	router.SetDaemonRecoverySink(daemonRecoverySinkForAccess(DaemonShutdownDisabled, home))
	_ = router.Close()
	if _, err := os.Stat(daemonrecovery.FilePath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery file stat error = %v, want not-exist for an unmanaged server", err)
	}
}
