package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgentRoleFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLoadAgentRolesMergesLayersLikeRust mirrors Rust's
// agent_role_without_description_after_merge_is_dropped_with_warning merge
// semantics: a higher layer only fills the fields it defines, and a role that
// still lacks a description after the merge is dropped with a warning.
func TestLoadAgentRolesMergesLayersLikeRust(t *testing.T) {
	home := t.TempDir()
	roleFile := filepath.Join(home, "agents", "researcher.toml")
	writeAgentRoleFile(t, roleFile, "description = \"File researcher\"\nnickname_candidates = [\"Scout\"]\ndeveloper_instructions = \"Research carefully\"\nmodel = \"gpt-5\"\n")

	userLayer := Layer{
		Name: LayerSource{Type: LayerSourceUser, File: filepath.Join(home, "config.toml")},
		Config: map[string]any{"agents": map[string]any{
			"researcher": map[string]any{"config_file": "./agents/researcher.toml"},
		}},
	}
	projectLayer := Layer{
		Name: LayerSource{Type: LayerSourceProject, DotCodexFolder: filepath.Join(home, ".gcode")},
		Config: map[string]any{"agents": map[string]any{
			"researcher": map[string]any{"description": "Project researcher"},
		}},
	}
	roles, warnings, err := LoadAgentRoles([]Layer{userLayer, projectLayer})
	if err != nil {
		t.Fatalf("LoadAgentRoles() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("LoadAgentRoles() warnings = %#v, want none", warnings)
	}
	role, ok := roles["researcher"]
	if !ok {
		t.Fatalf("LoadAgentRoles() = %#v, want the researcher role", roles)
	}
	if role.Description != "Project researcher" {
		t.Fatalf("description = %q, want the higher layer's value", role.Description)
	}
	if role.ConfigFile != roleFile {
		t.Fatalf("config_file = %q, want %q from the lower layer", role.ConfigFile, roleFile)
	}
	if strings.Join(role.NicknameCandidates, ",") != "Scout" {
		t.Fatalf("nickname_candidates = %#v, want the lower layer's value", role.NicknameCandidates)
	}
	if role.Settings["developer_instructions"] != "Research carefully" || role.Settings["model"] != "gpt-5" {
		t.Fatalf("settings = %#v, want the role file's config layer", role.Settings)
	}
}

// TestLoadAgentRolesDropsDescriptionlessRoleWithWarning mirrors Rust's
// agent_role_without_description_after_merge_is_dropped_with_warning.
func TestLoadAgentRolesDropsDescriptionlessRoleWithWarning(t *testing.T) {
	home := t.TempDir()
	roleFile := filepath.Join(home, "agents", "researcher.toml")
	writeAgentRoleFile(t, roleFile, "developer_instructions = \"Research carefully\"\nmodel = \"gpt-5\"\n")

	layer := Layer{
		Name: LayerSource{Type: LayerSourceUser, File: filepath.Join(home, "config.toml")},
		Config: map[string]any{"agents": map[string]any{
			"researcher": map[string]any{"config_file": "./agents/researcher.toml"},
			"reviewer":   map[string]any{"description": "Review role"},
		}},
	}
	roles, warnings, err := LoadAgentRoles([]Layer{layer})
	if err != nil {
		t.Fatalf("LoadAgentRoles() error = %v", err)
	}
	if _, ok := roles["researcher"]; ok {
		t.Fatalf("LoadAgentRoles() = %#v, want researcher dropped", roles)
	}
	if role, ok := roles["reviewer"]; !ok || role.Description != "Review role" {
		t.Fatalf("LoadAgentRoles() = %#v, want the reviewer role", roles)
	}
	want := "Ignoring malformed agent role definition: agent role `researcher` must define a description"
	if len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("warnings = %#v, want [%q]", warnings, want)
	}
}

// TestLoadAgentRolesDiscoversRoleFilesLikeRust mirrors Rust's
// discovered_agent_role_file_without_name_is_dropped_with_warning: discovered
// files need their own name and developer_instructions, a declared role file is
// not rediscovered, and discovery is recursive and sorted.
func TestLoadAgentRolesDiscoversRoleFilesLikeRust(t *testing.T) {
	home := t.TempDir()
	configFolder := filepath.Join(home, ".gcode")
	writeAgentRoleFile(t, filepath.Join(configFolder, "agents", "researcher.toml"),
		"name = \"researcher\"\ndescription = \"Role metadata from file\"\nmodel = \"gpt-5.2\"\n")
	writeAgentRoleFile(t, filepath.Join(configFolder, "agents", "reviewer.toml"),
		"name = \"reviewer\"\ndescription = \"Review role\"\ndeveloper_instructions = \"Review carefully\"\nmodel = \"gpt-5.2\"\n")
	writeAgentRoleFile(t, filepath.Join(configFolder, "agents", "nested", "planner.toml"),
		"name = \"planner\"\ndescription = \"Plan work\"\ndeveloper_instructions = \"Plan ahead\"\n")
	declaredFile := filepath.Join(configFolder, "roles", "declared.toml")
	writeAgentRoleFile(t, declaredFile, "description = \"Declared role\"\ndeveloper_instructions = \"Do the declared work\"\n")

	layer := Layer{
		Name: LayerSource{Type: LayerSourceProject, DotCodexFolder: configFolder},
		Config: map[string]any{"agents": map[string]any{
			"declared": map[string]any{"config_file": "./roles/declared.toml"},
		}},
	}
	roles, warnings, err := LoadAgentRoles([]Layer{layer})
	if err != nil {
		t.Fatalf("LoadAgentRoles() error = %v", err)
	}
	if _, ok := roles["researcher"]; ok {
		t.Fatalf("roles = %#v, want the discovered researcher file dropped", roles)
	}
	if role, ok := roles["reviewer"]; !ok || role.Description != "Review role" || role.ConfigFile == "" {
		t.Fatalf("roles = %#v, want the discovered reviewer role", roles)
	}
	if role, ok := roles["planner"]; !ok || role.Description != "Plan work" {
		t.Fatalf("roles = %#v, want the nested planner role", roles)
	}
	if role, ok := roles["declared"]; !ok || role.Description != "Declared role" {
		t.Fatalf("roles = %#v, want the declared role", roles)
	}
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning, "must define `developer_instructions`") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %#v, want the missing-instructions notice", warnings)
	}
}

// TestLoadAgentRolesWarnsOnDuplicateNamesLikeRust mirrors Rust's per-layer
// duplicate detection: two declared roles that resolve to the same name (via a
// role file's `name`) keep the first and warn about the second.
func TestLoadAgentRolesWarnsOnDuplicateNamesLikeRust(t *testing.T) {
	home := t.TempDir()
	configFolder := filepath.Join(home, ".gcode")
	writeAgentRoleFile(t, filepath.Join(configFolder, "roles", "one.toml"),
		"name = \"shared\"\ndescription = \"First\"\ndeveloper_instructions = \"First\"\n")
	writeAgentRoleFile(t, filepath.Join(configFolder, "roles", "two.toml"),
		"name = \"shared\"\ndescription = \"Second\"\ndeveloper_instructions = \"Second\"\n")
	layer := Layer{
		Name: LayerSource{Type: LayerSourceProject, DotCodexFolder: configFolder},
		Config: map[string]any{"agents": map[string]any{
			"alpha": map[string]any{"config_file": "./roles/one.toml"},
			"beta":  map[string]any{"config_file": "./roles/two.toml"},
		}},
	}
	roles, warnings, err := LoadAgentRoles([]Layer{layer})
	if err != nil {
		t.Fatalf("LoadAgentRoles() error = %v", err)
	}
	if role, ok := roles["shared"]; !ok || role.Description != "First" {
		t.Fatalf("roles = %#v, want the first declaration to win", roles)
	}
	want := "Ignoring malformed agent role definition: duplicate agent role name `shared` declared in the same config layer"
	if len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("warnings = %#v, want [%q]", warnings, want)
	}
}

// TestAgentsConfigHonorsRoleFileNameLikeRust pins the no-layer path: the role
// file's `name` overrides the declared key and a duplicate name is rejected
// (Rust load_agent_roles_without_layers).
func TestAgentsConfigHonorsRoleFileNameLikeRust(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roles", "renamed.toml")
	writeAgentRoleFile(t, path, "name = \"researcher\"\ndescription = \"From file\"\ndeveloper_instructions = \"Research\"\n")
	cfg := &Config{Values: map[string]any{"agents": map[string]any{
		"declared": map[string]any{"config_file": filepath.Join("roles", "renamed.toml")},
	}}}
	got, err := cfg.AgentsConfig(dir)
	if err != nil {
		t.Fatalf("AgentsConfig() error = %v", err)
	}
	if _, ok := got.Roles["researcher"]; !ok {
		t.Fatalf("Roles = %#v, want the file-defined role name", got.Roles)
	}
	if _, ok := got.Roles["declared"]; ok {
		t.Fatalf("Roles = %#v, want the declared key replaced by the file name", got.Roles)
	}

	other := filepath.Join(dir, "roles", "other.toml")
	writeAgentRoleFile(t, other, "name = \"researcher\"\ndescription = \"Also from file\"\ndeveloper_instructions = \"Research\"\n")
	duplicate := &Config{Values: map[string]any{"agents": map[string]any{
		"first":  map[string]any{"config_file": filepath.Join("roles", "renamed.toml")},
		"second": map[string]any{"config_file": filepath.Join("roles", "other.toml")},
	}}}
	if _, err := duplicate.AgentsConfig(dir); err == nil || !strings.Contains(err.Error(), "duplicate agent role name `researcher` declared in config") {
		t.Fatalf("AgentsConfig() error = %v, want the duplicate-name rejection", err)
	}
}

// TestAgentRolesForCWDUsesVisibleLayers pins the service wiring: a trusted
// project's `.gcode/agents` role files and declared roles are visible from the
// project cwd.
func TestAgentRolesForCWDUsesVisibleLayers(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repo, "packages", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(home), []byte("[projects.\""+strings.ReplaceAll(repo, `\`, `\\`)+"\"]\ntrust_level = \"trusted\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dotCodex := filepath.Join(repo, ".gcode")
	writeAgentRoleFile(t, filepath.Join(dotCodex, "agents", "reviewer.toml"),
		"name = \"reviewer\"\ndescription = \"Review role\"\ndeveloper_instructions = \"Review carefully\"\n")
	if err := os.WriteFile(filepath.Join(dotCodex, "config.toml"), []byte("[agents.planner]\ndescription = \"Plan work\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	service := NewConfigService(home)
	roles, warnings := service.AgentRolesForCWD(nested)
	if len(warnings) != 0 {
		t.Fatalf("AgentRolesForCWD() warnings = %#v, want none", warnings)
	}
	if _, ok := roles["reviewer"]; !ok {
		t.Fatalf("roles = %#v, want the discovered reviewer role", roles)
	}
	if role, ok := roles["planner"]; !ok || role.Description != "Plan work" {
		t.Fatalf("roles = %#v, want the declared planner role", roles)
	}
}
