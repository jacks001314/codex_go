package network

// Rust parity: codex-rs/network-proxy/src/credential_broker.rs
// CredentialProviderConfig + configured.rs ConfiguredCredentialProvider::compile
// (#44056): declarative, config-backed credential families. This file parses and
// validates the `features.network_proxy.credentials` table; the broker wiring
// (dummy generation, header translation, URL-scoped replacement) builds on it.

import (
	"fmt"
	"net"
	"net/url"
	"runtime"
	"strings"
)

// CredentialAuthMethod is one supported declarative authentication format.
type CredentialAuthMethod string

const (
	CredentialAuthBearer CredentialAuthMethod = "bearer"
	CredentialAuthToken  CredentialAuthMethod = "token"
	CredentialAuthBasic  CredentialAuthMethod = "basic"
	CredentialAuthHeader CredentialAuthMethod = "header"
)

// CredentialProviderConfig mirrors Rust CredentialProviderConfig
// (network-proxy/src/credential_broker/provider_config.rs).
type CredentialProviderConfig struct {
	Env              []string
	Patterns         []string
	URLPrefixes      []string
	URLPrefixFromEnv *string
	Auth             []CredentialAuthMethod
	Header           *string
	Prefix           *string
}

// CredentialDestination is a parsed, authorized injection destination
// (network-proxy/src/credential_broker/destination.rs).
type CredentialDestination struct {
	Scheme     string
	Host       string
	Port       uint16
	PathPrefix string
}

// ParseCredentialProviderConfigs parses and validates the
// `features.network_proxy.credentials` table (map of provider id to config),
// mirroring Rust ConfiguredCredentialProvider::compile so invalid definitions
// fail at the config boundary instead of silently doing nothing.
func ParseCredentialProviderConfigs(raw any) (map[string]CredentialProviderConfig, error) {
	if raw == nil {
		return nil, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("network_proxy.credentials must be a table")
	}
	out := make(map[string]CredentialProviderConfig, len(table))
	for id, value := range table {
		config, err := parseCredentialProviderConfig(id, value)
		if err != nil {
			return nil, err
		}
		out[id] = config
	}
	return out, nil
}

func parseCredentialProviderConfig(id string, value any) (CredentialProviderConfig, error) {
	if strings.TrimSpace(id) == "" {
		return CredentialProviderConfig{}, fmt.Errorf("credential provider name must not be empty")
	}
	table, ok := value.(map[string]any)
	if !ok {
		return CredentialProviderConfig{}, fmt.Errorf("credential provider `%s` must be a table", id)
	}
	config := CredentialProviderConfig{
		Env:         stringListFromCredentialTable(table, "env"),
		Patterns:    stringListFromCredentialTable(table, "patterns"),
		URLPrefixes: stringListFromCredentialTable(table, "url_prefixes"),
	}
	if raw, ok := stringCredentialValue(table, "url_prefix_from_env"); ok {
		config.URLPrefixFromEnv = &raw
	}
	auth, err := credentialAuthMethods(id, table["auth"])
	if err != nil {
		return CredentialProviderConfig{}, err
	}
	config.Auth = auth
	if raw, ok := stringCredentialValue(table, "header"); ok {
		config.Header = &raw
	}
	if raw, ok := stringCredentialValue(table, "prefix"); ok {
		config.Prefix = &raw
	}
	if err := validateCredentialProviderConfig(id, config); err != nil {
		return CredentialProviderConfig{}, err
	}
	return config, nil
}

func validateCredentialProviderConfig(id string, config CredentialProviderConfig) error {
	if len(config.Env) == 0 {
		return fmt.Errorf("credential provider `%s` has no environment keys", id)
	}
	if len(config.Patterns) == 0 {
		return fmt.Errorf("credential provider `%s` has no credential patterns", id)
	}
	if len(config.URLPrefixes) == 0 && config.URLPrefixFromEnv == nil {
		return fmt.Errorf("credential provider `%s` has no destination URL prefixes", id)
	}
	builtin := map[string]bool{}
	for _, key := range ProxyCredentialBrokerEnvKeys() {
		builtin[key] = true
	}
	builtin[CredentialBrokerActiveEnvKey] = true
	builtin[BrokeredCredentialsEnvKey] = true
	for _, key := range config.Env {
		if !validCredentialEnvKey(key) {
			return fmt.Errorf("credential provider `%s` has an invalid environment key", id)
		}
		if builtin[key] {
			return fmt.Errorf("credential provider `%s` overlaps a built-in credential source", id)
		}
	}
	if config.URLPrefixFromEnv != nil {
		key := *config.URLPrefixFromEnv
		if !validCredentialEnvKey(key) {
			return fmt.Errorf("credential provider `%s` has an invalid host environment key", id)
		}
		for _, source := range config.Env {
			if source == key || (runtime.GOOS == "windows" && strings.EqualFold(source, key)) {
				return fmt.Errorf("credential provider `%s` has an invalid host environment key", id)
			}
		}
	}
	for _, prefix := range config.URLPrefixes {
		if _, err := parseCredentialDestination(prefix); err != nil {
			return fmt.Errorf("invalid destination for credential provider `%s`: %w", id, err)
		}
	}
	headerAuth := false
	for _, method := range config.Auth {
		switch method {
		case CredentialAuthBearer, CredentialAuthToken, CredentialAuthBasic:
		case CredentialAuthHeader:
			headerAuth = true
		default:
			return fmt.Errorf("credential provider `%s` has an invalid authentication method %q", id, method)
		}
	}
	if headerAuth != (config.Header != nil) {
		return fmt.Errorf("credential provider `%s` requires a header name exactly when using header authentication", id)
	}
	if config.Prefix != nil && config.Header == nil {
		return fmt.Errorf("credential provider `%s` requires header authentication to use a header prefix", id)
	}
	return nil
}

// ParseCredentialDestinations parses the authorized URL prefixes of a provider.
func ParseCredentialDestinations(config CredentialProviderConfig) ([]CredentialDestination, error) {
	out := make([]CredentialDestination, 0, len(config.URLPrefixes))
	for _, prefix := range config.URLPrefixes {
		destination, err := parseCredentialDestination(prefix)
		if err != nil {
			return nil, err
		}
		out = append(out, destination)
	}
	return out, nil
}

// parseCredentialDestination mirrors CredentialDestination::parse: an explicit
// scheme wins; a bare loopback host implies http; any other bare host implies
// https. A path becomes the authorized path prefix.
func parseCredentialDestination(value string) (CredentialDestination, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return CredentialDestination{}, fmt.Errorf("destination must not be empty")
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return CredentialDestination{}, err
		}
		scheme := strings.ToLower(parsed.Scheme)
		if scheme != "http" && scheme != "https" {
			return CredentialDestination{}, fmt.Errorf("unsupported destination scheme %q", parsed.Scheme)
		}
		host := parsed.Hostname()
		if host == "" {
			return CredentialDestination{}, fmt.Errorf("destination %q has no host", value)
		}
		port := uint16(0)
		if parsed.Port() != "" {
			parsedPort, err := parseCredentialPort(parsed.Port())
			if err != nil {
				return CredentialDestination{}, err
			}
			port = parsedPort
		} else if scheme == "https" {
			port = 443
		} else {
			port = 80
		}
		return CredentialDestination{Scheme: scheme, Host: NormalizeProxyHost(host), Port: port, PathPrefix: normalizeCredentialPathPrefix(parsed.Path)}, nil
	}
	hostPart := value
	pathPart := ""
	if index := strings.IndexAny(hostPart, "/"); index >= 0 {
		hostPart, pathPart = hostPart[:index], hostPart[index:]
	}
	host := hostPart
	explicitPort := uint16(0)
	if parsedHost, parsedPort, err := net.SplitHostPort(hostPart); err == nil {
		host = parsedHost
		parsedPortValue, portErr := parseCredentialPort(parsedPort)
		if portErr != nil {
			return CredentialDestination{}, portErr
		}
		explicitPort = parsedPortValue
	}
	if host == "" {
		return CredentialDestination{}, fmt.Errorf("destination %q has no host", value)
	}
	scheme := "https"
	if isLoopbackCredentialHost(host) {
		scheme = "http"
	}
	port := explicitPort
	if port == 0 {
		if scheme == "https" {
			port = 443
		} else {
			port = 80
		}
	}
	return CredentialDestination{Scheme: scheme, Host: NormalizeProxyHost(host), Port: port, PathPrefix: normalizeCredentialPathPrefix(pathPart)}, nil
}

func parseCredentialPort(value string) (uint16, error) {
	var port int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &port); err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid destination port %q", value)
	}
	return uint16(port), nil
}

func normalizeCredentialPathPrefix(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return ""
	}
	return path
}

func isLoopbackCredentialHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validCredentialEnvKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	for index, character := range key {
		switch {
		case character >= 'A' && character <= 'Z':
		case character >= 'a' && character <= 'z':
		case character == '_':
		case character >= '0' && character <= '9' && index > 0:
		default:
			return false
		}
	}
	return true
}

func credentialAuthMethods(id string, raw any) ([]CredentialAuthMethod, error) {
	if raw == nil {
		return nil, fmt.Errorf("credential provider `%s` has no authentication methods", id)
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("credential provider `%s` auth must be an array", id)
	}
	out := make([]CredentialAuthMethod, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("credential provider `%s` has an invalid authentication method", id)
		}
		out = append(out, CredentialAuthMethod(strings.TrimSpace(text)))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("credential provider `%s` has no authentication methods", id)
	}
	return out, nil
}

func stringListFromCredentialTable(table map[string]any, key string) []string {
	raw, ok := table[key]
	if !ok || raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		if list, ok := raw.([]string); ok {
			return append([]string(nil), list...)
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func stringCredentialValue(table map[string]any, key string) (string, bool) {
	raw, ok := table[key]
	if !ok || raw == nil {
		return "", false
	}
	text, ok := raw.(string)
	if !ok {
		return "", false
	}
	return text, true
}

func cloneCredentialProviders(providers map[string]CredentialProviderConfig) map[string]CredentialProviderConfig {
	if providers == nil {
		return nil
	}
	clone := make(map[string]CredentialProviderConfig, len(providers))
	for id, config := range providers {
		config.Env = append([]string(nil), config.Env...)
		config.Patterns = append([]string(nil), config.Patterns...)
		config.URLPrefixes = append([]string(nil), config.URLPrefixes...)
		config.Auth = append([]CredentialAuthMethod(nil), config.Auth...)
		if config.URLPrefixFromEnv != nil {
			value := *config.URLPrefixFromEnv
			config.URLPrefixFromEnv = &value
		}
		if config.Header != nil {
			value := *config.Header
			config.Header = &value
		}
		if config.Prefix != nil {
			value := *config.Prefix
			config.Prefix = &value
		}
		clone[id] = config
	}
	return clone
}
