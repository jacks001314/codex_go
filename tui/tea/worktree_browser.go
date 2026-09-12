package tea

import (
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// Managed worktree support mirrors Rust's TUI worktree commands
// (#43120/#43286/#43942): `/worktree` offers starting or continuing a
// conversation in a new managed checkout or browsing the repository's existing
// managed worktrees. Creation and the browser are local-repository only.

// WorktreeBrowserLoadedMsg carries an asynchronous worktree listing back to the
// requesting session; stale requests are discarded.
type WorktreeBrowserLoadedMsg struct {
	Request codextui.WorktreeBrowserRequest
	Entries []codextui.WorktreeBrowserEntry
	Err     error
}

// WorktreeBrowserRemovedMsg carries the result of deleting a managed worktree.
type WorktreeBrowserRemovedMsg struct {
	Root string
	Err  error
}

// worktreeBrowserView is the active step of the worktree popup.
type worktreeBrowserView string

const (
	worktreeViewChooser worktreeBrowserView = "chooser"
	worktreeViewLoading worktreeBrowserView = "loading"
	worktreeViewList    worktreeBrowserView = "list"
	worktreeViewActions worktreeBrowserView = "actions"
	worktreeViewConfirm worktreeBrowserView = "confirm"
)

const (
	managedWorktreeModeNew  = "new"
	managedWorktreeModeFork = "fork"
)

// worktreeBrowserChoice is one row of the `/worktree` chooser.
type worktreeBrowserChoice struct {
	ID          string
	Label       string
	Description string
}

// worktreeBrowserState is the model-side state of the worktree popup.
type worktreeBrowserState struct {
	View     worktreeBrowserView
	Request  codextui.WorktreeBrowserRequest
	Choices  []worktreeBrowserChoice
	Selected int
	Entries  []codextui.WorktreeBrowserEntry
	Filter   string
	Entry    codextui.WorktreeBrowserEntry
	Actions  []codextui.WorktreeBrowserActionItem
	Confirm  []codextui.WorktreeBrowserActionItem
}

// managedWorktreeAvailable reports whether managed-worktree operations can run
// for the active session (Rust chatwidget managed_worktree_available): the
// worktrees feature is enabled, the session performs local worktree operations,
// and the current directory is inside a Git repository.
func (m *Model) managedWorktreeAvailable() bool {
	if m == nil || m.State == nil || !m.worktreesEnabled || !m.localWorktreeOperations {
		return false
	}
	cwd := strings.TrimSpace(m.State.CWD)
	if m.worktreeRepoCheck != nil {
		return m.worktreeRepoCheck(cwd)
	}
	return codextui.ManagedWorktreeRepositoryAvailable(cwd)
}

// applyWorktreeCommand mirrors Rust's `/worktree` command: unavailable
// worktree operations make the command unrecognized, a non-repository directory
// reports the managed-worktree prerequisite, and otherwise the chooser opens.
func (m *Model) applyWorktreeCommand(args string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if !m.worktreesEnabled {
		m.addErrorHistoryMessage("Enable worktrees in your Codex configuration to create a worktree.")
		return nil
	}
	if !m.localWorktreeOperations {
		m.notice = "Unrecognized command '/worktree'"
		m.refreshTranscript()
		return nil
	}
	if !m.managedWorktreeAvailable() {
		m.addErrorHistoryMessage("Managed worktrees require a local Git repository.")
		return nil
	}
	m.modal = &modalState{
		id:              "managed-worktrees",
		kind:            ModalKindWorktree,
		worktreeBrowser: newWorktreeChooser(),
	}
	return nil
}

func newWorktreeChooser() *worktreeBrowserState {
	return &worktreeBrowserState{
		View: worktreeViewChooser,
		Choices: []worktreeBrowserChoice{
			{
				ID:          managedWorktreeModeFork,
				Label:       "Continue current conversation",
				Description: "Preserve this conversation in the new checkout",
			},
			{
				ID:          managedWorktreeModeNew,
				Label:       "Start new conversation",
				Description: "Open a fresh conversation in the new checkout",
			},
			{
				ID:          "browse",
				Label:       "Browse worktrees",
				Description: "Resume an owner thread or copy a working directory",
			},
		},
	}
}

// startWorktreeBrowser opens the loading popup and schedules the asynchronous
// listing, mirroring Rust's request_managed_worktrees.
func (m *Model) startWorktreeBrowser() bubbletea.Cmd {
	if m == nil || m.State == nil || !m.managedWorktreeAvailable() {
		return nil
	}
	request := codextui.WorktreeBrowserRequest{
		ID:       uuid.NewString(),
		CWD:      strings.TrimSpace(m.State.CWD),
		ThreadID: strings.TrimSpace(m.State.ThreadID),
	}
	m.worktreePopupRequestID = request.ID
	m.modal = &modalState{
		id:              "managed-worktrees",
		kind:            ModalKindWorktree,
		worktreeBrowser: &worktreeBrowserState{View: worktreeViewLoading, Request: request},
	}
	return m.loadManagedWorktreesCmd(request)
}

// loadManagedWorktreesCmd lists the repository's managed worktrees and resolves
// their owner summaries off the update loop.
func (m *Model) loadManagedWorktreesCmd(request codextui.WorktreeBrowserRequest) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	settings := m.worktreeSettings
	lookup := m.onWorktreeOwnerLookup
	return func() bubbletea.Msg {
		entries, err := codextui.ListManagedWorktreeEntries(settings, request.CWD)
		if err != nil {
			return WorktreeBrowserLoadedMsg{Request: request, Err: err}
		}
		if lookup != nil {
			entries = codextui.ResolveWorktreeOwnerSummaries(entries, lookup)
		}
		return WorktreeBrowserLoadedMsg{Request: request, Entries: entries}
	}
}

// worktreeRequestIsCurrent mirrors Rust's worktree_request_is_current: a request
// belongs to the session that opened it while the popup is still requesting it.
func (m *Model) worktreeRequestIsCurrent(request codextui.WorktreeBrowserRequest) bool {
	if m == nil || m.State == nil || request.ID == "" || m.worktreePopupRequestID != request.ID {
		return false
	}
	if request.CWD != strings.TrimSpace(m.State.CWD) || request.ThreadID != strings.TrimSpace(m.State.ThreadID) {
		return false
	}
	return m.managedWorktreeAvailable()
}

// applyWorktreeBrowserLoaded installs a finished listing, ignoring results whose
// request was superseded or whose popup was dismissed.
func (m *Model) applyWorktreeBrowserLoaded(message WorktreeBrowserLoadedMsg) bubbletea.Cmd {
	if m == nil || m.worktreePopupRequestID != message.Request.ID {
		return nil
	}
	if !m.worktreeRequestIsCurrent(message.Request) {
		return nil
	}
	if m.modal == nil || m.modal.worktreeBrowser == nil ||
		m.modal.worktreeBrowser.View != worktreeViewLoading ||
		m.modal.worktreeBrowser.Request.ID != message.Request.ID {
		return nil
	}
	if message.Err != nil {
		m.modal = nil
		m.worktreePopupRequestID = ""
		m.addErrorHistoryMessage("Cannot list managed worktrees: " + message.Err.Error())
		return nil
	}
	browser := m.modal.worktreeBrowser
	browser.View = worktreeViewList
	browser.Entries = message.Entries
	browser.Filter = ""
	browser.Selected = 0
	return nil
}

// activeWorktreeBrowser returns the popup state when the worktree modal is open.
func (m *Model) activeWorktreeBrowser() *worktreeBrowserState {
	if m == nil || m.modal == nil || m.modal.worktreeBrowser == nil {
		return nil
	}
	return m.modal.worktreeBrowser
}

// closeWorktreeBrowser dismisses the popup and retires its request so late
// results are discarded (Rust #43286 stale-result gating).
func (m *Model) closeWorktreeBrowser() {
	m.modal = nil
	m.worktreePopupRequestID = ""
}

// filteredWorktreeIndices returns the visible entry indices for the current
// filter, preserving repository order.
func (s *worktreeBrowserState) filteredWorktreeIndices(now int64) []int {
	if s == nil {
		return nil
	}
	filter := strings.ToLower(strings.TrimSpace(s.Filter))
	indices := make([]int, 0, len(s.Entries))
	for index, entry := range s.Entries {
		if filter == "" {
			indices = append(indices, index)
			continue
		}
		_, _, searchValue := codextui.WorktreeBrowserRow(entry, now)
		if strings.Contains(strings.ToLower(searchValue), filter) {
			indices = append(indices, index)
		}
	}
	return indices
}

// selectedWorktreeEntry returns the focused entry in the list view.
func (m *Model) selectedWorktreeEntry() (codextui.WorktreeBrowserEntry, bool) {
	browser := m.activeWorktreeBrowser()
	if browser == nil || browser.View != worktreeViewList {
		return codextui.WorktreeBrowserEntry{}, false
	}
	indices := browser.filteredWorktreeIndices(m.currentTime().Unix())
	if browser.Selected < 0 || browser.Selected >= len(indices) {
		return codextui.WorktreeBrowserEntry{}, false
	}
	return browser.Entries[indices[browser.Selected]], true
}

// showWorktreeActions opens the action list for one entry (Rust
// show_managed_worktree_actions).
func (m *Model) showWorktreeActions(entry codextui.WorktreeBrowserEntry) {
	browser := m.activeWorktreeBrowser()
	if browser == nil {
		return
	}
	request := browser.Request
	if !m.worktreeRequestIsCurrent(request) {
		return
	}
	browser.View = worktreeViewActions
	browser.Entry = entry
	browser.Selected = 0
	browser.Actions = codextui.WorktreeBrowserActionItems(entry, request.CWD)
}

// showWorktreeRemovalConfirmation opens the delete confirmation view.
func (m *Model) showWorktreeRemovalConfirmation(root string) {
	browser := m.activeWorktreeBrowser()
	if browser == nil || !m.worktreeRequestIsCurrent(browser.Request) {
		return
	}
	if worktreePathInside(root, browser.Request.CWD) {
		return
	}
	browser.View = worktreeViewConfirm
	browser.Selected = 0
	browser.Confirm = codextui.WorktreeDeleteConfirmationItems(root)
}

// runWorktreeAction dispatches one action row.
func (m *Model) runWorktreeAction(action codextui.WorktreeBrowserAction) bubbletea.Cmd {
	browser := m.activeWorktreeBrowser()
	if browser == nil || !m.worktreeRequestIsCurrent(browser.Request) {
		return nil
	}
	switch action.Kind {
	case codextui.WorktreeActionResume:
		m.closeWorktreeBrowser()
		threadID := strings.TrimSpace(action.ThreadID)
		if threadID == "" {
			m.notice = "Resume failed: missing thread id"
			return nil
		}
		_, notice, _ := m.applySessionSelection(codextui.SessionSelection{
			Kind:   codextui.SessionSelectionResume,
			Target: codextui.SessionTarget{ThreadID: threadID},
		})
		m.notice = notice
		m.refreshTranscript()
		return nil
	case codextui.WorktreeActionCopy:
		m.closeWorktreeBrowser()
		if m.clipboardWrite == nil {
			m.addErrorHistoryMessage("Clipboard copy is unavailable for this runtime.")
			return nil
		}
		if err := m.clipboardWrite(action.Path); err != nil {
			m.addErrorHistoryMessage("Failed to copy worktree working directory: " + err.Error())
			return nil
		}
		m.notice = "Copied worktree working directory"
		return nil
	case codextui.WorktreeActionRemove:
		m.showWorktreeRemovalConfirmation(action.Path)
		return nil
	default:
		return nil
	}
}

// removeManagedWorktreeCmd deletes a managed worktree off the update loop.
func (m *Model) removeManagedWorktreeCmd(root string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	browser := m.activeWorktreeBrowser()
	sourceCWD := strings.TrimSpace(m.State.CWD)
	if browser != nil {
		sourceCWD = browser.Request.CWD
	}
	settings := m.worktreeSettings
	return func() bubbletea.Msg {
		err := codextui.RemoveManagedWorktree(settings, sourceCWD, root)
		return WorktreeBrowserRemovedMsg{Root: root, Err: err}
	}
}

// applyWorktreeBrowserRemoved reports the deletion result, mirroring Rust's
// ManagedWorktreeRemoved handling.
func (m *Model) applyWorktreeBrowserRemoved(message WorktreeBrowserRemovedMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		m.addErrorHistoryMessage(fmt.Sprintf("Could not remove worktree at %s: %s", message.Root, message.Err.Error()))
		return
	}
	m.notice = fmt.Sprintf("Removed worktree at %s. Thread history was kept.", message.Root)
	if m.onManagedWorktreeChanged != nil {
		m.onManagedWorktreeChanged()
	}
	m.refreshTranscript()
}

// updateWorktreeBrowserModal routes keys to the active popup view.
func (m *Model) updateWorktreeBrowserModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	browser := m.activeWorktreeBrowser()
	if browser == nil {
		return nil
	}
	switch browser.View {
	case worktreeViewChooser:
		return m.updateWorktreeChooser(message)
	case worktreeViewLoading:
		if message.Type == bubbletea.KeyEsc {
			m.closeWorktreeBrowser()
		}
		return nil
	case worktreeViewList:
		return m.updateWorktreeList(message)
	case worktreeViewActions:
		return m.updateWorktreeActions(message)
	case worktreeViewConfirm:
		return m.updateWorktreeConfirm(message)
	default:
		return nil
	}
}

func (m *Model) updateWorktreeChooser(message bubbletea.KeyMsg) bubbletea.Cmd {
	browser := m.activeWorktreeBrowser()
	if browser == nil {
		return nil
	}
	switch message.Type {
	case bubbletea.KeyEsc:
		m.closeWorktreeBrowser()
	case bubbletea.KeyUp, bubbletea.KeyShiftTab:
		m.moveWorktreeSelection(browser, -1, len(browser.Choices))
	case bubbletea.KeyDown, bubbletea.KeyTab:
		m.moveWorktreeSelection(browser, 1, len(browser.Choices))
	case bubbletea.KeyEnter:
		if browser.Selected < 0 || browser.Selected >= len(browser.Choices) {
			return nil
		}
		choice := browser.Choices[browser.Selected]
		if choice.ID == "browse" {
			return m.startWorktreeBrowser()
		}
		return m.startManagedWorktree(choice.ID)
	}
	return nil
}

func (m *Model) updateWorktreeList(message bubbletea.KeyMsg) bubbletea.Cmd {
	browser := m.activeWorktreeBrowser()
	if browser == nil {
		return nil
	}
	indices := browser.filteredWorktreeIndices(m.currentTime().Unix())
	switch message.Type {
	case bubbletea.KeyEsc:
		m.closeWorktreeBrowser()
	case bubbletea.KeyUp:
		m.moveWorktreeSelection(browser, -1, len(indices))
	case bubbletea.KeyDown:
		m.moveWorktreeSelection(browser, 1, len(indices))
	case bubbletea.KeyPgUp:
		m.moveWorktreeSelection(browser, -pageStep(len(indices)), len(indices))
	case bubbletea.KeyPgDown:
		m.moveWorktreeSelection(browser, pageStep(len(indices)), len(indices))
	case bubbletea.KeyHome:
		browser.Selected = 0
	case bubbletea.KeyEnd:
		if len(indices) > 0 {
			browser.Selected = len(indices) - 1
		}
	case bubbletea.KeyBackspace:
		if browser.Filter != "" {
			runes := []rune(browser.Filter)
			browser.Filter = string(runes[:len(runes)-1])
			browser.Selected = 0
		}
	case bubbletea.KeyRunes:
		if text := string(message.Runes); text != "" {
			browser.Filter += text
			browser.Selected = 0
		}
	case bubbletea.KeyEnter:
		entry, ok := m.selectedWorktreeEntry()
		if !ok {
			return nil
		}
		m.showWorktreeActions(entry)
	}
	return nil
}

func (m *Model) updateWorktreeActions(message bubbletea.KeyMsg) bubbletea.Cmd {
	browser := m.activeWorktreeBrowser()
	if browser == nil {
		return nil
	}
	switch message.Type {
	case bubbletea.KeyEsc:
		browser.View = worktreeViewList
		browser.Selected = 0
	case bubbletea.KeyUp:
		m.moveWorktreeSelection(browser, -1, len(browser.Actions))
	case bubbletea.KeyDown:
		m.moveWorktreeSelection(browser, 1, len(browser.Actions))
	case bubbletea.KeyEnter:
		if browser.Selected < 0 || browser.Selected >= len(browser.Actions) {
			return nil
		}
		item := browser.Actions[browser.Selected]
		if item.Disabled {
			return nil
		}
		return m.runWorktreeAction(item.Action)
	}
	return nil
}

func (m *Model) updateWorktreeConfirm(message bubbletea.KeyMsg) bubbletea.Cmd {
	browser := m.activeWorktreeBrowser()
	if browser == nil {
		return nil
	}
	switch message.Type {
	case bubbletea.KeyEsc:
		browser.View = worktreeViewActions
		browser.Selected = 0
	case bubbletea.KeyUp:
		m.moveWorktreeSelection(browser, -1, len(browser.Confirm))
	case bubbletea.KeyDown:
		m.moveWorktreeSelection(browser, 1, len(browser.Confirm))
	case bubbletea.KeyEnter:
		if browser.Selected < 0 || browser.Selected >= len(browser.Confirm) {
			return nil
		}
		item := browser.Confirm[browser.Selected]
		if item.Action.Kind != codextui.WorktreeActionRemove {
			browser.View = worktreeViewActions
			browser.Selected = 0
			return nil
		}
		root := item.Action.Path
		cmd := m.removeManagedWorktreeCmd(root)
		m.closeWorktreeBrowser()
		return cmd
	}
	return nil
}

// startManagedWorktree runs the local creation handler and attaches the
// resulting session.
func (m *Model) startManagedWorktree(mode string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if m.onStartManagedWorktree == nil {
		m.closeWorktreeBrowser()
		m.addErrorHistoryMessage("Managed worktrees require a local Git repository.")
		return nil
	}
	cwd := strings.TrimSpace(m.State.CWD)
	threadID := strings.TrimSpace(m.State.ThreadID)
	m.closeWorktreeBrowser()
	response, err := m.onStartManagedWorktree(mode, "", cwd, threadID)
	if err != nil {
		m.addErrorHistoryMessage("Failed to start managed worktree session: " + err.Error())
		return nil
	}
	resumedThreadID := threadID
	if response.Summary != nil && strings.TrimSpace(response.Summary.ThreadID) != "" {
		resumedThreadID = strings.TrimSpace(response.Summary.ThreadID)
		m.upsertSessionItem(*response.Summary)
	}
	if resumedThreadID != "" {
		m.applyResumeResponse(resumedThreadID, response)
	}
	return nil
}

// moveWorktreeSelection moves a bounded selection index by delta.
func (m *Model) moveWorktreeSelection(browser *worktreeBrowserState, delta int, count int) {
	if browser == nil || count <= 0 {
		return
	}
	next := browser.Selected + delta
	if next < 0 {
		next = 0
	}
	if next >= count {
		next = count - 1
	}
	browser.Selected = next
}

func pageStep(count int) int {
	if count <= 1 {
		return 1
	}
	step := count / 2
	if step < 1 {
		step = 1
	}
	return step
}

// worktreePathInside reports whether child is root or inside root.
func worktreePathInside(root string, child string) bool {
	root = strings.TrimSpace(root)
	child = strings.TrimSpace(child)
	if root == "" || child == "" {
		return false
	}
	return codextui.WorktreePathWithin(root, child)
}

// applyWorktreeOptions copies the worktree-related options onto the model.
func applyWorktreeOptions(m *Model, options Options) {
	m.worktreesEnabled = options.WorktreesEnabled
	m.localWorktreeOperations = options.LocalWorktreeOperations
	m.worktreeSettings = options.WorktreeSettings
	m.worktreeRepoCheck = options.WorktreeRepositoryAvailable
	m.onWorktreeOwnerLookup = options.OnWorktreeOwnerLookup
	m.onStartManagedWorktree = options.OnStartManagedWorktree
	m.onManagedWorktreeChanged = options.OnManagedWorktreeChanged
}

// ---- rendering ----

// renderWorktreeBrowserModal renders the active worktree popup view.
func (m *Model) renderWorktreeBrowserModal() string {
	browser := m.activeWorktreeBrowser()
	if browser == nil {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	contentWidth := width
	if contentWidth > 2 {
		contentWidth -= 2
	}
	var builder strings.Builder
	now := m.currentTime().Unix()
	switch browser.View {
	case worktreeViewChooser:
		builder.WriteString("Worktrees\n\n")
		rows := make([]bottompane.GenericDisplayRow, 0, len(browser.Choices))
		for _, choice := range browser.Choices {
			rows = append(rows, bottompane.GenericDisplayRow{
				Name:        choice.Label,
				Description: choice.Description,
			})
		}
		builder.WriteString(m.renderWorktreeRows(rows, browser.Selected, contentWidth))
	case worktreeViewLoading:
		builder.WriteString("Managed worktrees\n\n")
		builder.WriteString(m.renderWorktreeRows([]bottompane.GenericDisplayRow{{
			Name:           "Loading worktrees\u2026",
			IsDisabled:     true,
			DisabledReason: "",
		}}, -1, contentWidth))
	case worktreeViewList:
		builder.WriteString("Managed worktrees\n")
		builder.WriteString(renderThemePickerDimLine(codextui.WorktreeBrowserSubtitle(browser.Entries), contentWidth))
		builder.WriteString("\n\n")
		builder.WriteString(renderThemePickerSearchPrompt(browser.Filter, contentWidth))
		builder.WriteString("\n")
		indices := browser.filteredWorktreeIndices(now)
		rows := make([]bottompane.GenericDisplayRow, 0, len(indices))
		for _, index := range indices {
			name, description, _ := codextui.WorktreeBrowserRow(browser.Entries[index], now)
			rows = append(rows, bottompane.GenericDisplayRow{
				Name:        name,
				Description: description,
			})
		}
		builder.WriteString(m.renderWorktreeRows(rows, browser.Selected, contentWidth))
	case worktreeViewActions:
		builder.WriteString(codextui.WorktreeActionsTitle(browser.Entry))
		builder.WriteString("\n")
		builder.WriteString(renderThemePickerDimLine(browser.Entry.CWD, contentWidth))
		builder.WriteString("\n\n")
		builder.WriteString(m.renderWorktreeRows(worktreeActionRows(browser.Actions), browser.Selected, contentWidth))
	case worktreeViewConfirm:
		builder.WriteString("Delete this worktree?\n")
		builder.WriteString(renderThemePickerDimLine(browser.Entry.Root, contentWidth))
		builder.WriteString("\n\n")
		builder.WriteString(m.renderWorktreeRows(worktreeActionRows(browser.Confirm), browser.Selected, contentWidth))
	}
	builder.WriteString("\n")
	builder.WriteString(renderThemePickerDimLine(worktreeBrowserFooterHint(browser.View), contentWidth))
	return strings.TrimRight(builder.String(), "\n")
}

func worktreeActionRows(items []codextui.WorktreeBrowserActionItem) []bottompane.GenericDisplayRow {
	rows := make([]bottompane.GenericDisplayRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, bottompane.GenericDisplayRow{
			Name:           item.Name,
			Description:    item.Description,
			IsDisabled:     item.Disabled,
			DisabledReason: item.DisabledReason,
		})
	}
	return rows
}

func (m *Model) renderWorktreeRows(rows []bottompane.GenericDisplayRow, selected int, width int) string {
	if len(rows) == 0 {
		return ""
	}
	start := 0
	if maxRows := sessionPickerListHeight(m.height); maxRows > 0 && len(rows) > maxRows {
		start = selected - maxRows/2
		if start < 0 {
			start = 0
		}
		if start+maxRows > len(rows) {
			start = len(rows) - maxRows
		}
		rows = rows[start : start+maxRows]
		selected -= start
	}
	prefixed := make([]bottompane.GenericDisplayRow, 0, len(rows))
	for index, row := range rows {
		row.NamePrefix = codextui.SelectionPrefix(index == selected)
		prefixed = append(prefixed, row)
	}
	state := bottompane.ScrollState{SelectedIdx: selected, HasSelection: selected >= 0}
	return strings.Join(bottompane.RenderGenericRowsWithDescriptionLayout(
		prefixed, state, len(prefixed), "", width, bottompane.ColumnWidthConfig{}, bottompane.SelectionDescriptionLayout{},
	), "\n")
}

func worktreeBrowserFooterHint(view worktreeBrowserView) string {
	switch view {
	case worktreeViewLoading:
		return "esc to go back"
	case worktreeViewList:
		return "Type to filter \u00b7 enter to select \u00b7 esc to go back"
	case worktreeViewActions, worktreeViewConfirm:
		return "enter to confirm \u00b7 esc to go back"
	default:
		return "enter to select \u00b7 esc to go back"
	}
}
