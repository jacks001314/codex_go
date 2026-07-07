package network

import (
	"fmt"
	"strings"
)

func ProxyConfigFromConfigValues(values map[string]any) (*ProxyConfig, error) {
	settings := DefaultProxySettings()
	raw, ok := values["network_proxy"]
	if !ok || raw == nil {
		return &ProxyConfig{Network: settings}, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("network_proxy must be a table")
	}
	if value, ok := boolConfigValue(table, "enabled"); ok {
		settings.Enabled = value
	}
	if value, ok := stringConfigValue(table, "proxy_url"); ok {
		settings.ProxyURL = value
	}
	if value, ok := boolConfigValue(table, "enable_socks5"); ok {
		settings.EnableSocks5 = value
	}
	if value, ok := stringConfigValue(table, "socks_url"); ok {
		settings.SocksURL = value
	}
	if value, ok := boolConfigValue(table, "enable_socks5_udp"); ok {
		settings.EnableSocks5UDP = value
	}
	if value, ok := boolConfigValue(table, "allow_upstream_proxy"); ok {
		settings.AllowUpstreamProxy = value
	}
	if value, ok := boolConfigValue(table, "dangerously_allow_non_loopback_proxy"); ok {
		settings.DangerouslyAllowNonLoopbackProxy = value
	}
	if value, ok := boolConfigValue(table, "dangerously_allow_all_unix_sockets"); ok {
		settings.DangerouslyAllowAllUnixSockets = value
	}
	if value, ok := proxyModeConfigValue(table, "mode"); ok {
		settings.Mode = value
	}
	if value, ok := boolConfigValue(table, "allow_local_binding"); ok {
		settings.AllowLocalBinding = value
	}
	if value, ok := boolConfigValue(table, "mitm"); ok {
		settings.MITM = value
	}
	if value, ok := boolConfigValue(table, "credential_broker"); ok {
		settings.CredentialBroker = value
	}
	if value, ok := boolConfigValue(table, "dangerously_allow_plaintext_credential_injection"); ok {
		settings.DangerouslyAllowPlaintextCredentialInjection = value
	}
	if value, ok := domainPermissionsConfigValue(table, "domains"); ok {
		settings.Domains = value
	}
	if value, ok := unixSocketPermissionsConfigValue(table, "unix_sockets"); ok {
		settings.UnixSockets = value
	}
	if values, ok := stringListConfigValue(table, "allowed_domains"); ok {
		settings.SetAllowedDomains(values)
	}
	if values, ok := stringListConfigValue(table, "denied_domains"); ok {
		settings.SetDeniedDomains(values)
	}
	if values, ok := stringListConfigValue(table, "allow_unix_sockets"); ok {
		settings.SetAllowUnixSockets(values)
	}
	config := &ProxyConfig{Network: settings}
	if _, err := ResolveProxyRuntime(*config); err != nil {
		return nil, err
	}
	return config, nil
}

func boolConfigValue(values map[string]any, key string) (bool, bool) {
	value, ok := values[key].(bool)
	return value, ok
}

func stringConfigValue(values map[string]any, key string) (string, bool) {
	value, ok := values[key].(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func proxyModeConfigValue(values map[string]any, key string) (ProxyMode, bool) {
	value, ok := stringConfigValue(values, key)
	if !ok {
		return "", false
	}
	switch ProxyMode(value) {
	case "", ProxyModeFull:
		return ProxyModeFull, true
	case ProxyModeLimited:
		return ProxyModeLimited, true
	default:
		return "", false
	}
}

func domainPermissionsConfigValue(values map[string]any, key string) (*ProxyDomainPermissions, bool) {
	table, ok := values[key].(map[string]any)
	if !ok {
		return nil, false
	}
	permissions := &ProxyDomainPermissions{}
	for pattern, raw := range table {
		permission, ok := proxyDomainPermissionFromAny(raw)
		if !ok {
			continue
		}
		permissions.Entries = append(permissions.Entries, ProxyDomainPermissionEntry{
			Pattern:    strings.TrimSpace(pattern),
			Permission: permission,
		})
	}
	if len(permissions.Entries) == 0 {
		return nil, true
	}
	return permissions, true
}

func proxyDomainPermissionFromAny(value any) (ProxyDomainPermission, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	switch ProxyDomainPermission(strings.TrimSpace(text)) {
	case ProxyDomainAllow:
		return ProxyDomainAllow, true
	case ProxyDomainDeny:
		return ProxyDomainDeny, true
	case ProxyDomainNone:
		return ProxyDomainNone, true
	default:
		return "", false
	}
}

func unixSocketPermissionsConfigValue(values map[string]any, key string) (*ProxyUnixSocketPermissions, bool) {
	table, ok := values[key].(map[string]any)
	if !ok {
		return nil, false
	}
	entries := map[string]ProxyUnixSocketPermission{}
	for path, raw := range table {
		permission, ok := proxyUnixSocketPermissionFromAny(raw)
		if ok {
			entries[strings.TrimSpace(path)] = permission
		}
	}
	if len(entries) == 0 {
		return nil, true
	}
	return &ProxyUnixSocketPermissions{Entries: entries}, true
}

func proxyUnixSocketPermissionFromAny(value any) (ProxyUnixSocketPermission, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	switch ProxyUnixSocketPermission(strings.TrimSpace(text)) {
	case ProxyUnixSocketAllow:
		return ProxyUnixSocketAllow, true
	case ProxyUnixSocketDeny:
		return ProxyUnixSocketDeny, true
	default:
		return "", false
	}
}

func stringListConfigValue(values map[string]any, key string) ([]string, bool) {
	raw, ok := values[key]
	if !ok {
		return nil, false
	}
	switch v := raw.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out, true
	case []string:
		out := make([]string, 0, len(v))
		for _, text := range v {
			if strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out, true
	default:
		return nil, false
	}
}
