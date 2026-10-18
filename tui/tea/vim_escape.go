package tea

import (
	"runtime"
	"unicode"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
)

// Rust parity: codex-rs/tui/src/app/input.rs
// App::should_recover_vim_insert_escape (#42584). Legacy terminals encode
// Alt+character and Esc+character identically, so an Alt character that no
// active binding claims is replayed as Esc followed by the plain character.

func legacyVimEscapeRecoverySupported() bool {
	switch runtime.GOOS {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "linux", "netbsd", "openbsd", "solaris":
		return true
	default:
		return false
	}
}

// shouldRecoverVimInsertEscape reports whether msg is an ambiguous Alt+character
// stroke that should be split. The unix argument exists so tests can exercise
// the Unix-only behavior on every platform.
func (m *Model) shouldRecoverVimInsertEscape(msg bubbletea.KeyMsg, unix bool) bool {
	if m == nil {
		return false
	}
	if msg.Type != bubbletea.KeyRunes || len(msg.Runes) != 1 || msg.Paste || !msg.Alt {
		return false
	}
	character := msg.Runes[0]
	if !unicode.IsPrint(character) {
		// Control bytes are Ctrl/AltGr combinations, not the Esc prefix
		// ambiguity this recovery targets.
		return false
	}
	modalOrPopupActive := m.modal != nil || m.slashPopup.Active || m.skillPopup.Active ||
		m.mentionPopup != nil || m.agentsOverview != nil || m.readOnlyThread || m.windowsSandboxSetupActive ||
		m.warningsView != nil || m.quickDialMenu || m.mainFind.active() || m.activityFocus.active
	spec := keySpecFromKeyMsg(msg)
	imagePaste := character == 'v' || character == 'V'
	agentNavigation := m.composerIsEmpty() && (spec == "alt-b" || spec == "alt-f")
	return tuiapp.ShouldRecoverVimInsertEscape(
		unix,
		false, // Bubble Tea does not enable the enhanced keyboard protocol.
		m.overlay != nil,
		modalOrPopupActive,
		true,
		m.shouldHandleVimInsertEscape(),
		imagePaste,
		agentNavigation,
		m.keymapClaimsAltCharacter(spec),
	)
}

// keymapClaimsAltCharacter reports whether an active main-surface binding uses
// the ambiguous Alt+character stroke. Such bindings keep their existing meaning
// instead of being split into Esc plus the character.
func (m *Model) keymapClaimsAltCharacter(spec string) bool {
	if m == nil || spec == "" {
		return false
	}
	for _, action := range codextui.KeymapActions(codextui.KeymapActionFilter{FastModeEnabled: true}) {
		switch action.Context {
		case "global", "chat", "voice", "composer", "editor", "vim_normal", "vim_operator", "vim_text_object":
		default:
			continue
		}
		if m.keyMatches(action.Context, action.Action, spec) {
			return true
		}
	}
	return false
}
