package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/tool"
)

type recordingCodeModePrewarmProvider struct {
	prewarmed chan struct{}
}

func (p *recordingCodeModePrewarmProvider) NewSession(tool.CodeModeRemoteDelegate) tool.CodeModeRemoteSession {
	return recordingCodeModePrewarmSession{}
}

func (p *recordingCodeModePrewarmProvider) Prewarm(context.Context) error {
	select {
	case p.prewarmed <- struct{}{}:
	default:
	}
	return nil
}

type recordingCodeModePrewarmSession struct{}

func (recordingCodeModePrewarmSession) Execute(context.Context, tool.CodeModeRemoteExecuteRequest, *tool.YieldSignal) (tool.CodeModeRemoteResponse, error) {
	return tool.CodeModeRemoteResponse{}, nil
}
func (recordingCodeModePrewarmSession) Wait(context.Context, string, uint64, *int, *tool.YieldSignal) (tool.CodeModeRemoteResponse, error) {
	return tool.CodeModeRemoteResponse{}, nil
}
func (recordingCodeModePrewarmSession) Terminate(context.Context, string, *int) (tool.CodeModeRemoteResponse, error) {
	return tool.CodeModeRemoteResponse{}, nil
}
func (recordingCodeModePrewarmSession) Close() error { return nil }

func TestThreadStartPrewarmsCodeModeHostWhenEnabledLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\ncode_mode_prewarm = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &recordingCodeModePrewarmProvider{prewarmed: make(chan struct{}, 1)}
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(home), CodeModeProvider: provider, DefaultCWD: home,
	})
	defer func() { _ = router.Close() }()

	router.scheduleCodeModeHostPrewarm("thread-prewarm", home)
	select {
	case <-provider.prewarmed:
	case <-time.After(2 * time.Second):
		t.Fatal("code-mode host was not prewarmed at thread startup")
	}
}
