package tea

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/tui/styles"
)

// StatusBarComponent manages the status bar, footer, working indicator,
// MCP startup display, terminal title, and status controls.
type StatusBarComponent struct {
	statusStyle      lipgloss.Style
	footerStyle      lipgloss.Style
	bottomStyle      lipgloss.Style
	statusControls   *chatwidget.StatusControlsState
	statusConfigured bool
	bottomLines      []string
	notice           string
	taskStartedAt    time.Time

	// MCP startup state
	mcpStartup              chatwidget.McpStartupRoundState
	mcpStartupHeader        string
	mcpStartupActive        bool
	mcpStartupGeneration    uint64
	mcpStartupFinishPending bool

	// Terminal title
	lastTerminalTitleSequence string
}

// newStatusBarComponent initializes the status bar sub-component.
func newStatusBarComponent() StatusBarComponent {
	return StatusBarComponent{
		statusStyle: lipgloss.NewStyle().Bold(true),
		footerStyle: lipgloss.NewStyle().Foreground(styles.LipglossColor(styles.ColorDim)),
		bottomStyle: lipgloss.NewStyle(),
	}
}

// SetNotice sets a temporary notice message shown above the composer.
func (s *StatusBarComponent) SetNotice(text string) {
	if s != nil {
		s.notice = text
	}
}

// Notice returns the current notice text.
func (s *StatusBarComponent) Notice() string {
	if s == nil {
		return ""
	}
	return s.notice
}

// AddBottomLines appends lines to the bottom pane buffer.
func (s *StatusBarComponent) AddBottomLines(lines []string) {
	if s != nil {
		s.bottomLines = append(s.bottomLines, lines...)
	}
}

// AddBottomLine appends a single line to the bottom pane buffer.
func (s *StatusBarComponent) AddBottomLine(line string) {
	if s != nil {
		s.bottomLines = append(s.bottomLines, line)
	}
}

// ClearBottomLines clears the bottom pane buffer.
func (s *StatusBarComponent) ClearBottomLines() {
	if s != nil {
		s.bottomLines = nil
	}
}

// RenderStatusLine returns a one-line status summary for the footer.
func (s *StatusBarComponent) RenderStatusLine(state *codextui.State) string {
	if state == nil {
		return "Thread: new | Status: idle | Model: default | Approval: default | Sandbox: default"
	}
	parts := []string{
		"Thread: " + displayValue(state.ThreadID, "new"),
		"Status: " + displayValue(state.Status, "idle"),
		"Model: " + displayValue(state.Model, "default"),
		"Approval: " + displayValue(state.ApprovalPolicy, "default"),
		"Sandbox: " + displayValue(state.Sandbox, "default"),
	}
	if state.PlanMode {
		parts = append(parts, "Mode: Plan")
	}
	return strings.Join(parts, " | ")
}

// SetTaskStarted marks the current time as the task start.
func (s *StatusBarComponent) SetTaskStarted(now time.Time) {
	if s != nil {
		s.taskStartedAt = now
	}
}

// TaskStartedAt returns the task start time.
func (s *StatusBarComponent) TaskStartedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.taskStartedAt
}

// IsTaskRunning returns true if there is an active task.
func (s *StatusBarComponent) IsTaskRunning() bool {
	if s == nil {
		return false
	}
	return !s.taskStartedAt.IsZero()
}

// ClearTask clears the running task state.
func (s *StatusBarComponent) ClearTask() {
	if s != nil {
		s.taskStartedAt = time.Time{}
	}
}

// StatusControls returns the status controls state.
func (s *StatusBarComponent) StatusControls() *chatwidget.StatusControlsState {
	if s == nil {
		return nil
	}
	return s.statusControls
}

// SetStatusControls sets the status controls state.
func (s *StatusBarComponent) SetStatusControls(sc *chatwidget.StatusControlsState) {
	if s != nil {
		s.statusControls = sc
	}
}

// StatusStyle returns the bold status style.
func (s *StatusBarComponent) StatusStyle() lipgloss.Style {
	if s == nil {
		return lipgloss.NewStyle()
	}
	return s.statusStyle
}

// FooterStyle returns the dim footer style.
func (s *StatusBarComponent) FooterStyle() lipgloss.Style {
	if s == nil {
		return lipgloss.NewStyle()
	}
	return s.footerStyle
}

// HasMCPStartup returns whether MCP servers are initializing.
func (s *StatusBarComponent) HasMCPStartup() bool {
	if s == nil {
		return false
	}
	return s.mcpStartupActive
}
