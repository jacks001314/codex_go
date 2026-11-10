package codemode

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	codemodev1 "codex_go/codemode/grpc"
	"codex_go/tool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// rejectingAdmissionHost fails every session/open with one fixed gRPC status so
// the typed open error can be asserted end to end.
type rejectingAdmissionHost struct {
	grpcSessionHostServer
	code codes.Code

	mu       sync.Mutex
	attempts int
}

func (h *rejectingAdmissionHost) openAttempts() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attempts
}

func (h *rejectingAdmissionHost) Transport(stream codemodev1.CodeModeHost_TransportServer) error {
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
				h.attempts++
				h.mu.Unlock()
				return status.Error(h.code, h.code.String())
			}
		default:
			return nil
		}
	}
}

// TestGrpcOpenSessionKeepsTerminalAdmissionStatus pins Rust #60496: a terminal
// admission status reaches the caller as a typed error with its gRPC code, and
// it is not retried.
func TestGrpcOpenSessionKeepsTerminalAdmissionStatus(t *testing.T) {
	for _, code := range []codes.Code{codes.PermissionDenied, codes.InvalidArgument, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			host := &rejectingAdmissionHost{code: code}
			provider := NewGrpcCodeModeSessionProvider(startGrpcCodeModeHost(t, host), &http.Client{Transport: http.DefaultTransport})
			defer provider.Close()

			session, openErr := provider.OpenSession(context.Background(), &recordingGrpcDelegate{}, tool.CodeModeSessionOptions{})
			if session != nil {
				t.Fatal("OpenSession returned a session for a rejected admission")
			}
			if openErr == nil {
				t.Fatal("OpenSession returned no error for a rejected admission")
			}
			if !openErr.IsAdmissionStatus() || openErr.Code != code {
				t.Fatalf("open error = %#v, want typed %v", openErr, code)
			}
			if openErr.Timeout {
				t.Fatalf("terminal status %v was reported as an admission timeout", code)
			}
			if attempts := host.openAttempts(); attempts != 1 {
				t.Fatalf("session/open attempts = %d, want 1 (terminal status is not retried)", attempts)
			}
		})
	}
}

// TestGrpcOpenSessionReportsExhaustedResourceExhaustedRetries pins Rust #60496:
// a retryable ResourceExhausted admission is retried until the attempts run out
// and then surfaces with its original status code.
func TestGrpcOpenSessionReportsExhaustedResourceExhaustedRetries(t *testing.T) {
	host := &rejectingAdmissionHost{code: codes.ResourceExhausted}
	provider := NewGrpcCodeModeSessionProvider(startGrpcCodeModeHost(t, host), &http.Client{Transport: http.DefaultTransport})
	defer provider.Close()

	_, openErr := provider.OpenSession(context.Background(), &recordingGrpcDelegate{}, tool.CodeModeSessionOptions{})
	if openErr == nil || !openErr.IsAdmissionStatus() || openErr.Code != codes.ResourceExhausted {
		t.Fatalf("open error = %#v, want typed ResourceExhausted", openErr)
	}
	if attempts := host.openAttempts(); attempts != grpcSessionAdmissionAttempts {
		t.Fatalf("session/open attempts = %d, want %d", attempts, grpcSessionAdmissionAttempts)
	}
}

// TestGrpcOpenSessionReportsAdmissionTimeout pins the timeout half of Rust
// #60496: the caller's context ending during admission is its own kind instead
// of a status the caller cannot distinguish from a host rejection.
func TestGrpcOpenSessionReportsAdmissionTimeout(t *testing.T) {
	host := &blockingAdmissionHost{release: make(chan struct{})}
	provider := NewGrpcCodeModeSessionProvider(startGrpcCodeModeHost(t, host), &http.Client{Transport: http.DefaultTransport})
	defer provider.Close()
	defer close(host.release)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, openErr := provider.OpenSession(ctx, &recordingGrpcDelegate{}, tool.CodeModeSessionOptions{})
	if openErr == nil {
		t.Fatal("OpenSession returned no error for a cancelled admission")
	}
	if !openErr.Timeout {
		t.Fatalf("open error = %#v, want an admission timeout", openErr)
	}
}

// blockingAdmissionHost accepts the connection hello and then never answers
// session/open until the test releases it, so the client deadline decides.
type blockingAdmissionHost struct {
	grpcSessionHostServer
	release chan struct{}
}

func (h *blockingAdmissionHost) Transport(stream codemodev1.CodeModeHost_TransportServer) error {
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
			<-h.release
			return nil
		default:
			return nil
		}
	}
}
