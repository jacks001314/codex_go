package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMemoryVersion(t *testing.T) {
	cases := map[string]MemoryVersion{
		"":     MemoryVersionV1,
		"v1":   MemoryVersionV1,
		"v2":   MemoryVersionV2,
		" V2 ": MemoryVersionV2,
	}
	for input, want := range cases {
		got, ok := ParseMemoryVersion(input)
		if !ok || got != want {
			t.Fatalf("ParseMemoryVersion(%q) = %q,%v want %q", input, got, ok, want)
		}
	}
	if _, ok := ParseMemoryVersion("v3"); ok {
		t.Fatal("unrecognized version must be invalid")
	}
	if MemoryVersionV1.DirectoryName() != "memories" || MemoryVersionV2.DirectoryName() != "memories_v2" {
		t.Fatalf("directory names = %q,%q", MemoryVersionV1.DirectoryName(), MemoryVersionV2.DirectoryName())
	}
}

func TestMemoriesConfigVersionDefaultAndSelection(t *testing.T) {
	if got := (&Config{Values: map[string]any{}}).Memories().MemoryVersion(); got != MemoryVersionV1 {
		t.Fatalf("default version = %q, want v1", got)
	}
	cfg := &Config{Values: map[string]any{"memories": map[string]any{"version": "v2"}}}
	if got := cfg.Memories().MemoryVersion(); got != MemoryVersionV2 {
		t.Fatalf("configured version = %q, want v2", got)
	}
}

func TestLoadEffectiveRejectsInvalidMemoriesVersion(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte("[memories]\nversion = \"v3\"\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	if _, err := LoadEffective(home, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "invalid memories.version") {
		t.Fatalf("invalid version error = %v", err)
	}

	if err := os.WriteFile(configPath, []byte("[memories]\nversion = \"v2\"\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	cfg, err := LoadEffective(home, nil, nil, nil)
	if err != nil {
		t.Fatalf("v2 config error = %v", err)
	}
	if got := cfg.Memories().MemoryVersion(); got != MemoryVersionV2 {
		t.Fatalf("loaded version = %q, want v2", got)
	}
}

// Rust #56211: fresh profiles select V2 when memories are enabled, while
// explicit selections, the dual-write opt-out, existing V1 workspaces and
// filesystem errors keep V1.
func TestResolveFreshMemoryVersion(t *testing.T) {
	home := t.TempDir()
	versionV2 := "v2"
	explicit := MemoriesConfig{Version: &versionV2}
	if got := ResolveFreshMemoryVersion(home, explicit, true); got != MemoryVersionV2 {
		t.Fatalf("explicit v2 = %q, want v2", got)
	}
	if got := ResolveFreshMemoryVersion(home, MemoriesConfig{}, false); got != MemoryVersionV1 {
		t.Fatalf("memories disabled = %q, want v1", got)
	}
	if got := ResolveFreshMemoryVersion(home, MemoriesConfig{DualWriteConfigured: true, DualWrite: true}, true); got != MemoryVersionV2 {
		t.Fatalf("dual_write = true = %q, want v2", got)
	}
	if got := ResolveFreshMemoryVersion(home, MemoriesConfig{DualWriteConfigured: true, DualWrite: false}, true); got != MemoryVersionV1 {
		t.Fatalf("dual_write = false = %q, want v1", got)
	}
	if got := ResolveFreshMemoryVersion(home, MemoriesConfig{}, true); got != MemoryVersionV2 {
		t.Fatalf("fresh profile = %q, want v2", got)
	}
	if err := os.MkdirAll(filepath.Join(home, MemoryVersionV1.DirectoryName()), 0o755); err != nil {
		t.Fatalf("mkdir v1 workspace: %v", err)
	}
	if got := ResolveFreshMemoryVersion(home, MemoriesConfig{}, true); got != MemoryVersionV1 {
		t.Fatalf("existing v1 workspace = %q, want v1", got)
	}
}

// A fresh profile's loaded configuration reports V2 while an existing V1
// workspace keeps the historical default.
func TestLoadEffectiveDefaultsFreshProfileToV2(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\nmemories = true\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	cfg, err := LoadEffective(home, nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadEffective: %v", err)
	}
	if got := cfg.Memories().MemoryVersion(); got != MemoryVersionV2 {
		t.Fatalf("fresh profile version = %q, want v2", got)
	}

	enabled := t.TempDir()
	if err := os.WriteFile(filepath.Join(enabled, "config.toml"), []byte("[features]\nmemories = true\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(enabled, "memories"), 0o755); err != nil {
		t.Fatalf("mkdir v1 workspace: %v", err)
	}
	cfg, err = LoadEffective(enabled, nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadEffective: %v", err)
	}
	if got := cfg.Memories().MemoryVersion(); got != MemoryVersionV1 {
		t.Fatalf("existing v1 profile version = %q, want v1", got)
	}
}
