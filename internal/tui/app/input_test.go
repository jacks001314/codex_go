package app

import (
	"errors"
	"testing"
)

func TestExternalEditorHelpersMatchRust(t *testing.T) {
	if ExternalEditorHint != "Save and close external editor to continue." {
		t.Fatalf("ExternalEditorHint = %q", ExternalEditorHint)
	}
	if MissingExternalEditorMessage != "Cannot open external editor: set $VISUAL or $EDITOR before starting Codex." {
		t.Fatalf("MissingExternalEditorMessage = %q", MissingExternalEditorMessage)
	}
	if got := CleanExternalEditorText("draft \n\t"); got != "draft" {
		t.Fatalf("CleanExternalEditorText() = %q", got)
	}
	if got := ExternalEditorErrorMessage(errors.New("boom")); got != "Failed to open editor: boom" {
		t.Fatalf("ExternalEditorErrorMessage() = %q", got)
	}
	if !CanRequestExternalEditorLaunch(false, true, ExternalEditorClosed) {
		t.Fatal("closed editor should be launchable")
	}
	for _, tc := range []struct {
		overlay bool
		can     bool
		state   ExternalEditorState
	}{
		{overlay: true, can: true, state: ExternalEditorClosed},
		{overlay: false, can: false, state: ExternalEditorClosed},
		{overlay: false, can: true, state: ExternalEditorRequested},
		{overlay: false, can: true, state: ExternalEditorActive},
	} {
		if CanRequestExternalEditorLaunch(tc.overlay, tc.can, tc.state) {
			t.Fatalf("CanRequestExternalEditorLaunch(%#v) = true, want false", tc)
		}
	}
}

func TestInputShortcutGatingMatchesRust(t *testing.T) {
	if !AppKeymapShortcutsAvailable(false, false) {
		t.Fatal("shortcuts should be available with no overlay/modal")
	}
	if AppKeymapShortcutsAvailable(true, false) || AppKeymapShortcutsAvailable(false, true) {
		t.Fatal("shortcuts should be disabled for overlay or modal")
	}
	if !AllowAgentWordMotionFallback(false, "") {
		t.Fatal("word-motion fallback should be allowed without enhanced keys and empty composer")
	}
	if AllowAgentWordMotionFallback(true, "") || AllowAgentWordMotionFallback(false, "draft") {
		t.Fatal("word-motion fallback should require no enhanced keys and empty composer")
	}
	if !AgentSwitchShortcutsAvailable(false, false, "") {
		t.Fatal("agent switch should be available on empty composer")
	}
	if AgentSwitchShortcutsAvailable(false, false, "draft") {
		t.Fatal("agent switch should not steal word motion from non-empty composer")
	}
}

func TestBacktrackEscGatingMatchesRust(t *testing.T) {
	if !ShouldHandleBacktrackEsc(false, true, true, false) {
		t.Fatal("main normal empty composer Esc should handle backtrack")
	}
	if ShouldHandleBacktrackEsc(true, true, true, false) || ShouldHandleBacktrackEsc(false, false, true, false) || ShouldHandleBacktrackEsc(false, true, false, false) || ShouldHandleBacktrackEsc(false, true, true, true) {
		t.Fatal("main backtrack Esc accepted in invalid state")
	}
	if !ShouldRejectSideBacktrackEsc(true, true, true, false) {
		t.Fatal("side normal empty composer Esc should reject with message")
	}
	if ShouldRejectSideBacktrackEsc(false, true, true, false) {
		t.Fatal("side reject should require side conversation")
	}
	if SideEditPreviousUnavailableMessage != "Editing previous prompts is unavailable in side conversations." {
		t.Fatalf("SideEditPreviousUnavailableMessage = %q", SideEditPreviousUnavailableMessage)
	}
	if !ShouldConfirmBacktrackFromMain(true, 0, true) {
		t.Fatal("primed selected empty composer should confirm")
	}
	if ShouldConfirmBacktrackFromMain(true, BacktrackNoSelection, true) || ShouldConfirmBacktrackFromMain(false, 0, true) || ShouldConfirmBacktrackFromMain(true, 0, false) {
		t.Fatal("backtrack confirm accepted in invalid state")
	}
	if !ShouldResetPrimedBacktrackOnKeyPress(true, "a") || ShouldResetPrimedBacktrackOnKeyPress(true, "esc") || !ShouldResetPrimedBacktrackOnKeyPress(true, " esc ") || ShouldResetPrimedBacktrackOnKeyPress(false, "a") {
		t.Fatal("primed backtrack reset mismatch")
	}
}
