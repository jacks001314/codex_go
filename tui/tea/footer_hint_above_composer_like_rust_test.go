package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #47096: owned/fullscreen layouts reserve the hint row immediately above
// the composer, while inline rendering keeps it after the composer.
func TestFooterHintsSitAboveComposerInFullscreenLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		fullscreen bool
		beforeComp bool
	}{
		{name: "fullscreen", fullscreen: true, beforeComp: true},
		{name: "inline", fullscreen: false, beforeComp: false},
	} {
		fullscreen := testCase.fullscreen
		model := NewModel(codextui.NewState(nil), Options{
			Width:                120,
			Height:               24,
			FullscreenTranscript: &fullscreen,
			Placeholder:          "COMPOSER-MARKER",
		})
		view := model.View()
		footerIndex := strings.Index(view, footerHelpText)
		composerIndex := strings.Index(view, "COMPOSER-MARKER")
		if footerIndex < 0 || composerIndex < 0 {
			t.Fatalf("%s view missing footer/composer:\n%s", testCase.name, view)
		}
		if got := footerIndex < composerIndex; got != testCase.beforeComp {
			t.Fatalf("%s footer before composer = %v, want %v:\n%s", testCase.name, got, testCase.beforeComp, view)
		}
	}
}
