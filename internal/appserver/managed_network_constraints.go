package appserver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"codex_go/internal/config"
	"codex_go/internal/network"
)

func (r *RuntimeRouter) buildManagedNetworkProxyConfig(values map[string]any) (*network.ProxyConfig, bool, error) {
	return r.buildManagedNetworkProxyConfigForCWD(values, r.services.DefaultCWD)
}

func (r *RuntimeRouter) buildManagedNetworkProxyConfigForCWD(values map[string]any, cwd string) (*network.ProxyConfig, bool, error) {
	proxyConfig, err := network.ProxyConfigFromConfigValues(values)
	if err != nil {
		return nil, false, err
	}
	hasRequirements := r != nil && r.services.ManagedNetworkRequirements != nil
	if proxyConfig == nil {
		settings := network.DefaultProxySettings()
		proxyConfig = &network.ProxyConfig{Network: settings}
	}
	managedProfile, profileAllowsProxy, err := managedNetworkPermissionProfileState(values, cwd)
	if err != nil {
		return nil, false, err
	}
	if !hasRequirements && (!proxyConfig.Network.Enabled || !profileAllowsProxy) {
		return proxyConfig, false, nil
	}

	spec, err := network.NewProxySpec(*proxyConfig, proxyRequirementsFromConfig(r.services.ManagedNetworkRequirements), managedProfile)
	if err != nil {
		return nil, false, err
	}
	rules, err := loadExecPolicyNetworkRules(r.services.Config.CodexHome())
	if err != nil {
		slog.Warn("failed to parse execpolicy while building network proxy state", "error", err)
	} else if len(rules) > 0 {
		withRules, rulesErr := spec.WithNetworkRules(rules)
		if rulesErr != nil {
			slog.Warn("failed to apply execpolicy network rules to managed proxy; continuing with configured network policy", "error", rulesErr)
		} else {
			spec = withRules
		}
	}

	result := spec.Config()
	result.EnvironmentID = "local"
	if hasRequirements {
		result.BlockedObserver = r.networkApproval
	}
	if hasRequirements && !spec.HardDenyAllowlistMisses() {
		result.PolicyDecider = r.networkApproval
	}
	result.AuditMetadataProvider = r.networkProxyAuditMetadata
	result.AuditSink = emitNetworkProxyAuditEvent
	return &result, true, nil
}

type managedNetworkReloadInput struct {
	CWD       string
	Overrides map[string]any
}

func (r *RuntimeRouter) managedNetworkForTurn(threadID string, cwd string, cfg *config.Config, overrides ...map[string]any) (*network.PreparedProxyManagedNetwork, error) {
	if r == nil || cfg == nil || r.services.Config == nil || strings.TrimSpace(threadID) == "" {
		return nil, nil
	}
	if err := validateManagedNetworkRequirements(r.services.ManagedNetworkRequirements); err != nil {
		return nil, err
	}
	proxyConfig, shouldStart, err := r.buildManagedNetworkProxyConfigForCWD(cfg.Values, cwd)
	if err != nil {
		return nil, err
	}
	threadID = strings.TrimSpace(threadID)
	r.scopeManagedNetworkConfigForThread(threadID, proxyConfig)
	var turnOverrides map[string]any
	if len(overrides) > 0 {
		turnOverrides = cloneAnyMap(overrides[0])
	}
	r.managedNetworksMu.Lock()
	existing := r.managedNetworks[threadID]
	if !shouldStart {
		if existing != nil {
			delete(r.managedNetworks, threadID)
			delete(r.managedNetworkInputs, threadID)
			_ = existing.Close()
		}
		r.managedNetworksMu.Unlock()
		return nil, nil
	}
	r.managedNetworkInputs[threadID] = managedNetworkReloadInput{CWD: cwd, Overrides: turnOverrides}
	if existing != nil {
		if err := existing.ReloadConfig(*proxyConfig); err == nil {
			r.managedNetworksMu.Unlock()
			r.startManagedNetworkReloadWatcher()
			r.watchManagedNetworkProjectConfig(cwd)
			return existing, nil
		}
		delete(r.managedNetworks, threadID)
		_ = existing.Close()
	}
	prepared, err := network.StartProxyManagedNetwork(context.Background(), *proxyConfig, processEnvironmentMap())
	if err != nil {
		delete(r.managedNetworkInputs, threadID)
		r.managedNetworksMu.Unlock()
		return nil, err
	}
	r.managedNetworks[threadID] = prepared
	r.managedNetworksMu.Unlock()
	r.startManagedNetworkReloadWatcher()
	r.watchManagedNetworkProjectConfig(cwd)
	return prepared, nil
}

func (r *RuntimeRouter) scopeManagedNetworkConfigForThread(threadID string, proxyConfig *network.ProxyConfig) {
	if r == nil || proxyConfig == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if proxyConfig.PolicyDecider != nil {
		proxyConfig.PolicyDecider = network.ProxyPolicyDeciderFunc(func(ctx context.Context, request network.ProxyPolicyRequest) network.ProxyDecision {
			return r.networkApproval.decideForThread(ctx, threadID, request)
		})
	}
	if proxyConfig.BlockedObserver != nil {
		proxyConfig.BlockedObserver = network.ProxyBlockedRequestObserverFunc(func(_ context.Context, blocked network.ProxyBlockedRequest) {
			r.networkApproval.onBlockedRequestForThread(threadID, blocked)
		})
	}
	proxyConfig.AuditMetadataProvider = func(request network.ProxyPolicyRequest) network.ProxyAuditMetadata {
		return r.networkProxyAuditMetadataForThread(threadID, request)
	}
}

func (r *RuntimeRouter) closeThreadManagedNetworks() error {
	if r == nil {
		return nil
	}
	r.managedNetworksMu.Lock()
	networks := make([]*network.PreparedProxyManagedNetwork, 0, len(r.managedNetworks))
	for _, prepared := range r.managedNetworks {
		networks = append(networks, prepared)
	}
	r.managedNetworks = map[string]*network.PreparedProxyManagedNetwork{}
	r.managedNetworkInputs = map[string]managedNetworkReloadInput{}
	r.managedNetworksMu.Unlock()
	var closeErr error
	for _, prepared := range networks {
		if err := prepared.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (r *RuntimeRouter) closeThreadManagedNetwork(threadID string) error {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	r.managedNetworksMu.Lock()
	prepared := r.managedNetworks[threadID]
	delete(r.managedNetworks, threadID)
	delete(r.managedNetworkInputs, threadID)
	r.managedNetworksMu.Unlock()
	if prepared == nil {
		return nil
	}
	return prepared.Close()
}

func processEnvironmentMap() map[string]string {
	env := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok && key != "" {
			env[key] = value
		}
	}
	return env
}

func managedNetworkPermissionProfileState(values map[string]any, cwd string) (managed bool, allowsProxy bool, err error) {
	resolved, err := (&config.Config{Values: values}).ResolveSandboxPermissionProfile("", cwd)
	if err != nil {
		return false, false, err
	}
	if resolved == nil || resolved.Profile == nil {
		return true, true, nil
	}
	profile := resolved.Profile
	if profile.Disabled {
		return false, false, nil
	}
	if profile.SandboxPolicy != nil && string(profile.SandboxPolicy.Kind) == "external-sandbox" {
		return false, profile.AllowsNetwork(), nil
	}
	return true, true, nil
}

func proxyRequirementsFromConfig(requirements *config.NetworkRequirements) *network.ProxyRequirements {
	if requirements == nil {
		return nil
	}
	out := &network.ProxyRequirements{
		Enabled:                          cloneNetworkBool(requirements.Enabled),
		HTTPPort:                         cloneNetworkUint16(requirements.HTTPPort),
		SOCKSPort:                        cloneNetworkUint16(requirements.SOCKSPort),
		AllowUpstreamProxy:               cloneNetworkBool(requirements.AllowUpstreamProxy),
		DangerouslyAllowNonLoopbackProxy: cloneNetworkBool(requirements.DangerouslyAllowNonLoopbackProxy),
		DangerouslyAllowAllUnixSockets:   cloneNetworkBool(requirements.DangerouslyAllowAllUnixSockets),
		ManagedAllowedDomainsOnly:        requirements.ManagedAllowedDomainsOnly != nil && *requirements.ManagedAllowedDomainsOnly,
		AllowLocalBinding:                cloneNetworkBool(requirements.AllowLocalBinding),
	}
	if requirements.Domains != nil {
		out.Domains = proxyDomainRequirementsFromMap(requirements.Domains)
	} else if requirements.AllowedDomains != nil || requirements.DeniedDomains != nil {
		entries := make([]network.ProxyDomainPermissionEntry, 0, len(requirements.AllowedDomains)+len(requirements.DeniedDomains))
		for _, host := range requirements.AllowedDomains {
			entries = append(entries, network.ProxyDomainPermissionEntry{Pattern: host, Permission: network.ProxyDomainAllow})
		}
		for _, host := range requirements.DeniedDomains {
			entries = append(entries, network.ProxyDomainPermissionEntry{Pattern: host, Permission: network.ProxyDomainDeny})
		}
		out.Domains = &network.ProxyDomainPermissions{Entries: entries}
	}
	if requirements.UnixSockets != nil {
		out.UnixSockets = proxyUnixSocketRequirementsFromMap(requirements.UnixSockets)
	} else if requirements.AllowUnixSockets != nil {
		entries := make(map[string]network.ProxyUnixSocketPermission, len(requirements.AllowUnixSockets))
		for _, path := range requirements.AllowUnixSockets {
			entries[path] = network.ProxyUnixSocketAllow
		}
		out.UnixSockets = &network.ProxyUnixSocketPermissions{Entries: entries}
	}
	return out
}

func proxyDomainRequirementsFromMap(values map[string]config.NetworkPermission) *network.ProxyDomainPermissions {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]network.ProxyDomainPermissionEntry, 0, len(keys))
	for _, key := range keys {
		permission := network.ProxyDomainNone
		switch values[key] {
		case config.NetworkAllow:
			permission = network.ProxyDomainAllow
		case config.NetworkDeny:
			permission = network.ProxyDomainDeny
		}
		entries = append(entries, network.ProxyDomainPermissionEntry{Pattern: key, Permission: permission})
	}
	return &network.ProxyDomainPermissions{Entries: entries}
}

func proxyUnixSocketRequirementsFromMap(values map[string]config.NetworkPermission) *network.ProxyUnixSocketPermissions {
	entries := make(map[string]network.ProxyUnixSocketPermission, len(values))
	for path, value := range values {
		switch value {
		case config.NetworkAllow:
			entries[path] = network.ProxyUnixSocketAllow
		case config.NetworkDeny:
			entries[path] = network.ProxyUnixSocketDeny
		}
	}
	return &network.ProxyUnixSocketPermissions{Entries: entries}
}

func cloneRuntimeNetworkRequirements(requirements *config.NetworkRequirements) *config.NetworkRequirements {
	if requirements == nil {
		return nil
	}
	clone := *requirements
	clone.Enabled = cloneNetworkBool(requirements.Enabled)
	clone.HTTPPort = cloneNetworkUint16(requirements.HTTPPort)
	clone.SOCKSPort = cloneNetworkUint16(requirements.SOCKSPort)
	clone.AllowUpstreamProxy = cloneNetworkBool(requirements.AllowUpstreamProxy)
	clone.DangerouslyAllowNonLoopbackProxy = cloneNetworkBool(requirements.DangerouslyAllowNonLoopbackProxy)
	clone.DangerouslyAllowAllUnixSockets = cloneNetworkBool(requirements.DangerouslyAllowAllUnixSockets)
	clone.ManagedAllowedDomainsOnly = cloneNetworkBool(requirements.ManagedAllowedDomainsOnly)
	clone.AllowLocalBinding = cloneNetworkBool(requirements.AllowLocalBinding)
	clone.AllowedDomains = append([]string(nil), requirements.AllowedDomains...)
	clone.DeniedDomains = append([]string(nil), requirements.DeniedDomains...)
	clone.AllowUnixSockets = append([]string(nil), requirements.AllowUnixSockets...)
	if requirements.Domains != nil {
		clone.Domains = make(map[string]config.NetworkPermission, len(requirements.Domains))
		for host, permission := range requirements.Domains {
			clone.Domains[host] = permission
		}
	}
	if requirements.UnixSockets != nil {
		clone.UnixSockets = make(map[string]config.NetworkPermission, len(requirements.UnixSockets))
		for path, permission := range requirements.UnixSockets {
			clone.UnixSockets[path] = permission
		}
	}
	return &clone
}

func cloneNetworkBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneNetworkUint16(value *uint16) *uint16 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func validateManagedNetworkRequirements(requirements *config.NetworkRequirements) error {
	if requirements == nil {
		return nil
	}
	if requirements.Domains != nil && (requirements.AllowedDomains != nil || requirements.DeniedDomains != nil) {
		return fmt.Errorf("experimental_network.domains cannot be combined with legacy allowed_domains or denied_domains")
	}
	if requirements.UnixSockets != nil && requirements.AllowUnixSockets != nil {
		return fmt.Errorf("experimental_network.unix_sockets cannot be combined with legacy allow_unix_sockets")
	}
	for host, permission := range requirements.Domains {
		if strings.TrimSpace(host) == "" || (permission != config.NetworkAllow && permission != config.NetworkDeny) {
			return fmt.Errorf("invalid experimental_network.domains entry %q", host)
		}
	}
	return nil
}
