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
