package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	historycell "codex_go/tui/history_cell"
	"codex_go/utils"
)

func serverVersionNoticeCount(model *Model) int {
	count := 0
	for _, cell := range model.warningCells {
		switch warning := cell.(type) {
		case historycell.WarningEventCell:
			if warning.ServerVersionNotice {
				count++
			}
		case *historycell.WarningEventCell:
			if warning != nil && warning.ServerVersionNotice {
				count++
			}
		}
	}
	return count
}

// Rust #43698: `tui.show_server_version_notice` gates startup and overview
// notices, clears pending state when disabled, and restores it when re-enabled.
func TestServerVersionNoticeConfigGateLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.CLIVersion = "0.153.0"
	model := NewModel(state, Options{
		LocalDaemonSession: true,
		DaemonVersion:      "0.152.1",
	})
	if got := serverVersionNoticeCount(model); got != 1 {
		t.Fatalf("startup server-version notices = %d, want 1", got)
	}
	if !strings.Contains(model.agentsOverviewServerVersionNotice, "Service v0.152.1 < Codex CLI v0.153.0") ||
		!strings.Contains(model.agentsOverviewServerVersionNotice, "/daemon") {
		t.Fatalf("overview notice = %q", model.agentsOverviewServerVersionNotice)
	}

	model.applyLocalSettings(LocalSettings{Tui: SettingsWriteResult{
		ShowServerVersionNotice: boolPtrTea(false),
	}})
	if got := serverVersionNoticeCount(model); got != 0 {
		t.Fatalf("disabled notices retained %d warning cells", got)
	}
	if model.agentsOverviewServerVersionNotice != "" {
		t.Fatalf("disabled overview notice = %q", model.agentsOverviewServerVersionNotice)
	}

	model.applyLocalSettings(LocalSettings{Tui: SettingsWriteResult{
		ShowServerVersionNotice: boolPtrTea(true),
	}})
	if got := serverVersionNoticeCount(model); got != 1 {
		t.Fatalf("re-enabled server-version notices = %d, want 1", got)
	}
}

func TestServerVersionNoticeOnlyForConnectedNonEmbeddedSessionsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.CLIVersion = "0.153.0"
	for _, testCase := range []struct {
		name    string
		options Options
		want    int
	}{
		{name: "embedded", options: Options{DaemonVersion: "0.152.1"}, want: 0},
		{name: "same version", options: Options{LocalDaemonSession: true, DaemonVersion: "0.153.0"}, want: 0},
		{name: "local daemon", options: Options{LocalDaemonSession: true, DaemonVersion: "0.152.1"}, want: 1},
	} {
		model := NewModel(state, testCase.options)
		if got := serverVersionNoticeCount(model); got != testCase.want {
			t.Fatalf("%s: notices = %d, want %d", testCase.name, got, testCase.want)
		}
	}
}

func TestServerVersionOverviewNoticeUsesDifferentComparisonLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.CLIVersion = "0.0.0"
	model := NewModel(state, Options{
		LocalDaemonSession: true,
		DaemonVersion:      "0.153.0",
	})
	if !strings.Contains(model.agentsOverviewServerVersionNotice, "Service v0.153.0 \u2260 Codex CLI v0.0.0") {
		t.Fatalf("different-line notice = %q", model.agentsOverviewServerVersionNotice)
	}
}

func TestServerVersionNoticeRendersInCommandCenterFooterLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.CLIVersion = "0.153.0"
	model := NewModel(state, Options{
		LocalDaemonSession: true,
		DaemonVersion:      "0.152.1",
		Width:              120,
		Height:             24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return []agentsoverview.Row{{ThreadID: "root-1", Name: "alpha", Group: agentsoverview.GroupReady}}, nil
		},
	})
	openAgentsDashboard(t, model)
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "Service v0.152.1 < Codex CLI v0.153.0 \u00b7 /daemon") {
		t.Fatalf("command center missing server-version footer:\n%s", view)
	}

	model.applyLocalSettings(LocalSettings{Tui: SettingsWriteResult{
		ShowServerVersionNotice: boolPtrTea(false),
	}})
	view = utils.StripANSI(model.View())
	if strings.Contains(view, "Service v0.152.1") {
		t.Fatalf("disabled server-version notice still rendered:\n%s", view)
	}
}
