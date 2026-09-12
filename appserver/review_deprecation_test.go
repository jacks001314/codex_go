package appserver

import (
	"strings"
	"testing"

	"codex_go/review"
	"codex_go/session"
)

// TestRuntimeRouterReviewStartDetachedDeprecationNoticeLikeRust covers Rust
// #42602: `review/start` with `delivery: "detached"` emits a connection-scoped
// deprecation notice, including when validation later rejects the request,
// while omitted/inline delivery stays silent.
func TestRuntimeRouterReviewStartDetachedDeprecationNoticeLikeRust(t *testing.T) {
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

	// A detached review that validation rejects still reports the deprecation,
	// scoped to the requesting connection.
	detached := string(ReviewDeliveryDetached)
	rejected := requestWithParams(t, IntID(2), MethodReviewStart, review.StartParams{
		ThreadID: threadID,
		Target:   review.APITarget{Type: "bogus"},
		Delivery: &detached,
	})
	rejected.ConnectionID = "conn-a"
	if response := router.Handle(rejected); response.Error == nil {
		t.Fatal("invalid detached review was accepted")
	}
	notice := deprecationNoticeForConnection(sink, "conn-a")
	if notice == nil {
		t.Fatal("detached review did not emit a deprecation notice")
	}
	if !strings.Contains(notice.Summary, "delivery \"detached\" is deprecated") {
		t.Fatalf("notice summary = %q", notice.Summary)
	}
	if notice.Details == nil || !strings.Contains(*notice.Details, "thread/start followed by review/start") {
		t.Fatalf("notice details = %#v", notice.Details)
	}
	if other := deprecationNoticeForConnection(sink, "conn-b"); other != nil {
		t.Fatalf("notice leaked to another connection: %#v", other)
	}

	// Inline and omitted delivery stay silent.
	noticesBefore := countDeprecationNoticesForConnection(sink, "conn-a")
	inline := string(ReviewDeliveryInline)
	inlineRequest := requestWithParams(t, IntID(3), MethodReviewStart, review.StartParams{
		ThreadID: threadID,
		Target:   review.APITarget{Type: "baseBranch", Branch: "main"},
		Delivery: &inline,
	})
	inlineRequest.ConnectionID = "conn-a"
	if response := router.Handle(inlineRequest); response.Error != nil {
		t.Fatalf("inline review start = %+v", response.Error)
	}
	if after := countDeprecationNoticesForConnection(sink, "conn-a"); after != noticesBefore {
		t.Fatalf("inline review emitted a deprecation notice (count %d -> %d)", noticesBefore, after)
	}
}

func countDeprecationNoticesForConnection(sink *targetedNotificationTestSink, connectionID string) int {
	if sink == nil {
		return 0
	}
	count := 0
	for _, record := range sink.List() {
		if record.notification == nil || record.notification.Method != NotificationDeprecationNotice {
			continue
		}
		if strings.TrimSpace(record.connectionID) == strings.TrimSpace(connectionID) {
			count++
		}
	}
	return count
}

func deprecationNoticeForConnection(sink *targetedNotificationTestSink, connectionID string) *DeprecationNoticeNotification {
	if sink == nil {
		return nil
	}
	for _, record := range sink.List() {
		if record.notification == nil || record.notification.Method != NotificationDeprecationNotice {
			continue
		}
		if strings.TrimSpace(record.connectionID) != strings.TrimSpace(connectionID) {
			continue
		}
		if params, ok := record.notification.Params.(*DeprecationNoticeNotification); ok {
			return params
		}
	}
	return nil
}
