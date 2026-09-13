package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIgnoredConfigWarningCollectsUnknownSettings(t *testing.T) {
	layers := []Layer{
		{
			Name:   LayerSource{Type: LayerSourceUser, File: "config.toml"},
			Config: map[string]any{"network_proxy": map[string]any{"nested": "private_value"}},
		},
		{
			Name:   LayerSource{Type: LayerSourceProject, DotCodexFolder: ".gcode"},
			Config: map[string]any{"project_setting": "private_value"},
		},
	}
	warning := IgnoredConfigWarning(layers, map[string]any{"allowed_permissions": []any{":read-only"}})
	for _, want := range []string{
		"`network_proxy` is ignored.",
		"`allowed_permissions` is ignored.",
		"`project_setting` is ignored.",
		"user:",
		"project:",
	} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning missing %q:\n%s", want, warning)
		}
	}
	if strings.Contains(warning, "private_value") {
		t.Fatalf("warning leaked a configuration value:\n%s", warning)
	}
}

func TestIgnoredConfigWarningEmptyWhenNoUnknownSettings(t *testing.T) {
	layers := []Layer{{
		Name: LayerSource{Type: LayerSourceUser, File: "config.toml"},
		Config: map[string]any{
			"model":    "gpt-5",
			"features": map[string]any{"view_image": true},
		},
	}}
	if warning := IgnoredConfigWarning(layers, map[string]any{"allowed_approval_policies": []any{"never"}}); warning != "" {
		t.Fatalf("warning = %q, want empty", warning)
	}
}

// TestProjectIgnoredConfigKeysWarningsLikeRust mirrors Rust's
// project_ignored_config_keys_warning: every stripped project-local key is
// reported for its `.codex/config.toml`, while a project config without
// unsupported keys stays quiet.
func TestProjectIgnoredConfigKeysWarningsLikeRust(t *testing.T) {
	dir := t.TempDir()
	dotCodex := filepath.Join(dir, ".gcode")
	if err := os.MkdirAll(dotCodex, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dotCodex, "config.toml")
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		`model = "gpt-5"`,
		`openai_base_url = "https://project.example.com"`,
		`profile = "project"`,
		`[features]`,
		`respect_system_proxy = true`,
		`[tui.keymap.chat]`,
		`next_permission_mode = "y"`,
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	layers := []Layer{{
		Name: LayerSource{Type: LayerSourceProject, File: configPath, DotCodexFolder: dotCodex},
	}}
	warnings := ProjectIgnoredConfigKeysWarningsForLayers(layers)
	if len(warnings) != 1 {
		t.Fatalf("ProjectIgnoredConfigKeysWarningsForLayers() = %#v, want one warning", warnings)
	}
	got := warnings[0]
	for _, want := range []string{
		"Ignored unsupported project-local config keys in " + configPath + ": ",
		"openai_base_url, profile, features.respect_system_proxy, tui.keymap.chat.next_permission_mode. ",
		"If you want these settings to apply, manually set them in your user-level config.toml.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning missing %q:\n%s", want, got)
		}
	}

	plainPath := filepath.Join(dir, "plain.toml")
	if err := os.WriteFile(plainPath, []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if warnings := ProjectIgnoredConfigKeysWarningsForLayers([]Layer{{
		Name: LayerSource{Type: LayerSourceProject, File: plainPath},
	}}); len(warnings) != 0 {
		t.Fatalf("ProjectIgnoredConfigKeysWarningsForLayers() = %#v, want none", warnings)
	}
}

func TestIgnoredConfigWarningBoundsEntriesAndAddsHints(t *testing.T) {
	bounded := IgnoredConfigWarning([]Layer{{
		Name: LayerSource{Type: LayerSourceUser, File: "config.toml"},
		Config: map[string]any{
			"a": "1",
			"b": "2",
			"c": "3",
			"d": "4",
			"e": "5",
		},
	}}, nil)
	if !strings.Contains(bounded, "5 unrecognized configuration settings") {
		t.Fatalf("warning count missing:\n%s", bounded)
	}
	if !strings.Contains(bounded, "... and 2 more ignored settings.") {
		t.Fatalf("warning bound missing:\n%s", bounded)
	}

	network := IgnoredConfigWarning([]Layer{{
		Name:   LayerSource{Type: LayerSourceUser, File: "config.toml"},
		Config: map[string]any{"network_proxy": true},
	}}, nil)
	if !strings.Contains(network, "[permissions.<name>.network]") {
		t.Fatalf("network_proxy hint missing:\n%s", network)
	}

	viewImage := IgnoredConfigWarning([]Layer{{
		Name:   LayerSource{Type: LayerSourceUser, File: "config.toml"},
		Config: map[string]any{"features": map[string]any{"include_view_image_tool": true}},
	}}, nil)
	if !strings.Contains(viewImage, "[features].view_image") {
		t.Fatalf("view_image hint missing:\n%s", viewImage)
	}
}

func TestConfigServiceIgnoredSettingsWarning(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(home, "config.toml"),
		[]byte("network_proxy = { nested = \"private_value\" }\n"),
		0o600,
	); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(home, "requirements.toml"),
		[]byte("allowed_permissions = [\":read-only\"]\n"),
		0o600,
	); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
	service := NewConfigService(home)
	warning := service.IgnoredSettingsWarning("")
	if !strings.Contains(warning, "`network_proxy` is ignored.") ||
		!strings.Contains(warning, "`allowed_permissions` is ignored.") {
		t.Fatalf("warning = %q", warning)
	}
	if strings.Contains(warning, "private_value") {
		t.Fatalf("warning leaked a value: %q", warning)
	}
}
