package exec

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/model"
	"codex_go/reasoningoverride"
	"codex_go/session"
)

func execReasoningOverrideConfig(featureEnabled bool) *config.Config {
	values := map[string]any{}
	if featureEnabled {
		values["features"] = map[string]any{"reasoning_effort_override": true}
	}
	return &config.Config{Values: values}
}

func liteOpenAIModel() model.ModelInfo {
	return model.ModelInfo{Slug: "gpt-5", UseResponsesLite: true, SupportsReasoningEffortUpdates: true, DefaultReasoningLevel: "medium"}
}

// TestExecReasoningEffortOverridePinsAndRecordsLikeRust covers the exec entry
// point for Rust #43110/#43795: the first turn records a trusted update and
// pins the request baseline; later selections record an update while the
// request keeps the pinned baseline.
func TestExecReasoningEffortOverridePinsAndRecordsLikeRust(t *testing.T) {
	runner := NewRunner(t.TempDir())
	cfg := execReasoningOverrideConfig(true)
	info := liteOpenAIModel()

	items, effort, enabled := runner.execReasoningEffortOverride("thread-1", cfg, "gpt-5", &info, "openai", "high", nil)
	if len(items) != 1 {
		t.Fatalf("first turn override items = %#v, want one update", items)
	}
	if effort != "high" {
		t.Fatalf("first turn request effort = %q, want high", effort)
	}
	if !enabled {
		t.Fatal("first turn override must be enabled")
	}

	// Same selection: the pin already matches, so no duplicate update.
	items, effort, _ = runner.execReasoningEffortOverride("thread-1", cfg, "gpt-5", &info, "openai", "high", nil)
	if len(items) != 0 || effort != "high" {
		t.Fatalf("repeat selection items=%#v effort=%q, want none/high", items, effort)
	}

	// Changed selection: record the new effort, keep the pinned request baseline.
	items, effort, _ = runner.execReasoningEffortOverride("thread-1", cfg, "gpt-5", &info, "openai", "low", nil)
	if len(items) != 1 || effort != "high" {
		t.Fatalf("changed selection items=%#v effort=%q, want one update/pinned high", items, effort)
	}
}

func TestExecReasoningEffortOverrideGatingLikeRust(t *testing.T) {
	runner := NewRunner(t.TempDir())
	info := liteOpenAIModel()

	// Feature disabled: no update and the selected effort is used directly.
	items, effort, enabled := runner.execReasoningEffortOverride("thread-1", execReasoningOverrideConfig(false), "gpt-5", &info, "openai", "high", nil)
	if len(items) != 0 || effort != "high" {
		t.Fatalf("feature disabled items=%#v effort=%q, want none/high", items, effort)
	}
	if enabled {
		t.Fatal("feature disabled must not enable the override gate")
	}

	// Responses Lite alone does not establish revision support (#46530).
	unsupported := model.ModelInfo{Slug: "gpt-5", UseResponsesLite: true, DefaultReasoningLevel: "medium"}
	if items, _, enabled := runner.execReasoningEffortOverride("thread-2", execReasoningOverrideConfig(true), "gpt-5", &unsupported, "openai", "high", nil); len(items) != 0 || enabled {
		t.Fatalf("missing model support items = %#v, want none", items)
	}

	// Explicit model support is independent of Responses Lite.
	nonLiteSupported := model.ModelInfo{Slug: "gpt-5", SupportsReasoningEffortUpdates: true, DefaultReasoningLevel: "medium"}
	if items, effort, _ := runner.execReasoningEffortOverride("thread-2b", execReasoningOverrideConfig(true), "gpt-5", &nonLiteSupported, "openai", "high", nil); len(items) != 1 || effort != "high" {
		t.Fatalf("supported non-lite model items=%#v effort=%q, want one update/pinned high", items, effort)
	}

	// Non-OpenAI provider: no override.
	if items, _, enabled := runner.execReasoningEffortOverride("thread-3", execReasoningOverrideConfig(true), "gpt-5", &info, "azure", "high", nil); len(items) != 0 || enabled {
		t.Fatalf("non-openai items = %#v, want none", items)
	}
}

func TestExecConfigurationUpdateSessionItemIsTrustedLikeRust(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	item, ok := execConfigurationUpdateSessionItem("turn-1", 0, reasoningoverride.ConfigurationUpdateInputItem("high"), now, nil)
	if !ok {
		t.Fatal("configuration_update item was not persisted")
	}
	if item.Type != "configuration_update" {
		t.Fatalf("item type = %q", item.Type)
	}
	reasoning, _ := item.Data["reasoning"].(map[string]any)
	if strings.TrimSpace(reasoning["effort"].(string)) != "high" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	// A harness-authored update replays into model history; untrusted ones drop.
	replayed, _ := session.InputItemFromItem(&item, nil).(map[string]any)
	if replayed == nil || strings.TrimSpace(replayed["type"].(string)) != "configuration_update" {
		t.Fatalf("persisted item did not replay as a trusted update: %#v", replayed)
	}
	// Untrusted shapes are ignored.
	if _, ok := execConfigurationUpdateSessionItem("turn-1", 1, map[string]any{"type": "message"}, now, nil); ok {
		t.Fatal("non configuration_update item persisted as one")
	}
}

// TestExecTurnRecordsTrustedReasoningEffortUpdateLikeRust is the end-to-end
// wiring check: with the feature enabled for a Responses Lite OpenAI model, the
// exec turn carries a trusted configuration_update after the prompt and
// persists it as harness-authored history.
func TestExecTurnRecordsTrustedReasoningEffortUpdateLikeRust(t *testing.T) {
	home := t.TempDir()
	// Rust enables reasoning-effort updates only for models whose metadata sets
	// `supports_reasoning_effort_updates` (#46530); the bundled catalog omits it.
	catalogPath := filepath.Join(home, "models.json")
	if err := os.WriteFile(catalogPath, []byte(`{"models":[{
		"slug": "gpt-5.6-terra",
		"display_name": "GPT-5.6-Terra",
		"visibility": "list",
		"supported_in_api": true,
		"default_reasoning_level": "medium",
		"supported_reasoning_levels": ["low", "medium", "high"],
		"use_responses_lite": true,
		"supports_reasoning_effort_updates": true,
		"context_window": 272000,
		"max_context_window": 272000,
		"effective_context_window_percent": 95,
		"input_modalities": ["text", "image"]
	}]}`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	configText := fmt.Sprintf(`
model = "gpt-5.6-terra"
model_reasoning_effort = "high"
model_catalog_json = %q

[features]
reasoning_effort_override = true
`, filepath.ToSlash(catalogPath))
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configText), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	agent := &recordingAgent{message: "done"}
	runner := NewRunner(home)
	runner.Agent = agent

	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt: "hello",
			Shared: cli.SharedOptions{CWD: home, DangerouslyBypassApprovalsAndSandbox: true},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if agent.request == nil {
		t.Fatal("agent received no request")
	}
	if len(agent.request.PostPromptInputItems) != 1 {
		t.Fatalf("post-prompt items = %#v, want one configuration_update", agent.request.PostPromptInputItems)
	}
	update, ok := agent.request.PostPromptInputItems[0].(map[string]any)
	if !ok || strings.TrimSpace(update["type"].(string)) != "configuration_update" {
		t.Fatalf("post-prompt item = %#v", agent.request.PostPromptInputItems[0])
	}
	if agent.request.ReasoningEffort != "high" {
		t.Fatalf("request reasoning effort = %q, want pinned high", agent.request.ReasoningEffort)
	}

	store := session.NewStore(filepath.Join(home, "sessions"))
	record, err := store.Read(session.ThreadID(result.ThreadID), true, true)
	if err != nil {
		t.Fatalf("Read session record: %v", err)
	}
	found := false
	for i := range record.Items {
		if record.Items[i].Type != "configuration_update" {
			continue
		}
		found = true
		reasoning, _ := record.Items[i].Data["reasoning"].(map[string]any)
		if strings.TrimSpace(reasoning["effort"].(string)) != "high" {
			t.Fatalf("persisted reasoning = %#v", reasoning)
		}
		if replayed, _ := session.InputItemFromItem(&record.Items[i], nil).(map[string]any); replayed == nil {
			t.Fatal("persisted update is not harness-authored")
		}
	}
	if !found {
		t.Fatal("session record missing the trusted reasoning-effort update")
	}
}
