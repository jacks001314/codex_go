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
	"codex_go/model"
	"codex_go/protocol"
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

// TestExecReasoningSummaryStreamsInternallyOnly covers Rust #43921's status-row
// feed: reasoning summary deltas reach the internal (TUI) handler without
// changing the exec JSON event contract.
func TestExecReasoningSummaryStreamsInternallyOnly(t *testing.T) {
	var internal []protocol.ThreadEvent
	sink := &execEventSink{internalHandler: func(event protocol.ThreadEvent) { internal = append(internal, event) }}
	collector := &execStreamEventCollector{sink: sink}
	collector.Handle(&model.ResponsesStreamEvent{
		Kind:   model.ResponsesStreamEventReasoningSummaryTextDelta,
		ItemID: "reasoning-1",
		Delta:  "## Step one",
	})
	collector.Handle(&model.ResponsesStreamEvent{
		Kind:   model.ResponsesStreamEventReasoningSummaryPartAdded,
		ItemID: "reasoning-1",
	})

	if len(internal) != 2 {
		t.Fatalf("internal reasoning events = %#v, want two", internal)
	}
	if internal[0].Type != "item.reasoning.delta" || internal[0].Delta == nil || internal[0].Delta.Text != "## Step one" {
		t.Fatalf("reasoning delta event = %#v", internal[0])
	}
	if internal[1].Delta == nil || internal[1].Delta.Text != "\n" {
		t.Fatalf("reasoning section break = %#v", internal[1])
	}
	if events := sink.Events(); len(events) != 0 {
		t.Fatalf("reasoning events leaked into the JSON stream: %#v", events)
	}
}
