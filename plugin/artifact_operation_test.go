package plugin

import (
	"testing"
)

func TestRecognizeArtifactOperationMatchesTrustedPrimaryRuntimeMarkersLikeRust(t *testing.T) {
	attribution := &PluginCommandAttribution{
		PluginID:   "presentations@openai-primary-runtime",
		ScriptPath: "skills/presentations/container_tools/mark_artifact_operation_started.mjs",
	}
	command := []string{
		"python",
		"/plugins/presentations/skills/presentations/container_tools/mark_artifact_operation_started.mjs",
		"--operation-kind", "create",
		"--expected-output-count", "2",
		"--output-format", "pptx",
	}
	operation := RecognizeArtifactOperation(attribution, command)
	if operation == nil {
		t.Fatal("recognized operation = nil, want presentations create")
	}
	if operation.PluginName != "presentations" || operation.ArtifactType != "presentation" ||
		operation.OperationKind != "create" || operation.ExpectedOutputCount != 2 || operation.OutputFormat != "pptx" {
		t.Fatalf("operation = %+v", operation)
	}

	// Case-insensitive output formats are normalized to the known spelling.
	upper := append([]string(nil), command...)
	upper[len(upper)-1] = "PPTX"
	if got := RecognizeArtifactOperation(attribution, upper); got == nil || got.OutputFormat != "pptx" {
		t.Fatalf("uppercase format operation = %+v", got)
	}

	// Edit kind is accepted.
	edit := append([]string(nil), command...)
	edit[3] = "edit"
	if got := RecognizeArtifactOperation(attribution, edit); got == nil || got.OperationKind != "edit" {
		t.Fatalf("edit operation = %+v", got)
	}
}

func TestRecognizeArtifactOperationRejectsMismatchesLikeRust(t *testing.T) {
	base := []string{
		"python",
		"/plugins/presentations/skills/presentations/container_tools/mark_artifact_operation_started.mjs",
		"--operation-kind", "create",
		"--expected-output-count", "2",
		"--output-format", "pptx",
	}
	validAttribution := &PluginCommandAttribution{
		PluginID:   "presentations@openai-primary-runtime",
		ScriptPath: "skills/presentations/container_tools/mark_artifact_operation_started.mjs",
	}
	cases := []struct {
		name        string
		attribution *PluginCommandAttribution
		command     []string
	}{
		{"non-primary marketplace", &PluginCommandAttribution{PluginID: "presentations@openai-curated-remote", ScriptPath: "skills/presentations/container_tools/mark_artifact_operation_started.mjs"}, base},
		{"wrong plugin name", &PluginCommandAttribution{PluginID: "documents@openai-primary-runtime", ScriptPath: "skills/presentations/container_tools/mark_artifact_operation_started.mjs"}, base},
		{"wrong script path", &PluginCommandAttribution{PluginID: "presentations@openai-primary-runtime", ScriptPath: "skills/other/script.mjs"}, base},
		{"unknown operation kind", validAttribution, []string{"python", base[1], "--operation-kind", "delete", "--expected-output-count", "2", "--output-format", "pptx"}},
		{"zero expected output count", validAttribution, []string{"python", base[1], "--operation-kind", "create", "--expected-output-count", "0", "--output-format", "pptx"}},
		{"count above max", validAttribution, []string{"python", base[1], "--operation-kind", "create", "--expected-output-count", "101", "--output-format", "pptx"}},
		{"unknown output format", validAttribution, []string{"python", base[1], "--operation-kind", "create", "--expected-output-count", "2", "--output-format", "exe"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RecognizeArtifactOperation(tc.attribution, tc.command); got != nil {
				t.Fatalf("recognized = %+v, want nil", got)
			}
		})
	}
}
