// Package tcptunnel implements the hidden `codex tcp-tunnel` command: loopback
// TCP forwarding through an HTTP/3 CONNECT proxy, mirroring Rust's
// `codex-tcp-tunnel` crate. Bearer credentials and optional extension headers
// enter only through stdin; the listener survives transport loss, but
// individual TCP streams are never replayed.
package tcptunnel

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
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
	// DiagnosticsJSON emits credential-safe JSON diagnostics on stderr instead
	// of human-readable errors (Rust #50131).
	DiagnosticsJSON bool
}

// Diagnostic mirrors a clap argument diagnostic for the hidden `tcp-tunnel`
// command so the CLI can print the same block, trailing hint and exit status as
// Rust.
type Diagnostic struct {
	// Message is clap's diagnostic body without the leading "error: ".
	Message string
	// Usage is the "Usage: ..." block; it is empty for diagnostics clap prints
	// without one (invalid and missing values).
	Usage string
}

func (d *Diagnostic) Error() string { return d.Message }

const (
	tcpTunnelRequiredUsage   = "Usage: codex tcp-tunnel --proxy-url <PROXY_URL> --proxy-origins-file <PROXY_ORIGINS_FILE> --target <TARGET>"
	tcpTunnelUnexpectedUsage = "Usage: codex tcp-tunnel [OPTIONS] --proxy-url <PROXY_URL> --proxy-origins-file <PROXY_ORIGINS_FILE> --target <TARGET>"
	tcpTunnelGroupUsage      = "Usage: codex tcp-tunnel <--proxy-url <PROXY_URL>|--proxy-origins-file <PROXY_ORIGINS_FILE>|--target <TARGET>|--listen-addr <LISTEN_ADDR>|--auth-token-stdin|--auth-token-updates-stdin|--connect-headers-stdin|--diagnostics-json>"
)

// tcpTunnelValueFlags maps the value-taking flags to clap's placeholders.
var tcpTunnelValueFlags = map[string]string{
	"--proxy-url":          "<PROXY_URL>",
	"--proxy-origins-file": "<PROXY_ORIGINS_FILE>",
	"--target":             "<TARGET>",
	"--listen-addr":        "<LISTEN_ADDR>",
}

// tcpTunnelFlagOrder is the order clap lists the required options in.
var tcpTunnelFlagOrder = []string{"--proxy-url", "--proxy-origins-file", "--target"}

// ParseArgs parses the `codex tcp-tunnel` argument list with clap's shape: long
// options with a separated or `=`-attached value, the same requirement rules
// (`--auth-token-updates-stdin` and `--connect-headers-stdin` require
// `--auth-token-stdin`), and clap's value parsers for the proxy URL and the
// listen address.
func ParseArgs(argv []string) (*Args, error) {
	args := &Args{ListenAddr: DefaultListenAddr}
	seen := map[string]bool{}
	var consumed []string
	valueFlags, boolFlags := 0, 0
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		name, value, hasValue := strings.Cut(arg, "=")
		if placeholder, takesValue := tcpTunnelValueFlags[name]; takesValue {
			var parsed string
			if hasValue {
				parsed = value
			} else {
				if i+1 >= len(argv) {
					return nil, &Diagnostic{Message: "a value is required for '" + name + " " + placeholder + "' but none was supplied"}
				}
				i++
				parsed = argv[i]
			}
			if reason, ok := tcpTunnelValueProblem(name, parsed); !ok {
				return nil, &Diagnostic{Message: "invalid value '" + parsed + "' for '" + name + " " + placeholder + "': " + reason}
			}
			switch name {
			case "--proxy-url":
				args.ProxyURL = parsed
			case "--proxy-origins-file":
				args.ProxyOriginsFile = parsed
			case "--target":
				args.Target = parsed
			case "--listen-addr":
				args.ListenAddr = parsed
			}
			seen[name] = true
			consumed = append(consumed, name)
			valueFlags++
			continue
		}
		switch name {
		case "--auth-token-stdin", "--auth-token-updates-stdin", "--connect-headers-stdin", "--diagnostics-json":
			if hasValue {
				return nil, &Diagnostic{
					Message: "unexpected value '" + value + "' for '" + name + "' found; no more were expected",
					Usage:   tcpTunnelUnexpectedValueUsage(name, valueFlags, boolFlags),
				}
			}
			switch name {
			case "--auth-token-stdin":
				args.AuthTokenStdin = true
			case "--auth-token-updates-stdin":
				args.AuthTokenUpdatesStdin = true
			case "--connect-headers-stdin":
				args.ConnectHeadersStdin = true
			case "--diagnostics-json":
				args.DiagnosticsJSON = true
			}
			consumed = append(consumed, name)
			boolFlags++
		default:
			reported := arg
			if hasValue && strings.HasPrefix(arg, "--") {
				reported = name
			}
			return nil, &Diagnostic{Message: "unexpected argument '" + reported + "' found", Usage: tcpTunnelUnexpectedUsage}
		}
	}
	requiresAuth := (args.AuthTokenUpdatesStdin || args.ConnectHeadersStdin) && !args.AuthTokenStdin
	var missing []string
	for _, name := range tcpTunnelFlagOrder {
		if !seen[name] {
			missing = append(missing, name+" "+tcpTunnelValueFlags[name])
		}
	}
	if requiresAuth {
		missing = append(missing, "--auth-token-stdin")
	}
	if len(missing) > 0 {
		return nil, &Diagnostic{
			Message: tcpTunnelRequiredMessage(missing),
			Usage:   tcpTunnelMissingUsage(requiresAuth, consumed),
		}
	}
	return args, nil
}

func tcpTunnelRequiredMessage(missing []string) string {
	message := "the following required arguments were not provided:"
	for _, name := range missing {
		message += "\n  " + name
	}
	return message
}

// tcpTunnelMissingUsage echoes the required options plus every optional flag
// clap already consumed, in argv order (Rust's clap smart usage).
func tcpTunnelMissingUsage(requiresAuth bool, consumed []string) string {
	usage := tcpTunnelRequiredUsage
	added := map[string]bool{}
	if requiresAuth {
		usage += " --auth-token-stdin"
		added["--auth-token-stdin"] = true
	}
	for _, name := range consumed {
		if added[name] {
			continue
		}
		for _, required := range tcpTunnelFlagOrder {
			if name == required {
				added[name] = true
				break
			}
		}
		if added[name] {
			continue
		}
		if placeholder, ok := tcpTunnelValueFlags[name]; ok {
			usage += " " + name + " " + placeholder
		} else {
			usage += " " + name
		}
		added[name] = true
	}
	return usage
}

// tcpTunnelUnexpectedValueUsage mirrors clap's usage block for a value attached
// to a boolean flag: clap lists the required options plus the offending flag
// while at most one other option was consumed, and falls back to its required
// group rendering otherwise.
func tcpTunnelUnexpectedValueUsage(name string, valueFlags, boolFlags int) string {
	if boolFlags == 0 && valueFlags <= 1 {
		return tcpTunnelRequiredUsage + " " + name
	}
	return tcpTunnelGroupUsage
}

// tcpTunnelValueProblem runs clap's value parser for the flag: the URL parser
// Rust uses (`url::Url`) and `std::net::SocketAddr` for the listen address. The
// reason strings match the underlying crates so the diagnostic is identical.
func tcpTunnelValueProblem(name, value string) (string, bool) {
	switch name {
	case "--proxy-url":
		return tcpTunnelURLProblem(value)
	case "--listen-addr":
		if _, err := netip.ParseAddrPort(value); err != nil {
			return "invalid socket address syntax", false
		}
	}
	return "", true
}

var tcpTunnelSchemePrefix = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// tcpTunnelURLProblem mirrors `url::Url::parse`'s diagnostics for the cases a
// proxy URL can hit.
func tcpTunnelURLProblem(raw string) (string, bool) {
	if !tcpTunnelSchemePrefix.MatchString(raw) {
		return "relative URL without a base", false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "invalid port"):
			return "invalid port number", false
		case strings.Contains(err.Error(), "missing ']'") || strings.Contains(err.Error(), "invalid character \"[\""):
			return "invalid IPv6 address", false
		}
		return "relative URL without a base", false
	}
	switch parsed.Scheme {
	case "http", "https", "ftp", "ws", "wss":
		if parsed.Host == "" {
			return "empty host", false
		}
	}
	return "", true
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
