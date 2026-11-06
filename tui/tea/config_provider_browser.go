package tea

import (
	"errors"
	"strconv"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/config"
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

// ModelProviderConfigWriteFunc applies one provider configuration patch
// through the connected app server.
type ModelProviderConfigWriteFunc func(params model.ProviderConfigWriteParams) (model.ProviderConfigWriteResponse, error)

type configProviderBrowserState struct {
	generation       uint64
	loading          bool
	loaded           bool
	err              string
	providers        []model.ProviderSummary
	defaultID        string
	currentID        string
	selected         int
	scrollTop        int
	canChangeDefault bool
	clientOverride   string
	notice           string
	saving           bool
	editor           *configProviderEditorState
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

type configProviderEditorField struct {
	key      string
	label    string
	value    string
	original string
	required bool
}

type configProviderEditorState struct {
	providerID   string
	providerType string
	original     *model.ProviderSummary
	fields       []configProviderEditorField
	selected     int
	err          string
}

type configPanelProviderWriteResultMsg struct {
	generation uint64
	kind       string
	providerID string
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
		clientOverride:    strings.TrimSpace(m.providerClientOverride),
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
	browser.canChangeDefault = msg.page.CanChangeDefault && browser.clientOverride == ""
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
	if browser == nil || browser.saving {
		return nil
	}
	if browser.editor != nil {
		return m.updateConfigProviderEditorKey(message, browser)
	}
	if browser.details != nil {
		return m.updateConfigProviderDetailsKey(message, browser)
	}
	rowCount := len(browser.providers)
	if browser.loaded && browser.err == "" {
		rowCount++
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
		if rowCount > 0 {
			browser.selected = rowCount - 1
		}
		return nil
	case bubbletea.KeySpace:
		return m.writeConfigProviderDefaultCmd(browser)
	case bubbletea.KeyEnter:
		if browser.loading || browser.err != "" {
			m.closeConfigProviderBrowser()
			return nil
		}
		if browser.selected == len(browser.providers) {
			browser.editor = newConfigProviderEditor(nil)
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
	count := len(browser.providers)
	if browser.loaded && browser.err == "" {
		count++
	}
	if count == 0 || delta == 0 {
		return
	}
	index := clampModalInt(browser.selected, 0, count-1)
	index += delta
	if index < 0 {
		index = count - 1
	} else if index >= count {
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
	case bubbletea.KeyEnter:
		if browser.details.CanEdit && browser.details.Settings.Type != "openai" {
			browser.editor = newConfigProviderEditor(browser.details)
		}
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
	if browser.editor != nil {
		return m.renderConfigProviderEditor(browser.editor, rowWidth, contentWidth)
	}
	if browser.details != nil {
		return m.renderConfigProviderDetails(*browser.details, browser.detailScroll, rowWidth, contentWidth)
	}
	lines := []string{"  Model providers"}
	subtitle := "Loading model providers…"
	if browser.saving {
		subtitle = "Saving model provider…"
	} else if browser.err != "" {
		subtitle = browser.err
	} else if browser.loaded {
		current := browser.currentID
		if current == "" {
			current = "No active chat"
		}
		defaultLabel := "Default for new chats"
		if browser.clientOverride != "" {
			defaultLabel = "Server default"
		}
		subtitle = "Current chat: " + current + " · " + defaultLabel + ": " + firstNonEmpty(browser.defaultID, "openai")
	}
	lines = append(lines, "  "+codextui.TruncateWithEllipsis(subtitle, contentWidth), "", "")
	if browser.loading || browser.err != "" || len(browser.providers) == 0 {
		hint := ""
		if browser.loaded && browser.err == "" {
			hint = "  No providers reported."
		}
		lines = append(lines, "› 1. Back")
		if hint != "" {
			lines = append(lines, hint)
		}
		lines = append(lines, "", "  enter back · esc back")
		return strings.Join(lines, "\n")
	}
	rows := append([]model.ProviderSummary(nil), browser.providers...)
	numberWidth := len(strconv.Itoa(len(rows) + 1))
	maxNameWidth := 0
	labels := make([]string, 0, len(rows)+1)
	for _, provider := range rows {
		marker := "○ "
		if provider.ID == browser.defaultID {
			marker = "● "
		}
		label := marker + provider.Name
		if browser.currentID != "" && provider.ID == browser.currentID {
			label += " (current)"
		}
		labels = append(labels, label)
		maxNameWidth = max(maxNameWidth, codextui.DisplayWidth(label)+numberWidth+4)
	}
	labels = append(labels, "Add custom provider")
	maxNameWidth = max(maxNameWidth, codextui.DisplayWidth(labels[len(labels)-1])+numberWidth+4)
	descCol := configPanelClampDescCol(maxNameWidth+2, rowWidth)
	for index, label := range labels {
		cursor := "  "
		if index == browser.selected {
			cursor = "› "
		}
		gutter := strconv.Itoa(index+1) + ". "
		if pad := numberWidth + 2 - codextui.DisplayWidth(gutter); pad > 0 {
			gutter += strings.Repeat(" ", pad)
		}
		line := cursor + gutter + label
		if index < len(rows) {
			if gap := descCol - codextui.DisplayWidth(line); gap > 0 {
				line += strings.Repeat(" ", gap)
			}
			line += rows[index].ID
		}
		line = codextui.TruncateWithEllipsis(line, rowWidth)
		if index == browser.selected {
			line = codextui.RenderSelectedRowFilled(line, rowWidth)
		}
		lines = append(lines, line)
	}
	note := browser.notice
	if note == "" {
		switch {
		case browser.clientOverride != "":
			note = "This client's launch settings select " + browser.clientOverride + " for new chats."
		case !browser.canChangeDefault:
			note = "The server configuration fixes the default provider."
		default:
			note = "Use /new to start a chat with the selected provider."
		}
	}
	if note != "" {
		lines = append(lines, "", "  "+codextui.TruncateWithEllipsis(note, contentWidth))
	}
	hint := "  enter view details · esc back"
	if browser.canChangeDefault {
		hint = "  enter details · space set default · esc back"
	}
	lines = append(lines, "", hint)
	return strings.Join(lines, "\n")
}

func (m *Model) renderConfigProviderEditor(editor *configProviderEditorState, rowWidth int, contentWidth int) string {
	title := "Add custom provider"
	if editor.providerID != "" {
		title = "Edit provider"
		if editor.original != nil {
			title = "Edit " + editor.original.Name
		}
	}
	lines := []string{"  " + title, ""}
	for index, field := range editor.fields {
		marker := "  "
		if index == editor.selected {
			marker = "› "
		}
		required := ""
		if field.required {
			required = "*"
		}
		value := field.value
		if value == "" {
			value = "Not set"
		}
		line := marker + field.label + required + "  " + value
		line = codextui.TruncateWithEllipsis(line, rowWidth)
		if index == editor.selected {
			line = codextui.RenderSelectedRowFilled(line, rowWidth)
		}
		lines = append(lines, line)
	}
	save := "  Save"
	if editor.selected == len(editor.fields) {
		save = "› Save"
	}
	lines = append(lines, save, "", "  * Required")
	if editor.err != "" {
		lines = append(lines, "  "+codextui.TruncateWithEllipsis(editor.err, contentWidth))
	}
	lines = append(lines, "  tab move · enter next/save · esc back")
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
	if provider.CanEdit && provider.Settings.Type != "openai" {
		lines = append(lines, "  enter Edit configuration \u00b7 \u2191/\u2193 scroll \u00b7 esc back")
	} else {
		lines = append(lines, "  \u2191/\u2193 scroll \u00b7 esc back")
	}
	return strings.Join(lines, "\n")
}

func providerString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func newConfigProviderEditor(provider *model.ProviderSummary) *configProviderEditorState {
	editor := &configProviderEditorState{original: provider}
	if provider == nil {
		editor.providerType = "customGateway"
		editor.fields = []configProviderEditorField{
			{key: "name", label: "Name", required: true},
			{key: "base_url", label: "Base URL", required: true},
			{key: "env_key", label: "API key variable"},
		}
		return editor
	}
	editor.providerID = provider.ID
	editor.providerType = provider.Settings.Type
	switch provider.Settings.Type {
	case "bedrock":
		editor.fields = []configProviderEditorField{
			{key: "profile", label: "AWS profile", value: providerString(provider.Settings.Profile), original: providerString(provider.Settings.Profile)},
			{key: "region", label: "AWS region", value: providerString(provider.Settings.Region), original: providerString(provider.Settings.Region)},
		}
	case "gcp":
		editor.fields = []configProviderEditorField{
			{key: "project_id", label: "Project ID", value: providerString(provider.Settings.ProjectID), original: providerString(provider.Settings.ProjectID)},
			{key: "location", label: "Location", value: providerString(provider.Settings.Location), original: providerString(provider.Settings.Location)},
			{key: "credential_file", label: "Credential file", value: providerString(provider.Settings.CredentialFile), original: providerString(provider.Settings.CredentialFile)},
		}
	default:
		envKey := ""
		if provider.Settings.Auth != nil {
			envKey = providerString(provider.Settings.Auth.EnvKey)
		}
		baseURL := providerString(provider.BaseURL)
		editor.fields = []configProviderEditorField{
			{key: "name", label: "Name", value: provider.Name, original: provider.Name, required: true},
			{key: "base_url", label: "Base URL", value: baseURL, original: baseURL, required: provider.BaseURL != nil},
			{key: "env_key", label: "API key variable", value: envKey, original: envKey},
		}
	}
	return editor
}

func (m *Model) updateConfigProviderEditorKey(message bubbletea.KeyMsg, browser *configProviderBrowserState) bubbletea.Cmd {
	editor := browser.editor
	if editor == nil {
		return nil
	}
	count := len(editor.fields) + 1
	switch message.Type {
	case bubbletea.KeyEsc:
		browser.editor = nil
		return nil
	case bubbletea.KeyCtrlC:
		m.modal = nil
		m.refreshTranscript()
		return nil
	case bubbletea.KeyUp, bubbletea.KeyShiftTab:
		editor.selected = (editor.selected - 1 + count) % count
		return nil
	case bubbletea.KeyDown, bubbletea.KeyTab:
		editor.selected = (editor.selected + 1) % count
		return nil
	case bubbletea.KeyEnter:
		if editor.selected != len(editor.fields) {
			editor.selected = (editor.selected + 1) % count
			return nil
		}
		if err := validateConfigProviderEditor(editor); err != "" {
			editor.err = err
			return nil
		}
		return m.writeConfigProviderEditorCmd(browser)
	case bubbletea.KeyBackspace, bubbletea.KeyDelete:
		if editor.selected < len(editor.fields) {
			runes := []rune(editor.fields[editor.selected].value)
			if len(runes) > 0 {
				runes = runes[:len(runes)-1]
			}
			editor.fields[editor.selected].value = string(runes)
			editor.err = ""
		}
		return nil
	case bubbletea.KeySpace:
		if editor.selected < len(editor.fields) {
			editor.fields[editor.selected].value += " "
			editor.err = ""
		}
		return nil
	case bubbletea.KeyRunes:
		if editor.selected < len(editor.fields) {
			editor.fields[editor.selected].value += message.String()
			editor.err = ""
		}
		return nil
	}
	return nil
}

func validateConfigProviderEditor(editor *configProviderEditorState) string {
	if editor == nil {
		return "Provider settings are required."
	}
	values := map[string]string{}
	for _, field := range editor.fields {
		values[field.key] = strings.TrimSpace(field.value)
		if field.required && values[field.key] == "" {
			if field.key == "name" {
				return "Enter a provider name."
			}
			return "Enter a base URL."
		}
	}
	if key := values["env_key"]; key != "" {
		for index, char := range key {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_' || (index > 0 && char >= '0' && char <= '9') {
				continue
			}
			return "Enter an environment variable name such as TEAM_API_KEY."
		}
	}
	if editor.providerType == "customGateway" && values["env_key"] != "" && values["base_url"] == "" {
		return "Enter a base URL to use this API key variable."
	}
	return ""
}

func optionalProviderString(field configProviderEditorField) model.OptionalString {
	value := strings.TrimSpace(field.value)
	original := strings.TrimSpace(field.original)
	if value == original {
		return model.OptionalString{}
	}
	if value == "" {
		return model.OptionalString{Set: true}
	}
	return model.OptionalString{Set: true, Value: &value}
}

func configProviderEditorPatch(editor *configProviderEditorState) (model.ProviderSettingsPatch, error) {
	if editor == nil {
		return model.ProviderSettingsPatch{}, errModelProvidersUnavailable
	}
	values := map[string]configProviderEditorField{}
	for _, field := range editor.fields {
		values[field.key] = field
	}
	switch editor.providerType {
	case "", "customGateway":
		name := strings.TrimSpace(values["name"].value)
		custom := &model.CustomGatewaySettingsParams{BaseURL: optionalProviderString(values["base_url"]), EnvKey: optionalProviderString(values["env_key"])}
		if name != "" {
			custom.Name = &name
		}
		if editor.providerID != "" {
			id := editor.providerID
			custom.ProviderID = &id
		}
		return model.ProviderSettingsPatch{Type: "customGateway", CustomGateway: custom}, nil
	case "bedrock":
		profile := optionalProviderString(values["profile"])
		region := optionalProviderString(values["region"])
		return model.ProviderSettingsPatch{Type: "bedrock", Bedrock: &model.BedrockSettingsParams{
			ProviderID: editor.providerID, Profile: profile, Region: region,
		}}, nil
	case "gcp":
		project := optionalProviderString(values["project_id"])
		location := optionalProviderString(values["location"])
		credential := optionalProviderString(values["credential_file"])
		return model.ProviderSettingsPatch{Type: "gcp", Gcp: &model.GcpSettingsParams{
			ProjectID: project, Location: location, CredentialFile: credential,
		}}, nil
	default:
		return model.ProviderSettingsPatch{}, &providerCatalogError{message: "This provider configuration is read-only."}
	}
}

func (m *Model) writeConfigProviderDefaultCmd(browser *configProviderBrowserState) bubbletea.Cmd {
	if m == nil || browser == nil || browser.loading || browser.err != "" || !browser.canChangeDefault {
		return nil
	}
	if browser.selected < 0 || browser.selected >= len(browser.providers) {
		return nil
	}
	provider := browser.providers[browser.selected]
	if provider.ID == browser.defaultID {
		return nil
	}
	browser.saving = true
	browser.notice = "Saving model provider…"
	generation := browser.generation
	providerID := provider.ID
	writer := m.onWriteHookConfig
	return func() bubbletea.Msg {
		if writer == nil {
			return configPanelProviderWriteResultMsg{generation: generation, kind: "default", providerID: providerID, err: errors.New("config/batchWrite is unavailable")}
		}
		err := writer(config.ConfigBatchWriteParams{
			Edits:            []config.ConfigEdit{{KeyPath: "model_provider", Value: providerID, MergeStrategy: config.MergeReplace}},
			ReloadUserConfig: true,
		})
		return configPanelProviderWriteResultMsg{generation: generation, kind: "default", providerID: providerID, err: err}
	}
}

func (m *Model) writeConfigProviderEditorCmd(browser *configProviderBrowserState) bubbletea.Cmd {
	if m == nil || browser == nil || browser.editor == nil {
		return nil
	}
	patch, err := configProviderEditorPatch(browser.editor)
	if err != nil {
		browser.editor.err = err.Error()
		return nil
	}
	browser.saving = true
	browser.notice = "Saving model provider…"
	generation := browser.generation
	providerID := browser.editor.providerID
	writer := m.onWriteModelProviderConfig
	return func() bubbletea.Msg {
		if writer == nil {
			return configPanelProviderWriteResultMsg{generation: generation, kind: "settings", providerID: providerID, err: errors.New("modelProvider/config/write is unavailable")}
		}
		_, err := writer(model.ProviderConfigWriteParams{Settings: patch})
		return configPanelProviderWriteResultMsg{generation: generation, kind: "settings", providerID: providerID, err: err}
	}
}

func (m *Model) applyConfigPanelProviderWriteResult(msg configPanelProviderWriteResultMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindConfig || m.modal.configPanel == nil {
		return nil
	}
	browser := m.modal.configPanel.providers
	if browser == nil || browser.generation != msg.generation {
		return nil
	}
	browser.saving = false
	if msg.err != nil {
		message := strings.TrimSpace(msg.err.Error())
		if msg.kind == "settings" && browser.editor != nil {
			browser.editor.err = message
			return nil
		}
		browser.notice = "Could not save provider: " + message
		m.notice = browser.notice
		m.addErrorHistoryMessage(browser.notice)
		return nil
	}
	if msg.kind == "default" {
		browser.defaultID = msg.providerID
		browser.notice = "/new now uses " + msg.providerID + ". Restart the app server and TUI to refresh /model."
	} else {
		browser.editor = nil
		browser.details = nil
		browser.notice = "Saved provider configuration."
	}
	m.notice = browser.notice
	browser.loading = true
	return m.fetchConfigProvidersCmd(browser.generation)
}
