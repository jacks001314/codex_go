package tea

import (
	"testing"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: #42674 (048a936a23) experimental_features::FeatureWriteResult:
// the configured feature rows after a write plus the override warning, kept
// separate from the running task's feature state.
func TestExperimentalFeatureWriteResultLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:           90,
		Height:          24,
		FeatureSettings: map[string]bool{"worktrees": true},
	})
	model.experimentalItems = []chatwidget.ExperimentalFeatureOption{
		{Key: "worktrees", Name: "Worktrees", Enabled: false, Writable: true},
		{Key: experimentalFeaturePreventIdleSleepKey, Name: "Prevent idle sleep", Enabled: true},
	}

	result := model.experimentalFeatureWriteResult(false)
	if len(result.Features) != 2 {
		t.Fatalf("features = %#v", result.Features)
	}
	if !result.Features[0].Enabled {
		t.Fatalf("configured value was not read back: %#v", result.Features[0])
	}
	// The unmigrated control keeps its selection instead of being rewritten.
	if !result.Features[1].Enabled {
		t.Fatalf("special control lost its selection: %#v", result.Features[1])
	}
	if result.Warning != "" {
		t.Fatalf("unexpected warning: %q", result.Warning)
	}

	overridden := model.experimentalFeatureWriteResult(true)
	if overridden.Warning != experimentalFeaturesOverriddenStatus {
		t.Fatalf("warning = %q, want the Rust override text", overridden.Warning)
	}
}
