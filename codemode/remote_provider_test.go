package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/tool"

	"github.com/coder/websocket"
)

type recordingRemoteDelegate struct {
	mu       sync.Mutex
	calls    []tool.CodeModeRemoteNestedCall
	notifies []string
}

func TestRemoteConnectionStalledRequestTimesOutAndInvalidatesConnection(t *testing.T) {
	withShortHostWaitTimeout(t, func() {
		connection := newRemoteConnection(&stalledRemoteTransport{})
		_, err := connection.withTransportDeadline(0, "wait", func(ctx context.Context) (HostResponse, error) {
			return connection.Request(ctx, WaitSessionRequest("s", WaitRequest{CellID: "c"}))
		})
		if !errors.Is(err, errCodeModeHostRequestTimeout) {
			t.Fatalf("error = %v, want errCodeModeHostRequestTimeout", err)
		}
		if connection.Alive() {
			t.Fatal("connection still alive after a stalled request timed out")
		}
	})
}

func TestRemoteSessionTerminateStalledTimesOutAndInvalidatesSession(t *testing.T) {
	withShortHostWaitTimeout(t, func() {
		connection := newRemoteConnection(&stalledRemoteTransport{})
		session := &remoteSession{
			provider: &fakeRemoteSessionProvider{connection: connection},
			delegate: &recordingRemoteDelegate{},
			id:       "session-timeout",
		}
		session.mu.Lock()
		session.openedOn = connection
		session.mu.Unlock()

		_, err := session.Terminate(context.Background(), "cell-timeout")
		if !errors.Is(err, errCodeModeHostRequestTimeout) {
			t.Fatalf("Terminate() error = %v, want errCodeModeHostRequestTimeout", err)
		}
		session.mu.Lock()
		openedOn := session.openedOn
		session.mu.Unlock()
		if openedOn != nil {
			t.Fatalf("session still bound to the dead connection: %#v", openedOn)
		}
		if connection.Alive() {
			t.Fatal("connection still alive after terminate stalled")
		}
	})
}

// stalledRemoteTransport never delivers host frames; reads block until the
// context is cancelled. Write/Close are no-ops.
type stalledRemoteTransport struct{}

func (t *stalledRemoteTransport) Read(ctx context.Context, target any) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

func (t *stalledRemoteTransport) Write(_ context.Context, _ any) error { return nil }

func (t *stalledRemoteTransport) Close() error { return nil }

type fakeRemoteSessionProvider struct {
	connection *remoteConnection
}

func (p *fakeRemoteSessionProvider) connect(context.Context) (*remoteConnection, error) {
	return p.connection, nil
}

func withShortHostWaitTimeout(t *testing.T, fn func()) {
	t.Helper()
	previous := defaultHostWaitTransportTimeout
	defaultHostWaitTransportTimeout = 60 * time.Millisecond
	defer func() { defaultHostWaitTransportTimeout = previous }()
	fn()
}

func (d *recordingRemoteDelegate) Invoke(_ context.Context, call tool.CodeModeRemoteNestedCall) (json.RawMessage, error) {
	d.mu.Lock()
	d.calls = append(d.calls, call)
	d.mu.Unlock()
	return json.RawMessage(`{"output":"DELEGATE_OK","success":true}`), nil
}

func (d *recordingRemoteDelegate) Notify(_ context.Context, _, _, text string) error {
	d.mu.Lock()
	d.notifies = append(d.notifies, text)
	d.mu.Unlock()
	return nil
}

func TestWebSocketProviderExecutesAndHandlesDelegates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := request.Context()
		var hello ClientToHost
		if !readClientFrame(t, ctx, conn, &hello) || hello.Type != "connection/hello" {
			return
		}
		writeHostFrame(t, ctx, conn, HostHelloMessage(HostHello{SelectedVersion: ProtocolV1, Capabilities: CapabilitySet{}}))

		var open ClientToHost
		if !readClientFrame(t, ctx, conn, &open) || open.Request == nil || open.Request.Method != "session/open" {
			return
		}
		writeHostFrame(t, ctx, conn, HostOperationResponse(open.ID, ResultOK(SessionReady(open.Request.SessionID))))

		var execute ClientToHost
		if !readClientFrame(t, ctx, conn, &execute) || execute.Request == nil || execute.Request.Method != "session/execute" {
			return
		}
		cellID := CellID("cell-remote")
		writeHostFrame(t, ctx, conn, HostOperationResponse(execute.ID, ResultOK(ExecutionStarted(cellID))))
		input := json.RawMessage(`{"cmd":"Write-Output remote"}`)
		writeHostFrame(t, ctx, conn, DelegateRequestMessage(7, execute.Request.SessionID, InvokeToolRequest(NestedToolCall{
			CellID: cellID, RuntimeToolCallID: "nested-1", ToolName: PlainToolName("exec_command"), ProtocolToolKind: ProtocolToolKindFunction, Input: input,
		})))
		var delegateResponse ClientToHost
		if !readClientFrame(t, ctx, conn, &delegateResponse) || delegateResponse.Type != "delegate/response" || delegateResponse.DelegateResponse == nil || delegateResponse.DelegateResponse.Status != "ok" {
			return
		}
		writeHostFrame(t, ctx, conn, DelegateRequestMessage(8, execute.Request.SessionID, NotifyRequest("exec-call", cellID, "REMOTE_NOTIFY")))
		var notifyResponse ClientToHost
		if !readClientFrame(t, ctx, conn, &notifyResponse) || notifyResponse.DelegateResponse == nil || notifyResponse.DelegateResponse.Status != "ok" {
			return
		}
		writeHostFrame(t, ctx, conn, InitialResponse(execute.ID, ResultOK(Result(cellID, []ContentItem{InputText("REMOTE_OK")}, nil))))

		var shutdown ClientToHost
		if readClientFrame(t, ctx, conn, &shutdown) && shutdown.Request != nil && shutdown.Request.Method == "session/shutdown" {
			writeHostFrame(t, ctx, conn, HostOperationResponse(shutdown.ID, ResultOK(SessionClosed(shutdown.Request.SessionID))))
		}
	}))
	defer server.Close()

	provider := NewWebSocketProvider("ws"+strings.TrimPrefix(server.URL, "http"), server.Client())
	delegate := &recordingRemoteDelegate{}
	session := provider.NewSession(delegate)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := session.Execute(ctx, tool.CodeModeRemoteExecuteRequest{
		ToolCallID: "exec-call",
		Source:     `text("REMOTE_OK")`,
		EnabledTools: []tool.CodeModeRemoteToolDefinition{{
			Name: "exec_command", ToolName: tool.PlainName("exec_command"), Kind: tool.PayloadFunction,
		}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.State != "completed" || response.CellID != "cell-remote" || len(response.ContentItems) != 1 || response.ContentItems[0]["text"] != "REMOTE_OK" {
		t.Fatalf("response = %#v", response)
	}
	delegate.mu.Lock()
	if len(delegate.calls) != 1 || delegate.calls[0].ToolName.Key() != "exec_command" || len(delegate.notifies) != 1 || delegate.notifies[0] != "REMOTE_NOTIFY" {
		t.Fatalf("delegate calls=%#v notifies=%#v", delegate.calls, delegate.notifies)
	}
	delegate.mu.Unlock()
	if err := session.Close(); err != nil {
		t.Fatalf("session.Close() error = %v", err)
	}
	_ = provider.Close()
}

func TestWebSocketProviderNegotiatesDualWebSocketLanes(t *testing.T) {
	invokeSent := make(chan struct{})
	invokeDone := make(chan struct{})
	var sessionID SessionID
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := request.Context()
		if strings.HasSuffix(request.URL.Path, "/bulk/tok-1") {
			// Bulk socket: waits for the control lane to signal the execute
			// request, then carries the nested tool invocation and its result.
			<-invokeSent
			cellID := CellID("cell-dual")
			writeHostFrame(t, ctx, conn, DelegateRequestMessage(50, sessionID, InvokeToolRequest(NestedToolCall{
				CellID: cellID, RuntimeToolCallID: "nested-dual", ToolName: PlainToolName("exec_command"), ProtocolToolKind: ProtocolToolKindFunction, Input: json.RawMessage(`{"cmd":"echo dual"}`),
			})))
			var delegateResponse ClientToHost
			if !readClientFrame(t, ctx, conn, &delegateResponse) || delegateResponse.Type != "delegate/response" || delegateResponse.DelegateResponse == nil || delegateResponse.DelegateResponse.Status != "ok" {
				close(invokeDone)
				return
			}
			close(invokeDone)
			// Keep the bulk socket open for the connection lifetime (closing it
			// early would invalidate the whole connection) and acknowledge the
			// client's close handshake when the test tears down.
			for {
				var message ClientToHost
				if !readClientFrame(t, ctx, conn, &message) {
					return
				}
			}
		}
		var hello ClientToHost
		if !readClientFrame(t, ctx, conn, &hello) || hello.Hello == nil || !hello.Hello.OptionalCapabilities.Contains(Capability(DualWebSocketCapability)) {
			return
		}
		token := "tok-1"
		writeHostFrame(t, ctx, conn, HostHelloMessage(HostHello{SelectedVersion: ProtocolV1, Capabilities: CapabilitySet{Capability(DualWebSocketCapability)}, BulkConnectionToken: &token}))
		for {
			var message ClientToHost
			if !readClientFrame(t, ctx, conn, &message) || message.Request == nil {
				return
			}
			switch message.Request.Method {
			case "session/open":
				sessionID = message.Request.SessionID
				writeHostFrame(t, ctx, conn, HostOperationResponse(message.ID, ResultOK(SessionReady(sessionID))))
			case "session/execute":
				cellID := CellID("cell-dual")
				writeHostFrame(t, ctx, conn, HostOperationResponse(message.ID, ResultOK(ExecutionStarted(cellID))))
				// Notifications stay on the control lane.
				writeHostFrame(t, ctx, conn, DelegateRequestMessage(51, sessionID, NotifyRequest("exec-dual", cellID, "DUAL_NOTIFY")))
				var notifyResponse ClientToHost
				if !readClientFrame(t, ctx, conn, &notifyResponse) || notifyResponse.DelegateResponse == nil || notifyResponse.DelegateResponse.Status != "ok" {
					return
				}
				close(invokeSent)
				<-invokeDone
				writeHostFrame(t, ctx, conn, InitialResponse(message.ID, ResultOK(Result(cellID, []ContentItem{InputText("DUAL_OK")}, nil))))
			case "session/shutdown":
				writeHostFrame(t, ctx, conn, HostOperationResponse(message.ID, ResultOK(SessionClosed(message.Request.SessionID))))
				return
			default:
				return
			}
		}
	}))
	defer server.Close()

	provider := NewWebSocketProvider("ws"+strings.TrimPrefix(server.URL, "http"), server.Client())
	delegate := &recordingRemoteDelegate{}
	session := provider.NewSession(delegate)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := session.Execute(ctx, tool.CodeModeRemoteExecuteRequest{
		ToolCallID: "exec-dual",
		Source:     `text("DUAL_OK")`,
		EnabledTools: []tool.CodeModeRemoteToolDefinition{{
			Name: "exec_command", ToolName: tool.PlainName("exec_command"), Kind: tool.PayloadFunction,
		}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.State != "completed" || response.CellID != "cell-dual" || len(response.ContentItems) != 1 || response.ContentItems[0]["text"] != "DUAL_OK" {
		t.Fatalf("response = %#v", response)
	}
	delegate.mu.Lock()
	if len(delegate.calls) != 1 || delegate.calls[0].ToolName.Key() != "exec_command" || len(delegate.notifies) != 1 || delegate.notifies[0] != "DUAL_NOTIFY" {
		t.Fatalf("delegate calls=%#v notifies=%#v", delegate.calls, delegate.notifies)
	}
	delegate.mu.Unlock()
	_ = session.Close()
	_ = provider.Close()
}

func TestWebSocketProviderRejectsDualAdvertisementWithoutToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := request.Context()
		var hello ClientToHost
		if !readClientFrame(t, ctx, conn, &hello) {
			return
		}
		writeHostFrame(t, ctx, conn, HostHelloMessage(HostHello{SelectedVersion: ProtocolV1, Capabilities: CapabilitySet{Capability(DualWebSocketCapability)}}))
	}))
	defer server.Close()
	provider := NewWebSocketProvider("ws"+strings.TrimPrefix(server.URL, "http"), server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := provider.connect(ctx); err == nil || !strings.Contains(err.Error(), "without a pairing token") {
		t.Fatalf("connect() error = %v, want missing pairing token", err)
	}
}

func TestWebSocketProviderRejectsUnexpectedBulkToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := request.Context()
		var hello ClientToHost
		if !readClientFrame(t, ctx, conn, &hello) {
			return
		}
		token := "unexpected"
		writeHostFrame(t, ctx, conn, HostHelloMessage(HostHello{SelectedVersion: ProtocolV1, BulkConnectionToken: &token}))
	}))
	defer server.Close()
	provider := NewWebSocketProvider("ws"+strings.TrimPrefix(server.URL, "http"), server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := provider.connect(ctx); err == nil || !strings.Contains(err.Error(), "unexpected bulk pairing token") {
		t.Fatalf("connect() error = %v, want unexpected bulk token", err)
	}
}

func TestDualWebSocketRejectsBulkMessageOnControlLane(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := request.Context()
		if strings.HasSuffix(request.URL.Path, "/bulk/tok-lane") {
			<-ctx.Done()
			return
		}
		var hello ClientToHost
		if !readClientFrame(t, ctx, conn, &hello) {
			return
		}
		token := "tok-lane"
		writeHostFrame(t, ctx, conn, HostHelloMessage(HostHello{SelectedVersion: ProtocolV1, Capabilities: CapabilitySet{Capability(DualWebSocketCapability)}, BulkConnectionToken: &token}))
		// Send a bulk-family message (tool invocation) on the control socket:
		// the client must invalidate the connection.
		writeHostFrame(t, ctx, conn, DelegateRequestMessage(60, SessionID("s-lane"), InvokeToolRequest(NestedToolCall{
			CellID: CellID("c"), RuntimeToolCallID: "n", ToolName: PlainToolName("exec_command"), ProtocolToolKind: ProtocolToolKindFunction, Input: json.RawMessage(`{}`),
		})))
		// Keep the socket open so the client observes the mis-laned frame
		// instead of racing the connection close.
		<-ctx.Done()
	}))
	defer server.Close()
	provider := NewWebSocketProvider("ws"+strings.TrimPrefix(server.URL, "http"), server.Client())
	session := provider.NewSession(&recordingRemoteDelegate{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := session.Execute(ctx, tool.CodeModeRemoteExecuteRequest{Source: `text("x")`})
	if err == nil || !strings.Contains(err.Error(), "bulk message on the control websocket") {
		t.Fatalf("Execute() error = %v, want lane enforcement failure", err)
	}
}

func TestRemoteSessionConcurrentFirstExecuteOpensOnce(t *testing.T) {
	var openCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := request.Context()
		var hello ClientToHost
		if !readClientFrame(t, ctx, conn, &hello) {
			return
		}
		writeHostFrame(t, ctx, conn, HostHelloMessage(HostHello{SelectedVersion: ProtocolV1}))

		for completed := 0; completed < 2; {
			var message ClientToHost
			if !readClientFrame(t, ctx, conn, &message) || message.Request == nil {
				return
			}
			switch message.Request.Method {
			case "session/open":
				openCount.Add(1)
				writeHostFrame(t, ctx, conn, HostOperationResponse(message.ID, ResultOK(SessionReady(message.Request.SessionID))))
			case "session/execute":
				completed++
				cellID := CellID(fmt.Sprintf("cell-%d", completed))
				writeHostFrame(t, ctx, conn, HostOperationResponse(message.ID, ResultOK(ExecutionStarted(cellID))))
				writeHostFrame(t, ctx, conn, InitialResponse(message.ID, ResultOK(Result(cellID, []ContentItem{InputText("OK")}, nil))))
			default:
				t.Errorf("unexpected method %q", message.Request.Method)
				return
			}
		}
	}))
	defer server.Close()

	provider := NewWebSocketProvider("ws"+strings.TrimPrefix(server.URL, "http"), server.Client())
	session := provider.NewSession(&recordingRemoteDelegate{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := session.Execute(ctx, tool.CodeModeRemoteExecuteRequest{Source: `text("OK")`})
			errs <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	}
	if got := openCount.Load(); got != 1 {
		t.Fatalf("session/open count = %d, want 1", got)
	}
	_ = provider.Close()
}

func TestRemoteSessionReopensAfterConnectionLoss(t *testing.T) {
	var connectionCount atomic.Int32
	var openCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connectionNumber := connectionCount.Add(1)
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := request.Context()
		var hello ClientToHost
		if !readClientFrame(t, ctx, conn, &hello) {
			return
		}
		writeHostFrame(t, ctx, conn, HostHelloMessage(HostHello{SelectedVersion: ProtocolV1}))
		var open ClientToHost
		if !readClientFrame(t, ctx, conn, &open) || open.Request == nil || open.Request.Method != "session/open" {
			return
		}
		openCount.Add(1)
		writeHostFrame(t, ctx, conn, HostOperationResponse(open.ID, ResultOK(SessionReady(open.Request.SessionID))))
		var execute ClientToHost
		if !readClientFrame(t, ctx, conn, &execute) || execute.Request == nil || execute.Request.Method != "session/execute" {
			return
		}
		cellID := CellID(fmt.Sprintf("cell-%d", connectionNumber))
		writeHostFrame(t, ctx, conn, HostOperationResponse(execute.ID, ResultOK(ExecutionStarted(cellID))))
		writeHostFrame(t, ctx, conn, InitialResponse(execute.ID, ResultOK(Result(cellID, []ContentItem{InputText("OK")}, nil))))
	}))
	defer server.Close()

	provider := NewWebSocketProvider("ws"+strings.TrimPrefix(server.URL, "http"), server.Client())
	session := provider.NewSession(&recordingRemoteDelegate{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.Execute(ctx, tool.CodeModeRemoteExecuteRequest{Source: `text("ONE")`}); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	concrete := session.(*remoteSession)
	deadline := time.Now().Add(2 * time.Second)
	for {
		concrete.mu.Lock()
		connection := concrete.openedOn
		concrete.mu.Unlock()
		if connection != nil && !connection.Alive() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first connection did not close")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := session.Execute(ctx, tool.CodeModeRemoteExecuteRequest{Source: `text("TWO")`}); err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if got := openCount.Load(); got != 2 {
		t.Fatalf("session/open count = %d, want 2", got)
	}
	_ = provider.Close()
}

func TestWebSocketTransportRejectsTextFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"connection/ready"}`))
	}))
	defer server.Close()
	transport, _, err := DialWebSocket(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), server.Client())
	if err != nil {
		t.Fatalf("DialWebSocket() error = %v", err)
	}
	defer transport.Close()
	var message HostToClient
	_, err = transport.Read(context.Background(), &message)
	if err == nil || !strings.Contains(err.Error(), "must be binary framed messages") {
		t.Fatalf("Read(text frame) error = %v", err)
	}
}

func TestRemoteConnectionDelegateLimitRecoversWithoutDisconnecting(t *testing.T) {
	connection := &remoteConnection{
		alive:   true,
		cancels: map[DelegateRequestID]context.CancelFunc{},
	}
	cancels := make([]context.CancelFunc, 0, MaxPendingDelegateCalls)
	for value := 1; value <= MaxPendingDelegateCalls; value++ {
		_, cancel, ok := connection.reserveDelegate(DelegateRequestID(value))
		if !ok {
			t.Fatalf("reserveDelegate(%d) rejected before capacity", value)
		}
		cancels = append(cancels, cancel)
	}
	if _, overflowCancel, ok := connection.reserveDelegate(DelegateRequestID(MaxPendingDelegateCalls + 1)); ok || overflowCancel != nil || !connection.Alive() {
		t.Fatalf("overflow reservation = %t cancel=%v alive=%t", ok, overflowCancel != nil, connection.Alive())
	}
	connection.releaseDelegate(1, cancels[0])
	_, recoveredCancel, ok := connection.reserveDelegate(DelegateRequestID(MaxPendingDelegateCalls + 1))
	if !ok || recoveredCancel == nil || !connection.Alive() {
		t.Fatalf("connection did not recover capacity: ok=%t cancel=%v alive=%t", ok, recoveredCancel != nil, connection.Alive())
	}
	connection.releaseDelegate(DelegateRequestID(MaxPendingDelegateCalls+1), recoveredCancel)
	for value := 2; value <= MaxPendingDelegateCalls; value++ {
		connection.releaseDelegate(DelegateRequestID(value), cancels[value-1])
	}
}

func TestWebSocketTransportRejectsOversizedAndTruncatedFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_ = conn.Write(request.Context(), websocket.MessageBinary, []byte{10, 0, 0, 0, '{'})
	}))
	defer server.Close()
	transport, _, err := DialWebSocket(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), server.Client())
	if err != nil {
		t.Fatalf("DialWebSocket() error = %v", err)
	}
	defer transport.Close()
	var message HostToClient
	_, err = transport.Read(context.Background(), &message)
	if err == nil || !errors.Is(err, bytes.ErrTooLarge) && !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("Read(truncated frame) error = %v", err)
	}
}

func readClientFrame(t *testing.T, ctx context.Context, conn *websocket.Conn, target *ClientToHost) bool {
	t.Helper()
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
			return false
		}
		t.Errorf("Read() error = %v", err)
		return false
	}
	if messageType != websocket.MessageBinary {
		t.Errorf("message type = %v, want binary", messageType)
		return false
	}
	ok, err := NewFramedReader(bytes.NewReader(payload)).Read(target)
	if err != nil {
		t.Errorf("decode frame error = %v", err)
		return false
	}
	return ok
}

func writeHostFrame(t *testing.T, ctx context.Context, conn *websocket.Conn, message HostToClient) {
	t.Helper()
	frame, err := EncodeFrame(message)
	if err != nil {
		t.Fatalf("EncodeFrame() error = %v", err)
	}
	payload, err := (&frame).Bytes()
	if err != nil {
		t.Fatalf("frame.Bytes() error = %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}
