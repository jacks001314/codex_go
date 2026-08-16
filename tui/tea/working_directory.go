package tea

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// Working-directory slash commands (Rust #38894): /cd [path] changes an idle
// local session's working directory while preserving conversation history,
// and /pwd (alias /cwd) displays the current working directory.

// canChangeWorkingDirectory mirrors Rust ChatWidget::can_change_working_directory:
// the active primary local session must be idle with no queued input, no side
// conversation, no running background terminals, and no pending steers.
func (m *Model) canChangeWorkingDirectory(threadID string) bool {
	if m == nil || m.State == nil || m.onWorkingDirectoryChange == nil {
		return false
	}
	if strings.TrimSpace(m.State.ThreadID) == "" || m.State.ThreadID != threadID {
		return false
	}
	if m.inSideConversation() {
		return false
	}
	if len(m.backgroundProcesses) > 0 {
		return false
	}
	if m.isUserTurnPendingOrRunning() || len(m.queued) > 0 || len(m.pendingSteers) > 0 {
		return false
	}
	if m.reviewState.IsReviewMode {
		return false
	}
	return true
}

// applyWorkingDirectoryDisplayCommand mirrors Rust SlashCommand::Pwd: show the
// current working directory as an info message (also reachable via /cwd).
func (m *Model) applyWorkingDirectoryDisplayCommand() {
	if m == nil || m.State == nil {
		return
	}
	cwd := strings.TrimSpace(m.State.CWD)
	if cwd == "" {
		cwd = "."
	}
	m.applyHistoryCell(historycell.NewInfoEvent("Current working directory: "+cwd, ""))
	m.refreshTranscript()
}

// applyWorkingDirectoryChangeCommand mirrors Rust SlashCommand::Cd: default the
// target to the home directory, validate eligibility, then request the change.
func (m *Model) applyWorkingDirectoryChangeCommand(args string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	target := strings.TrimSpace(args)
	if target == "" {
		target = "~"
	}
	if !m.canChangeWorkingDirectory(threadID) {
		m.addErrorHistoryMessage("Changing directories requires an idle primary session without queued input.")
		m.refreshTranscript()
		return nil
	}
	return m.requestWorkingDirectoryChange(threadID, target)
}

func (m *Model) requestWorkingDirectoryChange(threadID string, target string) bubbletea.Cmd {
	return func() bubbletea.Msg {
		resolved, err := resolveWorkingDirectory(target, strings.TrimSpace(m.State.CWD))
		if err != nil {
			return WorkingDirectoryChangeResultMsg{ThreadID: threadID, Error: err.Error()}
		}
		if m.onWorkingDirectoryChange == nil {
			return WorkingDirectoryChangeResultMsg{ThreadID: threadID, CWD: resolved, Error: "working directory change is unavailable"}
		}
		summary, err := m.onWorkingDirectoryChange(threadID, resolved)
		if err != nil {
			return WorkingDirectoryChangeResultMsg{ThreadID: threadID, CWD: resolved, Error: err.Error()}
		}
		return WorkingDirectoryChangeResultMsg{ThreadID: threadID, CWD: resolved, Summary: summary}
	}
}

// applyWorkingDirectoryChangeResult attaches the replacement session returned
// by the backend, mirroring Rust change_working_directory's attach step:
// swap the thread id, cwd, and transcript to the new session while keeping the
// conversation history the fork preserved.
func (m *Model) applyWorkingDirectoryChangeResult(msg WorkingDirectoryChangeResultMsg) {
	if m == nil || m.State == nil {
		return
	}
	if strings.TrimSpace(msg.Error) != "" {
		m.addErrorHistoryMessage("Failed to change directory: " + strings.TrimSpace(msg.Error))
		m.refreshTranscript()
		return
	}
	if msg.Summary == nil || strings.TrimSpace(msg.Summary.ThreadID) == "" {
		m.addErrorHistoryMessage("Failed to change directory: no replacement session")
		m.refreshTranscript()
		return
	}
	m.invalidateAppsScope()
	m.resetAgentPickerRefresh(true)
	m.State.SetThreadID(strings.TrimSpace(msg.Summary.ThreadID))
	if title := strings.TrimSpace(msg.Summary.Title); title != "" {
		m.State.SetThreadName(title)
	}
	if cwd := strings.TrimSpace(msg.Summary.CWD); cwd != "" {
		m.State.CWD = cwd
		m.sessionCWD = cwd
	}
	m.activeSide = nil
	m.activeAgentLabel = ""
	if m.statusControls != nil {
		m.statusControls.SetActiveAgentLabel("", false)
	}
	m.setStatus("idle")
	m.applyHistoryCell(historycell.NewInfoEvent("Working directory changed to: "+msg.CWD, ""))
	m.refreshTranscript()
	m.transcript.GotoBottom()
}

// WorkingDirectoryChangeResultMsg carries the resolved cwd and, once the
// backend completes the fork, the replacement session summary.
type WorkingDirectoryChangeResultMsg struct {
	ThreadID string
	CWD      string
	Summary  *codextui.SessionSummary
	Error    string
}

func resolveWorkingDirectory(path string, base string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" {
		if home, err := userHomeDir(); err == nil {
			path = home
		}
	} else if strings.HasPrefix(path, "~/") {
		if home, err := userHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	if !filepath.IsAbs(path) {
		if base == "" {
			base = "."
		}
		path = filepath.Join(base, path)
	}
	resolved := filepath.Clean(path)
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", os.ErrInvalid
	}
	return resolved, nil
}

func userHomeDir() (string, error) {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return home, nil
	}
	if current, err := user.Current(); err == nil && current != nil && strings.TrimSpace(current.HomeDir) != "" {
		return current.HomeDir, nil
	}
	return "", os.ErrNotExist
}
