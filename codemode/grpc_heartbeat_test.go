package codemode

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestGrpcHeartbeatWriterDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames := make(chan []byte, 2)
	w := newGrpcHeartbeatWriter(ctx, func(frame *codemodev1.FramedMessage) error {
		frames <- frame.Payload
		return nil
	})
	id, _ := NewSessionID("disabled")
	w.configure(id, 0)
	select {
	case frame := <-frames:
		t.Fatalf("disabled heartbeat emitted %s", frame)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestGrpcTransportIdleHeartbeatWireAndShutdown(t *testing.T) {
	hostSide, clientSide := net.Pipe()
	hostDone := make(chan error, 1)
	go func() { hostDone <- RunGrpcStdioHost(context.Background(), hostSide, hostSide) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "passthrough:///stdio",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return newStdioConn(clientSide, clientSide), nil
		}), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	stream, err := codemodev1.NewCodeModeHostClient(conn).Transport(ctx)
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	send := func(message any) {
		t.Helper()
		payload, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Send(&codemodev1.FramedMessage{Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	recv := func() HostToClient {
		t.Helper()
		frame, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		var out HostToClient
		if err := json.Unmarshal(frame.Payload, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	versions, _ := NewSupportedProtocolVersions(ProtocolV1)
	hello, _ := NewClientHello(versions, CapabilitySet{}, CapabilitySet{})
	send(ClientHelloMessage(hello))
	if got := recv(); got.Type != "connection/ready" {
		t.Fatalf("ready = %#v", got)
	}
	sessionID, _ := NewSessionID("heartbeat-wire")
	send(ClientToHost{Type: "operation/request", ID: 1, Request: &HostRequest{
		Method: "session/open", SessionID: sessionID, HeartbeatIntervalSeconds: 1,
	}})
	if got := recv(); got.Result == nil || got.Result.Value.Type != "session/ready" {
		t.Fatalf("session ready = %#v", got)
	}
	heartbeat := make(chan HostToClient, 1)
	go func() { heartbeat <- recv() }()
	select {
	case got := <-heartbeat:
		if got.Type != "heartbeat" || got.SessionID != sessionID {
			t.Fatalf("heartbeat = %#v", got)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("idle heartbeat did not arrive")
	}
	// Shutdown removes the session timer and its ordinary response remains
	// usable immediately after an idle heartbeat.
	send(ClientToHost{Type: "operation/request", ID: 2, Request: &HostRequest{Method: "session/shutdown", SessionID: sessionID}})
	if got := recv(); got.Result == nil || got.Result.Value.Type != "session/closed" {
		t.Fatalf("shutdown = %#v", got)
	}
	_ = stream.CloseSend()
	_ = clientSide.Close()
	select {
	case <-hostDone:
	case <-time.After(time.Second):
		t.Fatal("host did not exit after client disconnect")
	}
}

func TestGrpcHeartbeatWriterRepeatsAndResetsOnOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames := make(chan []byte, 8)
	w := newGrpcHeartbeatWriter(ctx, func(frame *codemodev1.FramedMessage) error {
		frames <- append([]byte(nil), frame.Payload...)
		return nil
	})
	id, _ := NewSessionID("active")
	w.configure(id, 1)
	select {
	case payload := <-frames:
		var message HostToClient
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type != "heartbeat" || message.SessionID != id {
			t.Fatalf("heartbeat = %#v", message)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("heartbeat did not arrive")
	}
	// Normal output resets the deadline. A frame sent immediately after the
	// first tick must therefore not be followed by a second tick for another
	// full interval.
	if err := w.write(map[string]any{"type": "ordinary-output"}); err != nil {
		t.Fatalf("normal write: %v", err)
	}
	// Consume the normal frame itself; only a second frame during the next
	// fraction of the interval would indicate that the deadline was not reset.
	select {
	case <-frames:
	case <-time.After(time.Second):
		t.Fatal("normal output did not arrive")
	}
	select {
	case payload := <-frames:
		var message struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &message); err == nil && message.Type == "heartbeat" {
			t.Fatalf("heartbeat was not reset by normal output: %#v", message)
		}
	case <-time.After(350 * time.Millisecond):
	}
}

func TestGrpcHeartbeatWriterCancellationUnblocksWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	w := newGrpcHeartbeatWriter(ctx, func(frame *codemodev1.FramedMessage) error {
		close(started)
		<-release
		return nil
	})
	writeDone := make(chan error, 1)
	go func() { writeDone <- w.write(map[string]any{"type": "test"}) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start send")
	}
	cancel()
	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("write succeeded after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("write did not observe cancellation")
	}
	close(release)
}
