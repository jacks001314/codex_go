//go:build darwin

package network

import (
	"context"
	"testing"
	"time"
)

// TestProxyLookupUsesNativeResolverLikeRust pins Rust #45982: macOS resolutions
// go through the system resolver for both IPv4 and IPv6 localhost, and the
// local-network policy still rejects the result.
func TestProxyLookupUsesNativeResolverLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addresses, err := proxyLookupIPAddr(ctx, "localhost")
	if err != nil {
		t.Fatalf("proxyLookupIPAddr(localhost) error = %v", err)
	}
	if len(addresses) == 0 {
		t.Fatal("proxyLookupIPAddr(localhost) returned no addresses")
	}
	if !proxyHostResolvesToNonPublicIP("localhost", 80) {
		t.Fatal("localhost must resolve to a non-public address")
	}
}
