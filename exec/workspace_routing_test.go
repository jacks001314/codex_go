package exec

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
)

// TestApplyExecWorkspaceRoutingLikeRust covers the exec provider path: a
// first-party ChatGPT provider on the Codex backend routes is rewritten to the
// discovered workspace backend and rejects redirects (Rust
// RuntimeProvider::responses_api_provider).
func TestApplyExecWorkspaceRoutingLikeRust(t *testing.T) {
	home := t.TempDir()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/accounts/check" {
			t.Fatalf("accounts check path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accounts":[{"id":"workspace","workspace_backend_origin":"https://gov.chatgpt.com","account_routing_override":"us_cr"}]}`))
	}))
	defer backend.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+backend.URL+`"`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.LoadWithOptions(home, &config.LoadOptions{CWD: home})
	if err != nil {
		t.Fatalf("LoadWithOptions error = %v", err)
	}
	provider := &model.ProviderInfo{Name: model.OpenAIProviderName, BaseURL: "https://chatgpt.com/backend-api/codex"}
	snapshot := auth.FromChatGPTAuthTokens("chatgpt-token", "workspace", nil)
	agent := model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{Name: model.OpenAIProviderName, BaseURL: provider.BaseURL},
	})

	runner := &Runner{}
	if err := runner.applyExecWorkspaceRouting(cfg, provider, &snapshot, agent); err != nil {
		t.Fatalf("applyExecWorkspaceRouting error = %v", err)
	}
	if got := agent.Provider.BaseURL; got != "https://gov.chatgpt.com/backend-api/codex" {
		t.Fatalf("routed BaseURL = %q", got)
	}
	if got := agent.Provider.Headers.Get(model.AccountRoutingHeader); got != "us_cr" {
		t.Fatalf("routing header = %q", got)
	}
	if !agent.RejectRedirects {
		t.Fatal("routed provider must reject redirects")
	}
}

// TestApplyExecWorkspaceRoutingSkipsNonCodexBackends keeps providers that may
// not use the Codex backend routes untouched.
func TestApplyExecWorkspaceRoutingSkipsNonCodexBackends(t *testing.T) {
	home := t.TempDir()
	cfg, err := config.LoadWithOptions(home, &config.LoadOptions{CWD: home})
	if err != nil {
		t.Fatalf("LoadWithOptions error = %v", err)
	}
	provider := &model.ProviderInfo{Name: model.OpenAIProviderName, BaseURL: "https://api.openai.com/v1"}
	snapshot := auth.FromChatGPTAuthTokens("chatgpt-token", "workspace", nil)
	agent := model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{Name: model.OpenAIProviderName, BaseURL: provider.BaseURL},
	})

	if err := (&Runner{}).applyExecWorkspaceRouting(cfg, provider, &snapshot, agent); err != nil {
		t.Fatalf("applyExecWorkspaceRouting error = %v", err)
	}
	if got := agent.Provider.BaseURL; got != "https://api.openai.com/v1" {
		t.Fatalf("BaseURL = %q, want unchanged", got)
	}
	if agent.RejectRedirects {
		t.Fatal("non-Codex-backend provider must not reject redirects")
	}
}
