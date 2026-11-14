package tcptunnel

// Single-stream OpenSSH ProxyCommand mode (Rust #60568). Only the verified
// gateway receives credentials; OpenSSH owns SSH authentication, host-key
// verification and every byte that crosses stdio.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/quic-go/quic-go"
)

// PublicGateway is the built-in Portals gateway. Only gateways shipped with the
// CLI may receive the signed-in account's credential.
const PublicGateway = "https://codex-portals.api.openai.com"

// GatewayOrigins lists the gateways allowed to receive OAuth credentials.
var GatewayOrigins = []string{PublicGateway}

// SSHDestination is the validated gateway route and the Portals headers that
// describe one SSH session.
type SSHDestination struct {
	// Target is the gateway CONNECT authority.
	Target *ProxyTarget
	// Headers carry the environment id, published port and auth mode.
	Headers http.Header
}

// SSHDestination validates the `--environment-id` / `--target ENV:PORT` route and
// rejects anything the built-in gateway must not receive.
func (a *Args) SSHDestination() (*SSHDestination, error) {
	if a == nil {
		return nil, errors.New("missing tunnel arguments")
	}
	environmentID := strings.TrimSpace(a.EnvironmentID)
	port := a.Port
	if environmentID == "" {
		target := strings.TrimSpace(a.Target)
		index := strings.LastIndex(target, ":")
		if index < 0 {
			return nil, errors.New("SSH target must be ENV:PORT")
		}
		id, rawPort := target[:index], target[index+1:]
		parsed, err := strconv.ParseUint(rawPort, 10, 16)
		if err != nil || parsed == 0 {
			return nil, errors.New("invalid SSH target port")
		}
		environmentID, port = id, int(parsed)
	}
	if port == 0 {
		port = DefaultSSHPort
	}
	if len(environmentID) < 1 || len(environmentID) > 256 || !validEnvironmentID(environmentID) {
		return nil, errors.New("invalid environment ID")
	}
	proxyURL, err := url.Parse(strings.TrimSpace(a.ProxyURL))
	if err != nil {
		return nil, errors.New("invalid proxy URL")
	}
	host := proxyURL.Hostname()
	if host == "" {
		return nil, errors.New("missing gateway host")
	}
	portInURL := 443
	if raw := proxyURL.Port(); raw != "" {
		parsed, err := parsePort(raw)
		if err != nil {
			return nil, errors.New("invalid proxy URL port")
		}
		portInURL = parsed
	}
	authority := net.JoinHostPort(host, strconv.Itoa(portInURL))
	target, err := ParseProxyTarget(proxyURL, GatewayOrigins, authority)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("x-portals-auth-mode", "oauth")
	headers.Set("x-portals-environment-id", environmentID)
	headers.Set("x-portals-port", strconv.Itoa(port))
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
		return "", errors.New("invalid OAuth access token")
	}
	return bearerHeader(token)
}

// RunSSHProxy relays one SSH session over HTTP/3 CONNECT. The access token is
// polled only after the gateway connection is verified, so a misrouted proxy
// never triggers a credential lookup.
func RunSSHProxy(ctx context.Context, args *Args, accessToken func(context.Context) (string, error), stdin io.Reader, stdout io.Writer) error {
	return runSSHProxy(ctx, args, accessToken, stdin, stdout, newSession)
}

// runSSHProxy is RunSSHProxy with an injectable transport so tests can trust
// a local HTTP/3 proxy.
func runSSHProxy(ctx context.Context, args *Args, accessToken func(context.Context) (string, error), stdin io.Reader, stdout io.Writer, newSessionFn sessionFactory) error {
	destination, err := args.SSHDestination()
	if err != nil {
		return err
	}
	session := newSessionFn(destination.Target)
	// Linux can advertise UDP segmentation on paths where batched packets are
	// lost; single datagrams keep bulk SSH transfers moving there. Go's QUIC
	// transport only batches when the underlying socket exposes the batch
	// interfaces, so hiding them mirrors Rust's segmentation-offload opt-out.
	session.noGSO = true
	defer session.close()

	connectCtx, cancel := context.WithTimeout(ctx, proxyConnectTimeout)
	err = session.connect(connectCtx)
	connectTimedOut := connectCtx.Err() == context.DeadlineExceeded
	cancel()
	if err != nil {
		if connectTimedOut {
			return classify(err, CodeTimeout)
		}
		return err
	}

	token, err := accessToken(ctx)
	if err != nil {
		return err
	}
	bearer, err := oauthBearer(token)
	if err != nil {
		return err
	}
	if err := bridgeStreams(ctx, session, stdin, stdout, destination.Target, destination.Headers, bearer, bridgeProxyCommand); err != nil {
		return sshStreamError(err)
	}
	return nil
}

// sshStreamError reports a rejected CONNECT by HTTP status and hides every
// other peer-supplied detail, which may contain arbitrary text.
func sshStreamError(err error) error {
	var coded *codeError
	if errors.As(err, &coded) && coded.code == CodeRejected {
		return err
	}
	return fmt.Errorf("SSH tunnel stream failed")
}

// packetConnWithoutBatch hides quic-go's batched send path from the UDP socket,
// which disables UDP segmentation offload for SSH tunnels.
type packetConnWithoutBatch struct {
	net.PacketConn
}

// dialProxyWithoutGSO dials the proxy over a socket that never exposes batch
// writes.
func (s *session) dialProxyWithoutGSO(ctx context.Context, tlsConfig *tls.Config, quicConf *quic.Config) (*quic.Conn, error) {
	if tlsConfig == nil {
		tlsConfig = &tls.Config{NextProtos: []string{"h3"}}
	}
	if quicConf == nil {
		quicConf = &quic.Config{}
	}
	peers, err := net.DefaultResolver.LookupHost(ctx, s.target.Host)
	if err != nil {
		return nil, fmt.Errorf("connecting to MASQUE proxy: %w", err)
	}
	lastErr := errors.New("MASQUE proxy resolved to no addresses")
	lastTimeout := false
	for _, peer := range peers {
		config := tlsConfig.Clone()
		if config.ServerName == "" {
			config.ServerName = s.target.Host
		}
		remote, err := net.ResolveUDPAddr("udp", net.JoinHostPort(peer, strconv.Itoa(s.target.Port)))
		if err != nil {
			lastErr = err
			continue
		}
		local := &net.UDPAddr{}
		if ip := net.ParseIP(peer); ip != nil && ip.To4() == nil {
			local = &net.UDPAddr{IP: net.IPv6unspecified}
		}
		udp, err := net.ListenUDP("udp", local)
		if err != nil {
			lastErr = err
			continue
		}
		transport := &quic.Transport{Conn: packetConnWithoutBatch{udp}}
		attemptCtx, cancel := context.WithTimeout(ctx, quicHandshakeTimeout)
		conn, err := transport.Dial(attemptCtx, remote, config, quicConf)
		timedOut := attemptCtx.Err() == context.DeadlineExceeded
		cancel()
		if err != nil {
			_ = transport.Close()
			lastErr = err
			lastTimeout = timedOut
			continue
		}
		s.keepTransport(transport)
		return conn, nil
	}
	if lastTimeout {
		return nil, classify(lastErr, CodeHandshakeTimeout)
	}
	return nil, fmt.Errorf("QUIC handshake failed: %w", lastErr)
}
