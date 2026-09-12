package app

// Realtime notification decoding for the local voice session. The TUI owns the
// helper and the app-server only relays the session; this layer turns the
// app-server payloads into the model's voice messages.

import (
	"encoding/json"
	"strings"

	"codex_go/appserver"
	"codex_go/realtime"
	"codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

// DecodeThreadRealtimeNotification maps one app-server realtime notification to
// the TUI's voice message. It reports false for notifications the local session
// does not consume.
func DecodeThreadRealtimeNotification(method appserver.NotificationMethod, params json.RawMessage) (codextea.VoiceNotificationMsg, bool) {
	switch method {
	case appserver.NotificationThreadRealtimeStarted:
		return voiceNotificationMsg(chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationStarted})
	case appserver.NotificationThreadRealtimeSDP:
		var payload appserver.ThreadRealtimeSDPNotification
		if err := json.Unmarshal(params, &payload); err != nil {
			return codextea.VoiceNotificationMsg{}, false
		}
		return voiceNotificationMsg(chatwidget.VoiceNotification{
			Kind: chatwidget.VoiceNotificationSDP,
			Text: payload.SDP,
		})
	case appserver.NotificationThreadRealtimeTranscriptDelta:
		var payload appserver.ThreadRealtimeTranscriptDeltaNotification
		if err := json.Unmarshal(params, &payload); err != nil {
			return codextea.VoiceNotificationMsg{}, false
		}
		return voiceNotificationMsg(chatwidget.VoiceNotification{
			Kind: chatwidget.VoiceNotificationTranscriptDelta,
			Role: voiceTranscriptRole(payload.Role),
			Text: payload.Delta,
		})
	case appserver.NotificationThreadRealtimeTranscriptDone:
		var payload appserver.ThreadRealtimeTranscriptDoneNotification
		if err := json.Unmarshal(params, &payload); err != nil {
			return codextea.VoiceNotificationMsg{}, false
		}
		return voiceNotificationMsg(chatwidget.VoiceNotification{
			Kind: chatwidget.VoiceNotificationTranscriptDone,
			Role: voiceTranscriptRole(payload.Role),
			Text: payload.Text,
		})
	case appserver.NotificationThreadRealtimeItemTranscriptDelta:
		var payload appserver.ThreadRealtimeItemTranscriptDeltaNotification
		if err := json.Unmarshal(params, &payload); err != nil {
			return codextea.VoiceNotificationMsg{}, false
		}
		return voiceNotificationMsg(chatwidget.VoiceNotification{
			Kind: chatwidget.VoiceNotificationItemTranscript,
			Text: payload.Delta,
		})
	case appserver.NotificationThreadRealtimeItemCompleted:
		var payload appserver.ThreadRealtimeItemCompletedNotification
		if err := json.Unmarshal(params, &payload); err != nil {
			return codextea.VoiceNotificationMsg{}, false
		}
		content := payload.Item.Content
		if content.Kind != realtime.RealtimeItemKindTranscriptSegment {
			// Promotions and session boundaries are timeline events, not
			// captions; the flat transcript notifications own caption text.
			return codextea.VoiceNotificationMsg{}, false
		}
		return voiceNotificationMsg(chatwidget.VoiceNotification{
			Kind: chatwidget.VoiceNotificationItemCompleted,
			Role: voiceTranscriptRole(string(content.Role)),
			Text: content.Text,
		})
	case appserver.NotificationThreadRealtimeOutputAudioDelta:
		return voiceNotificationMsg(chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationOutputAudioDelta})
	case appserver.NotificationThreadRealtimeError:
		var payload appserver.ThreadRealtimeErrorNotification
		if err := json.Unmarshal(params, &payload); err != nil {
			return codextea.VoiceNotificationMsg{}, false
		}
		return voiceNotificationMsg(chatwidget.VoiceNotification{
			Kind:    chatwidget.VoiceNotificationError,
			Message: payload.Message,
		})
	case appserver.NotificationThreadRealtimeClosed:
		var payload appserver.ThreadRealtimeClosedNotification
		if err := json.Unmarshal(params, &payload); err != nil {
			return codextea.VoiceNotificationMsg{}, false
		}
		return voiceNotificationMsg(chatwidget.VoiceNotification{
			Kind:   chatwidget.VoiceNotificationClosed,
			Reason: strings.TrimSpace(stringPtrValue(payload.Reason)),
		})
	default:
		return codextea.VoiceNotificationMsg{}, false
	}
}

func voiceNotificationMsg(notification chatwidget.VoiceNotification) (codextea.VoiceNotificationMsg, bool) {
	return codextea.VoiceNotificationMsg{Notification: notification}, true
}

// voiceTranscriptRole normalizes the wire role to the two speakers the TUI
// renders. Unknown roles are treated as the assistant, matching the primary
// speaker.
func voiceTranscriptRole(role string) chatwidget.VoiceTranscriptRole {
	if strings.EqualFold(strings.TrimSpace(role), "user") {
		return chatwidget.VoiceTranscriptUser
	}
	return chatwidget.VoiceTranscriptAssistant
}
