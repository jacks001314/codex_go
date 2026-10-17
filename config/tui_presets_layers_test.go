package config

import (
	"path/filepath"
	"testing"
)

func TestTUIPresetLayerAccessDistinguishesSetAndClearLikeRust(t *testing.T) {
	home := t.TempDir()
	userPath := filepath.Join(home, "config.toml")
	managedPath := filepath.Join(home, "managed_config.toml")
	layers := []Layer{
		{
			Name: LayerSource{Type: LayerSourceUser, File: userPath},
			Config: map[string]any{"tui": map[string]any{"presets": map[string]any{
				"1": map[string]any{"input": "user one"},
				"2": map[string]any{"input": "user two"},
			}}},
		},
		{
			Name: LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: managedPath},
			Config: map[string]any{"tui": map[string]any{"presets": map[string]any{
				"2": map[string]any{"input": "managed two"},
				"3": map[string]any{"input": "managed three"},
			}}},
		},
	}
	setReadOnly, clearReadOnly := TUIPresetLayerAccess(layers, userPath)
	if setReadOnly["1"] {
		t.Fatal("user-only slot 1 should be writable")
	}
	if !setReadOnly["2"] {
		t.Fatal("managed slot 2 should be set-read-only")
	}
	if !clearReadOnly["2"] {
		t.Fatal("managed slot 2 should be clear-read-only")
	}
	if !setReadOnly["3"] || !clearReadOnly["3"] {
		t.Fatalf("managed-only slot 3 access = set:%v clear:%v", setReadOnly["3"], clearReadOnly["3"])
	}
}

func TestTUIPresetLayerAccessUserWinsSetButManagedStillBlocksClear(t *testing.T) {
	home := t.TempDir()
	userPath := filepath.Join(home, "config.toml")
	layers := []Layer{
		{
			Name: LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: filepath.Join(home, "managed.toml")},
			Config: map[string]any{"tui": map[string]any{"presets": map[string]any{
				"4": map[string]any{"input": "managed"},
			}}},
		},
		{
			Name: LayerSource{Type: LayerSourceUser, File: userPath},
			Config: map[string]any{"tui": map[string]any{"presets": map[string]any{
				"4": map[string]any{"input": "user"},
			}}},
		},
	}
	setReadOnly, clearReadOnly := TUIPresetLayerAccess(layers, userPath)
	if setReadOnly["4"] {
		t.Fatal("user-highest slot 4 should be writable")
	}
	if !clearReadOnly["4"] {
		t.Fatal("managed lower layer still blocks clearing slot 4")
	}
}
