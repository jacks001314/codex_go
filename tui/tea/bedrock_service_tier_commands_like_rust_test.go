package tea

import (
	"encoding/json"
	"fmt"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/model"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// TestBedrockUltrafastSlashCommandSelectsAndClearsTierLikeRust mirrors Rust
// #50472 (codex-rs/tui/src/chatwidget/tests/bedrock_catalog_tests.rs): the
// Bedrock Astra catalog advertises `ultrafast`, `/ultrafast` selects it and
// toggles back to the implicit default, model switches clear or restore the
// saved selection, and a custom catalog's `Express` tier answers `/express`.
func TestBedrockUltrafastSlashCommandSelectsAndClearsTierLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		providerID string
		modelID    string
		standardID string
	}{
		{"mantle", model.AmazonBedrockProviderID, model.AmazonBedrockGPT6AstraModelID, model.AmazonBedrockGPT6SolModelID},
		{"runtime_global", model.AmazonBedrockRuntimeProviderID, "global." + model.AmazonBedrockGPT6AstraModelID, "global." + model.AmazonBedrockGPT6SolModelID},
		{"runtime_us", model.AmazonBedrockRuntimeProviderID, "us." + model.AmazonBedrockGPT6AstraModelID, "us." + model.AmazonBedrockGPT6SolModelID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := codextui.ModelPickerOptionsFromCatalog(model.AmazonBedrockCatalogForProviderID(tc.providerID))
			state := codextui.NewState(nil)
			state.Model = tc.modelID
			var writes []string
			m := NewModel(state, Options{
				FeatureSettings:    map[string]bool{"fast_mode": true},
				ModelPickerOptions: options,
				OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
					for _, edit := range edits {
						if edit.KeyPath == "service_tier" {
							writes = append(writes, fmt.Sprint(edit.Value))
						}
					}
					return SettingsWriteResult{FilePath: "config.toml"}, nil
				},
			})
			m.refreshServiceTierCommands()
			if got := serviceTierCommandNames(m.serviceTierCommands); len(got) != 1 || got[0] != "ultrafast" {
				t.Fatalf("service tier commands = %#v, want [ultrafast]", got)
			}
			if m.State.ServiceTier != "" {
				t.Fatalf("initial tier = %q, want none", m.State.ServiceTier)
			}

			typeText(t, m, "/ultrafast")
			_, cmd := m.Update(key(bubbletea.KeyEnter))
			if m.State.ServiceTier != "ultrafast" {
				t.Fatalf("after /ultrafast tier = %q, want ultrafast", m.State.ServiceTier)
			}
			runTeaCmd(t, m, cmd)
			if len(writes) != 1 || writes[0] != "ultrafast" {
				t.Fatalf("persisted tiers = %#v, want [ultrafast]", writes)
			}

			// A model without the tier clears the effective selection, and the
			// saved choice comes back when the Astra model returns.
			m.State.Model = tc.standardID
			m.refreshServiceTierCommands()
			if m.State.ServiceTier != "" {
				t.Fatalf("tier on the standard model = %q, want none", m.State.ServiceTier)
			}
			if len(m.serviceTierCommands) != 0 {
				t.Fatalf("standard model commands = %#v, want none", m.serviceTierCommands)
			}
			m.State.Model = tc.modelID
			m.refreshServiceTierCommands()
			if m.State.ServiceTier != "ultrafast" {
				t.Fatalf("tier after returning to Astra = %q, want ultrafast", m.State.ServiceTier)
			}
			typeText(t, m, "/ultrafast")
			m.Update(key(bubbletea.KeyEnter))
			if m.State.ServiceTier != codextui.ServiceTierDefaultRequestValue {
				t.Fatalf("second /ultrafast tier = %q, want default", m.State.ServiceTier)
			}
		})
	}
}

// TestCustomBedrockCatalogTierSlashCommandLikeRust covers the custom-catalog
// half of the Rust test: the catalog's `Express` name becomes the command, its
// implicit default applies, and clearing plus reselecting works.
func TestCustomBedrockCatalogTierSlashCommandLikeRust(t *testing.T) {
	const catalogJSON = `{"models":[{
		"slug":"openai.gpt-6-astra",
		"display_name":"GPT-6-Astra",
		"visibility":"list",
		"supported_in_api":true,
		"service_tiers":[{"id":"ultrafast","name":"Express","description":"My custom Bedrock tier."}],
		"default_service_tier":"ultrafast"
	}]}`
	var catalog model.ModelsResponse
	if err := json.Unmarshal([]byte(catalogJSON), &catalog); err != nil {
		t.Fatalf("parse custom catalog: %v", err)
	}
	options := codextui.ModelPickerOptionsFromCatalog(catalog)
	state := codextui.NewState(nil)
	state.Model = "openai.gpt-6-astra"
	m := NewModel(state, Options{
		FeatureSettings:    map[string]bool{"fast_mode": true},
		ModelPickerOptions: options,
	})
	m.refreshServiceTierCommands()
	if got := serviceTierCommandNames(m.serviceTierCommands); len(got) != 1 || got[0] != "express" {
		t.Fatalf("custom catalog commands = %#v, want [express]", got)
	}
	if m.serviceTierCommands[0].Description != "My custom Bedrock tier." {
		t.Fatalf("custom command description = %q", m.serviceTierCommands[0].Description)
	}
	// No saved selection falls back to the catalog default tier.
	if m.State.ServiceTier != "ultrafast" {
		t.Fatalf("default tier = %q, want ultrafast", m.State.ServiceTier)
	}
	m.savedServiceTier = codextui.ServiceTierDefaultRequestValue
	m.refreshServiceTierCommands()
	if m.State.ServiceTier != codextui.ServiceTierDefaultRequestValue {
		t.Fatalf("explicit default tier = %q", m.State.ServiceTier)
	}
	typeText(t, m, "/express")
	m.Update(key(bubbletea.KeyEnter))
	if m.State.ServiceTier != "ultrafast" {
		t.Fatalf("after /express tier = %q, want ultrafast", m.State.ServiceTier)
	}
}

func serviceTierCommandNames(commands []bottompane.ServiceTierCommand) []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	return names
}
