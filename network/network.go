package network

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Mode string

const (
	ModeLimited Mode = "limited"
	ModeFull    Mode = "full"
)

type DomainPermission string

const (
	DomainAllow DomainPermission = "allow"
	DomainDeny  DomainPermission = "deny"
)

type Config struct {
	Enabled                          bool
	Mode                             Mode
	ProxyURL                         string
	SocksURL                         string
	EnableSocks5                     bool
	AllowUpstreamProxy               bool
	DangerouslyAllowNonLoopbackProxy bool
	DangerouslyAllowAllUnixSockets   bool
	AllowLocalBinding                bool
	Domains                          map[string]DomainPermission
	UnixSockets                      map[string]UnixSocketPermission
	MITM                             bool
	MITMHooks                        []MITMHook
}

type UnixSocketPermission string

const (
	UnixSocketAllow UnixSocketPermission = "allow"
	UnixSocketDeny  UnixSocketPermission = "deny"
)

type MITMHook struct {
	Host         string
	Methods      []string
	PathPrefixes []string
	Actions      MITMActions
}

type MITMActions struct {
	StripRequestHeaders []string
}

type Constraints struct {
	Enabled                          *bool
	Mode                             *Mode
	AllowUpstreamProxy               *bool
	DangerouslyAllowNonLoopbackProxy *bool
	DangerouslyAllowAllUnixSockets   *bool
	AllowLocalBinding                *bool
	AllowedDomains                   []string
	DeniedDomains                    []string
	AllowlistExpansionEnabled        *bool
	DenylistExpansionEnabled         *bool
}

type Requirements struct {
	Enabled                          *bool
	HTTPPort                         int
	SocksPort                        int
	AllowUpstreamProxy               *bool
	DangerouslyAllowNonLoopbackProxy *bool
	DangerouslyAllowAllUnixSockets   *bool
	AllowLocalBinding                *bool
	AllowedDomains                   []string
	DeniedDomains                    []string
	ManagedAllowedDomainsOnly        bool
}

type PermissionProfileKind string

const (
	PermissionManaged  PermissionProfileKind = "managed"
	PermissionDisabled PermissionProfileKind = "disabled"
	PermissionExternal PermissionProfileKind = "external"
)

type Spec struct {
	baseConfig              Config
	requirements            *Requirements
	config                  Config
	constraints             Constraints
	hardDenyAllowlistMisses bool
}

type LayerMTime struct {
	Path  string
	MTime time.Time
}

type MtimeReloader struct {
	layers []LayerMTime
}

func DefaultConfig() Config {
	return Config{
		Enabled:  true,
		Mode:     ModeLimited,
		ProxyURL: "http://127.0.0.1:3128",
	}
}

func NewSpec(config Config, requirements *Requirements, permissionKind PermissionProfileKind) (*Spec, error) {
	base := cloneConfig(&config)
	hardDeny := requirements != nil && requirements.ManagedAllowedDomainsOnly
	constraints := Constraints{}
	if requirements != nil {
		config, constraints = applyRequirements(config, requirements, permissionKind, hardDeny)
	}
	if err := ValidateAgainstConstraints(&config, &constraints); err != nil {
		return nil, err
	}
	return &Spec{
		baseConfig:              base,
		requirements:            cloneRequirements(requirements),
		config:                  config,
		constraints:             constraints,
		hardDenyAllowlistMisses: hardDeny,
	}, nil
}

// EnvironmentNetworkPolicy is the attachment/owner-provided traffic policy for
// one execution environment (#39980). Proxy enablement, listeners, network
// mode, MITM, and credentials remain outside attachment-owned traffic policy.
type EnvironmentNetworkPolicy struct {
	Domains                        map[string]DomainPermission
	UnixSockets                    map[string]UnixSocketPermission
	AllowUpstreamProxy             bool
	DangerouslyAllowAllUnixSockets bool
	AllowLocalBinding              bool
	ManagedAllowedDomainsOnly      bool
}

// EnvironmentNetworkPolicyFromConfig captures portable traffic restrictions
// without exposing controller runtime settings (#39980).
func EnvironmentNetworkPolicyFromConfig(config *Config, managedAllowedDomainsOnly bool) EnvironmentNetworkPolicy {
	return EnvironmentNetworkPolicy{
		Domains:                        copyDomainMapArray(config.Domains),
		UnixSockets:                    copyUnixSocketMap(config.UnixSockets),
		AllowUpstreamProxy:             config.AllowUpstreamProxy,
		DangerouslyAllowAllUnixSockets: config.DangerouslyAllowAllUnixSockets,
		AllowLocalBinding:              config.AllowLocalBinding,
		ManagedAllowedDomainsOnly:      managedAllowedDomainsOnly,
	}
}

// ApplyTo applies attachment-owned traffic settings while preserving inherited
// denials and proxy setup (#39980).
func (p *EnvironmentNetworkPolicy) ApplyTo(config *Config) {
	if p == nil || config == nil {
		return
	}
	inheritedDenials := deniedDomainMap(config)
	config.Domains = copyDomainMapArray(p.Domains)
	for host := range inheritedDenials {
		config.Domains[NormalizeHost(host)] = DomainDeny
	}
	inheritedSockets := config.UnixSockets
	config.UnixSockets = copyUnixSocketMap(p.UnixSockets)
	inheritedPermitsAll := config.DangerouslyAllowAllUnixSockets && !hasUnixSocketDeny(inheritedSockets)
	ownerPermitsAll := p.DangerouslyAllowAllUnixSockets && !hasUnixSocketDeny(p.UnixSockets)
	effective := copyUnixSocketMap(p.UnixSockets)
	for path, permission := range inheritedSockets {
		if permission == UnixSocketDeny || ownerPermitsAll {
			if effective == nil {
				effective = map[string]UnixSocketPermission{}
			}
			effective[path] = permission
		}
	}
	if inheritedPermitsAll {
		for path, permission := range inheritedSockets {
			if permission == UnixSocketAllow {
				if effective == nil {
					effective = map[string]UnixSocketPermission{}
				}
				if _, ok := effective[path]; !ok {
					effective[path] = permission
				}
			}
		}
	}
	config.UnixSockets = effective
	config.DangerouslyAllowAllUnixSockets = inheritedPermitsAll && ownerPermitsAll
	if p.AllowUpstreamProxy {
		config.AllowUpstreamProxy = true
	}
	if p.AllowLocalBinding {
		config.AllowLocalBinding = true
	}
}

// NewSpecForEnvironment resolves a remote environment's network policy for a
// selected command and applies it to the execution-scoped proxy (#39980).
func NewSpecForEnvironment(config Config, requirements *Requirements, permissionKind PermissionProfileKind, policy *EnvironmentNetworkPolicy, execRules []NetworkRule) (*Spec, error) {
	if permissionKind == PermissionDisabled {
		return nil, fmt.Errorf("environment network policy requires managed network enforcement")
	}
	spec, err := NewSpec(config, requirements, permissionKind)
	if err != nil {
		return nil, err
	}
	if policy != nil {
		next := *spec
		next.config = cloneConfig(&spec.config)
		policy.ApplyTo(&next.config)
		next.hardDenyAllowlistMisses = spec.hardDenyAllowlistMisses || policy.ManagedAllowedDomainsOnly || !managedSandboxActive(permissionKind)
		spec = &next
	}
	if len(execRules) > 0 {
		return spec.WithExecPolicyNetworkRules(execRules)
	}
	return spec, nil
}

func managedSandboxActive(permissionKind PermissionProfileKind) bool {
	return permissionKind == PermissionManaged || permissionKind == PermissionExternal
}

func copyDomainMapArray(values map[string]DomainPermission) map[string]DomainPermission {
	if values == nil {
		return nil
	}
	out := make(map[string]DomainPermission, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func copyUnixSocketMap(values map[string]UnixSocketPermission) map[string]UnixSocketPermission {
	if values == nil {
		return nil
	}
	out := make(map[string]UnixSocketPermission, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func deniedDomainMap(config *Config) map[string]struct{} {
	denials := map[string]struct{}{}
	if config == nil {
		return denials
	}
	for host, permission := range config.Domains {
		if permission == DomainDeny {
			denials[host] = struct{}{}
		}
	}
	return denials
}

func hasUnixSocketDeny(values map[string]UnixSocketPermission) bool {
	for _, permission := range values {
		if permission == UnixSocketDeny {
			return true
		}
	}
	return false
}

func (s *Spec) Enabled() bool {
	return s != nil && s.config.Enabled
}

func (s *Spec) ProxyHostAndPort() string {
	if s == nil {
		return ""
	}
	return HostAndPortFromNetworkAddr(s.config.ProxyURL, 3128)
}

func (s *Spec) SocksEnabled() bool {
	return s != nil && s.config.EnableSocks5
}

func (s *Spec) Config() Config {
	if s == nil {
		return Config{}
	}
	return cloneConfig(&s.config)
}

func (s *Spec) RecomputeForPermissionProfile(permissionKind PermissionProfileKind) (*Spec, error) {
	if s == nil {
		return nil, fmt.Errorf("nil network spec")
	}
	return NewSpec(s.baseConfig, s.requirements, permissionKind)
}

func (s *Spec) WithExecPolicyNetworkRules(rules []NetworkRule) (*Spec, error) {
	if s == nil {
		return nil, fmt.Errorf("nil network spec")
	}
	next := *s
	next.config = cloneConfig(&s.config)
	ApplyExecPolicyNetworkRules(&next.config, rules)
	if err := ValidateAgainstConstraints(&next.config, &next.constraints); err != nil {
		return nil, err
	}
	return &next, nil
}

func (s *Spec) HardDenyAllowlistMisses() bool {
	return s != nil && s.hardDenyAllowlistMisses
}

type NetworkRule struct {
	Host     string
	Decision DomainPermission
}

func ApplyExecPolicyNetworkRules(config *Config, rules []NetworkRule) {
	if config.Domains == nil {
		config.Domains = map[string]DomainPermission{}
	}
	for _, rule := range rules {
		host := NormalizeHost(rule.Host)
		if host == "" {
			continue
		}
		config.Domains[host] = rule.Decision
	}
}

func ValidateAgainstConstraints(config *Config, constraints *Constraints) error {
	if config == nil || constraints == nil {
		return nil
	}
	if constraints.Enabled != nil && config.Enabled != *constraints.Enabled {
		return fmt.Errorf("network enabled violates constraints")
	}
	if constraints.Mode != nil && config.Mode != *constraints.Mode {
		return fmt.Errorf("network mode violates constraints")
	}
	if constraints.AllowUpstreamProxy != nil && config.AllowUpstreamProxy != *constraints.AllowUpstreamProxy {
		return fmt.Errorf("allow_upstream_proxy violates constraints")
	}
	if constraints.DangerouslyAllowNonLoopbackProxy != nil && config.DangerouslyAllowNonLoopbackProxy != *constraints.DangerouslyAllowNonLoopbackProxy {
		return fmt.Errorf("dangerously_allow_non_loopback_proxy violates constraints")
	}
	if constraints.DangerouslyAllowAllUnixSockets != nil && config.DangerouslyAllowAllUnixSockets != *constraints.DangerouslyAllowAllUnixSockets {
		return fmt.Errorf("dangerously_allow_all_unix_sockets violates constraints")
	}
	if constraints.AllowLocalBinding != nil && config.AllowLocalBinding != *constraints.AllowLocalBinding {
		return fmt.Errorf("allow_local_binding violates constraints")
	}
	if constraints.AllowlistExpansionEnabled != nil && !*constraints.AllowlistExpansionEnabled {
		for host, permission := range config.Domains {
			if permission == DomainAllow && !containsString(constraints.AllowedDomains, host) {
				return fmt.Errorf("allowed domain %s violates constraints", host)
			}
		}
	}
	if constraints.DenylistExpansionEnabled != nil && !*constraints.DenylistExpansionEnabled {
		for host, permission := range config.Domains {
			if permission == DomainDeny && !containsString(constraints.DeniedDomains, host) {
				return fmt.Errorf("denied domain %s violates constraints", host)
			}
		}
	}
	return nil
}

func HostAndPortFromNetworkAddr(addr string, defaultPort int) string {
	parsed, err := url.Parse(addr)
	if err != nil || parsed.Host == "" {
		return fmt.Sprintf("127.0.0.1:%d", defaultPort)
	}
	if _, _, err := net.SplitHostPort(parsed.Host); err == nil {
		return parsed.Host
	}
	if strings.Contains(parsed.Host, ":") && strings.HasPrefix(parsed.Host, "[") {
		return parsed.Host
	}
	return fmt.Sprintf("%s:%d", parsed.Hostname(), defaultPort)
}

func NormalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	return host
}

func NewMtimeReloader(layers []LayerMTime) *MtimeReloader {
	return &MtimeReloader{layers: append([]LayerMTime(nil), layers...)}
}

func (r *MtimeReloader) SourceLabel() string {
	return "MtimeConfigReloader"
}

func (r *MtimeReloader) Changed(current []LayerMTime) bool {
	if r == nil {
		return false
	}
	if len(r.layers) != len(current) {
		return true
	}
	before := append([]LayerMTime(nil), r.layers...)
	after := append([]LayerMTime(nil), current...)
	sort.Slice(before, func(i int, j int) bool { return before[i].Path < before[j].Path })
	sort.Slice(after, func(i int, j int) bool { return after[i].Path < after[j].Path })
	for i := range before {
		if before[i] != after[i] {
			return true
		}
	}
	return false
}

func applyRequirements(config Config, requirements *Requirements, permissionKind PermissionProfileKind, hardDeny bool) (Config, Constraints) {
	constraints := Constraints{}
	allowExpansion := allowlistExpansionEnabled(permissionKind, hardDeny)
	denyExpansion := denylistExpansionEnabled(permissionKind)
	if requirements.Enabled != nil {
		config.Enabled = *requirements.Enabled
		constraints.Enabled = requirements.Enabled
	}
	if requirements.HTTPPort > 0 {
		config.ProxyURL = fmt.Sprintf("http://127.0.0.1:%d", requirements.HTTPPort)
	}
	if requirements.SocksPort > 0 {
		config.SocksURL = fmt.Sprintf("http://127.0.0.1:%d", requirements.SocksPort)
	}
	if requirements.AllowUpstreamProxy != nil {
		config.AllowUpstreamProxy = *requirements.AllowUpstreamProxy
		constraints.AllowUpstreamProxy = requirements.AllowUpstreamProxy
	}
	if requirements.DangerouslyAllowNonLoopbackProxy != nil {
		config.DangerouslyAllowNonLoopbackProxy = *requirements.DangerouslyAllowNonLoopbackProxy
		constraints.DangerouslyAllowNonLoopbackProxy = requirements.DangerouslyAllowNonLoopbackProxy
	}
	if requirements.DangerouslyAllowAllUnixSockets != nil {
		config.DangerouslyAllowAllUnixSockets = *requirements.DangerouslyAllowAllUnixSockets
		constraints.DangerouslyAllowAllUnixSockets = requirements.DangerouslyAllowAllUnixSockets
	}
	if requirements.AllowLocalBinding != nil {
		config.AllowLocalBinding = *requirements.AllowLocalBinding
		constraints.AllowLocalBinding = requirements.AllowLocalBinding
	}
	if len(requirements.AllowedDomains) > 0 || hardDeny {
		managed := normalizeHosts(requirements.AllowedDomains)
		effective := managed
		if allowExpansion {
			effective = mergeDomainLists(managed, domainsWithPermission(config.Domains, DomainAllow))
		} else {
			removeDomainsWithPermission(&config, DomainAllow)
		}
		setDomains(&config, effective, DomainAllow)
		constraints.AllowedDomains = managed
		constraints.AllowlistExpansionEnabled = &allowExpansion
	}
	if len(requirements.DeniedDomains) > 0 {
		managed := normalizeHosts(requirements.DeniedDomains)
		effective := managed
		if denyExpansion {
			effective = mergeDomainLists(managed, domainsWithPermission(config.Domains, DomainDeny))
		} else {
			removeDomainsWithPermission(&config, DomainDeny)
		}
		setDomains(&config, effective, DomainDeny)
		constraints.DeniedDomains = managed
		constraints.DenylistExpansionEnabled = &denyExpansion
	}
	return config, constraints
}

func allowlistExpansionEnabled(permissionKind PermissionProfileKind, hardDeny bool) bool {
	return permissionKind == PermissionManaged && !hardDeny
}

func denylistExpansionEnabled(permissionKind PermissionProfileKind) bool {
	return permissionKind == PermissionManaged
}

func setDomains(config *Config, hosts []string, permission DomainPermission) {
	if config.Domains == nil {
		config.Domains = map[string]DomainPermission{}
	}
	for _, host := range hosts {
		config.Domains[host] = permission
	}
}

func removeDomainsWithPermission(config *Config, permission DomainPermission) {
	if config.Domains == nil {
		return
	}
	for host, value := range config.Domains {
		if value == permission {
			delete(config.Domains, host)
		}
	}
}

func domainsWithPermission(domains map[string]DomainPermission, permission DomainPermission) []string {
	out := []string{}
	for host, value := range domains {
		if value == permission {
			out = append(out, host)
		}
	}
	sort.Strings(out)
	return out
}

func mergeDomainLists(first []string, second []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range [][]string{first, second} {
		for _, host := range list {
			host = NormalizeHost(host)
			if host == "" || seen[host] {
				continue
			}
			seen[host] = true
			out = append(out, host)
		}
	}
	return out
}

func normalizeHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = NormalizeHost(host)
		if host != "" {
			out = append(out, host)
		}
	}
	return out
}

func cloneConfig(config *Config) Config {
	if config == nil {
		return Config{}
	}
	cloned := *config
	if config.Domains != nil {
		cloned.Domains = make(map[string]DomainPermission, len(config.Domains))
		for host, permission := range config.Domains {
			cloned.Domains[host] = permission
		}
	}
	cloned.MITMHooks = append([]MITMHook(nil), config.MITMHooks...)
	return cloned
}

func cloneRequirements(requirements *Requirements) *Requirements {
	if requirements == nil {
		return nil
	}
	cloned := *requirements
	cloned.AllowedDomains = append([]string(nil), requirements.AllowedDomains...)
	cloned.DeniedDomains = append([]string(nil), requirements.DeniedDomains...)
	return &cloned
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
