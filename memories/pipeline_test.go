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
}

func (c *artifactConsolidator) ConsolidateMemory(_ context.Context, request ConsolidationRequest) error {
	c.mu.Lock()
	c.requests = append(c.requests, request)
	c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	if err := os.WriteFile(filepath.Join(request.Root, MemoryFilename), []byte("# Memory\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(request.Root, MemorySummaryFilename), []byte("v1\n# Summary\n"), 0o600)
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
	}}
	consolidator := &artifactConsolidator{}
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
