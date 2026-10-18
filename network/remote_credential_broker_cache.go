package network

import (
	"encoding/json"
	"sync"
)

// RemoteCredentialBrokerCache retains credential mappings across process
// launches within one executor session. Proxy listeners, policy, and approvals
// remain per command.
type RemoteCredentialBrokerCache struct {
	mu      sync.Mutex
	entries []remoteCredentialBrokerEntry
}

type remoteCredentialBrokerEntry struct {
	environmentID  string
	conversationID string
	configKey      string
	broker         *ProxyCredentialBroker
}

// BrokerIfEnabled returns a broker shared by commands with the same executor
// environment, conversation, and broker configuration.
func (c *RemoteCredentialBrokerCache) BrokerIfEnabled(environmentID string, conversationID string, settings ProxySettings, env map[string]string) *ProxyCredentialBroker {
	if c == nil || !CredentialBrokerEnabledForSettings(settings) {
		return nil
	}
	configKey := remoteCredentialBrokerConfigKey(settings, env)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		if entry.environmentID == environmentID &&
			entry.conversationID == conversationID &&
			entry.configKey == configKey {
			return entry.broker
		}
	}
	broker := NewProxyCredentialBrokerWithOpenAIHost(
		true,
		ConfiguredCredentialProviders(settings.CredentialProviders),
		settings.CredentialBrokerOpenAIHost,
	)
	c.entries = append(c.entries, remoteCredentialBrokerEntry{
		environmentID:  environmentID,
		conversationID: conversationID,
		configKey:      configKey,
		broker:         broker,
	})
	return broker
}

func remoteCredentialBrokerConfigKey(settings ProxySettings, env map[string]string) string {
	context := map[string]string{}
	providers := credentialProvidersWithOpenAIHost(settings.CredentialBrokerOpenAIHost)
	providers = append(providers, ConfiguredCredentialProviders(settings.CredentialProviders)...)
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		keys := append([]string(nil), provider.ContextEnvVars...)
		keys = append(keys, provider.DestinationEnvKeys...)
		for _, key := range keys {
			if value, ok := env[key]; ok {
				context[key] = value
			}
		}
	}
	key := struct {
		AllowLocalBinding   bool
		OpenAIHost          string
		DestinationContext  map[string]string
		CredentialProviders any
	}{
		AllowLocalBinding:   settings.AllowLocalBinding,
		OpenAIHost:          settings.CredentialBrokerOpenAIHost,
		DestinationContext:  context,
		CredentialProviders: settings.CredentialProviders,
	}
	data, err := json.Marshal(key)
	if err != nil {
		return ""
	}
	return string(data)
}
