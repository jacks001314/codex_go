package appserver

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	promptctx "codex_go/prompt"
	"codex_go/telemetry"
	"codex_go/turn"
)

// Mirrors Rust #46058: a realtime handoff queues the voice session so the turn
// it starts stays attributed after the realtime session closed, and every turn,
// app-use, skill-invocation, and MCP tool-call event reports it.
func TestVoiceHandoffAttributesAnalyticsAfterRealtimeClosesLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-voice-handoff"
		turnID       = "turn-voice-handoff"
		nextTurnID   = "turn-voice-handoff-text"
	)

	router.notify(NotificationThreadRealtimeStarted, &ThreadRealtimeStartedNotification{
		ThreadID:          threadID,
		RealtimeSessionID: stringPtrIfNotEmpty("work-voice-123"),
	})
	router.notify(NotificationThreadRealtimeItemAdded, &ThreadRealtimeItemAddedNotification{
		ThreadID: threadID,
		Item:     map[string]any{"type": "handoff_request", "input_transcript": "private speech"},
	})
	router.notify(NotificationThreadRealtimeClosed, &ThreadRealtimeClosedNotification{ThreadID: threadID})
	router.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: threadID, Turn: Turn{ID: turnID}})

	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID, connectionID, "drive", "Drive", "gpt-5", nil)
	assertVoiceSessionParam(t, waitForAppUsedEvent(t, sink).EventParams.VoiceSessionID, "work-voice-123")

	router.trackSkillInvocationEvent(context.Background(), threadID, turnID, "gpt-5", "codex-test",
		promptctx.InstructionsSkillMetadata{Name: "alpha", Scope: "user", Path: "/tmp/skills/alpha"}, telemetry.SkillInvocationTypeExplicit)
	select {
	case event := <-sink.skillInvocation:
		assertVoiceSessionParam(t, event.EventParams.VoiceSessionID, "work-voice-123")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for skill invocation analytics")
	}

	router.emitMCPToolCallAnalyticsEvent(context.Background(), connectionID, threadID, turnID, &ThreadItem{
		ID:   "mcp-voice",
		Type: "mcpToolCall",
		Data: map[string]any{"status": "completed", "server": "docs", "tool": "lookup"},
	}, &appTurnRunConfig{})
	assertVoiceSessionParam(t, waitForMCPToolCallAnalyticsEvent(t, sink, turnID).EventParams.VoiceSessionID, "work-voice-123")

	startedAt := time.Now().UTC()
	router.emitCodexTurnAnalyticsEvent(
		context.Background(),
		connectionID,
		&turn.TurnStartParams{ThreadID: threadID},
		&turn.TurnRecord{ID: turnID, ThreadID: threadID, StartedAt: startedAt.Unix()},
		&appTurnRunConfig{SessionID: threadID},
		nil,
		TurnStatusCompleted,
		startedAt,
		startedAt.Add(time.Second),
		1000,
		0,
		nil, nil, nil, nil, nil, nil,
	)
	assertVoiceSessionParam(t, waitForCodexTurnEvent(t, sink, turnID).EventParams.VoiceSessionID, "work-voice-123")

	// The attribution belongs to the handed-off turn only: a later text turn
	// reports no voice session (Rust #46058).
	router.notify(NotificationTurnCompleted, &TurnCompletedNotification{ThreadID: threadID, Turn: Turn{ID: turnID, Status: TurnStatusCompleted}})
	router.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: threadID, Turn: Turn{ID: nextTurnID}})
	router.emitCodexAppUsedEvent(context.Background(), threadID, nextTurnID, connectionID, "drive", "Drive", "gpt-5", nil)
	assertVoiceSessionParam(t, waitForAppUsedEvent(t, sink).EventParams.VoiceSessionID, "")
}

// Mirrors Rust #46058's active-turn steering case: a realtime session that
// starts while a turn runs stamps that turn, and the handoff marker it emits is
// not queued, so the next text turn is not tagged.
func TestVoiceHandoffSteeringActiveTurnDoesNotTagNextTextTurnLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-voice-steer"
		activeTurnID = "turn-voice-steer"
		textTurnID   = "turn-voice-steer-text"
	)

	router.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: threadID, Turn: Turn{ID: activeTurnID}})
	router.notify(NotificationThreadRealtimeStarted, &ThreadRealtimeStartedNotification{
		ThreadID:          threadID,
		RealtimeSessionID: stringPtrIfNotEmpty("work-voice-456"),
	})
	router.notify(NotificationThreadRealtimeItemAdded, &ThreadRealtimeItemAddedNotification{
		ThreadID: threadID,
		Item:     map[string]any{"type": "handoff_request", "input_transcript": "steer here"},
	})
	router.notify(NotificationThreadRealtimeClosed, &ThreadRealtimeClosedNotification{ThreadID: threadID})

	if queue := router.voiceHandoffQueue[threadID]; len(queue) != 0 {
		t.Fatalf("handoff queue = %#v, want empty for an active turn", queue)
	}

	router.emitCodexAppUsedEvent(context.Background(), threadID, activeTurnID, connectionID, "drive", "Drive", "gpt-5", nil)
	assertVoiceSessionParam(t, waitForAppUsedEvent(t, sink).EventParams.VoiceSessionID, "work-voice-456")

	router.notify(NotificationTurnCompleted, &TurnCompletedNotification{ThreadID: threadID, Turn: Turn{ID: activeTurnID, Status: TurnStatusCompleted}})
	router.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: threadID, Turn: Turn{ID: textTurnID}})
	router.emitCodexAppUsedEvent(context.Background(), threadID, textTurnID, connectionID, "drive", "Drive", "gpt-5", nil)
	assertVoiceSessionParam(t, waitForAppUsedEvent(t, sink).EventParams.VoiceSessionID, "")
}

// Mirrors Rust #46058's handoff marker: only the thread id is recorded, never
// the handoff item's transcript, and non-handoff realtime items are ignored.
func TestRealtimeHandoffMarkerRetainsOnlyThreadAttributionLikeRust(t *testing.T) {
	router, _ := newModelAttributionRouter(t)
	const threadID = "thread-voice-marker"
	const transcript = "private speech"

	router.notify(NotificationThreadRealtimeItemAdded, &ThreadRealtimeItemAddedNotification{
		ThreadID: threadID,
		Item:     map[string]any{"type": "input_transcript", "text": transcript},
	})
	if queue := router.voiceHandoffQueue[threadID]; len(queue) != 0 {
		t.Fatalf("non-handoff item queued %#v", queue)
	}

	router.notify(NotificationThreadRealtimeStarted, &ThreadRealtimeStartedNotification{
		ThreadID:          threadID,
		RealtimeSessionID: stringPtrIfNotEmpty("work-voice-789"),
	})
	router.notify(NotificationThreadRealtimeItemAdded, &ThreadRealtimeItemAddedNotification{
		ThreadID: threadID,
		Item:     map[string]any{"type": "handoff_request", "input_transcript": transcript},
	})
	queue := router.voiceHandoffQueue[threadID]
	if len(queue) != 1 || queue[0] != "work-voice-789" {
		t.Fatalf("handoff queue = %#v, want the active voice session id", queue)
	}
	assertNoTranscriptRetained(t, router, transcript)
}

// A handoff requested without an open realtime session still consumes its queue
// slot, so the turn it starts is unattributed even if a session opens later
// (Rust pops the queued Option before falling back to the active session).
func TestVoiceHandoffWithoutActiveSessionStaysUnattributedLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-voice-empty-handoff"
		turnID       = "turn-voice-empty-handoff"
	)

	router.notify(NotificationThreadRealtimeItemAdded, &ThreadRealtimeItemAddedNotification{
		ThreadID: threadID,
		Item:     map[string]any{"type": "handoff_request"},
	})
	router.notify(NotificationThreadRealtimeStarted, &ThreadRealtimeStartedNotification{
		ThreadID:          threadID,
		RealtimeSessionID: stringPtrIfNotEmpty("work-voice-late"),
	})
	router.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: threadID, Turn: Turn{ID: turnID}})

	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID, connectionID, "drive", "Drive", "gpt-5", nil)
	assertVoiceSessionParam(t, waitForAppUsedEvent(t, sink).EventParams.VoiceSessionID, "")
}

// Archiving or closing a thread drops its voice attribution (Rust #46058).
func TestVoiceAttributionClearsWhenThreadArchivedLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-voice-archived"
		turnID       = "turn-voice-archived"
	)

	router.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: threadID, Turn: Turn{ID: turnID}})
	router.notify(NotificationThreadRealtimeStarted, &ThreadRealtimeStartedNotification{
		ThreadID:          threadID,
		RealtimeSessionID: stringPtrIfNotEmpty("work-voice-archived"),
	})
	router.notify(NotificationThreadArchived, &ThreadIDNotification{ThreadID: threadID})

	if session, ok := router.voiceActiveSessions[threadID]; ok {
		t.Fatalf("active voice session after archive = %q", session)
	}
	if activeTurn, ok := router.voiceActiveTurns[threadID]; ok {
		t.Fatalf("active turn after archive = %q", activeTurn)
	}
	if queue, ok := router.voiceHandoffQueue[threadID]; ok {
		t.Fatalf("handoff queue after archive = %#v", queue)
	}

	router.notify(NotificationTurnStarted, &TurnStartedNotification{ThreadID: threadID, Turn: Turn{ID: turnID + "-next"}})
	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID+"-next", connectionID, "drive", "Drive", "gpt-5", nil)
	assertVoiceSessionParam(t, waitForAppUsedEvent(t, sink).EventParams.VoiceSessionID, "")
}

// The retained turn attribution stays bounded like Rust's pending turn states.
func TestAttributedVoiceTurnsAreBoundedLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	router.trackVoiceSessionStarted("thread-voice-bounded", "voice-bounded")
	for i := 0; i < maxAttributedVoiceTurns+10; i++ {
		router.trackVoiceTurnStarted("thread-voice-bounded", "turn-voice-bounded-"+strconv.Itoa(i))
	}
	if got := len(router.voiceTurnSessions); got != maxAttributedVoiceTurns {
		t.Fatalf("retained voice turns = %d, want %d", got, maxAttributedVoiceTurns)
	}
	if got := router.voiceSessionIDForTurn("turn-voice-bounded-0"); got != "" {
		t.Fatalf("oldest turn attribution = %q, want evicted", got)
	}
	if got := router.voiceSessionIDForTurn("turn-voice-bounded-" + strconv.Itoa(maxAttributedVoiceTurns+9)); got != "voice-bounded" {
		t.Fatalf("newest turn attribution = %q", got)
	}
}

// Every event Rust #46058 extends serializes voice_session_id, including the
// unset (null) case.
func TestVoiceSessionIDSerializesOnTurnAndToolEventsLikeRust(t *testing.T) {
	withVoice := "work-voice-json"
	for _, tc := range []struct {
		name  string
		event any
	}{
		{
			name: "turn",
			event: telemetry.NewCodexTurnEvent(telemetry.CodexTurnEventInput{
				ThreadID:       "thread-1",
				SessionID:      "thread-1",
				TurnID:         "turn-1",
				VoiceSessionID: &withVoice,
			}),
		},
		{
			name: "app used",
			event: telemetry.NewCodexAppUsedEvent(telemetry.CodexAppUsedEventParams{
				VoiceSessionID: &withVoice,
			}),
		},
		{
			name: "skill invocation",
			event: telemetry.SkillInvocationEventRequest{
				EventType:   telemetry.SkillInvocationEventType,
				EventParams: telemetry.SkillInvocationEventParams{VoiceSessionID: &withVoice},
			},
		},
		{
			name: "mcp tool call",
			event: telemetry.NewCodexMCPToolCallEvent(telemetry.CodexMCPToolCallEventParams{
				VoiceSessionID: &withVoice,
			}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := marshalEventParams(t, tc.event)
			if voice, present := params["voice_session_id"]; !present || voice != withVoice {
				t.Fatalf("voice_session_id = %#v (present %v), want %q", voice, present, withVoice)
			}
		})
	}

	params := marshalEventParams(t, telemetry.NewCodexTurnEvent(telemetry.CodexTurnEventInput{
		ThreadID:  "thread-1",
		SessionID: "thread-1",
		TurnID:    "turn-1",
	}))
	if voice, present := params["voice_session_id"]; !present || voice != nil {
		t.Fatalf("unset voice_session_id = %#v (present %v), want null", voice, present)
	}
}

func assertVoiceSessionParam(t *testing.T, got *string, want string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Fatalf("voice_session_id = %q, want null", *got)
		}
		return
	}
	if got == nil || *got != want {
		t.Fatalf("voice_session_id = %#v, want %q", got, want)
	}
}

func assertNoTranscriptRetained(t *testing.T, router *RuntimeRouter, transcript string) {
	t.Helper()
	router.realtimeEventMu.Lock()
	defer router.realtimeEventMu.Unlock()
	for _, value := range router.voiceActiveSessions {
		if strings.Contains(value, transcript) {
			t.Fatalf("active voice session retained transcript: %q", value)
		}
	}
	for turnID, value := range router.voiceTurnSessions {
		if strings.Contains(turnID, transcript) || strings.Contains(value, transcript) {
			t.Fatalf("turn attribution retained transcript: %q=%q", turnID, value)
		}
	}
	for _, queue := range router.voiceHandoffQueue {
		for _, value := range queue {
			if strings.Contains(value, transcript) {
				t.Fatalf("handoff queue retained transcript: %q", value)
			}
		}
	}
}

func marshalEventParams(t *testing.T, event any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	params, ok := decoded["event_params"].(map[string]any)
	if !ok {
		t.Fatalf("event_params = %#v", decoded["event_params"])
	}
	return params
}
