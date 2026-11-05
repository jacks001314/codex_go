package codemode

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"codex_go/tool"
	"google.golang.org/protobuf/proto"
)

// Mirrors Rust #59590: capable clients negotiate 4096 callbacks while legacy
// clients retain the 1024 budget and the host advertises the capability only
// to clients that asked for it.
func TestDelegateCapacityNegotiationLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name      string
		advertise bool
		wantLimit int
	}{
		{name: "large capable client", advertise: true, wantLimit: MaxPendingDelegateCalls},
		{name: "legacy client", advertise: false, wantLimit: LegacyMaxPendingDelegateCalls},
	} {
		t.Run(tc.name, func(t *testing.T) {
			versions, _ := NewSupportedProtocolVersions(ProtocolV1)
			optional := CapabilitySet{}
			if tc.advertise {
				optional = CapabilitySet{Capability(LargeDelegateCapacityCapability)}
			}
			hello, _ := NewClientHello(versions, CapabilitySet{}, optional)
			read := false
			var response HostToClient
			host := &stdioHostServer{
				ctx: context.Background(),
				read: func(target *ClientToHost) (bool, error) {
					if read {
						return false, nil
					}
					read = true
					*target = ClientHelloMessage(hello)
					return true, nil
				},
				writeFn: func(message any) error {
					response, _ = message.(HostToClient)
					return nil
				},
			}
			if err := host.negotiate(); err != nil {
				t.Fatalf("negotiate() error = %v", err)
			}
			if host.delegateLimit != tc.wantLimit {
				t.Fatalf("delegate limit = %d, want %d", host.delegateLimit, tc.wantLimit)
			}
			advertised := response.Hello != nil && response.Hello.Capabilities.Contains(Capability(LargeDelegateCapacityCapability))
			if advertised != tc.advertise {
				t.Fatalf("large capacity advertised = %t, want %t", advertised, tc.advertise)
			}
		})
	}
}

func TestGrpcPendingDelegateCapacityIsIsolatedLikeRust(t *testing.T) {
	host := &stdioHostServer{
		delegateLimit:   2,
		enforceSessions: true,
		pendingSession:  map[SessionID]int{},
	}
	first, _ := NewSessionID("session-1")
	second, _ := NewSessionID("session-2")
	for index := 0; index < 2; index++ {
		if err := host.reserveDelegateBudget(first); err != nil {
			t.Fatalf("reserve first session callback %d: %v", index, err)
		}
	}
	if err := host.reserveDelegateBudget(first); err == nil || err.Error() != "code-mode session has too many pending delegate calls" {
		t.Fatalf("session overflow error = %v", err)
	}
	if err := host.reserveDelegateBudget(second); err == nil || err.Error() != "code-mode host has too many pending delegate calls" {
		t.Fatalf("host overflow error = %v", err)
	}
	host.releaseDelegateBudget(first)
	if err := host.reserveDelegateBudget(second); err != nil {
		t.Fatalf("released capacity was not reusable: %v", err)
	}
	host.releaseDelegateBudget(first)
	host.releaseDelegateBudget(second)
}

func TestGrpcPendingDelegateCapacityFieldLikeRust(t *testing.T) {
	legacy, err := NormalizePendingDelegateCalls(nil)
	if err != nil || legacy != LegacyMaxPendingDelegateCalls {
		t.Fatalf("legacy capacity = %d, %v", legacy, err)
	}
	zero := uint32(0)
	if _, err := NormalizePendingDelegateCalls(&zero); err == nil || err.Error() != "callback capacity must be positive" {
		t.Fatalf("zero capacity error = %v", err)
	}
	large := uint32(MaxPendingDelegateCalls + 100)
	clamped, err := NormalizePendingDelegateCalls(&large)
	if err != nil || clamped != MaxPendingDelegateCalls {
		t.Fatalf("clamped capacity = %d, %v", clamped, err)
	}

	requested := uint32(2)
	encoded, err := proto.Marshal(&codemodev1.OpenSessionRequest{MaxPendingDelegateCalls: &requested})
	if err != nil {
		t.Fatalf("marshal open session: %v", err)
	}
	var decoded codemodev1.OpenSessionRequest
	if err := proto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal open session: %v", err)
	}
	if decoded.MaxPendingDelegateCalls == nil || decoded.GetMaxPendingDelegateCalls() != requested {
		t.Fatalf("decoded capacity = %#v", decoded.MaxPendingDelegateCalls)
	}
}

func TestCompletionWindowCancellationDoesNotWaitForDeliveryLikeRust(t *testing.T) {
	transport := &capacityTestTransport{}
	connection := newRemoteConnection(transport)
	window := newCompletionWindow()
	connection.setCompletionWindow(window)
	for index := 0; index < MaxInFlightCompletions; index++ {
		if !window.acquire(context.Background()) {
			t.Fatal("failed to fill completion window")
		}
	}
	delegate := &capacityTestDelegate{invoked: make(chan struct{})}
	sessionID, _ := NewSessionID("session-1")
	connection.SetDelegate(sessionID, delegate)
	done := make(chan struct{})
	go func() {
		connection.handleDelegate(HostToClient{
			Type:       "delegate/request",
			DelegateID: 1,
			SessionID:  sessionID,
			Request: &DelegateRequest{Type: "tool/invoke", Invocation: &NestedToolCall{
				CellID: CellID("cell-1"), RuntimeToolCallID: "call-1",
				ToolName: ToolName{Name: "probe"}, ProtocolToolKind: ProtocolToolKindFunction,
				Input: json.RawMessage("{}"),
			}},
		})
		close(done)
	}()
	select {
	case <-delegate.invoked:
	case <-time.After(time.Second):
		t.Fatal("delegate did not run")
	}
	connection.mu.Lock()
	cancel := connection.cancels[1]
	connection.mu.Unlock()
	if cancel == nil {
		t.Fatal("delegate cancellation was not registered")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled completion waited for delivery capacity")
	}
	if writes := transport.writeCount(); writes != 0 {
		t.Fatalf("queued completion wrote %d responses", writes)
	}
	for index := 0; index < MaxInFlightCompletions; index++ {
		window.release()
	}
}

func TestActiveCellCapacityRecoversLikeRust(t *testing.T) {
	if MaxActiveCells != 2048 {
		t.Fatalf("max active cells = %d, want 2048", MaxActiveCells)
	}
	host := &stdioHostServer{activeCells: map[activeCellKey]struct{}{}, activeCellLimit: 2}
	sessionID, _ := NewSessionID("session-1")
	for index := 0; index < 2; index++ {
		cellID := NewCellID("cell-" + strconv.Itoa(index+1))
		if !host.reserveActiveCell(sessionID, cellID) {
			t.Fatalf("active cell %d rejected before capacity", index)
		}
	}
	overflow := NewCellID("overflow")
	if host.reserveActiveCell(sessionID, overflow) {
		t.Fatal("active cell capacity overflow was admitted")
	}
	var released CellID
	for key := range host.activeCells {
		released = key.cellID
		break
	}
	host.releaseActiveCell(sessionID, released)
	if !host.reserveActiveCell(sessionID, overflow) {
		t.Fatal("released active cell capacity was not reusable")
	}
}

type capacityTestTransport struct {
	mu     sync.Mutex
	writes int
}

func (t *capacityTestTransport) Read(ctx context.Context, _ any) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

func (t *capacityTestTransport) Write(context.Context, any) error {
	t.mu.Lock()
	t.writes++
	t.mu.Unlock()
	return nil
}

func (t *capacityTestTransport) Close() error { return nil }

func (t *capacityTestTransport) writeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writes
}

type capacityTestDelegate struct {
	invoked chan struct{}
	once    sync.Once
}

func (d *capacityTestDelegate) Invoke(context.Context, tool.CodeModeRemoteNestedCall) (json.RawMessage, error) {
	d.once.Do(func() { close(d.invoked) })
	return json.RawMessage("{}"), nil
}

func (d *capacityTestDelegate) Notify(context.Context, string, string, string) error { return nil }
func (d *capacityTestDelegate) CellClosed(string)                                    {}

// Mirrors Rust #59590's stdio capacity integration test: two sessions on one
// shared host admit 256 live cells with 16 callbacks each while unrelated work
// still makes progress.
func TestStdioSharedHostCompletesThousandsOfCallbacksLikeRust(t *testing.T) {
	if testing.Short() {
		t.Skip("shared-host callback capacity test")
	}
	hostSide, clientSide := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	hostDone := make(chan error, 1)
	go func() { hostDone <- RunStdioHost(ctx, hostSide, hostSide) }()
	t.Cleanup(func() {
		cancel()
		_ = clientSide.Close()
		select {
		case <-hostDone:
		case <-time.After(5 * time.Second):
			t.Errorf("stdio host did not exit")
		}
	})
	transport := &framedCapacityTransport{conn: clientSide, reader: NewFramedReader(clientSide), writer: NewFramedWriter(clientSide)}
	connection, err := connectRemoteTransport(context.Background(), transport)
	if err != nil {
		t.Fatalf("connectRemoteTransport() error = %v", err)
	}
	defer connection.Close()
	if !connection.capabilities.Contains(Capability(LargeDelegateCapacityCapability)) {
		t.Fatal("host did not negotiate delegate-capacity-4096")
	}

	delegate := &blockingCapacityDelegate{started: make(chan struct{}, 4096), release: make(chan struct{})}
	sessionIDs := []SessionID{"capacity-1", "capacity-2"}
	for _, sessionID := range sessionIDs {
		response, err := connection.Request(ctx, OpenSessionRequest(sessionID))
		if err != nil || response.Type != "session/ready" || response.SessionID != sessionID {
			t.Fatalf("open %s = %#v, %v", sessionID, response, err)
		}
	}

	yield := uint64(1)
	type pendingCell struct {
		sessionID SessionID
		cellID    CellID
	}
	pending := make([]pendingCell, 0, 256)
	for index := 0; index < 256; index++ {
		sessionID := sessionIDs[index%len(sessionIDs)]
		response, err := connection.Execute(ctx, sessionID, delegate, ExecuteRequest{
			ToolCallID: "capacity-" + strconv.Itoa(index),
			EnabledTools: []ProtocolToolDefinition{{
				Name: "block", ToolName: ToolName{Name: "block"}, Description: "block",
				Kind: ProtocolToolKindFunction, InputSchema: json.RawMessage("{\"type\":\"object\"}"),
			}},
			Source:      "const results = await Promise.all(Array.from({length: 16}, () => tools.block({}))); text(String(results.length));",
			YieldTimeMS: &yield,
		}, nil)
		if err != nil {
			t.Fatalf("admit cell %d: %v", index, err)
		}
		if response.Variant != "Yielded" {
			t.Fatalf("cell %d initial response = %#v", index, response)
		}
		pending = append(pending, pendingCell{sessionID: sessionID, cellID: response.CellID})
	}
	for started := 0; started < 4096; started++ {
		select {
		case <-delegate.started:
		case <-time.After(30 * time.Second):
			t.Fatalf("only %d of 4096 callbacks started", started)
		}
	}

	probeYield := uint64(60000)
	probe, err := connection.Execute(ctx, sessionIDs[0], delegate, ExecuteRequest{
		ToolCallID: "unrelated-probe", Source: "text('ready')", YieldTimeMS: &probeYield,
	}, nil)
	if err != nil {
		t.Fatalf("unrelated probe: %v", err)
	}
	if probe.Variant != "Result" || probe.ErrorText != nil {
		t.Fatalf("unrelated probe response = %#v", probe)
	}

	close(delegate.release)
	for index, cell := range pending {
		response, err := connection.Request(ctx, WaitSessionRequest(cell.sessionID, WaitRequest{CellID: cell.cellID, YieldTimeMS: 60000}))
		if err != nil || response.Type != "wait/completed" || response.Outcome == nil {
			t.Fatalf("wait cell %d = %#v, %v", index, response, err)
		}
		if result := response.Outcome.Response; result.Variant != "Result" || result.ErrorText != nil {
			t.Fatalf("cell %d result = %#v", index, result)
		}
	}
	for _, sessionID := range sessionIDs {
		if _, err := connection.Request(ctx, ShutdownSessionRequest(sessionID)); err != nil {
			t.Fatalf("shutdown %s: %v", sessionID, err)
		}
	}
}

type framedCapacityTransport struct {
	conn   net.Conn
	reader *FramedReader
	writer *FramedWriter
	mu     sync.Mutex
}

func (t *framedCapacityTransport) Read(_ context.Context, target any) (bool, error) {
	return t.reader.Read(target)
}

func (t *framedCapacityTransport) Write(_ context.Context, message any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writer.Write(message)
}

func (t *framedCapacityTransport) Close() error { return t.conn.Close() }

type blockingCapacityDelegate struct {
	started chan struct{}
	release chan struct{}
}

func (d *blockingCapacityDelegate) Invoke(ctx context.Context, _ tool.CodeModeRemoteNestedCall) (json.RawMessage, error) {
	d.started <- struct{}{}
	select {
	case <-d.release:
		return json.RawMessage("{\"success\":true,\"output\":\"done\"}"), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *blockingCapacityDelegate) Notify(context.Context, string, string, string) error { return nil }
func (d *blockingCapacityDelegate) CellClosed(string)                                    {}
