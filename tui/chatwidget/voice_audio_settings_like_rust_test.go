package chatwidget

import (
	"strings"
	"testing"

	bottompane "codex_go/tui/bottom_pane"
	"codex_go/voicehost"
)

func voiceSettingsRows(view SelectionView) string {
	return strings.Join(SelectionViewRows(view, -1, 100), "\n")
}

// Rust #49437, realtime_settings_tests.rs `voice_settings` and
// `voice_settings_selects_a_voice_without_starting_audio`: /voice opens the
// "Voice settings" root, which leads to the sound devices and the voice picker.
func TestVoiceSettingsHierarchyLikeRust(t *testing.T) {
	root := NewVoiceSettingsView()
	if root.Title != "Voice settings" {
		t.Fatalf("root title = %q", root.Title)
	}
	rows := voiceSettingsRows(root)
	for _, want := range []string{"1. Set sound devices", "2. Choose a voice"} {
		if !strings.Contains(rows, want) {
			t.Fatalf("root rows missing %q:\n%s", want, rows)
		}
	}

	microphone := "Audio interface"
	soundDevices := NewVoiceSoundDevicesView(VoiceAudioPreferences{Microphone: &microphone})
	if soundDevices.Title != "Sound devices" || soundDevices.Subtitle != "Applies to your next voice conversation." {
		t.Fatalf("sound devices view = %q / %q", soundDevices.Title, soundDevices.Subtitle)
	}
	rows = voiceSettingsRows(soundDevices)
	for _, want := range []string{
		"1. Input device - Audio interface",
		"2. Output device - System default",
		"3. Back",
	} {
		if !strings.Contains(rows, want) {
			t.Fatalf("sound devices rows missing %q:\n%s", want, rows)
		}
	}
}

// Rust #49437, realtime_settings_tests.rs `voice_output_devices`: the picker
// lists System default first, marks the saved device and keeps Back last.
func TestVoiceDevicePickerMarksCurrentDeviceLikeRust(t *testing.T) {
	devices := []voicehost.AudioDevice{
		{Name: "Audio interface", Channels: 2},
		{Name: "Other device", Channels: 2},
	}
	view := VoiceDevicePickerView(voicehost.AudioDeviceKindOutput, VoiceAudioPreferences{}, devices)
	if view.Title != "Output device" || view.Subtitle != "Applies to your next voice conversation." {
		t.Fatalf("picker view = %q / %q", view.Title, view.Subtitle)
	}
	if len(view.Items) != 4 {
		t.Fatalf("picker items = %d, want 4", len(view.Items))
	}
	if !view.Items[0].IsCurrent || view.Items[0].Name != "System default" {
		t.Fatalf("default row = %#v", view.Items[0])
	}
	if view.Items[3].ID != VoiceDevicePickerBackOptionID || view.Items[3].Name != "Back" {
		t.Fatalf("last row = %#v", view.Items[3])
	}
	rows := voiceSettingsRows(view)
	if !strings.Contains(rows, "1. System default Currently selected") {
		t.Fatalf("picker rows:\n%s", rows)
	}

	speaker := "Other device"
	named := VoiceDevicePickerView(voicehost.AudioDeviceKindOutput, VoiceAudioPreferences{Speaker: &speaker}, devices)
	if named.Items[0].IsCurrent {
		t.Fatal("System default stayed current with a saved device name")
	}
	if !named.Items[2].IsCurrent {
		t.Fatalf("saved device row = %#v", named.Items[2])
	}
}

// Rust #49437, realtime_settings_tests.rs
// `ambiguous_device_names_cannot_select_the_wrong_device`: identical names are
// disabled because the name cannot identify the device.
func TestVoiceDevicePickerDisablesAmbiguousNamesLikeRust(t *testing.T) {
	devices := []voicehost.AudioDevice{
		{Name: "USB microphone", Channels: 1},
		{Name: "USB microphone", Channels: 1},
	}
	view := VoiceDevicePickerView(voicehost.AudioDeviceKindInput, VoiceAudioPreferences{}, devices)
	for _, index := range []int{1, 2} {
		item := view.Items[index]
		if !item.Disabled || item.DisabledReason != "Identical device names; use System default." {
			t.Fatalf("ambiguous row %d = %#v", index, item)
		}
	}
	if view.Items[3].ID != VoiceDevicePickerBackOptionID {
		t.Fatalf("ambiguous picker did not end with Back: %#v", view.Items[3])
	}
}

// Rust #49836, realtime_settings_tests.rs
// `device_pickers_route_selection_and_only_offer_channels_for_multichannel_inputs`:
// only an input device with at least three channels offers the channel row, and
// it sits directly under the selected row as a letter-labeled child.
func TestVoiceDevicePickerChannelRowLikeRust(t *testing.T) {
	stereo := []voicehost.AudioDevice{{Name: "Audio interface", Channels: 2, IsDefault: true}}
	view := VoiceDevicePickerView(voicehost.AudioDeviceKindInput, VoiceAudioPreferences{}, stereo)
	for _, item := range view.Items {
		if item.ID == VoiceDevicePickerChannelsOptionID {
			t.Fatalf("stereo input offered a channel row: %#v", view.Items)
		}
	}

	multichannel := []voicehost.AudioDevice{{Name: "Zen Go", Channels: 4, IsDefault: true}}
	view = VoiceDevicePickerView(voicehost.AudioDeviceKindInput, VoiceAudioPreferences{}, multichannel)
	if len(view.Items) != 4 {
		t.Fatalf("multichannel picker items = %d, want 4", len(view.Items))
	}
	child := view.Items[1]
	if child.ID != VoiceDevicePickerChannelsOptionID || child.Name != "Input channels" ||
		child.ChildLabel != "a" || child.Description != "All channels (mixed)" {
		t.Fatalf("channel child row = %#v", child)
	}
	if row := voiceSettingsRows(view); !strings.Contains(row, "  a. Input channels - All channels (mixed)") {
		t.Fatalf("channel child row rendering:\n%s", row)
	}

	// A saved subset is shown on the child row, and output devices never get one.
	saved := &MicrophoneChannels{Values: []uint16{1, 2}}
	view = VoiceDevicePickerView(voicehost.AudioDeviceKindInput, VoiceAudioPreferences{MicrophoneChannel: saved}, multichannel)
	if got := view.Items[1].Description; got != "Inputs 1, 2" {
		t.Fatalf("channel description = %q", got)
	}
	output := VoiceDevicePickerView(voicehost.AudioDeviceKindOutput, VoiceAudioPreferences{}, multichannel)
	for _, item := range output.Items {
		if item.ID == VoiceDevicePickerChannelsOptionID {
			t.Fatalf("output picker offered a channel row: %#v", output.Items)
		}
	}
}

// Rust #49836: confirming every channel clears the preference, one channel saves
// a single value and several save the ordered list.
func TestMicrophoneChannelsFromSelectionLikeRust(t *testing.T) {
	all := []string{"1", "2", "3", "4"}
	if got := MicrophoneChannelsFromSelection(4, all); got != nil {
		t.Fatalf("every channel = %#v, want nil", got)
	}
	single := MicrophoneChannelsFromSelection(4, []string{"2"})
	if single == nil || len(single.Values) != 1 || single.Values[0] != 2 {
		t.Fatalf("single channel = %#v", single)
	}
	multiple := MicrophoneChannelsFromSelection(4, []string{"3", "1"})
	if multiple == nil || len(multiple.Values) != 2 || multiple.Values[0] != 1 || multiple.Values[1] != 3 {
		t.Fatalf("multiple channels = %#v", multiple)
	}
	if got := multiple.ChannelsDescription(); got != "Inputs 1, 3" {
		t.Fatalf("multiple description = %q", got)
	}
}

// Rust #49836, realtime_settings_tests.rs
// `microphone_channel_picker_routes_selection_without_starting_audio`: an unset
// preference mixes everything, a saved one preselects exactly those channels,
// and nothing can be confirmed while every row is off.
func TestVoiceInputChannelsPickerLikeRust(t *testing.T) {
	device := voicehost.AudioDevice{Name: "Zen Go", Channels: 4, IsDefault: true}

	picker := VoiceInputChannelsPicker(device, nil)
	if picker.Title != "Microphone: Zen Go" ||
		picker.Subtitle != "Select the input channels to mix. Select at least one." {
		t.Fatalf("picker = %q / %q", picker.Title, picker.Subtitle)
	}
	if len(picker.Items) != 4 {
		t.Fatalf("channel items = %d, want 4", len(picker.Items))
	}
	for _, item := range picker.Items {
		if !item.Enabled {
			t.Fatalf("unset preference did not enable %#v", item)
		}
	}
	if !picker.RequireSelection || picker.PreviewEmpty != VoiceInputChannelsPreview {
		t.Fatalf("required-selection plumbing = %#v", picker)
	}

	saved := &MicrophoneChannels{Values: []uint16{2}}
	picker = VoiceInputChannelsPicker(device, saved)
	for _, item := range picker.Items {
		want := item.ID == "2"
		if item.Enabled != want {
			t.Fatalf("saved preference enabled %#v, want %v", item, want)
		}
	}
	picker.PreviewEmpty = VoiceInputChannelsPreview

	// Clearing every row blocks confirmation; the preview explains why.
	for _, item := range picker.Items {
		if item.Enabled {
			picker.Items = bottompane.ToggleMultiSelect(picker.Items, item.ID)
		}
	}
	picker.ApplyFilter()
	if got := picker.Confirm(); got != nil || picker.Complete {
		t.Fatalf("empty confirmation = %#v complete=%v", got, picker.Complete)
	}
	picker.UpdatePreview()
	if picker.Preview != VoiceInputChannelsPreview {
		t.Fatalf("empty preview = %q", picker.Preview)
	}
}
