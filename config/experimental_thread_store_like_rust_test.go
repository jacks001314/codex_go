package config

import (
	"os"
	"strings"
	"testing"
)

// Rust's config schema exposes experimental_thread_store with the local
// selector. Go always uses the local store, so the selector must load while
// unsupported store types fail explicitly.
func TestExperimentalThreadStoreLocalLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("experimental_thread_store = { type = \"local\" }\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadEffectiveWithOptions(home, &EffectiveOptions{StrictConfig: true}); err != nil {
		t.Fatalf("LoadEffectiveWithOptions(local) error = %v", err)
	}

	if err := os.WriteFile(ConfigPath(home), []byte("experimental_thread_store = { type = \"remote\" }\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := LoadEffectiveWithOptions(home, &EffectiveOptions{StrictConfig: true})
	if err == nil || !strings.Contains(err.Error(), `experimental_thread_store type "remote" is not supported`) {
		t.Fatalf("LoadEffectiveWithOptions(remote) error = %v", err)
	}
}
