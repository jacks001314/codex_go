package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalAgentMemoryDetectAndImportPreservesScopeAndProvenance(t *testing.T) {
	root := t.TempDir()
	externalHome := filepath.Join(root, ".claude")
	codexHome := filepath.Join(root, ".codex")
	cwd := filepath.Join(root, "repo")
	projectKey := externalMemoryProjectKey(cwd)
	memoryRoot := filepath.Join(externalHome, "projects", projectKey, "memory")
	if err := os.MkdirAll(memoryRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(memoryRoot, "MEMORY.md")
	if err := os.WriteFile(source, []byte("project scoped fact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(externalHome)
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true, CWDs: []string{cwd}})
	var memoryItem *ExternalAgentConfigMigrationItem
	for i := range detected.Items {
		if detected.Items[i].ItemType == MigrationMemory {
			memoryItem = &detected.Items[i]
		}
	}
	if memoryItem == nil || memoryItem.Details == nil || len(memoryItem.Details.MemoryFiles) != 1 {
		t.Fatalf("detected memory = %#v", detected.Items)
	}
	file := memoryItem.Details.MemoryFiles[0]
	if file.CWD == nil || *file.CWD != cwd || file.ProjectKey != projectKey || file.SourceFile != "MEMORY.md" || len(file.ContentSHA256) != 64 {
		t.Fatalf("memory file = %#v", file)
	}
	_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: []ExternalAgentConfigMigrationItem{*memoryItem}})
	if len(completed.ItemTypeResults) != 1 || len(completed.ItemTypeResults[0].Successes) != 1 || len(completed.ItemTypeResults[0].Failures) != 0 {
		t.Fatalf("completed = %#v", completed)
	}
	target := completed.ItemTypeResults[0].Successes[0].Target
	if target == nil {
		t.Fatal("missing target")
	}
	content, err := os.ReadFile(*target)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, `"source":"external_agent_import"`) || !strings.Contains(text, `"projectKey":"`+projectKey+`"`) || strings.Contains(text, "thread_id") || strings.Contains(text, "rollout_path") {
		t.Fatalf("imported provenance = %q", text)
	}
	scopeData, err := os.ReadFile(filepath.Join(filepath.Dir(*target), "scope.json"))
	if err != nil {
		t.Fatal(err)
	}
	var scope map[string]any
	if err := json.Unmarshal(scopeData, &scope); err != nil {
		t.Fatal(err)
	}
	if scope["cwd"] != cwd || scope["source"] != externalMemorySource {
		t.Fatalf("scope = %#v", scope)
	}
}
