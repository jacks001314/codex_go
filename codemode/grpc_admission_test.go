package codemode

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"codex_go/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// startGrpcCodeModeHost serves host on a loopback gRPC listener and returns the
// http origin endpoint the gRPC provider expects (Rust #51185 admission tests).
func startGrpcCodeModeHost(t *testing.T, host codemodev1.CodeModeHostServer) string {
	t.Helper()
	server := grpc.NewServer()
	codemodev1.RegisterCodeModeHostServer(server, host)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return "http://" + listener.Addr().String()
}

// transientAdmissionHost fails the first session/open with a transient gRPC
// status and then serves later admissions normally, so a retrying client
// succeeds while a single-attempt client surfaces the error (Rust #51185).
type transientAdmissionHost struct {
	grpcSessionHostServer

	mu          sync.Mutex
	openAttempt int
}

func (h *transientAdmissionHost) openAttempts() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.openAttempt
}

func (h *transientAdmissionHost) Transport(stream codemodev1.CodeModeHost_TransportServer) error {
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
			if envelope.Request != nil && envelope.Request.Method == "session/open" {
				h.mu.Lock()
				h.openAttempt++
				attempt := h.openAttempt
				h.mu.Unlock()
				if attempt == 1 {
					return status.Error(codes.Unavailable, "transient admission failure")
				}
			}
			if err := h.handleOperation(stream, envelope); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func TestGrpcCodeModeSessionAdmissionRetriesTransientFailureLikeRust(t *testing.T) {
	host := &transientAdmissionHost{}
	endpoint := startGrpcCodeModeHost(t, host)
	provider := NewGrpcCodeModeSessionProvider(endpoint, &http.Client{Transport: http.DefaultTransport})
	defer provider.Close()
	session := provider.NewSession(&recordingGrpcDelegate{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := session.Execute(ctx, tool.CodeModeRemoteExecuteRequest{ToolCallID: "call-1", Source: "test"}, nil)
	// Assert the retry first: with the retry wiring removed this is the
	// assertion that fails (session/open is attempted only once).
	if attempts := host.openAttempts(); attempts != 2 {
		t.Fatalf("session/open attempts = %d, want 2 (one transient failure then a retry) - Rust #51185", attempts)
	}
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.State != "completed" || len(response.ContentItems) != 1 || response.ContentItems[0]["text"] != "hello from host" {
		t.Fatalf("execute response = %#v", response)
	}
}

// transientExecuteHost fails the first session/execute with a transient gRPC
// status; Rust #51185 must never retry an established execution.
type transientExecuteHost struct {
	grpcSessionHostServer

	mu           sync.Mutex
	executeTries int
}

func (h *transientExecuteHost) executeAttempts() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.executeTries
}

func (h *transientExecuteHost) Transport(stream codemodev1.CodeModeHost_TransportServer) error {
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
			if envelope.Request != nil && envelope.Request.Method == "session/execute" {
				h.mu.Lock()
				h.executeTries++
				try := h.executeTries
				h.mu.Unlock()
				if try == 1 {
					return status.Error(codes.Unavailable, "transient execute failure")
				}
			}
			if err := h.handleOperation(stream, envelope); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func TestGrpcCodeModeSessionExecuteIsNeverRetriedLikeRust(t *testing.T) {
	host := &transientExecuteHost{}
	endpoint := startGrpcCodeModeHost(t, host)
	provider := NewGrpcCodeModeSessionProvider(endpoint, &http.Client{Transport: http.DefaultTransport})
	defer provider.Close()
	session := provider.NewSession(&recordingGrpcDelegate{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := session.Execute(ctx, tool.CodeModeRemoteExecuteRequest{ToolCallID: "call-1", Source: "test"}, nil); err == nil {
		t.Fatal("Execute() error = nil, want the transient failure surfaced")
	}
	if tries := host.executeAttempts(); tries != 1 {
		t.Fatalf("session/execute attempts = %d, want 1 (Execute must never be retried) - Rust #51185", tries)
	}
}
