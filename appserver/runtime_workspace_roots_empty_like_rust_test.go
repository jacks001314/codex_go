package appserver

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #43848: omitted runtimeWorkspaceRoots preserve the saved
// selection and retarget its old cwd root, while an explicit empty list clears
// additional roots and keeps only the effective cwd.
func TestRuntimeRouterRuntimeWorkspaceRootsEmptyVsOmittedLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	extra := t.TempDir()
	nextCWD := t.TempDir()
	store := session.NewStore(home)
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		DefaultCWD:   cwd,
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("done"),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	t.Cleanup(func() { _ = router.Close() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                   cwd,
		Prompt:                "workspace roots",
		RuntimeWorkspaceRoots: []string{cwd, extra},
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error = %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID:              threadID,
		Prompt:                "materialize roots",
		RuntimeWorkspaceRoots: []string{cwd, extra},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start error = %+v", turnStart.Error)
	}
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)

	resumeRaw := func(id int64, cwdValue *string, roots *[]string) *Request {
		params := map[string]any{"threadId": threadID}
		if cwdValue != nil {
			params["cwd"] = *cwdValue
		}
		if roots != nil {
			params["runtimeWorkspaceRoots"] = *roots
		}
		encoded, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		return &Request{JSONRPC: "2.0", ID: IntID(id), Method: MethodThreadResume, Params: encoded}
	}

	omitted := router.Handle(resumeRaw(3, &nextCWD, nil))
	if omitted.Error != nil {
		t.Fatalf("resume omitted roots error = %+v", omitted.Error)
	}
	assertRuntimeRoots(t, omitted.Result.(*ThreadResumeResponse).RuntimeWorkspaceRoots, nextCWD, extra)

	empty := []string{}
	cleared := router.Handle(resumeRaw(4, &cwd, &empty))
	if cleared.Error != nil {
		t.Fatalf("resume explicit empty roots error = %+v", cleared.Error)
	}
	assertRuntimeRoots(t, cleared.Result.(*ThreadResumeResponse).RuntimeWorkspaceRoots, cwd)
}

func assertRuntimeRoots(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("runtime roots = %#v, want %#v", got, want)
	}
	for index := range want {
		if !sameAppPath(got[index], want[index]) {
			t.Fatalf("runtime roots = %#v, want %#v", got, want)
		}
		if filepath.Clean(got[index]) == "" {
			t.Fatalf("runtime root %d is empty", index)
		}
	}
}

// Mirrors Rust #43848: restored roots are retargeted before host validation,
// saved foreign paths fail closed unless the request overrides them, and an
// override is checkpointed so a later resume without a turn restores it.
func TestThreadResumeRestoredRuntimeWorkspaceRootsValidateHostLikeRust(t *testing.T) {
	foreignRoot := foreignRuntimeWorkspaceRoot()
	localCWD := t.TempDir()
	overrideRoot := t.TempDir()
	router, _, _ := newResumeRootsFixture(t, localCWD, map[string]any{
		"runtime_workspace_roots": []string{foreignRoot},
	})

	failed := router.Handle(resumeRootsRequest(t, 1, "thread-roots", nil))
	if failed.Error == nil || failed.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("foreign roots resume error = %+v", failed.Error)
	}
	if !strings.Contains(failed.Error.Message, "cannot restore workspace root") || !strings.Contains(failed.Error.Message, foreignRoot) {
		t.Fatalf("foreign roots error = %q", failed.Error.Message)
	}

	overridden := router.Handle(resumeRootsRequest(t, 2, "thread-roots", &[]string{overrideRoot}))
	if overridden.Error != nil {
		t.Fatalf("explicit override error = %+v", overridden.Error)
	}
	assertRuntimeRoots(t, overridden.Result.(*ThreadResumeResponse).RuntimeWorkspaceRoots, overrideRoot)

	// The cold resume checkpoint makes the override the saved selection even
	// though no turn ran after it.
	restored := router.Handle(resumeRootsRequest(t, 3, "thread-roots", nil))
	if restored.Error != nil {
		t.Fatalf("restored override error = %+v", restored.Error)
	}
	assertRuntimeRoots(t, restored.Result.(*ThreadResumeResponse).RuntimeWorkspaceRoots, overrideRoot)

	empty := []string{}
	cleared := router.Handle(resumeRootsRequest(t, 4, "thread-roots", &empty))
	if cleared.Error != nil {
		t.Fatalf("explicit empty error = %+v", cleared.Error)
	}
	assertRuntimeRoots(t, cleared.Result.(*ThreadResumeResponse).RuntimeWorkspaceRoots, localCWD)
}

// The latest thread-owned settings snapshot owns the root selection even when
// it has none; startup metadata is only a fallback when no snapshot exists.
func TestThreadResumePrefersOwnedSettingsSnapshotRootsLikeRust(t *testing.T) {
	startupRoot := t.TempDir()
	snapshotRoot := t.TempDir()
	localCWD := t.TempDir()

	t.Run("snapshot roots win", func(t *testing.T) {
		router, _, record := newResumeRootsFixture(t, localCWD, map[string]any{
			"runtime_workspace_roots": []string{startupRoot},
		})
		roots := []string{snapshotRoot}
		appendResumeSettingsSnapshot(t, router, record, localCWD, &roots)
		response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
			ThreadID:     string(record.ID),
			ExcludeTurns: true,
		}))
		if response.Error != nil {
			t.Fatalf("resume error = %+v", response.Error)
		}
		assertRuntimeRoots(t, response.Result.(*ThreadResumeResponse).RuntimeWorkspaceRoots, snapshotRoot)
	})

	t.Run("snapshot without roots stays authoritative", func(t *testing.T) {
		router, _, record := newResumeRootsFixture(t, localCWD, map[string]any{
			"runtime_workspace_roots": []string{startupRoot},
		})
		appendResumeSettingsSnapshot(t, router, record, localCWD, nil)
		response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
			ThreadID:     string(record.ID),
			ExcludeTurns: true,
		}))
		if response.Error != nil {
			t.Fatalf("resume error = %+v", response.Error)
		}
		assertRuntimeRoots(t, response.Result.(*ThreadResumeResponse).RuntimeWorkspaceRoots, localCWD)
	})
}

// A thread without a saved selection or settings snapshot resolves its cwd
// only; a cwd recorded on another host is not treated as a restored workspace
// root that must pass validation.
func TestThreadResumeWithoutSavedRootsDoesNotValidateDefaultCWDLikeRust(t *testing.T) {
	foreignCWD := foreignRuntimeWorkspaceRoot()
	router, _, _ := newResumeRootsFixture(t, foreignCWD, nil)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID:     "thread-roots",
		ExcludeTurns: true,
	}))
	if response.Error != nil {
		t.Fatalf("resume without saved roots error = %+v", response.Error)
	}
	result := response.Result.(*ThreadResumeResponse)
	if result.CWD != foreignCWD {
		t.Fatalf("resume cwd = %q, want %q", result.CWD, foreignCWD)
	}
	assertRuntimeRoots(t, result.RuntimeWorkspaceRoots, foreignCWD)
}

func newResumeRootsFixture(t *testing.T, cwd string, extra map[string]any) (*Router, *session.Store, *session.Record) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	threadID := session.ThreadID("thread-roots")
	if extra == nil {
		extra = map[string]any{}
	}
	record := &session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           cwd,
			SessionPrefix: session.PrefixForSessionID(string(threadID)),
			Extra:         extra,
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := router.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	return router, store, record
}

func appendResumeSettingsSnapshot(t *testing.T, router *Router, record *session.Record, cwd string, roots *[]string) {
	t.Helper()
	recorder, err := rollout.Resume(router.threadRolloutPath(record))
	if err != nil {
		t.Fatalf("rollout.Resume() error = %v", err)
	}
	defer recorder.Close()
	if err := recorder.AppendThreadSettingsAppliedWithWorkspaceRoots(string(record.ID), "", cwd, nil, roots, fixedTime()); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedWithWorkspaceRoots() error = %v", err)
	}
}

// resumeRootsRequest encodes runtimeWorkspaceRoots explicitly so an empty
// selection survives JSON encoding instead of being omitted by omitempty.
func resumeRootsRequest(t *testing.T, id int64, threadID string, roots *[]string) *Request {
	t.Helper()
	params := map[string]any{"threadId": threadID, "excludeTurns": true}
	if roots != nil {
		params["runtimeWorkspaceRoots"] = *roots
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return &Request{JSONRPC: "2.0", ID: IntID(id), Method: MethodThreadResume, Params: encoded}
}

// foreignRuntimeWorkspaceRoot returns an absolute path that belongs to another
// host convention on the current platform.
func foreignRuntimeWorkspaceRoot() string {
	if filepath.VolumeName(filepath.Clean(`C:\`)) != "" {
		return "/home/foreign/project"
	}
	return `C:\foreign\project`
}
