package tool

import (
	"context"
	"testing"
)

// TestCodeModeCatalogLazyGlobalsLikeRust mirrors Rust #61845: each cell builds
// `tools` and `ALL_TOOLS` on first read, owns the mutations made in that cell,
// and defines the catalog without consulting inherited setters.
func TestCodeModeCatalogLazyGlobalsLikeRust(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo"), Description: "Echo"}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "tool-result"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	executor := NewCodeModeExecExecutor(registry)
	run := func(callID string, source string) string {
		t.Helper()
		output, err := executor.Execute(context.Background(), &Invocation{
			CallID:  callID,
			Payload: Payload{Kind: PayloadCustom, Input: source},
		})
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", callID, err)
		}
		if output == nil || !output.Success {
			t.Fatalf("Execute(%s) output = %#v", callID, output)
		}
		return codeModeBodyAfterHeader(t, output.Body, "Script completed")
	}

	mutated := run("cell-a", `
Object.defineProperty(Object.prototype, "echo", { configurable: true, set() { throw new Error("inherited echo setter ran"); } });
Object.defineProperty(Array.prototype, "0", { configurable: true, set() { throw new Error("inherited array setter ran"); } });
text(typeof tools.echo);
text(String(ALL_TOOLS.length));
text(ALL_TOOLS[0].name);
tools.marker = "cell-a";
ALL_TOOLS.marker = "cell-a";
text(String(tools.marker) + "/" + String(ALL_TOOLS.marker));
`)
	if want := "function\n1\necho\ncell-a/cell-a"; mutated != want {
		t.Fatalf("cell-a body = %q, want %q", mutated, want)
	}
	fresh := run("cell-b", `text(String(tools.marker) + "/" + String(ALL_TOOLS.marker));`)
	if want := "undefined/undefined"; fresh != want {
		t.Fatalf("cell-b body = %q, want a fresh catalog (%q)", fresh, want)
	}
}
