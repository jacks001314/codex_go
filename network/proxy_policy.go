package network

import (
	"context"
	"fmt"
	"net"
	"strings"

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

type ProxyDomainMatcher struct {
	patterns []glob.Glob
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
			compiled, err := glob.Compile(candidate)
			if err != nil {
				return nil, fmt.Errorf("invalid glob pattern %q: %w", candidate, err)
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
