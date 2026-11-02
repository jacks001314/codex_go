package onboarding

import (
	"context"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
)

func enterKey() bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyEnter}
}

func runeKey(r rune) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{r}}
}

// TestAuthPickerGroupsAlternativesLikeRust mirrors Rust #58427
// `alternative_pick_mode_shows_sign_in_another_way`: API key and provider
// wizards move under "Sign in another way", and Esc returns to the main menu.
func TestAuthPickerGroupsAlternativesLikeRust(t *testing.T) {
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      t.TempDir(),
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		BedrockAllowed: true,
	})
	main := model.displayedOptions()
	if len(main) != 3 || main[2].Choice != AuthChoiceOtherMethods {
		t.Fatalf("main menu = %#v", main)
	}
	if main[2].Label != "Sign in another way" || main[2].Description != "Use an API key or another provider" {
		t.Fatalf("other methods entry = %#v", main[2])
	}

	model.Update(runeKey('3'))
	if !model.otherMethodsOpen {
		t.Fatal("digit 3 must open the alternative methods submenu")
	}
	submenu := model.displayedOptions()
	if len(submenu) != 2 || submenu[0].Choice != AuthChoiceAPIKey || submenu[1].Choice != AuthChoiceBedrock {
		t.Fatalf("submenu = %#v", submenu)
	}
	if model.highlighted != AuthChoiceAPIKey {
		t.Fatalf("submenu highlight = %s, want api key", model.highlighted)
	}

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.otherMethodsOpen || model.state != SignInPickMode {
		t.Fatalf("esc left submenu open=%v state=%s", model.otherMethodsOpen, model.state)
	}
	if model.highlighted != AuthChoiceOtherMethods {
		t.Fatalf("highlight after esc = %s, want the alternative methods entry", model.highlighted)
	}
}

// TestAuthSubmenuRenderLikeRust pins the Rust #58427 copy: the submenu header,
// the alternative entries, and the "Press esc to go back" footer.
func TestAuthSubmenuRenderLikeRust(t *testing.T) {
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      t.TempDir(),
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		GcpAllowed:     true,
	})
	model.highlighted = AuthChoiceOtherMethods
	main := model.View()
	if !strings.Contains(main, "Sign in another way") {
		t.Fatalf("main menu is missing the submenu entry:\n%s", main)
	}
	if strings.Contains(main, "Use an OpenAI API key") {
		t.Fatalf("main menu still lists the API key entry:\n%s", main)
	}

	model.Update(enterKey())
	submenu := model.View()
	for _, want := range []string{"Sign in another way", "Use an OpenAI API key", "Use Google Cloud Vertex AI", "Press esc to go back"} {
		if !strings.Contains(submenu, want) {
			t.Fatalf("submenu is missing %q:\n%s", want, submenu)
		}
	}
	if strings.Contains(submenu, "Sign in with Device Code") {
		t.Fatalf("submenu leaked the main menu entries:\n%s", submenu)
	}
}

// TestAuthActivationKeyGuardBlocksHeldEnterLikeRust mirrors Rust #58427's
// `activation_key_guard` behaviour with the legacy press-only window: holding
// Enter on "Sign in another way" must not also submit the first alternative.
func TestAuthActivationKeyGuardBlocksHeldEnterLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      t.TempDir(),
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
	})
	model.now = func() time.Time { return now }
	model.highlighted = AuthChoiceOtherMethods

	model.Update(enterKey())
	if !model.otherMethodsOpen {
		t.Fatal("enter must open the submenu")
	}
	model.Update(enterKey())
	if model.state != SignInPickMode || !model.otherMethodsOpen || model.highlighted != AuthChoiceAPIKey {
		t.Fatalf("held enter advanced into %s (submenu=%v, highlight=%s)", model.state, model.otherMethodsOpen, model.highlighted)
	}

	now = now.Add(2 * time.Second)
	model.Update(enterKey())
	if model.state != SignInAPIKeyEntry {
		t.Fatalf("fresh enter after the guard window = %s, want api key entry", model.state)
	}
}

// TestAuthActivationGuardClearsOnOtherKeyLikeRust: a different key ends the
// guard, so typing in the API key field is never blocked by the selection.
func TestAuthActivationGuardClearsOnOtherKeyLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	guard := newActivationKeyGuard(keyIdentity(enterKey()), func() time.Time { return now })
	if !guard.consume(enterKey()) {
		t.Fatal("the held key must be consumed inside the guard window")
	}
	if guard.consume(runeKey('a')) {
		t.Fatal("a different key must not be consumed")
	}
	if guard.active {
		t.Fatal("a different key must end the guard")
	}
	if guard.consume(enterKey()) {
		t.Fatal("a fresh enter must pass once the guard ended")
	}
}
