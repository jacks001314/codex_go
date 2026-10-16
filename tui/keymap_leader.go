package tui

import (
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"
)

// Rust #52273 (a3f3b44cd8): configurable persistent leader shortcuts.
//
// Mirrored faces and their upstream anchors:
//   - codex-rs/tui/src/keymap/leader.rs:11-70  (resolve: default prefix, shadowing, per-prefix validation)
//   - codex-rs/tui/src/keymap.rs:2469-2491     (MAIN_RESERVED_BINDINGS)
//   - codex-rs/tui/src/keymap_setup/actions.rs:450-467 (resolved labels in the action summary)
//   - codex-rs/config/src/tui_keymap.rs:589-609 (`leader <key>` symbolic bindings)
//
// The chord-runtime half of the upstream change (persistent pending menus, the
// 1024-combination cap, cancel-on-paste/focus/mouse, leader-aware overlap rules)
// has no Go carrier: Go has no chord matcher at all
// (`git grep -c 'KeyChord|chordPrefix|PendingChord' -- '*.go'` = 0), so this file
// only resolves and displays leader prefixes. The drift is recorded in
// update/r90tui_2026_10_16.md section T8.

// defaultLeaderBinding is Rust `key_hint::ctrl(KeyCode::Char('x'))`
// (keymap/leader.rs:12).
const defaultLeaderBinding = "ctrl-x"

// leaderReservedBindings mirrors MAIN_RESERVED_BINDINGS
// (codex-rs/tui/src/keymap.rs:2469-2491): a leader prefix may not collide with a
// binding the TUI reserves for itself.
var leaderReservedBindings = []struct {
	Action  string
	Binding string
}{
	{"fixed.interrupt_or_quit", "ctrl-c"},
	{"fixed.quit", "ctrl-d"},
	{"fixed.paste_image", "ctrl-v"},
	{"fixed.paste_image", "ctrl-alt-v"},
	{"fixed.cycle_collaboration_mode", "shift-tab"},
	{"fixed.backtrack", "esc"},
	{"fixed.previous_agent", "alt-left"},
	{"fixed.next_agent", "alt-right"},
	{"fixed.slash_command", "/"},
	{"fixed.shell_command", "!"},
	{"fixed.file_paths", "@"},
	{"fixed.connector_mentions", "$"},
}

// LeaderKeybindings resolves the prefix set used by `leader ...` shortcuts.
//
// It mirrors `keymap/leader.rs::resolve`: the default prefix is ctrl-x, it
// yields to an existing ctrl-x alias anywhere in the keymap, a configured
// prefix replaces it, a multi-stroke value is rejected, and every resolved
// prefix is validated so ordinary text input is never intercepted.
//
// Go has no chord matcher, so the upstream rule "the default also yields to an
// existing chord prefix" (leader.rs:13-21) has no carrier here.
func LeaderKeybindings(config *KeymapConfig) ([]string, error) {
	configured := []string(nil)
	hasOverride := false
	if config != nil {
		configured, hasOverride = config.Binding("global", "leader")
	}
	for _, spec := range configured {
		if strings.Contains(spec, " ") {
			return nil, fmt.Errorf("Invalid `tui.keymap.global.leader`: use a single prefix key.")
		}
	}
	prefixes := make([]string, 0, len(configured)+1)
	switch {
	case hasOverride:
		prefixes = append(prefixes, configured...)
	case config == nil || !config.contextAliasConfigured(defaultLeaderBinding):
		prefixes = append(prefixes, defaultLeaderBinding)
	}
	resolved := make([]string, 0, len(prefixes))
	seen := map[string]bool{}
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" || seen[prefix] {
			continue
		}
		seen[prefix] = true
		if err := ValidateLeaderPrefix(prefix); err != nil {
			return nil, err
		}
		resolved = append(resolved, prefix)
	}
	return resolved, nil
}

// ValidateLeaderPrefix applies the per-prefix rules from keymap/leader.rs:42-71,
// in the upstream order: plain-text characters are rejected first, then the
// Unix ctrl-z reservation, then the reserved fixed bindings. The order is
// observable: `/`, `!`, `@` and `$` are bare characters, so Rust reports the
// text-input error rather than the reservation error even though those keys are
// also reserved.
func ValidateLeaderPrefix(prefix string) error {
	if leaderPrefixIsBareCharacter(prefix) {
		return fmt.Errorf("Invalid `tui.keymap.global.leader` = `%s`: use ctrl, alt, super, or a non-character key so ordinary text input is not intercepted. Ctrl-alt characters may be AltGr input on Windows.", prefix)
	}
	if runtime.GOOS != "windows" && prefix == "ctrl-z" {
		return fmt.Errorf("Invalid `tui.keymap.global.leader` = `%s`: ctrl-z is reserved for suspending the terminal on Unix.", prefix)
	}
	for _, reserved := range leaderReservedBindings {
		if reserved.Binding == prefix {
			return fmt.Errorf("Invalid `tui.keymap.global.leader` = `%s`: this key is reserved by `%s`.", prefix, reserved.Action)
		}
	}
	return nil
}

// leaderPrefixIsBareCharacter reports whether the prefix is a character key
// without ctrl or alt. Rust rejects those (`KeyCode::Char(_)` without
// ctrl/alt), because a leader prefix swallows the key it matches
// (keymap/leader.rs:47-52). Go models characters as single-rune segments and
// names every other key, so a single-rune key segment without ctrl/alt is the
// same condition; `space` is a character in Rust as well.
func leaderPrefixIsBareCharacter(prefix string) bool {
	segments := strings.Split(prefix, "-")
	key := segments[len(segments)-1]
	for _, modifier := range segments[:len(segments)-1] {
		switch modifier {
		case "ctrl", "control", "alt", "option":
			return false
		}
	}
	if key == "space" {
		return true
	}
	return utf8.RuneCountInString(key) == 1
}

// LeaderSpecCompletion reports the completion key of a `leader <key>` spec.
func LeaderSpecCompletion(spec string) (string, bool) {
	completion, ok := strings.CutPrefix(strings.TrimSpace(spec), "leader ")
	completion = strings.TrimSpace(completion)
	if !ok || completion == "" || strings.Contains(completion, " ") {
		return "", false
	}
	return completion, true
}

// LeaderSpecSummary renders one configured spec for the shortcut list and the
// action view, resolving `leader <key>` against the active prefixes and
// reporting an unbound leader as inactive
// (keymap_setup/actions.rs:450-467).
func LeaderSpecSummary(prefixes []string, spec string) string {
	completion, ok := LeaderSpecCompletion(spec)
	if !ok {
		return spec
	}
	if len(prefixes) == 0 {
		return spec + " (inactive)"
	}
	resolved := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		resolved = append(resolved, prefix+" "+completion)
	}
	return spec + " (" + strings.Join(resolved, ", ") + ")"
}

// KeymapDisplayBindings renders the resolved bindings for the shortcut list and
// the action view. A `leader <key>` spec is shown together with the concrete
// strokes it resolves to, or marked inactive when no prefix is active
// (keymap_setup/actions.rs:450-467).
func KeymapDisplayBindings(config *KeymapConfig, bindings []string) []string {
	if len(bindings) == 0 {
		return bindings
	}
	hasLeader := false
	for _, binding := range bindings {
		if _, ok := LeaderSpecCompletion(binding); ok {
			hasLeader = true
			break
		}
	}
	if !hasLeader {
		return bindings
	}
	prefixes, err := LeaderKeybindings(config)
	if err != nil {
		prefixes = nil
	}
	display := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		display = append(display, LeaderSpecSummary(prefixes, binding))
	}
	return display
}
