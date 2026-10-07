package appserver

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/daemonrecovery"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #45820 (`f2b5b81f39`) "Continue interrupted work after managed daemon
// restarts". request_processors/daemon_continuation.rs:23 `continue_daemon_turn`
// re-reads the interrupted turn's rollout after the restore, re-checks that the
// work is still unfinished, and starts one new turn with the hidden
// `daemon_recovery` fragment plus the saved output schema, service tier and root
// turn id. It appends the `TurnAborted` item for the saved turn before starting
// the continuation (daemon_continuation.rs:102).
//
// Rust tests mirrored here: core/tests/suite/turn_input_submission.rs
// `continue_turn_if_idle_starts_new_turn_with_internal_input` and
// app-server/tests/suite/v2/daemon_update_recovery.rs
// `managed_restart_resumes_loaded_threads_and_goal_without_client`.

// daemonRecoverySavedTurnID is the interrupted turn's id. Rust turn ids are
// UUIDs, so a Rust-style id is used here: the continuation's Go-generated
// `turn-N` id can never collide with the restored turn (Go numbers turns
// sequentially per process, a recorded structural difference).
const daemonRecoverySavedTurnID = "0f8e2b41-5c3d-4a97-8f16-2d9b7e64a0c3"

// daemonRecoveryContinuationFixture is one restored thread with an interrupted
// turn on disk, ready for continueDaemonTurn.
type daemonRecoveryContinuationFixture struct {
	home     string
	threadID string
	cwd      string
	router   *RuntimeRouter
	agent    *recordingRuntimeAgent
	sink     *NotificationBuffer
	options  daemonRecoveryContinuationSeedOptions
	saved    daemonrecovery.InterruptedTurn
}

// daemonRecoveryContinuationSeedOptions tweaks the restored rollout.
type daemonRecoveryContinuationSeedOptions struct {
	// omitTurnContext drops the interrupted turn's model-context record, which
	// is the gate-3 baseline.
	omitTurnContext bool
	// skipResume leaves the seeded thread off this generation's live set, so a
	// test can drive the restore (Phase B) itself.
	skipResume bool
}

func newDaemonRecoveryContinuationFixture(t *testing.T) *daemonRecoveryContinuationFixture {
	t.Helper()
	return newDaemonRecoveryContinuationFixtureWithOptions(t, daemonRecoveryContinuationSeedOptions{})
}

func newDaemonRecoveryContinuationFixtureWithOptions(t *testing.T, options daemonRecoveryContinuationSeedOptions) *daemonRecoveryContinuationFixture {
	t.Helper()
	home := t.TempDir()
	agent := newRecordingRuntimeAgent("recovered")
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		Config:       config.NewConfigService(home),
		ThreadExtras: NewThreadExtraService(),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		DefaultCWD:   home,
	})
	router.SetNotificationSink(sink)
	t.Cleanup(func() { _ = router.Close() })

	fixture := &daemonRecoveryContinuationFixture{home: home, router: router, agent: agent, sink: sink, options: options}
	fixture.seed(t)
	return fixture
}

// seed writes the restored thread's rollout: the interrupted regular turn with
// its model-context record, which is what the continuation gates and the saved
// turn-start options read.
func (f *daemonRecoveryContinuationFixture) seed(t *testing.T) {
	t.Helper()
	f.cwd = t.TempDir()
	f.threadID = "thread-daemon-continue"
	now := fixedTime()
	record := &session.Record{
		ID:        session.ThreadID(f.threadID),
		SessionID: f.threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD: f.cwd,
			// Go persists the sandbox mode in the SandboxMode vocabulary; the turn
			// context records the analytics tag. Both normalize to the same
			// canonical tag, which is what the permission gate compares.
			SandboxPolicy: "workspace-write",
			SessionPrefix: session.PrefixForSessionID(f.threadID),
			// A thread that selects no environment resolves to the implicit
			// local one, so production persists no environment selection and
			// the continuation gate re-derives the local identity from the
			// record, exactly like the daemon snapshot.
			Extra: map[string]any{
				"runtime_workspace_roots": []string{f.cwd},
			},
		},
		Items: []session.Item{{ID: runtimeUserPromptSessionItemID(daemonRecoverySavedTurnID), Type: "message", Role: "user", Text: "finish the work"}},
	}
	store := f.router.services.ThreadRouter.store
	if err := store.Create(record); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := f.router.services.ThreadRouter.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	steps := []func(*rollout.Recorder) error{
		// The interrupted turn started...
		func(recorder *rollout.Recorder) error {
			return recorder.AppendTurnStartedWithRoot("root-a", daemonRecoverySavedTurnID, now)
		},
	}
	if !f.options.omitTurnContext {
		// ...and persisted its model-visible context record.
		steps = append(steps, func(recorder *rollout.Recorder) error {
			return recorder.AppendTurnContext(rollout.TurnContextRecord{
				TurnID:        daemonRecoverySavedTurnID,
				RootTurnID:    "root-a",
				CWD:           f.cwd,
				SandboxPolicy: "workspace_write",
				Model:         "gpt-5",
				Summary:       rollout.TurnContextSummaryPlaceholder,
			}, now)
		})
	}
	f.appendRolloutLines(t, record, steps...)
	tier := "priority"
	f.saved = daemonrecovery.InterruptedTurn{
		TurnID:       daemonRecoverySavedTurnID,
		ServiceTier:  &tier,
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		LocalEnvironment: json.RawMessage(`{"environmentId":"local","cwd":` + jsonString(f.cwd) +
			`,"runtimeWorkspaceRoots":[` + jsonString(f.cwd) + `]}`),
	}
	if f.options.skipResume {
		return
	}
	// The restore resumes the saved thread into this generation's owner
	// (Phase B), which is what makes it a continuation candidate.
	if _, err := f.router.resumeDaemonThread(f.threadID); err != nil {
		t.Fatalf("resumeDaemonThread() error = %v", err)
	}
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (f *daemonRecoveryContinuationFixture) record(t *testing.T) *session.Record {
	t.Helper()
	record, err := f.router.services.ThreadRouter.store.Read(session.ThreadID(f.threadID), true, true)
	if err != nil || record == nil {
		t.Fatalf("store.Read() error = %v", err)
	}
	return record
}

func (f *daemonRecoveryContinuationFixture) appendRolloutLines(t *testing.T, record *session.Record, apply ...func(*rollout.Recorder) error) {
	t.Helper()
	path := f.router.services.ThreadRouter.threadRolloutPath(record)
	recorder, err := rollout.Resume(path)
	if err != nil {
		t.Fatalf("rollout.Resume() error = %v", err)
	}
	for _, step := range apply {
		if err := step(recorder); err != nil {
			t.Fatalf("append rollout line error = %v", err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("recorder.Close() error = %v", err)
	}
}

func (f *daemonRecoveryContinuationFixture) lines(t *testing.T) []rollout.Line {
	t.Helper()
	path := f.router.services.ThreadRouter.threadRolloutPath(f.record(t))
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("rollout.Load() error = %v", err)
	}
	return lines
}

func (f *daemonRecoveryContinuationFixture) turnAborts(t *testing.T) []daemonRecoveryRolloutEvent {
	t.Helper()
	aborted := []daemonRecoveryRolloutEvent{}
	for _, line := range f.lines(t) {
		event, kind, ok := daemonRecoveryDecodeEvent(&line)
		if ok && kind == "turn_aborted" {
			aborted = append(aborted, event)
		}
	}
	return aborted
}

func (f *daemonRecoveryContinuationFixture) startedTurnIDs(t *testing.T) []string {
	t.Helper()
	ids := []string{}
	for _, line := range f.lines(t) {
		event, kind, ok := daemonRecoveryDecodeEvent(&line)
		if ok && kind == "turn_started" {
			ids = append(ids, daemonRecoveryEventTurnID(event))
		}
	}
	return ids
}

// The interrupted turn continues: the saved turn is closed in persisted history
// first, and the continuation starts a new turn whose model input carries the
// hidden recovery fragment, the saved output schema, the saved tier and the
// saved root turn id.
func TestDaemonRecoveryContinuationStartsTurnLikeRust(t *testing.T) {
	fixture := newDaemonRecoveryContinuationFixture(t)

	fixture.router.continueDaemonTurn(fixture.threadID, fixture.saved)

	request := waitForRuntimeAgentRequest(t, fixture.agent)

	// Rust: the continuation has no user message; it carries the internal
	// context fragment instead.
	if request.Prompt != "" {
		t.Fatalf("continuation prompt = %q, want no user message", request.Prompt)
	}
	fragmentText := ""
	for _, text := range messageInputTextsForRole(request.InputItems, "user") {
		if strings.Contains(text, `<codex_internal_context source="daemon_recovery">`) {
			fragmentText = text
		}
	}
	if fragmentText == "" {
		t.Fatalf("continuation input items did not carry the daemon recovery fragment: %#v", request.InputItems)
	}
	if !strings.Contains(fragmentText, daemonRecoveryContinuationBody) {
		t.Fatalf("fragment text = %q, want the recovery body", fragmentText)
	}
	if !strings.Contains(fragmentText, "</codex_internal_context>") {
		t.Fatalf("fragment text = %q, want the internal-context close marker", fragmentText)
	}
	// Rust passes the saved tier through the continuation's start options.
	if request.ServiceTier != "priority" {
		t.Fatalf("continuation service tier = %q, want the saved priority tier", request.ServiceTier)
	}

	// The saved turn is closed in persisted history before the new turn runs.
	aborts := fixture.turnAborts(t)
	if len(aborts) != 1 || daemonRecoveryEventTurnID(aborts[0]) != daemonRecoverySavedTurnID {
		t.Fatalf("turn_aborted lines = %#v, want exactly one for the saved turn", aborts)
	}
	started := fixture.startedTurnIDs(t)
	if len(started) != 2 || started[0] != daemonRecoverySavedTurnID || started[1] == daemonRecoverySavedTurnID {
		t.Fatalf("started turn ids = %#v, want the saved turn then a new one", started)
	}
	// The continuation inherits the saved turn's root lineage (Rust
	// TurnStartOptions.root_turn_id = previous_context.root_turn_id).
	rootLine := ""
	for _, line := range fixture.lines(t) {
		event, kind, ok := daemonRecoveryDecodeEvent(&line)
		if !ok || kind != "turn_started" || daemonRecoveryEventTurnID(event) != started[1] {
			continue
		}
		var payload struct {
			RootTurnID string `json:"root_turn_id"`
		}
		if err := json.Unmarshal(line.Payload, &payload); err == nil {
			rootLine = strings.TrimSpace(payload.RootTurnID)
		}
	}
	if rootLine != "root-a" {
		t.Fatalf("continuation root_turn_id = %q, want the saved root-a", rootLine)
	}

	// Rust emits a "Resuming interrupted work" thread warning once the
	// continuation starts.
	warned := false
	for _, notification := range fixture.sink.List() {
		if notification.Method != NotificationWarning {
			continue
		}
		if warning, ok := notification.Params.(*WarningNotification); ok && warning.Message == daemonRecoveryResumeWarning {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no %q warning was emitted: %#v", daemonRecoveryResumeWarning, fixture.sink.List())
	}
}

// The continuation's start options restore the saved turn exactly: the recovery
// trigger, the saved output schema, the saved tier and the saved root turn id,
// with the hidden fragment as the only input.
func TestDaemonRecoveryContinuationParamsLikeRust(t *testing.T) {
	tier := "priority"
	saved := daemonrecovery.InterruptedTurn{
		TurnID:             "turn-1",
		ServiceTier:        &tier,
		OutputSchema:       json.RawMessage(`{"type":"object","additionalProperties":false}`),
		CyberAccessProgram: json.RawMessage(`"security_research"`),
	}
	previous := &rollout.TurnContextRecord{TurnID: "turn-1", RootTurnID: "root-a"}

	params := daemonRecoveryContinuationParams("thread-1", previous, saved)
	if params.ThreadID != "thread-1" || params.TurnTrigger != daemonRecoveryTurnTrigger {
		t.Fatalf("params = %#v, want the daemon recovery trigger", params)
	}
	if params.RootTurnID != "root-a" {
		t.Fatalf("root turn id = %q, want root-a", params.RootTurnID)
	}
	if params.ServiceTier == nil || *params.ServiceTier != "priority" {
		t.Fatalf("service tier = %#v, want priority", params.ServiceTier)
	}
	if params.CoreCyberAccessProgram != "security_research" {
		t.Fatalf("cyber program = %q, want security_research", params.CoreCyberAccessProgram)
	}
	schema, ok := params.OutputSchema.(map[string]any)
	if !ok || schema["additionalProperties"] != false {
		t.Fatalf("output schema = %#v, want the saved schema", params.OutputSchema)
	}
	if len(params.AdditionalInputItems) != 1 {
		t.Fatalf("input items = %#v, want exactly the recovery fragment", params.AdditionalInputItems)
	}
	texts := messageInputTextsForRole(params.AdditionalInputItems, "user")
	if len(texts) != 1 || !strings.Contains(texts[0], `<codex_internal_context source="daemon_recovery">`) {
		t.Fatalf("fragment texts = %#v, want the internal daemon_recovery context", texts)
	}
}

// Every gate fails closed. A stale snapshot must never continue finished,
// superseded, re-authorized or non-local work.
func TestDaemonRecoveryContinuationGatesLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, f *daemonRecoveryContinuationFixture)
	}{
		{
			// Rust daemon_continuation.rs:48-62: the old process may have finished
			// the turn during the shutdown grace period.
			name: "saved turn already completed",
			mutate: func(t *testing.T, f *daemonRecoveryContinuationFixture) {
				f.appendRolloutLines(t, f.record(t), func(recorder *rollout.Recorder) error {
					return recorder.AppendTurnComplete(daemonRecoverySavedTurnID, fixedTime(), 0)
				})
			},
		},
		{
			// A newer turn replaced the saved one, so the snapshot is stale.
			name: "saved turn superseded by a newer started turn",
			mutate: func(t *testing.T, f *daemonRecoveryContinuationFixture) {
				f.appendRolloutLines(t, f.record(t), func(recorder *rollout.Recorder) error {
					return recorder.AppendTurnStarted("turn-2", fixedTime())
				})
			},
		},
		{
			name: "saved turn already aborted",
			mutate: func(t *testing.T, f *daemonRecoveryContinuationFixture) {
				f.appendRolloutLines(t, f.record(t), func(recorder *rollout.Recorder) error {
					return recorder.AppendTurnAborted(daemonRecoverySavedTurnID, "interrupted", fixedTime(), 0)
				})
			},
		},
		{
			// Gate 4: a snapshot without environment identity is a legacy
			// snapshot and is reloaded without a continuation.
			name: "legacy snapshot without environment identity",
			mutate: func(t *testing.T, f *daemonRecoveryContinuationFixture) {
				f.saved.LocalEnvironment = nil
			},
		},
		{
			// Gate 4: the restored environment is not the saved one.
			name: "environment mismatch",
			mutate: func(t *testing.T, f *daemonRecoveryContinuationFixture) {
				f.saved.LocalEnvironment = json.RawMessage(`{"environmentId":"local","cwd":` + jsonString(t.TempDir()) +
					`,"runtimeWorkspaceRoots":[` + jsonString(f.cwd) + `]}`)
			},
		},
		{
			// Gate 4: only local execution continues automatically.
			name: "remote environment identity",
			mutate: func(t *testing.T, f *daemonRecoveryContinuationFixture) {
				f.saved.LocalEnvironment = json.RawMessage(`{"environmentId":"remote-1","cwd":` + jsonString(f.cwd) +
					`,"runtimeWorkspaceRoots":[` + jsonString(f.cwd) + `]}`)
			},
		},
		{
			// Gate 5 (Rust daemon_continuation.rs:84-87): recovery must not
			// override a stricter saved or newly configured policy.
			name: "permission profile changed",
			mutate: func(t *testing.T, f *daemonRecoveryContinuationFixture) {
				if _, err := f.router.runtimeUpdateThreadMetadata(session.ThreadID(f.threadID), &session.MetadataPatch{SandboxPolicy: stringPtrIfNotEmpty("read-only")}, true); err != nil {
					t.Fatalf("runtimeUpdateThreadMetadata() error = %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newDaemonRecoveryContinuationFixture(t)
			tc.mutate(t, fixture)
			abortsBefore := len(fixture.turnAborts(t))
			startedBefore := len(fixture.startedTurnIDs(t))

			fixture.router.continueDaemonTurn(fixture.threadID, fixture.saved)

			if aborts := fixture.turnAborts(t); len(aborts) != abortsBefore {
				t.Fatalf("turn_aborted lines = %#v, want the gate to fire before closing the saved turn", aborts)
			}
			if ids := fixture.startedTurnIDs(t); len(ids) != startedBefore {
				t.Fatalf("started turn ids = %#v, want no continuation", ids)
			}
			assertNoRuntimeAgentRequest(t, fixture.agent)
		})
	}
}

// Gate 3: without the saved turn's model-context record there is no baseline to
// restore, so the continuation is skipped (Rust daemon_continuation.rs:63-73).
func TestDaemonRecoveryContinuationRequiresTurnContextLikeRust(t *testing.T) {
	fixture := newDaemonRecoveryContinuationFixtureWithOptions(t, daemonRecoveryContinuationSeedOptions{omitTurnContext: true})
	startedBefore := len(fixture.startedTurnIDs(t))

	fixture.router.continueDaemonTurn(fixture.threadID, fixture.saved)

	if ids := fixture.startedTurnIDs(t); len(ids) != startedBefore {
		t.Fatalf("started turn ids = %#v, want no continuation without a turn context", ids)
	}
	if aborts := fixture.turnAborts(t); len(aborts) != 0 {
		t.Fatalf("turn_aborted lines = %#v, want the gate to fire before closing the saved turn", aborts)
	}
	assertNoRuntimeAgentRequest(t, fixture.agent)
}

// The idle guard: a thread with an active turn is never continued (Rust
// `continue_turn_if_idle` -> NotSubmittedReason::NotIdle). The saved turn is
// still closed first, matching Rust's ordering.
func TestDaemonRecoveryContinuationSkipsBusyThreadLikeRust(t *testing.T) {
	fixture := newDaemonRecoveryContinuationFixture(t)
	if err := fixture.router.threads.RegisterTurn(fixture.threadID, "turn-busy", nil, 0, &turn.TurnStartParams{ThreadID: fixture.threadID}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}

	fixture.router.continueDaemonTurn(fixture.threadID, fixture.saved)

	if aborts := fixture.turnAborts(t); len(aborts) != 1 {
		t.Fatalf("turn_aborted lines = %#v, want the saved turn closed before the idle check", aborts)
	}
	if ids := fixture.startedTurnIDs(t); len(ids) != 1 {
		t.Fatalf("started turn ids = %#v, want no continuation for a busy thread", ids)
	}
	assertNoRuntimeAgentRequest(t, fixture.agent)
}

func assertNoRuntimeAgentRequest(t *testing.T, agent *recordingRuntimeAgent) {
	t.Helper()
	select {
	case request := <-agent.requests:
		t.Fatalf("unexpected runtime agent request: %#v", request)
	case <-time.After(200 * time.Millisecond):
	}
}

// A saved thread that is not loaded in this generation is not a continuation
// candidate (Rust daemon_continuation.rs:27 `get_thread`).
func TestDaemonRecoveryContinuationRequiresLoadedThreadLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		ThreadExtras: NewThreadExtraService(),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		DefaultCWD:   home,
	})
	defer func() { _ = router.Close() }()

	router.continueDaemonTurn("thread-unknown", daemonrecovery.InterruptedTurn{TurnID: "turn-1"})
	if router.threads.HasLiveThread("thread-unknown") {
		t.Fatal("an unloaded thread must not become live through the continuation")
	}
}

// The managed restore wires the saved interrupted turns into the continuation:
// it resumes every candidate with `exclude_turns: true` (Phase B) and then
// continues the saved unfinished turn (Rust message_processor.rs:778
// restore_daemon_threads hands `DaemonRecovery(...)` to thread_processor.rs:3690
// / :4015 (calls :3696 / :4016), which call continue_daemon_turn).
func TestDaemonRecoveryRestoreContinuesSavedThreadsLikeRust(t *testing.T) {
	fixture := newDaemonRecoveryContinuationFixtureWithOptions(t, daemonRecoveryContinuationSeedOptions{skipResume: true})

	fixture.router.restoreDaemonThreads(daemonrecovery.Snapshot{
		Loaded:      []string{fixture.threadID},
		Interrupted: map[string]daemonrecovery.InterruptedTurn{fixture.threadID: fixture.saved},
	})

	if !fixture.router.threads.HasLiveThread(session.ThreadID(fixture.threadID)) {
		t.Fatal("the restore did not resume the saved thread")
	}
	request := waitForRuntimeAgentRequest(t, fixture.agent)
	if request.ServiceTier != "priority" {
		t.Fatalf("continuation service tier = %q, want the saved priority tier", request.ServiceTier)
	}
	fragment := false
	for _, text := range messageInputTextsForRole(request.InputItems, "user") {
		if strings.Contains(text, `<codex_internal_context source="daemon_recovery">`) {
			fragment = true
		}
	}
	if !fragment {
		t.Fatalf("restored continuation did not carry the daemon recovery fragment: %#v", request.InputItems)
	}
	if aborts := fixture.turnAborts(t); len(aborts) != 1 || daemonRecoveryEventTurnID(aborts[0]) != daemonRecoverySavedTurnID {
		t.Fatalf("turn_aborted lines = %#v, want exactly one for the saved turn", aborts)
	}
}

// daemonRecoverySandboxTag normalizes both the analytics tag vocabulary Go
// records on the turn context and the SandboxMode vocabulary Go persists on the
// thread settings.
func TestDaemonRecoverySandboxTagNormalizationLikeRust(t *testing.T) {
	cases := []struct {
		raw  any
		want string
	}{
		{raw: "workspace_write", want: "workspace_write"},
		{raw: "workspace-write", want: "workspace_write"},
		{raw: "read_only", want: "read_only"},
		{raw: "read-only", want: "read_only"},
		{raw: "full_access", want: "full_access"},
		{raw: "danger-full-access", want: "full_access"},
		{raw: "external_sandbox", want: "external_sandbox"},
		{raw: map[string]any{"type": "workspace-write"}, want: "workspace_write"},
		{raw: map[string]any{"type": "read-only"}, want: "read_only"},
		{raw: "unknown-mode", want: ""},
		{raw: nil, want: ""},
	}
	for _, tc := range cases {
		if got := daemonRecoverySandboxTagFromPolicy(tc.raw); got != tc.want {
			t.Fatalf("daemonRecoverySandboxTagFromPolicy(%#v) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
