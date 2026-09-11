package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func completeCredentialProvider() map[string]any {
	return map[string]any{
		"env":          []any{"VENDOR_TOKEN"},
		"patterns":     []any{"vk-[a-z0-9]{16}"},
		"url_prefixes": []any{"https://api.vendor.example"},
		"auth":         []any{"bearer"},
	}
}

// TestConfigWriteValidatesCredentialProvidersLikeRust mirrors #44241: a write
// that leaves a complete provider under features.network_proxy.credentials must
// pass the broker's compile rules before it is persisted.
func TestConfigWriteValidatesCredentialProvidersLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-5\"\n")
	service := NewConfigService(home)

	if _, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       "features.network_proxy.credentials.vendor",
		Value:         completeCredentialProvider(),
		MergeStrategy: MergeReplace,
	}); err != nil {
		t.Fatalf("valid provider write error = %v", err)
	}

	invalid := completeCredentialProvider()
	invalid["env"] = []any{"1BAD"}
	_, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       "features.network_proxy.credentials.bad",
		Value:         invalid,
		MergeStrategy: MergeReplace,
	})
	var writeErr *ConfigWriteError
	if !errors.As(err, &writeErr) || writeErr.Code != ConfigWriteValidation {
		t.Fatalf("invalid provider write error = %v", err)
	}
	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatal(err)
	}
	credentials := read.Config["features"].(map[string]any)["network_proxy"].(map[string]any)["credentials"].(map[string]any)
	if _, ok := credentials["bad"]; ok {
		t.Fatalf("invalid provider was persisted: %#v", credentials)
	}
	if _, ok := credentials["vendor"]; !ok {
		t.Fatalf("valid provider missing: %#v", credentials)
	}
}

// TestConfigWriteAllowsCredentialProviderDraftsAndDeletions mirrors the Rust
// rule that incomplete drafts and explicit deletions are permitted.
func TestConfigWriteAllowsCredentialProviderDraftsAndDeletions(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-5\"\n")
	service := NewConfigService(home)

	if _, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       "features.network_proxy.credentials.draft",
		Value:         map[string]any{"env": []any{"DRAFT_TOKEN"}},
		MergeStrategy: MergeReplace,
	}); err != nil {
		t.Fatalf("draft write error = %v", err)
	}
	if _, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       "features.network_proxy.credentials.draft",
		Value:         nil,
		MergeStrategy: MergeReplace,
	}); err != nil {
		t.Fatalf("delete write error = %v", err)
	}
	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatal(err)
	}
	features, _ := read.Config["features"].(map[string]any)
	if features == nil {
		return
	}
	networkProxy, _ := features["network_proxy"].(map[string]any)
	credentials, _ := networkProxy["credentials"].(map[string]any)
	if _, ok := credentials["draft"]; ok {
		t.Fatalf("deleted provider persisted: %#v", credentials)
	}
}

// TestConfigWriteReportsCredentialProviderSourceOwnershipOverride mirrors
// #44241: a written provider displaced by a higher-priority provider that owns
// the same env source is reported as overridden instead of silently vanishing.
func TestConfigWriteReportsCredentialProviderSourceOwnershipOverride(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-5\"\n")
	service := NewConfigService(home)
	service.SetManagedLayers([]Layer{{
		Name:    LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: filepath.Join(home, "managed_config.toml")},
		Version: "managed-v1",
		Config: credentialLayer(map[string]any{"managed": map[string]any{
			"env":          []any{"VENDOR_TOKEN"},
			"patterns":     []any{"vk-[a-z0-9]{16}"},
			"url_prefixes": []any{"https://api.vendor.example"},
			"auth":         []any{"bearer"},
		}}),
	}})

	response, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       "features.network_proxy.credentials.user",
		Value:         completeCredentialProvider(),
		MergeStrategy: MergeReplace,
	})
	if err != nil {
		t.Fatalf("write error = %v", err)
	}
	if response.Status != WriteOKOverridden || response.OverriddenMetadata == nil {
		t.Fatalf("response = %+v", response)
	}
	if response.OverriddenMetadata.OverridingLayer.Name.Type != LayerSourceLegacyManagedConfigFromFile {
		t.Fatalf("overriding layer = %+v", response.OverriddenMetadata.OverridingLayer)
	}
}

// TestConfigWriteValidatesMergedCredentialProviderLikeRust proves validation
// runs on the merged result: a partial upsert that makes an existing complete
// provider invalid is rejected.
func TestConfigWriteValidatesMergedCredentialProviderLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-5\"\n")
	service := NewConfigService(home)

	if _, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       "features.network_proxy.credentials.vendor",
		Value:         completeCredentialProvider(),
		MergeStrategy: MergeReplace,
	}); err != nil {
		t.Fatalf("valid provider write error = %v", err)
	}
	_, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       "features.network_proxy.credentials.vendor.url_prefixes",
		Value:         []any{"http://api.example.com"},
		MergeStrategy: MergeReplace,
	})
	var writeErr *ConfigWriteError
	if !errors.As(err, &writeErr) || writeErr.Code != ConfigWriteValidation {
		t.Fatalf("merged invalid write error = %v", err)
	}
}
