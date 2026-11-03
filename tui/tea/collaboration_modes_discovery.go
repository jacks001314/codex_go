package tea

// Server collaboration-mode discovery (Rust #42401, d4dc882998).
//
// Rust collaboration_modes::list fetches `collaborationMode/list` during TUI
// startup and reconnect, bounded by a two-second timeout, and installs the
// result as ModelCatalog::collaboration_modes via
// ModelCatalog::with_collaboration_modes. Discovery is optional: an
// unavailable, invalid, or unsupported response leaves an empty catalog, which
// means no visible collaboration modes. The RPC omits prompts, so restored
// developer-instruction overrides are cleared and the server supplies them when
// a mode is selected.

import (
	"errors"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	chatwidget "codex_go/tui/chatwidget"
)

// collaborationModeDiscoveryTimeout mirrors Rust's two-second bound so an
// unresponsive app server never blocks bootstrap. It is a variable so tests can
// shrink the bound.
var collaborationModeDiscoveryTimeout = 2 * time.Second

var errCollaborationModeDiscoveryTimeout = errors.New("collaborationMode/list discovery timed out")

// CollaborationModeLister reads the connected app server's
// `collaborationMode/list` catalog.
type CollaborationModeLister func() ([]chatwidget.CollaborationModeMask, error)

// CollaborationModesResultMsg delivers one discovery attempt.
type CollaborationModesResultMsg struct {
	Modes []chatwidget.CollaborationModeMask
	Err   error
}

// withCollaborationModes installs the discovered catalog (Rust
// ModelCatalog::with_collaboration_modes). Every mask keeps its name, mode,
// model, and reasoning effort, and loses any restored developer instructions
// (Rust sets `developer_instructions: Some(None)`).
func (m *Model) withCollaborationModes(modes []chatwidget.CollaborationModeMask) {
	if m == nil {
		return
	}
	m.collaborationModes = normalizeDiscoveredCollaborationModes(modes)
	m.collaborationModesDiscovered = true
}

func normalizeDiscoveredCollaborationModes(modes []chatwidget.CollaborationModeMask) []chatwidget.CollaborationModeMask {
	normalized := make([]chatwidget.CollaborationModeMask, 0, len(modes))
	for _, mask := range modes {
		clone := mask.Clone()
		clone.DeveloperInstructions = chatwidget.CollaborationClearValue()
		normalized = append(normalized, clone)
	}
	return normalized
}

// collaborationModePresets is the inventory the selection helpers filter. Before
// any discovery attempt the builtin presets stay authoritative (nil); afterwards
// the server catalog is authoritative even when it is empty, matching Rust's
// filtered_presets over ModelCatalog::collaboration_modes.
func (m *Model) collaborationModePresets() []chatwidget.CollaborationModeMask {
	if m == nil || !m.collaborationModesDiscovered {
		return nil
	}
	return m.collaborationModes
}

// filteredCollaborationPresets is the inventory the selection helpers filter
// (Rust collaboration_modes::filtered_presets). Once discovery has run the
// server catalog is authoritative even when it is empty, so a failed or empty
// discovery really does hide plan mode; before that the builtin presets stand
// in so an in-flight discovery never withdraws the modes the TUI already shows.
func (m *Model) filteredCollaborationPresets() []chatwidget.CollaborationModeMask {
	if m == nil || !m.collaborationModesDiscovered {
		return chatwidget.VisibleCollaborationModePresets(chatwidget.BuiltinCollaborationModePresets())
	}
	return chatwidget.VisibleCollaborationModePresets(m.collaborationModes)
}

// collaborationMaskForKind resolves kind against the authoritative catalog.
func (m *Model) collaborationMaskForKind(kind chatwidget.CollaborationModeKind) (chatwidget.CollaborationModeMask, bool) {
	return chatwidget.CollaborationMaskForVisibleKind(m.filteredCollaborationPresets(), kind)
}

// planModeAvailable reports whether the catalog still exposes plan mode. Rust
// keeps the `/plan` draft and reports the mode as unavailable when it does not.
func (m *Model) planModeAvailable() bool {
	_, ok := m.collaborationMaskForKind(chatwidget.CollaborationModeKindPlan)
	return ok
}

// discoverCollaborationModes runs the optional discovery. The timeout is
// applied here, not inside the loader, so a loader that never returns still
// cannot hold the TUI.
func (m *Model) discoverCollaborationModes() bubbletea.Cmd {
	if m == nil || m.onListCollaborationModes == nil {
		return nil
	}
	loader := m.onListCollaborationModes
	return func() bubbletea.Msg {
		type result struct {
			modes []chatwidget.CollaborationModeMask
			err   error
		}
		done := make(chan result, 1)
		go func() {
			modes, err := loader()
			done <- result{modes: modes, err: err}
		}()
		select {
		case value := <-done:
			return CollaborationModesResultMsg{Modes: value.modes, Err: value.err}
		case <-time.After(collaborationModeDiscoveryTimeout):
			return CollaborationModesResultMsg{Err: errCollaborationModeDiscoveryTimeout}
		}
	}
}

// applyCollaborationModesResult stores one attempt. A failure is not fatal:
// Rust logs that optional discovery is unavailable and installs an empty
// catalog, so the picker offers no server modes.
func (m *Model) applyCollaborationModesResult(message CollaborationModesResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		m.withCollaborationModes(nil)
		return
	}
	m.withCollaborationModes(message.Modes)
}
