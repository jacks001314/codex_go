// Single-stream OpenSSH ProxyCommand. Only the verified gateway receives
// credentials. OpenSSH owns SSH authentication, host-key verification, and all
// stdio bytes.

package tcptunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// SSHDestination is the validated gateway route and the Portals headers that
// describe one SSH session.
type SSHDestination struct {
	// Target is the gateway CONNECT authority.
	Target *ProxyTarget
	// Headers carry the environment id, published port and auth mode.
	Headers http.Header
}

// sshDestination validates the OAuth or legacy route before any network I/O.
// The OAuth route names a canonical environment; the legacy route is an
// ERS-issued IPv4 CONNECT authority that requires a private token file.
func (a *Args) sshDestination() (*SSHDestination, error) {
	if a == nil {
		return nil, errors.New("missing tunnel arguments")
	}
	proxyURL, err := url.Parse(strings.TrimSpace(a.ProxyURL))
	if err != nil {
		return nil, errors.New("invalid proxy URL")
	}
	if a.Target != "" {
		address, err := netip.ParseAddrPort(a.Target)
		if err != nil || !address.Addr().Is4() || address.Port() == 0 || !a.tokenFileSet {
			return nil, errors.New("invalid legacy CONNECT target")
		}
		target, err := ParseProxyTarget(proxyURL, GatewayOrigins, address.String())
		if err != nil {
			return nil, err
		}
		return &SSHDestination{Target: target, Headers: http.Header{}}, nil
	}
	environmentID := a.EnvironmentID
	if !a.environmentIDSet {
		return nil, errors.New("missing environment ID")
	}
	if len(environmentID) < 1 || len(environmentID) > 256 || !validEnvironmentID(environmentID) {
		return nil, errors.New("invalid environment ID")
	}
	host := proxyURL.Hostname()
	if host == "" {
		return nil, errors.New("missing gateway host")
	}
	port := 443
	if raw := proxyURL.Port(); raw != "" {
		parsed, err := parsePort(raw)
		if err != nil {
			return nil, errors.New("invalid proxy URL port")
		}
		port = parsed
	}
	target, err := ParseProxyTarget(proxyURL, GatewayOrigins, net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	publishedPort := a.Port
	if publishedPort == 0 {
		publishedPort = DefaultSSHPort
	}
	headers := http.Header{}
	headers.Set("x-portals-auth-mode", "oauth")
	headers.Set("x-portals-environment-id", environmentID)
	headers.Set("x-portals-port", strconv.Itoa(publishedPort))
	return &SSHDestination{Target: target, Headers: headers}, nil
}

// validEnvironmentID accepts the canonical Portals environment shape.
func validEnvironmentID(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

// oauthBearer validates the ChatGPT access token before it becomes a header.
// The error never echoes the credential.
func oauthBearer(token string) (string, error) {
	unpadded := strings.TrimRight(token, "=")
	valid := unpadded != "" && len(token) < MaxTokenBytes
	for i := 0; valid && i < len(unpadded); i++ {
		c := unpadded[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '.' || c == '_' || c == '~' || c == '+' || c == '/':
		default:
			valid = false
		}
	}
	if !valid {
		return "", errors.New("invalid access token")
	}
	bearer := "Bearer " + token
	if !validHeaderValue(bearer) {
		return "", errors.New("invalid access token")
	}
	return bearer, nil
}

func validHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// RunSSHProxy relays one SSH session through one authenticated CONNECT. The
// access token is polled only after a gateway connection has passed TLS
// verification, so a misrouted proxy never triggers a credential lookup.
func RunSSHProxy(ctx context.Context, args *Args, accessToken func(context.Context) (string, error), stdin io.Reader, stdout io.Writer) error {
	destination, err := args.sshDestination()
	if err != nil {
		return err
	}
	config, err := tlsConfig()
	if err != nil {
		return err
	}
	tunnel, err := newTransportConfig(config).open(ctx, destination.Target, func(ctx context.Context) (requestSpec, error) {
		token, err := accessToken(ctx)
		if err != nil {
			return requestSpec{}, err
		}
		bearer, err := oauthBearer(token)
		if err != nil {
			return requestSpec{}, err
		}
		return requestSpec{headers: destination.Headers, bearer: bearer}, nil
	})
	if err != nil {
		return safeTunnelError(err)
	}
	return safeTunnelError(tunnel.relay(stdin, stdout))
}

// RunSSHProxyFromFile reads a one-use bearer from a private file, mirroring
// Rust's `run_ssh_proxy_from_file`. The file is opened, checked, removed and
// read only after the gateway has passed TLS verification.
func RunSSHProxyFromFile(ctx context.Context, args *Args, stdin io.Reader, stdout io.Writer) error {
	if args == nil {
		return errors.New("missing tunnel arguments")
	}
	path := args.TokenFile
	if !args.tokenFileSet {
		return errors.New("missing token file")
	}
	return RunSSHProxy(ctx, args, func(context.Context) (string, error) {
		return readTokenFile(path)
	}, stdin, stdout)
}

// readTokenFile mirrors Rust's private token-file contract: a regular file
// smaller than the token bound, private on Unix, removed before the token is
// read so a crashed helper cannot replay it.
func readTokenFile(path string) (string, error) {
	file, err := openTokenFile(path)
	if err != nil {
		return "", fmt.Errorf("opening private token file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() >= MaxTokenBytes {
		return "", errors.New("invalid private token file")
	}
	if problem := tokenFilePrivacyProblem(info.Mode()); problem != "" {
		return "", errors.New(problem)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("removing one-use token file: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxTokenBytes))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// tlsConfig mirrors Rust's native-root client configuration: system trust
// anchors, TLS 1.2 or newer, and no client certificate.
func tlsConfig() (*tls.Config, error) {
	return &tls.Config{MinVersion: tls.VersionTLS12}, nil
}

// safeTunnelError keeps only the HTTP status of a rejected CONNECT and hides
// every other peer-supplied detail.
func safeTunnelError(err error) error {
	if err == nil {
		return nil
	}
	var setup *setupError
	if errors.As(err, &setup) {
		if setup.status != nil {
			return rejection(*setup.status)
		}
		return tunnelTimeout()
	}
	return streamError()
}
