package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mxcRequirementBoolPtr(value bool) *bool { return &value }

func writeManagedMXCRequirementFiles(t *testing.T, home, requirementsTOML, configTOML string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirementsTOML), 0o600); err != nil {
		t.Fatalf("write requirements.toml error = %v", err)
	}
	if configTOML != "" {
		if err := os.WriteFile(ConfigPath(home), []byte(configTOML), 0o600); err != nil {
			t.Fatalf("write config.toml error = %v", err)
		}
	}
}

// Mirrors Rust #49642: the managed `windows.allow_mxc = false` requirement
// rejects an explicit `windows.sandbox = "mxc"` at load with an error naming the
// requirement, while an unset or true requirement preserves the previous
// behavior. It also pins that the requirement coexists with
// `allowed_sandbox_implementations` without weakening the mxc block.
func TestManagedWindowsMXCRequirementRejectsExplicitMXCConfigLikeRust(t *testing.T) {
	const (
		optOut       = "[windows]\nallow_mxc = false\n"
		optIn        = "[windows]\nallow_mxc = true\n"
		optedImplems = "[windows]\nallowed_sandbox_implementations = [\"elevated\"]\nallow_mxc = false\n"
	)
	rejection := `windows.sandbox = "mxc" is not allowed when the managed requirement windows.allow_mxc = false`
	cases := []struct {
		name         string
		requirements string
		config       string
		wantErr      bool
		wantMode     WindowsSandboxMode
	}{
		{
			name:         "managed opt-out rejects an explicit mxc",
			requirements: optOut,
			config:       "[windows]\nsandbox = \"mxc\"\n",
			wantErr:      true,
		},
		{
			name:         "managed opt-out rejects mxc alongside an allowed implementation",
			requirements: optedImplems,
			config:       "[windows]\nsandbox = \"mxc\"\n",
			wantErr:      true,
		},
		{
			name:         "managed allow_mxc true keeps an explicit mxc",
			requirements: optIn,
			config:       "[windows]\nsandbox = \"mxc\"\n",
			wantMode:     WindowsSandboxModeMxc,
		},
		{
			name:         "absent managed allow_mxc keeps an explicit mxc",
			requirements: "[windows]\nallowed_sandbox_implementations = [\"elevated\"]\n",
			config:       "[windows]\nsandbox = \"mxc\"\n",
			wantMode:     WindowsSandboxModeMxc,
		},
		{
			name:         "managed opt-out keeps a legacy backend",
			requirements: optOut,
			config:       "[windows]\nsandbox = \"unelevated\"\n",
			wantMode:     WindowsSandboxModeUnelevated,
		},
		{
			name:         "managed opt-out without a configured backend",
			requirements: optOut,
			config:       "[windows]\n",
		},
		{
			name:         "managed opt-out keeps an allowed elevated backend",
			requirements: optedImplems,
			config:       "[windows]\nsandbox = \"elevated\"\n",
			wantMode:     WindowsSandboxModeElevated,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			writeManagedMXCRequirementFiles(t, home, testCase.requirements, testCase.config)
			loaded, err := LoadEffectiveWithOptions(home, nil)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("LoadEffectiveWithOptions() error = nil, want the managed MXC rejection")
				}
				if !strings.Contains(err.Error(), rejection) {
					t.Fatalf("LoadEffectiveWithOptions() error = %v, want %q", err, rejection)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
			}
			mode, _, ok := ResolveWindowsSandboxMode(loaded.Values, loaded.Requirements)
			if !ok {
				if testCase.wantMode != "" {
					t.Fatalf("ResolveWindowsSandboxMode() = not configured, want %q", testCase.wantMode)
				}
				return
			}
			if mode != testCase.wantMode {
				t.Fatalf("resolved mode = %q, want %q", mode, testCase.wantMode)
			}
		})
	}
}

// Mirrors Rust #49642's ConstrainedWithSource matrix: with
// `allowed_sandbox_implementations = ["elevated"]`, elevated stays settable,
// unelevated falls back, and mxc is settable exactly when allow_mxc is not
// false.
func TestManagedWindowsMXCRequirementConstrainedMatrixLikeRust(t *testing.T) {
	base := &ConfigRequirements{
		AllowedWindowsSandboxImplementations: []WindowsSandboxImplementation{WindowsSandboxImplementation("elevated")},
	}
	for _, want := range []struct {
		name       string
		allowMXC   *bool
		configured WindowsSandboxMode
		wantMode   WindowsSandboxMode
		fallback   bool
		rejected   bool
	}{
		{name: "allow_mxc unset accepts mxc", configured: WindowsSandboxModeMxc, wantMode: WindowsSandboxModeMxc},
		{name: "allow_mxc true accepts mxc", allowMXC: mxcRequirementBoolPtr(true), configured: WindowsSandboxModeMxc, wantMode: WindowsSandboxModeMxc},
		{name: "allow_mxc false rejects mxc", allowMXC: mxcRequirementBoolPtr(false), configured: WindowsSandboxModeMxc, rejected: true},
		{name: "allowed elevated is kept", configured: WindowsSandboxModeElevated, wantMode: WindowsSandboxModeElevated},
		{name: "unelevated falls back to elevated", configured: WindowsSandboxModeUnelevated, wantMode: WindowsSandboxModeElevated, fallback: true},
	} {
		t.Run(want.name, func(t *testing.T) {
			requirements := *base
			requirements.AllowMXC = want.allowMXC
			values := map[string]any{"windows": map[string]any{"sandbox": string(want.configured)}}
			if err := ValidateManagedWindowsMXCOptOut(values, &requirements); err != nil {
				if !want.rejected {
					t.Fatalf("ValidateManagedWindowsMXCOptOut() = %v, want nil", err)
				}
				if !strings.Contains(err.Error(), "windows.allow_mxc = false") {
					t.Fatalf("rejection = %v, want it to name the managed requirement", err)
				}
				return
			}
			if want.rejected {
				t.Fatal("ValidateManagedWindowsMXCOptOut() = nil, want the managed MXC rejection")
			}
			mode, fellBack, ok := ResolveWindowsSandboxMode(values, &requirements)
			if !ok || mode != want.wantMode || fellBack != want.fallback {
				t.Fatalf("ResolveWindowsSandboxMode() = %q, fellBack=%v, ok=%v; want %q, fallback=%v", mode, fellBack, ok, want.wantMode, want.fallback)
			}
		})
	}
}

// Mirrors Rust #49642's config_allows_mxc clause: the managed requirement turns
// off automatic MXC selection in addition to the local #51547 opt-out.
func TestManagedWindowsMXCRequirementBlocksAutomaticSelectionLikeRust(t *testing.T) {
	preferMXC := map[string]any{"features": map[string]any{"prefer_mxc": true}}
	if !WindowsAutomaticMXCAllowedWithRequirements(preferMXC, nil) {
		t.Fatal("automatic MXC selection must stay allowed without requirements")
	}
	if !WindowsAutomaticMXCAllowedWithRequirements(preferMXC, &ConfigRequirements{}) {
		t.Fatal("automatic MXC selection must stay allowed when allow_mxc is unset")
	}
	if !WindowsAutomaticMXCAllowedWithRequirements(preferMXC, &ConfigRequirements{AllowMXC: mxcRequirementBoolPtr(true)}) {
		t.Fatal("automatic MXC selection must stay allowed when allow_mxc is true")
	}
	if WindowsAutomaticMXCAllowedWithRequirements(preferMXC, &ConfigRequirements{AllowMXC: mxcRequirementBoolPtr(false)}) {
		t.Fatal("the managed windows.allow_mxc = false requirement must block automatic MXC selection")
	}
	// The local opt-out keeps working through the requirements-aware predicate.
	blocked := map[string]any{
		"features": map[string]any{"prefer_mxc": true},
		"windows":  map[string]any{"allow_mxc": false},
	}
	if WindowsAutomaticMXCAllowedWithRequirements(blocked, nil) {
		t.Fatal("the local windows.allow_mxc = false opt-out must still block automatic selection")
	}
}

// Mirrors Rust #49642's WindowsRequirementsToml deserialization: the snake_case
// and camelCase spellings both parse, an allow_mxc-only table is not empty, and
// the setting coexists with allowed_sandbox_implementations.
func TestManagedWindowsMXCRequirementParsingLikeRust(t *testing.T) {
	snake := ConfigRequirementsFromMap(map[string]any{"windows": map[string]any{"allow_mxc": false}})
	if snake.AllowMXC == nil || *snake.AllowMXC {
		t.Fatalf("AllowMXC = %v, want false", snake.AllowMXC)
	}
	if configRequirementsEmpty(snake) {
		t.Fatal("a windows.allow_mxc-only requirements value must not be considered empty")
	}
	alias := ConfigRequirementsFromMap(map[string]any{"windows": map[string]any{"allowMxc": true}})
	if alias.AllowMXC == nil || !*alias.AllowMXC {
		t.Fatalf("camelCase AllowMXC = %v, want true", alias.AllowMXC)
	}
	absent := ConfigRequirementsFromMap(map[string]any{"windows": map[string]any{
		"allowed_sandbox_implementations": []any{"elevated"},
	}})
	if absent == nil {
		t.Fatal("ConfigRequirementsFromMap() = nil, want a value for the windows table")
	}
	if absent.AllowMXC != nil {
		t.Fatalf("AllowMXC = %v, want nil when unset", absent.AllowMXC)
	}
	both := ConfigRequirementsFromMap(map[string]any{"windows": map[string]any{
		"allowed_sandbox_implementations": []any{"elevated"},
		"allow_mxc":                       false,
	}})
	if both.AllowMXC == nil || *both.AllowMXC {
		t.Fatalf("AllowMXC = %v, want false alongside implementations", both.AllowMXC)
	}
	if len(both.AllowedWindowsSandboxImplementations) != 1 {
		t.Fatalf("AllowedWindowsSandboxImplementations = %v, want one entry", both.AllowedWindowsSandboxImplementations)
	}
	if cloned := cloneRequirements(both); cloned.AllowMXC == nil || *cloned.AllowMXC {
		t.Fatalf("cloneRequirements() AllowMXC = %v, want false", cloned.AllowMXC)
	}
}
