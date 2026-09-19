package tui

import (
	"embed"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

// Bundled TextMate themes (Rust #46504: ada, babbage, curie, cushman, dali,
// davinci). Rust compiles the assets in with include_str! and treats the exact
// names as built in, so a custom `.tmTheme` with the same name follows the same
// precedence rule as every other bundled theme.
//
//go:embed themes/assets/*.tmTheme
var bundledThemeAssets embed.FS

var (
	bundledThemeChromaStyles sync.Map
	bundledThemeAccents      sync.Map
)

// bundledThemeFileNames lists the embedded theme asset ids.
func bundledThemeFileNames() []string {
	entries, err := bundledThemeAssets.ReadDir("themes/assets")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmTheme") {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".tmTheme"))
	}
	return names
}

func loadBundledTheme(name string) *tmTheme {
	id := strings.ToLower(strings.TrimSpace(name))
	if id == "" {
		return nil
	}
	data, err := bundledThemeAssets.ReadFile("themes/assets/" + id + ".tmTheme")
	if err != nil {
		return nil
	}
	theme, err := parseTMTheme(data)
	if err != nil {
		return nil
	}
	return theme
}

// bundledThemeChromaStyle returns the registered Chroma style name for a
// bundled theme, or "" when the name is not bundled. Custom themes are consulted
// first by the caller so a user file with the same name wins.
func bundledThemeChromaStyle(name string) string {
	id := strings.ToLower(strings.TrimSpace(name))
	if id == "" {
		return ""
	}
	if cached, ok := bundledThemeChromaStyles.Load(id); ok {
		return cached.(string)
	}
	theme := loadBundledTheme(id)
	if theme == nil {
		bundledThemeChromaStyles.Store(id, "")
		return ""
	}
	styleName := "codex-bundled-theme-" + id
	style, err := chroma.NewStyle(styleName, theme.chromaStyleEntries())
	if err != nil {
		bundledThemeChromaStyles.Store(id, "")
		return ""
	}
	chromastyles.Register(style)
	bundledThemeChromaStyles.Store(id, styleName)
	return styleName
}

// BundledThemeAccent returns the theme's `codex.accent` colour (Rust #46504:
// themes carry an accent used by active and selected controls), or "" when the
// theme does not declare one.
func BundledThemeAccent(name string) string {
	id := strings.ToLower(strings.TrimSpace(name))
	if id == "" {
		return ""
	}
	if cached, ok := bundledThemeAccents.Load(id); ok {
		return cached.(string)
	}
	accent := ""
	if theme := loadBundledTheme(id); theme != nil {
		for _, scoped := range theme.Scopes {
			for _, selector := range strings.Split(scoped.Scope, ",") {
				if strings.EqualFold(strings.TrimSpace(selector), "codex.accent") {
					accent = strings.TrimSpace(scoped.Settings.Foreground)
					break
				}
			}
			if accent != "" {
				break
			}
		}
	}
	bundledThemeAccents.Store(id, accent)
	return accent
}
