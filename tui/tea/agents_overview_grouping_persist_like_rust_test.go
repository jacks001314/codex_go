package tea

import (
	"errors"
	"strings"
	"testing"

	agentsoverview "codex_go/tui/agents_overview"
)

// TestAgentsOverviewGroupingPersistsLikeRust covers Rust #50786: cycling the
// Command Center grouping writes `tui.agents_overview_grouping` with the
// kebab-case value of the newly selected mode, keeps the live choice active,
// and preserves the other modes in the cycle. Rust counterpart:
// overview_grouping_persists_across_config_reloads.
func TestAgentsOverviewGroupingPersistsLikeRust(t *testing.T) {
	var edits [][]SettingsEdit
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewPinnableRows(), nil
		},
		OnWriteSettings: func(in []SettingsEdit) (SettingsWriteResult, error) {
			edits = append(edits, append([]SettingsEdit(nil), in...))
			return SettingsWriteResult{FilePath: "/home/user/.codex/config.toml"}, nil
		},
	})
	openAgentsDashboard(t, model)
	if model.agentsOverview.State.Grouping != agentsoverview.GroupingProject {
		t.Fatalf("default grouping = %v, want project", model.agentsOverview.State.Grouping)
	}

	for _, want := range []struct {
		value    string
		grouping agentsoverview.Grouping
	}{
		{"status", agentsoverview.GroupingStatus},
		{"model", agentsoverview.GroupingModel},
		{"project", agentsoverview.GroupingProject},
	} {
		updated, command := model.Update(agentsKeyEvent('g'))
		model = updated.(*Model)
		if command == nil {
			t.Fatalf("grouping toggle to %q returned no persistence command", want.value)
		}
		if model.agentsOverview.State.Grouping != want.grouping {
			t.Fatalf("live grouping = %v, want %v", model.agentsOverview.State.Grouping, want.grouping)
		}
		if model.agentsOverviewGrouping != want.grouping {
			t.Fatalf("remembered grouping = %v, want %v", model.agentsOverviewGrouping, want.grouping)
		}
		message := command()
		updated, _ = model.Update(message)
		model = updated.(*Model)
	}

	if len(edits) != 3 {
		t.Fatalf("settings writes = %d, want 3", len(edits))
	}
	for index, want := range []string{"status", "model", "project"} {
		got := edits[index]
		if len(got) != 1 || got[0].KeyPath != "tui.agents_overview_grouping" || got[0].Value != want {
			t.Fatalf("write %d = %#v, want tui.agents_overview_grouping=%q", index, got, want)
		}
	}
}

// TestAgentsOverviewGroupingSaveFailureLikeRust covers Rust #50786: a failed
// write reports "Failed to save Command Center grouping: {err}" inside Command
// Center while the selected grouping stays active.
func TestAgentsOverviewGroupingSaveFailureLikeRust(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewPinnableRows(), nil
		},
		OnWriteSettings: func([]SettingsEdit) (SettingsWriteResult, error) {
			return SettingsWriteResult{}, errors.New("config file is read-only")
		},
	})
	openAgentsDashboard(t, model)
	updated, command := model.Update(agentsKeyEvent('g'))
	model = updated.(*Model)
	if model.agentsOverview.State.Grouping != agentsoverview.GroupingStatus {
		t.Fatalf("grouping after failed save = %v, want status", model.agentsOverview.State.Grouping)
	}
	if command == nil {
		t.Fatal("grouping toggle returned no persistence command")
	}
	updated, _ = model.Update(command())
	model = updated.(*Model)
	if got := model.agentsOverviewNotice; !strings.Contains(got, "Failed to save Command Center grouping: config file is read-only") {
		t.Fatalf("Command Center notice = %q, want the grouping save failure", got)
	}
}

// TestAgentsOverviewGroupingRestoreLikeRust covers Rust #50786's startup half:
// the configured `tui.agents_overview_grouping` seeds the dashboard, unknown
// or missing values fall back to project, and every mode round-trips through
// its kebab-case configuration value.
func TestAgentsOverviewGroupingRestoreLikeRust(t *testing.T) {
	for _, tc := range []struct {
		value    string
		grouping agentsoverview.Grouping
	}{
		{"project", agentsoverview.GroupingProject},
		{"status", agentsoverview.GroupingStatus},
		{"model", agentsoverview.GroupingModel},
		{"", agentsoverview.GroupingProject},
		{" PROJECT ", agentsoverview.GroupingProject},
		{"bogus", agentsoverview.GroupingProject},
	} {
		if got := agentsoverview.ParseGroupingConfig(tc.value); got != tc.grouping {
			t.Fatalf("ParseGroupingConfig(%q) = %v, want %v", tc.value, got, tc.grouping)
		}
	}
	for _, grouping := range []agentsoverview.Grouping{
		agentsoverview.GroupingProject,
		agentsoverview.GroupingStatus,
		agentsoverview.GroupingModel,
	} {
		if got := agentsoverview.ParseGroupingConfig(grouping.ConfigValue()); got != grouping {
			t.Fatalf("round trip %v -> %q -> %v", grouping, grouping.ConfigValue(), got)
		}
	}

	model := NewModel(nil, Options{
		Width:                  120,
		Height:                 24,
		AgentsOverviewGrouping: "model",
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewPinnableRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	if model.agentsOverview.State.Grouping != agentsoverview.GroupingModel {
		t.Fatalf("restored grouping = %v, want model", model.agentsOverview.State.Grouping)
	}
}
