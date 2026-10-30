package appserver

import "testing"

// Mirrors Rust #57824: directly attached threads extend the spawned subtree
// without duplicating ids, and the bounded selection keeps them.
func TestFeedbackThreadIDsIncludeAttachedThreadsLikeRust(t *testing.T) {
	subtree := FeedbackThreadIDs("root", []string{"spawned"})
	merged := appendUniqueThreadIDs(subtree, []string{"attached", "root", "spawned", "  "})
	want := []string{"root", "spawned", "attached"}
	if len(merged) != len(want) {
		t.Fatalf("merged ids = %#v, want %#v", merged, want)
	}
	for index, id := range want {
		if merged[index] != id {
			t.Fatalf("merged ids = %#v, want %#v", merged, want)
		}
	}

	selected, index := selectFeedbackTreeThreads("root", merged, guardianReviewFailures{})
	if len(selected) != 3 || selected[0] != "root" {
		t.Fatalf("selected = %#v", selected)
	}
	found := false
	for _, thread := range index.Threads {
		if thread.ThreadID == "attached" {
			found = true
		}
	}
	if !found {
		t.Fatalf("thread index dropped the attached thread: %#v", index.Threads)
	}
}
