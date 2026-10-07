package tea

import (
	"sort"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codex_go/features"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
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
	Active   bool
	Query    string
	Items    []slashCommandPopupItem
	Selected int
}

func (m *Model) refreshSlashPopup() {
	if m == nil {
		return
	}
	query, ok := slashPopupQuery(m.composer.Value())
	if !ok {
		m.slashPopup = slashCommandPopup{}
		return
	}
	previousQuery := m.slashPopup.Query
	previous := m.selectedSlashPopupName()
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
		Active:   true,
		Query:    query,
		Items:    items,
		Selected: selected,
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
		AllowElevateSandbox:        m.windowsSandboxSetup != nil,
		// Rust #50756: build the unfiltered catalog so side-unavailable commands
		// can be carried as disabled rows instead of being dropped, and mark
		// them here.
		SideConversationActive: false,
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

func (m *Model) updateSlashPopupKey(msg bubbletea.KeyMsg) (bubbletea.Cmd, bool) {
	if m == nil || !m.slashPopup.Active {
		return nil, false
	}
	switch msg.Type {
	case bubbletea.KeyUp, bubbletea.KeyCtrlP:
		m.moveSlashPopupSelection(-1)
		return nil, true
	case bubbletea.KeyDown, bubbletea.KeyCtrlN:
		m.moveSlashPopupSelection(1)
		return nil, true
	case bubbletea.KeyEsc:
		m.slashPopup = slashCommandPopup{}
		return nil, true
	case bubbletea.KeyTab:
		m.completeSelectedSlashCommand()
		return nil, true
	case bubbletea.KeyEnter:
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
			return
		}
	}
	m.slashPopup.Selected = -1
}

func (m *Model) completeSelectedSlashCommand() {
	if m == nil || !m.slashPopup.Active {
		return
	}
	item, ok := m.currentSlashPopupItem()
	if !ok {
		return
	}
	m.composer.SetValue("/" + item.Name + " ")
	m.slashPopup = slashCommandPopup{}
}

func (m *Model) dispatchSelectedSlashCommand() (bubbletea.Cmd, bool) {
	if m == nil || !m.slashPopup.Active {
		return nil, false
	}
	item, ok := m.currentSlashPopupItem()
	if !ok {
		return nil, false
	}
	m.composer.Reset()
	// Rust #41921: slash-command dispatch returns the composer to Vim Insert.
	m.enterVimInsertAfterSubmission()
	m.slashPopup = slashCommandPopup{}
	invocation, ok := codextui.ParseCommand("/" + item.Name)
	if !ok {
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
	visible := slashPopupVisibleRange(len(m.slashPopup.Items), m.slashPopup.Selected, slashPopupMaxRows)
	nameWidth := slashPopupNameWidth(m.slashPopup.Items[visible.start:visible.end])

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

func slashPopupVisibleRange(length int, selected int, maxRows int) slashPopupRange {
	if length <= maxRows {
		return slashPopupRange{start: 0, end: length}
	}
	if selected < 0 {
		selected = 0
	}
	start := 0
	if selected >= maxRows {
		start = selected - maxRows + 1
	}
	end := start + maxRows
	if end > length {
		end = length
		start = end - maxRows
	}
	return slashPopupRange{start: start, end: end}
}

// slashPopupDisabledReason is the row reason Rust #50756 shows for a side
// conversation command that is known but cannot run here.
const slashPopupDisabledReason = "not available in a side conversation"

func slashPopupNameWidth(items []slashCommandPopupItem) int {
	width := codextui.DisplayWidth("/experimental")
	for _, item := range items {
		if n := codextui.DisplayWidth(slashPopupDisplayName(item)); n > width {
			width = n
		}
	}
	if width > 28 {
		return 28
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
	availableDescription := width - codextui.DisplayWidth(prefix) - nameWidth - 1
	if availableDescription < 0 {
		availableDescription = 0
	}
	name := codextui.TruncateWithEllipsis(slashPopupDisplayName(item), nameWidth)
	description := codextui.TruncateWithEllipsis(slashPopupDisplayDescription(item), availableDescription)
	rawName := padRightDisplay(name, nameWidth)
	if item.Unavailable {
		// Rust #50756 dims the disabled row and never draws the selection marker.
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		return prefix + dim.Render(rawName+" "+description)
	}
	if selected {
		return codextui.RenderSelectedRow(prefix + rawName + " " + description)
	}
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	descriptionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	return prefix + nameStyle.Render(rawName) + " " + descriptionStyle.Render(description)
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
