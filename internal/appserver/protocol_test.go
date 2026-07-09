package appserver

import (
	"encoding/json"
	"fmt"
	"testing"

	"codex_go/internal/config"
	"codex_go/internal/mcp"
)

func TestOutgoingMessagesMatchRustJSONRPCShape(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "response",
			value: OK(IntID(7), map[string]any{"ok": true}),
			want:  `{"id":7,"result":{"ok":true}}`,
		},
		{
			name:  "error",
			value: ErrorResponse(IntID(7), -32000, "Server overloaded; retry later.", nil),
			want:  `{"id":7,"error":{"code":-32000,"message":"Server overloaded; retry later."}}`,
		},
		{
			name: "config warning notification",
			value: NewNotification(NotificationConfigWarning, &config.ConfigWarningNotification{
				Summary: "queued",
			}),
			want: `{"method":"configWarning","params":{"summary":"queued","details":null}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(data) != tc.want {
				t.Fatalf("encoded = %s, want %s", data, tc.want)
			}
		})
	}
}

func TestJSONRPCErrorDataIncludesMCPRemoteErrorDetails(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &mcp.MCPRemoteError{
		Method:  "tools/call",
		Code:    -32001,
		Message: "remote denied request",
		Data:    json.RawMessage(`{"reason":"policy","retry":false}`),
	})

	data := jsonRPCErrorData(err)
	if data["type"] != "mcp_remote_error" || data["method"] != "tools/call" || data["message"] != "remote denied request" {
		t.Fatalf("error data = %#v", data)
	}
	if code, ok := data["code"].(int64); !ok || code != -32001 {
		t.Fatalf("error data code = %#v, want -32001", data["code"])
	}
	payload, ok := data["data"].(map[string]any)
	if !ok {
		t.Fatalf("error data payload = %#v, want object", data["data"])
	}
	if payload["reason"] != "policy" || payload["retry"] != false {
		t.Fatalf("error data payload = %#v", payload)
	}
	if code := runtimeErrorCode(err); code != -32001 {
		t.Fatalf("runtimeErrorCode() = %d, want -32001", code)
	}
}
