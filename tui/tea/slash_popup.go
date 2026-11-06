package tea

import (
	"sort"
	"strings"

	bubbletea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"codex_go/features"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/chatwidget"
	"codex_go/tui/keymsg"
)

const slashPopupMaxRows = 8

type slashCommandPopupItem struct {
	Name        string
	Description string
	// Unavailable marks a known command that cannot run in the active side
	// conversation (Rust #50756). Unavailable rows stay hidden from the
	// unfiltered `/` menu, render disabled while searching, and are never
	// selected, completed or dispatched.
	Unavailable bool
}

type slashCommandPopup struct {
	Active    bool
	Query     string
	Items     []slashCommandPopupItem
	Selected  int
	ScrollTop int
}

func (m *Model) refreshSlashPopup() {
	if m == nil {
		return
	}
	if m.threadUnavailable {
		m.slashPopup = slashCommandPopup{}
		return
	}
	query, ok := slashPopupQuery(m.composerText())
	if !ok {
		m.slashPopup = slashCommandPopup{}
		return
	}
	previousQuery := m.slashPopup.Query
	previous := m.selectedSlashPopupName()
	scrollTop := 0
	if previousQuery == query {
		scrollTop = m.slashPopup.ScrollTop
	}
	items := filterSlashPopupItems(m.slashPopupCatalog(), query)
	// Rust #50756: default to the first selectable (non-disabled) row.
	selected := firstSelectableSlashPopupIndex(items)
	if previous != "" && previousQuery == query {
		for i, item := range items {
			if item.Name == previous {
				selected = i
				break
			}
		}
	}
	m.slashPopup = slashCommandPopup{
		Active:    true,
		Query:     query,
		Items:     items,
		Selected:  selected,
		ScrollTop: scrollTop,
	}
}

func (m *Model) selectedSlashPopupName() string {
	if m == nil || !m.slashPopup.Active {
		return ""
	}
	if m.slashPopup.Selected < 0 || m.slashPopup.Selected >= len(m.slashPopup.Items) {
		return ""
	}
	return m.slashPopup.Items[m.slashPopup.Selected].Name
}

// slashPopupRows returns the number of rows the slash popup currently renders.
func (m *Model) slashPopupRows() int {
	if m == nil || !m.slashPopup.Active {
		return 0
	}
	if len(m.slashPopup.Items) == 0 {
		return 1 // "no matches"
	}
	return min(len(m.slashPopup.Items), slashPopupMaxRows)
}

func slashPopupQuery(text string) (string, bool) {
	if !strings.HasPrefix(text, "/") {
		return "", false
	}
	firstLine := text
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	query := strings.TrimPrefix(firstLine, "/")
	if strings.ContainsAny(query, " \t\r") {
		return "", false
	}
	return query, true
}

func filterSlashPopupItems(items []slashCommandPopupItem, query string) []slashCommandPopupItem {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		out := make([]slashCommandPopupItem, 0, len(items))
		for _, item := range items {
			// Rust #50756: a side conversation keeps its unavailable commands
			// out of the unfiltered `/` menu; they surface only once searched.
			if item.Unavailable {
				continue
			}
			if popupAliasCommandTea(item.Name) {
				continue
			}
			out = append(out, item)
		}
		return out
	}
	exact := []slashCommandPopupItem{}
	prefix := []slashCommandPopupItem{}
	for _, item := range items {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		switch {
		case name == query:
			exact = append(exact, item)
		case strings.HasPrefix(name, query):
			prefix = append(prefix, item)
		}
	}
	out := append(exact, prefix...)
	// Rust #50756: rank available matches before unavailable (disabled) ones.
	sort.SliceStable(out, func(i, j int) bool {
		return !out[i].Unavailable && out[j].Unavailable
	})
	return out
}

// firstSelectableSlashPopupIndex returns the index of the first row the user can
// actually select, or -1 when every row is disabled (Rust #50756).
func firstSelectableSlashPopupIndex(items []slashCommandPopupItem) int {
	for i, item := range items {
		if !item.Unavailable {
			return i
		}
	}
	return -1
}

func (m *Model) slashPopupCatalog() []slashCommandPopupItem {
	if m == nil {
		return nil
	}
	sideConversationActive := m.inSideConversation()
	daybreakDescription, daybreakVisible := m.daybreakCommandDescription()
	flags := bottompane.BuiltinCommandFlags{
		CollaborationModesEnabled:   features.Enabled(m.featureSettings, "collaboration_modes"),
		ConnectorsEnabled:           features.Enabled(m.featureSettings, "apps") && m.hasChatGPTAccount,
		PluginsCommandEnabled:       features.Enabled(m.featureSettings, "plugins"),
		TokenActivityCommandEnabled: m.hasChatGPTAccount,
		// Rust #51253 slash_dispatch: the tier commands are advertised when the
		// policy-filtered list is non-empty, so Fast and Ultra Fast are offered
		// independently.
		ServiceTierCommandsEnabled: len(m.serviceTierCommands) > 0,
		GoalCommandEnabled:         features.Enabled(m.featureSettings, "goals"),
		// Rust only advertises /setup-default-sandbox while the legacy
		// restricted-token sandbox is active and elevation is available
		// (chatwidget/slash_dispatch.rs builtin_command_flags).
		AllowElevateSandbox: m.windowsSandboxSetup != nil &&
			m.windowsSandboxLevel == chatwidget.WindowsSandboxLevelUnelevated &&
			chatwidget.WindowsSandboxModeAllowed(m.permissionRequirements, chatwidget.WindowsSandboxModeElevated),
		// Rust #50756: build the unfiltered catalog so side-unavailable commands
		// can be carried as disabled rows instead of being dropped, and mark
		// them here.
		SideConversationActive: false,
	}
	if daybreakVisible {
		flags.DaybreakCommandDescription = &daybreakDescription
	}
	commands := bottompane.CommandsForInput(flags, m.serviceTierCommands)
	items := make([]slashCommandPopupItem, 0, len(commands))
	for _, command := range commands {
		if command.Kind == bottompane.SlashCommandItemBuiltin && slashPopupHiddenCommand(command.Name) {
			continue
		}
		item := slashPopupItemFromCommand(command)
		if sideConversationActive && !command.AvailableInSideConversation() {
			item.Unavailable = true
		}
		items = append(items, item)
	}
	return items
}

func (m *Model) fastServiceTierCommand() *bottompane.ServiceTierCommand {
	if m == nil {
		return nil
	}
	for _, tier := range m.serviceTierCommands {
		if strings.EqualFold(strings.TrimSpace(tier.Name), "fast") {
			copy := tier
			return &copy
		}
	}
	return nil
}

func slashPopupItemFromCommand(command bottompane.SlashCommandItem) slashCommandPopupItem {
	description := command.Description
	if command.Kind == bottompane.SlashCommandItemServiceTier && command.ServiceTier != nil {
		description = command.ServiceTier.Description
	}
	return slashCommandPopupItem{
		Name:        command.CommandText(),
		Description: description,
	}
}

func slashPopupHiddenCommand(name string) bool {
	return name == "apps" || strings.HasPrefix(name, "debug")
}

func popupAliasCommandTea(name string) bool {
	switch strings.TrimSpace(name) {
	case "quit", "btw":
		return true
	default:
		return false
	}
}

func (m *Model) updateSlashPopupKey(msg keymsg.KeyMsg) (bubbletea.Cmd, bool) {
	if m == nil || !m.slashPopup.Active {
		return nil, false
	}
	switch msg.Type {
	case keymsg.KeyUp, keymsg.KeyCtrlP:
		m.moveSlashPopupSelection(-1)
		return nil, true
	case keymsg.KeyDown, keymsg.KeyCtrlN:
		m.moveSlashPopupSelection(1)
		return nil, true
	case keymsg.KeyEsc:
		m.slashPopup = slashCommandPopup{}
		return nil, true
	case keymsg.KeyTab:
		return m.completeSelectedSlashCommand(), true
	case keymsg.KeyEnter:
		if cmd, ok := m.dispatchSelectedSlashCommand(); ok {
			return cmd, true
		}
	}
	return nil, false
}

func (m *Model) moveSlashPopupSelection(delta int) {
	if m == nil || delta == 0 || len(m.slashPopup.Items) == 0 {
		return
	}
	items := m.slashPopup.Items
	index := m.slashPopup.Selected
	// Rust #50756: keyboard navigation walks past disabled rows.
	for range len(items) {
		index += delta
		if index < 0 {
			index = len(items) - 1
		}
		if index >= len(items) {
			index = 0
		}
		if !items[index].Unavailable {
			m.slashPopup.Selected = index
			m.slashPopup.ScrollTop = popupVisibleRange(len(items), index, m.slashPopup.ScrollTop, slashPopupMaxRows).start
			return
		}
	}
	m.slashPopup.Selected = -1
}

func (m *Model) completeSelectedSlashCommand() bubbletea.Cmd {
	if m == nil || !m.slashPopup.Active {
		return nil
	}
	item, ok := m.currentSlashPopupItem()
	if !ok {
		return nil
	}
	firstLine := m.composerText()
	if index := strings.IndexByte(firstLine, '\n'); index >= 0 {
		firstLine = firstLine[:index]
	}
	selected := "/" + item.Name
	// Rust #50756: an exact match has nothing to complete. While a turn runs,
	// Tab queues the typed command instead of rewriting the composer; an idle
	// composer keeps the text as-is.
	if strings.HasPrefix(strings.TrimLeft(firstLine, " \t"), selected) {
		m.slashPopup = slashCommandPopup{}
		if m.isUserTurnPendingOrRunning() {
			return m.queueComposer(true)
		}
		return nil
	}
	m.setComposerPrompt(selected + " ")
	m.slashPopup = slashCommandPopup{}
	return nil
}

func (m *Model) dispatchSelectedSlashCommand() (bubbletea.Cmd, bool) {
	if m == nil || !m.slashPopup.Active {
		return nil, false
	}
	item, ok := m.currentSlashPopupItem()
	if !ok {
		return nil, false
	}
	invocation, parsed := codextui.ParseCommand("/" + item.Name)
	// Rust #58767: the composer keeps the draft for commands that validate the
	// thread state first, so a busy rejection leaves /fork's text and
	// attachments intact.
	if parsed && invocation.Command.RequiresDispatchValidation() && m.slashDispatchValidationBlocked(invocation.Command) {
		m.slashPopup = slashCommandPopup{}
		m.showSlashCommandBusyError(invocation.Name)
		return nil, true
	}
	m.resetComposer()
	// Rust #41921: slash-command dispatch returns the composer to Vim Insert.
	m.enterVimInsertAfterSubmission()
	m.slashPopup = slashCommandPopup{}
	// Rust #50472: a catalog service-tier row dispatches as a tier selection
	// rather than a builtin command.
	if tier, ok := m.serviceTierCommandForInput("/" + item.Name); ok {
		return m.applyServiceTierCommand(*tier), true
	}
	if !parsed {
		return nil, true
	}
	return m.applyCommand(invocation), true
}

func (m *Model) currentSlashPopupItem() (slashCommandPopupItem, bool) {
	if m == nil || m.slashPopup.Selected < 0 || m.slashPopup.Selected >= len(m.slashPopup.Items) {
		return slashCommandPopupItem{}, false
	}
	item := m.slashPopup.Items[m.slashPopup.Selected]
	// Rust #50756: a disabled row is never selectable for completion or dispatch.
	if item.Unavailable {
		return slashCommandPopupItem{}, false
	}
	return item, true
}

func (m *Model) renderSlashPopup() string {
	if m == nil || !m.slashPopup.Active {
		return ""
	}
	width := firstPositive(m.width, defaultWidth)
	if len(m.slashPopup.Items) == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  no matches")
	}
	visible := popupVisibleRange(len(m.slashPopup.Items), m.slashPopup.Selected, m.slashPopup.ScrollTop, slashPopupMaxRows)
	// Rust renders the popup with ColumnWidthMode::AutoAllRows so scrolling does
	// not shift the description column (bottom_pane/command_popup.rs:27-31 and
	// selection_popup_common.rs:155-170).
	nameWidth := slashPopupNameWidth(m.slashPopup.Items, width)

	lines := []string{}
	for idx := visible.start; idx < visible.end; idx++ {
		item := m.slashPopup.Items[idx]
		selected := idx == m.slashPopup.Selected
		line := slashPopupRenderLine(item, selected, nameWidth, width)
		lines = append(lines, line)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

type slashPopupRange struct {
	start int
	end   int
}

// popupVisibleRange mirrors Rust's ScrollState + render_rows window arithmetic
// (bottom_pane/scroll_state.rs and selection_popup_common.rs:572-586): the
// viewport keeps its scroll_top until the selection leaves the window, so
// scrolling back from the end of a long list does not drag the window along.
func popupVisibleRange(length int, selected int, scrollTop int, maxRows int) slashPopupRange {
	if length == 0 || maxRows <= 0 {
		return slashPopupRange{}
	}
	visible := min(maxRows, length)
	start := scrollTop
	if limit := length - visible; start > limit {
		start = limit
	}
	if start < 0 {
		start = 0
	}
	if selected >= 0 {
		switch {
		case selected < start:
			start = selected
		case selected > start+visible-1:
			start = selected + 1 - visible
		}
	}
	return slashPopupRange{start: start, end: start + visible}
}

// slashPopupDisabledReason is the row reason Rust #50756 shows for a side
// conversation command that is known but cannot run here.
const slashPopupDisabledReason = "not available in a side conversation"

// slashPopupNameWidth mirrors Rust's compute_desc_col with
// ColumnWidthMode::AutoAllRows (selection_popup_common.rs:150-170): the column
// is sized from every row of the filtered list, so scrolling never shifts it,
// and the description column is clamped to min(content_width-1, 70% of
// content_width).
func slashPopupNameWidth(items []slashCommandPopupItem, contentWidth int) int {
	width := 0
	for _, item := range items {
		if n := codextui.DisplayWidth(slashPopupDisplayName(item)); n > width {
			width = n
		}
	}
	if contentWidth <= 0 {
		return width
	}
	maxDescCol := contentWidth - 1
	if auto := contentWidth * 70 / 100; auto > 1 && auto < maxDescCol {
		maxDescCol = auto
	}
	// The row prefix (2 columns) and the 2-column gap sit between the name and
	// the description column.
	if limit := maxDescCol - 4; width > limit {
		width = limit
	}
	if width < 0 {
		return 0
	}
	return width
}

// slashPopupDisplayName renders the name column, marking disabled rows the way
// Rust #50756 does.
func slashPopupDisplayName(item slashCommandPopupItem) string {
	name := "/" + item.Name
	if item.Unavailable {
		name += " (disabled)"
	}
	return name
}

// slashPopupDisplayDescription appends the disabled reason Rust #50756 shows.
func slashPopupDisplayDescription(item slashCommandPopupItem) string {
	if !item.Unavailable {
		return item.Description
	}
	if item.Description == "" {
		return "(disabled: " + slashPopupDisabledReason + ")"
	}
	return item.Description + " (disabled: " + slashPopupDisabledReason + ")"
}

func slashPopupRenderLine(item slashCommandPopupItem, selected bool, nameWidth int, width int) string {
	prefix := codextui.SelectionPrefix(selected)
	// Rust's selection popups place descriptions two columns past the widest
	// name (`compute_desc_col` adds 2).
	availableDescription := width - codextui.DisplayWidth(prefix) - nameWidth - 2
	if availableDescription < 0 {
		availableDescription = 0
	}
	name := codextui.TruncateWithEllipsis(slashPopupDisplayName(item), nameWidth)
	description := codextui.TruncateWithEllipsis(slashPopupDisplayDescription(item), availableDescription)
	rawName := padRightDisplay(name, nameWidth)
	if item.Unavailable {
		// Rust #50756 dims the disabled row and never draws the selection marker.
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		return prefix + dim.Render(rawName+"  "+description)
	}
	if selected {
		return codextui.RenderSelectedRow(prefix + rawName + "  " + description)
	}
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	descriptionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	return prefix + nameStyle.Render(rawName) + "  " + descriptionStyle.Render(description)
}

func padRightDisplay(value string, width int) string {
	if width <= 0 {
		return ""
	}
	padding := width - codextui.DisplayWidth(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}
