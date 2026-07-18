package chatwidget

import "testing"

func TestIdeContextStatusRefreshFailureDisablesMatchRust(t *testing.T) {
	state := IdeContextState{Enabled: true}
	result := state.HandleCommand("status", false, false, "editor unavailable")
	if result.Enabled || state.Enabled || result.Message != "IDE context could not be enabled." || result.Hint != "editor unavailable" || result.Error {
		t.Fatalf("status failure = %#v state=%#v", result, state)
	}
}

func TestIdeContextArgsAreNotTrimmedAtStateLayerMatchRust(t *testing.T) {
	state := IdeContextState{}
	result := state.HandleCommand(" on ", true, true, "")
	if !result.Error || result.Message != "Usage: /ide [on|off|status]" || state.Enabled {
		t.Fatalf("spaced arg result = %#v state=%#v", result, state)
	}
}

func TestIdeContextPreservesHintsMatchRust(t *testing.T) {
	state := IdeContextState{}
	result := state.HandleCommand("on", false, false, "  no editor  ")
	if result.Hint != "  no editor  " || result.Error {
		t.Fatalf("enable failure hint = %#v", result)
	}
	state.Enabled = true
	skipped, ok := state.MarkPromptFetchSkipped("  skipped  ")
	if !ok || skipped.Hint != "  skipped  " {
		t.Fatalf("skipped hint = %#v ok=%v", skipped, ok)
	}
}
