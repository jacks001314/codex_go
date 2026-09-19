package memories

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/state"
)

type recordingStageOneExtractor struct {
	mu       sync.Mutex
	requests []StageOneExtractionRequest
	response StageOneExtractionResponse
	err      error
}

func (e *recordingStageOneExtractor) ExtractMemory(_ context.Context, request StageOneExtractionRequest) (StageOneExtractionResponse, error) {
	e.mu.Lock()
	e.requests = append(e.requests, request)
	e.mu.Unlock()
	return e.response, e.err
}

type artifactConsolidator struct {
	mu       sync.Mutex
	requests []ConsolidationRequest
	err      error
	usage    *model.AgentUsage
}

func (c *artifactConsolidator) ConsolidateMemory(_ context.Context, request ConsolidationRequest) (*model.AgentUsage, error) {
	c.mu.Lock()
	c.requests = append(c.requests, request)
	c.mu.Unlock()
	if c.err != nil {
		return c.usage, c.err
	}
	if err := os.WriteFile(filepath.Join(request.Root, MemoryFilename), []byte("# Memory\n"), 0o600); err != nil {
		return c.usage, err
	}
	return c.usage, os.WriteFile(filepath.Join(request.Root, MemorySummaryFilename), []byte("v1\n# Summary\n"), 0o600)
}

func TestStartupPipelineRunsStageOneAndPhaseTwoEndToEnd(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime := newMemoryPipelineRuntime(t, home)
	updated := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	rolloutPath := writeMemoryPipelineRollout(t, home, "memory-source-thread", updated)
	if err := runtime.ReconcileRollout(ctx, rolloutPath, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `
UPDATE threads SET memory_mode = 'enabled', preview = 'remember this', updated_at = ?, updated_at_ms = ?
WHERE id = 'memory-source-thread'`, updated.Unix(), updated.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	slug := "memory pipeline"
	extractor := &recordingStageOneExtractor{response: StageOneExtractionResponse{
		RawMemory: "raw memory from rollout", RolloutSummary: "pipeline summary", RolloutSlug: &slug,
		Usage: &model.AgentUsage{
			InputTokens: 900, CachedInputTokens: 300, CacheWriteInputTokens: 20,
			OutputTokens: 40, ReasoningOutputTokens: 10, TotalTokens: 940,
		},
	}}
	consolidator := &artifactConsolidator{usage: &model.AgentUsage{
		InputTokens: 2000, CachedInputTokens: 800, CacheWriteInputTokens: 60,
		OutputTokens: 120, ReasoningOutputTokens: 30, TotalTokens: 2120,
	}}
	metrics := state.NewTaskMetrics()
	pipeline := &StartupPipeline{
		State: runtime, CodexHome: home, CurrentThreadID: "current-thread",
		Config: config.MemoriesConfig{
			GenerateMemories: true, UseMemories: true,
			MaxRawMemoriesForConsolidation: 256, MaxUnusedDays: 30,
			MaxRolloutAgeDays: 10, MaxRolloutsPerStartup: 2,
			MinRolloutIdleHours: 1, MinRateLimitRemainingPercent: 25,
		},
		StageOne: extractor, StageOneModel: "extract-model",
		StageOneModelInfo: model.ModelInfo{ContextWindow: 10_000, EffectiveContextWindowPercent: 95},
		PhaseTwo:          consolidator, PhaseTwoModel: "consolidate-model",
		Metrics: metrics,
	}
	report, err := pipeline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.StageOneClaimed != 1 || report.StageOneSucceeded != 1 || report.StageOneFailed != 0 || report.PhaseTwoStatus != "succeeded" {
		t.Fatalf("startup report = %+v", report)
	}
	if len(extractor.requests) != 1 {
		t.Fatalf("stage-one requests = %d", len(extractor.requests))
	}
	stageOne := extractor.requests[0]
	if stageOne.Model != "extract-model" || stageOne.Instructions != StageOneSystemPrompt() || !strings.Contains(stageOne.Input, "remember this") || strings.Contains(stageOne.Input, "developer secret") || strings.Contains(stageOne.Input, "AGENTS.md instructions") || strings.Contains(stageOne.Input, "<skill>") {
		t.Fatalf("stage-one request was not filtered: %+v", stageOne)
	}
	if len(consolidator.requests) != 1 || consolidator.requests[0].Model != "consolidate-model" || consolidator.requests[0].ReasoningEffort != "medium" || !strings.Contains(consolidator.requests[0].Prompt, WorkspaceDiffFilename) {
		t.Fatalf("consolidation requests = %+v", consolidator.requests)
	}

	var rawMemory, summary, rolloutSlug string
	var selected bool
	if err := runtime.MemoriesDB().QueryRowContext(ctx, `SELECT raw_memory, rollout_summary, rollout_slug, selected_for_phase2 FROM stage1_outputs WHERE thread_id = 'memory-source-thread'`).Scan(&rawMemory, &summary, &rolloutSlug, &selected); err != nil {
		t.Fatal(err)
	}
	if rawMemory != "raw memory from rollout" || summary != "pipeline summary" || rolloutSlug != slug || !selected {
		t.Fatalf("stage-one output = %q/%q/%q selected=%v", rawMemory, summary, rolloutSlug, selected)
	}
	var globalStatus string
	if err := runtime.MemoriesDB().QueryRowContext(ctx, `SELECT status FROM jobs WHERE kind = ? AND job_key = ?`, state.MemoryJobKindConsolidateGlobal, state.MemoryConsolidationJobKey).Scan(&globalStatus); err != nil || globalStatus != "done" {
		t.Fatalf("global job = %q, %v", globalStatus, err)
	}
	root := Root(home)
	for _, path := range []string{
		filepath.Join(root, MemoryFilename), filepath.Join(root, MemorySummaryFilename),
		RawMemoriesFile(root), filepath.Join(ExtensionsRoot(root), "ad_hoc", "instructions.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("memory artifact %s missing: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, WorkspaceDiffFilename)); !os.IsNotExist(err) {
		t.Fatalf("workspace diff survived successful baseline reset: %v", err)
	}
	diff, err := WorkspaceDiff(ctx, root)
	if err != nil || diff.HasChanges() {
		t.Fatalf("workspace after consolidation = %+v, %v", diff, err)
	}
	// Mirrors Rust #45956: the successful run reports the memory root's byte
	// size, excluding the git baseline the reset left behind.
	storage := storageBytesRecords(metrics)
	if len(storage) != 1 {
		t.Fatalf("storage byte records = %#v", metrics.Records())
	}
	wantBytes, err := MemoryStorageBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if storage[0].Value != int(wantBytes) || storage[0].Tags[MemoryVersionTag] != "v1" {
		t.Fatalf("storage record = %#v, want %d bytes at v1", storage[0], wantBytes)
	}
	if storage[0].Tags[MemoryVersionTag] != MemoryVersionTagValue(config.MemoryVersionV1) {
		t.Fatalf("storage version tag = %q", storage[0].Tags[MemoryVersionTag])
	}
	if len(storage[0].Boundaries) != len(MemoryStorageBytesBoundaries()) {
		t.Fatalf("storage boundaries = %#v", storage[0].Boundaries)
	}
	// The phase counters mirror Rust's phase1/phase2 emit_metrics for a run that
	// claimed one job, extracted output, spawned the agent and succeeded.
	assertMemoryStatusCounts(t, metrics, MemoryPhaseOneJobsMetric, map[string]int{"claimed": 1, "succeeded": 1})
	assertMemoryTimerRecorded(t, metrics, MemoryPhaseOneE2EMetric, config.MemoryVersionV1)
	if output := memoryMetricRecords(metrics, MemoryPhaseOneOutputMetric); len(output) != 1 || output[0].Inc != 1 {
		t.Fatalf("output records = %#v", output)
	}
	assertMemoryStatusCounts(t, metrics, MemoryPhaseTwoJobsMetric, map[string]int{
		"claimed": 1, "agent_spawned": 1, "succeeded": 1,
	})
	assertMemoryTimerRecorded(t, metrics, MemoryPhaseTwoE2EMetric, config.MemoryVersionV1)
	if input := memoryMetricRecords(metrics, MemoryPhaseTwoInputMetric); len(input) != 1 || input[0].Inc != 1 {
		t.Fatalf("input records = %#v", input)
	}
	// Rust reports the tokens the phase consumed as one sample per token type,
	// in its own order.
	tokenUsage := memoryMetricRecords(metrics, MemoryPhaseOneTokenUsageMetric)
	wantTokenTypes := []string{"total", "input", "cached_input", "cache_write_input", "output", "reasoning_output"}
	wantTokenValues := []int{940, 900, 300, 20, 40, 10}
	if len(tokenUsage) != len(wantTokenTypes) {
		t.Fatalf("token usage records = %#v", tokenUsage)
	}
	for index, tokenType := range wantTokenTypes {
		record := tokenUsage[index]
		if record.Tags[MemoryTokenTypeTag] != tokenType || record.Value != wantTokenValues[index] {
			t.Fatalf("token usage record %d = %#v", index, record)
		}
		if record.Tags[MemoryVersionTag] != "v1" || record.Kind != "histogram" {
			t.Fatalf("token usage record %d tags = %#v", index, record.Tags)
		}
	}
	// The consolidation agent's accumulated usage is reported the same way (Rust
	// reads the thread's total usage once the agent completed).
	phaseTwoUsage := memoryMetricRecords(metrics, MemoryPhaseTwoTokenUsageMetric)
	wantPhaseTwoValues := []int{2120, 2000, 800, 60, 120, 30}
	if len(phaseTwoUsage) != len(wantTokenTypes) {
		t.Fatalf("phase-two token usage records = %#v", phaseTwoUsage)
	}
	for index, tokenType := range wantTokenTypes {
		record := phaseTwoUsage[index]
		if record.Tags[MemoryTokenTypeTag] != tokenType || record.Value != wantPhaseTwoValues[index] {
			t.Fatalf("phase-two token usage record %d = %#v", index, record)
		}
	}
}

// storageBytesRecords selects the codex.memory.storage_bytes records.
func storageBytesRecords(metrics *state.TaskMetrics) []*state.TaskMetric {
	var out []*state.TaskMetric
	for _, record := range metrics.Records() {
		if record.Name == MemoryStorageBytesMetric {
			out = append(out, record)
		}
	}
	return out
}

// Mirrors Rust #45956: a phase-two run that leaves the workspace unchanged is
// still a successful consolidation, and it reports the storage size too.
func TestStartupPipelineReportsStorageBytesWithoutWorkspaceChanges(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime := newMemoryPipelineRuntime(t, home)
	root := Root(home)
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	// Seed exactly what the run would produce, so the sync steps rewrite
	// identical bytes and the workspace diff stays empty.
	if err := os.WriteFile(filepath.Join(root, MemoryFilename), []byte("registry\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, MemorySummaryFilename), []byte("v1\nsummary\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := RebuildRawMemoriesFile(root, nil, 0); err != nil {
		t.Fatal(err)
	}

	metrics := state.NewTaskMetrics()
	pipeline := &StartupPipeline{
		State: runtime, CodexHome: home, CurrentThreadID: "current-thread",
		Config:   config.MemoriesConfig{GenerateMemories: true, UseMemories: true, MaxUnusedDays: 30},
		PhaseTwo: &artifactConsolidator{}, PhaseTwoModel: "consolidate-model",
		Metrics: metrics,
	}
	report, err := pipeline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.PhaseTwoStatus != "succeeded_no_workspace_changes" {
		t.Fatalf("phase two status = %q", report.PhaseTwoStatus)
	}
	storage := storageBytesRecords(metrics)
	if len(storage) != 1 {
		t.Fatalf("storage byte records = %#v", metrics.Records())
	}
	wantBytes, err := MemoryStorageBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if storage[0].Value != int(wantBytes) || storage[0].Tags[MemoryVersionTag] != "v1" {
		t.Fatalf("storage record = %#v, want %d bytes at v1", storage[0], wantBytes)
	}
	// A run that changed nothing returns before the agent is dispatched, so it
	// reports neither the spawn counter nor the input count (Rust's early return).
	assertMemoryStatusCounts(t, metrics, MemoryPhaseTwoJobsMetric, map[string]int{
		"claimed": 1, "succeeded_no_workspace_changes": 1,
	})
	for _, name := range []string{MemoryPhaseTwoInputMetric, MemoryPhaseOneJobsMetric, MemoryPhaseTwoTokenUsageMetric} {
		if records := memoryMetricRecords(metrics, name); len(records) != 0 {
			t.Fatalf("%s records = %#v", name, records)
		}
	}
}

func TestSerializeFilteredRolloutForMemoryMatchesRustPolicy(t *testing.T) {
	home := t.TempDir()
	path := writeMemoryPipelineRollout(t, home, "filter-thread", time.Now().UTC())
	serialized, err := SerializeFilteredRolloutForMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"developer secret", "AGENTS.md instructions", "<skill>", "reasoning secret"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("serialized rollout retained %q: %s", forbidden, serialized)
		}
	}
	for _, retained := range []string{"remember this", "environment_context", "assistant response", "function_call", "tool-name"} {
		if !strings.Contains(serialized, retained) {
			t.Fatalf("serialized rollout missing %q: %s", retained, serialized)
		}
	}
}

func TestDecodeStageOneOutputIsStrictAndNullable(t *testing.T) {
	decoded, err := DecodeStageOneOutput(`{"raw_memory":"raw","rollout_summary":"summary","rollout_slug":null}`)
	if err != nil || decoded.RawMemory != "raw" || decoded.RolloutSummary != "summary" || decoded.RolloutSlug != nil {
		t.Fatalf("decoded output = %+v, %v", decoded, err)
	}
	if _, err := DecodeStageOneOutput(`{"raw_memory":"raw","rollout_summary":"summary","rollout_slug":null,"extra":true}`); err == nil {
		t.Fatal("unknown stage-one output field was accepted")
	}
	if _, err := DecodeStageOneOutput(`{"raw_memory":"raw","rollout_summary":"summary","rollout_slug":null} trailing`); err == nil {
		t.Fatal("trailing non-JSON stage-one output was accepted")
	}
	if _, err := DecodeStageOneOutput(`{"raw_memory":"raw","rollout_summary":"summary","rollout_slug":null} {}`); err == nil {
		t.Fatal("trailing JSON stage-one output was accepted")
	}
}

func TestConsolidationSpawnErrorClassificationMatchesRust(t *testing.T) {
	err := NewConsolidationSpawnError(errors.New("start failed"))
	var spawnErr *ConsolidationSpawnError
	if !errors.As(err, &spawnErr) || spawnErr.Error() != "start failed" {
		t.Fatalf("spawn error = %T %v", err, err)
	}
}

func newMemoryPipelineRuntime(t *testing.T, home string) *state.StateRuntime {
	t.Helper()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(context.Background(), sqliteConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func writeMemoryPipelineRollout(t *testing.T, home, threadID string, now time.Time) string {
	t.Helper()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: threadID, SessionID: threadID, Source: "cli", ThreadSource: "user",
		CWD: "/workspace", Model: "gpt-test", ModelProvider: "openai", HistoryMode: "legacy",
		MemoryMode: "enabled", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	appendItem := func(value map[string]any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.AppendLine(rollout.Line{Type: "item", Timestamp: now.Format(time.RFC3339Nano), Item: raw}); err != nil {
			t.Fatal(err)
		}
	}
	appendItem(map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "developer secret"}}})
	appendItem(map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "# AGENTS.md instructions for /tmp\n<INSTRUCTIONS>\nignore\n</INSTRUCTIONS>"},
		map[string]any{"type": "input_text", "text": "<skill>\nsecret\n</skill>"},
		map[string]any{"type": "input_text", "text": "<environment_context>\n<cwd>/tmp</cwd>\n</environment_context>"},
		map[string]any{"type": "input_text", "text": "remember this"},
	}})
	appendItem(map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "assistant response"}}})
	appendItem(map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": "reasoning secret"}}})
	appendItem(map[string]any{"type": "function_call", "name": "tool-name", "call_id": "call-1", "arguments": `{}`})
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	return recorder.Path()
}

// v2ArtifactConsolidator writes the summary-only v2 artifact contract.
type v2ArtifactConsolidator struct{}

func (c *v2ArtifactConsolidator) ConsolidateMemory(_ context.Context, request ConsolidationRequest) (*model.AgentUsage, error) {
	summary := "v1\n## User Profile\nprofile\n## User preferences\nprefs\n## General Tips\ntips\n## What's in Memory\nmemory\n"
	return nil, os.WriteFile(filepath.Join(request.Root, MemorySummaryFilename), []byte(summary), 0o600)
}

// Mirrors Rust #43808/#43800: a v2 startup run stores an empty raw memory, sends
// bounded extraction messages, isolates artifacts under memories_v2, and writes
// no raw_memories.md.
func TestStartupPipelineV2UsesBoundedExtractionMessages(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime := newMemoryPipelineRuntime(t, home)
	updated := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	rolloutPath := writeMemoryPipelineRollout(t, home, "memory-v2-thread", updated)
	if err := runtime.ReconcileRollout(ctx, rolloutPath, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `
UPDATE threads SET memory_mode = 'enabled', preview = 'remember this', updated_at = ?, updated_at_ms = ?
WHERE id = 'memory-v2-thread'`, updated.Unix(), updated.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	slug := "v2-slug"
	extractor := &recordingStageOneExtractor{response: StageOneExtractionResponse{
		RolloutSummary: "v2 summary", RolloutSlug: &slug,
	}}
	metrics := state.NewTaskMetrics()
	pipeline := &StartupPipeline{
		State: runtime, CodexHome: home, CurrentThreadID: "current-thread",
		Version: config.MemoryVersionV2,
		Config: config.MemoriesConfig{
			GenerateMemories: true, UseMemories: true,
			MaxRawMemoriesForConsolidation: 256, MaxUnusedDays: 30,
			MaxRolloutAgeDays: 10, MaxRolloutsPerStartup: 2,
			MinRolloutIdleHours: 1, MinRateLimitRemainingPercent: 25,
		},
		StageOne: extractor, StageOneModel: "extract-model",
		StageOneModelInfo: model.ModelInfo{ContextWindow: 10_000, EffectiveContextWindowPercent: 95},
		PhaseTwo:          &v2ArtifactConsolidator{}, PhaseTwoModel: "consolidate-model",
		Metrics: metrics,
	}
	report, err := pipeline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.StageOneSucceeded != 1 || report.StageOneFailed != 0 || report.PhaseTwoStatus != "succeeded" {
		t.Fatalf("startup report = %+v", report)
	}
	// An extractor that reports no usage leaves the token-usage series empty
	// (Rust's `has_token_usage` gate).
	if records := memoryMetricRecords(metrics, MemoryPhaseOneTokenUsageMetric); len(records) != 0 {
		t.Fatalf("token usage records = %#v without reported usage", records)
	}
	if records := memoryMetricRecords(metrics, MemoryPhaseTwoTokenUsageMetric); len(records) != 0 {
		t.Fatalf("phase-two token usage records = %#v without reported usage", records)
	}
	if len(extractor.requests) != 1 {
		t.Fatalf("stage-one requests = %d", len(extractor.requests))
	}
	request := extractor.requests[0]
	if request.Instructions != StageOneSystemPromptForVersion(config.MemoryVersionV2) {
		t.Fatal("v2 extraction did not use the v2 system prompt")
	}
	if strings.TrimSpace(request.Input) != "" || len(request.InputMessages) == 0 {
		t.Fatalf("v2 extraction input = %q / %#v", request.Input, request.InputMessages)
	}
	if _, ok := request.OutputSchema["properties"].(map[string]any)["raw_memory"]; ok {
		t.Fatalf("v2 output schema = %#v", request.OutputSchema)
	}

	var rawMemory, summary string
	if err := runtime.MemoriesDB().QueryRowContext(ctx, `SELECT raw_memory, rollout_summary FROM stage1_outputs WHERE thread_id = 'memory-v2-thread'`).Scan(&rawMemory, &summary); err != nil {
		t.Fatal(err)
	}
	if rawMemory != "" || summary != "v2 summary" {
		t.Fatalf("v2 stage-one output = %q/%q", rawMemory, summary)
	}
	v2Root := RootForVersion(home, config.MemoryVersionV2)
	if _, err := os.Stat(filepath.Join(v2Root, MemorySummaryFilename)); err != nil {
		t.Fatalf("v2 summary artifact missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v2Root, RawMemoriesFilename)); !os.IsNotExist(err) {
		t.Fatalf("v2 must not write raw_memories.md: %v", err)
	}
}
