package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/auth"
)

// Mirrors Rust `core::cyber_access_program::for_auth` (#44893): the explicit
// per-turn program reaches the request only for an authenticated human ChatGPT
// account, so API-key, Agent Identity and Bedrock credentials never send it.
func TestAccessProgramsForAuthGatesOnChatGPTAccountsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		program  string
		snapshot *auth.AuthDotJSON
		want     string
	}{
		{name: "chatgpt", program: "daybreak_blue", snapshot: &auth.AuthDotJSON{AuthMode: "chatgpt"}, want: "daybreak_blue"},
		{name: "chatgpt tokens", program: "standard", snapshot: &auth.AuthDotJSON{AuthMode: "chatgptAuthTokens"}, want: "standard"},
		{name: "personal access token", program: "daybreak_red", snapshot: &auth.AuthDotJSON{AuthMode: "personalAccessToken"}, want: "daybreak_red"},
		{name: "api key", program: "daybreak_blue", snapshot: &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}},
		{name: "agent identity", program: "daybreak_blue", snapshot: &auth.AuthDotJSON{AuthMode: "agent-identity", AgentIdentity: map[string]any{"id": "a"}}},
		{name: "bedrock", program: "daybreak_blue", snapshot: &auth.AuthDotJSON{AuthMode: "bedrock-api-key", BedrockAPIKey: map[string]any{"api_key": "b"}}},
		{name: "no auth", program: "daybreak_blue"},
		{name: "no program", snapshot: &auth.AuthDotJSON{AuthMode: "chatgpt"}},
		{name: "unknown program", program: "future_program", snapshot: &auth.AuthDotJSON{AuthMode: "chatgpt"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := AccessProgramsForAuth(testCase.program, testCase.snapshot)
			if testCase.want == "" {
				if got != nil {
					t.Fatalf("AccessProgramsForAuth() = %#v, want nil", got)
				}
				return
			}
			if got == nil || got.Cyber != testCase.want {
				t.Fatalf("AccessProgramsForAuth() = %#v, want cyber=%q", got, testCase.want)
			}
		})
	}
}

func TestAccessProgramsWireShapeLikeRust(t *testing.T) {
	encoded, err := json.Marshal(AccessPrograms{Cyber: "daybreak_red"})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"cyber":"daybreak_red"}` {
		t.Fatalf("access programs wire = %s", encoded)
	}
}

// The request body carries `access_programs` only for a ChatGPT-authorized
// turn that selected one, and never for a request whose account cannot use it.
func TestResponsesAgentRunnerSendsAccessProgramsOnlyForChatGPTAuth(t *testing.T) {
	var recordedBodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		recordedBodies = append(recordedBodies, body)
		_, _ = w.Write([]byte(`{"id":"resp-next","model":"gpt-test","output_text":"ok"}`))
	}))
	defer server.Close()

	run := func(t *testing.T, snapshot *auth.AuthDotJSON, program string) map[string]any {
		t.Helper()
		runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
			Provider:     &APIProvider{BaseURL: server.URL + "/v1"},
			AuthSnapshot: snapshot,
			ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
				Slug: "gpt-test",
			}}}),
		})
		if _, err := runner.Run(context.Background(), &AgentRequest{
			Prompt:             "hello",
			Model:              "gpt-test",
			CyberAccessProgram: program,
		}); err != nil {
			t.Fatalf("Run error = %v", err)
		}
		body := recordedBodies[len(recordedBodies)-1]
		return body
	}

	body := run(t, &auth.AuthDotJSON{AuthMode: "chatgpt"}, "daybreak_blue")
	programs, ok := body["access_programs"].(map[string]any)
	if !ok || programs["cyber"] != "daybreak_blue" {
		t.Fatalf("chatgpt body access_programs = %#v", body["access_programs"])
	}

	// No selection preserves the backend's automatic behavior.
	body = run(t, &auth.AuthDotJSON{AuthMode: "chatgpt"}, "")
	if _, present := body["access_programs"]; present {
		t.Fatalf("an unselected program was sent: %#v", body["access_programs"])
	}

	// An API-key account cannot authorize a program, so the field is dropped.
	body = run(t, &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}, "daybreak_blue")
	if _, present := body["access_programs"]; present {
		t.Fatalf("API-key request carried access_programs: %#v", body["access_programs"])
	}

	// An Agent Identity credential uses the Codex backend but is not a ChatGPT
	// account (Rust `AuthMode::has_chatgpt_account`).
	body = run(t, &auth.AuthDotJSON{AuthMode: "agent-identity", AgentIdentity: map[string]any{"id": "a"}}, "daybreak_blue")
	if _, present := body["access_programs"]; present {
		t.Fatalf("agent-identity request carried access_programs: %#v", body["access_programs"])
	}
}
