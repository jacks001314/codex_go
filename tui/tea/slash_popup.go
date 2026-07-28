package tea

import (
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
	selected := 0
	if len(items) == 0 {
		selected = -1
	} else if previous != "" && previousQuery == query {
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
	return append(exact, prefix...)
}

func (m *Model) slashPopupCatalog() []slashCommandPopupItem {
	if m == nil {
		return nil
	}
	flags := bottompane.BuiltinCommandFlags{
		CollaborationModesEnabled:   features.Enabled(m.featureSettings, "collaboration_modes"),
		ConnectorsEnabled:           features.Enabled(m.featureSettings, "apps") && m.hasChatGPTAccount,
		PluginsCommandEnabled:       features.Enabled(m.featureSettings, "plugins"),
		TokenActivityCommandEnabled: m.hasChatGPTAccount,
		ServiceTierCommandsEnabled:  features.Enabled(m.featureSettings, "fast_mode"),
		GoalCommandEnabled:          features.Enabled(m.featureSettings, "goals"),
		PersonalityCommandEnabled:   features.Enabled(m.featureSettings, "personality"),
		AllowElevateSandbox:         m.windowsSandboxSetup != nil,
		SideConversationActive:      m.inSideConversation(),
	}
	commands := bottompane.CommandsForInput(flags, m.serviceTierCommands)
	items := make([]slashCommandPopupItem, 0, len(commands))
	for _, command := range commands {
		if command.Kind == bottompane.SlashCommandItemBuiltin && slashPopupHiddenCommand(command.Name) {
			continue
		}
		items = append(items, slashPopupItemFromCommand(command))
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
	if m == nil || len(m.slashPopup.Items) == 0 {
		return
	}
	next := m.slashPopup.Selected + delta
	switch {
	case next < 0:
		next = len(m.slashPopup.Items) - 1
	case next >= len(m.slashPopup.Items):
		next = 0
	}
	m.slashPopup.Selected = next
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
	return m.slashPopup.Items[m.slashPopup.Selected], true
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

func slashPopupNameWidth(items []slashCommandPopupItem) int {
	width := codextui.DisplayWidth("/experimental")
	for _, item := range items {
		if n := codextui.DisplayWidth("/" + item.Name); n > width {
			width = n
		}
	}
	if width > 28 {
		return 28
	}
	return width
}

func slashPopupRenderLine(item slashCommandPopupItem, selected bool, nameWidth int, width int) string {
	prefix := codextui.SelectionPrefix(selected)
	availableDescription := width - codextui.DisplayWidth(prefix) - nameWidth - 1
	if availableDescription < 0 {
		availableDescription = 0
	}
	name := codextui.TruncateWithEllipsis("/"+item.Name, nameWidth)
	description := codextui.TruncateWithEllipsis(item.Description, availableDescription)
	rawName := padRightDisplay(name, nameWidth)
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
