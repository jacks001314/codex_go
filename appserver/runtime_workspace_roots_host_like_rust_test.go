package appserver

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #62377: thread lifecycle roots stay path strings on the wire, and the
// server validates each one with its own host path rules.
func TestThreadLifecycleRejectsRelativeRuntimeWorkspaceRootsLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		DefaultCWD:   t.TempDir(),
	})
	want := "invalid runtimeWorkspaceRoots: path is not absolute: relative-root"

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                   t.TempDir(),
		RuntimeWorkspaceRoots: []string{"relative-root"},
	}))
	if start.Error == nil || !strings.Contains(start.Error.Message, want) {
		t.Fatalf("thread/start error = %+v, want %q", start.Error, want)
	}

	resume := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID:              "missing",
		RuntimeWorkspaceRoots: []string{"relative-root"},
	}))
	if resume.Error == nil || !strings.Contains(resume.Error.Message, want) {
		t.Fatalf("thread/resume error = %+v, want %q", resume.Error, want)
	}

	fork := router.Handle(requestWithParams(t, IntID(3), MethodThreadFork, ThreadForkParams{
		ThreadID:              "missing",
		RuntimeWorkspaceRoots: []string{"relative-root"},
	}))
	if fork.Error == nil || !strings.Contains(fork.Error.Message, want) {
		t.Fatalf("thread/fork error = %+v, want %q", fork.Error, want)
	}

	absolute := t.TempDir()
	accepted := router.Handle(requestWithParams(t, IntID(4), MethodThreadStart, ThreadStartParams{
		CWD:                   t.TempDir(),
		RuntimeWorkspaceRoots: []string{absolute},
	}))
	if accepted.Error != nil {
		t.Fatalf("thread/start absolute roots error = %+v", accepted.Error)
	}
}
