package app

import (
	"os"
	"testing"

	"codex_go/cli"
	"codex_go/config"
)

// TestSettingsCarryAgentsOverviewGroupingLikeRust covers Rust #50786's startup
// read: the configured `tui.agents_overview_grouping` reaches the TUI settings
// record (which the Command Center restores from), and an unset key keeps the
// project default. Rust counterpart:
// agents_overview_startup_restores_saved_grouping.
func TestSettingsCarryAgentsOverviewGroupingLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("[tui]\nagents_overview_grouping = \"model\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if settings := interactiveTUISettings(&cli.RootOptions{}); settings.AgentsOverviewGrouping != "model" {
		t.Fatalf("settings AgentsOverviewGrouping = %q, want \"model\"", settings.AgentsOverviewGrouping)
	}

	empty := t.TempDir()
	t.Setenv("CODEX_HOME", empty)
	if err := os.WriteFile(config.ConfigPath(empty), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if settings := interactiveTUISettings(&cli.RootOptions{}); settings.AgentsOverviewGrouping != "" {
		t.Fatalf("unset AgentsOverviewGrouping = %q, want empty", settings.AgentsOverviewGrouping)
	}
}
