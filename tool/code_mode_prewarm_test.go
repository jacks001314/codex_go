package tool

import (
	"context"
	"testing"
)

type prewarmRemoteProvider struct {
	prewarmCalls int
}

func (p *prewarmRemoteProvider) NewSession(CodeModeRemoteDelegate) CodeModeRemoteSession {
	return (&prewarmRemoteSession{provider: p}).session()
}

func (p *prewarmRemoteProvider) Prewarm(context.Context) error {
	p.prewarmCalls++
	return nil
}

type prewarmRemoteSession struct {
	provider *prewarmRemoteProvider
}

func (s *prewarmRemoteSession) session() CodeModeRemoteSession { return s }
func (*prewarmRemoteSession) Execute(context.Context, CodeModeRemoteExecuteRequest, *YieldSignal) (CodeModeRemoteResponse, error) {
	return CodeModeRemoteResponse{}, nil
}
func (*prewarmRemoteSession) Wait(context.Context, string, uint64, *YieldSignal) (CodeModeRemoteResponse, error) {
	return CodeModeRemoteResponse{}, nil
}
func (*prewarmRemoteSession) Terminate(context.Context, string) (CodeModeRemoteResponse, error) {
	return CodeModeRemoteResponse{}, nil
}
func (*prewarmRemoteSession) Close() error { return nil }

func TestCodeModeRuntimePrewarmsSharedHostLikeRust(t *testing.T) {
	provider := &prewarmRemoteProvider{}
	runtime := NewCodeModeRuntime(provider, false)
	if err := runtime.Prewarm(context.Background()); err != nil {
		t.Fatalf("Prewarm() error = %v", err)
	}
	if provider.prewarmCalls != 1 {
		t.Fatalf("prewarm calls = %d, want 1", provider.prewarmCalls)
	}
}
