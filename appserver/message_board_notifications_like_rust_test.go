package appserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/agentboard"
)

const messageBoardNotificationTestToken = "0123456789abcdef0123456789abcdef"

type notificationBoardHostForTest struct {
	delivered chan agentboard.PostPreview
}

func (h *notificationBoardHostForTest) AgentPath(context.Context, string) (agent.AgentPath, error) {
	return agent.AgentPathRoot, nil
}

func (h *notificationBoardHostForTest) ResolveAgent(context.Context, agent.AgentPath) (string, error) {
	return "", nil
}

func (h *notificationBoardHostForTest) CurrentTime(context.Context, string) (time.Time, error) {
	return time.Now().UTC(), nil
}

func (h *notificationBoardHostForTest) Notify(_ context.Context, _ string, post agentboard.PostPreview) (agentboard.NotificationDelivery, error) {
	h.delivered <- post
	return agentboard.NotificationAccepted, nil
}

// TestMessageBoardNotificationReceiverRetriesAndDeliversLikeRust pins Rust
// #49686: the first connection attempt releases startup even when it fails, the
// turn-owned receiver retries, and accepted previews reach the board host.
func TestMessageBoardNotificationReceiverRetriesAndDeliversLikeRust(t *testing.T) {
	var attempts atomic.Int32
	notice := `{"recipient":"thread-1","turn_id":"turn-1","post":{"message_id":"m1","channel_name":"work","author":"/root","thread_id":"m1","created_at":"2026-01-02T03:04:05Z","text_preview":"hi","n_chars":2,"truncated":false}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/boards/tree-1/notifications" {
			t.Errorf("notification path = %q", r.URL.Path)
		}
		if attempts.Add(1) == 1 {
			http.Error(w, "transient board failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		_, _ = fmt.Fprintf(w, "event: notification\ndata: %s\n\n", notice)
		w.(http.Flusher).Flush()
	}))
	t.Cleanup(server.Close)
	board, err := agentboard.NewRemoteBoard(agentboard.RemoteBoardOptions{
		HTTP:     server.Client(),
		Endpoint: server.URL,
		Board:    "tree-1",
		Token:    messageBoardNotificationTestToken,
	})
	if err != nil {
		t.Fatalf("NewRemoteBoard() error = %v", err)
	}
	host := &notificationBoardHostForTest{delivered: make(chan agentboard.PostPreview, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runMessageBoardNotificationReceiver(ctx, board, host, "thread-1", "turn-1", ready)
		close(done)
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("first connection attempt did not release startup")
	}
	select {
	case post := <-host.delivered:
		if post.MessageID != "m1" || post.TextPreview != "hi" {
			t.Fatalf("delivered preview = %#v", post)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notification was not delivered after the retry")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("receiver did not stop with its turn")
	}
}

func TestInstallMessageBoardNotificationsWithoutRemoteBoardLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	defer router.Close()
	stop := router.installMessageBoardNotifications(context.Background(), "thread-1", "turn-1", nil)
	if stop == nil {
		t.Fatal("stop function = nil")
	}
	stop()
}
