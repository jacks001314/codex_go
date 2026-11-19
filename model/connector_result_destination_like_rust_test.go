package model

import (
	"testing"

	"codex_go/auth"
)

// connectorResultTestRunner builds a runner that satisfies every connector
// result-body destination condition, so a test can violate one at a time.
func connectorResultTestRunner() *ResponsesAgentRunner {
	return NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			Name:    OpenAIProviderName,
			BaseURL: ConnectorResultGatewayURL,
			Headers: map[string][]string{"version": {"go-port"}},
		},
		AuthSnapshot: &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens:   map[string]any{"chatgpt_account_id": "acct-1"},
		},
		ProviderRequiresOpenAIAuth: true,
	})
}

// Mirrors Rust sampling_result_owner (#62392): only a first-party ChatGPT
// credential against the Codex gateway may carry connector result bodies.
func TestConnectorResultDestinationAllowedLikeRust(t *testing.T) {
	if !connectorResultTestRunner().connectorResultDestinationAllowed() {
		t.Fatal("eligible runner was rejected")
	}
	cases := map[string]func(*ResponsesAgentRunner){
		"custom provider URL": func(r *ResponsesAgentRunner) { r.Provider.BaseURL = "https://proxy.example.com/v1" },
		"api-key auth": func(r *ResponsesAgentRunner) {
			r.AuthSnapshot = &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}
		},
		"provider-owned credential": func(r *ResponsesAgentRunner) { r.ProviderUsesOwnCredentials = true },
		"static auth config": func(r *ResponsesAgentRunner) {
			r.Provider.Auth = &ProviderAuthInfo{Command: "get-token"}
		},
		"routing header override": func(r *ResponsesAgentRunner) {
			r.Provider.Headers["x-custom-router"] = []string{"1"}
		},
		// Rust #62421: provider query parameters can retarget the request even
		// when the URL and headers still satisfy every other check.
		"provider query parameters": func(r *ResponsesAgentRunner) {
			r.Provider.QueryParams = map[string]string{"api-version": "2"}
		},
	}
	for name, mutate := range cases {
		runner := connectorResultTestRunner()
		mutate(runner)
		if runner.connectorResultDestinationAllowed() {
			t.Fatalf("%s: connectorResultDestinationAllowed() = true", name)
		}
	}
	// The runtime routing headers Rust still accepts must not disqualify.
	runner := connectorResultTestRunner()
	runner.Provider.Headers[AccountRoutingHeader] = []string{"us"}
	runner.Provider.Headers[ResidencyHeaderName] = []string{"us"}
	if !runner.connectorResultDestinationAllowed() {
		t.Fatal("runtime routing headers disqualified an eligible destination")
	}
}

// Mirrors Rust's AuthOwner identity: the owner names the credential class and
// the account, and an unidentifiable snapshot owns nothing.
func TestConnectorResultAuthOwnerLikeRust(t *testing.T) {
	runner := connectorResultTestRunner()
	if got := runner.connectorResultAuthOwner(); got != "chatgpt|acct-1" {
		t.Fatalf("connectorResultAuthOwner() = %q", got)
	}
	if got := ConnectorResultAuthOwner(nil); got != "" {
		t.Fatalf("ConnectorResultAuthOwner(nil) = %q", got)
	}
	if got := ConnectorResultAuthOwner(&auth.AuthDotJSON{AuthMode: "chatgpt"}); got != "" {
		t.Fatalf("owner without an account = %q", got)
	}
	if got := ConnectorResultAuthOwner(&auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk"}); got != "" {
		t.Fatalf("owner for an api-key snapshot = %q", got)
	}
}

// Mirrors Rust build_responses_request's ordering: the runner selects result
// bodies from the physical input and only for an eligible destination.
func TestBuildResponsesAgentRequestSelectsToolResultsLikeRust(t *testing.T) {
	runner := connectorResultTestRunner()
	var selectedInput []any
	request := &AgentRequest{
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ToolResultsForInput: func(input []any, owner string) []ToolResultAttachment {
			selectedInput = input
			if owner != "chatgpt|acct-1" {
				t.Errorf("owner = %q", owner)
			}
			return []ToolResultAttachment{{
				ItemIndex: 0,
				CallIndex: 0,
				Body:      mustToolResultBody(t, ToolResultBodySource{Content: []any{map[string]any{"type": "text", "text": "body"}}}),
			}}
		},
		ScopeToolResultsForInput: func(input []any) []any { return input },
		InputItems:               toolResultCarrierInput(),
	}
	apiRequest, err := runner.buildResponsesAgentRequest(request, false)
	if err != nil {
		t.Fatalf("buildResponsesAgentRequest error = %v", err)
	}
	if len(selectedInput) != len(apiRequest.Input) {
		t.Fatalf("selection ran on %d items, request has %d", len(selectedInput), len(apiRequest.Input))
	}
	if len(apiRequest.ToolResults) != 1 {
		t.Fatalf("apiRequest.ToolResults = %d, want 1", len(apiRequest.ToolResults))
	}

	ineligible := connectorResultTestRunner()
	ineligible.Provider.QueryParams = map[string]string{"api-version": "2"}
	apiRequest, err = ineligible.buildResponsesAgentRequest(request, false)
	if err != nil {
		t.Fatalf("buildResponsesAgentRequest error = %v", err)
	}
	if len(apiRequest.ToolResults) != 0 {
		t.Fatalf("apiRequest.ToolResults for an ineligible provider = %d, want 0", len(apiRequest.ToolResults))
	}
}

// Mirrors Rust with_tool_results' redirect fallback: the request-level bodies
// remain the caller's own when the runner selected none.
func TestToolResultsForRequestPrefersSelectedLikeRust(t *testing.T) {
	caller := &AgentRequest{ToolResults: []ToolResultAttachment{{ItemIndex: 0}}}
	if got := toolResultsForRequest(caller, &responsesAgentRequest{}); len(got) != 1 {
		t.Fatalf("fallback results = %d, want the caller's 1", len(got))
	}
	selected := &responsesAgentRequest{ToolResults: []ToolResultAttachment{{ItemIndex: 1}, {ItemIndex: 2}}}
	if got := toolResultsForRequest(caller, selected); len(got) != 2 {
		t.Fatalf("selected results = %d, want 2", len(got))
	}
}
