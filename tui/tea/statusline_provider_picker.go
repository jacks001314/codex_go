package tea

import (
	"context"
	"sort"
	"strconv"
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/config"
	"codex_go/features"
)

// Rust parity: codex-rs/tui/src/app/statusline_picker.rs (#61988).
//
// The `/statusline` item editor offers a "Choose a statusline plugin" action
// when status line extensions are enabled. It discovers MCP providers, shows
// the saved selection first, and lets the user save a provider or return to the
// built-in status line. The item editor stays staged underneath so cancelling
// the picker returns to it with its selections intact.

const (
	statuslineProviderPickerModalID  = "statusline-providers"
	statuslineProviderPickerActionID = "choose-statusline-plugin"
	statuslineProviderDefaultOption  = "statusline-provider-default"
	statuslineProviderOptionPrefix   = "statusline-provider:"
)

// StatuslineProviderDiscoveryRequest asks the host to probe MCP servers for
// statusline providers. Known names the servers already observed (the saved
// selection), so a cold inventory does not hide them.
type StatuslineProviderDiscoveryRequest struct {
	ThreadID         string
	WorkingDirectory string
	Known            []string
}

// StatuslineProviderCandidate is one discovered provider row.
type StatuslineProviderCandidate struct {
	Server      string
	Tool        string
	Title       string
	PluginLabel string
	URI         string
}

// StatuslineProviderDiscoveryUpdate streams one server's probe result.
type StatuslineProviderDiscoveryUpdate struct {
	Server    string
	Providers []StatuslineProviderCandidate
	Err       error
}

// StatuslineProviderDiscoveryFunc streams discovery updates until the channel
// closes. Cancelling ctx stops outstanding waits.
type StatuslineProviderDiscoveryFunc func(ctx context.Context, request StatuslineProviderDiscoveryRequest) <-chan StatuslineProviderDiscoveryUpdate

type statuslinePickerDiscoveryMsg struct {
	scan    uint64
	updates []StatuslineProviderDiscoveryUpdate
}

// statuslineProviderPickerState survives re-renders so a discovery batch can
// preserve the highlighted row and the item editor underneath.
type statuslineProviderPickerState struct {
	scan        uint64
	threadID    string
	hostCWD     string
	selected    *config.StatusLineProviderSelection
	providers   []StatuslineProviderCandidate
	discovering bool
	offline     bool
	cursorID    string
	// editor is the staged item editor modal the picker opened from.
	editor *modalState
}

func (s *statuslineProviderPickerState) candidate(server string, tool string) *StatuslineProviderCandidate {
	if s == nil {
		return nil
	}
	for i := range s.providers {
		if s.providers[i].Server == server && s.providers[i].Tool == tool {
			return &s.providers[i]
		}
	}
	return nil
}

// providerOptionID encodes a provider identity for a modal row.
func providerOptionID(server string, tool string) string {
	return statuslineProviderOptionPrefix + strings.TrimSpace(server) + "\x1f" + strings.TrimSpace(tool)
}

func parseProviderOptionID(id string) (server string, tool string, ok bool) {
	if !strings.HasPrefix(id, statuslineProviderOptionPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(id, statuslineProviderOptionPrefix)
	server, tool, found := strings.Cut(rest, "\x1f")
	if !found {
		return "", "", false
	}
	return server, tool, true
}

// statuslineStatuslinePickerEnabled mirrors the Rust feature gate around the
// "Choose a statusline plugin" action.
func (m *Model) statuslineProviderPickerEnabled() bool {
	if m == nil {
		return false
	}
	return features.Enabled(m.featureSettings, "tui_extensions_statusline")
}

// openStatuslineProviderPicker stashes the open item editor and shows the
// provider list.
func (m *Model) openStatuslineProviderPicker() bubbletea.Cmd {
	if m == nil || !m.statuslineProviderPickerEnabled() {
		return nil
	}
	selected := m.StatusLineProvider()
	known := []string{}
	if selected != nil && !selected.Default && strings.TrimSpace(selected.Server) != "" {
		known = append(known, strings.TrimSpace(selected.Server))
	}
	m.statuslinePickerScan++
	state := &statuslineProviderPickerState{
		scan:        m.statuslinePickerScan,
		threadID:    strings.TrimSpace(m.State.ThreadID),
		hostCWD:     m.statusLineCWD(),
		selected:    selected,
		offline:     m.statuslineDisconnected,
		discovering: m.onDiscoverStatuslineProviders != nil && !m.statuslineDisconnected,
		editor:      m.modal,
	}
	if state.selected != nil && !state.selected.Default {
		state.cursorID = providerOptionID(state.selected.Server, state.selected.Tool)
	} else {
		state.cursorID = statuslineProviderDefaultOption
	}
	m.statuslinePicker = state
	m.renderStatuslineProviderPicker()
	if !state.discovering {
		return nil
	}
	return m.statuslinePickerDiscoveryCmd(state, known)
}

func (m *Model) statuslinePickerDiscoveryCmd(state *statuslineProviderPickerState, known []string) bubbletea.Cmd {
	discover := m.onDiscoverStatuslineProviders
	if discover == nil || state == nil {
		return nil
	}
	request := StatuslineProviderDiscoveryRequest{
		ThreadID:         state.threadID,
		WorkingDirectory: state.hostCWD,
		Known:            known,
	}
	scan := state.scan
	return func() bubbletea.Msg {
		stream := discover(context.Background(), request)
		updates := []StatuslineProviderDiscoveryUpdate{}
		for update := range stream {
			updates = append(updates, update)
		}
		return statuslinePickerDiscoveryMsg{scan: scan, updates: updates}
	}
}

// renderStatuslineProviderPicker rebuilds the modal rows from the live state.
func (m *Model) renderStatuslineProviderPicker() {
	state := m.statuslinePicker
	if m == nil || state == nil {
		return
	}
	body := ""
	switch {
	case state.offline:
		body = "Plugin providers are unavailable while disconnected."
	case state.discovering:
		body = "Discovering MCP status line providers…"
	}
	options := m.statuslineProviderPickerOptions(state)
	selected := 0
	for i, option := range options {
		if option.ID == state.cursorID {
			selected = i
			break
		}
	}
	modal := &modalState{
		id:       statuslineProviderPickerModalID,
		kind:     ModalKindStatuslineProvider,
		title:    "Select a status line provider",
		body:     body,
		options:  options,
		selected: selected,
	}
	modal.selectionView = true
	m.replaceModal(modal)
}

// statuslineProviderPickerOptions mirrors Rust StatuslinePicker::view: the saved
// selection first, then the default status line, then discovered providers
// sorted by title and identity. Numeric shortcuts cover the first nine
// selectable rows.
func (m *Model) statuslineProviderPickerOptions(state *statuslineProviderPickerState) []ModalOption {
	if state == nil {
		return nil
	}
	options := []ModalOption{}
	appendRow := func(server string, tool string) {
		options = append(options, m.statuslineProviderPickerRow(state, server, tool))
	}
	if state.selected != nil && !state.selected.Default &&
		strings.TrimSpace(state.selected.Server) != "" && strings.TrimSpace(state.selected.Tool) != "" {
		appendRow(state.selected.Server, state.selected.Tool)
	}
	options = append(options, ModalOption{
		ID:    statuslineProviderDefaultOption,
		Label: statuslineProviderCurrentSuffix("Default status line", state.selected == nil || state.selected.Default),
	})
	for _, provider := range state.providers {
		if state.selected != nil && !state.selected.Default &&
			provider.Server == state.selected.Server && provider.Tool == state.selected.Tool {
			continue
		}
		appendRow(provider.Server, provider.Tool)
	}
	shortcut := 0
	for i := range options {
		if options[i].Disabled {
			continue
		}
		shortcut++
		if shortcut > 9 {
			break
		}
		options[i].Shortcut = strconv.Itoa(shortcut)
		options[i].DisplayShortcut = strconv.Itoa(shortcut)
	}
	return options
}

func (m *Model) statuslineProviderPickerRow(state *statuslineProviderPickerState, server string, tool string) ModalOption {
	candidate := state.candidate(server, tool)
	name := strings.TrimSpace(tool)
	if candidate != nil && strings.TrimSpace(candidate.Title) != "" {
		name = strings.TrimSpace(candidate.Title)
	}
	name = statuslinePickerLabel(name)
	if candidate != nil {
		if plugin := strings.TrimSpace(candidate.PluginLabel); plugin != "" {
			name += " · " + statuslinePickerLabel(plugin)
		}
	}
	if state.offline || (!state.discovering && candidate == nil) {
		name += " (Unavailable)"
	}
	current := state.selected != nil && !state.selected.Default &&
		strings.TrimSpace(state.selected.Server) == server && strings.TrimSpace(state.selected.Tool) == tool
	return ModalOption{
		ID:          providerOptionID(server, tool),
		Label:       statuslineProviderCurrentSuffix(name, current),
		Description: statuslinePickerLabel(server + " / " + tool),
		Disabled:    state.offline,
	}
}

func statuslineProviderCurrentSuffix(label string, current bool) string {
	if !current {
		return label
	}
	return label + " (current)"
}

// statuslinePickerLabel mirrors Rust's label(): leaf text with embedded newlines
// and tabs flattened into spaces.
func statuslinePickerLabel(text string) string {
	return strings.NewReplacer("\n", " ", "\t", " ", "\r", " ").Replace(strings.TrimSpace(text))
}

// applyStatuslinePickerDiscovery merges one finished discovery batch, keeping
// the highlighted row and ignoring results from a retired scan.
func (m *Model) applyStatuslinePickerDiscovery(msg statuslinePickerDiscoveryMsg) {
	state := m.statuslinePicker
	if m == nil || state == nil || msg.scan != state.scan {
		return
	}
	if !state.discovering {
		return
	}
	for _, update := range msg.updates {
		if update.Err != nil {
			continue
		}
		state.providers = append(state.providers, update.Providers...)
	}
	sort.SliceStable(state.providers, func(i int, j int) bool {
		left, right := state.providers[i], state.providers[j]
		if left.Title != right.Title {
			return left.Title < right.Title
		}
		if left.Server != right.Server {
			return left.Server < right.Server
		}
		return left.Tool < right.Tool
	})
	state.discovering = false
	m.renderStatuslineProviderPicker()
}

// cancelStatuslineProviderPicker restores the staged item editor, mirroring
// Rust keeping the editor open across picker transitions.
func (m *Model) cancelStatuslineProviderPicker() {
	state := m.statuslinePicker
	m.statuslinePicker = nil
	if state == nil || state.editor == nil {
		m.modal = nil
		return
	}
	m.replaceModal(state.editor)
}

// applyStatuslineProviderPickerOption saves the chosen row and returns to the
// item editor.
func (m *Model) applyStatuslineProviderPickerOption(optionID string) bubbletea.Cmd {
	state := m.statuslinePicker
	if m == nil || state == nil {
		m.modal = nil
		return nil
	}
	optionID = strings.TrimSpace(optionID)
	if optionID == statuslineProviderDefaultOption {
		m.cancelStatuslineProviderPicker()
		return m.SaveStatusLineProvider(config.StatusLineProviderSelection{Default: true})
	}
	server, tool, ok := parseProviderOptionID(optionID)
	if !ok || server == "" || tool == "" || state.offline {
		m.cancelStatuslineProviderPicker()
		return nil
	}
	if state.candidate(server, tool) == nil &&
		(state.selected == nil || state.selected.Default || state.selected.Server != server || state.selected.Tool != tool) {
		m.cancelStatuslineProviderPicker()
		return nil
	}
	m.cancelStatuslineProviderPicker()
	return m.SaveStatusLineProvider(config.StatusLineProviderSelection{Server: server, Tool: tool})
}
