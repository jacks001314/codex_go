package network

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultProxyURL      = "http://127.0.0.1:3128"
	DefaultProxySocksURL = "http://127.0.0.1:8081"
)

type ProxyMode string

const (
	ProxyModeLimited ProxyMode = "limited"
	ProxyModeFull    ProxyMode = "full"
)

func (m *ProxyMode) AllowsMethod(method string) bool {
	if m == nil || *m == ProxyModeFull {
		return true
	}
	return method == "GET" || method == "HEAD" || method == "OPTIONS"
}

type ProxyDomainPermission string

const (
	ProxyDomainNone  ProxyDomainPermission = "none"
	ProxyDomainAllow ProxyDomainPermission = "allow"
	ProxyDomainDeny  ProxyDomainPermission = "deny"
)

type ProxyDomainPermissionEntry struct {
	Pattern    string
	Permission ProxyDomainPermission
}

type ProxyDomainPermissions struct {
	Entries []ProxyDomainPermissionEntry
}

func (p *ProxyDomainPermissions) EffectiveEntries() []ProxyDomainPermissionEntry {
	if p == nil {
		return nil
	}
	order := make([]string, 0, len(p.Entries))
	effective := map[string]ProxyDomainPermission{}
	for _, entry := range p.Entries {
		if _, ok := effective[entry.Pattern]; !ok {
			order = append(order, entry.Pattern)
		}
		if rankPermission(entry.Permission) > rankPermission(effective[entry.Pattern]) {
			effective[entry.Pattern] = entry.Permission
		}
	}
	out := make([]ProxyDomainPermissionEntry, 0, len(order))
	for _, pattern := range order {
		out = append(out, ProxyDomainPermissionEntry{Pattern: pattern, Permission: effective[pattern]})
	}
	return out
}

type ProxyUnixSocketPermission string

const (
	ProxyUnixSocketAllow ProxyUnixSocketPermission = "allow"
	ProxyUnixSocketDeny  ProxyUnixSocketPermission = "deny"
)

type ProxyUnixSocketPermissions struct {
	Entries map[string]ProxyUnixSocketPermission
}

type ProxyConfig struct {
	Network               ProxySettings
	PolicyDecider         ProxyPolicyDecider
	BlockedObserver       ProxyBlockedRequestObserver
	AuditSink             ProxyPolicyAuditSink
	AuditMetadata         ProxyAuditMetadata
	AuditMetadataProvider ProxyAuditMetadataProvider
	EnvironmentID         string
}

type ProxySettings struct {
	Enabled                                      bool
	ProxyURL                                     string
	EnableSocks5                                 bool
	SocksURL                                     string
	EnableSocks5UDP                              bool
	AllowUpstreamProxy                           bool
	DangerouslyAllowNonLoopbackProxy             bool
	DangerouslyAllowAllUnixSockets               bool
	Mode                                         ProxyMode
	Domains                                      *ProxyDomainPermissions
	UnixSockets                                  *ProxyUnixSocketPermissions
	AllowLocalBinding                            bool
	MITM                                         bool
	MITMHooks                                    []ProxyMITMHookConfig
	CredentialBroker                             bool
	DangerouslyAllowPlaintextCredentialInjection bool
}

func DefaultProxySettings() ProxySettings {
	return ProxySettings{
		ProxyURL:           DefaultProxyURL,
		EnableSocks5:       true,
		SocksURL:           DefaultProxySocksURL,
		EnableSocks5UDP:    true,
		AllowUpstreamProxy: true,
		Mode:               ProxyModeFull,
		Domains:            nil,
		UnixSockets:        nil,
		AllowLocalBinding:  false,
		MITM:               false,
		CredentialBroker:   false,
		Enabled:            false,
	}
}

func (s *ProxySettings) AllowedDomains() []string {
	return s.domainEntries(ProxyDomainAllow)
}

func (s *ProxySettings) DeniedDomains() []string {
	return s.domainEntries(ProxyDomainDeny)
}

func (s *ProxySettings) AllowUnixSockets() []string {
	if s == nil || s.UnixSockets == nil {
		return nil
	}
	out := make([]string, 0, len(s.UnixSockets.Entries))
	for path, permission := range s.UnixSockets.Entries {
		if permission == ProxyUnixSocketAllow {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func (s *ProxySettings) SetAllowedDomains(domains []string) {
	s.setDomainEntries(domains, ProxyDomainAllow)
}

func (s *ProxySettings) SetDeniedDomains(domains []string) {
	s.setDomainEntries(domains, ProxyDomainDeny)
}

func (s *ProxySettings) UpsertDomainPermission(host string, permission ProxyDomainPermission, normalize func(string) string) {
	if normalize == nil {
		normalize = NormalizeProxyHost
	}
	if s.Domains == nil {
		s.Domains = &ProxyDomainPermissions{}
	}
	normalized := normalize(host)
	filtered := s.Domains.Entries[:0]
	for _, entry := range s.Domains.Entries {
		if normalize(entry.Pattern) != normalized {
			filtered = append(filtered, entry)
		}
	}
	s.Domains.Entries = append(filtered, ProxyDomainPermissionEntry{Pattern: host, Permission: permission})
}

func (s *ProxySettings) SetAllowUnixSockets(paths []string) {
	entries := map[string]ProxyUnixSocketPermission{}
	if s.UnixSockets != nil {
		for path, permission := range s.UnixSockets.Entries {
			if permission != ProxyUnixSocketAllow {
				entries[path] = permission
			}
		}
	}
	for _, path := range paths {
		entries[path] = ProxyUnixSocketAllow
	}
	if len(entries) == 0 {
		s.UnixSockets = nil
		return
	}
	s.UnixSockets = &ProxyUnixSocketPermissions{Entries: entries}
}

func (s *ProxySettings) domainEntries(permission ProxyDomainPermission) []string {
	if s == nil || s.Domains == nil {
		return nil
	}
	out := []string{}
	for _, entry := range s.Domains.EffectiveEntries() {
		if entry.Permission == permission {
			out = append(out, entry.Pattern)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *ProxySettings) setDomainEntries(entries []string, permission ProxyDomainPermission) {
	if s.Domains == nil {
		s.Domains = &ProxyDomainPermissions{}
	}
	filtered := s.Domains.Entries[:0]
	for _, entry := range s.Domains.Entries {
		if entry.Permission != permission {
			filtered = append(filtered, entry)
		}
	}
	seen := map[string]bool{}
	for _, entry := range filtered {
		if entry.Permission == permission {
			seen[entry.Pattern] = true
		}
	}
	for _, entry := range entries {
		if !seen[entry] {
			filtered = append(filtered, ProxyDomainPermissionEntry{Pattern: entry, Permission: permission})
			seen[entry] = true
		}
	}
	s.Domains.Entries = filtered
	if len(s.Domains.Entries) == 0 {
		s.Domains = nil
	}
}

type ProxyRuntimeConfig struct {
	HTTPAddr  net.TCPAddr
	SocksAddr net.TCPAddr
}

func ResolveProxyRuntime(config ProxyConfig) (ProxyRuntimeConfig, error) {
	if _, err := CompileProxyDomainMatcher(config.Network.AllowedDomains(), false); err != nil {
		return ProxyRuntimeConfig{}, fmt.Errorf("compile network.allowed_domains: %w", err)
	}
	if _, err := CompileProxyDomainMatcher(config.Network.DeniedDomains(), true); err != nil {
		return ProxyRuntimeConfig{}, err
	}
	if config.Network.CredentialBroker && !config.Network.MITM {
		return ProxyRuntimeConfig{}, fmt.Errorf("network.credential_broker requires network.mitm = true")
	}
	if err := ValidateProxyMITMHookConfig(config); err != nil {
		return ProxyRuntimeConfig{}, err
	}
	if err := ValidateProxyUnixSocketAllowlistPaths(config); err != nil {
		return ProxyRuntimeConfig{}, err
	}
	httpAddr, err := ResolveProxyAddr(config.Network.ProxyURL, 3128)
	if err != nil {
		return ProxyRuntimeConfig{}, fmt.Errorf("invalid network.proxy_url: %w", err)
	}
	socksAddr, err := ResolveProxyAddr(config.Network.SocksURL, 8081)
	if err != nil {
		return ProxyRuntimeConfig{}, fmt.Errorf("invalid network.socks_url: %w", err)
	}
	httpAddr, socksAddr = ClampProxyBindAddrs(httpAddr, socksAddr, config.Network)
	return ProxyRuntimeConfig{HTTPAddr: httpAddr, SocksAddr: socksAddr}, nil
}

func ResolveProxyAddr(value string, defaultPort uint16) (net.TCPAddr, error) {
	parts, err := ParseProxyHostPort(value, defaultPort)
	if err != nil {
		return net.TCPAddr{}, err
	}
	host := parts.Host
	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ip = net.ParseIP("127.0.0.1")
	}
	return net.TCPAddr{IP: ip, Port: int(parts.Port)}, nil
}

func ClampProxyBindAddrs(httpAddr net.TCPAddr, socksAddr net.TCPAddr, settings ProxySettings) (net.TCPAddr, net.TCPAddr) {
	httpAddr = clampNonLoopback(httpAddr, settings.DangerouslyAllowNonLoopbackProxy)
	socksAddr = clampNonLoopback(socksAddr, settings.DangerouslyAllowNonLoopbackProxy)
	if len(settings.AllowUnixSockets()) == 0 && !settings.DangerouslyAllowAllUnixSockets {
		return httpAddr, socksAddr
	}
	httpAddr.IP = net.ParseIP("127.0.0.1")
	socksAddr.IP = net.ParseIP("127.0.0.1")
	return httpAddr, socksAddr
}

type ProxySocketAddressParts struct {
	Host string
	Port uint16
}

func ParseProxyHostPort(value string, defaultPort uint16) (ProxySocketAddressParts, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ProxySocketAddressParts{}, fmt.Errorf("missing host in network proxy address")
	}
	if ip := net.ParseIP(trimmed); ip != nil && strings.Contains(trimmed, ":") && !strings.HasPrefix(trimmed, "[") {
		return ProxySocketAddressParts{Host: trimmed, Port: defaultPort}, nil
	}
	candidate := trimmed
	if !strings.Contains(candidate, "://") {
		candidate = "http://" + candidate
	}
	if parsed, err := url.Parse(candidate); err == nil && parsed.Hostname() != "" {
		port := defaultPort
		if parsed.Port() != "" {
			if parsedPort, err := strconv.ParseUint(parsed.Port(), 10, 16); err == nil {
				port = uint16(parsedPort)
			}
		}
		return ProxySocketAddressParts{Host: parsed.Hostname(), Port: port}, nil
	}
	return parseHostPortFallback(trimmed, defaultPort)
}

func ProxyHostAndPortFromNetworkAddr(value string, defaultPort uint16) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "<missing>"
	}
	parts, err := ParseProxyHostPort(trimmed, defaultPort)
	if err != nil {
		return formatHostAndPort(trimmed, defaultPort)
	}
	return formatHostAndPort(parts.Host, parts.Port)
}

func ValidateProxyUnixSocketAllowlistPaths(config ProxyConfig) error {
	for index, socketPath := range config.Network.AllowUnixSockets() {
		if !strings.HasPrefix(socketPath, "/") {
			return fmt.Errorf("invalid network.allow_unix_sockets[%d]: expected an absolute path, got %q", index, socketPath)
		}
	}
	return nil
}

func rankPermission(permission ProxyDomainPermission) int {
	switch permission {
	case ProxyDomainDeny:
		return 2
	case ProxyDomainAllow:
		return 1
	default:
		return 0
	}
}

func clampNonLoopback(addr net.TCPAddr, allow bool) net.TCPAddr {
	if addr.IP == nil || addr.IP.IsLoopback() || allow {
		return addr
	}
	addr.IP = net.ParseIP("127.0.0.1")
	return addr
}

func formatHostAndPort(host string, port uint16) string {
	if strings.Contains(host, ":") {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func parseHostPortFallback(input string, defaultPort uint16) (ProxySocketAddressParts, error) {
	withoutScheme := input
	if _, rest, ok := strings.Cut(input, "://"); ok {
		withoutScheme = rest
	}
	hostPort := strings.Split(withoutScheme, "/")[0]
	if _, rest, ok := strings.Cut(hostPort, "@"); ok {
		hostPort = rest
	}
	if strings.HasPrefix(hostPort, "[") {
		if end := strings.Index(hostPort, "]"); end >= 0 {
			host := hostPort[1:end]
			port := defaultPort
			if rest := strings.TrimPrefix(hostPort[end+1:], ":"); rest != hostPort[end+1:] {
				if parsed, err := strconv.ParseUint(rest, 10, 16); err == nil {
					port = uint16(parsed)
				}
			}
			if host == "" {
				return ProxySocketAddressParts{}, fmt.Errorf("missing host in network proxy address")
			}
			return ProxySocketAddressParts{Host: host, Port: port}, nil
		}
	}
	if strings.Count(hostPort, ":") == 1 {
		host, portText, _ := strings.Cut(hostPort, ":")
		if host == "" {
			return ProxySocketAddressParts{}, fmt.Errorf("missing host in network proxy address")
		}
		port := defaultPort
		if parsed, err := strconv.ParseUint(portText, 10, 16); err == nil {
			port = uint16(parsed)
		}
		return ProxySocketAddressParts{Host: host, Port: port}, nil
	}
	if hostPort == "" {
		return ProxySocketAddressParts{}, fmt.Errorf("missing host in network proxy address")
	}
	return ProxySocketAddressParts{Host: hostPort, Port: defaultPort}, nil
}
