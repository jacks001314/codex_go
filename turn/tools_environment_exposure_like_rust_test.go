package turn

import (
	"context"
	"testing"

	"codex_go/tool"
)

// waitingEnvironmentWaiter reports every selected environment as still
// starting, so `readyEnvironmentCount` sees a turn whose executors are not
// usable yet (Rust `TurnEnvironmentSnapshot::starting`).
type waitingEnvironmentWaiter struct{}

func (waitingEnvironmentWaiter) Status(context.Context, string) (tool.EnvironmentStatus, string, error) {
	return tool.EnvironmentStatusPending, "", nil
}

// readyEnvironmentWaiter reports a usable executor for every environment.
type readyEnvironmentWaiter struct{}

func (readyEnvironmentWaiter) Status(context.Context, string) (tool.EnvironmentStatus, string, error) {
	return tool.EnvironmentStatusReady, "", nil
}

// readyRequestPermissionsReviewer stands in for the host reviewer the
// request_permissions tool needs to be registered.
func readyRequestPermissionsReviewer(context.Context, string, string, string, string, string, map[string]any) (tool.RequestPermissionsDecision, error) {
	return tool.RequestPermissionsDecision{}, nil
}

func environmentBackedToolNames() []string {
	return []string{
		tool.DefaultExecCommandToolName,
		tool.DefaultWriteStdinToolName,
		tool.DefaultApplyPatchToolName,
		tool.ViewImageToolName,
		tool.RequestPermissionsToolName,
	}
}

func registryHasTool(t *testing.T, registry *tool.Registry, name string) bool {
	t.Helper()
	_, ok := registry.Lookup(tool.PlainName(name))
	return ok
}

// TestBuildToolRegistryGatesEnvironmentBackedToolsLikeRust covers the Go half of
// Rust #50962's `empty_turn_environments_gate_environment_backed_tools`
// (parameterized default_off/opted_in): a turn whose selected environments have
// no usable executor advertises environment-backed tools only while the
// default-off `stable_environment_tools` feature is on.
func TestBuildToolRegistryGatesEnvironmentBackedToolsLikeRust(t *testing.T) {
	for _, stable := range []bool{false, true} {
		name := "default_off"
		if stable {
			name = "opted_in"
		}
		t.Run(name, func(t *testing.T) {
			options := DefaultToolRegistryOptions(t.TempDir())
			options.EnableUnifiedExec = true
			options.SelectedEnvironmentIDs = []string{"remote"}
			options.EnvironmentWaiter = waitingEnvironmentWaiter{}
			options.StableEnvironmentTools = stable
			options.EnableRequestPermissions = true
			options.RequestPermissionsReviewer = readyRequestPermissionsReviewer
			options.ViewImage = &tool.ViewImageOptions{CWD: options.Shell.Validation.CWD}

			registry, err := BuildToolRegistry(options)
			if err != nil {
				t.Fatalf("BuildToolRegistry() error = %v", err)
			}
			for _, environmentTool := range environmentBackedToolNames() {
				if got := registryHasTool(t, registry, environmentTool); got != stable {
					t.Fatalf("%s registered = %v with stable_environment_tools=%v; want %v", environmentTool, got, stable, stable)
				}
			}
			// Non-environment tools stay available either way.
			if !registryHasTool(t, registry, "update_plan") {
				t.Fatalf("update_plan missing with stable_environment_tools=%v", stable)
			}
		})
	}
}

// TestBuildToolRegistryKeepsEnvironmentBackedToolsWithUsableEnvironmentLikeRust
// covers Rust `turn_environment_selection_keeps_environment_backed_tools`: a turn
// whose selected environment is usable keeps the tools with the feature off.
func TestBuildToolRegistryKeepsEnvironmentBackedToolsWithUsableEnvironmentLikeRust(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableUnifiedExec = true
	options.SelectedEnvironmentIDs = []string{"remote"}
	options.EnvironmentWaiter = readyEnvironmentWaiter{}
	options.Shell.UnifiedExecEnvironments = []tool.UnifiedExecEnvironment{{ID: "remote", ExecServerURL: "ws://127.0.0.1:1"}}
	options.ViewImage = &tool.ViewImageOptions{CWD: options.Shell.Validation.CWD}
	options.EnableRequestPermissions = true
	options.RequestPermissionsReviewer = readyRequestPermissionsReviewer

	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	for _, environmentTool := range environmentBackedToolNames() {
		if !registryHasTool(t, registry, environmentTool) {
			t.Fatalf("%s missing for a usable environment", environmentTool)
		}
	}
}

// TestBuildToolRegistryExposesEnvironmentSelectorsLikeRust covers Rust
// `multiple_environments_expose_environment_selectors`: the selector argument
// follows the usable environment count while the feature is off, and every
// selection once it is on (selectors stay stable as readiness changes).
func TestBuildToolRegistryExposesEnvironmentSelectorsLikeRust(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableUnifiedExec = true
	options.SelectedEnvironmentIDs = []string{"primary", "starting"}
	options.EnvironmentWaiter = readyEnvironmentWaiter{}
	options.Shell.UnifiedExecEnvironments = []tool.UnifiedExecEnvironment{{ID: "primary", ExecServerURL: "ws://127.0.0.1:1"}}

	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	spec, ok := registry.Spec(tool.PlainName(tool.DefaultExecCommandToolName))
	if !ok {
		t.Fatalf("exec_command spec missing")
	}
	properties, _ := spec.InputSchema["properties"].(map[string]any)
	if _, ok := properties["environment_id"]; ok {
		t.Fatalf("one usable environment still exposes environment_id: %#v", properties)
	}

	options.StableEnvironmentTools = true
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	spec, ok = registry.Spec(tool.PlainName(tool.DefaultExecCommandToolName))
	if !ok {
		t.Fatalf("exec_command spec missing with stable_environment_tools")
	}
	properties, _ = spec.InputSchema["properties"].(map[string]any)
	if _, ok := properties["environment_id"]; !ok {
		t.Fatalf("stable_environment_tools keeps selectors stable but environment_id is missing: %#v", properties)
	}
}

// TestExecCommandParameterGatesLikeRust covers Rust #50962's
// `include_shell_parameter` / `include_login_parameter` decisions for the
// exec_command surface.
func TestExecCommandParameterGatesLikeRust(t *testing.T) {
	// A zsh-fork turn pins the launch shell unless a usable environment is
	// remote, and the default-off login parameter follows the usable
	// environments' login-shell policy.
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableUnifiedExec = true
	options.SelectedEnvironmentIDs = []string{"local-exec"}
	options.EnvironmentWaiter = readyEnvironmentWaiter{}
	options.Shell.UnifiedExecEnvironments = []tool.UnifiedExecEnvironment{{ID: "local-exec"}}
	options.Shell.Validation.ShellMode = tool.UnifiedExecShellModeZshFork
	options.Shell.Validation.AllowLoginShell = false

	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	properties := execCommandProperties(t, registry)
	if _, ok := properties["shell"]; ok {
		t.Fatalf("zsh-fork turn with a local environment still exposes shell: %#v", properties)
	}
	if _, ok := properties["login"]; ok {
		t.Fatalf("login-shell policy off still exposes login: %#v", properties)
	}

	// A remote environment brings the shell parameter back.
	options.Shell.UnifiedExecEnvironments = []tool.UnifiedExecEnvironment{{ID: "local-exec", ExecServerURL: "ws://127.0.0.1:1"}}
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	if _, ok := execCommandProperties(t, registry)["shell"]; !ok {
		t.Fatalf("remote environment should keep the shell parameter")
	}

	// Any usable environment allowing a login shell brings the login
	// parameter back (Rust `login_shell_parameter_is_available_when_any_environment_allows_it`).
	allowLoginShell := true
	options.Shell.UnifiedExecEnvironments = []tool.UnifiedExecEnvironment{{ID: "local-exec", AllowLoginShell: &allowLoginShell}}
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	if _, ok := execCommandProperties(t, registry)["login"]; !ok {
		t.Fatalf("an environment allowing a login shell should keep the login parameter")
	}

	// The feature keeps both parameters regardless of the execution config.
	options.Shell.UnifiedExecEnvironments = []tool.UnifiedExecEnvironment{{ID: "local-exec"}}
	options.StableEnvironmentTools = true
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	properties = execCommandProperties(t, registry)
	if _, ok := properties["shell"]; !ok {
		t.Fatalf("stable_environment_tools keeps the shell parameter: %#v", properties)
	}
	if _, ok := properties["login"]; !ok {
		t.Fatalf("stable_environment_tools keeps the login parameter: %#v", properties)
	}
}

func execCommandProperties(t *testing.T, registry *tool.Registry) map[string]any {
	t.Helper()
	spec, ok := registry.Spec(tool.PlainName(tool.DefaultExecCommandToolName))
	if !ok {
		t.Fatalf("exec_command spec missing")
	}
	properties, _ := spec.InputSchema["properties"].(map[string]any)
	if properties == nil {
		t.Fatalf("exec_command schema has no properties: %#v", spec.InputSchema)
	}
	return properties
}
