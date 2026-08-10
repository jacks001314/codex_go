package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	codemodev1 "codex_go/codemode/grpc"
	"google.golang.org/grpc"
)

// GrpcTransport adapts the code-mode IPC JSON framing protocol to the generic
// bidirectional Transport RPC on the code-mode host gRPC service (Rust
// 8073dbb20b/61a3dd4387/c0ad3ab014). Each frame is carried as one FramedMessage
// payload on a single stream, so the existing protocol codec, session state,
// and delegate routing remain unchanged.
type GrpcTransport struct {
	mu      sync.Mutex
	stream  codemodev1.CodeModeHost_TransportClient
	conn    *grpc.ClientConn
	closed  bool
	readBuf [][]byte
}

// DialGrpcTransport connects to a code-mode host gRPC endpoint and opens the
// generic Transport stream.
func DialGrpcTransport(ctx context.Context, target string, dialOptions ...grpc.DialOption) (*GrpcTransport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := grpc.DialContext(ctx, target, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial code-mode gRPC host: %w", err)
	}
	client := codemodev1.NewCodeModeHostClient(conn)
	stream, err := client.Transport(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to open code-mode gRPC transport stream: %w", err)
	}
	return &GrpcTransport{stream: stream, conn: conn}, nil
}

// NewGrpcTransport wraps an established Transport stream.
func NewGrpcTransport(stream codemodev1.CodeModeHost_TransportClient, conn *grpc.ClientConn) *GrpcTransport {
	return &GrpcTransport{stream: stream, conn: conn}
}

// Read receives the next framed message from the gRPC stream (remoteTransport).
func (t *GrpcTransport) Read(ctx context.Context, target any) (bool, error) {
	if t == nil {
		return false, fmt.Errorf("code-mode gRPC transport is nil")
	}
	t.mu.Lock()
	if len(t.readBuf) > 0 {
		payload := t.readBuf[0]
		t.readBuf = t.readBuf[1:]
		t.mu.Unlock()
		if err := decodeGrpcFramedPayload(payload, target); err != nil {
			return false, err
		}
		return true, nil
	}
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return false, nil
	}
	message, err := t.stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
			t.mu.Lock()
			t.closed = true
			t.mu.Unlock()
			return false, nil
		}
		return false, fmt.Errorf("failed to read code-mode gRPC message: %w", err)
	}
	if message == nil || len(message.Payload) == 0 {
		return false, nil
	}
	if err := decodeGrpcFramedPayload(message.Payload, target); err != nil {
		return false, err
	}
	return true, nil
}

// Write sends one framed message over the gRPC stream (remoteTransport).
func (t *GrpcTransport) Write(ctx context.Context, message any) error {
	if t == nil || t.stream == nil {
		return fmt.Errorf("code-mode gRPC transport is nil")
	}
	frame, err := EncodeFrame(message)
	if err != nil {
		return err
	}
	if err := t.stream.Send(&codemodev1.FramedMessage{Payload: frame.Payload}); err != nil {
		return fmt.Errorf("failed to write code-mode gRPC message: %w", err)
	}
	return nil
}

// Close terminates the gRPC stream and connection.
func (t *GrpcTransport) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	stream := t.stream
	conn := t.conn
	t.mu.Unlock()
	if stream != nil {
		_ = stream.CloseSend()
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func decodeGrpcFramedPayload(payload []byte, target any) error {
	if target == nil {
		return fmt.Errorf("decode target is nil")
	}
	if len(payload) == 0 {
		return fmt.Errorf("empty gRPC framed payload")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("failed to decode code-mode gRPC frame: %w", err)
	}
	return nil
}
