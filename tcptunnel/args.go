// Package tcptunnel implements the hidden `codex tcp-tunnel` command: loopback
// TCP forwarding through an HTTP/3 CONNECT proxy, mirroring Rust's
// `codex-tcp-tunnel` crate. Bearer credentials and optional extension headers
// enter only through stdin; the listener survives transport loss, but
// individual TCP streams are never replayed.
package tcptunnel

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// DefaultListenAddr matches Rust's `listen_addr` default.
const DefaultListenAddr = "127.0.0.1:0"

// Args mirrors Rust's `codex_tcp_tunnel::Args`.
type Args struct {
	// ProxyURL is the HTTPS origin of the HTTP/3 proxy.
	ProxyURL string
	// ProxyOriginsFile lists one exact approved HTTPS proxy origin per line.
	ProxyOriginsFile string
	// Target is the CONNECT target authority, including a nonzero port.
	Target string
	// ListenAddr is the loopback listener address.
	ListenAddr string
	// AuthTokenStdin reads the initial bearer from stdin.
	AuthTokenStdin bool
	// AuthTokenUpdatesStdin reads replacement bearers from stdin and stops when
	// the controlling pipe closes. Requires AuthTokenStdin.
	AuthTokenUpdatesStdin bool
	// ConnectHeadersStdin reads a JSON list of extension-header name/value
	// pairs before the first bearer. Requires AuthTokenStdin.
	ConnectHeadersStdin bool
}

// ParseArgs parses the `codex tcp-tunnel` argument list with clap's shape: long
// options with a separated or `=`-attached value, and the same requirement
// rules (`--auth-token-updates-stdin` and `--connect-headers-stdin` require
// `--auth-token-stdin`).
func ParseArgs(argv []string) (*Args, error) {
	args := &Args{ListenAddr: DefaultListenAddr}
	seen := map[string]bool{}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		name, value, hasValue := strings.Cut(arg, "=")
		takeValue := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(argv) {
				return "", fmt.Errorf("a value is required for '%s' but none was supplied", name)
			}
			i++
			return argv[i], nil
		}
		switch name {
		case "--proxy-url":
			v, err := takeValue()
			if err != nil {
				return nil, err
			}
			args.ProxyURL = v
			seen["--proxy-url"] = true
		case "--proxy-origins-file":
			v, err := takeValue()
			if err != nil {
				return nil, err
			}
			args.ProxyOriginsFile = v
			seen["--proxy-origins-file"] = true
		case "--target":
			v, err := takeValue()
			if err != nil {
				return nil, err
			}
			args.Target = v
			seen["--target"] = true
		case "--listen-addr":
			v, err := takeValue()
			if err != nil {
				return nil, err
			}
			args.ListenAddr = v
			seen["--listen-addr"] = true
		case "--auth-token-stdin":
			if hasValue {
				return nil, fmt.Errorf("unexpected value for '--auth-token-stdin'")
			}
			args.AuthTokenStdin = true
		case "--auth-token-updates-stdin":
			if hasValue {
				return nil, fmt.Errorf("unexpected value for '--auth-token-updates-stdin'")
			}
			args.AuthTokenUpdatesStdin = true
		case "--connect-headers-stdin":
			if hasValue {
				return nil, fmt.Errorf("unexpected value for '--connect-headers-stdin'")
			}
			args.ConnectHeadersStdin = true
		default:
			if strings.HasPrefix(name, "-") {
				return nil, fmt.Errorf("unexpected argument '%s' found", arg)
			}
			return nil, fmt.Errorf("unexpected argument '%s' found", arg)
		}
	}
	if !seen["--proxy-url"] {
		return nil, fmt.Errorf("the following required arguments were not provided:\n  --proxy-url <PROXY_URL>")
	}
	if !seen["--proxy-origins-file"] {
		return nil, fmt.Errorf("the following required arguments were not provided:\n  --proxy-origins-file <PROXY_ORIGINS_FILE>")
	}
	if !seen["--target"] {
		return nil, fmt.Errorf("the following required arguments were not provided:\n  --target <TARGET>")
	}
	if args.AuthTokenUpdatesStdin && !args.AuthTokenStdin {
		return nil, fmt.Errorf("the following required arguments were not provided:\n  --auth-token-stdin")
	}
	if args.ConnectHeadersStdin && !args.AuthTokenStdin {
		return nil, fmt.Errorf("the following required arguments were not provided:\n  --auth-token-stdin")
	}
	if _, _, err := net.SplitHostPort(args.ListenAddr); err != nil {
		return nil, fmt.Errorf("invalid value '%s' for '--listen-addr <LISTEN_ADDR>': %v", args.ListenAddr, err)
	}
	return args, nil
}

// ProxyTarget is the validated CONNECT target and proxy address pair.
type ProxyTarget struct {
	// Host is the proxy host (the TLS server name).
	Host string
	// Port is the proxy port.
	Port int
	// Authority is the CONNECT target authority.
	Authority string
}

// Addr is the proxy's host:port dial address.
func (t ProxyTarget) Addr() string {
	return net.JoinHostPort(t.Host, fmt.Sprintf("%d", t.Port))
}

// ParseTrustedOrigins mirrors Rust's `parse_trusted_origins`: one exact HTTPS
// origin per non-empty line, no path, credentials, query, or fragment.
func ParseTrustedOrigins(origins string) ([]string, error) {
	trusted := []string{}
	for _, line := range strings.Split(origins, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parsed, err := url.Parse(line)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy origin")
		}
		path := parsed.Path
		if path == "" {
			// Go reports an empty path for an origin; Rust's `Url::parse`
			// normalizes it to "/".
			path = "/"
		}
		if parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" ||
			parsed.Fragment != "" || path != "/" || line != originOf(parsed) {
			return nil, fmt.Errorf("trusted proxy origins must be exact HTTPS origins")
		}
		trusted = append(trusted, line)
	}
	if len(trusted) == 0 {
		return nil, fmt.Errorf("no trusted proxy origins")
	}
	return trusted, nil
}

// ParseProxyTarget mirrors Rust's `ProxyTarget::parse`: the proxy URL must be
// an exact trusted HTTPS origin and the target must be a credential-free
// authority with a nonzero port.
func ParseProxyTarget(proxyURL *url.URL, trustedOrigins []string, target string) (*ProxyTarget, error) {
	if proxyURL == nil {
		return nil, fmt.Errorf("proxy URL is required")
	}
	if proxyURL.Scheme != "https" {
		return nil, fmt.Errorf("proxy URL must use HTTPS")
	}
	if proxyURL.User != nil || proxyURL.RawQuery != "" || proxyURL.Fragment != "" ||
		(proxyURL.Path != "" && proxyURL.Path != "/") {
		return nil, fmt.Errorf("proxy URL must be an HTTPS origin")
	}
	if !containsString(trustedOrigins, originOf(proxyURL)) {
		return nil, fmt.Errorf("proxy URL origin is not trusted")
	}
	host := proxyURL.Hostname()
	if host == "" {
		return nil, fmt.Errorf("proxy URL has no host")
	}
	port := 443
	if raw := proxyURL.Port(); raw != "" {
		parsed, err := parsePort(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL port")
		}
		port = parsed
	}
	if strings.Contains(target, "@") {
		return nil, fmt.Errorf("CONNECT target must not contain credentials")
	}
	authorityHost, authorityPort, err := net.SplitHostPort(target)
	if err != nil || authorityHost == "" {
		return nil, fmt.Errorf("invalid CONNECT target")
	}
	if parsedPort, err := parsePort(authorityPort); err != nil {
		return nil, fmt.Errorf("invalid CONNECT target")
	} else if parsedPort == 0 {
		return nil, fmt.Errorf("CONNECT target must include a nonzero port")
	} else if parsedPort > 65535 {
		return nil, fmt.Errorf("invalid CONNECT target")
	}
	return &ProxyTarget{Host: host, Port: port, Authority: target}, nil
}

// parsePort accepts only decimal digits, mirroring Rust's `Authority` parsing
// (`"22/path"` is not a port).
func parsePort(raw string) (int, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty port")
	}
	value := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, fmt.Errorf("invalid port %q", raw)
		}
		value = value*10 + int(raw[i]-'0')
		if value > 65535 {
			return 0, fmt.Errorf("port out of range")
		}
	}
	return value, nil
}

// originOf renders an origin the way Rust's `Url::origin().ascii_serialization()`
// does: scheme, host, and an explicit port only when it is not the scheme
// default.
func originOf(u *url.URL) string {
	if u.Port() == "" || u.Port() == "443" {
		return u.Scheme + "://" + u.Hostname()
	}
	return u.Scheme + "://" + net.JoinHostPort(u.Hostname(), u.Port())
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
