package tool

import (
	"context"
	"os"
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
