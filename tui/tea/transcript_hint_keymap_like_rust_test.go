package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// TestOpenTranscriptHintFollowsKeymapLikeRust covers Rust #48761's wiring: the
// model resolves the configured `open_transcript` shortcut for the hidden-output
// disclosure hints (so a runtime remap is reflected on the next render), and an
// action with no binding omits the hints.
func TestOpenTranscriptHintFollowsKeymapLikeRust(t *testing.T) {
	defer codextui.SetOpenTranscriptHintProvider(nil)

	model := NewModel(nil, Options{Width: 120, Height: 24})
	if got := codextui.TranscriptDisclosureHint(); got != "ctrl+t to view transcript" {
		t.Fatalf("default hint = %q, want the built-in binding", got)
	}

	remapped := codextui.NewKeymapConfig()
	if err := remapped.Set("global", "open_transcript", []string{"left"}); err != nil {
		t.Fatalf("bind open_transcript: %v", err)
	}
	NewModel(nil, Options{Width: 120, Height: 24, KeymapConfig: remapped})
	if want := displayKeyBinding("left") + " to view transcript"; codextui.TranscriptDisclosureHint() != want {
		t.Fatalf("remapped hint = %q, want %q", codextui.TranscriptDisclosureHint(), want)
	}

	unbound := codextui.NewKeymapConfig()
	if err := unbound.Set("global", "open_transcript", []string{}); err != nil {
		t.Fatalf("clear open_transcript: %v", err)
	}
	NewModel(nil, Options{Width: 120, Height: 24, KeymapConfig: unbound})
	if got := codextui.TranscriptDisclosureHint(); got != "" {
		t.Fatalf("unbound hint = %q, want empty", got)
	}
	_ = model
}
