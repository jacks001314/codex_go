package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// newHeldRealtimeHandshake accepts one frameless realtime websocket, reads the
// session update, and leaves the session start pending until the test releases
// it. The peer-closed channel reports that the client dropped the socket while
// the handshake was still pending.
func newHeldRealtimeHandshake(t *testing.T) (string, <-chan struct{}, chan struct{}, <-chan struct{}) {
	t.Helper()
	accepted := make(chan struct{}, 1)
	release := make(chan struct{})
	peerClosed := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(context.Background()); err != nil {
			return
		}
		accepted <- struct{}{}
		peer := make(chan error, 1)
		go func() {
			_, _, err := conn.Read(context.Background())
			peer <- err
		}()
		select {
		case <-release:
		case <-peer:
			peerClosed <- struct{}{}
			return
		}
		if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"session.started","session":{"id":"session-held"}}`)); err != nil {
			peerClosed <- struct{}{}
			return
		}
		for {
			_, _, err := conn.Read(context.Background())
			if err != nil {
				peerClosed <- struct{}{}
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, accepted, release, peerClosed
}

// newV3RealtimeServer completes the frameless handshake and records the
// messages the client sends afterwards.
func newV3RealtimeServer(t *testing.T) (*httptest.Server, <-chan map[string]any) {
	t.Helper()
	outbound := make(chan map[string]any, 16)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(context.Background()); err != nil {
			return
		}
		if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"session.started","session":{"id":"session-attach"}}`)); err != nil {
			return
		}
		for {
			_, payload, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil {
				select {
				case outbound <- message:
				default:
				}
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, outbound
}

func newAttachmentTestManager(t *testing.T) *Manager {
	t.Helper()
	manager := NewManager()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	return manager
}

func attachmentStartParams(threadID string, version Version) *StartParams {
	return &StartParams{ThreadID: threadID, OutputModality: OutputAudio, Version: &version}
}

// Rust #60443: cancelling a held handshake releases the pending connection
// attempt and the next call on the same thread still publishes.
func TestRealtimeAttachmentCancelReleasesPendingHandshakeLikeRust(t *testing.T) {
	heldURL, accepted, _, peerClosed := newHeldRealtimeHandshake(t)
	manager := newAttachmentTestManager(t)
	version := VersionV3
	cancellation := NewAttachmentCancellation()
	result := make(chan error, 1)
	go func() {
		_, _, err := manager.StartWithOptions(attachmentStartParams("thread-attach-cancel", version), &StartOptions{
			Backend:    &TransportBackendConfig{WebsocketBaseURL: heldURL},
			Attachment: &AttachmentOptions{Cancel: cancellation},
		})
		result <- err
	}()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the held handshake never started")
	}
	cancellation.Cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrRealtimeAttachmentCanceled) {
			t.Fatalf("canceled attachment error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation never released the pending handshake")
	}
	select {
	case <-peerClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("the canceled handshake kept its websocket open")
	}

	server, _ := newV3RealtimeServer(t)
	state, notifications, err := manager.StartWithOptions(attachmentStartParams("thread-attach-cancel", version), &StartOptions{
		Backend: &TransportBackendConfig{WebsocketBaseURL: server.URL},
	})
	if err != nil {
		t.Fatalf("the next attachment failed: %v", err)
	}
	if state == nil || state.Config.RealtimeSessionID != "thread-attach-cancel" {
		t.Fatalf("published state = %#v", state)
	}
	if len(notifications) == 0 || notifications[0].Method != NotificationStarted {
		t.Fatalf("notifications = %#v", notifications)
	}
	if _, err := manager.AppendText(&AppendTextParams{ThreadID: "thread-attach-cancel", Text: "hello"}); err != nil {
		t.Fatalf("published attachment is not usable: %v", err)
	}
}

// Rust #60443: cancelled and expired requests are rejected before they replace
// the resident conversation, and a cancellation that arrives after publication
// preserves the published connection.
func TestRealtimeAttachmentRejectsBeforeReplacingLikeRust(t *testing.T) {
	server, _ := newV3RealtimeServer(t)
	manager := newAttachmentTestManager(t)
	version := VersionV3
	threadID := "thread-attach-replace"
	resident := NewAttachmentCancellation()
	if _, _, err := manager.StartWithOptions(attachmentStartParams(threadID, version), &StartOptions{
		Backend:    &TransportBackendConfig{WebsocketBaseURL: server.URL},
		Attachment: &AttachmentOptions{Cancel: resident},
	}); err != nil {
		t.Fatalf("start resident attachment: %v", err)
	}

	cancelled := NewAttachmentCancellation()
	cancelled.Cancel()
	if _, _, err := manager.StartWithOptions(attachmentStartParams(threadID, version), &StartOptions{
		Backend:    &TransportBackendConfig{WebsocketBaseURL: server.URL},
		Attachment: &AttachmentOptions{Cancel: cancelled},
	}); !errors.Is(err, ErrRealtimeAttachmentCanceled) {
		t.Fatalf("cancelled attachment error = %v", err)
	}
	expired := time.Now().Add(-time.Second)
	if _, _, err := manager.StartWithOptions(attachmentStartParams(threadID, version), &StartOptions{
		Backend:    &TransportBackendConfig{WebsocketBaseURL: server.URL},
		Attachment: &AttachmentOptions{StartBefore: &expired},
	}); !errors.Is(err, ErrRealtimeAttachmentCanceled) {
		t.Fatalf("expired attachment error = %v", err)
	}
	if state, err := manager.AppendText(&AppendTextParams{ThreadID: threadID, Text: "before"}); err != nil || state.TextInputs != 1 {
		t.Fatalf("rejected attempts replaced the resident conversation: state=%#v err=%v", state, err)
	}

	resident.Cancel()
	state, err := manager.AppendText(&AppendTextParams{ThreadID: threadID, Text: "after"})
	if err != nil || state.TextInputs != 2 {
		t.Fatalf("a later cancellation tore down the published conversation: state=%#v err=%v", state, err)
	}
	stop, _, err := manager.Stop(&StopParams{ThreadID: threadID}, "requested")
	if err != nil || stop == nil || stop.ClosedAt == nil {
		t.Fatalf("published conversation did not stay resident: state=%#v err=%v", stop, err)
	}
}

// Rust #60443: a deadline without a cancellation receiver releases a held
// handshake so the next call can connect.
func TestRealtimeAttachmentDeadlineReleasesPendingHandshakeLikeRust(t *testing.T) {
	heldURL, accepted, _, peerClosed := newHeldRealtimeHandshake(t)
	manager := newAttachmentTestManager(t)
	version := VersionV3
	deadline := time.Now().Add(50 * time.Millisecond)
	result := make(chan error, 1)
	go func() {
		_, _, err := manager.StartWithOptions(attachmentStartParams("thread-attach-deadline", version), &StartOptions{
			Backend:    &TransportBackendConfig{WebsocketBaseURL: heldURL},
			Attachment: &AttachmentOptions{StartBefore: &deadline},
		})
		result <- err
	}()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the held handshake never started")
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrRealtimeAttachmentCanceled) {
			t.Fatalf("expired attachment error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the deadline never released the pending handshake")
	}
	select {
	case <-peerClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("the expired handshake kept its websocket open")
	}

	server, _ := newV3RealtimeServer(t)
	if _, _, err := manager.StartWithOptions(attachmentStartParams("thread-attach-deadline", version), &StartOptions{
		Backend: &TransportBackendConfig{WebsocketBaseURL: server.URL},
	}); err != nil {
		t.Fatalf("the next attachment failed: %v", err)
	}
}

// Rust #60443: dropping the caller's cancellation sender without cancelling
// lets the admitted attempt continue.
func TestRealtimeAttachmentDroppedCancelKeepsAttemptLikeRust(t *testing.T) {
	heldURL, accepted, release, _ := newHeldRealtimeHandshake(t)
	manager := newAttachmentTestManager(t)
	version := VersionV3
	cancellation := NewAttachmentCancellation()
	type outcome struct {
		state *SessionState
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		state, _, err := manager.StartWithOptions(attachmentStartParams("thread-attach-dropped", version), &StartOptions{
			Backend:    &TransportBackendConfig{WebsocketBaseURL: heldURL},
			Attachment: &AttachmentOptions{Cancel: cancellation},
		})
		result <- outcome{state: state, err: err}
	}()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the held handshake never started")
	}
	cancellation.Close()
	close(release)
	select {
	case got := <-result:
		if got.err != nil || got.state == nil {
			t.Fatalf("dropped cancellation handle ended the attempt: state=%#v err=%v", got.state, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the released handshake never published")
	}
	if _, err := manager.AppendText(&AppendTextParams{ThreadID: "thread-attach-dropped", Text: "still attached"}); err != nil {
		t.Fatalf("published attachment is not usable: %v", err)
	}
}
