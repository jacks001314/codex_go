package onboarding

// Rust parity: codex-rs/tui/src/daemon_recovery.rs (#55442). The blocking
// screen shown before the TUI opens when the shared background server has
// incompatible feature settings. Restart is managed-only, requires a second
// confirmation, and the caller re-checks compatibility exactly once.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// DaemonRecoveryChoice is the selection the recovery screen returned.
type DaemonRecoveryChoice int

const (
	// DaemonRecoveryRunWithoutDaemon keeps this launch on the embedded server.
	DaemonRecoveryRunWithoutDaemon DaemonRecoveryChoice = iota
	// DaemonRecoveryRestart restarts the managed daemon with the shown settings.
	DaemonRecoveryRestart
	// DaemonRecoveryCancel aborts startup with the compatibility error.
	DaemonRecoveryCancel
)

// DaemonRecoveryOptions describes the incompatibility to report.
type DaemonRecoveryOptions struct {
	Reason string
	// RestartFeatures is nil when a restart cannot resolve the issue.
	RestartFeatures map[string]bool
	// Managed reports whether Codex manages the running background server.
	Managed     bool
	NoAltScreen bool
}

// DaemonRecoveryResult reports how the recovery screen ended.
type DaemonRecoveryResult struct {
	Choice    DaemonRecoveryChoice
	Cancelled bool
}

type daemonRecoveryItem struct {
	label    string
	disabled bool
	reason   string
}

type daemonRecoveryModel struct {
	options  DaemonRecoveryOptions
	items    []daemonRecoveryItem
	selected int
	done     bool
	result   DaemonRecoveryResult
}

// RunDaemonRecovery shows the blocking incompatibility screen.
func RunDaemonRecovery(ctx context.Context, options DaemonRecoveryOptions, input io.Reader, output io.Writer) (DaemonRecoveryResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	model := newDaemonRecoveryModel(options)
	programOptions := []bubbletea.ProgramOption{bubbletea.WithContext(ctx)}
	if input != nil {
		programOptions = append(programOptions, bubbletea.WithInput(input))
	}
	if output != nil {
		programOptions = append(programOptions, bubbletea.WithOutput(output))
	}
	if !options.NoAltScreen {
		programOptions = append(programOptions, bubbletea.WithAltScreen())
	}
	final, err := bubbletea.NewProgram(model, programOptions...).Run()
	if err != nil {
		return DaemonRecoveryResult{}, err
	}
	finished, ok := final.(*daemonRecoveryModel)
	if !ok || finished == nil {
		return DaemonRecoveryResult{}, errors.New("daemon recovery returned an unexpected model")
	}
	return finished.result, nil
}

func newDaemonRecoveryModel(options DaemonRecoveryOptions) *daemonRecoveryModel {
	canRestart := options.Managed && options.RestartFeatures != nil
	restartLabel := "Restart with these settings"
	if canRestart {
		restartLabel = "Restart with these settings (recommended)"
	}
	restartReason := ""
	switch {
	case !options.Managed:
		restartReason = "This server is not managed by Codex."
	case options.RestartFeatures == nil:
		restartReason = "Restart cannot resolve this compatibility check."
	}
	return &daemonRecoveryModel{
		options: options,
		items: []daemonRecoveryItem{
			{label: "Run without daemon this time"},
			{
				label:    restartLabel,
				disabled: !canRestart,
				reason:   restartReason,
			},
			{label: "Cancel"},
		},
		// Rust starts on Cancel so dismissing the screen never restarts a
		// shared server by accident.
		selected: 2,
		result:   DaemonRecoveryResult{Choice: DaemonRecoveryCancel},
	}
}

func (m *daemonRecoveryModel) Init() bubbletea.Cmd { return nil }

func (m *daemonRecoveryModel) Update(message bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch message := message.(type) {
	case bubbletea.KeyMsg:
		switch message.Type {
		case bubbletea.KeyEsc, bubbletea.KeyCtrlC:
			m.finish(DaemonRecoveryResult{Choice: DaemonRecoveryCancel, Cancelled: true})
			return m, bubbletea.Quit
		case bubbletea.KeyUp:
			m.move(-1)
		case bubbletea.KeyDown:
			m.move(1)
		case bubbletea.KeyEnter:
			if m.selectItem(m.selected) {
				return m, bubbletea.Quit
			}
		default:
			if choice, ok := daemonRecoveryDigit(message.String()); ok {
				m.selectShortcut(choice)
			}
		}
	}
	return m, nil
}

// selectItem accepts the row under the cursor and reports whether the screen is
// complete.
func (m *daemonRecoveryModel) selectItem(index int) bool {
	if index < 0 || index >= len(m.items) {
		return false
	}
	if m.items[index].disabled {
		return false
	}
	m.finish(DaemonRecoveryResult{Choice: DaemonRecoveryChoice(index)})
	return true
}

// selectShortcut mirrors Rust's ListSelectionView::select_shortcut: a digit
// moves the highlight, and only the restart row (require_explicit_confirmation)
// stops there instead of accepting the selection immediately.
func (m *daemonRecoveryModel) selectShortcut(index int) {
	if index < 0 || index >= len(m.items) || m.items[index].disabled {
		return
	}
	if index == int(DaemonRecoveryRestart) {
		m.selectRow(index)
		return
	}
	m.finish(DaemonRecoveryResult{Choice: DaemonRecoveryChoice(index)})
}

func (m *daemonRecoveryModel) move(delta int) {
	for {
		next := m.selected + delta
		if next < 0 {
			m.selectRow(0)
			return
		}
		if next >= len(m.items) {
			m.selectRow(len(m.items) - 1)
			return
		}
		if !m.items[next].disabled {
			m.selectRow(next)
			return
		}
		m.selected = next
	}
}

// selectRow moves the highlight.
func (m *daemonRecoveryModel) selectRow(index int) {
	if index < 0 || index >= len(m.items) {
		return
	}
	m.selected = index
}

func (m *daemonRecoveryModel) finish(result DaemonRecoveryResult) {
	m.done = true
	m.result = result
}

func daemonRecoveryDigit(key string) (int, bool) {
	switch key {
	case "1":
		return int(DaemonRecoveryRunWithoutDaemon), true
	case "2":
		return int(DaemonRecoveryRestart), true
	case "3":
		return int(DaemonRecoveryCancel), true
	default:
		return 0, false
	}
}

func (m *daemonRecoveryModel) View() string {
	if m == nil || m.done {
		return ""
	}
	title := "Cannot use the background server"
	if m.options.RestartFeatures != nil {
		title = "Background server has incompatible feature settings"
	}
	lines := []string{title, m.options.Reason}
	if m.options.RestartFeatures != nil {
		lines = append(lines,
			"These settings persist and can disable functionality for other clients. Restart may interrupt active or queued work.",
			"Restart will use these shared feature settings:",
		)
		lines = append(lines, daemonRecoveryFeatureLines(m.options.RestartFeatures)...)
	}
	if reason := m.items[DaemonRecoveryRestart].reason; reason != "" && m.items[DaemonRecoveryRestart].disabled {
		lines = append(lines, reason)
	}
	lines = append(lines, "")
	for index, item := range m.items {
		line := "  " + item.label
		if index == m.selected {
			line = "> " + item.label
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", "up/down select \u00b7 enter confirm \u00b7 esc quit")
	return strings.Join(lines, "\n")
}

// daemonRecoveryFeatureLines mirrors Rust's three-row preview plus the
// "and N more" summary, ordered like the BTreeMap it renders from.
func daemonRecoveryFeatureLines(features map[string]bool) []string {
	names := make([]string, 0, len(features))
	for name := range features {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, 4)
	for index, name := range names {
		if index == 3 {
			break
		}
		lines = append(lines, fmt.Sprintf("  %s = %t", name, features[name]))
	}
	if remaining := len(names) - 3; remaining > 0 {
		lines = append(lines, fmt.Sprintf("  and %d more", remaining))
	}
	return lines
}
