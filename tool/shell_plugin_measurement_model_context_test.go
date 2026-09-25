package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"codex_go/plugin"
	"codex_go/sandbox"
)

// pluginMetricsShellRunner writes a valid measurement envelope to the sidecar
// path the executor installed, the way a trusted plugin script would.
type pluginMetricsShellRunner struct {
	onRun func(*ShellRequest)
}

func (r pluginMetricsShellRunner) Run(_ context.Context, req *ShellRequest) (*ShellResult, error) {
	if r.onRun != nil {
		r.onRun(req)
	}
	return &ShellResult{ExitCode: 0, Stdout: "measured\n"}, nil
}

// Mirrors Rust #45445's plugin-measurement half: the measurement batch carries
// the model and reasoning effort of the step that invoked the command, captured
// before the command ran so a later model switch cannot retarget it.
func TestShellExecutorPluginMeasurementsCarryTheInvokingModelLikeRust(t *testing.T) {
	resolved := plugin.ResolvedPluginMetricsOperation{
		PluginID: "sample@openai-curated",
		Operation: plugin.PluginMetricsOperation{
			OperationName: "security_scan",
			Measurements: map[string]plugin.PluginMeasurementDefinition{
				"issues_found": {},
			},
		},
	}
	var captured plugin.PluginMeasurementBatch
	trackerCalls := 0
	// The model switches to "later-model" while the command runs: the batch must
	// keep the model that invoked it.
	currentModel := "invoking-model"
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: pluginMetricsShellRunner{onRun: func(req *ShellRequest) {
			currentModel = "later-model"
			path := req.Env[plugin.PluginMetricsOutputEnvVar]
			if path == "" {
				t.Error("the plugin metrics sidecar path was not installed")
				return
			}
			if err := os.WriteFile(path, []byte(`{"version":1,"measurements":[{"name":"issues_found","value":3}]}`), 0o600); err != nil {
				t.Errorf("write measurements error = %v", err)
			}
		}},
		Shell: &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:   sandbox.ApprovalOnRequest,
			CWD:              t.TempDir(),
			DefaultTimeoutMS: 5000,
		},
		PluginMetricsResolver: func([]string, string) *plugin.ResolvedPluginMetricsOperation {
			return &resolved
		},
		PluginMeasurementTracker: func(_ context.Context, batch plugin.PluginMeasurementBatch) {
			trackerCalls++
			captured = batch
		},
		ModelContext: func() (string, string) { return currentModel, "max" },
	})

	if _, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-plugin-measurement",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"run-measure"}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if trackerCalls != 1 {
		t.Fatalf("plugin measurement tracker calls = %d, want 1", trackerCalls)
	}
	if captured.PluginID != "sample@openai-curated" || captured.Operation != "security_scan" || captured.ExecutionID == "" {
		t.Fatalf("measurement batch = %#v", captured)
	}
	if captured.ModelSlug != "invoking-model" || captured.ReasoningEffort != "max" {
		t.Fatalf("measurement attribution = %q/%q", captured.ModelSlug, captured.ReasoningEffort)
	}
	if len(captured.Rows) != 1 || captured.Rows[0].MeasurementName != "issues_found" || captured.Rows[0].NumberValue != 3 {
		t.Fatalf("measurement rows = %#v", captured.Rows)
	}

	// A command without an invoking context reports no attribution.
	unattributed := NewShellExecutor(&ShellExecutorOptions{
		Runner: pluginMetricsShellRunner{onRun: func(req *ShellRequest) {
			_ = os.WriteFile(req.Env[plugin.PluginMetricsOutputEnvVar], []byte(`{"version":1,"measurements":[{"name":"issues_found","value":1}]}`), 0o600)
		}},
		Shell: &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:   sandbox.ApprovalOnRequest,
			CWD:              t.TempDir(),
			DefaultTimeoutMS: 5000,
		},
		PluginMetricsResolver: func([]string, string) *plugin.ResolvedPluginMetricsOperation {
			return &resolved
		},
		PluginMeasurementTracker: func(_ context.Context, batch plugin.PluginMeasurementBatch) {
			captured = batch
		},
	})
	if _, err := unattributed.Execute(context.Background(), &Invocation{
		CallID:   "call-plugin-measurement-unattributed",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"run-measure"}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if captured.ModelSlug != "" || captured.ReasoningEffort != "" {
		t.Fatalf("unattributed measurement = %q/%q", captured.ModelSlug, captured.ReasoningEffort)
	}
}

// TestShellExecutorPluginMetricsGrantIsInternalLikeRust mirrors Rust #48073's
// launch half: the plugin-metrics sidecar's own filesystem grant is added to the
// command's permissions, recorded as a runtime-internal grant, and kept out of
// the agent-requested permissions, so the later write_stdin review does not
// treat it as something the agent asked for.
func TestShellExecutorPluginMetricsGrantIsInternalLikeRust(t *testing.T) {
	resolved := plugin.ResolvedPluginMetricsOperation{
		PluginID:  "sample@openai-curated",
		Operation: plugin.PluginMetricsOperation{OperationName: "security_scan"},
	}
	var launched *ShellRequest
	workspaceWrite := sandbox.WorkspaceWritePermissionProfile()
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: pluginMetricsShellRunner{onRun: func(req *ShellRequest) { launched = req }},
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:    sandbox.ApprovalOnRequest,
			CWD:               t.TempDir(),
			DefaultTimeoutMS:  5000,
			PermissionProfile: &workspaceWrite,
		},
		PluginMetricsResolver: func([]string, string) *plugin.ResolvedPluginMetricsOperation {
			return &resolved
		},
	})
	if _, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-plugin-grant",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"run-measure"}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if launched == nil {
		t.Fatal("the shell runner did not see the launch request")
	}
	internal := launched.InternalPermissions
	if internal == nil || len(internal.FileSystem) != 1 {
		t.Fatalf("InternalPermissions = %#v, want the sidecar's write grant", internal)
	}
	sidecarDir := internal.FileSystem[0]
	if sidecarDir == "" || !filepath.IsAbs(sidecarDir) {
		t.Fatalf("sidecar grant = %q, want an absolute directory", sidecarDir)
	}
	if launched.AdditionalPermissions != nil {
		t.Fatalf("agent additional permissions = %#v, want the runtime grant kept out", launched.AdditionalPermissions)
	}
	if launched.PermissionProfile == nil {
		t.Fatal("launch permission profile is nil")
	}
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(*launched.PermissionProfile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	if launched.PermissionProfileJSON != profileJSON {
		t.Fatalf("PermissionProfileJSON = %q, want the merged profile %q", launched.PermissionProfileJSON, profileJSON)
	}
	var wire struct {
		FileSystem struct {
			Entries []struct {
				Path struct {
					Type string `json:"type"`
					Path string `json:"path"`
				} `json:"path"`
				Access string `json:"access"`
			} `json:"entries"`
		} `json:"file_system"`
	}
	if err := json.Unmarshal([]byte(profileJSON), &wire); err != nil {
		t.Fatalf("Unmarshal permission profile JSON error = %v", err)
	}
	granted := false
	for _, entry := range wire.FileSystem.Entries {
		if entry.Path.Type == "path" && entry.Path.Path == sidecarDir && entry.Access == "write" {
			granted = true
		}
	}
	if !granted {
		t.Fatalf("the command's profile does not carry the sidecar write grant: %s", profileJSON)
	}
}
