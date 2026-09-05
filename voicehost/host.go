package voicehost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	hostHandshakeDeadline             = 5 * time.Second
	hostRuntimeInitializationDeadline = 30 * time.Second
	hostTransportDeadline             = 20 * time.Second
	hostShutdownDeadline              = 5 * time.Second
)

// ErrVoiceHostExited indicates the helper process ended before or while a
// protocol exchange completed.
var ErrVoiceHostExited = errors.New("voice helper exited")

// VoiceHost owns one helper process. Dropping or closing it terminates the
// process. A successful handshake establishes compatibility only, not an active
// audio session.
type VoiceHost struct {
	process *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser

	mu     sync.Mutex
	closed bool
	readMu sync.Mutex
}

// Connect starts the helper and completes the hello/ready handshake. The caller
// owns the returned process and must call Close.
func Connect(ctx context.Context, executable string, buildCommit string) (*VoiceHost, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if executable == "" || buildCommit == "" {
		return nil, errors.New("voice helper executable and build commit are required")
	}
	command := exec.Command(executable)
	configureHiddenProcess(command)
	command.Dir = filepath.Dir(executable)
	command.Env = childEnvironment(os.Environ())
	command.Stderr = io.Discard

	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open voice helper stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open voice helper stdout: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start voice helper: %w", err)
	}
	host := &VoiceHost{process: command, stdin: stdin, stdout: stdout}
	requestContext, cancel := context.WithTimeout(ctx, hostHandshakeDeadline)
	defer cancel()
	response, err := host.request(requestContext, NewHello(1, buildCommit))
	if err != nil {
		host.terminate()
		return nil, err
	}
	if response.Type != TypeReady {
		host.terminate()
		return nil, fmt.Errorf("unexpected voice helper response %q", response.Type)
	}
	return host, nil
}

// ConnectPackage resolves the physical package helper and connects to it.
func ConnectPackage(ctx context.Context, packageDir string, buildCommit string) (*VoiceHost, error) {
	executable, err := resolvePackageExecutable(packageDir)
	if err != nil {
		return nil, err
	}
	return Connect(ctx, executable, buildCommit)
}

// StartTransport asks the helper to create a peer and return its offer. It
// establishes neither connectivity nor audio readiness.
func (h *VoiceHost) StartTransport(ctx context.Context) (SessionDescription, error) {
	response, err := h.exchange(ctx, NewSimpleMessage(TypeStartTransport), hostTransportDeadline)
	if err != nil {
		return SessionDescription{}, err
	}
	if response.Type != TypeOffer || response.SDP == nil {
		return SessionDescription{}, errors.New("unexpected voice helper offer response")
	}
	return *response.SDP, nil
}

// ApplyAnswer sends the remote answer and returns only when the ordered event
// channel opens.
func (h *VoiceHost) ApplyAnswer(ctx context.Context, sdp SessionDescription) error {
	response, err := h.exchange(ctx, NewSDPMessage(TypeApplyAnswer, sdp), hostTransportDeadline)
	if err != nil {
		return err
	}
	if response.Type != TypeTransportReady {
		return fmt.Errorf("unexpected voice helper response %q", response.Type)
	}
	return nil
}

// InitializeRuntime initializes the packaged native runtime without opening
// devices or starting a session.
func (h *VoiceHost) InitializeRuntime(ctx context.Context) error {
	response, err := h.exchange(ctx, NewSimpleMessage(TypeInitializeRuntime), hostRuntimeInitializationDeadline)
	if err != nil {
		return err
	}
	if response.Type != TypeRuntimeReady {
		return fmt.Errorf("unexpected voice helper response %q", response.Type)
	}
	return nil
}

// Close sends close, waits for closed, and reaps the process. If the protocol
// exchange fails, the helper is terminated before returning an error.
func (h *VoiceHost) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	requestContext, cancel := context.WithTimeout(ctx, hostShutdownDeadline)
	defer cancel()
	response, requestErr := h.request(requestContext, NewSimpleMessage(TypeClose))
	if requestErr != nil || response.Type != TypeClosed {
		h.terminate()
		if requestErr != nil {
			return requestErr
		}
		return fmt.Errorf("unexpected voice helper response %q", response.Type)
	}
	_ = h.stdin.Close()
	if err := h.wait(requestContext); err != nil {
		return err
	}
	return nil
}

func (h *VoiceHost) exchange(ctx context.Context, message Message, deadline time.Duration) (Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestContext, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	return h.request(requestContext, message)
}

func (h *VoiceHost) request(ctx context.Context, message Message) (Message, error) {
	if h == nil {
		return Message{}, ErrVoiceHostExited
	}
	h.readMu.Lock()
	defer h.readMu.Unlock()

	if err := WriteMessage(h.stdin, message); err != nil {
		return Message{}, fmt.Errorf("write voice helper request: %w", err)
	}
	type readResult struct {
		message *Message
		err     error
	}
	result := make(chan readResult, 1)
	go func() {
		response, err := ReadMessage(h.stdout)
		result <- readResult{message: response, err: err}
	}()
	select {
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case response := <-result:
		if response.err != nil {
			return Message{}, fmt.Errorf("read voice helper response: %w", response.err)
		}
		if response.message == nil {
			return Message{}, ErrVoiceHostExited
		}
		return *response.message, nil
	}
}

func (h *VoiceHost) wait(ctx context.Context) error {
	if h == nil || h.process == nil {
		return nil
	}
	waitResult := make(chan error, 1)
	go func() { waitResult <- h.process.Wait() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-waitResult:
		if err == nil {
			return nil
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() != 0 {
			return fmt.Errorf("%w: %v", ErrVoiceHostExited, err)
		}
		return err
	}
}

func (h *VoiceHost) terminate() {
	if h == nil {
		return
	}
	_ = h.stdin.Close()
	if h.process != nil && h.process.Process != nil {
		_ = h.process.Process.Kill()
	}
	// Reap the killed process so it cannot become a zombie.
	if h.process != nil {
		go func() { _ = h.process.Wait() }()
	}
}

func resolvePackageExecutable(packageDir string) (string, error) {
	root, err := filepath.Abs(packageDir)
	if err != nil {
		return "", fmt.Errorf("resolve voice package: %w", err)
	}
	name := "codex-voice-host"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(root, "codex-resources", "voice", "bin", name)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("voice helper must be inside the physical package: %w", err)
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if absolute != path {
		return "", errors.New("voice helper must be inside the physical package")
	}
	return absolute, nil
}

func childEnvironment(parent []string) []string {
	allowed := map[string]bool{
		"SYSTEMROOT":               true,
		"WINDIR":                   true,
		"HOME":                     true,
		"USERPROFILE":              true,
		"LOCALAPPDATA":             true,
		"APPDATA":                  true,
		"TEMP":                     true,
		"TMP":                      true,
		"TMPDIR":                   true,
		"XDG_RUNTIME_DIR":          true,
		"PULSE_SERVER":             true,
		"PULSE_COOKIE":             true,
		"PIPEWIRE_REMOTE":          true,
		"DBUS_SESSION_BUS_ADDRESS": true,
		"HTTP_PROXY":               true,
		"HTTPS_PROXY":              true,
		"ALL_PROXY":                true,
		"NO_PROXY":                 true,
		"SSL_CERT_FILE":            true,
		"SSL_CERT_DIR":             true,
		"REQUESTS_CA_BUNDLE":       true,
		"CURL_CA_BUNDLE":           true,
	}
	filtered := make([]string, 0, len(parent)+len(RuntimeEnvironment()))
	for _, entry := range parent {
		key, _, ok := strings.Cut(entry, "=")
		if ok && allowed[strings.ToUpper(key)] {
			filtered = append(filtered, entry)
		}
	}
	for _, entry := range RuntimeEnvironment() {
		filtered = append(filtered, entry[0]+"="+entry[1])
	}
	return filtered
}
