package app

import (
	"testing"

	"codex_go/appserver"
	"codex_go/cli"
	codextui "codex_go/tui"
)

// TestStartupLaunchChoicesOmitUnselectedSettingsLikeRust mirrors Rust #50013
// (tui/src/app_server_session/startup_launch.rs, StartupLaunchChoices) and the
// startup request table in app/tests/startup_defaults_tests.rs: the first thread
// carries only the launch's explicit model, provider and reasoning effort, while
// resolved client settings stay out of the request.
func TestStartupLaunchChoicesOmitUnselectedSettingsLikeRust(t *testing.T) {
	resolvedParams := func() appserver.ThreadStartParams {
		return appserver.ThreadStartParams{
			Model:  "client-model",
			Config: map[string]any{"model_reasoning_effort": "low"},
		}
	}
	cases := []struct {
		name         string
		root         *cli.RootOptions
		params       appserver.ThreadStartParams
		wantModel    string
		wantEffort   any
		wantProvider string
	}{
		{
			// ("saved"): neither the resolved client model nor its effort is sent.
			name:   "resolved defaults are omitted",
			params: resolvedParams(),
		},
		{
			// ("cli_model"): `-m` survives, the implicit effort does not.
			name:      "typed model survives without the implicit effort",
			root:      &cli.RootOptions{Shared: cli.SharedOptions{Model: "cli-model"}},
			params:    resolvedParams(),
			wantModel: "cli-model",
		},
		{
			// ("kv_model"): repeated `-c model=` keeps the last assignment.
			name: "repeated generic model override keeps the last value",
			root: &cli.RootOptions{ConfigOverrides: []string{
				"model=earlier-model",
				"model=kv-model",
			}},
			params:    resolvedParams(),
			wantModel: "kv-model",
		},
		{
			// ("cli_effort"): the explicit effort survives without a model.
			name:       "explicit effort survives",
			root:       &cli.RootOptions{ConfigOverrides: []string{"model_reasoning_effort=low"}},
			params:     resolvedParams(),
			wantEffort: "low",
		},
		{
			// ("migration"): an accepted migration is an explicit pair.
			name:       "accepted migration choices survive",
			root:       &cli.RootOptions{ConfigOverrides: []string{"model=upgraded-model", "model_reasoning_effort=high"}},
			params:     resolvedParams(),
			wantModel:  "upgraded-model",
			wantEffort: "high",
		},
		{
			// ("cli_provider"): a typed provider is forwarded on its own.
			name:         "typed provider survives",
			root:         &cli.RootOptions{Shared: cli.SharedOptions{OSSProvider: "openai"}},
			params:       resolvedParams(),
			wantProvider: "openai",
		},
		{
			// ("kv_provider"): a generic provider override is forwarded on its own.
			name:         "generic provider override survives",
			root:         &cli.RootOptions{ConfigOverrides: []string{"model_provider=openai"}},
			params:       resolvedParams(),
			wantProvider: "openai",
		},
		{
			// ("profile_model"/"profile_effort"): an explicit profile keeps the
			// resolved configuration instead of clearing it.
			name:       "explicit profile keeps resolved settings",
			root:       &cli.RootOptions{Shared: cli.SharedOptions{Profile: "work"}},
			params:     resolvedParams(),
			wantModel:  "client-model",
			wantEffort: "low",
		},
		{
			name:         "explicit profile forwards its provider override",
			root:         &cli.RootOptions{Shared: cli.SharedOptions{Profile: "work", OSSProvider: "ollama"}},
			params:       resolvedParams(),
			wantModel:    "client-model",
			wantEffort:   "low",
			wantProvider: "ollama",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := cloneThreadStartParams(tc.params)
			startupLaunchChoicesFromLaunch(tc.root).configure(&params)
			if params.Model != tc.wantModel {
				t.Fatalf("model = %q, want %q", params.Model, tc.wantModel)
			}
			if got := params.Config["model_reasoning_effort"]; got != tc.wantEffort {
				t.Fatalf("effort = %#v, want %#v", got, tc.wantEffort)
			}
			if params.ModelProvider != tc.wantProvider {
				t.Fatalf("modelProvider = %q, want %q", params.ModelProvider, tc.wantProvider)
			}
		})
	}

	// A nil launch keeps nothing: no model, no provider, no effort.
	params := appserver.ThreadStartParams{Model: "client-model", ModelProvider: "openai", Config: map[string]any{"model_reasoning_effort": "low"}}
	startupLaunchChoicesFromLaunch(nil).configure(&params)
	if params.Model != "" || params.ModelProvider != "" || params.Config != nil {
		t.Fatalf("nil launch params = %#v, want every unselected setting cleared", params)
	}
}

// TestLocalThreadStartOmitsImplicitModelLikeRust covers the Rust #50013
// `embedded_saved` case: the embedded bootstrap thread also lets the app server
// resolve an implicitly configured model instead of sending it explicitly.
func TestLocalThreadStartOmitsImplicitModelLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	state := codextui.NewState(&codextui.Options{Model: "config-model", Provider: "openai"})

	params := interactiveLocalThreadStartParams(&cli.RootOptions{}, state)
	if params.Model != "" {
		t.Fatalf("implicit embedded model = %q, want it omitted", params.Model)
	}
	if params.ModelProvider != "" {
		t.Fatalf("implicit embedded provider = %q, want it omitted", params.ModelProvider)
	}

	explicit := &cli.RootOptions{Shared: cli.SharedOptions{Model: "cli-model"}}
	if got := interactiveLocalThreadStartParams(explicit, state).Model; got != "cli-model" {
		t.Fatalf("explicit embedded model = %q, want cli-model", got)
	}
}
