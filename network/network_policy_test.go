package network

import (
	"context"
	"net/url"
	"testing"
)

// These tests mirror codex-rs/http-client/src/network_policy_tests.rs.

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return parsed
}

func TestRestrictedDestinationPolicyMatchesExactSecureHosts(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	if !controller.Publish(policy.Revision(), RestrictedDestinationPolicy([]string{"example.com"})) {
		t.Fatal("publish rejected the current revision")
	}
	for _, tc := range []struct {
		url   string
		allow bool
	}{
		{"https://EXAMPLE.com/path", true},
		{"https://example.com.:8443/path", true},
		{"wss://example.com/path", true},
		{"http://example.com/path", false},
		{"ws://example.com/path", false},
		{"https://sub.example.com/path", false},
		{"https://example.com.evil.test/path", false},
		{"https://example.com@evil.test/path", false},
		{"https://127.0.0.1/path", false},
	} {
		_, err := policy.Acquire(mustParseURL(t, tc.url))
		if got := err == nil; got != tc.allow {
			t.Fatalf("acquire(%q) allowed = %v, want %v (err=%v)", tc.url, got, tc.allow, err)
		}
	}
}

func TestNetworkPolicyInvalidationRevokesPermitsAndRejectsStalePublication(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	target := mustParseURL(t, "https://example.com")
	if _, err := policy.Acquire(target); err != ErrNetworkPolicyUnavailable {
		t.Fatalf("acquire before publish error = %v", err)
	}
	previous := policy.Revision()
	if !controller.Publish(previous, UnrestrictedDestinationPolicy()) {
		t.Fatal("publish rejected the current revision")
	}
	permit, err := policy.Acquire(target)
	if err != nil {
		t.Fatalf("acquire error = %v", err)
	}
	policy.Invalidate()
	if controller.Publish(previous, UnrestrictedDestinationPolicy()) {
		t.Fatal("stale publication was accepted")
	}
	if !controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy()) {
		t.Fatal("publish after invalidate was rejected")
	}
	blocked := make(chan struct{})
	if _, err := RunWithNetworkPermit(context.Background(), permit, func(context.Context) string {
		<-blocked
		return "should not run"
	}); err != ErrNetworkPolicyRevoked {
		t.Fatalf("run error = %v, want revoked", err)
	}
	if _, err := policy.Acquire(target); err != nil {
		t.Fatalf("acquire after invalidate error = %v", err)
	}
}

func TestNetworkPolicyLoadFailureRecoversCurrentAccountButNotPrevious(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	account := policy.ForCurrentAccount()
	target := mustParseURL(t, "https://example.com")
	changes := policy.Changes()
	if !controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy()) {
		t.Fatal("publish rejected the current revision")
	}
	<-changes
	unchanged := policy.Changes()
	if !controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy()) {
		t.Fatal("republish rejected the current revision")
	}
	select {
	case <-unchanged:
		t.Fatal("republishing the same policy notified a change")
	default:
	}
	oldRequest, err := account.Acquire(target)
	if err != nil {
		t.Fatalf("account acquire error = %v", err)
	}
	controller.Unavailable(policy.Revision())
	if _, err := account.Acquire(target); err != ErrNetworkPolicyUnavailable {
		t.Fatalf("acquire after unavailable error = %v", err)
	}
	if !controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy()) {
		t.Fatal("publish after unavailable was rejected")
	}
	if _, err := account.Acquire(target); err != nil {
		t.Fatalf("account acquire after recovery error = %v", err)
	}
	if err := oldRequest.Check(); err != ErrNetworkPolicyRevoked {
		t.Fatalf("old request check = %v, want revoked", err)
	}
	policy.Invalidate()
	if !controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy()) {
		t.Fatal("publish after invalidate was rejected")
	}
	if _, err := account.Acquire(target); err != ErrNetworkPolicyRevoked {
		t.Fatalf("previous account acquired after invalidate: %v", err)
	}
	if _, err := policy.ForCurrentAccount().Acquire(target); err != nil {
		t.Fatalf("current account acquire error = %v", err)
	}
}

func TestNetworkPolicyUnsupportedSDKWorkIsDeniedAndCancelled(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	if !controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy()) {
		t.Fatal("publish rejected the current revision")
	}
	permit, err := policy.AcquireForUnsupportedSDK()
	if err != nil {
		t.Fatalf("acquire for unsupported SDK error = %v", err)
	}
	if !controller.Publish(policy.Revision(), RestrictedDestinationPolicy([]string{"example.com"})) {
		t.Fatal("publish rejected the current revision")
	}
	if _, err := policy.AcquireForUnsupportedSDK(); err != ErrNetworkPolicyUnsupportedTransport {
		t.Fatalf("restricted acquire for unsupported SDK error = %v", err)
	}
	blocked := make(chan struct{})
	if _, err := RunWithNetworkPermit(context.Background(), permit, func(context.Context) string {
		<-blocked
		return "should not run"
	}); err != ErrNetworkPolicyRevoked {
		t.Fatalf("run error = %v, want revoked", err)
	}
}
