package codemode

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"codex_go/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// GrpcCodeModeSessionProvider mirrors Rust GrpcCodeModeSessionProvider
// (1e557a554e #38041): it opens code-mode sessions over a gRPC transport and
// reuses the shared JSON framing handshake, session state, and delegate
// routing from the remote provider. Endpoints are http(s) origins validated
// like Rust 85f331772f (#38087), and the shared HTTP client's transport
// supplies proxy and custom-CA dialing.
type GrpcCodeModeSessionProvider struct {
	endpoint   string
	httpClient *http.Client

	mu          sync.Mutex
	connection  *remoteConnection
	nextSession atomic.Uint64
}

func NewGrpcCodeModeSessionProvider(endpoint string, httpClient *http.Client) *GrpcCodeModeSessionProvider {
	return &GrpcCodeModeSessionProvider{endpoint: strings.TrimSpace(endpoint), httpClient: httpClient}
}

// UsesGrpcCodeModeEndpoint reports whether the code-mode host URL selects the
// gRPC transport (http/https origin) rather than the WebSocket transport.
func UsesGrpcCodeModeEndpoint(endpoint string) bool {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "unix")
}

func (p *GrpcCodeModeSessionProvider) Availability() error {
	if p == nil {
		return errors.New("code-mode gRPC provider is nil")
	}
	return validateGrpcCodeModeEndpoint(p.endpoint)
}

func (p *GrpcCodeModeSessionProvider) TakeUnavailableWarning(string) string { return "" }

func (p *GrpcCodeModeSessionProvider) NewSession(delegate tool.CodeModeRemoteDelegate) tool.CodeModeRemoteSession {
	value := p.nextSession.Add(1)
	return &remoteSession{provider: p, delegate: delegate, id: SessionID(fmt.Sprintf("session-%d", value))}
}

func (p *GrpcCodeModeSessionProvider) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	connection := p.connection
	p.connection = nil
	p.mu.Unlock()
	if connection != nil {
		return connection.Close()
	}
	return nil
}

func (p *GrpcCodeModeSessionProvider) connect(ctx context.Context) (*remoteConnection, error) {
	if p == nil {
		return nil, errors.New("code-mode gRPC provider is nil")
	}
	if err := validateGrpcCodeModeEndpoint(p.endpoint); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.connection != nil && p.connection.Alive() {
		return p.connection, nil
	}
	// Rust #38087 preserves gRPC frame-size limits: the code-mode protocol
	// permits frames up to ProtocolMaxFrameBytes, so raise gRPC's 4 MiB
	// defaults to match.
	options := append([]grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(ProtocolMaxFrameBytes),
			grpc.MaxCallSendMsgSize(ProtocolMaxFrameBytes),
		),
	}, grpcDialOptionsForEndpoint(p.endpoint, p.httpClient)...)
	transport, err := DialGrpcTransport(ctx, grpcTargetForEndpoint(p.endpoint), options...)
	if err != nil {
		return nil, err
	}
	connection, err := connectRemoteTransport(ctx, transport)
	if err != nil {
		_ = transport.Close()
		return nil, err
	}
	p.connection = connection
	return connection, nil
}

// validateGrpcCodeModeEndpoint mirrors Rust #38087: http/https origins only,
// with no path (beyond "/"), query, or fragment.
func validateGrpcCodeModeEndpoint(endpoint string) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return fmt.Errorf("invalid gRPC code-mode host URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "unix" {
		return errors.New("gRPC code-mode host URL must use http, https, or unix")
	}
	if parsed.Scheme == "unix" {
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("gRPC code-mode Unix socket URL must not include a query or fragment")
		}
		return nil
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("gRPC code-mode host URL must not include a path, query, or fragment")
	}
	return nil
}

// grpcTargetForEndpoint maps an http(s) origin to a gRPC dial target, adding
// the scheme-default port when the endpoint omits one.
func grpcTargetForEndpoint(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Hostname() == "" {
		return strings.TrimSpace(endpoint)
	}
	host := parsed.Hostname()
	if port := parsed.Port(); port != "" {
		return net.JoinHostPort(host, port)
	}
	if parsed.Scheme == "https" {
		return net.JoinHostPort(host, "443")
	}
	return net.JoinHostPort(host, "80")
}

// grpcDialOptionsForEndpoint builds gRPC dial options honoring the shared
// HTTP client's proxy and custom CA configuration (Rust #38087
// HttpClientFactory): the http.Transport's DialContext (which applies the
// system proxy) becomes the gRPC context dialer, and its TLS client config
// (custom CA roots) becomes the transport credentials for https endpoints.
func grpcDialOptionsForEndpoint(endpoint string, httpClient *http.Client) []grpc.DialOption {
	parsed, _ := url.Parse(strings.TrimSpace(endpoint))
	secure := parsed != nil && parsed.Scheme == "https"
	options := []grpc.DialOption{}
	tlsConfigured := false
	if httpClient != nil {
		if transport, ok := httpClient.Transport.(*http.Transport); ok && transport.DialContext != nil {
			dial := transport.DialContext
			options = append(options, grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
				return dial(ctx, "tcp", address)
			}))
			if secure && transport.TLSClientConfig != nil {
				config := transport.TLSClientConfig.Clone()
				if config.MinVersion == 0 {
					config.MinVersion = tls.VersionTLS12
				}
				options = append(options, grpc.WithTransportCredentials(credentials.NewTLS(config)))
				tlsConfigured = true
			}
		}
	}
	if secure && !tlsConfigured {
		// gRPC requires explicit transport credentials; system roots with h2
		// ALPN mirror the Rust default TLS path.
		options = append(options, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
	}
	if !secure {
		options = append(options, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	return options
}

var _ tool.CodeModeRemoteProvider = (*GrpcCodeModeSessionProvider)(nil)
