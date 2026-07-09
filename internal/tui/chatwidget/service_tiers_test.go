package chatwidget

import "testing"

func TestServiceTierStateMatchesRustFastRequestValue(t *testing.T) {
	state := ServiceTierState{
		EffectiveServiceTier: ServiceTierFastRequestValue,
		HasChatGPTAccount:    true,
		FastModeEnabled:      true,
		ModelServiceTiers: []ServiceTierCommand{
			{ID: ServiceTierFastRequestValue, Name: "Fast", Description: "Fast tier"},
			{ID: "flex", Name: "Flex", Description: "Flex tier"},
		},
	}
	if state.CurrentServiceTier() != ServiceTierFastRequestValue {
		t.Fatalf("current tier = %q", state.CurrentServiceTier())
	}
	if !state.ShouldShowFastStatus() || !state.CanToggleFastModeFromKeybinding() {
		t.Fatalf("fast status/toggle = %#v", state)
	}
	if fast := state.FastServiceTier(); fast == nil || fast.ID != ServiceTierFastRequestValue {
		t.Fatalf("fast tier = %#v", fast)
	}
	if next := state.NextServiceTierForToggle(ServiceTierCommand{ID: ServiceTierFastRequestValue}); next != ServiceTierDefaultRequestValue {
		t.Fatalf("toggle off = %q", next)
	}
	if next := state.NextServiceTierForToggle(ServiceTierCommand{ID: "flex"}); next != "flex" {
		t.Fatalf("toggle flex = %q", next)
	}
}

func TestServiceTierStateUsesExactTierIDsMatchRust(t *testing.T) {
	state := ServiceTierState{
		ConfiguredServiceTier: " priority ",
		EffectiveServiceTier:  " priority ",
		HasChatGPTAccount:     true,
		FastModeEnabled:       true,
		ModelServiceTiers:     []ServiceTierCommand{{ID: ServiceTierFastRequestValue, Name: "Fast"}},
	}
	if state.ConfiguredTier() != " priority " || state.CurrentServiceTier() != " priority " {
		t.Fatalf("tiers should preserve raw strings: %#v", state)
	}
	if state.ShouldShowFastStatus() {
		t.Fatalf("spaced tier id should not match fast status: %#v", state)
	}
	if state.ModelSupportsServiceTier(" priority ") {
		t.Fatalf("service tier support should use exact ids: %#v", state)
	}

	idOnly := ServiceTierState{ModelServiceTiers: []ServiceTierCommand{{ID: "fast", Name: "Priority"}}}
	if fast := idOnly.FastServiceTier(); fast != nil {
		t.Fatalf("fast tier lookup should use name, not id: %#v", fast)
	}
}
