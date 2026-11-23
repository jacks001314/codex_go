package codemode

import (
	"fmt"

	"github.com/grafana/sobek"

	"codex_go/tool"
)

// maxToolPreloadBindingChars bounds the binding name echoed in preload errors,
// mirroring Rust #62430 MAX_PRELOAD_BINDING_CHARS so a hostile or very long
// tool name cannot grow the reported cell error without limit.
const maxToolPreloadBindingChars = 96

// ToolPreload is trusted JavaScript a session installs in cells that expose the
// named nested tool (Rust #62430 code-mode-runtime ToolPreload). The helper runs
// in the same JavaScript context as the cell's module, before the module itself,
// and only when the cell's catalog exposes exactly one binding with the expected
// tool name and namespace.
type ToolPreload struct {
	// Tool is the binding's name on the Code Mode `tools` object.
	Tool string
	// ToolName is the exact name dispatched by that binding.
	ToolName string
	// ToolNamespace is the exact namespace dispatched by that binding, if any.
	ToolNamespace *string
	// Source is a script evaluated before the cell's module.
	Source string
}

func cloneToolPreloads(values []ToolPreload) []ToolPreload {
	if values == nil {
		return nil
	}
	cloned := make([]ToolPreload, len(values))
	for index, value := range values {
		cloned[index] = value
		if value.ToolNamespace != nil {
			namespace := *value.ToolNamespace
			cloned[index].ToolNamespace = &namespace
		}
	}
	return cloned
}

// toolPreloadLabel renders the bounded error label Rust uses for a preload
// entry: `tool preload #<1-based index> for `<binding>“.
func toolPreloadLabel(index int, tool string) string {
	binding := tool
	if runes := []rune(binding); len(runes) > maxToolPreloadBindingChars {
		binding = string(runes[:maxToolPreloadBindingChars])
	}
	return fmt.Sprintf("tool preload #%d for `%s`", index+1, binding)
}

// runToolPreloads evaluates the session's trusted helpers against one cell's
// catalog. It mirrors Rust #62430 install_globals: an ambiguous binding fails
// the cell, a helper is skipped unless the unique matching binding dispatches
// the expected tool name and namespace, and compile/execute failures surface as
// bounded labels instead of raw script diagnostics.
func runToolPreloads(runtime *sobek.Runtime, enabledTools []ProtocolToolDefinition, preloads []ToolPreload) error {
	if runtime == nil || len(preloads) == 0 {
		return nil
	}
	for index, preload := range preloads {
		label := toolPreloadLabel(index, preload.Tool)
		matches := 0
		enabled := false
		for _, definition := range enabledTools {
			metadata := EnabledMetadata(definition)
			if metadata.GlobalName != preload.Tool {
				continue
			}
			matches++
			if matches > 1 {
				return fmt.Errorf("ambiguous binding: %s", label)
			}
			enabled = metadata.ToolName.Name == preload.ToolName &&
				toolNamespaceMatches(metadata.ToolName.Namespace, preload.ToolNamespace)
		}
		if !enabled {
			continue
		}
		if err := runToolPreloadSource(runtime, preload.Source, label); err != nil {
			return err
		}
	}
	return nil
}

func toolNamespaceMatches(got *string, want *string) bool {
	if (got == nil) != (want == nil) {
		return false
	}
	if got == nil {
		return true
	}
	return *got == *want
}

// toolPreloadsForHost adapts the session table to the executor's context type.
func toolPreloadsForHost(values []ToolPreload) []tool.CodeModeToolPreload {
	if len(values) == 0 {
		return nil
	}
	out := make([]tool.CodeModeToolPreload, 0, len(values))
	for _, value := range values {
		converted := tool.CodeModeToolPreload{Tool: value.Tool, ToolName: value.ToolName, Source: value.Source}
		if value.ToolNamespace != nil {
			namespace := *value.ToolNamespace
			converted.ToolNamespace = &namespace
		}
		out = append(out, converted)
	}
	return out
}

func runToolPreloadSource(runtime *sobek.Runtime, source string, label string) error {
	program, err := sobek.Compile(label, source, false)
	if err != nil {
		return fmt.Errorf("failed to compile %s", label)
	}
	if _, err := runtime.RunProgram(program); err != nil {
		return fmt.Errorf("failed to execute %s", label)
	}
	return nil
}
