package telemetry

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"codex_go/network"
)

// Rust parity (#47408): the OTLP HTTP export client carries the application
// network policy, so a managed restriction denies an export destination before
// connecting and revocation cancels further exports.
func TestBuildOTLPHTTPClientEnforcesApplicationPolicyLikeRust(t *testing.T) {
	defer setOTLPExportPolicy(network.UnmanagedNetworkPolicy())

	allowed, err := url.Parse("https://allowed.example/v1/metrics")
	if err != nil {
		t.Fatal(err)
	}
	denied, err := url.Parse("https://denied.example/v1/metrics")
	if err != nil {
		t.Fatal(err)
	}

	setOTLPExportPolicy(network.UnmanagedNetworkPolicy().RestrictToEndpoints([]*url.URL{allowed}))
	scoped, _, err := buildOTLPHTTPClient(nil, time.Second)
	if err != nil {
		t.Fatalf("buildOTLPHTTPClient() error = %v", err)
	}
	if _, err := scoped.Do(&http.Request{URL: denied}); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("denied OTLP export error = %v, want a destination denial", err)
	}

	// Revocation cancels managed exports.
	controller := network.NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), network.UnrestrictedDestinationPolicy())
	setOTLPExportPolicy(policy)
	managed, _, err := buildOTLPHTTPClient(nil, time.Second)
	if err != nil {
		t.Fatalf("buildOTLPHTTPClient(managed) error = %v", err)
	}
	controller.Policy().Invalidate()
	if _, err := managed.Do(&http.Request{URL: allowed}); !errors.Is(err, network.ErrNetworkPolicyUnavailable) {
		t.Fatalf("revoked OTLP export error = %v, want the policy denial", err)
	}

	// An unscoped policy leaves the export client untouched.
	setOTLPExportPolicy(network.UnmanagedNetworkPolicy())
	plain, _, err := buildOTLPHTTPClient(nil, time.Second)
	if err != nil {
		t.Fatalf("buildOTLPHTTPClient(plain) error = %v", err)
	}
	client, ok := plain.(*http.Client)
	if !ok {
		t.Fatalf("plain export client = %#v", plain)
	}
	if _, wrapped := client.Transport.(*network.PolicyRoundTripper); wrapped {
		t.Fatal("an unscoped policy wrapped the OTLP export client")
	}
}
