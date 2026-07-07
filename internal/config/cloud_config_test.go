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

func TestCloudConfigLoaderCachesResult(t *testing.T) {
	calls := 0
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) {
		calls++
		return &CloudConfigBundle{}, nil
	})
	if _, err := loader.Get(); err != nil {
		t.Fatalf("first Get() error = %v", err)
	}
	if _, err := loader.Get(); err != nil {
		t.Fatalf("second Get() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1", calls)
	}
}
