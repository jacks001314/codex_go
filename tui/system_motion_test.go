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
