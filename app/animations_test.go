package app

import (
	"testing"

	codextui "codex_go/tui"
)

func TestInteractiveAnimationsEnabledHonorsConfigAndSystemMotion(t *testing.T) {
	disabled := interactiveAnimationsEnabled(map[string]any{"tui": map[string]any{"animations": false}})
	if disabled == nil || *disabled {
		t.Fatalf("animations=false should be disabled, got %#v", disabled)
	}

	effective := codextui.EffectiveAnimations(true)
	for name, values := range map[string]map[string]any{
		"default":          {},
		"explicit enabled": {"tui": map[string]any{"animations": true}},
	} {
		got := interactiveAnimationsEnabled(values)
		if got == nil || *got != effective {
			t.Fatalf("%s animations = %#v, want the host motion preference %v", name, got, effective)
		}
	}
}
