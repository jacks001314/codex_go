package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/config"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	codextea "codex_go/tui/tea"
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

func TestRemoteNewThreadPreservesLaunchAndLaterPermissionsLikeRust(t *testing.T) {
	root := &cli.RootOptions{Shared: cli.SharedOptions{
		ApprovalPolicy: "never",
		Sandbox:        "danger-full-access",
	}}
	state := codextui.NewState(&codextui.Options{
		ApprovalPolicy: "never",
		Sandbox:        "danger-full-access",
	})

	params, err := remoteThreadStartParams(root, state)
	if err != nil {
		t.Fatalf("launch thread/start params: %v", err)
	}
	if params.ApprovalPolicy != "never" || params.Sandbox != "danger-full-access" {
		t.Fatalf("launch permissions = approval:%#v sandbox:%#v", params.ApprovalPolicy, params.Sandbox)
	}

	// A later permission-picker choice becomes the live session state and must
	// win for the next fresh session, rather than the original launch value.
	state.ApprovalPolicy = "on-request"
	state.Sandbox = "read-only"
	params, err = remoteThreadStartParams(root, state)
	if err != nil {
		t.Fatalf("updated thread/start params: %v", err)
	}
	if params.ApprovalPolicy != "on-request" || params.Sandbox != "read-only" {
		t.Fatalf("updated permissions = approval:%#v sandbox:%#v", params.ApprovalPolicy, params.Sandbox)
	}
}

func TestRemoteSideForkPreservesSessionProviderLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Provider: "ollama"})
	params, err := remoteSideThreadForkParams(&cli.RootOptions{}, state, "thread-parent", nil)
	if err != nil {
		t.Fatalf("remoteSideThreadForkParams() error = %v", err)
	}
	if params.ModelProvider == nil || *params.ModelProvider != "ollama" {
		t.Fatalf("fork modelProvider = %#v, want ollama", params.ModelProvider)
	}
}

func TestRemoteTurnStartSnapshotsLocalImagesLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context.png")
	data := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("image-data")...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-image")
	params, err := remoteTurnStartParams(&cli.RootOptions{}, state, "thread-image", codextea.SubmitRequest{
		Attachments: []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: path}},
	})
	if err != nil {
		t.Fatalf("remoteTurnStartParams() error = %v", err)
	}
	if len(params.Input) != 1 || params.Input[0].Type != "image" ||
		!strings.HasPrefix(params.Input[0].URL, "data:image/png;base64,") ||
		params.Input[0].Path != "" {
		t.Fatalf("remote image input = %#v", params.Input)
	}
}
