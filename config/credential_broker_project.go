package config

import (
	"runtime"
	"sort"
	"strings"

	"codex_go/network"
)

// Rust parity: codex-rs/config/src/loader/mod.rs CredentialBrokerProjectState and
// its credential_broker_provider_env_keys. A trusted project must not be able to
// reconfigure the network proxy credential broker or rebind the environment
// variables that the trusted layers already bound to credentials.

// CredentialBrokerProjectState is whether the trusted configuration configures
// the network-proxy credential broker.
type CredentialBrokerProjectState string

const (
	CredentialBrokerProjectUnconfigured CredentialBrokerProjectState = "unconfigured"
	CredentialBrokerProjectEnabled      CredentialBrokerProjectState = "enabled"
	CredentialBrokerProjectDisabled     CredentialBrokerProjectState = "disabled"
)

// CredentialBrokerProjectStateForValues derives the broker state from the
// trusted configuration: only an explicit
// `features.network_proxy.credential_broker = true` configures the broker, which
// is enabled when the network proxy is enabled and disabled otherwise.
func CredentialBrokerProjectStateForValues(values map[string]any) CredentialBrokerProjectState {
	networkProxy, ok := nestedConfigTable(values, "features", "network_proxy")
	if !ok {
		return CredentialBrokerProjectUnconfigured
	}
	if configured, ok := networkProxy["credential_broker"].(bool); !ok || !configured {
		return CredentialBrokerProjectUnconfigured
	}
	if enabled, ok := networkProxy["enabled"].(bool); ok && enabled {
		return CredentialBrokerProjectEnabled
	}
	return CredentialBrokerProjectDisabled
}

// CredentialBrokerProviderEnvKeys returns the environment keys the trusted
// configuration binds to credential providers: each provider's `env` entries
// plus its `url_prefix_from_env`, in provider-id order.
func CredentialBrokerProviderEnvKeys(values map[string]any) []string {
	providers, ok := nestedConfigTable(values, "features", "network_proxy", "credentials")
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	keys := []string{}
	seen := map[string]bool{}
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		keys = append(keys, key)
	}
	for _, id := range ids {
		provider, ok := providers[id].(map[string]any)
		if !ok {
			continue
		}
		switch env := provider["env"].(type) {
		case []any:
			for _, entry := range env {
				if value, ok := entry.(string); ok {
					add(value)
				}
			}
		case []string:
			for _, value := range env {
				add(value)
			}
		}
		if value, ok := provider["url_prefix_from_env"].(string); ok {
			add(value)
		}
	}
	return keys
}

// nestedConfigTable walks a config table path.
func nestedConfigTable(values map[string]any, path ...string) (map[string]any, bool) {
	current := values
	for _, key := range path {
		if current == nil {
			return nil, false
		}
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, current != nil
}

// isCredentialBrokerProviderEnvKey reports whether a built-in credential
// provider binds the environment key (Rust
// credential_broker::environment::is_credential_broker_provider_env_key).
func isCredentialBrokerProviderEnvKey(key string) bool {
	for _, candidate := range network.ProxyCredentialBrokerEnvKeys() {
		if credentialEnvKeyMatches(key, candidate) {
			return true
		}
	}
	return false
}

// credentialEnvKeyMatches mirrors Rust's env_key_matches: exact on Unix and
// case-insensitive on Windows.
func credentialEnvKeyMatches(left string, right string) bool {
	if left == right {
		return true
	}
	return runtime.GOOS == "windows" && strings.EqualFold(left, right)
}
