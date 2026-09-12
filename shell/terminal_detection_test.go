package shell

import "testing"

func TestDetectTermProgram(t *testing.T) {
	env := &MapEnvironment{Values: map[string]string{
		"TERM_PROGRAM":         "iTerm.app",
		"TERM_PROGRAM_VERSION": "3.5.0",
	}}
	info := Detect(env)
	if info.Name != TerminalIterm2 {
		t.Fatalf("Name = %q, want iterm2", info.Name)
	}
	if got := info.UserAgentToken(); got != "iTerm.app/3.5.0" {
		t.Fatalf("UserAgentToken() = %q", got)
	}

	env = &MapEnvironment{Values: map[string]string{
		"TERM_PROGRAM":         "iTerm.app",
		"TERM_PROGRAM_VERSION": "",
	}}
	info = Detect(env)
	if info.Name != TerminalIterm2 || info.Version != nil {
		t.Fatalf("info = %+v, want iterm2 without version", info)
	}
	if got := info.UserAgentToken(); got != "iTerm.app" {
		t.Fatalf("UserAgentToken() = %q", got)
	}

	env = &MapEnvironment{Values: map[string]string{
		"TERM_PROGRAM":    "iTerm.app",
		"WEZTERM_VERSION": "2024.2",
	}}
	info = Detect(env)
	if info.Name != TerminalIterm2 || info.TermProgram == nil || *info.TermProgram != "iTerm.app" {
		t.Fatalf("info = %+v, want TERM_PROGRAM to win", info)
	}
}

func TestDetectNamedTerminals(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		wantName  TerminalName
		wantToken string
	}{
		{name: "iterm session", env: map[string]string{"ITERM_SESSION_ID": "w0t1p0"}, wantName: TerminalIterm2, wantToken: "iTerm.app"},
		{name: "apple term program", env: map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, wantName: TerminalAppleTerminal, wantToken: "Apple_Terminal"},
		{name: "apple session", env: map[string]string{"TERM_SESSION_ID": "A1B2C3"}, wantName: TerminalAppleTerminal, wantToken: "Apple_Terminal"},
		{name: "ghostty", env: map[string]string{"TERM_PROGRAM": "Ghostty"}, wantName: TerminalGhostty, wantToken: "Ghostty"},
		{name: "ghostty resources", env: map[string]string{"GHOSTTY_RESOURCES_DIR": "/Applications/Ghostty.app/Contents/Resources/ghostty"}, wantName: TerminalGhostty, wantToken: "Ghostty"},
		{name: "vscode", env: map[string]string{"TERM_PROGRAM": "vscode", "TERM_PROGRAM_VERSION": "1.86.0"}, wantName: TerminalVSCode, wantToken: "vscode/1.86.0"},
		{name: "warp", env: map[string]string{"TERM_PROGRAM": "WarpTerminal", "TERM_PROGRAM_VERSION": "v0.2025.12.10.08.12.stable_03"}, wantName: TerminalWarp, wantToken: "WarpTerminal/v0.2025.12.10.08.12.stable_03"},
		{name: "wezterm version", env: map[string]string{"WEZTERM_VERSION": "2024.2"}, wantName: TerminalWezTerm, wantToken: "WezTerm/2024.2"},
		{name: "wezterm term", env: map[string]string{"TERM": "wezterm"}, wantName: TerminalWezTerm, wantToken: "wezterm"},
		{name: "wezterm mux term", env: map[string]string{"TERM": "wezterm-mux"}, wantName: TerminalWezTerm, wantToken: "wezterm-mux"},
		{name: "kitty window", env: map[string]string{"KITTY_WINDOW_ID": "1"}, wantName: TerminalKitty, wantToken: "kitty"},
		{name: "kitty term program", env: map[string]string{"TERM_PROGRAM": "kitty", "TERM_PROGRAM_VERSION": "0.30.1"}, wantName: TerminalKitty, wantToken: "kitty/0.30.1"},
		{name: "alacritty socket", env: map[string]string{"ALACRITTY_SOCKET": "/tmp/alacritty"}, wantName: TerminalAlacritty, wantToken: "Alacritty"},
		{name: "alacritty term", env: map[string]string{"TERM": "alacritty"}, wantName: TerminalAlacritty, wantToken: "Alacritty"},
		{name: "konsole", env: map[string]string{"KONSOLE_VERSION": "230800"}, wantName: TerminalKonsole, wantToken: "Konsole/230800"},
		{name: "gnome", env: map[string]string{"GNOME_TERMINAL_SCREEN": "1"}, wantName: TerminalGnomeTerminal, wantToken: "gnome-terminal"},
		{name: "vte", env: map[string]string{"VTE_VERSION": "7000"}, wantName: TerminalVTE, wantToken: "VTE/7000"},
		{name: "windows terminal", env: map[string]string{"WT_SESSION": "1"}, wantName: TerminalWindowsTerminal, wantToken: "WindowsTerminal"},
		{name: "dumb", env: map[string]string{"TERM": "dumb"}, wantName: TerminalDumb, wantToken: "dumb"},
		{name: "term fallback", env: map[string]string{"TERM": "xterm-256color"}, wantName: TerminalUnknown, wantToken: "xterm-256color"},
		{name: "unknown", env: map[string]string{}, wantName: TerminalUnknown, wantToken: "unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := Detect(&MapEnvironment{Values: tc.env})
			if info.Name != tc.wantName {
				t.Fatalf("Name = %q, want %q; info = %+v", info.Name, tc.wantName, info)
			}
			if got := info.UserAgentToken(); got != tc.wantToken {
				t.Fatalf("UserAgentToken() = %q, want %q", got, tc.wantToken)
			}
		})
	}
}

func TestDetectPriority(t *testing.T) {
	info := Detect(&MapEnvironment{Values: map[string]string{
		"TERM":             "xterm-kitty",
		"ALACRITTY_SOCKET": "/tmp/alacritty",
	}})
	if info.Name != TerminalKitty {
		t.Fatalf("Name = %q, want kitty", info.Name)
	}
}

func TestDetectTmuxMultiplexer(t *testing.T) {
	env := &MapEnvironment{Values: map[string]string{
		"TMUX":                 "/tmp/tmux-1000/default,123,0",
		"TERM_PROGRAM":         "tmux",
		"TERM_PROGRAM_VERSION": "3.5a",
	}}
	info := Detect(env)
	if info.Name != TerminalUnknown || info.TermProgram != nil || info.Version != nil || info.Term != nil {
		t.Fatalf("info = %+v, want bare unknown with tmux multiplexer", info)
	}
	if info.Multiplexer == nil || info.Multiplexer.Name != MultiplexerTmux || info.Multiplexer.Version == nil || *info.Multiplexer.Version != "3.5a" {
		t.Fatalf("multiplexer = %+v, want tmux 3.5a", info.Multiplexer)
	}
	if got := info.UserAgentToken(); got != "unknown" {
		t.Fatalf("UserAgentToken() = %q, want unknown", got)
	}
}

// TestDetectTmuxPreservesSafeUnderlyingTerminalIdentifiers mirrors Rust's
// tmux_preserves_safe_underlying_terminal_identifiers (#42324): detection must
// recover the underlying terminal from environment variables instead of
// executing a PATH-provided tmux helper.
func TestDetectTmuxPreservesSafeUnderlyingTerminalIdentifiers(t *testing.T) {
	cases := []struct {
		variable string
		value    string
		wantName TerminalName
		wantVer  *string
	}{
		{variable: "WEZTERM_VERSION", value: "2024.2", wantName: TerminalWezTerm, wantVer: stringPtr("2024.2")},
		{variable: "ITERM_SESSION_ID", value: "w0t1p0", wantName: TerminalIterm2},
		{variable: "KITTY_WINDOW_ID", value: "1", wantName: TerminalKitty},
		{variable: "WT_SESSION", value: "session", wantName: TerminalWindowsTerminal},
		{variable: "ALACRITTY_SOCKET", value: "/tmp/alacritty", wantName: TerminalAlacritty},
		{variable: "GHOSTTY_RESOURCES_DIR", value: "/Applications/Ghostty.app/Contents/Resources/ghostty", wantName: TerminalGhostty},
	}
	for _, tc := range cases {
		env := &MapEnvironment{Values: map[string]string{
			"TMUX":                 "/tmp/tmux-1000/default,123,0",
			"TERM_PROGRAM":         "tmux",
			"TERM_PROGRAM_VERSION": "3.5a",
			"TERM":                 "screen-256color",
			tc.variable:            tc.value,
		}}
		info := Detect(env)
		if info.Name != tc.wantName || info.TermProgram != nil || info.Term != nil {
			t.Fatalf("%s: info = %+v, want %q without term program or term", tc.variable, info, tc.wantName)
		}
		if (info.Version == nil) != (tc.wantVer == nil) || (tc.wantVer != nil && *info.Version != *tc.wantVer) {
			t.Fatalf("%s: version = %v, want %v", tc.variable, info.Version, tc.wantVer)
		}
		if info.Multiplexer == nil || info.Multiplexer.Name != MultiplexerTmux || info.Multiplexer.Version == nil || *info.Multiplexer.Version != "3.5a" {
			t.Fatalf("%s: multiplexer = %+v, want tmux 3.5a", tc.variable, info.Multiplexer)
		}
	}
}

func TestDetectZellijAndSanitizesUserAgent(t *testing.T) {
	env := &MapEnvironment{Values: map[string]string{"ZELLIJ": "1", "WEZTERM_VERSION": "20240203 beta"}}
	info := Detect(env)
	if !info.IsZellij() {
		t.Fatalf("IsZellij() = false")
	}
	if got := info.UserAgentToken(); got != "WezTerm/20240203_beta" {
		t.Fatalf("UserAgentToken() = %q", got)
	}
}

func TestDetectZellijVersion(t *testing.T) {
	info := Detect(&MapEnvironment{Values: map[string]string{"ZELLIJ_VERSION": "0.43.1"}})
	if info.Multiplexer == nil || info.Multiplexer.Name != MultiplexerZellij {
		t.Fatalf("multiplexer = %+v, want zellij", info.Multiplexer)
	}
	if info.Multiplexer.Version == nil || *info.Multiplexer.Version != "0.43.1" {
		t.Fatalf("multiplexer version = %+v, want 0.43.1", info.Multiplexer.Version)
	}
}

func TestTerminalInfoIsZellij(t *testing.T) {
	zellij := &TerminalInfo{Name: TerminalUnknown, Multiplexer: &Multiplexer{Name: MultiplexerZellij}}
	if !zellij.IsZellij() {
		t.Fatal("IsZellij() = false")
	}

	tmux := &TerminalInfo{Name: TerminalUnknown, Multiplexer: &Multiplexer{Name: MultiplexerTmux}}
	if tmux.IsZellij() {
		t.Fatal("IsZellij() = true for tmux")
	}
}

func stringPtr(value string) *string {
	return &value
}
