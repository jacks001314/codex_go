package codemode

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"codex_go/tool"
	"google.golang.org/grpc"
)

func TestValidateGrpcCodeModeEndpointMatchesRust(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		want     bool
	}{
		{endpoint: "http://127.0.0.1:8080", want: true},
		{endpoint: "https://codex.example.com", want: true},
		{endpoint: "unix:///tmp/codex.sock", want: true},
		{endpoint: "unix:/tmp/codex.sock", want: true},
		{endpoint: "unix:///tmp/codex.sock", want: true},
		{endpoint: "unix:/tmp/codex.sock", want: true},
		{endpoint: "http://127.0.0.1:8080/", want: true},
		{endpoint: "ws://127.0.0.1:8080", want: false},
		{endpoint: "wss://127.0.0.1:8080", want: false},
		{endpoint: "file:///tmp/codex", want: false},
		{endpoint: "http://127.0.0.1:8080/path", want: false},
		{endpoint: "http://127.0.0.1:8080?query=1", want: false},
		{endpoint: "http://127.0.0.1:8080#frag", want: false},
		{endpoint: "not a url", want: false},
		{endpoint: "", want: false},
	} {
		got := validateGrpcCodeModeEndpoint(tc.endpoint) == nil
		if got != tc.want {
			t.Fatalf("validateGrpcCodeModeEndpoint(%q) valid = %v, want %v", tc.endpoint, got, tc.want)
		}
	}
}

func TestUsesGrpcCodeModeEndpoint(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		want     bool
	}{
		{endpoint: "http://127.0.0.1:8080", want: true},
		{endpoint: "https://codex.example.com", want: true},
		{endpoint: "ws://127.0.0.1:8080", want: false},
		{endpoint: "wss://127.0.0.1:8080", want: false},
		{endpoint: "", want: false},
	} {
		if got := UsesGrpcCodeModeEndpoint(tc.endpoint); got != tc.want {
			t.Fatalf("UsesGrpcCodeModeEndpoint(%q) = %v, want %v", tc.endpoint, got, tc.want)
		}
	}
}

func TestGrpcTargetForEndpointAddsSchemeDefaultPorts(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		want     string
	}{
		{endpoint: "http://127.0.0.1:8080", want: "127.0.0.1:8080"},
		{endpoint: "https://codex.example.com", want: "codex.example.com:443"},
		{endpoint: "http://codex.example.com", want: "codex.example.com:80"},
		{endpoint: "https://codex.example.com:9443", want: "codex.example.com:9443"},
	} {
		if got := grpcTargetForEndpoint(tc.endpoint); got != tc.want {
			t.Fatalf("grpcTargetForEndpoint(%q) = %q, want %q", tc.endpoint, got, tc.want)
		}
	}
}

func TestGrpcCodeModeSessionProviderEndToEnd(t *testing.T) {
	server := grpc.NewServer()
	handler := &grpcSessionHostServer{}
	codemodev1.RegisterCodeModeHostServer(server, handler)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	endpoint := "http://" + listener.Addr().String()
	provider := NewGrpcCodeModeSessionProvider(endpoint, &http.Client{Transport: http.DefaultTransport})
	defer provider.Close()
	if err := provider.Availability(); err != nil {
		t.Fatalf("Availability() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session := provider.NewSession(&recordingGrpcDelegate{})

	response, err := session.Execute(ctx, tool.CodeModeRemoteExecuteRequest{
		ToolCallID: "call-1",
		Source:     "test",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.State != "completed" || len(response.ContentItems) != 1 || response.ContentItems[0]["text"] != "hello from host" {
		t.Fatalf("execute response = %#v", response)
	}
	wait, err := session.Wait(ctx, "cell-1", 0)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if wait.State != "completed" {
		t.Fatalf("wait response = %#v", wait)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

type recordingGrpcDelegate struct{}

func (d *recordingGrpcDelegate) Invoke(context.Context, tool.CodeModeRemoteNestedCall) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func (d *recordingGrpcDelegate) Notify(context.Context, string, string, string) error { return nil }

// grpcSessionHostServer is a minimal code-mode host answering the handshake
// and the open/execute/wait/shutdown session operations over the gRPC
// Transport stream, using the same JSON framing as the client.
type grpcSessionHostServer struct {
	codemodev1.UnimplementedCodeModeHostServer
}

func (s *grpcSessionHostServer) Transport(stream codemodev1.CodeModeHost_TransportServer) error {
	for {
		message, err := stream.Recv()
		if err != nil {
			return nil
		}
		if message == nil {
			continue
		}
		var envelope ClientToHost
		if err := json.Unmarshal(message.Payload, &envelope); err != nil {
			return err
		}
		switch envelope.Type {
		case "connection/hello":
			payload, _ := json.Marshal(HostHelloMessage(HostHello{SelectedVersion: ProtocolV1, Capabilities: CapabilitySet{}}))
			if err := stream.Send(&codemodev1.FramedMessage{Payload: payload}); err != nil {
				return err
			}
		case "operation/request":
			if err := s.handleOperation(stream, envelope); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (s *grpcSessionHostServer) handleOperation(stream codemodev1.CodeModeHost_TransportServer, envelope ClientToHost) error {
	if envelope.Request == nil {
		return nil
	}
	method := envelope.Request.Method
	var response HostResponse
	switch method {
	case "session/open":
		response = SessionReady(envelope.Request.SessionID)
	case "session/shutdown":
		response = SessionClosed(envelope.Request.SessionID)
	case "session/execute":
		started := HostOperationResponse(envelope.ID, ResultOK(ExecutionStarted(CellID("cell-1"))))
		payload, _ := json.Marshal(started)
		if err := stream.Send(&codemodev1.FramedMessage{Payload: payload}); err != nil {
			return err
		}
		items := []ContentItem{{Type: "text", Text: "hello from host"}}
		initial := InitialResponse(envelope.ID, ResultOK(Result(CellID("cell-1"), items, nil)))
		payload, _ = json.Marshal(initial)
		return stream.Send(&codemodev1.FramedMessage{Payload: payload})
	case "session/wait":
		response = WaitCompleted(LiveCell(Result(CellID("cell-1"), []ContentItem{{Type: "text", Text: "done"}}, nil)))
	case "session/terminate":
		response = WaitCompleted(LiveCell(Result(CellID(envelope.Request.CellID.String()), nil, nil)))
	default:
		return nil
	}
	payload, err := json.Marshal(HostOperationResponse(envelope.ID, ResultOK(response)))
	if err != nil {
		return err
	}
	return stream.Send(&codemodev1.FramedMessage{Payload: payload})
}
