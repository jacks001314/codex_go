package app

import (
	"testing"

	"codex_go/shell"
)

// TestParseCopyOnSelectMode mirrors Rust's `tui.copy_on_select` value
// resolution: the three lowercase spellings are recognized and any other value
// falls back to `auto`, matching `#[serde(default)] CopyOnSelect::Auto`.
func TestParseCopyOnSelectMode(t *testing.T) {
	cases := map[string]CopyOnSelectMode{
		"always":  CopyOnSelectAlways,
		"never":   CopyOnSelectNever,
		"auto":    CopyOnSelectAuto,
		"":        CopyOnSelectAuto,
		"ALWAYS":  CopyOnSelectAlways,
		" Never ": CopyOnSelectNever,
		"bogus":   CopyOnSelectAuto,
	}
	for raw, want := range cases {
		if got := ParseCopyOnSelectMode(raw); got != want {
			t.Fatalf("ParseCopyOnSelectMode(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestCopyOnSelectForTerminalMatchesRustDefaults mirrors Rust's
// `copy_on_select_respects_terminal_defaults_and_config_overrides` terminal
// matrix from #48469: copying is the default and only a direct terminal that
// forwards its own copy shortcut suppresses it. Kitty and VS Code are
// platform-sensitive, so the matrix is evaluated against macOS and Windows.
func TestCopyOnSelectForTerminalMatchesRustDefaults(t *testing.T) {
	tests := []struct {
		name        string
		terminal    shell.TerminalName
		version     *string
		multiplexer *shell.Multiplexer
	}{
		{"iterm2", shell.TerminalIterm2, nil, nil},
		{"appleTerminal", shell.TerminalAppleTerminal, nil, nil},
		{"ghostty 1.2.0", shell.TerminalGhostty, strptr("1.2.0"), nil},
		{"ghostty 1.3.0", shell.TerminalGhostty, strptr("1.3.0"), nil},
		{"ghostty 1.1.3", shell.TerminalGhostty, strptr("1.1.3"), nil},
		{"ghostty 1.2.0-dev", shell.TerminalGhostty, strptr("1.2.0-dev"), nil},
		{"ghostty invalid", shell.TerminalGhostty, strptr("invalid"), nil},
		{"ghostty unknown version", shell.TerminalGhostty, nil, nil},
		{"kitty", shell.TerminalKitty, nil, nil},
		{"windowsTerminal", shell.TerminalWindowsTerminal, nil, nil},
		{"vscode", shell.TerminalVSCode, nil, nil},
		{"alacritty", shell.TerminalAlacritty, nil, nil},
		{"gnomeTerminal", shell.TerminalGnomeTerminal, nil, nil},
		{"konsole", shell.TerminalKonsole, nil, nil},
		{"vte", shell.TerminalVTE, nil, nil},
		{"warp", shell.TerminalWarp, nil, nil},
		{"wezterm", shell.TerminalWezTerm, nil, nil},
		{"dumb", shell.TerminalDumb, nil, nil},
		{"unknown", shell.TerminalUnknown, nil, nil},
		{"ghostty under tmux", shell.TerminalGhostty, strptr("1.3.0"), &shell.Multiplexer{Name: shell.MultiplexerTmux}},
		{"kitty under zellij", shell.TerminalKitty, nil, &shell.Multiplexer{Name: shell.MultiplexerZellij}},
		{"windowsTerminal under tmux", shell.TerminalWindowsTerminal, nil, &shell.Multiplexer{Name: shell.MultiplexerTmux}},
	}

	// Rust evaluates `cfg!(target_os = "macos")` / `cfg!(target_os = "windows")`
	// at compile time; Go threads the host OS in, so the platform-sensitive
	// arms are checked under both targets plus an unrelated one.
	for _, goos := range []string{"darwin", "windows", "linux"} {
		kittyCopy := goos != "darwin"
		vscodeCopy := goos != "windows"
		for _, tc := range tests {
			want := true
			switch {
			case tc.multiplexer != nil:
				want = true
			case tc.terminal == shell.TerminalGhostty:
				want = !ghosttyForwardsCopy(tc.version)
			case tc.terminal == shell.TerminalKitty:
				want = kittyCopy
			case tc.terminal == shell.TerminalWindowsTerminal:
				want = false
			case tc.terminal == shell.TerminalVSCode:
				want = vscodeCopy
			}
			terminal := &shell.TerminalInfo{Name: tc.terminal, Version: tc.version, Multiplexer: tc.multiplexer}
			if got := CopyOnSelectForTerminal("auto", terminal, goos); got != want {
				t.Fatalf("goos=%s terminal=%s version=%v multiplexer=%v: CopyOnSelectForTerminal = %v, want %v",
					goos, tc.name, deref(tc.version), derefMultiplexer(tc.multiplexer), got, want)
			}
		}
	}
}

// TestCopyOnSelectOverridesWin pins that explicit `always`/`never` overrides
// beat the terminal default (Rust's config/launch override matrix), and that a
// nil terminal still copies under `auto`.
func TestCopyOnSelectOverridesWin(t *testing.T) {
	terminal := &shell.TerminalInfo{Name: shell.TerminalGhostty, Version: strptr("1.3.0")}
	if got := CopyOnSelectForTerminal("always", terminal, "darwin"); !got {
		t.Fatalf("always override = %v, want true", got)
	}
	if got := CopyOnSelectForTerminal("never", terminal, "darwin"); got {
		t.Fatalf("never override = %v, want false", got)
	}
	if got := CopyOnSelectForTerminal(" NEVER ", terminal, "darwin"); got {
		t.Fatalf("normalized never override = %v, want false", got)
	}
	if got := CopyOnSelectForTerminal("auto", nil, "darwin"); !got {
		t.Fatalf("auto without a terminal = %v, want true", got)
	}
}

// TestGhosttyForwardsCopySemverMatrix pins the semver boundary Rust applies:
// Ghostty 1.2.0 or newer forwards its own copy shortcut, while a missing,
// unparseable or pre-release version does not (a 1.2.0 pre-release sorts below
// the 1.2.0 release).
func TestGhosttyForwardsCopySemverMatrix(t *testing.T) {
	cases := map[string]bool{
		"1.2.0":       true,
		"1.2.1":       true,
		"1.3.0":       true,
		"2.0.0":       true,
		"1.2.0+build": true,
		"1.2.0-dev":   false,
		"1.1.3":       false,
		"0.9.0":       false,
		"invalid":     false,
		"v1.2.0":      false,
		"1.2":         false,
		"01.2.0":      false,
	}
	for version, want := range cases {
		if got := ghosttyForwardsCopy(strptr(version)); got != want {
			t.Fatalf("ghosttyForwardsCopy(%q) = %v, want %v", version, got, want)
		}
	}
	if ghosttyForwardsCopy(nil) {
		t.Fatal("ghosttyForwardsCopy(nil) = true, want false")
	}
}

func strptr(value string) *string {
	return &value
}

func deref(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}

func derefMultiplexer(multiplexer *shell.Multiplexer) string {
	if multiplexer == nil {
		return "<nil>"
	}
	return string(multiplexer.Name)
}
