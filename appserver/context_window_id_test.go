package appserver

import "testing"

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

func TestNewContextWindowIDIsUUIDShaped(t *testing.T) {
	id := newContextWindowID()
	if len(id) != 36 {
		t.Fatalf("expected a 36-char UUID, got %q (len %d)", id, len(id))
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
