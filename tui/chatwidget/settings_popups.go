package chatwidget


func NewExperimentalFeaturesPopup(settings map[string]bool) ExperimentalFeaturesViewModel {
	return NewExperimentalFeaturesView(settings)
}
