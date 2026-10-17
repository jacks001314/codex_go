//go:build !darwin

package network

import (
	"context"
	"net"
)

// proxyLookupIPAddr resolves a proxy target host to its addresses. It is a
// package-level indirection so tests can script the DNS layer and assert that a
// host which is neither allowlisted nor approved never reaches DNS at all
// (mirrors Rust #51650's injectable lookup in
// NetworkProxyState::host_blocked_with_lookup).
var proxyLookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}
