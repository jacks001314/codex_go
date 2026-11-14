package tui

import (
	"testing"

	"codex_go/appserver"
	"codex_go/session"
)

// Rust #61183: the session summary carries the selected thread's own fork
// parent, both for persisted records and app-server threads.
func TestSessionSummaryCarriesThreadForkParentLikeRust(t *testing.T) {
	parent := "parent-1"
	summary := sessionSummaryFromAppServerThread(&appserver.Thread{ID: "child", ForkedFromID: &parent}, false)
	if summary.ForkedFromID != "parent-1" {
		t.Fatalf("app-server fork parent = %q", summary.ForkedFromID)
	}

	record := &session.Record{ID: "child", ForkedFromID: session.ThreadID(parent)}
	if got := sessionSummaryFromRecord(nil, record).ForkedFromID; got != "parent-1" {
		t.Fatalf("record fork parent = %q", got)
	}
	if got := sessionSummaryFromAppServerThread(&appserver.Thread{ID: "solo"}, false).ForkedFromID; got != "" {
		t.Fatalf("thread without a parent reported %q", got)
	}
}
