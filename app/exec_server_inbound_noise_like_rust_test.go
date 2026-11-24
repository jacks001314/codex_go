package app

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/auth"
	"codex_go/cli"
)

// Rust #62325: `--listen ws://IP:PORT` with a Noise `--remote` selects the
// inbound transport, and only that shape is accepted.
func TestExecServerInboundNoiseListenOptionsLikeRust(t *testing.T) {
	address, err := execServerInboundNoiseListen(&cli.ExecServerOptions{})
	if err != nil || address != "" {
		t.Fatalf("default inbound listen = %q, %v; want quiet", address, err)
	}
	address, err = execServerInboundNoiseListen(&cli.ExecServerOptions{Listen: "ws://127.0.0.1:8080", ListenSet: true})
	if err != nil || address != "127.0.0.1:8080" {
		t.Fatalf("inbound listen = %q, %v", address, err)
	}
	if _, err := execServerInboundNoiseListen(&cli.ExecServerOptions{Listen: "ws://127.0.0.1:8080", ListenSet: true, RemoteTransport: "direct"}); err == nil || !strings.Contains(err.Error(), "requires Noise transport") {
		t.Fatalf("direct inbound listen error = %v", err)
	}
	if _, err := execServerInboundNoiseListen(&cli.ExecServerOptions{Listen: "ws://127.0.0.1:8080", ListenSet: true, ForwardBound: true}); err == nil || !strings.Contains(err.Error(), "cannot be used with forward") {
		t.Fatalf("forward inbound listen error = %v", err)
	}
	if _, err := execServerInboundNoiseListen(&cli.ExecServerOptions{Listen: "stdio", ListenSet: true}); err == nil {
		t.Fatalf("stdio inbound listen accepted")
	}
}

// Rust #62325: a Noise remote executor started with --listen serves inbound
// WebSockets from the published address instead of dialing the rendezvous URL.
func TestExecServerRemoteListenServesInboundNoiseLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "sk-test")
	t.Setenv(auth.CodexAccessTokenEnv, "")

	registerSeen := make(chan struct{}, 1)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/cloud/environment/env-1/register":
			registerSeen <- struct{}{}
			// The relay URL is unreachable on purpose: inbound mode must never
			// dial it.
			fmt.Fprint(w, `{"environment_id":"env-1","url":"ws://127.0.0.1:1/relay","security_profile":"noise_hybrid_ik_v1","executor_registration_id":"registration-1"}`+"\n")
		case "/cloud/environment/env-1/validate":
			fmt.Fprint(w, `{"valid":true}`+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer registry.Close()

	port := freeLoopbackPort(t)
	listen := fmt.Sprintf("ws://127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, []string{
			"exec-server",
			"--remote", registry.URL,
			"--environment-id", "env-1",
			"--listen", listen,
		}, strings.NewReader(""), &stdout, &stderr)
	}()
	defer func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("exec-server returned error after cancel: %v; stderr=%q", err, stderr.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("exec-server did not stop; stderr=%q", stderr.String())
		}
	}()
	select {
	case <-registerSeen:
	case err := <-errCh:
		t.Fatalf("exec-server exited before registering: %v; stderr=%q", err, stderr.String())
	case <-time.After(5 * time.Second):
		t.Fatalf("exec-server did not register; stderr=%q", stderr.String())
	}
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, listen, nil)
	if err != nil {
		t.Fatalf("dial inbound listener: %v; stderr=%q", err, stderr.String())
	}
	defer conn.CloseNow()
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer writeCancel()
	if err := conn.Write(writeCtx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)); err != nil {
		t.Fatalf("write plaintext request: %v", err)
	}
	readCtx, readCancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer readCancel()
	if messageType, payload, err := conn.Read(readCtx); err == nil {
		t.Fatalf("inbound Noise listener answered plaintext with %d %s", messageType, payload)
	}
}

// freeLoopbackPort reserves and releases a loopback port for the listener.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return port
}
