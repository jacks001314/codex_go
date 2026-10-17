package config

import (
	"os"
	"strings"
	"testing"
)

// TestTUIPresetsLoadLikeRust covers Rust #54379: `tui.presets` accepts entries
// keyed by a single digit with an `input` and an optional `label`, and both
// entries are exposed on the runtime configuration.
func TestTUIPresetsLoadLikeRust(t *testing.T) {
	home := t.TempDir()
	body := strings.Join([]string{
		"[tui.presets.1]",
		`input = "/review"`,
		`label = "Review"`,
		"",
		"[tui.presets.2]",
		`input = "explain this diff"`,
		"",
	}, "\n")
	if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	cfg, err := LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	presets := cfg.TUIPresets()
	if len(presets) != 2 {
		t.Fatalf("presets = %#v, want two entries", presets)
	}
	first, ok := presets["1"]
	if !ok || first.Input != "/review" || first.Label == nil || *first.Label != "Review" {
		t.Fatalf("preset 1 = %#v", first)
	}
	second, ok := presets["2"]
	if !ok || second.Input != "explain this diff" || second.Label != nil {
		t.Fatalf("preset 2 = %#v", second)
	}
}

// TestTUIPresetsRejectInvalidEntriesLikeRust covers the four QuickDial
// validation failures plus the single-digit slot rule.
func TestTUIPresetsRejectInvalidEntriesLikeRust(t *testing.T) {
	tooLong := strings.Repeat("a", TUIPresetMaxInputBytes+1)
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "invalid slot",
			body:    "[tui.presets.10]\ninput = \"hello\"\n",
			wantErr: "tui.presets.10: slot must be a single digit from 0 to 9",
		},
		{
			name:    "non digit slot",
			body:    "[tui.presets.a]\ninput = \"hello\"\n",
			wantErr: "tui.presets.a: slot must be a single digit from 0 to 9",
		},
		{
			name:    "blank input",
			body:    "[tui.presets.1]\ninput = \"   \"\n",
			wantErr: "tui.presets.1: Preset input cannot be empty.",
		},
		{
			name:    "shell command",
			body:    "[tui.presets.1]\ninput = \"  !rm -rf build\"\n",
			wantErr: "tui.presets.1: Presets support prompts and slash commands, not shell commands.",
		},
		{
			name:    "too long",
			body:    "[tui.presets.1]\ninput = \"" + tooLong + "\"\n",
			wantErr: "tui.presets.1: Preset input cannot exceed 8,000 UTF-8 bytes.",
		},
		{
			name:    "unknown field",
			body:    "[tui.presets.1]\ninput = \"hello\"\ncommand = \"x\"\n",
			wantErr: "tui.presets.1: unknown field `command`",
		},
		{
			name:    "missing input",
			body:    "[tui.presets.1]\nlabel = \"only a label\"\n",
			wantErr: "tui.presets.1: missing field `input`",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(ConfigPath(home), []byte(test.body), 0o600); err != nil {
				t.Fatalf("write config error = %v", err)
			}
			_, err := LoadEffectiveWithOptions(home, nil)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("LoadEffectiveWithOptions() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

// TestTUIPresetValidateMatchesRust checks the QuickDial limits directly,
// including the byte (not rune) length rule.
func TestTUIPresetValidateMatchesRust(t *testing.T) {
	if err := (TUIPreset{Input: "/compact"}).Validate(); err != nil {
		t.Fatalf("valid preset rejected: %v", err)
	}
	if err := (TUIPreset{Input: "!ls"}).Validate(); err == nil {
		t.Fatal("shell command accepted")
	}
	if err := (TUIPreset{Input: "\t !ls"}).Validate(); err == nil {
		t.Fatal("shell command after whitespace accepted")
	}
	// 8,000 UTF-8 bytes of three-byte runes is 2,666 runes and fits, while the
	// 8,001st byte does not.
	if err := (TUIPreset{Input: strings.Repeat("\u4f60", 2666)}).Validate(); err != nil {
		t.Fatalf("8,000-byte preset rejected: %v", err)
	}
	if err := (TUIPreset{Input: strings.Repeat("\u4f60", 2667)}).Validate(); err == nil {
		t.Fatal("oversized preset accepted")
	}
}
