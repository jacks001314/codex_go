package plugin

// Rust parity: codex-rs/core-plugins/src/agent_plugin_mcp_overlay.rs (#40363).
// Applies local env_vars declarations from .codex-plugin/plugin.json to matching
// stdio servers loaded from an Agent Plugin manifest. For each matching server,
// ${NAME} references in the portable server environment whose name is declared
// by the overlay are replaced with host-environment forwarding, while preserving
// the portable command, arguments, and unrelated servers. Remote-sourced env
// vars are ignored.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func applyCodexEnvOverlay(pluginRoot string, configs map[string]map[string]any) {
	if len(configs) == 0 {
		return
	}
	if _, err := os.Stat(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")); err != nil {
		return
	}
	overlayServers := readLegacyMCPServerConfigs(pluginRoot)
	if len(overlayServers) == 0 {
		return
	}
	for name, overlayServer := range overlayServers {
		agentServer, ok := configs[name]
		if !ok || !isStdioServerConfig(agentServer) {
			continue
		}
		overlayEnvVars := filterRemoteSourceEnvVars(configEnvVars(overlayServer))
		if len(overlayEnvVars) == 0 {
			continue
		}
		agentEnv := configEnvMap(agentServer)
		for key, value := range agentEnv {
			reference, ok := envReference(value)
			if !ok {
				continue
			}
			if overlayEnvVarMatches(overlayEnvVars, key, reference) {
				delete(agentEnv, key)
			}
		}
		agentServer["env"] = stringMapToAny(agentEnv)
		agentServer["env_vars"] = append(configEnvVars(agentServer), overlayEnvVars...)
		configs[name] = agentServer
	}
}

func isStdioServerConfig(config map[string]any) bool {
	return configString(config, "command") != "" && configString(config, "url") == ""
}

func configEnvMap(config map[string]any) map[string]string {
	env := map[string]string{}
	switch typed := config["env"].(type) {
	case map[string]any:
		for k, v := range typed {
			if sv, ok := v.(string); ok {
				env[k] = sv
			}
		}
	case map[string]string:
		for k, v := range typed {
			env[k] = v
		}
	}
	return env
}

func configString(config map[string]any, key string) string {
	if value, ok := config[key].(string); ok {
		return value
	}
	return ""
}

// envReference extracts the referenced variable name from a "${NAME}" value.
func envReference(value string) (string, bool) {
	if len(value) < 3 || value[0] != '$' || value[1] != '{' || value[len(value)-1] != '}' {
		return "", false
	}
	return value[2 : len(value)-1], true
}

func overlayEnvVarMatches(envVars []map[string]any, envKey string, reference string) bool {
	for _, variable := range envVars {
		name := strings.TrimSpace(configString(variable, "name"))
		if name == "" {
			continue
		}
		if environmentNamesMatch(envKey, name) && environmentNamesMatch(reference, name) {
			return true
		}
	}
	return false
}

func environmentNamesMatch(left string, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func filterRemoteSourceEnvVars(envVars []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(envVars))
	for _, variable := range envVars {
		if isRemoteSourceEnvVar(variable) {
			continue
		}
		out = append(out, variable)
	}
	return out
}

func isRemoteSourceEnvVar(variable map[string]any) bool {
	source := strings.ToLower(strings.TrimSpace(configString(variable, "source")))
	return source == "remote" || source == "host.remote" || strings.HasPrefix(source, "remote.")
}

func readLegacyMCPServerConfigs(pluginRoot string) map[string]map[string]any {
	path := filepath.Join(pluginRoot, ".mcp.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var payload struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if json.Unmarshal(contents, &payload) != nil || len(payload.MCPServers) == 0 {
		return nil
	}
	return payload.MCPServers
}

func configEnvVars(config map[string]any) []map[string]any {
	var out []map[string]any
	switch typed := config["env_vars"].(type) {
	case []map[string]any:
		out = append(out, typed...)
	case []any:
		for _, item := range typed {
			if entry, ok := item.(map[string]any); ok {
				out = append(out, entry)
			} else if entryStr, ok := item.(string); ok && strings.TrimSpace(entryStr) != "" {
				out = append(out, map[string]any{"name": entryStr})
			}
		}
	}
	return out
}

func stringMapToAny(env map[string]string) map[string]any {
	out := make(map[string]any, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}
