package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectlessClassificationMatchesRust mirrors Rust #46328's loader tests
// (config/src/loader/projectless_directory_tests.rs): a completed discovery is
// projectless only when it found no project-root marker, no Git checkout root
// and no project-local config directory.
func TestProjectlessClassificationMatchesRust(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	load := func(t *testing.T, dir string) *Config {
		t.Helper()
		cfg, err := LoadWithOptions(home, &LoadOptions{CWD: dir})
		if err != nil {
			t.Fatalf("LoadWithOptions(%s) error = %v", dir, err)
		}
		return cfg
	}

	if cfg := load(t, cwd); !cfg.IsProjectless() {
		t.Fatal("an unmarked directory should be projectless")
	}
	// A saved trust decision (either level) does not make an unmarked
	// directory a project.
	for _, level := range []string{"trusted", "untrusted"} {
		key := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
		body := "[projects.\"" + key + "\"]\ntrust_level = \"" + level + "\"\n"
		if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if cfg := load(t, cwd); !cfg.IsProjectless() {
			t.Fatalf("saved %s trust should not clear projectless", level)
		}
	}
	if err := os.Remove(ConfigPath(home)); err != nil {
		t.Fatal(err)
	}

	// Skipped discovery never claims projectless.
	if cfg, err := LoadWithOptions(home, nil); err != nil || cfg.IsProjectless() {
		t.Fatalf("no-cwd load projectless = %v (err=%v)", cfg != nil && cfg.IsProjectless(), err)
	}
	if cfg, err := LoadWithOptions(home, &LoadOptions{CWD: cwd, IgnoreProjectConfig: true}); err != nil || cfg.IsProjectless() {
		t.Fatalf("ignore-project-config load projectless = %v (err=%v)", cfg != nil && cfg.IsProjectless(), err)
	}
}

// TestProjectMarkersAndLocalLayersPreventProjectlessMatchesRust mirrors the
// Rust table: a project-root marker, a Git checkout root, or a project-local
// config directory (even without config.toml) all classify the directory as a
// project.
func TestProjectMarkersAndLocalLayersPreventProjectlessMatchesRust(t *testing.T) {
	for _, testCase := range []struct {
		marker string
		config string
		child  string
	}{
		{marker: ".git"},
		{marker: ".git", child: "nested"},
		{marker: ".git", config: "project_root_markers = []\n", child: "nested"},
		{marker: ".gcode"},
		{marker: ".gcode", config: "project_root_markers = ['.gcode']\n", child: "nested"},
		{marker: ".company-root", config: "project_root_markers = ['.company-root']\n"},
		{marker: ".company-root", config: "project_root_markers = ['.company-root']\n", child: "nested"},
	} {
		root := t.TempDir()
		home := filepath.Join(root, "home")
		cwd := filepath.Join(root, "workspace")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(cwd, testCase.marker), 0o755); err != nil {
			t.Fatal(err)
		}
		if testCase.marker == ".git" {
			if err := os.WriteFile(filepath.Join(cwd, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if testCase.config != "" {
			if err := os.WriteFile(ConfigPath(home), []byte(testCase.config), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		dir := cwd
		if testCase.child != "" {
			dir = filepath.Join(cwd, testCase.child)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		cfg, err := LoadWithOptions(home, &LoadOptions{CWD: dir})
		if err != nil {
			t.Fatalf("LoadWithOptions error = %v", err)
		}
		if cfg.IsProjectless() {
			t.Fatalf("marker=%s config=%q child=%s should not be projectless", testCase.marker, testCase.config, testCase.child)
		}
	}
}

// TestUserCodexHomeIsNotAProjectLayerMatchesRust mirrors the Rust case: a
// config directory that is the user's CODEX_HOME is not a project layer, so the
// directory stays projectless.
func TestUserCodexHomeIsNotAProjectLayerMatchesRust(t *testing.T) {
	cwd := t.TempDir()
	home := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(home), []byte("model = 'user-model'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithOptions(home, &LoadOptions{CWD: cwd})
	if err != nil {
		t.Fatalf("LoadWithOptions error = %v", err)
	}
	if !cfg.IsProjectless() {
		t.Fatal("the user's CODEX_HOME should not count as a project layer")
	}
}

// TestManagedRootMarkersControlProjectlessMatchesRust mirrors the Rust case
// where managed configuration supplies project_root_markers.
func TestManagedRootMarkersControlProjectlessMatchesRust(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace", ".company-root"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(root, "workspace", "nested")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(home), []byte("project_root_markers = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(home, "managed_config.toml")
	for _, testCase := range []struct {
		markers string
		want    bool
	}{
		{markers: "['.company-root']", want: false},
		{markers: "[]", want: true},
	} {
		if err := os.WriteFile(managed, []byte("project_root_markers = "+testCase.markers+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadWithOptions(home, &LoadOptions{CWD: cwd, IncludeManagedConfig: true, ManagedConfigPath: managed})
		if err != nil {
			t.Fatalf("LoadWithOptions error = %v", err)
		}
		if cfg.IsProjectless() != testCase.want {
			t.Fatalf("markers=%s projectless = %v, want %v", testCase.markers, cfg.IsProjectless(), testCase.want)
		}
	}
}
