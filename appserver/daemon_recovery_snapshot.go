package appserver

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"codex_go/daemonrecovery"
	"codex_go/execserver"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Managed daemon recovery: the shutdown-time candidate snapshot.
//
// Rust app-server/src/lib.rs:1037-1042 asks the thread processor for a
// RecoverySnapshot while a managed daemon shuts down, and
// request_processors/daemon_snapshot.rs:15 selects the candidates. The next
// daemon start consumes the file once (daemon_thread_recovery.rs:32) and
// resumes the threads it names. DaemonRecoverySnapshot is the producer half.

// Structural differences from Rust (recorded with the Phase A handoff):
//   - Rust snapshots from a daemon-level ThreadManager; Go builds a RuntimeRouter
//     (with its own ThreadManager) per WebSocket connection, so DaemonRecoverySink
//     unions the per-connection routers and the union is written once.
//   - Rust gates the interrupted turn on the in-memory RecordedTurnInput marker
//     (core/src/session/turn.rs:450); Go has no such marker, so
//     daemonRecoveryInputRecorded checks the recorded user-prompt item instead.
//     Same meaning, at the cost of one record read per candidate.
//   - Rust skips threads in pending_thread_unloads; Go has no equivalent set.
//   - Rust records the resolved next-step service tier; Go falls back to
//     model.ServiceTierDefaultRequestValue ("default") when the turn carried none,
//     keeping the field present like Rust's Some(..) (deliberate difference).
//   - Rust reads the resolved selection cwd; Go falls back
//     selection cwd -> params.CWD -> record cwd (deliberate difference).
//   - Rust's restore loop is bounded by turn_admission permits; Go has no permit
//     equivalent, so the Phase B restore loop must bound itself.

// DaemonRecoverySnapshot mirrors ThreadRequestProcessor::daemon_recovery_snapshot
// (Rust app-server/src/request_processors/daemon_snapshot.rs). It returns the
// persistent root threads that are loaded right now, plus, for each one whose
// newest regular turn started but did not finish, the interrupted turn that may
// continue automatically after the restart.
func (r *RuntimeRouter) DaemonRecoverySnapshot() daemonrecovery.Snapshot {
	snapshot := daemonrecovery.Snapshot{Interrupted: map[string]daemonrecovery.InterruptedTurn{}}
	if r == nil || r.threads == nil {
		return snapshot
	}
	for _, threadID := range r.daemonRecoveryLoadedThreadIDs() {
		// includeHistory keeps the recorded input items the continuation gate
		// reads; includeArchived matches Rust, whose candidate filter never looks
		// at archival state.
		record, err := r.threadRecord(session.ThreadID(threadID), true, true)
		if err != nil || record == nil {
			continue
		}
		if !daemonRecoveryRootThread(record) {
			continue
		}
		// A thread becomes a candidate only after it is persisted through the
		// thread store, so a restart only restores threads that are on disk
		// (Rust persist_thread(PersistContext::Standard)).
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			continue
		}
		snapshot.Loaded = append(snapshot.Loaded, threadID)
		if interrupted, ok := r.daemonRecoveryInterruptedTurn(record, threadID); ok {
			snapshot.Interrupted[threadID] = interrupted
		}
	}
	sort.Strings(snapshot.Loaded)
	return snapshot
}

// daemonRecoveryLoadedThreadIDs mirrors Rust thread_manager.list_thread_ids():
// the threads loaded in this server. Go's app-server builds a RuntimeRouter per
// WebSocket connection (websocket.go serveWebSocketConnection), so this is the
// set loaded through the connection that is serving the snapshot request.
func (r *RuntimeRouter) daemonRecoveryLoadedThreadIDs() []string {
	status := r.requireThreadStatus()
	if status == nil {
		return nil
	}
	return status.LoadedThreadIDs()
}

// daemonRecoveryRootThread mirrors the candidate filter in Rust
// daemon_snapshot.rs: not ephemeral, no parent thread, and not a non-root agent.
func daemonRecoveryRootThread(record *session.Record) bool {
	if record == nil {
		return false
	}
	if runtimeRecordEphemeral(record) {
		return false
	}
	if strings.TrimSpace(string(record.ParentThreadID)) != "" {
		return false
	}
	return !runtimeRecordIsSubagent(record)
}

// daemonRecoveryInterruptedTurn mirrors Session::interrupted_turn
// (Rust core/src/session/daemon_recovery.rs:21). Only a regular turn whose
// accepted input is already recorded, and whose sole captured environment is
// the local thread-owned one, can continue automatically after a restart.
func (r *RuntimeRouter) daemonRecoveryInterruptedTurn(record *session.Record, threadID string) (daemonrecovery.InterruptedTurn, bool) {
	active := r.threads.ActiveTurn(strings.TrimSpace(threadID))
	if active == nil || strings.TrimSpace(active.TurnID) == "" || active.Params == nil {
		return daemonrecovery.InterruptedTurn{}, false
	}
	// TaskKind::Regular: review and other non-regular tasks are never resumed.
	if turnStartReviewRuntime(active.Params) {
		return daemonrecovery.InterruptedTurn{}, false
	}
	// RecordedTurnInput: the turn's accepted input must already be in the record,
	// otherwise a continuation would have nothing to continue from
	// (Rust core/src/session/turn.rs:450 inserts the marker after the input and
	// turn-start injections are persisted).
	if !daemonRecoveryInputRecorded(record, active.TurnID) {
		return daemonrecovery.InterruptedTurn{}, false
	}
	environment, ok := r.daemonRecoveryLocalEnvironment(active.Params, record)
	if !ok {
		return daemonrecovery.InterruptedTurn{}, false
	}
	return daemonrecovery.InterruptedTurn{
		TurnID:             active.TurnID,
		OutputSchema:       daemonRecoveryRawJSON(active.Params.OutputSchema),
		ServiceTier:        daemonRecoveryServiceTier(active.Params),
		CyberAccessProgram: daemonRecoveryRawJSON(daemonRecoveryCoreCyberAccessProgram(active.Params)),
		LocalEnvironment:   daemonRecoveryRawJSON(environment),
	}, true
}

// daemonRecoveryInputRecorded reports whether the turn's accepted input is in
// the thread record. The recorded user-prompt item carries the turn id, so it is
// the Go stand-in for Rust's in-memory RecordedTurnInput marker.
func daemonRecoveryInputRecorded(record *session.Record, turnID string) bool {
	if record == nil {
		return false
	}
	want := runtimeUserPromptSessionItemID(turnID)
	for i := range record.Items {
		if record.Items[i].ID == want {
			return true
		}
	}
	return false
}

// daemonRecoveryThreadEnvironment is the protocol ThreadEnvironment shape the
// daemon snapshot stores for the saved local environment (Rust
// app-server-protocol protocol/v2/environment.rs, camelCase on the wire).
type daemonRecoveryThreadEnvironment struct {
	EnvironmentID         string   `json:"environmentId"`
	CWD                   string   `json:"cwd"`
	RuntimeWorkspaceRoots []string `json:"runtimeWorkspaceRoots"`
}

// daemonRecoveryLocalEnvironment mirrors the single-local-environment gate in
// Rust Session::interrupted_turn: the resolved turn environments must be exactly
// the local one, and its configuration must be thread-owned (FromThread).
// Remote identities are never persisted across a daemon restart.
func (r *RuntimeRouter) daemonRecoveryLocalEnvironment(params *turn.TurnStartParams, record *session.Record) (daemonRecoveryThreadEnvironment, bool) {
	if params == nil {
		return daemonRecoveryThreadEnvironment{}, false
	}
	selections := params.Environments
	if len(selections) == 0 {
		// Rust EnvironmentManager::default_environment: a turn that selects no
		// environment uses the provider default, which for a configured executor
		// replaces the implicit local one.
		environmentID := execserver.LocalEnvironmentID
		if r.services.Environment != nil {
			if defaultID, ok := r.services.Environment.DefaultEnvironmentID(); ok {
				environmentID = defaultID
			}
		}
		selections = []map[string]any{{"environmentId": environmentID}}
	}
	if len(selections) != 1 {
		return daemonRecoveryThreadEnvironment{}, false
	}
	selected := selections[0]
	if selectionEnvironmentID(selected) != execserver.LocalEnvironmentID {
		return daemonRecoveryThreadEnvironment{}, false
	}
	// Read the raw selection state: FromThread is what Rust requires on the
	// captured TurnEnvironmentSelection (an owner-supplied Ready/Pending/Failed
	// attachment cannot resume automatically). Resolving through the thread
	// config would turn FromThread into Ready and lose that distinction.
	state, err := environmentConfigStateFromAnyMap(selected)
	if err != nil || state.Kind != EnvironmentConfigFromThread {
		return daemonRecoveryThreadEnvironment{}, false
	}
	// Rust stores `ThreadEnvironment::from(&TurnEnvironmentSelection)` and reads
	// its `workspace_roots` from the selection; Go's environment selections never
	// carry workspace roots (they are only read), so both this snapshot and the
	// Phase C continuation gate use the thread's canonical runtime workspace
	// roots, which is the same value the turn runtime resolves the profile from.
	roots := threadRecordRuntimeWorkspaceRoots(record, params.CWD, params.RuntimeWorkspaceRoots)
	return daemonRecoveryThreadEnvironment{
		EnvironmentID: execserver.LocalEnvironmentID,
		CWD: strings.TrimSpace(firstNonEmpty(
			threadItemStringFromAnyMap(selected, "cwd"),
			params.CWD,
			record.Metadata.CWD,
		)),
		RuntimeWorkspaceRoots: roots,
	}, true
}

// daemonRecoveryCoreCyberAccessProgram resolves the core snake_case program the
// saved turn used (Rust TurnStartOptions.cyber_access_program). An internally
// started turn already carries the resolved value; a client-selected program is
// mapped onto it.
func daemonRecoveryCoreCyberAccessProgram(params *turn.TurnStartParams) any {
	if params == nil {
		return nil
	}
	if core := strings.TrimSpace(params.CoreCyberAccessProgram); core != "" {
		return core
	}
	if params.CyberAccessProgram != nil {
		if core := params.CyberAccessProgram.CoreValue(); core != "" {
			return core
		}
	}
	return nil
}

// daemonRecoveryServiceTier mirrors Rust interrupted_turn, which always records
// a tier and falls back to the service-tier default request value.
func daemonRecoveryServiceTier(params *turn.TurnStartParams) *string {
	if params == nil {
		return nil
	}
	tier := ""
	if params.ServiceTier != nil {
		tier = strings.TrimSpace(*params.ServiceTier)
	}
	if tier == "" {
		tier = model.ServiceTierDefaultRequestValue
	}
	return &tier
}

// daemonRecoveryRawJSON marshals a value for storage, keeping Rust's absent
// option as an omitted RawMessage (which the snapshot then writes as null).
func daemonRecoveryRawJSON(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" {
		return nil
	}
	return data
}

// DaemonRecoverySink is the daemon-scoped contribution slot for a managed
// daemon's recovery snapshot.
//
// Rust takes one snapshot from a single daemon-level MessageProcessor
// (app-server/src/lib.rs:1037-1042). Go serves every WebSocket connection with
// its own RuntimeRouter (websocket.go serveWebSocketConnection), so the
// daemon-level snapshot is the union of the per-connection routers, written once
// after the server loop returns.
type DaemonRecoverySink struct {
	mu          sync.Mutex
	path        string
	loaded      map[string]struct{}
	interrupted map[string]daemonrecovery.InterruptedTurn
}

// NewDaemonRecoverySink returns the sink for a managed daemon rooted at
// codexHome. It writes the shared daemon recovery file (daemonrecovery.FilePath).
func NewDaemonRecoverySink(codexHome string) *DaemonRecoverySink {
	return &DaemonRecoverySink{
		path:        daemonrecovery.FilePath(codexHome),
		loaded:      map[string]struct{}{},
		interrupted: map[string]daemonrecovery.InterruptedTurn{},
	}
}

// daemonRecoverySinkForAccess returns a sink only for a managed daemon. Every
// other server must build none and write nothing, so an unmanaged app-server
// never leaves a recovery file behind.
func daemonRecoverySinkForAccess(access DaemonShutdownAccess, codexHome string) *DaemonRecoverySink {
	if access != DaemonShutdownManaged {
		return nil
	}
	return NewDaemonRecoverySink(codexHome)
}

// SetDaemonRecoverySink points the router at the daemon-scoped sink. A nil sink
// is a no-op, which is how an unmanaged server (and every test router) stays
// inert.
func (r *RuntimeRouter) SetDaemonRecoverySink(sink *DaemonRecoverySink) {
	if r == nil {
		return
	}
	r.daemonRecoverySink = sink
}

// contributeDaemonRecoverySnapshot offers this router's candidates to the
// daemon-scoped sink. It is a no-op without a sink.
func (r *RuntimeRouter) contributeDaemonRecoverySnapshot() {
	if r == nil || r.daemonRecoverySink == nil {
		return
	}
	r.daemonRecoverySink.Contribute(r.DaemonRecoverySnapshot())
}

// Contribute merges one router's candidates. Loaded is a union; Interrupted
// keeps the entry whose turn id sorts first, so the merged snapshot never
// depends on the order in which connections close.
func (s *DaemonRecoverySink) Contribute(snapshot daemonrecovery.Snapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, threadID := range snapshot.Loaded {
		s.loaded[threadID] = struct{}{}
	}
	for threadID, interrupted := range snapshot.Interrupted {
		if existing, ok := s.interrupted[threadID]; ok && existing.TurnID <= interrupted.TurnID {
			continue
		}
		s.interrupted[threadID] = interrupted
	}
}

// Path reports the file WriteSnapshot targets.
func (s *DaemonRecoverySink) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// WriteSnapshot writes the merged candidates once, after the server loop
// returns. A connection whose router has not closed by then loses its
// contribution: recovery stays best-effort, matching Rust's warn-only snapshot
// failure handling (app-server/src/daemon_thread_recovery.rs).
func (s *DaemonRecoverySink) WriteSnapshot() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	loaded := make([]string, 0, len(s.loaded))
	for threadID := range s.loaded {
		loaded = append(loaded, threadID)
	}
	sort.Strings(loaded)
	interrupted := make(map[string]daemonrecovery.InterruptedTurn, len(s.interrupted))
	for threadID, interruptedTurn := range s.interrupted {
		interrupted[threadID] = interruptedTurn
	}
	return daemonrecovery.WriteSnapshot(s.path, daemonrecovery.Snapshot{Loaded: loaded, Interrupted: interrupted})
}
