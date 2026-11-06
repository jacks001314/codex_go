package app

import (
	"encoding/json"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/appserver"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust #59253: a 429 stream disconnect keeps the retry counter and explains
// that the server is busy.
func TestRetryStatusMessageExplainsBusyServerLikeRust(t *testing.T) {
	busy := appserver.CodexErrorInfo(map[string]any{
		"responseStreamDisconnected": map[string]any{"httpStatusCode": float64(429)},
	})
	if got := retryStatusMessage(appserver.TurnError{Message: "Reconnecting... 2/5", CodexErrorInfo: busy}); got != "Server is busy, reconnecting 2/5" {
		t.Fatalf("429 retry status = %q", got)
	}
	other := appserver.CodexErrorInfo(map[string]any{
		"responseStreamDisconnected": map[string]any{"httpStatusCode": float64(503)},
	})
	if got := retryStatusMessage(appserver.TurnError{Message: "Reconnecting... 3/5", CodexErrorInfo: other}); got != "Reconnecting... 3/5" {
		t.Fatalf("503 retry status = %q", got)
	}
	if got := retryStatusMessage(appserver.TurnError{}); got != "Reconnecting..." {
		t.Fatalf("empty retry status = %q", got)
	}
	if got := retryStatusMessage(appserver.TurnError{Message: "Reconnecting... 1/5"}); got != "Reconnecting... 1/5" {
		t.Fatalf("statusless retry = %q", got)
	}
}

func TestRemoteBusyRetryNotificationShowsServerBusyLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-retry")
	messages := make(chan bubbletea.Msg, 2)
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	payload, err := json.Marshal(appserver.ErrorNotification{
		Error: appserver.TurnError{
			Message:        "Reconnecting... 2/5",
			CodexErrorInfo: map[string]any{"responseStreamDisconnected": map[string]any{"httpStatusCode": 429}},
		},
		WillRetry: true,
		ThreadID:  "thread-retry",
		TurnID:    "turn-retry",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.handleNotification(remoteAppServerMessage{Method: string(appserver.NotificationError), Params: payload}); err != nil {
		t.Fatal(err)
	}
	msg, ok := (<-messages).(codextea.ModelRetryStatusMsg)
	if !ok || !msg.Active || msg.Message != "Server is busy, reconnecting 2/5" {
		t.Fatalf("message = %#v", msg)
	}
}
