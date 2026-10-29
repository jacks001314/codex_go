package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust keeps the passive hint row (and the status line above it) below the
// composer block; only the `?` shortcut overlay takes the space above the input.
func TestFooterHintSitsBelowComposerLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		fullscreen bool
	}{
		{name: "fullscreen", fullscreen: true},
		{name: "inline", fullscreen: false},
	} {
		fullscreen := testCase.fullscreen
		model := NewModel(codextui.NewState(nil), Options{
			Width:                120,
			Height:               24,
			FullscreenTranscript: &fullscreen,
			Placeholder:          "COMPOSER-MARKER",
		})
		view := strings.TrimRight(model.View(), "\n")
		footerIndex := strings.LastIndex(view, footerHelpText)
		composerIndex := strings.Index(view, "COMPOSER-MARKER")
		if footerIndex < 0 || composerIndex < 0 {
			t.Fatalf("%s view missing footer/composer:\n%s", testCase.name, view)
		}
		if footerIndex < composerIndex {
			t.Fatalf("%s footer rendered above the composer:\n%s", testCase.name, view)
		}
	}
}
