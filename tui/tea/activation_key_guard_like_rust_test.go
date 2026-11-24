package tea

import (
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/tui/keymsg"
)

var (
	guardEnter = keymsg.KeyMsg{Type: keymsg.KeyEnter, Code: '\r'}
	guardTab   = keymsg.KeyMsg{Type: keymsg.KeyTab, Code: '\t'}
	guardEsc   = keymsg.KeyMsg{Type: keymsg.KeyEsc, Code: 0x1b}
)

// Rust activation_key_guard_tests
// suppresses_activation_until_release_and_bounds_legacy_presses.
func TestActivationKeyGuardBoundsLegacyPressesLikeRust(t *testing.T) {
	start := time.Now()
	guard := newActivationKeyGuard(guardEnter, start)

	if !guard.consume(guardEnter, false, start.Add(900*time.Millisecond)) {
		t.Fatal("press inside the initial window was not swallowed")
	}
	if !guard.consume(guardEnter, false, start.Add(1300*time.Millisecond)) {
		t.Fatal("press inside the extended window was not swallowed")
	}
	if guard.consume(guardEnter, false, start.Add(2301*time.Millisecond)) {
		t.Fatal("press after the extended window was swallowed")
	}
	if guard.isActive() {
		t.Fatal("guard stayed active after the window lapsed")
	}

	released := newActivationKeyGuard(guardEnter, start)
	if !released.consume(guardEnter, true, start) {
		t.Fatal("release of the held key was not swallowed")
	}
	if released.consume(guardEnter, false, start) {
		t.Fatal("press after the release was still swallowed")
	}
}

// Rust activation_key_guard_tests: a different key ends the guard, and Shift plus
// Tab counts as the same physical key as Tab.
func TestActivationKeyGuardStopsOnAnotherKeyLikeRust(t *testing.T) {
	start := time.Now()
	guard := newActivationKeyGuard(guardEsc, start)
	if guard.consume(guardTab, false, start) {
		t.Fatal("a different key was swallowed")
	}
	if guard.consume(guardEsc, false, start) {
		t.Fatal("guard stayed active after another key")
	}

	shiftTab := keymsg.KeyMsg{Type: keymsg.KeyShiftTab, Code: '\t'}
	guard = newActivationKeyGuard(shiftTab, start)
	if !guard.consume(guardTab, false, start) {
		t.Fatal("Tab did not match Shift+Tab")
	}
}

// Rust #61988: the Enter that opens the provider picker must not also choose its
// highlighted row when the key is held, and a release frees the key again.
func TestActivationKeyGuardSwallowsHeldEnterAcrossPickerLikeRust(t *testing.T) {
	writes := []SettingsEdit{}
	model := statuslineProviderPickerModel(t, nil, statuslineProviderDiscovered(t,
		StatuslineProviderCandidate{Server: "fixture", Tool: "zeta", Title: "Zeta"}), &writes)
	model.openStatusLineSetup()

	model.Update(guardEnter)
	if model.statuslinePicker == nil || model.modal == nil || model.modal.kind != ModalKindStatuslineProvider {
		t.Fatal("Enter did not open the provider picker")
	}
	model.Update(guardEnter)
	if model.statuslinePicker == nil {
		t.Fatal("held Enter chose a provider")
	}
	if len(writes) != 0 {
		t.Fatalf("held Enter wrote settings: %#v", writes)
	}

	model.Update(bubbletea.KeyReleaseMsg{Code: '\r'})
	_, choice := model.Update(guardEnter)
	if model.statuslinePicker != nil {
		t.Fatal("Enter after the release did not choose a provider")
	}
	if choice == nil {
		t.Fatal("choosing a provider did not save the selection")
	}
	runProviderCmd(model, choice)
	if len(writes) != 1 || writes[0].KeyPath != statusLineProviderConfigKey {
		t.Fatalf("settings writes = %#v", writes)
	}
}

// Rust #63146 activation_key_guard_tests: a replay that outlasts the initial
// press window rearms the guard so a held selection key cannot act on an
// approval dialog the destination opens.
func TestActivationKeyGuardRearmExtendsReplayWindowLikeRust(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	guard := newActivationKeyGuard(guardEnter, start)
	guard.rearm(start.Add(900 * time.Millisecond))
	if !guard.consume(guardEnter, false, start.Add(1_500*time.Millisecond)) {
		t.Fatal("a press inside the rearmed window was not swallowed")
	}

	// Rearming never shortens an existing block.
	longer := newActivationKeyGuard(guardEnter, start)
	longer.rearm(start.Add(2 * time.Second))
	if until := longer.blockUntil; !until.Equal(start.Add(3 * time.Second)) {
		t.Fatalf("blockUntil = %v, want the later deadline", until.Sub(start))
	}

	// A released guard stays inactive.
	released := newActivationKeyGuard(guardEnter, start)
	if !released.consume(guardEnter, true, start) {
		t.Fatal("release inside the window was not consumed")
	}
	released.rearm(start.Add(500 * time.Millisecond))
	if released.consume(guardEnter, false, start.Add(500*time.Millisecond)) {
		t.Fatal("rearm reactivated a released guard")
	}
}
