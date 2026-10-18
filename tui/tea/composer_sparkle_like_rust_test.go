package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

func sparkleTestModel(t *testing.T, modelID string, animations bool, effects EffectsSettings) (*Model, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	model := NewModel(codextui.NewState(&codextui.Options{Model: modelID}), Options{
		Width:             80,
		Height:            24,
		AnimationsEnabled: &animations,
		Effects:           &effects,
	})
	model.now = func() time.Time { return now }
	return model, &now
}

func countSparkleDots(view string) int {
	view = utils.StripANSI(view)
	count := 0
	for _, r := range view {
		if strings.ContainsRune(string(sparkleDots), r) {
			count++
		}
	}
	return count
}

// Rust #46096: a fresh Astra task decorates blank composer cells with the
// one-time starfield while keeping the placeholder visible.
func TestFreshAstraTaskShowsComposerStarfieldLikeRust(t *testing.T) {
	effects := DefaultEffectsSettings()
	model, _ := sparkleTestModel(t, "gpt-6-astra", true, effects)
	if model.sparkle.phase != sparkleWaiting {
		t.Fatalf("initial sparkle phase = %v, want waiting", model.sparkle.phase)
	}
	view := model.View()
	if countSparkleDots(view) == 0 {
		t.Fatalf("fresh Astra composer has no starfield:\n%s", utils.StripANSI(view))
	}
	if model.sparkle.phase != sparkleVisible {
		t.Fatalf("sparkle phase after render = %v, want visible", model.sparkle.phase)
	}
	plain := utils.StripANSI(view)
	if !strings.Contains(plain, "Ask gcode") {
		t.Fatalf("starfield replaced the placeholder:\n%s", plain)
	}
}

// Rust #46096: the starfield is subordinate to the animations and starfield
// effects, and only Astra models are eligible.
func TestComposerStarfieldHonorsEffectsAndModelLikeRust(t *testing.T) {
	effectsOff := DefaultEffectsSettings()
	effectsOff.Starfield = false
	for _, testCase := range []struct {
		name       string
		model      string
		animations bool
		effects    EffectsSettings
	}{
		{name: "starfield off", model: "gpt-6-astra", animations: true, effects: effectsOff},
		{name: "animations off", model: "gpt-6-astra", animations: false, effects: DefaultEffectsSettings()},
		{name: "non-Astra model", model: "gpt-5", animations: true, effects: DefaultEffectsSettings()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			model, _ := sparkleTestModel(t, testCase.model, testCase.animations, testCase.effects)
			if dots := countSparkleDots(model.View()); dots != 0 {
				t.Fatalf("ineligible composer rendered %d stars", dots)
			}
		})
	}
}

// Rust #46096: ordinary draft input ends the opportunity; a slash command can
// still select Astra and only hides the field temporarily.
func TestComposerStarfieldDismissalRulesLikeRust(t *testing.T) {
	model, _ := sparkleTestModel(t, "gpt-6-astra", true, DefaultEffectsSettings())
	typeText(t, model, "x")
	if model.sparkle.phase != sparkleFinished {
		t.Fatalf("ordinary input phase = %v, want finished", model.sparkle.phase)
	}
	if dots := countSparkleDots(model.View()); dots != 0 {
		t.Fatalf("dismissed composer rendered %d stars", dots)
	}

	slash, _ := sparkleTestModel(t, "gpt-6-astra", true, DefaultEffectsSettings())
	typeText(t, slash, "/")
	_ = slash.View()
	if !slash.sparkleActive() {
		t.Fatalf("slash draft phase = %v, want retained opportunity", slash.sparkle.phase)
	}
	if dots := countSparkleDots(slash.View()); dots != 0 {
		t.Fatalf("slash draft rendered %d stars", dots)
	}
	typeText(t, slash, "m")
	if !slash.sparkleActive() {
		t.Fatalf("slash command body phase = %v, want retained opportunity", slash.sparkle.phase)
	}
	slash.resetComposer()
	if dots := countSparkleDots(slash.View()); dots == 0 {
		t.Fatal("clearing the slash draft did not restore the starfield")
	}
}

// Rust #46096: the field fades out and is gone after the 15-second deadline.
func TestComposerStarfieldExpiresLikeRust(t *testing.T) {
	model, now := sparkleTestModel(t, "gpt-6-astra", true, DefaultEffectsSettings())
	_ = model.View()
	*now = (*now).Add(16 * time.Second)
	if dots := countSparkleDots(model.View()); dots != 0 {
		t.Fatalf("expired composer rendered %d stars", dots)
	}
	if model.sparkle.phase != sparkleFinished {
		t.Fatalf("expired phase = %v, want finished", model.sparkle.phase)
	}
	model.Update(StatusMsg{Status: "idle"})
	if dots := countSparkleDots(model.View()); dots != 0 {
		t.Fatalf("expired starfield restarted after an update: %d stars", dots)
	}
}

// Rust #46096: selecting Astra from the model picker on a fresh task arms the
// same flourish.
func TestComposerStarfieldArmsWhenModelBecomesAstraLikeRust(t *testing.T) {
	model, _ := sparkleTestModel(t, "gpt-5", true, DefaultEffectsSettings())
	model.State.Model = "gpt-6-astra"
	model.Update(StatusMsg{Status: "idle"})
	if model.sparkle.phase != sparkleWaiting {
		t.Fatalf("phase after Astra selection = %v, want waiting", model.sparkle.phase)
	}
	if dots := countSparkleDots(model.View()); dots == 0 {
		t.Fatal("Astra selection did not render the starfield")
	}
}

// The bootstrap thread arrives through thread.started after local settings
// resolve the model; the fresh-task opportunity must survive that transition.
func TestComposerStarfieldSurvivesBootstrapThreadStart(t *testing.T) {
	model, _ := sparkleTestModel(t, "", true, DefaultEffectsSettings())
	model.State.Model = "gpt-6-astra"
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "thread.started", ThreadID: "thread-astra"}})
	if model.sparkle.phase != sparkleWaiting {
		t.Fatalf("phase after bootstrap = %v, want waiting", model.sparkle.phase)
	}
	if dots := countSparkleDots(model.View()); dots == 0 {
		t.Fatal("bootstrap Astra thread omitted the starfield")
	}
}

// Rust #44879: losing focus stops animation frames, but the idle deadline
// keeps running on wall-clock time; regaining focus after it clears the field.
func TestComposerStarfieldPausesFramesWhileUnfocusedAndExpires(t *testing.T) {
	model, now := sparkleTestModel(t, "gpt-6-astra", true, DefaultEffectsSettings())
	_ = model.View()
	if model.sparkle.phase != sparkleVisible {
		t.Fatalf("phase before blur = %v, want visible", model.sparkle.phase)
	}
	updated, _ := model.Update(bubbletea.BlurMsg{})
	model = updated.(*Model)
	if !model.animEngine.Paused() {
		t.Fatal("BlurMsg did not pause animation frames")
	}
	*now = (*now).Add(16 * time.Second)
	updated, cmd := model.Update(bubbletea.FocusMsg{})
	model = updated.(*Model)
	if model.animEngine.Paused() {
		t.Fatal("FocusMsg did not resume animation frames")
	}
	if cmd == nil {
		t.Fatal("FocusMsg did not restart the tick loop")
	}
	if dots := countSparkleDots(model.View()); dots != 0 {
		t.Fatalf("expired starfield rendered %d dots after focus", dots)
	}
	if model.sparkle.phase != sparkleFinished {
		t.Fatalf("phase after expired focus = %v, want finished", model.sparkle.phase)
	}
}

// Rust #44879: a focus round-trip before the deadline preserves the original
// visible deadline instead of restarting the 15-second opportunity.
func TestComposerStarfieldFocusGainPreservesVisibleDeadline(t *testing.T) {
	model, now := sparkleTestModel(t, "gpt-6-astra", true, DefaultEffectsSettings())
	_ = model.View()
	visibleSince := model.sparkle.visibleSince
	updated, _ := model.Update(bubbletea.BlurMsg{})
	model = updated.(*Model)
	*now = (*now).Add(2 * time.Second)
	updated, _ = model.Update(bubbletea.FocusMsg{})
	model = updated.(*Model)
	if model.sparkle.phase != sparkleVisible || !model.sparkle.visibleSince.Equal(visibleSince) {
		t.Fatalf("focus restarted sparkle: phase=%v since=%v want=%v", model.sparkle.phase, model.sparkle.visibleSince, visibleSince)
	}
	if dots := countSparkleDots(model.View()); dots == 0 {
		t.Fatal("starfield disappeared during an early focus round-trip")
	}
}
