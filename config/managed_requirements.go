package config

import (
	"os"
	"path/filepath"
	"strings"
)

// LoadManagedRequirements loads the current managed requirements without
// reading system defaults, user or project settings, or thread-provided
// configuration (Rust #44944 loader::load_managed_requirements_state). The
// legacy managed_config.toml still participates, matching Rust's
// requirements_layers_from_legacy_scheme: a malformed file fails the load, but
// it only contributes approval and sandbox requirements, never model provider
// policy.
func LoadManagedRequirements(codexHome string, cloudBundle *CloudConfigBundle) (*ConfigRequirements, error) {
	if path, ok := managedConfigPathForRuntime(codexHome); ok {
		if _, _, err := loadConfigFileIfExists(path); err != nil {
			return nil, err
		}
	}
	requirements, err := LoadRequirementsFile(requirementsPathForHome(codexHome))
	if err != nil {
		return nil, err
	}
	if cloudBundle != nil && !cloudBundle.IsEmpty() {
		values := map[string]any{}
		requirements, err = applyCloudConfigBundle(values, requirements, *cloudBundle, codexHome)
		if err != nil {
			return nil, err
		}
	}
	return requirements, nil
}

func requirementsPathForHome(codexHome string) string {
	home := strings.TrimSpace(codexHome)
	if home == "" {
		return "requirements.toml"
	}
	return filepath.Join(home, "requirements.toml")
}

// managedConfigPathForRuntime resolves the legacy managed config path the same
// way ConfigService.loadManagedConfigLayerFromEnv does, reporting whether a
// file should be consulted at all (Windows ignores the default location,
// Rust #38947).
func managedConfigPathForRuntime(codexHome string) (string, bool) {
	override, ok := os.LookupEnv(appServerManagedConfigPathEnv)
	if ok && strings.TrimSpace(override) != "" {
		return managedConfigPath(codexHome, override), true
	}
	if shouldIgnoreDefaultManagedConfig("") {
		return "", false
	}
	return managedConfigPath(codexHome, ""), true
}
