package voicehost

// Synchronous facade for one helper-owning session, mirroring the Rust
// codex-realtime-webrtc session actor. Native media stays entirely inside the
// helper process; this layer only orders commands, applies the offer/answer
// handshake, exposes privacy controls, and reports bounded levels.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codex_go/install"
)

const (
	// sessionStartupWait covers the handshake (30s), offer gathering (20s), and
	// overhead.
	sessionStartupWait = 60 * time.Second
	// sessionAnswerWait covers an in-flight answer (20s), device open (5s),
	// controls (5s), and overhead.
	sessionAnswerWait = 40 * time.Second
	// sessionPollInterval is how often the actor samples audio levels, which
	// also detects helper loss while neither device is producing audio.
	sessionPollInterval = 50 * time.Millisecond
	// sessionCommandBuffer bounds ordered commands. Saturation terminates the
	// session instead of silently dropping a privacy transition.
	sessionCommandBuffer = 8

	sessionAnswerTimeout   = 20 * time.Second
	sessionDeviceTimeout   = 5 * time.Second
	sessionControlTimeout  = 5 * time.Second
	sessionShutdownTimeout = 5 * time.Second
)

// ConnectionError classifies a voice session failure. Startup failures that are
// eligible for recovery stay distinct from all other failures.
type ConnectionError int

const (
	// ConnectionNegotiationTimedOut means answer negotiation exceeded its
	// deadline. The helper has retired and the session may be retried.
	ConnectionNegotiationTimedOut ConnectionError = iota
	// ConnectionFailed is the fallback classification.
	ConnectionFailed
	// ConnectionHelperStartup means the helper process could not start.
	ConnectionHelperStartup
	// ConnectionRuntimeInitialization means the audio runtime could not
	// initialize.
	ConnectionRuntimeInitialization
	// ConnectionTransport means the WebRTC transport could not connect.
	ConnectionTransport
	// ConnectionAudioDevices means local devices could not open.
	ConnectionAudioDevices
	// ConnectionAudioControls means a privacy transition failed.
	ConnectionAudioControls
	// ConnectionAudioSession means the audio session stopped unexpectedly.
	ConnectionAudioSession
	// ConnectionShutdown means the helper could not shut down cleanly.
	ConnectionShutdown
)

// Error implements error using the same bounded wording as the Rust facade.
func (e ConnectionError) Error() string {
	switch e {
	case ConnectionNegotiationTimedOut:
		return "voice negotiation timed out"
	case ConnectionHelperStartup:
		return "voice helper could not start"
	case ConnectionRuntimeInitialization:
		return "voice audio runtime could not initialize"
	case ConnectionTransport:
		return "voice transport could not connect"
	case ConnectionAudioDevices:
		return "voice audio devices could not open; check microphone and speaker setup"
	case ConnectionAudioControls:
		return "voice audio controls failed"
	case ConnectionAudioSession:
		return "voice audio session stopped unexpectedly"
	case ConnectionShutdown:
		return "voice helper could not shut down cleanly"
	default:
		return "voice connection failed"
	}
}

// Retryable reports whether a fresh negotiation may succeed after this failure.
func (e ConnectionError) Retryable() bool {
	return e == ConnectionNegotiationTimedOut
}

// reportFailure replaces an untyped failure with its stage, keeping any
// connection classification the lower layer already established.
func reportFailure(stage ConnectionError, err error) error {
	if err == nil {
		return nil
	}
	var connectionErr ConnectionError
	if errors.As(err, &connectionErr) {
		return connectionErr
	}
	return stage
}

// SessionOptions configures a voice session.
type SessionOptions struct {
	// PackageDir is the installed package root that owns
	// codex-resources/voice. It is resolved from the install context when empty.
	PackageDir string
	// Executable overrides the resolved helper path, primarily for tests.
	Executable string
	// BuildCommit is the exact helper build identity. It defaults to the build
	// commit reported by the environment.
	BuildCommit string
}

// buildCommit is the identity stamped by release builders. It must match the
// helper's own stamp, so it deliberately does not read the user's environment.
var buildCommit = "dev"

// SetBuildCommit records the CLI's build identity for the same-build handshake.
// An empty value leaves the current identity unchanged.
func SetBuildCommit(value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		buildCommit = trimmed
	}
}

// DefaultBuildCommit returns the stamped identity, or "dev" for an unstamped
// build.
func DefaultBuildCommit() string {
	return buildCommit
}

// IsSupported reports whether voice can run on this host and package. It
// checks the platform and the installed helper without loading native code or
// touching an audio device. Availability is not proof of runtime integrity,
// device access, or session connectivity.
func IsSupported() bool {
	if !supportedPlatform() {
		return false
	}
	layout := installPackageLayout()
	if layout == nil {
		return false
	}
	return packageHasHelper(layout.PackageDir)
}

// IsSupportedPackage reports whether the named package carries a voice helper.
func IsSupportedPackage(packageDir string) bool {
	return supportedPlatform() && packageHasHelper(packageDir)
}

// supportedPlatform mirrors the platforms whose helper links a native audio
// backend.
func supportedPlatform() bool {
	switch runtime.GOOS {
	case "darwin", "linux":
		return true
	case "windows":
		return true
	default:
		return false
	}
}

// installPackageLayout returns the current install context's package layout.
func installPackageLayout() *install.CodexPackageLayout {
	context := install.Current()
	if context == nil || context.PackageLayout == nil {
		return nil
	}
	return context.PackageLayout
}

// packageHasHelper reports whether the physical package carries the helper.
func packageHasHelper(packageDir string) bool {
	if strings.TrimSpace(packageDir) == "" {
		return false
	}
	name := "codex-voice-host"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(packageDir, "codex-resources", "voice", "bin", name)
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// StartedSession is the successful result of starting a voice session. An offer
// is not proof of connectivity or working devices; callers must apply the
// remote answer before audio flows.
type StartedSession struct {
	OfferSDP string
	Handle   *SessionHandle
}

// sessionHost is the helper surface the session actor drives. *VoiceHost
// implements it; tests substitute a fake so the actor can be exercised without
// spawning a process.
type sessionHost interface {
	InitializeRuntime(ctx context.Context) error
	StartTransport(ctx context.Context) (SessionDescription, error)
	ApplyAnswer(ctx context.Context, sdp SessionDescription) error
	OpenDevices(ctx context.Context) error
	SetAudioControls(ctx context.Context, controls AudioControls) error
	InspectAudio(ctx context.Context) (AudioState, error)
	Close(ctx context.Context) error
}

var _ sessionHost = (*VoiceHost)(nil)

// connectSessionHost opens a helper for one session. Tests replace it to run
// the actor against a fake. It is only called from the startup goroutine.
var connectSessionHost = func(ctx context.Context, executable, buildCommit string) (sessionHost, error) {
	return Connect(ctx, executable, buildCommit)
}

// RealtimeSession starts helper-owned realtime voice sessions. It has no state
// of its own; every call owns one helper process.
type RealtimeSession struct{}

// Start launches the helper, negotiates a transport offer, and returns a handle
// for the caller. Cancel ctx to abandon startup.
func (RealtimeSession) Start(ctx context.Context) (*StartedSession, error) {
	return (RealtimeSession{}).StartWithOptions(ctx, SessionOptions{})
}

// StartWithOptions is Start with explicit package and build identity.
func (RealtimeSession) StartWithOptions(ctx context.Context, options SessionOptions) (*StartedSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	buildCommit := strings.TrimSpace(options.BuildCommit)
	if buildCommit == "" {
		buildCommit = DefaultBuildCommit()
	}
	executable := strings.TrimSpace(options.Executable)
	packageDir := strings.TrimSpace(options.PackageDir)
	if executable == "" {
		if packageDir == "" {
			layout := installPackageLayout()
			if layout == nil {
				return nil, ConnectionFailed
			}
			packageDir = layout.PackageDir
		}
		resolved, err := resolvePackageExecutable(packageDir)
		if err != nil {
			return nil, reportFailure(ConnectionHelperStartup, err)
		}
		executable = resolved
	}

	actorContext, cancel := context.WithCancel(context.Background())
	owner := &sessionOwner{
		commands: make(chan sessionCommand, sessionCommandBuffer),
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	handle := &SessionHandle{owner: owner}
	offer := make(chan sessionOffer, 1)

	go func() {
		defer close(owner.done)
		defer cancel()
		sessionStartup(actorContext, owner, executable, buildCommit, offer)
	}()

	// Abandoning startup must also stop the actor, matching the Rust facade
	// where cancellation owns startup before a handle is returned.
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			handle.Close()
		case <-stopped:
		}
	}()
	defer close(stopped)

	select {
	case result := <-offer:
		if result.err != nil {
			return nil, result.err
		}
		return &StartedSession{OfferSDP: result.sdp, Handle: handle}, nil
	case <-time.After(sessionStartupWait):
		handle.Close()
		return nil, ConnectionHelperStartup
	case <-ctx.Done():
		handle.Close()
		return nil, ConnectionHelperStartup
	}
}

type sessionOffer struct {
	sdp string
	err error
}

// sessionStartup runs the pre-actor stages: connect, initialize, offer.
func sessionStartup(ctx context.Context, owner *sessionOwner, executable, buildCommit string, offer chan<- sessionOffer) {
	host, err := connectSessionHost(ctx, executable, buildCommit)
	if err != nil {
		completeStartup(owner, offer, ConnectionHelperStartup, err)
		return
	}
	if err := host.InitializeRuntime(ctx); err != nil {
		_ = host.Close(context.Background())
		completeStartup(owner, offer, ConnectionRuntimeInitialization, err)
		return
	}
	startupContext, cancel := context.WithTimeout(ctx, hostTransportDeadline)
	defer cancel()
	description, err := host.StartTransport(startupContext)
	if err != nil {
		_ = host.Close(context.Background())
		completeStartup(owner, offer, ConnectionTransport, err)
		return
	}
	offer <- sessionOffer{sdp: description.SDP()}
	owner.run(ctx, host)
}

// completeStartup reports a startup failure to the caller and records it for
// later inspection.
func completeStartup(owner *sessionOwner, offer chan<- sessionOffer, stage ConnectionError, err error) {
	failure := reportFailure(stage, err)
	owner.state.setError(failure.Error())
	offer <- sessionOffer{err: failure}
}

type sessionCommandKind int

const (
	sessionCommandAnswer sessionCommandKind = iota
	sessionCommandControls
)

type sessionCommand struct {
	kind     sessionCommandKind
	sdp      SessionDescription
	controls AudioControls
	complete chan error
}

// sessionState holds the levels and the last bounded failure for the handle.
type sessionState struct {
	microphone atomic.Uint32
	speaker    atomic.Uint32
	errorMu    sync.Mutex
	lastError  string
}

func (s *sessionState) recordPeak(target *atomic.Uint32, value uint16) {
	for {
		current := target.Load()
		if uint32(value) <= current {
			return
		}
		if target.CompareAndSwap(current, uint32(value)) {
			return
		}
	}
}

func (s *sessionState) setError(message string) {
	s.errorMu.Lock()
	s.lastError = message
	s.errorMu.Unlock()
}

func (s *sessionState) takeError() string {
	s.errorMu.Lock()
	defer s.errorMu.Unlock()
	message := s.lastError
	s.lastError = ""
	return message
}

// sessionOwner owns the helper process and the ordered command channel.
type sessionOwner struct {
	commands   chan sessionCommand
	cancel     context.CancelFunc
	done       chan struct{}
	controlsMu sync.Mutex
	controls   AudioControls
	state      sessionState
}

// SessionHandle controls one running session. It is safe for concurrent use.
type SessionHandle struct {
	owner *sessionOwner
}

// Close cancels the session. Pending I/O stops and the helper is retired.
func (h *SessionHandle) Close() {
	if h == nil || h.owner == nil {
		return
	}
	h.owner.cancel()
}

// Done reports when the session actor has stopped.
func (h *SessionHandle) Done() <-chan struct{} {
	if h == nil || h.owner == nil {
		return nil
	}
	return h.owner.done
}

// SetMicrophoneMuted queues an ordered privacy transition. Saturation
// terminates the session rather than dropping the transition.
func (h *SessionHandle) SetMicrophoneMuted(muted bool) error {
	if h == nil || h.owner == nil {
		return ConnectionFailed
	}
	owner := h.owner
	owner.controlsMu.Lock()
	owner.controls.MicrophoneMuted = muted
	controls := owner.controls
	owner.controlsMu.Unlock()
	return owner.send(sessionCommand{kind: sessionCommandControls, controls: controls})
}

// SetSpeakerSuppressed queues an ordered speaker transition.
func (h *SessionHandle) SetSpeakerSuppressed(suppressed bool) {
	if h == nil || h.owner == nil {
		return
	}
	owner := h.owner
	owner.controlsMu.Lock()
	owner.controls.SpeakerSuppressed = suppressed
	controls := owner.controls
	owner.controlsMu.Unlock()
	_ = owner.send(sessionCommand{kind: sessionCommandControls, controls: controls})
}

// ApplyAnswerSDP applies the remote answer and returns only after negotiation,
// device startup, and restored controls complete.
func (h *SessionHandle) ApplyAnswerSDP(answer string) error {
	if h == nil || h.owner == nil {
		return ConnectionFailed
	}
	description, err := NewSessionDescription(answer)
	if err != nil {
		return ConnectionFailed
	}
	owner := h.owner
	complete := make(chan error, 1)
	if err := owner.send(sessionCommand{kind: sessionCommandAnswer, sdp: description, complete: complete}); err != nil {
		return ConnectionFailed
	}
	select {
	case result := <-complete:
		return result
	case <-time.After(sessionAnswerWait):
		h.Close()
		return ConnectionFailed
	}
}

// TakeError returns and clears the last bounded session failure.
func (h *SessionHandle) TakeError() string {
	if h == nil || h.owner == nil {
		return ""
	}
	return h.owner.state.takeError()
}

// TakeMicrophonePeak returns and clears the accumulated capture level.
func (h *SessionHandle) TakeMicrophonePeak() uint16 {
	if h == nil || h.owner == nil {
		return 0
	}
	return uint16(h.owner.state.microphone.Swap(0))
}

// TakeSpeakerPeak returns and clears the accumulated playback level.
func (h *SessionHandle) TakeSpeakerPeak() uint16 {
	if h == nil || h.owner == nil {
		return 0
	}
	return uint16(h.owner.state.speaker.Swap(0))
}

// send queues a command. Saturation fails closed and stops the session.
func (o *sessionOwner) send(command sessionCommand) error {
	select {
	case o.commands <- command:
		return nil
	default:
		o.state.setError("Voice control channel unavailable.")
		o.cancel()
		return errors.New("voice control channel unavailable")
	}
}

// run is the session actor. It applies the answer, keeps privacy controls
// ordered, and polls levels until the session stops.
func (o *sessionOwner) run(ctx context.Context, host sessionHost) {
	connected := false
	poll := time.NewTicker(sessionPollInterval)
	defer poll.Stop()
	var sessionFailure error
	for {
		select {
		case <-ctx.Done():
			sessionFailure = reportFailure(ConnectionShutdown, closeForSession(host))
			o.finish(sessionFailure)
			return
		case command, ok := <-o.commands:
			if !ok {
				o.finish(reportFailure(ConnectionShutdown, closeForSession(host)))
				return
			}
			switch command.kind {
			case sessionCommandAnswer:
				if connected {
					command.complete <- errors.New("voice answer already applied")
					continue
				}
				if err := o.applyAnswer(ctx, host, command.sdp); err != nil {
					command.complete <- err
					// This failure is delivered by the startup completion only.
					o.finish(nil)
					return
				}
				connected = true
				command.complete <- nil
			case sessionCommandControls:
				if !connected {
					continue
				}
				if err := withTimeout(ctx, sessionControlTimeout, func(step context.Context) error {
					return host.SetAudioControls(step, command.controls)
				}); err != nil {
					o.finish(reportFailure(ConnectionAudioControls, err))
					return
				}
			}
		case <-poll.C:
			if err := withTimeout(ctx, sessionControlTimeout, func(step context.Context) error {
				state, err := host.InspectAudio(step)
				if err != nil {
					return err
				}
				o.state.recordPeak(&o.state.microphone, state.MicrophonePeak)
				o.state.recordPeak(&o.state.speaker, state.SpeakerPeak)
				return nil
			}); err != nil {
				o.finish(reportFailure(ConnectionAudioSession, err))
				_ = closeForSession(host)
				return
			}
		}
	}
}

// applyAnswer negotiates, opens devices, and restores queued controls before
// reporting success.
func (o *sessionOwner) applyAnswer(ctx context.Context, host sessionHost, sdp SessionDescription) error {
	if err := withTimeout(ctx, sessionAnswerTimeout, func(step context.Context) error {
		return host.ApplyAnswer(step, sdp)
	}); err != nil {
		if errors.Is(err, ErrVoiceNegotiationTimedOut) {
			return ConnectionNegotiationTimedOut
		}
		return reportFailure(ConnectionTransport, err)
	}
	if err := withTimeout(ctx, sessionDeviceTimeout, func(step context.Context) error {
		return host.OpenDevices(step)
	}); err != nil {
		return reportFailure(ConnectionAudioDevices, err)
	}
	if err := o.applyStartupControls(ctx, host); err != nil {
		return reportFailure(ConnectionAudioControls, err)
	}
	return nil
}

// applyStartupControls drains privacy transitions queued during device startup
// and applies the merged snapshot. Holding the control lock keeps later setters
// ordered after this request without waiting for its acknowledgement.
func (o *sessionOwner) applyStartupControls(ctx context.Context, host sessionHost) error {
	o.controlsMu.Lock()
	defer o.controlsMu.Unlock()
	for index := 0; index < cap(o.commands); index++ {
		select {
		case command, ok := <-o.commands:
			if !ok {
				return errors.New("voice startup control sequence invalid")
			}
			if command.kind != sessionCommandControls {
				return errors.New("voice startup control sequence invalid")
			}
			// The shared snapshot already reflects this transition.
		default:
			return o.dispatchControls(ctx, host)
		}
	}
	return o.dispatchControls(ctx, host)
}

func (o *sessionOwner) dispatchControls(ctx context.Context, host sessionHost) error {
	controls := o.controls
	return withTimeout(ctx, sessionControlTimeout, func(step context.Context) error {
		return host.SetAudioControls(step, controls)
	})
}

// finish closes the helper and records the bounded failure.
func (o *sessionOwner) finish(failure error) {
	if failure != nil {
		o.state.setError(failure.Error())
	}
}

// closeForSession shuts the helper down and classifies a failed shutdown.
func closeForSession(host sessionHost) error {
	if host == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionShutdownTimeout)
	defer cancel()
	if err := host.Close(ctx); err != nil {
		return fmt.Errorf("%w: %v", ConnectionShutdown, err)
	}
	return nil
}

// withTimeout runs one protocol step with its own deadline.
func withTimeout(ctx context.Context, deadline time.Duration, step func(context.Context) error) error {
	stepContext, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	return step(stepContext)
}
