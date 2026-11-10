package network

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Rust #60523: restrictive rules must recognize equivalent numeric spellings of
// the same address, so ::ffff:10.1.2.3 blocks a resolved 10.1.2.3.
func TestProxyIPSpellingsMatchEquivalentRulesLikeRust(t *testing.T) {
	spellings := proxyIPSpellings(net.ParseIP("10.1.2.3"))
	matcher, err := CompileProxyDomainMatcher([]string{"::ffff:10.1.2.3"}, true)
	if err != nil {
		t.Fatalf("CompileProxyDomainMatcher() error = %v", err)
	}
	matched := false
	for _, spelling := range spellings {
		if matcher.Match(spelling) {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("no spelling of 10.1.2.3 matched the IPv4-mapped rule: %#v", spellings)
	}
}

// Rust #60523: an allowlisted hostname must not bypass deny or review rules on
// the addresses it resolves to.
func TestResolvedAddressPolicyReasonEnforcesRulesOnResolvedIPLikeRust(t *testing.T) {
	denyMatcher, err := CompileProxyDomainMatcher([]string{"10.1.2.3"}, true)
	if err != nil {
		t.Fatalf("deny matcher error = %v", err)
	}
	reviewMatcher, err := CompileProxyDomainMatcher([]string{"10.9.9.9"}, false)
	if err != nil {
		t.Fatalf("review matcher error = %v", err)
	}
	server := &ProxyServer{}
	server.policy.Store(&proxyRuntimePolicy{
		settings:      ProxySettings{Enabled: true, AllowLocalBinding: true},
		denyMatcher:   denyMatcher,
		reviewMatcher: reviewMatcher,
	})

	if reason := server.resolvedAddressPolicyReason(context.Background(), ProxyProtocolSocks5TCP, "allowed.test", "443", net.ParseIP("10.1.2.3")); reason != ProxyReasonDenied {
		t.Fatalf("denied address reason = %q, want %q", reason, ProxyReasonDenied)
	}
	if reason := server.resolvedAddressPolicyReason(context.Background(), ProxyProtocolSocks5TCP, "allowed.test", "443", net.ParseIP("10.9.9.9")); reason != ProxyReasonPolicyDenied {
		t.Fatalf("reviewed address reason = %q, want %q", reason, ProxyReasonPolicyDenied)
	}
	if reason := server.resolvedAddressPolicyReason(context.Background(), ProxyProtocolSocks5TCP, "allowed.test", "443", net.ParseIP("10.4.4.4")); reason != "" {
		t.Fatalf("unblocked address reason = %q, want none", reason)
	}
}

// The SOCKS dial path resolves the host and refuses to connect when the
// resolved address is denied, leaving the upstream untouched.
func TestSOCKS5DialRefusesDeniedResolvedAddressLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer listener.Close()
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = conn.Close()
		}
	}()
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}

	denyMatcher, err := CompileProxyDomainMatcher([]string{"127.0.0.1"}, true)
	if err != nil {
		t.Fatalf("deny matcher error = %v", err)
	}
	server := &ProxyServer{}
	server.policy.Store(&proxyRuntimePolicy{
		settings:    ProxySettings{Enabled: true, AllowLocalBinding: true},
		denyMatcher: denyMatcher,
	})

	previous := proxyLookupIPAddr
	proxyLookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	defer func() { proxyLookupIPAddr = previous }()

	conn, err := server.dialCheckedTargetForProtocol(context.Background(), "tcp", net.JoinHostPort("allowed.test", portText), ProxyProtocolSocks5TCP)
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("dial succeeded for an address denied by a resolved-IP rule")
	}
	if !strings.Contains(err.Error(), ProxyReasonDenied) {
		t.Fatalf("dial error = %v, want a policy denial", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := accepted.Load(); got != 0 {
		t.Fatalf("upstream accepted %d connections, want none", got)
	}
}
