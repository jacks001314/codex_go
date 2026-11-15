package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/protocol"
	"codex_go/session"
	"codex_go/turn"
)

// TestContextWindowAsToolOutputLikeRust mirrors Rust #61452: the token-budget
// context-window metadata is a developer message by default and becomes an
// agent.thread_hint tool output when the setting is enabled.
func TestContextWindowAsToolOutputLikeRust(t *testing.T) {
	dir := t.TempDir()
	catalog, err := json.Marshal(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug:           "context-window-model",
		DisplayName:    "Context Window Model",
		Visibility:     model.VisibilityList,
		SupportedInAPI: true,
		Priority:       1,
		ContextWindow:  100_000,
	}}})
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	catalogPath := filepath.Join(dir, "models.json")
	if err := os.WriteFile(catalogPath, catalog, 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	store := session.NewStore(filepath.Join(dir, "sessions"))
	now := time.Now().UTC()
	const threadID = "thread-context-window-shape"
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:       dir,
			AgentPath: "/root",
		},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	first := router.contextWindowIDForThread(threadID)
	router.advanceContextWindowID(threadID)
	ids := router.autoCompactWindowIDsForThread(threadID)
	if ids.First != first || ids.Previous != first || ids.Current == "" || ids.Current == first {
		t.Fatalf("window lineage = %+v, want first/previous %q and a new current", ids, first)
	}
	if _, ok := protocol.ParseContextWindowID(ids.First); !ok {
		t.Fatalf("first window id %q is not a context window id", ids.First)
	}

	runTurn := func(t *testing.T, asToolOutput bool) []any {
		t.Helper()
		params := &turn.TurnStartParams{
			ThreadID: threadID,
			Prompt:   "hello",
			Model:    "context-window-model",
			Config: map[string]any{
				"model_catalog_json": catalogPath,
				"features": map[string]any{"token_budget": map[string]any{
					"enabled": true, "context_window_as_tool_output": asToolOutput,
				}},
			},
		}
		runConfig, err := router.appTurnConfig(context.Background(), threadID, "turn-context-window", "", params, now.UnixMilli(), nil)
		if err != nil {
			t.Fatalf("appTurnConfig() error = %v", err)
		}
		if runConfig == nil {
			t.Fatal("appTurnConfig() returned nil")
		}
		return runConfig.InputItems
	}

	for _, asToolOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "developer message", true: "tool output"}[asToolOutput], func(t *testing.T) {
			items := runTurn(t, asToolOutput)
			found := false
			for _, item := range items {
				object, _ := item.(map[string]any)
				if object == nil {
					continue
				}
				text := contextWindowTestItemText(object)
				if !strings.Contains(text, "/root") || !strings.Contains(text, ids.First) || !strings.Contains(text, ids.Current) || !strings.Contains(text, ids.Previous) {
					continue
				}
				found = true
				if asToolOutput {
					if object["type"] != "function_call_output" || object["namespace"] != "agent" || object["name"] != "thread_hint" {
						t.Fatalf("tool output shape = %#v", object)
					}
					output, _ := object["output"].(string)
					if strings.Contains(output, contextWindowOpenTag) || !strings.Contains(output, "Agent name: /root") || !strings.Contains(output, "Previous context window id: "+ids.Previous) {
						t.Fatalf("tool output = %q", output)
					}
				} else {
					if object["type"] != "message" || object["role"] != "developer" {
						t.Fatalf("developer message shape = %#v", object)
					}
					if !strings.Contains(text, contextWindowOpenTag) || !strings.Contains(text, contextWindowCloseTag) {
						t.Fatalf("context window message = %s", text)
					}
				}
			}
			if !found {
				t.Fatalf("context-window item with lineage %+v not found in %#v", ids, items)
			}
		})
	}
}

func contextWindowTestItemText(item map[string]any) string {
	if output, _ := item["output"].(string); output != "" {
		return output
	}
	content, _ := item["content"].([]map[string]any)
	parts := make([]string, 0, len(content))
	for _, part := range content {
		if text, _ := part["text"].(string); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}
