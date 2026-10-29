package app

import (
	"testing"

	"codex_go/cli"
	codextea "codex_go/tui/tea"
)

func boolPtrApp(value bool) *bool { return &value }

// Rust #46883/#47417: fullscreen transcript and alternate-screen policy both
// decide terminal ownership; --no-alt-screen always wins.
func TestInteractiveNoAltScreenResolutionLikeRust(t *testing.T) {
	tests := []struct {
		name               string
		root               *cli.RootOptions
		settings           codextea.SettingsWriteResult
		terminalAppOverSSH bool
		want               bool
	}{
		{
			name:     "defaults fullscreen",
			settings: codextea.SettingsWriteResult{},
			want:     false,
		},
		{
			name:     "fullscreen disabled",
			settings: codextea.SettingsWriteResult{FullscreenTranscript: boolPtrApp(false)},
			want:     true,
		},
		{
			name: "alternate screen never",
			settings: codextea.SettingsWriteResult{
				FullscreenTranscript: boolPtrApp(true),
				AlternateScreenMode:  "never",
			},
			want: true,
		},
		{
			name: "alternate screen always",
			settings: codextea.SettingsWriteResult{
				FullscreenTranscript: boolPtrApp(true),
				AlternateScreenMode:  "always",
			},
			want: false,
		},
		{
			name:     "launch override",
			root:     &cli.RootOptions{Shared: cli.SharedOptions{NoAltScreen: true}},
			settings: codextea.SettingsWriteResult{FullscreenTranscript: boolPtrApp(true)},
			want:     true,
		},
		{
			name:               "auto keeps native scrollback for Apple Terminal over SSH",
			settings:           codextea.SettingsWriteResult{FullscreenTranscript: boolPtrApp(true), AlternateScreenMode: "auto"},
			terminalAppOverSSH: true,
			want:               true,
		},
		{
			name:               "auto owns the screen for other terminals",
			settings:           codextea.SettingsWriteResult{FullscreenTranscript: boolPtrApp(true), AlternateScreenMode: "auto"},
			terminalAppOverSSH: false,
			want:               false,
		},
		{
			name:               "always overrides the terminal identity",
			settings:           codextea.SettingsWriteResult{FullscreenTranscript: boolPtrApp(true), AlternateScreenMode: "always"},
			terminalAppOverSSH: true,
			want:               false,
		},
		{
			name:               "never overrides the terminal identity",
			settings:           codextea.SettingsWriteResult{FullscreenTranscript: boolPtrApp(true), AlternateScreenMode: "never"},
			terminalAppOverSSH: false,
			want:               true,
		},
	}
	for _, testCase := range tests {
		if got := interactiveNoAltScreen(testCase.root, testCase.settings, testCase.terminalAppOverSSH); got != testCase.want {
			t.Fatalf("%s: noAltScreen = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestInteractiveTUIModeConfigParsingLikeRust(t *testing.T) {
	values := map[string]any{"tui": map[string]any{
		"fullscreen_transcript": false,
		"alternate_screen":      "never",
	}}
	if got := interactiveFullscreenTranscript(values); got == nil || *got {
		t.Fatalf("fullscreen transcript = %#v, want false", got)
	}
	if got := interactiveAlternateScreenMode(values); got != "never" {
		t.Fatalf("alternate screen mode = %q", got)
	}
	if got := interactiveFullscreenTranscript(nil); got == nil || !*got {
		t.Fatalf("default fullscreen transcript = %#v, want true", got)
	}
	if got := interactiveAlternateScreenMode(nil); got != "auto" {
		t.Fatalf("default alternate screen mode = %q", got)
	}
	if !interactiveNoAltScreenFromValues(nil, values, false) {
		t.Fatal("configured scrollback mode did not disable the alternate screen")
	}
}
