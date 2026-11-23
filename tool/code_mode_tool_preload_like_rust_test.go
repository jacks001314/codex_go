package tool

import (
	"context"
	"strings"
	"testing"
)

func toolPreloadRegistry(t *testing.T, specs ...Spec) *Registry {
	t.Helper()
	registry := NewRegistry()
	for _, spec := range specs {
		if err := registry.Register(NewExecutorFunc(spec, noopExecutor)); err != nil {
			t.Fatalf("Register(%s) error = %v", spec.Name.Key(), err)
		}
	}
	return registry
}

// Mirrors Rust #62430: a trusted host helper runs in the cell's own context
// before the cell script, and only when the cell exposes the matching binding.
func TestCodeModeToolPreloadsInstallBeforeCellScriptLikeRust(t *testing.T) {
	registry := toolPreloadRegistry(t, Spec{Name: PlainName("lookup")})
	executor := NewCodeModeExecExecutor(registry)
	run := func(preloads []CodeModeToolPreload, source string) *Output {
		t.Helper()
		output, err := executor.Execute(context.Background(), &Invocation{
			CallID:  "preload",
			Payload: Payload{Kind: PayloadCustom, Input: source},
			Context: map[string]any{CodeModeToolPreloadsContextKey: preloads},
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		return output
	}

	output := run([]CodeModeToolPreload{{
		Tool: "lookup", ToolName: "lookup",
		Source: `globalThis.findItem = () => "installed";`,
	}}, `text(typeof findItem === "function" ? findItem() : typeof findItem);`)
	if body := codeModeBodyAfterHeader(t, output.Body, "Script completed"); body != "installed" {
		t.Fatalf("cell body = %q, want installed", body)
	}

	// Each cell gets a fresh runtime, so the helper is not visible without the
	// preload table.
	output = run(nil, `text(typeof findItem);`)
	if body := codeModeBodyAfterHeader(t, output.Body, "Script completed"); body != "undefined" {
		t.Fatalf("cell body without preloads = %q, want undefined", body)
	}

	output = run([]CodeModeToolPreload{{
		Tool: "lookup", ToolName: "unrelated",
		Source: `globalThis.findItem = () => "installed";`,
	}}, `text(typeof findItem);`)
	if body := codeModeBodyAfterHeader(t, output.Body, "Script completed"); body != "undefined" {
		t.Fatalf("cell body with mismatched dispatch = %q, want undefined", body)
	}
}

// Mirrors Rust #62430: an ambiguous binding and bounded helper failures fail the
// cell with the preload label instead of a raw script diagnostic.
func TestCodeModeToolPreloadsReportBoundedFailuresLikeRust(t *testing.T) {
	// The executor's registry catalog already collapses bindings that normalize
	// to one identifier, so it never reports an ambiguous binding; the request
	// catalog behind codemode.SessionRuntime is where Rust #62430's ambiguity
	// check applies (codemode/tool_preload_like_rust_test.go).
	deduped := toolPreloadRegistry(t,
		Spec{Name: PlainName("a-b")},
		Spec{Name: PlainName("a.b")},
	)
	dedupedOutput, err := NewCodeModeExecExecutor(deduped).Execute(context.Background(), &Invocation{
		CallID:  "deduped",
		Payload: Payload{Kind: PayloadCustom, Input: `text(typeof tools.a_b);`},
		Context: map[string]any{CodeModeToolPreloadsContextKey: []CodeModeToolPreload{{
			Tool: "a_b", ToolName: "a-b", Source: `globalThis.preloaded = "yes";`,
		}}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if body := codeModeBodyAfterHeader(t, dedupedOutput.Body, "Script completed"); body != "function" {
		t.Fatalf("deduped catalog body = %q, want a single function binding", body)
	}

	registry := toolPreloadRegistry(t, Spec{Name: PlainName("lookup")})
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "compile", source: "const =", want: "failed to compile tool preload #1 for `lookup`"},
		{name: "execute", source: `throw new Error("boom")`, want: "failed to execute tool preload #1 for `lookup`"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			output, err := NewCodeModeExecExecutor(registry).Execute(context.Background(), &Invocation{
				CallID:  "failure",
				Payload: Payload{Kind: PayloadCustom, Input: `text("never runs")`},
				Context: map[string]any{CodeModeToolPreloadsContextKey: []CodeModeToolPreload{{
					Tool: "lookup", ToolName: "lookup", Source: test.source,
				}}},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if output.Success || !strings.Contains(output.Body, test.want) {
				t.Fatalf("output success=%v body=%q, want %q", output.Success, output.Body, test.want)
			}
		})
	}
}
