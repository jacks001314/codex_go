package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"codex_go/envutil"
	"codex_go/tool"
)

const processHostHandshakeTimeout = 10 * time.Second

type ProcessProvider struct {
	program string

	mu          sync.Mutex
	connection  *remoteConnection
	nextSession atomic.Uint64
	warned      atomic.Bool
	available   error
}

func NewProcessProvider(program string) *ProcessProvider {
	provider := &ProcessProvider{program: program}
	if stat, err := os.Stat(program); err != nil || !stat.Mode().IsRegular() {
		provider.available = fmt.Errorf("failed to spawn code-mode host %s: host executable was not found", boundedHostProgram(program))
	}
	return provider
}

func (p *ProcessProvider) Availability() error {
	if p == nil {
		return errors.New("code-mode process provider is nil")
	}
	return p.available
}

func (p *ProcessProvider) TakeUnavailableWarning(effectiveToolMode string) string {
	if p == nil || p.available == nil || p.warned.Swap(true) {
		return ""
	}
	return unavailableWarning(p.available, effectiveToolMode)
}

func (p *ProcessProvider) NewSession(delegate tool.CodeModeRemoteDelegate) tool.CodeModeRemoteSession {
	value := p.nextSession.Add(1)
	return &remoteSession{provider: p, delegate: delegate, id: SessionID(fmt.Sprintf("session-%d", value))}
}

func (p *ProcessProvider) connect(ctx context.Context) (*remoteConnection, error) {
	if p == nil {
		return nil, errors.New("code-mode process provider is nil")
	}
	if p.available != nil {
		return nil, p.available
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.connection != nil && p.connection.Alive() {
		return p.connection, nil
	}
	transport, err := spawnProcessTransport(p.program)
	if err != nil {
		return nil, err
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, processHostHandshakeTimeout)
	defer cancel()
	connection, err := connectRemoteTransport(handshakeCtx, transport)
	if err != nil {
		_ = transport.Close()
		return nil, err
	}
	p.connection = connection
	return connection, nil
}

func (p *ProcessProvider) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	connection := p.connection
	p.connection = nil
	p.mu.Unlock()
	if connection != nil {
		return connection.Close()
	}
	return nil
}

type DisabledProvider struct {
	warned atomic.Bool
}

func NewDisabledProvider() *DisabledProvider { return &DisabledProvider{} }

func (p *DisabledProvider) Availability() error { return errors.New("code-mode host is disabled") }

func (p *DisabledProvider) TakeUnavailableWarning(effectiveToolMode string) string {
	if p == nil || p.warned.Swap(true) {
		return ""
	}
	return unavailableWarning(p.Availability(), effectiveToolMode)
}

func (p *DisabledProvider) NewSession(tool.CodeModeRemoteDelegate) tool.CodeModeRemoteSession {
	return unavailableRemoteSession{err: p.Availability()}
}

func (p *DisabledProvider) Close() error { return nil }

type unavailableRemoteSession struct{ err error }

func (s unavailableRemoteSession) Execute(context.Context, tool.CodeModeRemoteExecuteRequest) (tool.CodeModeRemoteResponse, error) {
	return tool.CodeModeRemoteResponse{}, s.err
}

func (s unavailableRemoteSession) Wait(context.Context, string, uint64) (tool.CodeModeRemoteResponse, error) {
	return tool.CodeModeRemoteResponse{}, s.err
}

func (s unavailableRemoteSession) Terminate(context.Context, string) (tool.CodeModeRemoteResponse, error) {
	return tool.CodeModeRemoteResponse{}, s.err
}

func (unavailableRemoteSession) Close() error { return nil }

func unavailableWarning(err error, effectiveToolMode string) string {
	behavior := "Code mode will fail closed"
	if effectiveToolMode == "direct" {
		behavior = "Falling back to direct tools"
	}
	return fmt.Sprintf("Code Mode is unavailable because %v. %s; enable `features.code_mode_host` and install `codex-code-mode-host`.", err, behavior)
}

func boundedHostProgram(program string) string {
	const maxBytes = 512
	const prefix = "..."
	if len(program) <= maxBytes {
		return program
	}
	start := len(program) - (maxBytes - len(prefix))
	for start < len(program) && program[start]&0xc0 == 0x80 {
		start++
	}
	return prefix + program[start:]
}

type processReadResult struct {
	payload json.RawMessage
	ok      bool
	err     error
}

type processTransport struct {
	stdin  io.WriteCloser
	readCh chan processReadResult
	waitCh chan error
	cmd    *exec.Cmd

	writeMu   sync.Mutex
	closeOnce sync.Once
}

func spawnProcessTransport(program string) (*processTransport, error) {
	cmd := exec.Command(program)
	// Rust c4513cb982: remote helper processes must not inherit Codex launch
	// context (OPENAI_FEDERATION_RULE_ID / OPENAI_IDENTITY_TOKEN_FILE).
	envutil.ScrubCommandEnv(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("spawned code-mode host has no stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("spawned code-mode host has no stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("spawned code-mode host has no stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to spawn code-mode host %s: %w", boundedHostProgram(program), err)
	}
	transport := &processTransport{stdin: stdin, readCh: make(chan processReadResult, 1), waitCh: make(chan error, 1), cmd: cmd}
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()
	go transport.readLoop(stdout)
	go func() { transport.waitCh <- cmd.Wait() }()
	return transport, nil
}

func (t *processTransport) readLoop(stdout io.Reader) {
	reader := NewFramedReader(stdout)
	for {
		var payload json.RawMessage
		ok, err := reader.Read(&payload)
		t.readCh <- processReadResult{payload: payload, ok: ok, err: err}
		if err != nil || !ok {
			close(t.readCh)
			return
		}
	}
}

func (t *processTransport) Read(ctx context.Context, target any) (bool, error) {
	if t == nil {
		return false, errors.New("code-mode process transport is nil")
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case result, ok := <-t.readCh:
		if !ok {
			return false, nil
		}
		if result.err != nil || !result.ok {
			return result.ok, result.err
		}
		if err := json.Unmarshal(result.payload, target); err != nil {
			return false, fmt.Errorf("failed to decode code-mode IPC frame: %w", err)
		}
		return true, nil
	}
}

func (t *processTransport) Write(ctx context.Context, message any) error {
	if t == nil || t.stdin == nil {
		return errors.New("code-mode process transport is nil")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return NewFramedWriter(t.stdin).Write(message)
}

func (t *processTransport) Close() error {
	if t == nil {
		return nil
	}
	t.closeOnce.Do(func() {
		_ = t.stdin.Close()
		if t.cmd != nil && t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
	})
	select {
	case err := <-t.waitCh:
		if err != nil && !stringsContainsProcessExit(err.Error()) {
			return err
		}
		return nil
	case <-time.After(WebSocketCloseTimeout):
		return errors.New("timed out closing code-mode host process")
	}
}

func stringsContainsProcessExit(message string) bool {
	return message == "signal: killed" || message == "signal: terminated" || message == "exit status 1"
}
