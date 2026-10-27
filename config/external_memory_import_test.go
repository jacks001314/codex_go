package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"codex_go/memorylock"
)

type memoryConsolidationRecorder struct {
	mu       sync.Mutex
	versions []MemoryVersion
	err      error
}

func (r *memoryConsolidationRecorder) enqueue(_ context.Context, version MemoryVersion) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.versions = append(r.versions, version)
	return r.err
}

func (r *memoryConsolidationRecorder) recorded() []MemoryVersion {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]MemoryVersion(nil), r.versions...)
}

func writeExternalMemoryProject(t *testing.T, externalHome, cwd string, files map[string]string) string {
	t.Helper()
	projectKey := externalMemoryProjectKey(cwd)
	memoryRoot := filepath.Join(externalHome, "projects", projectKey, "memory")
	for relative, content := range files {
		path := filepath.Join(memoryRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return projectKey
}

func detectExternalMemoryItem(t *testing.T, service *ConfigService, cwds []string) *ExternalAgentConfigMigrationItem {
	t.Helper()
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true, CWDs: cwds})
	for i := range detected.Items {
		if detected.Items[i].ItemType == MigrationMemory {
			return &detected.Items[i]
		}
	}
	return nil
}

func completeMemoryResult(t *testing.T, service *ConfigService, item *ExternalAgentConfigMigrationItem) ExternalAgentConfigImportTypeResult {
	t.Helper()
	_, completed := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{MigrationItems: []ExternalAgentConfigMigrationItem{*item}})
	for _, result := range completed.ItemTypeResults {
		if result.ItemType == MigrationMemory {
			return result
		}
	}
	t.Fatalf("missing memory result in %#v", completed.ItemTypeResults)
	return ExternalAgentConfigImportTypeResult{}
}

func TestExternalAgentMemoryDetectAndImportMatchesRustNamespaces(t *testing.T) {
	root := t.TempDir()
	externalHome := filepath.Join(root, ".claude")
	codexHome := filepath.Join(root, ".gcode")
	cwd := filepath.Join(root, "repo")
	projectKey := writeExternalMemoryProject(t, externalHome, cwd, map[string]string{
		"MEMORY.md":          "project scoped fact\n",
		"notes/debugging.md": "nested detail\n",
	})
	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(externalHome)
	recorder := &memoryConsolidationRecorder{}
	service.SetMemoryImportSettings(MemoryImportSettings{Version: MemoryVersionV1, EnqueueConsolidation: recorder.enqueue})

	item := detectExternalMemoryItem(t, service, []string{cwd})
	if item == nil || item.Details == nil || len(item.Details.MemoryFiles) != 2 {
		t.Fatalf("detected memory item = %#v", item)
	}
	v2Resources := filepath.Join(codexHome, "memories_v2", "extensions", "external_agent_import", "resources")
	if !strings.Contains(item.Description, v2Resources) {
		t.Fatalf("description = %q, want v2 target %q", item.Description, v2Resources)
	}

	result := completeMemoryResult(t, service, item)
	if len(result.Failures) != 0 || len(result.Successes) != 1 {
		t.Fatalf("memory result = %#v", result)
	}
	success := result.Successes[0]
	if success.CWD == nil || *success.CWD != projectKey || success.Source != nil || success.Target == nil || *success.Target != v2Resources {
		t.Fatalf("success = %#v", success)
	}
	for _, memoryRoot := range []string{filepath.Join(codexHome, "memories"), filepath.Join(codexHome, "memories_v2")} {
		memoryPath := filepath.Join(memoryRoot, "extensions", "external_agent_import", "resources", projectKey, "MEMORY.md")
		content, err := os.ReadFile(memoryPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "project scoped fact\n" {
			t.Fatalf("imported content in %s = %q", memoryPath, content)
		}
		scope, err := os.ReadFile(filepath.Join(filepath.Dir(memoryPath), "scope.json"))
		if err != nil {
			t.Fatal(err)
		}
		expectedScope, _ := json.Marshal(map[string]string{"cwd": cwd})
		if string(scope) != string(expectedScope) {
			t.Fatalf("scope in %s = %q, want %q", memoryRoot, scope, expectedScope)
		}
		instructions, err := os.ReadFile(filepath.Join(memoryRoot, "extensions", "external_agent_import", "instructions.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(instructions) != externalMemoryInstructions {
			t.Fatalf("instructions in %s do not match the embedded migration instructions", memoryRoot)
		}
	}
	if got := recorder.recorded(); len(got) != 2 || got[0] != MemoryVersionV1 || got[1] != MemoryVersionV2 {
		t.Fatalf("queued consolidations = %v", got)
	}
	if again := detectExternalMemoryItem(t, service, []string{cwd}); again != nil {
		t.Fatalf("detection after import = %#v, want pending-free", again)
	}
}

func TestExternalAgentMemoryImportFreshV2ProfileSkipsV1(t *testing.T) {
	root := t.TempDir()
	externalHome := filepath.Join(root, ".claude")
	codexHome := filepath.Join(root, ".gcode")
	cwd := filepath.Join(root, "repo")
	writeExternalMemoryProject(t, externalHome, cwd, map[string]string{"MEMORY.md": "v2 only\n"})
	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(externalHome)
	recorder := &memoryConsolidationRecorder{}
	service.SetMemoryImportSettings(MemoryImportSettings{Version: MemoryVersionV2, EnqueueConsolidation: recorder.enqueue})

	item := detectExternalMemoryItem(t, service, []string{cwd})
	if item == nil {
		t.Fatal("missing detected memory item")
	}
	result := completeMemoryResult(t, service, item)
	if len(result.Failures) != 0 || len(result.Successes) != 1 {
		t.Fatalf("memory result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "memories")); !os.IsNotExist(err) {
		t.Fatalf("v1 workspace exists after a fresh v2 import: %v", err)
	}
	if got := recorder.recorded(); len(got) != 1 || got[0] != MemoryVersionV2 {
		t.Fatalf("queued consolidations = %v", got)
	}
}

func TestExternalAgentMemoryImportBusyWorkspaceIsRetryable(t *testing.T) {
	root := t.TempDir()
	externalHome := filepath.Join(root, ".claude")
	codexHome := filepath.Join(root, ".gcode")
	cwd := filepath.Join(root, "repo")
	writeExternalMemoryProject(t, externalHome, cwd, map[string]string{"MEMORY.md": "busy\n"})
	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(externalHome)
	recorder := &memoryConsolidationRecorder{}
	service.SetMemoryImportSettings(MemoryImportSettings{Version: MemoryVersionV2, EnqueueConsolidation: recorder.enqueue})
	item := detectExternalMemoryItem(t, service, []string{cwd})
	if item == nil {
		t.Fatal("missing detected memory item")
	}

	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	workspaceLock, err := memorylock.TryAcquire(filepath.Join(codexHome, "memories_v2"))
	if err != nil {
		t.Fatal(err)
	}
	result := completeMemoryResult(t, service, item)
	if len(result.Successes) != 0 || len(result.Failures) != 1 {
		t.Fatalf("busy result = %#v", result)
	}
	failure := result.Failures[0]
	if failure.FailureStage != "import_request_failed" || failure.ErrorType == nil || *failure.ErrorType != "external_agent_config_import_error" {
		t.Fatalf("busy failure = %#v", failure)
	}
	if !strings.Contains(failure.Message, "busy") {
		t.Fatalf("busy message = %q", failure.Message)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "memories_v2", "extensions")); !os.IsNotExist(err) {
		t.Fatalf("busy import touched the workspace: %v", err)
	}
	if got := recorder.recorded(); len(got) != 0 {
		t.Fatalf("busy import queued consolidations = %v", got)
	}

	if err := workspaceLock.Close(); err != nil {
		t.Fatal(err)
	}
	retry := completeMemoryResult(t, service, item)
	if len(retry.Failures) != 0 || len(retry.Successes) != 1 {
		t.Fatalf("retry result = %#v", retry)
	}
	if got := recorder.recorded(); len(got) != 1 || got[0] != MemoryVersionV2 {
		t.Fatalf("retry queued consolidations = %v", got)
	}
}

func TestExternalAgentMemoryDetectionAndImportRemovesDeletedProjects(t *testing.T) {
	root := t.TempDir()
	externalHome := filepath.Join(root, ".claude")
	codexHome := filepath.Join(root, ".gcode")
	cwdA := filepath.Join(root, "repo-a")
	cwdB := filepath.Join(root, "repo-b")
	keyA := writeExternalMemoryProject(t, externalHome, cwdA, map[string]string{"MEMORY.md": "a\n"})
	keyB := writeExternalMemoryProject(t, externalHome, cwdB, map[string]string{"MEMORY.md": "b\n"})
	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(externalHome)
	service.SetMemoryImportSettings(MemoryImportSettings{Version: MemoryVersionV2})
	item := detectExternalMemoryItem(t, service, []string{cwdA, cwdB})
	if item == nil {
		t.Fatal("missing detected memory item")
	}
	if result := completeMemoryResult(t, service, item); len(result.Failures) != 0 || len(result.Successes) != 2 {
		t.Fatalf("initial import = %#v", result)
	}
	if err := os.RemoveAll(filepath.Join(externalHome, "projects", keyB)); err != nil {
		t.Fatal(err)
	}
	item = detectExternalMemoryItem(t, service, []string{cwdA, cwdB})
	if item == nil {
		t.Fatal("deleted project was not detected for cleanup")
	}
	result := completeMemoryResult(t, service, item)
	if len(result.Failures) != 0 || len(result.Successes) != 1 || result.Successes[0].CWD == nil || *result.Successes[0].CWD != keyB {
		t.Fatalf("cleanup import = %#v", result)
	}
	resources := filepath.Join(codexHome, "memories_v2", "extensions", "external_agent_import", "resources")
	if _, err := os.Stat(filepath.Join(resources, keyB)); !os.IsNotExist(err) {
		t.Fatalf("deleted project remains imported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(resources, keyA, "MEMORY.md")); err != nil {
		t.Fatalf("existing project was removed: %v", err)
	}
}
