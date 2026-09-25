package network

import (
	"context"
	"errors"
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

// Rust parity: restricting a policy to exact endpoint URLs enforces the scope
// even without a policy owner, intersects with an existing scope, and cannot
// grant access a destination policy denies.
func TestNetworkPolicyRestrictToEndpointsLikeRust(t *testing.T) {
	allowed := mustParseURL(t, "https://cloud.example/backend-api/wham/config/bundle")
	other := mustParseURL(t, "https://cloud.example/backend-api/other")

	policy := UnmanagedNetworkPolicy().RestrictToEndpoints([]*url.URL{allowed})
	if !policy.IsScoped() {
		t.Fatal("an endpoint-scoped policy reports no scope")
	}
	if _, err := policy.Acquire(allowed); err != nil {
		t.Fatalf("Acquire(allowed) error = %v", err)
	}
	if _, err := policy.Acquire(other); !errors.Is(err, ErrNetworkPolicyDestination) {
		t.Fatalf("Acquire(other) error = %v, want a destination denial", err)
	}
	if _, err := policy.AcquireForUnsupportedSDK(); !errors.Is(err, ErrNetworkPolicyUnsupportedTransport) {
		t.Fatalf("AcquireForUnsupportedSDK() error = %v", err)
	}

	// Restricting again intersects with the current scope.
	intersected := policy.RestrictToEndpoints([]*url.URL{other})
	if _, err := intersected.Acquire(allowed); !errors.Is(err, ErrNetworkPolicyDestination) {
		t.Fatalf("intersection kept a dropped endpoint: %v", err)
	}
	// An empty set denies every destination.
	if _, err := UnmanagedNetworkPolicy().RestrictToEndpoints(nil).Acquire(allowed); !errors.Is(err, ErrNetworkPolicyDestination) {
		t.Fatalf("empty scope error = %v", err)
	}

	// A managed policy's own destination rules still bind under a scope.
	controller := NewNetworkPolicyController()
	managed := controller.Policy()
	controller.Publish(managed.Revision(), RestrictedDestinationPolicy([]string{"granted.example"}))
	scoped := managed.RestrictToEndpoints([]*url.URL{other})
	if _, err := scoped.Acquire(other); !errors.Is(err, ErrNetworkPolicyDestination) {
		t.Fatalf("scoped managed policy granted a denied host: %v", err)
	}
	granted := mustParseURL(t, "https://granted.example/path")
	if _, err := scoped.Acquire(granted); !errors.Is(err, ErrNetworkPolicyDestination) {
		t.Fatalf("scoped managed policy escaped its endpoint scope: %v", err)
	}
}
