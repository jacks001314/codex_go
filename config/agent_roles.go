package config

// Rust parity: codex-rs/agent-roles/src/loader.rs (load_agent_roles),
// discovery.rs (collect_agent_role_files), and agent_role_config.rs
// (parse_agent_role_file_contents). Go previously parsed a single merged
// `[agents]` table lazily and treated every malformed definition as a hard
// error; Rust builds roles per config layer, discovers role files under each
// layer's `<config_folder>/agents` tree, and reports malformed definitions as
// startup warnings while skipping them.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"codex_go/agent"
)

// agentRoleReservedKeys lists the `[agents]` keys that configure the agent
// subsystem rather than declaring a role (Rust AgentsToml's named fields).
var agentRoleReservedKeys = map[string]bool{
	"enabled": true, "max_concurrent_threads_per_session": true, "max_threads": true,
	"max_depth": true, "default_subagent_model": true,
	"default_subagent_reasoning_effort": true, "job_max_runtime_seconds": true,
	"interrupt_message": true,
}

// LoadAgentRoles mirrors Rust's load_agent_roles for a config layer stack: each
// layer contributes its declared `[agents.<name>]` roles plus the role files
// discovered under `<config_folder>/agents`, higher layers win, and a role that
// only fills fields missing in a lower layer keeps the lower layer's values.
//
// Malformed definitions are reported as "Ignoring malformed agent role
// definition: ..." warnings instead of failing the load, matching Rust.
func LoadAgentRoles(layers []Layer) (map[string]agent.RoleConfig, []string, error) {
	roles := map[string]agent.RoleConfig{}
	var warnings []string
	for _, layer := range layersLowToHigh(layers) {
		layerRoles := map[string]agent.RoleConfig{}
		declaredFiles := map[string]bool{}
		configFolder := agentRoleConfigFolder(layer.Name)
		values := layerConfigMap(layer)
		if agentsTable, ok := values["agents"].(map[string]any); ok {
			for _, declaredName := range sortedAnyKeys(agentsTable) {
				if agentRoleReservedKeys[declaredName] {
					continue
				}
				roleName, role, err := parseAgentRoleConfig(declaredName, agentsTable[declaredName], configFolder)
				if err != nil {
					warnings = append(warnings, agentRoleWarning(err))
					continue
				}
				if role.ConfigFile != "" {
					declaredFiles[normalizedAgentRolePath(role.ConfigFile)] = true
				}
				if _, duplicate := layerRoles[roleName]; duplicate {
					warnings = append(warnings, agentRoleWarning(fmt.Errorf(
						"duplicate agent role name `%s` declared in the same config layer", roleName)))
					continue
				}
				layerRoles[roleName] = role
			}
		}
		if configFolder != "" {
			discovered, discoveryWarnings, err := discoverAgentRolesInDir(filepath.Join(configFolder, "agents"), declaredFiles)
			if err != nil {
				return nil, warnings, err
			}
			warnings = append(warnings, discoveryWarnings...)
			for _, roleName := range sortedRoleNames(discovered) {
				if _, duplicate := layerRoles[roleName]; duplicate {
					warnings = append(warnings, agentRoleWarning(fmt.Errorf(
						"duplicate agent role name `%s` declared in the same config layer", roleName)))
					continue
				}
				layerRoles[roleName] = discovered[roleName]
			}
		}
		for _, roleName := range sortedRoleNames(layerRoles) {
			role := layerRoles[roleName]
			if existing, ok := roles[roleName]; ok {
				mergeMissingAgentRoleFields(&role, existing)
			}
			if strings.TrimSpace(role.Description) == "" {
				warnings = append(warnings, agentRoleWarning(fmt.Errorf(
					"agent role `%s` must define a description", roleName)))
				continue
			}
			roles[roleName] = role
		}
	}
	if len(roles) == 0 {
		roles = nil
	}
	return roles, warnings, nil
}

// discoverAgentRolesInDir mirrors Rust's discover_agent_roles_in_dir: every
// `.toml` under `<config_folder>/agents` (recursively, sorted) is a role unless
// a declared role already named that file.
func discoverAgentRolesInDir(dir string, declaredFiles map[string]bool) (map[string]agent.RoleConfig, []string, error) {
	files, err := collectAgentRoleFiles(dir)
	if err != nil {
		return nil, nil, err
	}
	roles := map[string]agent.RoleConfig{}
	var warnings []string
	for _, file := range files {
		if declaredFiles[normalizedAgentRolePath(file)] {
			continue
		}
		role, roleName, err := parseAgentRoleFile(file, "")
		if err != nil {
			warnings = append(warnings, agentRoleWarning(err))
			continue
		}
		if _, duplicate := roles[roleName]; duplicate {
			warnings = append(warnings, agentRoleWarning(fmt.Errorf(
				"duplicate agent role name `%s` discovered in %s", roleName, dir)))
			continue
		}
		role.ConfigFile = file
		roles[roleName] = role
	}
	return roles, warnings, nil
}

// collectAgentRoleFiles mirrors Rust's collect_agent_role_files: a recursive
// walk of the agents directory, sorted, tolerating a missing directory.
func collectAgentRoleFiles(dir string) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	var files []string
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(path), ".toml") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// mergeMissingAgentRoleFields mirrors Rust's merge_missing_role_fields: a
// higher layer only supplies fields the role does not define itself.
func mergeMissingAgentRoleFields(role *agent.RoleConfig, fallback agent.RoleConfig) {
	if role.Description == "" {
		role.Description = fallback.Description
	}
	if role.ConfigFile == "" {
		role.ConfigFile = fallback.ConfigFile
	}
	if role.NicknameCandidates == nil {
		role.NicknameCandidates = fallback.NicknameCandidates
	}
	if role.Settings == nil {
		role.Settings = fallback.Settings
	}
}

// agentRoleConfigFolder mirrors Rust ConfigLayerSource::config_folder: the
// directory that hosts supplementary config such as the `agents` role files.
func agentRoleConfigFolder(source LayerSource) string {
	switch source.Type {
	case LayerSourceProject:
		return strings.TrimSpace(source.DotCodexFolder)
	case LayerSourceUser, LayerSourceSystem:
		file := strings.TrimSpace(source.File)
		if file == "" {
			return ""
		}
		return filepath.Dir(file)
	default:
		return ""
	}
}

func agentRoleWarning(err error) string {
	return fmt.Sprintf("Ignoring malformed agent role definition: %s", err)
}

func normalizedAgentRolePath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

func sortedRoleNames(roles map[string]agent.RoleConfig) []string {
	if len(roles) == 0 {
		return nil
	}
	names := make([]string, 0, len(roles))
	for name := range roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// AgentRolesForCWD loads the agent-role catalog from the config layers visible
// from cwd, mirroring Rust's load_agent_roles over the layer stack, and returns
// the warnings for malformed definitions alongside the roles.
func (s *ConfigService) AgentRolesForCWD(cwd string) (map[string]agent.RoleConfig, []string) {
	if s == nil {
		return nil, nil
	}
	layers, err := s.configLayersForWarning(cwd)
	if err != nil {
		return nil, nil
	}
	roles, warnings, err := LoadAgentRoles(layers)
	if err != nil {
		return nil, warnings
	}
	return roles, warnings
}
