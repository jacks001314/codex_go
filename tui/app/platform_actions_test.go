package app

import (
	"testing"
	"time"
)

func TestSideReturnShortcutMatchesCtrlCAndCtrlDMatchRust(t *testing.T) {
	for _, key := range []string{"c", "C", "d", "D", "ctrl-c", "ctrl-d"} {
		if !SideReturnShortcutMatches(key, true, true) {
			t.Fatalf("expected side return shortcut for %q", key)
		}
	}
	for _, tc := range []struct {
		key     string
		control bool
		press   bool
	}{
		{key: "esc", control: false, press: true},
		{key: "c", control: false, press: true},
		{key: " c ", control: true, press: true},
		{key: "d", control: true, press: false},
	} {
		if SideReturnShortcutMatches(tc.key, tc.control, tc.press) {
			t.Fatalf("unexpected side return shortcut for %#v", tc)
		}
	}
}

func TestWindowsSandboxStateMatchesRustCore(t *testing.T) {
	var state WindowsSandboxState
	started := time.Unix(123, 0)
	state.MarkSetupStarted(started)
	if !state.SetupStarted || !state.SetupStartedAt.Equal(started) {
		t.Fatalf("setup start state = %#v", state)
	}
	state.SkipWorldWritableScanOnce = true
	if !state.ConsumeSkipWorldWritableScan() {
		t.Fatal("expected first skip consume to return true")
	}
	if state.ConsumeSkipWorldWritableScan() {
		t.Fatal("expected one-shot skip to be consumed")
	}
}
