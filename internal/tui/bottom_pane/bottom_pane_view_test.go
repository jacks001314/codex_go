package bottompane

import (
	"testing"
	"time"
)

func TestBottomPaneViewStateCompletionAndCancellationMatchRustDefaults(t *testing.T) {
	state := BottomPaneViewState{Height: 3, Active: true}
	if !state.Visible() || state.IsComplete() || state.Completion() != nil {
		t.Fatalf("initial state = %#v", state)
	}
	if got := state.OnCtrlC(); got != CancellationHandled {
		t.Fatalf("OnCtrlC = %s", got)
	}
	if !state.IsComplete() {
		t.Fatal("ctrl-c should complete the view")
	}
	reason := state.Completion()
	if reason == nil || *reason != ViewCompletionCancelled {
		t.Fatalf("completion = %#v", reason)
	}
	if !state.PreDrawTick() || state.Active {
		t.Fatalf("pre draw should deactivate completed active view: %#v", state)
	}
}

func TestBottomPaneViewStateMetadataPasteAndFrameHooks(t *testing.T) {
	state := BottomPaneViewStateWithSelection(5, 2)
	state.ViewID = "hooks"
	state.ActiveTab = "events"
	state.DismissAfterChildAcceptFlag = true
	state.PreferEscHandle = true
	state.InterruptsTurn = true
	state.TerminalTitleRequiresAction = true
	state.InPasteBurst = true
	state.NextFrameDelayValue = 50 * time.Millisecond

	if selected, ok := state.SelectedIndexValue(); !ok || selected != 2 {
		t.Fatalf("selected = %d ok=%v", selected, ok)
	}
	if state.StableViewID() != "hooks" || state.ActiveTabID() != "events" {
		t.Fatalf("metadata view=%q tab=%q", state.StableViewID(), state.ActiveTabID())
	}
	if !state.DismissAfterChildAccept() {
		t.Fatal("dismiss-after-child flag should be set")
	}
	state.ClearDismissAfterChildAccept()
	if state.DismissAfterChildAccept() {
		t.Fatal("dismiss-after-child flag should clear")
	}
	if !state.PreferEscToHandleKeyEvent() || !state.WillInterruptTurnOnKeyEvent() {
		t.Fatalf("key flags not reflected: %#v", state)
	}
	if !state.HandlePaste("pasted") || state.ConsumedPaste != "pasted" {
		t.Fatalf("paste not consumed: %#v", state)
	}
	if !state.IsInPasteBurst() || !state.FlushPasteBurstIfDue() || state.IsInPasteBurst() {
		t.Fatalf("paste burst flush failed: %#v", state)
	}
	if delay, ok := state.NextFrameDelay(); !ok || delay != 50*time.Millisecond {
		t.Fatalf("next frame delay = %v ok=%v", delay, ok)
	}
	if !state.TerminalTitleRequiresActionNow() {
		t.Fatal("active unresolved view should require terminal title action")
	}
}

func TestBottomPaneViewStateDismissAppServerRequest(t *testing.T) {
	state := BottomPaneViewState{Active: true, Height: 1}
	if !state.DismissAppServerRequest(ResolvedAppServerRequest{ServerName: "codex_apps", RequestID: "request-1"}) {
		t.Fatal("first resolved request should dismiss")
	}
	if state.DismissedAppServerRequestKey != "codex_apps/request-1" || !state.Complete {
		t.Fatalf("dismissed state = %#v", state)
	}
	if state.DismissAppServerRequest(ResolvedAppServerRequest{ServerName: "other", RequestID: "request-2"}) {
		t.Fatal("non-matching later request should not dismiss")
	}
	reason := state.Completion()
	if reason == nil || *reason != ViewCompletionCancelled {
		t.Fatalf("dismiss completion = %#v", reason)
	}
}
