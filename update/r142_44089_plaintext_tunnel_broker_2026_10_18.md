# #44089 Support credential brokering in plaintext HTTP tunnels

## Upstream change

Rust brokers credentials for configured plaintext HTTP destinations inside
CONNECT/SOCKS tunnels, intercepting only same-destination HTTP requests and
substituting the real credential for the dummy.

## Go carriers

- `HostRequiresHTTPInterception` distinguishes plaintext HTTP interception from
  TLS MITM.
- `plaintextTunnelRequestAllowed`/`classifyPlaintextTunnelRequest` accept only a
  same-host/port HTTP/1.1 request and classify h2c/upgrade separately.
- The tunnel forwarder removes hop-by-hop request headers and injects the real
  broker credential.
- End-to-end tests cover HTTP inside CONNECT and SOCKS5 tunnels and assert the
  upstream observes `Bearer <real token>`.

## Verification

- `go test ./network -run 'PlaintextTunnel|PlaintextHTTP|CredentialDestinationHost' -count=1`
