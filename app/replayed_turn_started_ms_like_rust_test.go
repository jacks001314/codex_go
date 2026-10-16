package app

import (
	"encoding/json"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// The bridge layer is where #54047's elapsed-time restore breaks silently.
// appserver publishes a turn's startedAt in *seconds*
// (appserver/runtime_router.go:13660 time.UnixMilli(startedAtMS).UTC().Unix(),
// appserver/protocol.go:863 StartedAt *int64 json "startedAt"), while the TUI
// event field is documented as Unix *milliseconds* (protocol/exec_events.go:24-27)
// and is consumed as milliseconds (tui/tea/model.go:4644 time.UnixMilli). Rust
// keeps seconds on the wire end to end (codex-rs/tui/src/chatwidget/
// turn_runtime.rs:119-126 Utc::now().timestamp().saturating_sub(started_at) plus
// Duration::from_secs, chatwidget/protocol.rs:105-109), so the Go bridge must
// convert. These tests drive the bridge, not the model: tui/tea's own cases seed
// milliseconds by hand, which is why a unit mismatch stayed green there.

const r90ReplayedTurnStartedAtSeconds int64 = 1750000000 // 2025-06-15T14:26:40Z

func TestReplayedTurnStartedNotificationConvertsSecondsToMillisecondsLikeRust(t *testing.T) {
	messages := make(chan bubbletea.Msg, 8)
	state := codextui.NewState(nil)
	// A different thread is on screen, so this turn/started takes the replay
	// branch that carries the timestamp (app/remote_tui.go:4114-4121).
	state.SetThreadID("thread-active")
	client := &remoteAppServerTUIClient{state: state, messages: messages}

	payload, err := json.Marshal(appserver.TurnStartedNotification{
		ThreadID: "thread-replayed",
		Turn: appserver.Turn{
			ID:        "turn-replayed",
			Status:    appserver.TurnStatusInProgress,
			StartedAt: int64PtrAppTest(r90ReplayedTurnStartedAtSeconds),
		},
	})
	if err != nil {
		t.Fatalf("marshal turn/started: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationTurnStarted),
		Params: payload,
	}); err != nil {
		t.Fatalf("handle turn/started: %v", err)
	}

	scoped := r90ThreadScopedEvent(t, drainRemoteTUIMessages(messages))
	if scoped.ThreadID != "thread-replayed" {
		t.Fatalf("replayed event thread = %q, want thread-replayed", scoped.ThreadID)
	}
	if scoped.Event.StartedAtMS == nil {
		t.Fatalf("replayed turn/started StartedAtMS = nil, want %d", r90ReplayedTurnStartedAtSeconds*1000)
	}
	if got, want := *scoped.Event.StartedAtMS, r90ReplayedTurnStartedAtSeconds*1000; got != want {
		t.Fatalf("replayed turn/started StartedAtMS = %d, want %d (appserver sends seconds, the event field is Unix ms: Rust turn_runtime.rs:119-126)", got, want)
	}
}

func TestResumeResponseInProgressTurnStartedAtIsMillisecondsLikeRust(t *testing.T) {
	thread := &appserver.Thread{
		ID: "thread-resume",
		Turns: []appserver.Turn{{
			ID:        "turn-in-progress",
			Status:    appserver.TurnStatusInProgress,
			StartedAt: int64PtrAppTest(r90ReplayedTurnStartedAtSeconds),
		}},
	}

	response := remoteTUIResumeResponseFromThread(thread)
	if response.WorkingTurnStartedAtMS == nil {
		t.Fatalf("resume WorkingTurnStartedAtMS = nil, want %d", r90ReplayedTurnStartedAtSeconds*1000)
	}
	if got, want := *response.WorkingTurnStartedAtMS, r90ReplayedTurnStartedAtSeconds*1000; got != want {
		t.Fatalf("resume WorkingTurnStartedAtMS = %d, want %d (appserver sends seconds, tui/tea/model.go:4644 reads Unix ms)", got, want)
	}
}

func TestResumeAndReplayWithoutInProgressTurnKeepStartedAtNilLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		thread *appserver.Thread
	}{
		{"nil thread", nil},
		{"completed turn", &appserver.Thread{ID: "t", Turns: []appserver.Turn{{
			ID:        "turn-done",
			Status:    appserver.TurnStatusCompleted,
			StartedAt: int64PtrAppTest(r90ReplayedTurnStartedAtSeconds),
		}}}},
		{"in-progress turn without a timestamp", &appserver.Thread{ID: "t", Turns: []appserver.Turn{{
			ID:     "turn-live",
			Status: appserver.TurnStatusInProgress,
		}}}},
	}
	for _, tc := range cases {
		if got := remoteTUIResumeResponseFromThread(tc.thread).WorkingTurnStartedAtMS; got != nil {
			t.Fatalf("%s: resume WorkingTurnStartedAtMS = %d, want nil (no in-progress timestamp means the TUI keeps its local clock; Rust replay.rs:146-149)", tc.name, *got)
		}
	}

	messages := make(chan bubbletea.Msg, 8)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-active")
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	payload, err := json.Marshal(appserver.TurnStartedNotification{
		ThreadID: "thread-replayed",
		Turn:     appserver.Turn{ID: "turn-replayed", Status: appserver.TurnStatusInProgress},
	})
	if err != nil {
		t.Fatalf("marshal turn/started: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationTurnStarted),
		Params: payload,
	}); err != nil {
		t.Fatalf("handle turn/started: %v", err)
	}
	scoped := r90ThreadScopedEvent(t, drainRemoteTUIMessages(messages))
	if scoped.Event.StartedAtMS != nil {
		t.Fatalf("replayed turn/started without a server timestamp carried StartedAtMS = %d, want nil (a zero would render 1970)", *scoped.Event.StartedAtMS)
	}
}

func TestLiveTurnStartedNotificationCarriesNoTimestampLikeRust(t *testing.T) {
	messages := make(chan bubbletea.Msg, 8)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-live")
	client := &remoteAppServerTUIClient{state: state, messages: messages}

	payload, err := json.Marshal(appserver.TurnStartedNotification{
		ThreadID: "thread-live",
		Turn: appserver.Turn{
			ID:        "turn-live",
			Status:    appserver.TurnStatusInProgress,
			StartedAt: int64PtrAppTest(r90ReplayedTurnStartedAtSeconds),
		},
	})
	if err != nil {
		t.Fatalf("marshal turn/started: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationTurnStarted),
		Params: payload,
	}); err != nil {
		t.Fatalf("handle turn/started: %v", err)
	}

	live := false
	for _, message := range drainRemoteTUIMessages(messages) {
		event, ok := message.(codextea.ThreadEventMsg)
		if !ok {
			continue
		}
		live = true
		if event.Event.Type != "turn.started" {
			t.Fatalf("live event type = %q, want turn.started", event.Event.Type)
		}
		if event.Event.StartedAtMS != nil {
			t.Fatalf("live turn/started carried StartedAtMS = %d, want nil (Rust protocol.rs:105-109 starts a live timer now)", *event.Event.StartedAtMS)
		}
	}
	if !live {
		t.Fatal("no ThreadEventMsg published for a live turn/started")
	}
}

func drainRemoteTUIMessages(messages chan bubbletea.Msg) []bubbletea.Msg {
	var out []bubbletea.Msg
	for {
		select {
		case message := <-messages:
			out = append(out, message)
		default:
			return out
		}
	}
}

func r90ThreadScopedEvent(t *testing.T, messages []bubbletea.Msg) codextea.ThreadScopedEventMsg {
	t.Helper()
	for _, message := range messages {
		if scoped, ok := message.(codextea.ThreadScopedEventMsg); ok {
			return scoped
		}
	}
	t.Fatalf("no ThreadScopedEventMsg in %#v", messages)
	return codextea.ThreadScopedEventMsg{}
}
