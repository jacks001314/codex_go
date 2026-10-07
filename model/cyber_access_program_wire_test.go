package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/auth"
	"codex_go/codexapi"
)

// cyberAccessProgramsDisabledMessage is Rust's `CodexErr::InvalidRequest`
// message for an API-key session that selected a program while forwarding is off.
const cyberAccessProgramsDisabledMessage = "Cyber access programs are disabled for this API-key session."

// Mirrors Rust `core::cyber_access_program::for_auth` (#44893, #49714): the
// explicit per-turn program reaches the request for an authenticated human
// ChatGPT account, and for an API-key session only while the resolved policy
// allows it. Agent Identity and Bedrock credentials never send it.
func TestAccessProgramsForAuthGatesOnChatGPTAccountsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		program  string
		snapshot *auth.AuthDotJSON
		policy   ApiKeyCyberAccessPrograms
		want     string
		wantErr  bool
	}{
		{name: "chatgpt", program: "daybreak_blue", snapshot: &auth.AuthDotJSON{AuthMode: "chatgpt"}, policy: ApiKeyCyberAccessProgramsUnsupportedProvider, want: "daybreak_blue"},
		{name: "chatgpt tokens", program: "standard", snapshot: &auth.AuthDotJSON{AuthMode: "chatgptAuthTokens"}, policy: ApiKeyCyberAccessProgramsDisabled, want: "standard"},
		{name: "personal access token", program: "daybreak_red", snapshot: &auth.AuthDotJSON{AuthMode: "personalAccessToken"}, policy: ApiKeyCyberAccessProgramsDisabled, want: "daybreak_red"},
		// Rust #49714: the forwarding feature alone admits an API-key program.
		{name: "api key forwarding enabled", program: "daybreak_blue", snapshot: &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}, policy: ApiKeyCyberAccessProgramsEnabled, want: "daybreak_blue"},
		{name: "api key forwarding disabled", program: "daybreak_blue", snapshot: &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}, policy: ApiKeyCyberAccessProgramsDisabled, wantErr: true},
		{name: "api key unsupported provider", program: "daybreak_blue", snapshot: &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}, policy: ApiKeyCyberAccessProgramsUnsupportedProvider},
		{name: "api key without program", snapshot: &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}, policy: ApiKeyCyberAccessProgramsDisabled},
		{name: "api key unknown program", program: "future_program", snapshot: &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}, policy: ApiKeyCyberAccessProgramsDisabled},
		{name: "agent identity", program: "daybreak_blue", snapshot: &auth.AuthDotJSON{AuthMode: "agent-identity", AgentIdentity: map[string]any{"id": "a"}}, policy: ApiKeyCyberAccessProgramsEnabled},
		{name: "bedrock", program: "daybreak_blue", snapshot: &auth.AuthDotJSON{AuthMode: "bedrock-api-key", BedrockAPIKey: map[string]any{"api_key": "b"}}, policy: ApiKeyCyberAccessProgramsEnabled},
		{name: "no auth", program: "daybreak_blue", policy: ApiKeyCyberAccessProgramsEnabled},
		{name: "no program", snapshot: &auth.AuthDotJSON{AuthMode: "chatgpt"}},
		{name: "unknown program", program: "future_program", snapshot: &auth.AuthDotJSON{AuthMode: "chatgpt"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := AccessProgramsForAuth(testCase.program, testCase.snapshot, testCase.policy)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("AccessProgramsForAuth() error = nil, want the API-key invalid-request error")
				}
				if err.Error() != cyberAccessProgramsDisabledMessage {
					t.Fatalf("AccessProgramsForAuth() error = %q, want %q", err.Error(), cyberAccessProgramsDisabledMessage)
				}
				var apiErr *codexapi.APIError
				if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorInvalidRequest {
					t.Fatalf("AccessProgramsForAuth() error = %#v, want codexapi.ErrorInvalidRequest", err)
				}
				if got != nil {
					t.Fatalf("AccessProgramsForAuth() = %#v, want nil programs", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("AccessProgramsForAuth() error = %v", err)
			}
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

// Mirrors Rust `ApiKeyCyberAccessPrograms::from_config` (#49714): the policy is
// the OpenAI provider id plus `api_key_cyber_access_programs` alone, so
// `api_key_model_discovery` never changes the answer.
func TestApiKeyCyberAccessProgramsFromConfigLikeRust(t *testing.T) {
	for _, discovery := range []bool{false, true} {
		for _, forwarding := range []bool{false, true} {
			settings := map[string]bool{
				"api_key_model_discovery":       discovery,
				"api_key_cyber_access_programs": forwarding,
			}
			want := ApiKeyCyberAccessProgramsDisabled
			if forwarding {
				want = ApiKeyCyberAccessProgramsEnabled
			}
			if got := ApiKeyCyberAccessProgramsFromConfig(settings, OpenAIProviderID); got != want {
				t.Fatalf("ApiKeyCyberAccessProgramsFromConfig(model discovery %v, forwarding %v) = %v, want %v", discovery, forwarding, got, want)
			}
			if got := ApiKeyCyberAccessProgramsFromConfig(settings, "bedrock"); got != ApiKeyCyberAccessProgramsUnsupportedProvider {
				t.Fatalf("ApiKeyCyberAccessProgramsFromConfig(bedrock) = %v, want UnsupportedProvider", got)
			}
		}
	}
	// The feature defaults to off, so an absent entry is the disabled policy.
	if got := ApiKeyCyberAccessProgramsFromConfig(map[string]bool{}, OpenAIProviderID); got != ApiKeyCyberAccessProgramsDisabled {
		t.Fatalf("default policy = %v, want Disabled", got)
	}
	// A blank provider id is not the OpenAI provider id.
	if got := ApiKeyCyberAccessProgramsFromConfig(map[string]bool{"api_key_cyber_access_programs": true}, "  "); got != ApiKeyCyberAccessProgramsUnsupportedProvider {
		t.Fatalf("blank provider policy = %v, want UnsupportedProvider", got)
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

// The request body carries `access_programs` only for a turn whose credential
// and policy authorize it.
func TestResponsesAgentRunnerSendsAccessProgramsOnlyForChatGPTAuth(t *testing.T) {
	recordedBodies := newAccessProgramRecorder(t)

	body := recordedBodies.run(t, &auth.AuthDotJSON{AuthMode: "chatgpt"}, "daybreak_blue", ApiKeyCyberAccessProgramsUnsupportedProvider)
	programs, ok := body["access_programs"].(map[string]any)
	if !ok || programs["cyber"] != "daybreak_blue" {
		t.Fatalf("chatgpt body access_programs = %#v", body["access_programs"])
	}

	// No selection preserves the backend's automatic behavior.
	body = recordedBodies.run(t, &auth.AuthDotJSON{AuthMode: "chatgpt"}, "", ApiKeyCyberAccessProgramsUnsupportedProvider)
	if _, present := body["access_programs"]; present {
		t.Fatalf("an unselected program was sent: %#v", body["access_programs"])
	}

	// An unconfigured runner keeps the pre-#49714 behavior: the program is
	// dropped for an API-key account instead of failing the turn.
	body = recordedBodies.run(t, &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}, "daybreak_blue", ApiKeyCyberAccessProgramsUnsupportedProvider)
	if _, present := body["access_programs"]; present {
		t.Fatalf("API-key request carried access_programs: %#v", body["access_programs"])
	}

	// An Agent Identity credential uses the Codex backend but is not a ChatGPT
	// account (Rust `AuthMode::has_chatgpt_account`).
	body = recordedBodies.run(t, &auth.AuthDotJSON{AuthMode: "agent-identity", AgentIdentity: map[string]any{"id": "a"}}, "daybreak_blue", ApiKeyCyberAccessProgramsEnabled)
	if _, present := body["access_programs"]; present {
		t.Fatalf("agent-identity request carried access_programs: %#v", body["access_programs"])
	}
}

// Rust #49714: `features.api_key_cyber_access_programs` is enough on its own, so
// an API-key session reaches the wire with the selected program even while
// `features.api_key_model_discovery` is off.
func TestResponsesAgentRunnerForwardsAPIKeyAccessProgramsWhenForwardingEnabledLikeRust(t *testing.T) {
	recordedBodies := newAccessProgramRecorder(t)
	body := recordedBodies.run(t, &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}, "daybreak_blue", ApiKeyCyberAccessProgramsEnabled)
	programs, ok := body["access_programs"].(map[string]any)
	if !ok || programs["cyber"] != "daybreak_blue" {
		t.Fatalf("API-key body access_programs = %#v", body["access_programs"])
	}
}

// Rust `for_auth` fails the turn with `CodexErr::InvalidRequest` when an API-key
// session selects a program while forwarding is disabled.
func TestResponsesAgentRunnerRejectsAPIKeyAccessProgramsWhenForwardingDisabledLikeRust(t *testing.T) {
	recorder := newAccessProgramRecorder(t)
	runner := recorder.runner(&auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}, ApiKeyCyberAccessProgramsDisabled)
	_, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:             "hello",
		Model:              "gpt-test",
		CyberAccessProgram: "daybreak_blue",
	})
	if err == nil {
		t.Fatalf("Run() error = nil, want the API-key invalid-request error")
	}
	if err.Error() != cyberAccessProgramsDisabledMessage {
		t.Fatalf("Run() error = %q, want %q", err.Error(), cyberAccessProgramsDisabledMessage)
	}
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorInvalidRequest {
		t.Fatalf("Run() error = %#v, want codexapi.ErrorInvalidRequest", err)
	}
	if len(recorder.bodies) != 0 {
		t.Fatalf("a rejected program still issued %d request(s)", len(recorder.bodies))
	}
}

// accessProgramRecorder records the request bodies a runner sends to a stub
// provider so a test can assert on the exact wire payload.
type accessProgramRecorder struct {
	t       *testing.T
	baseURL string
	bodies  []map[string]any
}

func newAccessProgramRecorder(t *testing.T) *accessProgramRecorder {
	t.Helper()
	recorder := &accessProgramRecorder{t: t}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		recorder.bodies = append(recorder.bodies, body)
		_, _ = w.Write([]byte(`{"id":"resp-next","model":"gpt-test","output_text":"ok"}`))
	}))
	t.Cleanup(server.Close)
	recorder.baseURL = server.URL + "/v1"
	return recorder
}

func (r *accessProgramRecorder) runner(snapshot *auth.AuthDotJSON, policy ApiKeyCyberAccessPrograms) *ResponsesAgentRunner {
	r.t.Helper()
	return NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                  &APIProvider{BaseURL: r.baseURL},
		AuthSnapshot:              snapshot,
		ApiKeyCyberAccessPrograms: policy,
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug: "gpt-test",
		}}}),
	})
}

func (r *accessProgramRecorder) run(t *testing.T, snapshot *auth.AuthDotJSON, program string, policy ApiKeyCyberAccessPrograms) map[string]any {
	t.Helper()
	runner := r.runner(snapshot, policy)
	if _, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:             "hello",
		Model:              "gpt-test",
		CyberAccessProgram: program,
	}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	return r.bodies[len(r.bodies)-1]
}
