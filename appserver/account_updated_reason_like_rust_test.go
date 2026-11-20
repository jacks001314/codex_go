package appserver

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"codex_go/auth"
)

// Rust #62145: the initial saved-workspace routing notification carries
// reason=workspaceRoutingReady so clients can tell it apart from login, logout
// and workspace switches.
func TestWorkspaceRoutingReadyAccountUpdateReasonLikeRust(t *testing.T) {
	router := workspaceRoutingRouter(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"accounts": []any{map[string]any{
			"id":                       "workspace",
			"workspace_backend_origin": "https://chatgpt.com",
			"account_routing_override": "us",
		}}})
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	router.notifyWorkspaceRoutingToConnection("conn-1")
	var payload *auth.AccountUpdatedNotification
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && payload == nil {
		for _, notification := range sink.List() {
			if notification.Method != NotificationAccountUpdated {
				continue
			}
			if updated, ok := notification.Params.(*auth.AccountUpdatedNotification); ok {
				payload = updated
			}
		}
		if payload == nil {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if payload == nil {
		t.Fatalf("workspace routing did not notify account/updated: %+v", sink.List())
	}
	if payload.Reason == nil || *payload.Reason != auth.AccountUpdateReasonWorkspaceRoutingReady {
		t.Fatalf("account/updated reason = %v, want workspaceRoutingReady", payload.Reason)
	}
}

// Rust #62145 serializes reason as null for updates that are not the initial
// routing notification.
func TestAccountUpdatedPlainReasonIsNullLikeRust(t *testing.T) {
	encoded, err := json.Marshal(auth.NewAccountManager().AccountUpdated())
	if err != nil {
		t.Fatalf("marshal account/updated: %v", err)
	}
	const want = `{"authMode":null,"planType":null,"reason":null}`
	if string(encoded) != want {
		t.Fatalf("account/updated payload = %s, want %s", encoded, want)
	}
}
