package config

import (
	"os"
	"testing"
)

// Rust #52176 persists acknowledgment as notice.hide_gov_cloud_guidance; the
// strict loader must accept it and the accessor must report it.
func TestHideGovCloudGuidanceNoticeLikeRust(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(ConfigPath(dir), []byte("[notice]\nhide_gov_cloud_guidance = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	cfg, err := LoadEffectiveWithOptions(dir, &EffectiveOptions{StrictConfig: true})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	if !cfg.HideGovCloudGuidance() {
		t.Fatalf("HideGovCloudGuidance() = false, values = %#v", cfg.Values["notice"])
	}
	if (&Config{}).HideGovCloudGuidance() {
		t.Fatal("an absent notice must not hide the guidance")
	}
	if (&Config{Values: map[string]any{"notices": map[string]any{"hide_gov_cloud_guidance": true}}}).HideGovCloudGuidance() != true {
		t.Fatal("the legacy [notices] fallback was not honored")
	}
	if (&Config{Values: map[string]any{"notice": map[string]any{"hide_gov_cloud_guidance": false}}}).HideGovCloudGuidance() {
		t.Fatal("an explicit false must keep the guidance")
	}
}
