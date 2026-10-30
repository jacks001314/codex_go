package codemode

import (
	"fmt"

	"codex_go/tool"
)

// CreateSessionWithOptions creates a session that honors the requested session
// options. Providers that cannot negotiate options keep client-side output
// truncation; asking such a provider for server truncation fails instead of
// silently narrowing the request (Rust
// CodeModeSessionProvider::create_session_with_options, #58125).
func CreateSessionWithOptions(provider tool.CodeModeRemoteProvider, delegate tool.CodeModeRemoteDelegate, options SessionOptions) (tool.CodeModeRemoteSession, error) {
	if provider == nil {
		return nil, fmt.Errorf("code-mode session provider is nil")
	}
	if err := ValidateSessionOptions(options); err != nil {
		return nil, err
	}
	if optionsProvider, ok := provider.(tool.CodeModeRemoteSessionOptionsProvider); ok {
		return optionsProvider.NewSessionWithOptions(delegate, tool.CodeModeSessionOptions{
			CellExecutionLimits: cellExecutionLimitsFromSessionOptions(options),
			OutputTruncation:    tool.CodeModeOutputTruncation(options.OutputTruncation),
		}), nil
	}
	if options.OutputTruncation == OutputTruncationServer {
		return nil, fmt.Errorf("code-mode session provider does not support server output truncation")
	}
	return provider.NewSession(delegate), nil
}

// sessionOptionsFromTool maps the tool-package options shared with Core onto
// the protocol-level session options.
func sessionOptionsFromTool(options tool.CodeModeSessionOptions) SessionOptions {
	out := SessionOptions{OutputTruncation: OutputTruncation(options.OutputTruncation)}
	if limits := options.CellExecutionLimits; limits != nil {
		out.CellExecutionLimits = &CellExecutionLimits{MaxYieldTimeMS: limits.MaxYieldTimeMS, MaxHeapSizeBytes: limits.MaxHeapSizeBytes}
	}
	return out
}

func cellExecutionLimitsFromSessionOptions(options SessionOptions) *tool.CodeModeCellExecutionLimits {
	limits := options.CellExecutionLimits
	if limits == nil || limits.IsZero() {
		return nil
	}
	return &tool.CodeModeCellExecutionLimits{MaxYieldTimeMS: limits.MaxYieldTimeMS, MaxHeapSizeBytes: limits.MaxHeapSizeBytes}
}
