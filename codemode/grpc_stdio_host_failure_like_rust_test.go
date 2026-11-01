package codemode

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Mirrors Rust code-mode-host/src/grpc/service_tests.rs
// `server_output_budgets_follow_exec_wait_and_terminate_over_grpc` for the
// failure branch: over the gRPC transport the host reports the script
// diagnostic and, with server truncation, one budgeted observation that carries
// the `Script error:` label.

func TestGrpcStdioHostReportsScriptFailuresLikeRust(t *testing.T) {
	hostSide, clientSide := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- RunGrpcStdioHost(context.Background(), hostSide, hostSide)
	}()
	t.Cleanup(func() {
		_ = clientSide.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("gRPC stdio host did not exit")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "passthrough:///stdio",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return newStdioConn(clientSide, clientSide), nil
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	stream, err := codemodev1.NewCodeModeHostClient(conn).Transport(ctx)
	if err != nil {
		t.Fatalf("transport stream error = %v", err)
	}

	send := func(message any) {
		t.Helper()
		payload, err := json.Marshal(message)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if err := stream.Send(&codemodev1.FramedMessage{Payload: payload}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	next := func() HostToClient {
		t.Helper()
		message, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		var frame HostToClient
		if err := json.Unmarshal(message.Payload, &frame); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return frame
	}

	versions, _ := NewSupportedProtocolVersions(ProtocolV1)
	hello, _ := NewClientHello(versions, CapabilitySet{}, CapabilitySet{})
	send(ClientHelloMessage(hello))
	if frame := next(); frame.Type != "connection/ready" {
		t.Fatalf("handshake frame = %#v", frame)
	}

	server := OutputTruncationServer
	sessionID, _ := NewSessionID("grpc-failure")
	send(ClientToHost{Type: "operation/request", ID: 1, Request: &HostRequest{Method: "session/open", SessionID: sessionID, OutputTruncation: &server}})
	if frame := next(); frame.Result == nil || frame.Result.Value.Type != "session/ready" {
		t.Fatalf("session/open frame = %#v", frame)
	}

	yield := uint64(60000)
	budget := 7
	send(ClientToHost{Type: "operation/request", ID: 2, Request: &HostRequest{
		Method:    "session/execute",
		SessionID: sessionID,
		Request: &ExecuteRequest{
			ToolCallID:      "call-grpc-failure",
			Source:          "text('partial'); throw new Error('boom');",
			YieldTimeMS:     &yield,
			MaxOutputTokens: &budget,
		},
	}})
	if frame := next(); frame.Result == nil || frame.Result.Value.Type != "execution/started" {
		t.Fatalf("execution/started frame = %#v", frame)
	}
	initial := next()
	if initial.Initial == nil || initial.Initial.Status != "ok" {
		t.Fatalf("initial response = %#v", initial.Initial)
	}
	response := initial.Initial.Value
	if response.Variant != "Result" {
		t.Fatalf("variant = %q, want Result", response.Variant)
	}
	if response.ErrorText == nil || *response.ErrorText != "Error: boom" {
		t.Fatalf("error_text = %#v", response.ErrorText)
	}
	want := "Warning: truncated output (original token count: 9)\nTotal output lines: 3\n\npartial\nScript…2 tokens truncated…r:\nError: boom"
	if got := observationText(response.ContentItems); got != want {
		t.Fatalf("gRPC failure observation =\n%q\nwant\n%q", got, want)
	}
}

// The same contract holds for the wait that observes a yielded cell: the
// failure arrives on the second observation with its diagnostic and one
// budgeted \`Script error:\` item.
func TestGrpcStdioHostReportsFailuresAfterAYieldLikeRust(t *testing.T) {
	_, send, next := startGrpcHostForFailureTest(t)
	server := OutputTruncationServer
	sessionID, _ := NewSessionID("grpc-late-failure")
	send(ClientToHost{Type: "operation/request", ID: 1, Request: &HostRequest{Method: "session/open", SessionID: sessionID, OutputTruncation: &server}})
	if frame := next(); frame.Result == nil || frame.Result.Value.Type != "session/ready" {
		t.Fatalf("session/open frame = %#v", frame)
	}
	yield := uint64(60000)
	budget := 7
	send(ClientToHost{Type: "operation/request", ID: 2, Request: &HostRequest{
		Method:    "session/execute",
		SessionID: sessionID,
		Request: &ExecuteRequest{
			ToolCallID:      "call-grpc-late",
			Source:          "text('partial'); yield_control(); throw '0123456789'.repeat(8);",
			YieldTimeMS:     &yield,
			MaxOutputTokens: &budget,
		},
	}})
	if frame := next(); frame.Result == nil || frame.Result.Value.Type != "execution/started" {
		t.Fatalf("execution/started frame = %#v", frame)
	}
	initial := next()
	if initial.Initial == nil || initial.Initial.Status != "ok" || initial.Initial.Value.Variant != "Yielded" {
		t.Fatalf("first observation = %#v", initial.Initial)
	}
	if got := observationText(initial.Initial.Value.ContentItems); got != "partial" {
		t.Fatalf("first observation = %q, want the script output once", got)
	}
	cellID := initial.Initial.Value.CellID
	send(ClientToHost{Type: "operation/request", ID: 3, Request: &HostRequest{
		Method:          "session/wait",
		SessionID:       sessionID,
		CellID:          cellID,
		MaxOutputTokens: &budget,
		Wait:            &WaitRequest{CellID: cellID, YieldTimeMS: 5000, MaxOutputTokens: &budget},
	}})
	var outcome *WaitOutcome
	for attempt := 0; attempt < 4 && outcome == nil; attempt++ {
		frame := next()
		if frame.Result != nil && frame.Result.Status == "ok" && frame.Result.Value.Outcome != nil {
			outcome = frame.Result.Value.Outcome
		}
	}
	if outcome == nil {
		t.Fatalf("no wait outcome")
	}
	response := outcome.Response
	diagnostic := strings.Repeat("0123456789", 8)
	if response.ErrorText == nil || *response.ErrorText != diagnostic {
		t.Fatalf("wait error_text = %#v, want the script diagnostic", response.ErrorText)
	}
	// The yield consumed the pre-yield text, so the wait observes only the
	// failure, exactly like Rust's collector handoff and its 24-token header.
	want := "Warning: truncated output (original token count: 24)\nTotal output lines: 2\n\nScript error:\n…17 tokens truncated…67890123456789"
	if got := observationText(response.ContentItems); got != want {
		t.Fatalf("wait failure observation =\n%q\nwant\n%q", got, want)
	}
}

func startGrpcHostForFailureTest(t *testing.T) (stream codemodev1.CodeModeHost_TransportClient, send func(any), next func() HostToClient) {
	t.Helper()
	hostSide, clientSide := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- RunGrpcStdioHost(context.Background(), hostSide, hostSide)
	}()
	t.Cleanup(func() {
		_ = clientSide.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("gRPC stdio host did not exit")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	conn, err := grpc.DialContext(ctx, "passthrough:///stdio",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return newStdioConn(clientSide, clientSide), nil
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	transport, err := codemodev1.NewCodeModeHostClient(conn).Transport(ctx)
	if err != nil {
		t.Fatalf("transport stream error = %v", err)
	}
	send = func(message any) {
		t.Helper()
		payload, err := json.Marshal(message)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if err := transport.Send(&codemodev1.FramedMessage{Payload: payload}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	next = func() HostToClient {
		t.Helper()
		message, err := transport.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		var frame HostToClient
		if err := json.Unmarshal(message.Payload, &frame); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return frame
	}
	versions, _ := NewSupportedProtocolVersions(ProtocolV1)
	hello, _ := NewClientHello(versions, CapabilitySet{}, CapabilitySet{})
	send(ClientHelloMessage(hello))
	handshake := next()
	if handshake.Type != "connection/ready" {
		t.Fatalf("handshake frame = %#v", handshake)
	}
	return transport, send, next
}
