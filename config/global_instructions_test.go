package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGlobalInstructionsManagerRetainsLastSuccessfulAndSuppressesWarnings(t *testing.T) {
	manager := NewGlobalInstructionsManager()
	good := &LoadedUserInstructions{Instructions: &Instructions{Text: "global v1", Source: "/home/AGENTS.md"}}
	failing := &LoadedUserInstructions{Warnings: []string{"Failed to read global AGENTS.md (boom)"}}
	manager.loadFn = func(string) *LoadedUserInstructions { return good }

	first := manager.Load("/home")
	if first.Instructions == nil || first.Instructions.Text != "global v1" || len(first.Warnings) != 0 {
		t.Fatalf("first load = %#v", first)
	}

	// A read failure preserves the last successful instructions and reports once.
	manager.loadFn = func(string) *LoadedUserInstructions { return failing }
	second := manager.Load("/home")
	if second.Instructions == nil || second.Instructions.Text != "global v1" {
		t.Fatalf("failed load did not retain instructions: %#v", second)
	}
	if !reflect.DeepEqual(second.Warnings, []string{"Failed to read global AGENTS.md (boom)"}) {
		t.Fatalf("first failure warnings = %#v", second.Warnings)
	}

	// The same ongoing failure is suppressed.
	third := manager.Load("/home")
	if len(third.Warnings) != 0 {
		t.Fatalf("recurring failure warning = %#v, want suppressed", third.Warnings)
	}
	if third.Instructions == nil || third.Instructions.Text != "global v1" {
		t.Fatalf("suppressed failure lost instructions: %#v", third)
	}

	// Recovery clears the failure, so a later recurrence warns again.
	manager.loadFn = func(string) *LoadedUserInstructions { return good }
	if recovered := manager.Load("/home"); len(recovered.Warnings) != 0 {
		t.Fatalf("recovery warnings = %#v", recovered.Warnings)
	}
	manager.loadFn = func(string) *LoadedUserInstructions { return failing }
	if recurrent := manager.Load("/home"); len(recurrent.Warnings) != 1 {
		t.Fatalf("post-recovery failure warnings = %#v, want one", recurrent.Warnings)
	}
}

func TestGlobalInstructionsManagerClearsWhenSourceRemoved(t *testing.T) {
	manager := NewGlobalInstructionsManager()
	manager.loadFn = func(string) *LoadedUserInstructions {
		return &LoadedUserInstructions{Instructions: &Instructions{Text: "global", Source: "/home/AGENTS.md"}}
	}
	if got := manager.Load("/home"); got.Instructions == nil {
		t.Fatal("expected instructions")
	}
	// A confirmed absence (no instructions and no warnings) clears the snapshot.
	manager.loadFn = func(string) *LoadedUserInstructions { return &LoadedUserInstructions{} }
	cleared := manager.Load("/home")
	if cleared.Instructions != nil || len(cleared.Warnings) != 0 {
		t.Fatalf("cleared load = %#v", cleared)
	}
	// A subsequent failure must not resurrect the cleared instructions.
	manager.loadFn = func(string) *LoadedUserInstructions {
		return &LoadedUserInstructions{Warnings: []string{"boom"}}
	}
	afterFailure := manager.Load("/home")
	if afterFailure.Instructions != nil {
		t.Fatalf("cleared instructions resurrected: %#v", afterFailure)
	}
}

func TestGlobalInstructionsManagerReadsCodexHome(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, DefaultAgentsMDFilename), []byte("live instructions"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	manager := NewGlobalInstructionsManager()
	got := manager.Load(home)
	if got.Instructions == nil || got.Instructions.Text != "live instructions" {
		t.Fatalf("loaded instructions = %#v", got.Instructions)
	}
	if got.Instructions.Source != filepath.Join(home, DefaultAgentsMDFilename) {
		t.Fatalf("source = %q", got.Instructions.Source)
	}
}
