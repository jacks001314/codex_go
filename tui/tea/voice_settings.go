package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/tui/chatwidget"
	"codex_go/voicehost"
)

// Voice settings hierarchy and local audio device selection (Rust #49437
// codex-rs/tui/src/chatwidget/realtime_settings.rs + #49836).
//
// The host answers the device list (Rust App::list_realtime_devices) and writes
// audio.microphone / audio.speaker / audio.microphone_channel on the TUI's own
// machine; the TUI only drives the hierarchy, the pickers and the parent
// navigation.

// VoiceDevicesMsg carries one helper device listing (Rust
// AppEvent::RealtimeDevicesListed).
type VoiceDevicesMsg struct {
	Kind    voicehost.AudioDeviceKind
	Devices []voicehost.AudioDevice
	Err     error
}

// VoiceDeviceSavedMsg reports a persisted audio.microphone / audio.speaker
// selection (Rust App::persist_realtime_device). ChannelOverridden reports that
// saving an input device could not clear a channel override coming from another
// configuration layer, and Overridden reports that the stored value is not the
// effective one because another layer wins (Rust persist_realtime_audio).
type VoiceDeviceSavedMsg struct {
	Kind              voicehost.AudioDeviceKind
	Name              *string
	ChannelOverridden bool
	Overridden        bool
	Err               error
}

// VoiceInputChannelSavedMsg reports a persisted audio.microphone_channel
// selection (Rust App::persist_realtime_input_channel).
type VoiceInputChannelSavedMsg struct {
	Channel    *chatwidget.MicrophoneChannels
	Overridden bool
	Err        error
}

// Device-list failure copy (Rust #49437 list_realtime_devices).
const voiceDevicesListFailedNotice = "Could not list audio devices. Check your audio device and voice package."

// Audio-settings notices (Rust #49437/#49836 persist_realtime_audio).
const (
	voiceAudioSavedNotice           = "Audio setting saved. Applies to your next voice conversation."
	voiceAudioOverriddenNotice      = "Audio setting was saved but is overridden by another configuration layer."
	voiceAudioChannelOverriddenNote = "Input device saved, but the input channel is overridden by another configuration layer. Update that override before starting voice."
)

// openVoiceSettingsView shows the "Voice settings" root menu (Rust #49437
// ChatWidget::open_realtime_settings).
func (m *Model) openVoiceSettingsView() {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewVoiceSettingsView())
}

// openVoiceSoundDevicesView shows the saved input/output devices (Rust #49437
// ChatWidget::open_realtime_sound_devices).
func (m *Model) openVoiceSoundDevicesView() {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewVoiceSoundDevicesView(m.voiceAudio))
}

// openVoiceDevicePickerView shows the picker for one direction from the devices
// the helper listed (Rust #49437 ChatWidget::open_realtime_device_picker).
func (m *Model) openVoiceDevicePickerView(kind voicehost.AudioDeviceKind) {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.VoiceDevicePickerView(kind, m.voiceAudio, m.voiceDevices))
}

// requestVoiceDevices asks the host to enumerate one direction (Rust #49437
// App::list_realtime_devices).
func (m *Model) requestVoiceDevices(kind voicehost.AudioDeviceKind) bubbletea.Cmd {
	if m == nil || m.onVoiceListDevices == nil {
		m.notice = voiceDevicesListFailedNotice
		return nil
	}
	return m.onVoiceListDevices(kind)
}

// applyVoiceDevices opens the picker for a successful listing (Rust
// AppEvent::RealtimeDevicesListed).
func (m *Model) applyVoiceDevices(msg VoiceDevicesMsg) {
	if m == nil {
		return
	}
	if msg.Err != nil {
		m.voiceDevices = nil
		m.notice = voiceDevicesListFailedNotice
		return
	}
	m.voiceDeviceKind = msg.Kind
	m.voiceDevices = append([]voicehost.AudioDevice(nil), msg.Devices...)
	m.openVoiceDevicePickerView(msg.Kind)
}

// saveVoiceDevice persists one audio.microphone / audio.speaker value (Rust
// #49437 App::persist_realtime_device).
func (m *Model) saveVoiceDevice(kind voicehost.AudioDeviceKind, name *string) bubbletea.Cmd {
	if m == nil || m.onVoiceSaveDevice == nil {
		m.notice = "Failed to save audio setting: audio settings are unavailable in this runtime."
		return nil
	}
	return m.onVoiceSaveDevice(kind, name)
}

// applyVoiceDeviceSaved stages a saved device and reports the outcome (Rust
// #49437/#49836 persist_realtime_audio).
func (m *Model) applyVoiceDeviceSaved(msg VoiceDeviceSavedMsg) {
	if m == nil {
		return
	}
	if msg.Err != nil {
		m.notice = "Failed to save audio setting: " + msg.Err.Error()
		return
	}
	switch msg.Kind {
	case voicehost.AudioDeviceKindOutput:
		m.voiceAudio.Speaker = msg.Name
	default:
		m.voiceAudio.Microphone = msg.Name
		// Rust #49836: saving an input device clears the saved channel
		// selection; another layer can still override it.
		m.voiceAudio.MicrophoneChannel = nil
	}
	if msg.ChannelOverridden {
		m.notice = voiceAudioChannelOverriddenNote
		return
	}
	if msg.Overridden {
		m.notice = voiceAudioOverriddenNotice
		return
	}
	m.notice = voiceAudioSavedNotice
}

// saveVoiceInputChannel persists audio.microphone_channel (Rust #49836
// App::persist_realtime_input_channel).
func (m *Model) saveVoiceInputChannel(channel *chatwidget.MicrophoneChannels) bubbletea.Cmd {
	if m == nil || m.onVoiceSaveInputChannel == nil {
		m.notice = "Failed to save audio setting: audio settings are unavailable in this runtime."
		return nil
	}
	return m.onVoiceSaveInputChannel(channel)
}

// applyVoiceInputChannelSaved stages a saved channel selection.
func (m *Model) applyVoiceInputChannelSaved(msg VoiceInputChannelSavedMsg) {
	if m == nil {
		return
	}
	if msg.Err != nil {
		m.notice = "Failed to save audio setting: " + msg.Err.Error()
		return
	}
	m.voiceAudio.MicrophoneChannel = msg.Channel
	if msg.Overridden {
		m.notice = voiceAudioOverriddenNotice
		return
	}
	m.notice = voiceAudioSavedNotice
}

// applyVoiceSettingsOption routes one "Voice settings" root selection (Rust
// #49437 open_realtime_settings items).
func (m *Model) applyVoiceSettingsOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch optionID {
	case chatwidget.VoiceSettingsSoundDevicesOptionID:
		m.openVoiceSoundDevicesView()
		return nil
	case chatwidget.VoiceSettingsVoicesOptionID:
		// Rust #49437: the voice catalog is the server list the host already
		// fetched for /voice.
		m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewVoicePickerView(m.voicePreference, m.voiceChoices))
		return nil
	default:
		return nil
	}
}

// applyVoiceSoundDevicesOption routes one sound-devices selection (Rust #49437
// open_realtime_sound_devices items).
func (m *Model) applyVoiceSoundDevicesOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if optionID == chatwidget.VoiceSettingsBackOptionID {
		m.openVoiceSettingsView()
		return nil
	}
	kind, ok := chatwidget.VoiceDeviceKindFromOptionID(optionID)
	if !ok {
		m.notice = "Device selection failed: unknown option"
		return nil
	}
	return m.requestVoiceDevices(kind)
}

// applyVoiceDevicePickerOption routes one device-picker selection (Rust #49437
// / #49836 open_realtime_device_picker items).
func (m *Model) applyVoiceDevicePickerOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch {
	case optionID == chatwidget.VoiceDevicePickerBackOptionID:
		m.openVoiceSoundDevicesView()
		return nil
	case optionID == chatwidget.VoiceDevicePickerDefaultOptionID:
		return m.saveVoiceDevice(m.voiceDeviceKind, nil)
	case optionID == chatwidget.VoiceDevicePickerChannelsOptionID:
		// Rust #49836: the channel picker opens for the selected microphone.
		if device, ok := chatwidget.VoiceSelectedDevice(m.voiceDevices, m.voiceAudio.Microphone); ok {
			m.openVoiceInputChannelsModal(device)
		}
		return nil
	}
	name, ok := chatwidget.VoiceDeviceNameFromOptionID(optionID)
	if !ok {
		m.notice = "Device selection failed: unknown option"
		return nil
	}
	return m.saveVoiceDevice(m.voiceDeviceKind, &name)
}

// openVoiceInputChannelsModal shows the microphone channel picker (Rust #49836
// ChatWidget::open_realtime_input_channels).
func (m *Model) openVoiceInputChannelsModal(device voicehost.AudioDevice) {
	if m == nil {
		return
	}
	m.modal = &modalState{
		kind: ModalKindVoiceChannels,
		voiceChannels: chatwidget.VoiceInputChannelsPicker(
			device, m.voiceAudio.MicrophoneChannel,
		),
		voiceChannelDevice: device,
	}
	m.notice = ""
}

// updateVoiceChannelsModal drives the channel multi-select: space toggles,
// enter saves the selection and esc returns to the device picker (Rust #49836).
func (m *Model) updateVoiceChannelsModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.voiceChannels == nil {
		return nil
	}
	picker := m.modal.voiceChannels
	for _, key := range manageSkillsKeyNames(message) {
		picker.HandleKey(key)
	}
	if !picker.Complete {
		return nil
	}
	device := m.modal.voiceChannelDevice
	m.modal = nil
	if picker.Cancelled {
		m.openVoiceDevicePickerView(voicehost.AudioDeviceKindInput)
		return nil
	}
	channel := chatwidget.MicrophoneChannelsFromSelection(device.Channels, picker.ConfirmedIDs)
	return m.saveVoiceInputChannel(channel)
}

// renderVoiceChannelsModal draws the channel multi-select.
func (m *Model) renderVoiceChannelsModal() string {
	if m == nil || m.modal == nil || m.modal.voiceChannels == nil {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	rows := m.modal.voiceChannels.Rows(width)
	if preview := strings.TrimSpace(m.modal.voiceChannels.Preview); preview != "" {
		rows = append(rows, preview)
	}
	return strings.Join(rows, "\n")
}
