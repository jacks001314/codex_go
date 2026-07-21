package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginProviderResolvedPlugin(t *testing.T) {
	manifest := PluginManifestWithResources{
		Name:        "sample",
		Version:     "1.0.0",
		Description: "Test plugin",
		Paths: PluginManifestPathsWithResources{
			Skills: []PluginResourceLocator{
				{EnvironmentID: "local-env", Path: "/plugins/sample/skills/review.md"},
			},
		},
	}
	envID := "local-env"
	root := "/plugins/sample"
	manifestPath := "/plugins/sample/.codex-plugin/plugin.json"

	resolved, err := NewResolvedPluginFromEnvironment("root-1", envID, root, manifestPath, manifest)
	if err != nil {
		t.Fatalf("NewResolvedPluginFromEnvironment() error = %v", err)
	}
	if resolved.SelectedRootID != "root-1" {
		t.Fatalf("SelectedRootID = %q, want root-1", resolved.SelectedRootID)
	}
	if resolved.Location.EnvironmentID != envID || resolved.Location.Root != root {
		t.Fatalf("Location = %+v", resolved.Location)
	}
	if resolved.ManifestPath.EnvironmentID != envID || resolved.ManifestPath.Path != manifestPath {
		t.Fatalf("ManifestPath = %+v", resolved.ManifestPath)
	}
	if len(resolved.Manifest.Paths.Skills) != 1 {
		t.Fatalf("skills len = %d, want 1", len(resolved.Manifest.Paths.Skills))
	}
	skill := resolved.Manifest.Paths.Skills[0]
	if skill.EnvironmentID != envID || skill.Path != "/plugins/sample/skills/review.md" {
		t.Fatalf("skill locator = %+v", skill)
	}
}

func TestNewResolvedPluginFromEnvironmentResourceOutsideRoot(t *testing.T) {
	manifest := PluginManifestWithResources{
		Name: "sample",
		Paths: PluginManifestPathsWithResources{
			Skills: []PluginResourceLocator{
				{EnvironmentID: "", Path: "/other-root/skills/review.md"},
			},
		},
	}
	_, err := NewResolvedPluginFromEnvironment("root-1", "local-env", "/plugins/sample", "/plugins/sample/manifest.json", manifest)
	if err == nil {
		t.Fatalf("expected error for resource outside root, got nil")
	}
	if !strings.Contains(err.Error(), "outside package root") {
		t.Fatalf("error = %v, want 'outside package root'", err)
	}
}

func TestAppMcpRoutingPolicy(t *testing.T) {
	// When apps are routable and there's overlap
	mcpNames := []string{"github-issues", "slack"}
	apps := []AppSummary{
		{ID: "slack", DisplayName: "Slack"},
		{ID: "github", DisplayName: "GitHub"},
	}
	filtered, filteredApps := ApplyAppMcpRoutingPolicy("chatgpt", mcpNames, apps, nil)
	if len(filtered) != 1 || filtered[0] != "github-issues" {
		t.Fatalf("filtered MCP = %v, want [github-issues]", filtered)
	}
	if len(filteredApps) != 2 {
		t.Fatalf("filtered apps = %v, want both apps", filteredApps)
	}

	// When apps are NOT routable, apps should be cleared
	_, filteredApps = ApplyAppMcpRoutingPolicy("offline", mcpNames, apps, nil)
	if len(filteredApps) != 0 {
		t.Fatalf("apps should be cleared when routing unavailable, got %v", filteredApps)
	}
}

func TestAppsRouteAvailable(t *testing.T) {
	routable := []string{"chatgpt", "oauth", "bearer", "api_key", "ChatGPT"}
	for _, mode := range routable {
		if !AppsRouteAvailable(mode) {
			t.Fatalf("AppsRouteAvailable(%q) should be true", mode)
		}
	}
	notRoutable := []string{"", "none", "offline", "local"}
	for _, mode := range notRoutable {
		if AppsRouteAvailable(mode) {
			t.Fatalf("AppsRouteAvailable(%q) should be false", mode)
		}
	}
}

func TestCollectPluginEnabledCandidates(t *testing.T) {
	tests := []struct {
		name   string
		edits  []ConfigEdit
		want   map[string]bool
	}{
		{
			name: "direct toggle",
			edits: []ConfigEdit{
				{Key: "plugins.github@openai-curated.enabled", Value: true},
			},
			want: map[string]bool{"github@openai-curated": true},
		},
		{
			name: "table with enabled",
			edits: []ConfigEdit{
				{Key: "plugins.github@openai-curated", Value: map[string]any{"enabled": false}},
			},
			want: map[string]bool{"github@openai-curated": false},
		},
		{
			name: "full plugins map",
			edits: []ConfigEdit{
				{Key: "plugins", Value: map[string]any{
					"github@openai-curated": map[string]any{"enabled": true},
					"slack@openai-curated":  map[string]any{"enabled": false},
				}},
			},
			want: map[string]bool{"github@openai-curated": true, "slack@openai-curated": false},
		},
		{
			name: "override",
			edits: []ConfigEdit{
				{Key: "plugins.github@openai-curated.enabled", Value: false},
				{Key: "plugins.github@openai-curated.enabled", Value: true},
			},
			want: map[string]bool{"github@openai-curated": true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toggles := CollectPluginEnabledCandidates(tc.edits)
			got := map[string]bool{}
			for _, toggle := range toggles {
				got[toggle.PluginID.Key()] = toggle.Enabled
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("toggle[%s] = %v, want %v (got: %v)", k, got[k], v, got)
				}
			}
		})
	}
}

func TestCommandMigration(t *testing.T) {
	root := t.TempDir()

	// Create a plugin with commands
	pluginRoot := filepath.Join(root, "plugins", "sample")
	cmdDir := filepath.Join(pluginRoot, "commands")
	os.MkdirAll(cmdDir, 0o755)

	// Write a valid command
	validCmd := `---
name: deploy
description: Deploy the application to production
---

Run the deployment script to push changes to production.
`
	os.WriteFile(filepath.Join(cmdDir, "deploy.md"), []byte(validCmd), 0o600)

	// Write a command without frontmatter
	noFrontmatter := `# No frontmatter

Just some instructions.
`
	os.WriteFile(filepath.Join(cmdDir, "no-frontmatter.md"), []byte(noFrontmatter), 0o600)

	result, err := MigratePluginCommands(pluginRoot)
	if err != nil {
		t.Fatalf("MigratePluginCommands() error = %v", err)
	}
	if result.Migrated != 1 {
		t.Fatalf("result.Migrated = %d, want 1", result.Migrated)
	}
	if result.Skipped != 1 {
		t.Fatalf("result.Skipped = %d, want 1", result.Skipped)
	}

	// Verify migrated file exists
	migratedDir := filepath.Join(pluginRoot, migratedCommandSkillsDir)
	files, err := os.ReadDir(migratedDir)
	if err != nil {
		t.Fatalf("ReadDir(migrated) error = %v", err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".md") {
		t.Fatalf("migrated files = %v", files)
	}

	// Check content
	data, err := os.ReadFile(filepath.Join(migratedDir, files[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile(migrated) error = %v", err)
	}
	contents := string(data)
	if !strings.Contains(contents, "name: deploy") {
		t.Fatalf("migrated skill missing name: %s", contents)
	}
	if !strings.Contains(contents, "description: Deploy the application to production") {
		t.Fatalf("migrated skill missing description: %s", contents)
	}
}

func TestCommandMigrationSkipsUnsupportedFeatures(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins", "sample")
	cmdDir := filepath.Join(pluginRoot, "commands")
	os.MkdirAll(cmdDir, 0o755)

	// Command with $ARGUMENTS
	withArgs := `---
name: build
description: Build the project
---
Run ./build.sh $ARGUMENTS
`
	os.WriteFile(filepath.Join(cmdDir, "build.md"), []byte(withArgs), 0o600)

	result, err := MigratePluginCommands(pluginRoot)
	if err != nil {
		t.Fatalf("MigratePluginCommands() error = %v", err)
	}
	if result.Migrated != 0 || result.Skipped != 1 {
		t.Fatalf("result = %+v, want Migrated=0 Skipped=1", result)
	}
}
