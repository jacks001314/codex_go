package voicehost

import (
	"errors"
	"testing"
	"time"
)

func TestManagerStateMachine(t *testing.T) {
	manager := NewManager()
	if _, err := manager.Create("session-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create("session-a"); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("duplicate create = %v", err)
	}
	steps := []State{StateNegotiating, StateStreaming, StateReconnecting, StateStreaming}
	current := StateCreated
	for _, next := range steps {
		snapshot, err := manager.Transition("session-a", current, next)
		if err != nil {
			t.Fatalf("transition %s -> %s: %v", current, next, err)
		}
		if snapshot.State != next {
			t.Fatalf("state = %q, want %q", snapshot.State, next)
		}
		current = next
	}
	if _, err := manager.Transition("session-a", current, StateNegotiating); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("invalid transition = %v", err)
	}
	snapshot, err := manager.Close("session-a", "requested")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != StateClosed || snapshot.CloseReason != "requested" || snapshot.ClosedAt == nil {
		t.Fatalf("closed snapshot = %+v", snapshot)
	}
	if _, err := manager.Transition("session-a", StateClosed, StateStreaming); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("transition from closed = %v", err)
	}
	if err := manager.Delete("session-a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Session("session-a"); ok {
		t.Fatal("deleted session still exists")
	}
	if err := manager.Delete("session-a"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("delete missing = %v", err)
	}
}

func TestManagerSnapshotsAreIsolated(t *testing.T) {
	manager := NewManager()
	snapshot, err := manager.Create("session-b")
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot.LastActivity
	snapshot.State = StateClosed
	snapshot.LastActivity = nil
	current, ok := manager.Session("session-b")
	if !ok || current.State != StateCreated || current.LastActivity == nil || !current.LastActivity.Equal(*before) {
		t.Fatalf("snapshot mutation escaped: current=%+v before=%v", current, before)
	}
}

func TestManagerUpdateSnapshot(t *testing.T) {
	manager := NewManager()
	if _, err := manager.Create("session-c"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.UpdateSnapshot("session-c", func(session *Session) {
		session.RuntimeName = "fake"
		session.InputDeviceID = "mic-1"
		session.Format = AudioFormat{SampleRate: 24000, Channels: 1, Encoding: AudioEncodingS16LE}
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RuntimeName != "fake" || snapshot.InputDeviceID != "mic-1" || snapshot.Format.SampleRate != 24000 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestManagerList(t *testing.T) {
	manager := NewManager()
	for _, id := range []string{"a", "b", "c"} {
		if _, err := manager.Create(id); err != nil {
			t.Fatal(err)
		}
	}
	list := manager.List()
	if len(list) != 3 {
		t.Fatalf("list length = %d", len(list))
	}
	seen := map[string]bool{}
	for _, session := range list {
		seen[session.ID] = true
	}
	for _, id := range []string{"a", "b", "c"} {
		if !seen[id] {
			t.Fatalf("missing session %q", id)
		}
	}
}

func TestManagerCloseIsIdempotent(t *testing.T) {
	manager := NewManager()
	if _, err := manager.Create("session-d"); err != nil {
		t.Fatal(err)
	}
	first, err := manager.Close("session-d", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Close("session-d", "second")
	if err != nil {
		t.Fatal(err)
	}
	if !first.ClosedAt.Equal(*second.ClosedAt) || second.CloseReason != "first" {
		t.Fatalf("close reason changed: first=%+v second=%+v", first, second)
	}
	_ = time.Second
}
