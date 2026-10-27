package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/memorylock"
	"codex_go/session"
	"codex_go/state"
)

func TestExternalAgentMemoryImportExcludesBusyConsolidationLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[memories]\nversion = \"v1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, sqliteConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateRuntime.Close() })
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(filepath.Join(home, "sessions")), home, &RuntimeRouterOptions{StateRuntime: stateRuntime})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	externalHome := filepath.Join(t.TempDir(), ".claude")
	cwd := filepath.Join(t.TempDir(), "repo")
	projectKey := config.ExternalMemoryProjectKey(cwd)
	sourceDir := filepath.Join(externalHome, "projects", projectKey, "memory")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "MEMORY.md"), []byte("concurrent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router.services.Config.SetExternalAgentHome(externalHome)

	detectResponse := router.Handle(requestWithParams(t, IntID(1), MethodExternalAgentConfigDetect, config.ExternalAgentConfigDetectParams{
		IncludeHome: true, CWDs: []string{cwd},
	}))
	if detectResponse.Error != nil {
		t.Fatalf("detect response = %+v", detectResponse)
	}
	detected := detectResponse.Result.(*config.ExternalAgentConfigDetectResponse)
	var memoryItem *config.ExternalAgentConfigMigrationItem
	for i := range detected.Items {
		if detected.Items[i].ItemType == config.MigrationMemory {
			memoryItem = &detected.Items[i]
		}
	}
	if memoryItem == nil {
		t.Fatalf("detected items = %#v", detected.Items)
	}

	workspaceLock, err := memorylock.TryAcquire(filepath.Join(home, "memories"))
	if err != nil {
		t.Fatal(err)
	}
	importResponse := router.Handle(requestWithParams(t, IntID(2), MethodExternalAgentConfigImport, config.ExternalAgentConfigImportParams{
		MigrationItems: []config.ExternalAgentConfigMigrationItem{*memoryItem},
	}))
	if importResponse.Error != nil {
		t.Fatalf("busy import response = %+v", importResponse)
	}
	busy := waitForExternalAgentImportNotification(t, sink, importResponse.Result.(*config.ExternalAgentConfigImportResponse).ImportID)
	busyMemory := externalAgentMemoryResult(t, busy)
	if len(busyMemory.Successes) != 0 || len(busyMemory.Failures) != 1 || busyMemory.Failures[0].FailureStage != "import_request_failed" {
		t.Fatalf("busy memory result = %#v", busyMemory)
	}
	if countExternalConsolidationJobs(t, ctx, stateRuntime, config.MemoryVersionV1) != 0 ||
		countExternalConsolidationJobs(t, ctx, stateRuntime, config.MemoryVersionV2) != 0 {
		t.Fatal("busy import enqueued a consolidation")
	}

	if err := workspaceLock.Close(); err != nil {
		t.Fatal(err)
	}
	retryResponse := router.Handle(requestWithParams(t, IntID(3), MethodExternalAgentConfigImport, config.ExternalAgentConfigImportParams{
		MigrationItems: []config.ExternalAgentConfigMigrationItem{*memoryItem},
	}))
	if retryResponse.Error != nil {
		t.Fatalf("retry import response = %+v", retryResponse)
	}
	retried := waitForExternalAgentImportNotification(t, sink, retryResponse.Result.(*config.ExternalAgentConfigImportResponse).ImportID)
	retryMemory := externalAgentMemoryResult(t, retried)
	if len(retryMemory.Successes) != 1 || len(retryMemory.Failures) != 0 {
		t.Fatalf("retry memory result = %#v", retryMemory)
	}
	if countExternalConsolidationJobs(t, ctx, stateRuntime, config.MemoryVersionV1) != 1 ||
		countExternalConsolidationJobs(t, ctx, stateRuntime, config.MemoryVersionV2) != 1 {
		t.Fatal("retry did not enqueue one consolidation per namespace")
	}
}

func externalAgentMemoryResult(t *testing.T, completed *config.ExternalAgentConfigImportCompletedNotification) config.ExternalAgentConfigImportTypeResult {
	t.Helper()
	for _, result := range completed.ItemTypeResults {
		if result.ItemType == config.MigrationMemory {
			return result
		}
	}
	t.Fatalf("missing memory result in %#v", completed.ItemTypeResults)
	return config.ExternalAgentConfigImportTypeResult{}
}

func countExternalConsolidationJobs(t *testing.T, ctx context.Context, runtime *state.StateRuntime, version config.MemoryVersion) int {
	t.Helper()
	store, err := runtime.MemoryStoreForVersion(ctx, string(version))
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.MemoriesDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind = ?`, state.MemoryJobKindConsolidateGlobal).Scan(&count); err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return 0
		}
		t.Fatal(err)
	}
	return count
}
