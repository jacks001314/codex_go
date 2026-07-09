package app

import "testing"

func TestDisableAmbientPetBeforeShutdownDecisionMatchRust(t *testing.T) {
	asset := DisableAmbientPetBeforeShutdownDecision(PetImageRenderErrorAsset)
	if !asset.DisableAmbientPetForSession || !asset.ClearAmbientPetImage || asset.ReturnTerminalError || asset.WarningMessage != "failed to clear ambient pet image before shutdown feedback" {
		t.Fatalf("asset clear decision = %#v", asset)
	}
	terminal := DisableAmbientPetBeforeShutdownDecision(PetImageRenderErrorTerminal)
	if !terminal.ReturnTerminalError || !terminal.DisableAmbientPetForSession || !terminal.ClearAmbientPetImage {
		t.Fatalf("terminal clear decision = %#v", terminal)
	}
}

func TestAmbientPetRenderErrorDecisionMatchRust(t *testing.T) {
	terminal := HandleAmbientPetImageRenderErrorDecision(PetImageRenderErrorTerminal, PetImageRenderErrorNone)
	if !terminal.ReturnTerminalError || terminal.DisableAmbientPetForSession || terminal.ClearAmbientPetImage {
		t.Fatalf("terminal render decision = %#v", terminal)
	}

	asset := HandleAmbientPetImageRenderErrorDecision(PetImageRenderErrorAsset, PetImageRenderErrorNone)
	if !asset.DisableAmbientPetForSession || !asset.ClearAmbientPetImage || asset.ReturnTerminalError || asset.WarningMessage != "failed to render ambient pet image; disabling pet for session" {
		t.Fatalf("asset render decision = %#v", asset)
	}

	clearTerminal := HandleAmbientPetImageRenderErrorDecision(PetImageRenderErrorAsset, PetImageRenderErrorTerminal)
	if !clearTerminal.ReturnTerminalError || !clearTerminal.DisableAmbientPetForSession || !clearTerminal.ClearAmbientPetImage {
		t.Fatalf("clear terminal decision = %#v", clearTerminal)
	}

	clearAsset := HandleAmbientPetImageRenderErrorDecision(PetImageRenderErrorAsset, PetImageRenderErrorAsset)
	if clearAsset.WarningMessage != "failed to clear ambient pet image after render failure" {
		t.Fatalf("clear asset warning = %#v", clearAsset)
	}
}

func TestPetPickerPreviewRenderErrorDecisionMatchRust(t *testing.T) {
	terminal := HandlePetPickerPreviewImageRenderErrorDecision(PetImageRenderErrorTerminal, PetImageRenderErrorNone)
	if !terminal.ReturnTerminalError || terminal.FailPetPickerPreview || terminal.ClearPetPickerPreview {
		t.Fatalf("terminal picker decision = %#v", terminal)
	}

	asset := HandlePetPickerPreviewImageRenderErrorDecision(PetImageRenderErrorAsset, PetImageRenderErrorNone)
	if !asset.FailPetPickerPreview || !asset.ClearPetPickerPreview || asset.ReturnTerminalError || asset.WarningMessage != "failed to render pet picker preview image" {
		t.Fatalf("asset picker decision = %#v", asset)
	}

	clearTerminal := HandlePetPickerPreviewImageRenderErrorDecision(PetImageRenderErrorAsset, PetImageRenderErrorTerminal)
	if !clearTerminal.ReturnTerminalError || !clearTerminal.FailPetPickerPreview || !clearTerminal.ClearPetPickerPreview {
		t.Fatalf("clear terminal picker decision = %#v", clearTerminal)
	}

	clearAsset := HandlePetPickerPreviewImageRenderErrorDecision(PetImageRenderErrorAsset, PetImageRenderErrorAsset)
	if clearAsset.WarningMessage != "failed to clear pet picker preview image after render failure" {
		t.Fatalf("clear asset picker warning = %#v", clearAsset)
	}
}

func TestConfiguredPetLoadedDecisionMatchRust(t *testing.T) {
	ignored := ConfiguredPetLoadedDecision("cat", "dog", "")
	if ignored.ApplyLoadedPet || ignored.WarningMessage != "" {
		t.Fatalf("ignored configured pet = %#v", ignored)
	}
	loaded := ConfiguredPetLoadedDecision("cat", "cat", "")
	if !loaded.ApplyLoadedPet || loaded.WarningMessage != "" {
		t.Fatalf("loaded configured pet = %#v", loaded)
	}
	failed := ConfiguredPetLoadedDecision("cat", "cat", "missing asset")
	if failed.ApplyLoadedPet || failed.WarningMessage != "Failed to load configured pet: missing asset" {
		t.Fatalf("failed configured pet = %#v", failed)
	}
}
