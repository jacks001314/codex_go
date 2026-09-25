package codemode

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"codex_go/tool"
)

// blockingEngine keeps its cell running until the test releases it, so a preempt
// signal can be observed deterministically (mirrors Rust #48123's runtime tests,
// which use a script that has not finished when the signal fires).
type blockingEngine struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingEngine() *blockingEngine {
	return &blockingEngine{started: make(chan struct{}), release: make(chan struct{})}
}

func (e *blockingEngine) Execute(ctx context.Context, request EngineRequest) (*EngineResult, error) {
	e.once.Do(func() { close(e.started) })
	select {
	case <-e.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &EngineResult{ContentItems: []ContentItem{InputText("released")}}, nil
}

func (e *blockingEngine) Interrupt(error) {}

func (e *blockingEngine) Close() error { return nil }

type fixedEngineFactory struct{ engine Engine }

func (f fixedEngineFactory) NewEngine() (Engine, error) { return f.engine, nil }

func newPreemptRuntime(engine Engine) *SessionRuntime {
	runtime := NewSessionRuntime()
	runtime.factory = fixedEngineFactory{engine: engine}
	return runtime
}

// Mirrors Rust #48123: an execute observation yields early while the cell keeps
// running and stays available to a later wait, which then completes normally.
func TestSessionRuntimePreemptEndsExecuteObservationLikeRust(t *testing.T) {
	engine := newBlockingEngine()
	runtime := newPreemptRuntime(engine)
	preempt := tool.NewYieldSignal()
	// The signal fires before the observation is registered, which Rust preserves.
	preempt.Cancel()

	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID: "call-preempt-execute", Source: "text('released')", YieldTimeMS: uint64Ptr(60000),
	}, preempt)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if started.InitialResponse.Variant != "Yielded" {
		t.Fatalf("preempted execute response = %#v", started.InitialResponse)
	}

	close(engine.release)
	wait, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 3000}, nil)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if wait.Response.Variant != "Result" || len(wait.Response.ContentItems) != 1 || wait.Response.ContentItems[0].Text != "released" {
		t.Fatalf("wait after an early yield = %#v", wait)
	}
}

// Mirrors Rust #48123: a wait observation yields early without waiting for its
// yield time, and the cell is still available afterwards.
func TestSessionRuntimePreemptEndsWaitObservationLikeRust(t *testing.T) {
	engine := newBlockingEngine()
	runtime := newPreemptRuntime(engine)
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID: "call-preempt-wait", Source: "text('released')", YieldTimeMS: uint64Ptr(0),
	}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if started.InitialResponse.Variant != "Yielded" {
		t.Fatalf("execute response = %#v", started.InitialResponse)
	}

	preempt := tool.NewYieldSignal()
	preempt.Cancel()
	begin := time.Now()
	wait, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 60000}, preempt)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if wait.Response.Variant != "Yielded" {
		t.Fatalf("preempted wait response = %#v", wait.Response)
	}
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("the preempted wait did not return early: %v", elapsed)
	}

	close(engine.release)
	completed, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 3000}, nil)
	if err != nil {
		t.Fatalf("Wait() after release error = %v", err)
	}
	if completed.Response.Variant != "Result" {
		t.Fatalf("cell did not complete after its yielded observation: %#v", completed)
	}
}

// Mirrors Rust HostState: the host threads the request's yield signal into the
// session, so a signal that fired before the request was served still ends the
// observation early.
func TestSessionHostThreadsPreemptIntoTheSessionLikeRust(t *testing.T) {
	engine := newBlockingEngine()
	runtime := newPreemptRuntime(engine)
	host := NewSessionHost(runtime)
	sessionID, _ := NewSessionID("session-preempt")
	preempt := tool.NewYieldSignal()
	preempt.Cancel()

	response, initial, err := host.Handle(context.Background(), &HostRequest{
		Method: "session/execute", SessionID: sessionID,
		Request: &ExecuteRequest{ToolCallID: "call-host-preempt", Source: "text('released')", YieldTimeMS: uint64Ptr(60000)},
	}, preempt)
	if err != nil {
		t.Fatalf("Handle(execute) error = %v", err)
	}
	if response == nil || response.Type != "execution/started" || initial == nil || initial.Variant != "Yielded" {
		t.Fatalf("response=%#v initial=%#v", response, initial)
	}
	close(engine.release)
}

// Mirrors Rust's host negotiation: the host advertises `yield-observation` only
// when the client asked for it, and rejects a required capability it cannot
// provide.
func TestStdioHostNegotiatesYieldObservationLikeRust(t *testing.T) {
	capability, _ := NewCapability(YieldObservationCapability)

	hello := func(required CapabilitySet, optional CapabilitySet) ClientToHost {
		versions, _ := NewSupportedProtocolVersions(ProtocolV1)
		value, _ := NewClientHello(versions, required, optional)
		return ClientHelloMessage(value)
	}

	for _, tc := range []struct {
		name     string
		hello    ClientToHost
		wantType string
		wantCap  bool
	}{
		{"optional request", hello(CapabilitySet{}, CapabilitySet{capability}), "connection/ready", true},
		{"required request", hello(CapabilitySet{capability}, CapabilitySet{}), "connection/ready", true},
		{"not requested", hello(CapabilitySet{}, CapabilitySet{}), "connection/ready", false},
	} {
		response, err := runStdioHandshake(t, tc.hello)
		if err != nil {
			t.Fatalf("%s: handshake error = %v", tc.name, err)
		}
		if response.Type != tc.wantType {
			t.Fatalf("%s: host response = %#v", tc.name, response)
		}
		advertised := response.Hello != nil && response.Hello.Capabilities.Contains(capability)
		if advertised != tc.wantCap {
			t.Fatalf("%s: advertised = %v, want %v (%#v)", tc.name, advertised, tc.wantCap, response.Hello)
		}
	}

	// A required capability the host does not support is rejected with the
	// missing-required-capability reason.
	unsupported, _ := NewCapability("no-such-capability")
	response, err := runStdioHandshake(t, hello(CapabilitySet{unsupported}, CapabilitySet{}))
	if err != nil {
		t.Fatalf("unsupported required capability error = %v", err)
	}
	if response.Type != "connection/rejected" || response.Reason == nil || response.Reason.Type != "missingRequiredCapability" {
		t.Fatalf("unsupported required capability response = %#v", response)
	}
}

// runStdioHandshake pipes one client hello through RunStdioHost and returns the
// host's handshake answer.
func runStdioHandshake(t *testing.T, hello ClientToHost) (HostToClient, error) {
	t.Helper()
	hostIn, clientOut := io.Pipe()
	clientIn, hostOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunStdioHost(ctx, hostIn, hostOut) }()
	defer func() {
		cancel()
		_ = clientOut.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("RunStdioHost did not exit")
		}
	}()

	if err := NewFramedWriter(clientOut).Write(hello); err != nil {
		return HostToClient{}, err
	}
	var response HostToClient
	ok, err := NewFramedReader(clientIn).Read(&response)
	if err != nil {
		return HostToClient{}, err
	}
	if !ok {
		return HostToClient{}, io.ErrUnexpectedEOF
	}
	return response, nil
}

type recordingYieldTransport struct {
	mu      sync.Mutex
	written []any
}

func (t *recordingYieldTransport) Read(ctx context.Context, target any) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

func (t *recordingYieldTransport) Write(_ context.Context, message any) error {
	t.mu.Lock()
	t.written = append(t.written, message)
	t.mu.Unlock()
	return nil
}

func (t *recordingYieldTransport) Close() error { return nil }

func (t *recordingYieldTransport) frames() []any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]any(nil), t.written...)
}

// Mirrors Rust Connection::supported_yield_signal and ObservationYield: the
// frame is only sent to a host that negotiated the capability, and firing the
// preempt signal sends `operation/yield` for the observation's request id.
func TestRemoteConnectionYieldWatcherMatchesRust(t *testing.T) {
	transport := &recordingYieldTransport{}
	connection := newRemoteConnection(transport)

	// An older host never negotiated the capability, so the signal is dropped and
	// no frame is written.
	stale := tool.NewYieldSignal()
	if connection.supportedYieldSignal(stale) != nil {
		t.Fatal("an unnegotiated host must keep its normal timeout behavior")
	}
	stop := connection.watchYield(7, connection.supportedYieldSignal(stale))
	stale.Cancel()
	stop()
	if frames := transport.frames(); len(frames) != 0 {
		t.Fatalf("unnegotiated yield wrote %#v", frames)
	}

	connection.capabilities = CapabilitySet{Capability(YieldObservationCapability)}
	signal := tool.NewYieldSignal()
	stop = connection.watchYield(9, connection.supportedYieldSignal(signal))
	defer stop()
	signal.Cancel()
	deadline := time.Now().Add(2 * time.Second)
	for len(transport.frames()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	frames := transport.frames()
	if len(frames) != 1 {
		t.Fatalf("yield frames = %#v", frames)
	}
	frame, ok := frames[0].(ClientToHost)
	if !ok || frame.Type != "operation/yield" || frame.ID != 9 {
		t.Fatalf("yield frame = %#v", frames[0])
	}
	encoded, err := frame.MarshalJSON()
	if err != nil || string(encoded) != `{"type":"operation/yield","id":9}` {
		t.Fatalf("encoded yield frame = %s (%v)", encoded, err)
	}
}
