# #45463 Dedicated listeners for managed network proxies

## Upstream change

Rust exposes `ManagedProxyRouting`: the default `SharedIngress` mode uses
Windows SID-attributed shared ingress when available, while
`DedicatedListeners` reserves per-proxy loopback ports for sandboxes that
enforce endpoint access directly.

## Go status

Not applicable by architecture.

Go's managed proxy always reserves its own loopback HTTP and SOCKS listeners
in `network/proxy_server.go`; it has no shared-ingress or SID-attributed mode
to select between. Dedicated per-proxy endpoints and their sandbox metadata
are therefore already the only behavior.

## Verification

- `go test ./network -run 'TestProxyManagedNetwork|TestStandaloneProxyWait' -count=1`
