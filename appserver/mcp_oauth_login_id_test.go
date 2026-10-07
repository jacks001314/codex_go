package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/mcp"
)

// TestMCPServerOauthLoginCompletedLoginIDLikeRust pins the tri-state `loginId`
// of the v2 `mcpServer/oauthLogin/completed` notification against Rust #49276
// (commit 4994306e9f "Enable enterprise MCP sign-in and account-scoped grant
// cleanup").
//
// Rust reference points:
//   - app-server-protocol/src/protocol/v2/mcp.rs:341-348 declares
//     `login_id: Option<String>` with `#[ts(optional, as = "Option<Option<String>>")]`
//     and no `skip_serializing_if` (unlike the neighbouring `error`), so on the
//     wire the key is always present and `None` serializes as `null`. The
//     generated TypeScript is `loginId?: string | null` (tri-state, for older
//     servers that omit the key).
//   - app-server/tests/suite/v2/workspace_routing.rs:482 pins the sibling
//     `account/login/completed` notification (same `Option<String>` shape) to
//     `json!({ "loginId": null, ... })`.
//   - app-server-protocol/src/protocol/v2/tests.rs::
//     mcp_oauth_login_response_accepts_older_servers_without_login_id pins that
//     a payload without `loginId` still deserializes (the "older server" case).
func TestMCPServerOauthLoginCompletedLoginIDLikeRust(t *testing.T) {
	newHandler := func() (*appserverMCPOAuthLoginCompletionHandler, *NotificationBuffer) {
		sink := NewNotificationBuffer()
		handler := &appserverMCPOAuthLoginCompletionHandler{notify: func(method NotificationMethod, params any) {
			sink.Notify(NewNotification(method, params))
		}}
		return handler, sink
	}
	payloadOf := func(t *testing.T, sink *NotificationBuffer) *MCPServerOauthLoginCompletedNotification {
		t.Helper()
		notifications := sink.List()
		if len(notifications) != 1 {
			t.Fatalf("notifications = %#v", notifications)
		}
		payload, ok := notifications[0].Params.(*MCPServerOauthLoginCompletedNotification)
		if !ok {
			t.Fatalf("notification params = %#v", notifications[0].Params)
		}
		return payload
	}

	t.Run("value", func(t *testing.T) {
		handler, sink := newHandler()
		handler.HandleMCPOAuthLoginCompleted(context.Background(), &mcp.MCPOAuthLoginCompletion{
			Name:     "docs",
			ThreadID: "thread-1",
			// The carrier trims the id, like every other field.
			LoginID: " login-1 ",
			Success: true,
		})
		payload := payloadOf(t, sink)
		if !payload.LoginID.Set || payload.LoginID.Value == nil || *payload.LoginID.Value != "login-1" {
			t.Fatalf("LoginID = %#v, want login-1", payload.LoginID)
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if !strings.Contains(string(data), `"loginId":"login-1"`) {
			t.Fatalf("marshaled notification = %s, want loginId login-1", data)
		}
	})

	t.Run("omitted id still writes an explicit null like Rust", func(t *testing.T) {
		handler, sink := newHandler()
		// mcp.MCPOAuthLoginCompletion.LoginID is empty when the MCP layer has no
		// attempt id, mirroring Rust's `Option::None`.
		handler.HandleMCPOAuthLoginCompleted(context.Background(), &mcp.MCPOAuthLoginCompletion{
			Name:    "docs",
			Success: true,
		})
		payload := payloadOf(t, sink)
		if payload.LoginID.Value != nil {
			t.Fatalf("LoginID.Value = %#v, want nil", payload.LoginID.Value)
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if !strings.Contains(string(data), `"loginId":null`) {
			t.Fatalf("marshaled notification = %s, want an explicit null loginId", data)
		}
		// Value and pointer marshaling must agree (Rust always writes the key).
		byValue, err := json.Marshal(*payload)
		if err != nil {
			t.Fatalf("Marshal(value) error = %v", err)
		}
		if string(byValue) != string(data) {
			t.Fatalf("value marshal = %s, pointer marshal = %s", byValue, data)
		}
	})

	t.Run("older server payload without loginId still decodes", func(t *testing.T) {
		var decoded MCPServerOauthLoginCompletedNotification
		if err := json.Unmarshal([]byte(`{"name":"docs","threadId":null,"success":true}`), &decoded); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if decoded.LoginID.Set {
			t.Fatalf("LoginID.Set = true, want false for an omitted field")
		}
		if decoded.LoginID.Value != nil {
			t.Fatalf("LoginID.Value = %#v, want nil", decoded.LoginID.Value)
		}
		data, err := json.Marshal(&decoded)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if !strings.Contains(string(data), `"loginId":null`) {
			t.Fatalf("marshaled notification = %s, want an explicit null loginId", data)
		}
	})

	t.Run("explicit null decodes distinctly from an omitted field", func(t *testing.T) {
		var decoded MCPServerOauthLoginCompletedNotification
		if err := json.Unmarshal([]byte(`{"name":"docs","threadId":null,"loginId":null,"success":true}`), &decoded); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if !decoded.LoginID.Set || decoded.LoginID.Value != nil {
			t.Fatalf("LoginID = %#v, want a present explicit null", decoded.LoginID)
		}
		var withValue MCPServerOauthLoginCompletedNotification
		if err := json.Unmarshal([]byte(`{"name":"docs","loginId":"login-2","success":true}`), &withValue); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if !withValue.LoginID.Set || withValue.LoginID.Value == nil || *withValue.LoginID.Value != "login-2" {
			t.Fatalf("LoginID = %#v, want login-2", withValue.LoginID)
		}
	})
}
