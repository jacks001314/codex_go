package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ValidateLoadedConfigFiles reproduces the file reads Config::load performs on
// the three path-valued settings. The OS text itself is localized, so these
// tests pin the wrapper and the numeric os error Rust appends on Windows.
func TestValidateLoadedConfigFilesReadsPathSettings(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.md")

	err := ValidateLoadedConfigFiles(map[string]any{"model_instructions_file": "missing.md"}, dir)
	if err == nil {
		t.Fatal("expected a missing instructions file to fail the load")
	}
	wantPrefix := "failed to read model instructions file " + missing + ": "
	if got := err.Error(); !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("message = %q, want prefix %q", got, wantPrefix)
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(err.Error(), " (os error 2)") {
		t.Fatalf("message = %q, want the os error suffix", err.Error())
	}

	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, []byte(" \n\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = ValidateLoadedConfigFiles(map[string]any{"experimental_compact_prompt_file": "empty.md"}, dir)
	if err == nil || err.Error() != "experimental compact prompt file is empty: "+empty {
		t.Fatalf("empty prompt error = %v, want the is-empty wording", err)
	}

	valid := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(valid, []byte("be nice\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLoadedConfigFiles(map[string]any{"model_instructions_file": "prompt.md"}, dir); err != nil {
		t.Fatalf("valid instructions file rejected: %v", err)
	}
	// A directory reports access denied rather than a handle error.
	if err := ValidateLoadedConfigFiles(map[string]any{"model_instructions_file": "."}, dir); err == nil {
		t.Fatal("expected a directory to be rejected")
	} else if runtime.GOOS == "windows" && !strings.HasSuffix(err.Error(), " (os error 5)") {
		t.Fatalf("directory error = %q, want os error 5", err.Error())
	}
	_ = missing
}

func TestValidateLoadedConfigFilesReadsModelCatalog(t *testing.T) {
	dir := t.TempDir()

	err := ValidateLoadedConfigFiles(map[string]any{"model_catalog_json": "catalog.json"}, dir)
	if err == nil {
		t.Fatal("expected a missing catalog to fail the load")
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, "catalog.json")) && runtime.GOOS != "windows" {
		t.Fatalf("catalog error = %q, want the resolved path", err.Error())
	}
	if strings.HasPrefix(err.Error(), "failed to read") {
		t.Fatalf("catalog read errors carry no wrapper in Rust, got %q", err.Error())
	}

	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("bad.json", []byte{0xff, 0xfe})
	if err := ValidateLoadedConfigFiles(map[string]any{"model_catalog_json": "bad.json"}, dir); err == nil ||
		err.Error() != "stream did not contain valid UTF-8" {
		t.Fatalf("invalid utf-8 catalog error = %v", err)
	}

	write("empty.json", []byte(`{"models": []}`))
	if err := ValidateLoadedConfigFiles(map[string]any{"model_catalog_json": "empty.json"}, dir); err == nil ||
		!strings.HasSuffix(err.Error(), "must contain at least one model") {
		t.Fatalf("empty catalog error = %v", err)
	}

	write("partial.json", []byte(`{"models": [{"slug": "m"}]}`))
	if err := ValidateLoadedConfigFiles(map[string]any{"model_catalog_json": "partial.json"}, dir); err == nil ||
		!strings.HasSuffix(err.Error(), "as JSON: missing field `display_name` at line 1 column 25") {
		t.Fatalf("incomplete catalog error = %v", err)
	}

	write("ok.json", []byte(`{"models": [{"slug":"m","display_name":"M","supported_reasoning_levels":[],`+
		`"shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,`+
		`"support_verbosity":true,"truncation_policy":{"mode":"tokens","limit":5},`+
		`"experimental_supported_tools":[],"model_messages":{"instructions_template":"x"}}]}`))
	if err := ValidateLoadedConfigFiles(map[string]any{"model_catalog_json": "ok.json"}, dir); err != nil {
		t.Fatalf("catalog with a model rejected: %v", err)
	}

	write("broken.json", []byte("not json"))
	if err := ValidateLoadedConfigFiles(map[string]any{"model_catalog_json": "broken.json"}, dir); err == nil ||
		!strings.Contains(err.Error(), "failed to parse model_catalog_json path `"+filepath.Join(dir, "broken.json")+"` as JSON: ") {
		t.Fatalf("broken catalog error = %v", err)
	}
}

func TestValidateLoadedConfigFilesKeepsSerdeOrder(t *testing.T) {
	dir := t.TempDir()
	// The instructions read happens before the compact prompt read.
	err := ValidateLoadedConfigFiles(map[string]any{
		"model_instructions_file":          "a.md",
		"experimental_compact_prompt_file": "b.md",
	}, dir)
	if err == nil || !strings.HasPrefix(err.Error(), "failed to read model instructions file ") {
		t.Fatalf("order error = %v, want the instructions file first", err)
	}
}

func TestResolveConfigFilePath(t *testing.T) {
	home := `C:\codex-home`
	if got, want := ResolveConfigFilePath("x.md", home), filepath.Join(home, "x.md"); got != want {
		t.Fatalf("relative path = %q, want %q", got, want)
	}
	absolute := "/abs/x.md"
	if runtime.GOOS == "windows" {
		absolute = `C:\abs\x.md`
	}
	if got, want := ResolveConfigFilePath(absolute, home), filepath.Clean(absolute); got != want {
		t.Fatalf("absolute path = %q, want %q", got, want)
	}
	if got := ResolveConfigFilePath("~/x.md", home); !filepath.IsAbs(got) || strings.HasPrefix(got, home) {
		t.Fatalf("tilde path = %q, want the user home", got)
	}
}
