package tea

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/plugin"
)

// Rust parity: codex-rs/tui/src/bottom_pane/skill_popup.rs (#61871).

func pluginSkillCachePath(home string, components ...string) string {
	parts := append([]string{home, filepath.FromSlash(plugin.PluginsCacheDir)}, components...)
	return filepath.Join(parts...)
}

func TestPluginSkillKeyIgnoresInstalledVersionLikeRust(t *testing.T) {
	home := t.TempDir()
	roots := skillPluginCacheRoots(home)
	if len(roots) == 0 {
		t.Fatal("plugin cache roots must be derived from CODEX_HOME")
	}
	oldSkill := pluginSkillCachePath(home, "marketplace", "plugin", "1.0.0", "skills", "alpha", "SKILL.md")
	currentSkill := pluginSkillCachePath(home, "marketplace", "plugin", "2.0.0", "skills", "alpha", "SKILL.md")
	const want = "plugin-skill://marketplace/plugin/*/skills/alpha/SKILL.md"
	oldKey, ok := skillPluginKey(oldSkill, roots)
	if !ok || oldKey != want {
		t.Fatalf("old key = %q, %v; want %q", oldKey, ok, want)
	}
	currentKey, ok := skillPluginKey(currentSkill, roots)
	if !ok || currentKey != want {
		t.Fatalf("current key = %q, %v; want %q", currentKey, ok, want)
	}
	if _, ok := skillPluginKey(pluginSkillCachePath(home, "marketplace", "plugin", "2.0.0", "README.md"), roots); ok {
		t.Fatal("only SKILL.md files inside a versioned plugin directory carry a key")
	}
	if _, ok := skillPluginKey(filepath.Join(t.TempDir(), "skills", "alpha", "SKILL.md"), roots); ok {
		t.Fatal("paths outside the plugin cache keep their raw identity")
	}
}

func TestPluginSkillRankingSurvivesVersionChangeLikeRust(t *testing.T) {
	home := t.TempDir()
	roots := skillPluginCacheRoots(home)
	oldSkill := pluginSkillCachePath(home, "marketplace", "plugin", "1.0.0", "skills", "alpha", "SKILL.md")
	currentSkill := pluginSkillCachePath(home, "marketplace", "plugin", "2.0.0", "skills", "alpha", "SKILL.md")
	usage := map[string]int{oldSkill: 3}
	items := []skillPopupItem{
		{Name: "albert", DisplayName: "albert", Path: pluginSkillCachePath(home, "marketplace", "other", "2.0.0", "skills", "albert", "SKILL.md")},
		{Name: "alpha", DisplayName: "alpha", Path: currentSkill},
	}

	raw := filterSkillPopupItems(items, "al", usage, nil)
	if len(raw) != 2 || raw[0].Name != "albert" {
		t.Fatalf("raw path ranking = %#v, want the alphabetical tie-break", raw)
	}

	frequencies := skillFrequenciesByVersionIndependentKey(usage, roots)
	got := filterSkillPopupItems(items, "al", frequencies, roots)
	if len(got) != 2 || got[0].Name != "alpha" {
		t.Fatalf("version-independent ranking = %#v, want alpha first", got)
	}
}

func TestPluginSkillKeyMatchesThroughSymlinkedCacheLikeRust(t *testing.T) {
	home := t.TempDir()
	realCache := filepath.Join(home, "real-cache")
	if err := os.MkdirAll(filepath.Join(realCache, "marketplace", "plugin", "2.0.0", "skills", "alpha"), 0o755); err != nil {
		t.Fatalf("mkdir real cache: %v", err)
	}
	linkedHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(linkedHome, "plugins"), 0o755); err != nil {
		t.Fatalf("mkdir linked home: %v", err)
	}
	if err := os.Symlink(realCache, filepath.Join(linkedHome, "plugins", "cache")); err != nil {
		t.Skipf("symlinked plugin cache unavailable: %v", err)
	}
	roots := skillPluginCacheRoots(linkedHome)
	canonicalCache, err := filepath.EvalSymlinks(realCache)
	if err != nil {
		t.Fatal(err)
	}
	canonicalSkill := filepath.Join(canonicalCache, "marketplace", "plugin", "2.0.0", "skills", "alpha", "SKILL.md")
	key, ok := skillPluginKey(canonicalSkill, roots)
	if !ok || key != "plugin-skill://marketplace/plugin/*/skills/alpha/SKILL.md" {
		t.Fatalf("canonical path under a symlinked cache = %q, %v", key, ok)
	}
}
