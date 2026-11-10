package config

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Rust #60492: the `claude-code-local-skills` source imports only locally
// authored skills, while the default Claude import also takes marketplace-synced
// skills; the home trash directory and the synced trash directory never migrate.
func TestExternalClaudeSkillImportHonorsLocalOnlyModeLikeRust(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".gcode")
	externalHome := filepath.Join(root, ".claude")
	skillsDir := filepath.Join(externalHome, "skills")
	writeSkill := func(relative string) {
		t.Helper()
		dir := filepath.Join(skillsDir, relative)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("local\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill("alpha")
	writeSkill(".trash/removed")
	writeSkill("synced/beta")
	writeSkill("synced/.trash/gamma")

	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(externalHome)

	detectedNames := func(source string) []string {
		t.Helper()
		params := &ExternalAgentConfigDetectParams{IncludeHome: true}
		if source != "" {
			params.MigrationSource = &source
		}
		detected := service.DetectExternalAgentConfig(params)
		for _, item := range detected.Items {
			if item.ItemType != MigrationSkills || item.Details == nil {
				continue
			}
			names := make([]string, 0, len(item.Details.Skills))
			for _, skill := range item.Details.Skills {
				names = append(names, skill.Name)
			}
			sort.Strings(names)
			return names
		}
		return nil
	}

	if got := detectedNames("claude-code-local-skills"); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("local-only detected skills = %v, want [alpha]", got)
	}
	if got := detectedNames(""); len(got) != 2 || got[0] != "alpha" || got[1] != "synced" {
		t.Fatalf("default detected skills = %v, want [alpha synced]", got)
	}

	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true})
	_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: detected.Items})
	if len(completed.ItemTypeResults) != 1 || len(completed.ItemTypeResults[0].Successes) != 2 {
		t.Fatalf("import results = %#v", completed.ItemTypeResults)
	}
	target := filepath.Join(root, ".agents", "skills")
	if _, err := os.Stat(filepath.Join(target, "alpha", "SKILL.md")); err != nil {
		t.Fatalf("alpha skill not imported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "synced", "beta", "SKILL.md")); err != nil {
		t.Fatalf("synced skill not imported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "synced", ".trash")); !os.IsNotExist(err) {
		t.Fatalf("nested synced trash was imported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".trash")); !os.IsNotExist(err) {
		t.Fatalf("home trash was imported: %v", err)
	}
}
