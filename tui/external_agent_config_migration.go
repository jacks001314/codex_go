package tui

import (
	"reflect"
	"strconv"
	"strings"

	"codex_go/config"
)

// Rust parity: codex-rs/tui/src/external_agent_config_migration.rs.

type ExternalAgentConfigMigrationStatus string

const (
	ExternalAgentMigrationPending  ExternalAgentConfigMigrationStatus = "pending"
	ExternalAgentMigrationComplete ExternalAgentConfigMigrationStatus = "complete"
)

type ExternalAgentConfigMigrationOutcomeKind string

const (
	ExternalAgentConfigMigrationProceed ExternalAgentConfigMigrationOutcomeKind = "proceed"
	ExternalAgentConfigMigrationSkip    ExternalAgentConfigMigrationOutcomeKind = "skip"
)

type ExternalAgentConfigMigrationOutcome struct {
	Kind  ExternalAgentConfigMigrationOutcomeKind
	Items []config.ExternalAgentConfigMigrationItem
}

type ExternalAgentConfigMigrationFocusArea string

const (
	ExternalAgentMigrationFocusItems   ExternalAgentConfigMigrationFocusArea = "items"
	ExternalAgentMigrationFocusActions ExternalAgentConfigMigrationFocusArea = "actions"
)

type ExternalAgentConfigMigrationAction string

const (
	ExternalAgentMigrationActionProceed   ExternalAgentConfigMigrationAction = "proceed"
	ExternalAgentMigrationActionCustomize ExternalAgentConfigMigrationAction = "customize"
	ExternalAgentMigrationActionSkip      ExternalAgentConfigMigrationAction = "skip"
	ExternalAgentMigrationActionBack      ExternalAgentConfigMigrationAction = "back"
)

func (a ExternalAgentConfigMigrationAction) Label() string {
	switch a {
	case ExternalAgentMigrationActionProceed:
		return "Import selected"
	case ExternalAgentMigrationActionCustomize:
		return "Customize selection"
	case ExternalAgentMigrationActionSkip:
		return "Cancel"
	case ExternalAgentMigrationActionBack:
		return "Review selection"
	default:
		return string(a)
	}
}

type ExternalAgentConfigMigrationView string

const (
	ExternalAgentMigrationViewSummary   ExternalAgentConfigMigrationView = "summary"
	ExternalAgentMigrationViewCustomize ExternalAgentConfigMigrationView = "customize"
)

type externalAgentMigrationSelection struct {
	Item    config.ExternalAgentConfigMigrationItem
	Enabled bool
}

type ExternalAgentConfigMigrationScreen struct {
	items             []externalAgentMigrationSelection
	groups            []ExternalAgentConfigMigrationGroupModel
	view              ExternalAgentConfigMigrationView
	selectedItemIndex *int
	focus             ExternalAgentConfigMigrationFocusArea
	highlightedAction ExternalAgentConfigMigrationAction
	done              bool
	outcome           ExternalAgentConfigMigrationOutcome
	errorMessage      string
}

func NewExternalAgentConfigMigrationScreen(items []config.ExternalAgentConfigMigrationItem, selectedItems []config.ExternalAgentConfigMigrationItem, errorMessage string) *ExternalAgentConfigMigrationScreen {
	selections := make([]externalAgentMigrationSelection, 0, len(items))
	for _, item := range items {
		selections = append(selections, externalAgentMigrationSelection{
			Item:    item,
			Enabled: migrationItemSelected(item, selectedItems),
		})
	}
	var selected *int
	if len(items) > 0 {
		zero := 0
		selected = &zero
	}
	screen := &ExternalAgentConfigMigrationScreen{
		items:             selections,
		groups:            ExternalAgentConfigMigrationGroups(items),
		view:              ExternalAgentMigrationViewSummary,
		selectedItemIndex: selected,
		focus:             ExternalAgentMigrationFocusActions,
		highlightedAction: ExternalAgentMigrationActionProceed,
		outcome:           ExternalAgentConfigMigrationOutcome{Kind: ExternalAgentConfigMigrationSkip},
		errorMessage:      strings.TrimSpace(errorMessage),
	}
	screen.normalizeHighlightedAction()
	return screen
}

func (s *ExternalAgentConfigMigrationScreen) HandleKey(key string) {
	if s == nil || s.done {
		return
	}
	rawKey := strings.ToLower(key)
	if rawKey == " " {
		key = " "
	} else {
		key = strings.TrimSpace(rawKey)
	}
	if key == "release" {
		return
	}
	switch key {
	case "ctrl-c", "ctrl-d":
		s.skip()
	case "up", "k":
		s.moveUp()
	case "down", "j":
		s.moveDown()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		s.selectNumberedAction(int(key[0] - '1'))
	case "c":
		if s.view == ExternalAgentMigrationViewSummary {
			s.customize()
		}
	case "b":
		if s.view == ExternalAgentMigrationViewCustomize {
			s.backToSummary()
		}
	case " ":
		if s.view == ExternalAgentMigrationViewCustomize {
			s.toggleSelectedItem()
		}
	case "a":
		if s.view == ExternalAgentMigrationViewCustomize {
			s.setAllEnabled(true)
		}
	case "n":
		if s.view == ExternalAgentMigrationViewCustomize {
			s.setAllEnabled(false)
		}
	case "enter":
		s.confirmSelection()
	case "esc", "escape":
		if s.view == ExternalAgentMigrationViewSummary {
			s.skip()
		} else {
			s.backToSummary()
		}
	}
}

func (s *ExternalAgentConfigMigrationScreen) IsDone() bool {
	return s != nil && s.done
}

func (s *ExternalAgentConfigMigrationScreen) Outcome() ExternalAgentConfigMigrationOutcome {
	if s == nil {
		return ExternalAgentConfigMigrationOutcome{Kind: ExternalAgentConfigMigrationSkip}
	}
	return s.outcome
}

func (s *ExternalAgentConfigMigrationScreen) View() ExternalAgentConfigMigrationView {
	if s == nil {
		return ExternalAgentMigrationViewSummary
	}
	return s.view
}

func (s *ExternalAgentConfigMigrationScreen) SelectedItems() []config.ExternalAgentConfigMigrationItem {
	if s == nil {
		return nil
	}
	selected := []config.ExternalAgentConfigMigrationItem{}
	for _, item := range s.items {
		if item.Enabled {
			selected = append(selected, item.Item)
		}
	}
	return selected
}

func (s *ExternalAgentConfigMigrationScreen) Rows() []string {
	if s == nil {
		return nil
	}
	title := "Import setup"
	intro := []string{
		"Bring over supported setup from another coding agent.",
		"Codex may add files to your current project folder.",
		"Your existing setup will not be changed.",
	}
	footer := "Use up/down to move, enter to select, c to customize"
	if s.view == ExternalAgentMigrationViewCustomize {
		title = "Choose what to import"
		intro[0] = "Choose items to import."
		footer = "Use up/down to move, space to toggle, b to go back"
		if s.focus == ExternalAgentMigrationFocusActions {
			footer = "Press enter to continue, up/down to move, b to go back"
		}
	}
	rows := append([]string{"> " + title}, intro...)
	if s.errorMessage != "" {
		rows = append(rows, s.errorMessage)
	}
	if s.view == ExternalAgentMigrationViewSummary {
		rows = append(rows, s.summaryRows()...)
	} else {
		rows = append(rows, s.customizeRows()...)
	}
	itemLabel := "items"
	if len(s.items) == 1 {
		itemLabel = "item"
	}
	rows = append(rows, "", "Selected "+strconv.Itoa(len(s.SelectedItems()))+" of "+strconv.Itoa(len(s.items))+" "+itemLabel+".")
	for idx, action := range s.availableActions() {
		selected := s.focus == ExternalAgentMigrationFocusActions && s.highlightedAction == action
		row := NumberedSelectionPrefix(idx, selected) + action.Label()
		if selected {
			row = RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	rows = append(rows, footer)
	return rows
}

func (s *ExternalAgentConfigMigrationScreen) availableActions() []ExternalAgentConfigMigrationAction {
	if s.view == ExternalAgentMigrationViewCustomize {
		return []ExternalAgentConfigMigrationAction{ExternalAgentMigrationActionBack}
	}
	actions := []ExternalAgentConfigMigrationAction{}
	if len(s.SelectedItems()) > 0 {
		actions = append(actions, ExternalAgentMigrationActionProceed)
	}
	return append(actions, ExternalAgentMigrationActionCustomize, ExternalAgentMigrationActionSkip)
}

func (s *ExternalAgentConfigMigrationScreen) normalizeHighlightedAction() {
	actions := s.availableActions()
	for _, action := range actions {
		if action == s.highlightedAction {
			return
		}
	}
	if len(actions) > 0 {
		s.highlightedAction = actions[0]
	}
}

func (s *ExternalAgentConfigMigrationScreen) proceed() {
	s.outcome = ExternalAgentConfigMigrationOutcome{Kind: ExternalAgentConfigMigrationProceed, Items: s.SelectedItems()}
	s.done = true
}

func (s *ExternalAgentConfigMigrationScreen) skip() {
	s.outcome = ExternalAgentConfigMigrationOutcome{Kind: ExternalAgentConfigMigrationSkip}
	s.done = true
}

func (s *ExternalAgentConfigMigrationScreen) customize() {
	s.view = ExternalAgentMigrationViewCustomize
	s.focus = ExternalAgentMigrationFocusItems
	s.highlightedAction = ExternalAgentMigrationActionBack
	if len(s.items) > 0 && s.selectedItemIndex == nil {
		zero := 0
		s.selectedItemIndex = &zero
	}
}

func (s *ExternalAgentConfigMigrationScreen) backToSummary() {
	s.view = ExternalAgentMigrationViewSummary
	s.focus = ExternalAgentMigrationFocusActions
	s.highlightedAction = s.availableActions()[0]
}

func (s *ExternalAgentConfigMigrationScreen) setAllEnabled(enabled bool) {
	for idx := range s.items {
		s.items[idx].Enabled = enabled
	}
	s.errorMessage = ""
	s.normalizeHighlightedAction()
}

func (s *ExternalAgentConfigMigrationScreen) toggleSelectedItem() {
	if s.selectedItemIndex == nil || *s.selectedItemIndex < 0 || *s.selectedItemIndex >= len(s.items) {
		return
	}
	s.items[*s.selectedItemIndex].Enabled = !s.items[*s.selectedItemIndex].Enabled
	s.errorMessage = ""
	s.normalizeHighlightedAction()
}

func (s *ExternalAgentConfigMigrationScreen) moveUp() {
	if s.view == ExternalAgentMigrationViewSummary {
		s.moveAction(-1)
		return
	}
	if s.focus == ExternalAgentMigrationFocusItems {
		if s.selectedItemIndex != nil && *s.selectedItemIndex > 0 {
			next := *s.selectedItemIndex - 1
			s.selectedItemIndex = &next
			return
		}
		s.focus = ExternalAgentMigrationFocusActions
		s.highlightedAction = s.availableActions()[len(s.availableActions())-1]
		return
	}
	s.focus = ExternalAgentMigrationFocusItems
	if len(s.items) > 0 {
		last := len(s.items) - 1
		s.selectedItemIndex = &last
	}
}

func (s *ExternalAgentConfigMigrationScreen) moveDown() {
	if s.view == ExternalAgentMigrationViewSummary {
		s.moveAction(1)
		return
	}
	if s.focus == ExternalAgentMigrationFocusItems {
		if s.selectedItemIndex != nil && *s.selectedItemIndex+1 < len(s.items) {
			next := *s.selectedItemIndex + 1
			s.selectedItemIndex = &next
			return
		}
		s.focus = ExternalAgentMigrationFocusActions
		s.highlightedAction = s.availableActions()[0]
		return
	}
	s.focus = ExternalAgentMigrationFocusItems
	if len(s.items) > 0 {
		zero := 0
		s.selectedItemIndex = &zero
	}
}

func (s *ExternalAgentConfigMigrationScreen) moveAction(delta int) {
	actions := s.availableActions()
	if len(actions) == 0 {
		return
	}
	idx := 0
	for i, action := range actions {
		if action == s.highlightedAction {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(actions)) % len(actions)
	s.highlightedAction = actions[idx]
	s.focus = ExternalAgentMigrationFocusActions
}

func (s *ExternalAgentConfigMigrationScreen) selectNumberedAction(index int) {
	actions := s.availableActions()
	if index < 0 || index >= len(actions) {
		return
	}
	s.highlightedAction = actions[index]
	s.focus = ExternalAgentMigrationFocusActions
	s.confirmSelection()
}

func (s *ExternalAgentConfigMigrationScreen) confirmSelection() {
	if s.focus == ExternalAgentMigrationFocusItems {
		s.toggleSelectedItem()
		return
	}
	switch s.highlightedAction {
	case ExternalAgentMigrationActionProceed:
		s.proceed()
	case ExternalAgentMigrationActionCustomize:
		s.customize()
	case ExternalAgentMigrationActionSkip:
		s.skip()
	case ExternalAgentMigrationActionBack:
		s.backToSummary()
	}
}

func (s *ExternalAgentConfigMigrationScreen) summaryRows() []string {
	rows := []string{}
	for _, group := range s.groups {
		selected := []config.ExternalAgentConfigMigrationItem{}
		for _, idx := range group.ItemIndices {
			if idx >= 0 && idx < len(s.items) && s.items[idx].Enabled {
				selected = append(selected, s.items[idx].Item)
			}
		}
		summary := ExternalAgentConfigMigrationCountSummary(selected)
		if summary == "" {
			summary = "none"
		}
		rows = append(rows,
			"["+s.groupSelectionMarker(group)+"] "+group.Label,
			"    "+group.Description,
			"    Importing: "+summary,
		)
	}
	return rows
}

func (s *ExternalAgentConfigMigrationScreen) customizeRows() []string {
	rows := []string{}
	for idx, selection := range s.items {
		selected := s.focus == ExternalAgentMigrationFocusItems && s.selectedItemIndex != nil && *s.selectedItemIndex == idx
		row := SelectionPrefix(selected) + "[" + enabledMarker(selection.Enabled) + "] " + ExternalAgentConfigMigrationItemLabel(selection.Item)
		if selected {
			row = RenderSelectedRow(row)
		}
		rows = append(rows, row, "    "+ExternalAgentConfigMigrationDisplayDescription(selection.Item))
		if detail, ok := ExternalAgentConfigMigrationItemDetail(selection.Item); ok {
			rows = append(rows, "    "+detail)
		}
	}
	return rows
}

func (s *ExternalAgentConfigMigrationScreen) groupSelectionMarker(group ExternalAgentConfigMigrationGroupModel) string {
	enabled := 0
	for _, idx := range group.ItemIndices {
		if idx >= 0 && idx < len(s.items) && s.items[idx].Enabled {
			enabled++
		}
	}
	switch {
	case enabled == 0:
		return " "
	case enabled == len(group.ItemIndices):
		return "x"
	default:
		return "-"
	}
}

func ExternalAgentConfigMigrationDisplayDescription(item config.ExternalAgentConfigMigrationItem) string {
	description := strings.TrimSpace(item.Description)
	if rest, ok := strings.CutPrefix(description, "Migrate "); ok {
		description = "Import " + rest
	}
	if item.ItemType == config.MigrationPlugins && item.Details != nil && strings.HasPrefix(description, "Import enabled plugins from ") {
		marketplaceCount := len(item.Details.Plugins)
		pluginCount := 0
		for _, pluginGroup := range item.Details.Plugins {
			pluginCount += len(pluginGroup.PluginNames)
		}
		return description + " (" + pluralCount(marketplaceCount, "marketplace") + ", " + pluralCount(pluginCount, "plugin") + ")"
	}
	return description
}

func migrationItemSelected(item config.ExternalAgentConfigMigrationItem, selected []config.ExternalAgentConfigMigrationItem) bool {
	for _, candidate := range selected {
		if reflect.DeepEqual(item, candidate) {
			return true
		}
	}
	return false
}

func enabledMarker(enabled bool) string {
	if enabled {
		return "x"
	}
	return " "
}

func pluralCount(count int, noun string) string {
	suffix := "s"
	if count == 1 {
		suffix = ""
	}
	return strings.TrimSpace(strings.Join([]string{strconv.Itoa(count), noun + suffix}, " "))
}
