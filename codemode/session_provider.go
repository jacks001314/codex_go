package codemode

import (
	"fmt"

	"codex_go/tool"
)

// CreateSessionWithOptions creates a session that honors the requested session
// options. Providers keep client-side output truncation unless they advertise
// bounded-output-v1 support, so server truncation is rejected by default
// (Rust CodeModeSessionProvider::create_session_with_options, #57272).
//
// Cell execution limits travel on session/open in Go hosts, so providers only
// receive the truncation setting here.
func CreateSessionWithOptions(provider tool.CodeModeRemoteProvider, delegate tool.CodeModeRemoteDelegate, options SessionOptions) (tool.CodeModeRemoteSession, error) {
	if provider == nil {
		return nil, fmt.Errorf("code-mode session provider is nil")
	}
	if err := ValidateSessionOptions(options); err != nil {
		return nil, err
	}
	return provider.NewSession(delegate), nil
}
