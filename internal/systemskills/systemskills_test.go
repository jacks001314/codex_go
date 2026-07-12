package systemskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallSystemSkillsMatchesRustCacheBehavior(t *testing.T) {
	home := t.TempDir()
	if err := Install(home); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	root := CacheRoot(home)
	for _, relative := range []string{
		"skill-installer/SKILL.md",
		"skill-creator/scripts/init_skill.py",
		"plugin-creator/references/plugin-json-spec.md",
		"imagegen/assets/imagegen.png",
		"openai-docs/agents/openai.yaml",
	} {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil || info.IsDir() {
			t.Fatalf("installed asset %s info=%v err=%v", relative, info, err)
		}
	}
	markerPath := filepath.Join(root, markerFilename)
	marker, err := os.ReadFile(markerPath)
	if err != nil || strings.TrimSpace(string(marker)) == "" {
		t.Fatalf("marker = %q err=%v", marker, err)
	}
	stale := filepath.Join(root, "stale.txt")
	if err := os.WriteFile(stale, []byte("preserved while marker matches"), 0o600); err != nil {
		t.Fatalf("WriteFile(stale) error = %v", err)
	}
	if err := Install(home); err != nil {
		t.Fatalf("Install(matching marker) error = %v", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("matching marker should skip rewrite: %v", err)
	}
	if err := os.WriteFile(markerPath, []byte("old-version\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(marker) error = %v", err)
	}
	if err := Install(home); err != nil {
		t.Fatalf("Install(stale marker) error = %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale marker should replace system directory, stat err=%v", err)
	}
}

func TestFingerprintIncludesNestedSampleAssets(t *testing.T) {
	first, err := Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint() error = %v", err)
	}
	second, err := Fingerprint()
	if err != nil || first == "" || first != second {
		t.Fatalf("fingerprints = %q/%q err=%v", first, second, err)
	}
}

func TestUninstallWithEmptyCodexHomeDoesNotRemoveRelativeSkills(t *testing.T) {
	cwd := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir(temp) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	marker := filepath.Join(cwd, "skills", ".system", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatalf("MkdirAll(relative system root) error = %v", err)
	}
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("WriteFile(marker) error = %v", err)
	}

	Uninstall("  ")

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("empty CODEX_HOME removed relative system skills: %v", err)
	}
}
