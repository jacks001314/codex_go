package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	promptctx "codex_go/prompt"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

// Mirrors codex-rs/ext/skills/src/render_observability.rs
// (record_catalog_metrics): the enabled/kept totals, whether anything was
// truncated, and the truncated description characters, tagged by surface; a
// skipped render records the zero report.
func TestRecordSkillCatalogRenderLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{SkillShadowMetrics: metrics})

	router.recordSkillCatalogRender(skillCatalogSurfaceTurnInput, &promptctx.SkillRenderReport{
		TotalCount:                5,
		IncludedCount:             3,
		OmittedCount:              2,
		TruncatedDescriptionChars: 120,
	})
	router.recordSkillCatalogRender(skillCatalogSurfaceExecutorWorld, nil)

	byName := map[string]*state.TaskMetric{}
	for _, record := range metrics.Records() {
		byName[record.Name+"/"+record.Tags["catalog_surface"]] = record
	}
	if len(byName) != 8 {
		t.Fatalf("records = %#v", byName)
	}
	for name, want := range map[string]int{
		telemetry.ThreadSkillsEnabledTotalMetric:              5,
		telemetry.ThreadSkillsKeptTotalMetric:                 3,
		telemetry.ThreadSkillsTruncatedMetric:                 1,
		telemetry.ThreadSkillsDescriptionTruncatedCharsMetric: 120,
	} {
		record := byName[name+"/"+skillCatalogSurfaceTurnInput]
		if record == nil || record.Kind != "histogram" || record.Value != want {
			t.Fatalf("%s record = %#v, want %d", name, record, want)
		}
	}
	for name, want := range map[string]int{
		telemetry.ThreadSkillsEnabledTotalMetric:              0,
		telemetry.ThreadSkillsKeptTotalMetric:                 0,
		telemetry.ThreadSkillsTruncatedMetric:                 0,
		telemetry.ThreadSkillsDescriptionTruncatedCharsMetric: 0,
	} {
		record := byName[name+"/"+skillCatalogSurfaceExecutorWorld]
		if record == nil || record.Value != want {
			t.Fatalf("zero-report %s record = %#v", name, record)
		}
	}

	// A nil sink records nothing.
	NewRuntimeRouter(RuntimeServices{}).recordSkillCatalogRender(skillCatalogSurfaceTurnInput, nil)
}

// The turn-instructions render records the catalog metrics for the host
// (turn_input) and executor surfaces.
func TestInstructionsWithSkillsContextRecordsCatalogMetrics(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skill-a")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: skill-a\ndescription: Useful skill\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{
		Skills:             NewSkillsService([]string{root}),
		SkillShadowMetrics: metrics,
	})
	if _, _, _, err := router.instructionsWithSkillsContextForTurn(
		context.Background(), "thread-1", "turn-1", nil, &turn.TurnStartParams{Prompt: "hello"}, "",
	); err != nil {
		t.Fatalf("instructionsWithSkillsContextForTurn() error = %v", err)
	}
	byName := map[string]*state.TaskMetric{}
	for _, record := range metrics.Records() {
		byName[record.Name+"/"+record.Tags["catalog_surface"]] = record
	}
	enabled := byName[telemetry.ThreadSkillsEnabledTotalMetric+"/"+skillCatalogSurfaceTurnInput]
	if enabled == nil || enabled.Value != 1 {
		t.Fatalf("turn_input enabled total = %#v", enabled)
	}
	kept := byName[telemetry.ThreadSkillsKeptTotalMetric+"/"+skillCatalogSurfaceTurnInput]
	if kept == nil || kept.Value != 1 {
		t.Fatalf("turn_input kept total = %#v", kept)
	}
	if byName[telemetry.ThreadSkillsTruncatedMetric+"/"+skillCatalogSurfaceTurnInput] == nil {
		t.Fatalf("turn_input truncated record missing: %#v", byName)
	}
	if byName[telemetry.ThreadSkillsEnabledTotalMetric+"/"+skillCatalogSurfaceExecutorWorld] == nil {
		t.Fatalf("executor surface record missing: %#v", byName)
	}
}
