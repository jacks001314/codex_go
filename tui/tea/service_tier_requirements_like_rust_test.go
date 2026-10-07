package tea

import (
	"reflect"
	"testing"

	"codex_go/config"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

func serviceTierRequirementsTestBool(value bool) *bool { return &value }

func serviceTierRequirementsTestCatalog() []codextui.ModelPickerOption {
	return []codextui.ModelPickerOption{{
		ID:           "gpt-5.5",
		Label:        "gpt-5.5",
		ServiceTiers: []string{"priority", "ultrafast", "flex"},
	}}
}

func serviceTierRequirementsTestCommands() []bottompane.ServiceTierCommand {
	return bottompane.ServiceTierCommandsFromIDs([]string{"priority", "ultrafast", "flex"})
}

func serviceTierRequirementsTestResponse(independent *bool, features map[string]bool) *config.ConfigRequirementsReadResponse {
	if features == nil {
		return nil
	}
	return &config.ConfigRequirementsReadResponse{
		SupportsIndependentSpeedModes: independent,
		Requirements:                  &config.ConfigRequirements{FeatureRequirements: features},
	}
}

func serviceTierCommandIDs(commands []bottompane.ServiceTierCommand) []string {
	ids := make([]string, 0, len(commands))
	for _, command := range commands {
		ids = append(ids, command.ID)
	}
	return ids
}

// TestNewModelConstrainsCatalogServiceTiersLikeRust drives the TUI's real
// construction path (Options -> NewModel) with the requirements the app layer
// reads from configRequirements/read, and pins that the server's speed policies
// reach both surfaces the catalog feeds: the model picker's tier list and the
// service-tier commands (Rust #51253
// service_tier_resolution::constrain_server_service_tiers, applied by the TUI
// bootstrap in app_server_session.rs).
//
// Rust counterparts: `config_requirements_read_exposes_independent_speed_policy`
// (codex-rs/app-server/tests/suite/v2/config_rpc.rs) and
// `service_tier_commands_and_saved_selection_respect_independent_speed_policy`
// (codex-rs/tui/src/chatwidget/tests/slash_commands.rs).
func TestNewModelConstrainsCatalogServiceTiersLikeRust(t *testing.T) {
	cases := []struct {
		name         string
		independent  *bool
		features     map[string]bool
		wantTiers    []string
		wantCommands []string
	}{
		{
			name:         "no feature requirements leaves the catalog alone",
			features:     nil,
			wantTiers:    []string{"priority", "ultrafast", "flex"},
			wantCommands: []string{"priority", "ultrafast", "flex"},
		},
		{
			name:         "older server keeps ultrafast behind the shared fast gate",
			independent:  nil,
			features:     map[string]bool{"fast_mode": false, "ultrafast_mode": true},
			wantTiers:    []string{"flex"},
			wantCommands: []string{"flex"},
		},
		{
			name:         "independent server allows ultrafast while fast is denied",
			independent:  serviceTierRequirementsTestBool(true),
			features:     map[string]bool{"fast_mode": false, "ultrafast_mode": true},
			wantTiers:    []string{"ultrafast", "flex"},
			wantCommands: []string{"ultrafast", "flex"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.Model = "gpt-5.5"
			m := NewModel(state, Options{
				ModelPickerOptions:      serviceTierRequirementsTestCatalog(),
				ServiceTierCommands:     serviceTierRequirementsTestCommands(),
				FeatureSettings:         map[string]bool{"fast_mode": true, "ultrafast_mode": true},
				ServiceTierRequirements: serviceTierRequirementsTestResponse(tc.independent, tc.features),
			})
			if len(m.modelPickerOpts) != 1 {
				t.Fatalf("modelPickerOpts = %#v, want one option", m.modelPickerOpts)
			}
			if !reflect.DeepEqual(m.modelPickerOpts[0].ServiceTiers, tc.wantTiers) {
				t.Fatalf("model picker service tiers = %#v, want %#v", m.modelPickerOpts[0].ServiceTiers, tc.wantTiers)
			}
			if got := serviceTierCommandIDs(m.serviceTierCommands); !reflect.DeepEqual(got, tc.wantCommands) {
				t.Fatalf("service tier commands = %#v, want %#v", got, tc.wantCommands)
			}
		})
	}
}

// TestApplyModelsResultConstrainsCatalogServiceTiersLikeRust drives the raw
// model/list refresh path (OnListModels -> ModelsResultMsg -> applyModelsResult),
// which is the live catalog the picker and the fast-mode toggle read after a
// server catalog refresh (Rust #51253, same constrain step as the bootstrap).
func TestApplyModelsResultConstrainsCatalogServiceTiersLikeRust(t *testing.T) {
	cases := []struct {
		name         string
		independent  *bool
		features     map[string]bool
		wantTiers    []string
		wantCommands []string
	}{
		{
			name:         "older server keeps ultrafast behind the shared fast gate",
			independent:  nil,
			features:     map[string]bool{"fast_mode": false, "ultrafast_mode": true},
			wantTiers:    []string{"flex"},
			wantCommands: []string{"flex"},
		},
		{
			name:         "independent server allows ultrafast while fast is denied",
			independent:  serviceTierRequirementsTestBool(true),
			features:     map[string]bool{"fast_mode": false, "ultrafast_mode": true},
			wantTiers:    []string{"ultrafast", "flex"},
			wantCommands: []string{"ultrafast", "flex"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.Model = "gpt-5.5"
			m := NewModel(state, Options{
				FeatureSettings:         map[string]bool{"fast_mode": true, "ultrafast_mode": true},
				ServiceTierRequirements: serviceTierRequirementsTestResponse(tc.independent, tc.features),
			})
			m.pendingModelsRequestID = 7
			m.applyModelsResult(ModelsResultMsg{RequestID: 7, Options: serviceTierRequirementsTestCatalog()})
			if len(m.modelPickerOpts) != 1 {
				t.Fatalf("modelPickerOpts = %#v, want the refreshed catalog", m.modelPickerOpts)
			}
			if !reflect.DeepEqual(m.modelPickerOpts[0].ServiceTiers, tc.wantTiers) {
				t.Fatalf("refreshed service tiers = %#v, want %#v", m.modelPickerOpts[0].ServiceTiers, tc.wantTiers)
			}
			if got := serviceTierCommandIDs(m.serviceTierCommands); !reflect.DeepEqual(got, tc.wantCommands) {
				t.Fatalf("service tier commands = %#v, want %#v", got, tc.wantCommands)
			}
		})
	}
}

// TestApplyModelCatalogResultConstrainsCatalogServiceTiersLikeRust pins the
// hidden-inclusive catalog path (the Reserve model is picker-hidden but still
// resolves through this list and its tiers back the service-tier resolution), so
// a hidden model cannot escape the server's speed policies.
func TestApplyModelCatalogResultConstrainsCatalogServiceTiersLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Model = "gpt-5.5"
	m := NewModel(state, Options{
		ServiceTierRequirements: serviceTierRequirementsTestResponse(nil, map[string]bool{"fast_mode": false}),
	})
	m.pendingModelCatalogRequestID = 3
	m.applyModelCatalogResult(ModelCatalogResultMsg{RequestID: 3, Options: serviceTierRequirementsTestCatalog()})
	if len(m.modelCatalogOpts) != 1 {
		t.Fatalf("modelCatalogOpts = %#v, want one option", m.modelCatalogOpts)
	}
	if want := []string{"flex"}; !reflect.DeepEqual(m.modelCatalogOpts[0].ServiceTiers, want) {
		t.Fatalf("hidden catalog service tiers = %#v, want %#v", m.modelCatalogOpts[0].ServiceTiers, want)
	}
}
