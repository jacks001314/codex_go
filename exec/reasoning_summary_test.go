package exec

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/mcp"
)

// TestRunAgentRequestUsesConfiguredReasoningSummaryLikeRust covers #43921's
// request-side behavior for the exec runner: the effective
// `model_reasoning_summary` config value reaches the model request.
func TestRunAgentRequestUsesConfiguredReasoningSummaryLikeRust(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("access-token", "account-1", nil)); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-5.5\"\nmodel_reasoning_summary = \"concise\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	agent := &recordingAgent{message: "ok"}
	runner := NewRunner(home)
	runner.Agent = agent
	runner.MCPService = mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{}})

	var stdout, stderr bytes.Buffer
	if _, err := runner.Run(Request{
		Exec: cli.ExecOptions{Prompt: "hello", Ephemeral: true},
	}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("Run returned error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
	if agent.request == nil {
		t.Fatal("agent request is nil")
	}
	if agent.request.ReasoningSummary != "concise" {
		t.Fatalf("agent request reasoning summary = %q, want concise", agent.request.ReasoningSummary)
	}
}
