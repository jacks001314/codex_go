// Package shell terminal detection feeds terminal metadata into user-agent
// logging and terminal-specific configuration. Detection only reads the
// environment; it must not execute helpers selected by an untrusted PATH
// (Rust #42324).
package shell

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

type TerminalName string

const (
	TerminalAppleTerminal   TerminalName = "appleTerminal"
	TerminalGhostty         TerminalName = "ghostty"
	TerminalIterm2          TerminalName = "iterm2"
	TerminalWarp            TerminalName = "warpTerminal"
	TerminalVSCode          TerminalName = "vscode"
	TerminalWezTerm         TerminalName = "wezterm"
	TerminalKitty           TerminalName = "kitty"
	TerminalAlacritty       TerminalName = "alacritty"
	TerminalKonsole         TerminalName = "konsole"
	TerminalGnomeTerminal   TerminalName = "gnomeTerminal"
	TerminalVTE             TerminalName = "vte"
	TerminalWindowsTerminal TerminalName = "windowsTerminal"
	TerminalDumb            TerminalName = "dumb"
	TerminalUnknown         TerminalName = "unknown"
)

type MultiplexerName string

const (
	MultiplexerTmux   MultiplexerName = "tmux"
	MultiplexerZellij MultiplexerName = "zellij"
)

type Multiplexer struct {
	Name    MultiplexerName `json:"name"`
	Version *string         `json:"version,omitempty"`
}

// TerminalBackendKind identifies a rendering backend discovered from the
// environment independently of the terminal application identity.
type TerminalBackendKind string

const (
	// TerminalBackendVTE covers VTE-based terminals such as GNOME Terminal.
	TerminalBackendVTE TerminalBackendKind = "vte"
)

// TerminalBackend is backend metadata observed independently of the terminal
// application identity (Rust #57612). Version uses VTE's own encoding
// (major*10_000 + minor*100 + micro) and is nil when the environment value is
// missing or malformed.
type TerminalBackend struct {
	Kind    TerminalBackendKind `json:"kind"`
	Version *uint32             `json:"version,omitempty"`
}

type TerminalInfo struct {
	Name        TerminalName `json:"name"`
	TermProgram *string      `json:"termProgram,omitempty"`
	Version     *string      `json:"version,omitempty"`
	Term        *string      `json:"term,omitempty"`
	Multiplexer *Multiplexer `json:"multiplexer,omitempty"`
	// Backend is set independently of Name: GNOME Terminal and other
	// applications still report their VTE backend version.
	Backend *TerminalBackend `json:"backend,omitempty"`
}

func (t *TerminalInfo) UserAgentToken() string {
	if t == nil {
		return "unknown"
	}
	raw := ""
	switch {
	case t.TermProgram != nil && *t.TermProgram != "":
		raw = *t.TermProgram
		if t.Version != nil && *t.Version != "" {
			raw += "/" + *t.Version
		}
	case t.Term != nil && *t.Term != "":
		raw = *t.Term
	default:
		raw = terminalDisplayToken(t.Name, t.Version)
	}
	return sanitizeHeaderValue(raw)
}

func (t *TerminalInfo) IsZellij() bool {
	return t != nil && t.Multiplexer != nil && t.Multiplexer.Name == MultiplexerZellij
}

type Environment interface {
	Var(name string) (string, bool)
}

type ProcessEnvironment struct{}

func (e *ProcessEnvironment) Var(name string) (string, bool) {
	return os.LookupEnv(name)
}

type MapEnvironment struct {
	Values map[string]string
}

func (e *MapEnvironment) Var(name string) (string, bool) {
	if e == nil || e.Values == nil {
		return "", false
	}
	value, ok := e.Values[name]
	return value, ok
}

var (
	currentOnce sync.Once
	currentInfo *TerminalInfo
)

func CurrentTerminalInfo() *TerminalInfo {
	currentOnce.Do(func() {
		currentInfo = Detect(&ProcessEnvironment{})
	})
	return cloneInfo(currentInfo)
}

func UserAgent() string {
	return CurrentTerminalInfo().UserAgentToken()
}

// Detect mirrors Rust's detect_terminal_info_from_env
// (terminal-detection/src/lib.rs): TERM_PROGRAM (plus TERM_PROGRAM_VERSION)
// drives the detected terminal name unless it identifies tmux, terminal-specific
// variables (Ghostty, WezTerm, iTerm2, kitty, ...) are probed next, and TERM is
// the capability fallback. TERM_SESSION_ID is not terminal-specific and cannot
// identify Apple Terminal (Rust #53438); Apple Terminal is still detected
// through TERM_PROGRAM=Apple_Terminal.
func Detect(env Environment) *TerminalInfo {
	if env == nil {
		env = &ProcessEnvironment{}
	}
	terminal := detectTerminalIdentity(env)
	// Identity probes can return before VTE is considered. Keep its backend
	// metadata even when GNOME or another TERM_PROGRAM identifies the
	// application (Rust #57612).
	vteVersion, hasVTEVersion := env.Var("VTE_VERSION")
	if hasVTEVersion || terminal.Name == TerminalVTE {
		backend := &TerminalBackend{Kind: TerminalBackendVTE}
		raw, hasRaw := vteVersion, hasVTEVersion
		if !hasRaw && terminal.Version != nil {
			// The raw VTE_VERSION keeps precedence over the terminal version so
			// a malformed value stays unknown instead of falling back.
			raw, hasRaw = *terminal.Version, true
		}
		if hasRaw {
			if version, ok := parseVTEVersion(raw); ok {
				backend.Version = &version
			}
		}
		terminal.Backend = backend
	}
	return terminal
}

// parseVTEVersion mirrors Rust's `str::parse::<u32>()` on VTE_VERSION: an
// optional leading `+` is accepted, any other non-numeric input is unknown.
func parseVTEVersion(value string) (uint32, bool) {
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, "+"), 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(parsed), true
}

func detectTerminalIdentity(env Environment) *TerminalInfo {
	multiplexer := detectMultiplexer(env)
	if termProgram := nonEmptyEnv(env, "TERM_PROGRAM"); termProgram != nil && !strings.EqualFold(*termProgram, "tmux") {
		return fromTermProgram(terminalNameFromTermProgram(*termProgram), *termProgram, nonEmptyEnv(env, "TERM_PROGRAM_VERSION"), multiplexer)
	}
	if hasNonEmptyEnv(env, "GHOSTTY_RESOURCES_DIR") {
		return fromName(TerminalGhostty, nil, multiplexer)
	}
	if hasEnv(env, "WEZTERM_VERSION") {
		return fromName(TerminalWezTerm, nonEmptyEnv(env, "WEZTERM_VERSION"), multiplexer)
	}
	if hasEnv(env, "ITERM_SESSION_ID") || hasEnv(env, "ITERM_PROFILE") || hasEnv(env, "ITERM_PROFILE_NAME") {
		return fromName(TerminalIterm2, nil, multiplexer)
	}
	if hasEnv(env, "KITTY_WINDOW_ID") || envContains(env, "TERM", "kitty") {
		return fromName(TerminalKitty, nil, multiplexer)
	}
	if hasEnv(env, "ALACRITTY_SOCKET") || envEquals(env, "TERM", "alacritty") {
		return fromName(TerminalAlacritty, nil, multiplexer)
	}
	if hasEnv(env, "KONSOLE_VERSION") {
		return fromName(TerminalKonsole, nonEmptyEnv(env, "KONSOLE_VERSION"), multiplexer)
	}
	if hasEnv(env, "GNOME_TERMINAL_SCREEN") {
		return fromName(TerminalGnomeTerminal, nil, multiplexer)
	}
	if hasEnv(env, "VTE_VERSION") {
		return fromName(TerminalVTE, nonEmptyEnv(env, "VTE_VERSION"), multiplexer)
	}
	if hasEnv(env, "WT_SESSION") {
		return fromName(TerminalWindowsTerminal, nil, multiplexer)
	}
	if term := nonEmptyEnv(env, "TERM"); term != nil {
		return fromTerm(*term, multiplexer)
	}
	return fromName(TerminalUnknown, nil, multiplexer)
}

func detectMultiplexer(env Environment) *Multiplexer {
	if hasNonEmptyEnv(env, "TMUX") || hasNonEmptyEnv(env, "TMUX_PANE") {
		return &Multiplexer{Name: MultiplexerTmux, Version: tmuxVersionFromEnv(env)}
	}
	if hasNonEmptyEnv(env, "ZELLIJ") || hasNonEmptyEnv(env, "ZELLIJ_SESSION_NAME") || hasNonEmptyEnv(env, "ZELLIJ_VERSION") {
		return &Multiplexer{Name: MultiplexerZellij, Version: nonEmptyEnv(env, "ZELLIJ_VERSION")}
	}
	return nil
}

func fromTermProgram(name TerminalName, program string, version *string, multiplexer *Multiplexer) *TerminalInfo {
	return &TerminalInfo{Name: name, TermProgram: &program, Version: cloneString(version), Multiplexer: cloneMultiplexer(multiplexer)}
}

func fromName(name TerminalName, version *string, multiplexer *Multiplexer) *TerminalInfo {
	return &TerminalInfo{Name: name, Version: cloneString(version), Multiplexer: cloneMultiplexer(multiplexer)}
}

func fromTerm(term string, multiplexer *Multiplexer) *TerminalInfo {
	name := TerminalUnknown
	switch term {
	case "dumb":
		name = TerminalDumb
	case "wezterm", "wezterm-mux":
		name = TerminalWezTerm
	}
	return &TerminalInfo{Name: name, Term: &term, Multiplexer: cloneMultiplexer(multiplexer)}
}

func tmuxVersionFromEnv(env Environment) *string {
	if termProgram := nonEmptyEnv(env, "TERM_PROGRAM"); termProgram == nil || !strings.EqualFold(*termProgram, "tmux") {
		return nil
	}
	return nonEmptyEnv(env, "TERM_PROGRAM_VERSION")
}

func terminalNameFromTermProgram(value string) TerminalName {
	normalized := strings.ToLower(strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '_', '.':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(value)))
	switch normalized {
	case "appleterminal":
		return TerminalAppleTerminal
	case "ghostty":
		return TerminalGhostty
	case "iterm", "iterm2", "itermapp":
		return TerminalIterm2
	case "warp", "warpterminal":
		return TerminalWarp
	case "vscode":
		return TerminalVSCode
	case "wezterm":
		return TerminalWezTerm
	case "kitty":
		return TerminalKitty
	case "alacritty":
		return TerminalAlacritty
	case "konsole":
		return TerminalKonsole
	case "gnometerminal":
		return TerminalGnomeTerminal
	case "vte":
		return TerminalVTE
	case "windowsterminal":
		return TerminalWindowsTerminal
	case "dumb":
		return TerminalDumb
	default:
		return TerminalUnknown
	}
}

func terminalDisplayToken(name TerminalName, version *string) string {
	base := "unknown"
	switch name {
	case TerminalAppleTerminal:
		base = "Apple_Terminal"
	case TerminalGhostty:
		base = "Ghostty"
	case TerminalIterm2:
		base = "iTerm.app"
	case TerminalWarp:
		base = "WarpTerminal"
	case TerminalVSCode:
		base = "vscode"
	case TerminalWezTerm:
		base = "WezTerm"
	case TerminalKitty:
		base = "kitty"
	case TerminalAlacritty:
		base = "Alacritty"
	case TerminalKonsole:
		base = "Konsole"
	case TerminalGnomeTerminal:
		base = "gnome-terminal"
	case TerminalVTE:
		base = "VTE"
	case TerminalWindowsTerminal:
		base = "WindowsTerminal"
	case TerminalDumb:
		base = "dumb"
	}
	if version != nil && *version != "" {
		return base + "/" + *version
	}
	return base
}

func sanitizeHeaderValue(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' || char == '/' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func nonEmptyEnv(env Environment, name string) *string {
	value, ok := env.Var(name)
	if !ok {
		return nil
	}
	return noneIfWhitespace(value)
}

func hasEnv(env Environment, name string) bool {
	_, ok := env.Var(name)
	return ok
}

func hasNonEmptyEnv(env Environment, name string) bool {
	return nonEmptyEnv(env, name) != nil
}

func envContains(env Environment, name string, needle string) bool {
	value, ok := env.Var(name)
	return ok && strings.Contains(value, needle)
}

func envEquals(env Environment, name string, expected string) bool {
	value, ok := env.Var(name)
	return ok && value == expected
}

func noneIfWhitespace(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneMultiplexer(value *Multiplexer) *Multiplexer {
	if value == nil {
		return nil
	}
	return &Multiplexer{Name: value.Name, Version: cloneString(value.Version)}
}

func cloneInfo(value *TerminalInfo) *TerminalInfo {
	if value == nil {
		return &TerminalInfo{Name: TerminalUnknown}
	}
	return &TerminalInfo{
		Name:        value.Name,
		TermProgram: cloneString(value.TermProgram),
		Version:     cloneString(value.Version),
		Term:        cloneString(value.Term),
		Multiplexer: cloneMultiplexer(value.Multiplexer),
		Backend:     cloneTerminalBackend(value.Backend),
	}
}

func cloneTerminalBackend(value *TerminalBackend) *TerminalBackend {
	if value == nil {
		return nil
	}
	cloned := &TerminalBackend{Kind: value.Kind}
	if value.Version != nil {
		version := *value.Version
		cloned.Version = &version
	}
	return cloned
}
