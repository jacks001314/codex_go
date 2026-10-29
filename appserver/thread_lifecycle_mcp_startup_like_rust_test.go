package appserver

import (
	"testing"
	"time"
)

// Mirrors Rust's #50380 regression: required MCP startup can outlast the
// requesting connection. The completed start must not attach the closed
// connection, and the retained thread still carries its idle lifecycle so it
// unloads, disappears from thread/loaded/list and broadcasts thread/closed.
func TestThreadStartFinishingAfterConnectionCloseStillUnloadsLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 1)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	retainIdleLifecycleThread(t, router, "child")

	// The connection cleanup runs while required MCP startup is still blocked.
	router.ConnectionClosed("conn-owner")

	// Startup completes afterwards and publishes the thread it created.
	router.markResponseThreadLoaded(&ThreadStartResponse{Thread: &Thread{ID: "child"}}, "conn-owner")
	if subscribers := router.threads.Subscribers("child"); len(subscribers) != 0 {
		t.Fatalf("closed connection was re-subscribed: %#v", subscribers)
	}

	waitForIdleUnload(t, router, "child")
	if loaded := router.requireThreadStatus().LoadedThreadIDs(); len(loaded) != 0 {
		t.Fatalf("thread/loaded/list = %#v, want empty", loaded)
	}
	closed := latestNotificationPayload[ThreadClosedNotification](t, sink, NotificationThreadClosed)
	if closed.ThreadID != "child" {
		t.Fatalf("thread/closed payload = %+v", closed)
	}
}

// A live connection keeps the ordinary subscribe-then-idle path: the thread
// stays loaded while the subscriber is attached and unloads after it leaves.
func TestThreadStartWithLiveConnectionSubscribesThenUnloadsLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 1)
	retainIdleLifecycleThread(t, router, "child")
	router.markResponseThreadLoaded(&ThreadStartResponse{Thread: &Thread{ID: "child"}}, "conn-live")

	subscribers := router.threads.Subscribers("child")
	if len(subscribers) != 1 || subscribers[0] != "conn-live" {
		t.Fatalf("subscribers = %#v, want the live connection", subscribers)
	}
	time.Sleep(150 * time.Millisecond)
	if router.threads.LiveThread("child") == nil {
		t.Fatal("a subscribed thread was evicted")
	}
	if !router.unsubscribeThreadConnection("child", "conn-live") {
		t.Fatal("unsubscribe returned false")
	}
	waitForIdleUnload(t, router, "child")
}

// A transport id reused by a new initialize is live again, so the closure mark
// must not outlive the connection it described.
func TestInitializeReopensClosedConnectionLikeRust(t *testing.T) {
	router, _ := newIdleLifecycleTestRouter(t, 1)
	router.ConnectionClosed("conn-reopen")
	if !router.connectionClosed("conn-reopen") {
		t.Fatal("connection closure was not recorded")
	}
	initializeExperimentalConnection(t, router, "conn-reopen")
	if router.connectionClosed("conn-reopen") {
		t.Fatal("initialize did not clear the recorded closure")
	}
	retainIdleLifecycleThread(t, router, "child")
	router.markResponseThreadLoaded(&ThreadStartResponse{Thread: &Thread{ID: "child"}}, "conn-reopen")
	if subscribers := router.threads.Subscribers("child"); len(subscribers) != 1 || subscribers[0] != "conn-reopen" {
		t.Fatalf("subscribers = %#v, want the reinitialized connection", subscribers)
	}
}
