package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codextea "codex_go/tui/tea"
)

func withScreenReaderProbe(t *testing.T, probe func() bool, timeout time.Duration) {
	t.Helper()
	previousProbe := screenReaderProbeFunc
	previousTimeout := screenReaderDetectionTimeout
	screenReaderProbeFunc = probe
	screenReaderDetectionTimeout = timeout
	t.Cleanup(func() {
		screenReaderProbeFunc = previousProbe
		screenReaderDetectionTimeout = previousTimeout
	})
}

func writeScreenReaderConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// Rust #46040: a present marker skips detection entirely.
func TestScreenReaderMarkerSkipsDetectionLikeRust(t *testing.T) {
	called := false
	withScreenReaderProbe(t, func() bool { called = true; return true }, time.Second)
	path := writeScreenReaderConfig(t, "[tui]\nscreen_reader_detection_done = false\n")
	settings := applyScreenReaderAnimationSettings(codextea.SettingsWriteResult{}, map[string]any{
		"tui": map[string]any{"screen_reader_detection_done": false},
	}, path)
	if called {
		t.Fatal("probe ran despite the completion marker")
	}
	if settings.AnimationsEnabled != nil {
		t.Fatalf("animations = %v, want untouched", *settings.AnimationsEnabled)
	}
}

// Rust #46040: detection seeds `tui.animations=false` when the user has not
// configured it, preserving comments and unrelated sections.
func TestScreenReaderDetectionSeedsAnimationDefaultLikeRust(t *testing.T) {
	withScreenReaderProbe(t, func() bool { return true }, time.Second)
	path := writeScreenReaderConfig(t, "# keep this comment\n[tui]\n# theme comment\ntheme = \"dark\"\n\n[other]\nvalue = 1\n")
	settings := applyScreenReaderAnimationSettings(codextea.SettingsWriteResult{}, map[string]any{
		"tui": map[string]any{"theme": "dark"},
	}, path)
	if settings.AnimationsEnabled == nil || *settings.AnimationsEnabled {
		t.Fatalf("animations = %v, want false", settings.AnimationsEnabled)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"# keep this comment",
		"# theme comment",
		`theme = "dark"`,
		"animations = false",
		"screen_reader_detection_done = true",
		"[other]",
		"value = 1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
}

// Rust #46040: an explicit animation preference wins while the marker is still
// recorded.
func TestScreenReaderDetectionPreservesExplicitAnimationsLikeRust(t *testing.T) {
	withScreenReaderProbe(t, func() bool { return true }, time.Second)
	path := writeScreenReaderConfig(t, "[tui]\nanimations = true\n")
	settings := applyScreenReaderAnimationSettings(codextea.SettingsWriteResult{}, map[string]any{
		"tui": map[string]any{"animations": true},
	}, path)
	if settings.AnimationsEnabled == nil || !*settings.AnimationsEnabled {
		t.Fatalf("explicit animations = %v, want true", settings.AnimationsEnabled)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if strings.Count(text, "animations = true") != 1 || strings.Contains(text, "animations = false") {
		t.Fatalf("explicit animations rewritten:\n%s", text)
	}
	if !strings.Contains(text, "screen_reader_detection_done = true") {
		t.Fatalf("marker missing:\n%s", text)
	}
}

// Rust #46040: a blocked probe is bounded and still records the attempt.
func TestScreenReaderDetectionTimeoutLikeRust(t *testing.T) {
	withScreenReaderProbe(t, func() bool {
		time.Sleep(200 * time.Millisecond)
		return true
	}, 10*time.Millisecond)
	path := writeScreenReaderConfig(t, "[tui]\n")
	settings := applyScreenReaderAnimationSettings(codextea.SettingsWriteResult{}, map[string]any{"tui": map[string]any{}}, path)
	if settings.AnimationsEnabled != nil {
		t.Fatalf("timed-out probe changed animations: %v", *settings.AnimationsEnabled)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "screen_reader_detection_done = true") {
		t.Fatalf("timeout did not record the marker:\n%s", data)
	}
}

// A persistence failure retains the session default and is non-fatal.
func TestScreenReaderPersistenceFailureKeepsSessionDefaultLikeRust(t *testing.T) {
	withScreenReaderProbe(t, func() bool { return true }, time.Second)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	path := filepath.Join(blocker, "config.toml")
	settings := applyScreenReaderAnimationSettings(codextea.SettingsWriteResult{}, map[string]any{"tui": map[string]any{}}, path)
	if settings.AnimationsEnabled == nil || *settings.AnimationsEnabled {
		t.Fatalf("session animations = %v, want false", settings.AnimationsEnabled)
	}
}

func TestScreenReaderPersistenceRejectsInlineTuiTable(t *testing.T) {
	path := writeScreenReaderConfig(t, "tui = { theme = \"dark\" }\n")
	if err := persistScreenReaderDetection(path, false, false); err == nil {
		t.Fatal("inline tui table was edited")
	}
}
