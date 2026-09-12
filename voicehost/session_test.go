package voicehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// fakeSessionHost records the ordered helper exchanges the session actor
// performs, so the facade contract can be asserted without a helper process.
type fakeSessionHost struct {
	mu              sync.Mutex
	initialized     bool
	initErr         error
	transportCalled bool
	transportErr    error
	answerSDP       string
	answerErr       error
	devicesOpened   bool
	deviceErr       error
	controls        []AudioControls
	controlErr      error
	state           AudioState
	inspectErr      error
	inspectCalls    int
	closed          bool
	closeErr        error
	handle          *SessionHandle
	queueOnOpen     *AudioControls
}

func (f *fakeSessionHost) InitializeRuntime(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initialized = true
	return f.initErr
}

func (f *fakeSessionHost) StartTransport(context.Context) (SessionDescription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transportCalled = true
	if f.transportErr != nil {
		return SessionDescription{}, f.transportErr
	}
	return NewSessionDescription("v=0\r\no=offer\r\n")
}

func (f *fakeSessionHost) ApplyAnswer(_ context.Context, sdp SessionDescription) error {
	f.mu.Lock()
	f.answerSDP = sdp.SDP()
	err := f.answerErr
	f.mu.Unlock()
	return err
}

func (f *fakeSessionHost) OpenDevices(context.Context) error {
	f.mu.Lock()
	f.devicesOpened = true
	err := f.deviceErr
	handle := f.handle
	queue := f.queueOnOpen
	f.mu.Unlock()
	if err == nil && handle != nil && queue != nil {
		// A privacy transition can arrive while devices are still starting.
		_ = handle.SetMicrophoneMuted(queue.MicrophoneMuted)
		handle.SetSpeakerSuppressed(queue.SpeakerSuppressed)
	}
	return err
}

func (f *fakeSessionHost) SetAudioControls(_ context.Context, controls AudioControls) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.controlErr != nil {
		return f.controlErr
	}
	f.controls = append(f.controls, controls)
	return nil
}

func (f *fakeSessionHost) InspectAudio(context.Context) (AudioState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectCalls++
	if f.inspectErr != nil {
		return AudioState{}, f.inspectErr
	}
	return f.state, nil
}

func (f *fakeSessionHost) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.closeErr
}

type fakeSessionSnapshot struct {
	initialized     bool
	transportCalled bool
	answerSDP       string
	devicesOpened   bool
	controls        []AudioControls
	closed          bool
	inspectCalls    int
}

func (f *fakeSessionHost) snapshot() fakeSessionSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeSessionSnapshot{
		initialized:     f.initialized,
		transportCalled: f.transportCalled,
		answerSDP:       f.answerSDP,
		devicesOpened:   f.devicesOpened,
		controls:        append([]AudioControls(nil), f.controls...),
		closed:          f.closed,
		inspectCalls:    f.inspectCalls,
	}
}

// withSessionHost substitutes the helper connector for one test.
func withSessionHost(t *testing.T, host *fakeSessionHost, connectErr error) {
	t.Helper()
	previous := connectSessionHost
	connectSessionHost = func(context.Context, string, string) (sessionHost, error) {
		if connectErr != nil {
			return nil, connectErr
		}
		return host, nil
	}
	t.Cleanup(func() { connectSessionHost = previous })
}

func startTestSession(t *testing.T, options SessionOptions) (*StartedSession, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return (RealtimeSession{}).StartWithOptions(ctx, options)
}

func TestRealtimeSessionStartExposesOfferAndAppliesAnswer(t *testing.T) {
	host := &fakeSessionHost{}
	withSessionHost(t, host, nil)
	started, err := startTestSession(t, SessionOptions{Executable: "fake-helper"})
	if err != nil {
		t.Fatal(err)
	}
	if started.OfferSDP != "v=0\r\no=offer\r\n" {
		t.Fatalf("offer = %q", started.OfferSDP)
	}
	before := host.snapshot()
	if !before.initialized || !before.transportCalled {
		t.Fatalf("startup stages = %#v", before)
	}
	if before.answerSDP != "" {
		t.Fatalf("answer applied before the caller requested it: %q", before.answerSDP)
	}

	host.handle = started.Handle
	if err := started.Handle.ApplyAnswerSDP("v=0\r\no=answer\r\n"); err != nil {
		t.Fatalf("apply answer: %v", err)
	}
	after := host.snapshot()
	if after.answerSDP != "v=0\r\no=answer\r\n" || !after.devicesOpened {
		t.Fatalf("answer stages = %#v", after)
	}
	if len(after.controls) != 1 || after.controls[0] != (AudioControls{}) {
		t.Fatalf("startup controls = %#v", after.controls)
	}

	started.Handle.Close()
	select {
	case <-started.Handle.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("closing the handle did not stop the session")
	}
	if closed := host.snapshot(); !closed.closed {
		t.Fatal("closing the handle did not close the helper")
	}
}

func TestRealtimeSessionOrdersControlsQueuedDuringStartup(t *testing.T) {
	host := &fakeSessionHost{queueOnOpen: &AudioControls{MicrophoneMuted: true}}
	withSessionHost(t, host, nil)
	started, err := startTestSession(t, SessionOptions{Executable: "fake-helper"})
	if err != nil {
		t.Fatal(err)
	}
	defer started.Handle.Close()
	host.handle = started.Handle
	if err := started.Handle.ApplyAnswerSDP("v=0\r\no=answer\r\n"); err != nil {
		t.Fatal(err)
	}
	after := host.snapshot()
	if len(after.controls) != 1 {
		t.Fatalf("startup controls = %#v, want the merged snapshot only", after.controls)
	}
	if !after.controls[0].MicrophoneMuted {
		t.Fatalf("merged control = %#v", after.controls[0])
	}
}

func TestRealtimeSessionReportsStartupFailures(t *testing.T) {
	tests := []struct {
		name string
		host *fakeSessionHost
		err  error
		want ConnectionError
	}{
		{name: "helper startup", err: errors.New("cannot start"), want: ConnectionHelperStartup},
		{name: "runtime initialization", host: &fakeSessionHost{initErr: errors.New("no runtime")}, want: ConnectionRuntimeInitialization},
		{name: "transport", host: &fakeSessionHost{transportErr: errors.New("no peer")}, want: ConnectionTransport},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withSessionHost(t, test.host, test.err)
			_, err := startTestSession(t, SessionOptions{Executable: "fake-helper"})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRealtimeSessionClassifiesAnswerFailures(t *testing.T) {
	tests := []struct {
		name string
		host *fakeSessionHost
		want ConnectionError
	}{
		{name: "negotiation timeout", host: &fakeSessionHost{answerErr: ErrVoiceNegotiationTimedOut}, want: ConnectionNegotiationTimedOut},
		{name: "transport failure", host: &fakeSessionHost{answerErr: ErrVoiceTransportClosed}, want: ConnectionTransport},
		{name: "device failure", host: &fakeSessionHost{deviceErr: errors.New("no microphone")}, want: ConnectionAudioDevices},
		{name: "control failure", host: &fakeSessionHost{controlErr: errors.New("controls failed")}, want: ConnectionAudioControls},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withSessionHost(t, test.host, nil)
			started, err := startTestSession(t, SessionOptions{Executable: "fake-helper"})
			if err != nil {
				t.Fatal(err)
			}
			defer started.Handle.Close()
			test.host.handle = started.Handle
			err = started.Handle.ApplyAnswerSDP("v=0\r\no=answer\r\n")
			if !errors.Is(err, test.want) {
				t.Fatalf("answer error = %v, want %v", err, test.want)
			}
			if err != nil && err.Error() != test.want.Error() {
				t.Fatalf("error message = %q, want %q", err.Error(), test.want.Error())
			}
			if test.want.Retryable() != (test.want == ConnectionNegotiationTimedOut) {
				t.Fatalf("retryable = %v for %v", test.want.Retryable(), test.want)
			}
		})
	}
}

func TestRealtimeSessionPollsLevelsAndReportsHelperLoss(t *testing.T) {
	host := &fakeSessionHost{state: AudioState{MicrophonePeak: 11, SpeakerPeak: 22}}
	withSessionHost(t, host, nil)
	started, err := startTestSession(t, SessionOptions{Executable: "fake-helper"})
	if err != nil {
		t.Fatal(err)
	}
	host.handle = started.Handle
	if err := started.Handle.ApplyAnswerSDP("v=0\r\no=answer\r\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for started.Handle.TakeMicrophonePeak() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if peak := started.Handle.TakeMicrophonePeak(); peak != 0 {
		t.Fatalf("take must clear the level, got %d", peak)
	}

	host.mu.Lock()
	host.inspectErr = errors.New("helper gone")
	host.mu.Unlock()
	select {
	case <-started.Handle.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("helper loss did not stop the session")
	}
	if message := started.Handle.TakeError(); message != ConnectionAudioSession.Error() {
		t.Fatalf("recorded error = %q", message)
	}
}

func TestSessionHandleSaturationStopsTheSession(t *testing.T) {
	owner := &sessionOwner{
		commands: make(chan sessionCommand, 1),
		cancel:   func() {},
		done:     make(chan struct{}),
	}
	handle := &SessionHandle{owner: owner}
	owner.commands <- sessionCommand{kind: sessionCommandControls}
	if err := handle.SetMicrophoneMuted(true); err == nil {
		t.Fatal("a saturated control channel must fail closed")
	}
	if message := handle.TakeError(); message != "Voice control channel unavailable." {
		t.Fatalf("recorded error = %q", message)
	}
}

func TestRealtimeSessionIsSupportedPackage(t *testing.T) {
	root := t.TempDir()
	if IsSupportedPackage(root) {
		t.Fatal("an empty package reported voice support")
	}
	name := "codex-voice-host"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	directory := filepath.Join(root, "codex-resources", "voice", "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte("helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsSupportedPackage(root) {
		t.Fatal("a package carrying the helper did not report support")
	}
}

func TestDefaultBuildCommitFallsBackToDev(t *testing.T) {
	previous := buildCommit
	t.Cleanup(func() { buildCommit = previous })
	buildCommit = "dev"
	if commit := DefaultBuildCommit(); commit != "dev" {
		t.Fatalf("build commit = %q, want dev", commit)
	}
	SetBuildCommit("   ")
	if commit := DefaultBuildCommit(); commit != "dev" {
		t.Fatalf("a blank stamp changed the identity: %q", commit)
	}
	SetBuildCommit(" 1.2.3 ")
	if commit := DefaultBuildCommit(); commit != "1.2.3" {
		t.Fatalf("build commit = %q, want the trimmed stamp", commit)
	}
}

func TestConnectionErrorMessagesMatchRust(t *testing.T) {
	tests := []struct {
		err  ConnectionError
		want string
	}{
		{ConnectionNegotiationTimedOut, "voice negotiation timed out"},
		{ConnectionFailed, "voice connection failed"},
		{ConnectionHelperStartup, "voice helper could not start"},
		{ConnectionRuntimeInitialization, "voice audio runtime could not initialize"},
		{ConnectionTransport, "voice transport could not connect"},
		{ConnectionAudioDevices, "voice audio devices could not open; check microphone and speaker setup"},
		{ConnectionAudioControls, "voice audio controls failed"},
		{ConnectionAudioSession, "voice audio session stopped unexpectedly"},
		{ConnectionShutdown, "voice helper could not shut down cleanly"},
	}
	for _, test := range tests {
		if got := test.err.Error(); got != test.want {
			t.Fatalf("message = %q, want %q", got, test.want)
		}
	}
}

func TestReportFailureKeepsNestedClassification(t *testing.T) {
	if err := reportFailure(ConnectionTransport, nil); err != nil {
		t.Fatalf("nil failure = %v", err)
	}
	if err := reportFailure(ConnectionTransport, errors.New("raw")); !errors.Is(err, ConnectionTransport) {
		t.Fatalf("untyped failure = %v", err)
	}
	if err := reportFailure(ConnectionTransport, ConnectionAudioDevices); !errors.Is(err, ConnectionAudioDevices) {
		t.Fatalf("nested classification = %v", err)
	}
}
