package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"codex_go/realtime"
	codextea "codex_go/tui/tea"
	"codex_go/voicehost"
)

// withFakeVoiceSession substitutes the helper launcher for one test.
func withFakeVoiceSession(t *testing.T, session *voicehost.StartedSession, err error) *voicehost.SessionOptions {
	t.Helper()
	previous := startVoiceSession
	captured := &voicehost.SessionOptions{}
	startVoiceSession = func(_ context.Context, options voicehost.SessionOptions) (*voicehost.StartedSession, error) {
		*captured = options
		if err != nil {
			return nil, err
		}
		return session, nil
	}
	t.Cleanup(func() { startVoiceSession = previous })
	return captured
}

func TestVoiceRuntimeStartsHelperAndPostsOffer(t *testing.T) {
	options := withFakeVoiceSession(t, &voicehost.StartedSession{
		OfferSDP: "v=0 offer",
		Handle:   &voicehost.SessionHandle{},
	}, nil)
	var gotParams realtime.StartParams
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		packageDir:  "/pkg",
		buildCommit: "commit-1",
		realtimeSettings: func(context.Context) VoiceSettings {
			return VoiceSettings{Model: "gpt-realtime-test", Voice: "cove", VoiceSet: true}
		},
		startSession: func(_ context.Context, params realtime.StartParams) error {
			gotParams = params
			return nil
		},
	})
	msg := runtime.startCmd("thread-1", 3)()
	attached, ok := msg.(codextea.VoiceHelperAttachedMsg)
	if !ok || attached.AttemptID != 3 {
		t.Fatalf("start reported %#v", msg)
	}
	if gotParams.ThreadID != "thread-1" {
		t.Fatalf("start thread = %q", gotParams.ThreadID)
	}
	if gotParams.Transport == nil || gotParams.Transport.Type != "webrtc" || gotParams.Transport.SDP != "v=0 offer" {
		t.Fatalf("start transport = %#v", gotParams.Transport)
	}
	// The TUI starts a V3 WebRTC conversation with client-managed handoffs and
	// no startup context, matching the Rust client.
	if gotParams.Version == nil || *gotParams.Version != realtime.VersionV3 {
		t.Fatalf("start version = %#v", gotParams.Version)
	}
	if gotParams.ClientManagedHandoffs == nil || !*gotParams.ClientManagedHandoffs {
		t.Fatalf("clientManagedHandoffs = %#v", gotParams.ClientManagedHandoffs)
	}
	if gotParams.IncludeStartupContext == nil || *gotParams.IncludeStartupContext {
		t.Fatalf("includeStartupContext = %#v", gotParams.IncludeStartupContext)
	}
	if gotParams.OutputModality != realtime.OutputAudio {
		t.Fatalf("outputModality = %q", gotParams.OutputModality)
	}
	if gotParams.Model == nil || *gotParams.Model != "gpt-realtime-test" {
		t.Fatalf("model = %#v", gotParams.Model)
	}
	if gotParams.Voice == nil || *gotParams.Voice != realtime.Voice("cove") {
		t.Fatalf("voice = %#v", gotParams.Voice)
	}
	if options.PackageDir != "/pkg" || options.BuildCommit != "commit-1" {
		t.Fatalf("session options = %#v", options)
	}
	if !runtime.running() {
		t.Fatal("the helper was not retained")
	}
}

func TestVoiceRuntimeRequiresThread(t *testing.T) {
	withFakeVoiceSession(t, &voicehost.StartedSession{Handle: &voicehost.SessionHandle{}}, nil)
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		startSession: func(context.Context, realtime.StartParams) error {
			t.Fatal("a start RPC was issued without a thread")
			return nil
		},
	})
	msg := runtime.startCmd("   ", 1)()
	result, ok := msg.(codextea.VoiceAnswerResultMsg)
	if !ok || result.Err == nil || result.AttemptID != 1 {
		t.Fatalf("message = %#v", msg)
	}
}

func TestVoiceRuntimeReportsHelperStartupFailure(t *testing.T) {
	withFakeVoiceSession(t, nil, errors.New("helper missing"))
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		startSession: func(context.Context, realtime.StartParams) error {
			t.Fatal("the start RPC ran after a helper failure")
			return nil
		},
	})
	msg := runtime.startCmd("thread-1", 2)()
	result, ok := msg.(codextea.VoiceAnswerResultMsg)
	if !ok || result.Err == nil || result.AttemptID != 2 {
		t.Fatalf("message = %#v", msg)
	}
	if runtime.running() {
		t.Fatal("a failed startup retained a helper")
	}
}

func TestVoiceRuntimeReportsStartRPCFailure(t *testing.T) {
	withFakeVoiceSession(t, &voicehost.StartedSession{
		OfferSDP: "v=0 offer",
		Handle:   &voicehost.SessionHandle{},
	}, nil)
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		startSession: func(context.Context, realtime.StartParams) error {
			return errors.New("app-server refused the session")
		},
	})
	msg := runtime.startCmd("thread-1", 4)()
	result, ok := msg.(codextea.VoiceAnswerResultMsg)
	if !ok || result.Err == nil || result.AttemptID != 4 {
		t.Fatalf("message = %#v", msg)
	}
	if runtime.running() {
		t.Fatal("a refused session retained a helper")
	}
}

func TestVoiceRuntimeApplyAnswerRequiresHelper(t *testing.T) {
	runtime := newVoiceRuntime(voiceRuntimeOptions{})
	msg := runtime.applyAnswerCmd("thread-1", 5, "v=0 answer")()
	result, ok := msg.(codextea.VoiceAnswerResultMsg)
	if !ok || result.Err == nil || result.AttemptID != 5 {
		t.Fatalf("message = %#v", msg)
	}
}

func TestVoiceRuntimeCloseRetiresHelperAndStopsSession(t *testing.T) {
	withFakeVoiceSession(t, &voicehost.StartedSession{
		OfferSDP: "v=0 offer",
		Handle:   &voicehost.SessionHandle{},
	}, nil)
	var stoppedThreadID string
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		startSession: func(context.Context, realtime.StartParams) error { return nil },
		stopSession: func(_ context.Context, threadID string) error {
			stoppedThreadID = threadID
			return nil
		},
	})
	if msg := runtime.startCmd("thread-close", 6)(); msg != nil {
		if _, ok := msg.(codextea.VoiceHelperAttachedMsg); !ok {
			t.Fatalf("start reported %#v", msg)
		}
	}
	runtime.close()
	if stoppedThreadID != "thread-close" {
		t.Fatalf("stopped thread = %q", stoppedThreadID)
	}
	if runtime.running() {
		t.Fatal("close retained the helper")
	}
	// A second close must be a no-op rather than stopping an unrelated session.
	stoppedThreadID = ""
	runtime.close()
	if stoppedThreadID != "" {
		t.Fatalf("a second close stopped %q", stoppedThreadID)
	}
}

func TestVoiceRuntimeControlsRequireARunningHelper(t *testing.T) {
	runtime := newVoiceRuntime(voiceRuntimeOptions{})
	if err := runtime.setMicrophoneMuted(true); err == nil {
		t.Fatal("muting without a helper succeeded")
	}
	if microphone, speaker := runtime.peaks(); microphone != 0 || speaker != 0 {
		t.Fatalf("peaks = %d/%d", microphone, speaker)
	}
}

func TestRealtimeSettingsFromConfigValues(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   VoiceSettings
	}{
		{name: "empty", values: nil, want: VoiceSettings{}},
		{
			name:   "model only",
			values: map[string]any{"experimental_realtime_ws_model": "gpt-realtime-test"},
			want:   VoiceSettings{Model: "gpt-realtime-test"},
		},
		{
			name:   "voice preference",
			values: map[string]any{"realtime": map[string]any{"voice": "cove"}},
			want:   VoiceSettings{Voice: "cove", VoiceSet: true},
		},
		{
			name:   "null voice falls back to the default",
			values: map[string]any{"realtime": map[string]any{"voice": nil}},
			want:   VoiceSettings{},
		},
		{
			name:   "non-string voice is ignored",
			values: map[string]any{"realtime": map[string]any{"voice": 3}},
			want:   VoiceSettings{},
		},
		{
			name:   "non-string model is ignored",
			values: map[string]any{"experimental_realtime_ws_model": 3},
			want:   VoiceSettings{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// The struct carries an audio selection (pointer + channel slice),
			// so the table compares structurally rather than with ==.
			if got := RealtimeSettingsFromConfigValues(test.values); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("settings = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestVoiceRuntimeResolvesVoice(t *testing.T) {
	runtime := newVoiceRuntime(voiceRuntimeOptions{})
	// An explicit preference wins when it names a known voice.
	configured := runtime.resolveVoice(context.Background(), VoiceSettings{Voice: "cove", VoiceSet: true})
	if configured == nil || *configured != realtime.Voice("cove") {
		t.Fatalf("configured voice = %#v", configured)
	}
	// An unknown name is dropped rather than sent.
	if voice := runtime.resolveVoice(context.Background(), VoiceSettings{Voice: "not-a-voice", VoiceSet: true}); voice != nil {
		t.Fatalf("unknown voice = %#v", voice)
	}
	// An absent preference falls back to the builtin default for v1.
	fallback := runtime.resolveVoice(context.Background(), VoiceSettings{})
	voices := realtime.BuiltinVoices()
	builtin := voices.DefaultForVersion(realtime.VersionV1)
	if fallback == nil || *fallback != builtin {
		t.Fatalf("fallback voice = %#v, want %q", fallback, builtin)
	}
}

func TestVoiceRuntimeUsesServerVoiceList(t *testing.T) {
	served := realtime.VoicesList{
		V1:        []realtime.Voice{"served-default"},
		V2:        []realtime.Voice{"served-default"},
		DefaultV1: "served-default",
	}
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		listVoices: func(context.Context) realtime.VoicesList { return served },
	})
	fallback := runtime.resolveVoice(context.Background(), VoiceSettings{})
	if fallback == nil || *fallback != realtime.Voice("served-default") {
		t.Fatalf("fallback voice = %#v", fallback)
	}
}

func TestRealtimeVoiceNames(t *testing.T) {
	names := RealtimeVoiceNames([]realtime.Voice{"alloy", "cove"})
	if len(names) != 2 || names[0] != "alloy" || names[1] != "cove" {
		t.Fatalf("names = %#v", names)
	}
	if names := RealtimeVoiceNames(nil); len(names) != 0 {
		t.Fatalf("empty names = %#v", names)
	}
}

func TestInteractiveRemoteSpeechSenderRequiresAThread(t *testing.T) {
	send := interactiveRemoteSpeechSender(nil, func() string { return "   " })
	msg := send("item-1", "the answer")()
	result, ok := msg.(codextea.VoiceSpeechResultMsg)
	if !ok {
		t.Fatalf("message = %#v", msg)
	}
	if result.ItemID != "item-1" || result.Err == nil || !strings.Contains(result.Err.Error(), "no active thread") {
		t.Fatalf("result = %#v", result)
	}
}

func TestInteractiveRemoteSpeechSenderReportsTransportFailure(t *testing.T) {
	send := interactiveRemoteSpeechSender(nil, func() string { return "thread-1" })
	msg := send("item-2", "the answer")()
	result, ok := msg.(codextea.VoiceSpeechResultMsg)
	if !ok {
		t.Fatalf("message = %#v", msg)
	}
	if result.ItemID != "item-2" || result.Err == nil {
		t.Fatalf("result = %#v", result)
	}
}

// TestVoiceRuntimePassesAudioSelectionLikeRust pins the app half of the Rust
// realtime facade: the machine-local device preference is handed to the helper
// session, which the facade opens through its single open_devices(selection)
// entry point (Rust #49437/#49836, codex-rs/tui/src/chatwidget/realtime.rs).
func TestVoiceRuntimePassesAudioSelectionLikeRust(t *testing.T) {
	microphone := "Studio mic"
	speaker := "Headphones"
	captured := withFakeVoiceSession(t, &voicehost.StartedSession{
		OfferSDP: "v=0 offer",
		Handle:   &voicehost.SessionHandle{},
	}, nil)
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		realtimeSettings: func(context.Context) VoiceSettings {
			return VoiceSettings{Audio: voicehost.AudioDeviceSelection{
				Microphone: &microphone,
				Speaker:    &speaker,
				Channel:    []uint16{1, 2},
			}}
		},
		startSession: func(context.Context, realtime.StartParams) error { return nil },
	})
	if msg := runtime.startCmd("thread-audio", 9)(); msg != nil {
		if _, ok := msg.(codextea.VoiceHelperAttachedMsg); !ok {
			t.Fatalf("start reported %#v", msg)
		}
	}
	want := voicehost.AudioDeviceSelection{
		Microphone: &microphone,
		Speaker:    &speaker,
		Channel:    []uint16{1, 2},
	}
	if !reflect.DeepEqual(captured.AudioSelection, want) {
		t.Fatalf("session selection = %#v, want %#v", captured.AudioSelection, want)
	}
}

// TestVoiceRuntimeKeepsDefaultDevicesWithoutAudioLikeRust pins the other half
// of the contract: a run without a stored preference opens exactly the default
// devices, which is the zero AudioDeviceSelection.
func TestVoiceRuntimeKeepsDefaultDevicesWithoutAudioLikeRust(t *testing.T) {
	captured := withFakeVoiceSession(t, &voicehost.StartedSession{
		OfferSDP: "v=0 offer",
		Handle:   &voicehost.SessionHandle{},
	}, nil)
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		realtimeSettings: func(context.Context) VoiceSettings {
			return VoiceSettings{Model: "gpt-realtime-test"}
		},
		startSession: func(context.Context, realtime.StartParams) error { return nil },
	})
	if msg := runtime.startCmd("thread-default", 10)(); msg != nil {
		if _, ok := msg.(codextea.VoiceHelperAttachedMsg); !ok {
			t.Fatalf("start reported %#v", msg)
		}
	}
	if captured.AudioSelection.Microphone != nil || captured.AudioSelection.Speaker != nil {
		t.Fatalf("session selection = %#v, want the system defaults", captured.AudioSelection)
	}
	if captured.AudioSelection.Channel != nil {
		t.Fatalf("session channels = %#v, want the device's full mix", captured.AudioSelection.Channel)
	}
}

// TestVoiceRuntimeRefusesUnusableAudioSelectionLikeRust mirrors the Rust TUI,
// where an unreadable local audio preference surfaces an error and never starts
// the conversation, so a broken selection cannot silently open other devices
// (Rust realtime_settings.rs `realtime_audio_settings`;
// microphone_channel_is_machine_local_across_project_and_remote_transitions).
func TestVoiceRuntimeRefusesUnusableAudioSelectionLikeRust(t *testing.T) {
	previous := startVoiceSession
	startVoiceSession = func(context.Context, voicehost.SessionOptions) (*voicehost.StartedSession, error) {
		t.Fatal("the helper started although the audio preference was unusable")
		return nil, nil
	}
	t.Cleanup(func() { startVoiceSession = previous })
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		realtimeSettings: func(context.Context) VoiceSettings {
			return VoiceSettings{AudioErr: "audio.microphone_channel entries must be one-based channel numbers"}
		},
		startSession: func(context.Context, realtime.StartParams) error {
			t.Fatal("the start RPC ran for an unusable audio preference")
			return nil
		},
	})
	msg := runtime.startCmd("thread-bad-audio", 11)()
	result, ok := msg.(codextea.VoiceAnswerResultMsg)
	if !ok || result.Err == nil || result.AttemptID != 11 {
		t.Fatalf("message = %#v", msg)
	}
	if !strings.Contains(result.Err.Error(), "microphone_channel") {
		t.Fatalf("error = %v", result.Err)
	}
	if runtime.running() {
		t.Fatal("a refused session retained a helper")
	}
}

// TestRealtimeSettingsFromConfigValuesParsesAudioLikeRust covers the two
// spellings of RealtimeAudioToml.microphone_channel: the scalar form and the
// list form of the untagged MicrophoneChannels enum.
func TestRealtimeSettingsFromConfigValuesParsesAudioLikeRust(t *testing.T) {
	microphone := "Studio mic"
	speaker := "Headphones"
	tests := []struct {
		name string
		raw  any
		want voicehost.AudioDeviceSelection
	}{
		{name: "absent", raw: nil, want: voicehost.AudioDeviceSelection{}},
		{
			name: "devices and scalar channel",
			raw: map[string]any{
				"microphone":         "Studio mic",
				"speaker":            "Headphones",
				"microphone_channel": 2,
			},
			want: voicehost.AudioDeviceSelection{Microphone: &microphone, Speaker: &speaker, Channel: []uint16{2}},
		},
		{
			name: "channel list",
			raw:  map[string]any{"microphone_channel": []any{1, 2}},
			want: voicehost.AudioDeviceSelection{Channel: []uint16{1, 2}},
		},
		{
			name: "json numbers",
			raw:  map[string]any{"microphone_channel": []any{float64(3)}},
			want: voicehost.AudioDeviceSelection{Channel: []uint16{3}},
		},
		{
			name: "empty list stays an explicit selection",
			raw:  map[string]any{"microphone_channel": []any{}},
			want: voicehost.AudioDeviceSelection{Channel: []uint16{}},
		},
		{
			name: "null channel keeps the full mix",
			raw:  map[string]any{"microphone_channel": nil},
			want: voicehost.AudioDeviceSelection{},
		},
		{
			name: "null devices keep the system defaults",
			raw:  map[string]any{"microphone": nil, "speaker": nil},
			want: voicehost.AudioDeviceSelection{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := RealtimeSettingsFromConfigValues(map[string]any{"audio": test.raw})
			if got.AudioErr != "" {
				t.Fatalf("settings reported %q", got.AudioErr)
			}
			if !reflect.DeepEqual(got.Audio, test.want) {
				t.Fatalf("audio = %#v, want %#v", got.Audio, test.want)
			}
		})
	}
}

// TestRealtimeSettingsFromConfigValuesReportsUnusableAudioLikeRust pins the
// strictness of the upstream deserializer: NonZeroU16 and the typed device
// names reject unusable values instead of degrading to the device defaults.
func TestRealtimeSettingsFromConfigValuesReportsUnusableAudioLikeRust(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{
			name: "zero channel",
			raw:  map[string]any{"microphone_channel": 0},
			want: "audio.microphone_channel must be a one-based channel number or a list of channel numbers",
		},
		{
			name: "fractional channel",
			raw:  map[string]any{"microphone_channel": 1.5},
			want: "audio.microphone_channel must be a one-based channel number or a list of channel numbers",
		},
		{
			name: "out of range channel",
			raw:  map[string]any{"microphone_channel": 70000},
			want: "audio.microphone_channel must be a one-based channel number or a list of channel numbers",
		},
		{
			name: "non numeric channel",
			raw:  map[string]any{"microphone_channel": "two"},
			want: "audio.microphone_channel must be a one-based channel number or a list of channel numbers",
		},
		{
			name: "list with a zero channel",
			raw:  map[string]any{"microphone_channel": []any{1, 0}},
			want: "audio.microphone_channel entries must be one-based channel numbers",
		},
		{
			name: "non string microphone",
			raw:  map[string]any{"microphone": 3},
			want: "audio.microphone must be a string",
		},
		{
			name: "non string speaker",
			raw:  map[string]any{"speaker": false},
			want: "audio.speaker must be a string",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := RealtimeSettingsFromConfigValues(map[string]any{"audio": test.raw})
			if got.AudioErr != test.want {
				t.Fatalf("audio error = %q, want %q", got.AudioErr, test.want)
			}
			if got.Audio.Microphone != nil || got.Audio.Speaker != nil || got.Audio.Channel != nil {
				t.Fatalf("an unusable audio section still produced a selection: %#v", got.Audio)
			}
		})
	}
}
