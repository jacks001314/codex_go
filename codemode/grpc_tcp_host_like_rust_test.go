package codemode

import (
	"context"
	"net"
	"testing"
	"time"

	"codex_go/tool"
)

// Rust's `--listen grpc://IP:PORT` serves the same gRPC code-mode host on a TCP
// listener (code-mode-host/src/transport.rs); Go previously only had the stdio
// transport wired into the host binary.
func TestServeGrpcTCPHostServesExecLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- ServeGrpcTCPHost(ctx, listener) }()

	provider := NewGrpcCodeModeSessionProvider("http://"+listener.Addr().String(), nil)
	runtime := tool.NewCodeModeRuntime(provider, false)
	execExecutor, _ := runtime.Executors(tool.NewRegistry())
	output, err := execExecutor.Execute(context.Background(), &tool.Invocation{
		CallID:  "tcp-host-call",
		Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `text("TCP_HOST_OK")`},
	})
	if err != nil {
		cancel()
		<-served
		t.Fatalf("Execute() error = %v", err)
	}
	if body := codeModeBodyAfterHeader(t, output.Body); body != "TCP_HOST_OK" {
		t.Fatalf("body = %q", body)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("runtime.Close() error = %v", err)
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("ServeGrpcTCPHost() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ServeGrpcTCPHost did not stop after cancellation")
	}
}
