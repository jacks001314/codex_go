package chatwidget

import (
	"strconv"
	"strings"

	bottompane "codex_go/tui/bottom_pane"
	"codex_go/voicehost"
)

// Local audio device selection for voice settings (Rust #49437 "Add local audio
// device selection to TUI voice settings" and #49836 "Allow microphone channel
// selection for voice conversations").
//
// Rust keeps this tree in codex-rs/tui/src/chatwidget/realtime_settings.rs and
// reads the machine-local audio preferences out of LocalSettings:
//
//   - `open_realtime_settings` is the "Voice settings" root with "Set sound
//     devices" and "Choose a voice"; every submenu has a Back row, and cancel
//     returns to its parent.
//   - `open_realtime_sound_devices` shows the current microphone and speaker
//     (or "System default").
//   - `open_realtime_device_picker` lists System default plus the helper's
//     devices; identical device names cannot be told apart and are disabled, and
//     an input device with at least three channels gets an indented "Input
//     channels" child row.
//   - `open_realtime_input_channels` is the multi-select channel picker: a
//     nonempty selection is required, and confirming every channel clears the
//     preference so the helper mixes all inputs.
//
// The device list itself is answered by the voice helper (`voicehost`), never
// invented here.

// MicrophoneChannels mirrors Rust config_toml::MicrophoneChannels: the one-based
// microphone input channels to mix. An absent preference mixes every input.
type MicrophoneChannels struct {
	Values []uint16
}

// ChannelsDescription renders the saved selection the way the device picker's
// child row does (Rust "Inputs 1, 2").
func (c *MicrophoneChannels) ChannelsDescription() string {
	if c == nil || len(c.Values) == 0 {
		return "All channels (mixed)"
	}
	parts := make([]string, 0, len(c.Values))
	for _, channel := range c.Values {
		parts = append(parts, strconv.Itoa(int(channel)))
	}
	return "Inputs " + strings.Join(parts, ", ")
}

// Includes reports whether one channel is part of the saved selection.
func (c *MicrophoneChannels) Includes(channel uint16) bool {
	if c == nil {
		return true
	}
	for _, value := range c.Values {
		if value == channel {
			return true
		}
	}
	return false
}

// VoiceAudioPreferences is the TUI's view of the machine-local audio settings
// (Rust RealtimeAudioToml): audio.microphone, audio.speaker and
// audio.microphone_channel.
type VoiceAudioPreferences struct {
	Microphone        *string
	Speaker           *string
	MicrophoneChannel *MicrophoneChannels
}

// View identifiers for the voice settings hierarchy.
const (
	VoiceSettingsViewID     = "voice-settings"
	VoiceSoundDevicesViewID = "voice-sound-devices"
	VoiceDevicePickerViewID = "voice-device-picker"

	VoiceSettingsSoundDevicesOptionID = "voice-setting:sound-devices"
	VoiceSettingsVoicesOptionID       = "voice-setting:voices"
	VoiceSettingsBackOptionID         = "voice-setting:back"

	VoiceDeviceKindOptionPrefix = "voice-device-kind:"

	VoiceDevicePickerDefaultOptionID  = "voice-device-option:system-default"
	VoiceDevicePickerChannelsOptionID = "voice-device-option:input-channels"
	VoiceDevicePickerBackOptionID     = "voice-device-option:back"
	VoiceDeviceNameOptionPrefix       = "voice-device-option:name:"

	// VoiceInputChannelPickerViewID identifies the microphone channel
	// multi-select view (Rust #49836).
	VoiceInputChannelPickerViewID = "voice-input-channels"
)

// VoiceDeviceKindOptionID is the sound-devices row that opens one picker.
func VoiceDeviceKindOptionID(kind voicehost.AudioDeviceKind) string {
	return VoiceDeviceKindOptionPrefix + string(kind)
}

// VoiceDeviceKindFromOptionID resolves a sound-devices row back to its device
// direction.
func VoiceDeviceKindFromOptionID(optionID string) (voicehost.AudioDeviceKind, bool) {
	if !strings.HasPrefix(optionID, VoiceDeviceKindOptionPrefix) {
		return "", false
	}
	kind := voicehost.AudioDeviceKind(strings.TrimPrefix(optionID, VoiceDeviceKindOptionPrefix))
	if !kind.IsValid() {
		return "", false
	}
	return kind, true
}

// VoiceDeviceNameOptionID is the picker row for one named device.
func VoiceDeviceNameOptionID(name string) string {
	return VoiceDeviceNameOptionPrefix + name
}

// VoiceDeviceNameFromOptionID resolves a picker row to the device name it
// selects.
func VoiceDeviceNameFromOptionID(optionID string) (string, bool) {
	if !strings.HasPrefix(optionID, VoiceDeviceNameOptionPrefix) {
		return "", false
	}
	name := strings.TrimPrefix(optionID, VoiceDeviceNameOptionPrefix)
	if name == "" {
		return "", false
	}
	return name, true
}

// VoiceDevicePickerTitle is the picker title for one direction, shared with the
// sound-devices rows.
func VoiceDevicePickerTitle(kind voicehost.AudioDeviceKind) string {
	if kind == voicehost.AudioDeviceKindOutput {
		return "Output device"
	}
	return "Input device"
}

// voiceDevicePreference returns the saved name for one direction.
func (p VoiceAudioPreferences) voiceDevicePreference(kind voicehost.AudioDeviceKind) *string {
	if kind == voicehost.AudioDeviceKindOutput {
		return p.Speaker
	}
	return p.Microphone
}

func voiceDeviceDescription(name *string) string {
	if name == nil || strings.TrimSpace(*name) == "" {
		return "System default"
	}
	return *name
}

// NewVoiceSettingsView is the "Voice settings" root menu (Rust #49437
// ChatWidget::open_realtime_settings).
func NewVoiceSettingsView() SelectionView {
	return SelectionView{
		ViewID:      VoiceSettingsViewID,
		Title:       "Voice settings",
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:              VoiceSettingsSoundDevicesOptionID,
				Name:            "Set sound devices",
				DismissOnSelect: true,
			},
			{
				ID:              VoiceSettingsVoicesOptionID,
				Name:            "Choose a voice",
				DismissOnSelect: true,
			},
		},
	}
}

// NewVoiceSoundDevicesView lists the two device rows with their saved selection
// (Rust #49437 ChatWidget::open_realtime_sound_devices).
func NewVoiceSoundDevicesView(audio VoiceAudioPreferences) SelectionView {
	return SelectionView{
		ViewID:      VoiceSoundDevicesViewID,
		Title:       "Sound devices",
		Subtitle:    "Applies to your next voice conversation.",
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:              VoiceDeviceKindOptionID(voicehost.AudioDeviceKindInput),
				Name:            VoiceDevicePickerTitle(voicehost.AudioDeviceKindInput),
				Description:     voiceDeviceDescription(audio.Microphone),
				DismissOnSelect: true,
			},
			{
				ID:              VoiceDeviceKindOptionID(voicehost.AudioDeviceKindOutput),
				Name:            VoiceDevicePickerTitle(voicehost.AudioDeviceKindOutput),
				Description:     voiceDeviceDescription(audio.Speaker),
				DismissOnSelect: true,
			},
			{
				ID:              VoiceSettingsBackOptionID,
				Name:            "Back",
				DismissOnSelect: true,
			},
		},
	}
}

// VoiceSelectedDevice resolves which listed device the current preference
// points at (Rust #49836): the named device, or the system default when no name
// is saved.
func VoiceSelectedDevice(devices []voicehost.AudioDevice, current *string) (voicehost.AudioDevice, bool) {
	for _, device := range devices {
		if current == nil {
			if device.IsDefault {
				return device, true
			}
			continue
		}
		if device.Name == *current {
			return device, true
		}
	}
	return voicehost.AudioDevice{}, false
}

// VoiceDeviceNamesWithDuplicates reports the names that appear more than once in
// a device list; Rust disables those rows because the name cannot identify them.
func VoiceDeviceNamesWithDuplicates(devices []voicehost.AudioDevice) map[string]bool {
	counts := make(map[string]int, len(devices))
	for _, device := range devices {
		counts[device.Name]++
	}
	duplicates := make(map[string]bool)
	for name, count := range counts {
		if count > 1 {
			duplicates[name] = true
		}
	}
	return duplicates
}

// VoiceDevicePickerView is the input/output device picker: System default, every
// enumerated device, an optional indented channel row for multichannel inputs,
// and Back (Rust #49437/#49836 ChatWidget::open_realtime_device_picker).
func VoiceDevicePickerView(kind voicehost.AudioDeviceKind, audio VoiceAudioPreferences, devices []voicehost.AudioDevice) SelectionView {
	current := audio.voiceDevicePreference(kind)
	duplicates := VoiceDeviceNamesWithDuplicates(devices)

	items := make([]SelectionItem, 0, len(devices)+3)
	items = append(items, SelectionItem{
		ID:              VoiceDevicePickerDefaultOptionID,
		Name:            "System default",
		IsCurrent:       current == nil,
		DismissOnSelect: true,
	})
	for _, device := range devices {
		item := SelectionItem{
			ID:              VoiceDeviceNameOptionID(device.Name),
			Name:            device.Name,
			DismissOnSelect: true,
		}
		if current != nil && device.Name == *current {
			item.IsCurrent = true
		}
		if duplicates[device.Name] {
			item.Disabled = true
			item.DisabledReason = "Identical device names; use System default."
		}
		items = append(items, item)
	}
	if kind == voicehost.AudioDeviceKindInput {
		if device, ok := VoiceSelectedDevice(devices, current); ok && device.Channels >= 3 {
			parent := 0
			for index, item := range items {
				if item.IsCurrent {
					parent = index
					break
				}
			}
			child := SelectionItem{
				ID:              VoiceDevicePickerChannelsOptionID,
				Name:            "Input channels",
				ChildLabel:      "a",
				Description:     audio.MicrophoneChannel.ChannelsDescription(),
				DismissOnSelect: true,
			}
			items = append(items, SelectionItem{})
			copy(items[parent+2:], items[parent+1:])
			items[parent+1] = child
		}
	}
	items = append(items, SelectionItem{
		ID:              VoiceDevicePickerBackOptionID,
		Name:            "Back",
		DismissOnSelect: true,
	})

	return SelectionView{
		ViewID:      VoiceDevicePickerViewID,
		Title:       VoiceDevicePickerTitle(kind),
		Subtitle:    "Applies to your next voice conversation.",
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items:       items,
	}
}

// VoiceInputChannelsPicker builds the microphone channel multi-selector (Rust
// #49836 ChatWidget::open_realtime_input_channels): one row per channel, every
// row on when nothing is saved, a required selection and the "Select at least
// one input channel." preview.
func VoiceInputChannelsPicker(device voicehost.AudioDevice, current *MicrophoneChannels) *bottompane.MultiSelectPicker {
	items := make([]bottompane.MultiSelectItem, 0, device.Channels)
	for number := uint16(1); number <= device.Channels; number++ {
		items = append(items, bottompane.MultiSelectItem{
			ID:      strconv.Itoa(int(number)),
			Name:    "Input " + strconv.Itoa(int(number)),
			Enabled: current.Includes(number),
		})
	}
	picker := bottompane.NewMultiSelectPicker(
		"Microphone: "+device.Name,
		"Select the input channels to mix. Select at least one.",
		items,
	)
	picker.RequireSelection = true
	picker.PreviewEmpty = VoiceInputChannelsPreview
	picker.UpdatePreview()
	return picker
}

// VoiceInputChannelsPreview is the picker's empty preview (Rust #49836
// on_preview).
const VoiceInputChannelsPreview = "Select at least one input channel."

// MicrophoneChannelsFromSelection mirrors Rust #49836's confirm mapping: every
// channel clears the preference, one channel saves a single value, and several
// save the ordered list.
func MicrophoneChannelsFromSelection(channels uint16, selectedIDs []string) *MicrophoneChannels {
	selected := make([]uint16, 0, len(selectedIDs))
	for number := uint16(1); number <= channels; number++ {
		for _, id := range selectedIDs {
			if id == strconv.Itoa(int(number)) {
				selected = append(selected, number)
				break
			}
		}
	}
	switch {
	case channels != 0 && len(selected) == int(channels):
		return nil
	case len(selected) == 1:
		return &MicrophoneChannels{Values: selected}
	default:
		// The loop walks channels in order, so the selection is already sorted.
		return &MicrophoneChannels{Values: selected}
	}
}
