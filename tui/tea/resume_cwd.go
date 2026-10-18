package tea

import (
	"path/filepath"
	"runtime"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

const resumeCWDPromptID = "resume-cwd-prompt"

type pendingResumeCWD struct {
	threadID    string
	response    SessionResumeResponse
	selection   *codextui.SessionSelection
	modalID     string
	optionID    string
	optionLabel string
	currentCWD  string
	sessionCWD  string
}

// resolveResumeSelectionCWD applies `tui.resume_cwd` before the resume request.
// Rust resolves the directory before rebuilding config and calling
// thread/resume, so an Ask choice can change the server-side cwd instead of
// only relabeling an already-resumed thread.
func (m *Model) resolveResumeSelectionCWD(selection codextui.SessionSelection) (codextui.SessionSelection, bool, string) {
	if m == nil || strings.TrimSpace(selection.ResumeCWD) != "" {
		return selection, false, ""
	}
	currentCWD := strings.TrimSpace(m.sessionCWD)
	sessionCWD := strings.TrimSpace(selection.Target.CWD)
	if m.remoteAppServer {
		// Rust makes --cd the effective Current mode for a remote workspace and
		// otherwise leaves the server-owned saved directory alone.
		if override := strings.TrimSpace(m.remoteCWDOverride); override != "" {
			selection.ResumeCWD = override
			return selection, false, ""
		}
		if normalizeResumeCWD(m.resumeCWD) == "current" {
			return selection, false, "`tui.resume_cwd = \"current\"` requires `--cd` when using a remote workspace"
		}
		return selection, false, ""
	}
	switch normalizeResumeCWD(m.resumeCWD) {
	case "current":
		selection.ResumeCWD = firstNonEmpty(currentCWD, sessionCWD)
	case "session":
		if sessionCWD == "" {
			return selection, false, "failed to determine the working directory recorded for the selected session"
		}
		selection.ResumeCWD = sessionCWD
	default:
		if sessionCWD != "" && currentCWD != "" && !sameResumeCWD(currentCWD, sessionCWD) {
			return selection, true, ""
		}
		selection.ResumeCWD = firstNonEmpty(sessionCWD, currentCWD)
	}
	return selection, false, ""
}

// openResumeCWDPromptForSelection asks for the directory before resuming.
func (m *Model) openResumeCWDPromptForSelection(selection codextui.SessionSelection) {
	if m == nil {
		return
	}
	currentCWD := strings.TrimSpace(m.sessionCWD)
	sessionCWD := strings.TrimSpace(selection.Target.CWD)
	modalID := "session-picker-resume"
	optionID := firstNonEmpty(strings.TrimSpace(selection.Target.ThreadID), strings.TrimSpace(selection.Target.Path))
	optionLabel := ""
	if m.modal != nil {
		if strings.TrimSpace(m.modal.id) != "" {
			modalID = strings.TrimSpace(m.modal.id)
		}
		if m.modal.sessionPicker != nil {
			if item, ok := m.modal.sessionPicker.SelectedItem(); ok {
				optionID = firstNonEmpty(item.ThreadID, item.Path)
				optionLabel = item.DisplayTitle()
			}
		}
	}
	if optionLabel == "" {
		for _, item := range m.sessionItems {
			if strings.TrimSpace(item.ThreadID) == strings.TrimSpace(selection.Target.ThreadID) &&
				strings.TrimSpace(item.Path) == strings.TrimSpace(selection.Target.Path) {
				optionLabel = item.DisplayTitle()
				break
			}
		}
	}
	if optionLabel == "" {
		optionLabel = selection.Target.DisplayLabel()
	}
	copied := selection
	m.pendingResumeCWD = &pendingResumeCWD{
		selection:   &copied,
		modalID:     modalID,
		optionID:    optionID,
		optionLabel: optionLabel,
		currentCWD:  currentCWD,
		sessionCWD:  sessionCWD,
	}
	m.openResumeCWDPromptModal(currentCWD, sessionCWD)
}

// openResumeCWDPrompt keeps the post-resume fallback for summaries that arrive
// without a selection-time cwd.
func (m *Model) openResumeCWDPrompt(threadID string, response SessionResumeResponse, currentCWD string, sessionCWD string) {
	if m == nil {
		return
	}
	m.pendingResumeCWD = &pendingResumeCWD{
		threadID:   threadID,
		response:   response,
		currentCWD: currentCWD,
		sessionCWD: sessionCWD,
	}
	m.openResumeCWDPromptModal(currentCWD, sessionCWD)
}

func (m *Model) openResumeCWDPromptModal(currentCWD string, sessionCWD string) {
	options := []ModalOption{
		{ID: "session", Label: "Use session directory (" + sessionCWD + ")"},
		{ID: "current", Label: "Use current directory (" + currentCWD + ")"},
		{ID: "always_session", Label: "Always use session directory"},
		{ID: "always_current", Label: "Always use current directory"},
	}
	m.openModal(ModalRequestMsg{
		ID:         resumeCWDPromptID,
		Kind:       ModalKindGeneric,
		Title:      "Working directory · resume",
		Body:       "Session = latest cwd recorded in the resumed session\nCurrent = your current working directory",
		Options:    options,
		FooterHint: "enter continue · esc use session",
	})
}

func (m *Model) finishResumeCWDPrompt(response ModalResponse) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	pending := m.pendingResumeCWD
	m.pendingResumeCWD = nil
	m.modal = nil
	if pending == nil {
		return nil
	}
	option := strings.TrimSpace(response.OptionID)
	if response.Cancelled || option == "" {
		option = "session"
	}
	cwd := pending.sessionCWD
	mode := ""
	switch option {
	case "current", "always_current":
		cwd = pending.currentCWD
		mode = "current"
	case "always_session":
		mode = "session"
	}
	var cmd bubbletea.Cmd
	if mode != "" {
		m.resumeCWD = mode
		cmd = m.saveConfigPanelPreference("tui.resume_cwd", mode)
	}
	if pending.selection != nil {
		selection := *pending.selection
		selection.ResumeCWD = cwd
		decision, notice, _ := m.applySessionSelection(selection)
		if strings.TrimSpace(notice) != "" {
			m.notice = strings.TrimSpace(notice)
		} else {
			m.notice = strings.TrimSpace(pending.optionLabel)
		}
		var callback bubbletea.Cmd
		if m.onModalResponse != nil {
			callback = m.onModalResponse(ModalResponse{
				ID:          pending.modalID,
				Kind:        ModalKindPicker,
				OptionID:    pending.optionID,
				OptionLabel: pending.optionLabel,
				Picker:      decision,
			})
		}
		return bubbletea.Sequence(cmd, callback)
	}
	m.applyResumeResponseWithCWD(pending.threadID, pending.response, cwd)
	return cmd
}

func sameResumeCWD(a string, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
