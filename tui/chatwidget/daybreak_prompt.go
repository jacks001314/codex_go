package chatwidget

// Rust parity: chatwidget/turn_runtime.rs on_cyber_policy_error. The refusal
// picker changes the thread preference for the next request without retrying.

const (
	DaybreakPromptViewID = "daybreak-enable-prompt"
	DaybreakEnableItem   = "enable-daybreak"
	DaybreakNotNowItem   = "not-now"
)

func DaybreakEnableView() SelectionView {
	return SelectionView{
		ViewID:      DaybreakPromptViewID,
		Title:       "Turn on Daybreak for your next request?",
		Subtitle:    "Daybreak gives broader access for authorized security work. This request will not be retried automatically.",
		AllowCancel: true,
		Items: []SelectionItem{
			{ID: DaybreakEnableItem, Name: "Enable Daybreak", DismissOnSelect: true},
			{ID: DaybreakNotNowItem, Name: "Not now", DismissOnSelect: true},
		},
	}
}
