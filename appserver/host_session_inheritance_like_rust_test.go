package appserver

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/session"
)

// Mirrors Rust #61945: a host may put a thread it started into an existing root
// session, for a fresh start and for a fork, while the thread keeps its own id.
// Resumed threads keep their persisted session and native children keep their
// controller's session, so neither surface accepts the field.
func TestHostStartedThreadsInheritSessionIDLikeRust(t *testing.T) {
	rootSession := "session-root-61945"
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                filepath.ToSlash(t.TempDir()),
		Prompt:             "hello",
		InheritedSessionID: &rootSession,
	}))
	if start.Error != nil {
		t.Fatalf("thread/start with inheritedSessionId: %+v", start.Error)
	}
	started := start.Result.(*ThreadStartResponse).Thread
	if started.ID == rootSession {
		t.Fatalf("thread id = %q, want a distinct id", started.ID)
	}
	if started.SessionID != rootSession {
		t.Fatalf("started session = %q, want the inherited root session %q", started.SessionID, rootSession)
	}
	persisted, err := store.Read(session.ThreadID(started.ID), true, true)
	if err != nil {
		t.Fatalf("reload started thread: %v", err)
	}
	if persisted.SessionID != rootSession {
		t.Fatalf("persisted session = %q, want %q", persisted.SessionID, rootSession)
	}

	materializeThreadRolloutForTest(t, router, store, started.ID)
	forkSession := "session-fork-61945"
	fork := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:           started.ID,
		InheritedSessionID: &forkSession,
	}))
	if fork.Error != nil {
		t.Fatalf("thread/fork with inheritedSessionId: %+v", fork.Error)
	}
	forked := fork.Result.(*ThreadForkResponse).Thread
	if forked.ID == forkSession || forked.ID == started.ID {
		t.Fatalf("forked thread id = %q, want its own id", forked.ID)
	}
	if forked.SessionID != forkSession {
		t.Fatalf("forked session = %q, want %q", forked.SessionID, forkSession)
	}
	if forked.ForkedFromID == nil || *forked.ForkedFromID != started.ID {
		t.Fatalf("forkedFromId = %v, want %q", forked.ForkedFromID, started.ID)
	}

	// A resumed thread keeps the session it was created with.
	resume := router.Handle(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{ThreadID: forked.ID}))
	if resume.Error != nil {
		t.Fatalf("thread/resume: %+v", resume.Error)
	}
	if got := resume.Result.(*ThreadResumeResponse).Thread.SessionID; got != forkSession {
		t.Fatalf("resumed session = %q, want the persisted %q", got, forkSession)
	}
}

func TestHostInheritedSessionIDRejectsBlankAndNativeSources(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	blank := "   "
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                filepath.ToSlash(t.TempDir()),
		Prompt:             "hello",
		InheritedSessionID: &blank,
	}))
	if start.Error == nil || !strings.Contains(start.Error.Message, "inheritedSessionId") {
		t.Fatalf("blank inheritedSessionId error = %+v", start.Error)
	}

	subagent := ThreadSourceSubagent
	sessionID := "session-child-61945"
	sub := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD:                filepath.ToSlash(t.TempDir()),
		Prompt:             "child",
		ThreadSource:       &subagent,
		InheritedSessionID: &sessionID,
	}))
	if sub.Error == nil || !strings.Contains(sub.Error.Message, "started directly by the host") {
		t.Fatalf("subagent inheritedSessionId error = %+v", sub.Error)
	}

	// Without the field the thread keeps its own id as the session, unchanged.
	plain := router.Handle(requestWithParams(t, IntID(3), MethodThreadStart, ThreadStartParams{
		CWD:    filepath.ToSlash(t.TempDir()),
		Prompt: "plain",
	}))
	if plain.Error != nil {
		t.Fatalf("thread/start without inheritedSessionId: %+v", plain.Error)
	}
	thread := plain.Result.(*ThreadStartResponse).Thread
	if thread.SessionID != thread.ID {
		t.Fatalf("plain session = %q, want the thread id %q", thread.SessionID, thread.ID)
	}
}
