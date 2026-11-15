package network

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseTcpUpstreamProxyLikeRust(t *testing.T) {
	proxy, err := ParseTcpUpstreamProxy("proxy.example:8080")
	if err != nil {
		t.Fatalf("ParseTcpUpstreamProxy() error = %v", err)
	}
	if proxy.Host != "proxy.example" || proxy.Port != 8080 {
		t.Fatalf("proxy = %#v", proxy)
	}
	for _, raw := range []string{"", "proxy.example", "proxy.example:0", "proxy.example:notaport", ":8080"} {
		if _, err := ParseTcpUpstreamProxy(raw); err == nil {
			t.Fatalf("ParseTcpUpstreamProxy(%q) accepted an invalid value", raw)
		}
	}
}

// TestDialMandatoryTCPUpstreamLikeRust pins Rust #61372's trusted next hop: the
// SOCKS target keeps its hostname in the CONNECT authority, and the stream is
// returned only after the proxy answers 200.
func TestDialMandatoryTCPUpstreamLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()
	requests := make(chan string, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		requests <- strings.TrimSpace(line)
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\nok"))
	}()
	addr := listener.Addr().(*net.TCPAddr)
	server := &ProxyServer{}
	server.policy.Store(&proxyRuntimePolicy{settings: ProxySettings{
		Enabled:            true,
		AllowUpstreamProxy: true,
		AllowLocalBinding:  true,
	}})
	upstream := &TcpUpstreamProxy{Host: addr.IP.String(), Port: uint16(addr.Port)}
	conn, err := server.dialMandatoryTCPUpstream(context.Background(), upstream, "example.com:443")
	if err != nil {
		t.Fatalf("dialMandatoryTCPUpstream() error = %v", err)
	}
	defer conn.Close()
	select {
	case request := <-requests:
		if request != "CONNECT example.com:443 HTTP/1.1" {
			t.Fatalf("CONNECT request = %q", request)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream proxy did not receive a CONNECT request")
	}
	buffer := make([]byte, 2)
	if _, err := conn.Read(buffer); err != nil || string(buffer) != "ok" {
		t.Fatalf("tunnel read = %q, %v", buffer, err)
	}
}

func TestMandatoryTCPUpstreamHasNoDirectFallbackLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	_ = listener.Close()
	server := &ProxyServer{}
	server.policy.Store(&proxyRuntimePolicy{settings: ProxySettings{
		Enabled:            true,
		AllowUpstreamProxy: true,
		AllowLocalBinding:  true,
	}})
	server.SetTcpUpstreamProxy(&TcpUpstreamProxy{Host: addr.IP.String(), Port: uint16(addr.Port)})
	conn, used, err := server.dialSOCKSUpstreamTunnel(context.Background(), "example.com:443")
	if conn != nil {
		_ = conn.Close()
	}
	if !used || err == nil {
		t.Fatalf("dialSOCKSUpstreamTunnel() = (%v, %v, %v), want a mandatory upstream failure", conn, used, err)
	}
}

func TestMandatoryTCPUpstreamRejectsLoopAndNonPublicIPLikeRust(t *testing.T) {
	server := &ProxyServer{}
	server.policy.Store(&proxyRuntimePolicy{settings: ProxySettings{
		Enabled:            true,
		AllowUpstreamProxy: true,
		AllowLocalBinding:  true,
	}})
	upstream := &TcpUpstreamProxy{Host: "127.0.0.1", Port: 8080}
	if _, err := server.dialMandatoryTCPUpstream(context.Background(), upstream, "127.0.0.1:8080"); err == nil {
		t.Fatal("dialMandatoryTCPUpstream() accepted a loop back into the upstream listener")
	}
	if _, err := server.dialMandatoryTCPUpstream(context.Background(), upstream, "10.0.0.5:443"); err == nil {
		t.Fatal("dialMandatoryTCPUpstream() accepted a private IP without private-IP upstream routing")
	}
	if _, err := server.dialMandatoryTCPUpstream(context.Background(), upstream, "localhost:"+strconv.Itoa(8443)); err == nil {
		t.Fatal("dialMandatoryTCPUpstream() accepted a localhost target")
	}
}
