package bottompane

import (
	"strings"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/command_popup.rs.

var commandPopupColumnWidth = NewColumnWidthConfig(ColumnWidthAutoAllRows, nil)

type CommandPopupItem struct {
	Name         string
	Description  string
	Aliases      []string
	Kind         SlashCommandItemKind
	Command      SlashCommandItem
	MatchIndices []int
	IsAlias      bool
}

func FilterCommandPopupItems(items []CommandPopupItem, query string) []CommandPopupItem {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		out := make([]CommandPopupItem, 0, len(items))
		for _, item := range items {
			if item.IsAlias || popupAliasCommand(item.Name) {
				continue
			}
			item.MatchIndices = nil
			out = append(out, item)
		}
		return out
	}
	exact := []CommandPopupItem{}
	prefix := []CommandPopupItem{}
	for _, item := range items {
		nameLower := strings.ToLower(strings.TrimSpace(item.Name))
		switch {
		case nameLower == query:
			item.MatchIndices = commandPopupMatchIndices(query)
			exact = append(exact, item)
		case strings.HasPrefix(nameLower, query):
			item.MatchIndices = commandPopupMatchIndices(query)
			prefix = append(prefix, item)
		}
	}
	return append(exact, prefix...)
}

type CommandPopupFlags struct {
	CollaborationModesEnabled    bool
	ConnectorsEnabled            bool
	PluginsCommandEnabled        bool
	TokenActivityCommandEnabled  bool
	ServiceTierCommandsEnabled   bool
	GoalCommandEnabled           bool
	PersonalityCommandEnabled    bool
	WindowsDegradedSandboxActive bool
	SideConversationActive       bool
}

type CommandPopup struct {
	commandFilter string
	commands      []CommandPopupItem
	state         ScrollState
}

func NewCommandPopup(flags CommandPopupFlags, serviceTierCommands []ServiceTierCommand) *CommandPopup {
	builtinFlags := BuiltinCommandFlags{
		CollaborationModesEnabled:   flags.CollaborationModesEnabled,
		ConnectorsEnabled:           flags.ConnectorsEnabled,
		PluginsCommandEnabled:       flags.PluginsCommandEnabled,
		TokenActivityCommandEnabled: flags.TokenActivityCommandEnabled,
		ServiceTierCommandsEnabled:  flags.ServiceTierCommandsEnabled,
		GoalCommandEnabled:          flags.GoalCommandEnabled,
		PersonalityCommandEnabled:   flags.PersonalityCommandEnabled,
		AllowElevateSandbox:         flags.WindowsDegradedSandboxActive,
		SideConversationActive:      flags.SideConversationActive,
	}
	commands := CommandsForInput(builtinFlags, serviceTierCommands)
	items := make([]CommandPopupItem, 0, len(commands))
	for _, command := range commands {
		if command.Kind == SlashCommandItemBuiltin &&
			(strings.HasPrefix(command.CommandText(), "debug") || command.Command == codextui.CommandApps) {
			continue
		}
		items = append(items, commandPopupItemFromSlashCommand(command))
	}
	popup := &CommandPopup{
		commands: items,
		state:    NewScrollState(),
	}
	popup.refreshSelection()
	return popup
}

func popupAliasCommand(name string) bool {
	switch strings.TrimSpace(name) {
	case "quit", "btw":
		return true
	default:
		return false
	}
}

func (p *CommandPopup) OnComposerTextChange(text string) {
	if p == nil {
		return
	}
	firstLine := text
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	previous := p.commandFilter
	if stripped, ok := strings.CutPrefix(firstLine, "/"); ok {
		token := strings.TrimLeft(stripped, " \t\r")
		fields := strings.Fields(token)
		if len(fields) == 0 {
			p.commandFilter = ""
		} else {
			p.commandFilter = fields[0]
		}
	} else {
		p.commandFilter = ""
	}
	if p.commandFilter != previous {
		p.state.Reset()
	}
	p.refreshSelection()
}

func (p *CommandPopup) Filter() string {
	if p == nil {
		return ""
	}
	return p.commandFilter
}

func (p *CommandPopup) FilteredItems() []CommandPopupItem {
	if p == nil {
		return nil
	}
	return FilterCommandPopupItems(p.commands, p.commandFilter)
}

func (p *CommandPopup) SelectedItem() (CommandPopupItem, bool) {
	items := p.FilteredItems()
	if p == nil || !p.state.HasSelection || p.state.SelectedIdx < 0 || p.state.SelectedIdx >= len(items) {
		return CommandPopupItem{}, false
	}
	return items[p.state.SelectedIdx], true
}

func (p *CommandPopup) MoveUp() {
	if p == nil {
		return
	}
	length := len(p.FilteredItems())
	p.state.MoveUpWrap(length)
	p.state.EnsureVisible(length, min(MaxPopupRows, length))
}

func (p *CommandPopup) MoveDown() {
	if p == nil {
		return
	}
	length := len(p.FilteredItems())
	p.state.MoveDownWrap(length)
	p.state.EnsureVisible(length, min(MaxPopupRows, length))
}

func (p *CommandPopup) CalculateRequiredHeight(width int) int {
	if p == nil {
		return 0
	}
	return MeasureGenericRowsHeight(commandPopupDisplayRows(p.FilteredItems()), p.state, MaxPopupRows, max(width-2, 1), commandPopupColumnWidth)
}

func (p *CommandPopup) Rows(width int) []string {
	if p == nil {
		return nil
	}
	items := p.FilteredItems()
	if len(items) == 0 {
		return []string{"  no matches"}
	}
	p.state.ClampSelection(len(items))
	p.state.EnsureVisible(len(items), min(MaxPopupRows, len(items)))
	rendered := RenderGenericRows(commandPopupDisplayRows(items), p.state, MaxPopupRows, "no matches", max(width-2, 1), commandPopupColumnWidth)
	rows := make([]string, 0, len(rendered))
	for _, row := range rendered {
		rows = append(rows, "  "+row)
	}
	return rows
}

func (p *CommandPopup) refreshSelection() {
	items := p.FilteredItems()
	p.state.ClampSelection(len(items))
	p.state.EnsureVisible(len(items), min(MaxPopupRows, len(items)))
}

func commandPopupItemFromSlashCommand(command SlashCommandItem) CommandPopupItem {
	description := command.Description
	if command.Kind == SlashCommandItemServiceTier && command.ServiceTier != nil {
		description = command.ServiceTier.Description
	}
	return CommandPopupItem{
		Name:        command.CommandText(),
		Description: description,
		Kind:        command.Kind,
		Command:     command,
		IsAlias:     command.IsAlias,
	}
}

func commandPopupMatchIndices(query string) []int {
	if query == "" {
		return nil
	}
	runes := []rune(query)
	indices := make([]int, 0, len(runes))
	for idx := range runes {
		indices = append(indices, idx+1)
	}
	return indices
}

func commandPopupDisplayRows(items []CommandPopupItem) []GenericDisplayRow {
	rows := make([]GenericDisplayRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, GenericDisplayRow{
			Name:         "/" + item.Name,
			MatchIndices: item.MatchIndices,
			Description:  item.Description,
		})
	}
	return rows
}
