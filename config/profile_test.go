package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestNormalizeProfileName(t *testing.T) {
	name, err := NormalizeProfileName(" work.dev_1 ")
	if err != nil {
		t.Fatalf("NormalizeProfileName returned error: %v", err)
	}
	if name != "work.dev_1" {
		t.Fatalf("name = %q", name)
	}
	for _, profile := range []string{"", "../bad", ".bad", "bad.", "bad/name", "bad name"} {
		if _, err := NormalizeProfileName(profile); !errors.Is(err, ErrInvalidConfigRequest) {
			t.Fatalf("NormalizeProfileName(%q) error = %v, want ErrInvalidConfigRequest", profile, err)
		}
	}
}

func TestResolveProfileConfigPath(t *testing.T) {
	home := t.TempDir()
	path, err := ResolveProfileConfigPath(home, "work")
	if err != nil {
		t.Fatalf("ResolveProfileConfigPath returned error: %v", err)
	}
	if path != filepath.Join(home, "work.config.toml") {
		t.Fatalf("path = %q", path)
	}
}
