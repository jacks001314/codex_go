package appserver

import (
	"reflect"
	"testing"
)

// TestRuntimeAgentMailboxSurvivesUnloadAndDiscardsOnRemovalLikeRust covers
// #50087: queue-only mail is independent of the loaded session, retains order
// across eviction, and is discarded when the agent or runtime is removed.
func TestRuntimeAgentMailboxSurvivesUnloadAndDiscardsOnRemovalLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID = "mailbox-thread"
	router.enqueueRuntimeAgentMessage(threadID, "first")
	router.markThreadUnloaded(threadID)
	router.enqueueRuntimeAgentMessage(threadID, "second")
	if got, want := router.drainRuntimeAgentMessages(threadID), []any{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mail after unload = %#v, want %#v", got, want)
	}
	if got := router.drainRuntimeAgentMessages(threadID); len(got) != 0 {
		t.Fatalf("mail was not consumed exactly once: %#v", got)
	}

	router.enqueueRuntimeAgentMessage(threadID, "removed")
	router.discardRuntimeAgentMessages(threadID)
	if got := router.drainRuntimeAgentMessages(threadID); len(got) != 0 {
		t.Fatalf("removed agent retained mail: %#v", got)
	}

	router.enqueueRuntimeAgentMessage("thread-a", "a")
	router.enqueueRuntimeAgentMessage("thread-b", "b")
	router.discardAllRuntimeAgentMessages()
	if got := router.drainRuntimeAgentMessages("thread-a"); len(got) != 0 {
		t.Fatalf("shutdown retained thread-a mail: %#v", got)
	}
	if got := router.drainRuntimeAgentMessages("thread-b"); len(got) != 0 {
		t.Fatalf("shutdown retained thread-b mail: %#v", got)
	}
}
