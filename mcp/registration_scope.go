package mcp

import (
	"sort"
	"strings"
)

// MCP inventory and runtime status are keyed by registration scope so
// same-named servers in different environments stay separate (Rust #61257).
// A local or unnamed registration keeps its bare server name, so legacy
// name-only routing and existing persisted identities keep their meaning; a
// remote registration is distinguished by its environment id.
const serverScopeKeySeparator = "\x00"

// displayMCPServerName strips the registration scope from an internal server
// identity so user-visible messages keep the bare server name.
func displayMCPServerName(name string) string {
	trimmed := strings.TrimSpace(name)
	if index := strings.Index(trimmed, serverScopeKeySeparator); index >= 0 {
		return trimmed[:index]
	}
	return trimmed
}

// ServerRegistrationKey returns the inventory key for one registration scope.
func ServerRegistrationKey(name, environmentID string) string {
	name = strings.TrimSpace(name)
	environmentID = strings.TrimSpace(environmentID)
	if name == "" || environmentID == "" || environmentID == DefaultMCPServerEnvironmentID {
		return name
	}
	return name + serverScopeKeySeparator + environmentID
}

// SplitServerRegistrationKey returns the server name and environment id a
// registration key was built from. An unscoped key reports the local
// environment.
func SplitServerRegistrationKey(key string) (string, string) {
	key = strings.TrimSpace(key)
	name, environmentID, found := strings.Cut(key, serverScopeKeySeparator)
	if !found {
		if name == "" {
			return "", ""
		}
		return name, DefaultMCPServerEnvironmentID
	}
	if environmentID == "" {
		environmentID = DefaultMCPServerEnvironmentID
	}
	return name, environmentID
}

// RegistrationEnvironmentID returns the environment scope a registration
// owns. The registration config is authoritative; an unset environment means
// the local environment.
func RegistrationEnvironmentID(registration *ServerRegistration) string {
	if registration == nil {
		return DefaultMCPServerEnvironmentID
	}
	if registration.Config.EffectiveEnvironmentID() != "" {
		return registration.Config.EffectiveEnvironmentID()
	}
	return DefaultMCPServerEnvironmentID
}

// RegistrationKey derives the inventory key a registration must be stored
// under, preferring the explicit registration name over an inherited map key.
func RegistrationKey(registration *ServerRegistration) string {
	if registration == nil {
		return ""
	}
	return ServerRegistrationKey(registration.Name, RegistrationEnvironmentID(registration))
}

// sortedRegistrationKeys returns registration keys in attribution order so the
// first registration of a name stays the one name-only routing selects
// (Rust #61257).
func sortedRegistrationKeys(servers map[string]ServerRegistration) []string {
	keys := make([]string, 0, len(servers))
	for key := range servers {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i int, j int) bool {
		left, right := servers[keys[i]], servers[keys[j]]
		if left.ContributionOrder != right.ContributionOrder {
			return left.ContributionOrder < right.ContributionOrder
		}
		if left.SelectionOrder != right.SelectionOrder {
			return left.SelectionOrder < right.SelectionOrder
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return RegistrationEnvironmentID(&left) < RegistrationEnvironmentID(&right)
	})
	return keys
}

// ScopedServerKeys re-keys one registration collection for publication. The
// first registration of a name keeps the bare server name so legacy name-only
// routing and stored identities keep working; every further registration of
// the same name is stored under its scope key (Rust #61257).
func ScopedServerKeys(scoped map[string]ServerRegistration) map[string]ServerRegistration {
	servers := make(map[string]ServerRegistration, len(scoped))
	claimed := make(map[string]bool, len(scoped))
	for _, key := range sortedRegistrationKeys(scoped) {
		registration := scoped[key]
		name := strings.TrimSpace(registration.Name)
		if name == "" {
			name, _ = SplitServerRegistrationKey(key)
			registration.Name = name
		}
		if !claimed[name] {
			claimed[name] = true
			servers[name] = registration
			continue
		}
		scopedKey := ServerRegistrationKey(name, RegistrationEnvironmentID(&registration))
		if scopedKey == name {
			scopedKey = name + serverScopeKeySeparator + DefaultMCPServerEnvironmentID
		}
		servers[scopedKey] = registration
	}
	return servers
}

// ServerRegistrationFor returns the stored registration for one (name,
// environment) scope. It mirrors Rust #61257's scoped lookup: the map key may
// be the bare server name for the first registration, so the scope is read
// from the registration itself.
func (c *RuntimeConfig) ServerRegistrationFor(name, environmentID string) (string, ServerRegistration, bool) {
	if c == nil {
		return "", ServerRegistration{}, false
	}
	name = strings.TrimSpace(name)
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		environmentID = DefaultMCPServerEnvironmentID
	}
	for _, key := range sortedRegistrationKeys(c.Servers) {
		registration := c.Servers[key]
		registrationName := strings.TrimSpace(registration.Name)
		if registrationName == "" {
			registrationName, _ = SplitServerRegistrationKey(key)
		}
		if registrationName != name {
			continue
		}
		if RegistrationEnvironmentID(&registration) != environmentID {
			continue
		}
		return key, registration, true
	}
	return "", ServerRegistration{}, false
}
