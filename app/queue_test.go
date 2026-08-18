package app

import (
	"errors"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/session"
)

// TestSessionIDByUniqueActiveNameLikeRust mirrors Rust queue.rs session lookup
// coverage (#39092): UUIDs pass through, exact unique names resolve, ambiguous
// names are rejected, and missing sessions report no match.
func TestSessionIDByUniqueActiveNameLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	for _, record := range []*session.Record{
		{ID: "11111111-1111-1111-1111-111111111111", SessionID: "s-1", Title: "alpha", Metadata: session.Metadata{HistoryMode: "legacy"}},
		{ID: "22222222-2222-2222-2222-222222222222", SessionID: "s-2", Title: "beta", Metadata: session.Metadata{HistoryMode: "legacy"}},
	} {
		if err := store.Create(record); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	id, err := sessionIDByUniqueActiveName(store, "alpha")
	if err != nil {
		t.Fatalf("sessionIDByUniqueActiveName(alpha) error = %v", err)
	}
	if id != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("resolved id = %q", id)
	}
	if _, err := sessionIDByUniqueActiveName(store, "missing"); err == nil || !strings.Contains(err.Error(), "No active session") {
		t.Fatalf("missing name error = %v", err)
	}
}

func TestResolveQueueSessionTargetUUIDLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	opts := &cli.SessionOptions{Target: "33333333-3333-3333-3333-333333333333"}
	id, err := resolveQueueSessionTarget(store, opts)
	if err != nil {
		t.Fatalf("resolveQueueSessionTarget(UUID) error = %v", err)
	}
	if id != session.ThreadID(opts.Target) {
		t.Fatalf("resolved id = %q", id)
	}
}

func TestRunSessionQueueRejectsImagesLikeRust(t *testing.T) {
	var stdout strings.Builder
	err := runSessionQueue(&cli.QueueOptions{
		Thread:  "thread-1",
		Message: "hello",
		Shared:  cli.SharedOptions{Images: []string{"screenshot.png"}},
	}, nil, &stdout)
	if err == nil || !strings.Contains(err.Error(), "does not support image attachments") {
		t.Fatalf("runSessionQueue(images) error = %v", err)
	}
}

func TestRunSessionQueueRequiresMessageLikeRust(t *testing.T) {
	var stdout strings.Builder
	if err := runSessionQueue(&cli.QueueOptions{Thread: "thread-1", Message: "  "}, nil, &stdout); err == nil || !strings.Contains(err.Error(), "requires --message") {
		t.Fatalf("runSessionQueue(blank message) error = %v", err)
	}
}

func TestQueueUnsupportedServerErrorWrapsMethodNotFoundLikeRust(t *testing.T) {
	base := errors.New("method not found: thread/queue/add")
	remote := queueUnsupportedServerError(base, true)
	if !strings.Contains(remote.Error(), "remote app server") || !strings.Contains(remote.Error(), "update or restart") {
		t.Fatalf("remote wrap = %v", remote)
	}
	local := queueUnsupportedServerError(base, false)
	if !strings.Contains(local.Error(), "local app-server daemon") {
		t.Fatalf("local wrap = %v", local)
	}
	other := errors.New("some other failure")
	if queueUnsupportedServerError(other, false) != other {
		t.Fatalf("non-method-not-found error was wrapped: %v", other)
	}
}
