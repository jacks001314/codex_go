package config

import (
	"fmt"
	"os"
	"strings"

	"codex_go/sandbox"
)

// RemoteSandboxConfig mirrors Rust RemoteSandboxConfigToml
// (config/src/config_requirements.rs): hostname-pattern-scoped sandbox mode
// requirements selected from the execution host name.
type RemoteSandboxConfig struct {
	HostnamePatterns    []string
	AllowedSandboxModes []sandbox.SandboxMode
}

// HostnameResolver returns the execution host name (Rust hostname_resolver).
// Defaults to os.Hostname when nil.
type HostnameResolver func() string

// applyRemoteSandboxConfig mirrors Rust apply_remote_sandbox_config: when a
// remote_sandbox_config entry's hostname pattern matches the execution host,
// its allowed_sandbox_modes replace the layer's top-level value. Only the
// first matching entry applies.
func applyRemoteSandboxConfig(out *ConfigRequirements, remoteConfigs []RemoteSandboxConfig, resolver HostnameResolver) {
	if out == nil || len(remoteConfigs) == 0 {
		return
	}
	hostname := ""
	if resolver != nil {
		hostname = resolver()
	} else {
		hostname = hostnameFromOS()
	}
	normalizedHost, ok := normalizeHostname(hostname)
	if !ok {
		return
	}
	for _, config := range remoteConfigs {
		if hostnameMatchesAnyPattern(normalizedHost, config.HostnamePatterns) {
			out.AllowedSandboxModes = append([]sandbox.SandboxMode(nil), config.AllowedSandboxModes...)
			return
		}
	}
}

// hostnameFromOS returns the execution host name (Rust hostname_resolver
// default). Kept separate so the parity differential can inject a
// deterministic hostname through ConfigRequirementsFromMapWithHostname.
func hostnameFromOS() string {
	if host, err := os.Hostname(); err == nil {
		return host
	}
	return ""
}

// ConfigRequirementsFromMapWithHostname parses the raw requirements map and
// applies remote_sandbox_config selection against the provided hostname,
// mirroring Rust apply_remote_sandbox_config(hostname: Option<&str>). An empty
// hostname skips remote selection (Rust None), keeping the top-level
// allowed_sandbox_modes. This is the deterministic entry point used by the
// parity shared-fixture differential; production paths keep the os.Hostname()
// fallback.
func ConfigRequirementsFromMapWithHostname(values map[string]any, hostname string) (*ConfigRequirements, error) {
	return configRequirementsFromMapWithHostname(values, hostname)
}

func configRequirementsFromMapWithHostname(values map[string]any, hostname string) (*ConfigRequirements, error) {
	remoteConfigs, err := parseRemoteSandboxConfigs(values)
	if err != nil {
		return nil, err
	}
	return configRequirementsFromMapWithResolver(values, remoteConfigs, func() string { return hostname })
}

// normalizeHostname mirrors Rust normalize_hostname: trim, strip trailing dot,
// lowercase ASCII; empty results in None.
func normalizeHostname(hostname string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(hostname))
	normalized = strings.TrimSuffix(normalized, ".")
	if normalized == "" {
		return "", false
	}
	return normalized, true
}

// hostnameMatchesAnyPattern mirrors Rust hostname_matches_any_pattern with
// WildMatchPattern::<*, ?>::new_case_insensitive semantics: '*' matches any
// sequence, '?' matches one character, comparison is case-insensitive.
func hostnameMatchesAnyPattern(hostname string, patterns []string) bool {
	for _, pattern := range patterns {
		normalized, ok := normalizeHostname(pattern)
		if !ok {
			continue
		}
		if wildcardMatchCaseInsensitive(hostname, normalized) {
			return true
		}
	}
	return false
}

func wildcardMatchCaseInsensitive(value string, pattern string) bool {
	return wildcardMatch(strings.ToLower(value), strings.ToLower(pattern))
}

func wildcardMatch(value string, pattern string) bool {
	v, p := 0, 0
	star := -1
	match := 0
	for v < len(value) {
		if p < len(pattern) && (pattern[p] == '?' || pattern[p] == value[v]) {
			v++
			p++
		} else if p < len(pattern) && pattern[p] == '*' {
			star = p
			match = v
			p++
		} else if star >= 0 {
			p = star + 1
			match++
			v = match
		} else {
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// parseRemoteSandboxConfigs reads the `remote_sandbox_config` table from the
// raw requirements map, validating the Rust field shape (hostname_patterns
// must be a list, allowed_sandbox_modes must be a list of mode strings).
func parseRemoteSandboxConfigs(values map[string]any) ([]RemoteSandboxConfig, error) {
	raw, ok := values["remote_sandbox_config"]
	if !ok {
		return nil, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		if _, isSingle := raw.(map[string]any); isSingle {
			return nil, fmt.Errorf("remote_sandbox_config must be a list of tables")
		}
		return nil, nil
	}
	configs := make([]RemoteSandboxConfig, 0, len(entries))
	for _, entry := range entries {
		table, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		rawPatterns, hasPatterns := table["hostname_patterns"]
		if !hasPatterns {
			rawPatterns, hasPatterns = table["hostnamePatterns"]
		}
		if !hasPatterns {
			return nil, fmt.Errorf("remote_sandbox_config entry requires hostname_patterns list")
		}
		patterns, ok := rawPatterns.([]any)
		if !ok {
			return nil, fmt.Errorf("remote_sandbox_config entry requires hostname_patterns list")
		}
		hostnamePatterns := make([]string, 0, len(patterns))
		for _, pattern := range patterns {
			hostnamePatterns = append(hostnamePatterns, fmt.Sprintf("%v", pattern))
		}
		rawModes, hasModes := table["allowed_sandbox_modes"]
		if !hasModes {
			rawModes, hasModes = table["allowedSandboxModes"]
		}
		if !hasModes {
			return nil, fmt.Errorf("remote_sandbox_config entry requires allowed_sandbox_modes list")
		}
		rawModeList, ok := rawModes.([]any)
		if !ok {
			return nil, fmt.Errorf("remote_sandbox_config entry requires allowed_sandbox_modes list")
		}
		sandboxModes := make([]sandbox.SandboxMode, 0, len(rawModeList))
		for _, mode := range rawModeList {
			sandboxModes = append(sandboxModes, sandbox.SandboxMode(fmt.Sprintf("%v", mode)))
		}
		configs = append(configs, RemoteSandboxConfig{
			HostnamePatterns:    hostnamePatterns,
			AllowedSandboxModes: sandboxModes,
		})
	}
	return configs, nil
}
