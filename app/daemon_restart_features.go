package app

import (
	"fmt"
	"sort"
	"strings"

	"codex_go/cli"
	"codex_go/config"
	"codex_go/features"
)

type daemonRestartFeatureEntry struct {
	name    string
	enabled bool
}

// daemonRestartFeatureOverrides extracts the feature toggles an explicit
// daemon restart should persist. Rust #53751 accepts --enable/--disable,
// boolean features.* overrides, and nested features.<name>.enabled values.
// Unknown features and non-boolean toggles fail before the daemon is stopped.
func daemonRestartFeatureOverrides(root *cli.RootOptions) (map[string]bool, error) {
	overrides, err := config.ParseOverrides(daemonRawLaunchOverrides(root))
	if err != nil {
		return nil, fmt.Errorf("parse daemon restart overrides: %w", err)
	}
	sort.SliceStable(overrides, func(i, j int) bool {
		return overrides[i].Path < overrides[j].Path
	})

	entries := map[string]daemonRestartFeatureEntry{}
	for _, override := range overrides {
		path := strings.TrimSpace(override.Path)
		if path == "features" {
			table, ok := override.Value.(map[string]any)
			if !ok {
				continue
			}
			for name, value := range table {
				if err := addDaemonRestartFeature(entries, name, value); err != nil {
					return nil, err
				}
			}
			continue
		}
		name, ok := strings.CutPrefix(path, "features.")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		if nested, ok := strings.CutSuffix(name, ".enabled"); ok {
			name = nested
		} else if strings.Contains(name, ".") {
			// A nested feature table without an enabled key is ignored, matching
			// Rust's extraction from the features table.
			continue
		}
		if err := addDaemonRestartFeature(entries, name, override.Value); err != nil {
			return nil, err
		}
	}

	result := make(map[string]bool, len(entries))
	for canonical, entry := range entries {
		result[canonical] = entry.enabled
	}
	return result, nil
}

func addDaemonRestartFeature(entries map[string]daemonRestartFeatureEntry, name string, value any) error {
	name = strings.TrimSpace(name)
	enabled, ok := daemonRestartFeatureBool(value)
	if !ok {
		return fmt.Errorf("daemon restart requires a boolean enablement for features.%s", name)
	}
	canonical, ok := features.CanonicalKey(name)
	if !ok {
		return fmt.Errorf("daemon restart requires a boolean feature toggle: %s", name)
	}
	if _, ok := entries[canonical]; !ok || name == canonical {
		entries[canonical] = daemonRestartFeatureEntry{name: name, enabled: enabled}
	}
	return nil
}

func daemonRestartFeatureBool(value any) (bool, bool) {
	if enabled, ok := value.(bool); ok {
		return enabled, true
	}
	if table, ok := value.(map[string]any); ok {
		enabled, ok := table["enabled"].(bool)
		return enabled, ok
	}
	return false, false
}
