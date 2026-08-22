package tea

import (
	"os"
	"path/filepath"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
)

type statusLineSetupModal struct {
	items          []bottompane.StatusLineItem
	selected       map[string]bool
	useThemeColors bool
}

type terminalTitleSetupModal struct {
	items    []chatwidget.TerminalTitleItem
	selected map[string]bool
}

func terminalTitleWriterOrDefault(writer TerminalTitleWriterFunc) TerminalTitleWriterFunc {
	if writer != nil {
		return writer
	}
	return func(sequence string) bubbletea.Cmd {
		if sequence == "" {
			return nil
		}
		return bubbletea.Printf("%s", sequence)
	}
}

func (m *Model) applyStatusLineCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	args = strings.TrimSpace(args)
	if args == "" {
		m.openStatusLineSetup()
		return nil
	}
	if isStatusSetupDefaultArg(args) {
		m.ensureStatusControls()
		items, invalid := parseStatusLineItemsArg(strings.Join(chatwidget.DefaultStatusLineItems, " "))
		if len(invalid) > 0 {
			m.notice = "Unknown status line item: " + strings.Join(invalid, ", ")
			m.refreshTranscript()
			return nil
		}
		result := m.statusControls.SetupStatusLine(items, true)
		m.statusLineConfiguredByUser = true
		m.notice = "Status line reset to default: " + firstNonEmpty(result.StatusLineText, m.renderStatusHeader())
		m.refreshTranscript()
		return nil
	}
	items, invalid := parseStatusLineItemsArg(args)
	if len(invalid) > 0 {
		m.notice = "Unknown status line item: " + strings.Join(invalid, ", ")
		m.refreshTranscript()
		return nil
	}
	m.ensureStatusControls()
	result := m.statusControls.SetupStatusLine(items, true)
	m.statusLineConfiguredByUser = true
	m.notice = "Status line: " + firstNonEmpty(result.StatusLineText, "disabled")
	m.refreshTranscript()
	return nil
}

func (m *Model) applyTerminalTitleCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	args = strings.TrimSpace(args)
	if args == "" {
		return m.openTerminalTitleSetup()
	}
	if isStatusSetupDefaultArg(args) {
		m.ensureStatusControls()
		m.statusControls.TerminalTitleConfigured = false
		m.statusControls.TerminalTitleIDs = nil
		result := m.statusControls.RefreshTerminalTitle()
		m.notice = "Terminal title reset to default: " + firstNonEmpty(result.TerminalTitleText, "disabled")
		m.refreshTranscript()
		return m.applyTerminalTitleResult(result)
	}
	items, invalid := parseTerminalTitleItemsArg(args)
	if len(invalid) > 0 {
		m.notice = "Unknown terminal title item: " + strings.Join(invalid, ", ")
		m.refreshTranscript()
		return nil
	}
	m.ensureStatusControls()
	result := m.statusControls.SetupTerminalTitle(items)
	m.notice = "Terminal title: " + firstNonEmpty(result.TerminalTitleText, "disabled")
	m.refreshTranscript()
	return m.applyTerminalTitleResult(result)
}

func (m *Model) openStatusLineSetup() {
	if m == nil {
		return
	}
	m.ensureStatusControls()
	m.syncStatusControlsRuntime()
	selections := chatwidget.NewStatusSurfaceSelections(m.statusControls.ConfiguredStatusLineItems(), m.statusControls.ConfiguredTerminalTitleItems())
	view := chatwidget.NewStatusLineSetupView(selections.StatusLineItems, m.statusControls.StatusLineUseThemeColors, m.statusControls.StatusSurfacePreviewData())
	setup := &statusLineSetupModal{
		items:          chatwidget.AllStatusLineItems(),
		selected:       map[string]bool{},
		useThemeColors: view.UseThemeColors,
	}
	for _, item := range selections.StatusLineItems {
		setup.selected[chatwidget.StatusLineItemID(item)] = true
	}
	options := make([]ModalOption, 0, len(view.Items))
	for _, item := range view.Items {
		description := item.Description
		if strings.TrimSpace(item.Preview) != "" {
			description = item.Preview + " - " + description
		}
		options = append(options, ModalOption{ID: item.ID, Label: item.Name, Description: description})
	}
	m.modal = &modalState{
		id:              "status-line-setup",
		kind:            ModalKindStatusLine,
		title:           view.Title,
		body:            statusLineSetupBody(view.PreviewText),
		options:         options,
		statusLineSetup: setup,
	}
	m.notice = ""
}

func (m *Model) openTerminalTitleSetup() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.ensureStatusControls()
	m.syncStatusControlsRuntime()
	selections := chatwidget.NewStatusSurfaceSelections(m.statusControls.ConfiguredStatusLineItems(), m.statusControls.ConfiguredTerminalTitleItems())
	view := chatwidget.NewTerminalTitleSetupView(selections.TerminalTitleItems, m.statusControls.TerminalTitlePreviewData())
	setup := &terminalTitleSetupModal{
		items:    chatwidget.AllTerminalTitleItems(),
		selected: map[string]bool{},
	}
	for _, item := range selections.TerminalTitleItems {
		setup.selected[item.ID()] = true
	}
	options := make([]ModalOption, 0, len(view.Items))
	for _, item := range view.Items {
		description := item.Description
		if strings.TrimSpace(item.Preview) != "" {
			description = item.Preview + " - " + description
		}
		options = append(options, ModalOption{ID: item.ID, Label: item.Name, Description: description})
	}
	m.modal = &modalState{
		id:                 "terminal-title-setup",
		kind:               ModalKindTitle,
		title:              view.Title,
		body:               terminalTitleSetupBody(view.PreviewText),
		options:            options,
		terminalTitleSetup: setup,
	}
	m.notice = ""
	result := m.statusControls.PreviewTerminalTitle(selectedTerminalTitleItems(setup))
	return m.applyTerminalTitleResult(result)
}

func (m *Model) toggleStatusSetupSelection() bubbletea.Cmd {
	if m == nil || m.modal == nil || len(m.modal.options) == 0 {
		return nil
	}
	option := m.modal.options[m.modal.selected]
	if m.modal.statusLineSetup != nil {
		setup := m.modal.statusLineSetup
		setup.selected[option.ID] = !setup.selected[option.ID]
		line, ok := m.statusControls.StatusSurfacePreviewData().StatusLineForItems(selectedStatusLineItems(setup), setup.useThemeColors)
		preview := ""
		if ok {
			preview = line.PlainText()
		}
		m.modal.body = statusLineSetupBody(preview)
		return nil
	}
	if m.modal.terminalTitleSetup != nil {
		setup := m.modal.terminalTitleSetup
		setup.selected[option.ID] = !setup.selected[option.ID]
		items := selectedTerminalTitleItems(setup)
		preview, _ := chatwidget.PreviewLineForTitleItems(items, m.statusControls.TerminalTitlePreviewData())
		m.modal.body = terminalTitleSetupBody(preview)
		result := m.statusControls.PreviewTerminalTitle(items)
		return m.applyTerminalTitleResult(result)
	}
	return nil
}

func (m *Model) commitStatusLineSetup(setup *statusLineSetupModal) bubbletea.Cmd {
	if m == nil || setup == nil {
		return nil
	}
	m.ensureStatusControls()
	result := m.statusControls.SetupStatusLine(selectedStatusLineItems(setup), setup.useThemeColors)
	m.statusLineConfiguredByUser = true
	m.notice = "Status line: " + firstNonEmpty(result.StatusLineText, "disabled")
	m.refreshTranscript()
	return nil
}

func (m *Model) commitTerminalTitleSetup(setup *terminalTitleSetupModal) bubbletea.Cmd {
	if m == nil || setup == nil {
		return nil
	}
	m.ensureStatusControls()
	result := m.statusControls.SetupTerminalTitle(selectedTerminalTitleItems(setup))
	m.notice = "Terminal title: " + firstNonEmpty(result.TerminalTitleText, "disabled")
	m.refreshTranscript()
	return m.applyTerminalTitleResult(result)
}

func (m *Model) cancelTerminalTitleSetup() bubbletea.Cmd {
	if m == nil || m.statusControls == nil {
		return nil
	}
	result := m.statusControls.CancelTerminalTitleSetup()
	m.notice = "Cancelled"
	m.refreshTranscript()
	return m.applyTerminalTitleResult(result)
}

func setupOptionSelected(modal *modalState, id string) bool {
	if modal == nil {
		return false
	}
	if modal.statusLineSetup != nil {
		return modal.statusLineSetup.selected[id]
	}
	if modal.terminalTitleSetup != nil {
		return modal.terminalTitleSetup.selected[id]
	}
	return false
}

func selectedStatusLineItems(setup *statusLineSetupModal) []bottompane.StatusLineItem {
	if setup == nil {
		return nil
	}
	items := make([]bottompane.StatusLineItem, 0, len(setup.items))
	for _, item := range setup.items {
		if setup.selected[chatwidget.StatusLineItemID(item)] {
			items = append(items, item)
		}
	}
	return items
}

func selectedTerminalTitleItems(setup *terminalTitleSetupModal) []chatwidget.TerminalTitleItem {
	if setup == nil {
		return nil
	}
	items := make([]chatwidget.TerminalTitleItem, 0, len(setup.items))
	for _, item := range setup.items {
		if setup.selected[item.ID()] {
			items = append(items, item)
		}
	}
	return items
}

func statusLineSetupBody(preview string) string {
	preview = strings.TrimSpace(preview)
	if preview == "" {
		preview = "disabled"
	}
	return "Select which items to display in the status line.\nPreview: " + preview
}

func terminalTitleSetupBody(preview string) string {
	preview = strings.TrimSpace(preview)
	if preview == "" {
		preview = "disabled"
	}
	return "Select which items to display in the terminal title.\nPreview: " + preview
}

func parseStatusLineItemsArg(args string) ([]bottompane.StatusLineItem, []string) {
	if isStatusSetupOffArg(args) {
		return []bottompane.StatusLineItem{}, nil
	}
	parts := statusItemArgParts(args)
	items := make([]bottompane.StatusLineItem, 0, len(parts))
	var invalid []string
	for _, part := range parts {
		item, ok := chatwidget.ParseStatusLineItem(part)
		if !ok {
			invalid = append(invalid, part)
			continue
		}
		items = append(items, item)
	}
	return items, invalid
}

func parseTerminalTitleItemsArg(args string) ([]chatwidget.TerminalTitleItem, []string) {
	if isStatusSetupOffArg(args) {
		return []chatwidget.TerminalTitleItem{}, nil
	}
	parts := statusItemArgParts(args)
	items := make([]chatwidget.TerminalTitleItem, 0, len(parts))
	var invalid []string
	for _, part := range parts {
		item, ok := chatwidget.ParseTerminalTitleItem(part)
		if !ok {
			invalid = append(invalid, part)
			continue
		}
		items = append(items, item)
	}
	return items, invalid
}

func statusItemArgParts(args string) []string {
	return strings.FieldsFunc(args, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

func isStatusSetupDefaultArg(args string) bool {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "default", "defaults", "reset":
		return true
	default:
		return false
	}
}

func isStatusSetupOffArg(args string) bool {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "off", "none", "empty", "disable", "disabled":
		return true
	default:
		return false
	}
}

func (m *Model) ensureStatusControls() {
	if m == nil {
		return
	}
	if m.statusControls == nil {
		m.statusControls = chatwidget.NewStatusControlsState(m.statusControlsRuntime())
	}
	m.syncStatusControlsRuntime()
}

func (m *Model) syncStatusControlsRuntime() {
	if m == nil || m.statusControls == nil {
		return
	}
	m.statusControls.Runtime = m.statusControlsRuntime()
}

// modelSupportsFastMode reports whether the currently selected model exposes a
// Fast/priority service tier (#39999). An empty tier list (uncatalogued model)
// keeps the status value visible, while a catalogued model without a Fast tier
// hides it.
func modelSupportsFastMode(commands []bottompane.ServiceTierCommand) bool {
	if len(commands) == 0 {
		return true
	}
	for _, command := range commands {
		if strings.EqualFold(strings.TrimSpace(command.Name), "fast") ||
			strings.EqualFold(strings.TrimSpace(command.ID), "priority") {
			return true
		}
	}
	return false
}

func (m *Model) statusControlsRuntime() chatwidget.StatusControlsRuntime {
	cwd := ""
	if m != nil {
		cwd = strings.TrimSpace(m.sessionCWD)
		if cwd == "" && m.State != nil {
			cwd = strings.TrimSpace(m.State.CWD)
		}
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cwd = strings.TrimSpace(cwd)
	projectName := ""
	if cwd != "" {
		projectName = filepath.Base(cwd)
	}
	if m == nil || m.State == nil {
		return chatwidget.StatusControlsRuntime{CWD: cwd, ProjectName: projectName, ProjectRoot: cwd}
	}
	return chatwidget.StatusControlsRuntime{
		CWD:                cwd,
		ProjectName:        projectName,
		ProjectRoot:        cwd,
		ModelName:          strings.TrimSpace(m.State.Model),
		ReasoningEffort:    m.State.EffectiveReasoningEffort(),
		StatusText:         strings.TrimSpace(m.State.Status),
		Permissions:        strings.TrimSpace(m.State.Sandbox),
		ApprovalMode:       strings.TrimSpace(m.State.ApprovalPolicy),
		ThreadID:           strings.TrimSpace(m.State.ThreadID),
		RawOutput:          m.rawOutput,
		ModelSupportsFastMode: modelSupportsFastMode(m.serviceTierCommands),
		ThreadTitle:        strings.TrimSpace(m.State.ThreadID),
		TaskProgress:       m.goalTaskProgress(),
		CodexVersion:       "codex_go",
		RateLimitSnapshots: cloneRateLimitSnapshots(m.rateLimitSnapshots),
	}
}

func (m *Model) renderStatusHeader() string {
	if m == nil {
		return ""
	}
	result := m.refreshStatusControls()
	if m.statusLineConfiguredByUser && m.statusControls != nil && m.statusControls.StatusLineConfigured && result.StatusLineRendered && strings.TrimSpace(result.StatusLineText) != "" {
		return result.StatusLineText
	}
	if m.State != nil {
		return m.State.RenderStatusLine()
	}
	return ""
}

func (m *Model) refreshStatusControls() chatwidget.StatusSurfaceRefreshResult {
	if m == nil {
		return chatwidget.StatusSurfaceRefreshResult{}
	}
	m.ensureStatusControls()
	return m.statusControls.RefreshStatusSurfaces()
}

func (m *Model) refreshStatusControlsCmd() bubbletea.Cmd {
	result := m.refreshStatusControls()
	if m == nil || m.statusControls == nil || (!m.statusControls.TerminalTitleConfigured && !m.statusControls.TerminalTitleSetupActive) {
		return nil
	}
	return m.applyTerminalTitleResult(result)
}

func (m *Model) applyTerminalTitleResult(result chatwidget.StatusSurfaceRefreshResult) bubbletea.Cmd {
	if m == nil || m.terminalTitleWriter == nil {
		return nil
	}
	sequence := ""
	if result.TerminalTitleRendered {
		if osc, ok := codextui.TerminalTitleOSC(result.TerminalTitleText); ok {
			sequence = osc
		}
	}
	if sequence == "" && m.lastTerminalTitleSequence != "" {
		sequence = codextui.ClearTerminalTitleOSC()
	}
	if sequence == "" || sequence == m.lastTerminalTitleSequence {
		return nil
	}
	m.lastTerminalTitleSequence = sequence
	return m.terminalTitleWriter(sequence)
}

func cloneRateLimitSnapshots(values map[string]chatwidget.RateLimitSnapshot) map[string]chatwidget.RateLimitSnapshot {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]chatwidget.RateLimitSnapshot, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
