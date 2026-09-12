package app

import (
	"encoding/json"
	"testing"

	"codex_go/appserver"
	"codex_go/realtime"
	"codex_go/tui/chatwidget"
)

func TestDecodeThreadRealtimeNotifications(t *testing.T) {
	tests := []struct {
		name   string
		method appserver.NotificationMethod
		params string
		want   chatwidget.VoiceNotification
	}{
		{
			name:   "started",
			method: appserver.NotificationThreadRealtimeStarted,
			params: `{"threadId":"t1","version":"v1"}`,
			want:   chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationStarted},
		},
		{
			name:   "sdp",
			method: appserver.NotificationThreadRealtimeSDP,
			params: `{"threadId":"t1","sdp":"v=0 answer"}`,
			want:   chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationSDP, Text: "v=0 answer"},
		},
		{
			name:   "transcript delta",
			method: appserver.NotificationThreadRealtimeTranscriptDelta,
			params: `{"threadId":"t1","role":"user","delta":"hi"}`,
			want: chatwidget.VoiceNotification{
				Kind: chatwidget.VoiceNotificationTranscriptDelta,
				Role: chatwidget.VoiceTranscriptUser,
				Text: "hi",
			},
		},
		{
			name:   "transcript done",
			method: appserver.NotificationThreadRealtimeTranscriptDone,
			params: `{"threadId":"t1","role":"assistant","text":"hello"}`,
			want: chatwidget.VoiceNotification{
				Kind: chatwidget.VoiceNotificationTranscriptDone,
				Role: chatwidget.VoiceTranscriptAssistant,
				Text: "hello",
			},
		},
		{
			name:   "item transcript delta",
			method: appserver.NotificationThreadRealtimeItemTranscriptDelta,
			params: `{"threadId":"t1","itemId":"i1","delta":"chunk"}`,
			want:   chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationItemTranscript, Text: "chunk"},
		},
		{
			name:   "output audio delta",
			method: appserver.NotificationThreadRealtimeOutputAudioDelta,
			params: `{"threadId":"t1","audio":{"data":"","sampleRate":48000,"numChannels":1}}`,
			want:   chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationOutputAudioDelta},
		},
		{
			name:   "error",
			method: appserver.NotificationThreadRealtimeError,
			params: `{"threadId":"t1","message":"boom"}`,
			want:   chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationError, Message: "boom"},
		},
		{
			name:   "closed trims reason",
			method: appserver.NotificationThreadRealtimeClosed,
			params: `{"threadId":"t1","reason":"  requested  "}`,
			want:   chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationClosed, Reason: "requested"},
		},
		{
			name:   "closed without reason",
			method: appserver.NotificationThreadRealtimeClosed,
			params: `{"threadId":"t1","reason":null}`,
			want:   chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationClosed},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			msg, ok := DecodeThreadRealtimeNotification(test.method, json.RawMessage(test.params))
			if !ok {
				t.Fatalf("notification %s was not decoded", test.method)
			}
			if msg.Notification != test.want {
				t.Fatalf("notification = %#v, want %#v", msg.Notification, test.want)
			}
		})
	}
}

func TestDecodeThreadRealtimeNotificationRejectsUnrelatedInput(t *testing.T) {
	if _, ok := DecodeThreadRealtimeNotification(appserver.NotificationTurnStarted, json.RawMessage(`{}`)); ok {
		t.Fatal("an unrelated notification was decoded as voice")
	}
	if _, ok := DecodeThreadRealtimeNotification(appserver.NotificationThreadRealtimeSDP, json.RawMessage(`{`)); ok {
		t.Fatal("a malformed payload was decoded")
	}
}

func TestDecodeThreadRealtimeItemCompletedOnlyReportsTranscriptSegments(t *testing.T) {
	segment := appserver.ThreadRealtimeItemCompletedNotification{
		ThreadID: "t1",
		Item: realtime.RealtimeItem{
			ID:      "i1",
			Content: realtime.RealtimeItemContent{Kind: realtime.RealtimeItemKindTranscriptSegment, Role: realtime.RealtimeTranscriptRoleUser, Text: "said"},
		},
	}
	params, err := json.Marshal(segment)
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := DecodeThreadRealtimeNotification(appserver.NotificationThreadRealtimeItemCompleted, params)
	if !ok {
		t.Fatal("a transcript segment item was not decoded")
	}
	if msg.Notification.Role != chatwidget.VoiceTranscriptUser || msg.Notification.Text != "said" {
		t.Fatalf("notification = %#v", msg.Notification)
	}

	promotion := appserver.ThreadRealtimeItemCompletedNotification{
		ThreadID: "t1",
		Item: realtime.RealtimeItem{
			ID: "i2",
			Content: realtime.RealtimeItemContent{
				Kind:         realtime.RealtimeItemKindBemItemPromoted,
				TurnID:       "turn-1",
				ItemID:       "agent-1",
				Presentation: realtime.BemItemPresentation{Kind: realtime.BemPresentationWholeItem},
			},
		},
	}
	params, err = json.Marshal(promotion)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := DecodeThreadRealtimeNotification(appserver.NotificationThreadRealtimeItemCompleted, params); ok {
		t.Fatal("a timeline promotion was decoded as a caption")
	}
}
