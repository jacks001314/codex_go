package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func resumeCWDFixture(t *testing.T, mode string) *Model {
	t.Helper()
	return NewModel(codextui.NewState(nil), Options{
		Width:            100,
		Height:           30,
		SessionPickerCWD: "/current",
		ResumeCWD:        mode,
	})
}

func resumeCWDResponse() SessionResumeResponse {
	return SessionResumeResponse{
		Summary: &codextui.SessionSummary{
			ThreadID: "thread-resume",
			Title:    "Saved session",
			CWD:      "/saved",
		},
		Status: "idle",
	}
}

func TestResumeCWDModesAndPromptLikeRust(t *testing.T) {
	ask := resumeCWDFixture(t, "ask")
	ask.applyResumeResponse("thread-resume", resumeCWDResponse())
	if ask.modal == nil || ask.modal.id != resumeCWDPromptID || ask.pendingResumeCWD == nil {
		t.Fatalf("ask did not open the cwd prompt: %#v", ask.modal)
	}
	if ask.State.ThreadID == "thread-resume" {
		t.Fatal("resume applied before the cwd selection")
	}
	ask.modal.selected = 1 // Use current directory
	ask.respondModal(false)
	if ask.modal != nil || ask.State.ThreadID != "thread-resume" || ask.State.CWD != "/current" || ask.sessionCWD != "/current" {
		t.Fatalf("current selection = modal:%#v thread:%q cwd:%q", ask.modal, ask.State.ThreadID, ask.State.CWD)
	}

	always := resumeCWDFixture(t, "ask")
	always.onWriteSettings = func([]SettingsEdit) (SettingsWriteResult, error) {
		return SettingsWriteResult{FilePath: "/tmp/config.toml"}, nil
	}
	always.applyResumeResponse("thread-resume", resumeCWDResponse())
	always.modal.selected = 2 // Always use session directory
	cmd := always.respondModal(false)
	if always.State.CWD != "/saved" || always.sessionCWD != "/saved" || always.resumeCWD != "session" {
		t.Fatalf("always-session = cwd:%q mode:%q", always.State.CWD, always.resumeCWD)
	}
	if cmd == nil {
		t.Fatal("always-session did not persist the preference")
	}

	current := resumeCWDFixture(t, "current")
	current.applyResumeResponse("thread-resume", resumeCWDResponse())
	if current.modal != nil || current.State.CWD != "/current" {
		t.Fatalf("current mode = modal:%#v cwd:%q", current.modal, current.State.CWD)
	}

	session := resumeCWDFixture(t, "session")
	session.applyResumeResponse("thread-resume", resumeCWDResponse())
	if session.modal != nil || session.State.CWD != "/saved" {
		t.Fatalf("session mode = modal:%#v cwd:%q", session.modal, session.State.CWD)
	}
}

func TestResumeCWDPromptEscUsesSessionDirectoryLikeRust(t *testing.T) {
	model := resumeCWDFixture(t, "ask")
	model.applyResumeResponse("thread-resume", resumeCWDResponse())
	model.respondModal(true)
	if model.State.CWD != "/saved" || model.pendingResumeCWD != nil {
		t.Fatalf("Esc selection = cwd:%q pending:%#v", model.State.CWD, model.pendingResumeCWD)
	}
}

// TestResumeCWDResolvedBeforeResumeLikeRust locks Rust's ordering: the cwd
// choice reaches the resume request, so a remote app server switches the
// thread to the chosen directory instead of receiving an unconditional caller
// override.
func TestResumeCWDResolvedBeforeResumeLikeRust(t *testing.T) {
	selection := func() codextui.SessionSelection {
		return codextui.SessionSelection{
			Kind:   codextui.SessionSelectionResume,
			Target: codextui.SessionTarget{ThreadID: "thread-resume", CWD: "/saved"},
		}
	}
	for _, tc := range []struct {
		mode      string
		choose    int
		wantCWD   string
		wantModal bool
	}{
		{mode: "session", wantCWD: "/saved"},
		{mode: "current", wantCWD: "/current"},
		{mode: "ask", choose: 1, wantCWD: "/current", wantModal: true},
		{mode: "ask", choose: -1, wantCWD: "/saved", wantModal: true},
	} {
		model := resumeCWDFixture(t, tc.mode)
		var resolved []string
		model.onResumeSession = func(got codextui.SessionSelection) (SessionResumeResponse, error) {
			resolved = append(resolved, got.ResumeCWD)
			return resumeCWDResponse(), nil
		}
		decision, notice, complete := model.applySessionSelection(selection())
		if tc.wantModal {
			if complete || model.modal == nil || model.modal.id != resumeCWDPromptID {
				t.Fatalf("%s: expected pre-resume prompt, decision=%v notice=%q modal=%#v", tc.mode, decision, notice, model.modal)
			}
			if len(resolved) != 0 {
				t.Fatalf("%s: resumed before the cwd choice: %#v", tc.mode, resolved)
			}
			if tc.choose >= 0 {
				model.modal.selected = tc.choose
			}
			model.respondModal(tc.choose < 0)
		} else if !complete || decision == nil {
			t.Fatalf("%s: resume incomplete, notice=%q", tc.mode, notice)
		}
		if len(resolved) != 1 || resolved[0] != tc.wantCWD {
			t.Fatalf("%s: resume cwds = %#v, want %q", tc.mode, resolved, tc.wantCWD)
		}
		if model.State.ThreadID != "thread-resume" || model.State.CWD != tc.wantCWD {
			t.Fatalf("%s: state thread=%q cwd=%q", tc.mode, model.State.ThreadID, model.State.CWD)
		}
	}
}

func TestResumeCWDCurrentUnavailableForRemoteLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:            100,
		Height:           30,
		SessionPickerCWD: "/caller",
		ResumeCWD:        "current",
		RemoteAppServer:  true,
	})
	var resolved []string
	model.onResumeSession = func(got codextui.SessionSelection) (SessionResumeResponse, error) {
		resolved = append(resolved, got.ResumeCWD)
		return resumeCWDResponse(), nil
	}
	_, notice, _ := model.applySessionSelection(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{ThreadID: "thread-resume", CWD: "/remote/saved"},
	})
	want := "`tui.resume_cwd = \"current\"` requires `--cd` when using a remote workspace"
	if strings.TrimSpace(notice) != want || len(resolved) != 0 {
		t.Fatalf("remote current = notice:%q resumed:%#v", notice, resolved)
	}
}

func TestConfigPanelResumeDirectoryCyclesLikeRust(t *testing.T) {
	var edits []SettingsEdit
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		OnWriteSettings: func(next []SettingsEdit) (SettingsWriteResult, error) {
			edits = append([]SettingsEdit(nil), next...)
			return SettingsWriteResult{FilePath: "/tmp/config.toml"}, nil
		},
	})
	model.openConfigPanel()
	model.modal.configPanel.section = configSectionSessions
	rows := model.configPanelFilteredRows()
	var resumeRow configPanelRow
	for _, row := range rows {
		if row.label == "Resume directory" {
			resumeRow = row
		}
	}
	if resumeRow.action != configActionResumeCWD {
		t.Fatalf("resume row = %#v", resumeRow)
	}
	runTeaCmd(t, model, model.activateConfigRow(resumeRow))
	if len(edits) != 1 || edits[0].KeyPath != "tui.resume_cwd" || edits[0].Value != "current" {
		t.Fatalf("resume write = %#v", edits)
	}
	if !strings.Contains(model.configPanelValue(resumeRow), "Current") {
		t.Fatalf("resume value = %q", model.configPanelValue(resumeRow))
	}
}

func TestConfigPanelResumeDirectoryRemoteAvailabilityLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:           100,
		Height:          30,
		ResumeCWD:       "ask",
		RemoteAppServer: true,
	})
	model.openConfigPanel()
	model.modal.configPanel.section = configSectionSessions
	var resumeRow configPanelRow
	for _, row := range model.configPanelFilteredRows() {
		if row.action == configActionResumeCWD {
			resumeRow = row
		}
	}
	model.activateConfigRow(resumeRow)
	if model.resumeCWD != "session" {
		t.Fatalf("remote Ask cycle chose %q, want Session", model.resumeCWD)
	}
	model.resumeCWD = "current"
	if value := model.configPanelValue(resumeRow); !strings.Contains(value, "Requires --cd for remote sessions") {
		t.Fatalf("remote Current value = %q", value)
	}
	model.remoteCWDOverride = "/remote/worktree"
	model.resumeCWD = "ask"
	model.activateConfigRow(resumeRow)
	if model.resumeCWD != "current" {
		t.Fatalf("remote Ask with --cd chose %q, want Current", model.resumeCWD)
	}
}
