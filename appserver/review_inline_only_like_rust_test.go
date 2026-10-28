package appserver

import (
	"testing"

	"codex_go/review"
	"codex_go/session"
)

// TestRuntimeRouterReviewStartIsInlineOnlyLikeRust covers Rust #57667:
// `delivery: "detached"` is no longer a protocol variant, so it is rejected
// without a deprecation notice, while omitted and "inline" delivery return the
// supplied thread id.
func TestRuntimeRouterReviewStartIsInlineOnlyLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := &targetedNotificationTestSink{}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Reviews:      review.NewService(),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	initializeAttestationTestConnection(t, router, "conn-a", false)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start = %+v", threadStart)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	detached := "detached"
	rejected := requestWithParams(t, IntID(2), MethodReviewStart, review.StartParams{
		ThreadID: threadID,
		Target:   review.APITarget{Type: "custom", Instructions: "detached review"},
		Delivery: &detached,
	})
	rejected.ConnectionID = "conn-a"
	response := router.Handle(rejected)
	if response.Error == nil || response.Error.Code != -32602 {
		t.Fatalf("detached review = %+v, want invalid params", response)
	}
	if got := response.Error.Message; got != `invalid review request: unknown review delivery variant "detached", expected "inline"` {
		t.Fatalf("detached review error = %q", got)
	}
	if count := countDeprecationNoticesForConnection(sink, "conn-a"); count != 0 {
		t.Fatalf("detached review emitted %d deprecation notices", count)
	}
	if _, err := store.Read(session.ThreadID(threadID), true, false); err != nil {
		t.Fatalf("parent thread disappeared: %v", err)
	}

	inline := "inline"
	for _, delivery := range []*string{nil, &inline} {
		start := requestWithParams(t, IntID(3), MethodReviewStart, review.StartParams{
			ThreadID: threadID,
			Target:   review.APITarget{Type: "baseBranch", Branch: "main"},
			Delivery: delivery,
		})
		start.ConnectionID = "conn-a"
		result := router.Handle(start)
		if result.Error != nil {
			t.Fatalf("review start delivery=%v = %+v", delivery, result.Error)
		}
		payload := result.Result.(*review.StartResponse)
		if payload.ReviewThreadID != threadID {
			t.Fatalf("reviewThreadId = %q, want parent %q", payload.ReviewThreadID, threadID)
		}
	}
}
