package chatwidget

func NewReviewPopupView() SelectionView {
	return NewReviewPresetView()
}

func NewReviewCustomInstructionsPopup() ReviewCustomPromptView {
	return NewReviewCustomPromptView()
}
