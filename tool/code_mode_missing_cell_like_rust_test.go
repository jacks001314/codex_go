package tool

import (
	"context"
	"strings"
	"testing"
)

// Rust's code-mode wait/terminate on an unknown or already-closed cell is a
// normal failed observation: `missing_cell_response` builds a Result whose
// error text is `exec cell <id> not found`, and `handle_runtime_response`
// renders it as a `Script error:` block under the "Script failed" status. It is
// never a model-facing tool error.
func TestCodeModeMissingCellIsAFailedObservationLikeRust(t *testing.T) {
	registry := NewRegistry()
	_, wait := NewCodeModeExecutors(registry)
	cases := []struct {
		name      string
		arguments string
	}{
		{name: "wait", arguments: `{"cell_id":"ghost"}`},
		{name: "terminate", arguments: `{"cell_id":"ghost","terminate":true}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			output, err := wait.Execute(context.Background(), &Invocation{
				CallID:  "missing-" + testCase.name,
				Payload: Payload{Kind: PayloadFunction, Arguments: testCase.arguments},
			})
			if err != nil {
				t.Fatalf("missing cell returned a tool error: %v", err)
			}
			if output == nil || output.Success {
				t.Fatalf("output = %#v", output)
			}
			if !strings.Contains(output.Body, "Script failed") {
				t.Fatalf("body lacks the failed status: %q", output.Body)
			}
			if !strings.Contains(output.Body, "Script error:\nexec cell ghost not found") {
				t.Fatalf("body lacks the Script error block: %q", output.Body)
			}
			if got := output.Data["error_text"]; got != "exec cell ghost not found" {
				t.Fatalf("error_text = %#v", got)
			}
			if got := output.Data["cell_id"]; got != "ghost" {
				t.Fatalf("cell_id = %#v", got)
			}
		})
	}
}
