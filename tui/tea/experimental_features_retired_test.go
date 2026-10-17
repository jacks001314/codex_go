package tea

import "testing"

// TestRetiredWorktreeAndVoiceCatalogEntriesAreHiddenLikeRust mirrors Rust
// #54175: /experimental hides `worktrees` and `realtime_conversation` even when
// an older app server still reports them as beta, while other beta entries stay
// listable.
func TestRetiredWorktreeAndVoiceCatalogEntriesAreHiddenLikeRust(t *testing.T) {
	options := experimentalFeatureOptionsFromCatalog([]ExperimentalFeatureEntry{
		{Name: "worktrees", Stage: experimentalFeatureStageBeta, DefaultEnabled: true},
		{Name: "realtime_conversation", Stage: experimentalFeatureStageBeta, DefaultEnabled: true},
		{Name: "beta_feature", Stage: experimentalFeatureStageBeta},
	})
	if len(options) != 1 || options[0].Key != "beta_feature" {
		t.Fatalf("options = %#v, want only beta_feature", options)
	}
}
