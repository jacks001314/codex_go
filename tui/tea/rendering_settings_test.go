package tea

import (
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/markdown"
)

// Mirrors Rust's `streaming/rendering_preferences_tests.rs`: a resolved settings
// write refreshes the Markdown rendering preferences, and a result without them
// preserves the current value.
func TestSettingsWriteResultRefreshesRenderingPreferencesLikeRust(t *testing.T) {
	markdown.InitRendering(markdown.DefaultRendering())
	t.Cleanup(func() { markdown.InitRendering(markdown.DefaultRendering()) })

	model := NewModel(codextui.NewState(nil), Options{})
	model.pendingSettingsRequestID = 7
	model.Update(SettingsWriteResultMsg{
		RequestID: 7,
		Result: SettingsWriteResult{Rendering: &RenderingSettings{
			Mermaid: false,
			Math:    true,
			Tables:  false,
			Lists:   true,
		}},
	})
	if got := markdown.CurrentRendering(); got.Mermaid || !got.Math || got.Tables || !got.Lists {
		t.Fatalf("rendering = %#v, want mermaid=false math=true tables=false lists=true", got)
	}

	// A result without rendering preferences preserves the current value.
	model.pendingSettingsRequestID = 8
	model.Update(SettingsWriteResultMsg{RequestID: 8, Result: SettingsWriteResult{}})
	if got := markdown.CurrentRendering(); got.Mermaid || got.Tables {
		t.Fatalf("nil rendering changed the preferences: %#v", got)
	}
}
