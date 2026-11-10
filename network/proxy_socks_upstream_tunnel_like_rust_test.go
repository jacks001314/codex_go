package network

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// Rust #60523: opaque SOCKS TCP traffic can be forwarded through a configured
// HTTP CONNECT next hop. The proxy waits for a successful CONNECT response and
// keeps bytes the upstream sent past those headers.
func TestSOCKSUpstreamTunnelForwardsThroughConnectLikeRust(t *testing.T) {
	var connectTargets atomic.Value
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer upstream.Close()
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		request, err := http.ReadRequest(reader)
		if err != nil || request.Method != http.MethodConnect {
			return
		}
		connectTargets.Store(request.Host)
		// Answer and immediately send payload data in the same write so the
		// reader buffer holds bytes past the response headers.
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\nhello")
		_, _ = io.Copy(conn, reader)
	}()

	server := &ProxyServer{}
	server.policy.Store(&proxyRuntimePolicy{settings: ProxySettings{Enabled: true, AllowUpstreamProxy: true}})
	server.baseEnv = map[string]string{"HTTPS_PROXY": "http://" + upstream.Addr().String()}

	conn, used, err := server.dialSOCKSUpstreamTunnel(context.Background(), "1.1.1.1:443")
	if err != nil {
		t.Fatalf("dialSOCKSUpstreamTunnel() error = %v", err)
	}
	if !used {
		t.Fatal("upstream hop was not used")
	}
	defer conn.Close()
	if got := connectTargets.Load(); got != "1.1.1.1:443" {
		t.Fatalf("CONNECT target = %v, want 1.1.1.1:443", got)
	}
	buffer := make([]byte, 5)
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("read buffered payload: %v", err)
	}
	if string(buffer) != "hello" {
		t.Fatalf("buffered payload = %q, want hello", buffer)
	}
}

// A refused CONNECT must fail the dial instead of forwarding payloads.
func TestSOCKSUpstreamTunnelRejectsRefusedConnectLikeRust(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer upstream.Close()
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if _, err := http.ReadRequest(reader); err != nil {
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
	}()

	server := &ProxyServer{}
	server.policy.Store(&proxyRuntimePolicy{settings: ProxySettings{Enabled: true, AllowUpstreamProxy: true}})
	server.baseEnv = map[string]string{"HTTPS_PROXY": "http://" + upstream.Addr().String()}

	conn, used, err := server.dialSOCKSUpstreamTunnel(context.Background(), "1.1.1.1:443")
	if !used {
		t.Fatal("upstream hop was not attempted")
	}
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("a refused CONNECT must fail the tunnel")
	}
}

// An upstream that points back at this proxy is rejected instead of looping.
func TestSOCKSUpstreamTunnelRejectsLoopLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer listener.Close()
	server := &ProxyServer{httpListener: listener}
	server.policy.Store(&proxyRuntimePolicy{settings: ProxySettings{Enabled: true, AllowUpstreamProxy: true}})
	server.baseEnv = map[string]string{"HTTPS_PROXY": "http://127.0.0.1:" + portOf(t, listener.Addr().String())}

	if _, used, err := server.dialSOCKSUpstreamTunnel(context.Background(), "1.1.1.1:443"); !used || err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("loop dial = used %v, err %v", used, err)
	}
}

func portOf(t *testing.T, address string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error = %v", address, err)
	}
	return port
}
