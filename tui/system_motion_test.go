package tui

import "testing"

func TestEffectiveAnimationsHonorsSystemReducedMotion(t *testing.T) {
	original := detectSystemMotion
	t.Cleanup(func() {
		detectSystemMotion = original
		resetSystemMotionForTest()
	})

	detectSystemMotion = func() (MotionMode, bool) { return MotionReduced, true }
	resetSystemMotionForTest()
	if SystemMotionMode() != MotionReduced {
		t.Fatalf("SystemMotionMode() = %v", SystemMotionMode())
	}
	if EffectiveAnimations(true) {
		t.Fatal("configured animations should be suppressed under reduced motion")
	}
	if EffectiveAnimations(false) {
		t.Fatal("disabled animations stay disabled")
	}

	detectSystemMotion = func() (MotionMode, bool) { return MotionAnimated, true }
	resetSystemMotionForTest()
	if !EffectiveAnimations(true) {
		t.Fatal("configured animations should stay enabled when the system allows motion")
	}

	// An unavailable preference preserves the configured behavior.
	detectSystemMotion = func() (MotionMode, bool) { return MotionAnimated, false }
	resetSystemMotionForTest()
	if SystemMotionMode() != MotionAnimated || !EffectiveAnimations(true) {
		t.Fatalf("unavailable preference should preserve configuration: mode=%v", SystemMotionMode())
	}
}

func TestSystemMotionModeIsReadOnce(t *testing.T) {
	original := detectSystemMotion
	t.Cleanup(func() {
		detectSystemMotion = original
		resetSystemMotionForTest()
	})

	calls := 0
	detectSystemMotion = func() (MotionMode, bool) {
		calls++
		return MotionReduced, true
	}
	resetSystemMotionForTest()
	InitializeSystemMotion()
	_ = SystemMotionMode()
	_ = EffectiveAnimations(true)
	if calls != 1 {
		t.Fatalf("detection calls = %d, want 1", calls)
	}
}

// Rust #44666 reads NSWorkspace on macOS and the XDG portal on Linux. The Go
// probes parse those exact outputs; unknown values preserve the configured
// animations.
func TestSystemMotionPlatformProbeParsingLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		output string
		want   MotionMode
		ok     bool
	}{
		{"macOS reduce", "1\n", MotionReduced, true},
		{"macOS animated", "0\n", MotionAnimated, true},
		{"macOS missing", "", MotionAnimated, false},
		{"portal reduce", "(<<uint32 1>>,)\n", MotionReduced, true},
		{"portal animated", "(<<uint32 0>>,)\n", MotionAnimated, true},
		{"portal unknown", "(<<uint32 2>>,)\n", MotionAnimated, false},
		{"portal unparsable", "(error)", MotionAnimated, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var got MotionMode
			var ok bool
			if testCase.name[:6] == "portal" {
				got, ok = parsePortalReducedMotion(testCase.output)
			} else {
				got, ok = parseMacOSReduceMotion(testCase.output)
			}
			if got != testCase.want || ok != testCase.ok {
				t.Fatalf("parse(%q) = (%v, %v), want (%v, %v)", testCase.output, got, ok, testCase.want, testCase.ok)
			}
		})
	}
}
