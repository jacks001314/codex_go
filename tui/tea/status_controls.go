package tea

import (
	"fmt"
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

// statusSurfaceItemArgs mirrors Rust #54387 (codex-rs/tui/src/chatwidget/
// status_surface_commands.rs) tokenizing for the two status-surface commands:
// positional items, the `--clear` switch, and -- for `/statusline` only -- the
// `colors` boolean setting Rust passes as `boolean_settings`.
type statusSurfaceItemArgs struct {
	items     []string
	clear     bool
	useColors *bool
}

// scanStatusSurfaceItemArgs tokenizes a status-surface argument string with the
// shared parser Rust uses for every slash command that takes inline arguments
// (codex-rs/tui/src/chatwidget/slash_command_args.rs:54-102). `/title` passes
// only the `--clear` switch; `/statusline` adds the `colors` boolean setting
// (status_surface_commands.rs:77 and :94). Because the shared grammar owns the
// tokenization, a bare `--` makes every later token positional, a repeated
// setting is rejected, and unbalanced quoting fails with Rust's quoting error.
func scanStatusSurfaceItemArgs(args string, allowColors bool) (statusSurfaceItemArgs, string) {
	var booleanSettings []string
	if allowColors {
		booleanSettings = []string{"colors"}
	}
	parsed, failure := parseSlashCommandArgs(args, nil, []string{"--clear"}, booleanSettings)
	if failure != "" {
		return statusSurfaceItemArgs{}, failure
	}
	result := statusSurfaceItemArgs{
		items: append([]string(nil), parsed.positionals...),
		clear: parsed.hasSwitch("--clear"),
	}
	if allowColors {
		if value, ok := parsed.boolean("colors"); ok {
			useColors := value
			result.useColors = &useColors
		}
	}
	return result, ""
}

// statusSurfaceUnknownItemNotice mirrors Rust #54387's
// "Unknown <surface> item '<token>'. Use <command> to see available items."
func statusSurfaceUnknownItemNotice(surface string, command string, token string) string {
	return fmt.Sprintf("Unknown %s item '%s'. Use %s to see available items.", surface, token, command)
}

// applyStatusLineThemeColors mirrors Rust #54387's `colors` boolean setting: a
// colour-only update keeps the selected items and only flips the preference.
// statusSurfaceOverrides mirrors Rust #54387's session-only status-surface
// selections (codex-rs/tui/src/chatwidget/status_surface_commands.rs:11-21).
// `/title` and `/statusline` record what the user asked for in this process
// without changing saved defaults, and the record is re-applied when the thread's
// state is restored (chatwidget/input_restore.rs:601,621 restores the per-thread
// input state, overrides included). Saving through the status editors supersedes
// the override (chatwidget/status_controls.rs:136-137,180) and a saved settings
// write clears the colour override (chatwidget/config_panel.rs:103).
//
// Go keeps a single in-process slot keyed by thread id: the overrides follow the
// thread they were recorded on, but switching away and back loses them, because
// the per-thread input state lives in the app layer (see the lane contract).
type statusSurfaceOverrides struct {
	threadID            string
	terminalTitle       []string
	terminalTitleSet    bool
	statusLine          []string
	statusLineSet       bool
	statusLineUseColors *bool
}

func (o *statusSurfaceOverrides) recordTerminalTitle(threadID string, ids []string) {
	if o == nil {
		return
	}
	o.threadID = strings.TrimSpace(threadID)
	o.terminalTitle = append([]string(nil), ids...)
	o.terminalTitleSet = true
}

func (o *statusSurfaceOverrides) recordStatusLine(threadID string, ids []string) {
	if o == nil {
		return
	}
	o.threadID = strings.TrimSpace(threadID)
	o.statusLine = append([]string(nil), ids...)
	o.statusLineSet = true
}

func (o *statusSurfaceOverrides) recordStatusLineUseColors(threadID string, value bool) {
	if o == nil {
		return
	}
	o.threadID = strings.TrimSpace(threadID)
	o.statusLineUseColors = &value
}

// clearStatusLine records that a saved status-line selection supersedes the
// session override.
func (o *statusSurfaceOverrides) clearStatusLine() {
	if o == nil {
		return
	}
	o.statusLine = nil
	o.statusLineSet = false
}

// clearTerminalTitle records that a saved terminal-title selection supersedes
// the session override.
func (o *statusSurfaceOverrides) clearTerminalTitle() {
	if o == nil {
		return
	}
	o.terminalTitle = nil
	o.terminalTitleSet = false
}

// clearStatusLineUseColors records that a saved colour preference supersedes the
// session override.
func (o *statusSurfaceOverrides) clearStatusLineUseColors() {
	if o == nil {
		return
	}
	o.statusLineUseColors = nil
}

func (o *statusSurfaceOverrides) empty() bool {
	if o == nil {
		return true
	}
	return !o.terminalTitleSet && !o.statusLineSet && o.statusLineUseColors == nil
}

// matchesThread reports whether the recorded overrides belong to threadID. An
// empty record or an empty thread id never matches.
func (o *statusSurfaceOverrides) matchesThread(threadID string) bool {
	if o == nil || o.empty() {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	return threadID != "" && threadID == o.threadID
}

// restoreStatusSurfaceOverrides re-applies the session-only selections recorded
// by `/title` and `/statusline` after a thread restore reseeded the status
// surfaces from saved settings (Rust chatwidget/input_restore.rs:601,621).
// Overrides recorded on another thread stay put, so a thread the user has not
// overridden in this process keeps its saved layout. The terminal title state is
// restored without re-emitting the OSC here: `applyResumeResponse` has no command
// outlet, and the next status refresh writes the restored title.
func (m *Model) restoreStatusSurfaceOverrides(threadID string) {
	if m == nil || !m.statusSurfaceOverrides.matchesThread(threadID) {
		return
	}
	overrides := m.statusSurfaceOverrides
	if overrides.statusLineUseColors != nil {
		m.applyStatusLineThemeColors(*overrides.statusLineUseColors)
	}
	m.ensureStatusControls()
	if overrides.terminalTitleSet {
		items, _ := parseTerminalTitleItemsArgTokens(overrides.terminalTitle)
		m.statusControls.SetupTerminalTitle(items)
	}
	if overrides.statusLineSet {
		items, _ := parseStatusLineItemsArgTokens(overrides.statusLine)
		m.statusControls.SetupStatusLine(items, m.statusLineUseColors)
		m.statusLineConfiguredByUser = true
	}
}

func (m *Model) applyStatusLineThemeColors(useColors bool) {
	m.statusLineUseColors = useColors
	if m.statusControls != nil {
		m.statusControls.StatusLineUseThemeColors = useColors
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
			m.notice = statusSurfaceUnknownItemNotice("status-line", "/statusline", invalid[0])
			m.refreshTranscript()
			return nil
		}
		result := m.statusControls.SetupStatusLine(items, m.statusLineUseColors)
		m.statusLineConfiguredByUser = true
		m.statusSurfaceOverrides.recordStatusLine(m.currentThreadID(), chatwidget.StatusLineItemIDs(items))
		m.notice = "Status line reset to default: " + firstNonEmpty(result.StatusLineText, m.renderStatusHeader())
		m.refreshTranscript()
		return nil
	}
	parsed, argErr := scanStatusSurfaceItemArgs(args, true)
	if argErr != "" {
		m.notice = argErr
		m.refreshTranscript()
		return nil
	}
	// Go compatibility: the bare off/none/disable forms clear the items exactly
	// like Rust's `--clear` switch.
	if isStatusSetupOffArg(args) {
		parsed.clear = true
		parsed.items = nil
	}
	// Rust resolves every positional item before it inspects `--clear`
	// (status_surface_commands.rs:94-104 with parse_item at :122-160), so
	// `--clear nope` reports the unknown item instead of the combination error.
	items, invalid := parseStatusLineItemsArgTokens(parsed.items)
	if len(invalid) > 0 {
		m.notice = statusSurfaceUnknownItemNotice("status-line", "/statusline", invalid[0])
		m.refreshTranscript()
		return nil
	}
	if parsed.clear && len(items) > 0 {
		m.notice = "Cannot combine --clear with status-line items."
		m.refreshTranscript()
		return nil
	}
	if parsed.useColors != nil {
		m.applyStatusLineThemeColors(*parsed.useColors)
		m.statusSurfaceOverrides.recordStatusLineUseColors(m.currentThreadID(), *parsed.useColors)
	}
	if parsed.clear {
		m.ensureStatusControls()
		result := m.statusControls.SetupStatusLine([]bottompane.StatusLineItem{}, m.statusLineUseColors)
		m.statusLineConfiguredByUser = true
		// Rust records `Some(vec![])` for --clear, so the empty selection is an
		// explicit session choice rather than a missing override.
		m.statusSurfaceOverrides.recordStatusLine(m.currentThreadID(), []string{})
		m.notice = "Status line: " + firstNonEmpty(result.StatusLineText, "disabled")
		m.refreshTranscript()
		return nil
	}
	if len(items) == 0 {
		// Rust #54387: `--colors`/`--no-colors` without items preserves the
		// current selection and only updates the colour preference.
		m.refreshTranscript()
		return nil
	}
	m.ensureStatusControls()
	result := m.statusControls.SetupStatusLine(items, m.statusLineUseColors)
	m.statusLineConfiguredByUser = true
	m.statusSurfaceOverrides.recordStatusLine(m.currentThreadID(), chatwidget.StatusLineItemIDs(items))
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
	// Rust #54387 passes switches=["--clear"] for `/title` and no boolean
	// settings (status_surface_commands.rs:77), so `--colors` is an unknown
	// setting here.
	parsed, argErr := scanStatusSurfaceItemArgs(args, false)
	if argErr != "" {
		m.notice = argErr
		m.refreshTranscript()
		return nil
	}
	if isStatusSetupOffArg(args) {
		parsed.clear = true
		parsed.items = nil
	}
	// Rust resolves every positional item before it inspects `--clear`
	// (status_surface_commands.rs:73-88).
	items, invalid := parseTerminalTitleItemsArgTokens(parsed.items)
	if len(invalid) > 0 {
		m.notice = statusSurfaceUnknownItemNotice("terminal-title", "/title", invalid[0])
		m.refreshTranscript()
		return nil
	}
	if parsed.clear && len(items) > 0 {
		m.notice = "Cannot combine --clear with terminal-title items."
		m.refreshTranscript()
		return nil
	}
	if parsed.clear {
		// Rust records `Some(vec![])` for --clear.
		items = []chatwidget.TerminalTitleItem{}
		m.statusSurfaceOverrides.recordTerminalTitle(m.currentThreadID(), []string{})
	}
	m.ensureStatusControls()
	result := m.statusControls.SetupTerminalTitle(items)
	m.statusSurfaceOverrides.recordTerminalTitle(m.currentThreadID(), chatwidget.TerminalTitleItemIDs(items))
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
	// Rust #54387 (chatwidget/status_controls.rs:136-137): a saved editor
	// selection supersedes the session-only override.
	m.statusSurfaceOverrides.clearStatusLine()
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
	// Rust #54387 (chatwidget/status_controls.rs:180): a saved editor selection
	// supersedes the session-only override.
	m.statusSurfaceOverrides.clearTerminalTitle()
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

func parseStatusLineItemsArgTokens(tokens []string) ([]bottompane.StatusLineItem, []string) {
	items := make([]bottompane.StatusLineItem, 0, len(tokens))
	var invalid []string
	for _, token := range tokens {
		item, ok := chatwidget.ParseStatusLineItem(token)
		if !ok {
			invalid = append(invalid, token)
			continue
		}
		items = append(items, item)
	}
	return items, invalid
}

func parseTerminalTitleItemsArgTokens(tokens []string) ([]chatwidget.TerminalTitleItem, []string) {
	items := make([]chatwidget.TerminalTitleItem, 0, len(tokens))
	var invalid []string
	for _, token := range tokens {
		item, ok := chatwidget.ParseTerminalTitleItem(token)
		if !ok {
			invalid = append(invalid, token)
			continue
		}
		items = append(items, item)
	}
	return items, invalid
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
		// The configured `tui.status_line_use_colors` seeds the status line and
		// its setup preview (Rust #44857).
		m.statusControls.StatusLineUseThemeColors = m.statusLineUseColors
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
		CWD:                    cwd,
		ProjectName:            projectName,
		ProjectRoot:            cwd,
		ModelName:              m.modelDisplayName(m.State.Model),
		ReasoningEffort:        m.State.EffectiveReasoningEffort(),
		StatusText:             strings.TrimSpace(m.State.Status),
		Permissions:            strings.TrimSpace(m.State.Sandbox),
		ApprovalMode:           strings.TrimSpace(m.State.ApprovalPolicy),
		ThreadID:               strings.TrimSpace(m.State.ThreadID),
		RawOutput:              m.rawOutput,
		DaybreakEnabled:        m.daybreakEnabled,
		SideConversationActive: m.inSideConversation(),
		ModelSupportsFastMode:  modelSupportsFastMode(m.serviceTierCommands),
		ThreadTitle:            strings.TrimSpace(m.State.ThreadID),
		TaskProgress:           m.goalTaskProgress(),
		CodexVersion:           "codex_go",
		RateLimitSnapshots:     cloneRateLimitSnapshots(m.rateLimitSnapshots),
	}
}

// modelDisplayName resolves the catalog display name for a model id and falls
// back to the id itself when the catalog does not list the model (Rust #46503).
// Selection and persistence keep using the id; only display surfaces use the
// resolved label.
func (m *Model) modelDisplayName(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ""
	}
	if m == nil {
		return modelID
	}
	for _, options := range [][]codextui.ModelPickerOption{m.modelPickerOpts, m.modelCatalogOpts} {
		for _, option := range options {
			if strings.TrimSpace(option.ID) != modelID {
				continue
			}
			if label := strings.TrimSpace(option.Label); label != "" {
				return label
			}
		}
	}
	return modelID
}

func (m *Model) renderStatusHeader() string {
	if m == nil {
		return ""
	}
	result := m.refreshStatusControls()
	if m.statusLineConfiguredByUser && m.statusControls != nil && m.statusControls.StatusLineConfigured && result.StatusLineRendered && strings.TrimSpace(result.StatusLineText) != "" {
		if m.statusLineUseColors {
			threadColor := ""
			if threadID := strings.TrimSpace(m.State.ThreadID); threadID != "" {
				threadColor = codextui.ThreadColorSGR(codextui.ThreadColorForTheme(threadID, m.tuiTheme))
			}
			return result.StatusLine.RenderStyled(threadColor, func(accent bottompane.StatusLineAccent) string {
				return statusLineAccentSGR(m.tuiTheme, accent)
			}, true)
		}
		return result.StatusLineText
	}
	if m.State != nil {
		// Rust's status line shows the model's catalog display name.
		return m.State.RenderStatusLineWithModelName(m.modelDisplayName(m.State.Model))
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
