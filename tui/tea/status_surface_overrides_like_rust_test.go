package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
)

// Rust #54387 (upstream 141bf7680d, "Add session-only arguments to /title and
// /statusline"): the inline selections are session-only -- they never touch
// config.toml -- but they are carried with the thread's input state so
// navigation and reconnects keep the selected layout
// (codex-rs/tui/src/chatwidget/status_surface_commands.rs:29,44,48 record,
// :58-70 replay; input_restore.rs:601 captures and :621 replays the overrides
// whenever the thread's input state is restored).
//
// Go keeps one live status widget and has no per-thread input state yet (the
// app-side persistence is filed as an interface contract for the app lane), so
// the in-process subset is a single record keyed by thread id that is replayed
// after a restore reseeded the surfaces from saved settings.

const statusOverridesThreadA = "thread-status-overrides-a"
const statusOverridesThreadB = "thread-status-overrides-b"

func statusOverridesTestModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5", ReasoningEffort: "high"})
	state.SetThreadID(statusOverridesThreadA)
	return NewModel(state, Options{Width: 90, Height: 24})
}

func statusLineItemsFromIDs(t *testing.T, ids []string) []bottompane.StatusLineItem {
	t.Helper()
	items := make([]bottompane.StatusLineItem, 0, len(ids))
	for _, id := range ids {
		item, ok := bottompane.ParseStatusLineItem(id)
		if !ok {
			t.Fatalf("ParseStatusLineItem(%q) failed", id)
		}
		items = append(items, item)
	}
	return items
}

func terminalTitleItemsFromIDs(t *testing.T, ids []string) []chatwidget.TerminalTitleItem {
	t.Helper()
	items := make([]chatwidget.TerminalTitleItem, 0, len(ids))
	for _, id := range ids {
		item, ok := chatwidget.ParseTerminalTitleItem(id)
		if !ok {
			t.Fatalf("ParseTerminalTitleItem(%q) failed", id)
		}
		items = append(items, item)
	}
	return items
}

// seedSavedStatusSurfaces stands in for the app-side restore that reloads the
// thread's saved (config.toml) surfaces into the live widget before the input
// state -- overrides included -- is replayed.
func seedSavedStatusSurfaces(t *testing.T, model *Model, statusLine []string, title []string) {
	t.Helper()
	model.ensureStatusControls()
	model.statusControls.SetupStatusLine(statusLineItemsFromIDs(t, statusLine), model.statusLineUseColors)
	model.statusControls.SetupTerminalTitle(terminalTitleItemsFromIDs(t, title))
}

func TestStatusSurfaceOverridesSurviveResumedThreadLikeRust(t *testing.T) {
	model := statusOverridesTestModel(t)
	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})

	model.applyStatusLineCommand("model current-dir")
	model.applyTerminalTitleCommand("project-name run-state")

	wantStatusLine := []string{"model", "current-dir"}
	if got := model.statusControls.StatusLineIDs; strings.Join(got, " ") != strings.Join(wantStatusLine, " ") {
		t.Fatalf("live status line after /statusline = %v, want %v", got, wantStatusLine)
	}
	wantTitle := chatwidget.TerminalTitleItemIDs([]chatwidget.TerminalTitleItem{chatwidget.TerminalTitleProject, chatwidget.TerminalTitleStatus})
	if got := model.statusControls.TerminalTitleIDs; strings.Join(got, " ") != strings.Join(wantTitle, " ") {
		t.Fatalf("live terminal title after /title = %v, want %v", got, wantTitle)
	}

	// A restore reseeds the saved layout first; the replayed input state must
	// bring the session selections back.
	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})
	model.applyResumeResponse(statusOverridesThreadA, SessionResumeResponse{})

	if got := model.statusControls.StatusLineIDs; strings.Join(got, " ") != strings.Join(wantStatusLine, " ") {
		t.Fatalf("resumed status line = %v, want the session override %v", got, wantStatusLine)
	}
	if got := model.statusControls.TerminalTitleIDs; strings.Join(got, " ") != strings.Join(wantTitle, " ") {
		t.Fatalf("resumed terminal title = %v, want the session override %v", got, wantTitle)
	}
	if !model.statusLineConfiguredByUser {
		t.Fatal("replayed status-line override did not mark the status line as user-configured")
	}
}

func TestStatusSurfaceOverridesStayWithTheirThreadLikeRust(t *testing.T) {
	model := statusOverridesTestModel(t)
	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})
	model.applyStatusLineCommand("model")

	// Another thread restores its own input state, which has no override, so the
	// saved layout must survive untouched.
	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})
	model.applyResumeResponse(statusOverridesThreadB, SessionResumeResponse{})
	if got := strings.Join(model.statusControls.StatusLineIDs, " "); got != "current-dir" {
		t.Fatalf("thread B resumed with %q, want the saved layout untouched by thread A's override", got)
	}

	// Coming back to the overriding thread replays it.
	model.applyResumeResponse(statusOverridesThreadA, SessionResumeResponse{})
	if got := strings.Join(model.statusControls.StatusLineIDs, " "); got != "model" {
		t.Fatalf("thread A resumed with %q, want its session override replayed", got)
	}
}

func TestSavedStatusLineSetupSupersedesSessionOverrideLikeRust(t *testing.T) {
	model := statusOverridesTestModel(t)
	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})
	model.applyStatusLineCommand("model")

	// Saving through the editor drops the session-only override
	// (chatwidget/status_controls.rs:136-137).
	setup := &statusLineSetupModal{
		items:          statusLineItemsFromIDs(t, []string{"current-dir", "hostname"}),
		selected:       map[string]bool{"hostname": true},
		useThemeColors: false,
	}
	model.commitStatusLineSetup(setup)
	if got := strings.Join(model.statusControls.StatusLineIDs, " "); got != "hostname" {
		t.Fatalf("editor save produced %q, want hostname", got)
	}

	// A following restore has no session override left to replay.
	seedSavedStatusSurfaces(t, model, []string{"hostname"}, []string{"project-name"})
	model.applyResumeResponse(statusOverridesThreadA, SessionResumeResponse{})
	if got := strings.Join(model.statusControls.StatusLineIDs, " "); got != "hostname" {
		t.Fatalf("resumed status line = %q, want the saved editor selection hostname", got)
	}
}

func TestSavedColorPreferenceSupersedesSessionColorOverrideLikeRust(t *testing.T) {
	model := statusOverridesTestModel(t)

	model.applyStatusLineCommand("--no-colors")
	if model.statusControls == nil || model.statusControls.StatusLineUseThemeColors {
		t.Fatal("--no-colors did not turn the status-line colours off")
	}

	// The saved settings write for status_line_use_colors clears the override
	// (chatwidget/config_panel.rs:103).
	model.applyLocalSettingsValues(SettingsWriteResult{StatusLineUseColors: boolPtrTea(true)})
	if !model.statusLineUseColors {
		t.Fatal("the saved colour preference did not reach the live model")
	}
	model.ensureStatusControls()
	model.statusControls.StatusLineUseThemeColors = true

	model.applyResumeResponse(statusOverridesThreadA, SessionResumeResponse{})
	if !model.statusControls.StatusLineUseThemeColors || !model.statusLineUseColors {
		t.Fatal("a saved colour preference must win over the cleared session override")
	}
}

// Rust status_line_setup.rs:754-760 keeps `Hostname` in the setup view and it
// stringifies to "hostname" (status_line_setup.rs:79 Display), so the session
// record has to keep that id: a missing id would record an empty selection.
func TestStatusLineHostnameItemKeepsItsIDLikeRust(t *testing.T) {
	model := statusOverridesTestModel(t)
	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})

	model.applyStatusLineCommand("hostname")
	if got := strings.Join(model.statusSurfaceOverrides.lookup(statusOverridesThreadA).statusLine, " "); got != "hostname" {
		t.Fatalf("recorded status-line override = %q, want %q", got, "hostname")
	}

	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})
	model.applyResumeResponse(statusOverridesThreadA, SessionResumeResponse{})
	if got := strings.Join(model.statusControls.StatusLineIDs, " "); got != "hostname" {
		t.Fatalf("resumed status line = %q, want %q", got, "hostname")
	}
}

// TestStatusSurfaceOverridesKeepEveryThreadLikeRust pins the per-thread
// carrier: Rust stores StatusSurfaceOverrides inside each thread's
// ThreadInputState (chatwidget/input_restore.rs:601,621), so two threads can
// each keep their own /statusline and /title selection. A single process-wide
// slot would forget thread A's selection as soon as thread B recorded one.
func TestStatusSurfaceOverridesKeepEveryThreadLikeRust(t *testing.T) {
	model := statusOverridesTestModel(t)
	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})

	model.applyStatusLineCommand("model")
	model.applyTerminalTitleCommand("project-name")

	// Thread B starts from its saved layout and records its own selection.
	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})
	model.applyResumeResponse(statusOverridesThreadB, SessionResumeResponse{})
	if got := strings.Join(model.statusControls.StatusLineIDs, " "); got != "current-dir" {
		t.Fatalf("thread B resumed with %q, want the saved layout untouched by thread A", got)
	}
	model.applyStatusLineCommand("hostname")
	model.applyTerminalTitleCommand("status")

	// Back to A: its own selection must return.
	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})
	model.applyResumeResponse(statusOverridesThreadA, SessionResumeResponse{})
	if got := strings.Join(model.statusControls.StatusLineIDs, " "); got != "model" {
		t.Fatalf("thread A status line = %q, want its own override model", got)
	}
	if got := strings.Join(model.statusControls.TerminalTitleIDs, " "); got != "project-name" {
		t.Fatalf("thread A terminal title = %q, want project-name", got)
	}

	// And B still holds its own record.
	seedSavedStatusSurfaces(t, model, []string{"current-dir"}, []string{"project-name"})
	model.applyResumeResponse(statusOverridesThreadB, SessionResumeResponse{})
	if got := strings.Join(model.statusControls.StatusLineIDs, " "); got != "hostname" {
		t.Fatalf("thread B status line = %q, want its own override hostname", got)
	}
	if got := strings.Join(model.statusControls.TerminalTitleIDs, " "); got != "run-state" {
		t.Fatalf("thread B terminal title = %q, want run-state", got)
	}
}
