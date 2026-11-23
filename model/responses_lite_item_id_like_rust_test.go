package model

import (
	"testing"

	"github.com/google/uuid"
)

// Mirrors Rust #62635 `ThreadContextIds`: base instructions and Responses Lite
// tool definitions keep the same thread-scoped, content-derived UUID when a
// request is rebuilt, while different threads or payloads get distinct IDs.
func TestResponsesLiteItemIDIsThreadScopedAndContentDerivedLikeRust(t *testing.T) {
	payload := []byte(`[{"type":"function","name":"lookup"}]`)
	first := ResponsesLiteItemID("at", payload, "thread-a")
	if first != ResponsesLiteItemID("at", payload, "thread-a") {
		t.Fatal("the same thread and payload must keep one Responses Lite item ID")
	}
	if ResponsesLiteItemID("msg", payload, "thread-a") == first {
		t.Fatal("the item kind prefix must separate IDs")
	}
	if ResponsesLiteItemID("at", payload, "thread-b") == first {
		t.Fatal("a different thread must derive a different ID")
	}
	if ResponsesLiteItemID("at", []byte(`[{"type":"function","name":"other"}]`), "thread-a") == first {
		t.Fatal("a different payload must derive a different ID")
	}

	// The id is a UUIDv5 keyed by the thread id namespace, so it is stable
	// across processes instead of depending on a per-run generator.
	namespace := uuid.NewSHA1(uuid.NameSpaceOID, []byte("thread-a"))
	want := "at_" + uuid.NewSHA1(namespace, payload).String()
	if first != want {
		t.Fatalf("Responses Lite item ID = %q, want %q", first, want)
	}
}
