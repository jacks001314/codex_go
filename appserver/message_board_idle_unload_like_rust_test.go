package appserver

import (
	"encoding/json"
	"testing"
	"time"
)

// Rust #63621: the message board connection only injects mail, so it must not
// keep an otherwise idle child thread loaded. Go has no broadcast
// thread-listener attach at all: subscriptions are explicit, and injection
// never joins the injecting connection. This pins that outcome, so a future
// auto-subscribe path cannot silently regress it.
func TestMessageBoardConnectionDoesNotKeepIdleChildLoadedLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 0)
	retainIdleLifecycleThread(t, router, "child")
	router.requireThreadStatus().UpsertThread("child", false)
	router.subscribeThreadConnection("child", "owner")

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex_message_board", Version: "0.0.0"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	})
	initialize.ConnectionID = "board"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error = %+v", response.Error)
	}
	if got := router.clientInfoForConnection("board").Name; got != "codex_message_board" {
		t.Fatalf("board client name = %q, want codex_message_board", got)
	}

	item, err := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "developer",
		"content": []any{map[string]any{"type": "input_text", "text": "mail for the worker"}},
	})
	if err != nil {
		t.Fatalf("marshal injected item: %v", err)
	}
	inject := requestWithParams(t, IntID(2), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: "child",
		Items:    []json.RawMessage{item},
	})
	inject.ConnectionID = "board"
	if response := router.Handle(inject); response.Error != nil {
		t.Fatalf("inject error = %+v", response.Error)
	}

	subscribers := router.threads.Subscribers("child")
	if len(subscribers) != 1 || subscribers[0] != "owner" {
		t.Fatalf("child subscribers = %v, want only the owner connection", subscribers)
	}

	router.scheduleThreadIdleUnload("child")
	time.Sleep(100 * time.Millisecond)
	if router.threads.LiveThread("child") == nil {
		t.Fatal("a child with an active owner subscriber was evicted")
	}
	if !router.unsubscribeThreadConnection("child", "owner") {
		t.Fatal("owner unsubscribe returned false")
	}
	waitForIdleUnload(t, router, "child")
}
