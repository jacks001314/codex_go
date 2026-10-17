package prompt

import (
	"strings"
	"testing"
)

// Ports Rust #41744's update_plan_instructions_tests: only known checklist
// sections are omitted; unrelated planning text is preserved.
func TestWithoutUpdatePlanInstructionsLikeRust(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "checklist sections",
			input: "Before.\n\n## Planning\nYou have access to an `update_plan` tool which tracks steps.\n\n### Examples\nKeep steps current.\n\n## Work\nImplement.\n\n## `update_plan`\nUpdate the checklist.\n\n# Next\n## Planning\nDiscuss architecture and inspect update_plan before editing.\n",
			want:  "Before.\n\n## Work\nImplement.\n\n# Next\n## Planning\nDiscuss architecture and inspect update_plan before editing.\n",
		},
		{
			name:  "legacy cross reference",
			input: "# Plan Mode\nKeep planning.\n\n## Plan Mode vs update_plan tool\nThe tools are separate.\n\n## Execution\nDo not edit.\n\n## Plan tool\nWhen using the planning tool:\n- Update steps.\n",
			want:  "# Plan Mode\nKeep planning.\n\n## Execution\nDo not edit.\n\n",
		},
		{
			name:  "list items and goal guidance",
			input: "Keep working.\n- Use the plan tool to explain the work\n    - Skip simple tasks.\n    - Keep steps current.\n- Explain discoveries.\n- If you create a checklist or task list, update its statuses.\n\nProgress visibility:\nIf update_plan is available, use it for complex work.\n\nCompletion:\nVerify the result.\n",
			want:  "Keep working.\n- Explain discoveries.\n\nCompletion:\nVerify the result.\n",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := WithoutUpdatePlanInstructions(test.input); got != test.want {
				t.Fatalf("WithoutUpdatePlanInstructions() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestContinuationWithUpdatePlanLikeRust(t *testing.T) {
	goal := &Goal{Objective: "finish the sync"}
	full := ContinuationWithUpdatePlan(goal, true)
	without := ContinuationWithUpdatePlan(goal, false)
	if !strings.Contains(full, "If update_plan is available") {
		t.Fatal("enabled continuation lost the planning guidance")
	}
	if strings.Contains(without, "If update_plan is available") {
		t.Fatal("disabled continuation kept the planning guidance")
	}
	if !strings.Contains(without, "finish the sync") {
		t.Fatal("disabled continuation lost the objective")
	}
}
