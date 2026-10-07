package tea

import (
	"errors"
	"testing"

	codextui "codex_go/tui"
)

// Rust #49835, `app/tests/model_defaults_tests.rs` snapshot
// `service_tier_default_save_error`: a rejected service-tier default save reports
// the underlying configuration error and explains how to retry the save without
// interrupting the running task.
func TestServiceTierDefaultSaveErrorLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	model.pendingSettingsRequestID = 7
	model.Update(SettingsWriteResultMsg{
		RequestID: 7,
		Kind:      settingsWriteKindServiceTier,
		Err:       errors.New("failed to parse /home/u/.codex/config.toml: expected `]`"),
	})

	want := "Failed to save default service tier: failed to parse /home/u/.codex/config.toml: expected `]`\n" +
		"You can continue this task. To save the default, resolve the error above, then switch to a different tier and back to the desired tier."
	if model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
}
