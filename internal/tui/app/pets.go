package app

type PetSelection struct {
	PetID string
}

type PetImageRenderErrorKind string

const (
	PetImageRenderErrorNone     PetImageRenderErrorKind = ""
	PetImageRenderErrorTerminal PetImageRenderErrorKind = "terminal"
	PetImageRenderErrorAsset    PetImageRenderErrorKind = "asset"
)

type PetRenderErrorDecision struct {
	ReturnTerminalError         bool
	DisableAmbientPetForSession bool
	ClearAmbientPetImage        bool
	ClearPetPickerPreview       bool
	FailPetPickerPreview        bool
	WarningMessage              string
}

func DisableAmbientPetBeforeShutdownDecision(clearError PetImageRenderErrorKind) PetRenderErrorDecision {
	decision := PetRenderErrorDecision{DisableAmbientPetForSession: true, ClearAmbientPetImage: true}
	switch clearError {
	case PetImageRenderErrorTerminal:
		decision.ReturnTerminalError = true
	case PetImageRenderErrorAsset:
		decision.WarningMessage = "failed to clear ambient pet image before shutdown feedback"
	}
	return decision
}

func HandleAmbientPetImageRenderErrorDecision(renderError PetImageRenderErrorKind, clearError PetImageRenderErrorKind) PetRenderErrorDecision {
	if renderError == PetImageRenderErrorTerminal {
		return PetRenderErrorDecision{ReturnTerminalError: true}
	}
	if renderError != PetImageRenderErrorAsset {
		return PetRenderErrorDecision{}
	}
	decision := PetRenderErrorDecision{
		DisableAmbientPetForSession: true,
		ClearAmbientPetImage:        true,
		WarningMessage:              "failed to render ambient pet image; disabling pet for session",
	}
	switch clearError {
	case PetImageRenderErrorTerminal:
		decision.ReturnTerminalError = true
	case PetImageRenderErrorAsset:
		decision.WarningMessage = "failed to clear ambient pet image after render failure"
	}
	return decision
}

func HandlePetPickerPreviewImageRenderErrorDecision(renderError PetImageRenderErrorKind, clearError PetImageRenderErrorKind) PetRenderErrorDecision {
	if renderError == PetImageRenderErrorTerminal {
		return PetRenderErrorDecision{ReturnTerminalError: true}
	}
	if renderError != PetImageRenderErrorAsset {
		return PetRenderErrorDecision{}
	}
	decision := PetRenderErrorDecision{
		ClearPetPickerPreview: true,
		FailPetPickerPreview:  true,
		WarningMessage:        "failed to render pet picker preview image",
	}
	switch clearError {
	case PetImageRenderErrorTerminal:
		decision.ReturnTerminalError = true
	case PetImageRenderErrorAsset:
		decision.WarningMessage = "failed to clear pet picker preview image after render failure"
	}
	return decision
}

type PetLoadDecision struct {
	ApplyLoadedPet bool
	WarningMessage string
}

func ConfiguredPetLoadedDecision(configuredPetID string, loadedPetID string, loadErr string) PetLoadDecision {
	if configuredPetID != loadedPetID {
		return PetLoadDecision{}
	}
	if loadErr != "" {
		return PetLoadDecision{WarningMessage: "Failed to load configured pet: " + loadErr}
	}
	return PetLoadDecision{ApplyLoadedPet: true}
}
