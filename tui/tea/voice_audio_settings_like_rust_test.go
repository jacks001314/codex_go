package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/chatwidget"
	"codex_go/utils"
	"codex_go/voicehost"
)

// Voice audio-settings failures used by the tests below.
var (
	errVoiceListing = errors.New("helper unavailable")
	errVoiceSave    = errors.New("read-only config")
)

func modalOptionIDs(model *Model) []string {
	if model.modal == nil {
		return nil
	}
	ids := make([]string, 0, len(model.modal.options))
	for _, option := range model.modal.options {
		ids = append(ids, option.ID)
	}
	return ids
}

func voiceOptionLabels(model *Model) []string {
	if model.modal == nil {
		return nil
	}
	labels := make([]string, 0, len(model.modal.options))
	for _, option := range model.modal.options {
		labels = append(labels, option.Label)
	}
	return labels
}

func openVoiceSettingsModel(t *testing.T, audio chatwidget.VoiceAudioPreferences) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.onVoiceSettings = func() bubbletea.Cmd {
		return func() bubbletea.Msg {
			return VoiceSettingsMsg{Voices: []string{"alloy", "cove"}, Current: "alloy", Audio: audio}
		}
	}
	runTeaCmd(t, model, model.applyVoiceCommand("settings"))
	return model
}

// Rust #49437, app/tests/realtime_requests.rs `audio_devices_are_persisted` and
// chatwidget/realtime_settings_tests.rs
// `device_pickers_route_selection_and_only_offer_channels_for_multichannel_inputs`:
// /voice settings opens the "Voice settings" root, "Set sound devices" lists the
// saved microphone and speaker, and picking a direction asks the host to
// enumerate that direction before the picker opens.
func TestModelVoiceSettingsSoundDevicesOpenPickerLikeRust(t *testing.T) {
	model := openVoiceSettingsModel(t, chatwidget.VoiceAudioPreferences{})
	if model.modal == nil || model.modal.id != chatwidget.VoiceSettingsViewID {
		t.Fatalf("modal = %#v, want the Voice settings root", model.modal)
	}
	if got := modalOptionIDs(model); len(got) != 2 ||
		got[0] != chatwidget.VoiceSettingsSoundDevicesOptionID ||
		got[1] != chatwidget.VoiceSettingsVoicesOptionID {
		t.Fatalf("root options = %#v", got)
	}

	// "Set sound devices" leads to the two device rows drawn from the saved
	// preference (nothing saved yet -> System default).
	model.modal.selected = 0
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal.id != chatwidget.VoiceSoundDevicesViewID {
		t.Fatalf("modal = %#v, want Sound devices", model.modal)
	}
	if labels := voiceOptionLabels(model); len(labels) != 3 ||
		labels[0] != "Input device" || labels[1] != "Output device" || labels[2] != "Back" {
		t.Fatalf("sound device rows = %#v", labels)
	}
	for _, option := range model.modal.options[:2] {
		if option.Description != "System default" {
			t.Fatalf("row %q description = %q", option.ID, option.Description)
		}
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Sound devices") {
		t.Fatalf("sound devices view:\n%s", view)
	}

	// Selecting the input row asks the host for the device list instead of
	// inventing one.
	requested := []voicehost.AudioDeviceKind{}
	devices := []voicehost.AudioDevice{{Name: "Audio interface", Channels: 4, IsDefault: true}}
	model.onVoiceListDevices = func(kind voicehost.AudioDeviceKind) bubbletea.Cmd {
		requested = append(requested, kind)
		return func() bubbletea.Msg { return VoiceDevicesMsg{Kind: kind, Devices: devices} }
	}
	model.modal.selected = 0
	runTeaCmd(t, model, model.respondModal(false))
	if len(requested) != 1 || requested[0] != voicehost.AudioDeviceKindInput {
		t.Fatalf("device requests = %#v", requested)
	}
	if model.modal == nil || model.modal.id != chatwidget.VoiceDevicePickerViewID {
		t.Fatalf("modal = %#v, want the input device picker", model.modal)
	}
	ids := modalOptionIDs(model)
	want := []string{
		chatwidget.VoiceDevicePickerDefaultOptionID,
		chatwidget.VoiceDevicePickerChannelsOptionID,
		chatwidget.VoiceDeviceNameOptionID("Audio interface"),
		chatwidget.VoiceDevicePickerBackOptionID,
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("picker options = %#v, want %#v", ids, want)
	}

	// Rust #49437 ChatWidget::open_realtime_sound_devices: without a wired host
	// the listing fails loudly instead of opening an empty picker.
	model.modal = nil
	model.onVoiceListDevices = nil
	model.openVoiceSoundDevicesView()
	model.modal.selected = 0
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal != nil {
		t.Fatalf("picker opened without a device lister: %#v", model.modal)
	}
	if model.notice != voiceDevicesListFailedNotice {
		t.Fatalf("notice = %q", model.notice)
	}

	// A failed listing reports the same failure copy and keeps the picker shut.
	model.applyVoiceDevices(VoiceDevicesMsg{Kind: voicehost.AudioDeviceKindInput, Err: errVoiceListing})
	if model.modal != nil {
		t.Fatalf("picker opened after a failed listing: %#v", model.modal)
	}
	if model.notice != voiceDevicesListFailedNotice {
		t.Fatalf("notice = %q", model.notice)
	}
}

// Rust #49437 chatwidget/realtime_settings_tests.rs
// `submenus_return_to_their_parent_with_back_or_escape`: every submenu returns to
// its parent instead of closing the whole hierarchy.
func TestModelVoiceSettingsSubmenusReturnToParentLikeRust(t *testing.T) {
	model := openVoiceSettingsModel(t, chatwidget.VoiceAudioPreferences{})

	// Escape from the root closes the hierarchy.
	runTeaCmd(t, model, model.respondModal(true))
	if model.modal != nil {
		t.Fatalf("escape from the root left %#v", model.modal)
	}

	// Sound devices -> Back returns to the root.
	model.openVoiceSoundDevicesView()
	model.modal.selected = 2
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.id != chatwidget.VoiceSettingsViewID {
		t.Fatalf("Back from Sound devices landed on %#v", model.modal)
	}

	// Escape from sound devices returns to the root as well.
	model.openVoiceSoundDevicesView()
	runTeaCmd(t, model, model.respondModal(true))
	if model.modal == nil || model.modal.id != chatwidget.VoiceSettingsViewID {
		t.Fatalf("escape from Sound devices landed on %#v", model.modal)
	}

	// Rust #49437 also moved the voice catalog under the root: "Choose a voice"
	// opens it and Back or escape returns to the root.
	model.openVoiceSettingsView()
	model.modal.selected = 1
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.id != chatwidget.VoicePickerViewID {
		t.Fatalf("voice catalog landed on %#v", model.modal)
	}
	last := len(model.modal.options) - 1
	if model.modal.options[last].ID != chatwidget.VoiceSettingsBackOptionID {
		t.Fatalf("voice catalog lost its Back row: %#v", model.modal.options)
	}
	model.modal.selected = last
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.id != chatwidget.VoiceSettingsViewID {
		t.Fatalf("Back from the voice catalog landed on %#v", model.modal)
	}
	model.modal.selected = 1
	runTeaCmd(t, model, model.respondModal(false))
	runTeaCmd(t, model, model.respondModal(true))
	if model.modal == nil || model.modal.id != chatwidget.VoiceSettingsViewID {
		t.Fatalf("escape from the voice catalog landed on %#v", model.modal)
	}

	// Back from a device picker returns to Sound devices.
	model.applyVoiceDevices(VoiceDevicesMsg{
		Kind:    voicehost.AudioDeviceKindInput,
		Devices: []voicehost.AudioDevice{{Name: "Mic", Channels: 4, IsDefault: true}},
	})
	model.modal.selected = len(model.modal.options) - 1
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.id != chatwidget.VoiceSoundDevicesViewID {
		t.Fatalf("Back from the picker landed on %#v", model.modal)
	}
}

// Rust #49437 app/tests/realtime_requests.rs `audio_devices_are_persisted`: a
// named device persists audio.microphone / audio.speaker, System default clears
// it, and saving an input device clears the saved channel selection (Rust
// #49836).
func TestModelVoiceDeviceSelectionLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	type savedDevice struct {
		kind voicehost.AudioDeviceKind
		name *string
	}
	var saved []savedDevice
	model.onVoiceSaveDevice = func(kind voicehost.AudioDeviceKind, name *string) bubbletea.Cmd {
		saved = append(saved, savedDevice{kind: kind, name: name})
		return func() bubbletea.Msg {
			return VoiceDeviceSavedMsg{Kind: kind, Name: name}
		}
	}

	model.voiceAudio = chatwidget.VoiceAudioPreferences{
		MicrophoneChannel: &chatwidget.MicrophoneChannels{Values: []uint16{2}},
	}
	model.voiceDevices = []voicehost.AudioDevice{
		{Name: "Audio interface", Channels: 4, IsDefault: true},
		{Name: "Other device", Channels: 2},
	}

	// The first named row saves that device.
	model.applyVoiceDevices(VoiceDevicesMsg{Kind: voicehost.AudioDeviceKindInput, Devices: model.voiceDevices})
	model.modal.selected = 2
	runTeaCmd(t, model, model.respondModal(false))
	if len(saved) != 1 || saved[0].kind != voicehost.AudioDeviceKindInput ||
		saved[0].name == nil || *saved[0].name != "Audio interface" {
		t.Fatalf("saved = %#v", saved)
	}
	if model.voiceAudio.Microphone == nil || *model.voiceAudio.Microphone != "Audio interface" {
		t.Fatalf("microphone = %#v", model.voiceAudio.Microphone)
	}
	// Rust #49836: saving an input device clears the channel override.
	if model.voiceAudio.MicrophoneChannel != nil {
		t.Fatalf("channel survived the device save: %#v", model.voiceAudio.MicrophoneChannel)
	}
	if model.notice != voiceAudioSavedNotice {
		t.Fatalf("notice = %q", model.notice)
	}

	// System default clears the saved name.
	model.applyVoiceDevices(VoiceDevicesMsg{Kind: voicehost.AudioDeviceKindInput, Devices: model.voiceDevices})
	model.modal.selected = 0
	runTeaCmd(t, model, model.respondModal(false))
	if len(saved) != 2 || saved[1].name != nil {
		t.Fatalf("saved = %#v", saved)
	}
	if model.voiceAudio.Microphone != nil {
		t.Fatalf("System default kept %#v", model.voiceAudio.Microphone)
	}

	// Output devices write audio.speaker and never touch the channels.
	model.applyVoiceDevices(VoiceDevicesMsg{Kind: voicehost.AudioDeviceKindOutput, Devices: model.voiceDevices})
	model.modal.selected = 1
	runTeaCmd(t, model, model.respondModal(false))
	if len(saved) != 3 || saved[2].kind != voicehost.AudioDeviceKindOutput {
		t.Fatalf("saved = %#v", saved)
	}
	if model.voiceAudio.Speaker == nil || *model.voiceAudio.Speaker != "Audio interface" {
		t.Fatalf("speaker = %#v", model.voiceAudio.Speaker)
	}

	// Rust #49836: an override from another configuration layer is reported.
	model.applyVoiceDeviceSaved(VoiceDeviceSavedMsg{
		Kind: voicehost.AudioDeviceKindInput, Name: strPtr("Machine mic"), ChannelOverridden: true,
	})
	if model.notice != voiceAudioChannelOverriddenNote {
		t.Fatalf("notice = %q", model.notice)
	}

	// A failing write reports the error instead of faking a save.
	model.applyVoiceDeviceSaved(VoiceDeviceSavedMsg{
		Kind: voicehost.AudioDeviceKindInput, Err: errVoiceSave,
	})
	if !strings.Contains(model.notice, "read-only") {
		t.Fatalf("notice = %q", model.notice)
	}
}

// Rust #49836 chatwidget/realtime_settings_tests.rs
// `microphone_channel_picker_routes_selection_without_starting_audio`: the
// channel picker saves the mixed subset, needs at least one channel, and escape
// returns to the device picker without persisting.
func TestModelVoiceInputChannelSelectionLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	var saved []*chatwidget.MicrophoneChannels
	model.onVoiceSaveInputChannel = func(channel *chatwidget.MicrophoneChannels) bubbletea.Cmd {
		saved = append(saved, channel)
		return func() bubbletea.Msg { return VoiceInputChannelSavedMsg{Channel: channel} }
	}
	device := voicehost.AudioDevice{Name: "Zen Go", Channels: 4, IsDefault: true}
	model.voiceDevices = []voicehost.AudioDevice{device}

	model.openVoiceInputChannelsModal(device)
	if model.modal == nil || model.modal.kind != ModalKindVoiceChannels || model.modal.voiceChannels == nil {
		t.Fatalf("modal = %#v", model.modal)
	}
	picker := model.modal.voiceChannels
	if picker.Title != "Microphone: Zen Go" || !picker.RequireSelection {
		t.Fatalf("picker = %#v", picker)
	}
	if len(picker.Items) != 4 {
		t.Fatalf("channel rows = %d", len(picker.Items))
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Microphone: Zen Go") ||
		!strings.Contains(view, "Input 4") {
		t.Fatalf("channel modal view:\n%s", view)
	}

	// Turn every channel off: an empty selection cannot be confirmed and the
	// modal stays open.
	for _, item := range picker.Items {
		if item.Enabled {
			picker.Items = bottompane.ToggleMultiSelect(picker.Items, item.ID)
		}
	}
	picker.ApplyFilter()
	runTeaCmd(t, model, model.updateVoiceChannelsModal(key(bubbletea.KeyEnter)))
	if model.modal == nil || model.modal.voiceChannels == nil {
		t.Fatalf("empty selection closed the picker: %#v", model.modal)
	}
	if len(saved) != 0 {
		t.Fatalf("empty selection saved %#v", saved)
	}

	// Keep only channel 2 and confirm.
	picker = model.modal.voiceChannels
	picker.Items = bottompane.ToggleMultiSelect(picker.Items, "2")
	picker.ApplyFilter()
	runTeaCmd(t, model, model.updateVoiceChannelsModal(key(bubbletea.KeyEnter)))
	if len(saved) != 1 || saved[0] == nil || len(saved[0].Values) != 1 || saved[0].Values[0] != 2 {
		t.Fatalf("saved = %#v", saved)
	}
	if model.modal != nil {
		t.Fatalf("modal = %#v, want closed after saving", model.modal)
	}
	if model.voiceAudio.MicrophoneChannel == nil || model.voiceAudio.MicrophoneChannel.Values[0] != 2 {
		t.Fatalf("channel = %#v", model.voiceAudio.MicrophoneChannel)
	}
	if model.notice != voiceAudioSavedNotice {
		t.Fatalf("notice = %q", model.notice)
	}

	// Escape returns to the device picker without persisting.
	model.openVoiceInputChannelsModal(device)
	runTeaCmd(t, model, model.updateVoiceChannelsModal(key(bubbletea.KeyEsc)))
	if len(saved) != 1 {
		t.Fatalf("escape saved %#v", saved)
	}
	if model.modal == nil || model.modal.id != chatwidget.VoiceDevicePickerViewID {
		t.Fatalf("escape landed on %#v", model.modal)
	}

	// The device picker's "Input channels" child row routes to the channel
	// picker instead of saving a device (Rust #49836).
	model.voiceAudio = chatwidget.VoiceAudioPreferences{}
	model.applyVoiceDevices(VoiceDevicesMsg{Kind: voicehost.AudioDeviceKindInput, Devices: []voicehost.AudioDevice{device}})
	if model.modal == nil || model.modal.id != chatwidget.VoiceDevicePickerViewID {
		t.Fatalf("modal = %#v", model.modal)
	}
	for index, option := range model.modal.options {
		if option.ID == chatwidget.VoiceDevicePickerChannelsOptionID {
			model.modal.selected = index
		}
	}
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.kind != ModalKindVoiceChannels {
		t.Fatalf("channels row landed on %#v", model.modal)
	}
}

// Rust #49437 chatwidget/realtime_settings_tests.rs
// `ambiguous_device_names_cannot_select_the_wrong_device`: duplicated names are
// disabled, so the TUI cannot save the wrong device.
func TestModelVoiceAmbiguousDeviceNamesLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	asked := 0
	model.onVoiceSaveDevice = func(voicehost.AudioDeviceKind, *string) bubbletea.Cmd {
		asked++
		return nil
	}
	model.applyVoiceDevices(VoiceDevicesMsg{
		Kind: voicehost.AudioDeviceKindInput,
		Devices: []voicehost.AudioDevice{
			{Name: "USB microphone", Channels: 2},
			{Name: "USB microphone", Channels: 4},
		},
	})

	disabled := 0
	for index, option := range model.modal.options {
		if option.ID == chatwidget.VoiceDeviceNameOptionID("USB microphone") {
			if !option.Disabled || option.DisabledReason != "Identical device names; use System default." {
				t.Fatalf("row %d = %#v", index, option)
			}
			disabled++
			model.modal.selected = index
		}
	}
	if disabled != 2 {
		t.Fatalf("ambiguous rows = %d, want 2", disabled)
	}
	runTeaCmd(t, model, model.respondModal(false))
	if asked != 0 {
		t.Fatalf("an ambiguous row saved a device (%d)", asked)
	}
	if model.modal == nil || model.modal.id != chatwidget.VoiceDevicePickerViewID {
		t.Fatalf("modal = %#v, want the picker to stay open", model.modal)
	}
}

func strPtr(value string) *string { return &value }

// Rust #49437 app/tests/realtime_requests.rs
// `audio_device_is_machine_local_across_project_and_remote_transitions`: the
// machine-local audio is a staged record the runtime resolves at launch and
// after a reload, and it is what a voice conversation starts with.
func TestModelVoiceAudioStagingLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	if got := model.VoiceAudioPreferences(); got.Microphone != nil || got.Speaker != nil || got.MicrophoneChannel != nil {
		t.Fatalf("fresh stage = %#v", got)
	}

	mic := "Machine mic"
	model.Update(VoiceAudioStagedMsg{Audio: chatwidget.VoiceAudioPreferences{
		Microphone:        &mic,
		MicrophoneChannel: &chatwidget.MicrophoneChannels{Values: []uint16{1, 2}},
	}})
	got := model.VoiceAudioPreferences()
	if got.Microphone == nil || *got.Microphone != "Machine mic" {
		t.Fatalf("staged microphone = %#v", got.Microphone)
	}
	if got.MicrophoneChannel == nil || len(got.MicrophoneChannel.Values) != 2 {
		t.Fatalf("staged channels = %#v", got.MicrophoneChannel)
	}

	// /voice settings carries the same record into the hierarchy.
	model.onVoiceSettings = func() bubbletea.Cmd {
		return func() bubbletea.Msg {
			return VoiceSettingsMsg{
				Voices: []string{"alloy"}, Current: "alloy",
				Audio: chatwidget.VoiceAudioPreferences{Microphone: &mic},
			}
		}
	}
	runTeaCmd(t, model, model.applyVoiceCommand("settings"))
	if got := model.VoiceAudioPreferences(); got.Microphone == nil || *got.Microphone != "Machine mic" {
		t.Fatalf("settings did not carry the audio record: %#v", got)
	}

	// A configuration reload stages the machine audio it resolved; a machine
	// without audio clears the record instead of keeping a stale device.
	model.Update(VoiceAudioStagedMsg{Audio: chatwidget.VoiceAudioPreferences{}})
	if got := model.VoiceAudioPreferences(); got.Microphone != nil {
		t.Fatalf("reload kept %#v", got.Microphone)
	}
}
