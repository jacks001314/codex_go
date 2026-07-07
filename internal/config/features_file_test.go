package config

import (
	"os"
	"strings"
	"testing"
)

func TestSetFeatureCreatesConfig(t *testing.T) {
	dir := t.TempDir()
	if err := SetFeature(dir, "unified_exec", true); err != nil {
		t.Fatalf("SetFeature returned error: %v", err)
	}
	settings, err := LoadFeatureSettings(dir)
	if err != nil {
		t.Fatalf("LoadFeatureSettings returned error: %v", err)
	}
	if !settings["unified_exec"] {
		t.Fatalf("unified_exec = false, want true")
	}
}

func TestSetFeatureUpdatesExistingSection(t *testing.T) {
	dir := t.TempDir()
	body := "model = \"gpt\"\n\n[features]\nshell_tool = true\n\n[other]\nvalue = 1\n"
	if err := os.WriteFile(ConfigPath(dir), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := SetFeature(dir, "shell_tool", false); err != nil {
		t.Fatalf("SetFeature returned error: %v", err)
	}
	data, err := os.ReadFile(ConfigPath(dir))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "shell_tool = false") {
		t.Fatalf("config missing updated feature: %s", string(data))
	}
}

func TestSetFeatureInsertsBeforeNextSection(t *testing.T) {
	dir := t.TempDir()
	body := "[features]\nshell_tool = true\n[other]\nvalue = 1\n"
	if err := os.WriteFile(ConfigPath(dir), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := SetFeature(dir, "unified_exec", true); err != nil {
		t.Fatalf("SetFeature returned error: %v", err)
	}
	data, err := os.ReadFile(ConfigPath(dir))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "unified_exec = true") {
		t.Fatalf("feature was not inserted: %s", text)
	}
	if strings.Index(text, "unified_exec = true") > strings.Index(text, "[other]") {
		t.Fatalf("feature was not inserted before next section: %s", string(data))
	}
}
