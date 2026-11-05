package tea

import (
	"strconv"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/model"
	codextui "codex_go/tui"
)

// Model provider browsing (Rust #59541, codex-rs/tui/src/app/provider.rs).
// The browser reads the connected app server's provider catalog and never
// changes the active provider. Results carry the generation of the popup that
// requested them so a dismissed or reopened view never shows a stale reply.

// ModelProviderListFunc loads the connected app server's provider catalog
// (Rust #59541 app::provider).
type ModelProviderListFunc func() (model.ProviderListResponse, error)

type configProviderBrowserState struct {
	generation uint64
	loading    bool
	loaded     bool
	err        string
	providers  []model.ProviderSummary
	defaultID  string
	currentID  string
	selected   int
	scrollTop  int
	// chatPresentAtLoad records whether the catalog was loaded after the first
	// chat attached; the list refreshes once when that changes.
	chatPresentAtLoad bool
	details           *model.ProviderSummary
	detailScroll      int
}

type configPanelProvidersLoadedMsg struct {
	generation uint64
	page       model.ProviderListResponse
	err        error
}

// currentChatProviderID mirrors Rust chat.thread_id().map(|_|
// config.model_provider_id): only an attached chat names a current provider.
func (m *Model) currentChatProviderID() string {
	if m == nil || m.State == nil || strings.TrimSpace(m.State.ThreadID) == "" {
		return ""
	}
	return strings.TrimSpace(m.State.Provider)
}

func (m *Model) openConfigProviderBrowser() bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return nil
	}
	panel := m.modal.configPanel
	m.configProviderGeneration++
	browser := &configProviderBrowserState{
		generation:        m.configProviderGeneration,
		loading:           true,
		currentID:         m.currentChatProviderID(),
		chatPresentAtLoad: m.currentChatProviderID() != "",
	}
	panel.providers = browser
	return m.fetchConfigProvidersCmd(browser.generation)
}

// closeConfigProviderBrowser invalidates the popup's in-flight request.
func (m *Model) closeConfigProviderBrowser() {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return
	}
	m.configProviderGeneration++
	m.modal.configPanel.providers = nil
}

func (m *Model) fetchConfigProvidersCmd(generation uint64) bubbletea.Cmd {
	if m == nil || m.onListModelProviders == nil {
		return func() bubbletea.Msg {
			return configPanelProvidersLoadedMsg{
				generation: generation,
				err:        errModelProvidersUnavailable,
			}
		}
	}
	loader := m.onListModelProviders
	return func() bubbletea.Msg {
		page, err := loader()
		return configPanelProvidersLoadedMsg{generation: generation, page: page, err: err}
	}
}

// errModelProvidersUnavailable reports a TUI session with no provider catalog
// source, mirroring Rust's disconnected-server message.
var errModelProvidersUnavailable = &providerCatalogError{message: "Reconnect to the app server to browse model providers."}

type providerCatalogError struct{ message string }

func (e *providerCatalogError) Error() string { return e.message }

// applyConfigPanelProvidersLoaded replaces the loading view with the list or
// the error view, dropping replies for a dismissed or reopened browser.
func (m *Model) applyConfigPanelProvidersLoaded(msg configPanelProvidersLoadedMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindConfig || m.modal.configPanel == nil {
		return nil
	}
	browser := m.modal.configPanel.providers
	if browser == nil || browser.generation != msg.generation {
		return nil
	}
	browser.loading = false
	if msg.err != nil {
		browser.loaded = false
		browser.err = strings.TrimSpace(msg.err.Error())
		if browser.err == "" {
			browser.err = "Could not load model providers."
		}
		return nil
	}
	browser.err = ""
	browser.loaded = true
	browser.providers = append([]model.ProviderSummary(nil), msg.page.Data...)
	browser.defaultID = strings.TrimSpace(msg.page.DefaultProviderID)
	browser.currentID = m.currentChatProviderID()
	browser.chatPresentAtLoad = browser.currentID != ""
	browser.selected = 0
	for index, provider := range browser.providers {
		if browser.currentID != "" && provider.ID == browser.currentID {
			browser.selected = index
		}
	}
	return nil
}

// refreshOpenProviderBrowserCmd re-reads a list that was opened before the
// first chat attached (Rust provider::refresh_list_if_present).
func (m *Model) refreshOpenProviderBrowserCmd() bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindConfig || m.modal.configPanel == nil {
		return nil
	}
	browser := m.modal.configPanel.providers
	if browser == nil || browser.loading || browser.details != nil {
		return nil
	}
	chatPresent := m.currentChatProviderID() != ""
	if browser.chatPresentAtLoad == chatPresent {
		return nil
	}
	m.configProviderGeneration++
	browser.generation = m.configProviderGeneration
	browser.loading = true
	browser.err = ""
	browser.chatPresentAtLoad = chatPresent
	return m.fetchConfigProvidersCmd(browser.generation)
}

func (m *Model) updateConfigProviderBrowserKey(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return nil
	}
	browser := m.modal.configPanel.providers
	if browser == nil {
		return nil
	}
	if browser.details != nil {
		return m.updateConfigProviderDetailsKey(message, browser)
	}
	switch message.Type {
	case bubbletea.KeyEsc:
		m.closeConfigProviderBrowser()
		return nil
	case bubbletea.KeyCtrlC:
		m.modal = nil
		m.refreshTranscript()
		return nil
	case bubbletea.KeyUp:
		m.moveConfigProviderSelection(-1)
		return nil
	case bubbletea.KeyDown:
		m.moveConfigProviderSelection(1)
		return nil
	case bubbletea.KeyHome:
		browser.selected = 0
		return nil
	case bubbletea.KeyEnd:
		if len(browser.providers) > 0 {
			browser.selected = len(browser.providers) - 1
		}
		return nil
	case bubbletea.KeyEnter:
		if browser.loading || browser.err != "" || len(browser.providers) == 0 {
			m.closeConfigProviderBrowser()
			return nil
		}
		if browser.selected < 0 || browser.selected >= len(browser.providers) {
			return nil
		}
		provider := browser.providers[browser.selected]
		browser.details = &provider
		browser.detailScroll = 0
		return nil
	case bubbletea.KeyRunes:
		switch m.configPanelListActionForSpec(keySpecFromKeyMsg(message)) {
		case "move_up":
			m.moveConfigProviderSelection(-1)
		case "move_down":
			m.moveConfigProviderSelection(1)
		case "cancel":
			m.closeConfigProviderBrowser()
		}
		return nil
	}
	return nil
}

func (m *Model) moveConfigProviderSelection(delta int) {
	if m == nil || m.modal == nil || m.modal.configPanel == nil || m.modal.configPanel.providers == nil {
		return
	}
	browser := m.modal.configPanel.providers
	if len(browser.providers) == 0 || delta == 0 {
		return
	}
	index := clampModalInt(browser.selected, 0, len(browser.providers)-1)
	index += delta
	if index < 0 {
		index = len(browser.providers) - 1
	} else if index >= len(browser.providers) {
		index = 0
	}
	browser.selected = index
}

func (m *Model) updateConfigProviderDetailsKey(message bubbletea.KeyMsg, browser *configProviderBrowserState) bubbletea.Cmd {
	scroll := func(delta int) {
		lines := providerDetailLines(*browser.details)
		maxScroll := max(len(lines)-m.providerDetailsVisibleLines(), 0)
		browser.detailScroll = clampModalInt(browser.detailScroll+delta, 0, maxScroll)
	}
	switch message.Type {
	case bubbletea.KeyEsc:
		browser.details = nil
		browser.detailScroll = 0
	case bubbletea.KeyCtrlC:
		m.modal = nil
		m.refreshTranscript()
	case bubbletea.KeyUp:
		scroll(-1)
	case bubbletea.KeyDown:
		scroll(1)
	case bubbletea.KeyPgUp:
		scroll(-m.providerDetailsVisibleLines())
	case bubbletea.KeyPgDown:
		scroll(m.providerDetailsVisibleLines())
	case bubbletea.KeyHome:
		browser.detailScroll = 0
	case bubbletea.KeyEnd:
		scroll(len(providerDetailLines(*browser.details)))
	}
	return nil
}

func (m *Model) providerDetailsVisibleLines() int {
	if m == nil {
		return 1
	}
	return max(m.height-8, 1)
}

// providerDetailFields mirrors Rust app/provider/fields.rs provider_fields: the
// fields the server reports, including values it manages externally.
type providerDetailField int

const (
	providerFieldName providerDetailField = iota
	providerFieldURL
	providerFieldManagedURL
	providerFieldVariable
	providerFieldExternalAuth
	providerFieldProfile
	providerFieldRegion
	providerFieldProject
	providerFieldLocation
	providerFieldCredentials
)

func (f providerDetailField) label() string {
	switch f {
	case providerFieldName:
		return "Name"
	case providerFieldURL, providerFieldManagedURL:
		return "Base URL"
	case providerFieldVariable:
		return "API key variable"
	case providerFieldExternalAuth:
		return "External authentication"
	case providerFieldProfile:
		return "AWS profile"
	case providerFieldRegion:
		return "AWS region"
	case providerFieldProject:
		return "Project ID"
	case providerFieldLocation:
		return "Location"
	case providerFieldCredentials:
		return "Credential file (server)"
	}
	return ""
}

func (f providerDetailField) absentValue() string {
	switch f {
	case providerFieldURL, providerFieldManagedURL:
		return "Not reported"
	case providerFieldExternalAuth:
		return "Managed externally"
	case providerFieldVariable:
		return "None"
	default:
		return "Not set"
	}
}

type providerDetailValue struct {
	field providerDetailField
	value *string
}

func providerDetailValues(provider model.ProviderSummary) []providerDetailValue {
	url := provider.BaseURL
	trimmed := func(value *string) *string {
		if value == nil {
			return nil
		}
		if strings.TrimSpace(*value) == "" {
			return nil
		}
		return value
	}
	switch provider.Settings.Type {
	case "openai":
		return nil
	case "customGateway":
		authField := providerDetailValue{field: providerFieldVariable}
		if provider.Settings.Auth != nil {
			switch provider.Settings.Auth.Mode {
			case "environmentVariable":
				authField = providerDetailValue{field: providerFieldVariable, value: provider.Settings.Auth.EnvKey}
			case "externallyConfigured":
				authField = providerDetailValue{field: providerFieldExternalAuth, value: provider.Settings.Auth.EnvKey}
			}
		}
		endpoint := providerFieldURL
		if authField.field == providerFieldExternalAuth && url == nil {
			endpoint = providerFieldManagedURL
		}
		fields := []providerDetailValue{
			{field: providerFieldName, value: &provider.Name},
			{field: endpoint, value: url},
			authField,
		}
		if url != nil {
			fields = append(fields, providerDetailValue{field: providerFieldManagedURL, value: url})
		}
		return fields
	case "bedrock":
		return []providerDetailValue{
			{field: providerFieldProfile, value: trimmed(provider.Settings.Profile)},
			{field: providerFieldRegion, value: provider.Settings.Region},
		}
	case "gcp":
		return []providerDetailValue{
			{field: providerFieldProject, value: provider.Settings.ProjectID},
			{field: providerFieldLocation, value: provider.Settings.Location},
			{field: providerFieldCredentials, value: provider.Settings.CredentialFile},
		}
	}
	var fields []providerDetailValue
	if url != nil {
		fields = append(fields, providerDetailValue{field: providerFieldManagedURL, value: url})
	}
	return fields
}

// providerDetailLines mirrors Rust provider::details: the bold provider name,
// a blank line, then every reported field with its absent-value fallback.
func providerDetailLines(provider model.ProviderSummary) []string {
	lines := []string{
		provider.Name,
		"",
		"Provider ID  " + provider.ID,
	}
	if provider.Settings.Type == "openai" {
		lines = append(lines, "Type  Built-in OpenAI provider")
	}
	for _, field := range providerDetailValues(provider) {
		value := field.field.absentValue()
		if field.value != nil && strings.TrimSpace(*field.value) != "" {
			value = *field.value
		}
		lines = append(lines, field.field.label()+"  "+value)
	}
	return lines
}

// renderConfigProviderBrowser renders the loading view, the error view, the
// provider list, or the scrollable details page.
func (m *Model) renderConfigProviderBrowser(browser *configProviderBrowserState, rowWidth int) string {
	contentWidth := max(rowWidth-2, 1)
	if browser.details != nil {
		return m.renderConfigProviderDetails(*browser.details, browser.detailScroll, rowWidth, contentWidth)
	}
	lines := []string{
		"  Model providers",
	}
	subtitle := "Loading model providers\u2026"
	if browser.err != "" {
		subtitle = browser.err
	} else if browser.loaded {
		current := browser.currentID
		if current == "" {
			current = "No active chat"
		}
		subtitle = "Current chat: " + current + " \u00b7 Default for new chats: " + firstNonEmpty(browser.defaultID, "openai")
	}
	lines = append(lines, "  "+codextui.TruncateWithEllipsis(subtitle, contentWidth), "", "")
	if browser.loading || browser.err != "" || len(browser.providers) == 0 {
		hint := ""
		if browser.loaded && browser.err == "" {
			hint = "  No providers reported."
		}
		lines = append(lines, "\u203a 1. Back")
		if hint != "" {
			lines = append(lines, hint)
		}
		lines = append(lines, "", "  enter back \u00b7 esc back")
		return strings.Join(lines, "\n")
	}
	names := make([]string, len(browser.providers))
	prefixes := make([]string, len(browser.providers))
	numberWidth := len(strconv.Itoa(len(browser.providers)))
	maxNameWidth := 0
	for index, provider := range browser.providers {
		marker := "  "
		if index == browser.selected {
			marker = "\u203a "
		}
		gutter := strconv.Itoa(index+1) + ". "
		if pad := numberWidth + 2 - codextui.DisplayWidth(gutter); pad > 0 {
			gutter += strings.Repeat(" ", pad)
		}
		prefixes[index] = marker + gutter
		names[index] = provider.Name
		if browser.currentID != "" && provider.ID == browser.currentID {
			names[index] += " (current)"
		}
		maxNameWidth = max(maxNameWidth, codextui.DisplayWidth(prefixes[index]+names[index]))
	}
	descCol := configPanelClampDescCol(maxNameWidth+2, rowWidth)
	for index, provider := range browser.providers {
		line := prefixes[index] + names[index]
		if gap := descCol - codextui.DisplayWidth(line); gap > 0 {
			line += strings.Repeat(" ", gap)
		}
		line += provider.ID
		line = codextui.TruncateWithEllipsis(line, rowWidth)
		if index == browser.selected {
			line = codextui.RenderSelectedRowFilled(line, rowWidth)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", "  enter view details \u00b7 esc back")
	return strings.Join(lines, "\n")
}

func (m *Model) renderConfigProviderDetails(provider model.ProviderSummary, scroll int, rowWidth int, contentWidth int) string {
	all := providerDetailLines(provider)
	visible := m.providerDetailsVisibleLines()
	scroll = clampModalInt(scroll, 0, max(len(all)-visible, 0))
	end := min(scroll+visible, len(all))
	lines := []string{
		"  Model provider details",
		"",
	}
	for index, line := range all[scroll:end] {
		if scroll+index == 0 {
			line = strings.ToUpper(line[:1]) + line[1:]
		}
		lines = append(lines, "  "+codextui.TruncateWithEllipsis(line, contentWidth))
	}
	lines = append(lines, "")
	if scroll > 0 {
		lines = append(lines, "  (more above)")
	}
	if end < len(all) {
		lines = append(lines, "  (more below)")
	}
	lines = append(lines, "  \u2191/\u2193 scroll \u00b7 esc back")
	return strings.Join(lines, "\n")
}
