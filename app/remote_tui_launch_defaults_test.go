package app

import (
	"testing"

	"codex_go/appserver"
	"codex_go/config"
)

// TestApplyServerEffectiveLaunchDefaults covers Rust #43177's precedence: the
// server's effective config seeds model/reasoning effort unless the launch chose
// them explicitly (CLI, generic override, or a selected profile), and the
// server catalog default fills in a model the server did not configure.
//
// The connected fresh-start path passes `serverOwned` (Rust #50913): the client's
// implicit model is dropped instead of forwarded, while an explicit profile
// launch (`serverOwned == false`) keeps its resolved model.
func TestApplyServerEffectiveLaunchDefaults(t *testing.T) {
	effective := map[string]any{"model": "server-model", "model_reasoning_effort": "high"}
	catalog := func() (string, bool) { return "catalog-default", true }
	profile := "work"
	profileLayer := []config.Layer{{
		Name:   config.LayerSource{Type: config.LayerSourceUser, Profile: &profile},
		Config: map[string]any{"model": "profile-model"},
	}}

	cases := []struct {
		name            string
		params          appserver.ThreadStartParams
		layers          []config.Layer
		cliKeys         []string
		harnessModelSet bool
		wantModel       string
		wantEffort      any
	}{
		{
			name:       "server values seed an unexplicit launch",
			params:     appserver.ThreadStartParams{Model: "local-default"},
			wantModel:  "server-model",
			wantEffort: "high",
		},
		{
			name:            "explicit CLI model wins",
			params:          appserver.ThreadStartParams{Model: "cli-model"},
			harnessModelSet: true,
			wantModel:       "cli-model",
			wantEffort:      "high",
		},
		{
			name:       "generic config override wins",
			params:     appserver.ThreadStartParams{Model: "override-model"},
			cliKeys:    []string{"model"},
			wantModel:  "override-model",
			wantEffort: "high",
		},
		{
			name:       "selected profile model wins",
			params:     appserver.ThreadStartParams{Model: "profile-model"},
			layers:     profileLayer,
			wantModel:  "profile-model",
			wantEffort: "high",
		},
		{
			name:       "explicit CLI reasoning effort wins",
			params:     appserver.ThreadStartParams{Config: map[string]any{"model_reasoning_effort": "low"}},
			cliKeys:    []string{"model_reasoning_effort"},
			wantModel:  "server-model",
			wantEffort: "low",
		},
	}
	for _, tc := range cases {
		params := cloneThreadStartParams(tc.params)
		applyServerEffectiveLaunchDefaults(&params, effective, tc.layers, tc.cliKeys, tc.harnessModelSet, catalog, true)
		if params.Model != tc.wantModel {
			t.Fatalf("%s: model = %q, want %q", tc.name, params.Model, tc.wantModel)
		}
		if got := params.Config["model_reasoning_effort"]; got != tc.wantEffort {
			t.Fatalf("%s: effort = %#v, want %#v", tc.name, got, tc.wantEffort)
		}
	}

	// A server with no configured model falls back to the catalog default.
	params := appserver.ThreadStartParams{Model: "local-default"}
	applyServerEffectiveLaunchDefaults(&params, map[string]any{}, nil, nil, false, catalog, true)
	if params.Model != "catalog-default" {
		t.Fatalf("catalog fallback model = %q", params.Model)
	}

	// Rust #50913: a server-owned fresh start never forwards the implicit client
	// model, so a server that configures no model and offers no catalog default
	// leaves the launch unset for the app server to resolve.
	params = appserver.ThreadStartParams{Model: "local-default"}
	applyServerEffectiveLaunchDefaults(&params, map[string]any{}, nil, nil, false, nil, true)
	if params.Model != "" {
		t.Fatalf("implicit client model = %q, want it dropped", params.Model)
	}

	// An explicit profile launch keeps the resolved client model (Rust #50913:
	// `uses_server_owned_fresh_bootstrap` is false for a selected profile).
	params = appserver.ThreadStartParams{Model: "profile-model"}
	applyServerEffectiveLaunchDefaults(&params, map[string]any{}, nil, nil, false, nil, false)
	if params.Model != "profile-model" {
		t.Fatalf("profile model = %q, want it preserved", params.Model)
	}
}

func cloneThreadStartParams(params appserver.ThreadStartParams) appserver.ThreadStartParams {
	cloned := params
	if params.Config != nil {
		cloned.Config = map[string]any{}
		for key, value := range params.Config {
			cloned.Config[key] = value
		}
	}
	return cloned
}
