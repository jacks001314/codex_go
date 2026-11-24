package tea

import (
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust parity: codex-rs/tui/src/app/sidebar_document/path_actions.rs (#63388).
//
// An open preview shows its full path in a scrollable view (`p`) and puts that
// path on the clipboard (`c`), including when the read failed and there is no
// body to select. Clipboard status paints in the preview footer, and a copy
// that could not be confirmed reveals the path so the file stays findable.

const (
	// sidebarDocumentPathCopyLabel names the preview's clipboard write so its
	// result lands on the path feedback instead of the transcript notice.
	sidebarDocumentPathCopyLabel = "sidebar document path"
	// sidebarDocumentPathFeedbackWindow is how long a finished copy message
	// stays on the footer (Rust CopyFeedback::Finished expires after 5s).
	sidebarDocumentPathFeedbackWindow = 5 * time.Second
)

// sidebarDocumentPathFeedback is Rust CopyFeedback: a clipboard write that is
// still running, or a finished message with the moment it disappears.
type sidebarDocumentPathFeedback struct {
	pending   bool
	requestID uint64
	reveal    bool
	message   string
	expiresAt time.Time
}

// sidebarDocumentPathFeedbackExpiredMsg repaints the pane once a finished copy
// message has timed out.
type sidebarDocumentPathFeedbackExpiredMsg struct{}

// sidebarDocumentPathActionsAvailable mirrors Rust path_actions_available: the
// path shortcuts work in every preview state except the initial wait, and only
// while the reader is not using a link, a pointer selection, or Find.
func (m *Model) sidebarDocumentPathActionsAvailable(tab *sidebarDocumentTab) bool {
	if m == nil || tab == nil {
		return false
	}
	if !tab.Loading && tab.View.Err == "" {
		return tab.Link < 0 && !tab.Selection.active && !tab.Find.active()
	}
	return true
}

// sidebarDocumentPathText is the path the shortcuts show and copy: the loaded
// absolute path, or the requested one while the read is still running.
func sidebarDocumentPathText(tab *sidebarDocumentTab) string {
	if tab == nil {
		return ""
	}
	if path := strings.TrimSpace(tab.Path); path != "" {
		return path
	}
	return strings.TrimSpace(tab.View.Path)
}

// dismissSidebarDocumentPath closes the path view and stops a running copy from
// reopening it (Rust PathActions::dismiss).
func (m *Model) dismissSidebarDocumentPath(tab *sidebarDocumentTab) {
	if m == nil || tab == nil {
		return
	}
	tab.PathScroll = nil
	if feedback := tab.PathFeedback; feedback != nil && feedback.pending {
		feedback.reveal = false
	}
	m.refreshTranscript()
}

// handleSidebarDocumentPathKey is Rust PathActions::handle_key: `c` copies the
// path and `p` toggles the path view.
func (m *Model) handleSidebarDocumentPathKey(tab *sidebarDocumentTab, msg keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || tab == nil {
		return false, nil
	}
	switch msg.String() {
	case "c":
		return true, m.copySidebarDocumentPath(tab)
	case "p":
		if tab.PathScroll != nil {
			m.dismissSidebarDocumentPath(tab)
			return true, nil
		}
		top := 0
		tab.PathScroll = &top
		m.refreshTranscript()
		return true, nil
	}
	return false, nil
}

// copySidebarDocumentPath puts the preview's full path on the clipboard. The
// write reports back through ClipboardWriteResultMsg, or ClipboardBusyMsg when
// another copy is still running (Rust copy_transcript_selection).
func (m *Model) copySidebarDocumentPath(tab *sidebarDocumentTab) bubbletea.Cmd {
	if m == nil || tab == nil {
		return nil
	}
	path := sidebarDocumentPathText(tab)
	if path == "" {
		return nil
	}
	before := m.pendingClipboardRequest
	cmd := m.clipboardWriteCmd(sidebarDocumentPathCopyLabel, path, false, false)
	feedback := &sidebarDocumentPathFeedback{pending: true, reveal: true}
	if m.clipboardBusy && m.pendingClipboardRequest != before {
		feedback.requestID = m.pendingClipboardRequest
	}
	tab.PathFeedback = feedback
	m.refreshTranscript()
	return cmd
}

// finishSidebarDocumentPathCopy records a finished copy. A failure reveals the
// path unless the user dismissed it while the write ran (Rust copy_result).
func (m *Model) finishSidebarDocumentPathCopy(tab *sidebarDocumentTab, message string, reveal bool) bubbletea.Cmd {
	if m == nil || tab == nil {
		return nil
	}
	previous := tab.PathFeedback
	dismissed := previous != nil && previous.pending && !previous.reveal
	if reveal && !dismissed && tab.PathScroll == nil {
		top := 0
		tab.PathScroll = &top
	}
	tab.PathFeedback = &sidebarDocumentPathFeedback{
		message:   message,
		expiresAt: time.Now().Add(sidebarDocumentPathFeedbackWindow),
	}
	m.refreshTranscript()
	return bubbletea.Tick(sidebarDocumentPathFeedbackWindow, func(time.Time) bubbletea.Msg {
		return sidebarDocumentPathFeedbackExpiredMsg{}
	})
}

// applySidebarDocumentPathCopyResult routes a clipboard result onto the tab
// that asked for the path copy.
func (m *Model) applySidebarDocumentPathCopyResult(msg ClipboardWriteResultMsg) (bool, bubbletea.Cmd) {
	if msg.Label != sidebarDocumentPathCopyLabel {
		return false, nil
	}
	if m != nil {
		for _, tab := range m.sidebarDocumentTabs() {
			feedback := tab.PathFeedback
			if feedback == nil || !feedback.pending {
				continue
			}
			if feedback.requestID != 0 && msg.RequestID != 0 && feedback.requestID != msg.RequestID {
				continue
			}
			if msg.Err != nil {
				return true, m.finishSidebarDocumentPathCopy(tab, "Could not copy path", true)
			}
			return true, m.finishSidebarDocumentPathCopy(tab, "Path copied", false)
		}
	}
	return true, nil
}

// applySidebarDocumentPathBusy routes a rejected clipboard write onto the tab
// whose path copy is waiting for it (Rust CopyStatus::Busy).
func (m *Model) applySidebarDocumentPathBusy(label string) (bool, bubbletea.Cmd) {
	if label != sidebarDocumentPathCopyLabel {
		return false, nil
	}
	if m != nil {
		for _, tab := range m.sidebarDocumentTabs() {
			if tab.PathFeedback != nil && tab.PathFeedback.pending {
				return true, m.finishSidebarDocumentPathCopy(tab, "Clipboard busy", true)
			}
		}
	}
	return true, nil
}

// applySidebarDocumentPathFeedbackExpired clears finished copy messages whose
// five seconds are up.
func (m *Model) applySidebarDocumentPathFeedbackExpired() {
	if m == nil {
		return
	}
	now := time.Now()
	changed := false
	for _, tab := range m.sidebarDocumentTabs() {
		feedback := tab.PathFeedback
		if feedback == nil || feedback.pending || feedback.expiresAt.After(now) {
			continue
		}
		tab.PathFeedback = nil
		changed = true
	}
	if changed {
		m.refreshTranscript()
	}
}

// sidebarDocumentTabs lists every open preview in strip order.
func (m *Model) sidebarDocumentTabs() []*sidebarDocumentTab {
	if m == nil {
		return nil
	}
	tabs := make([]*sidebarDocumentTab, 0, len(m.sidebarDocuments))
	for _, parentTabs := range m.sidebarDocuments {
		for _, tab := range parentTabs {
			if tab != nil {
				tabs = append(tabs, tab)
			}
		}
	}
	return tabs
}

// sidebarDocumentPathFeedbackHint is Rust feedback_hint: the transient copy
// status, or empty while nothing is running and once the message expires.
func (m *Model) sidebarDocumentPathFeedbackHint(tab *sidebarDocumentTab) string {
	if tab == nil || tab.PathFeedback == nil {
		return ""
	}
	feedback := tab.PathFeedback
	if feedback.pending {
		return "\x1b[2mCopying path…\x1b[22m"
	}
	if !feedback.expiresAt.After(time.Now()) {
		return ""
	}
	return "\x1b[2m" + feedback.message + "\x1b[22m"
}

// sidebarDocumentPathFooterLine is Rust revealed_footer: the copy status, the
// hide shortcut, and the copy shortcut on one row.
func (m *Model) sidebarDocumentPathFooterLine(width int, tab *sidebarDocumentTab) string {
	esc := codextui.KeybindingDisplayLabel("esc")
	hints := make([]string, 0, 3)
	if feedback := m.sidebarDocumentPathFeedbackHint(tab); feedback != "" {
		hints = append(hints, feedback)
	}
	hints = append(hints, "p/"+esc+" hide path", "c copy path")
	return fitSidebarChooserHints(hints, width)
}

// sidebarDocumentPathFooterRows is Rust footer_lines: the first reader hint
// (close or clear highlight), the path shortcuts, the copy status, then the
// remaining reader hints, wrapped into at most maxRows rows.
func (m *Model) sidebarDocumentPathFooterRows(base []string, width int, maxRows int, tab *sidebarDocumentTab) []string {
	hints := make([]string, 0, len(base)+3)
	if len(base) > 0 {
		hints = append(hints, base[0])
		base = base[1:]
	}
	hints = append(hints, "c copy path", "p show path")
	if feedback := m.sidebarDocumentPathFeedbackHint(tab); feedback != "" {
		hints = append(hints, feedback)
	}
	hints = append(hints, base...)
	return wrapSidebarChooserHintRows(hints, width, maxRows)
}

// sidebarDocumentReaderHints is Rust footer::reader_hints: close or clear
// highlight, read, find, link, and select.
func (m *Model) sidebarDocumentReaderHints(tab *sidebarDocumentTab) []string {
	if m == nil || tab == nil {
		return nil
	}
	hints := make([]string, 0, 5)
	if key := m.sidebarChooserListHint("cancel"); key != "" {
		if tab.Link >= 0 || (tab.View.Target != nil && !tab.TargetCleared) {
			hints = append(hints, key+" clear highlight")
		} else {
			hints = append(hints, key+" close")
		}
	}
	up := m.sidebarChooserListHint("move_up")
	down := m.sidebarChooserListHint("move_down")
	if up != "" && down != "" {
		hints = append(hints, up+"/"+down+" read")
	}
	if label := m.sidebarDocumentKeyHint("pager", "find"); label != "" {
		hints = append(hints, label+" find")
	}
	if len(m.sidebarDocumentLinkTargets(tab)) > 0 {
		hints = append(hints, "tab link")
	}
	if label := m.sidebarDocumentKeyHint("global", "select_transcript"); label != "" {
		hints = append(hints, label+" select")
	}
	return hints
}

// sidebarDocumentKeyHint is one action's first resolved binding, or empty when
// the action is unbound.
func (m *Model) sidebarDocumentKeyHint(context string, action string) string {
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, context, action)
	if len(bindings) == 0 {
		return ""
	}
	label := codextui.KeybindingDisplayLabel(strings.TrimSpace(bindings[0]))
	if label == "" || strings.TrimSpace(bindings[0]) == "" {
		return ""
	}
	return label
}

// sidebarDocumentFooterRows mirrors Rust Document::paint: the reader footer
// reserves only the rows it uses, and a non-Ready pane builds its own base
// hints.
func (m *Model) sidebarDocumentFooterRows(width int, height int, tab *sidebarDocumentTab) []string {
	if m == nil || tab == nil || height <= 1 {
		return nil
	}
	bodyWidth := max(width-2, 1)
	if tab.Find.active() {
		return []string{tab.Find.barLine(bodyWidth)}
	}
	bodyHeight := height - 1
	maxRows := min(max(bodyHeight-1, 0), 2)
	var base []string
	switch {
	case tab.Loading:
		// Rust State::Loading keeps an empty base so the pane can paint the
		// loading glyph and the path shortcuts only.
	case tab.View.Err != "":
		count := len(sidebarDocumentOpenErrorLines(tab, tab.View, bodyWidth))
		maxRows = min(max(bodyHeight-count, 0), 2)
		if count+2 < bodyHeight {
			if key := m.sidebarChooserListHint("cancel"); key != "" {
				base = append(base, key+" close")
			}
		}
	default:
		base = m.sidebarDocumentReaderHints(tab)
	}
	if maxRows == 0 {
		return nil
	}
	if m.sidebarDocumentPathActionsAvailable(tab) && bodyHeight > 2 {
		return m.sidebarDocumentPathFooterRows(base, bodyWidth, maxRows, tab)
	}
	if len(base) == 0 {
		return nil
	}
	return []string{fitSidebarChooserHints(base, bodyWidth)}
}

// renderSidebarDocumentPath paints the full-path view (Rust's PathActions paint
// branch): the strip, the wrapped path under its caption, and the path footer.
func (m *Model) renderSidebarDocumentPath(rows []string, width int, height int, tab *sidebarDocumentTab) string {
	bodyWidth, bodyHeight, top := sidebarFilePathBody(width, height)
	visible := sidebarFilePathVisible(bodyHeight)
	pathRows := wrapSidebarFilePath(sidebarDocumentPathText(tab), bodyWidth)
	offset := 0
	if tab.PathScroll != nil {
		offset = clampViewportOffset(*tab.PathScroll, 0, max(len(pathRows)-visible, 0))
		*tab.PathScroll = offset
	}
	row := top
	if bodyHeight > 2 && row+1 < height-1 {
		rows[row] = " \x1b[1mFile path\x1b[22m"
		rows[row+1] = ""
		row += 2
	}
	for index := offset; index < len(pathRows) && index < offset+visible && row < height-1; index++ {
		rows[row] = " " + codextui.TruncateToWidth(pathRows[index], bodyWidth)
		row++
	}
	rows[height-1] = " " + m.sidebarDocumentPathFooterLine(max(width-2, 1), tab)
	return strings.Join(rows, "\n")
}

// scrollSidebarDocumentPath applies one list action to the open path view
// (Rust PathActions::scroll_path).
func (m *Model) scrollSidebarDocumentPath(tab *sidebarDocumentTab, action string) {
	if m == nil || tab == nil || tab.PathScroll == nil {
		return
	}
	bodyWidth, bodyHeight, _ := sidebarFilePathBody(m.sidebarTabStripWidth(), m.sidebarChooserPanelHeight())
	*tab.PathScroll = scrollSidebarFilePathBody(bodyWidth, bodyHeight, sidebarDocumentPathText(tab), *tab.PathScroll, action)
	m.refreshTranscript()
}

// wrapSidebarChooserHintRows is Rust HintLine::wrapped: whole shortcuts fill a
// row while they fit, the last allowed row absorbs whatever is left, and that
// row is truncated the way a single hint line is.
func wrapSidebarChooserHintRows(hints []string, width int, maxRows int) []string {
	if maxRows <= 0 || len(hints) == 0 {
		return nil
	}
	rows := make([][]string, 0, maxRows)
	current := make([]string, 0, len(hints))
	currentWidth := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		rows = append(rows, current)
		current = make([]string, 0, len(hints))
		currentWidth = 0
	}
	for _, hint := range hints {
		hintWidth := codextui.DisplayWidth(hint)
		separator := 0
		if len(current) > 0 {
			separator = 3
		}
		if len(current) > 0 && currentWidth+separator+hintWidth > width {
			if len(rows)+1 == maxRows {
				current = append(current, hint)
				currentWidth += separator + hintWidth
				continue
			}
			flush()
			separator = 0
		}
		current = append(current, hint)
		currentWidth += separator + hintWidth
	}
	flush()
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, fitSidebarChooserHints(row, width))
	}
	return out
}
