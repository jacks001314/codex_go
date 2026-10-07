package app

// Local voice helper ownership for the interactive TUI. The TUI owns the media
// path: it starts the packaged helper, posts the helper's offer through the
// app-server, and applies the remote answer. The app-server never sees audio.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/config"
	"codex_go/realtime"
	"codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
	"codex_go/voicehost"
)

const (
	// voiceStartTimeout covers helper startup and its transport offer.
	voiceStartTimeout = 90 * time.Second
	// voiceStopTimeout bounds the best-effort stop RPC.
	voiceStopTimeout = 15 * time.Second
)

// voiceRuntimeOptions wires the helper to the TUI's transport.
type voiceRuntimeOptions struct {
	// packageDir overrides the resolved package root, primarily for tests.
	packageDir string
	// buildCommit overrides the stamped helper identity.
	buildCommit string
	// realtimeSettings reads the effective realtime model and voice preference.
	realtimeSettings func(ctx context.Context) VoiceSettings
	// listVoices asks the app-server which voices it supports.
	listVoices func(ctx context.Context) realtime.VoicesList
	// startSession asks the app-server to start the thread's realtime session
	// with the helper's offer.
	startSession func(ctx context.Context, params realtime.StartParams) error
	// stopSession ends the thread's realtime session.
	stopSession func(ctx context.Context, threadID string) error
}

// VoiceSettings is the effective realtime configuration the TUI applies when it
// starts a session.
type VoiceSettings struct {
	// Model overrides the configured realtime model when non-empty.
	Model string
	// Voice is the configured voice preference. VoiceSet distinguishes an
	// absent or null preference, which falls back to the default voice, from an
	// explicit name.
	Voice    string
	VoiceSet bool
	// Audio is the machine-local device preference the conversation opens with
	// (Rust `audio.microphone` / `audio.speaker` / `audio.microphone_channel`).
	// It mirrors the Rust webrtc facade, whose single open_devices entry point
	// always carries a selection; the zero value keeps the system defaults.
	Audio voicehost.AudioDeviceSelection
	// AudioErr reports an `audio` preference that cannot be used. The Rust TUI
	// surfaces that error and never starts the conversation, so a broken
	// preference must not silently open other devices.
	AudioErr string
}

// voiceRuntime owns at most one helper process for the TUI.
type voiceRuntime struct {
	options voiceRuntimeOptions

	mu       sync.Mutex
	session  *voicehost.StartedSession
	threadID string
}

func newVoiceRuntime(options voiceRuntimeOptions) *voiceRuntime {
	return &voiceRuntime{options: options}
}

// startVoiceSession launches the packaged helper. Tests replace it so the
// runtime can be exercised without a helper process.
var startVoiceSession = func(ctx context.Context, options voicehost.SessionOptions) (*voicehost.StartedSession, error) {
	return (voicehost.RealtimeSession{}).StartWithOptions(ctx, options)
}

// startCmd launches the helper and posts its offer for one startup attempt.
func (r *voiceRuntime) startCmd(threadID string, attemptID uint64) bubbletea.Cmd {
	return func() bubbletea.Msg {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: errors.New("start a conversation before using voice mode")}
		}
		if r == nil {
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: errors.New("voice runtime is unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), voiceStartTimeout)
		defer cancel()
		settings := r.voiceSettings(ctx)
		if settings.AudioErr != "" {
			// The helper is not started at all: the Rust TUI refuses to open a
			// conversation whose stored audio preference cannot be read.
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: errors.New(settings.AudioErr)}
		}
		started, err := startVoiceSession(ctx, voicehost.SessionOptions{
			PackageDir:     r.options.packageDir,
			BuildCommit:    r.options.buildCommit,
			AudioSelection: settings.Audio,
		})
		if err != nil {
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: err}
		}
		if err := r.options.startSession(ctx, r.startParams(ctx, threadID, started.OfferSDP, settings)); err != nil {
			// The app-server never accepted the offer, so the helper is
			// retired before the caller sees the failure.
			started.Handle.Close()
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: err}
		}
		r.setSession(started, threadID)
		return codextea.VoiceHelperAttachedMsg{AttemptID: attemptID}
	}
}

// voiceSettings reads the effective realtime settings once per start attempt.
// A runtime without a reader keeps the built-in defaults, which is what the
// Rust TUI does when the server cannot report the configuration.
func (r *voiceRuntime) voiceSettings(ctx context.Context) VoiceSettings {
	if r.options.realtimeSettings == nil {
		return VoiceSettings{}
	}
	return r.options.realtimeSettings(ctx)
}

// startParams builds the app-server start request the TUI sends for a voice
// session: a V3 WebRTC conversation with client-managed handoffs and no startup
// context, carrying the configured model and voice.
func (r *voiceRuntime) startParams(ctx context.Context, threadID string, offerSDP string, settings VoiceSettings) realtime.StartParams {
	clientManagedHandoffs := true
	includeStartupContext := false
	version := realtime.VersionV3
	params := realtime.StartParams{
		ThreadID:              threadID,
		ClientManagedHandoffs: &clientManagedHandoffs,
		OutputModality:        realtime.OutputAudio,
		IncludeStartupContext: &includeStartupContext,
		Transport:             realtime.WebRTCTransport(offerSDP),
		Version:               &version,
	}
	if model := strings.TrimSpace(settings.Model); model != "" {
		params.Model = &model
	}
	if voice := r.resolveVoice(ctx, settings); voice != nil {
		params.Voice = voice
	}
	return params
}

// resolveVoice mirrors the Rust resolver: an explicit preference is used when
// it names a known voice and is dropped when it does not parse; an absent or
// null preference falls back to the version's default voice.
func (r *voiceRuntime) resolveVoice(ctx context.Context, settings VoiceSettings) *realtime.Voice {
	if settings.VoiceSet {
		configured := realtime.Voice(strings.TrimSpace(settings.Voice))
		if !realtime.IsKnownVoice(configured) {
			return nil
		}
		return &configured
	}
	voices := realtime.BuiltinVoices()
	if r.options.listVoices != nil {
		voices = r.options.listVoices(ctx)
	}
	fallback := voices.DefaultForVersion(realtime.VersionV1)
	if fallback == "" {
		return nil
	}
	return &fallback
}

// applyAnswerCmd applies the remote answer and reports the outcome.
func (r *voiceRuntime) applyAnswerCmd(threadID string, attemptID uint64, answer string) bubbletea.Cmd {
	return func() bubbletea.Msg {
		session := r.currentSession()
		if session == nil {
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: errors.New("voice helper is not running")}
		}
		if err := session.Handle.ApplyAnswerSDP(answer); err != nil {
			return codextea.VoiceAnswerResultMsg{AttemptID: attemptID, Err: err}
		}
		return codextea.VoiceAnswerResultMsg{AttemptID: attemptID}
	}
}

// close retires the helper and asks the app-server to end the session.
func (r *voiceRuntime) close() {
	if r == nil {
		return
	}
	session, threadID := r.takeSession()
	if session != nil {
		session.Handle.Close()
	}
	if threadID == "" || r.options.stopSession == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), voiceStopTimeout)
	defer cancel()
	// The local session is already gone; a failed stop RPC must not surface as
	// a user-visible error.
	_ = r.options.stopSession(ctx, threadID)
}

// setMicrophoneMuted queues an ordered privacy transition.
func (r *voiceRuntime) setMicrophoneMuted(muted bool) error {
	session := r.currentSession()
	if session == nil {
		return errors.New("voice conversation is not running")
	}
	return session.Handle.SetMicrophoneMuted(muted)
}

// peaks returns and clears the accumulated audio levels.
func (r *voiceRuntime) peaks() (uint16, uint16) {
	session := r.currentSession()
	if session == nil {
		return 0, 0
	}
	return session.Handle.TakeMicrophonePeak(), session.Handle.TakeSpeakerPeak()
}

// running reports whether a helper is currently owned.
func (r *voiceRuntime) running() bool {
	return r.currentSession() != nil
}

func (r *voiceRuntime) currentSession() *voicehost.StartedSession {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

func (r *voiceRuntime) setSession(session *voicehost.StartedSession, threadID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.session = session
	r.threadID = threadID
}

func (r *voiceRuntime) takeSession() (*voicehost.StartedSession, string) {
	if r == nil {
		return nil, ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.session
	threadID := r.threadID
	r.session = nil
	r.threadID = ""
	return session, threadID
}

// interactiveRemoteRealtimeSettings reads the effective realtime model and
// voice preference from the app-server. A read failure keeps the built-in
// defaults, which is what the Rust TUI does when the server cannot report them.
func interactiveRemoteRealtimeSettings(endpoint *appserverdaemon.RemoteAppServerEndpoint) func(context.Context) VoiceSettings {
	return func(ctx context.Context) VoiceSettings {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return VoiceSettings{}
		}
		defer client.close()
		var response config.ConfigReadResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRead, config.ConfigReadParams{}, &response); err != nil {
			return VoiceSettings{}
		}
		return RealtimeSettingsFromConfigValues(response.Config)
	}
}

// RealtimeSettingsFromConfigValues resolves the realtime model and voice
// preference from effective configuration values.
func RealtimeSettingsFromConfigValues(values map[string]any) VoiceSettings {
	settings := VoiceSettings{}
	if values == nil {
		return settings
	}
	if model, ok := values["experimental_realtime_ws_model"].(string); ok {
		settings.Model = model
	}
	settings.Audio, settings.AudioErr = audioSelectionFromConfigValues(values["audio"])
	table, _ := values["realtime"].(map[string]any)
	raw, present := table["voice"]
	if !present || raw == nil {
		return settings
	}
	if name, ok := raw.(string); ok {
		settings.Voice = name
		settings.VoiceSet = true
	}
	return settings
}

// audioSelectionFromConfigValues reads the machine-local realtime audio
// preference (Rust `RealtimeAudioToml`, config/src/config_toml.rs) out of the
// effective configuration. `microphone` and `speaker` name devices, and
// `microphone_channel` is either one one-based channel or a list of them
// (`MicrophoneChannels` is an untagged Single|Multiple). A present value that
// cannot be used is reported so the caller can refuse to start.
func audioSelectionFromConfigValues(raw any) (voicehost.AudioDeviceSelection, string) {
	table, ok := raw.(map[string]any)
	if !ok {
		return voicehost.AudioDeviceSelection{}, ""
	}
	selection := voicehost.AudioDeviceSelection{}
	if value, present := table["microphone"]; present && value != nil {
		name, ok := value.(string)
		if !ok {
			return voicehost.AudioDeviceSelection{}, "audio.microphone must be a string"
		}
		selection.Microphone = &name
	}
	if value, present := table["speaker"]; present && value != nil {
		name, ok := value.(string)
		if !ok {
			return voicehost.AudioDeviceSelection{}, "audio.speaker must be a string"
		}
		selection.Speaker = &name
	}
	if value, present := table["microphone_channel"]; present && value != nil {
		channels, err := microphoneChannelsFromConfigValue(value)
		if err != nil {
			return voicehost.AudioDeviceSelection{}, err.Error()
		}
		selection.Channel = channels
	}
	return selection, ""
}

// microphoneChannelsFromConfigValue decodes both spellings of the one-based
// channel selection. Zero, out-of-range and fractional entries are rejected
// while decoding, exactly like the NonZeroU16 deserializer upstream. An empty
// list stays a valid but unusable explicit selection: the helper rejects it
// later with its own "selected microphone channels unavailable" error.
func microphoneChannelsFromConfigValue(raw any) ([]uint16, error) {
	if channel, ok := oneBasedChannel(raw); ok {
		return []uint16{channel}, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.New("audio.microphone_channel must be a one-based channel number or a list of channel numbers")
	}
	channels := make([]uint16, 0, len(list))
	for _, item := range list {
		channel, ok := oneBasedChannel(item)
		if !ok {
			return nil, errors.New("audio.microphone_channel entries must be one-based channel numbers")
		}
		channels = append(channels, channel)
	}
	return channels, nil
}

// oneBasedChannel accepts the integral spellings a configuration value can
// arrive in and enforces the helper's one-based uint16 range.
func oneBasedChannel(raw any) (uint16, bool) {
	var number float64
	switch value := raw.(type) {
	case int:
		number = float64(value)
	case int32:
		number = float64(value)
	case int64:
		number = float64(value)
	case uint:
		number = float64(value)
	case uint16:
		if value == 0 {
			return 0, false
		}
		return value, true
	case uint32:
		number = float64(value)
	case uint64:
		number = float64(value)
	case float32:
		number = float64(value)
	case float64:
		number = value
	default:
		return 0, false
	}
	if number < 1 || number > 65535 || number != float64(int64(number)) {
		return 0, false
	}
	return uint16(number), true
}

// interactiveRemoteRealtimeVoices asks the app-server which voices it supports.
// A failure falls back to the builtin list, matching the Rust TUI.
func interactiveRemoteRealtimeVoices(endpoint *appserverdaemon.RemoteAppServerEndpoint) func(context.Context) realtime.VoicesList {
	return func(ctx context.Context) realtime.VoicesList {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return realtime.BuiltinVoices()
		}
		defer client.close()
		var response realtime.ListVoicesResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadRealtimeListVoices, realtime.ListVoicesParams{}, &response); err != nil {
			return realtime.BuiltinVoices()
		}
		if len(response.Voices.V1) == 0 && len(response.Voices.V2) == 0 {
			return realtime.BuiltinVoices()
		}
		return response.Voices
	}
}

// voiceSettingsTimeout bounds one settings read or write.
const voiceSettingsTimeout = 15 * time.Second

// interactiveRemoteVoiceSettings fetches the voice catalog and the effective
// preference for the settings picker. The TUI uses V3, which shares the V1
// voice catalog.
func interactiveRemoteVoiceSettings(endpoint *appserverdaemon.RemoteAppServerEndpoint) func() bubbletea.Cmd {
	return func() bubbletea.Cmd {
		return func() bubbletea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), voiceSettingsTimeout)
			defer cancel()
			voices := interactiveRemoteRealtimeVoices(endpoint)(ctx)
			settings := interactiveRemoteRealtimeSettings(endpoint)(ctx)
			current := settings.Voice
			if !settings.VoiceSet {
				current = string(voices.DefaultV1)
			}
			return codextea.VoiceSettingsMsg{
				Voices:  RealtimeVoiceNames(voices.V1),
				Current: current,
				// Rust #49437: the settings tree shows the machine-local
				// audio.microphone / audio.speaker / audio.microphone_channel
				// selection alongside the voice catalog.
				Audio: voiceAudioPreferencesFromSelection(settings.Audio),
			}
		}
	}
}

// RealtimeVoiceNames renders a voice list for the picker.
func RealtimeVoiceNames(voices []realtime.Voice) []string {
	names := make([]string, 0, len(voices))
	for _, voice := range voices {
		names = append(names, string(voice))
	}
	return names
}

// interactiveRemoteVoiceSaver persists the chosen voice and confirms it is the
// effective preference, mirroring the Rust save-and-confirm flow.
func interactiveRemoteVoiceSaver(endpoint *appserverdaemon.RemoteAppServerEndpoint) func(voice string) bubbletea.Cmd {
	return func(voice string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), voiceSettingsTimeout)
			defer cancel()
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				return codextea.VoiceSavedMsg{Voice: voice, Err: err}
			}
			defer client.close()
			var response config.ConfigWriteResponse
			err = remoteSessionRequest(ctx, client, appserver.MethodConfigBatchWrite, config.ConfigBatchWriteParams{
				Edits: []config.ConfigEdit{{
					KeyPath:       "realtime.voice",
					Value:         voice,
					MergeStrategy: config.MergeReplace,
				}},
			}, &response)
			if err != nil {
				return codextea.VoiceSavedMsg{Voice: voice, Err: err}
			}
			// A managed layer can override the saved value, in which case the
			// preference is stored but not applied.
			settings := interactiveRemoteRealtimeSettings(endpoint)(ctx)
			if settings.VoiceSet && strings.TrimSpace(settings.Voice) != voice {
				return codextea.VoiceSavedMsg{
					Voice: voice,
					Err:   errors.New("the saved voice is overridden by a managed setting"),
				}
			}
			return codextea.VoiceSavedMsg{Voice: voice}
		}
	}
}

// interactiveRemoteSpeechSender speaks one delegated answer into the thread's
// realtime conversation. The thread is resolved at delivery time so a resumed
// or switched thread is never addressed by mistake.
func interactiveRemoteSpeechSender(endpoint *appserverdaemon.RemoteAppServerEndpoint, threadID func() string) func(itemID string, text string) bubbletea.Cmd {
	return func(itemID string, text string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			target := ""
			if threadID != nil {
				target = strings.TrimSpace(threadID())
			}
			if target == "" {
				return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: errors.New("no active thread to speak into")}
			}
			ctx, cancel := context.WithTimeout(context.Background(), voiceSettingsTimeout)
			defer cancel()
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: err}
			}
			defer client.close()
			var response realtime.AppendSpeechResponse
			err = remoteSessionRequest(ctx, client, appserver.MethodThreadRealtimeAppendSpeech, realtime.AppendSpeechParams{
				ThreadID: target,
				Text:     text,
			}, &response)
			return codextea.VoiceSpeechResultMsg{ItemID: itemID, Err: err}
		}
	}
}

// Machine-local audio device settings (Rust #49437 / #49836
// codex-rs/tui/src/app/realtime_settings.rs).
//
// The helper owns the audio devices, so enumerating them always starts a
// short-lived local helper process — even when the app-server is remote, since
// "audio runs on the TUI's machine" (Rust persist_realtime_audio). The saved
// preference lives in the active configuration service: audio.microphone and
// audio.speaker name devices, audio.microphone_channel selects capture
// channels, and saving an input device clears the saved channel.

const (
	// voiceDeviceListTimeout bounds one helper connect, runtime initialization
	// and device listing.
	voiceDeviceListTimeout = 90 * time.Second
	// voiceAudioWriteTimeout bounds one audio setting write plus its read-back.
	voiceAudioWriteTimeout = 30 * time.Second
)

// voiceAudioConfig is the mode-specific configuration surface the audio device
// settings need: the write path plus the effective read-back used to report
// whether the saved value is the one in effect (Rust App::persist_realtime_audio).
type voiceAudioConfig struct {
	write func(ctx context.Context, edits []config.ConfigEdit) error
	read  func(ctx context.Context) (voicehost.AudioDeviceSelection, error)
}

// voiceAudioPreferencesFromSelection renders the machine-local preference the
// settings tree shows (Rust LocalSettings::audio).
func voiceAudioPreferencesFromSelection(selection voicehost.AudioDeviceSelection) chatwidget.VoiceAudioPreferences {
	preferences := chatwidget.VoiceAudioPreferences{
		Microphone: selection.Microphone,
		Speaker:    selection.Speaker,
	}
	if len(selection.Channel) > 0 {
		channels := make([]uint16, len(selection.Channel))
		copy(channels, selection.Channel)
		preferences.MicrophoneChannel = &chatwidget.MicrophoneChannels{Values: channels}
	}
	return preferences
}

// effectiveAudioSelection resolves audio.* out of effective configuration
// values. A present but unusable preference is an error rather than a silently
// ignored setting.
func effectiveAudioSelection(values map[string]any) (voicehost.AudioDeviceSelection, error) {
	selection, problem := audioSelectionFromConfigValues(values["audio"])
	if problem != "" {
		return voicehost.AudioDeviceSelection{}, errors.New(problem)
	}
	return selection, nil
}

// connectVoiceHelper starts one throwaway helper for a listing request, using
// the same package and build identity a conversation would start.
func connectVoiceHelper(ctx context.Context, runtime *voiceRuntime) (*voicehost.VoiceHost, error) {
	if runtime == nil {
		return nil, errors.New("voice runtime is unavailable")
	}
	packageDir := strings.TrimSpace(runtime.options.packageDir)
	if packageDir == "" {
		packageDir = voicehost.VoicePackageDir()
		if packageDir == "" {
			return nil, errors.New("voice package unavailable")
		}
	}
	buildCommit := strings.TrimSpace(runtime.options.buildCommit)
	if buildCommit == "" {
		buildCommit = voicehost.DefaultBuildCommit()
	}
	return voicehost.ConnectPackage(ctx, packageDir, buildCommit)
}

// voiceListDevicesCmd enumerates one direction through the local helper (Rust
// App::list_realtime_devices). Listing never opens a stream, and the helper is
// retired as soon as the answer arrives.
func voiceListDevicesCmd(runtime *voiceRuntime) func(kind voicehost.AudioDeviceKind) bubbletea.Cmd {
	return func(kind voicehost.AudioDeviceKind) bubbletea.Cmd {
		return func() bubbletea.Msg {
			if kind != voicehost.AudioDeviceKindInput && kind != voicehost.AudioDeviceKindOutput {
				return codextea.VoiceDevicesMsg{Kind: kind, Err: errors.New("unknown audio device direction")}
			}
			ctx, cancel := context.WithTimeout(context.Background(), voiceDeviceListTimeout)
			defer cancel()
			host, err := connectVoiceHelper(ctx, runtime)
			if err != nil {
				return codextea.VoiceDevicesMsg{Kind: kind, Err: err}
			}
			// The Go helper enumerates through the miniaudio context it later
			// captures with, so the runtime must be initialized first.
			if err := host.InitializeRuntime(ctx); err != nil {
				_ = host.Close(context.Background())
				return codextea.VoiceDevicesMsg{Kind: kind, Err: err}
			}
			devices, listErr := host.ListDevices(ctx, kind)
			_ = host.Close(context.Background())
			return codextea.VoiceDevicesMsg{Kind: kind, Devices: devices, Err: listErr}
		}
	}
}

// voiceAudioDeviceKey maps one direction to its configuration key (Rust
// persist_realtime_device).
func voiceAudioDeviceKey(kind voicehost.AudioDeviceKind) (string, error) {
	switch kind {
	case voicehost.AudioDeviceKindInput:
		return "microphone", nil
	case voicehost.AudioDeviceKindOutput:
		return "speaker", nil
	default:
		return "", errors.New("unknown audio device direction")
	}
}

// voiceSaveDeviceCmd persists audio.microphone / audio.speaker and reports
// whether the stored value is the effective one (Rust
// App::persist_realtime_device → persist_realtime_audio).
func voiceSaveDeviceCmd(cfg voiceAudioConfig) func(kind voicehost.AudioDeviceKind, name *string) bubbletea.Cmd {
	return func(kind voicehost.AudioDeviceKind, name *string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			key, err := voiceAudioDeviceKey(kind)
			if err != nil {
				return codextea.VoiceDeviceSavedMsg{Kind: kind, Name: name, Err: err}
			}
			ctx, cancel := context.WithTimeout(context.Background(), voiceAudioWriteTimeout)
			defer cancel()
			edits := []config.ConfigEdit{{
				KeyPath:       "audio." + key,
				Value:         deviceConfigValue(name),
				MergeStrategy: config.MergeReplace,
			}}
			if key == "microphone" {
				// Rust #49437: saving an input device clears the saved channel
				// selection; another layer can still override it.
				edits = append(edits, config.ConfigEdit{
					KeyPath:       "audio.microphone_channel",
					Value:         nil,
					MergeStrategy: config.MergeReplace,
				})
			}
			if err := cfg.write(ctx, edits); err != nil {
				return codextea.VoiceDeviceSavedMsg{Kind: kind, Name: name, Err: err}
			}
			effective, err := cfg.read(ctx)
			if err != nil {
				return codextea.VoiceDeviceSavedMsg{Kind: kind, Name: name, Err: fmt.Errorf("Audio setting was saved, but effective settings could not be read: %w", err)}
			}
			return codextea.VoiceDeviceSavedMsg{
				Kind:              kind,
				Name:              name,
				ChannelOverridden: key == "microphone" && len(effective.Channel) > 0,
				Overridden:        !deviceValueMatches(effective, key, name),
			}
		}
	}
}

// voiceSaveInputChannelCmd persists audio.microphone_channel (Rust
// App::persist_realtime_input_channel).
func voiceSaveInputChannelCmd(cfg voiceAudioConfig) func(channel *chatwidget.MicrophoneChannels) bubbletea.Cmd {
	return func(channel *chatwidget.MicrophoneChannels) bubbletea.Cmd {
		return func() bubbletea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), voiceAudioWriteTimeout)
			defer cancel()
			if err := cfg.write(ctx, []config.ConfigEdit{{
				KeyPath:       "audio.microphone_channel",
				Value:         channelConfigValue(channel),
				MergeStrategy: config.MergeReplace,
			}}); err != nil {
				return codextea.VoiceInputChannelSavedMsg{Channel: channel, Err: err}
			}
			effective, err := cfg.read(ctx)
			if err != nil {
				return codextea.VoiceInputChannelSavedMsg{Channel: channel, Err: fmt.Errorf("Audio setting was saved, but effective settings could not be read: %w", err)}
			}
			return codextea.VoiceInputChannelSavedMsg{
				Channel:    channel,
				Overridden: !channelsValueMatches(effective.Channel, channel),
			}
		}
	}
}

// deviceConfigValue renders the stored value: nil clears the key (Rust
// ConfigEdit::ClearPath), a name stores the device.
func deviceConfigValue(name *string) any {
	if name == nil {
		return nil
	}
	return strings.TrimSpace(*name)
}

// channelConfigValue renders the stored channel selection: nil clears it, a
// single channel stays a scalar and several stay a list, mirroring Rust
// MicrophoneChannels::Single | Multiple.
func channelConfigValue(channel *chatwidget.MicrophoneChannels) any {
	if channel == nil || len(channel.Values) == 0 {
		return nil
	}
	if len(channel.Values) == 1 {
		return channel.Values[0]
	}
	values := make([]uint16, len(channel.Values))
	copy(values, channel.Values)
	return values
}

// deviceValueMatches reports whether the effective audio preference equals the
// value just written.
func deviceValueMatches(effective voicehost.AudioDeviceSelection, key string, name *string) bool {
	if key == "speaker" {
		return deviceNameMatches(effective.Speaker, name)
	}
	return deviceNameMatches(effective.Microphone, name)
}

func deviceNameMatches(current *string, want *string) bool {
	if want == nil {
		return current == nil
	}
	if current == nil {
		return false
	}
	return strings.TrimSpace(*current) == strings.TrimSpace(*want)
}

// channelsValueMatches reports whether the effective capture-channel selection
// equals the value just written; an absent and an empty selection both mean
// "all channels (mixed)".
func channelsValueMatches(current []uint16, want *chatwidget.MicrophoneChannels) bool {
	var wantValues []uint16
	if want != nil {
		wantValues = want.Values
	}
	if len(current) != len(wantValues) {
		return false
	}
	for index := range current {
		if current[index] != wantValues[index] {
			return false
		}
	}
	return true
}

// remoteVoiceAudioConfig writes audio.* through the remote app-server and
// re-reads the effective values so the host can report saved vs overridden.
func remoteVoiceAudioConfig(endpoint *appserverdaemon.RemoteAppServerEndpoint) voiceAudioConfig {
	return voiceAudioConfig{
		write: func(ctx context.Context, edits []config.ConfigEdit) error {
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				return err
			}
			defer client.close()
			var response config.ConfigWriteResponse
			return remoteSessionRequest(ctx, client, appserver.MethodConfigBatchWrite, config.ConfigBatchWriteParams{
				Edits:            edits,
				ReloadUserConfig: true,
			}, &response)
		},
		read: func(ctx context.Context) (voicehost.AudioDeviceSelection, error) {
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				return voicehost.AudioDeviceSelection{}, err
			}
			defer client.close()
			var response config.ConfigReadResponse
			if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRead, config.ConfigReadParams{}, &response); err != nil {
				return voicehost.AudioDeviceSelection{}, err
			}
			return effectiveAudioSelection(response.Config)
		},
	}
}
