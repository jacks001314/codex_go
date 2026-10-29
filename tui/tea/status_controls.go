package tea

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codex_go/config"
	"codex_go/features"
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

func terminalSequenceWriterOrDefault(writer TerminalTitleWriterFunc) TerminalTitleWriterFunc {
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
// statusSurfaceOverrideRecord mirrors Rust #54387's StatusSurfaceOverrides
// (codex-rs/tui/src/chatwidget/status_surface_commands.rs:11-21): the
// session-only selections recorded for one thread.
type statusSurfaceOverrideRecord struct {
	terminalTitle       []string
	terminalTitleSet    bool
	statusLine          []string
	statusLineSet       bool
	statusLineUseColors *bool
}

func (r *statusSurfaceOverrideRecord) empty() bool {
	if r == nil {
		return true
	}
	return !r.terminalTitleSet && !r.statusLineSet && r.statusLineUseColors == nil
}

// statusSurfaceOverrides keeps the session-only selections for every thread,
// exactly where Rust carries them: inside the per-thread ThreadInputState
// (chatwidget/input_restore.rs:601 captures, :621 replays). A single slot would
// forget thread A's selection as soon as thread B set its own, so switching
// away and back must find each thread's record intact.
//
// Saving through the status editors supersedes the current thread's override
// (chatwidget/status_controls.rs:136-137,180) and a saved settings write clears
// the current thread's colour override (chatwidget/config_panel.rs:103).
type statusSurfaceOverrides struct {
	byThread map[string]*statusSurfaceOverrideRecord
}

// existing returns the thread's record without creating one.
func (o *statusSurfaceOverrides) existing(threadID string) *statusSurfaceOverrideRecord {
	if o == nil {
		return nil
	}
	return o.byThread[strings.TrimSpace(threadID)]
}

// record returns the thread's record, creating it when needed. An empty thread
// id has no thread-scoped state to attach to.
func (o *statusSurfaceOverrides) record(threadID string) *statusSurfaceOverrideRecord {
	threadID = strings.TrimSpace(threadID)
	if o == nil || threadID == "" {
		return nil
	}
	if o.byThread == nil {
		o.byThread = map[string]*statusSurfaceOverrideRecord{}
	}
	record := o.byThread[threadID]
	if record == nil {
		record = &statusSurfaceOverrideRecord{}
		o.byThread[threadID] = record
	}
	return record
}

// lookup returns the thread's non-empty record, if it has one.
func (o *statusSurfaceOverrides) lookup(threadID string) *statusSurfaceOverrideRecord {
	record := o.existing(threadID)
	if record == nil || record.empty() {
		return nil
	}
	return record
}

func (o *statusSurfaceOverrides) recordTerminalTitle(threadID string, ids []string) {
	record := o.record(threadID)
	if record == nil {
		return
	}
	record.terminalTitle = append([]string(nil), ids...)
	record.terminalTitleSet = true
}

func (o *statusSurfaceOverrides) recordStatusLine(threadID string, ids []string) {
	record := o.record(threadID)
	if record == nil {
		return
	}
	record.statusLine = append([]string(nil), ids...)
	record.statusLineSet = true
}

func (o *statusSurfaceOverrides) recordStatusLineUseColors(threadID string, value bool) {
	record := o.record(threadID)
	if record == nil {
		return
	}
	record.statusLineUseColors = &value
}

// clearStatusLine records that a saved status-line selection supersedes the
// thread's session override.
func (o *statusSurfaceOverrides) clearStatusLine(threadID string) {
	if record := o.existing(threadID); record != nil {
		record.statusLine = nil
		record.statusLineSet = false
	}
}

// clearTerminalTitle records that a saved terminal-title selection supersedes
// the thread's session override.
func (o *statusSurfaceOverrides) clearTerminalTitle(threadID string) {
	if record := o.existing(threadID); record != nil {
		record.terminalTitle = nil
		record.terminalTitleSet = false
	}
}

// clearStatusLineUseColors records that a saved colour preference supersedes
// the thread's session override.
func (o *statusSurfaceOverrides) clearStatusLineUseColors(threadID string) {
	if record := o.existing(threadID); record != nil {
		record.statusLineUseColors = nil
	}
}

func (o *statusSurfaceOverrides) empty() bool {
	if o == nil {
		return true
	}
	for _, record := range o.byThread {
		if !record.empty() {
			return false
		}
	}
	return true
}

// matchesThread reports whether threadID has a non-empty record.
func (o *statusSurfaceOverrides) matchesThread(threadID string) bool {
	return o.lookup(threadID) != nil
}

// restoreStatusSurfaceOverrides re-applies the session-only selections recorded
// by `/title` and `/statusline` after a thread restore reseeded the status
// surfaces from saved settings (Rust chatwidget/input_restore.rs:601,621).
// Overrides recorded on another thread stay put, so a thread the user has not
// overridden in this process keeps its saved layout. The terminal title state is
// restored without re-emitting the OSC here: `applyResumeResponse` has no command
// outlet, and the next status refresh writes the restored title.
func (m *Model) restoreStatusSurfaceOverrides(threadID string) {
	if m == nil {
		return
	}
	overrides := m.statusSurfaceOverrides.lookup(threadID)
	if overrides == nil {
		return
	}
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
		return m.refreshStatusControlsCmd()
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
	return m.refreshStatusControlsCmd()
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
		result := m.refreshStatusControls()
		m.notice = "Terminal title reset to default: " + firstNonEmpty(result.TerminalTitleText, "disabled")
		m.refreshTranscript()
		return m.refreshStatusControlsCmd()
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
	return m.refreshStatusControlsCmd()
}

func (m *Model) openStatusLineSetup() {
	if m == nil {
		return
	}
	m.ensureStatusControls()
	m.syncStatusControlsRuntime()
	selections := chatwidget.NewStatusSurfaceSelections(m.statusControls.ConfiguredStatusLineItems(), m.statusControls.ConfiguredTerminalTitleItems())
	previewData := m.statusControls.StatusSurfacePreviewData()
	view := chatwidget.NewStatusLineSetupView(selections.StatusLineItems, m.statusControls.StatusLineUseThemeColors, previewData)
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
	m.replaceModal(&modalState{
		id:              "status-line-setup",
		kind:            ModalKindStatusLine,
		title:           view.Title,
		body:            statusLineSetupBody(m.statusLinePreviewStyled(previewData, selections.StatusLineItems, view.UseThemeColors)),
		options:         options,
		statusLineSetup: setup,
	})
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
	m.replaceModal(&modalState{
		id:                 "terminal-title-setup",
		kind:               ModalKindTitle,
		title:              view.Title,
		body:               terminalTitleSetupBody(view.PreviewText),
		options:            options,
		terminalTitleSetup: setup,
	})
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
		preview := m.statusLinePreviewStyled(m.statusControls.StatusSurfacePreviewData(), selectedStatusLineItems(setup), setup.useThemeColors)
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
	m.statusSurfaceOverrides.clearStatusLine(m.currentThreadID())
	m.notice = "Status line: " + firstNonEmpty(result.StatusLineText, "disabled")
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
}

func (m *Model) commitTerminalTitleSetup(setup *terminalTitleSetupModal) bubbletea.Cmd {
	if m == nil || setup == nil {
		return nil
	}
	m.ensureStatusControls()
	result := m.statusControls.SetupTerminalTitle(selectedTerminalTitleItems(setup))
	// Rust #54387 (chatwidget/status_controls.rs:180): a saved editor selection
	// supersedes the session-only override.
	m.statusSurfaceOverrides.clearTerminalTitle(m.currentThreadID())
	m.notice = "Terminal title: " + firstNonEmpty(result.TerminalTitleText, "disabled")
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
}

func (m *Model) cancelTerminalTitleSetup() bubbletea.Cmd {
	if m == nil || m.statusControls == nil {
		return nil
	}
	m.statusControls.CancelTerminalTitleSetup()
	m.notice = "Cancelled"
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
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
	projectRoot := ""
	projectName := ""
	if cwd != "" {
		// Rust's status-line and terminal-title project name is the repository
		// root, not the basename of a nested working directory. Fall back to the
		// cwd when no project root marker is present.
		projectRoot = strings.TrimSpace(config.ActiveProjectRoot(cwd))
		if projectRoot == "" {
			projectRoot = cwd
		}
		projectName = filepath.Base(projectRoot)
	}
	if m == nil || m.State == nil {
		return chatwidget.StatusControlsRuntime{CWD: cwd, ProjectName: projectName, ProjectRoot: projectRoot}
	}
	runtimeUsage := m.currentThreadUsageEstimate()
	return chatwidget.StatusControlsRuntime{
		CWD:                           cwd,
		ProjectName:                   projectName,
		ProjectRoot:                   projectRoot,
		ModelName:                     m.modelDisplayName(m.State.Model),
		ReasoningEffort:               m.State.EffectiveReasoningEffort(),
		StatusText:                    strings.TrimSpace(m.State.Status),
		Permissions:                   strings.TrimSpace(m.State.Sandbox),
		ApprovalMode:                  strings.TrimSpace(m.State.ApprovalPolicy),
		ThreadID:                      strings.TrimSpace(m.State.ThreadID),
		Profile:                       strings.TrimSpace(m.profile),
		ModelProviderID:               strings.TrimSpace(m.State.Provider),
		RawOutput:                     m.rawOutput,
		DaybreakEnabled:               m.daybreakEnabled,
		SideConversationActive:        m.inSideConversation(),
		ProcessingTier:                m.processingTierDisplayName(),
		ThreadName:                    strings.TrimSpace(m.State.ThreadName),
		ThreadTitle:                   strings.TrimSpace(m.State.ThreadName),
		TaskProgress:                  m.goalTaskProgress(),
		CodexVersion:                  "codex_go",
		RateLimitSnapshots:            cloneRateLimitSnapshots(m.rateLimitSnapshots),
		PlanType:                      m.chatGPTPlanType,
		ThreadUsageCreditsMicros:      threadUsageCreditsMicros(runtimeUsage),
		ThreadUsageEstimatedUSDMicros: threadUsageEstimatedUSDMicros(runtimeUsage),
		ThreadUsageNativeUSDMicros:    threadUsageNativeUSDMicros(runtimeUsage),
		ThreadUsageAvailable:          runtimeUsage != nil,
	}
}

// processingTierDisplayName mirrors Rust #57265: the catalog's tier name wins,
// with Flex as the non-Bedrock fallback. An unset/default tier renders as empty;
// the status item itself falls back to "Standard".
func (m *Model) processingTierDisplayName() string {
	if m == nil || m.State == nil {
		return ""
	}
	id := strings.TrimSpace(m.State.ServiceTier)
	if id == "" || id == codextui.ServiceTierDefaultRequestValue {
		return ""
	}
	for _, command := range m.serviceTierCommands {
		if strings.TrimSpace(command.ID) == id {
			if name := strings.TrimSpace(command.Name); name != "" {
				return name
			}
		}
	}
	provider := strings.ToLower(strings.TrimSpace(m.State.Provider))
	if id == codextui.ServiceTierFlexRequestValue && !strings.HasPrefix(provider, "amazon-bedrock") {
		return "Flex"
	}
	return ""
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
	base := ""
	if result.StatusLineRendered {
		accent := func(value bottompane.StatusLineAccent) string {
			return statusLineAccentSGR(m.tuiTheme, value)
		}
		threadColor := ""
		if threadID := strings.TrimSpace(m.State.ThreadID); threadID != "" {
			threadColor = codextui.ThreadColorSGR(codextui.ThreadColorForTheme(threadID, m.tuiTheme))
		}
		base = result.StatusLine.RenderStyled(threadColor, accent, false)
	}
	return m.attachPlanModeStatusIndicator(base)
}

// attachPlanModeStatusIndicator mirrors Rust #49037's fullscreen status
// surface: an idle, editable composer keeps the mode's cycle hint at the far
// right while it fits, then degrades to the compact "Plan mode" label.
func (m *Model) attachPlanModeStatusIndicator(base string) string {
	if m == nil || m.State == nil || !m.State.PlanMode {
		return base
	}
	if !features.Enabled(m.featureSettings, "collaboration_modes") {
		return base
	}
	if !m.planModeCycleHintAvailable() {
		return rightAlignStatusLine(base, []string{bottompane.CollaborationModeLabel(bottompane.CollaborationModePlan, false)}, m.width)
	}
	return rightAlignStatusLine(base, []string{
		bottompane.CollaborationModeLabel(bottompane.CollaborationModePlan, true),
		bottompane.CollaborationModeLabel(bottompane.CollaborationModePlan, false),
	}, m.width)
}

func (m *Model) planModeCycleHintAvailable() bool {
	if m == nil || m.readOnlyThread || m.misalignmentPolicyStopped || m.isTaskRunning() {
		return false
	}
	if m.modal != nil || m.slashPopup.Active || m.skillPopup.Active || m.mentionPopup != nil || m.vimSearchMode {
		return false
	}
	if m.editorActive || m.windowsSandboxSetupActive || m.warningsView != nil || m.quickDialMenu {
		return false
	}
	if m.mainFind.active() || m.activityFocus.active || m.escBacktrackHint || m.asyncQuestions.Expanded() {
		return false
	}
	return true
}

func rightAlignStatusLine(base string, labels []string, width int) string {
	if len(labels) == 0 {
		return base
	}
	if width <= 0 {
		return strings.TrimSpace(base + " " + labels[0])
	}
	baseWidth := lipgloss.Width(base)
	for _, label := range labels {
		labelWidth := lipgloss.Width(label)
		if labelWidth > width {
			continue
		}
		if base == "" || baseWidth+2+labelWidth <= width {
			return base + strings.Repeat(" ", max(width-baseWidth-labelWidth, 0)) + label
		}
	}
	compact := labels[len(labels)-1]
	compactWidth := lipgloss.Width(compact)
	if compactWidth <= width {
		return strings.Repeat(" ", width-compactWidth) + compact
	}
	return fitTerminalLine(compact, width)
}

func (m *Model) refreshStatusControls() chatwidget.StatusSurfaceRefreshResult {
	if m == nil {
		return chatwidget.StatusSurfaceRefreshResult{}
	}
	m.ensureStatusControls()
	m.syncTerminalTitleAnimation()
	return m.statusControls.RefreshStatusSurfaces()
}

func (m *Model) refreshStatusControlsCmd() bubbletea.Cmd {
	result := m.refreshStatusControls()
	cmds := []bubbletea.Cmd{}
	if m != nil {
		cmds = append(cmds, m.applyTerminalTitleResult(result))
		cmds = append(cmds, m.ensureThreadUsageForSurfaces(result.Selections))
	}
	return bubbletea.Batch(cmds...)
}

// terminalTitleStatusCmd updates the animated terminal-title pieces. Rust
// gates the spinner with `animations && effects.progress` and the
// action-required blink with `animations && effects.title`, while a blocked
// request keeps the static action-required prefix when either effect is off.
func (m *Model) terminalTitleStatusCmd() bubbletea.Cmd {
	if m == nil || m.statusControls == nil || m.terminalTitleWriter == nil {
		return nil
	}
	if !m.syncTerminalTitleAnimation() {
		return nil
	}
	return m.applyTerminalTitleResult(m.statusControls.RefreshTerminalTitle())
}

// syncTerminalTitleAnimation updates the time-varying title state and reports
// whether it changed. The static title is emitted at startup and whenever a
// status surface refreshes; animation ticks only need to write when a spinner
// frame or action-required blink phase actually changes.
func (m *Model) syncTerminalTitleAnimation() bool {
	if m == nil || m.statusControls == nil {
		return false
	}
	status := m.desiredProgramStatus()
	blocked := status.State == codextui.ProgramStatusBlocked
	now := m.currentTime()
	if m.terminalTitleAnimationOrigin.IsZero() {
		m.terminalTitleAnimationOrigin = now
	}
	spinner := ""
	if !blocked && m.isTaskRunning() && m.animationsEnabled && m.effects.Progress {
		spinner = chatwidget.TerminalTitleSpinnerFrameAt(m.terminalTitleAnimationOrigin, now)
	}

	hidden := false
	if blocked && m.animationsEnabled && m.effects.Title {
		elapsed := now.Sub(m.terminalTitleAnimationOrigin)
		hidden = (elapsed/time.Second)%2 == 1
	}

	if m.terminalTitleAnimationReady &&
		blocked == m.terminalTitleActionRequired &&
		spinner == m.terminalTitleSpinnerText &&
		hidden == m.terminalTitleActionHidden {
		return false
	}
	m.terminalTitleAnimationReady = true
	m.terminalTitleActionRequired = blocked
	m.terminalTitleSpinnerText = spinner
	m.terminalTitleActionHidden = hidden
	m.statusControls.TerminalTitleActionRequired = blocked
	m.statusControls.TerminalTitleSpinnerText = spinner
	m.statusControls.TerminalTitleActionHidden = hidden
	return true
}

// animEngineTick returns the shared animation tick, defaulting to zero when the
// engine is absent (tests and embedded models).
func (m *Model) animEngineTick() int {
	if m == nil || m.animEngine == nil {
		return 0
	}
	return m.animEngine.CurrentTick()
}

// writeTerminalTitleSequence emits a title update through the same output lock
// as rendered frames. Bubble Tea suppresses its Printf command while the
// alternate screen is active, but Rust manages the terminal title in both
// screen modes.
func (m *Model) writeTerminalTitleSequence(sequence string) bubbletea.Cmd {
	if m == nil || m.petRuntime == nil {
		return nil
	}
	_ = m.petRuntime.writeTerminalSequence(sequence)
	return nil
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

// clearTerminalTitleOnExit mirrors Rust App::drop's
// clear_managed_terminal_title: only a title Codex actually wrote is cleared,
// and an already-cleared title is not emitted again.
func clearTerminalTitleOnExit(model *Model, output io.Writer) {
	if model == nil || output == nil {
		return
	}
	sequence := model.lastTerminalTitleSequence
	if sequence == "" || sequence == codextui.ClearTerminalTitleOSC() {
		return
	}
	_, _ = io.WriteString(output, codextui.ClearTerminalTitleOSC())
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
