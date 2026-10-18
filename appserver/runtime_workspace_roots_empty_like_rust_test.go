package appserver

import (
	"encoding/json"
	"path/filepath"
	"testing"

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
