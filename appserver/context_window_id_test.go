package appserver

import (
	"testing"

	"codex_go/protocol"
)

func TestContextWindowIDForThreadIsStableAndAdvancesOnReset(t *testing.T) {
	router := &RuntimeRouter{}
	first := router.contextWindowIDForThread("thread-1")
	if first == "" {
		t.Fatal("context window ID should be generated")
	}
	if got := router.contextWindowIDForThread("thread-1"); got != first {
		t.Fatalf("context window ID should be stable: %q != %q", got, first)
	}
	router.advanceContextWindowID("thread-1")
	if got := router.contextWindowIDForThread("thread-1"); got == first {
		t.Fatal("context window ID should advance after compaction")
	}
	if got := router.contextWindowIDForThread("thread-2"); got == "" || got == first {
		t.Fatalf("thread-2 should get its own distinct ID, got %q", got)
	}
}

func TestNewContextWindowIDIsUUIDv7(t *testing.T) {
	// Rust #59328: context window IDs are UUIDv7 strings.
	id := newContextWindowID()
	parsed, ok := protocol.ParseContextWindowID(id)
	if !ok || len(id) != 36 || parsed.String() != id {
		t.Fatalf("expected a canonical UUIDv7, got %q (ok=%v)", id, ok)
	}
	if _, ok := protocol.ParseContextWindowID("6f1c2d3e-0000-4000-8000-000000000000"); ok {
		t.Fatal("a UUIDv4 value was accepted as a context window id")
	}
	if _, ok := protocol.ParseContextWindowID("context-1"); ok {
		t.Fatal("a non-UUID value was accepted as a context window id")
	}
	if _, ok := protocol.ParseContextWindowID(""); ok {
		t.Fatal("an empty value was accepted as a context window id")
	}
}

func TestWindowNumberForThreadTracksCompactionLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	if got := router.windowNumberForThread("thread-1"); got != 0 {
		t.Fatalf("initial window number = %d, want 0", got)
	}
	router.advanceWindowNumber("thread-1")
	if got := router.windowNumberForThread("thread-1"); got != 1 {
		t.Fatalf("window number after compaction = %d, want 1", got)
	}
	router.advanceWindowNumber("thread-1")
	if got := router.windowNumberForThread("thread-1"); got != 2 {
		t.Fatalf("window number after second compaction = %d, want 2", got)
	}
	// Different thread starts at 0.
	if got := router.windowNumberForThread("thread-2"); got != 0 {
		t.Fatalf("thread-2 window number = %d, want 0", got)
	}
}
