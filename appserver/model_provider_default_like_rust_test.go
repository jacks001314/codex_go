package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/model"
)

// Mirrors Rust #61397: the default provider can be switched, an unknown
// provider is rejected without touching the catalog/config, and switching back
// restores the previous selection.
func TestModelProviderDefaultSetSwitchesAndRejectsUnknownLikeRust(t *testing.T) {
	home := t.TempDir()
	router := newManagedProviderRouter(t, home)

	setDefault := func(id string) *Response {
		t.Helper()
		return router.Handle(requestWithParams(t, IntID(1), MethodModelProviderDefaultSet, model.ProviderDefaultSetParams{ProviderID: id}))
	}

	response := setDefault("amazon-bedrock")
	if response.Error != nil {
		t.Fatalf("modelProvider/default/set amazon-bedrock error = %+v", response.Error)
	}
	if got := response.Result.(*config.ConfigWriteResponse).Status; got != config.WriteOK {
		t.Fatalf("amazon-bedrock write status = %q, want %q", got, config.WriteOK)
	}
	if got := readParsedConfigFile(t, home)["model_provider"]; got != "amazon-bedrock" {
		t.Fatalf("model_provider = %#v, want amazon-bedrock", got)
	}

	missing := setDefault("missing")
	if missing.Error == nil {
		t.Fatalf("modelProvider/default/set missing unexpectedly succeeded: %#v", missing.Result)
	}
	requireProviderWriteErrorCode(t, missing.Error, string(config.ConfigWriteValidation))
	if got := readParsedConfigFile(t, home)["model_provider"]; got != "amazon-bedrock" {
		t.Fatalf("model_provider after rejected write = %#v, want amazon-bedrock", got)
	}

	response = setDefault("openai")
	if response.Error != nil {
		t.Fatalf("modelProvider/default/set openai error = %+v", response.Error)
	}
	if got := readParsedConfigFile(t, home)["model_provider"]; got != "openai" {
		t.Fatalf("model_provider = %#v, want openai", got)
	}
}

// Mirrors Rust #61397's managed-requirement check: a pinned default provider is
// not writable and reports configRequirementReadonly.
func TestModelProviderDefaultSetRespectsManagedRequirementsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("model_provider = \"openai\"\n"), 0o600); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
	router := newManagedProviderRouter(t, home)

	response := router.Handle(requestWithParams(t, IntID(1), MethodModelProviderDefaultSet, model.ProviderDefaultSetParams{ProviderID: "amazon-bedrock"}))
	if response.Error == nil {
		t.Fatalf("modelProvider/default/set unexpectedly succeeded: %#v", response.Result)
	}
	requireProviderWriteErrorCode(t, response.Error, string(config.ConfigWriteRequirementReadonly))
}
