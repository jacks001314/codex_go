package tea

import (
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func blossomTestModel(t *testing.T, animations bool, effects EffectsSettings, width, height int) (*Model, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	model := NewModel(codextui.NewState(&codextui.Options{Model: "gpt-5"}), Options{
		Width:             width,
		Height:            height,
		ShowSessionHeader: true,
		AnimationsEnabled: &animations,
		Effects:           &effects,
	})
	model.now = func() time.Time { return now }
	return model, &now
}

func countBlossomDots(view string) int {
	plain := utils.StripANSI(view)
	count := 0
	for _, r := range plain {
		if r >= 0x2800 && r <= 0x28ff {
			count++
		}
	}
	return count
}

// Rust #48513: a fresh fullscreen conversation shows the settled blossom in
// unused space.
func TestFreshFullscreenShowsSettledBlossomLikeRust(t *testing.T) {
	model, _ := blossomTestModel(t, true, DefaultEffectsSettings(), 100, 40)
	if dots := countBlossomDots(model.View()); dots == 0 {
		t.Fatalf("fresh fullscreen omitted the blossom:\n%s", utils.StripANSI(model.View()))
	}
	if rect := model.welcomeBlossom.rect; rect.W <= 0 || rect.H < blossomMinRows {
		t.Fatalf("blossom rect = %#v", rect)
	}
}

// Rust #48513: welcome opt-outs, reduced motion, typing and insufficient space
// hide the decoration.
func TestWelcomeBlossomEligibilityLikeRust(t *testing.T) {
	effectsOff := DefaultEffectsSettings()
	effectsOff.Welcome = false
	cases := []struct {
		name       string
		animations bool
		effects    EffectsSettings
		width      int
		height     int
		mutate     func(*Model)
	}{
		{name: "welcome effect off", animations: true, effects: effectsOff, width: 100, height: 40},
		{name: "animations off", animations: false, effects: DefaultEffectsSettings(), width: 100, height: 40},
		{name: "narrow screen", animations: true, effects: DefaultEffectsSettings(), width: 40, height: 12},
		{name: "typing", animations: true, effects: DefaultEffectsSettings(), width: 100, height: 40, mutate: func(model *Model) {
			typeText(t, model, "x")
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			model, _ := blossomTestModel(t, testCase.animations, testCase.effects, testCase.width, testCase.height)
			if testCase.mutate != nil {
				testCase.mutate(model)
			}
			if dots := countBlossomDots(model.View()); dots != 0 {
				t.Fatalf("ineligible view rendered %d blossom cells", dots)
			}
		})
	}
}

// Rust #48513: clicking the drawn blossom replays one spin and returns to the
// settled dim pose.
func TestWelcomeBlossomClickReplaysLikeRust(t *testing.T) {
	model, now := blossomTestModel(t, true, DefaultEffectsSettings(), 100, 40)
	_ = model.View()
	rect := model.welcomeBlossom.rect
	if rect.W <= 0 {
		t.Fatalf("no blossom stage: %#v", rect)
	}
	model.Update(bubbletea.MouseMsg{
		X:      rect.X + rect.W/2,
		Y:      rect.Y + rect.H/2,
		Action: bubbletea.MouseActionPress,
		Button: bubbletea.MouseButtonLeft,
	})
	if model.welcomeBlossom.replayStartedAt.IsZero() {
		t.Fatal("clicking the blossom did not start a replay")
	}
	*now = (*now).Add(200 * time.Millisecond)
	if opacity, replaying := model.blossomOpacity(*now); !replaying || opacity <= blossomStaticOpacity {
		t.Fatalf("replay opacity = %v replaying=%v", opacity, replaying)
	}
	*now = (*now).Add(8 * time.Second)
	if opacity, replaying := model.blossomOpacity(*now); replaying || opacity != blossomStaticOpacity {
		t.Fatalf("settled opacity = %v replaying=%v", opacity, replaying)
	}

	outside := *now
	model.welcomeBlossom.replayStartedAt = outside.Add(-20 * time.Second)
	model.blossomOpacity(outside)
	model.Update(bubbletea.MouseMsg{
		X:      rect.X + rect.W + 2,
		Y:      rect.Y,
		Action: bubbletea.MouseActionPress,
		Button: bubbletea.MouseButtonLeft,
	})
	if !model.welcomeBlossom.replayStartedAt.IsZero() {
		t.Fatal("click outside the blossom started a replay")
	}
}
