package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gcpPrepareSettings(projectID string, location *string, credentialFile *string) []ProviderSetting {
	return []ProviderSetting{
		{Key: "project_id", Value: &projectID},
		{Key: "location", Value: location},
		{Key: "credential_file", Value: credentialFile},
	}
}

func TestPrepareProviderConfigPreviewIncludesPackagedDefaultsAndPreservesHeadersLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `
model_provider = "openai"

[model_providers.gcp.http_headers]
X-Existing = "preserved"
`)
	packagedPath := filepath.Join(home, "packaged.toml")
	if err := os.WriteFile(packagedPath, []byte(`
[model_providers.gcp.http_headers]
X-Packaged = "yes"
`), 0o600); err != nil {
		t.Fatalf("WriteFile(packaged) error = %v", err)
	}
	service := NewConfigService(home)
	if err := service.SetPackagedDefaultsLayer(packagedPath); err != nil {
		t.Fatalf("SetPackagedDefaultsLayer() error = %v", err)
	}
	location := " global "
	credential := filepath.Join(home, "adc.json")
	write, providerTable, err := service.PrepareProviderConfig("gcp", gcpPrepareSettings(" project ", &location, &credential))
	if err != nil {
		t.Fatalf("PrepareProviderConfig() error = %v", err)
	}
	if write == nil || len(write.Edits) != 4 {
		t.Fatalf("prepared write = %+v", write)
	}
	if write.ExpectedVersion == "" {
		t.Fatal("expectedVersion must be captured")
	}
	headers, _ := providerTable["http_headers"].(map[string]any)
	if headers["X-Existing"] != "preserved" || headers["X-Packaged"] != "yes" {
		t.Fatalf("provider table headers = %+v, want user and packaged headers", headers)
	}
	if providerTable["project_id"] != " project " || providerTable["location"] != " global " {
		t.Fatalf("provider table = %+v (caller trimming belongs to the handler)", providerTable)
	}

	// Committing the preview succeeds and writes the edits to the user layer.
	response, err := service.BatchWrite(&ConfigBatchWriteParams{Edits: write.Edits, ExpectedVersion: &write.ExpectedVersion})
	if err != nil {
		t.Fatalf("BatchWrite() error = %v", err)
	}
	if response == nil || response.Status != WriteOK {
		t.Fatalf("BatchWrite() response = %+v", response)
	}
	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Config["model_provider"] != "gcp" {
		t.Fatalf("model_provider = %v", read.Config["model_provider"])
	}
}

func TestPrepareProviderConfigHonorsRequiredProviderSelectionLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `model_provider = "openai"`+"\n")
	service := NewConfigService(home)
	service.SetRequirements(&ConfigRequirements{ModelProvider: gcpStrPtr("gcp")})

	write, _, err := service.PrepareProviderConfig("gcp", gcpPrepareSettings("project", nil, nil))
	if err != nil {
		t.Fatalf("PrepareProviderConfig(required gcp) error = %v", err)
	}
	for _, edit := range write.Edits {
		if edit.KeyPath == "model_provider" {
			t.Fatalf("requirement-pinned selection should not be edited: %+v", write.Edits)
		}
	}
	if len(write.Edits) != 3 {
		t.Fatalf("edits = %+v, want project/location/credential only", write.Edits)
	}

	service.SetRequirements(&ConfigRequirements{ModelProvider: gcpStrPtr("openai")})
	_, _, err = service.PrepareProviderConfig("gcp", gcpPrepareSettings("project", nil, nil))
	var writeErr *ConfigWriteError
	if !errors.As(err, &writeErr) || writeErr.Code != ConfigWriteRequirementReadonly {
		t.Fatalf("PrepareProviderConfig(required openai) error = %v", err)
	}
	if !strings.Contains(err.Error(), "requirements set `model_provider` to `openai`") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareProviderConfigRejectsRequirementPinnedProviderLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewConfigService(home)
	service.SetRequirements(&ConfigRequirements{
		ModelProviders: map[string]any{"gcp": map[string]any{"project_id": "managed-project"}},
	})
	_, _, err := service.PrepareProviderConfig("gcp", gcpPrepareSettings("project", nil, nil))
	var writeErr *ConfigWriteError
	if !errors.As(err, &writeErr) || writeErr.Code != ConfigWriteRequirementReadonly {
		t.Fatalf("PrepareProviderConfig() error = %v", err)
	}
	if !strings.Contains(err.Error(), "model_providers.gcp") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareProviderConfigReportsOverridingLayerLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "")
	managedPath := filepath.Join(home, "managed_config.toml")
	service := NewConfigService(home)
	service.SetManagedLayers([]Layer{{
		Name:    LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: managedPath},
		Version: "managed-v1",
		Config: map[string]any{
			"model_providers": map[string]any{
				"gcp": map[string]any{"project_id": "managed-project"},
			},
		},
	}})
	_, _, err := service.PrepareProviderConfig("gcp", gcpPrepareSettings("project", nil, nil))
	var writeErr *ConfigWriteError
	if !errors.As(err, &writeErr) || writeErr.Code != ConfigWriteLayerReadonly {
		t.Fatalf("PrepareProviderConfig() error = %v", err)
	}
	want := "Cannot configure `model_providers.gcp.project_id`: Overridden by legacy managed_config.toml: " + managedPath
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err.Error(), want)
	}
}

func TestPrepareProviderConfigVersionConflictOnCommitLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `model = "gpt-a"`+"\n")
	service := NewConfigService(home)
	write, _, err := service.PrepareProviderConfig("gcp", gcpPrepareSettings("project", nil, nil))
	if err != nil {
		t.Fatalf("PrepareProviderConfig() error = %v", err)
	}
	// An intervening write changes the user layer version.
	if _, err := service.WriteValue(&ConfigValueWriteParams{KeyPath: "model", Value: "gpt-b", MergeStrategy: MergeReplace}); err != nil {
		t.Fatalf("WriteValue() error = %v", err)
	}
	_, err = service.BatchWrite(&ConfigBatchWriteParams{Edits: write.Edits, ExpectedVersion: &write.ExpectedVersion})
	var writeErr *ConfigWriteError
	if !errors.As(err, &writeErr) || writeErr.Code != ConfigWriteVersionConflict {
		t.Fatalf("BatchWrite() error = %v", err)
	}
}

func gcpStrPtr(value string) *string { return &value }
