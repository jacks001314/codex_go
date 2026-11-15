package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"codex_go/chatgptapi"
)

// Mirrors Rust #61600 thread_title_tests: the title request uses the selected
// account's backend origin and routing override, skips commentary items, and
// returns the final structured message.
func TestCloudTitleGenerationUsesSelectedAccountLikeRust(t *testing.T) {
	var checkCalls, responseCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/wham/accounts/check":
			checkCalls++
			if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("chatgpt-account-id") != "cloud-account" {
				t.Errorf("accounts/check headers = %#v", r.Header)
			}
			_, _ = io.WriteString(w, `{"accounts":[
				{"id":"other-account","workspace_backend_origin":"https://other.chatgpt.com","account_routing_override":"us"},
				{"id":"cloud-account","workspace_backend_origin":"NO_CONSTRAINT","account_routing_override":"us_cr"}
			]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/codex/responses":
			responseCalls++
			if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("chatgpt-account-id") != "cloud-account" {
				t.Errorf("responses auth headers = %#v", r.Header)
			}
			if got := r.Header.Get("x-openai-account-routing-override"); got != "us_cr" {
				t.Errorf("routing header = %q, want us_cr", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			want := map[string]any{
				"model":        "title-model",
				"instructions": "Generate a thread title from the user prompt.",
				"input": []any{map[string]any{
					"type": "message",
					"role": "user",
					"content": []any{map[string]any{
						"type": "input_text",
						"text": "Fix the login timeout",
					}},
				}},
				"store":               false,
				"stream":              true,
				"tools":               []any{},
				"tool_choice":         "none",
				"parallel_tool_calls": false,
				"include":             []any{},
				"client_metadata":     map[string]any{"x-codex-turn-metadata": `{"thread_source":"thread_title"}`},
				"text": map[string]any{"format": map[string]any{
					"type":   "json_schema",
					"strict": true,
					"name":   "codex_output_schema",
					"schema": map[string]any{
						"type":                 "object",
						"properties":           map[string]any{"title": map[string]any{"type": "string"}},
						"required":             []any{"title"},
						"additionalProperties": false,
					},
				}},
				"reasoning": map[string]any{"effort": "low"},
			}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("title request body = %#v, want %#v", body, want)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range []string{
				`{"type":"response.output_item.done","item":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Considering a title"}]}}`,
				`{"type":"response.output_item.done","item":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"{\"title\":\"Login timeout\"}"}]}}`,
				`{"type":"response.completed","response":{"id":"response-1"}}`,
			} {
				_, _ = io.WriteString(w, "data: "+event+"\n\n")
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	bootstrap, err := url.Parse(server.URL + "/backend-api")
	if err != nil {
		t.Fatal(err)
	}
	client := &CloudTitleClient{
		bootstrap:   bootstrap,
		bearerToken: "token",
		accountID:   "cloud-account",
		httpClient:  server.Client(),
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"title": map[string]any{"type": "string"}},
		"required":             []any{"title"},
		"additionalProperties": false,
	}
	title, err := client.Generate(context.Background(), "title-model", "low", "Fix the login timeout", schema)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if title != `{"title":"Login timeout"}` {
		t.Fatalf("title = %q", title)
	}
	if checkCalls != 1 || responseCalls != 1 {
		t.Fatalf("calls = %d checks, %d responses", checkCalls, responseCalls)
	}
}

// Mirrors Rust #61600 routing validation: a configured backend must match the
// configured ChatGPT origin, and only known routing overrides are accepted.
func TestCloudTitleRoutingValidationLikeRust(t *testing.T) {
	bootstrap := mustParseCloudTitleURL(t, "https://chatgpt.com/backend-api")
	client := &CloudTitleClient{bootstrap: bootstrap, requireOriginMatch: true, accountID: "a"}
	noConstraint := "NO_CONSTRAINT"
	origin := "https://other.chatgpt.com"
	if got, err := client.resolveOrigin(&chatgptapi.AccountsCheckEntry{WorkspaceBackendOrigin: &noConstraint}); err != nil || got != "https://chatgpt.com" {
		t.Fatalf("NO_CONSTRAINT origin = %q, %v", got, err)
	}
	if _, err := client.resolveOrigin(&chatgptapi.AccountsCheckEntry{WorkspaceBackendOrigin: &origin}); err == nil {
		t.Fatal("configured origin mismatch accepted")
	}
	if _, err := client.resolveOrigin(&chatgptapi.AccountsCheckEntry{}); err == nil {
		t.Fatal("missing backend origin accepted")
	}
}

func mustParseCloudTitleURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// Mirrors Rust #61600: oversized titles are rejected rather than unbounded.
func TestCloudTitleRejectsOversizedOutputLikeRust(t *testing.T) {
	payload := `{"item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + strings.Repeat("x", cloudTitleMaxBytes+1) + `"}]}}`
	if _, _, err := cloudTitleItemText([]byte(payload)); err == nil {
		t.Fatal("oversized title accepted")
	}
}
