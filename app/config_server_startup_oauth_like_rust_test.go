package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/config"
)

// Rust #62867 oauth_tests: the startup connection sends the browser handoff and
// a cancel request completes the pending login with LoginFailed.
func TestStartupGatewayOAuthCancelsPendingBrowserLoginLikeRust(t *testing.T) {
	restore := startupConfigServerLogin
	defer func() { startupConfigServerLogin = restore }()
	startupConfigServerLogin = func(ctx context.Context, _ string, _ *config.Config, onURL func(string)) error {
		onURL("https://login.example.test/authorize?state=1")
		<-ctx.Done()
		return ctx.Err()
	}

	stdin := strings.NewReader(strings.Join([]string{
		`{"id":1,"method":"account/gatewayOAuth/login","params":{"target":{"kind":"configServer"}}}`,
		`{"id":2,"method":"account/gatewayOAuth/cancel"}`,
		"",
	}, "\n"))
	var stdout bytes.Buffer
	if err := serveAppServerStartupOAuth(context.Background(), cli.AppServerOptions{StartupOAuth: true}, &cli.RootOptions{}, stdin, &stdout); err != nil {
		t.Fatalf("serve error = %v", err)
	}
	messages := startupOAuthTestMessages(t, stdout.Bytes())
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3: %v", len(messages), messages)
	}
	started := messages[0]
	if started["method"] != "account/gatewayOAuth/changed" {
		t.Fatalf("first message = %#v", started)
	}
	params, _ := started["params"].(map[string]any)
	if params["status"] != "started" {
		t.Fatalf("handoff status = %#v", params["status"])
	}
	target, _ := params["target"].(map[string]any)
	if target["kind"] != "configServer" {
		t.Fatalf("handoff target = %#v", target)
	}
	url, _ := params["authUrl"].(string)
	if !strings.HasPrefix(url, "https://login.example.test/authorize") {
		t.Fatalf("handoff authUrl = %q", url)
	}

	cancelled := messages[1]
	if numberID(cancelled["id"]) != 1 {
		t.Fatalf("cancelled reply id = %#v, want 1", cancelled["id"])
	}
	failure, _ := cancelled["error"].(map[string]any)
	data, _ := failure["data"].(map[string]any)
	if data["errorCode"] != "LoginFailed" {
		t.Fatalf("cancel error = %#v", cancelled["error"])
	}
	if failure["message"] != startupOAuthCancelledMessage {
		t.Fatalf("cancel message = %#v", failure["message"])
	}

	cancelReply := messages[2]
	if numberID(cancelReply["id"]) != 2 {
		t.Fatalf("cancel reply id = %#v, want 2", cancelReply["id"])
	}
	if result, ok := cancelReply["result"].(map[string]any); !ok || len(result) != 0 {
		t.Fatalf("cancel result = %#v, want {}", cancelReply["result"])
	}
}

// A successful browser sign-in replies to the pending login with a null prompt.
func TestStartupGatewayOAuthCompletesLoginLikeRust(t *testing.T) {
	restore := startupConfigServerLogin
	defer func() { startupConfigServerLogin = restore }()
	startupConfigServerLogin = func(_ context.Context, _ string, _ *config.Config, onURL func(string)) error {
		onURL("https://login.example.test/authorize?state=done")
		return nil
	}

	stdin := strings.NewReader(`{"id":7,"method":"account/gatewayOAuth/login","params":{"target":{"kind":"configServer"}}}` + "\n")
	var stdout bytes.Buffer
	if err := serveAppServerStartupOAuth(context.Background(), cli.AppServerOptions{StartupOAuth: true}, &cli.RootOptions{}, stdin, &stdout); err != nil {
		t.Fatalf("serve error = %v", err)
	}
	messages := startupOAuthTestMessages(t, stdout.Bytes())
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2: %v", len(messages), messages)
	}
	if messages[0]["method"] != "account/gatewayOAuth/changed" {
		t.Fatalf("first message = %#v", messages[0])
	}
	reply := messages[1]
	if numberID(reply["id"]) != 7 {
		t.Fatalf("login reply id = %#v", reply["id"])
	}
	result, _ := reply["result"].(map[string]any)
	if value, ok := result["prompt"]; !ok || value != nil {
		t.Fatalf("login result = %#v, want prompt null", reply["result"])
	}
}

// Requests outside the startup contract are rejected with Rust's codes and
// messages before a sign-in starts.
func TestStartupGatewayOAuthRejectsBadRequestsLikeRust(t *testing.T) {
	restore := startupConfigServerLogin
	defer func() { startupConfigServerLogin = restore }()
	startupConfigServerLogin = func(context.Context, string, *config.Config, func(string)) error {
		t.Fatal("invalid startup request started a sign-in")
		return nil
	}
	cases := []struct {
		name       string
		request    string
		wantCode   int
		wantOutput string
	}{
		{name: "no target", request: `{"id":1,"method":"account/gatewayOAuth/login","params":{}}`, wantCode: -32602, wantOutput: startupOAuthTargetRequired},
		{name: "provider", request: `{"id":1,"method":"account/gatewayOAuth/login","params":{"modelProvider":"company","target":{"kind":"configServer"}}}`, wantCode: -32602, wantOutput: startupOAuthTargetRequired},
		{name: "device auth", request: `{"id":1,"method":"account/gatewayOAuth/login","params":{"target":{"kind":"configServer"},"deviceAuth":true}}`, wantCode: -32602, wantOutput: startupOAuthBrowserOnly},
		{name: "unknown method", request: `{"id":1,"method":"thread/start"}`, wantCode: -32601, wantOutput: startupOAuthUnknownMethod},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			if err := serveAppServerStartupOAuth(context.Background(), cli.AppServerOptions{StartupOAuth: true}, &cli.RootOptions{}, strings.NewReader(tc.request+"\n"), &stdout); err != nil {
				t.Fatalf("serve error = %v", err)
			}
			messages := startupOAuthTestMessages(t, stdout.Bytes())
			if len(messages) != 1 {
				t.Fatalf("messages = %v", messages)
			}
			failure, _ := messages[0]["error"].(map[string]any)
			if numberID(failure["code"]) != tc.wantCode || failure["message"] != tc.wantOutput {
				t.Fatalf("error = %#v, want code %d message %q", failure, tc.wantCode, tc.wantOutput)
			}
		})
	}
}

func startupOAuthTestMessages(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var messages []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var message map[string]any
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		messages = append(messages, message)
	}
	return messages
}

func numberID(value any) int {
	number, ok := value.(float64)
	if !ok {
		return -1
	}
	return int(number)
}
