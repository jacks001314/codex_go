package app

import (
	"strconv"
	"strings"

	"codex_go/shell"
)

// Rust parity subset: codex-rs/tui/src/local_settings.rs's `copy_on_select`
// (#48469). The fullscreen TUI copies a transcript selection when the mouse
// button is released unless the terminal is known to forward its own copy
// shortcut.
//
// Go's fullscreen (alt-screen) program deliberately leaves terminal mouse
// tracking off so the terminal keeps its own selection/copy (see
// tui/tea/right_click_paste.go's parity note). The decision function below is
// therefore the aligned config semantics for the owned-transcript lane, while
// the Go runtime owes its copy-on-release behavior to the terminal; the
// app-owned selection consumer remains unported.

// CopyOnSelectMode is the `tui.copy_on_select` value. Rust deserializes it
// strictly (an unknown spelling fails the config load); Go's `[tui]` sub-table is
// not value-validated, so the resolver falls back to `auto`.
type CopyOnSelectMode string

const (
	CopyOnSelectAlways CopyOnSelectMode = "always"
	CopyOnSelectNever  CopyOnSelectMode = "never"
	CopyOnSelectAuto   CopyOnSelectMode = "auto"
)

// ParseCopyOnSelectMode resolves a configured value, defaulting to `auto`
// exactly like Rust's `#[serde(default)]` enum.
func ParseCopyOnSelectMode(raw string) CopyOnSelectMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(CopyOnSelectAlways):
		return CopyOnSelectAlways
	case string(CopyOnSelectNever):
		return CopyOnSelectNever
	default:
		return CopyOnSelectAuto
	}
}

// CopyOnSelectForTerminal mirrors Rust's `LocalSettings::copy_on_select`
// (#48469): copying is the default, and only a direct terminal that is known to
// forward its own copy shortcut suppresses it. `goos` stands in for Rust's
// compile-time target, because two of the arms are platform-specific.
func CopyOnSelectForTerminal(mode string, terminal *shell.TerminalInfo, goos string) bool {
	switch ParseCopyOnSelectMode(mode) {
	case CopyOnSelectAlways:
		return true
	case CopyOnSelectNever:
		return false
	}
	if terminal == nil {
		return true
	}
	if terminal.Multiplexer != nil {
		// Multiplexers own the mouse and forward their own copy shortcut.
		return true
	}
	switch terminal.Name {
	case shell.TerminalGhostty:
		// Since 1.2, Ghostty's Cmd-C and Ctrl-Shift-C bindings forward the key
		// when Ghostty has no selection.
		return !ghosttyForwardsCopy(terminal.Version)
	case shell.TerminalKitty:
		// Kitty's Cmd-C uses copy_or_noop; its Ctrl-Shift-C consumes the key.
		return goos != "darwin"
	case shell.TerminalWindowsTerminal:
		// Windows Terminal's Copy action forwards its key when nothing is
		// selected, including when the CLI runs in WSL.
		return false
	case shell.TerminalVSCode:
		// VS Code gates Copy on a native selection; only Windows' plain Ctrl-C
		// is also forwarded by the legacy xterm.js encoder.
		return goos != "windows"
	default:
		// Apple Terminal, iTerm2, Warp, WezTerm, Alacritty, Konsole, GNOME
		// Terminal, VTE, Dumb and unknown terminals all default to copying.
		return true
	}
}

// ghosttyForwardsCopy reports whether a Ghostty version is at least 1.2.0, so
// its own copy shortcut reaches the application. A missing, unparseable or
// pre-release version does not (Rust's `semver::Version::parse` plus `>= 1.2.0`
// treats a pre-release of 1.2.0 as older than the release).
func ghosttyForwardsCopy(version *string) bool {
	if version == nil {
		return false
	}
	major, minor, patch, prerelease, ok := parseSemverVersion(strings.TrimSpace(*version))
	if !ok {
		return false
	}
	switch {
	case major != 1:
		return major > 1
	case minor != 2:
		return minor > 2
	case patch != 0:
		return patch > 0
	default:
		// A release (no pre-release identifiers) is >= 1.2.0; a 1.2.x
		// pre-release sorts below the release and does not forward.
		return !prerelease
	}
}

// parseSemverVersion parses Rust's `semver::Version` shape (major.minor.patch
// with an optional pre-release and build metadata) and reports the pre-release
// presence. A leading `v`, a missing component or a non-numeric component fails,
// as it does in Rust.
func parseSemverVersion(version string) (major int, minor int, patch int, prerelease bool, ok bool) {
	if version == "" {
		return 0, 0, 0, false, false
	}
	if core, _, found := strings.Cut(version, "+"); found {
		version = core
	}
	if core, _, found := strings.Cut(version, "-"); found {
		prerelease = core != version
		version = core
	}
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false, false
	}
	numbers := make([]int, 0, 3)
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return 0, 0, 0, false, false
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return 0, 0, 0, false, false
		}
		numbers = append(numbers, value)
	}
	return numbers[0], numbers[1], numbers[2], prerelease, true
}
