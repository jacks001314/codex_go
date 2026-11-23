package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/features"
)

// Rust #62690: features.daemon_updates is requirements-only, so user and CLI
// settings cannot disable it.
func TestDaemonUpdatesIgnoreUserConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\ndaemon_updates = false\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadEffectiveWithOptions(home, &EffectiveOptions{StrictConfig: true})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions returned error: %v", err)
	}
	if !features.Enabled(cfg.FeatureSettings(), "daemon_updates") {
		t.Fatal("user config disabled daemon_updates; want the default true")
	}

	cfg.Requirements = &ConfigRequirements{FeatureRequirements: map[string]bool{"daemon_updates": false}}
	if features.Enabled(cfg.FeatureSettings(), "daemon_updates") {
		t.Fatal("managed requirement did not override the default")
	}
}

// Rust #62690 rejects the daemon update gate in fetched policy: only the
// machine's own managed requirements may set it.
func TestCloudRequirementsRejectDaemonUpdatesLikeRust(t *testing.T) {
	for _, contents := range []string{
		"[features]\ndaemon_updates = false\n",
		"[feature_requirements]\ndaemon_updates = true\n",
	} {
		bundle := CloudConfigBundle{
			RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
				ID:       "remote",
				Name:     "remote",
				Contents: contents,
			}}},
		}
		if _, err := applyCloudConfigBundle(map[string]any{}, nil, bundle, t.TempDir()); err == nil ||
			!errors.Is(err, ErrInvalidCloudConfig) ||
			!strings.Contains(err.Error(), "features.daemon_updates may only be configured in local managed requirements") {
			t.Fatalf("applyCloudConfigBundle(%q) error = %v, want the cloud rejection", contents, err)
		}
	}
}
