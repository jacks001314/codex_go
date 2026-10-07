package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/turn"
)

// TestGuardianTurnSkipsHostSkillCatalogLikeRust pins Rust #49584
// (core/src/session/turn_context.rs): Guardian is a "basic session source", so a
// Guardian turn builds no host skill catalog and contributes no skill input
// items. Go injected the host catalog for every app turn, Guardian included.
func TestGuardianTurnSkipsHostSkillCatalogLikeRust(t *testing.T) {
	skillsRoot := t.TempDir()
	skillDir := filepath.Join(skillsRoot, "guardian-gated-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: guardian-gated-skill\ndescription: Must not reach guardian turns\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{Skills: NewSkillsService([]string{skillsRoot})})
	cfg := &config.Config{Values: map[string]any{}}
	cwd := t.TempDir()

	// Guard the guard: a plain app turn still renders the host catalog, so the
	// negative assertions below cannot pass merely because nothing renders.
	plain, _, _, err := router.instructionsWithSkillsContextForTurn(context.Background(), "thread-guardian-plain", "turn-guardian-plain", cfg, &turn.TurnStartParams{CWD: cwd}, "")
	if err != nil {
		t.Fatalf("plain instructions error = %v", err)
	}
	if !strings.Contains(plain, "## Skills") || !strings.Contains(plain, "guardian-gated-skill") {
		t.Fatalf("plain turn lost the host skill catalog:\n%s", plain)
	}

	cases := map[string]*turn.TurnStartParams{
		"originator":   {CWD: cwd, Originator: "guardian"},
		"responsesapi": {CWD: cwd, ResponsesAPIMetadata: map[string]string{"x-openai-subagent": "guardian"}},
	}
	for name, params := range cases {
		instructions, items, _, err := router.instructionsWithSkillsContextForTurn(context.Background(), "thread-guardian-"+name, "turn-guardian-"+name, cfg, params, "")
		if err != nil {
			t.Fatalf("%s: guardian instructions error = %v", name, err)
		}
		if strings.Contains(instructions, "## Skills") || strings.Contains(instructions, "guardian-gated-skill") {
			t.Fatalf("%s: guardian turn injected the host skill catalog:\n%s", name, instructions)
		}
		if len(items) != 0 {
			t.Fatalf("%s: guardian turn contributed %d skill input items, want 0", name, len(items))
		}
	}
}
