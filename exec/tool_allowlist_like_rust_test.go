package exec

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/mcp"
)

// Rust start_temporary_thread passes an empty tool allowlist so the hidden
// structured title/recap turn never advertises a tool. The same Request field
// must also work for ordinary turns that restrict the surface to a subset.
func TestRunResponsesRequestHonorsAllowedToolsLikeRust(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("access-token", "account-1", nil)); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isExecWebsocketUpgrade(r) {
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("request path = %q, want /v1/responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body returned error: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeExecResponseSSE(w, `{"type":"response.created","response":{"id":"resp-allow-tools"}}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.added","item":{"id":"msg-allow-tools","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`)
		writeExecResponseSSE(w, `{"type":"response.output_text.delta","item_id":"msg-allow-tools","delta":"ready"}`)
		writeExecResponseSSE(w, `{"type":"response.output_item.done","item":{"id":"msg-allow-tools","type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}}`)
		writeExecResponseSSE(w, `{"type":"response.completed","response":{"id":"resp-allow-tools","model":"gpt-5.5","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
	}))
	defer server.Close()
	configBody := "model = \"gpt-5.5\"\nopenai_base_url = \"" + server.URL + "/v1\"\n\n[features]\nenable_request_compression = false\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	run := func(allowed *[]string) []any {
		t.Helper()
		recordedBody = nil
		runner := NewRunner(home)
		runner.MCPService = mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{}})
		var stdout, stderr bytes.Buffer
		_, err := runner.Run(Request{
			AllowedTools: allowed,
			Exec: cli.ExecOptions{
				Prompt:    "say ready",
				JSON:      true,
				Ephemeral: true,
			},
		}, strings.NewReader(""), &stdout, &stderr)
		if err != nil {
			t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
		}
		tools, ok := recordedBody["tools"].([]any)
		if !ok {
			t.Fatalf("tools = %#v, body = %#v", recordedBody["tools"], recordedBody)
		}
		return tools
	}

	unrestricted := run(nil)
	if len(unrestricted) == 0 {
		t.Fatalf("unrestricted request should advertise tools: %#v", restrictedToolNamesForExecTest(unrestricted))
	}
	restricted := run(&[]string{"exec_command"})
	if len(restricted) == 0 {
		t.Fatalf("allowlisted request should keep the selected tool: %#v", restrictedToolNamesForExecTest(unrestricted))
	}
	if len(restricted) >= len(unrestricted) {
		t.Fatalf("restricted tools = %#v, want fewer than %#v", restrictedToolNamesForExecTest(restricted), restrictedToolNamesForExecTest(unrestricted))
	}
	for _, name := range restrictedToolNamesForExecTest(restricted) {
		if name != "exec_command" {
			t.Fatalf("restricted tools = %#v, want only exec_command", restrictedToolNamesForExecTest(restricted))
		}
	}
	empty := run(&[]string{})
	if len(empty) != 0 {
		t.Fatalf("empty allowlist should disable every tool: %#v", restrictedToolNamesForExecTest(empty))
	}
}

func restrictedToolNamesForExecTest(tools []any) []string {
	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := entry["name"].(string); ok && name != "" {
			names = append(names, name)
			continue
		}
		if fn, ok := entry["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}
