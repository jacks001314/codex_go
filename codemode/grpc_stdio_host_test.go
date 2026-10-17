package codemode

import (
	"context"
	"net"
	"testing"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Mirrors Rust #52723: the host process exits when its stdio connection closes
// rather than waiting for another client.
func TestGrpcStdioHostExitsWhenConnectionClosesLikeRust(t *testing.T) {
	hostSide, clientSide := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- RunGrpcStdioHost(context.Background(), hostSide, hostSide)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	stream, err := codemodev1.NewCodeModeHostClient(conn).Transport(context.Background())
	if err != nil {
		t.Fatalf("transport stream error = %v", err)
	}
	_ = stream
	_ = conn.Close()
	_ = clientSide.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunGrpcStdioHost() error = %v, want a clean exit", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gRPC stdio host did not exit after its connection closed")
	}
}
