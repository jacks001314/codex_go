package network

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/elazarl/goproxy"
)

// scriptedProxyLookupFor51650 swaps the package-level DNS seam and returns a
// counter of how many times the proxy actually resolved a hostname. It mirrors
// the injectable lookup used by Rust #51650's host_policy_tests so that
// "an unapproved hostname never reaches DNS" is a value-level assertion.
func scriptedProxyLookupFor51650(t *testing.T, addrs []net.IPAddr, err error) *int {
	t.Helper()
	lookups := 0
	previous := proxyLookupIPAddr
	proxyLookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		return addrs, err
	}
	t.Cleanup(func() { proxyLookupIPAddr = previous })
	return &lookups
}

func policyServerWithDeciderFor51650(t *testing.T, decider ProxyPolicyDeciderFunc) *ProxyServer {
	t.Helper()
	settings := DefaultProxySettings()
	settings.Enabled = true
	server := &ProxyServer{policyDecider: decider}
	setProxyServerSettingsForTest(server, settings)
	return server
}

// Rust #51650 (1): an unapproved hostname (allowlist miss + decider deny) must
// be rejected without ever being disclosed to DNS.
func TestProxyServerRequiresHostAuthorizationBeforeDNSLikeRust(t *testing.T) {
	lookups := scriptedProxyLookupFor51650(t, []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil)
	server := policyServerWithDeciderFor51650(t, func(context.Context, ProxyPolicyRequest) ProxyDecision {
		return DenyProxyDecision(ProxyReasonNotAllowed)
	})
	decision := server.evaluateProxyPolicy(context.Background(), ProxyPolicyRequest{
		Protocol: ProxyProtocolHTTP, Host: "unapproved.example", Port: 443, Method: http.MethodGet,
	})
	if decision.Allow {
		t.Fatalf("unapproved host allowed: %#v", decision)
	}
	if *lookups != 0 {
		t.Fatalf("unapproved host triggered %d DNS lookup(s), want 0", *lookups)
	}
}

// Rust #51650 (2): policy approval permits DNS, but it cannot bypass the
// local/private address restriction; an approved host that resolves to a
// non-public address must still be rejected after the decider runs.
func TestProxyServerRechecksPrivateAddressAfterApprovalLikeRust(t *testing.T) {
	lookups := scriptedProxyLookupFor51650(t, []net.IPAddr{{IP: net.ParseIP("10.0.0.7")}}, nil)
	server := policyServerWithDeciderFor51650(t, func(context.Context, ProxyPolicyRequest) ProxyDecision {
		return AllowProxyDecision()
	})
	decision := server.evaluateProxyPolicy(context.Background(), ProxyPolicyRequest{
		Protocol: ProxyProtocolHTTP, Host: "approved-rebind.example", Port: 443, Method: http.MethodGet,
	})
	if decision.Allow {
		t.Fatalf("approved host resolving to a private address was allowed: %#v (dns lookups=%d)", decision, *lookups)
	}
	if decision.Reason != ProxyReasonNotAllowedLocal {
		t.Fatalf("rejection reason = %q, want %q", decision.Reason, ProxyReasonNotAllowedLocal)
	}
	if *lookups != 1 {
		t.Fatalf("post-approval DNS lookups = %d, want 1", *lookups)
	}
}

// Rust #51650 (3) / mitm_tests.rs::mitm_policy_rechecks_dns_for_approved_unallowlisted_host:
// an inner HTTPS request after an approved CONNECT re-enters the policy. A host
// with no allowlist entry plus a configured decider must still be rejected
// (403 not_allowed_local) when its address check fails, and the decider must be
// consulted rather than pre-empted by a pre-approval DNS lookup.
func TestProxyServerMITMInnerRequestRechecksDNSForApprovedUnlistedHostLikeRust(t *testing.T) {
	lookups := scriptedProxyLookupFor51650(t, nil, errors.New("no such host"))
	deciderCalls := 0
	server := policyServerWithDeciderFor51650(t, func(context.Context, ProxyPolicyRequest) ProxyDecision {
		deciderCalls++
		return AllowProxyDecision()
	})
	request, err := http.NewRequest(http.MethodGet, "https://unresolvable.example/secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := request.Clone(context.WithValue(request.Context(), proxyMITMContextKey{}, proxyMITMRequestContext{
		host: "unresolvable.example",
		port: 443,
	}))
	_, response := server.handleHTTPRequest(inner, &goproxy.ProxyCtx{})
	if response == nil {
		t.Fatal("inner MITM request was not blocked")
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("inner MITM status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}
	if got := response.Header.Get("X-Proxy-Error"); got != "blocked-by-allowlist" {
		t.Fatalf("inner MITM x-proxy-error = %q, want %q", got, "blocked-by-allowlist")
	}
	if deciderCalls != 1 {
		t.Fatalf("inner MITM decider calls = %d, want 1", deciderCalls)
	}
	if *lookups != 1 {
		t.Fatalf("inner MITM DNS lookups = %d, want 1", *lookups)
	}
}
