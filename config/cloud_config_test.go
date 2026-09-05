package config

import (
	"path/filepath"
	"testing"
)

func TestCloudConfigLayersFromFragmentsReversesBackendPriority(t *testing.T) {
	layers, err := CloudConfigLayersFromFragments([]CloudConfigFragment{
		{ID: "high", Name: "High", Contents: `model = "gpt-5"`},
		{ID: "low", Name: "Low", Contents: `model = "gpt-4"`},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("CloudConfigLayersFromFragments() error = %v", err)
	}
	if layers[0].Source.ID != "low" || layers[1].Source.ID != "high" {
		t.Fatalf("unexpected layer order: %#v", layers)
	}
	merged := MergeCloudConfigLayers(layers)
	if merged["model"] != "gpt-5" {
		t.Fatalf("highest priority layer should win, got %#v", merged)
	}
}

func TestFeatureRequirementAliasesMergeWithLayerPrecedence(t *testing.T) {
	for _, tc := range []struct {
		lowKey  string
		highKey string
	}{
		{lowKey: "features", highKey: "feature_requirements"},
		{lowKey: "feature_requirements", highKey: "features"},
	} {
		t.Run(tc.lowKey+"_"+tc.highKey, func(t *testing.T) {
			low, err := parseRequirementsTOMLValues([]byte("[" + tc.lowKey + "]\nchronicle = true\nshell_snapshot = false\n"))
			if err != nil {
				t.Fatalf("parse low layer: %v", err)
			}
			high, err := parseRequirementsTOMLValues([]byte("[" + tc.highKey + "]\nchronicle = false\napps = false\n"))
			if err != nil {
				t.Fatalf("parse high layer: %v", err)
			}
			normalizeFeatureRequirementAliases(low)
			normalizeFeatureRequirementAliases(high)
			merged := map[string]any{}
			mergeConfigMaps(merged, low)
			mergeConfigMaps(merged, high)
			requirements, err := configRequirementsFromValidatedMap(merged)
			if err != nil {
				t.Fatalf("configRequirementsFromValidatedMap: %v", err)
			}
			features := requirements.FeatureRequirements
			if features["chronicle"] || features["shell_snapshot"] || features["apps"] {
				t.Fatalf("merged feature requirements = %#v, want chronicle=false shell_snapshot=false apps=false", features)
			}
		})
	}
}

func TestCloudConfigLayersResolveRelativePathFields(t *testing.T) {
	base := t.TempDir()
	layers, err := CloudConfigLayersFromFragments([]CloudConfigFragment{{ID: "a", Name: "A", Contents: `cache_path = "cache/data"`}}, base)
	if err != nil {
		t.Fatalf("CloudConfigLayersFromFragments() error = %v", err)
	}
	want := filepath.Join(base, "cache", "data")
	if layers[0].Values["cache_path"] != want {
		t.Fatalf("path = %#v, want %q", layers[0].Values["cache_path"], want)
	}
}

func TestCloudConfigBundleLayersIncludesRequirements(t *testing.T) {
	layers, err := CloudConfigLayersFromBundle(CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
			{ID: "req", Name: "Requirements", Contents: "network.enabled = true"},
		}},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("CloudConfigLayersFromBundle() error = %v", err)
	}
	if layers.EnterpriseManagedRequirements[0].Values["network.enabled"] != "true" {
		t.Fatalf("unexpected requirements: %#v", layers.EnterpriseManagedRequirements)
	}
}

func TestCloudConfigLoaderRetrievesLatestOnEachLoadLikeRust(t *testing.T) {
	calls := 0
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) {
		calls++
		return &CloudConfigBundle{ConfigTOML: CloudConfigTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{ID: "latest"}}}}, nil
	})
	first, err := loader.Get()
	if err != nil || first == nil {
		t.Fatalf("first Get() = %#v, %v", first, err)
	}
	second, err := loader.Get()
	if err != nil || second == nil || len(second.ConfigTOML.EnterpriseManaged) == 0 {
		t.Fatalf("second Get() = %#v, %v", second, err)
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want 2 (each load retrieves the latest bundle)", calls)
	}
}

func TestCloudConfigLoaderPreservesLastSuccessfulOnRefreshFailure(t *testing.T) {
	calls := 0
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) {
		calls++
		if calls == 2 {
			return nil, NewCloudConfigLoadError(CloudConfigLoadRequestFailed, nil, "network down")
		}
		return &CloudConfigBundle{ConfigTOML: CloudConfigTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{ID: "ok"}}}}, nil
	})
	first, err := loader.Get()
	if err != nil || first == nil {
		t.Fatalf("first Get() = %#v, %v", first, err)
	}
	second, err := loader.Get()
	if err != nil {
		t.Fatalf("second Get() error = %v, want the last successful bundle", err)
	}
	if second == nil || len(second.ConfigTOML.EnterpriseManaged) == 0 || second.ConfigTOML.EnterpriseManaged[0].ID != "ok" {
		t.Fatalf("second Get() = %#v, want preserved last successful bundle", second)
	}
}

func TestCloudConfigLoaderReturnsErrorWhenNeverSucceeded(t *testing.T) {
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) {
		return nil, NewCloudConfigLoadError(CloudConfigLoadRequestFailed, nil, "network down")
	})
	if _, err := loader.Get(); err == nil {
		t.Fatal("Get() error = nil, want error when no successful bundle exists")
	}
}
