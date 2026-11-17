package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusLineProviderSelectionValidationLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  *StatusLineProviderSelection
		err   bool
	}{
		{name: "default", value: "default", want: &StatusLineProviderSelection{Default: true}},
		{name: "provider", value: map[string]any{"server": "calendar", "tool": "usage"}, want: &StatusLineProviderSelection{Server: "calendar", Tool: "usage"}},
		{name: "unknown string", value: "mcp", err: true},
		{name: "unknown field", value: map[string]any{"server": "calendar", "tool": "usage", "extra": true}, err: true},
		{name: "missing tool", value: map[string]any{"server": "calendar"}, err: true},
		{name: "wrong field type", value: map[string]any{"server": 1, "tool": "usage"}, err: true},
		{name: "integer", value: 42, err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := StatusLineProviderSelectionFromValue(tc.value)
			if tc.err {
				if err == nil {
					t.Fatalf("value %#v was accepted as %#v", tc.value, got)
				}
				if !strings.Contains(err.Error(), "untagged enum StatusLineSelection") || !strings.Contains(err.Error(), "tui.status_line_provider") {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || got == nil || *got != *tc.want {
				t.Fatalf("got = %#v, error = %v", got, err)
			}
		})
	}
	if err := ValidateConfigValues(map[string]any{"tui": map[string]any{"status_line_provider": map[string]any{"server": "calendar"}}}); err == nil {
		t.Fatal("strict validation accepted an incomplete statusline provider")
	}
}

func TestStatusLineProviderPackagedDefaultsAreIgnoredLikeRust(t *testing.T) {
	home := t.TempDir()
	packaged := filepath.Join(home, "packaged.toml")
	if err := os.WriteFile(packaged, []byte("[tui]\nstatus_line_provider = { server = 'packaged', tool = 'usage' }\n"), 0o600); err != nil {
		t.Fatalf("WriteFile packaged: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[tui]\nstatus_line_provider = { server = 'user', tool = 'usage' }\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user: %v", err)
	}
	cfg, err := LoadWithOptions(home, &LoadOptions{PackagedDefaultsPath: packaged})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	selection := cfg.TUIStatusLineProvider()
	if selection == nil || selection.Server != "user" || selection.Tool != "usage" {
		t.Fatalf("selection = %#v", selection)
	}

	// Rust #61915: a malformed packaged selection does not fail a normal load;
	// the packaged layer cannot select a provider, so the user layer still wins.
	if err := os.WriteFile(packaged, []byte("[tui]\nstatus_line_provider = 42\n"), 0o600); err != nil {
		t.Fatalf("WriteFile malformed packaged: %v", err)
	}
	cfg, err = LoadWithOptions(home, &LoadOptions{PackagedDefaultsPath: packaged})
	if err != nil {
		t.Fatalf("LoadWithOptions(malformed packaged) error = %v", err)
	}
	if selection := cfg.TUIStatusLineProvider(); selection == nil || selection.Server != "user" || selection.Tool != "usage" {
		t.Fatalf("selection = %#v, want the user selection", selection)
	}
	if warnings := cfg.StartupWarnings(); len(warnings) != 0 {
		t.Fatalf("StartupWarnings() = %#v, want none for an ignored packaged selection", warnings)
	}

	// --strict-config rejects the malformed packaged selection in every source.
	_, err = LoadEffectiveWithOptions(home, &EffectiveOptions{PackagedDefaultsPath: packaged, StrictConfig: true})
	if err == nil || !strings.Contains(err.Error(), "Invalid `tui.status_line_provider` in packaged defaults (") {
		t.Fatalf("strict error = %v, want the packaged defaults source", err)
	}
}

func TestStatusLineProviderProjectValueIsIgnoredLikeRust(t *testing.T) {
	projectValues := map[string]any{"tui": map[string]any{
		"status_line_provider": map[string]any{"server": "project", "tool": "usage"},
		"theme":                "dark",
	}}
	ignored := sanitizeProjectConfigValues(projectValues, CredentialBrokerProjectUnconfigured, nil)
	if len(ignored) != 0 {
		t.Fatalf("ignored = %#v, want the local-only provider to be a separate rule", ignored)
	}
	if _, ok := projectValues["tui"].(map[string]any)["status_line_provider"]; !ok {
		t.Fatalf("project layer lost the provider choice: %#v", projectValues)
	}
	project := Layer{
		Name:   LayerSource{Type: LayerSourceProject, DotCodexFolder: ".codex"},
		Config: projectValues,
	}
	effective, origins := mergeConfigLayers([]Layer{project})
	if tui, _ := effective["tui"].(map[string]any); tui["status_line_provider"] != nil {
		t.Fatalf("effective config kept the project provider selection: %#v", effective)
	}
	if _, ok := origins["tui.status_line_provider"]; ok {
		t.Fatalf("origins = %#v, want no provider origin", origins)
	}
	user := Layer{
		Name:   LayerSource{Type: LayerSourceUser, File: "config.toml"},
		Config: map[string]any{"tui": map[string]any{"status_line_provider": "default"}},
	}
	effective, origins = mergeConfigLayers([]Layer{project, user})
	tui, _ := effective["tui"].(map[string]any)
	if tui["status_line_provider"] != "default" {
		t.Fatalf("effective provider = %#v, want the user's default", tui["status_line_provider"])
	}
	if origin := origins["tui.status_line_provider"].Name; origin.Type != LayerSourceUser {
		t.Fatalf("origin = %#v, want the user layer", origin)
	}
}

func TestStatusLineProviderInvalidUserSelectionFallsBackLikeRust(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte("[tui]\nstatus_line_provider = { plugin = 'old', server = 'local', tool = 'usage' }\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := LoadWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	if selection := cfg.TUIStatusLineProvider(); selection == nil || !selection.Default {
		t.Fatalf("selection = %#v, want the built-in status line", selection)
	}
	want := "Invalid `tui.status_line_provider` in user (" + configPath + "). Using the built-in status line."
	warnings := cfg.StartupWarnings()
	if len(warnings) == 0 || warnings[0] != want {
		t.Fatalf("StartupWarnings() = %#v, want %q first", warnings, want)
	}
	service := NewConfigService(home)
	found := false
	for _, warning := range service.StartupWarningsForCWD("") {
		found = found || warning == want
	}
	if !found {
		t.Fatalf("ConfigService warnings = %#v, want %q", service.StartupWarningsForCWD(""), want)
	}
	if _, err := LoadEffectiveWithOptions(home, &EffectiveOptions{StrictConfig: true}); err == nil ||
		!strings.Contains(err.Error(), "Invalid `tui.status_line_provider` in user (") {
		t.Fatalf("strict error = %v, want the user source", err)
	}
}

func TestStatusLineProviderIncompleteProfileSelectionFallsBackLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[tui]\nstatus_line_provider = { server = 'personal', tool = 'usage' }\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user: %v", err)
	}
	for _, partial := range []string{"server = 'work'", "tool = 'quota'"} {
		if err := os.WriteFile(filepath.Join(home, "work.config.toml"), []byte("[tui]\nstatus_line_provider = { "+partial+" }\n"), 0o600); err != nil {
			t.Fatalf("WriteFile profile: %v", err)
		}
		cfg, err := LoadWithOptions(home, &LoadOptions{Profile: "work"})
		if err != nil {
			t.Fatalf("LoadWithOptions(%s) error = %v", partial, err)
		}
		if selection := cfg.TUIStatusLineProvider(); selection == nil || !selection.Default {
			t.Fatalf("selection(%s) = %#v, want the built-in status line", partial, selection)
		}
		if warnings := cfg.StartupWarnings(); len(warnings) == 0 ||
			!strings.Contains(warnings[0], "Invalid `tui.status_line_provider` in user (") ||
			!strings.HasSuffix(warnings[0], "Using the built-in status line.") {
			t.Fatalf("StartupWarnings(%s) = %#v", partial, warnings)
		}
		layers := cfg.ConfigLayers()
		profileLayer := layers[len(layers)-1]
		tui, _ := profileLayer.Config.(map[string]any)["tui"].(map[string]any)
		raw, _ := tui["status_line_provider"].(map[string]any)
		if len(raw) != 1 {
			t.Fatalf("stored profile layer = %#v, want the incomplete identity for diagnostics", raw)
		}
	}
}

func TestStatusLineProviderSessionFlagFallsBackOrFailsLikeRust(t *testing.T) {
	home := t.TempDir()
	cfg, err := LoadEffectiveWithOptions(home, &EffectiveOptions{RawOverrides: []string{"tui.status_line_provider=42"}})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	tui, _ := cfg.Values["tui"].(map[string]any)
	if tui["status_line_provider"] != "default" {
		t.Fatalf("effective provider = %#v, want default", tui["status_line_provider"])
	}
	warnings := cfg.StartupWarnings()
	if len(warnings) == 0 || warnings[0] != "Invalid `tui.status_line_provider` in session-flags. Using the built-in status line." {
		t.Fatalf("StartupWarnings() = %#v", warnings)
	}
	_, err = LoadEffectiveWithOptions(home, &EffectiveOptions{
		RawOverrides: []string{"tui.status_line_provider=42"},
		StrictConfig: true,
	})
	if err == nil || !strings.Contains(err.Error(), "Invalid `tui.status_line_provider` in session-flags.") {
		t.Fatalf("strict error = %v, want the session-flags source", err)
	}
}

func TestFormatConfigLayerSourceLikeRust(t *testing.T) {
	for _, tc := range []struct {
		source LayerSource
		want   string
	}{
		{LayerSource{Type: LayerSourcePackagedDefaults, File: "defaults.toml"}, "packaged defaults (defaults.toml)"},
		{LayerSource{Type: LayerSourceMDM, Domain: "com.openai.codex", Key: "Config"}, "MDM (com.openai.codex:Config)"},
		{LayerSource{Type: LayerSourceSystem, File: "/etc/codex/config.toml"}, "system (/etc/codex/config.toml)"},
		{LayerSource{Type: LayerSourceEnterpriseManaged, ID: "42", Name: "team"}, "enterprise-managed (team, 42)"},
		{LayerSource{Type: LayerSourceUser, File: "config.toml"}, "user (config.toml)"},
		{LayerSource{Type: LayerSourceProject, DotCodexFolder: ".codex"}, "project (.codex/config.toml)"},
		{LayerSource{Type: LayerSourceSessionFlags}, "session-flags"},
		{LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: "managed_config.toml"}, "legacy managed_config.toml (managed_config.toml)"},
		{LayerSource{Type: LayerSourceLegacyManagedConfigFromMDM}, "legacy managed_config.toml (MDM)"},
	} {
		if got := formatConfigLayerSource(tc.source); got != tc.want {
			t.Fatalf("formatConfigLayerSource(%#v) = %q, want %q", tc.source, got, tc.want)
		}
	}
	if got, want := boundedConfigLabel("a\tb\nc\rd\x01e\x7f"), `a\tb\nc\rd\u{1}e\u{7f}`; got != want {
		t.Fatalf("boundedConfigLabel() = %q, want %q", got, want)
	}
	bounded := boundedConfigLabel(strings.Repeat("a", 250))
	if bounded != strings.Repeat("a", 200)+"\u2026" {
		t.Fatalf("boundedConfigLabel() = %q", bounded)
	}
}

func TestStatusLineProviderMergeReplacesCompleteIdentity(t *testing.T) {
	base := map[string]any{"tui": map[string]any{"status_line_provider": map[string]any{"server": "base", "tool": "usage"}}}
	overlay := map[string]any{"tui": map[string]any{"status_line_provider": map[string]any{"server": "overlay"}}}
	mergeConfigMaps(base, overlay)
	value := base["tui"].(map[string]any)["status_line_provider"].(map[string]any)
	if len(value) != 1 || value["server"] != "overlay" {
		t.Fatalf("partial identity inherited a lower layer: %#v", value)
	}
}

func TestStatusLineProviderPersistenceEdit(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "[tui]\nstatus_line = ['model']\n")
	service := NewConfigService(home)
	selection := &StatusLineProviderSelection{Server: "work", Tool: "usage"}
	edit, err := StatusLineProviderConfigEdit(selection)
	if err != nil {
		t.Fatalf("StatusLineProviderConfigEdit() error = %v", err)
	}
	if _, err := service.BatchWrite(&ConfigBatchWriteParams{Edits: []ConfigEdit{*edit}}); err != nil {
		t.Fatalf("BatchWrite() error = %v", err)
	}
	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	tui := read.Config["tui"].(map[string]any)
	stored := tui["status_line_provider"].(map[string]any)
	if stored["server"] != "work" || stored["tool"] != "usage" {
		t.Fatalf("stored provider = %#v", stored)
	}
	defaultEdit, err := StatusLineProviderConfigEdit(&StatusLineProviderSelection{Default: true})
	if err != nil || defaultEdit.Value != "default" {
		t.Fatalf("default edit = %#v, error = %v", defaultEdit, err)
	}
}
