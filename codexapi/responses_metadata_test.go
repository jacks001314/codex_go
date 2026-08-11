package codexapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateResponsesAPIMetadataBoundsAndKeys(t *testing.T) {
	valid := map[string]string{
		"product.sku": "abc",
		"tier":        "pro",
	}
	if err := ValidateResponsesAPIMetadata(valid); err != nil {
		t.Fatalf("ValidateResponsesAPIMetadata(valid) = %v", err)
	}
	if err := ValidateResponsesAPIMetadata(nil); err != nil {
		t.Fatalf("ValidateResponsesAPIMetadata(nil) = %v", err)
	}
	over := map[string]string{}
	for i := 0; i < 17; i++ {
		over[strings.Repeat("k", 2)+string(rune('a'+i))] = "v"
	}
	if err := ValidateResponsesAPIMetadata(over); err == nil || !strings.Contains(err.Error(), "at most 16") {
		t.Fatalf("ValidateResponsesAPIMetadata(17 entries) = %v", err)
	}
	reserved := map[string]string{TurnIDKey: "x"}
	if err := ValidateResponsesAPIMetadata(reserved); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("ValidateResponsesAPIMetadata(reserved) = %v", err)
	}
	badKey := map[string]string{"bad key": "v"}
	if err := ValidateResponsesAPIMetadata(badKey); err == nil {
		t.Fatalf("ValidateResponsesAPIMetadata(space key) = nil")
	}
	digitKey := map[string]string{"1abc": "v"}
	if err := ValidateResponsesAPIMetadata(digitKey); err == nil {
		t.Fatalf("ValidateResponsesAPIMetadata(digit-leading key) = nil")
	}
	longValue := map[string]string{"k": strings.Repeat("v", 129)}
	if err := ValidateResponsesAPIMetadata(longValue); err == nil || !strings.Contains(err.Error(), "128") {
		t.Fatalf("ValidateResponsesAPIMetadata(long value) = %v", err)
	}
}

func TestClientMetadataProductMetadataPrecedenceOverExtra(t *testing.T) {
	metadata := NewClientMetadata("install", "session", "thread", "window")
	metadata.RequestKind = ClientRequestTurn
	metadata.Extra = map[string]string{"product.sku": "client-value", "client_only": "kept"}
	metadata.ResponsesAPIMetadata = map[string]string{"product.sku": "product-value"}
	value := metadata.TurnMetadataValue()
	if value["product.sku"] != "product-value" {
		t.Fatalf("product metadata must win over client metadata: %#v", value["product.sku"])
	}
	if value["client_only"] != "kept" {
		t.Fatalf("client extra should be preserved: %#v", value)
	}
}

func TestMetadataClientAndTurnMetadata(t *testing.T) {
	metadata := NewResponsesMetadata("install", "session", "thread", "window")
	metadata.TurnID = "turn"
	metadata.ParentThreadID = "parent"
	metadata.ParentTurnID = "parent-turn"
	metadata.SubagentHeader = "review"
	metadata.RequestKind = &ResponsesRequestKind{
		Kind:       RequestKindCompaction,
		Compaction: NewResponsesCompactionMetadata("manual", "userRequested", "local", "midTurn"),
	}
	metadata.Extra = map[string]string{"workspace_kind": "git", ThreadIDKey: "reserved"}
	client := metadata.ClientMetadata()
	if client[InstallationIDHeader] != "install" || client[TurnIDKey] != "turn" || client[OpenAISubagentHeader] != "review" {
		t.Fatalf("client = %#v", client)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(client[TurnMetadataHeader]), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload[RequestKindKey] != RequestKindCompaction || payload["workspace_kind"] != "git" || payload[ThreadIDKey] != "thread" || payload[ParentTurnIDKey] != "parent-turn" || client[ParentTurnIDKey] != "parent-turn" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestMemoryRequestOmitsTurnIdentity(t *testing.T) {
	metadata := NewResponsesMetadata("install", "session", "thread", "window")
	metadata.RequestKind = &ResponsesRequestKind{Kind: RequestKindMemory}
	payload := metadata.TurnMetadataValue()
	if _, ok := payload[ThreadIDKey]; ok {
		t.Fatalf("memory payload should omit thread id: %#v", payload)
	}
	if payload[RequestKindKey] != RequestKindMemory {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSandboxModeReservedAndEmitted(t *testing.T) {
	metadata := NewResponsesMetadata("install", "session", "thread", "window")
	metadata.TurnID = "turn"
	metadata.SandboxMode = "workspace-write"
	metadata.RequestKind = &ResponsesRequestKind{Kind: RequestKindTurn}
	metadata.Extra = map[string]string{"sandbox_mode": "client-provided", "custom": "ok"}
	payload := metadata.TurnMetadataValue()
	if payload[SandboxModeKey] != "workspace-write" {
		t.Fatalf("sandbox_mode = %#v, want workspace-write", payload[SandboxModeKey])
	}
	if payload["custom"] != "ok" {
		t.Fatalf("custom extra should survive: %#v", payload)
	}
}

func TestFilterExtraMetadataAndSubagentHeader(t *testing.T) {
	filtered := FilterExtraMetadata(map[string]string{ThreadIDKey: "bad", "custom": "ok"})
	if len(filtered) != 1 || filtered["custom"] != "ok" {
		t.Fatalf("filtered = %#v", filtered)
	}
	if SubagentHeaderValue("thread_spawn") != "collab_spawn" || SubagentHeaderValue("subagent:worker") != "worker" {
		t.Fatalf("subagent headers wrong")
	}
}

func TestRustResponsesMetadataSubagentHeaderParity(t *testing.T) {
	cases := []struct {
		sessionSource string
		want          string
	}{
		{sessionSource: "review", want: "review"},
		{sessionSource: "compact", want: "compact"},
		{sessionSource: "memory_consolidation", want: "memory_consolidation"},
		{sessionSource: "thread_spawn", want: "collab_spawn"},
		{sessionSource: "subagent:custom-task", want: "custom-task"},
		{sessionSource: "unknown", want: ""},
	}
	for _, tt := range cases {
		if got := SubagentHeaderValue(tt.sessionSource); got != tt.want {
			t.Fatalf("SubagentHeaderValue(%q) = %q, want %q", tt.sessionSource, got, tt.want)
		}
	}
}
