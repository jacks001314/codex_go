package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/plugin"
	"codex_go/session"
)

func pluginWorldStateTestRouter(t *testing.T) (*RuntimeRouter, string) {
	t.Helper()
	home := t.TempDir()
	pluginsService := plugin.NewPluginService()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	pluginsService.AddPlugin(plugin.PluginDetail{
		Summary: plugin.PluginSummary{
			ID: "docs@market", Name: "docs", MarketplaceName: "market",
			Installed: true, Enabled: true,
			Source: plugin.PluginSource{Type: "local", Path: root},
		},
		ManifestPath: filepath.Join(root, ".codex-plugin", "plugin.json"),
	})
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Plugins:      pluginsService,
		Config:       config.NewConfigService(home),
		DefaultCWD:   home,
	})
	const threadID = "thread-plugin-guidance"
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy), Extra: map[string]any{}},
	}); err != nil {
		t.Fatal(err)
	}
	return router, threadID
}

// TestPluginGuidanceWorldStateNoticesLikeRust covers the transition half of
// Rust #58894: guidance is emitted once, a catalog wording change emits a
// replacement notice, and retiring catalog guidance emits a removal notice
// exactly once.
func TestPluginGuidanceWorldStateNoticesLikeRust(t *testing.T) {
	router, threadID := pluginWorldStateTestRouter(t)
	open := &model.ModelInfo{Slug: "open-model", IncludePluginUsageInstructions: true}

	first, err := router.pluginGuidanceWorldStateInputItem(threadID, open)
	if err != nil {
		t.Fatalf("first item error = %v", err)
	}
	if first == nil || !strings.Contains(permissionsInputItemText(t, first), pluginInstructionsOpenTag) {
		t.Fatalf("first item = %#v, want the generic guidance", first)
	}
	repeat, err := router.pluginGuidanceWorldStateInputItem(threadID, open)
	if err != nil {
		t.Fatalf("repeat item error = %v", err)
	}
	if repeat != nil {
		t.Fatalf("unchanged guidance = %#v, want no item", repeat)
	}

	catalogIntro := "Catalog plugin intro."
	custom := &model.ModelInfo{
		Slug: "open-model", IncludePluginUsageInstructions: true,
		ModelMessages: &model.ModelMessages{Plugins: &model.PluginMessages{
			Generic: &model.GenericPluginMessages{Intro: &catalogIntro},
		}},
	}
	replacement, err := router.pluginGuidanceWorldStateInputItem(threadID, custom)
	if err != nil {
		t.Fatalf("replacement item error = %v", err)
	}
	text := permissionsInputItemText(t, replacement)
	if !strings.Contains(text, "replace all previously provided plugin usage instructions") || !strings.Contains(text, catalogIntro) {
		t.Fatalf("replacement text = %q", text)
	}

	gated := &model.ModelInfo{Slug: "gated-model", IncludePluginUsageInstructions: false}
	removal, err := router.pluginGuidanceWorldStateInputItem(threadID, gated)
	if err != nil {
		t.Fatalf("removal item error = %v", err)
	}
	if !strings.Contains(permissionsInputItemText(t, removal), "no longer apply") {
		t.Fatalf("removal item = %#v", removal)
	}
	after, err := router.pluginGuidanceWorldStateInputItem(threadID, gated)
	if err != nil {
		t.Fatalf("after-removal item error = %v", err)
	}
	if after != nil {
		t.Fatalf("retired guidance = %#v, want no further notice", after)
	}
}

// TestPluginGuidanceWorldStateBundledRemovalIsSilentLikeRust keeps the bundled
// guidance's previous silent removal behavior.
func TestPluginGuidanceWorldStateBundledRemovalIsSilentLikeRust(t *testing.T) {
	router, threadID := pluginWorldStateTestRouter(t)
	open := &model.ModelInfo{Slug: "open-model", IncludePluginUsageInstructions: true}
	if item, err := router.pluginGuidanceWorldStateInputItem(threadID, open); err != nil || item == nil {
		t.Fatalf("initial guidance = %#v, %v", item, err)
	}
	gated := &model.ModelInfo{Slug: "gated-model", IncludePluginUsageInstructions: false}
	item, err := router.pluginGuidanceWorldStateInputItem(threadID, gated)
	if err != nil {
		t.Fatalf("gated item error = %v", err)
	}
	if item != nil {
		t.Fatalf("bundled guidance removal = %#v, want silence", item)
	}
}
