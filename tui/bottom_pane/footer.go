package bottompane

import (
	"strings"

	"codex_go/tui"
	tuistatus "codex_go/tui/status"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/footer.rs.

const (
	FooterModeCycleHint   = "shift+tab to cycle"
	FooterContextGapCols  = 1
	FooterContextJoiner   = " \u00b7 "
	DefaultQuitShortcut   = "Ctrl+C"
	DefaultQueueShortcut  = "Tab"
	DefaultToggleShortcut = "?"
)

type FooterState struct {
	Left  string
	Right string
}

func (s FooterState) Empty() bool {
	return s.Left == "" && s.Right == ""
}

type FooterMode string

const (
	FooterModeHistorySearch        FooterMode = "history_search"
	FooterModeQuitShortcutReminder FooterMode = "quit_shortcut_reminder"
	FooterModeShortcutOverlay      FooterMode = "shortcut_overlay"
	FooterModeEscHint              FooterMode = "esc_hint"
	FooterModeComposerEmpty        FooterMode = "composer_empty"
	FooterModeComposerHasDraft     FooterMode = "composer_has_draft"
)

type CollaborationModeIndicator string

const (
	CollaborationModePlan            CollaborationModeIndicator = "plan"
	CollaborationModePairProgramming CollaborationModeIndicator = "pair_programming"
	CollaborationModeExecute         CollaborationModeIndicator = "execute"
)

type GoalStatusIndicator string

const (
	GoalStatusActive        GoalStatusIndicator = "active"
	GoalStatusPaused        GoalStatusIndicator = "paused"
	GoalStatusBlocked       GoalStatusIndicator = "blocked"
	GoalStatusUsageLimited  GoalStatusIndicator = "usage_limited"
	GoalStatusBudgetLimited GoalStatusIndicator = "budget_limited"
	GoalStatusComplete      GoalStatusIndicator = "complete"
)

type FooterGoalStatusIndicator struct {
	Kind     GoalStatusIndicator
	Usage    string
	HasUsage bool
}

type FooterKeyHints struct {
	ToggleShortcuts string
	Queue           string
	InsertNewline   string
	ExternalEditor  string
	EditPrevious    string
	ShowTranscript  string
	HistorySearch   string
	ReasoningDown   string
	ReasoningUp     string
}

func DefaultFooterKeyHints() FooterKeyHints {
	return FooterKeyHints{
		ToggleShortcuts: DefaultToggleShortcut,
		Queue:           DefaultQueueShortcut,
		InsertNewline:   "Ctrl+J",
		ExternalEditor:  "Ctrl+G",
		EditPrevious:    "Esc",
		ShowTranscript:  "Ctrl+T",
		HistorySearch:   "Ctrl+R",
		ReasoningDown:   "Alt+,",
		ReasoningUp:     "Alt+.",
	}
}

type FooterProps struct {
	Mode                       FooterMode
	EscBacktrackHint           bool
	UseShiftEnterHint          bool
	IsTaskRunning              bool
	QueueSubmissions           bool
	CollaborationModesEnabled  bool
	IsWSL                      bool
	QuitShortcutKey            string
	StatusLineValue            string
	StatusLineEnabled          bool
	KeyHints                   FooterKeyHints
	ActiveAgentLabel           string
	CollaborationModeIndicator CollaborationModeIndicator
	ShowCycleHint              bool
}

func (p FooterProps) normalized() FooterProps {
	if p.Mode == "" {
		p.Mode = FooterModeComposerEmpty
	}
	if p.QuitShortcutKey == "" {
		p.QuitShortcutKey = DefaultQuitShortcut
	}
	if p.KeyHints == (FooterKeyHints{}) {
		p.KeyHints = DefaultFooterKeyHints()
	}
	return p
}

func ToggleShortcutMode(current FooterMode, ctrlCHint bool, isEmpty bool) FooterMode {
	if ctrlCHint && current == FooterModeQuitShortcutReminder {
		return current
	}
	base := FooterModeComposerHasDraft
	if isEmpty {
		base = FooterModeComposerEmpty
	}
	switch current {
	case FooterModeShortcutOverlay, FooterModeQuitShortcutReminder:
		return base
	default:
		return FooterModeShortcutOverlay
	}
}

func EscHintMode(current FooterMode, isTaskRunning bool) FooterMode {
	if isTaskRunning {
		return current
	}
	return FooterModeEscHint
}

func ResetFooterModeAfterActivity(current FooterMode) FooterMode {
	switch current {
	case FooterModeEscHint, FooterModeShortcutOverlay, FooterModeQuitShortcutReminder, FooterModeHistorySearch, FooterModeComposerHasDraft:
		return FooterModeComposerEmpty
	default:
		return current
	}
}

func FooterHeight(props FooterProps) int {
	return len(FooterLines(props))
}

func FooterLines(props FooterProps) []string {
	props = props.normalized()
	showShortcuts := props.Mode == FooterModeComposerEmpty
	showQueue := props.Mode == FooterModeComposerHasDraft && props.IsTaskRunning
	return footerLinesFromProps(props, showShortcuts, showQueue)
}

func footerLinesFromProps(props FooterProps, showShortcuts bool, showQueue bool) []string {
	if line := PassiveFooterStatusLine(props); line != "" {
		return []string{line}
	}
	switch props.Mode {
	case FooterModeHistorySearch:
		return []string{"reverse-i-search: "}
	case FooterModeQuitShortcutReminder:
		return []string{props.QuitShortcutKey + " again to quit"}
	case FooterModeShortcutOverlay:
		return FooterShortcutOverlayLines(props)
	case FooterModeEscHint:
		return []string{EscHintLine(props.EscBacktrackHint)}
	case FooterModeComposerHasDraft:
		if showQueue {
			return []string{props.KeyHints.Queue + " to queue message"}
		}
		return []string{CollaborationModeLabel(props.CollaborationModeIndicator, props.ShowCycleHint)}
	default:
		left := ""
		if showShortcuts && props.KeyHints.ToggleShortcuts != "" {
			left = props.KeyHints.ToggleShortcuts + " for shortcuts"
		}
		mode := CollaborationModeLabel(props.CollaborationModeIndicator, props.ShowCycleHint)
		if mode != "" {
			left = joinFooterParts(left, mode)
		}
		context := FooterContextLine(props)
		return []string{joinFooterContext(left, context)}
	}
}

func FooterShortcutOverlayLines(props FooterProps) []string {
	props = props.normalized()
	newlineKey := props.KeyHints.InsertNewline
	if props.UseShiftEnterHint {
		newlineKey = "Shift+Enter"
	}
	pasteImageKey := "Ctrl+V"
	if props.IsWSL {
		pasteImageKey = "Ctrl+Alt+V"
	}
	queueAction := "to submit message"
	if props.IsTaskRunning || props.QueueSubmissions {
		queueAction = "to queue message"
	}
	editPrevious := props.KeyHints.EditPrevious + " " + props.KeyHints.EditPrevious + " to edit previous message"
	if props.EscBacktrackHint {
		editPrevious = props.KeyHints.EditPrevious + " again to edit previous message"
	}
	quitAction := "to exit"
	if props.IsTaskRunning {
		quitAction = "to interrupt"
	}
	lines := []string{
		"/ for commands",
		"! for shell commands",
		newlineKey + " for newline",
		props.KeyHints.Queue + " " + queueAction,
		"@ for file paths",
		pasteImageKey + " to paste images",
		props.KeyHints.ExternalEditor + " to edit in external editor",
		editPrevious,
		props.KeyHints.HistorySearch + " search history",
		props.QuitShortcutKey + " " + quitAction,
		props.KeyHints.ReasoningDown + " reasoning down",
		props.KeyHints.ReasoningUp + " reasoning up",
	}
	if props.CollaborationModesEnabled {
		lines = append(lines, "Shift+Tab to change mode")
	}
	lines = append(lines, props.KeyHints.ShowTranscript+" to view transcript")
	lines = append(lines, "")
	lines = append(lines, "customize shortcuts with /keymap")
	return lines
}

func FooterContextLine(props FooterProps) string {
	return PassiveFooterStatusLine(props)
}

func PassiveFooterStatusLine(props FooterProps) string {
	if !ShowsPassiveFooterLine(props) {
		return ""
	}
	parts := []string{}
	if props.StatusLineEnabled && props.StatusLineValue != "" {
		parts = append(parts, props.StatusLineValue)
	}
	if props.ActiveAgentLabel != "" {
		parts = append(parts, props.ActiveAgentLabel)
	}
	return strings.Join(parts, FooterContextJoiner)
}

func ShowsPassiveFooterLine(props FooterProps) bool {
	switch props.Mode {
	case FooterModeComposerEmpty:
		return true
	case FooterModeComposerHasDraft:
		return !props.IsTaskRunning
	default:
		return false
	}
}

func UsesPassiveFooterStatusLayout(props FooterProps) bool {
	return props.StatusLineEnabled && ShowsPassiveFooterLine(props)
}

func EscHintLine(escBacktrackHint bool) string {
	if escBacktrackHint {
		return "Esc again to edit previous message"
	}
	return "Esc Esc to edit previous message"
}

func GoalStatusIndicatorLine(indicator *FooterGoalStatusIndicator) (string, bool) {
	if indicator == nil {
		return "", false
	}
	switch indicator.Kind {
	case GoalStatusActive:
		if indicator.HasUsage {
			return "Pursuing goal (" + indicator.Usage + ")", true
		}
		return "Pursuing goal", true
	case GoalStatusPaused:
		return "Goal paused (/goal resume)", true
	case GoalStatusBlocked:
		return "Goal blocked (/goal resume)", true
	case GoalStatusUsageLimited:
		return "Goal hit usage limits (/goal resume)", true
	case GoalStatusBudgetLimited:
		if indicator.HasUsage {
			return "Goal unmet (" + indicator.Usage + ")", true
		}
		return "Goal abandoned", true
	case GoalStatusComplete:
		if indicator.HasUsage {
			return "Goal achieved (" + indicator.Usage + ")", true
		}
		return "Goal achieved", true
	default:
		return "", false
	}
}

func StatusLineRightIndicatorLine(mode CollaborationModeIndicator, goal *FooterGoalStatusIndicator, ideContextActive bool, showCycleHint bool) string {
	parts := []string{}
	if modeLabel := CollaborationModeLabel(mode, showCycleHint); modeLabel != "" {
		parts = append(parts, modeLabel)
	} else if goalLabel, ok := GoalStatusIndicatorLine(goal); ok {
		parts = append(parts, goalLabel)
	}
	if ideContextActive {
		parts = append(parts, "IDE context")
	}
	return strings.Join(parts, FooterContextJoiner)
}

func SideConversationContextLine(label string) string {
	return label
}

func ContextWindowLine(percent *int64, usedTokens *int64) string {
	if percent != nil {
		value := *percent
		if value < 0 {
			value = 0
		}
		if value > 100 {
			value = 100
		}
		return tui.FormatInt(value) + "% context left"
	}
	if usedTokens != nil {
		return tuistatus.FormatTokensCompact(*usedTokens) + " used"
	}
	return "100% context left"
}

func CollaborationModeLabel(mode CollaborationModeIndicator, showCycleHint bool) string {
	suffix := ""
	if showCycleHint {
		suffix = " (" + FooterModeCycleHint + ")"
	}
	switch mode {
	case CollaborationModePlan:
		return "Plan mode" + suffix
	case CollaborationModePairProgramming:
		return "Pair Programming mode" + suffix
	case CollaborationModeExecute:
		return "Execute mode" + suffix
	default:
		return ""
	}
}

type SummaryLeftKind string

const (
	SummaryLeftDefault SummaryLeftKind = "default"
	SummaryLeftCustom  SummaryLeftKind = "custom"
	SummaryLeftNone    SummaryLeftKind = "none"
)

type SingleLineFooterLayout struct {
	LeftKind    SummaryLeftKind
	Left        string
	ShowContext bool
}

func ComputeSingleLineFooterLayout(width int, contextWidth int, props FooterProps, showShortcutsHint bool, showQueueHint bool) SingleLineFooterLayout {
	props = props.normalized()
	defaultLeft := footerSummaryLeft(props, showShortcutsHint, showQueueHint, true)
	if footerFitsWithContext(width, defaultLeft, contextWidth) {
		return SingleLineFooterLayout{LeftKind: SummaryLeftDefault, Left: defaultLeft, ShowContext: true}
	}
	if showQueueHint {
		for _, left := range []string{
			footerSummaryLeft(props, false, true, false),
			props.KeyHints.Queue + " to queue",
		} {
			if footerFitsWithContext(width, left, contextWidth) {
				return SingleLineFooterLayout{LeftKind: SummaryLeftCustom, Left: left, ShowContext: true}
			}
			if footerLeftFits(width, left) {
				return SingleLineFooterLayout{LeftKind: SummaryLeftCustom, Left: left, ShowContext: false}
			}
		}
	}
	modeWithCycle := CollaborationModeLabel(props.CollaborationModeIndicator, true)
	modeOnly := CollaborationModeLabel(props.CollaborationModeIndicator, false)
	for _, left := range []string{modeWithCycle, modeOnly} {
		if left == "" {
			continue
		}
		if footerFitsWithContext(width, left, contextWidth) {
			return SingleLineFooterLayout{LeftKind: SummaryLeftCustom, Left: left, ShowContext: true}
		}
		if footerLeftFits(width, left) {
			return SingleLineFooterLayout{LeftKind: SummaryLeftCustom, Left: left, ShowContext: false}
		}
	}
	if contextWidth > 0 && contextWidth <= width {
		return SingleLineFooterLayout{LeftKind: SummaryLeftNone, ShowContext: true}
	}
	return SingleLineFooterLayout{LeftKind: SummaryLeftNone}
}

func RenderSingleLineFooter(width int, props FooterProps) string {
	props = props.normalized()
	context := FooterContextLine(props)
	layout := ComputeSingleLineFooterLayout(width, tui.DisplayWidth(context), props, props.Mode == FooterModeComposerEmpty, props.Mode == FooterModeComposerHasDraft && props.IsTaskRunning)
	left := layout.Left
	if layout.LeftKind == SummaryLeftDefault {
		left = footerSummaryLeft(props, props.Mode == FooterModeComposerEmpty, props.Mode == FooterModeComposerHasDraft && props.IsTaskRunning, true)
	}
	if layout.ShowContext && context != "" {
		line := joinFooterContext(left, context)
		if tui.DisplayWidth(line) > width {
			keep := max(width-tui.DisplayWidth(context)-len(FooterContextJoiner), 0)
			left = tui.TruncateWithEllipsis(left, keep)
			line = joinFooterContext(left, context)
		}
		return tui.TruncateWithEllipsis(line, width)
	}
	return tui.TruncateWithEllipsis(left, width)
}

func footerSummaryLeft(props FooterProps, showShortcutsHint bool, showQueueHint bool, showCycleHint bool) string {
	left := ""
	if showQueueHint && props.KeyHints.Queue != "" {
		left = props.KeyHints.Queue + " to queue message"
	} else if showShortcutsHint && props.KeyHints.ToggleShortcuts != "" {
		left = props.KeyHints.ToggleShortcuts + " for shortcuts"
	}
	mode := CollaborationModeLabel(props.CollaborationModeIndicator, showCycleHint && props.ShowCycleHint)
	return joinFooterParts(left, mode)
}

func footerFitsWithContext(width int, left string, contextWidth int) bool {
	if left == "" {
		return contextWidth <= width
	}
	total := tui.DisplayWidth(left)
	if contextWidth > 0 {
		total += len(FooterContextJoiner) + contextWidth
	}
	return total <= width
}

func footerLeftFits(width int, left string) bool {
	return left != "" && tui.DisplayWidth(left) <= width
}

func joinFooterParts(a string, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + FooterContextJoiner + b
}

func joinFooterContext(left string, context string) string {
	return joinFooterParts(left, context)
}
