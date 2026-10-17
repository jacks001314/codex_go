package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/compact"
	"codex_go/session"
	"codex_go/telemetry"
)

// Mirrors Rust #51963: compaction attribution resolves the voice-session
// window containing the compaction start, including closed windows.
func TestVoiceSessionWindowsResolveCompactionAttributionLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	t0 := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(10 * time.Second)
	t2 := t0.Add(20 * time.Second)
	t3 := t0.Add(30 * time.Second)
	t4 := t0.Add(40 * time.Second)

	router.trackRealtimeSessionStarted("thread-1", "voice-1", t0)
	router.trackRealtimeSessionClosed("thread-1", t2)
	router.trackRealtimeSessionStarted("thread-1", "voice-2", t3)

	for _, tc := range []struct {
		name      string
		timestamp time.Time
		want      string
	}{
		{name: "first window", timestamp: t1, want: "voice-1"},
		{name: "closed boundary", timestamp: t2, want: "voice-1"},
		{name: "second window", timestamp: t4, want: "voice-2"},
		{name: "before any session", timestamp: t0.Add(-time.Second), want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := router.voiceSessionIDForCompaction("thread-1", tc.timestamp); got != tc.want {
				t.Fatalf("voice session = %q, want %q", got, tc.want)
			}
		})
	}

	router.trackRealtimeSessionUpdated("thread-1", "voice-2-updated")
	if got := router.voiceSessionIDAt("thread-1", t4); got != "voice-2-updated" {
		t.Fatalf("updated voice session = %q", got)
	}

	// Starting a replacement closes the prior window at the new boundary.
	router.trackRealtimeSessionStarted("thread-1", "voice-3", t4.Add(time.Second))
	for i := 0; i < 30; i++ {
		startedAt := t4.Add(time.Duration(i+2) * time.Second)
		router.trackRealtimeSessionStarted("thread-1", "voice-bounded", startedAt)
	}
	if got := len(router.voiceSessionWindows["thread-1"]); got > maxVoiceSessionWindows {
		t.Fatalf("retained voice windows = %d, want <= %d", got, maxVoiceSessionWindows)
	}
}

// A delayed compaction completion still attributes to the session active when
// compaction started, even after a replacement voice session has begun.
func TestCompactionAnalyticsUsesCompactionStartVoiceSessionLikeRust(t *testing.T) {
	sink := newRecordingTurnEventSink()
	router := NewRuntimeRouter(RuntimeServices{
		Analytics:             sink,
		AnalyticsRPCTransport: telemetry.AppServerRPCTransportStdio,
	})
	router.clientInfo["conn-1"] = ClientInfo{Name: "codex-test", Version: "1.0"}

	t0 := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(10 * time.Second)
	t2 := t0.Add(20 * time.Second)
	t3 := t0.Add(30 * time.Second)
	router.trackRealtimeSessionStarted("thread-1", "voice-1", t0)
	router.trackRealtimeSessionClosed("thread-1", t2)
	router.trackRealtimeSessionStarted("thread-1", "voice-2", t3)

	record := &session.Record{ID: "thread-1", SessionID: "session-1"}
	router.emitCompactionAnalyticsEvent(
		context.Background(),
		"conn-1",
		record,
		&compact.Request{ThreadID: "thread-1", TurnID: "turn-1"},
		nil,
		nil,
		t1,
		t3.Add(time.Second),
		100,
	)
	select {
	case event := <-sink.compaction:
		if event.EventParams.VoiceSessionID == nil || *event.EventParams.VoiceSessionID != "voice-1" {
			t.Fatalf("voice_session_id = %#v, want voice-1", event.EventParams.VoiceSessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("compaction analytics event was not emitted")
	}
}
