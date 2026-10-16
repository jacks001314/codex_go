package network

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Regression guard for upstream Rust #53506 (12cca49c81 "Reject duplicate Host
// headers in the network proxy").
//
// Rust carries the check in the proxy itself, next to the request it validates:
//
//	network-proxy/src/http_proxy.rs:193-198  (CONNECT path)
//	network-proxy/src/http_proxy.rs:568-574  (plain path)
//	    if req.headers().get_all(header::HOST).iter().nth(1).is_some() {
//	        return ... text_response(StatusCode::BAD_REQUEST, "multiple Host headers")
//	    }
//
// Go rejects the same shape one layer below the proxy, inside the standard
// library request reader, so the proxy never observes the request at all:
//
//	net/http/request.go:1138-1140
//	    if len(req.Header["Host"]) > 1 {
//	            return nil, fmt.Errorf("too many Host headers")
//	    }
//
// That runs right after ReadMIMEHeader and before "req.Host = req.URL.Host" is
// derived (request.go:1149-1152), i.e. before any proxy handler, before policy
// evaluation and before any upstream dial. The proxy's own single-value Host
// view is network/proxy_server.go:1200-1205 (validateAbsoluteFormHostHeader
// reads only request.Host), which structurally cannot see a second Host line.
//
// These tests turn that static reading into a runtime guard, mirroring the
// upstream cases in network-proxy/src/http_proxy/host_header_tests.rs:
//   - rejects_duplicate_host_fields_before_upstream (:115) - identical and
//     conflicting values, absolute-form and CONNECT, 400 without an upstream
//     connection.
//   - preserves_single_host_forwarding (:137) - a single-Host request is
//     forwarded with exactly one Host field (host_count == 1).

const duplicateHostObservationWindow = 750 * time.Millisecond

// duplicateHostUpstreamProbe stands in for the origin server behind the managed
// proxy. It is a bare TCP listener whose only job is to make an accidental
// upstream connection - and the request head that arrives on it - observable.
type duplicateHostUpstreamProbe struct {
	listener  net.Listener
	accepts   atomic.Int32
	connected chan struct{}
	heads     chan string
}

func startDuplicateHostUpstreamProbe(t *testing.T) *duplicateHostUpstreamProbe {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen error = %v", err)
	}
	probe := &duplicateHostUpstreamProbe{
		listener:  listener,
		connected: make(chan struct{}, 1),
		heads:     make(chan string, 1),
	}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			probe.accepts.Add(1)
			select {
			case probe.connected <- struct{}{}:
			default:
			}
			go func(upstream net.Conn) {
				defer upstream.Close()
				_ = upstream.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(upstream)
				var head strings.Builder
				for {
					line, readErr := reader.ReadString('\n')
					if readErr != nil || line == "\r\n" || line == "\n" {
						break
					}
					head.WriteString(line)
				}
				select {
				case probe.heads <- head.String():
				default:
				}
				_, _ = io.WriteString(upstream, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return probe
}

func (p *duplicateHostUpstreamProbe) address() string {
	return p.listener.Addr().String()
}

// requireUpstreamConnection is the positive control: a legal single-Host request
// must actually arrive, otherwise the fixture is hollow and a 400 elsewhere
// would prove nothing. It returns the request head the origin received.
func (p *duplicateHostUpstreamProbe) requireUpstreamConnection(t *testing.T) string {
	t.Helper()
	select {
	case head := <-p.heads:
		return head
	case <-time.After(3 * time.Second):
		t.Fatalf("fixture is hollow: a legal single-Host request never reached the upstream listener (accepts=%d)", p.accepts.Load())
		return ""
	}
}

// requireNoUpstreamConnection asserts the negative direction: within a bounded
// observation window the origin must see no connection at all, because the
// duplicate-Host request is rejected while the request head is parsed.
func (p *duplicateHostUpstreamProbe) requireNoUpstreamConnection(t *testing.T) {
	t.Helper()
	select {
	case <-p.connected:
		t.Fatalf("upstream accepted a connection (accepts=%d): a duplicate-Host request must be rejected while the request head is parsed, before any upstream dial", p.accepts.Load())
	case <-time.After(duplicateHostObservationWindow):
	}
}

// countForwardedHostHeaders mirrors the upstream host_count == 1 assertion of
// host_header_tests.rs:137.
func countForwardedHostHeaders(head string) int {
	count := 0
	for _, line := range strings.Split(head, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "host:") {
			count++
		}
	}
	return count
}

// sendRawProxyRequest writes one raw request head to the managed proxy and
// returns everything the proxy answered.
func sendRawProxyRequest(t *testing.T, proxyAddress string, raw string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxyAddress, 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy error = %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, raw); err != nil {
		t.Fatalf("write request error = %v", err)
	}
	payload, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read response error = %v", err)
	}
	return string(payload)
}

func firstProxyStatusLine(response string) string {
	if index := strings.Index(response, "\r\n"); index >= 0 {
		return response[:index]
	}
	return strings.TrimSpace(response)
}

func requireProxyStatus(t *testing.T, response string, want string) {
	t.Helper()
	status := firstProxyStatusLine(response)
	if !strings.Contains(status, want) {
		t.Fatalf("proxy status = %q, want it to contain %q (full response = %q)", status, want, response)
	}
}

// duplicateHostRequest builds an absolute-form GET aimed at upstreamAddress with
// hostLines repeated Host header lines.
func duplicateHostRequest(upstreamAddress string, path string, hostLines int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "GET http://%s%s HTTP/1.1\r\n", upstreamAddress, path)
	for index := 0; index < hostLines; index++ {
		fmt.Fprintf(&builder, "Host: %s\r\n", upstreamAddress)
	}
	builder.WriteString("Connection: close\r\n\r\n")
	return builder.String()
}

func TestProxyManagedNetworkRejectsDuplicateHostHeadersLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.EnableSocks5 = false
	settings.AllowLocalBinding = true
	settings.ProxyURL = "http://127.0.0.1:0"
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	prepared, err := StartProxyManagedNetwork(context.Background(), ProxyConfig{Network: settings}, nil)
	if err != nil {
		t.Fatalf("StartProxyManagedNetwork() error = %v", err)
	}
	defer prepared.Close()

	proxyURL, err := url.Parse(prepared.Env["HTTP_PROXY"])
	if err != nil {
		t.Fatalf("parse HTTP_PROXY error = %v", err)
	}
	proxyAddress := proxyURL.Host

	t.Run("single host control reaches upstream", func(t *testing.T) {
		upstream := startDuplicateHostUpstreamProbe(t)
		controlHostLines := 1
		response := sendRawProxyRequest(t, proxyAddress, duplicateHostRequest(upstream.address(), "/control", controlHostLines))
		requireProxyStatus(t, response, " 200 ")
		head := upstream.requireUpstreamConnection(t)
		if got := countForwardedHostHeaders(head); got != 1 {
			t.Fatalf("forwarded Host header count = %d, want 1 (upstream request head = %q)", got, head)
		}
	})

	t.Run("duplicate identical host is rejected", func(t *testing.T) {
		upstream := startDuplicateHostUpstreamProbe(t)
		response := sendRawProxyRequest(t, proxyAddress, duplicateHostRequest(upstream.address(), "/duplicate-same", 2))
		requireProxyStatus(t, response, " 400 ")
		upstream.requireNoUpstreamConnection(t)
	})

	t.Run("duplicate conflicting host is rejected", func(t *testing.T) {
		upstream := startDuplicateHostUpstreamProbe(t)
		raw := "GET http://" + upstream.address() + "/duplicate-conflict HTTP/1.1\r\n" +
			"Host: " + upstream.address() + "\r\n" +
			"Host: 127.0.0.1:1\r\n" +
			"Connection: close\r\n\r\n"
		response := sendRawProxyRequest(t, proxyAddress, raw)
		requireProxyStatus(t, response, " 400 ")
		upstream.requireNoUpstreamConnection(t)
	})

	t.Run("connect with duplicate host is rejected", func(t *testing.T) {
		upstream := startDuplicateHostUpstreamProbe(t)
		raw := "CONNECT " + upstream.address() + " HTTP/1.1\r\n" +
			"Host: " + upstream.address() + "\r\n" +
			"Host: " + upstream.address() + "\r\n\r\n"
		response := sendRawProxyRequest(t, proxyAddress, raw)
		requireProxyStatus(t, response, " 400 ")
		upstream.requireNoUpstreamConnection(t)
	})
}
