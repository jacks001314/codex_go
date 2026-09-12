package app

import (
	"encoding/json"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/codexapi"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

func TestTurnErrorIsCyberPolicy(t *testing.T) {
	cases := []struct {
		name string
		info any
		want bool
	}{
		{"string camel", "cyberPolicy", true},
		{"string snake", "cyber_policy", true},
		{"object tagged", map[string]any{"type": "cyberPolicy"}, true},
		{"other string", "badRequest", false},
		{"other object", map[string]any{"type": "responseStreamDisconnected"}, false},
		{"nil", nil, false},
	}
	for _, test := range cases {
		if got := turnErrorIsCyberPolicy(appserver.TurnError{CodexErrorInfo: test.info}); got != test.want {
			t.Fatalf("%s: turnErrorIsCyberPolicy = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestCyberPolicyErrorClassifiesLocalErrors(t *testing.T) {
	cyber := codexapi.NewAPIErrorWithDetails(codexapi.APIErrorDetails{Kind: codexapi.ErrorCyberPolicy})
	if !cyberPolicyError(cyber) {
		t.Fatal("a cyber policy API error should classify as cyber policy")
	}
	other := codexapi.NewAPIErrorWithDetails(codexapi.APIErrorDetails{Kind: codexapi.ErrorInvalidRequest})
	if cyberPolicyError(other) {
		t.Fatal("an unrelated API error must not classify as cyber policy")
	}
	if cyberPolicyError(nil) {
		t.Fatal("nil must not classify as cyber policy")
	}
}

// TestRemoteClientCyberPolicyNotificationRendersRefusalCell covers the remote
// error path: the server's cyber-policy classification reaches the TUI as a
// refusal message rather than a generic turn error.
func TestRemoteClientCyberPolicyNotificationRendersRefusalCell(t *testing.T) {
	messages := make(chan bubbletea.Msg, 4)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	client := &remoteAppServerTUIClient{messages: messages, state: state}

	params, err := json.Marshal(appserver.ErrorNotification{
		Error:    appserver.TurnError{Message: "blocked by policy", CodexErrorInfo: "cyberPolicy"},
		ThreadID: "thread-1",
		TurnID:   "turn-1",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{Method: string(appserver.NotificationError), Params: params}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}

	found := false
	for {
		select {
		case msg := <-messages:
			if _, ok := msg.(codextea.CyberPolicyErrorMsg); ok {
				found = true
			}
		default:
			if !found {
				t.Fatal("expected a CyberPolicyErrorMsg")
			}
			return
		}
	}
}
