package codemode

// Wrapper that requests server-side output truncation from Core's built-in
// code-mode host providers.
//
// Rust parity: codex-rs/core/src/code_mode_host.rs::LocalServerOutputProvider
// (#58159). Injected providers own their session settings, so only the
// process-owned stdio/gRPC providers are wrapped.

import (
	"context"

	"codex_go/tool"
)

// LocalServerOutputProvider forces the host, rather than the client, to apply
// observation output budgets. Explicit session options pass through unchanged.
type LocalServerOutputProvider struct {
	Inner tool.CodeModeRemoteProvider
}

// NewLocalServerOutputProvider wraps a built-in provider; a nil inner provider
// is returned unchanged so callers keep the disabled behaviour.
func NewLocalServerOutputProvider(inner tool.CodeModeRemoteProvider) tool.CodeModeRemoteProvider {
	if inner == nil {
		return nil
	}
	return &LocalServerOutputProvider{Inner: inner}
}

func (p *LocalServerOutputProvider) NewSession(delegate tool.CodeModeRemoteDelegate) tool.CodeModeRemoteSession {
	return p.NewSessionWithOptions(delegate, tool.CodeModeSessionOptions{OutputTruncation: tool.CodeModeOutputTruncationServer})
}

func (p *LocalServerOutputProvider) NewSessionWithOptions(delegate tool.CodeModeRemoteDelegate, options tool.CodeModeSessionOptions) tool.CodeModeRemoteSession {
	if optionsProvider, ok := p.Inner.(tool.CodeModeRemoteSessionOptionsProvider); ok {
		return optionsProvider.NewSessionWithOptions(delegate, options)
	}
	return p.Inner.NewSession(delegate)
}

func (p *LocalServerOutputProvider) Availability() error {
	if provider, ok := p.Inner.(tool.CodeModeRemoteAvailabilityProvider); ok {
		return provider.Availability()
	}
	return nil
}

func (p *LocalServerOutputProvider) TakeUnavailableWarning(effectiveToolMode string) string {
	if provider, ok := p.Inner.(interface {
		TakeUnavailableWarning(string) string
	}); ok {
		return provider.TakeUnavailableWarning(effectiveToolMode)
	}
	return ""
}

func (p *LocalServerOutputProvider) Prewarm(ctx context.Context) error {
	if provider, ok := p.Inner.(tool.CodeModeRemotePrewarmer); ok {
		return provider.Prewarm(ctx)
	}
	return nil
}

func (p *LocalServerOutputProvider) Close() error {
	if closer, ok := p.Inner.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
