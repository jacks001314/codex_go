package codexapi

import (
	"encoding/json"
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
	enabled := true
	metadata.AutoReviewEnabled = &enabled
	nodeReplAutoReviewRequired := true
	metadata.NodeReplAutoReviewRequired = &nodeReplAutoReviewRequired
	nodeReplDisabled := false
	metadata.NodeReplDisabled = &nodeReplDisabled
	metadata.ParentThreadID = "parent"
	metadata.ParentTurnID = "parent-turn"
	metadata.RootTurnID = "root-turn"
	metadata.SubagentHeader = "review"
	metadata.Extra = map[string]string{"workspace_kind": "git", "thread_id": "bad"}
	client := metadata.ClientMetadata()
	if client[ClientCodexInstallationIDHeader] != "install" || client[ClientCodexWindowIDHeader] != "window" {
		t.Fatalf("ClientMetadata() = %v", client)
	}
	if client[ClientCodexParentThreadIDHeader] != "parent" || client[ClientOpenAISubagentHeader] != "review" {
		t.Fatalf("ClientMetadata() missing compatibility fields: %v", client)
	}
	if client["parent_turn_id"] != "parent-turn" || !strings.Contains(client[ClientCodexTurnMetadataHeader], `"parent_turn_id":"parent-turn"`) {
		t.Fatalf("ClientMetadata() missing parent turn: %v", client)
	}
	if !strings.Contains(client[ClientCodexTurnMetadataHeader], `"root_turn_id":"root-turn"`) {
		t.Fatalf("ClientMetadata() missing root turn: %v", client)
	}
	if !strings.Contains(client[ClientCodexTurnMetadataHeader], `"workspace_kind":"git"`) {
		t.Fatalf("turn metadata missing extra: %s", client[ClientCodexTurnMetadataHeader])
	}
	if strings.Contains(client[ClientCodexTurnMetadataHeader], `"thread_id":"bad"`) {
		t.Fatalf("reserved extra leaked: %s", client[ClientCodexTurnMetadataHeader])
	}
	if !strings.Contains(client[ClientCodexTurnMetadataHeader], `"auto_review_enabled":true`) {
		t.Fatalf("turn metadata missing auto_review_enabled: %s", client[ClientCodexTurnMetadataHeader])
	}
	if !strings.Contains(client[ClientCodexTurnMetadataHeader], `"node_repl_auto_review_required":true`) ||
		!strings.Contains(client[ClientCodexTurnMetadataHeader], `"node_repl_disabled":false`) {
		t.Fatalf("turn metadata missing node repl policy: %s", client[ClientCodexTurnMetadataHeader])
	}
	headers := metadata.CompatibilityHeaders()
	if headers[ClientCodexTurnMetadataHeader] == "" || headers[ClientCodexWindowIDHeader] != "window" {
		t.Fatalf("CompatibilityHeaders() = %v", headers)
	}
}

func TestClientReservedMetadataKeysIncludeAutoReviewEnabled(t *testing.T) {
	if !ClientReservedMetadataKeys()["auto_review_enabled"] {
		t.Fatal("auto_review_enabled must be a reserved metadata key (Rust f2a6f2585c)")
	}
	if !ClientReservedMetadataKeys()["root_turn_id"] {
		t.Fatal("root_turn_id must be a reserved metadata key")
	}
	filtered := ClientFilterExtraMetadata(map[string]string{"auto_review_enabled": "client-value", "workspace_kind": "git"})
	if _, ok := filtered["auto_review_enabled"]; ok {
		t.Fatalf("client-provided auto_review_enabled leaked: %#v", filtered)
	}
	if filtered["workspace_kind"] != "git" {
		t.Fatalf("non-reserved extra was filtered: %#v", filtered)
	}
}

func TestClientReservedMetadataKeysIncludeNodeReplPolicy(t *testing.T) {
	reserved := ClientReservedMetadataKeys()
	if !reserved[NodeReplAutoReviewRequiredKey] || !reserved[NodeReplDisabledKey] {
		t.Fatalf("node repl policy keys are not reserved: %#v", reserved)
	}
	filtered := ClientFilterExtraMetadata(map[string]string{
		NodeReplAutoReviewRequiredKey: "client-value",
		NodeReplDisabledKey:           "client-value",
		"workspace_kind":              "git",
	})
	if _, ok := filtered[NodeReplAutoReviewRequiredKey]; ok {
		t.Fatalf("client-provided node repl policy leaked: %#v", filtered)
	}
	if _, ok := filtered[NodeReplDisabledKey]; ok {
		t.Fatalf("client-provided node repl disabled leaked: %#v", filtered)
	}
	if filtered["workspace_kind"] != "git" {
		t.Fatalf("non-reserved extra was filtered: %#v", filtered)
	}
}

func TestClientCompatibilityHeadersOmitUnboundedCodeModeToolNames(t *testing.T) {
	value := map[string]any{
		"thread_id": "thread",
		CodeModeToolNamesKey: map[string]any{
			"view_image": map[string]any{"name": "view_image", "namespace": nil},
		},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	bounded, ok := clientCompatibilityTurnMetadataJSON(value)
	if !ok || strings.Contains(bounded, CodeModeToolNamesKey) || !strings.Contains(bounded, `"thread_id":"thread"`) {
		t.Fatalf("bounded metadata = %q", bounded)
	}
	if !strings.Contains(string(encoded), CodeModeToolNamesKey) {
		t.Fatalf("canonical metadata unexpectedly missing mapping: %s", encoded)
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
		"thread_id":          "bad",
		"parent_turn_id":     "spoofed",
		CodeModeToolNamesKey: "bad",
		"workspace_kind":     "git",
	})
	if got["thread_id"] != "" || got["parent_turn_id"] != "" || got[CodeModeToolNamesKey] != "" || got["workspace_kind"] != "git" {
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
