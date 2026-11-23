package tool

import (
	"fmt"

	"github.com/grafana/sobek"
)

// maxCodeModeToolPreloadBindingChars bounds the binding name echoed in preload
// errors, mirroring Rust #62430 MAX_PRELOAD_BINDING_CHARS.
const maxCodeModeToolPreloadBindingChars = 96

// CodeModeToolPreload is trusted JavaScript a Code Mode host installs in cells
// that expose the named nested tool (Rust #62430 ToolPreload). The codemode host
// adapts its session table into this type so the executor can evaluate the
// helper in the cell's own runtime before the cell's script.
type CodeModeToolPreload struct {
	// Tool is the binding's name on the `tools` object.
	Tool string
	// ToolName is the exact name dispatched by that binding.
	ToolName string
	// ToolNamespace is the exact namespace dispatched by that binding, if any.
	// The tool registry spells "no namespace" as an empty string.
	ToolNamespace *string
	// Source is a script evaluated before the cell's script.
	Source string
}

func codeModeToolPreloadLabel(index int, toolKey string) string {
	binding := toolKey
	if runes := []rune(binding); len(runes) > maxCodeModeToolPreloadBindingChars {
		binding = string(runes[:maxCodeModeToolPreloadBindingChars])
	}
	return fmt.Sprintf("tool preload #%d for `%s`", index+1, binding)
}

// installCodeModeToolPreloads mirrors Rust #62430 install_globals for the
// in-process nested-tool runtime: an ambiguous binding fails the cell, a helper
// is skipped unless the unique matching binding dispatches the expected tool
// name and namespace, and compile/execute failures surface as bounded labels.
func installCodeModeToolPreloads(runtime *sobek.Runtime, nested []codeModeNestedTool, preloads []CodeModeToolPreload) error {
	if runtime == nil || len(preloads) == 0 {
		return nil
	}
	for index, preload := range preloads {
		label := codeModeToolPreloadLabel(index, preload.Tool)
		matches := 0
		enabled := false
		for _, candidate := range nested {
			if candidate.globalName != preload.Tool {
				continue
			}
			matches++
			if matches > 1 {
				return fmt.Errorf("ambiguous binding: %s", label)
			}
			namespace := ""
			if preload.ToolNamespace != nil {
				namespace = *preload.ToolNamespace
			}
			enabled = candidate.name.Name == preload.ToolName && candidate.name.Namespace == namespace
		}
		if !enabled {
			continue
		}
		program, err := sobek.Compile(label, preload.Source, false)
		if err != nil {
			return fmt.Errorf("failed to compile %s", label)
		}
		if _, err := runtime.RunProgram(program); err != nil {
			return fmt.Errorf("failed to execute %s", label)
		}
	}
	return nil
}

// codeModeToolPreloadsForInvocation reads the host's preload table from the
// invocation context. A host that supplies none keeps the zero table.
func codeModeToolPreloadsForInvocation(invocation *Invocation) []CodeModeToolPreload {
	if invocation == nil || invocation.Context == nil {
		return nil
	}
	preloads, _ := invocation.Context[CodeModeToolPreloadsContextKey].([]CodeModeToolPreload)
	return preloads
}
