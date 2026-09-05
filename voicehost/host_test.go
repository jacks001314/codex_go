package voicehost

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestChildEnvironmentFiltersAndAddsRuntimeEnvironment(t *testing.T) {
	parent := []string{
		"PATH=secret",
		"SYSTEMROOT=C:\\Windows",
		"APPDATA=C:\\Users\\test\\AppData",
		"GST_PLUGIN_PATH=host-value",
		"lowercase=kept",
	}
	filtered := childEnvironment(parent)
	values := map[string]string{}
	for _, entry := range filtered {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[strings.ToUpper(key)] = value
		}
	}
	if _, ok := values["PATH"]; ok {
		t.Fatal("PATH leaked into child environment")
	}
	if values["SYSTEMROOT"] != "C:\\Windows" || values["APPDATA"] != "C:\\Users\\test\\AppData" {
		t.Fatalf("allowed environment = %#v", values)
	}
	if _, ok := values["LOWERCASE"]; ok {
		t.Fatal("case-sensitive non-allowlisted key leaked into child environment")
	}
	if values["GST_PLUGIN_PATH"] != "" {
		t.Fatalf("runtime environment did not override host value: %q", values["GST_PLUGIN_PATH"])
	}
	if values["GST_REGISTRY_UPDATE"] != "no" || values["GST_REGISTRY_FORK"] != "no" {
		t.Fatalf("runtime environment missing: %#v", values)
	}
}

func TestResolvePackageExecutable(t *testing.T) {
	root := t.TempDir()
	name := "codex-voice-host"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	directory := filepath.Join(root, "codex-resources", "voice", "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("placeholder"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolvePackageExecutable(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != path {
		t.Fatalf("resolved = %q, want %q", resolved, path)
	}
	if _, err := resolvePackageExecutable(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing package resolved successfully")
	}
}
