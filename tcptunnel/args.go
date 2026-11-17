// Package tcptunnel implements the hidden `codex tcp-tunnel` command: a
// single-stream OpenSSH ProxyCommand relay through one authenticated CONNECT to
// the verified Cloud Portals gateway. OpenSSH owns SSH authentication, host-key
// verification and every byte that crosses stdio; the helper only hands the
// signed-in account's credential to a gateway shipped with the CLI. Selection,
// HTTP/2 fallback and system HTTPS proxy support mirror Rust #61975/#61964.
package tcptunnel

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// DefaultSSHPort is the published Portals SSH port used when
// `--environment-id` omits `--port`.
const DefaultSSHPort = 22

// Args mirrors Rust's `codex_tcp_tunnel::Args`. Every field is parsed by clap
// in Rust, so ParseArgs reproduces clap's validation and diagnostics.
type Args struct {
	// ProxyURL is the verified HTTPS gateway origin.
	ProxyURL string
	// EnvironmentID names the canonical Cloud environment to reach.
	EnvironmentID string
	// Port is the published SSH port for EnvironmentID; zero means 22.
	Port int
	// Target is the ERS-issued legacy CONNECT authority. It must be an IPv4
	// socket address and requires a private token file.
	Target string
	// TokenFile is a one-use bearer in a private file supplied by Desktop. It
	// is never read from SSH stdin.
	TokenFile string

	// set tracks clap presence for optional values whose empty string is still
	// a supplied value (`--environment-id ""`, `--token-file ""`).
	environmentIDSet bool
	tokenFileSet     bool
}

// UsesTokenFile reports whether `--token-file` was supplied, matching Rust's
// `args.token_file.is_some()` dispatch.
func (a *Args) UsesTokenFile() bool { return a != nil && a.tokenFileSet }

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

const tcpTunnelUnexpectedUsage = "Usage: codex tcp-tunnel [OPTIONS]"

// tcpTunnelValueFlags maps the value-taking flags to clap's placeholders.
var tcpTunnelValueFlags = map[string]string{
	"--proxy-url":      "<PROXY_URL>",
	"--environment-id": "<ENVIRONMENT_ID>",
	"--port":           "<PORT>",
	"--target":         "<TARGET>",
	"--token-file":     "<TOKEN_FILE>",
}

// ParseArgs parses the `codex tcp-tunnel` argument list with clap's shape: long
// options with a separated or `=`-attached value, clap's value parsers for the
// proxy URL, the published port and the legacy socket address, and clap's
// conditional requirements (`--environment-id` unless `--target`, `--port`
// requires `--environment-id`, `--target` requires `--token-file` and
// conflicts with `--environment-id`).
func ParseArgs(argv []string) (*Args, error) {
	args := &Args{ProxyURL: PublicGateway}
	seen := map[string]bool{}
	var consumed []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		name, value, hasValue := strings.Cut(arg, "=")
		placeholder, takesValue := tcpTunnelValueFlags[name]
		if !takesValue {
			reported := arg
			if hasValue && strings.HasPrefix(arg, "--") {
				reported = name
			}
			return nil, &Diagnostic{Message: "unexpected argument '" + reported + "' found", Usage: tcpTunnelUnexpectedUsage}
		}
		if seen[name] {
			return nil, &Diagnostic{
				Message: "the argument '" + name + " " + placeholder + "' cannot be used multiple times",
				Usage:   tcpTunnelUnexpectedUsage,
			}
		}
		var parsed string
		if hasValue {
			parsed = value
		} else {
			if i+1 >= len(argv) {
				return nil, &Diagnostic{Message: "a value is required for '" + name + " " + placeholder + "' but none was supplied"}
			}
			next := argv[i+1]
			if len(next) > 1 && next[0] == '-' {
				// clap refuses to consume a flag-shaped token as a value: a
				// known flag reports the missing value, anything else is an
				// unexpected argument. A bare "-" is still a value.
				nextName, _, _ := strings.Cut(next, "=")
				if _, known := tcpTunnelValueFlags[nextName]; known {
					return nil, &Diagnostic{Message: "a value is required for '" + name + " " + placeholder + "' but none was supplied"}
				}
				return nil, &Diagnostic{Message: "unexpected argument '" + next + "' found", Usage: tcpTunnelUnexpectedUsage}
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
		case "--environment-id":
			args.EnvironmentID = parsed
			args.environmentIDSet = true
		case "--port":
			args.Port, _ = strconv.Atoi(parsed)
		case "--target":
			args.Target = parsed
		case "--token-file":
			args.TokenFile = parsed
			args.tokenFileSet = true
		}
		seen[name] = true
		consumed = append(consumed, name)
	}
	if conflict := tcpTunnelConflict(args, consumed); conflict != nil {
		return nil, conflict
	}
	if missing := tcpTunnelMissing(args, consumed); len(missing) > 0 {
		// clap prunes a requirement implied by `--port` once it conflicts with a
		// supplied `--target` and every other requirement is satisfied.
		if len(missing) == 1 && missing[0] == "--environment-id" && args.Target != "" && !args.environmentIDSet {
			return args, nil
		}
		return nil, &Diagnostic{
			Message: tcpTunnelRequiredMessage(missing),
			Usage:   "Usage: codex tcp-tunnel" + tcpTunnelUsage(args, consumed, missing),
		}
	}
	return args, nil
}

// tcpTunnelConflict reproduces clap's conflict validation for the pair
// `--environment-id` / `--target`. clap reports the argument that appears
// first in argv as the one that cannot be used with the other. Requirements
// implied by `--port` are dropped when they conflict with a supplied argument,
// which is why only explicitly supplied pairs are reported.
func tcpTunnelConflict(args *Args, consumed []string) *Diagnostic {
	if !args.environmentIDSet || args.Target == "" {
		return nil
	}
	first, second := "--environment-id", "--target"
	if tcpTunnelArgIndex(consumed, "--target") < tcpTunnelArgIndex(consumed, "--environment-id") {
		first, second = second, first
	}
	kept := make([]string, 0, len(consumed))
	for _, used := range consumed {
		if used != second {
			kept = append(kept, used)
		}
	}
	return &Diagnostic{
		Message: "the argument '" + tcpTunnelStylized(first) + "' cannot be used with '" + tcpTunnelStylized(second) + "'",
		Usage:   "Usage: codex tcp-tunnel" + tcpTunnelUsage(args, kept, nil),
	}
}

func tcpTunnelArgIndex(consumed []string, name string) int {
	for index, used := range consumed {
		if used == name {
			return index
		}
	}
	return len(consumed)
}

func tcpTunnelStylized(name string) string {
	if placeholder, ok := tcpTunnelValueFlags[name]; ok {
		return name + " " + placeholder
	}
	return name
}

// tcpTunnelMissing lists the arguments clap reports in
// "the following required arguments were not provided", in requirement order:
// the `--port` / `--target` requirements first (in argv order) and the
// unconditional `--environment-id` last when neither route was supplied.
func tcpTunnelMissing(args *Args, consumed []string) []string {
	var missing []string
	add := func(name string) {
		for _, present := range missing {
			if present == name {
				return
			}
		}
		missing = append(missing, name)
	}
	for _, used := range consumed {
		switch used {
		case "--port":
			if !args.environmentIDSet {
				add("--environment-id")
			}
		case "--target":
			if !args.tokenFileSet {
				add("--token-file")
			}
		}
	}
	if !args.environmentIDSet && args.Target == "" {
		add("--environment-id")
	}
	return missing
}

func tcpTunnelRequiredMessage(missing []string) string {
	message := "the following required arguments were not provided:"
	for _, name := range missing {
		message += "\n  " + tcpTunnelStylized(name)
	}
	return message
}

// tcpTunnelUsage reproduces clap's smart usage block: supplied arguments in
// argv order, with a requirement implied by a supplied argument inserted just
// before it, and an unconditional missing requirement appended last.
func tcpTunnelUsage(args *Args, consumed []string, missing []string) string {
	needed := map[string]bool{}
	for _, name := range missing {
		needed[name] = true
	}
	printed := map[string]bool{}
	usage := ""
	emit := func(name string) {
		if printed[name] {
			return
		}
		printed[name] = true
		usage += " " + tcpTunnelStylized(name)
	}
	for _, used := range consumed {
		switch used {
		case "--port":
			if needed["--environment-id"] {
				emit("--environment-id")
			}
		case "--target":
			if needed["--token-file"] {
				emit("--token-file")
			}
		}
	}
	for _, used := range consumed {
		emit(used)
	}
	if needed["--environment-id"] && tcpTunnelArgIndex(consumed, "--port") == len(consumed) {
		emit("--environment-id")
	}
	if needed["--token-file"] {
		emit("--token-file")
	}
	_ = args
	return usage
}

// tcpTunnelValueProblem runs clap's value parser for the flag: the URL parser
// Rust uses (`url::Url`), `NonZeroU16` for the published port and
// `std::net::SocketAddr` for the legacy target. The reason strings match the
// underlying crates so the diagnostic is identical.
func tcpTunnelValueProblem(name, value string) (string, bool) {
	switch name {
	case "--proxy-url":
		return tcpTunnelURLProblem(value)
	case "--target":
		if _, err := netip.ParseAddrPort(value); err != nil {
			return "invalid socket address syntax", false
		}
	case "--port":
		return tcpTunnelPortProblem(value)
	}
	return "", true
}

// tcpTunnelPortProblem mirrors `NonZeroU16::from_str`'s clap diagnostics.
func tcpTunnelPortProblem(value string) (string, bool) {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return "invalid digit found in string", false
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		if strings.Contains(err.Error(), "value out of range") {
			return "number too large to fit in target type", false
		}
		return "invalid digit found in string", false
	}
	if parsed == 0 {
		return "number would be zero for non-zero type", false
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
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
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
		(proxyURL.Path != "" && proxyURL.Path != "/") || proxyURL.Host == "" {
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
