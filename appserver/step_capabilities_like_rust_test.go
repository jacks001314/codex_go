package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/skillprovider"
	"codex_go/turn"
)

// Rust #50050: a step keeps the capability roots it captured even when the
// active turn's environment selection moves on. The next step sees the new
// roots, and the skills tool follows the same snapshot as model context.
func TestStepCapabilitySnapshotKeepsSkillsScopedToEachStepLikeRust(t *testing.T) {
	dir := t.TempDir()
	rootA := filepath.Join(dir, "first-root")
	rootB := filepath.Join(dir, "next-root")
	for _, root := range []struct {
		path string
		name string
	}{
		{rootA, "first-helper"},
		{rootB, "next-helper"},
	} {
		skillDir := filepath.Join(root.path, root.name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		contents := "---\nname: " + root.name + "\ndescription: " + root.name + " skill.\n---\n\nUse " + root.name + ".\n"
		if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	roots := []SelectedCapabilityRoot{
		{ID: "first-root", Location: CapabilityRootLocation{Type: CapabilityRootLocationEnvironment, EnvironmentID: "env-a", Path: rootA}},
		{ID: "next-root", Location: CapabilityRootLocation{Type: CapabilityRootLocationEnvironment, EnvironmentID: "env-b", Path: rootB}},
	}
	rawRoots := make([]json.RawMessage, 0, len(roots))
	for _, root := range roots {
		raw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		rawRoots = append(rawRoots, raw)
	}
	store := session.NewStore(dir)
	now := time.Now().UTC()
	threadID := session.ThreadID("thread-step-capabilities")
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			CWD:                     dir,
			SelectedCapabilityRoots: rawRoots,
		},
	}); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	configService := config.NewConfigService(home)
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       configService,
		Skills:       NewSkillsServiceWithOptions(&SkillsServiceOptions{Config: configService}),
	})
	t.Cleanup(func() { _ = router.Close() })

	params := &turn.TurnStartParams{
		ThreadID:     string(threadID),
		CWD:          dir,
		Environments: []map[string]any{{"environmentId": "env-a", "cwd": dir}},
	}
	snapshot := newRuntimeStepCapabilitySnapshot(router, string(threadID), params)
	if err := router.threads.RegisterTurn(string(threadID), "turn-step-capabilities", nil, now.UnixMilli(), params); err != nil {
		t.Fatal(err)
	}
	if !router.threads.UpdateTurn(string(threadID), "turn-step-capabilities", func(active *activeRuntimeTurn) {
		active.StepCapabilities = snapshot
	}) {
		t.Fatal("active turn was not updated")
	}
	if got := rootIDs(snapshot.selectedRoots()); len(got) != 1 || got[0] != "first-root" {
		t.Fatalf("first step roots = %v", got)
	}
	instructionNames := func(params *turn.TurnStartParams) string {
		t.Helper()
		instructions, _, _, err := router.instructionsWithSkillsContextForTurn(context.Background(), string(threadID), "turn-step-capabilities", &config.Config{Values: map[string]any{}}, params, "base instructions")
		if err != nil {
			t.Fatal(err)
		}
		return instructions
	}
	if instructions := instructionNames(params); !strings.Contains(instructions, "first-helper") || strings.Contains(instructions, "next-helper") {
		t.Fatalf("first step instructions = %q", instructions)
	}
	provider := router.executorSkillProviderForThreadSnapshotWithSandbox(string(threadID), snapshot, nil)
	if provider == nil {
		t.Fatal("executor skill provider is nil")
	}
	listNames := func() []string {
		t.Helper()
		catalog := provider.ListKind(context.Background(), skillprovider.SourceExecutor, skillprovider.ListQuery{})
		names := make([]string, 0, len(catalog.Entries))
		for _, entry := range catalog.Entries {
			names = append(names, entry.Name)
		}
		return names
	}
	if names := listNames(); len(names) != 1 || names[0] != "first-helper" {
		t.Fatalf("first step skills = %#v", names)
	}

	// The settings update changes the next step's captured environments without
	// mutating the snapshot that the first step's tools and model context use.
	nextEnvironments := []map[string]any{{"environmentId": "env-b", "cwd": dir}}
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID:     string(threadID),
		TurnID:       "turn-step-capabilities",
		Environments: nextEnvironments,
	})
	if got := rootIDs(snapshot.selectedRoots()); len(got) != 1 || got[0] != "first-root" {
		t.Fatalf("in-flight step roots changed = %v", got)
	}
	if settings := router.turnStepSettingsProvider(string(threadID), "turn-step-capabilities")(); settings == nil {
		t.Fatal("next step settings were not captured")
	}
	if got := rootIDs(snapshot.selectedRoots()); len(got) != 1 || got[0] != "next-root" {
		t.Fatalf("next step roots = %v", got)
	}
	params.Environments = nextEnvironments
	if instructions := instructionNames(params); !strings.Contains(instructions, "next-helper") || strings.Contains(instructions, "first-helper") {
		t.Fatalf("next step instructions = %q", instructions)
	}
	if names := listNames(); len(names) != 1 || names[0] != "next-helper" {
		t.Fatalf("next step skills = %#v", names)
	}
}
