package tea

import (
	"path/filepath"
	"strings"

	"codex_go/plugin"
)

// Rust parity: codex-rs/tui/src/bottom_pane/skill_popup.rs (#61871).
// Cached plugin skill paths embed the installed version, so ranking keyed by the
// raw path forgets earlier selections whenever a plugin version changes. Usage
// frequencies are aggregated under one version-independent key per skill.

const skillPluginKeyScheme = "plugin-skill://"

// skillPluginCacheRoots lists the plugin cache roots a skill path may sit under.
// Rust pushes the canonicalized root first so a symlinked cache still matches,
// then the configured root for paths rendered without canonicalization.
func skillPluginCacheRoots(codexHome string) []string {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil
	}
	root := filepath.Join(codexHome, filepath.FromSlash(plugin.PluginsCacheDir))
	roots := make([]string, 0, 2)
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		roots = append(roots, canonical)
	}
	return append(roots, root)
}

// skillPluginKey renders a cached plugin skill path as a version-independent
// key: <cache>/<marketplace>/<plugin>/<version>/.../SKILL.md becomes
// plugin-skill://<marketplace>/<plugin>/*/.../SKILL.md. Paths that are not a
// cached plugin skill have no key and keep their raw path as the identity.
func skillPluginKey(path string, roots []string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || len(roots) == 0 {
		return "", false
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		relative = filepath.ToSlash(relative)
		components := strings.Split(relative, "/")
		if len(components) < 4 {
			continue
		}
		if components[0] == "." || components[0] == ".." {
			continue
		}
		if components[len(components)-1] != "SKILL.md" {
			continue
		}
		return skillPluginKeyScheme + components[0] + "/" + components[1] + "/*/" +
			strings.Join(components[3:], "/"), true
	}
	return "", false
}

// skillFrequenciesByVersionIndependentKey aggregates usage counts for cached
// plugin skills under their version-independent key (Rust with_frequencies).
func skillFrequenciesByVersionIndependentKey(frequencies map[string]int, roots []string) map[string]int {
	if len(frequencies) == 0 {
		return nil
	}
	aggregated := make(map[string]int, len(frequencies))
	for path, count := range frequencies {
		identity := path
		if key, ok := skillPluginKey(path, roots); ok {
			identity = key
		}
		aggregated[identity] += count
	}
	return aggregated
}

// skillSelectionFrequency looks a mention up by its version-independent key
// first, then by its raw path, matching Rust's ranked frequency lookup.
func skillSelectionFrequency(frequencies map[string]int, path string, roots []string) int {
	if len(frequencies) == 0 {
		return 0
	}
	if key, ok := skillPluginKey(path, roots); ok {
		return frequencies[key]
	}
	return frequencies[strings.TrimSpace(path)]
}
