package chatwidget

// Voice picker for /voice settings. The TUI fetches the supported voices from
// the app-server and persists the chosen preference, mirroring the Rust voice
// settings surface.

import "strings"

// VoicePickerViewID identifies the voice selection modal.
const VoicePickerViewID = "voice-picker"

// VoicePickerOptionPrefix keeps voice option IDs distinct from other pickers.
const VoicePickerOptionPrefix = "voice:"

// NewVoicePickerView lists the supported voices with the current preference
// marked. An empty list renders a disabled row so the picker stays readable.
func NewVoicePickerView(current string, voices []string) SelectionView {
	current = strings.TrimSpace(current)
	items := make([]SelectionItem, 0, len(voices))
	for _, voice := range voices {
		voice = strings.TrimSpace(voice)
		if voice == "" {
			continue
		}
		items = append(items, SelectionItem{
			ID:        VoicePickerOptionPrefix + voice,
			Name:      voice,
			IsCurrent: voice == current,
		})
	}
	if len(items) == 0 {
		items = append(items, SelectionItem{Name: "No voices are available", Disabled: true})
	}
	return SelectionView{
		ViewID:               VoicePickerViewID,
		Title:                "Voice",
		Subtitle:             "Applies to future voice conversations",
		FooterHint:           standardPopupHintLine,
		AllowCancel:          true,
		Searchable:           true,
		SearchPlaceholder:    "Filter voices",
		Items:                items,
		InitialSelectedIndex: firstEnabledSelectionIndex(items),
	}
}

// VoiceFromPickerOption extracts the voice name from a picker option ID.
func VoiceFromPickerOption(optionID string) (string, bool) {
	if !strings.HasPrefix(optionID, VoicePickerOptionPrefix) {
		return "", false
	}
	voice := strings.TrimSpace(strings.TrimPrefix(optionID, VoicePickerOptionPrefix))
	if voice == "" {
		return "", false
	}
	return voice, true
}
