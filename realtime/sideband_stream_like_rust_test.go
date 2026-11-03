package realtime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func waitSidebandNotification(t *testing.T, notifications <-chan Notification, method NotificationMethod) Notification {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case notification := <-notifications:
			if notification.Method == method {
				return notification
			}
		case <-deadline:
			t.Fatalf("notification %s never arrived", method)
		}
	}
}

// Rust #58953: a caller-owned attachment drains input the host already queued
// before the attachment ends, and the final transcript reaches the tail flush.
func TestCallerOwnedSidebandDrainsAcceptedInputLikeRust(t *testing.T) {
	manager := NewManager()
	notifications := make(chan Notification, 64)
	manager.SetNotificationSink(func(notification Notification) { notifications <- notification })
	flushed := make(chan Event, 4)
	manager.SetEventSink(func(_ string, event Event) {
		if event.Type == "transcript_tail.flush" {
			flushed <- event
		}
	})
	incoming := make(chan string, 4)
	outgoing := make(chan string, 4)
	version := VersionV1
	includeContext := false
	flushTail := true
	if _, _, err := manager.AttachStream(&StartParams{
		ThreadID:                 "thread-sideband-stream",
		OutputModality:           OutputAudio,
		Version:                  &version,
		IncludeStartupContext:    &includeContext,
		FlushTranscriptTailOnEnd: &flushTail,
	}, nil, SidebandTransport{Incoming: incoming, Outgoing: outgoing}); err != nil {
		t.Fatalf("AttachStream() error = %v", err)
	}
	incoming <- `{"type":"conversation.input_transcript.delta","delta":"last words"}`
	close(incoming)
	notification := waitSidebandNotification(t, notifications, NotificationTranscriptDelta)
	params, ok := notification.Params.(TranscriptDeltaNotification)
	if !ok || params.Delta != "last words" {
		t.Fatalf("transcript delta = %#v", notification.Params)
	}
	waitSidebandNotification(t, notifications, NotificationClosed)
	select {
	case event := <-flushed:
		if len(event.ActiveTranscript) != 1 || event.ActiveTranscript[0].Text != "last words" {
			t.Fatalf("transcript tail = %#v", event.ActiveTranscript)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the transcript tail was never flushed")
	}
}

// Rust #58953: a stalled outgoing half must not block inbound events.
func TestCallerOwnedSidebandStalledOutputDoesNotBlockInputLikeRust(t *testing.T) {
	incoming := make(chan string, 1)
	outgoing := make(chan string, 1)
	conn := newSidebandStreamConn(incoming, outgoing)
	defer conn.CloseNow()
	if err := conn.Write(context.Background(), websocket.MessageText, []byte("first")); err != nil {
		t.Fatalf("first write = %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// The pump blocks once the caller stops reading its channel.
		_ = conn.Write(context.Background(), websocket.MessageText, []byte("second"))
	}()
	incoming <- `{"type":"session.updated","session":{"id":"call"}}`
	read := make(chan error, 1)
	go func() {
		_, payload, err := conn.Read(context.Background())
		if err == nil && !strings.Contains(string(payload), "session.updated") {
			err = context.Canceled
		}
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("inbound events were blocked by the stalled outgoing half")
	}
	select {
	case payload := <-outgoing:
		if payload != "first" {
			t.Fatalf("first payload = %q", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first payload was not handed to the caller")
	}
	// Ending the attachment aborts the pump without waiting for the caller to
	// read; pending writes are never replayed.
	conn.CloseNow()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the pending write never released")
	}
}

// Rust #58953: oversized input fails the transport before the parser sees it.
func TestCallerOwnedSidebandRejectsOversizedMessagesLikeRust(t *testing.T) {
	manager := NewManager()
	notifications := make(chan Notification, 64)
	manager.SetNotificationSink(func(notification Notification) { notifications <- notification })
	incoming := make(chan string, 1)
	outgoing := make(chan string, 1)
	version := VersionV1
	includeContext := false
	if _, _, err := manager.AttachStream(&StartParams{
		ThreadID:              "thread-sideband-oversized",
		OutputModality:        OutputAudio,
		Version:               &version,
		IncludeStartupContext: &includeContext,
	}, nil, SidebandTransport{Incoming: incoming, Outgoing: outgoing}); err != nil {
		t.Fatalf("AttachStream() error = %v", err)
	}
	incoming <- strings.Repeat("x", RealtimeSidebandMaxMessageBytes+1)
	errorNotification := waitSidebandNotification(t, notifications, NotificationError)
	if params, ok := errorNotification.Params.(ErrorNotification); !ok || !strings.Contains(params.Message, "1 MiB") {
		t.Fatalf("error notification = %#v", errorNotification.Params)
	}
	waitSidebandNotification(t, notifications, NotificationClosed)
}

// Rust #58953: detaching a caller-owned attachment never sends session.close,
// so the frontend-owned call survives.
func TestCallerOwnedSidebandDetachSendsNoSessionCloseLikeRust(t *testing.T) {
	manager := NewManager()
	incoming := make(chan string, 1)
	outgoing := make(chan string, 4)
	version := VersionV3
	includeContext := false
	if _, _, err := manager.AttachStream(&StartParams{
		ThreadID:              "thread-sideband-detach",
		OutputModality:        OutputAudio,
		Version:               &version,
		IncludeStartupContext: &includeContext,
	}, nil, SidebandTransport{Incoming: incoming, Outgoing: outgoing}); err != nil {
		t.Fatalf("AttachStream() error = %v", err)
	}
	if _, _, err := manager.Stop(&StopParams{ThreadID: "thread-sideband-detach"}, "requested"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case payload := <-outgoing:
		t.Fatalf("detaching wrote to the caller-owned output: %q", payload)
	case <-time.After(150 * time.Millisecond):
	}
}
