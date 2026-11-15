package network

import (
	"context"
	"fmt"
	"net"
	"strings"
	"unicode"

	"github.com/gobwas/glob"
)

const (
	ProxyReasonDenied                = "denied"
	ProxyReasonMethodNotAllowed      = "method_not_allowed"
	ProxyReasonMITMHookDenied        = "mitm_hook_denied"
	ProxyReasonMITMRequired          = "mitm_required"
	ProxyReasonNotAllowed            = "not_allowed"
	ProxyReasonNotAllowedLocal       = "not_allowed_local"
	ProxyReasonPolicyDenied          = "policy_denied"
	ProxyReasonProxyDisabled         = "proxy_disabled"
	ProxyReasonUnixSocketUnsupported = "unix_socket_unsupported"
)

type ProxyHost struct {
	value string
}

func ParseProxyHost(input string) (ProxyHost, error) {
	normalized := NormalizeProxyHost(input)
	if normalized == "" {
		return ProxyHost{}, &ProxyParseError{Message: "host is empty"}
	}
	return ProxyHost{value: normalized}, nil
}

func (h *ProxyHost) String() string {
	if h == nil {
		return ""
	}
	return h.value
}

func NormalizeProxyHost(host string) string {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end >= 0 {
			return normalizeDNSHostOrIPLiteral(host[1:end])
		}
	}
	if strings.Count(host, ":") == 1 {
		before, _, _ := strings.Cut(host, ":")
		return normalizeDNSHostOrIPLiteral(before)
	}
	return normalizeDNSHostOrIPLiteral(host)
}

func IsLoopbackProxyHost(host ProxyHost) bool {
	value := unscopedIPLiteral(host.value)
	if value == "" {
		value = host.value
	}
	if value == "localhost" {
		return true
	}
	ip := net.ParseIP(value)
	return ip != nil && ip.IsLoopback()
}

func IsNonPublicProxyIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return len(ip) == net.IPv6len && (ip[0]&0xfe) == 0xfc
	}
	return ipv4InCIDR(ip4, [4]byte{0, 0, 0, 0}, 8) ||
		ipv4InCIDR(ip4, [4]byte{100, 64, 0, 0}, 10) ||
		ipv4InCIDR(ip4, [4]byte{192, 0, 0, 0}, 24) ||
		ipv4InCIDR(ip4, [4]byte{192, 0, 2, 0}, 24) ||
		ipv4InCIDR(ip4, [4]byte{198, 18, 0, 0}, 15) ||
		ipv4InCIDR(ip4, [4]byte{198, 51, 100, 0}, 24) ||
		ipv4InCIDR(ip4, [4]byte{203, 0, 113, 0}, 24) ||
		ipv4InCIDR(ip4, [4]byte{240, 0, 0, 0}, 4)
}

// IsPrivateNetworkProxyIP reports whether ip is a private unicast destination
// that an upstream VPN proxy may be able to reach (Rust #48568
// network-proxy/src/policy.rs::is_private_network_ip): RFC 1918,
// carrier-grade NAT (100.64.0.0/10), and IPv6 unique-local (fc00::/7)
// addresses. Loopback, link-local, and other special-use addresses keep their
// normal direct routing.
func IsPrivateNetworkProxyIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// Rust recurses into the IPv4 classification for IPv4-mapped IPv6 literals;
	// net.IP.IsPrivate already unwraps those through To4.
	if ip.IsPrivate() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ipv4InCIDR(ip4, [4]byte{100, 64, 0, 0}, 10)
	}
	return false
}

type ProxyDomainPatternKind string

const (
	ProxyPatternExact             ProxyDomainPatternKind = "exact"
	ProxyPatternSubdomainsOnly    ProxyDomainPatternKind = "subdomains_only"
	ProxyPatternApexAndSubdomains ProxyDomainPatternKind = "apex_and_subdomains"
)

type ProxyDomainPattern struct {
	Kind   ProxyDomainPatternKind
	Domain string
}

// ProxyDomainMatcher matches candidate hosts against normalized domain patterns.
//
// Wildcards operate on UTF-8 bytes, mirroring Rust
// network-proxy/src/domain_matcher.rs (`DomainPatternSet::patterns` is
// `Arc<[Vec<u8>]>` and `matches(pattern: &[u8], host: &[u8])`; #52277 /
// 14ff9c3a). `?` therefore consumes exactly one byte, so "api??.com" matches
// "apié.com" while "api?.com" does not, and literal bytes compare with ASCII
// case folding (domain_matcher.rs:190-214).
type ProxyDomainMatcher struct {
	patterns []glob.Glob
}

// proxyDomainByteMatcher is a glob.Glob over UTF-8 bytes for the wildcard subset
// Rust supports: `*`, `?` and literal bytes. gobwas/glob matches runes and only
// lets `?` consume runes that appear in the pattern, so byte-level patterns are
// compiled here instead. Patterns that use gobwas/glob extensions (`[...]`
// classes, `{...}` alternatives, escapes) keep the glob library, whose ASCII
// behavior is unchanged.
type proxyDomainByteMatcher struct {
	pattern string
}

func (m proxyDomainByteMatcher) String() string {
	return m.pattern
}

func (m proxyDomainByteMatcher) Match(subject string) bool {
	return proxyDomainByteMatch(m.pattern, subject)
}

// proxyDomainByteMatch ports Rust's byte matcher (network-proxy/src/domain_matcher.rs:190-214):
// only the latest star is retried, so work stays bounded by pattern length times host length.
func proxyDomainByteMatch(pattern string, host string) bool {
	position := 0
	index := 0
	starResume := -1
	starConsumed := 0
	for index < len(host) {
		if position < len(pattern) {
			octet := pattern[position]
			switch {
			case octet == '*':
				starResume = position + 1
				starConsumed = index
				position++
				continue
			case octet == '?' || proxyDomainByteEqualFold(octet, host[index]):
				position++
				index++
				continue
			}
		}
		if starResume >= 0 {
			starConsumed++
			position = starResume
			index = starConsumed
			continue
		}
		return false
	}
	for position < len(pattern) && pattern[position] == '*' {
		position++
	}
	return position == len(pattern)
}

// proxyDomainByteEqualFold mirrors Rust's `u8::eq_ignore_ascii_case`: ASCII case
// folding only, so non-ASCII bytes compare exactly.
func proxyDomainByteEqualFold(left byte, right byte) bool {
	if left == right {
		return true
	}
	lowerLeft := left | 0x20
	lowerRight := right | 0x20
	return lowerLeft == lowerRight && lowerLeft >= 'a' && lowerLeft <= 'z'
}

// proxyDomainPatternUsesGlobExtensions reports whether candidate relies on a
// gobwas/glob extension that the byte matcher does not implement.
func proxyDomainPatternUsesGlobExtensions(candidate string) bool {
	return strings.ContainsAny(candidate, "[]{}\\")
}

func CompileProxyDomainMatcher(patterns []string, rejectGlobalWildcard bool) (*ProxyDomainMatcher, error) {
	matcher := &ProxyDomainMatcher{}
	seen := map[string]bool{}
	for _, pattern := range patterns {
		normalized := normalizeProxyConstraintPattern(pattern)
		parsed := ParseProxyDomainPattern(normalized)
		if rejectGlobalWildcard && parsed.Domain == "*" {
			return nil, fmt.Errorf("unsupported global wildcard domain pattern %q; use exact hosts or scoped wildcards like *.example.com or **.example.com", strings.TrimSpace(pattern))
		}
		candidates := []string{parsed.Domain}
		switch parsed.Kind {
		case ProxyPatternSubdomainsOnly:
			candidates = []string{"?*." + parsed.Domain}
		case ProxyPatternApexAndSubdomains:
			candidates = []string{parsed.Domain, "?*." + parsed.Domain}
		}
		for _, candidate := range candidates {
			candidate = strings.ToLower(candidate)
			if seen[candidate] {
				continue
			}
			var compiled glob.Glob
			if proxyDomainPatternUsesGlobExtensions(candidate) {
				globbed, err := glob.Compile(candidate)
				if err != nil {
					return nil, fmt.Errorf("invalid glob pattern %q: %w", candidate, err)
				}
				compiled = globbed
			} else {
				compiled = proxyDomainByteMatcher{pattern: candidate}
			}
			seen[candidate] = true
			matcher.patterns = append(matcher.patterns, compiled)
		}
	}
	return matcher, nil
}

func (m *ProxyDomainMatcher) Match(host string) bool {
	if m == nil {
		return false
	}
	host = strings.ToLower(NormalizeProxyHost(host))
	for _, pattern := range m.patterns {
		if pattern.Match(host) {
			return true
		}
	}
	return false
}

func ParseProxyDomainPattern(input string) ProxyDomainPattern {
	input = strings.TrimSpace(input)
	switch {
	case strings.HasPrefix(input, "**."):
		return ProxyDomainPattern{Kind: ProxyPatternApexAndSubdomains, Domain: normalizeDomain(strings.TrimPrefix(input, "**."))}
	case strings.HasPrefix(input, "*."):
		return ProxyDomainPattern{Kind: ProxyPatternSubdomainsOnly, Domain: normalizeDomain(strings.TrimPrefix(input, "*."))}
	default:
		return ProxyDomainPattern{Kind: ProxyPatternExact, Domain: normalizeDomain(input)}
	}
}

// CanonicalAllowProxyPattern validates an allow selector and returns the
// comparison identity the proxy uses for it (Rust #61351
// network-proxy/src/domain_matcher.rs::canonical_allow_pattern).
//
// A grant keeps the textual spelling of IPv4/IPv6 literals, and a wildcard in a
// stripped port still disqualifies the rule from the local-binding exception, so
// `[::1]:*` and `::1` are different grants.
func CanonicalAllowProxyPattern(pattern string) (string, error) {
	host, err := canonicalProxyDomainPattern(pattern, false)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(pattern, "*?") && !strings.ContainsAny(host, "*?") && isLocalProxyLiteral(host) {
		if strings.Contains(host, ":") {
			return "[" + host + "]:*", nil
		}
		return host + ":*", nil
	}
	return host, nil
}

// CanonicalDenyProxyPattern validates a deny selector, including the global
// wildcard restriction, and compresses unscoped IPv6 literals by address
// (Rust #61351 canonical_deny_pattern).
func CanonicalDenyProxyPattern(pattern string) (string, error) {
	host, err := canonicalProxyDomainPattern(pattern, true)
	if err != nil {
		return "", err
	}
	return restrictiveProxyDomainIdentity(host), nil
}

// CanonicalReviewProxyPattern mirrors CanonicalDenyProxyPattern's identity but
// allows global wildcards like an allow selector (Rust #61351
// canonical_review_pattern).
func CanonicalReviewProxyPattern(pattern string) (string, error) {
	host, err := canonicalProxyDomainPattern(pattern, false)
	if err != nil {
		return "", err
	}
	return restrictiveProxyDomainIdentity(host), nil
}

// restrictiveProxyDomainIdentity compares unscoped IPv6 literals by address.
func restrictiveProxyDomainIdentity(pattern string) string {
	ip := net.ParseIP(pattern)
	if ip == nil || ip.To4() != nil {
		return pattern
	}
	return ip.String()
}

func canonicalProxyDomainPattern(pattern string, rejectGlobalWildcard bool) (string, error) {
	normalized, err := validateProxyDomainPattern(pattern, rejectGlobalWildcard)
	if err != nil {
		return "", err
	}
	// Subdomains-only selectors compile to the single-byte glob `?*.`. Treat the
	// explicitly written glob as the same selector for comparisons.
	if domain, ok := strings.CutPrefix(normalized, "?*."); ok {
		return "*." + domain, nil
	}
	return normalized, nil
}

func validateProxyDomainPattern(pattern string, rejectGlobalWildcard bool) (string, error) {
	if rejectGlobalWildcard && isGlobalWildcardProxyDomainPattern(pattern) {
		return "", fmt.Errorf("unsupported global wildcard domain pattern %q; use exact hosts or scoped wildcards like *.example.com or **.example.com", strings.TrimSpace(pattern))
	}
	raw := strings.TrimSpace(pattern)
	if strings.Contains(raw, "{") || strings.Contains(raw, "}") || strings.Contains(raw, "\\") || strings.Contains(raw, "/") || strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("unsupported domain pattern syntax: %s", pattern)
	}
	host := raw
	if trimmed, ok := strings.CutPrefix(host, "**."); ok {
		host = trimmed
	} else if trimmed, ok := strings.CutPrefix(host, "*."); ok {
		host = trimmed
	}
	if strings.Contains(host, "**") {
		return "", fmt.Errorf("unsupported repeated wildcard: %s", pattern)
	}
	if strings.ContainsAny(host, "[]") && !proxyDomainBracketsSupported(host) {
		return "", fmt.Errorf("brackets are only supported for IPv6 literals or [*]: %s", pattern)
	}
	normalized := normalizeProxyConstraintPattern(pattern)
	switch normalized {
	case "", "*.", "**.":
		return "", fmt.Errorf("domain pattern is empty")
	}
	return normalized, nil
}

func proxyDomainBracketsSupported(host string) bool {
	rest, ok := strings.CutPrefix(host, "[")
	if !ok {
		return false
	}
	ip, suffix, ok := strings.Cut(rest, "]")
	if !ok || strings.ContainsAny(ip, "[]") || strings.ContainsAny(suffix, "[]") {
		return false
	}
	normalized := normalizeDNSHostOrIPLiteral(ip)
	unscoped := normalized
	if before, _, found := strings.Cut(normalized, "%"); found {
		unscoped = before
	}
	if normalized != "*" && !isProxyIPv6Literal(unscoped) {
		return false
	}
	return proxyDomainSuffixAllDots(suffix) || strings.HasPrefix(suffix, ":")
}

func isProxyIPv6Literal(value string) bool {
	return strings.Contains(value, ":") && net.ParseIP(value) != nil
}

func proxyDomainSuffixAllDots(suffix string) bool {
	for _, char := range suffix {
		if char != '.' {
			return false
		}
	}
	return true
}

// isLocalProxyLiteral reports whether host names localhost or a non-public IP
// literal (Rust #61351 policy::is_local_literal).
func isLocalProxyLiteral(host string) bool {
	if host == "localhost" {
		return true
	}
	value := host
	if unscoped := unscopedIPLiteral(host); unscoped != "" {
		value = unscoped
	}
	return IsNonPublicProxyIP(net.ParseIP(value))
}

func isGlobalWildcardProxyDomainPattern(pattern string) bool {
	for _, candidate := range expandProxyDomainPattern(normalizeProxyConstraintPattern(pattern)) {
		if candidate == "*" {
			return true
		}
	}
	return false
}

func expandProxyDomainPattern(pattern string) []string {
	pattern = strings.TrimSpace(pattern)
	if domain, ok := strings.CutPrefix(pattern, "**."); ok {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			return []string{""}
		}
		return []string{domain, "?*." + domain}
	}
	if domain, ok := strings.CutPrefix(pattern, "*."); ok {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			return []string{""}
		}
		return []string{"?*." + domain}
	}
	return []string{pattern}
}

func (p *ProxyDomainPattern) Allows(candidate ProxyDomainPattern) bool {
	if p == nil {
		return false
	}
	switch p.Kind {
	case ProxyPatternExact:
		return candidate.Kind == ProxyPatternExact && domainEqual(candidate.Domain, p.Domain)
	case ProxyPatternSubdomainsOnly:
		switch candidate.Kind {
		case ProxyPatternExact:
			return isStrictSubdomain(candidate.Domain, p.Domain)
		case ProxyPatternSubdomainsOnly:
			return isSubdomainOrEqual(candidate.Domain, p.Domain)
		case ProxyPatternApexAndSubdomains:
			return isStrictSubdomain(candidate.Domain, p.Domain)
		}
	case ProxyPatternApexAndSubdomains:
		return isSubdomainOrEqual(candidate.Domain, p.Domain)
	}
	return false
}

type ProxyProtocol string

const (
	ProxyProtocolHTTP         ProxyProtocol = "http"
	ProxyProtocolHTTPSConnect ProxyProtocol = "https_connect"
	ProxyProtocolSocks5TCP    ProxyProtocol = "socks5_tcp"
	ProxyProtocolSocks5UDP    ProxyProtocol = "socks5_udp"
)

type ProxyPolicyDecision string

const (
	ProxyPolicyDecisionDeny ProxyPolicyDecision = "deny"
	ProxyPolicyDecisionAsk  ProxyPolicyDecision = "ask"
)

type ProxyDecisionSource string

const (
	ProxyDecisionSourceBaselinePolicy ProxyDecisionSource = "baseline_policy"
	ProxyDecisionSourceModeGuard      ProxyDecisionSource = "mode_guard"
	ProxyDecisionSourceProxyState     ProxyDecisionSource = "proxy_state"
	ProxyDecisionSourceDecider        ProxyDecisionSource = "decider"
)

type ProxyDecision struct {
	Allow    bool
	Reason   string
	Source   ProxyDecisionSource
	Decision ProxyPolicyDecision
}

type ProxyPolicyRequest struct {
	Protocol      ProxyProtocol
	Host          string
	Port          uint16
	EnvironmentID string
	ExecutionID   string
	ClientAddr    string
	Method        string
}

type ProxyPolicyDecider interface {
	Decide(context.Context, ProxyPolicyRequest) ProxyDecision
}

type ProxyPolicyDeciderFunc func(context.Context, ProxyPolicyRequest) ProxyDecision

func (f ProxyPolicyDeciderFunc) Decide(ctx context.Context, request ProxyPolicyRequest) ProxyDecision {
	return f(ctx, request)
}

type ProxyPolicyAuditEvent struct {
	Request        ProxyPolicyRequest
	Timestamp      string
	Scope          string
	Method         string
	Client         string
	Decision       string
	Source         ProxyDecisionSource
	Reason         string
	PolicyOverride bool
	Metadata       ProxyAuditMetadata
}

type ProxyPolicyAuditSink func(ProxyPolicyAuditEvent)

type ProxyBlockedRequest struct {
	Host      string              `json:"host"`
	Reason    string              `json:"reason"`
	Client    string              `json:"client,omitempty"`
	Method    string              `json:"method,omitempty"`
	Mode      *ProxyMode          `json:"mode,omitempty"`
	Protocol  string              `json:"protocol"`
	Decision  string              `json:"decision,omitempty"`
	Source    ProxyDecisionSource `json:"source,omitempty"`
	Port      *uint16             `json:"port,omitempty"`
	Timestamp int64               `json:"timestamp"`
}

type ProxyBlockedRequestObserver interface {
	OnBlockedRequest(context.Context, ProxyBlockedRequest)
}

type ProxyBlockedRequestObserverFunc func(context.Context, ProxyBlockedRequest)

func (f ProxyBlockedRequestObserverFunc) OnBlockedRequest(ctx context.Context, request ProxyBlockedRequest) {
	if f != nil {
		f(ctx, request)
	}
}

type ProxyAuditMetadata struct {
	ConversationID string
	AppVersion     string
	UserAccountID  string
	AuthMode       string
	Originator     string
	UserEmail      string
	TerminalType   string
	Model          string
	Slug           string
	// ProcessLog carries launch-local attribution for executor-managed
	// processes. These values are telemetry only and never authorize a request.
	ProcessLog ProxyProcessLogMetadata
}

// ProxyProcessLogMetadata mirrors Rust #42373's process log attribution.
type ProxyProcessLogMetadata struct {
	ThreadID               string
	ToolCallID             string
	ExecutorEnvironmentID  string
	ExecutorRegistrationID string
	LaunchTraceID          string
	LaunchSpanID           string
}

type ProxyAuditMetadataProvider func(ProxyPolicyRequest) ProxyAuditMetadata

func AllowProxyDecision() ProxyDecision {
	return ProxyDecision{Allow: true}
}

func DenyProxyDecision(reason string) ProxyDecision {
	return DenyProxyDecisionWithSource(reason, ProxyDecisionSourceDecider)
}

func AskProxyDecision(reason string) ProxyDecision {
	return AskProxyDecisionWithSource(reason, ProxyDecisionSourceDecider)
}

func DenyProxyDecisionWithSource(reason string, source ProxyDecisionSource) ProxyDecision {
	if reason == "" {
		reason = ProxyReasonPolicyDenied
	}
	return ProxyDecision{Reason: reason, Source: source, Decision: ProxyPolicyDecisionDeny}
}

func AskProxyDecisionWithSource(reason string, source ProxyDecisionSource) ProxyDecision {
	if reason == "" {
		reason = ProxyReasonPolicyDenied
	}
	return ProxyDecision{Reason: reason, Source: source, Decision: ProxyPolicyDecisionAsk}
}

type ProxyParseError struct {
	Message string
}

func (e *ProxyParseError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func normalizeDNSHostOrIPLiteral(host string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if before, after, ok := strings.Cut(host, "%25"); ok && net.ParseIP(before) != nil {
		return before + "%" + after
	}
	return host
}

func unscopedIPLiteral(host string) string {
	before, _, ok := strings.Cut(host, "%")
	if ok && net.ParseIP(before) != nil {
		return before
	}
	return ""
}

func normalizeDomain(domain string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
}

func domainEqual(left string, right string) bool {
	return normalizeDomain(left) == normalizeDomain(right)
}

func isSubdomainOrEqual(child string, parent string) bool {
	child = normalizeDomain(child)
	parent = normalizeDomain(parent)
	return child == parent || strings.HasSuffix(child, "."+parent)
}

func isStrictSubdomain(child string, parent string) bool {
	child = normalizeDomain(child)
	parent = normalizeDomain(parent)
	return child != parent && strings.HasSuffix(child, "."+parent)
}

func ipv4InCIDR(ip net.IP, base [4]byte, prefix uint) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	mask := uint32(0xffffffff) << (32 - prefix)
	ipNum := uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
	baseNum := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	return ipNum&mask == baseNum&mask
}
