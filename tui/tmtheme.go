package tui

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

// Custom `.tmTheme` loading. Rust parses the TextMate theme with syntect
// (render/highlight.rs load_custom_theme) and highlights through it; Go
// highlights through Chroma, so the parsed theme is mapped onto a Chroma style:
// the unscoped settings provide the text/background colors and each scoped entry
// is mapped to the closest Chroma token type. Chroma tokenises with
// Pygments-style token types rather than TextMate scopes, so the mapping is
// necessarily approximate, but a valid custom theme now applies its own colors
// instead of silently falling back to the bundled default - and an invalid
// `.tmTheme` is detected, mirroring Rust's validate_theme_name and
// list_available_themes.

// tmThemeSettings is one settings block: the unscoped block or a scoped entry.
type tmThemeSettings struct {
	Foreground string
	Background string
	FontStyle  string
}

type tmThemeScope struct {
	// Scope is the raw TextMate selector list ("string, constant").
	Scope    string
	Settings tmThemeSettings
}

type tmTheme struct {
	Name     string
	Settings tmThemeSettings
	Scopes   []tmThemeScope
}

// plistValue is the subset of plist values a `.tmTheme` needs.
type plistValue struct {
	String *string
	Bool   *bool
	Int    *int64
	Dict   map[string]plistValue
	Array  []plistValue
}

func (v plistValue) text() string {
	if v.String != nil {
		return *v.String
	}
	return ""
}

// parseTMTheme parses an Apple plist `.tmTheme` document. Rust accepts exactly
// what syntect's ThemeSet::get_theme parses, so a document without a settings
// array (or without a plist root) is rejected rather than silently defaulted.
func parseTMTheme(data []byte) (*tmTheme, error) {
	root, err := parsePlist(data)
	if err != nil {
		return nil, err
	}
	if root.Dict == nil {
		return nil, fmt.Errorf("tmTheme root is not a plist dictionary")
	}
	settingsValue, ok := root.Dict["settings"]
	if !ok || settingsValue.Array == nil {
		return nil, fmt.Errorf("tmTheme settings array is missing")
	}
	theme := &tmTheme{Name: strings.TrimSpace(root.Dict["name"].text())}
	for _, entry := range settingsValue.Array {
		if entry.Dict == nil {
			continue
		}
		scoped := tmThemeScope{Scope: strings.TrimSpace(entry.Dict["scope"].text())}
		if block, ok := entry.Dict["settings"]; ok && block.Dict != nil {
			scoped.Settings = tmThemeSettings{
				Foreground: strings.TrimSpace(block.Dict["foreground"].text()),
				Background: strings.TrimSpace(block.Dict["background"].text()),
				FontStyle:  strings.TrimSpace(block.Dict["fontStyle"].text()),
			}
		}
		if scoped.Scope == "" {
			theme.Settings = mergeTMThemeSettings(theme.Settings, scoped.Settings)
			continue
		}
		theme.Scopes = append(theme.Scopes, scoped)
	}
	if theme.Settings.Foreground == "" && theme.Settings.Background == "" && len(theme.Scopes) == 0 {
		return nil, fmt.Errorf("tmTheme has no settings")
	}
	return theme, nil
}

func mergeTMThemeSettings(base tmThemeSettings, override tmThemeSettings) tmThemeSettings {
	if override.Foreground != "" {
		base.Foreground = override.Foreground
	}
	if override.Background != "" {
		base.Background = override.Background
	}
	if override.FontStyle != "" {
		base.FontStyle = override.FontStyle
	}
	return base
}

func parsePlist(data []byte) (plistValue, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return plistValue{}, fmt.Errorf("decode tmTheme plist: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "plist" {
			continue
		}
		for {
			token, err := decoder.Token()
			if err != nil {
				return plistValue{}, fmt.Errorf("decode tmTheme plist: %w", err)
			}
			if start, ok := token.(xml.StartElement); ok {
				return parsePlistElement(decoder, start)
			}
		}
	}
}

func parsePlistElement(decoder *xml.Decoder, start xml.StartElement) (plistValue, error) {
	switch start.Name.Local {
	case "dict":
		dict := map[string]plistValue{}
		key := ""
		for {
			token, err := decoder.Token()
			if err != nil {
				return plistValue{}, err
			}
			switch element := token.(type) {
			case xml.StartElement:
				if element.Name.Local == "key" {
					value, err := readPlistText(decoder, element)
					if err != nil {
						return plistValue{}, err
					}
					key = value
					continue
				}
				value, err := parsePlistElement(decoder, element)
				if err != nil {
					return plistValue{}, err
				}
				if key != "" {
					dict[key] = value
					key = ""
				}
			case xml.EndElement:
				if element.Name.Local == "dict" {
					return plistValue{Dict: dict}, nil
				}
			}
		}
	case "array":
		array := []plistValue{}
		for {
			token, err := decoder.Token()
			if err != nil {
				return plistValue{}, err
			}
			switch element := token.(type) {
			case xml.StartElement:
				value, err := parsePlistElement(decoder, element)
				if err != nil {
					return plistValue{}, err
				}
				array = append(array, value)
			case xml.EndElement:
				if element.Name.Local == "array" {
					return plistValue{Array: array}, nil
				}
			}
		}
	case "string":
		value, err := readPlistText(decoder, start)
		if err != nil {
			return plistValue{}, err
		}
		return plistValue{String: &value}, nil
	case "integer":
		text, err := readPlistText(decoder, start)
		if err != nil {
			return plistValue{}, err
		}
		value := int64(0)
		if _, err := fmt.Sscanf(strings.TrimSpace(text), "%d", &value); err != nil {
			return plistValue{}, fmt.Errorf("invalid tmTheme integer %q", strings.TrimSpace(text))
		}
		return plistValue{Int: &value}, nil
	case "true":
		if err := skipPlistElement(decoder, start); err != nil {
			return plistValue{}, err
		}
		value := true
		return plistValue{Bool: &value}, nil
	case "false":
		if err := skipPlistElement(decoder, start); err != nil {
			return plistValue{}, err
		}
		value := false
		return plistValue{Bool: &value}, nil
	default:
		// data/date/real are not part of the theme model; consume them so the
		// surrounding dict keeps parsing.
		if err := skipPlistElement(decoder, start); err != nil {
			return plistValue{}, err
		}
		return plistValue{}, nil
	}
}

func readPlistText(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var builder strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", err
		}
		switch element := token.(type) {
		case xml.CharData:
			builder.Write(element)
		case xml.EndElement:
			if element.Name.Local == start.Name.Local {
				return builder.String(), nil
			}
		}
	}
}

func skipPlistElement(decoder *xml.Decoder, start xml.StartElement) error {
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("unterminated plist element %q", start.Name.Local)
			}
			return err
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

// customThemeChromaStyles caches the registered style name per theme id.
var customThemeChromaStyles sync.Map

// LoadCustomTheme reads and parses `<codexHome>/themes/<name>.tmTheme`, matching
// Rust's load_custom_theme.
func LoadCustomTheme(name string, codexHome string) (*tmTheme, error) {
	path := customThemePath(name, codexHome)
	if path == "" {
		return nil, fmt.Errorf("no codex home for custom theme %q", name)
	}
	return parseTMThemeFile(path)
}

// parseTMThemeFile reads and parses a `.tmTheme` path.
func parseTMThemeFile(path string) (*tmTheme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseTMTheme(data)
}

// customThemeChromaStyle returns the registered Chroma style name for a custom
// theme, building it on first use. An empty result means the theme is not a
// loadable custom `.tmTheme`.
func customThemeChromaStyle(name string, themeDir string) string {
	id := strings.ToLower(strings.TrimSpace(name))
	if id == "" {
		return ""
	}
	if cached, ok := customThemeChromaStyles.Load(id); ok {
		return cached.(string)
	}
	data, err := os.ReadFile(filepath.Join(themeDir, id+".tmTheme"))
	if err != nil {
		// Cache the miss: the resolved theme is session-scoped, so an absent or
		// invalid file is not re-read (and re-failed) on every render.
		customThemeChromaStyles.Store(id, "")
		return ""
	}
	theme, err := parseTMTheme(data)
	if err != nil {
		customThemeChromaStyles.Store(id, "")
		return ""
	}
	styleName := "codex-tmtheme-" + id
	style, err := chroma.NewStyle(styleName, theme.chromaStyleEntries())
	if err != nil {
		customThemeChromaStyles.Store(id, "")
		return ""
	}
	chromastyles.Register(style)
	customThemeChromaStyles.Store(id, styleName)
	return styleName
}

// chromaStyleEntries maps the theme's settings onto Chroma style entries: the
// unscoped block styles the default text, and each scoped entry styles the token
// type its selector maps to (later entries win, following the tmTheme cascade).
func (t *tmTheme) chromaStyleEntries() chroma.StyleEntries {
	entries := chroma.StyleEntries{}
	if spec := tmThemeStyleSpec(t.Settings.Foreground, "", t.Settings.FontStyle); spec != "" {
		entries[chroma.Text] = spec
	}
	if background := tmThemeColour(t.Settings.Background); background != "" {
		entries[chroma.Background] = "bg:" + background
	}
	for _, scoped := range t.Scopes {
		spec := tmThemeStyleSpec(scoped.Settings.Foreground, scoped.Settings.Background, scoped.Settings.FontStyle)
		if spec == "" {
			continue
		}
		for _, selector := range strings.Split(scoped.Scope, ",") {
			token, ok := tmThemeSelectorToken(strings.TrimSpace(selector))
			if !ok {
				continue
			}
			entries[token] = spec
		}
	}
	return entries
}

func tmThemeStyleSpec(foreground string, background string, fontStyle string) string {
	parts := []string{}
	if colour := tmThemeColour(foreground); colour != "" {
		parts = append(parts, colour)
	}
	if colour := tmThemeColour(background); colour != "" {
		parts = append(parts, "bg:"+colour)
	}
	for _, modifier := range strings.Fields(fontStyle) {
		switch strings.ToLower(modifier) {
		case "bold":
			parts = append(parts, "bold")
		case "italic":
			parts = append(parts, "italic")
		case "underline":
			parts = append(parts, "underline")
		}
	}
	return strings.Join(parts, " ")
}

// tmThemeColour normalizes a tmTheme colour (#RGB, #RRGGBB, or #RRGGBBAA) to the
// Chroma spec form.
func tmThemeColour(value string) string {
	colour := strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(colour) == 8 {
		colour = colour[:6]
	}
	if len(colour) == 3 {
		colour = string([]byte{colour[0], colour[0], colour[1], colour[1], colour[2], colour[2]})
	}
	if len(colour) != 6 {
		return ""
	}
	for _, digit := range colour {
		if !strings.ContainsRune("0123456789abcdefABCDEF", digit) {
			return ""
		}
	}
	return "#" + strings.ToLower(colour)
}

// tmThemeSelectorToken maps a TextMate scope selector to the closest Chroma
// token type. Only the leading scope segment is considered, matching how
// tmTheme files scope broad token families.
func tmThemeSelectorToken(selector string) (chroma.TokenType, bool) {
	selector = strings.ToLower(strings.TrimSpace(selector))
	if selector == "" {
		return 0, false
	}
	// Drop a leading negation or a trailing " - other" exclusion list.
	if index := strings.Index(selector, " - "); index >= 0 {
		selector = strings.TrimSpace(selector[:index])
	}
	prefixes := []struct {
		prefix string
		token  chroma.TokenType
	}{
		{"comment", chroma.Comment},
		{"string.regexp", chroma.StringRegex},
		{"string.interpolated", chroma.StringInterpol},
		{"string.quoted.double", chroma.StringDouble},
		{"string.quoted.single", chroma.StringSingle},
		{"string", chroma.String},
		{"constant.numeric", chroma.Number},
		{"constant.language", chroma.KeywordConstant},
		{"constant.character.escape", chroma.StringEscape},
		{"constant", chroma.NameConstant},
		{"keyword.control", chroma.Keyword},
		{"keyword.operator", chroma.Operator},
		{"keyword.declaration", chroma.KeywordDeclaration},
		{"keyword.type", chroma.KeywordType},
		{"keyword", chroma.Keyword},
		{"storage.type", chroma.KeywordType},
		{"storage", chroma.KeywordNamespace},
		{"entity.name.function", chroma.NameFunction},
		{"entity.name.type", chroma.NameClass},
		{"entity.name.tag", chroma.NameTag},
		{"entity.name", chroma.NameClass},
		{"entity.other.attribute", chroma.NameAttribute},
		{"entity.other", chroma.NameAttribute},
		{"entity", chroma.Name},
		{"variable.parameter", chroma.NameVariable},
		{"variable.language", chroma.NameBuiltin},
		{"variable.function", chroma.NameFunction},
		{"variable", chroma.NameVariable},
		{"support.function", chroma.NameBuiltin},
		{"support.type", chroma.NameClass},
		{"support.constant", chroma.NameConstant},
		{"support.variable", chroma.NameBuiltin},
		{"support", chroma.NameBuiltin},
		{"punctuation", chroma.Punctuation},
		{"invalid", chroma.Error},
		{"markup.inserted", chroma.GenericInserted},
		{"markup.deleted", chroma.GenericDeleted},
		{"markup.heading", chroma.GenericHeading},
		{"markup.underline", chroma.GenericUnderline},
		{"markup.bold", chroma.GenericStrong},
		{"markup.italic", chroma.GenericEmph},
		{"markup.raw", chroma.GenericOutput},
		{"markup.list", chroma.GenericSubheading},
		{"markup", chroma.Generic},
		{"meta", chroma.Text},
		{"source", chroma.Text},
	}
	for _, candidate := range prefixes {
		if selector == candidate.prefix || strings.HasPrefix(selector, candidate.prefix+".") {
			return candidate.token, true
		}
	}
	return 0, false
}
