package tui

import (
	"path/filepath"
	"sort"
	"strings"
)

// Rust parity: codex-rs/tui/src/theme_picker.rs.

const (
	ThemePreviewWideMinWidth = 44
	ThemePreviewListMinWidth = 40
)

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
		if picker.Themes[i].Source != picker.Themes[j].Source {
			return picker.Themes[i].Source < picker.Themes[j].Source
		}
		return strings.ToLower(picker.Themes[i].Label) < strings.ToLower(picker.Themes[j].Label)
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

func ThemePreviewRows(width int) []string {
	rows := []struct {
		sign string
		code string
	}{
		{" ", "fn summarize(users: &[User]) -> String {"},
		{"-", "    let active = users.iter().filter(|u| u.is_active).count();"},
		{"+", "    let active = users.iter().filter(|u| u.is_active()).count();"},
		{" ", "    let names: Vec<&str> = users.iter().map(User::name).take(3).collect();"},
		{"-", "    format!(\"{} active: {}\", active, names.join(\", \"))"},
		{"+", "    format!(\"{active} active users: {}\", names.join(\", \"))"},
		{" ", "}"},
	}
	out := make([]string, 0, len(rows))
	for i, row := range rows {
		prefix := leftPadInt(i+31, 2) + " " + row.sign + " "
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
		prefix := "  "
		if i == p.Selected {
			prefix = "> "
		}
		row := prefix + theme.Label
		if theme.ID == p.Current {
			row += " (current)"
		}
		if theme.Description != "" {
			row += " - " + theme.Description
		}
		if width > 0 {
			row = TruncateWithEllipsis(row, width)
		}
		rows = append(rows, row)
	}
	return rows
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
