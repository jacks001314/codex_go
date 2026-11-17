package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	contextfrag "codex_go/context"
	"codex_go/model"
	"codex_go/plugin"
	"codex_go/session"
)

// Rust's committed world-state snapshot fixtures
// (core/src/context/world_state/snapshots/*.snap) are the authoritative
// fingerprints for these fragments.
func TestCollaborationModeSnapshotMatchesRustFragmentHashLikeRust(t *testing.T) {
	defaultText := "pair with the user"
	defaultInfo := &model.ModelInfo{Slug: "test-model", ModelMessages: &model.ModelMessages{
		CollaborationModes: &model.CollaborationModeMessages{Default: &defaultText},
	}}
	snapshot, ok := collaborationModeSnapshot(collaborationModeParamsForTest("default", "test-model", ""), defaultInfo, true)
	if !ok {
		t.Fatal("collaborationModeSnapshot() reported invalid state")
	}
	if snapshot.Instructions != "90f8f6675171813762488f76c60b062fc8adeaec" {
		t.Fatalf("default snapshot = %#v, want Rust's fragment hash", snapshot)
	}

	planText := "make a plan"
	planInfo := &model.ModelInfo{Slug: "test-model", ModelMessages: &model.ModelMessages{
		CollaborationModes: &model.CollaborationModeMessages{Plan: &planText},
	}}
	snapshot, ok = collaborationModeSnapshot(collaborationModeParamsForTest("plan", "test-model", ""), planInfo, true)
	if !ok {
		t.Fatal("collaborationModeSnapshot() reported invalid plan state")
	}
	if snapshot.Instructions != "bc50ed412c1138502df45ef1b6634a6ef618da4c" {
		t.Fatalf("plan snapshot = %#v, want Rust's fragment hash", snapshot)
	}
}

// Rust hashes the plugin guidance body with `WorldStateHash::from_json`, and
// the committed snapshot pins both the bundled wording and its fingerprint.
func TestPluginBundledGuidanceHashMatchesRustSnapshotLikeRust(t *testing.T) {
	body := contextfrag.PluginBundledGuidanceBody()
	if !strings.HasPrefix(body, "\n## Plugins\n") || !strings.HasSuffix(body, "\n") {
		t.Fatalf("bundled plugin body framing = %q", body)
	}
	if got := pluginGuidanceHash(body); got != "d3f660b06d428f262fffddce3d2b11f390d8b1da" {
		t.Fatalf("plugin guidance hash = %s, want Rust's snapshot fingerprint", got)
	}
}

func TestPluginInstructionsSnapshotDecodesRustFormsLikeRust(t *testing.T) {
	bundled := pluginBundledGuidanceHash()
	cases := []struct {
		name        string
		raw         string
		wantHash    string
		wantCatalog bool
	}{
		{"rust legacy available", `true`, bundled, false},
		{"rust legacy unavailable", `false`, "", false},
		{"rust current removal checkpoint", `{"catalog":true}`, "", true},
		{"rust current", `{"instructions":"abc","catalog":true}`, "abc", true},
		{"legacy go", `{"hash":"legacy","catalog":false}`, "legacy", false},
		{"unknown", `"unexpected"`, bundled, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := decodePluginInstructionsSnapshot(json.RawMessage(testCase.raw))
			if got.Instructions != testCase.wantHash || got.Catalog != testCase.wantCatalog {
				t.Fatalf("decode(%s) = %#v, want hash %q catalog %v", testCase.raw, got, testCase.wantHash, testCase.wantCatalog)
			}
		})
	}
}

// A session recorded by the Rust binary stores `plugins_instructions` as a
// bare bool. Go must read that legacy form so resumed threads do not repeat
// guidance the model already saw.
func TestPluginGuidanceReadsRustLegacySnapshotLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-plugin-rust-snapshot")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{WorldState: json.RawMessage(`{"plugins_instructions":true}`)},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		Name: "docs", MarketplaceName: "local", DisplayName: "Docs",
		Installed: true, Enabled: true, HasSkills: true,
	}})
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Plugins: plugins})
	t.Cleanup(func() { _ = router.Close() })

	item, err := router.pluginGuidanceWorldStateInputItem(string(threadID), &model.ModelInfo{
		Slug: "gpt-5.6-sol", IncludePluginUsageInstructions: true,
	})
	if err != nil {
		t.Fatalf("pluginGuidanceWorldStateInputItem() error = %v", err)
	}
	if item != nil {
		t.Fatalf("bundled guidance was re-emitted for a Rust legacy snapshot: %#v", item)
	}
	record, err := store.Load(threadID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatal(err)
	}
	if string(state.PluginsInstructions) != "true" {
		t.Fatalf("legacy snapshot rewritten before guidance changed: %s", state.PluginsInstructions)
	}
}

// A catalog-supplied guidance change replaces the wording once and persists
// Rust's `{"instructions","catalog"}` snapshot under the section ID.
func TestPluginGuidancePersistsRustSnapshotShapeLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-plugin-rust-snapshot-update")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{WorldState: json.RawMessage(`{"plugins_instructions":{"instructions":"stale","catalog":true}}`)},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		Name: "docs", MarketplaceName: "local", DisplayName: "Docs",
		Installed: true, Enabled: true, HasSkills: true,
	}})
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Plugins: plugins})
	t.Cleanup(func() { _ = router.Close() })

	item, err := router.pluginGuidanceWorldStateInputItem(string(threadID), &model.ModelInfo{
		Slug: "gpt-5.6-sol", IncludePluginUsageInstructions: true,
	})
	if err != nil {
		t.Fatalf("pluginGuidanceWorldStateInputItem() error = %v", err)
	}
	text := inputItemText(item)
	if !strings.Contains(text, pluginInstructionsReplacementPrefix) {
		t.Fatalf("changed guidance did not announce replacement: %s", text)
	}
	record, err := store.Load(threadID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot pluginInstructionsSnapshot
	if err := json.Unmarshal(state.PluginsInstructions, &snapshot); err != nil {
		t.Fatalf("snapshot = %s, err = %v", state.PluginsInstructions, err)
	}
	if snapshot.Instructions != pluginBundledGuidanceHash() || snapshot.Catalog {
		t.Fatalf("persisted snapshot = %#v, want bundled hash with catalog=false", snapshot)
	}
	if !strings.Contains(string(record.Metadata.WorldState), `"plugins_instructions"`) {
		t.Fatalf("world state did not use the Rust section ID: %s", record.Metadata.WorldState)
	}
}
