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

func metadataWithOriginRemote(url string) *ResponsesMetadata {
	metadata := NewResponsesMetadata("install", "session", "thread", "window")
	metadata.TurnID = "turn"
	metadata.RequestKind = &ResponsesRequestKind{Kind: RequestKindTurn}
	hasChanges := false
	metadata.Workspaces["/workspace/東京"] = ResponsesWorkspace{
		AssociatedRemoteURLs: map[string]string{"origin": url},
		LatestGitCommitHash:  "abc123",
		HasChanges:           &hasChanges,
	}
	return metadata
}

func unboundedTurnMetadataJSON(t *testing.T, metadata *ResponsesMetadata) string {
	t.Helper()
	value, ok := asciiJSONString(metadata.TurnMetadataValue())
	if !ok {
		t.Fatal("asciiJSONString() failed")
	}
	return value
}

// Rust #61488 bounds turn metadata at the Responses API limit while keeping
// complete remote URLs and every non-remote field.
func TestTurnMetadataKeepsExactBoundaryAndOmitsOverlongRemote(t *testing.T) {
	prefix := "https://example.invalid/"
	initialLen := len(unboundedTurnMetadataJSON(t, metadataWithOriginRemote(prefix)))
	if initialLen >= MaxTurnMetadataBytes {
		t.Fatalf("initial metadata length = %d, want below %d", initialLen, MaxTurnMetadataBytes)
	}
	boundaryURL := prefix + strings.Repeat("x", MaxTurnMetadataBytes-initialLen)
	boundary := metadataWithOriginRemote(boundaryURL)
	expected := unboundedTurnMetadataJSON(t, boundary)
	if len(expected) != MaxTurnMetadataBytes {
		t.Fatalf("boundary length = %d, want %d", len(expected), MaxTurnMetadataBytes)
	}
	encoded, ok := boundary.TurnMetadataJSON()
	if !ok || encoded != expected {
		t.Fatalf("boundary metadata changed at the exact limit")
	}

	over := metadataWithOriginRemote(boundaryURL + "x")
	encoded, ok = over.TurnMetadataJSON()
	if !ok {
		t.Fatal("TurnMetadataJSON() failed")
	}
	if len(encoded) > MaxTurnMetadataBytes {
		t.Fatalf("bounded length = %d, want at most %d", len(encoded), MaxTurnMetadataBytes)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	workspaces, _ := payload[WorkspacesKey].(map[string]any)
	workspace, _ := workspaces["/workspace/東京"].(map[string]any)
	if workspace == nil {
		t.Fatalf("workspace metadata was dropped: %#v", payload[WorkspacesKey])
	}
	if _, ok := workspace["associated_remote_urls"]; ok {
		t.Fatalf("overlong remote should be omitted, not truncated")
	}
	if workspace["latest_git_commit_hash"] != "abc123" || workspace["has_changes"] != false {
		t.Fatalf("non-remote workspace fields changed: %#v", workspace)
	}
	if payload["thread_id"] != "thread" || payload["request_kind"] != RequestKindTurn {
		t.Fatalf("non-workspace fields changed: %#v", payload)
	}
}

func TestTurnMetadataSerializesAsASCII(t *testing.T) {
	encoded := unboundedTurnMetadataJSON(t, metadataWithOriginRemote("https://example.invalid/repo.git"))
	for _, character := range encoded {
		if character >= 0x7f {
			t.Fatalf("turn metadata kept a non-ASCII rune %q in %q", character, encoded)
		}
	}
	if !strings.Contains(encoded, `\u6771\u4eac`) {
		t.Fatalf("workspace path was not ASCII-escaped: %q", encoded)
	}
}

func TestClientTurnMetadataAppliesTheSameRemoteBudget(t *testing.T) {
	metadata := NewClientMetadata("install", "session", "thread", "window")
	metadata.RequestKind = ClientRequestTurn
	metadata.Workspaces["/workspace"] = ClientWorkspaceMetadata{
		AssociatedRemoteURLs: map[string]string{
			"origin":   "https://example.invalid/" + strings.Repeat("o", MaxTurnMetadataBytes),
			"upstream": "https://example.invalid/upstream.git",
		},
	}
	encoded, ok := metadata.TurnMetadataJSON()
	if !ok {
		t.Fatal("TurnMetadataJSON() failed")
	}
	if len(encoded) > MaxTurnMetadataBytes {
		t.Fatalf("bounded length = %d, want at most %d", len(encoded), MaxTurnMetadataBytes)
	}
	if !strings.Contains(encoded, "upstream.git") {
		t.Fatalf("shorter remote should survive the budget: %q", encoded)
	}
	if strings.Contains(encoded, strings.Repeat("o", 64)) {
		t.Fatalf("overlong remote should be omitted")
	}
}
