package app

import (
	"strings"
	"testing"

	tuiinternal "codex_go/tui/tui"
)

// Mirrors Rust tui/src/app/right_click_paste_tests.rs
// policy_preserves_explicit_modes_and_terminal_ownership.
func TestRightClickPastePolicyPreservesExplicitModesAndTerminalOwnership(t *testing.T) {
	cases := []struct {
		platformDefault bool
		ssh             bool
		wsl             bool
		vscode          tuiinternal.VscodeDetection
		expected        [3]bool
	}{
		{true, false, false, tuiinternal.VscodeDetectionOther, [3]bool{false, true, true}},
		{false, false, false, tuiinternal.VscodeDetectionOther, [3]bool{false, true, false}},
		{true, true, false, tuiinternal.VscodeDetectionOther, [3]bool{false, false, false}},
		{true, false, false, tuiinternal.VscodeDetectionVsCode, [3]bool{false, false, false}},
		{true, false, true, tuiinternal.VscodeDetectionUnknown, [3]bool{false, true, false}},
		{true, false, true, tuiinternal.VscodeDetectionOther, [3]bool{false, true, true}},
	}
	modes := [3]RightClickPasteMode{RightClickPasteOff, RightClickPasteOn, RightClickPasteAuto}
	for _, tc := range cases {
		env := PasteEnvironment{
			PlatformDefault: tc.platformDefault,
			SSH:             tc.ssh,
			WSL:             tc.wsl,
			Vscode:          tc.vscode,
		}
		for index, mode := range modes {
			if got := env.Allows(mode); got != tc.expected[index] {
				t.Fatalf("allows(%s) with env %#v = %v, want %v", mode, env, got, tc.expected[index])
			}
		}
	}
}

func TestDetectPasteEnvironmentMirrorsRustDetection(t *testing.T) {
	env := DetectPasteEnvironment("windows", map[string]string{"SSH_TTY": " /dev/pts/1 "}, false, tuiinternal.VscodeDetectionOther)
	if !env.PlatformDefault || !env.SSH || env.WSL {
		t.Fatalf("windows ssh env = %#v", env)
	}
	if env.Allows(RightClickPasteAuto) {
		t.Fatal("an SSH session must keep right-click paste off")
	}

	if got := DetectPasteEnvironment("darwin", nil, false, tuiinternal.VscodeDetectionOther); got.PlatformDefault {
		t.Fatalf("macOS platform default = %v, want false", got.PlatformDefault)
	}
	if got := DetectPasteEnvironment("linux", map[string]string{"SSH_CONNECTION": "1.2.3.4"}, false, tuiinternal.VscodeDetectionOther); !got.SSH {
		t.Fatalf("SSH_CONNECTION env = %#v", got)
	}
}

func TestParseRightClickPasteMode(t *testing.T) {
	cases := map[string]RightClickPasteMode{
		"":      RightClickPasteAuto,
		"auto":  RightClickPasteAuto,
		"on":    RightClickPasteOn,
		" OFF ": RightClickPasteOff,
		"bogus": RightClickPasteAuto,
	}
	for raw, want := range cases {
		if got := ParseRightClickPasteMode(raw); got != want {
			t.Fatalf("ParseRightClickPasteMode(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestRightClickPasteTargetForGatesOwnershipAndComposerEligibility(t *testing.T) {
	env := PasteEnvironment{PlatformDefault: true, Vscode: tuiinternal.VscodeDetectionOther}
	base := RightClickPasteTargetInput{
		OwnedScreen:      true,
		ComposerEditable: true,
		Thread:           "t1",
		Draft:            "draft",
		Cursor:           3,
	}
	target, ok := RightClickPasteTargetFor(base, env, RightClickPasteAuto)
	if !ok || target != (RightClickPasteTarget{Thread: "t1", Draft: "draft", Cursor: 3}) {
		t.Fatalf("eligible target = %#v ok=%v", target, ok)
	}

	blocked := []struct {
		name   string
		mutate func(*RightClickPasteTargetInput)
		mode   RightClickPasteMode
	}{
		{"inline screen", func(in *RightClickPasteTargetInput) { in.OwnedScreen = false }, RightClickPasteAuto},
		{"overlay", func(in *RightClickPasteTargetInput) { in.OverlayActive = true }, RightClickPasteAuto},
		{"selection", func(in *RightClickPasteTargetInput) { in.HasSelection = true }, RightClickPasteAuto},
		{"search", func(in *RightClickPasteTargetInput) { in.SearchActive = true }, RightClickPasteAuto},
		{"blocked composer", func(in *RightClickPasteTargetInput) { in.ComposerEditable = false }, RightClickPasteAuto},
		{"mode off", func(in *RightClickPasteTargetInput) {}, RightClickPasteOff},
	}
	for _, tc := range blocked {
		in := base
		tc.mutate(&in)
		if _, ok := RightClickPasteTargetFor(in, env, tc.mode); ok {
			t.Fatalf("%s should not be pasteable", tc.name)
		}
	}

	// An SSH session and a recognized VS Code terminal disable every mode.
	for _, env := range []PasteEnvironment{
		{PlatformDefault: true, SSH: true, Vscode: tuiinternal.VscodeDetectionOther},
		{PlatformDefault: true, Vscode: tuiinternal.VscodeDetectionVsCode},
	} {
		for _, mode := range []RightClickPasteMode{RightClickPasteAuto, RightClickPasteOn, RightClickPasteOff} {
			if _, ok := RightClickPasteTargetFor(base, env, mode); ok {
				t.Fatalf("env %#v mode %s should not be pasteable", env, mode)
			}
		}
	}
}

func TestShouldStartRightClickPaste(t *testing.T) {
	if !ShouldStartRightClickPaste(true, true, false, false) {
		t.Fatal("an unmodified right click with an idle worker should start a read")
	}
	cases := [][4]bool{
		{false, true, false, false},
		{true, false, false, false},
		{true, true, true, false},
		{true, true, false, true},
	}
	for _, tc := range cases {
		if ShouldStartRightClickPaste(tc[0], tc[1], tc[2], tc[3]) {
			t.Fatalf("guard %v should not start a read", tc)
		}
	}
}

func TestRightClickPasteInvalidationMatchesRustEventRule(t *testing.T) {
	keep := []RightClickPasteEvent{
		{Kind: RightClickPasteEventDraw},
		{Kind: RightClickPasteEventResize},
		{Kind: RightClickPasteEventFocusGained},
		{Kind: RightClickPasteEventMouse, MouseKind: RightClickPasteMouseMoved},
		{Kind: RightClickPasteEventMouse, MouseKind: RightClickPasteMouseUpRight},
		{Kind: RightClickPasteEventMouse, MouseKind: RightClickPasteMouseDownRight, ModifiersEmpty: true},
	}
	for _, event := range keep {
		if !RightClickPasteKeepsPending(event) {
			t.Fatalf("event %#v should keep the pending paste", event)
		}
	}
	drop := []RightClickPasteEvent{
		{Kind: RightClickPasteEventKey},
		{Kind: RightClickPasteEventPaste},
		{Kind: RightClickPasteEventFocusLost},
		{Kind: RightClickPasteEventResume},
		{Kind: RightClickPasteEventMouse, MouseKind: RightClickPasteMouseOther},
		{Kind: RightClickPasteEventMouse, MouseKind: RightClickPasteMouseDownRight},
	}
	for _, event := range drop {
		if RightClickPasteKeepsPending(event) {
			t.Fatalf("event %#v should drop the pending paste", event)
		}
	}
}

func TestRightClickPasteDeliveryRequiresMatchingTarget(t *testing.T) {
	target := RightClickPasteTarget{Thread: "t1", Draft: "draft", Cursor: 4}
	other := RightClickPasteTarget{Thread: "t2", Draft: "draft", Cursor: 4}

	if got := RightClickPasteDeliveryFor(target, true, target, true, "hello", ""); got.Kind != RightClickPasteDeliveryPaste || got.Text != "hello" {
		t.Fatalf("delivery = %#v", got)
	}
	if got := RightClickPasteDeliveryFor(target, true, other, true, "hello", ""); got.Kind != RightClickPasteDeliveryNone {
		t.Fatalf("stale thread delivery = %#v", got)
	}
	if got := RightClickPasteDeliveryFor(target, true, target, false, "hello", ""); got.Kind != RightClickPasteDeliveryNone {
		t.Fatalf("ineligible current target delivery = %#v", got)
	}
	if got := RightClickPasteDeliveryFor(target, false, target, true, "hello", ""); got.Kind != RightClickPasteDeliveryNone {
		t.Fatalf("no pending delivery = %#v", got)
	}
	if got := RightClickPasteDeliveryFor(target, true, target, true, "", ""); got.Kind != RightClickPasteDeliveryNone {
		t.Fatalf("empty clipboard delivery = %#v", got)
	}
	if got := RightClickPasteDeliveryFor(target, true, target, true, "", "clipboard read timed out"); got.Kind != RightClickPasteDeliveryError || got.Error != "clipboard read timed out" {
		t.Fatalf("error delivery = %#v", got)
	}
}

func TestValidateRightClickPasteText(t *testing.T) {
	if text, err := ValidateRightClickPasteText("hello", false); text != "hello" || err != "" {
		t.Fatalf("validate = %q %q", text, err)
	}
	if _, err := ValidateRightClickPasteText("hello", true); err != "clipboard read timed out" {
		t.Fatalf("expired read error = %q", err)
	}
	oversized := strings.Repeat("x", RightClickPasteMaxTextChars+1)
	if _, err := ValidateRightClickPasteText(oversized, false); err != "clipboard text exceeds the message size limit" {
		t.Fatalf("oversized read error = %q", err)
	}
	// The limit counts characters, so a rune-counted value exactly at the bound
	// is accepted even when its UTF-8 encoding is longer.
	atLimit := strings.Repeat("é", RightClickPasteMaxTextChars)
	if _, err := ValidateRightClickPasteText(atLimit, false); err != "" {
		t.Fatalf("at-limit read error = %q", err)
	}
}
