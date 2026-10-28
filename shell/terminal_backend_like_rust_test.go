package shell

import "testing"

// Mirrors Rust #57612: backend metadata is captured even when the terminal
// application identity (GNOME Terminal here) takes precedence over the VTE
// probe.
func TestDetectCapturesVTEBackendAlongsideGnomeIdentityLikeRust(t *testing.T) {
	info := Detect(&MapEnvironment{Values: map[string]string{
		"GNOME_TERMINAL_SCREEN": "1",
		"VTE_VERSION":           "5204",
		"TMUX":                  "session",
	}})
	if info.Name != TerminalGnomeTerminal {
		t.Fatalf("Name = %q, want gnomeTerminal", info.Name)
	}
	if info.Version != nil {
		t.Fatalf("Version = %v, want nil", *info.Version)
	}
	if info.Multiplexer == nil || info.Multiplexer.Name != MultiplexerTmux {
		t.Fatalf("Multiplexer = %#v, want tmux", info.Multiplexer)
	}
	if info.Backend == nil || info.Backend.Kind != TerminalBackendVTE {
		t.Fatalf("Backend = %#v, want vte", info.Backend)
	}
	if info.Backend.Version == nil || *info.Backend.Version != 5204 {
		t.Fatalf("Backend.Version = %#v, want 5204", info.Backend.Version)
	}
}

// Mirrors Rust #57612: the raw VTE_VERSION keeps precedence over the terminal
// application version, including when it is empty or malformed.
func TestDetectRawVTEVersionTakesPrecedenceLikeRust(t *testing.T) {
	cases := []struct {
		raw     string
		version *uint32
	}{
		{raw: "5204", version: uint32Pointer(5204)},
		{raw: "invalid", version: nil},
		{raw: "", version: nil},
	}
	for _, testCase := range cases {
		info := Detect(&MapEnvironment{Values: map[string]string{
			"TERM_PROGRAM":         "VTE",
			"TERM_PROGRAM_VERSION": "7600",
			"VTE_VERSION":          testCase.raw,
		}})
		if info.Name != TerminalVTE {
			t.Fatalf("VTE_VERSION=%q Name = %q, want vte", testCase.raw, info.Name)
		}
		if info.TermProgram == nil || *info.TermProgram != "VTE" {
			t.Fatalf("VTE_VERSION=%q TermProgram = %#v, want VTE", testCase.raw, info.TermProgram)
		}
		if info.Version == nil || *info.Version != "7600" {
			t.Fatalf("VTE_VERSION=%q Version = %#v, want 7600", testCase.raw, info.Version)
		}
		if info.Backend == nil || info.Backend.Kind != TerminalBackendVTE {
			t.Fatalf("VTE_VERSION=%q Backend = %#v, want vte", testCase.raw, info.Backend)
		}
		if !equalUint32Pointer(info.Backend.Version, testCase.version) {
			t.Fatalf("VTE_VERSION=%q Backend.Version = %#v, want %#v", testCase.raw, info.Backend.Version, testCase.version)
		}
	}
}

// Mirrors Rust's detects_vte expectations: VTE identity carries a parsed
// backend version, and an absent VTE_VERSION leaves it unknown.
func TestDetectVTEBackendVersionsLikeRust(t *testing.T) {
	info := Detect(&MapEnvironment{Values: map[string]string{"VTE_VERSION": "7000"}})
	if info.Name != TerminalVTE || info.Version == nil || *info.Version != "7000" {
		t.Fatalf("info = %+v, want vte/7000", info)
	}
	if info.Backend == nil || info.Backend.Version == nil || *info.Backend.Version != 7000 {
		t.Fatalf("Backend = %#v, want vte/7000", info.Backend)
	}
	if got := info.UserAgentToken(); got != "VTE/7000" {
		t.Fatalf("UserAgentToken() = %q, want VTE/7000", got)
	}

	info = Detect(&MapEnvironment{Values: map[string]string{
		"TERM_PROGRAM":         "VTE",
		"TERM_PROGRAM_VERSION": "7000",
	}})
	if info.Backend == nil || info.Backend.Version == nil || *info.Backend.Version != 7000 {
		t.Fatalf("Backend without VTE_VERSION = %#v, want vte/7000 from the terminal version", info.Backend)
	}

	info = Detect(&MapEnvironment{Values: map[string]string{
		"TERM_PROGRAM":         "VTE",
		"TERM_PROGRAM_VERSION": "not-a-version",
	}})
	if info.Backend == nil || info.Backend.Kind != TerminalBackendVTE || info.Backend.Version != nil {
		t.Fatalf("Backend with malformed terminal version = %#v, want vte with unknown version", info.Backend)
	}
}

// Mirrors Rust #57612: VTE metadata is recorded for any identity when
// VTE_VERSION is present, and non-VTE terminals without it stay backend-free.
func TestDetectVTEBackendIndependentOfIdentityLikeRust(t *testing.T) {
	info := Detect(&MapEnvironment{Values: map[string]string{
		"TERM_PROGRAM": "iTerm.app",
		"VTE_VERSION":  "5400",
	}})
	if info.Name != TerminalIterm2 {
		t.Fatalf("Name = %q, want iterm2", info.Name)
	}
	if info.Backend == nil || info.Backend.Version == nil || *info.Backend.Version != 5400 {
		t.Fatalf("Backend = %#v, want vte/5400", info.Backend)
	}

	info = Detect(&MapEnvironment{Values: map[string]string{"TERM_PROGRAM": "iTerm.app"}})
	if info.Backend != nil {
		t.Fatalf("Backend = %#v, want nil without VTE metadata", info.Backend)
	}
}

func uint32Pointer(value uint32) *uint32 { return &value }

func equalUint32Pointer(left, right *uint32) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
