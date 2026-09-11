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

// TestConfigBatchWriteDisplacesCredentialProvidersLikeRust mirrors #44241's
// ordered batch handling: a provider written with an overlapping env source
// displaces the sibling that owned it, while unrelated siblings are untouched.
func TestConfigBatchWriteDisplacesCredentialProvidersLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-5\"\n")
	service := NewConfigService(home)

	if _, err := service.BatchWrite(&ConfigBatchWriteParams{Edits: []ConfigEdit{
		{KeyPath: "features.network_proxy.credentials.vendor", Value: completeCredentialProvider(), MergeStrategy: MergeReplace},
		{KeyPath: "features.network_proxy.credentials.other", Value: map[string]any{
			"env":          []any{"OTHER_TOKEN"},
			"patterns":     []any{"ok-[a-z0-9]{16}"},
			"url_prefixes": []any{"https://api.other.example"},
			"auth":         []any{"token"},
		}, MergeStrategy: MergeReplace},
	}}); err != nil {
		t.Fatal(err)
	}

	_, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath: "features.network_proxy.credentials.replacement",
		Value: map[string]any{
			"env":          []any{"VENDOR_TOKEN"},
			"patterns":     []any{"rk-[a-z0-9]{16}"},
			"url_prefixes": []any{"https://api.replacement.example"},
			"auth":         []any{"bearer"},
		},
		MergeStrategy: MergeReplace,
	})
	if err != nil {
		t.Fatal(err)
	}

	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatal(err)
	}
	credentials := credentialProviderTable(t, read.Config)
	if _, ok := credentials["vendor"]; ok {
		t.Fatalf("displaced provider survived the write: %#v", credentials)
	}
	if _, ok := credentials["replacement"]; !ok {
		t.Fatalf("replacement provider missing: %#v", credentials)
	}
	if _, ok := credentials["other"]; !ok {
		t.Fatalf("unrelated sibling was removed: %#v", credentials)
	}
}

// TestConfigBatchWriteRestoresDisplacedCredentialProviderLikeRust mirrors
// #44241's ordered-remap restoration: a provider displaced earlier in the batch
// is restored from its original definition before a later source upsert, so the
// partial update keeps the inherited settings.
func TestConfigBatchWriteRestoresDisplacedCredentialProviderLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-5\"\n")
	service := NewConfigService(home)

	if _, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       "features.network_proxy.credentials.vendor",
		Value:         completeCredentialProvider(),
		MergeStrategy: MergeReplace,
	}); err != nil {
		t.Fatal(err)
	}

	displacer := completeCredentialProvider()
	displacer["patterns"] = []any{"rk-[a-z0-9]{16}"}
	displacer["url_prefixes"] = []any{"https://api.displacer.example"}
	if _, err := service.BatchWrite(&ConfigBatchWriteParams{Edits: []ConfigEdit{
		{KeyPath: "features.network_proxy.credentials.displacer", Value: displacer, MergeStrategy: MergeReplace},
		{KeyPath: "features.network_proxy.credentials.vendor", Value: map[string]any{"env": []any{"ROTATED_TOKEN"}}, MergeStrategy: MergeUpsert},
	}}); err != nil {
		t.Fatal(err)
	}

	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatal(err)
	}
	credentials := credentialProviderTable(t, read.Config)
	vendor, ok := credentials["vendor"].(map[string]any)
	if !ok {
		t.Fatalf("restored provider missing: %#v", credentials)
	}
	if env := stringSliceFromAny(vendor["env"]); len(env) != 1 || env[0] != "ROTATED_TOKEN" {
		t.Fatalf("vendor env = %#v", vendor["env"])
	}
	if patterns := stringSliceFromAny(vendor["patterns"]); len(patterns) != 1 || patterns[0] != "vk-[a-z0-9]{16}" {
		t.Fatalf("restored provider lost its definition: %#v", vendor)
	}
	if _, ok := credentials["displacer"]; !ok {
		t.Fatalf("displacer missing: %#v", credentials)
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
