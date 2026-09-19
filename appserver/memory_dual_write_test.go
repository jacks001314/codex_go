package appserver

import (
	"context"
	"testing"

	"codex_go/config"
	"codex_go/memories"
	"codex_go/state"
)

// Mirrors Rust #43827: memories.dual_write runs v2 alongside the selected
// version with an isolated store, and never duplicates an already-selected v2.
func TestMemoryStartupPipelinesHonorDualWrite(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatalf("NewSqliteConfig() error = %v", err)
	}
	runtime, err := state.InitStateRuntime(ctx, sqliteConfig, "provider")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v", err)
	}
	defer func() { _ = runtime.Close() }()

	metrics := state.NewTaskMetrics()
	primary := &memories.StartupPipeline{
		State: runtime, CodexHome: home, Version: config.MemoryVersionV1, Metrics: metrics,
	}

	off := memoryStartupPipelines(primary, config.MemoriesConfig{}, config.MemoryVersionV1, runtime, ctx)
	if len(off) != 1 || off[0] != primary {
		t.Fatalf("dual-write off pipelines = %#v", off)
	}
	if runtime.HasMemoriesV2Database() {
		t.Fatal("dual-write off must not create the v2 store")
	}

	dual := memoryStartupPipelines(primary, config.MemoriesConfig{DualWrite: true}, config.MemoryVersionV1, runtime, ctx)
	if len(dual) != 2 {
		t.Fatalf("dual-write pipelines = %d, want 2", len(dual))
	}
	if dual[1].Version != config.MemoryVersionV2 {
		t.Fatalf("dual pipeline version = %q, want v2", dual[1].Version)
	}
	if dual[1].CodexHome != home || dual[1].State == nil || dual[1].State.MemoriesDB() == runtime.MemoriesDB() {
		t.Fatal("dual pipeline must use the isolated v2 store and the same codex home")
	}
	if dual[1].State.StateDB() != runtime.StateDB() {
		t.Fatal("dual pipeline must share the thread catalog")
	}
	// The dual pipeline reports its own root version on the same sink (#45956).
	if dual[1].Metrics != memories.MemoryMetricSink(metrics) {
		t.Fatalf("dual pipeline metrics = %#v", dual[1].Metrics)
	}
	if !runtime.HasMemoriesV2Database() {
		t.Fatal("dual-write must create the v2 store")
	}

	selectedV2 := &memories.StartupPipeline{State: runtime, CodexHome: home, Version: config.MemoryVersionV2}
	alreadyV2 := memoryStartupPipelines(selectedV2, config.MemoriesConfig{DualWrite: true}, config.MemoryVersionV2, runtime, ctx)
	if len(alreadyV2) != 1 || alreadyV2[0] != selectedV2 {
		t.Fatalf("v2-selected dual-write pipelines = %#v", alreadyV2)
	}
}
