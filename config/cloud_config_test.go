package config

import (
	"path/filepath"
	"reflect"
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

// Mirrors Rust's network_aliases_merge_with_layer_precedence_and_sources.
func TestNetworkAliasesMergeWithLayerPrecedence(t *testing.T) {
	for _, tc := range []struct {
		lowKey  string
		highKey string
	}{
		{lowKey: "network_proxy", highKey: "experimental_network"},
		{lowKey: "experimental_network", highKey: "network_proxy"},
	} {
		t.Run(tc.lowKey+"_"+tc.highKey, func(t *testing.T) {
			low := "[" + tc.lowKey + "]\nenabled = true\nallow_local_binding = false\n\n[" +
				tc.lowKey + ".domains]\n\"low.example.com\" = \"allow\"\n\"shared.example.com\" = \"allow\"\n"
			high := "[" + tc.highKey + "]\nenabled = false\nallow_upstream_proxy = true\n\n[" +
				tc.highKey + ".domains]\n\"high.example.com\" = \"allow\"\n\"shared.example.com\" = \"deny\"\n"
			values := map[string]any{}
			requirements, err := applyCloudConfigBundle(values, nil, CloudConfigBundle{
				RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
					{ID: "high", Name: "High", Contents: high},
					{ID: "low", Name: "Low", Contents: low},
				}},
			}, t.TempDir())
			if err != nil {
				t.Fatalf("applyCloudConfigBundle() error = %v", err)
			}
			network := requirements.Network
			if network == nil {
				t.Fatalf("requirements = %#v, want managed network", requirements)
			}
			if network.Enabled == nil || *network.Enabled {
				t.Fatalf("enabled = %#v, want false from the high-priority layer", network.Enabled)
			}
			if network.AllowLocalBinding == nil || *network.AllowLocalBinding {
				t.Fatalf("allow_local_binding = %#v, want false from the low-priority layer", network.AllowLocalBinding)
			}
			if network.AllowUpstreamProxy == nil || !*network.AllowUpstreamProxy {
				t.Fatalf("allow_upstream_proxy = %#v, want true", network.AllowUpstreamProxy)
			}
			wantDomains := map[string]NetworkPermission{
				"low.example.com":    NetworkAllow,
				"high.example.com":   NetworkAllow,
				"shared.example.com": NetworkDeny,
			}
			if !reflect.DeepEqual(network.Domains, wantDomains) {
				t.Fatalf("domains = %#v, want %#v", network.Domains, wantDomains)
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

// Rust #40637 retries a startup timeout through the background worker. Go's
// loader has no cached-error window: every Get calls the loader again, so the
// next configuration load observes recovery without waiting for a refresh
// interval.
func TestCloudConfigLoaderRetriesTimeoutOnNextLoadLikeRust(t *testing.T) {
	calls := 0
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) {
		calls++
		if calls == 1 {
			return nil, NewCloudConfigLoadError(CloudConfigLoadTimeout, nil, "startup timed out")
		}
		return &CloudConfigBundle{ConfigTOML: CloudConfigTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{ID: "recovered"}}}}, nil
	})
	if _, err := loader.Get(); err == nil {
		t.Fatal("first Get() error = nil, want the startup timeout")
	}
	recovered, err := loader.Get()
	if err != nil || recovered == nil || len(recovered.ConfigTOML.EnterpriseManaged) != 1 || recovered.ConfigTOML.EnterpriseManaged[0].ID != "recovered" {
		t.Fatalf("second Get() = %#v, %v; want the recovered bundle", recovered, err)
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want 2", calls)
	}
}
