package codemode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"codex_go/envutil"
	"codex_go/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// grpcHostIdleGrace is Rust's HOST_IDLE_GRACE (#63537): a host stays alive
// this long after its last session lease and operation RPC drain, so nearby
// sessions can reuse the process before it is released.
var grpcHostIdleGrace = 5 * time.Second

// GrpcProcessProvider mirrors Rust's ProcessOwnedGrpcCodeModeSessionProvider
// (#52723): one lazy host process serves a shared HTTP/2 channel, and sessions
// stay isolated. A dead or unresponsive host is reaped and respawned on the
// next connect.
//
// Rust #63537 keeps the host alive while session leases or operation RPCs are
// in flight and retires it after grpcHostIdleGrace once they drain. Metrics
// sampling only observes a running host and never extends its life.
type GrpcProcessProvider struct {
	program     string
	completions *completionWindow

	mu                 sync.Mutex
	connection         *remoteConnection
	grpcConn           *grpc.ClientConn
	process            *exec.Cmd
	stdin              io.WriteCloser
	stdout             io.ReadCloser
	nextSession        atomic.Uint64
	nextCellGeneration atomic.Uint64
	warned             atomic.Bool
	available          error

	// leases counts the session leases and in-flight operation RPCs that keep
	// the current host alive; hostEpoch identifies the generation an idle
	// timer may retire.
	leases    int
	hostEpoch uint64
	idleTimer *time.Timer
}

func NewGrpcProcessProvider(program string) *GrpcProcessProvider {
	provider := &GrpcProcessProvider{program: program, completions: newCompletionWindow()}
	if stat, err := os.Stat(program); err != nil || !stat.Mode().IsRegular() {
		provider.available = fmt.Errorf("failed to spawn code-mode host %s: host executable was not found", boundedHostProgram(program))
	}
	return provider
}

func (p *GrpcProcessProvider) completionWindow() *completionWindow {
	if p == nil {
		return nil
	}
	return p.completions
}

func (p *GrpcProcessProvider) Availability() error {
	if p == nil {
		return errors.New("code-mode gRPC process provider is nil")
	}
	return p.available
}

func (p *GrpcProcessProvider) TakeUnavailableWarning(effectiveToolMode string) string {
	if p == nil || p.available == nil || p.warned.Swap(true) {
		return ""
	}
	return unavailableWarning(p.available, effectiveToolMode)
}

func (p *GrpcProcessProvider) NewSession(delegate tool.CodeModeRemoteDelegate) tool.CodeModeRemoteSession {
	return p.NewSessionWithOptions(delegate, tool.CodeModeSessionOptions{})
}

// NewSessionWithOptions honors the requested session options (Rust
// CodeModeSessionProvider::create_session_with_options, #58125).
func (p *GrpcProcessProvider) NewSessionWithOptions(delegate tool.CodeModeRemoteDelegate, options tool.CodeModeSessionOptions) tool.CodeModeRemoteSession {
	value := p.nextSession.Add(1)
	return &remoteSession{provider: p, delegate: delegate, id: SessionID(fmt.Sprintf("session-%d", value)), options: sessionOptionsFromTool(options)}
}

// nextGeneration keeps a reloaded thread's replacement session from reusing
// cell IDs owned by its previous session (Rust #57615).
func (p *GrpcProcessProvider) nextGeneration() uint64 {
	return p.nextCellGeneration.Add(1)
}

func (p *GrpcProcessProvider) Prewarm(ctx context.Context) error {
	_, err := p.connect(ctx)
	return err
}

func (p *GrpcProcessProvider) connect(ctx context.Context) (*remoteConnection, error) {
	if p == nil {
		return nil, errors.New("code-mode gRPC process provider is nil")
	}
	if p.available != nil {
		return nil, p.available
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.connection != nil && p.connection.Alive() {
		return p.connection, nil
	}
	p.stopLocked()
	stdin, stdout, cmd, err := spawnGrpcStdioProcess(p.program)
	if err != nil {
		return nil, err
	}
	ensureGrpcZstdRegistered()
	handshakeCtx, cancel := context.WithTimeout(ctx, localHostStartupTimeout)
	defer cancel()
	conn := newStdioConn(stdout, stdin)
	options := []grpc.DialOption{
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return conn, nil }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(ProtocolMaxFrameBytes),
			grpc.MaxCallSendMsgSize(ProtocolMaxFrameBytes),
		),
		// Rust #52723 uses HTTP/2 keepalives to detect an unresponsive host.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	}
	grpcConn, err := grpc.DialContext(handshakeCtx, "passthrough:///codex-code-mode-stdio", options...)
	if err != nil {
		_ = conn.Close()
		reapProcess(cmd)
		return nil, err
	}
	// The Transport stream must outlive the handshake context; only the dial
	// itself is bounded by localHostStartupTimeout.
	stream, err := codemodev1.NewCodeModeHostClient(grpcConn).Transport(context.Background())
	if err != nil {
		_ = grpcConn.Close()
		_ = conn.Close()
		reapProcess(cmd)
		return nil, fmt.Errorf("failed to open code-mode gRPC transport stream: %w", err)
	}
	transport := NewGrpcTransport(stream, grpcConn)
	connection, err := connectRemoteTransport(handshakeCtx, transport)
	if err != nil {
		_ = transport.Close()
		_ = conn.Close()
		reapProcess(cmd)
		return nil, err
	}
	// A fresh host owns a new generation, so an idle timer armed for the
	// previous process can no longer retire it.
	p.hostEpoch++
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}
	p.connection = connection
	p.grpcConn = grpcConn
	p.process = cmd
	p.stdin = stdin
	p.stdout = stdout
	return connection, nil
}

// retainHostActivity keeps the current host alive until the returned release
// runs (Rust #63537's session lease / operation RPC guard). Overlapping holders
// share the host; the grace period starts when the last one releases.
func (p *GrpcProcessProvider) retainHostActivity() func() {
	if p == nil {
		return func() {}
	}
	p.mu.Lock()
	p.leases++
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(p.releaseHostActivity)
	}
}

func (p *GrpcProcessProvider) releaseHostActivity() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.leases > 0 {
		p.leases--
	}
	if p.leases == 0 {
		p.armIdleRetirementLocked()
	}
}

// armIdleRetirementLocked starts the grace timer for the current host
// generation. A timer that fires after new activity or after a fresh host
// spawned finds a different lease count or epoch and leaves that host alone.
func (p *GrpcProcessProvider) armIdleRetirementLocked() {
	if p.connection == nil {
		return
	}
	epoch := p.hostEpoch
	if p.idleTimer != nil {
		p.idleTimer.Stop()
	}
	p.idleTimer = time.AfterFunc(grpcHostIdleGrace, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.leases != 0 || p.hostEpoch != epoch {
			return
		}
		p.idleTimer = nil
		_ = p.stopLocked()
	})
}

func (p *GrpcProcessProvider) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}
	return p.stopLocked()
}

func (p *GrpcProcessProvider) stopLocked() error {
	var closeErr error
	if p.connection != nil {
		closeErr = p.connection.Close()
		p.connection = nil
	}
	if p.grpcConn != nil {
		_ = p.grpcConn.Close()
		p.grpcConn = nil
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
	if p.stdout != nil {
		_ = p.stdout.Close()
		p.stdout = nil
	}
	if p.process != nil {
		reapProcess(p.process)
		p.process = nil
	}
	return closeErr
}

// sessionAdmissionRetries mirrors Rust #51185 for the gRPC transport.
func (*GrpcProcessProvider) sessionAdmissionRetries() int {
	return grpcSessionAdmissionAttempts
}

func spawnGrpcStdioProcess(program string) (io.WriteCloser, io.ReadCloser, *exec.Cmd, error) {
	// Rust's HostConnection::spawn always asks the host for the gRPC stdio
	// transport (`--listen grpc+stdio://`); the host's default listens for the
	// framed JSON protocol instead.
	cmd := exec.Command(program, "--listen", "grpc+stdio://")
	// Rust c4513cb982: remote helper processes must not inherit Codex launch
	// context.
	envutil.ScrubCommandEnv(cmd)
	// Rust #48138: launching the host must not create a console window.
	suppressCodeModeHostConsoleWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("spawned code-mode gRPC host has no stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, fmt.Errorf("spawned code-mode gRPC host has no stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, fmt.Errorf("spawned code-mode gRPC host has no stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to spawn code-mode host %s: %w", boundedHostProgram(program), err)
	}
	go func() { _, _ = io.Copy(os.Stderr, stderr) }()
	return stdin, stdout, cmd, nil
}

func reapProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
