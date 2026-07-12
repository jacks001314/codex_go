package network

import (
	"fmt"
	"sort"
	"strings"
)

func ProxyConfigFromConfigValues(values map[string]any) (*ProxyConfig, error) {
	settings := DefaultProxySettings()
	if raw, exists := values["network_proxy"]; exists && raw != nil {
		table, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("network_proxy must be a table")
		}
		if err := applyProxySettingsTable(&settings, table, "network_proxy"); err != nil {
			return nil, err
		}
	}
	profileNetworks, err := selectedPermissionProfileNetworkTables(values)
	if err != nil {
		return nil, err
	}
	mitm := newProxyProfileMITMAccumulator()
	for _, selected := range profileNetworks {
		if err := applyProxySettingsTable(&settings, selected.table, selected.path); err != nil {
			return nil, err
		}
		if err := mitm.apply(selected.table, selected.path); err != nil {
			return nil, err
		}
	}
	if len(profileNetworks) > 0 {
		hooks, err := mitm.runtimeHooks()
		if err != nil {
			return nil, err
		}
		settings.MITMHooks = hooks
		settings.MITM = settings.Mode == ProxyModeLimited || len(hooks) > 0
	}
	config := &ProxyConfig{Network: settings}
	if _, err := ResolveProxyRuntime(*config); err != nil {
		return nil, err
	}
	return config, nil
}

func applyProxySettingsTable(settings *ProxySettings, table map[string]any, path string) error {
	if settings == nil {
		return nil
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
	if hooks, ok, err := proxyMITMHooksConfigValue(table, "mitm_hooks"); err != nil {
		return err
	} else if ok {
		settings.MITMHooks = hooks
	}
	if value, ok := boolConfigValue(table, "credential_broker"); ok {
		settings.CredentialBroker = value
	}
	if value, ok := boolConfigValue(table, "dangerously_allow_plaintext_credential_injection"); ok {
		settings.DangerouslyAllowPlaintextCredentialInjection = value
	}
	if value, ok := domainPermissionsConfigValue(table, "domains"); ok {
		overlayProxyDomainPermissions(settings, value)
	}
	if value, ok := unixSocketPermissionsConfigValue(table, "unix_sockets"); ok {
		overlayProxyUnixSocketPermissions(settings, value)
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
	_ = path
	return nil
}

func overlayProxyDomainPermissions(settings *ProxySettings, permissions *ProxyDomainPermissions) {
	if settings == nil || permissions == nil {
		return
	}
	for _, entry := range permissions.Entries {
		settings.UpsertDomainPermission(entry.Pattern, entry.Permission, NormalizeProxyHost)
	}
}

func overlayProxyUnixSocketPermissions(settings *ProxySettings, permissions *ProxyUnixSocketPermissions) {
	if settings == nil || permissions == nil {
		return
	}
	if settings.UnixSockets == nil {
		settings.UnixSockets = &ProxyUnixSocketPermissions{Entries: map[string]ProxyUnixSocketPermission{}}
	}
	if settings.UnixSockets.Entries == nil {
		settings.UnixSockets.Entries = map[string]ProxyUnixSocketPermission{}
	}
	for path, permission := range permissions.Entries {
		settings.UnixSockets.Entries[path] = permission
	}
}

type selectedProxyNetworkTable struct {
	path  string
	table map[string]any
}

func selectedPermissionProfileNetworkTables(values map[string]any) ([]selectedProxyNetworkTable, error) {
	name, _ := values["default_permissions"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	if strings.HasPrefix(name, ":") {
		if supportedProxyBuiltinPermissionProfile(name) {
			return nil, nil
		}
		return nil, fmt.Errorf("default_permissions refers to unknown built-in profile `%s`", name)
	}
	profiles, ok := values["permissions"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("default_permissions requires a `[permissions]` table for network settings")
	}
	chain := []selectedProxyNetworkTable{}
	visited := map[string]int{}
	lineage := []string{}
	for name != "" {
		if index, exists := visited[name]; exists {
			cycle := append([]string(nil), lineage[index:]...)
			cycle = append(cycle, name)
			return nil, fmt.Errorf("permissions profile inheritance cycle detected: %s", strings.Join(cycle, " -> "))
		}
		visited[name] = len(lineage)
		lineage = append(lineage, name)
		raw, exists := profiles[name]
		if !exists {
			if len(lineage) == 1 {
				return nil, fmt.Errorf("default_permissions refers to undefined profile `%s`", name)
			}
			return nil, fmt.Errorf("permissions profile `%s` extends undefined profile `%s`", lineage[len(lineage)-2], name)
		}
		profile, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("permissions.%s must be a table", name)
		}
		if rawNetwork, exists := profile["network"]; exists && rawNetwork != nil {
			networkTable, ok := rawNetwork.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("permissions.%s.network must be a table", name)
			}
			chain = append(chain, selectedProxyNetworkTable{path: "permissions." + name + ".network", table: networkTable})
		}
		parent, _ := profile["extends"].(string)
		parent = strings.TrimSpace(parent)
		if strings.HasPrefix(parent, ":") {
			if !supportedProxyBuiltinPermissionProfile(parent) {
				return nil, fmt.Errorf("permissions profile `%s` extends unknown built-in profile `%s`", name, parent)
			}
			break
		}
		name = parent
	}
	for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
		chain[left], chain[right] = chain[right], chain[left]
	}
	return chain, nil
}

func supportedProxyBuiltinPermissionProfile(name string) bool {
	switch name {
	case ":workspace", ":read-only", ":danger-full-access":
		return true
	default:
		return false
	}
}

type proxyProfileMITMAccumulator struct {
	actions map[string]ProxyMITMHookActionsConfig
	hooks   map[string]proxyProfileMITMHook
}

type proxyProfileMITMHook struct {
	path       string
	host       string
	methods    []string
	paths      []string
	query      map[string][]string
	headers    map[string][]string
	body       *ProxyMITMHookBodyConfig
	actionRefs []string
}

func newProxyProfileMITMAccumulator() *proxyProfileMITMAccumulator {
	return &proxyProfileMITMAccumulator{
		actions: map[string]ProxyMITMHookActionsConfig{},
		hooks:   map[string]proxyProfileMITMHook{},
	}
}

func (a *proxyProfileMITMAccumulator) apply(networkTable map[string]any, path string) error {
	rawMITM, exists := networkTable["mitm"]
	if !exists || rawMITM == nil {
		return nil
	}
	mitm, ok := rawMITM.(map[string]any)
	if !ok {
		return fmt.Errorf("%s.mitm must be a table", path)
	}
	if rawActions, exists := mitm["actions"]; exists {
		actions, ok := rawActions.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.mitm.actions must be a table", path)
		}
		for name, rawAction := range actions {
			table, ok := rawAction.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.mitm.actions.%s must be a table", path, name)
			}
			strip, err := optionalStringListConfigValue(table, "strip_request_headers")
			if err != nil {
				return fmt.Errorf("%s.mitm.actions.%s: %w", path, name, err)
			}
			inject, err := injectedHeadersConfigValue(table, "inject_request_headers")
			if err != nil {
				return fmt.Errorf("%s.mitm.actions.%s: %w", path, name, err)
			}
			if len(strip) == 0 && len(inject) == 0 {
				return fmt.Errorf("%s.mitm.actions.%s must define at least one operation", path, name)
			}
			a.actions[name] = ProxyMITMHookActionsConfig{StripRequestHeaders: strip, InjectRequestHeaders: inject}
		}
	}
	if rawHooks, exists := mitm["hooks"]; exists {
		hooks, ok := rawHooks.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.mitm.hooks must be a table", path)
		}
		for name, rawHook := range hooks {
			table, ok := rawHook.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.mitm.hooks.%s must be a table", path, name)
			}
			host, ok := stringConfigValue(table, "host")
			if !ok {
				return fmt.Errorf("%s.mitm.hooks.%s.host must be a string", path, name)
			}
			methods, err := requiredStringListConfigValue(table, "methods")
			if err != nil {
				return fmt.Errorf("%s.mitm.hooks.%s: %w", path, name, err)
			}
			paths, err := requiredStringListConfigValue(table, "path_prefixes")
			if err != nil {
				return fmt.Errorf("%s.mitm.hooks.%s: %w", path, name, err)
			}
			query, err := stringListMapConfigValue(table, "query")
			if err != nil {
				return fmt.Errorf("%s.mitm.hooks.%s: %w", path, name, err)
			}
			headers, err := stringListMapConfigValue(table, "headers")
			if err != nil {
				return fmt.Errorf("%s.mitm.hooks.%s: %w", path, name, err)
			}
			actionRefs, err := requiredStringListConfigValue(table, "action")
			if err != nil {
				return fmt.Errorf("%s.mitm.hooks.%s: %w", path, name, err)
			}
			if len(actionRefs) == 0 {
				return fmt.Errorf("%s.mitm.hooks.%s.action must not be empty", path, name)
			}
			var body *ProxyMITMHookBodyConfig
			if rawBody, exists := table["body"]; exists {
				body = &ProxyMITMHookBodyConfig{Raw: rawBody}
			}
			a.hooks[name] = proxyProfileMITMHook{path: path, host: host, methods: methods, paths: paths, query: query, headers: headers, body: body, actionRefs: actionRefs}
		}
	}
	return nil
}

func (a *proxyProfileMITMAccumulator) runtimeHooks() ([]ProxyMITMHookConfig, error) {
	if a == nil || len(a.hooks) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(a.hooks))
	for name := range a.hooks {
		names = append(names, name)
	}
	sort.Strings(names)
	hooks := make([]ProxyMITMHookConfig, 0, len(names))
	for _, name := range names {
		raw := a.hooks[name]
		actions := ProxyMITMHookActionsConfig{}
		for _, actionName := range raw.actionRefs {
			action, ok := a.actions[actionName]
			if !ok {
				return nil, fmt.Errorf("%s.mitm.hooks.%s.action references undefined action `%s`", raw.path, name, actionName)
			}
			actions.StripRequestHeaders = append(actions.StripRequestHeaders, action.StripRequestHeaders...)
			actions.InjectRequestHeaders = append(actions.InjectRequestHeaders, action.InjectRequestHeaders...)
		}
		hooks = append(hooks, ProxyMITMHookConfig{
			Host: raw.host,
			Match: ProxyMITMHookMatchConfig{
				Methods:      raw.methods,
				PathPrefixes: raw.paths,
				Query:        raw.query,
				Headers:      raw.headers,
				Body:         raw.body,
			},
			Actions: actions,
		})
	}
	return hooks, nil
}

func proxyMITMHooksConfigValue(values map[string]any, key string) ([]ProxyMITMHookConfig, bool, error) {
	raw, exists := values[key]
	if !exists {
		return nil, false, nil
	}
	entries, ok := tableListFromAny(raw)
	if !ok {
		return nil, true, fmt.Errorf("network_proxy.%s must be an array", key)
	}
	hooks := make([]ProxyMITMHookConfig, 0, len(entries))
	for index, table := range entries {
		hook, err := proxyMITMHookConfigFromTable(table)
		if err != nil {
			return nil, true, fmt.Errorf("invalid network_proxy.%s[%d]: %w", key, index, err)
		}
		hooks = append(hooks, hook)
	}
	return hooks, true, nil
}

func proxyMITMHookConfigFromTable(table map[string]any) (ProxyMITMHookConfig, error) {
	host, ok := stringConfigValue(table, "host")
	if !ok {
		return ProxyMITMHookConfig{}, fmt.Errorf("host must be a string")
	}
	hook := ProxyMITMHookConfig{Host: host}
	if rawMatch, exists := table["match"]; exists {
		match, ok := rawMatch.(map[string]any)
		if !ok {
			return ProxyMITMHookConfig{}, fmt.Errorf("match must be a table")
		}
		var err error
		hook.Match.Methods, err = requiredStringListConfigValue(match, "methods")
		if err != nil {
			return ProxyMITMHookConfig{}, err
		}
		hook.Match.PathPrefixes, err = requiredStringListConfigValue(match, "path_prefixes")
		if err != nil {
			return ProxyMITMHookConfig{}, err
		}
		if hook.Match.Query, err = stringListMapConfigValue(match, "query"); err != nil {
			return ProxyMITMHookConfig{}, err
		}
		if hook.Match.Headers, err = stringListMapConfigValue(match, "headers"); err != nil {
			return ProxyMITMHookConfig{}, err
		}
		if body, exists := match["body"]; exists {
			hook.Match.Body = &ProxyMITMHookBodyConfig{Raw: body}
		}
	}
	if rawActions, exists := table["actions"]; exists {
		actions, ok := rawActions.(map[string]any)
		if !ok {
			return ProxyMITMHookConfig{}, fmt.Errorf("actions must be a table")
		}
		var err error
		if hook.Actions.StripRequestHeaders, err = optionalStringListConfigValue(actions, "strip_request_headers"); err != nil {
			return ProxyMITMHookConfig{}, err
		}
		if hook.Actions.InjectRequestHeaders, err = injectedHeadersConfigValue(actions, "inject_request_headers"); err != nil {
			return ProxyMITMHookConfig{}, err
		}
	}
	return hook, nil
}

func requiredStringListConfigValue(values map[string]any, key string) ([]string, error) {
	items, exists := values[key]
	if !exists {
		return nil, nil
	}
	result, ok := stringListFromAny(items)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	return result, nil
}

func optionalStringListConfigValue(values map[string]any, key string) ([]string, error) {
	return requiredStringListConfigValue(values, key)
}

func stringListMapConfigValue(values map[string]any, key string) (map[string][]string, error) {
	raw, exists := values[key]
	if !exists {
		return nil, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a table", key)
	}
	result := make(map[string][]string, len(table))
	for name, value := range table {
		items, ok := stringListFromAny(value)
		if !ok {
			return nil, fmt.Errorf("%s.%s must be an array of strings", key, name)
		}
		result[name] = items
	}
	return result, nil
}

func injectedHeadersConfigValue(values map[string]any, key string) ([]ProxyInjectedHeaderConfig, error) {
	raw, exists := values[key]
	if !exists {
		return nil, nil
	}
	entries, ok := tableListFromAny(raw)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", key)
	}
	result := make([]ProxyInjectedHeaderConfig, 0, len(entries))
	for index, table := range entries {
		name, ok := stringConfigValue(table, "name")
		if !ok {
			return nil, fmt.Errorf("%s[%d].name must be a string", key, index)
		}
		header := ProxyInjectedHeaderConfig{Name: name}
		if value, ok := stringConfigValue(table, "prefix"); ok {
			header.Prefix = value
		}
		if value, ok := stringConfigValue(table, "secret_env_var"); ok {
			header.SecretEnvVar = &value
		}
		if value, ok := stringConfigValue(table, "secret_file"); ok {
			header.SecretFile = &value
		}
		result = append(result, header)
	}
	return result, nil
}

func tableListFromAny(value any) ([]map[string]any, bool) {
	switch entries := value.(type) {
	case []map[string]any:
		return entries, true
	case []any:
		result := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			table, ok := entry.(map[string]any)
			if !ok {
				return nil, false
			}
			result = append(result, table)
		}
		return result, true
	default:
		return nil, false
	}
}

func stringListFromAny(value any) ([]string, bool) {
	switch items := value.(type) {
	case []string:
		return append([]string(nil), items...), true
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	default:
		return nil, false
	}
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
	patterns := make([]string, 0, len(table))
	for pattern := range table {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		raw := table[pattern]
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
