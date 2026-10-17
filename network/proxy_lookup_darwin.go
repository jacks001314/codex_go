//go:build darwin

package network

import (
	"context"
	"net"
)

// proxyLookupIPAddr resolves a proxy target host with the macOS system
// resolver (Rust #45982 `tokio::net::lookup_host`). Supplemental resolvers and
// VPN split DNS are only visible through the system resolver; the pure Go
// resolver reads /etc/resolv.conf and misses them. `PreferGo: false` selects
// the cgo system resolver when the binary was built with cgo, and degrades to
// the standard resolver otherwise.
//
// It stays a package-level indirection so tests can script the DNS layer
// (mirrors Rust #51650's injectable lookup).
var proxyLookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	addresses, err := (&net.Resolver{PreferGo: false}).LookupIPAddr(ctx, host)
	if err == nil {
		return addresses, nil
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}
