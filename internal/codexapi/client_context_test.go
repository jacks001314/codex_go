package codexapi

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClientPromptFormattedInputStripsImageDetailsForLite(t *testing.T) {
	prompt := NewClientPrompt()
	prompt.Input = []ClientResponseItem{{
		Type: "message",
		Role: "user",
		Content: []ClientContentItem{
			{Kind: ClientContentText, Text: "hello"},
			{Kind: ClientContentImage, ImageURL: "data:", Detail: "high"},
		},
	}}
	got := prompt.FormattedInput(true)
	if got[0].Content[1].Detail != "" {
		t.Fatalf("FormattedInput(lite) detail = %q, want empty", got[0].Content[1].Detail)
	}
	if prompt.Input[0].Content[1].Detail != "high" {
		t.Fatalf("FormattedInput mutated prompt")
	}
}

func TestClientMetadataClientMetadataAndHeaders(t *testing.T) {
	metadata := NewClientMetadata("install", "session", "thread", "window")
	metadata.TurnID = "turn"
	metadata.RequestKind = ClientRequestTurn
	metadata.ParentThreadID = "parent"
	metadata.SubagentHeader = "review"
	metadata.Extra = map[string]string{"workspace_kind": "git", "thread_id": "bad"}
	client := metadata.ClientMetadata()
	if client[ClientCodexInstallationIDHeader] != "install" || client[ClientCodexWindowIDHeader] != "window" {
		t.Fatalf("ClientMetadata() = %v", client)
	}
	if client[ClientCodexParentThreadIDHeader] != "parent" || client[ClientOpenAISubagentHeader] != "review" {
		t.Fatalf("ClientMetadata() missing compatibility fields: %v", client)
	}
	if !strings.Contains(client[ClientCodexTurnMetadataHeader], `"workspace_kind":"git"`) {
		t.Fatalf("turn metadata missing extra: %s", client[ClientCodexTurnMetadataHeader])
	}
	if strings.Contains(client[ClientCodexTurnMetadataHeader], `"thread_id":"bad"`) {
		t.Fatalf("reserved extra leaked: %s", client[ClientCodexTurnMetadataHeader])
	}
	headers := metadata.CompatibilityHeaders()
	if headers[ClientCodexTurnMetadataHeader] == "" || headers[ClientCodexWindowIDHeader] != "window" {
		t.Fatalf("CompatibilityHeaders() = %v", headers)
	}
}

func TestClientMetadataMemoryRequestOmitsTurnIdentity(t *testing.T) {
	metadata := NewClientMetadata("install", "session", "thread", "window")
	metadata.RequestKind = ClientRequestMemory
	value := metadata.TurnMetadataValue()
	if _, ok := value["thread_id"]; ok {
		t.Fatalf("memory metadata included thread_id: %v", value)
	}
	if value["request_kind"] != "memory" {
		t.Fatalf("request_kind = %v", value["request_kind"])
	}
}

func TestClientFilterExtraMetadata(t *testing.T) {
	got := ClientFilterExtraMetadata(map[string]string{
		"thread_id":      "bad",
		"workspace_kind": "git",
	})
	if got["thread_id"] != "" || got["workspace_kind"] != "git" {
		t.Fatalf("ClientFilterExtraMetadata() = %v", got)
	}
}

func TestClientRetryStateHandleRetriesFallbackAndFailure(t *testing.T) {
	state := &ClientRetryState{MaxRetries: 2}
	decision := state.Handle(&ClientRetryableError{Message: "stream", RequestedDelay: time.Second}, ClientRetrySampling, false, true, false)
	if !decision.Retry || decision.Delay != time.Second || decision.NotifyUser {
		t.Fatalf("first retry decision = %#v", decision)
	}
	decision = state.Handle(errors.New("stream"), ClientRetrySampling, false, true, false)
	if !decision.Retry || !decision.NotifyUser {
		t.Fatalf("second retry decision = %#v", decision)
	}
	decision = state.Handle(errors.New("stream"), ClientRetrySampling, true, true, false)
	if !decision.Retry || !decision.Fallback || state.Retries != 0 {
		t.Fatalf("fallback decision = %#v state=%#v", decision, state)
	}
	state.UsedFallback = true
	state.Retries = 2
	decision = state.Handle(errors.New("final"), ClientRetrySampling, true, true, false)
	if decision.Error == nil {
		t.Fatalf("final decision error = nil")
	}
}

func TestClientBackoff(t *testing.T) {
	if ClientBackoff(1) != 100*time.Millisecond || ClientBackoff(3) != 400*time.Millisecond {
		t.Fatalf("Backoff values unexpected")
	}
}

func TestClientSubagentHeaderValue(t *testing.T) {
	cases := map[string]string{
		"subagent:review":                 "review",
		"subagent:thread_spawn":           "collab_spawn",
		"subagent_review":                 "review",
		"subagent_thread_spawn_parent_d2": "collab_spawn",
		"subagent_guardian":               "guardian",
		"internal:memory_consolidation":   "memory_consolidation",
		"internal_memory_consolidation":   "memory_consolidation",
		"cli":                             "",
	}
	for input, want := range cases {
		if got := ClientSubagentHeaderValue(input); got != want {
			t.Fatalf("ClientSubagentHeaderValue(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClientSubagentMetadataKind(t *testing.T) {
	cases := map[string]string{
		"subagent:review":                 "review",
		"subagent:thread_spawn":           "thread_spawn",
		"subagent_thread_spawn_parent_d2": "thread_spawn",
		"subagent_guardian":               "guardian",
		"internal_memory_consolidation":   "",
		"cli":                             "",
	}
	for input, want := range cases {
		if got := ClientSubagentMetadataKind(input); got != want {
			t.Fatalf("ClientSubagentMetadataKind(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClientCompactionMetadataDefaults(t *testing.T) {
	metadata := &ClientCompactionMetadata{}
	metadata.EnsureDefaults()
	if metadata.Strategy != "memento" {
		t.Fatalf("Strategy = %q, want memento", metadata.Strategy)
	}
}
