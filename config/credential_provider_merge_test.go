package config

import (
	"strings"
	"testing"
)

func credentialProviderTable(t *testing.T, values map[string]any) map[string]any {
	t.Helper()
	features, ok := values["features"].(map[string]any)
	if !ok {
		t.Fatalf("features table missing: %#v", values)
	}
	networkProxy, ok := features["network_proxy"].(map[string]any)
	if !ok {
		t.Fatalf("features.network_proxy table missing: %#v", features)
	}
	credentials, ok := networkProxy["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credentials table missing: %#v", networkProxy)
	}
	return credentials
}

func credentialLayer(providers map[string]any) map[string]any {
	return map[string]any{"features": map[string]any{"network_proxy": map[string]any{"credentials": providers}}}
}

// TestMergeConfigLayersOmitsDisplacedProviderOrigins mirrors #44241: a
// provider displaced by a higher-priority provider's overlapping source must
// disappear from both the merged config and its origins.
func TestMergeConfigLayersPrunesDisplacedProviderOrigins(t *testing.T) {
	layers := []Layer{
		{
			Name:   LayerSource{Type: LayerSourceUser, File: "/home/user/config.toml"},
			Config: credentialLayer(map[string]any{"old": map[string]any{"env": []any{"VENDOR_TOKEN"}}}),
		},
		{
			Name:   LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: "/managed/config.toml"},
			Config: credentialLayer(map[string]any{"new": map[string]any{"env": []any{"VENDOR_TOKEN"}}}),
		},
	}
	values, origins := mergeConfigLayers(layers)
	credentials := credentialProviderTable(t, values)
	if _, ok := credentials["old"]; ok {
		t.Fatalf("displaced provider survived the merge: %#v", credentials)
	}
	if _, ok := credentials["new"]; !ok {
		t.Fatalf("overlay provider missing: %#v", credentials)
	}
	for path := range origins {
		if path == "features.network_proxy.credentials.old" || strings.HasPrefix(path, "features.network_proxy.credentials.old.") {
			t.Fatalf("displaced provider origin retained: %s in %#v", path, origins)
		}
	}
	if _, ok := origins["features.network_proxy.credentials.new.env"]; !ok {
		t.Fatalf("overlay provider origins missing: %#v", origins)
	}
}

// TestMergeConfigMapsDisplacesCredentialProvidersBySource mirrors Rust #44241:
// a higher-priority provider displaces lower-priority providers that declare an
// overlapping env source.
func TestMergeConfigMapsDisplacesCredentialProvidersBySource(t *testing.T) {
	base := credentialLayer(map[string]any{
		"old":  map[string]any{"env": []any{"VENDOR_TOKEN"}},
		"keep": map[string]any{"env": []any{"OTHER_TOKEN"}},
	})
	overlay := credentialLayer(map[string]any{
		"new": map[string]any{"env": []any{"VENDOR_TOKEN"}},
	})
	mergeConfigMaps(base, overlay)

	credentials := credentialProviderTable(t, base)
	if _, ok := credentials["old"]; ok {
		t.Fatalf("overlapping provider was not displaced: %#v", credentials)
	}
	if _, ok := credentials["new"]; !ok {
		t.Fatalf("overlay provider missing: %#v", credentials)
	}
	if _, ok := credentials["keep"]; !ok {
		t.Fatalf("non-overlapping provider was displaced: %#v", credentials)
	}
}

// TestMergeConfigMapsKeepsRedefinedCredentialProvider mirrors the Rust rule that
// a provider redefined by the overlay (same id) is not displaced.
func TestMergeConfigMapsKeepsRedefinedCredentialProvider(t *testing.T) {
	base := credentialLayer(map[string]any{
		"vendor": map[string]any{"env": []any{"VENDOR_TOKEN"}, "patterns": []any{"old"}},
	})
	overlay := credentialLayer(map[string]any{
		"vendor": map[string]any{"env": []any{"VENDOR_TOKEN"}, "patterns": []any{"new"}},
	})
	mergeConfigMaps(base, overlay)

	credentials := credentialProviderTable(t, base)
	provider, ok := credentials["vendor"].(map[string]any)
	if !ok {
		t.Fatalf("vendor provider missing: %#v", credentials)
	}
	if patterns := stringSliceFromAny(provider["patterns"]); len(patterns) != 1 || patterns[0] != "new" {
		t.Fatalf("overlay provider not applied: %#v", provider)
	}
}

func TestMergeConfigMapsDisplacesCredentialProvidersInProfiles(t *testing.T) {
	base := map[string]any{"profiles": map[string]any{"work": map[string]any{
		"features": map[string]any{"network_proxy": map[string]any{"credentials": map[string]any{
			"old": map[string]any{"env": []any{"VENDOR_TOKEN"}},
		}}},
	}}}
	overlay := map[string]any{"profiles": map[string]any{"work": map[string]any{
		"features": map[string]any{"network_proxy": map[string]any{"credentials": map[string]any{
			"new": map[string]any{"env": []any{"VENDOR_TOKEN"}},
		}}},
	}}}
	mergeConfigMaps(base, overlay)

	profile := base["profiles"].(map[string]any)["work"].(map[string]any)
	credentials := credentialProviderTable(t, profile)
	if _, ok := credentials["old"]; ok {
		t.Fatalf("profile provider was not displaced: %#v", credentials)
	}
	if _, ok := credentials["new"]; !ok {
		t.Fatalf("profile overlay provider missing: %#v", credentials)
	}
}

// TestMergeConfigMapsConvertsNetworkProxyBoolLikeRust pins the structured
// feature handling now that the proxy is configured under features.network_proxy.
func TestMergeConfigMapsConvertsNetworkProxyBoolLikeRust(t *testing.T) {
	base := map[string]any{"features": map[string]any{"network_proxy": true}}
	overlay := map[string]any{"features": map[string]any{"network_proxy": map[string]any{
		"enabled": false,
		"mode":    "limited",
	}}}
	mergeConfigMaps(base, overlay)

	features := base["features"].(map[string]any)
	networkProxy, ok := features["network_proxy"].(map[string]any)
	if !ok {
		t.Fatalf("network_proxy = %#v", features["network_proxy"])
	}
	if networkProxy["enabled"] != false || networkProxy["mode"] != "limited" {
		t.Fatalf("merged network_proxy = %#v", networkProxy)
	}

	reverseBase := map[string]any{"features": map[string]any{"network_proxy": map[string]any{"mode": "full"}}}
	reverseOverlay := map[string]any{"features": map[string]any{"network_proxy": true}}
	mergeConfigMaps(reverseBase, reverseOverlay)
	reverseMerged := reverseBase["features"].(map[string]any)["network_proxy"].(map[string]any)
	if reverseMerged["enabled"] != true || reverseMerged["mode"] != "full" {
		t.Fatalf("reverse merged network_proxy = %#v", reverseMerged)
	}
}
