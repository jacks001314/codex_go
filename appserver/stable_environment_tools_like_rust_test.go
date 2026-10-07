package appserver

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust #50741/#50962: the app server owns the effective config, so it has to
// turn the default-off `stable_environment_tools` feature into the turn's tool
// options. With the feature off the `environment_id` selector follows the
// *usable* environments; once it is on the selector stays stable and follows
// the turn's *selections*. The registry the app server builds is the only place
// this decision is observable, so the test drives `toolRouterForTurn` and reads
// the model-visible specs.
func TestRuntimeRouterStableEnvironmentToolsSelectorLikeRust(t *testing.T) {
	for _, stable := range []bool{false, true} {
		name := "default_off"
		if stable {
			name = "opted_in"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			body := fmt.Sprintf("[features]\nstable_environment_tools = %t\n", stable)
			if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
				t.Fatalf("WriteFile(config) error = %v", err)
			}
			manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "/workspace")
			if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "remote", ExecServerURL: "ws://127.0.0.1:1"}); err != nil {
				t.Fatalf("EnvironmentManager.Add() error = %v", err)
			}
			cwd := t.TempDir()
			router := NewRuntimeRouter(RuntimeServices{
				Config:      config.NewConfigService(home),
				Environment: manager,
				DefaultCWD:  cwd,
			})
			defer router.Close()

			// `remote` has an executor record, `starting` does not, so the turn has
			// two selections but only one usable environment.
			params := &turn.TurnStartParams{
				ThreadID: "thread-stable-environment-tools",
				Model:    defaultModelForAppTurn(),
				Environments: []map[string]any{
					{"environmentId": "remote"},
					{"environmentId": "starting"},
				},
			}
			toolRouter, err := router.toolRouterForTurn(cwd, params, "turn-stable-environment-tools")
			if err != nil {
				t.Fatalf("toolRouterForTurn() error = %v", err)
			}
			specs := toolRouter.ModelVisibleSpecs()

			// The app server exposes the code-mode surface by default, so the shell
			// family's selector shows up in the nested `exec_command` schema that
			// `exec` embeds in its description; view_image and apply_patch stay top
			// level (JSON schema and freeform grammar). All three follow the same
			// readiness decision: two selections with one usable environment expose
			// the selector only while the feature is on.
			assertSelector := func(surface string, hasSelector bool) {
				t.Helper()
				if hasSelector != stable {
					t.Fatalf("%s exposes environment_id = %t with stable_environment_tools=%t, want %t", surface, hasSelector, stable, stable)
				}
			}

			exec, ok := modelVisibleSpecForTest(specs, "exec")
			if !ok {
				t.Fatalf("exec spec missing with stable_environment_tools=%t", stable)
			}
			assertSelector("exec_command", strings.Contains(exec.Description, `"environment_id"`))

			viewImage, ok := modelVisibleSpecForTest(specs, tool.ViewImageToolName)
			if !ok {
				t.Fatalf("%s spec missing with stable_environment_tools=%t", tool.ViewImageToolName, stable)
			}
			properties, _ := viewImage.InputSchema["properties"].(map[string]any)
			_, hasViewImageSelector := properties["environment_id"]
			assertSelector(tool.ViewImageToolName, hasViewImageSelector)

			applyPatch, ok := modelVisibleSpecForTest(specs, tool.DefaultApplyPatchToolName)
			if !ok || applyPatch.Freeform == nil {
				t.Fatalf("apply_patch freeform spec missing with stable_environment_tools=%t", stable)
			}
			assertSelector(tool.DefaultApplyPatchToolName, strings.Contains(applyPatch.Freeform.Definition, "environment_id"))
		})
	}
}

func modelVisibleSpecForTest(specs []tool.Spec, name string) (tool.Spec, bool) {
	for _, spec := range specs {
		if spec.Name.Key() == name {
			return spec, true
		}
	}
	return tool.Spec{}, false
}
