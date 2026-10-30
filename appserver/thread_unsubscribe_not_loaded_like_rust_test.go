package appserver

import (
	"reflect"
	"testing"
)

// TestThreadUnsubscribeNotLoadedKeepsOtherSubscribersLikeRust mirrors Rust
// #57788: unsubscribing one connection from a not-loaded thread removes only
// that connection. Terminal cleanup happens once the last subscriber is gone,
// so a replacement published by a cold resume is not cleared by an unrelated
// unsubscribe.
func TestThreadUnsubscribeNotLoadedKeepsOtherSubscribersLikeRust(t *testing.T) {
	router, _, _, _ := newRealtimeTailRouter(t)
	threadID := "thread-not-loaded"
	router.threads.Subscribe(threadID, "conn-a")
	router.threads.Subscribe(threadID, "conn-b")

	request := requestWithParams(t, IntID(1), MethodThreadUnsubscribe, ThreadUnsubscribeParams{ThreadID: threadID})
	request.ConnectionID = "conn-a"
	response, err := router.handleThreadUnsubscribeRuntime(request)
	if err != nil {
		t.Fatalf("unsubscribe error = %v", err)
	}
	if response.Status != ThreadUnsubscribeStatusNotLoaded {
		t.Fatalf("unsubscribe status = %q, want notLoaded", response.Status)
	}
	if got, want := router.threads.Subscribers(threadID), []string{"conn-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("subscribers after conn-a unsubscribe = %#v, want %#v", got, want)
	}

	request = requestWithParams(t, IntID(2), MethodThreadUnsubscribe, ThreadUnsubscribeParams{ThreadID: threadID})
	request.ConnectionID = "conn-b"
	if _, err := router.handleThreadUnsubscribeRuntime(request); err != nil {
		t.Fatalf("last unsubscribe error = %v", err)
	}
	if got := router.threads.Subscribers(threadID); len(got) != 0 {
		t.Fatalf("subscribers after last unsubscribe = %#v, want none", got)
	}
}
