package network

import (
	"fmt"
	"sort"
	"strings"
)

func ProxyConfigFromConfigValues(values map[string]any) (*ProxyConfig, error) {
	settings := DefaultProxySettings()
	// Rust reads the configured-network base from `features.network_proxy`
	// (codex-rs/core/src/config/mod.rs network_proxy_toml_config); a top-level
	// `network_proxy` is not part of ConfigToml and is ignored (it carries the
	// #44691 migration warning). A non-table `features.network_proxy` value is
	// ignored the same way Rust drops the boolean form.
	if features, ok := values["features"].(map[string]any); ok {
		if raw, exists := features["network_proxy"]; exists && raw != nil {
			if table, ok := raw.(map[string]any); ok {
				if err := applyProxySettingsTable(&settings, table, "features.network_proxy"); err != nil {
					return nil, err
				}
			}
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
	if err := rejectProxyUnknownFields(table, path, proxySettingsAllowedKeys); err != nil {
		return err
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
		// Rust #46004: keep omission distinct from an explicit false.
		settings.DangerouslyAllowAllUnixSockets = &value
	}
	if value, ok := proxyModeConfigValue(table, "mode"); ok {
		settings.Mode = value
	}
	if value, ok := boolConfigValue(table, "allow_local_binding"); ok {
		settings.AllowLocalBinding = value
		settings.AllowLocalBindingSet = true
	}
	if value, ok := boolConfigValue(table, "mitm"); ok {
		settings.MITM = value
	}
	if hooks, ok, err := proxyMITMHooksConfigValue(table, "mitm_hooks"); err != nil {
		return err
	} else if ok {
		settings.MITMHooks = hooks
	}
	if raw, ok := table["mitm_ca"]; ok && raw != nil {
		caTable, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("network.mitm_ca must be a table")
		}
		if err := rejectProxyUnknownFields(caTable, path+".mitm_ca", proxyMITMCAAllowedKeys); err != nil {
			return err
		}
		certificateFile, _ := caTable["certificate_file"].(string)
		privateKeyFile, _ := caTable["private_key_file"].(string)
		settings.MITMCA = &ProxyMITMCAConfig{
			CertificateFile: strings.TrimSpace(certificateFile),
			PrivateKeyFile:  strings.TrimSpace(privateKeyFile),
		}
	}
	if value, ok := boolConfigValue(table, "credential_broker"); ok {
		settings.CredentialBroker = value
	}
	if raw, ok := table["credentials"]; ok && raw != nil {
		providers, err := ParseCredentialProviderConfigs(raw)
		if err != nil {
			return err
		}
		settings.CredentialProviders = providers
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

// Rust #47132 rejects unknown fields in NetworkProxyConfig and its nested
// MITM configuration. The accepted set is the union of the config.toml
// feature surface and the profile/network fields Go already maps.
var proxySettingsAllowedKeys = map[string]struct{}{
	"enabled":                              {},
	"proxy_url":                            {},
	"enable_socks5":                        {},
	"socks_url":                            {},
	"enable_socks5_udp":                    {},
	"allow_upstream_proxy":                 {},
	"dangerously_allow_non_loopback_proxy": {},
	"dangerously_allow_all_unix_sockets":   {},
	"mode":                                 {},
	"domains":                              {},
	"unix_sockets":                         {},
	"allowed_domains":                      {},
	"denied_domains":                       {},
	"allow_unix_sockets":                   {},
	"allow_local_binding":                  {},
	"mitm":                                 {},
	"mitm_ca":                              {},
	"mitm_hooks":                           {},
	"credential_broker":                    {},
	"credentials":                          {},
	"dangerously_allow_plaintext_credential_injection": {},
}

var proxyMITMCAAllowedKeys = map[string]struct{}{
	"certificate_file": {},
	"private_key_file": {},
}

var proxyMITMHookAllowedKeys = map[string]struct{}{
	"host":    {},
	"match":   {},
	"actions": {},
}

var proxyMITMHookMatchAllowedKeys = map[string]struct{}{
	"schemes":       {},
	"ports":         {},
	"methods":       {},
	"path_prefixes": {},
	"query":         {},
	"headers":       {},
	"body":          {},
}

var proxyMITMHookActionsAllowedKeys = map[string]struct{}{
	"strip_request_headers":    {},
	"inject_request_headers":   {},
	"aws_sigv4":                {},
	"body_secret_substitution": {},
}

var proxyBodySubstitutionAllowedKeys = map[string]struct{}{
	"replacements":   {},
	"port":           {},
	"max_body_bytes": {},
}

var proxyBodySubstitutionReplacementAllowedKeys = map[string]struct{}{
	"placeholder": {},
	"secret":      {},
}

var proxyAWSSigV4AllowedKeys = map[string]struct{}{
	"placeholder_access_key_id": {},
	"region":                    {},
	"service":                   {},
	"access_key_id":             {},
	"secret_access_key":         {},
	"session_token":             {},
	"port":                      {},
	"max_body_bytes":            {},
}

var proxyProfileMITMAllowedKeys = map[string]struct{}{
	"version": {},
	"hooks":   {},
	"actions": {},
}

var proxyProfileMITMHookAllowedKeys = map[string]struct{}{
	"host":          {},
	"schemes":       {},
	"ports":         {},
	"methods":       {},
	"path_prefixes": {},
	"query":         {},
	"headers":       {},
	"action":        {},
	"body":          {},
}

// rejectProxyUnknownFields mirrors serde's deny_unknown_fields: the first
// unknown key (in sorted order, for deterministic diagnostics) fails the load.
func rejectProxyUnknownFields(table map[string]any, path string, allowed map[string]struct{}) error {
	unknown := make([]string, 0)
	for key := range table {
		if _, ok := allowed[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("unknown field `%s` in %s", unknown[0], path)
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
	version          *int
	usesScopedAction bool
	usesScopedHook   bool
	actions          map[string]ProxyMITMHookActionsConfig
	hooks            map[string]proxyProfileMITMHook
}

type proxyProfileMITMHook struct {
	path       string
	host       string
	schemes    []string
	ports      []int
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
	if err := rejectProxyUnknownFields(mitm, path+".mitm", proxyProfileMITMAllowedKeys); err != nil {
		return err
	}
	if rawVersion, exists := mitm["version"]; exists {
		version, ok := numberConfigValue(rawVersion)
		if !ok || version != 2 {
			return fmt.Errorf("%s.mitm.version must be 2", path)
		}
		a.version = &version
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
			if err := rejectProxyUnknownFields(table, path+".mitm.actions."+name, proxyMITMHookActionsAllowedKeys); err != nil {
				return err
			}
			strip, err := optionalStringListConfigValue(table, "strip_request_headers")
			if err != nil {
				return fmt.Errorf("%s.mitm.actions.%s: %w", path, name, err)
			}
			inject, err := injectedHeadersConfigValue(table, "inject_request_headers")
			if err != nil {
				return fmt.Errorf("%s.mitm.actions.%s: %w", path, name, err)
			}
			awsSigV4, err := awsSigV4ConfigValue(table, "aws_sigv4", path+".mitm.actions."+name)
			if err != nil {
				return err
			}
			substitution, err := bodySecretSubstitutionConfigValue(table, "body_secret_substitution", path+".mitm.actions."+name)
			if err != nil {
				return err
			}
			if len(strip) == 0 && len(inject) == 0 && awsSigV4 == nil && substitution == nil {
				return fmt.Errorf("%s.mitm.actions.%s must define at least one operation", path, name)
			}
			if awsSigV4 != nil || substitution != nil {
				a.usesScopedAction = true
			}
			for _, header := range inject {
				if header.Placeholder != nil {
					a.usesScopedAction = true
				}
			}
			a.actions[name] = ProxyMITMHookActionsConfig{
				StripRequestHeaders:    strip,
				InjectRequestHeaders:   inject,
				AWSSigV4:               awsSigV4,
				BodySecretSubstitution: substitution,
			}
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
			if err := rejectProxyUnknownFields(table, path+".mitm.hooks."+name, proxyProfileMITMHookAllowedKeys); err != nil {
				return err
			}
			host, ok := stringConfigValue(table, "host")
			if !ok {
				return fmt.Errorf("%s.mitm.hooks.%s.host must be a string", path, name)
			}
			schemes, err := optionalStringListConfigValue(table, "schemes")
			if err != nil {
				return fmt.Errorf("%s.mitm.hooks.%s: %w", path, name, err)
			}
			ports, err := portListConfigValue(table, "ports")
			if err != nil {
				return fmt.Errorf("%s.mitm.hooks.%s: %w", path, name, err)
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
			if len(schemes) > 0 || len(ports) > 0 {
				a.usesScopedHook = true
			}
			a.hooks[name] = proxyProfileMITMHook{path: path, host: host, schemes: schemes, ports: ports, methods: methods, paths: paths, query: query, headers: headers, body: body, actionRefs: actionRefs}
		}
	}
	return nil
}

func (a *proxyProfileMITMAccumulator) runtimeHooks() ([]ProxyMITMHookConfig, error) {
	if a == nil || len(a.hooks) == 0 {
		return nil, nil
	}
	if a.version == nil && a.usesScopedAction {
		return nil, fmt.Errorf("scoped MITM actions require network.mitm.version = 2")
	}
	if a.version == nil && a.usesScopedHook {
		return nil, fmt.Errorf("scoped MITM hooks require network.mitm.version = 2")
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
			if action.AWSSigV4 != nil {
				if actions.AWSSigV4 != nil {
					return nil, fmt.Errorf("%s.mitm.hooks.%s must select at most one AWS signing action", raw.path, name)
				}
				actions.AWSSigV4 = action.AWSSigV4
			}
			if action.BodySecretSubstitution != nil {
				if actions.BodySecretSubstitution != nil {
					return nil, fmt.Errorf("%s.mitm.hooks.%s must select at most one body substitution action", raw.path, name)
				}
				actions.BodySecretSubstitution = action.BodySecretSubstitution
			}
		}
		hooks = append(hooks, ProxyMITMHookConfig{
			Host: raw.host,
			Match: ProxyMITMHookMatchConfig{
				Schemes:      raw.schemes,
				Ports:        raw.ports,
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
	if err := rejectProxyUnknownFields(table, "network_proxy.mitm_hooks[]", proxyMITMHookAllowedKeys); err != nil {
		return ProxyMITMHookConfig{}, err
	}
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
		if err := rejectProxyUnknownFields(match, "network_proxy.mitm_hooks[].match", proxyMITMHookMatchAllowedKeys); err != nil {
			return ProxyMITMHookConfig{}, err
		}
		var err error
		if hook.Match.Schemes, err = optionalStringListConfigValue(match, "schemes"); err != nil {
			return ProxyMITMHookConfig{}, err
		}
		if hook.Match.Ports, err = portListConfigValue(match, "ports"); err != nil {
			return ProxyMITMHookConfig{}, err
		}
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
		if err := rejectProxyUnknownFields(actions, "network_proxy.mitm_hooks[].actions", proxyMITMHookActionsAllowedKeys); err != nil {
			return ProxyMITMHookConfig{}, err
		}
		var err error
		if hook.Actions.StripRequestHeaders, err = optionalStringListConfigValue(actions, "strip_request_headers"); err != nil {
			return ProxyMITMHookConfig{}, err
		}
		if hook.Actions.InjectRequestHeaders, err = injectedHeadersConfigValue(actions, "inject_request_headers"); err != nil {
			return ProxyMITMHookConfig{}, err
		}
		if hook.Actions.AWSSigV4, err = awsSigV4ConfigValue(actions, "aws_sigv4", "network_proxy.mitm_hooks[].actions"); err != nil {
			return ProxyMITMHookConfig{}, err
		}
		if hook.Actions.BodySecretSubstitution, err = bodySecretSubstitutionConfigValue(actions, "body_secret_substitution", "network_proxy.mitm_hooks[].actions"); err != nil {
			return ProxyMITMHookConfig{}, err
		}
	}
	return hook, nil
}

// awsSigV4ConfigValue parses an aws_sigv4 hook action table.
func awsSigV4ConfigValue(values map[string]any, key string, path string) (*AWSSigV4Config, error) {
	raw, exists := values[key]
	if !exists || raw == nil {
		return nil, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s.%s must be a table", path, key)
	}
	configPath := path + "." + key
	if err := rejectProxyUnknownFields(table, configPath, proxyAWSSigV4AllowedKeys); err != nil {
		return nil, err
	}
	config := &AWSSigV4Config{Port: awsSigV4DefaultPort, MaxBodyBytes: DefaultAwsSigV4MaxBodyBytes}
	required := map[string]*string{
		"placeholder_access_key_id": &config.PlaceholderAccessKeyID,
		"region":                    &config.Region,
		"service":                   &config.Service,
	}
	for name, target := range required {
		value, ok := stringConfigValue(table, name)
		if !ok {
			return nil, fmt.Errorf("%s.%s must be a string", configPath, name)
		}
		*target = value
	}
	for name, target := range map[string]*ProxySecretSource{
		"access_key_id":     &config.AccessKeyID,
		"secret_access_key": &config.SecretAccessKey,
	} {
		source, err := proxySecretSourceConfigValue(table, name, configPath)
		if err != nil {
			return nil, err
		}
		if source == nil {
			return nil, fmt.Errorf("%s.%s must define a secret source", configPath, name)
		}
		*target = *source
	}
	sessionToken, err := proxySecretSourceConfigValue(table, "session_token", configPath)
	if err != nil {
		return nil, err
	}
	config.SessionToken = sessionToken
	if value, exists := table["port"]; exists {
		port, ok := numberConfigValue(value)
		if !ok {
			return nil, fmt.Errorf("%s.port must be an integer", configPath)
		}
		config.Port = port
	}
	if value, exists := table["max_body_bytes"]; exists {
		size, ok := numberConfigValue(value)
		if !ok {
			return nil, fmt.Errorf("%s.max_body_bytes must be an integer", configPath)
		}
		config.MaxBodyBytes = size
	}
	return config, nil
}

// bodySecretSubstitutionConfigValue parses a body_secret_substitution action.
func bodySecretSubstitutionConfigValue(values map[string]any, key string, path string) (*BodySecretSubstitutionConfig, error) {
	raw, exists := values[key]
	if !exists || raw == nil {
		return nil, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s.%s must be a table", path, key)
	}
	configPath := path + "." + key
	if err := rejectProxyUnknownFields(table, configPath, proxyBodySubstitutionAllowedKeys); err != nil {
		return nil, err
	}
	config := &BodySecretSubstitutionConfig{Port: bodySubstitutionDefaultPort, MaxBodyBytes: DefaultBodySubstitutionMaxBodyBytes}
	rawReplacements, exists := table["replacements"]
	if !exists {
		return nil, fmt.Errorf("%s.replacements must be an array", configPath)
	}
	entries, ok := tableListFromAny(rawReplacements)
	if !ok {
		return nil, fmt.Errorf("%s.replacements must be an array", configPath)
	}
	for index, entry := range entries {
		entryPath := fmt.Sprintf("%s.replacements[%d]", configPath, index)
		if err := rejectProxyUnknownFields(entry, entryPath, proxyBodySubstitutionReplacementAllowedKeys); err != nil {
			return nil, err
		}
		placeholder, ok := stringConfigValue(entry, "placeholder")
		if !ok {
			return nil, fmt.Errorf("%s.placeholder must be a string", entryPath)
		}
		secret, err := proxySecretSourceConfigValue(entry, "secret", entryPath)
		if err != nil {
			return nil, err
		}
		if secret == nil {
			return nil, fmt.Errorf("%s.secret must define a secret source", entryPath)
		}
		config.Replacements = append(config.Replacements, BodySecretReplacement{Placeholder: placeholder, Secret: *secret})
	}
	if value, exists := table["port"]; exists {
		port, ok := numberConfigValue(value)
		if !ok {
			return nil, fmt.Errorf("%s.port must be an integer", configPath)
		}
		config.Port = port
	}
	if value, exists := table["max_body_bytes"]; exists {
		size, ok := numberConfigValue(value)
		if !ok {
			return nil, fmt.Errorf("%s.max_body_bytes must be an integer", configPath)
		}
		config.MaxBodyBytes = size
	}
	return config, nil
}

func proxySecretSourceConfigValue(values map[string]any, key string, path string) (*ProxySecretSource, error) {
	raw, exists := values[key]
	if !exists || raw == nil {
		return nil, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s.%s must be a table", path, key)
	}
	if len(table) != 1 {
		return nil, fmt.Errorf("%s.%s must define exactly one of env_var or file", path, key)
	}
	if value, ok := table["env_var"]; ok {
		name, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s.%s.env_var must be a string", path, key)
		}
		source := ProxySecretSource{Kind: "env_var", Name: name}
		if err := source.validate(); err != nil {
			return nil, fmt.Errorf("%s.%s: %w", path, key, err)
		}
		return &source, nil
	}
	if value, ok := table["file"]; ok {
		name, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s.%s.file must be a string", path, key)
		}
		parsed, err := parseSecretFile(name)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", path, key, err)
		}
		return &ProxySecretSource{Kind: "file", Name: parsed}, nil
	}
	return nil, fmt.Errorf("%s.%s must define exactly one of env_var or file", path, key)
}

func numberConfigValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	}
	return 0, false
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

func portListConfigValue(values map[string]any, key string) ([]int, error) {
	raw, exists := values[key]
	if !exists {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of integers", key)
	}
	ports := make([]int, 0, len(items))
	for _, item := range items {
		port, ok := numberConfigValue(item)
		if !ok {
			return nil, fmt.Errorf("%s must be an array of integers", key)
		}
		ports = append(ports, port)
	}
	return ports, nil
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
		if value, ok := stringConfigValue(table, "placeholder"); ok {
			header.Placeholder = &value
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
	// Rust #46004: an explicitly empty table is a restrictive ceiling, not an
	// omitted policy, so it must survive as a present-but-empty map.
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
