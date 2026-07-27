package appserver

import (
	"errors"
	"testing"

	"codex_go/session"
)

func TestSessionStartTranscriptPathSkipsEphemeralThreadPersistence(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	defer router.Close()

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:       t.TempDir(),
		Ephemeral: true,
	}))
	if response.Error != nil {
		t.Fatalf("thread/start error = %+v", response.Error)
	}
	thread := response.Result.(*ThreadStartResponse).Thread
	record, ok := router.ephemeralThreadRecord(session.ThreadID(thread.ID), true)
	if !ok || record == nil {
		t.Fatalf("ephemeral record = %#v, %t", record, ok)
	}
	if path := router.sessionStartTranscriptPath(record); path != nil {
		t.Fatalf("ephemeral transcript path = %q, want nil", *path)
	}
	if _, err := store.Load(session.ThreadID(thread.ID)); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("ephemeral thread persisted to local store: %v", err)
	}
}
