package network

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Rust parity: codex-rs/core/src/config/network_proxy_spec.rs (#60046).
// MCPNetworkPolicy derives the network restrictions a trusted MCP server may
// apply to one call from the controller spec, the executor route, the call's
// permission profile, the approval policy and the saved exec-policy rules. The
// result never carries proxy endpoints or credentials.

// MCPLocalBindingPolicy mirrors Rust's LocalBindingPolicy: DefaultFalse leaves
// an omitted allow_local_binding false, RequireTrue resolves an omission to the
// proxy's enabled state.
type MCPLocalBindingPolicy string

const (
	MCPLocalBindingDefaultFalse MCPLocalBindingPolicy = "default_false"
	MCPLocalBindingRequireTrue  MCPLocalBindingPolicy = "require_true"
)

// resolve mirrors LocalBindingPolicy::resolve.
func (p MCPLocalBindingPolicy) resolve(enabled bool, configured *bool) bool {
	if configured != nil {
		return *configured
	}
	return enabled && p == MCPLocalBindingRequireTrue
}

// MCPNetworkRoute mirrors Rust McpNetworkRoute. The primary attachment decides
// whether the shared proxy runs; the executor handling the call supplies its
// local-binding semantics even when it inherits the controller policy.
type MCPNetworkRoute struct {
	PrimaryAllowsProxy bool
	LocalBinding       MCPLocalBindingPolicy
	Owner              *EnvironmentNetworkPolicy
}

// MCPNetworkPolicyMetadata is the effective restriction delivered with an MCP
// call. The fields match the Rust NetworkPolicyMetadata wire shape.
type MCPNetworkPolicyMetadata struct {
	Enabled                 bool
	Mode                    Mode
	AllowedDomains          []string
	DeniedDomains           []string
	HardDenyAllowlistMisses bool
}

// MCPNetworkPolicy mirrors NetworkProxySpec::mcp_network_policy.
func MCPNetworkPolicy(
	controller *Spec,
	controllerProfile PermissionProfileKind,
	route MCPNetworkRoute,
	profile PermissionProfileKind,
	directNetwork bool,
	approvalNever bool,
	rules []NetworkRule,
) (*MCPNetworkPolicyMetadata, error) {
	if route.LocalBinding == "" {
		route.LocalBinding = MCPLocalBindingDefaultFalse
	}
	// An owner can supply its own proxy when the controller is absent. A disabled
	// controller vetoes that route, so file tools receive an offline policy.
	proxyActive := false
	if route.Owner != nil {
		if controller != nil {
			proxyActive = controller.Enabled()
		} else {
			proxyActive = route.Owner.RequiresProxy || directNetwork
		}
	} else {
		proxyActive = route.PrimaryAllowsProxy && controller != nil && controller.Enabled()
	}
	networkEnabled := proxyActive || (route.Owner == nil && directNetwork)

	var spec *Spec
	var err error
	switch {
	case route.Owner != nil:
		spec, err = composeMCPEnvironmentPolicy(controller, route.Owner, profile, proxyActive, route.LocalBinding, rules)
	case controller != nil && proxyActive:
		// The routed controller uses the thread's profile even for calls from
		// another executor.
		spec, err = controller.RecomputeForPermissionProfile(controllerProfile)
		if err == nil {
			if withRules, ruleErr := spec.WithExecPolicyNetworkRules(rules); ruleErr == nil {
				spec = withRules
			}
		}
	default:
		// Direct calls retain administrative policy without importing unused
		// proxy settings. Managed review domains stay because the receiver has to
		// deny traffic it cannot route through an approval.
		config := Config{Enabled: false, Mode: ModeFull}
		var requirements *Requirements
		if controller != nil {
			requirements = cloneRequirements(controller.requirements)
			config.ReviewDomains = append([]string(nil), controller.Config().ReviewDomains...)
		}
		spec, err = NewSpec(config, requirements, controllerProfile)
	}
	if err != nil {
		return nil, err
	}

	config := spec.Config()
	// The native decider reviews fresh misses only for managed calls with
	// approvals enabled.
	canReview := (route.Owner != nil || spec.requirements != nil) &&
		managedSandboxActive(profile) && !approvalNever
	// The domain-only MCP contract cannot check DNS answers at connect time.
	// With local access restricted, only explicit IP grants are safe to forward.
	requireIPLiteral := proxyActive && !route.LocalBinding.resolve(config.Enabled, config.AllowLocalBinding)
	// An MXC executor cannot launch an active proxy with an explicit
	// local-binding denial.
	canRoute := !(requireIPLiteral && route.LocalBinding == MCPLocalBindingRequireTrue)
	allowed := domainsWithPermission(config.Domains, DomainAllow)
	if requireIPLiteral {
		allowed = mcpIPLiteralRules(allowed)
	}
	denied := domainsWithPermission(config.Domains, DomainDeny)
	// The receiver cannot prompt for review or enforce a proxy hook's
	// per-request restrictions.
	additional := append([]string(nil), config.ReviewDomains...)
	if proxyActive {
		for _, hook := range config.MITMHooks {
			if host := normalizeMCPHookHost(hook.Host); host != "" {
				additional = append(additional, host)
			}
		}
	}
	enabled := networkEnabled && canRoute && (spec.constraints.Enabled == nil || *spec.constraints.Enabled)
	return &MCPNetworkPolicyMetadata{
		Enabled:                 enabled,
		Mode:                    config.Mode,
		AllowedDomains:          allowed,
		DeniedDomains:           mergeDomainLists(denied, additional),
		HardDenyAllowlistMisses: requireIPLiteral || spec.hardDenyAllowlistMisses || (proxyActive && !canReview),
	}, nil
}

// composeMCPEnvironmentPolicy mirrors NetworkProxySpec::compose_environment_policy
// with an optional routed binding: when the owner's proxy is offline the
// composition still keeps the owner's traffic rules but cannot require a
// startable proxy.
func composeMCPEnvironmentPolicy(
	controller *Spec,
	policy *EnvironmentNetworkPolicy,
	profile PermissionProfileKind,
	routed bool,
	binding MCPLocalBindingPolicy,
	rules []NetworkRule,
) (*Spec, error) {
	if routed && profile == PermissionDisabled {
		return nil, fmt.Errorf("environment network policy requires managed network enforcement")
	}
	if routed && controller != nil && !controller.Enabled() {
		return nil, fmt.Errorf("environment network policy cannot override a disabled controller proxy")
	}
	var spec *Spec
	var err error
	if controller != nil {
		spec, err = controller.RecomputeForPermissionProfile(profile)
	} else {
		// Without a controller the owner supplies the entire permission ceiling.
		allowAllSockets := true
		allowLocalBinding := false
		if policy != nil && policy.AllowLocalBinding != nil {
			allowLocalBinding = *policy.AllowLocalBinding
		}
		spec, err = NewSpec(Config{
			Enabled:                        true,
			Mode:                           ModeFull,
			DangerouslyAllowAllUnixSockets: &allowAllSockets,
			AllowLocalBinding:              &allowLocalBinding,
		}, nil, profile)
	}
	if err != nil {
		return nil, err
	}
	next := *spec
	next.config = cloneConfig(&spec.config)
	if policy != nil {
		policy.ApplyTo(&next.config)
	}
	resolvedBinding := binding.resolve(next.config.Enabled, next.config.AllowLocalBinding)
	next.config.AllowLocalBinding = &resolvedBinding
	protectedDenials := domainsWithPermission(next.config.Domains, DomainDeny)

	// A fixed controller allowlist remains a ceiling; an expandable one is only
	// a baseline.
	fixedAllowlist := spec.hardDenyAllowlistMisses ||
		(spec.constraints.AllowlistExpansionEnabled != nil && !*spec.constraints.AllowlistExpansionEnabled)
	next.hardDenyAllowlistMisses = spec.hardDenyAllowlistMisses ||
		(policy != nil && policy.ManagedAllowedDomainsOnly) || !managedSandboxActive(profile)
	allowOwnerGrants := !next.hardDenyAllowlistMisses && !fixedAllowlist
	// Rust forces denylist expansion here so owner denials can be restored as
	// constraints after the saved rules ran.
	denylistExpansion := true
	next.constraints.DenylistExpansionEnabled = &denylistExpansion
	if fixedAllowlist {
		next.constraints.AllowlistExpansionEnabled = nil
	}

	// Saved grants can extend a reviewable owner policy; owner denials are
	// restored last.
	applied := rules
	if !allowOwnerGrants {
		applied = execPolicyDenials(rules)
	}
	ApplyExecPolicyNetworkRules(&next.config, applied)
	for _, host := range protectedDenials {
		next.config.Domains[host] = DomainDeny
	}
	next.constraints.DeniedDomains = domainsWithPermission(next.config.Domains, DomainDeny)
	if routed {
		if err := ValidateAgainstConstraints(&next.config, &next.constraints); err != nil {
			return nil, fmt.Errorf("environment network policy violates managed requirements: %w", err)
		}
	}
	return &next, nil
}

// mcpIPLiteralRules keeps the allow rules a domain-only receiver can enforce
// without a DNS check: explicit IP literals without wildcards.
func mcpIPLiteralRules(rules []string) []string {
	out := []string{}
	for _, rule := range rules {
		if strings.ContainsAny(rule, "*?") {
			continue
		}
		host := strings.Trim(NormalizeHost(rule), "[]")
		if net.ParseIP(host) == nil {
			continue
		}
		out = append(out, rule)
	}
	return out
}

// normalizeMCPHookHost mirrors Rust's normalize_host for MITM hook hosts, which
// may carry a port ("Hook.Profile.Test.:443").
func normalizeMCPHookHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if parsed, err := url.Parse("//" + host); err == nil {
		if name := parsed.Hostname(); name != "" {
			return NormalizeHost(name)
		}
	}
	return NormalizeHost(host)
}
