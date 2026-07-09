package chatwidget

import (
	"testing"
)

func TestPersonalityPopupDisabledUntilSessionConfigured(t *testing.T) {
	got := NewPersonalityPopup(PersonalityFriendly, false, true, "gpt-5")
	if got.Kind != SettingsPopupInfo || got.Message != "Personality selection is disabled until startup completes." {
		t.Fatalf("popup = %#v", got)
	}
}

func TestPersonalityPopupRejectsUnsupportedModel(t *testing.T) {
	got := NewPersonalityPopup(PersonalityFriendly, true, false, "gpt-4")
	if got.Kind != SettingsPopupError || got.Message != "Current model (gpt-4) doesn't support personalities. Try /model to pick a different model." {
		t.Fatalf("popup = %#v", got)
	}
}

func TestPersonalityPopupView(t *testing.T) {
	got := NewPersonalityPopup(PersonalityPragmatic, true, true, "gpt-5")
	if got.Kind != SettingsPopupOK {
		t.Fatalf("kind = %s", got.Kind)
	}
	if got.View.Title != "Select Personality" || got.View.Subtitle != "Choose a communication style for Codex." || len(got.View.Items) != 2 {
		t.Fatalf("view = %#v", got.View)
	}
	if got.View.Items[0].Name != "Friendly" || got.View.Items[0].Description != "Warm, collaborative, and helpful." {
		t.Fatalf("friendly option = %#v", got.View.Items[0])
	}
	if got.View.Items[1].Name != "Pragmatic" || !got.View.Items[1].Current {
		t.Fatalf("pragmatic option = %#v", got.View.Items[1])
	}
}

func TestPersonalityLabelsAndDescriptions(t *testing.T) {
	if PersonalityLabel(PersonalityNone) != "None" || PersonalityDescription(PersonalityNone) != "No personality instructions." {
		t.Fatal("none personality copy mismatch")
	}
	if PersonalityLabel(Personality("unknown")) != "Friendly" {
		t.Fatalf("unknown personality label = %q", PersonalityLabel(Personality("unknown")))
	}
}

func TestExperimentalFeaturesViewUsesRegistryExperimentalStage(t *testing.T) {
	view := NewExperimentalFeaturesView(map[string]bool{
		"memories":      true,
		"network_proxy": false,
	})
	if view.Title != "Experimental Features" || len(view.Items) == 0 {
		t.Fatalf("view = %#v", view)
	}
	foundMemories := false
	foundStable := false
	for _, item := range view.Items {
		if item.Key == "memories" {
			foundMemories = true
			if item.Name != "Memories" || item.Description != "Allow Codex to create new memories from conversations and bring relevant memories into new conversations." || !item.Enabled {
				t.Fatalf("memories item = %#v", item)
			}
		}
		if item.Key == "network_proxy" && (item.Name != "Network proxy" || item.Description != "Apply network proxy restrictions to sandboxed sessions that already have network access.") {
			t.Fatalf("network proxy item = %#v", item)
		}
		if item.Key == "plugins" {
			foundStable = true
		}
	}
	if !foundMemories {
		t.Fatalf("memories feature missing: %#v", view.Items)
	}
	if foundStable {
		t.Fatalf("stable feature should not be in experimental menu: %#v", view.Items)
	}
}
