// Package styles provides a centralized, semantic color system for the TUI.
// Instead of hardcoded ANSI color indices ("8", "12", "236") or raw escape
// sequences scattered across packages, all visual styling flows through a
// single Styles struct that can be swapped for theming.
package styles

import "github.com/charmbracelet/lipgloss"

// SemanticColor is a typed constant identifying a semantic color role.
// Use these instead of raw lipgloss.Color("8") or ANSI escapes.
type SemanticColor string

const (
	// Text colors
	ColorDim    SemanticColor = "dim"    // Dim / secondary text (ANSI 8)
	ColorBright SemanticColor = "bright" // Bright / primary text (ANSI 15)
	ColorAccent SemanticColor = "accent" // Accent / highlight (cyan for dark, blue for light)

	// Selection
	ColorSelected SemanticColor = "selected" // Selected row highlight (ANSI 12 blue + bold)

	// Surface colors
	ColorBackgroundDeep SemanticColor = "background_deep" // Deep background for panels (ANSI 236)
	ColorBorder         SemanticColor = "border"          // Border / separator color (ANSI 8)

	// Semantic state colors
	ColorError   SemanticColor = "error"   // Error / failure (ANSI 1;31 red bold)
	ColorSuccess SemanticColor = "success" // Success (ANSI 1;32 green bold)

	// User message styling
	ColorUserMessageBG      SemanticColor = "user_message_bg"      // User message background (48;5;235)
	ColorUserMessagePrefix  SemanticColor = "user_message_prefix"  // User message prefix style (1;2)
	ColorUserMessagePostfix SemanticColor = "user_message_postfix" // End bold (22)

	// Reset
	ColorReset SemanticColor = "reset" // ANSI reset (0)
)

// semanticToANSI maps each SemanticColor to its raw ANSI SGR parameter sequence
// (without the \x1b[ prefix or the m suffix), matching the exact hardcoded
// values that were scattered across the codebase before centralization.
var semanticToANSI = map[SemanticColor]string{
	ColorDim:                "2",        // dim
	ColorBright:             "15",       // bright white via lipgloss
	ColorAccent:             "12",       // blue via lipgloss (dark default; overridden per theme)
	ColorSelected:           "12",       // blue + bold
	ColorBackgroundDeep:     "236",      // dark gray
	ColorBorder:             "2",        // dim / gray border
	ColorError:              "1;31",     // red bold
	ColorSuccess:            "1;32",     // green bold
	ColorUserMessageBG:      "48;5;235", // background ANSI-256 color 235
	ColorUserMessagePrefix:  "1;2",      // bold + dim
	ColorUserMessagePostfix: "22",       // normal intensity
	ColorReset:              "0",        // reset
}

// semanticToLipgloss maps SemanticColor values to lipgloss.Color strings.
var semanticToLipgloss = map[SemanticColor]string{
	ColorDim:            "8",
	ColorBright:         "15",
	ColorAccent:         "12",
	ColorSelected:       "12",
	ColorBackgroundDeep: "236",
	ColorBorder:         "8",
}

// LipglossColor returns the lipgloss.Color equivalent for a semantic color.
// Returns "0" (reset) for colors that don't have a lipgloss mapping.
func LipglossColor(c SemanticColor) lipgloss.Color {
	if v, ok := semanticToLipgloss[c]; ok {
		return lipgloss.Color(v)
	}
	return lipgloss.Color("0")
}

// ANSISGR returns the ANSI SGR parameter string for a semantic color
// (without \x1b[ prefix or m suffix). Returns "0" for unknown colors.
func ANSISGR(c SemanticColor) string {
	if v, ok := semanticToANSI[c]; ok {
		return v
	}
	return "0"
}

// ANSIEscape returns a full ANSI escape sequence for a semantic color.
// Example: ANSIEscape(ColorError) returns "\x1b[1;31m".
func ANSIEscape(c SemanticColor) string {
	return "\x1b[" + ANSISGR(c) + "m"
}

// =============================================================================
// Styles struct
// =============================================================================

// ChatStyles groups colors used for rendering chat messages.
type ChatStyles struct {
	UserMessageBG      string // Background for user message lines
	UserMessagePrefix  string // Prefix style for user messages
	UserMessagePostfix string // Postfix end-style for user messages
	UserMessageReset   string // Reset after user message
	DimText            string // Dim secondary text in chat
	BrightText         string // Bright primary text in chat
}

// EditorStyles groups colors used for the composer/editor area.
type EditorStyles struct {
	BorderColor string // Composer border
	PromptColor string // Prompt indicator
}

// StatusStyles groups colors used for the status bar and footer.
type StatusStyles struct {
	HeaderBold  bool   // Whether status header is bold
	FooterColor string // Footer help text color
	BottomColor string // Bottom pane color (usually empty/default)
}

// DialogStyles groups colors used for modal dialogs and pickers.
type DialogStyles struct {
	DimText     string // Dim / secondary text in dialogs
	SelectedRow string // Selected row highlight
	Highlight   string // General highlight/accent in dialogs
	BrightText  string // Primary text in dialogs
}

// ExecCellStyles groups colors for exec cell rendering.
type ExecCellStyles struct {
	Reset   string // ANSI reset
	Bold    string // ANSI bold
	Dim     string // ANSI dim
	Error   string // ANSI error (red bold)
	Success string // ANSI success (green bold)
}

// Styles is the centralized theme configuration for the entire TUI.
// All visual styling should flow through this struct rather than using
// hardcoded colors or raw ANSI escapes.
type Styles struct {
	Chat     ChatStyles
	Editor   EditorStyles
	Status   StatusStyles
	Dialog   DialogStyles
	ExecCell ExecCellStyles

	IsDark bool
}
