package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/plugin"
	"codex_go/turn"
)

func TestInstructionsWithPluginContextGatesGenericGuidanceByModelCapability(t *testing.T) {
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
	if len(pluginsService.EnabledCapabilities()) == 0 {
		t.Fatal("no enabled plugin capabilities")
	}

	manager := model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
		{Slug: "gated-model", BaseInstructions: "base", IncludePluginUsageInstructions: false},
		{Slug: "open-model", BaseInstructions: "base", IncludePluginUsageInstructions: true},
	}})
	router := NewRuntimeRouter(RuntimeServices{
		Plugins: pluginsService,
		Models:  model.NewModelService(manager),
	})

	gated := router.instructionsWithPluginContext("thread", nil, &turn.TurnStartParams{Model: "gated-model"}, "BASE")
	if strings.Contains(gated, "## Plugins") {
		t.Fatalf("gated model received generic plugin guidance:\n%s", gated)
	}
	if !strings.Contains(gated, "BASE") {
		t.Fatalf("base instructions lost for gated model:\n%s", gated)
	}
	open := router.instructionsWithPluginContext("thread", nil, &turn.TurnStartParams{Model: "open-model"}, "BASE")
	if !strings.Contains(open, "## Plugins") {
		t.Fatalf("open model did not receive generic plugin guidance:\n%s", open)
	}
}
