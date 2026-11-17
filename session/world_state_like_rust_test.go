package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust persists `WorldStateItem.state` keyed by the world-state section ID
// (core/src/context/world_state/*, `WorldStateSection::ID`). A rollout recorded
// by the Rust binary therefore uses snake_case section IDs, for example
// `{"apps_instructions":false,"collaboration_mode":{...},"plugins_instructions":true}`.
func TestDecodeWorldStateReadsRustSectionIDsLikeRust(t *testing.T) {
	raw := json.RawMessage(`{
		"agents_md": {},
		"apps_instructions": false,
		"collaboration_mode": {"mode": "default", "model": "test-model", "instructions": "90f8f6675171813762488f76c60b062fc8adeaec"},
		"model": "test-model",
		"model_catalog": "ea1a55de2973a50e2e404222c5499c24df5591ed",
		"plugins_instructions": true,
		"skills": {"includeInstructions": true},
		"host_skills": {"body": "\n## Skills\n"}
	}`)

	state, err := DecodeWorldState(raw)
	if err != nil {
		t.Fatalf("DecodeWorldState() error = %v", err)
	}
	if string(state.AppsInstructions) != "false" {
		t.Fatalf("AppsInstructions = %s", state.AppsInstructions)
	}
	var collaborationMode struct {
		Mode         string `json:"mode"`
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(state.CollaborationMode, &collaborationMode); err != nil ||
		collaborationMode.Mode != "default" || collaborationMode.Model != "test-model" ||
		collaborationMode.Instructions != "90f8f6675171813762488f76c60b062fc8adeaec" {
		t.Fatalf("CollaborationMode = %s, err = %v", state.CollaborationMode, err)
	}
	if string(state.Model) != `"test-model"` {
		t.Fatalf("Model = %s", state.Model)
	}
	if string(state.ModelCatalog) != `"ea1a55de2973a50e2e404222c5499c24df5591ed"` {
		t.Fatalf("ModelCatalog = %s", state.ModelCatalog)
	}
	if string(state.PluginsInstructions) != "true" {
		t.Fatalf("PluginsInstructions = %s", state.PluginsInstructions)
	}
	var skills struct {
		IncludeInstructions bool `json:"includeInstructions"`
	}
	if err := json.Unmarshal(state.SkillsCatalog, &skills); err != nil || !skills.IncludeInstructions {
		t.Fatalf("SkillsCatalog = %s, err = %v", state.SkillsCatalog, err)
	}
	if !strings.Contains(string(state.HostSkillsCatalog), "## Skills") {
		t.Fatalf("HostSkillsCatalog = %s", state.HostSkillsCatalog)
	}
}

// Re-encoding must restore Rust's key names and keep sections Go does not
// model, so a Go-written world_state stays readable by the Rust binary.
func TestEncodeWorldStateWritesRustSectionIDsAndKeepsUnknownLikeRust(t *testing.T) {
	raw := json.RawMessage(`{"agents_md":{"sha":"abc"},"collaboration_mode":{"mode":"plan","model":"m"},"model":"m"}`)
	state, err := DecodeWorldState(raw)
	if err != nil {
		t.Fatal(err)
	}
	state.PluginsInstructions = json.RawMessage(`{"instructions":"d3f660b06d428f262fffddce3d2b11f390d8b1da","catalog":false}`)

	encoded, err := EncodeWorldState(state)
	if err != nil {
		t.Fatal(err)
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &sections); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"agents_md", "collaboration_mode", "model", "plugins_instructions"} {
		if _, ok := sections[key]; !ok {
			t.Fatalf("encoded world state dropped %q: %s", key, encoded)
		}
	}
	for _, legacy := range []string{"agentsMd", "collaborationMode", "pluginsInstructions"} {
		if _, ok := sections[legacy]; ok {
			t.Fatalf("encoded world state kept legacy key %q: %s", legacy, encoded)
		}
	}
	if string(sections["agents_md"]) != `{"sha":"abc"}` {
		t.Fatalf("agents_md = %s", sections["agents_md"])
	}
}

// Stores written before the wire alignment used camelCase keys; they must keep
// decoding and migrate on the next write.
func TestDecodeWorldStateAcceptsLegacyGoKeys(t *testing.T) {
	raw := json.RawMessage(`{"collaborationMode":{"mode":"plan"},"pluginsInstructions":true,"skillsCatalog":{"entries":[]}}`)
	state, err := DecodeWorldState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state.CollaborationMode), `"mode":"plan"`) {
		t.Fatalf("CollaborationMode = %s", state.CollaborationMode)
	}
	if string(state.PluginsInstructions) != "true" {
		t.Fatalf("PluginsInstructions = %s", state.PluginsInstructions)
	}
	if string(state.SkillsCatalog) != `{"entries":[]}` {
		t.Fatalf("SkillsCatalog = %s", state.SkillsCatalog)
	}
}
