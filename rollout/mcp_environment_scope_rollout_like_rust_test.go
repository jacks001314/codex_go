package rollout

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// Rust #61257 writes the MCP registration scope on TurnItem::McpToolCall and
// replays it from both the canonical core payload and the public item stream.
// Go previously dropped environmentId on every path between them, so a resumed
// transcript could not tell same-named servers apart.
func TestMCPEnvironmentScopeSurvivesRolloutLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 17, 1, 2, 3, 0, time.UTC)
	item := session.Item{
		ID: "call-scoped", Type: "function_call", Name: "notes.lookup", CallID: "call-scoped",
		Text: `{"q":"x"}`, CreatedAt: now,
		Data: map[string]any{
			"mcpToolCall":   true,
			"server":        "notes",
			"tool":          "lookup",
			"environmentId": "env-b",
		},
		Metadata: map[string]any{"turnId": "turn-1"},
	}
	core, _, err := CoreTurnItemJSONFromSessionItem(&item)
	if err != nil {
		t.Fatalf("CoreTurnItemJSONFromSessionItem() error = %v", err)
	}
	var corePayload map[string]any
	if err := json.Unmarshal(core, &corePayload); err != nil {
		t.Fatalf("Unmarshal(core) error = %v", err)
	}
	if corePayload["environmentId"] != "env-b" {
		t.Fatalf("core payload = %#v", corePayload)
	}

	public, _, _, err := PublicThreadItemJSONFromCore(core)
	if err != nil {
		t.Fatalf("PublicThreadItemJSONFromCore() error = %v", err)
	}
	var publicPayload map[string]any
	if err := json.Unmarshal(public, &publicPayload); err != nil {
		t.Fatalf("Unmarshal(public) error = %v", err)
	}
	if publicPayload["environmentId"] != "env-b" {
		t.Fatalf("public payload = %#v", publicPayload)
	}

	migrated, ok, err := canonicalMcpToolCallItemPayload(json.RawMessage(`{"type":"McpToolCall","id":"call-scoped","server":"notes","environmentId":"env-b","tool":"lookup","arguments":{"q":"x"},"status":"completed","result":{"content":[],"isError":false}}`))
	if err != nil || !ok {
		t.Fatalf("canonicalMcpToolCallItemPayload() = %v, %v", ok, err)
	}
	if !strings.Contains(string(migrated), `"environmentId":"env-b"`) {
		t.Fatalf("canonical migration dropped the environment scope: %s", migrated)
	}

	replayed := SessionItemFromRolloutItem(&Item{
		Type:   "McpToolCall",
		ID:     "call-scoped",
		CallID: "call-scoped",
		Raw:    public,
		Data:   map[string]any{},
	}, now, 0)
	if replayed.Data["environmentId"] != "env-b" {
		t.Fatalf("replayed scope = %#v", replayed.Data["environmentId"])
	}

	local := item
	local.Data = map[string]any{"mcpToolCall": true, "server": "notes", "tool": "lookup"}
	localCore, _, err := CoreTurnItemJSONFromSessionItem(&local)
	if err != nil {
		t.Fatalf("CoreTurnItemJSONFromSessionItem(local) error = %v", err)
	}
	if strings.Contains(string(localCore), "environmentId") {
		t.Fatalf("local MCP call gained an environment scope: %s", localCore)
	}
}
