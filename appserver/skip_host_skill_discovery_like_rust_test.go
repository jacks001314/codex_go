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

func writeHostSkillFixtureForTest(t *testing.T, skillsRoot, name string) {
	t.Helper()
	skillDir := filepath.Join(skillsRoot, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	body := "---\nname: " + name + "\ndescription: Host skill fixture\n---\n"
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
}

func skipHostSkillDiscoveryConfigForTest(enabled bool) *config.Config {
	return &config.Config{Values: map[string]any{
		"features": map[string]any{"skip_host_skill_discovery": enabled},
	}}
}

type skillInvocationContributorForTest struct{ requiresHostSkills bool }

func (c skillInvocationContributorForTest) RequiresHostSkillDiscovery() bool {
	return c.requiresHostSkills
}

// TestTurnSkipsHostSkillDiscoveryFeatureLikeRust pins Rust #40631
// (core/src/session/turn_context.rs): when skip_host_skill_discovery is enabled,
// turn setup supplies an empty host skill snapshot while executor, orchestrator,
// and instruction loading stay in place.
func TestTurnSkipsHostSkillDiscoveryFeatureLikeRust(t *testing.T) {
	skillsRoot := t.TempDir()
	writeHostSkillFixtureForTest(t, skillsRoot, "host-only-skill")
	router := NewRuntimeRouter(RuntimeServices{Skills: NewSkillsService([]string{skillsRoot})})
	cwd := t.TempDir()
	params := &turn.TurnStartParams{CWD: cwd}

	// Guard the guard: without the feature the host catalog still renders.
	plain, _, _, err := router.instructionsWithSkillsContextForTurn(context.Background(), "thread-skip-plain", "turn-skip-plain", skipHostSkillDiscoveryConfigForTest(false), params, "base instructions")
	if err != nil {
		t.Fatalf("plain instructions error = %v", err)
	}
	if !strings.Contains(plain, "host-only-skill") {
		t.Fatalf("plain turn lost the host skill catalog:\n%s", plain)
	}

	instructions, items, _, err := router.instructionsWithSkillsContextForTurn(context.Background(), "thread-skip-host", "turn-skip-host", skipHostSkillDiscoveryConfigForTest(true), params, "base instructions")
	if err != nil {
		t.Fatalf("skip instructions error = %v", err)
	}
	if strings.Contains(instructions, "host-only-skill") || strings.Contains(instructions, "## Skills") {
		t.Fatalf("skip_host_skill_discovery still rendered the host catalog:\n%s", instructions)
	}
	if !strings.Contains(instructions, "base instructions") {
		t.Fatalf("skip_host_skill_discovery dropped base instructions:\n%s", instructions)
	}
	if len(items) != 0 {
		t.Fatalf("skip_host_skill_discovery contributed %d skill input items, want 0", len(items))
	}

	// A contributor that only supplies executor skills does not keep host
	// discovery, while one that requires host-owned skills restores it.
	executorOnly := NewRuntimeRouter(RuntimeServices{
		Skills:                      NewSkillsService([]string{skillsRoot}),
		SkillInvocationContributors: []SkillInvocationContributor{skillInvocationContributorForTest{}},
	})
	instructions, _, _, err = executorOnly.instructionsWithSkillsContextForTurn(context.Background(), "thread-skip-executor", "turn-skip-executor", skipHostSkillDiscoveryConfigForTest(true), params, "base instructions")
	if err != nil {
		t.Fatalf("executor-only instructions error = %v", err)
	}
	if strings.Contains(instructions, "host-only-skill") {
		t.Fatalf("executor-only contributor kept host discovery:\n%s", instructions)
	}
	hostDependent := NewRuntimeRouter(RuntimeServices{
		Skills:                      NewSkillsService([]string{skillsRoot}),
		SkillInvocationContributors: []SkillInvocationContributor{skillInvocationContributorForTest{requiresHostSkills: true}},
	})
	instructions, _, _, err = hostDependent.instructionsWithSkillsContextForTurn(context.Background(), "thread-skip-host-dependent", "turn-skip-host-dependent", skipHostSkillDiscoveryConfigForTest(true), params, "base instructions")
	if err != nil {
		t.Fatalf("host-dependent instructions error = %v", err)
	}
	if !strings.Contains(instructions, "host-only-skill") {
		t.Fatalf("host-dependent contributor lost host discovery:\n%s", instructions)
	}
}

// TestBuildHostSkillsPromptContextSkipsHostDiscoveryLikeRust covers the
// non-app-server entry point, which builds the host catalog directly.
func TestBuildHostSkillsPromptContextSkipsHostDiscoveryLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	skillsRoot := filepath.Join(codexHome, "skills")
	writeHostSkillFixtureForTest(t, skillsRoot, "host-only-skill")

	enabled, err := BuildHostSkillsPromptContext(&HostSkillsPromptOptions{
		CodexHome: codexHome,
		CWD:       t.TempDir(),
		Config:    skipHostSkillDiscoveryConfigForTest(true),
	})
	if err != nil {
		t.Fatalf("BuildHostSkillsPromptContext(enabled) error = %v", err)
	}
	if strings.Contains(enabled.Instructions, "host-only-skill") || len(enabled.InputItems) != 0 {
		t.Fatalf("skip_host_skill_discovery built a host catalog: %#v", enabled)
	}

	disabled, err := BuildHostSkillsPromptContext(&HostSkillsPromptOptions{
		CodexHome: codexHome,
		CWD:       t.TempDir(),
		Config:    skipHostSkillDiscoveryConfigForTest(false),
	})
	if err != nil {
		t.Fatalf("BuildHostSkillsPromptContext(disabled) error = %v", err)
	}
	if !strings.Contains(disabled.Instructions, "host-only-skill") {
		t.Fatalf("disabled config lost the host skill catalog:\n%s", disabled.Instructions)
	}

	hostDependent, err := BuildHostSkillsPromptContext(&HostSkillsPromptOptions{
		CodexHome:    codexHome,
		CWD:          t.TempDir(),
		Config:       skipHostSkillDiscoveryConfigForTest(true),
		Contributors: []SkillInvocationContributor{skillInvocationContributorForTest{requiresHostSkills: true}},
	})
	if err != nil {
		t.Fatalf("BuildHostSkillsPromptContext(host-dependent) error = %v", err)
	}
	if !strings.Contains(hostDependent.Instructions, "host-only-skill") {
		t.Fatalf("host-dependent contributor lost the host catalog:\n%s", hostDependent.Instructions)
	}
}
