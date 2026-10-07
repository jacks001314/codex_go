package config

import (
	"os"
	"strings"
	"testing"
)

// Mirrors Rust #51547: `windows.allow_mxc = false` rejects an explicit
// `windows.sandbox = "mxc"` at load time with the same message, while an
// omitted or true setting preserves the previous behavior.
func TestWindowsMXCOptOutRejectsExplicitMXCConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		name     string
		config   string
		wantErr  bool
		wantMode WindowsSandboxMode
	}{
		{
			name:    "explicit mxc with the opt-out",
			config:  "[windows]\nsandbox = \"mxc\"\nallow_mxc = false\n",
			wantErr: true,
		},
		{
			name:     "explicit mxc with allow_mxc true",
			config:   "[windows]\nsandbox = \"mxc\"\nallow_mxc = true\n",
			wantMode: WindowsSandboxModeMxc,
		},
		{
			name:     "explicit mxc without the opt-out",
			config:   "[windows]\nsandbox = \"mxc\"\n",
			wantMode: WindowsSandboxModeMxc,
		},
		{
			name:     "legacy backend with the opt-out",
			config:   "[windows]\nsandbox = \"unelevated\"\nallow_mxc = false\n",
			wantMode: WindowsSandboxModeUnelevated,
		},
		{
			name:   "opt-out without a configured backend",
			config: "[windows]\nallow_mxc = false\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := os.WriteFile(ConfigPath(home), []byte(testCase.config), 0o600); err != nil {
				t.Fatalf("write config error = %v", err)
			}
			loaded, err := LoadEffectiveWithOptions(home, nil)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("LoadEffectiveWithOptions() error = nil, want the MXC opt-out rejection")
				}
				want := `windows.sandbox = "mxc" is not allowed when windows.allow_mxc = false`
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("LoadEffectiveWithOptions() error = %v, want %q", err, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
			}
			mode, ok := WindowsSandboxModeFromValues(loaded.Values)
			if !ok {
				if testCase.wantMode != "" {
					t.Fatalf("WindowsSandboxModeFromValues() = not configured, want %q", testCase.wantMode)
				}
				return
			}
			if mode != testCase.wantMode {
				t.Fatalf("resolved mode = %q, want %q", mode, testCase.wantMode)
			}
		})
	}
}

// Mirrors Rust #51547's `config_allows_mxc` opt-out: the setting is absent by
// default and only an explicit false blocks MXC. Go never selects the native
// backend implicitly - `windows.sandbox = "mxc"` is required - so the automatic
// path is frozen here: with `features.prefer_mxc = true` and no explicit
// backend, no mode is configured and the opt-out cannot be bypassed.
func TestWindowsMXCOptOutBlocksAutomaticSelectionLikeRust(t *testing.T) {
	preferMXC := map[string]any{
		"features": map[string]any{"prefer_mxc": true},
	}
	if mode, ok := WindowsSandboxModeFromValues(preferMXC); ok {
		t.Fatalf("WindowsSandboxModeFromValues() = %q, %v; want no implicit MXC selection", mode, ok)
	}
	if !WindowsAutomaticMXCAllowed(preferMXC) {
		t.Fatal("automatic MXC selection must stay allowed when the opt-out is absent")
	}

	blocked := map[string]any{
		"features": map[string]any{"prefer_mxc": true},
		"windows":  map[string]any{"allow_mxc": false},
	}
	if WindowsAutomaticMXCAllowed(blocked) {
		t.Fatal("windows.allow_mxc = false must block automatic MXC selection")
	}
	if allow, configured := WindowsAllowMXCFromValues(blocked); allow || !configured {
		t.Fatalf("WindowsAllowMXCFromValues() = %v, %v; want false, true", allow, configured)
	}
	if allow, configured := WindowsAllowMXCFromValues(preferMXC); !allow || configured {
		t.Fatalf("WindowsAllowMXCFromValues() = %v, %v; want true, false", allow, configured)
	}

	// The load-time rejection only fires for an explicit MXC selection, so the
	// opt-out alone must not fail a load (Rust asserts !result.prefer_mxc).
	if err := ValidateWindowsMXCOptOut(blocked); err != nil {
		t.Fatalf("ValidateWindowsMXCOptOut() = %v, want nil without an explicit MXC backend", err)
	}
	explicit := map[string]any{
		"windows": map[string]any{"sandbox": "mxc", "allow_mxc": false},
	}
	if err := ValidateWindowsMXCOptOut(explicit); err == nil {
		t.Fatal("ValidateWindowsMXCOptOut() = nil, want the explicit MXC rejection")
	}
}
