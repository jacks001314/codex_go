package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Rust parity: codex-rs/tui/src/theme_picker.rs.

const (
	ThemePreviewWideMinWidth     = 44
	ThemePreviewListMinWidth     = 40
	ThemePreviewFallbackSubtitle = "Move up/down to live preview themes"
)

var builtinThemeIDs = []string{
	"1337",
	"ansi",
	"base16",
	"base16-256",
	"base16-eighties-dark",
	"base16-mocha-dark",
	"base16-ocean-dark",
	"base16-ocean-light",
	"catppuccin-frappe",
	"catppuccin-latte",
	"catppuccin-macchiato",
	"catppuccin-mocha",
	"coldark-cold",
	"coldark-dark",
	"dark-neon",
	"dracula",
	"github",
	"gruvbox-dark",
	"gruvbox-light",
	"inspired-github",
	"monokai-extended",
	"monokai-extended-bright",
	"monokai-extended-light",
	"monokai-extended-origin",
	"nord",
	"one-half-dark",
	"one-half-light",
	"solarized-dark",
	"solarized-light",
	"sublime-snazzy",
	"two-dark",
	"zenburn",
}

type ThemeSource string

const (
	ThemeSourceBundled ThemeSource = "bundled"
	ThemeSourceCustom  ThemeSource = "custom"
)

type ThemeOption struct {
	ID          string
	Label       string
	Path        string
	Source      ThemeSource
	Description string
}

func BuiltinThemeIDs() []string {
	return append([]string(nil), builtinThemeIDs...)
}

type ThemePicker struct {
	Themes   []ThemeOption
	Current  string
	Selected int
	snapshot string
}

func NewThemePicker(themes []ThemeOption, current string) *ThemePicker {
	picker := &ThemePicker{
		Themes:   append([]ThemeOption(nil), themes...),
		Current:  strings.TrimSpace(current),
		snapshot: strings.TrimSpace(current),
	}
	sort.SliceStable(picker.Themes, func(i, j int) bool {
		left := strings.ToLower(picker.Themes[i].ID)
		right := strings.ToLower(picker.Themes[j].ID)
		if left != right {
			return left < right
		}
		return picker.Themes[i].ID < picker.Themes[j].ID
	})
	for i, theme := range picker.Themes {
		if theme.ID == picker.Current {
			picker.Selected = i
			break
		}
	}
	return picker
}

func DiscoverThemeOptions(bundled []string, customPaths []string) []ThemeOption {
	options := make([]ThemeOption, 0, len(bundled)+len(customPaths))
	seen := map[string]bool{}
	for _, name := range bundled {
		id := strings.TrimSpace(name)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		options = append(options, ThemeOption{
			ID:          id,
			Label:       humanThemeLabel(id),
			Source:      ThemeSourceBundled,
			Description: "Built in",
		})
	}
	for _, path := range customPaths {
		path = strings.TrimSpace(path)
		if path == "" || strings.ToLower(filepath.Ext(path)) != ".tmtheme" {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if id == "" {
			id = path
		}
		if seen[id] {
			id = path
		}
		seen[id] = true
		options = append(options, ThemeOption{
			ID:          id,
			Label:       humanThemeLabel(id),
			Path:        path,
			Source:      ThemeSourceCustom,
			Description: "Custom theme",
		})
	}
	return options
}

func DefaultThemeDir() string {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			codexHome = filepath.Join(home, ".codex")
		}
	}
	if codexHome == "" {
		return filepath.Join(".codex", "themes")
	}
	return filepath.Join(codexHome, "themes")
}

func DiscoverCustomThemePaths(themeDir string) []string {
	themeDir = strings.TrimSpace(themeDir)
	if themeDir == "" {
		return nil
	}
	entries, err := os.ReadDir(themeDir)
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.ToLower(filepath.Ext(name)) != ".tmtheme" {
			continue
		}
		paths = append(paths, filepath.Join(themeDir, name))
	}
	sort.SliceStable(paths, func(i, j int) bool {
		left := strings.ToLower(filepath.Base(paths[i]))
		right := strings.ToLower(filepath.Base(paths[j]))
		if left != right {
			return left < right
		}
		return paths[i] < paths[j]
	})
	return paths
}

func ThemePickerSubtitle(themeDir string, width int) string {
	display := formatThemeDirDisplay(themeDir)
	subtitle := "Custom .tmTheme files can be added to the " + display + " directory."
	available := width
	if layout := ComputeThemePreviewLayout(width); layout.Wide {
		available = layout.ListWidth
	}
	if strings.HasPrefix(display, "~") && (available <= 0 || DisplayWidth(subtitle) <= available) {
		return subtitle
	}
	return ThemePreviewFallbackSubtitle
}

func formatThemeDirDisplay(themeDir string) string {
	themeDir = strings.TrimSpace(themeDir)
	if themeDir == "" {
		return filepath.Join("~", ".codex", "themes")
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		home = filepath.Clean(home)
		cleaned := filepath.Clean(themeDir)
		if cleaned == home {
			return "~"
		}
		if rel, relErr := filepath.Rel(home, cleaned); relErr == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return filepath.Join("~", rel)
		}
	}
	return themeDir
}

func (p *ThemePicker) Move(delta int) {
	if p == nil || len(p.Themes) == 0 {
		return
	}
	p.Selected = (p.Selected + delta) % len(p.Themes)
	if p.Selected < 0 {
		p.Selected += len(p.Themes)
	}
}

func (p *ThemePicker) Select(index int) {
	if p != nil && index >= 0 && index < len(p.Themes) {
		p.Selected = index
	}
}

func (p *ThemePicker) SelectedTheme() (ThemeOption, bool) {
	if p == nil || len(p.Themes) == 0 || p.Selected < 0 || p.Selected >= len(p.Themes) {
		return ThemeOption{}, false
	}
	return p.Themes[p.Selected], true
}

func (p *ThemePicker) PreviewThemeID() string {
	theme, ok := p.SelectedTheme()
	if !ok {
		return p.Current
	}
	return theme.ID
}

func (p *ThemePicker) Confirm() string {
	theme, ok := p.SelectedTheme()
	if !ok {
		return p.Current
	}
	p.Current = theme.ID
	p.snapshot = theme.ID
	return p.Current
}

func (p *ThemePicker) Cancel() string {
	if p == nil {
		return ""
	}
	p.Current = p.snapshot
	for i, theme := range p.Themes {
		if theme.ID == p.Current {
			p.Selected = i
			break
		}
	}
	return p.Current
}

type ThemePreviewLayout struct {
	Wide      bool
	ListWidth int
	SideWidth int
}

type ThemePreviewPalette struct {
	Foreground       string
	Dim              string
	InsertForeground string
	DeleteForeground string
	InsertBackground string
	DeleteBackground string
}

func ComputeThemePreviewLayout(totalWidth int) ThemePreviewLayout {
	if totalWidth < ThemePreviewWideMinWidth+ThemePreviewListMinWidth {
		return ThemePreviewLayout{Wide: false, ListWidth: totalWidth, SideWidth: 0}
	}
	side := totalWidth / 2
	if side < ThemePreviewWideMinWidth {
		side = ThemePreviewWideMinWidth
	}
	list := totalWidth - side
	if list < ThemePreviewListMinWidth {
		return ThemePreviewLayout{Wide: false, ListWidth: totalWidth, SideWidth: 0}
	}
	return ThemePreviewLayout{Wide: true, ListWidth: list, SideWidth: side}
}

func ThemePreviewPaletteForID(themeID string) ThemePreviewPalette {
	id := strings.ToLower(strings.TrimSpace(themeID))
	if palette, ok := themePreviewPalettes[id]; ok {
		return palette
	}
	for prefix, palette := range themePreviewPalettePrefixes {
		if strings.HasPrefix(id, prefix) {
			return palette
		}
	}
	return generatedThemePreviewPalette(id)
}

var themePreviewPalettes = map[string]ThemePreviewPalette{
	"1337": {
		Foreground:       "#f0f0f0",
		Dim:              "#7f7f7f",
		InsertForeground: "#afff00",
		DeleteForeground: "#ff5f87",
		InsertBackground: "#1f3a00",
		DeleteBackground: "#3a0016",
	},
	"ansi": {
		Foreground:       "#eeeeee",
		Dim:              "#8a8a8a",
		InsertForeground: "#00ff00",
		DeleteForeground: "#ff0000",
		InsertBackground: "#003300",
		DeleteBackground: "#330000",
	},
	"base16": {
		Foreground:       "#d8d8d8",
		Dim:              "#808080",
		InsertForeground: "#b8bb26",
		DeleteForeground: "#fb4934",
		InsertBackground: "#2d3a24",
		DeleteBackground: "#3a2424",
	},
	"base16-256": {
		Foreground:       "#d8d8d8",
		Dim:              "#808080",
		InsertForeground: "#5fd700",
		DeleteForeground: "#ff5f5f",
		InsertBackground: "#1f3a1f",
		DeleteBackground: "#3a1f1f",
	},
	"base16-eighties-dark": {
		Foreground:       "#d3d0c8",
		Dim:              "#747369",
		InsertForeground: "#99cc99",
		DeleteForeground: "#f2777a",
		InsertBackground: "#263326",
		DeleteBackground: "#3a2528",
	},
	"base16-mocha-dark": {
		Foreground:       "#d0c8c6",
		Dim:              "#7e705a",
		InsertForeground: "#beb55b",
		DeleteForeground: "#cb6077",
		InsertBackground: "#333021",
		DeleteBackground: "#38252b",
	},
	"base16-ocean-dark": {
		Foreground:       "#c0c5ce",
		Dim:              "#65737e",
		InsertForeground: "#a3be8c",
		DeleteForeground: "#bf616a",
		InsertBackground: "#26342d",
		DeleteBackground: "#39282d",
	},
	"base16-ocean-light": {
		Foreground:       "#343d46",
		Dim:              "#a7adba",
		InsertForeground: "#4f7d3f",
		DeleteForeground: "#ac4142",
		InsertBackground: "#e4f4df",
		DeleteBackground: "#f6dfdf",
	},
	"catppuccin-frappe": {
		Foreground:       "#c6d0f5",
		Dim:              "#838ba7",
		InsertForeground: "#a6d189",
		DeleteForeground: "#e78284",
		InsertBackground: "#2b3f2b",
		DeleteBackground: "#493131",
	},
	"catppuccin-latte": {
		Foreground:       "#4c4f69",
		Dim:              "#8c8fa1",
		InsertForeground: "#40a02b",
		DeleteForeground: "#d20f39",
		InsertBackground: "#dff4d8",
		DeleteBackground: "#f8d7dc",
	},
	"catppuccin-macchiato": {
		Foreground:       "#cad3f5",
		Dim:              "#8087a2",
		InsertForeground: "#a6da95",
		DeleteForeground: "#ed8796",
		InsertBackground: "#263b2f",
		DeleteBackground: "#4a2d36",
	},
	"catppuccin-mocha": {
		Foreground:       "#cdd6f4",
		Dim:              "#7f849c",
		InsertForeground: "#a6e3a1",
		DeleteForeground: "#f38ba8",
		InsertBackground: "#263a2b",
		DeleteBackground: "#4a2834",
	},
	"dracula": {
		Foreground:       "#f8f8f2",
		Dim:              "#6272a4",
		InsertForeground: "#50fa7b",
		DeleteForeground: "#ff5555",
		InsertBackground: "#1f3d2b",
		DeleteBackground: "#3d1f2b",
	},
	"github": {
		Foreground:       "#24292f",
		Dim:              "#57606a",
		InsertForeground: "#116329",
		DeleteForeground: "#82071e",
		InsertBackground: "#dafbe1",
		DeleteBackground: "#ffebe9",
	},
	"gruvbox-dark": {
		Foreground:       "#ebdbb2",
		Dim:              "#928374",
		InsertForeground: "#b8bb26",
		DeleteForeground: "#fb4934",
		InsertBackground: "#32351e",
		DeleteBackground: "#3c2521",
	},
	"gruvbox-light": {
		Foreground:       "#3c3836",
		Dim:              "#928374",
		InsertForeground: "#79740e",
		DeleteForeground: "#9d0006",
		InsertBackground: "#ebf0c5",
		DeleteBackground: "#f0d8ca",
	},
	"nord": {
		Foreground:       "#d8dee9",
		Dim:              "#616e88",
		InsertForeground: "#a3be8c",
		DeleteForeground: "#bf616a",
		InsertBackground: "#26352f",
		DeleteBackground: "#3b2830",
	},
	"one-half-dark": {
		Foreground:       "#dcdfe4",
		Dim:              "#5c6370",
		InsertForeground: "#98c379",
		DeleteForeground: "#e06c75",
		InsertBackground: "#293827",
		DeleteBackground: "#3a282d",
	},
	"one-half-light": {
		Foreground:       "#383a42",
		Dim:              "#a0a1a7",
		InsertForeground: "#50a14f",
		DeleteForeground: "#e45649",
		InsertBackground: "#e3f4de",
		DeleteBackground: "#f5ddd8",
	},
	"solarized-dark": {
		Foreground:       "#839496",
		Dim:              "#586e75",
		InsertForeground: "#859900",
		DeleteForeground: "#dc322f",
		InsertBackground: "#173a3a",
		DeleteBackground: "#3a2226",
	},
	"solarized-light": {
		Foreground:       "#657b83",
		Dim:              "#93a1a1",
		InsertForeground: "#859900",
		DeleteForeground: "#dc322f",
		InsertBackground: "#eef6d6",
		DeleteBackground: "#f6d9d8",
	},
}

var themePreviewPalettePrefixes = map[string]ThemePreviewPalette{
	"base16": {
		Foreground:       "#d8d8d8",
		Dim:              "#808080",
		InsertForeground: "#b8bb26",
		DeleteForeground: "#fb4934",
		InsertBackground: "#2d3a24",
		DeleteBackground: "#3a2424",
	},
	"coldark": {
		Foreground:       "#e3eaf2",
		Dim:              "#8da1b9",
		InsertForeground: "#8bd49c",
		DeleteForeground: "#ff6b7a",
		InsertBackground: "#21362d",
		DeleteBackground: "#3a242d",
	},
	"gruvbox": {
		Foreground:       "#ebdbb2",
		Dim:              "#928374",
		InsertForeground: "#b8bb26",
		DeleteForeground: "#fb4934",
		InsertBackground: "#32351e",
		DeleteBackground: "#3c2521",
	},
	"monokai": {
		Foreground:       "#f8f8f2",
		Dim:              "#75715e",
		InsertForeground: "#a6e22e",
		DeleteForeground: "#f92672",
		InsertBackground: "#27351d",
		DeleteBackground: "#3a1f2d",
	},
	"one-half": {
		Foreground:       "#dcdfe4",
		Dim:              "#5c6370",
		InsertForeground: "#98c379",
		DeleteForeground: "#e06c75",
		InsertBackground: "#293827",
		DeleteBackground: "#3a282d",
	},
}

func generatedThemePreviewPalette(themeID string) ThemePreviewPalette {
	if strings.TrimSpace(themeID) == "" {
		return themePreviewPalettes["catppuccin-mocha"]
	}
	hash := uint32(2166136261)
	for _, r := range themeID {
		hash ^= uint32(r)
		hash *= 16777619
	}
	hue := int(hash % 6)
	palettes := []ThemePreviewPalette{
		{Foreground: "#e6edf3", Dim: "#7d8590", InsertForeground: "#56d364", DeleteForeground: "#ff7b72", InsertBackground: "#19361f", DeleteBackground: "#3b1f24"},
		{Foreground: "#f5e0dc", Dim: "#988ba2", InsertForeground: "#94e2d5", DeleteForeground: "#f38ba8", InsertBackground: "#1f3438", DeleteBackground: "#3c2633"},
		{Foreground: "#e0def4", Dim: "#908caa", InsertForeground: "#9ccfd8", DeleteForeground: "#eb6f92", InsertBackground: "#1f3438", DeleteBackground: "#3c2633"},
		{Foreground: "#eceff4", Dim: "#81a1c1", InsertForeground: "#a3be8c", DeleteForeground: "#bf616a", InsertBackground: "#26352f", DeleteBackground: "#3b2830"},
		{Foreground: "#fdf6e3", Dim: "#93a1a1", InsertForeground: "#859900", DeleteForeground: "#dc322f", InsertBackground: "#eef6d6", DeleteBackground: "#f6d9d8"},
		{Foreground: "#f8f8f2", Dim: "#a59f85", InsertForeground: "#a6e22e", DeleteForeground: "#f92672", InsertBackground: "#27351d", DeleteBackground: "#3a1f2d"},
	}
	return palettes[hue]
}

func ThemePreviewRows(width int) []string {
	rows := []struct {
		line int
		sign string
		code string
	}{
		{31, " ", "fn summarize(users: &[User]) -> String {"},
		{32, "-", "    let active = users.iter().filter(|u| u.is_active).count();"},
		{32, "+", "    let active = users.iter().filter(|u| u.is_active()).count();"},
		{33, " ", "    let names: Vec<&str> = users.iter().map(User::name).take(3).collect();"},
		{34, "-", "    format!(\"{} active: {}\", active, names.join(\", \"))"},
		{34, "+", "    format!(\"{active} active users: {}\", names.join(\", \"))"},
		{35, "+", "        .trim()"},
		{36, " ", "}"},
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		prefix := leftPadInt(row.line, 2) + " " + row.sign + " "
		out = append(out, AdaptiveWrapLine(row.code, WrapOptions{
			Width:            width,
			InitialIndent:    prefix,
			SubsequentIndent: strings.Repeat(" ", DisplayWidth(prefix)),
			BreakWords:       true,
		})...)
	}
	return out
}

func NarrowThemePreviewRows(width int) []string {
	rows := []struct {
		line int
		sign string
		code string
	}{
		{12, " ", "fn greet(name: &str) -> String {"},
		{13, "-", "    format!(\"Hello, {}!\", name)"},
		{13, "+", "    format!(\"Hello, {name}!\")"},
		{14, " ", "}"},
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		prefix := leftPadInt(row.line, 2) + " " + row.sign + " "
		out = append(out, AdaptiveWrapLine(row.code, WrapOptions{
			Width:            width,
			InitialIndent:    prefix,
			SubsequentIndent: strings.Repeat(" ", DisplayWidth(prefix)),
			BreakWords:       true,
		})...)
	}
	return out
}

func (p *ThemePicker) RenderRows(width int) []string {
	if p == nil || len(p.Themes) == 0 {
		return []string{"No themes found"}
	}
	rows := make([]string, 0, len(p.Themes))
	for i, theme := range p.Themes {
		selected := i == p.Selected
		row := SelectionPrefix(selected) + ThemePickerDisplayName(theme)
		if theme.ID == p.Current {
			row += " (current)"
		}
		if width > 0 {
			row = TruncateWithEllipsis(row, width)
		}
		if selected {
			row = RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	return rows
}

func ThemePickerDisplayName(theme ThemeOption) string {
	id := strings.TrimSpace(theme.ID)
	if id == "" {
		id = strings.TrimSpace(theme.Label)
	}
	if id == "" {
		id = "theme"
	}
	if theme.Source == ThemeSourceCustom {
		return id + " (custom)"
	}
	return id
}

func (p *ThemePicker) FilteredIndices(filter string) []int {
	if p == nil {
		return nil
	}
	filter = strings.ToLower(strings.TrimSpace(filter))
	indices := make([]int, 0, len(p.Themes))
	for i, theme := range p.Themes {
		if filter == "" || themeMatchesFilter(theme, filter) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (p *ThemePicker) SelectFirstFiltered(filter string) {
	indices := p.FilteredIndices(filter)
	if len(indices) > 0 {
		p.Select(indices[0])
	}
}

func (p *ThemePicker) MoveFiltered(delta int, filter string) {
	indices := p.FilteredIndices(filter)
	if p == nil || len(indices) == 0 {
		return
	}
	position := 0
	for i, index := range indices {
		if index == p.Selected {
			position = i
			break
		}
	}
	position = (position + delta) % len(indices)
	if position < 0 {
		position += len(indices)
	}
	p.Select(indices[position])
}

func themeMatchesFilter(theme ThemeOption, filter string) bool {
	values := []string{
		theme.ID,
		theme.Label,
		theme.Description,
		filepath.Base(theme.Path),
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), filter) {
			return true
		}
	}
	return false
}

func humanThemeLabel(id string) string {
	id = strings.TrimSpace(strings.TrimSuffix(id, filepath.Ext(id)))
	id = strings.ReplaceAll(id, "_", " ")
	id = strings.ReplaceAll(id, "-", " ")
	words := strings.Fields(id)
	for i, word := range words {
		if len(word) == 0 {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	if len(words) == 0 {
		return "Theme"
	}
	return strings.Join(words, " ")
}
