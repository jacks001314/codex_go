package tea

import (
	"strings"
	"testing"

	"codex_go/config"
	codextui "codex_go/tui"
)

// Rust #42639 (app/model_defaults.rs::persist_model_defaults): a saved default
// can succeed while a higher-priority configuration layer overrides it, so the
// service-tier confirmation is followed by an explicit override warning. A
// plain successful write keeps only the confirmation, and a rejected write
// keeps the existing failure notice.
func TestServiceTierDefaultOverrideWarningLikeRust(t *testing.T) {
	view := func(model *Model) string {
		model.refreshTranscript()
		return model.View()
	}
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 40})
	model.pendingSettingsRequestID = 11
	model.Update(SettingsWriteResultMsg{
		RequestID: 11,
		Kind:      settingsWriteKindServiceTier,
		Result: SettingsWriteResult{
			FilePath:    "/home/u/.codex/config.toml",
			WriteStatus: config.WriteOKOverridden,
		},
	})
	if got := model.notice; !strings.Contains(got, "Service tier set to") {
		t.Fatalf("notice = %q, want the saved confirmation", got)
	}
	rendered := view(model)
	if !strings.Contains(rendered, "Saved default service tier, but a higher-priority configuration layer overrides the saved value.") {
		t.Fatalf("override warning missing:\n%s", rendered)
	}

	model = NewModel(codextui.NewState(nil), Options{Width: 100, Height: 40})
	model.pendingSettingsRequestID = 12
	model.Update(SettingsWriteResultMsg{
		RequestID: 12,
		Kind:      settingsWriteKindServiceTier,
		Result: SettingsWriteResult{
			FilePath:    "/home/u/.codex/config.toml",
			WriteStatus: config.WriteOK,
		},
	})
	if rendered := view(model); strings.Contains(rendered, "higher-priority configuration layer overrides") {
		t.Fatalf("plain save reported a spurious override:\n%s", rendered)
	}
}
