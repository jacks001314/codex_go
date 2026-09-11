package tui

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

// Rust parity: codex-rs/tui/src/thread_color.rs.
//
// Stateless thread identity colors drawn from the active syntax theme. Only the
// theme palette is cached. Assignments depend on the full thread ID, never on
// names, list order, or other threads; palette collisions are allowed.

const (
	threadColorHashOffset = 0xcbf29ce484222325
	threadColorHashPrime  = 0x100000001b3
)

var (
	threadColorPaletteMu    sync.Mutex
	threadColorPaletteCache = map[string][]string{}
)

// ThreadColorPalette returns the accent colors (as "#rrggbb") of the syntax
// theme that backs the given Codex theme id. Base text, background, comment and
// punctuation colors are excluded, and a foreground paired with a special
// background is not treated as a standalone accent. The result is empty when
// the theme contributes no accents; Rust falls back to the terminal default in
// that case.
func ThreadColorPalette(themeID string) []string {
	styleName := ChromaThemeForCodexTheme(themeID)
	threadColorPaletteMu.Lock()
	defer threadColorPaletteMu.Unlock()
	if palette, ok := threadColorPaletteCache[styleName]; ok {
		return palette
	}
	palette := chromaStyleAccents(styles.Get(styleName))
	threadColorPaletteCache[styleName] = palette
	return palette
}

// ResetThreadColorPaletteCache clears the cached theme palettes (theme change).
func ResetThreadColorPaletteCache() {
	threadColorPaletteMu.Lock()
	threadColorPaletteCache = map[string][]string{}
	threadColorPaletteMu.Unlock()
}

func chromaStyleAccents(style *chroma.Style) []string {
	if style == nil {
		return nil
	}
	baseBackground := style.Get(chroma.Background).Background
	excluded := map[chroma.Colour]bool{}
	for _, ttype := range []chroma.TokenType{chroma.Text, chroma.Comment, chroma.Punctuation} {
		if fg := style.Get(ttype).Colour; fg.IsSet() {
			excluded[fg] = true
		}
	}

	types := style.Types()
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	seen := map[chroma.Colour]bool{}
	palette := []string{}
	for _, ttype := range types {
		entry := style.Get(ttype)
		if !entry.Colour.IsSet() {
			continue
		}
		if entry.Background.IsSet() && entry.Background != baseBackground {
			continue
		}
		if excluded[entry.Colour] || seen[entry.Colour] {
			continue
		}
		seen[entry.Colour] = true
		palette = append(palette, entry.Colour.String())
	}
	return palette
}

// ThreadColorIndex maps a thread id to a palette slot using an explicit FNV-1a
// hash (not Go's process-randomized map hashing), matching Rust's
// `color_for_id`. paletteSize must be positive.
func ThreadColorIndex(threadID string, paletteSize int) int {
	if paletteSize <= 0 {
		return 0
	}
	hash := uint64(threadColorHashOffset)
	for i := 0; i < len(threadID); i++ {
		hash ^= uint64(threadID[i])
		hash *= threadColorHashPrime
	}
	return int(hash % uint64(paletteSize))
}

// ThreadColorForTheme returns the deterministic accent color ("#rrggbb") for a
// thread id under the given Codex theme id. It returns "" when the id is empty
// or the theme has no accents (callers then leave the terminal default).
func ThreadColorForTheme(threadID string, themeID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	palette := ThreadColorPalette(themeID)
	if len(palette) == 0 {
		return ""
	}
	return palette[ThreadColorIndex(threadID, len(palette))]
}

// ThreadColorSGR converts a "#rrggbb" accent color to a truecolor foreground
// SGR sequence, or "" when the value is not a parseable hex triplet.
func ThreadColorSGR(color string) string {
	r, g, b, ok := parseThreadColor(color)
	if !ok {
		return ""
	}
	return "\x1b[38;2;" + strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(b) + "m"
}

func parseThreadColor(color string) (int, int, int, bool) {
	color = strings.TrimSpace(color)
	if !strings.HasPrefix(color, "#") {
		return 0, 0, 0, false
	}
	color = color[1:]
	if len(color) != 6 {
		return 0, 0, 0, false
	}
	value, err := strconv.ParseUint(color, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(value>>16) & 0xff, int(value>>8) & 0xff, int(value) & 0xff, true
}
