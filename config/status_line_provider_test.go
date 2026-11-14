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

func TestStatusLineProviderPackagedDefaultsAreIgnoredButValidated(t *testing.T) {
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

	if err := os.WriteFile(packaged, []byte("[tui]\nstatus_line_provider = 42\n"), 0o600); err != nil {
		t.Fatalf("WriteFile malformed packaged: %v", err)
	}
	if _, err := LoadWithOptions(home, &LoadOptions{PackagedDefaultsPath: packaged}); err == nil {
		t.Fatal("malformed packaged statusline provider was accepted")
	}
}

func TestStatusLineProviderProjectValueIsIgnoredLikeRust(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".codex", "config.toml"), []byte("[tui]\nstatus_line_provider = { server = 'project', tool = 'usage' }\n"), 0o600); err != nil {
		t.Fatalf("WriteFile project: %v", err)
	}
	projectValues := map[string]any{"tui": map[string]any{"status_line_provider": map[string]any{"server": "project", "tool": "usage"}}}
	ignored := sanitizeProjectConfigValues(projectValues, CredentialBrokerProjectUnconfigured, nil)
	if len(ignored) != 1 || ignored[0] != "tui.status_line_provider" {
		t.Fatalf("ignored = %#v", ignored)
	}
	if _, ok := projectValues["tui"].(map[string]any)["status_line_provider"]; ok {
		t.Fatalf("project value survived sanitization: %#v", projectValues)
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
