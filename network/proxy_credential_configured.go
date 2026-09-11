package network

// Rust parity: codex-rs/network-proxy/src/credential_broker/configured.rs
// (#44056). Builds the runtime ProxyCredentialProvider for a validated
// CredentialProviderConfig: pattern-matching dummies, destination-backed host
// binding, and bearer/token/basic/custom-header translation.

import (
	"encoding/base64"
	"net/textproto"
	"strings"
)

// ConfiguredCredentialProviders builds one runtime provider per configured
// credential family, in the map's iteration order (the broker only needs a
// stable set; Rust iterates its BTreeMap by id).
func ConfiguredCredentialProviders(configs map[string]CredentialProviderConfig) []*ProxyCredentialProvider {
	if len(configs) == 0 {
		return nil
	}
	out := make([]*ProxyCredentialProvider, 0, len(configs))
	for id, config := range configs {
		provider := ConfiguredCredentialProvider(id, config)
		if provider != nil {
			out = append(out, provider)
		}
	}
	return out
}

// ConfiguredCredentialProvider builds the runtime provider for one configured
// credential family (Rust ConfiguredCredentialProvider).
func ConfiguredCredentialProvider(id string, config CredentialProviderConfig) *ProxyCredentialProvider {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	destinations, err := ParseCredentialDestinations(config)
	if err != nil {
		return nil
	}
	patterns := append([]string(nil), config.Patterns...)
	provider := &ProxyCredentialProvider{
		ContextEnvVars: append([]string(nil), config.Env...),
		Destinations:   destinations,
		DummyValue: func(realValue string) string {
			return configuredDummyValue(patterns, realValue)
		},
		RequestHeaderValue: func(value string) (string, bool) {
			return configuredRequestHeaderValue(config, value)
		},
		RequestHeader: func(headers map[string][]string) (string, bool) {
			return configuredRequestHeader(config, headers)
		},
		InsertHeader: func(headers map[string][]string, value string) {
			configuredInsertHeader(config, headers, value)
		},
	}
	provider.Sources = []ProxyCredentialSource{{
		EnvVars:     append([]string(nil), config.Env...),
		HostBinding: configuredHostBinding(config, destinations),
	}}
	return provider
}

func configuredDummyValue(patterns []string, realValue string) string {
	for _, pattern := range patterns {
		matches, err := credentialPatternMatches(pattern, realValue)
		if err != nil || !matches {
			continue
		}
		if dummy, ok := GenerateCredentialDummy(pattern, realValue); ok {
			return dummy
		}
	}
	return ""
}

func configuredHostBinding(config CredentialProviderConfig, static []CredentialDestination) func(map[string]string) (ProxyCredentialHostBinding, bool) {
	return func(env map[string]string) (ProxyCredentialHostBinding, bool) {
		destinations := append([]CredentialDestination(nil), static...)
		if config.URLPrefixFromEnv != nil {
			if value := strings.TrimSpace(env[*config.URLPrefixFromEnv]); value != "" {
				if dynamic, err := parseCredentialDestination(value); err == nil {
					destinations = append(destinations, dynamic)
				}
			}
		}
		hosts := make([]string, 0, len(destinations))
		seen := map[string]bool{}
		for _, destination := range destinations {
			host := NormalizeProxyHost(destination.Host)
			if host == "" || seen[host] {
				continue
			}
			seen[host] = true
			hosts = append(hosts, host)
		}
		if len(hosts) == 0 {
			return ProxyCredentialHostBinding{}, false
		}
		return ProxyCredentialHostBinding{ExactHosts: hosts}, true
	}
}

// configuredHeaderName returns the header the provider injects under the first
// supported auth method (Authorization for bearer/token/basic, the configured
// header for header auth).
func configuredHeaderName(config CredentialProviderConfig) string {
	for _, method := range config.Auth {
		if method == CredentialAuthHeader {
			if config.Header != nil {
				return textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(*config.Header))
			}
			return ""
		}
		if method == CredentialAuthBearer || method == CredentialAuthToken || method == CredentialAuthBasic {
			return textproto.CanonicalMIMEHeaderKey("authorization")
		}
	}
	return ""
}

func configuredRequestHeaderValue(config CredentialProviderConfig, value string) (string, bool) {
	for _, method := range config.Auth {
		var headerValue string
		switch method {
		case CredentialAuthBearer:
			headerValue = "Bearer " + value
		case CredentialAuthToken:
			headerValue = "token " + value
		case CredentialAuthBasic:
			headerValue = "Basic " + base64.StdEncoding.EncodeToString([]byte(value))
		case CredentialAuthHeader:
			prefix := ""
			if config.Prefix != nil {
				prefix = *config.Prefix
			}
			headerValue = prefix + value
		default:
			continue
		}
		if err := validateHeaderValue(headerValue); err != nil {
			continue
		}
		return headerValue, true
	}
	return "", false
}

func configuredRequestHeader(config CredentialProviderConfig, headers map[string][]string) (string, bool) {
	name := configuredHeaderName(config)
	if name == "" {
		return "", false
	}
	values := headerValuesIgnoreCase(headers, name)
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func configuredInsertHeader(config CredentialProviderConfig, headers map[string][]string, value string) {
	name := configuredHeaderName(config)
	if name == "" {
		return
	}
	headers[name] = []string{value}
}

func headerValuesIgnoreCase(headers map[string][]string, name string) []string {
	if values := headers[name]; len(values) > 0 {
		return values
	}
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values
		}
	}
	return nil
}
