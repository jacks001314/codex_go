package appserver

// Mirrors Rust #62299: an unconfirmed realtime history write marks the thread
// as needing recovery, the host is told, and later frame writes fail closed.

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/realtime"
)

func TestRealtimeHistoryRecoveryRequiredBlocksWritesLikeRust(t *testing.T) {
	router, _, threadID, _ := openPersistedRealtimeThread(t)
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	// A frame whose content cannot be serialized fails the write.
	broken := realtime.RealtimeItem{
		ID:                "broken",
		RealtimeSessionID: "voice-1",
		Content:           realtime.RealtimeItemContent{Kind: realtime.RealtimeItemKindTranscriptSegment, Role: "narrator", Text: "hi"},
	}
	if err := router.persistRealtimeHistoryItems(threadID, []realtime.RealtimeItem{broken}, true); err == nil {
		t.Fatal("unserializable frame was written")
	}
	if !router.realtimeHistoryRecoveryRequired(threadID) {
		t.Fatal("failed write did not require recovery")
	}
	if !sinkWaitsForError(sink, "realtime history recovery required; reload the thread before continuing") {
		t.Fatal("host was not told that recovery is required")
	}

	if !sinkWaitsForClosedReason(sink, realtimeHistoryRecoveryReason) {
		t.Fatal("host was not told which closure reason ended the call")
	}

	// Later writes fail closed until the thread is reloaded and verified.
	valid := realtime.RealtimeItem{
		ID:                "voice-1:started",
		RealtimeSessionID: "voice-1",
		Content:           realtime.RealtimeItemContent{Kind: realtime.RealtimeItemKindSessionStarted},
	}
	if err := router.persistRealtimeHistoryItems(threadID, []realtime.RealtimeItem{valid}, true); err == nil {
		t.Fatal("write succeeded while recovery was required")
	}

	router.ClearRealtimeHistoryRecovery(threadID)
	if router.realtimeHistoryRecoveryRequired(threadID) {
		t.Fatal("clear left the recovery requirement")
	}
	if err := router.persistRealtimeHistoryItems(threadID, []realtime.RealtimeItem{valid}, true); err != nil {
		t.Fatalf("write after recovery = %v", err)
	}
	if router.realtimeHistoryRecoveryRequired(threadID) {
		t.Fatal("successful write required recovery")
	}
}

func sinkWaitsForClosedReason(sink *NotificationBuffer, reason string) bool {
	if sink == nil {
		return false
	}
	for _, notification := range sink.List() {
		if notification == nil || notification.Method != NotificationThreadRealtimeClosed {
			continue
		}
		closed, ok := notification.Params.(*ThreadRealtimeClosedNotification)
		if !ok || closed == nil || closed.Reason == nil {
			continue
		}
		if *closed.Reason == reason {
			return true
		}
	}
	return false
}

func sinkWaitsForError(sink *NotificationBuffer, message string) bool {
	if sink == nil {
		return false
	}
	for _, notification := range sink.List() {
		if notification == nil || string(notification.Method) != string(realtime.NotificationError) {
			continue
		}
		params, err := json.Marshal(notification.Params)
		if err == nil && strings.Contains(string(params), message) {
			return true
		}
	}
	return false
}
