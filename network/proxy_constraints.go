package network

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ProxyRequirements is the transport-neutral form of managed network constraints.
type ProxyRequirements struct {
	Enabled                          *bool
	HTTPPort                         *uint16
	SOCKSPort                        *uint16
	AllowUpstreamProxy               *bool
	DangerouslyAllowNonLoopbackProxy *bool
	DangerouslyAllowAllUnixSockets   *bool
	Domains                          *ProxyDomainPermissions
	ManagedAllowedDomainsOnly        bool
	UnixSockets                      *ProxyUnixSocketPermissions
	AllowLocalBinding                *bool
	HeaderInjections                 []ProxyHeaderInjection
}

// ProxyHeaderInjection annotates matching requests with extra headers. It is a
// requirements-only rule and does not change whether non-matching requests are
// allowed (Rust #42173).
type ProxyHeaderInjection struct {
	Host         string
	Methods      []string
	PathPrefixes []string
	Headers      map[string]string
}

// ApplyProxyHeaderInjections annotates a matching outbound request with the
// configured header values (Rust #42173). Host matching is ASCII case
// insensitive; method and path-prefix filters only constrain when non-empty.
func ApplyProxyHeaderInjections(request *http.Request, injections []ProxyHeaderInjection) {
	if request == nil || request.URL == nil || len(injections) == 0 {
		return
	}
	host := request.URL.Hostname()
	method := request.Method
	path := request.URL.Path
	for _, injection := range injections {
		if injection.Host != "" && !strings.EqualFold(injection.Host, host) {
			continue
		}
		if len(injection.Methods) > 0 && !containsFold(injection.Methods, method) {
			continue
		}
		if len(injection.PathPrefixes) > 0 && !hasPathPrefix(path, injection.PathPrefixes) {
			continue
		}
		for name, value := range injection.Headers {
			request.Header.Set(name, value)
		}
	}
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func hasPathPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

type ProxyConstraints struct {
	Enabled                          *bool
	Mode                             *ProxyMode
	AllowUpstreamProxy               *bool
	DangerouslyAllowNonLoopbackProxy *bool
	DangerouslyAllowAllUnixSockets   *bool
	AllowedDomains                   *[]string
	AllowlistExpansionEnabled        *bool
	DeniedDomains                    *[]string
	DenylistExpansionEnabled         *bool
	AllowUnixSockets                 *[]string
	AllowLocalBinding                *bool
	HeaderInjections                 []ProxyHeaderInjection
}

type ProxyNetworkRule struct {
	Host       string
	Permission ProxyDomainPermission
}

type ProxySpec struct {
	config                  ProxyConfig
	constraints             ProxyConstraints
	hardDenyAllowlistMisses bool
}

// HeaderInjections returns the resolved requirements-only header injection
// rules, preserving a defensive copy.
func (s *ProxySpec) HeaderInjections() []ProxyHeaderInjection {
	if s == nil {
		return nil
	}
	return cloneProxyHeaderInjections(s.constraints.HeaderInjections)
}

func NewProxySpec(config ProxyConfig, requirements *ProxyRequirements, managedSandboxActive bool) (*ProxySpec, error) {
	config = cloneProxyConfig(config)
	constraints := ProxyConstraints{}
	hardDeny := requirements != nil && requirements.ManagedAllowedDomainsOnly
	if requirements != nil {
		config, constraints = applyProxyRequirements(config, requirements, managedSandboxActive, hardDeny)
	}
	if err := ValidateProxyPolicyAgainstConstraints(config, constraints); err != nil {
		return nil, fmt.Errorf("network proxy constraints are invalid: %w", err)
	}
	return &ProxySpec{config: config, constraints: constraints, hardDenyAllowlistMisses: hardDeny}, nil
}

func (s *ProxySpec) Config() ProxyConfig {
	if s == nil {
		return ProxyConfig{}
	}
	return cloneProxyConfig(s.config)
}

func (s *ProxySpec) HardDenyAllowlistMisses() bool {
	return s != nil && s.hardDenyAllowlistMisses
}

func (s *ProxySpec) WithNetworkRules(rules []ProxyNetworkRule) (*ProxySpec, error) {
	if s == nil {
		return nil, fmt.Errorf("nil network proxy spec")
	}
	next := *s
	next.config = cloneProxyConfig(s.config)
	for _, rule := range rules {
		switch rule.Permission {
		case ProxyDomainAllow, ProxyDomainDeny:
			next.config.Network.UpsertDomainPermission(rule.Host, rule.Permission, NormalizeProxyHost)
		}
	}
	if err := ValidateProxyPolicyAgainstConstraints(next.config, next.constraints); err != nil {
		return nil, fmt.Errorf("network proxy constraints are invalid: %w", err)
	}
	return &next, nil
}

func applyProxyRequirements(config ProxyConfig, requirements *ProxyRequirements, managedSandboxActive bool, hardDeny bool) (ProxyConfig, ProxyConstraints) {
	constraints := ProxyConstraints{}
	allowExpansion := managedSandboxActive && !hardDeny
	denyExpansion := managedSandboxActive
	settings := &config.Network

	if requirements.Enabled != nil {
		settings.Enabled = *requirements.Enabled
		constraints.Enabled = cloneBool(requirements.Enabled)
	}
	if requirements.HTTPPort != nil {
		settings.ProxyURL = fmt.Sprintf("http://127.0.0.1:%d", *requirements.HTTPPort)
	}
	if requirements.SOCKSPort != nil {
		settings.SocksURL = fmt.Sprintf("http://127.0.0.1:%d", *requirements.SOCKSPort)
	}
	if requirements.AllowUpstreamProxy != nil {
		settings.AllowUpstreamProxy = *requirements.AllowUpstreamProxy
		constraints.AllowUpstreamProxy = cloneBool(requirements.AllowUpstreamProxy)
	}
	if requirements.DangerouslyAllowNonLoopbackProxy != nil {
		settings.DangerouslyAllowNonLoopbackProxy = *requirements.DangerouslyAllowNonLoopbackProxy
		constraints.DangerouslyAllowNonLoopbackProxy = cloneBool(requirements.DangerouslyAllowNonLoopbackProxy)
	}
	if requirements.DangerouslyAllowAllUnixSockets != nil {
		settings.DangerouslyAllowAllUnixSockets = *requirements.DangerouslyAllowAllUnixSockets
		constraints.DangerouslyAllowAllUnixSockets = cloneBool(requirements.DangerouslyAllowAllUnixSockets)
	}

	managedAllowed, hasManagedAllowed := proxyRequirementDomains(requirements.Domains, ProxyDomainAllow)
	if hardDeny {
		hasManagedAllowed = true
	}
	if hasManagedAllowed {
		effective := managedAllowed
		if allowExpansion {
			effective = mergeProxyDomainLists(managedAllowed, settings.AllowedDomains())
		}
		settings.SetAllowedDomains(effective)
		constraints.AllowedDomains = cloneStringSlicePtr(managedAllowed)
		constraints.AllowlistExpansionEnabled = boolPtr(allowExpansion)
	}

	managedDenied, hasManagedDenied := proxyRequirementDomains(requirements.Domains, ProxyDomainDeny)
	if hasManagedDenied {
		effective := managedDenied
		if denyExpansion {
			effective = mergeProxyDomainLists(managedDenied, settings.DeniedDomains())
		}
		settings.SetDeniedDomains(effective)
		constraints.DeniedDomains = cloneStringSlicePtr(managedDenied)
		constraints.DenylistExpansionEnabled = boolPtr(denyExpansion)
	}

	if requirements.UnixSockets != nil {
		allowed := proxyRequirementUnixSockets(requirements.UnixSockets)
		settings.SetAllowUnixSockets(allowed)
		constraints.AllowUnixSockets = cloneStringSlicePtr(allowed)
	}
	if requirements.AllowLocalBinding != nil {
		settings.AllowLocalBinding = *requirements.AllowLocalBinding
		constraints.AllowLocalBinding = cloneBool(requirements.AllowLocalBinding)
	}
	constraints.HeaderInjections = cloneProxyHeaderInjections(requirements.HeaderInjections)
	return config, constraints
}

func cloneProxyHeaderInjections(values []ProxyHeaderInjection) []ProxyHeaderInjection {
	if values == nil {
		return nil
	}
	out := make([]ProxyHeaderInjection, len(values))
	for i, value := range values {
		headers := map[string]string{}
		for name, header := range value.Headers {
			headers[name] = header
		}
		out[i] = ProxyHeaderInjection{
			Host:         value.Host,
			Methods:      append([]string(nil), value.Methods...),
			PathPrefixes: append([]string(nil), value.PathPrefixes...),
			Headers:      headers,
		}
	}
	return out
}

func ValidateProxyPolicyAgainstConstraints(config ProxyConfig, constraints ProxyConstraints) error {
	settings := config.Network
	if err := ValidateProxyMITMHookConfig(config); err != nil {
		return invalidProxyConstraint("network.mitm_hooks", err.Error(), "valid MITM hook configuration")
	}
	if err := validateNonGlobalProxyPatterns("network.denied_domains", settings.DeniedDomains()); err != nil {
		return err
	}
	if constraints.Enabled != nil && settings.Enabled && !*constraints.Enabled {
		return invalidProxyConstraint("network.enabled", "true", "false (disabled by managed config)")
	}
	if constraints.Mode != nil && proxyModeRank(settings.Mode) > proxyModeRank(*constraints.Mode) {
		return invalidProxyConstraint("network.mode", string(settings.Mode), string(*constraints.Mode)+" or more restrictive")
	}
	if constraints.AllowUpstreamProxy != nil && settings.AllowUpstreamProxy && !*constraints.AllowUpstreamProxy {
		return invalidProxyConstraint("network.allow_upstream_proxy", "true", "false (disabled by managed config)")
	}
	if constraints.DangerouslyAllowNonLoopbackProxy != nil && settings.DangerouslyAllowNonLoopbackProxy && !*constraints.DangerouslyAllowNonLoopbackProxy {
		return invalidProxyConstraint("network.dangerously_allow_non_loopback_proxy", "true", "false (disabled by managed config)")
	}
	allowAllUnixSockets := constraints.AllowUnixSockets == nil
	if constraints.DangerouslyAllowAllUnixSockets != nil {
		allowAllUnixSockets = *constraints.DangerouslyAllowAllUnixSockets
	}
	if settings.DangerouslyAllowAllUnixSockets && !allowAllUnixSockets {
		return invalidProxyConstraint("network.dangerously_allow_all_unix_sockets", "true", "false (disabled by managed config)")
	}
	if constraints.AllowLocalBinding != nil && settings.AllowLocalBinding && !*constraints.AllowLocalBinding {
		return invalidProxyConstraint("network.allow_local_binding", "true", "false (disabled by managed config)")
	}

	deniedOverrides := lowerStringSet(settings.DeniedDomains())
	if constraints.AllowedDomains != nil {
		managed := append([]string(nil), (*constraints.AllowedDomains)...)
		if err := validateNonGlobalProxyPatterns("network.allowed_domains", managed); err != nil {
			return err
		}
		candidate := settings.AllowedDomains()
		switch {
		case constraints.AllowlistExpansionEnabled != nil && *constraints.AllowlistExpansionEnabled:
			candidateSet := lowerStringSet(candidate)
			for _, entry := range managed {
				key := strings.ToLower(entry)
				if !candidateSet[key] && !deniedOverrides[key] {
					return invalidProxyConstraint("network.allowed_domains", "missing managed allowed_domains entries", entry)
				}
			}
		case constraints.AllowlistExpansionEnabled != nil:
			expected := lowerStringSet(managed)
			for denied := range deniedOverrides {
				delete(expected, denied)
			}
			if !equalStringSets(lowerStringSet(candidate), expected) {
				return invalidProxyConstraint("network.allowed_domains", fmt.Sprintf("%v", candidate), "must match managed allowed_domains")
			}
		default:
			managedPatterns := make([]ProxyDomainPattern, 0, len(managed))
			for _, entry := range managed {
				managedPatterns = append(managedPatterns, ParseProxyDomainPattern(normalizeProxyConstraintPattern(entry)))
			}
			for _, entry := range candidate {
				candidatePattern := ParseProxyDomainPattern(normalizeProxyConstraintPattern(entry))
				allowed := false
				for i := range managedPatterns {
					if (&managedPatterns[i]).Allows(candidatePattern) {
						allowed = true
						break
					}
				}
				if !allowed {
					return invalidProxyConstraint("network.allowed_domains", entry, "subset of managed allowed_domains")
				}
			}
		}
	}

	if constraints.DeniedDomains != nil {
		managed := append([]string(nil), (*constraints.DeniedDomains)...)
		if err := validateNonGlobalProxyPatterns("network.denied_domains", managed); err != nil {
			return err
		}
		managedSet := lowerStringSet(managed)
		candidateSet := lowerStringSet(settings.DeniedDomains())
		if constraints.DenylistExpansionEnabled != nil && !*constraints.DenylistExpansionEnabled {
			if !equalStringSets(candidateSet, managedSet) {
				return invalidProxyConstraint("network.denied_domains", fmt.Sprintf("%v", settings.DeniedDomains()), "must match managed denied_domains")
			}
		} else {
			for entry := range managedSet {
				if !candidateSet[entry] {
					return invalidProxyConstraint("network.denied_domains", "missing managed denied_domains entries", entry)
				}
			}
		}
	}

	if constraints.AllowUnixSockets != nil {
		allowed := lowerStringSet(*constraints.AllowUnixSockets)
		for _, candidate := range settings.AllowUnixSockets() {
			if !allowed[strings.ToLower(candidate)] {
				return invalidProxyConstraint("network.allow_unix_sockets", candidate, "subset of managed allow_unix_sockets")
			}
		}
	}
	return nil
}

func proxyRequirementDomains(domains *ProxyDomainPermissions, permission ProxyDomainPermission) ([]string, bool) {
	if domains == nil {
		return nil, false
	}
	out := make([]string, 0, len(domains.Entries))
	for _, entry := range domains.EffectiveEntries() {
		if entry.Permission == permission {
			out = append(out, entry.Pattern)
		}
	}
	return out, len(out) > 0
}

func proxyRequirementUnixSockets(sockets *ProxyUnixSocketPermissions) []string {
	if sockets == nil {
		return nil
	}
	out := make([]string, 0, len(sockets.Entries))
	for path, permission := range sockets.Entries {
		if permission == ProxyUnixSocketAllow {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func mergeProxyDomainLists(managed []string, user []string) []string {
	out := append([]string(nil), managed...)
	seen := lowerStringSet(managed)
	for _, entry := range user {
		key := strings.ToLower(entry)
		if !seen[key] {
			seen[key] = true
			out = append(out, entry)
		}
	}
	return out
}

func validateNonGlobalProxyPatterns(field string, patterns []string) error {
	for _, pattern := range patterns {
		normalized := normalizeProxyConstraintPattern(pattern)
		parsed := ParseProxyDomainPattern(normalized)
		if parsed.Domain == "*" {
			return invalidProxyConstraint(field, strings.TrimSpace(pattern), "exact hosts or scoped wildcards like *.example.com or **.example.com")
		}
	}
	return nil
}

func normalizeProxyConstraintPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	prefix := ""
	remainder := pattern
	if strings.HasPrefix(remainder, "**.") {
		prefix, remainder = "**.", strings.TrimPrefix(remainder, "**.")
	} else if strings.HasPrefix(remainder, "*.") {
		prefix, remainder = "*.", strings.TrimPrefix(remainder, "*.")
	}
	return prefix + NormalizeProxyHost(remainder)
}

func invalidProxyConstraint(field string, candidate string, allowed string) error {
	return fmt.Errorf("invalid value for %s: %s (allowed %s)", field, candidate, allowed)
}

func proxyModeRank(mode ProxyMode) int {
	if mode == ProxyModeFull {
		return 1
	}
	return 0
}

func lowerStringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[strings.ToLower(value)] = true
	}
	return out
}

func equalStringSets(left map[string]bool, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if !right[value] {
			return false
		}
	}
	return true
}

func cloneProxyConfig(config ProxyConfig) ProxyConfig {
	clone := config
	clone.Network = cloneProxySettings(config.Network)
	return clone
}

func cloneProxySettings(settings ProxySettings) ProxySettings {
	clone := settings
	if settings.Domains != nil {
		clone.Domains = &ProxyDomainPermissions{Entries: append([]ProxyDomainPermissionEntry(nil), settings.Domains.Entries...)}
	}
	if settings.UnixSockets != nil {
		entries := make(map[string]ProxyUnixSocketPermission, len(settings.UnixSockets.Entries))
		for path, permission := range settings.UnixSockets.Entries {
			entries[path] = permission
		}
		clone.UnixSockets = &ProxyUnixSocketPermissions{Entries: entries}
	}
	clone.MITMHooks = append([]ProxyMITMHookConfig(nil), settings.MITMHooks...)
	return clone
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func boolPtr(value bool) *bool {
	return &value
}

func cloneStringSlicePtr(values []string) *[]string {
	clone := append([]string(nil), values...)
	return &clone
}
