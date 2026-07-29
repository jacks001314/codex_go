package tea

import (
	"errors"
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/config"
	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

const externalAgentMigrationNoItemsMessage = "No compatible setup was found to import."

type externalAgentMigrationSource struct {
	migrationSource string
	label           string
	items           []config.ExternalAgentConfigMigrationItem
}

var externalAgentMigrationSources = []externalAgentMigrationSource{
	{migrationSource: "claude-code", label: "Claude Code"},
	{migrationSource: "cursor", label: "Cursor"},
}

type externalAgentMigrationModalState struct {
	screen          *codextui.ExternalAgentConfigMigrationScreen
	sources         []externalAgentMigrationSource
	sourceIndex     int
	migrationSource string
	detected        []config.ExternalAgentConfigMigrationItem
	selected        []config.ExternalAgentConfigMigrationItem
	busy            bool
}

func (m *Model) applyExternalAgentImportCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onDetectExternalAgent == nil {
		m.notice = "External agent config migration is unavailable in this runtime."
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	m.modal = &modalState{
		kind: ModalKindExternalImport,
		externalAgentMigration: &externalAgentMigrationModalState{
			busy: true,
		},
	}
	m.notice = ""
	detect := m.onDetectExternalAgent
	cwd := ""
	if m.State != nil {
		cwd = strings.TrimSpace(m.State.CWD)
	}
	if cwd == "" {
		cwd = strings.TrimSpace(m.sessionCWD)
	}
	return func() bubbletea.Msg {
		results := make([]ExternalAgentSourceDetectResult, 0, len(externalAgentMigrationSources))
		for _, source := range externalAgentMigrationSources {
			response, err := detect(cwd, source.migrationSource)
			results = append(results, ExternalAgentSourceDetectResult{
				MigrationSource: source.migrationSource,
				Label:           source.label,
				Response:        response,
				Err:             err,
			})
		}
		return ExternalAgentDetectResultMsg{Results: results}
	}
}

func (m *Model) applyExternalAgentDetectResult(message ExternalAgentDetectResultMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	sources := make([]externalAgentMigrationSource, 0, len(message.Results))
	errorsBySource := make([]string, 0, len(message.Results))
	rawErrors := make([]string, 0, len(message.Results))
	for _, result := range message.Results {
		if result.Err != nil {
			rawErrors = append(rawErrors, strings.TrimSpace(result.Err.Error()))
			label := strings.TrimSpace(result.Label)
			if label == "" {
				label = strings.TrimSpace(result.MigrationSource)
			}
			errorsBySource = append(errorsBySource, fmt.Sprintf("%s: %v", label, result.Err))
			continue
		}
		if len(result.Response.Items) == 0 {
			continue
		}
		sources = append(sources, externalAgentMigrationSource{
			migrationSource: result.MigrationSource,
			label:           result.Label,
			items:           append([]config.ExternalAgentConfigMigrationItem(nil), result.Response.Items...),
		})
	}
	if len(sources) == 0 && len(errorsBySource) > 0 {
		m.modal = nil
		m.notice = externalAgentDetectionErrorMessage(errorsBySource, rawErrors)
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	if len(sources) == 0 {
		m.modal = nil
		m.notice = externalAgentMigrationNoItemsMessage
		m.addInfoHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	state := &externalAgentMigrationModalState{sources: sources}
	m.modal = &modalState{kind: ModalKindExternalImport, externalAgentMigration: state}
	if len(sources) == 1 {
		state.selectSource(0)
	}
	return nil
}

func externalAgentDetectionErrorMessage(errorsBySource []string, rawErrors []string) string {
	if len(rawErrors) > 0 && strings.HasPrefix(rawErrors[0], "Import from other apps is unavailable") {
		allEqual := true
		for _, message := range rawErrors[1:] {
			if message != rawErrors[0] {
				allEqual = false
				break
			}
		}
		if allEqual {
			return rawErrors[0]
		}
	}
	return "Could not check for importable setup: " + strings.Join(errorsBySource, "; ")
}

func (s *externalAgentMigrationModalState) selectSource(index int) {
	if s == nil || index < 0 || index >= len(s.sources) {
		return
	}
	source := s.sources[index]
	s.sourceIndex = index
	s.migrationSource = source.migrationSource
	s.detected = append([]config.ExternalAgentConfigMigrationItem(nil), source.items...)
	s.screen = codextui.NewExternalAgentConfigMigrationScreen(s.detected, s.detected, "")
}

func (m *Model) updateExternalAgentMigrationModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.externalAgentMigration == nil {
		return nil
	}
	state := m.modal.externalAgentMigration
	if state.busy {
		return nil
	}
	if state.screen == nil {
		return m.updateExternalAgentSourceModal(state, message)
	}
	for _, key := range externalAgentMigrationKeyNames(message) {
		state.screen.HandleKey(key)
	}
	if !state.screen.IsDone() {
		return nil
	}
	outcome := state.screen.Outcome()
	if outcome.Kind == codextui.ExternalAgentConfigMigrationSkip {
		m.modal = nil
		m.refreshTranscript()
		return nil
	}
	if m.onImportExternalAgent == nil {
		state.screen = codextui.NewExternalAgentConfigMigrationScreen(state.detected, outcome.Items, "Import failed: external agent config import is unavailable in this runtime")
		return nil
	}
	state.busy = true
	state.selected = append([]config.ExternalAgentConfigMigrationItem(nil), outcome.Items...)
	importer := m.onImportExternalAgent
	selected := append([]config.ExternalAgentConfigMigrationItem(nil), outcome.Items...)
	return func() bubbletea.Msg {
		response, completion, err := importer(selected, state.migrationSource)
		return ExternalAgentImportResultMsg{Selected: selected, Source: state.migrationSource, Response: response, Completion: completion, Err: err}
	}
}

func (m *Model) updateExternalAgentSourceModal(state *externalAgentMigrationModalState, message bubbletea.KeyMsg) bubbletea.Cmd {
	if state == nil || len(state.sources) == 0 {
		return nil
	}
	keys := externalAgentMigrationKeyNames(message)
	for _, key := range keys {
		switch key {
		case "up", "k":
			if state.sourceIndex > 0 {
				state.sourceIndex--
			}
		case "down", "j":
			if state.sourceIndex+1 < len(state.sources) {
				state.sourceIndex++
			}
		case "enter":
			state.selectSource(state.sourceIndex)
			return nil
		case "esc", "ctrl-c", "ctrl-d":
			m.modal = nil
			m.refreshTranscript()
			return nil
		default:
			if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
				index := int(key[0] - '1')
				if index < len(state.sources) {
					state.selectSource(index)
					return nil
				}
			}
		}
	}
	return nil
}

func externalAgentMigrationKeyNames(message bubbletea.KeyMsg) []string {
	switch message.Type {
	case bubbletea.KeyRunes:
		keys := make([]string, 0, len(message.Runes))
		for _, value := range message.Runes {
			keys = append(keys, string(value))
		}
		return keys
	case bubbletea.KeyUp:
		return []string{"up"}
	case bubbletea.KeyDown:
		return []string{"down"}
	case bubbletea.KeyEnter:
		return []string{"enter"}
	case bubbletea.KeySpace:
		return []string{" "}
	case bubbletea.KeyEsc:
		return []string{"esc"}
	case bubbletea.KeyCtrlC:
		return []string{"ctrl-c"}
	case bubbletea.KeyCtrlD:
		return []string{"ctrl-d"}
	default:
		return nil
	}
}

func (m *Model) applyExternalAgentImportResult(message ExternalAgentImportResultMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.externalAgentMigration == nil {
		return nil
	}
	state := m.modal.externalAgentMigration
	if message.Err != nil {
		state.busy = false
		state.screen = codextui.NewExternalAgentConfigMigrationScreen(
			state.detected,
			message.Selected,
			"Import failed: "+message.Err.Error(),
		)
		return nil
	}
	if strings.TrimSpace(message.Response.ImportID) == "" || message.Completion == nil {
		state.busy = false
		state.screen = codextui.NewExternalAgentConfigMigrationScreen(
			state.detected,
			message.Selected,
			"Import failed: external agent config import did not start a completion stream",
		)
		return nil
	}
	m.modal = nil
	remaining := len(state.detected) - len(message.Selected)
	if remaining < 0 {
		remaining = 0
	}
	m.applyHistoryCell(historycell.NewPlainHistoryCell(externalAgentMigrationStartedLines(message.Selected, remaining)))
	if m.pendingExternalAgentImports == nil {
		m.pendingExternalAgentImports = map[string]bool{}
	}
	importID := strings.TrimSpace(message.Response.ImportID)
	m.pendingExternalAgentImports[importID] = true
	m.notice = "Import started."
	m.refreshTranscript()
	return waitForExternalAgentImportCompletion(importID, message.Completion)
}

func waitForExternalAgentImportCompletion(importID string, completion <-chan ExternalAgentImportCompletion) bubbletea.Cmd {
	return func() bubbletea.Msg {
		result, ok := <-completion
		if !ok {
			result.Err = errors.New("external agent config import completion stream closed")
		}
		return ExternalAgentImportCompletedResultMsg{ImportID: importID, Result: result}
	}
}

func (m *Model) applyExternalAgentImportCompletedResult(message ExternalAgentImportCompletedResultMsg) {
	if m == nil || !m.pendingExternalAgentImports[strings.TrimSpace(message.ImportID)] {
		return
	}
	delete(m.pendingExternalAgentImports, strings.TrimSpace(message.ImportID))
	if message.Result.Err != nil {
		m.notice = "Import failed: " + message.Result.Err.Error()
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return
	}
	completed := message.Result.Completed
	if completed.ImportID == "" {
		completed.ImportID = strings.TrimSpace(message.ImportID)
	}
	m.applyHistoryCell(historycell.NewPlainHistoryCell(externalAgentMigrationFinishedLines(completed)))
	m.notice = "Import finished."
	m.refreshTranscript()
}

func (m *Model) renderExternalAgentMigrationModal() string {
	if m == nil || m.modal == nil || m.modal.externalAgentMigration == nil {
		return ""
	}
	state := m.modal.externalAgentMigration
	if state.screen == nil {
		if !state.busy && len(state.sources) > 1 {
			return renderExternalAgentSourceModal(state)
		}
		return "> Import setup\nChecking for compatible setup..."
	}
	return strings.Join(state.screen.Rows(), "\n")
}

func renderExternalAgentSourceModal(state *externalAgentMigrationModalState) string {
	rows := []string{
		"Choose an import source",
		"",
		"  Select the app whose setup you want to import.",
		"",
	}
	for index, source := range state.sources {
		prefix := " "
		if index == state.sourceIndex {
			prefix = ">"
		}
		rows = append(rows, fmt.Sprintf("%s %d. %s", prefix, index+1, source.label))
	}
	return strings.Join(append(rows, "", "  Press Enter to continue"), "\n")
}

func externalAgentMigrationStartedLines(items []config.ExternalAgentConfigMigrationItem, remaining int) []string {
	lines := []string{
		"\u2022 Import started. You can keep working while it finishes.",
		"  Imported setup will apply to new chats.",
		"  Importing:",
	}
	for _, item := range items {
		line := fmt.Sprintf("    %s: %d", codextui.ExternalAgentConfigMigrationTypeLabel(item.ItemType), codextui.ExternalAgentConfigMigrationItemCount(item))
		if names := externalAgentMigrationItemNames(item); len(names) > 0 {
			shown := names
			if len(shown) > 3 {
				shown = shown[:3]
			}
			line += " - " + strings.Join(shown, ", ")
			if len(names) > len(shown) {
				line += fmt.Sprintf(", +%d more", len(names)-len(shown))
			}
		}
		lines = append(lines, line)
	}
	if remaining == 1 {
		lines = append(lines, "  1 additional item remains. After it finishes, run /import again to review it.")
	} else if remaining > 1 {
		lines = append(lines, fmt.Sprintf("  %d additional items remain. After it finishes, run /import again to review them.", remaining))
	}
	return lines
}

func externalAgentMigrationFinishedLines(completed config.ExternalAgentConfigImportCompletedNotification) []string {
	imported := 0
	failed := 0
	for _, result := range completed.ItemTypeResults {
		imported += len(result.Successes)
		failed += len(result.Failures)
	}
	lines := []string{fmt.Sprintf("\u2022 Import finished: %d imported, %d failed.", imported, failed)}
	if len(completed.ItemTypeResults) > 0 {
		lines = append(lines, "  Results by type:")
		for _, result := range completed.ItemTypeResults {
			lines = append(lines, fmt.Sprintf("    %s: %d imported, %d failed", codextui.ExternalAgentConfigMigrationTypeLabel(result.ItemType), len(result.Successes), len(result.Failures)))
		}
	}
	return append(lines, "  Run /import again to check for additional items.")
}

func externalAgentMigrationItemNames(item config.ExternalAgentConfigMigrationItem) []string {
	if item.Details == nil {
		return nil
	}
	names := []string{}
	switch item.ItemType {
	case config.MigrationPlugins:
		for _, group := range item.Details.Plugins {
			names = append(names, group.PluginNames...)
		}
	case config.MigrationSkills:
		for _, value := range item.Details.Skills {
			names = append(names, value.Name)
		}
	case config.MigrationMCPServerConfig:
		for _, value := range item.Details.MCPServers {
			names = append(names, value.Name)
		}
	case config.MigrationSubagents:
		for _, value := range item.Details.Subagents {
			names = append(names, value.Name)
		}
	case config.MigrationHooks:
		for _, value := range item.Details.Hooks {
			names = append(names, value.Name)
		}
	case config.MigrationCommands:
		for _, value := range item.Details.Commands {
			names = append(names, value.Name)
		}
	case config.MigrationMemory:
		for _, value := range item.Details.MemoryFiles {
			names = append(names, value.SourceFile)
		}
	case config.MigrationSessions:
		for _, value := range item.Details.Sessions {
			if value.Title != nil && strings.TrimSpace(*value.Title) != "" {
				names = append(names, strings.TrimSpace(*value.Title))
			}
		}
	}
	return names
}
