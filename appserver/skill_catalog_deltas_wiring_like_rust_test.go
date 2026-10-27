package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

func writeDeltaCatalogSkill(t *testing.T, root string, name string, description string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", name, err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n"
	if err := os.WriteFile(filepath.Join(dir, SkillFilename), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
}

func newSkillCatalogDeltaRouter(t *testing.T, home string, skillsRoot string) *RuntimeRouter {
	t.Helper()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	record := &session.Record{ID: "thread-deltas", SessionID: "thread-deltas", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-6-astra", ModelProvider: "openai"}}
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread record error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Skills:       NewSkillsService([]string{skillsRoot}),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(NewNotificationBuffer())
	return router
}

// Rust #57262 end to end: the first turn lists the catalog, and a later turn on
// the same thread publishes only the newly added skill.
func TestSkillCatalogDeltasPublishOnlyNewSkillsEndToEndLikeRust(t *testing.T) {
	home := t.TempDir()
	skillsRoot := t.TempDir()
	writeDeltaCatalogSkill(t, skillsRoot, "alpha", "Alpha skill.")
	router := newSkillCatalogDeltaRouter(t, home, skillsRoot)
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{"skill_catalog_deltas": true}}}
	params := &turn.TurnStartParams{CWD: t.TempDir()}

	first, _, _, err := router.instructionsWithSkillsContextForTurn(context.Background(), "thread-deltas", "turn-1", cfg, params, "")
	if err != nil {
		t.Fatalf("first turn error = %v", err)
	}
	if !strings.Contains(first, "## Host skills") || !strings.Contains(first, "- alpha: Alpha skill.") {
		t.Fatalf("first turn should publish a full host listing:\n%s", first)
	}

	writeDeltaCatalogSkill(t, skillsRoot, "beta", "Beta skill.")
	router.services.Skills.ClearCache()
	second, _, _, err := router.instructionsWithSkillsContextForTurn(context.Background(), "thread-deltas", "turn-2", cfg, params, "")
	if err != nil {
		t.Fatalf("second turn error = %v", err)
	}
	if !strings.Contains(second, "## Host skills update") || !strings.Contains(second, "- beta: Beta skill.") {
		t.Fatalf("second turn should publish only the new skill:\n%s", second)
	}
	if strings.Contains(second, "- alpha:") {
		t.Fatalf("second turn must not repeat unchanged skills:\n%s", second)
	}
	if strings.Contains(second, "### How to use skills") {
		t.Fatalf("second turn must not repeat usage instructions:\n%s", second)
	}

	// Compaction restarts the catalog: the snapshot is dropped with the window.
	record, err := router.threadRecord("thread-deltas", true, true)
	if err != nil {
		t.Fatalf("read record error = %v", err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatalf("decode world state error = %v", err)
	}
	if len(state.HostSkillsCatalog) == 0 {
		t.Fatal("expected the host catalog snapshot to be persisted")
	}
	if err := placeIncrementalCatalogBeforeCompactedHistory(record, nil, 2, "gpt-6-astra", nil); err != nil {
		t.Fatalf("compaction reset error = %v", err)
	}
	if err := router.runtimeSaveThreadRecord(record); err != nil {
		t.Fatalf("save record error = %v", err)
	}
	afterCompaction, _, _, err := router.instructionsWithSkillsContextForTurn(context.Background(), "thread-deltas", "turn-3", cfg, params, "")
	if err != nil {
		t.Fatalf("post-compaction turn error = %v", err)
	}
	if !strings.Contains(afterCompaction, "## Host skills") || !strings.Contains(afterCompaction, "- alpha: Alpha skill.") {
		t.Fatalf("compaction should rebuild the full catalog:\n%s", afterCompaction)
	}
}

// Rust #57500 end to end: the enabled flag removes descriptions from the
// model-visible catalog.
func TestOmitSkillDescriptionsEndToEndLikeRust(t *testing.T) {
	skillsRoot := t.TempDir()
	writeDeltaCatalogSkill(t, skillsRoot, "alpha", "Alpha skill.")
	router := NewRuntimeRouter(RuntimeServices{Skills: NewSkillsService([]string{skillsRoot})})
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{"omit_skill_descriptions": true}}}
	instructions, _, _, err := router.instructionsWithSkillsContextForTurn(context.Background(), "thread-omit", "turn-omit", cfg, &turn.TurnStartParams{CWD: t.TempDir()}, "")
	if err != nil {
		t.Fatalf("turn error = %v", err)
	}
	if !strings.Contains(instructions, "- alpha: (file:") {
		t.Fatalf("expected a name-only skill row:\n%s", instructions)
	}
	if strings.Contains(instructions, "Alpha skill.") {
		t.Fatalf("catalog should omit the description:\n%s", instructions)
	}
}
