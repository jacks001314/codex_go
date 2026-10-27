package network

import (
	"context"
	"net"
	"testing"
)

func reviewDomainPolicy(t *testing.T, settings ProxySettings) *proxyRuntimePolicy {
	t.Helper()
	policy, err := buildProxyRuntimePolicy(settings, nil)
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	return policy
}

func reviewDomainServer(t *testing.T, settings ProxySettings, decider ProxyPolicyDecider) *ProxyServer {
	t.Helper()
	server := &ProxyServer{policyDecider: decider}
	server.policy.Store(reviewDomainPolicy(t, settings))
	return server
}

func reviewDomainRequest() ProxyPolicyRequest {
	return ProxyPolicyRequest{
		Protocol: ProxyProtocolHTTPSConnect,
		Host:     "api.example.com",
		Port:     443,
		Method:   "GET",
	}
}

// TestReviewDomainsRouteThroughApprovalLikeRust mirrors #56154: managed review
// domains require approval even when an exact or wildcard allow rule matches.
func TestReviewDomainsRouteThroughApprovalLikeRust(t *testing.T) {
	for _, allow := range []string{"api.example.com", "*.example.com"} {
		settings := DefaultProxySettings()
		settings.Enabled = true
		settings.AllowLocalBinding = true
		settings.SetAllowedDomains([]string{allow})
		calls := 0
		server := reviewDomainServer(t, settings, ProxyPolicyDeciderFunc(func(context.Context, ProxyPolicyRequest) ProxyDecision {
			calls++
			return AllowProxyDecision()
		}))
		decision := server.evaluateProxyPolicy(context.Background(), reviewDomainRequest())
		if !decision.Allow || decision.Source != ProxyDecisionSourceBaselinePolicy {
			t.Fatalf("allow %q without review domain = %#v", allow, decision)
		}
		if calls != 0 {
			t.Fatalf("unexpected approval for allow %q", allow)
		}

		settings.ReviewDomains = []string{"api.example.com"}
		server = reviewDomainServer(t, settings, ProxyPolicyDeciderFunc(func(context.Context, ProxyPolicyRequest) ProxyDecision {
			calls++
			return AllowProxyDecision()
		}))
		decision = server.evaluateProxyPolicy(context.Background(), reviewDomainRequest())
		if !decision.Allow || calls != 1 {
			t.Fatalf("allow %q with review domain = %#v calls=%d", allow, decision, calls)
		}
	}
}

func TestReviewDomainsKeepExplicitDenialsLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.SetAllowedDomains([]string{"api.example.com"})
	settings.SetDeniedDomains([]string{"api.example.com"})
	settings.ReviewDomains = []string{"api.example.com"}
	server := reviewDomainServer(t, settings, ProxyPolicyDeciderFunc(func(context.Context, ProxyPolicyRequest) ProxyDecision {
		t.Fatalf("explicit denial must not consult the approval flow")
		return AllowProxyDecision()
	}))
	decision := server.evaluateProxyPolicy(context.Background(), reviewDomainRequest())
	if decision.Allow || decision.Reason != ProxyReasonDenied {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestReviewDomainsRequireApprovalWithoutDeciderLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.AllowLocalBinding = true
	settings.SetAllowedDomains([]string{"api.example.com"})
	settings.ReviewDomains = []string{"*.example.com"}
	server := reviewDomainServer(t, settings, nil)
	decision := server.evaluateProxyPolicy(context.Background(), reviewDomainRequest())
	if decision.Allow || decision.Reason != ProxyReasonNotAllowed {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestProxyRequirementsApplyReviewDomainsLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.SetAllowedDomains([]string{"api.example.com"})
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, &ProxyRequirements{
		ReviewDomains: []string{"api.example.com", "*.review.example.com"},
	}, false)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	got := spec.Config().Network.ReviewDomains
	if len(got) != 2 || got[0] != "api.example.com" || got[1] != "*.review.example.com" {
		t.Fatalf("review domains = %#v", got)
	}
	if spec.constraints.ReviewDomains == nil || len(*spec.constraints.ReviewDomains) != 2 {
		t.Fatalf("constraints did not pin review domains")
	}
}

// TestApprovedTargetsRecheckExplicitDenialsLikeRust mirrors #56154's
// connect-time re-check: a denial recorded after policy evaluation still blocks
// an already-authorized target.
func TestApprovedTargetsRecheckExplicitDenialsLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	settings := DefaultProxySettings()
	settings.Enabled = true
	settings.AllowLocalBinding = true
	settings.SetAllowedDomains([]string{"127.0.0.1"})
	server := reviewDomainServer(t, settings, nil)
	conn, err := server.dialCheckedTarget(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("allowlisted local target should dial: %v", err)
	}
	conn.Close()

	settings.SetDeniedDomains([]string{"127.0.0.1"})
	server = reviewDomainServer(t, settings, nil)
	if _, err := server.dialCheckedTarget(context.Background(), "tcp", listener.Addr().String()); err == nil {
		t.Fatal("denied target must be rejected at connect time")
	}
}
