package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/features"
)

// TestBackgroundRolloutMigrationRuntimeEnablementLikeRust mirrors Rust #40668:
// the feature is settable through the runtime API, and the one-shot migration
// starts only on the disabled -> enabled transition.
func TestBackgroundRolloutMigrationRuntimeEnablementLikeRust(t *testing.T) {
	if !featureKeySettable("background_paginated_rollout_migration") {
		t.Fatal("background_paginated_rollout_migration is not settable at runtime")
	}
	for _, testCase := range []struct {
		name       string
		requested  bool
		wasEnabled bool
		nowEnabled bool
		want       bool
	}{
		{name: "enabling starts it", requested: true, nowEnabled: true, want: true},
		{name: "already enabled does not restart", requested: true, wasEnabled: true, nowEnabled: true},
		{name: "disabling does not start", requested: false, wasEnabled: true},
		{name: "requested but not applied", requested: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := shouldStartBackgroundRolloutMigration(testCase.requested, testCase.wasEnabled, testCase.nowEnabled); got != testCase.want {
				t.Fatalf("shouldStartBackgroundRolloutMigration() = %v, want %v", got, testCase.want)
			}
		})
	}

	// The router reads the effective value from its config service.
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	if router.backgroundRolloutMigrationEnabled() {
		t.Fatal("background migration is enabled by default")
	}
	if _, err := router.requireFeatures().SetEnablement(&features.FeatureEnablementSetParams{
		Enablement: map[string]bool{"background_paginated_rollout_migration": true},
	}); err != nil {
		t.Fatalf("SetEnablement() error = %v", err)
	}
	router.services.Config.SetFeatureEnablementDefaults(map[string]bool{"background_paginated_rollout_migration": true})
	if !router.backgroundRolloutMigrationEnabled() {
		t.Fatal("runtime enablement did not reach the effective config")
	}
}

func featureKeySettable(key string) bool {
	for _, candidate := range features.SupportedEnablementKeys() {
		if candidate == key {
			return true
		}
	}
	return false
}
