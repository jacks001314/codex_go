package styles

import (
	"testing"
)

func TestDefaultDarkMappings(t *testing.T) {
	s := DefaultDark()

	// Verify exact ANSI escape values match previously hardcoded values
	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"Chat.UserMessageBG", s.Chat.UserMessageBG, "\x1b[48;5;235m"},
		{"Chat.UserMessagePrefix", s.Chat.UserMessagePrefix, "\x1b[1;2m"},
		{"Chat.UserMessagePostfix", s.Chat.UserMessagePostfix, "\x1b[22m"},
		{"Chat.UserMessageReset", s.Chat.UserMessageReset, "\x1b[0m"},
		{"Chat.DimText", s.Chat.DimText, "\x1b[2m"},
		{"Editor.BorderColor", s.Editor.BorderColor, "\x1b[2m"},
		{"Status.FooterColor", s.Status.FooterColor, "\x1b[2m"},
		{"Dialog.DimText", s.Dialog.DimText, "\x1b[2m"},
		{"Dialog.SelectedRow", s.Dialog.SelectedRow, "\x1b[12m"},
		{"Dialog.Highlight", s.Dialog.Highlight, "\x1b[12m"},
		{"Dialog.BrightText", s.Dialog.BrightText, "\x1b[15m"},
		{"ExecCell.Reset", s.ExecCell.Reset, "\x1b[0m"},
		{"ExecCell.Bold", s.ExecCell.Bold, "\x1b[1m"},
		{"ExecCell.Dim", s.ExecCell.Dim, "\x1b[2m"},
		{"ExecCell.Error", s.ExecCell.Error, "\x1b[1;31m"},
		{"ExecCell.Success", s.ExecCell.Success, "\x1b[1;32m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.expected)
			}
		})
	}
}

func TestDefaultDarkIsDark(t *testing.T) {
	s := DefaultDark()
	if !s.IsDark {
		t.Error("DefaultDark() should have IsDark=true")
	}
}

func TestDefaultLightIsNotDark(t *testing.T) {
	s := DefaultLight()
	if s.IsDark {
		t.Error("DefaultLight() should have IsDark=false")
	}
}

func TestLipglossColor(t *testing.T) {
	tests := []struct {
		color    SemanticColor
		expected string
	}{
		{ColorDim, "8"},
		{ColorBright, "15"},
		{ColorAccent, "12"},
		{ColorSelected, "12"},
		{ColorBackgroundDeep, "236"},
		{ColorBorder, "8"},
	}

	for _, tt := range tests {
		t.Run(string(tt.color), func(t *testing.T) {
			got := LipglossColor(tt.color)
			if string(got) != tt.expected {
				t.Errorf("LipglossColor(%s) = %q, want %q", tt.color, got, tt.expected)
			}
		})
	}
}

func TestANSISGR(t *testing.T) {
	tests := []struct {
		color    SemanticColor
		expected string
	}{
		{ColorDim, "2"},
		{ColorError, "1;31"},
		{ColorSuccess, "1;32"},
		{ColorReset, "0"},
		{ColorUserMessageBG, "48;5;235"},
		{ColorUserMessagePrefix, "1;2"},
		{ColorUserMessagePostfix, "22"},
	}

	for _, tt := range tests {
		t.Run(string(tt.color), func(t *testing.T) {
			got := ANSISGR(tt.color)
			if got != tt.expected {
				t.Errorf("ANSISGR(%s) = %q, want %q", tt.color, got, tt.expected)
			}
		})
	}
}

func TestANSIEscape(t *testing.T) {
	tests := []struct {
		color    SemanticColor
		expected string
	}{
		{ColorReset, "\x1b[0m"},
		{ColorError, "\x1b[1;31m"},
		{ColorDim, "\x1b[2m"},
	}

	for _, tt := range tests {
		t.Run(string(tt.color), func(t *testing.T) {
			got := ANSIEscape(tt.color)
			if got != tt.expected {
				t.Errorf("ANSIEscape(%s) = %q, want %q", tt.color, got, tt.expected)
			}
		})
	}
}

func TestUnknownColorReturnsReset(t *testing.T) {
	unknown := SemanticColor("nonexistent")
	if ANSISGR(unknown) != "0" {
		t.Error("Unknown color should return ANSI reset '0'")
	}
	if string(LipglossColor(unknown)) != "0" {
		t.Error("Unknown lipgloss color should return '0'")
	}
}
