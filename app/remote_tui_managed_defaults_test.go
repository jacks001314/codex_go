package app

import (
	"testing"

	"codex_go/appserver"
	"codex_go/config"
)

func strPtrValue(value string) *string { return &value }

// TestApplyServerEffectiveLaunchDefaults covers Rust #43177's precedence: the
// server's effective config seeds model/reasoning effort unless the launch chose
// them explicitly (CLI, generic override, or a selected profile), and the
// server catalog default fills in a model the server did not configure.
func TestApplyServerEffectiveLaunchDefaults(t *testing.T) {
	effective := map[string]any{"model": "server-model", "model_reasoning_effort": "high"}
	catalog := func() (string, bool) { return "catalog-default", true }
	profileLayer := []config.Layer{{
		Name:   config.LayerSource{Type: config.LayerSourceUser, Profile: strPtrValue("work")},
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
		applyServerEffectiveLaunchDefaults(&params, effective, tc.layers, tc.cliKeys, tc.harnessModelSet, catalog)
		if params.Model != tc.wantModel {
			t.Fatalf("%s: model = %q, want %q", tc.name, params.Model, tc.wantModel)
		}
		if got := params.Config["model_reasoning_effort"]; got != tc.wantEffort {
			t.Fatalf("%s: effort = %#v, want %#v", tc.name, got, tc.wantEffort)
		}
	}

	// A server with no configured model falls back to the catalog default.
	params := appserver.ThreadStartParams{Model: "local-default"}
	applyServerEffectiveLaunchDefaults(&params, map[string]any{}, nil, nil, false, catalog)
	if params.Model != "catalog-default" {
		t.Fatalf("catalog fallback model = %q", params.Model)
	}

	// Without a catalog default the bootstrap model is preserved.
	params = appserver.ThreadStartParams{Model: "local-default"}
	applyServerEffectiveLaunchDefaults(&params, map[string]any{}, nil, nil, false, nil)
	if params.Model != "local-default" {
		t.Fatalf("bootstrap model = %q, want it preserved", params.Model)
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
