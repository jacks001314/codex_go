package tea

// Rust parity: codex-rs/tui/src/app/session_lifecycle.rs
// `start_statusline_authoring` (#62037). The item editor can open a fresh
// conversation with an editable draft that binds the bundled
// `create-statusline` skill when the system skill is available.

import (
	"strings"

	"codex_go/appserver"
	"codex_go/tui/chatwidget"

	bubbletea "charm.land/bubbletea/v2"
)

const (
	statuslineCreateCustomActionID = "create-custom-statusline"
	createStatuslineSkillName      = "create-statusline"
	statuslineAuthoringDraft       = "Help me build and enable a custom statusline."
	statuslineAuthoringSkillDraft  = "Use $create-statusline to help me build and enable a custom statusline."
)

// statuslineAuthoringSkillsMsg carries the skill inventory read after the
// fresh conversation was opened so the draft can bind the bundled skill.
type statuslineAuthoringSkillsMsg struct {
	ThreadID string
	CWD      string
	Response appserver.SkillsListResponse
	Err      error
}

// statuslineCreateCustomEnabled mirrors Rust StatuslineProviderActions: the
// create action exists only when status line extensions are enabled and the
// conversation is not hosted in cloud mode.
func (m *Model) statuslineCreateCustomEnabled() bool {
	if m == nil || !m.statuslineProviderPickerEnabled() {
		return false
	}
	return !m.threadLabelCloud(m.currentThreadID(), m.backendID)
}

// startStatuslineAuthoring opens a fresh conversation and restores an editable
// draft bound to the bundled create-statusline skill when it is available,
// falling back to a plain request otherwise.
func (m *Model) startStatuslineAuthoring() bubbletea.Cmd {
	if m == nil || m.State == nil || !m.statuslineCreateCustomEnabled() {
		return nil
	}
	// Rust #62037 preserves the previous thread's draft while switching to the
	// authoring conversation, so returning to it restores what was composed.
	if current := strings.TrimSpace(m.currentThreadID()); current != "" {
		m.captureAgentsOverviewDraft(current)
	}
	m.modal = nil
	m.startFreshNamedSession("", "")
	// Rust #62037 suppresses the held activation key so the key that opened the
	// conversation cannot immediately submit the restored draft.
	m.suppressCurrentViewKeyUntilRelease()
	// The fresh conversation has no app-server thread yet, so the empty thread
	// id is the identity an in-flight skill read must still match.
	threadID := strings.TrimSpace(m.State.ThreadID)
	cwd := strings.TrimSpace(m.sessionCWD)
	if reader := m.onReadSkills; reader != nil {
		return func() bubbletea.Msg {
			response, err := reader(cwd, false)
			return statuslineAuthoringSkillsMsg{ThreadID: threadID, CWD: cwd, Response: response, Err: err}
		}
	}
	m.restoreStatuslineAuthoringDraft(cwd, m.skillsInventory)
	return nil
}

// applyStatuslineAuthoringSkills drops a skill listing that arrived after the
// conversation changed, then restores the draft.
func (m *Model) applyStatuslineAuthoringSkills(message statuslineAuthoringSkillsMsg) {
	if m == nil || m.State == nil {
		return
	}
	if strings.TrimSpace(m.State.ThreadID) != message.ThreadID {
		return
	}
	cwd := strings.TrimSpace(message.CWD)
	if sessionCWD := strings.TrimSpace(m.sessionCWD); cwd != "" && sessionCWD != "" && cwd != sessionCWD {
		return
	}
	var response *appserver.SkillsListResponse
	if message.Err == nil {
		response = &message.Response
	}
	m.restoreStatuslineAuthoringDraft(cwd, response)
}

func (m *Model) restoreStatuslineAuthoringDraft(cwd string, response *appserver.SkillsListResponse) {
	if m == nil {
		return
	}
	text := statuslineAuthoringDraft
	binding := ""
	if path := createStatuslineSkillPath(response, cwd); path != "" {
		text = statuslineAuthoringSkillDraft
		binding = skillPopupMentionBinding(skillPopupItem{Name: createStatuslineSkillName, Path: path})
	}
	m.setComposerPrompt(text)
	m.composer.SetCursorColumn(len(m.composer.Value()))
	m.composerMentionBindings = nil
	if binding != "" {
		m.addComposerMentionBinding(binding)
	}
	m.slashPopup = slashCommandPopup{}
	m.skillPopup = skillPopupState{}
}

// createStatuslineSkillPath resolves Rust's bundled-skill lookup: the enabled
// system scoped `create-statusline` skill for the conversation's cwd.
func createStatuslineSkillPath(response *appserver.SkillsListResponse, cwd string) string {
	if response == nil {
		return ""
	}
	skills := chatwidget.SkillsForCWD(cwd, response)
	if len(skills) == 0 {
		skills = response.Skills
	}
	for _, skill := range skills {
		if !skill.Enabled || strings.TrimSpace(skill.Name) != createStatuslineSkillName {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(skill.Scope), "system") {
			continue
		}
		return strings.TrimSpace(string(skill.Path))
	}
	return ""
}
