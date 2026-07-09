package chatwidget

func NewPersonalitySelectionPopup(current Personality, sessionConfigured bool, modelSupportsPersonality bool, currentModel string) PersonalityPopupResult {
	return NewPersonalityPopup(current, sessionConfigured, modelSupportsPersonality, currentModel)
}

func NewExperimentalFeaturesPopup(settings map[string]bool) ExperimentalFeaturesViewModel {
	return NewExperimentalFeaturesView(settings)
}
