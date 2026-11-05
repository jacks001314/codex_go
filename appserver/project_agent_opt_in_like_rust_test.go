package appserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

// TestProjectAgentOptInResolvesRuntimeToolsLikeRust ports the tool-selection
// part of Rust #59476/#59496: migrating a project to [agents].enabled=true
// restores V1 agent tools over a lower-priority user opt-out, while a session
// opt-out still wins and a model-selected V2 thread keeps its V2 surface.
func TestProjectAgentOptInResolvesRuntimeToolsLikeRust(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(project, ".gcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	trusted := "\n[projects.\"" + strings.ReplaceAll(filepath.Clean(project), `\`, `\\`) + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nmulti_agent = false\n"+trusted), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ProjectConfigPath(project), []byte("[agents]\nenabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	manager := model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
		{Slug: "catalog-v2", DisplayName: "catalog-v2", Visibility: model.VisibilityVisible, SupportedInAPI: true, MultiAgentVersion: "v2"},
		{Slug: "catalog-unspecified", DisplayName: "catalog-unspecified", Visibility: model.VisibilityVisible, SupportedInAPI: true},
	}})
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Models: model.NewModelService(manager)})

	tests := []struct {
		name      string
		model     string
		overrides []string
		want      agent.MultiAgentVersion
	}{
		{name: "project opt-in restores v1", model: "catalog-unspecified", want: agent.VersionV1},
		{name: "project opt-in keeps model selected v2", model: "catalog-v2", want: agent.VersionV2},
		{name: "session opt-out wins", model: "catalog-unspecified", overrides: []string{"features.collab=false"}, want: ""},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.LoadEffectiveWithOptions(home, &config.EffectiveOptions{CWD: project, RawOverrides: tc.overrides})
			if err != nil {
				t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
			}
			agentsConfig, err := cfg.AgentsConfig(project)
			if err != nil {
				t.Fatalf("AgentsConfig() error = %v", err)
			}
			record := &session.Record{
				ID: session.ThreadID(fmt.Sprintf("project-opt-in-%d", i)), SessionID: fmt.Sprintf("project-opt-in-%d", i),
				CreatedAt: now, UpdatedAt: now, RecencyAt: now,
				Metadata: session.Metadata{CWD: project, Model: tc.model},
			}
			if err := store.Create(record); err != nil {
				t.Fatal(err)
			}
			if got := router.runtimeMultiAgentVersionForThread(string(record.ID), cfg, agentsConfig); got != tc.want {
				t.Fatalf("runtimeMultiAgentVersionForThread() = %q, want %q", got, tc.want)
			}
		})
	}
}
